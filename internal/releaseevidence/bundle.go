package releaseevidence

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/ildarbinanas-design/env-vault/internal/canonicalgzip"
	"github.com/ildarbinanas-design/env-vault/internal/releasecontract"
)

const (
	BundleSchemaID      = "env-vault.release-evidence-bundle.v2"
	BundleSchemaVersion = 2
	ParitySchemaID      = "env-vault.release-evidence-parity.v1"
	ParitySchemaVersion = 1
	StorageMetricsID    = "env-vault.release-evidence-storage-metrics.v1"

	BundleRootName             = "release-evidence-bundle.json"
	BundleObjectStoreDirectory = "objects/sha256"
	LedgerObjectStoreDirectory = "evidence/objects/sha256"

	BundleEncodingGZIP = "gzip"
	EvidenceCoreMedia  = "application/vnd.env-vault.release-evidence-core.v2+json"
	RawJSONMedia       = "application/json"

	MaxBundleRootBytes             = 150 << 10
	MaxBundleObjects               = 64
	MaxBundleObjectUncompressed    = 16 << 20
	MaxBundleObjectCompressed      = (16 << 20) + (2 << 10)
	MaxBundleAggregateUncompressed = 64 << 20
	MaxBundleAggregateCompressed   = (64 << 20) + (64 << 10)
	MaxStorageMetricsBytes         = 64 << 10
)

// Bundle is the small canonical index for the v2 offline evidence format.
// Raw attestation documents and the v1 evidence core live in deterministic,
// content-addressed objects. Object identity is always the SHA-256 of the
// uncompressed bytes.
type Bundle struct {
	SchemaID                  string         `json:"schema_id"`
	SchemaVersion             int            `json:"schema_version"`
	Repository                string         `json:"repository"`
	ReleaseVersion            string         `json:"release_version"`
	SourceSHA                 string         `json:"source_sha"`
	PublisherRepairMode       string         `json:"publisher_repair_mode"`
	PublisherRunID            int64          `json:"publisher_run_id"`
	PublisherRunAttempt       int            `json:"publisher_run_attempt"`
	EvidenceSchemaID          string         `json:"evidence_schema_id"`
	EvidenceSchemaVersion     int            `json:"evidence_schema_version"`
	LegacyEvidenceSHA256      string         `json:"legacy_evidence_sha256"`
	LegacyCanonicalJSONSHA256 string         `json:"legacy_canonical_json_sha256"`
	LegacyCanonicalJSONSize   int64          `json:"legacy_canonical_json_size"`
	EvidenceCoreObjectSHA256  string         `json:"evidence_core_object_sha256"`
	Objects                   []BundleObject `json:"objects"`
	Result                    string         `json:"result"`
	BundleSHA256              string         `json:"bundle_sha256"`
}

type BundleObject struct {
	SHA256           string `json:"sha256"`
	MediaType        string `json:"media_type"`
	Encoding         string `json:"encoding"`
	UncompressedSize int64  `json:"uncompressed_size"`
	CompressedSize   int64  `json:"compressed_size"`
	CompressedSHA256 string `json:"compressed_sha256"`
}

// BundleFiles is a complete exported bundle. Object map keys are canonical
// slash-separated paths relative to the directory containing BundleRootName.
type BundleFiles struct {
	Root    []byte
	Objects map[string][]byte
}

type ParityResult struct {
	SchemaID                  string `json:"schema_id"`
	SchemaVersion             int    `json:"schema_version"`
	OK                        bool   `json:"ok"`
	Repository                string `json:"repository"`
	ReleaseVersion            string `json:"release_version"`
	SourceSHA                 string `json:"source_sha"`
	LegacyDecision            string `json:"legacy_decision"`
	BundleDecision            string `json:"bundle_decision"`
	LegacyErrorCode           string `json:"legacy_error_code"`
	BundleErrorCode           string `json:"bundle_error_code"`
	LegacyCanonicalJSONSHA256 string `json:"legacy_canonical_json_sha256"`
	BundleSHA256              string `json:"bundle_sha256"`
	ReconstructedByteExact    bool   `json:"reconstructed_byte_exact"`
	Result                    string `json:"result"`
}

type StorageMetrics struct {
	SchemaID                               string `json:"schema_id"`
	SchemaVersion                          int    `json:"schema_version"`
	Repository                             string `json:"repository"`
	ReleaseVersion                         string `json:"release_version"`
	SourceSHA                              string `json:"source_sha"`
	LegacyRootJSONBytes                    int64  `json:"legacy_root_json_bytes"`
	AuxiliaryBytes                         int64  `json:"auxiliary_bytes"`
	ParityBytes                            int64  `json:"parity_bytes"`
	StorageMetricsSelfBytes                int64  `json:"storage_metrics_self_bytes"`
	LegacyDurableMetadataBytes             int64  `json:"legacy_durable_metadata_bytes"`
	CompactDurableMetadataBytes            int64  `json:"compact_durable_metadata_bytes"`
	LogicalPathBytesScope                  string `json:"logical_path_bytes_scope"`
	LegacyRootAttemptLogicalBytes          int64  `json:"legacy_root_attempt_logical_bytes"`
	UniqueGitBlobBytesScope                string `json:"unique_git_blob_bytes_scope"`
	LegacyUniqueGitBlobBytes               int64  `json:"legacy_unique_git_blob_bytes"`
	CompactRootIndexBytes                  int64  `json:"compact_root_index_bytes"`
	CompactObjectCount                     int    `json:"compact_object_count"`
	CompactObjectUncompressedBytes         int64  `json:"compact_object_uncompressed_bytes"`
	CompactObjectCompressedBytes           int64  `json:"compact_object_compressed_bytes"`
	CompactRootAttemptLogicalBytes         int64  `json:"compact_root_attempt_logical_bytes"`
	CompactUniqueGitBlobBytes              int64  `json:"compact_unique_git_blob_bytes"`
	OfflineReconstructedBytesScope         string `json:"offline_reconstructed_bytes_scope"`
	CompactOfflineReconstructedBytes       int64  `json:"compact_offline_reconstructed_bytes"`
	LogicalPayloadReductionPermille        int64  `json:"logical_payload_reduction_permille"`
	DeterministicExportScope               string `json:"deterministic_export_scope"`
	LegacyDeterministicExportTarGZIPBytes  int64  `json:"legacy_deterministic_export_tar_gzip_bytes"`
	CompactDeterministicExportTarGZIPBytes int64  `json:"compact_deterministic_export_tar_gzip_bytes"`
	DeterministicExportReductionPermille   int64  `json:"deterministic_export_reduction_permille"`
	RootTargetBytes                        int64  `json:"root_target_bytes"`
	ReductionTargetPermille                int64  `json:"reduction_target_permille"`
	TargetsMet                             bool   `json:"targets_met"`
	Result                                 string `json:"result"`
}

type bundleDigestFunc func([]byte) string

func BuildBundle(evidence Evidence, contract releasecontract.Contract) (BundleFiles, error) {
	return buildBundleWithDigest(evidence, contract, sha256Text)
}

func buildBundleWithDigest(evidence Evidence, contract releasecontract.Contract, objectDigest bundleDigestFunc) (BundleFiles, error) {
	if objectDigest == nil {
		return BundleFiles{}, fail(CodeInputInvalid, "bundle object digest function is nil", nil)
	}
	if err := Verify(evidence, contract); err != nil {
		return BundleFiles{}, err
	}
	legacyBytes, err := MarshalJSON(evidence)
	if err != nil {
		return BundleFiles{}, fail(CodeInputInvalid, "encode canonical v1 evidence", err)
	}
	legacyJSONDigest := sha256Text(legacyBytes)

	core, err := cloneJSON(evidence)
	if err != nil {
		return BundleFiles{}, fail(CodeInputInvalid, "clone v1 evidence core", err)
	}
	type pendingObject struct {
		media string
		raw   []byte
	}
	pending := make(map[string]pendingObject)
	add := func(media string, raw []byte) (string, error) {
		if len(raw) == 0 || len(raw) > MaxBundleObjectUncompressed {
			return "", fail(CodeInputInvalid, "bundle object size is outside the supported limit", nil)
		}
		digest := objectDigest(raw)
		if !digestPattern.MatchString(digest) {
			return "", fail(CodeDigestMismatch, "bundle object digest is malformed", nil)
		}
		if existing, ok := pending[digest]; ok {
			if existing.media != media || !bytes.Equal(existing.raw, raw) {
				return "", fail(CodeDigestMismatch, "content-addressed object digest collision", nil)
			}
			return digest, nil
		}
		pending[digest] = pendingObject{media: media, raw: append([]byte(nil), raw...)}
		return digest, nil
	}

	for index := range core.AttestationVerificationBundle.Entries {
		document := []byte(evidence.AttestationVerificationBundle.Entries[index].DocumentJSON)
		digest, addErr := add(RawJSONMedia, document)
		if addErr != nil {
			return BundleFiles{}, addErr
		}
		if digest != evidence.AttestationVerificationBundle.Entries[index].DocumentSHA256 {
			return BundleFiles{}, fail(CodeDigestMismatch, "attestation document object identity differs from its v1 digest", nil)
		}
		core.AttestationVerificationBundle.Entries[index].DocumentJSON = ""
	}
	coreBytes, err := MarshalJSON(core)
	if err != nil {
		return BundleFiles{}, fail(CodeInputInvalid, "encode canonical evidence core", err)
	}
	coreDigest, err := add(EvidenceCoreMedia, coreBytes)
	if err != nil {
		return BundleFiles{}, err
	}
	if len(pending) > MaxBundleObjects {
		return BundleFiles{}, fail(CodeInputInvalid, "bundle object count exceeds the supported limit", nil)
	}

	digests := make([]string, 0, len(pending))
	for digest := range pending {
		digests = append(digests, digest)
	}
	sort.Strings(digests)
	objects := make(map[string][]byte, len(digests))
	descriptors := make([]BundleObject, 0, len(digests))
	var aggregateRaw, aggregateCompressed int64
	for _, digest := range digests {
		item := pending[digest]
		compressed, compressErr := deterministicGZIP(item.raw)
		if compressErr != nil {
			return BundleFiles{}, fail(CodeInputInvalid, "compress bundle object", compressErr)
		}
		objectPath := bundleObjectPath(digest)
		descriptors = append(descriptors, BundleObject{
			SHA256: digest, MediaType: item.media, Encoding: BundleEncodingGZIP,
			UncompressedSize: int64(len(item.raw)), CompressedSize: int64(len(compressed)),
			CompressedSHA256: sha256Text(compressed),
		})
		objects[objectPath] = compressed
		aggregateRaw += int64(len(item.raw))
		aggregateCompressed += int64(len(compressed))
	}
	if aggregateRaw > MaxBundleAggregateUncompressed || aggregateCompressed > MaxBundleAggregateCompressed {
		return BundleFiles{}, fail(CodeInputInvalid, "bundle aggregate object size exceeds the supported limit", nil)
	}

	bundle := Bundle{
		SchemaID: BundleSchemaID, SchemaVersion: BundleSchemaVersion,
		Repository: evidence.Repository, ReleaseVersion: evidence.ReleaseVersion, SourceSHA: evidence.SourceSHA,
		PublisherRepairMode: evidence.PublisherRepairMode,
		PublisherRunID:      evidence.PublisherMetrics.RunID, PublisherRunAttempt: evidence.PublisherMetrics.Attempt,
		EvidenceSchemaID: SchemaID, EvidenceSchemaVersion: SchemaVersion,
		LegacyEvidenceSHA256:      evidence.EvidenceSHA256,
		LegacyCanonicalJSONSHA256: legacyJSONDigest, LegacyCanonicalJSONSize: int64(len(legacyBytes)),
		EvidenceCoreObjectSHA256: coreDigest, Objects: descriptors, Result: "pass",
	}
	bundle.BundleSHA256, err = BundleSHA256(bundle)
	if err != nil {
		return BundleFiles{}, fail(CodeDigestMismatch, "seal evidence bundle", err)
	}
	root, err := MarshalJSON(bundle)
	if err != nil {
		return BundleFiles{}, fail(CodeInputInvalid, "encode evidence bundle", err)
	}
	if len(root) > MaxBundleRootBytes {
		return BundleFiles{}, fail(CodeInputInvalid, fmt.Sprintf("bundle root is %d bytes, limit is %d", len(root), MaxBundleRootBytes), nil)
	}
	return BundleFiles{Root: root, Objects: objects}, nil
}

func ParseBundle(data []byte) (Bundle, error) {
	if len(data) == 0 || len(data) > MaxBundleRootBytes {
		return Bundle{}, fail(CodeInputInvalid, fmt.Sprintf("bundle root size %d is outside 1..%d", len(data), MaxBundleRootBytes), nil)
	}
	var bundle Bundle
	if err := decodeStrict(data, &bundle); err != nil {
		return Bundle{}, fail(CodeInputInvalid, "strictly decode evidence bundle", err)
	}
	canonical, err := MarshalJSON(bundle)
	if err != nil || !bytes.Equal(canonical, data) {
		return Bundle{}, fail(CodeInputInvalid, "bundle root is not canonical JSON", err)
	}
	return bundle, nil
}

func BundleSHA256(bundle Bundle) (string, error) {
	bundle.BundleSHA256 = ""
	return compactSHA256(bundle)
}

func VerifyBundle(files BundleFiles, contract releasecontract.Contract) (Evidence, error) {
	bundle, err := ParseBundle(files.Root)
	if err != nil {
		return Evidence{}, err
	}
	if err := validateBundleIndex(bundle); err != nil {
		return Evidence{}, err
	}
	wantBundleDigest, err := BundleSHA256(bundle)
	if err != nil || bundle.BundleSHA256 != wantBundleDigest {
		return Evidence{}, fail(CodeDigestMismatch, "bundle self-digest mismatch", err)
	}
	if len(files.Objects) != len(bundle.Objects) {
		return Evidence{}, fail(CodeInputIncomplete, "bundle object set has missing or extra paths", nil)
	}

	rawByDigest := make(map[string][]byte, len(bundle.Objects))
	mediaByDigest := make(map[string]string, len(bundle.Objects))
	var aggregateRaw, aggregateCompressed int64
	for _, descriptor := range bundle.Objects {
		objectPath := bundleObjectPath(descriptor.SHA256)
		compressed, ok := files.Objects[objectPath]
		if !ok {
			return Evidence{}, fail(CodeInputIncomplete, "bundle object is missing: "+objectPath, nil)
		}
		if int64(len(compressed)) != descriptor.CompressedSize || sha256Text(compressed) != descriptor.CompressedSHA256 {
			return Evidence{}, fail(CodeDigestMismatch, "compressed bundle object differs from its descriptor", nil)
		}
		raw, decompressErr := strictGunzip(compressed, descriptor.UncompressedSize)
		if decompressErr != nil {
			return Evidence{}, fail(CodeInputInvalid, "decompress bundle object "+objectPath, decompressErr)
		}
		if sha256Text(raw) != descriptor.SHA256 {
			return Evidence{}, fail(CodeDigestMismatch, "uncompressed bundle object digest mismatch", nil)
		}
		canonicalCompressed, compressErr := deterministicGZIP(raw)
		if compressErr != nil || !bytes.Equal(canonicalCompressed, compressed) {
			return Evidence{}, fail(CodeInputInvalid, "bundle object compression is not deterministic canonical gzip", compressErr)
		}
		rawByDigest[descriptor.SHA256] = raw
		mediaByDigest[descriptor.SHA256] = descriptor.MediaType
		aggregateRaw += int64(len(raw))
		aggregateCompressed += int64(len(compressed))
		if aggregateRaw > MaxBundleAggregateUncompressed || aggregateCompressed > MaxBundleAggregateCompressed {
			return Evidence{}, fail(CodeInputInvalid, "bundle aggregate object size exceeds the supported limit", nil)
		}
	}

	coreBytes, ok := rawByDigest[bundle.EvidenceCoreObjectSHA256]
	if !ok || mediaByDigest[bundle.EvidenceCoreObjectSHA256] != EvidenceCoreMedia {
		return Evidence{}, fail(CodeInputIncomplete, "bundle evidence-core object is missing or has the wrong media type", nil)
	}
	core, err := ParseEvidence(coreBytes)
	if err != nil {
		return Evidence{}, err
	}
	wantObjects := map[string]bool{bundle.EvidenceCoreObjectSHA256: true}
	for index := range core.AttestationVerificationBundle.Entries {
		entry := &core.AttestationVerificationBundle.Entries[index]
		if entry.DocumentJSON != "" || !digestPattern.MatchString(entry.DocumentSHA256) {
			return Evidence{}, fail(CodeInputInvalid, "evidence core contains inline or malformed attestation material", nil)
		}
		document, found := rawByDigest[entry.DocumentSHA256]
		if !found || mediaByDigest[entry.DocumentSHA256] != RawJSONMedia {
			return Evidence{}, fail(CodeInputIncomplete, "referenced attestation object is missing or has the wrong media type", nil)
		}
		entry.DocumentJSON = string(document)
		wantObjects[entry.DocumentSHA256] = true
	}
	if len(wantObjects) != len(bundle.Objects) {
		return Evidence{}, fail(CodeInputInvalid, "bundle contains an unreferenced object", nil)
	}
	if err := Verify(core, contract); err != nil {
		return Evidence{}, err
	}
	legacyBytes, err := MarshalJSON(core)
	if err != nil {
		return Evidence{}, fail(CodeInputInvalid, "reconstruct canonical v1 evidence", err)
	}
	if bundle.Repository != core.Repository || bundle.ReleaseVersion != core.ReleaseVersion || bundle.SourceSHA != core.SourceSHA ||
		bundle.PublisherRepairMode != core.PublisherRepairMode || bundle.PublisherRunID != core.PublisherMetrics.RunID ||
		bundle.PublisherRunAttempt != core.PublisherMetrics.Attempt || bundle.LegacyEvidenceSHA256 != core.EvidenceSHA256 ||
		bundle.LegacyCanonicalJSONSHA256 != sha256Text(legacyBytes) || bundle.LegacyCanonicalJSONSize != int64(len(legacyBytes)) {
		return Evidence{}, fail(CodeSourceMismatch, "bundle index differs from reconstructed v1 evidence", nil)
	}
	return core, nil
}

func VerifyParity(legacy []byte, files BundleFiles, contract releasecontract.Contract) (ParityResult, error) {
	evidence, err := ParseEvidence(legacy)
	if err != nil {
		return ParityResult{}, err
	}
	if err := Verify(evidence, contract); err != nil {
		return ParityResult{}, err
	}
	canonical, err := MarshalJSON(evidence)
	if err != nil || !bytes.Equal(canonical, legacy) {
		return ParityResult{}, fail(CodeInputInvalid, "legacy evidence is not canonical JSON", err)
	}
	reconstructed, err := VerifyBundle(files, contract)
	if err != nil {
		return ParityResult{}, err
	}
	reconstructedBytes, err := MarshalJSON(reconstructed)
	if err != nil || !bytes.Equal(reconstructedBytes, legacy) {
		return ParityResult{}, fail(CodeDigestMismatch, "v1/v2 reconstructed bytes differ", err)
	}
	bundle, err := ParseBundle(files.Root)
	if err != nil {
		return ParityResult{}, err
	}
	return ParityResult{
		SchemaID: ParitySchemaID, SchemaVersion: ParitySchemaVersion, OK: true,
		Repository: evidence.Repository, ReleaseVersion: evidence.ReleaseVersion, SourceSHA: evidence.SourceSHA,
		LegacyDecision: "pass", BundleDecision: "pass", LegacyErrorCode: "", BundleErrorCode: "",
		LegacyCanonicalJSONSHA256: sha256Text(legacy), BundleSHA256: bundle.BundleSHA256,
		ReconstructedByteExact: true, Result: "pass",
	}, nil
}

func MeasureBundle(legacy []byte, files BundleFiles, auxiliary map[string][]byte, contract releasecontract.Contract) (StorageMetrics, error) {
	parity, err := VerifyParity(legacy, files, contract)
	if err != nil {
		return StorageMetrics{}, err
	}
	bundle, err := ParseBundle(files.Root)
	if err != nil {
		return StorageMetrics{}, err
	}
	parityBytes, err := MarshalJSON(parity)
	if err != nil {
		return StorageMetrics{}, fail(CodeInputInvalid, "encode canonical parity result", err)
	}
	if len(auxiliary) != 3 {
		return StorageMetrics{}, fail(CodeInputIncomplete, "common evidence metadata set must contain exactly three files", nil)
	}
	var auxiliaryBytes, objectRaw, objectCompressed int64
	for name, data := range auxiliary {
		if path.Base(name) != name || !commonEvidenceMetadataName(name) || len(data) == 0 {
			return StorageMetrics{}, fail(CodeInputInvalid, "auxiliary evidence path or bytes are invalid", nil)
		}
		auxiliaryBytes += int64(len(data))
	}
	for _, object := range bundle.Objects {
		objectRaw += object.UncompressedSize
		objectCompressed += object.CompressedSize
	}
	legacyMetadata := int64(len(legacy)) + auxiliaryBytes
	legacyLogical := 2 * legacyMetadata
	archiveAuxiliary := make(map[string][]byte, len(auxiliary)+1)
	for name, data := range auxiliary {
		archiveAuxiliary[name] = data
	}
	archiveAuxiliary["parity.json"] = parityBytes
	compactArchiveBytes, err := deterministicExportArchiveSize(files, archiveAuxiliary)
	if err != nil {
		return StorageMetrics{}, fail(CodeInputInvalid, "measure deterministic bundle export", err)
	}
	legacyEntries := make(map[string][]byte, len(auxiliary)+1)
	legacyEntries["release-evidence.json"] = legacy
	for name, data := range auxiliary {
		if _, exists := legacyEntries[name]; exists {
			return StorageMetrics{}, fail(CodeInputInvalid, "legacy export path collides with auxiliary evidence", nil)
		}
		legacyEntries[name] = data
	}
	legacyArchiveBytes, err := deterministicArchiveSize(legacyEntries)
	if err != nil {
		return StorageMetrics{}, fail(CodeInputInvalid, "measure deterministic legacy export", err)
	}
	archiveReduction := int64(0)
	if compactArchiveBytes < legacyArchiveBytes {
		archiveReduction = (legacyArchiveBytes - compactArchiveBytes) * 1000 / legacyArchiveBytes
	}
	metrics, err := storageMetricsFixedPoint(func(selfBytes int64) StorageMetrics {
		compactMetadata := int64(len(files.Root)) + auxiliaryBytes + int64(len(parityBytes)) + selfBytes
		compactLogical := 2*compactMetadata + objectCompressed
		reduction := int64(0)
		if legacyLogical > compactLogical {
			reduction = (legacyLogical - compactLogical) * 1000 / legacyLogical
		}
		return StorageMetrics{
			SchemaID: StorageMetricsID, SchemaVersion: 1,
			Repository: parity.Repository, ReleaseVersion: parity.ReleaseVersion, SourceSHA: parity.SourceSHA,
			LegacyRootJSONBytes: int64(len(legacy)), AuxiliaryBytes: auxiliaryBytes,
			ParityBytes: int64(len(parityBytes)), StorageMetricsSelfBytes: selfBytes,
			LegacyDurableMetadataBytes: legacyMetadata, CompactDurableMetadataBytes: compactMetadata,
			LogicalPathBytesScope:         "git_blob_payload_bytes_per_ledger_path",
			LegacyRootAttemptLogicalBytes: legacyLogical,
			UniqueGitBlobBytesScope:       "git_blob_payload_bytes_after_object_id_deduplication",
			LegacyUniqueGitBlobBytes:      legacyMetadata,
			CompactRootIndexBytes:         int64(len(files.Root)), CompactObjectCount: len(bundle.Objects),
			CompactObjectUncompressedBytes: objectRaw, CompactObjectCompressedBytes: objectCompressed,
			CompactRootAttemptLogicalBytes:         compactLogical,
			CompactUniqueGitBlobBytes:              compactMetadata + objectCompressed,
			OfflineReconstructedBytesScope:         "durable_metadata_plus_gunzip_object_bytes",
			CompactOfflineReconstructedBytes:       compactMetadata + objectRaw,
			LogicalPayloadReductionPermille:        reduction,
			DeterministicExportScope:               "excluding_storage_metrics_self_report",
			LegacyDeterministicExportTarGZIPBytes:  legacyArchiveBytes,
			CompactDeterministicExportTarGZIPBytes: compactArchiveBytes,
			DeterministicExportReductionPermille:   archiveReduction,
			RootTargetBytes:                        MaxBundleRootBytes, ReductionTargetPermille: 600,
			TargetsMet: storageTargetsMet(int64(len(files.Root)), reduction),
			Result:     "pass",
		}
	})
	if err != nil {
		return StorageMetrics{}, err
	}
	if !metrics.TargetsMet {
		return StorageMetrics{}, fail(CodeInputInvalid, fmt.Sprintf("compact evidence targets not met: root=%d reduction_permille=%d", len(files.Root), metrics.LogicalPayloadReductionPermille), nil)
	}
	return metrics, nil
}

func commonEvidenceMetadataName(name string) bool {
	switch name {
	case "index.md", "metrics-comparison.json", "metrics-comparison.md":
		return true
	default:
		return false
	}
}

func storageMetricsFixedPoint(build func(int64) StorageMetrics) (StorageMetrics, error) {
	var match StorageMetrics
	matches := 0
	for candidate := int64(1); candidate <= MaxStorageMetricsBytes; candidate++ {
		metrics := build(candidate)
		encoded, err := MarshalJSON(metrics)
		if err != nil {
			return StorageMetrics{}, fail(CodeInputInvalid, "encode storage metrics candidate", err)
		}
		if int64(len(encoded)) == candidate {
			match = metrics
			matches++
		}
	}
	if matches != 1 {
		return StorageMetrics{}, fail(CodeInputInvalid, fmt.Sprintf("storage metrics self-size has %d fixed points within the supported limit", matches), nil)
	}
	return match, nil
}

func storageTargetsMet(rootBytes, reductionPermille int64) bool {
	return rootBytes < MaxBundleRootBytes && reductionPermille >= 600
}

func validateBundleIndex(bundle Bundle) error {
	if bundle.SchemaID != BundleSchemaID || bundle.SchemaVersion != BundleSchemaVersion || bundle.Result != "pass" ||
		bundle.EvidenceSchemaID != SchemaID || bundle.EvidenceSchemaVersion != SchemaVersion {
		return fail(CodeInputInvalid, "evidence bundle schema or result is invalid", nil)
	}
	if !validRepository(bundle.Repository) || !releasecontract.IsVersion(bundle.ReleaseVersion) || !shaPattern.MatchString(bundle.SourceSHA) ||
		bundle.PublisherRunID <= 0 || bundle.PublisherRunAttempt <= 0 || !validBundleRepairMode(bundle.PublisherRepairMode) ||
		!digestPattern.MatchString(bundle.LegacyEvidenceSHA256) || !digestPattern.MatchString(bundle.LegacyCanonicalJSONSHA256) ||
		bundle.LegacyCanonicalJSONSize <= 0 || !digestPattern.MatchString(bundle.EvidenceCoreObjectSHA256) ||
		!digestPattern.MatchString(bundle.BundleSHA256) {
		return fail(CodeInputInvalid, "evidence bundle identity is invalid", nil)
	}
	if len(bundle.Objects) == 0 || len(bundle.Objects) > MaxBundleObjects {
		return fail(CodeInputIncomplete, "evidence bundle object inventory is empty or too large", nil)
	}
	var aggregateRaw, aggregateCompressed int64
	for index, object := range bundle.Objects {
		if !digestPattern.MatchString(object.SHA256) ||
			(object.MediaType != EvidenceCoreMedia && object.MediaType != RawJSONMedia) || object.Encoding != BundleEncodingGZIP ||
			object.UncompressedSize <= 0 || object.UncompressedSize > MaxBundleObjectUncompressed ||
			object.CompressedSize <= 0 || object.CompressedSize > MaxBundleObjectCompressed || !digestPattern.MatchString(object.CompressedSHA256) {
			return fail(CodeInputInvalid, "evidence bundle object descriptor is invalid", nil)
		}
		if index > 0 && bundle.Objects[index-1].SHA256 >= object.SHA256 {
			return fail(CodeInputInvalid, "evidence bundle objects must be strictly digest-sorted and unique", nil)
		}
		aggregateRaw += object.UncompressedSize
		aggregateCompressed += object.CompressedSize
	}
	if aggregateRaw > MaxBundleAggregateUncompressed || aggregateCompressed > MaxBundleAggregateCompressed {
		return fail(CodeInputInvalid, "evidence bundle aggregate descriptor size exceeds the supported limit", nil)
	}
	return nil
}

func validBundleRepairMode(mode string) bool {
	switch mode {
	case "none", "release-assets", "homebrew", "health":
		return true
	default:
		return false
	}
}

func bundleObjectPath(digest string) string {
	return BundleObjectStoreDirectory + "/" + digest + ".gz"
}

// BundleObjectRelativePath maps a digest-only index reference into the strict
// object-store root of a clean exported artifact.
func BundleObjectRelativePath(digest string) (string, error) {
	if !digestPattern.MatchString(digest) {
		return "", errors.New("bundle object digest is malformed")
	}
	return bundleObjectPath(digest), nil
}

// LedgerObjectPath maps the same digest-only reference into the shared,
// append-only ledger namespace. Publication must verify exact compressed bytes
// before reusing the returned path.
func LedgerObjectPath(digest string) (string, error) {
	if !digestPattern.MatchString(digest) {
		return "", errors.New("ledger object digest is malformed")
	}
	return LedgerObjectStoreDirectory + "/" + digest + ".gz", nil
}

func sha256Text(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func deterministicGZIP(data []byte) ([]byte, error) {
	return canonicalgzip.Encode(data)
}

func deterministicGZIPCapacity(dataLength int) (int, error) {
	return canonicalgzip.Capacity(dataLength)
}

func strictGunzip(compressed []byte, declaredSize int64) ([]byte, error) {
	if declaredSize <= 0 || declaredSize > MaxBundleObjectUncompressed || len(compressed) == 0 || len(compressed) > MaxBundleObjectCompressed {
		return nil, errors.New("compressed or declared object size is outside the supported limit")
	}
	return canonicalgzip.Decode(compressed, declaredSize, MaxBundleObjectUncompressed, MaxBundleObjectCompressed)
}

func deterministicExportArchiveSize(files BundleFiles, auxiliary map[string][]byte) (int64, error) {
	entries := make(map[string][]byte)
	entries[BundleRootName] = files.Root
	for name, data := range files.Objects {
		entries[name] = data
	}
	for name, data := range auxiliary {
		if name == BundleRootName || strings.HasPrefix(name, BundleObjectStoreDirectory+"/") {
			return 0, fmt.Errorf("auxiliary export path collides with reserved bundle path %q", name)
		}
		if _, exists := entries[name]; exists {
			return 0, fmt.Errorf("duplicate export path %q", name)
		}
		entries[name] = data
	}
	return deterministicArchiveSize(entries)
}

func deterministicArchiveSize(entries map[string][]byte) (int64, error) {
	names := make([]string, 0, len(entries))
	for name := range entries {
		if !safeExportPath(name) {
			return 0, fmt.Errorf("unsafe export path %q", name)
		}
		names = append(names, name)
	}
	sort.Strings(names)
	var tarBytes bytes.Buffer
	tarWriter := tar.NewWriter(&tarBytes)
	for _, name := range names {
		data := entries[name]
		header := &tar.Header{
			Name: name, Mode: 0o644, Size: int64(len(data)),
			ModTime: time.Unix(0, 0).UTC(), AccessTime: time.Time{}, ChangeTime: time.Time{},
			Uid: 0, Gid: 0, Uname: "", Gname: "", Format: tar.FormatUSTAR,
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			return 0, err
		}
		if _, err := tarWriter.Write(data); err != nil {
			return 0, err
		}
	}
	if err := tarWriter.Close(); err != nil {
		return 0, err
	}
	compressed, err := deterministicGZIP(tarBytes.Bytes())
	if err != nil {
		return 0, err
	}
	return int64(len(compressed)), nil
}

func safeExportPath(name string) bool {
	if name == "" || name == "." || name == ".." || path.IsAbs(name) || path.Clean(name) != name ||
		strings.HasPrefix(name, "../") || strings.Contains(name, "\\") || strings.Contains(name, ":") {
		return false
	}
	for _, character := range name {
		if character == 0 || character < 0x20 || character == 0x7f {
			return false
		}
	}
	for _, component := range strings.Split(name, "/") {
		if component == "" || component == "." || component == ".." {
			return false
		}
	}
	return true
}
