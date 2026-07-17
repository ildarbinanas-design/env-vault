package releaseevidence

import (
	"bytes"
	"compress/gzip"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ildarbinanas-design/env-vault/internal/releasecontract"
)

func TestBundleV2BuildVerifyAndParityAreDeterministicAndDeduplicated(t *testing.T) {
	fixture := newEvidenceFixture(t)
	evidence, err := Assemble(fixture.contract, fixture.authorization, fixture.manifest, fixture.ci, fixture.publisher, fixture.observation, fixture.attestationBundle)
	if err != nil {
		t.Fatal(err)
	}
	first, err := BuildBundle(evidence, fixture.contract)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildBundle(evidence, fixture.contract)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Root, second.Root) || !equalObjectMaps(first.Objects, second.Objects) {
		t.Fatal("repeated bundle builds differ")
	}
	bundle, err := ParseBundle(first.Root)
	if err != nil {
		t.Fatal(err)
	}
	// The fixture has ten references but only one provenance and one SBOM raw
	// document, plus the canonical evidence core.
	if len(bundle.Objects) != 3 || len(first.Objects) != 3 {
		t.Fatalf("deduplicated objects=%d/%d, want 3", len(bundle.Objects), len(first.Objects))
	}
	verified, err := VerifyBundle(first, fixture.contract)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := MarshalJSON(evidence)
	if err != nil {
		t.Fatal(err)
	}
	reconstructed, err := MarshalJSON(verified)
	if err != nil || !bytes.Equal(legacy, reconstructed) {
		t.Fatalf("reconstructed v1 bytes differ: err=%v", err)
	}
	parity, err := VerifyParity(legacy, first, fixture.contract)
	if err != nil {
		t.Fatal(err)
	}
	if !parity.OK || parity.Result != "pass" || !parity.ReconstructedByteExact || parity.LegacyDecision != "pass" || parity.BundleDecision != "pass" || parity.LegacyErrorCode != "" || parity.BundleErrorCode != "" {
		t.Fatalf("parity=%+v", parity)
	}
}

func TestBundleV2SemanticFailureCodeMatchesLegacyV1(t *testing.T) {
	fixture, evidence, files := newBundleFixture(t)
	changed := contractWithChangedPublisherWorkflow(t, fixture.contract)
	legacyErr := Verify(evidence, changed)
	_, bundleErr := VerifyBundle(files, changed)
	legacyCode := ErrorCode(legacyErr)
	bundleCode := ErrorCode(bundleErr)
	if legacyCode == "" || legacyCode != bundleCode || legacyCode != CodeDigestMismatch {
		t.Fatalf("semantic failure parity: v1=%q (%v) v2=%q (%v)", legacyCode, legacyErr, bundleCode, bundleErr)
	}
}

func contractWithChangedPublisherWorkflow(t *testing.T, contract releasecontract.Contract) releasecontract.Contract {
	t.Helper()
	changed := contract
	changed.Workflows = append([]releasecontract.Workflow(nil), contract.Workflows...)
	found := false
	for index := range changed.Workflows {
		if changed.Workflows[index].ID == "publisher" {
			changed.Workflows[index].Name += "-semantic-mismatch"
			found = true
		}
	}
	if !found {
		t.Fatal("fixture contract has no publisher workflow")
	}
	if err := changed.Validate(); err != nil {
		t.Fatalf("changed contract must remain structurally valid: %v", err)
	}
	return changed
}

func TestBundleV2RejectsMissingExtraReorderedAndAliasedObjects(t *testing.T) {
	fixture, evidence, files := newBundleFixture(t)
	bundle, err := ParseBundle(files.Root)
	if err != nil {
		t.Fatal(err)
	}

	tests := map[string]func(*BundleFiles, *Bundle){
		"missing object": func(candidate *BundleFiles, _ *Bundle) {
			objectPath, _ := BundleObjectRelativePath(bundle.Objects[0].SHA256)
			delete(candidate.Objects, objectPath)
		},
		"extra object": func(candidate *BundleFiles, _ *Bundle) {
			candidate.Objects["objects/sha256/"+strings.Repeat("f", 64)+".gz"] = []byte("extra")
		},
		"reordered index": func(_ *BundleFiles, candidate *Bundle) {
			candidate.Objects[0], candidate.Objects[1] = candidate.Objects[1], candidate.Objects[0]
		},
		"traversal path": func(_ *BundleFiles, candidate *Bundle) {
			// handled below as an artifact-store alias, not an index field
		},
		"case path alias": func(_ *BundleFiles, candidate *Bundle) {
			// handled below as an artifact-store alias, not an index field
		},
		"unsupported codec": func(_ *BundleFiles, candidate *Bundle) {
			candidate.Objects[0].Encoding = "zstd"
		},
		"wrong uncompressed size": func(_ *BundleFiles, candidate *Bundle) {
			candidate.Objects[0].UncompressedSize++
		},
		"wrong compressed digest": func(_ *BundleFiles, candidate *Bundle) {
			candidate.Objects[0].CompressedSHA256 = strings.Repeat("0", 64)
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			candidate := cloneBundleFiles(files)
			index := bundle
			index.Objects = append([]BundleObject(nil), bundle.Objects...)
			mutate(&candidate, &index)
			if name == "traversal path" || name == "case path alias" {
				objectPath, _ := BundleObjectRelativePath(bundle.Objects[0].SHA256)
				contents := candidate.Objects[objectPath]
				delete(candidate.Objects, objectPath)
				alias := "objects/sha256/../" + bundle.Objects[0].SHA256 + ".gz"
				if name == "case path alias" {
					alias = strings.ToUpper(objectPath)
				}
				candidate.Objects[alias] = contents
			}
			if !bytes.Equal(candidate.Root, files.Root) {
				t.Fatal("test unexpectedly edited root directly")
			}
			if name != "missing object" && name != "extra object" && name != "traversal path" && name != "case path alias" {
				resealBundleForTest(t, &index)
				candidate.Root = mustMarshalBundle(t, index)
			}
			if _, err := VerifyBundle(candidate, fixture.contract); err == nil {
				t.Fatal("invalid bundle was accepted")
			}
		})
	}

	legacy, err := MarshalJSON(evidence)
	if err != nil {
		t.Fatal(err)
	}
	noncanonical := append([]byte(" "), files.Root...)
	if _, err := ParseBundle(noncanonical); err == nil {
		t.Fatal("noncanonical root JSON was accepted")
	}
	duplicate := append([]byte(`{"schema_id":"duplicate",`), bytes.TrimSpace(files.Root)[1:]...)
	if _, err := ParseBundle(duplicate); err == nil {
		t.Fatal("duplicate root field was accepted")
	}
	caseVariant := append([]byte(`{"SCHEMA_ID":"duplicate",`), bytes.TrimSpace(files.Root)[1:]...)
	if _, err := ParseBundle(caseVariant); err == nil {
		t.Fatal("case-variant root field was accepted")
	}
	if _, err := VerifyParity(legacy, files, fixture.contract); err != nil {
		t.Fatalf("valid parity after adversarial clones: %v", err)
	}
}

func TestBundleV2RejectsCorruptConcatenatedTrailingAndNoncanonicalGZIP(t *testing.T) {
	fixture, _, files := newBundleFixture(t)
	bundle, err := ParseBundle(files.Root)
	if err != nil {
		t.Fatal(err)
	}
	target := bundle.Objects[0]
	targetPath, err := BundleObjectRelativePath(target.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	valid := files.Objects[targetPath]
	var alternative bytes.Buffer
	alternativeWriter, err := gzip.NewWriterLevel(&alternative, gzip.BestSpeed)
	if err != nil {
		t.Fatal(err)
	}
	alternativeWriter.Header.ModTime = time.Unix(123, 0).UTC()
	alternativeWriter.Header.OS = 3
	raw, err := strictGunzip(valid, target.UncompressedSize)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := alternativeWriter.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := alternativeWriter.Close(); err != nil {
		t.Fatal(err)
	}

	tests := map[string][]byte{
		"corrupt":                 append(append([]byte(nil), valid[:len(valid)-1]...), valid[len(valid)-1]^0xff),
		"truncated":               append([]byte(nil), valid[:len(valid)-4]...),
		"concatenated":            append(append([]byte(nil), valid...), valid...),
		"trailing":                append(append([]byte(nil), valid...), 0),
		"valid noncanonical gzip": alternative.Bytes(),
	}
	for name, compressed := range tests {
		t.Run(name, func(t *testing.T) {
			candidate := cloneBundleFiles(files)
			index := bundle
			index.Objects = append([]BundleObject(nil), bundle.Objects...)
			candidate.Objects[targetPath] = compressed
			index.Objects[0].CompressedSize = int64(len(compressed))
			index.Objects[0].CompressedSHA256 = sha256Text(compressed)
			resealBundleForTest(t, &index)
			candidate.Root = mustMarshalBundle(t, index)
			if _, err := VerifyBundle(candidate, fixture.contract); err == nil {
				t.Fatal("invalid gzip object was accepted")
			}
		})
	}
}

func TestCanonicalStoredGZIPGoldenVectorAndBlockBoundary(t *testing.T) {
	standardRoundTrip := func(encoded, want []byte) {
		t.Helper()
		reader, err := gzip.NewReader(bytes.NewReader(encoded))
		if err != nil {
			t.Fatalf("standard gzip reader rejected canonical stream: %v", err)
		}
		decoded, err := io.ReadAll(reader)
		closeErr := reader.Close()
		if err != nil || closeErr != nil || !bytes.Equal(decoded, want) {
			t.Fatalf("standard gzip roundtrip bytes=%d error=%v close=%v", len(decoded), err, closeErr)
		}
	}
	compressed, err := deterministicGZIP([]byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	const golden = "1f8b08000000000000ff010500faff68656c6c6f86a6103605000000"
	if got := hex.EncodeToString(compressed); got != golden {
		t.Fatalf("canonical stored gzip changed:\n got %s\nwant %s", got, golden)
	}
	decoded, err := strictGunzip(compressed, 5)
	if err != nil || string(decoded) != "hello" {
		t.Fatalf("golden vector roundtrip=%q error=%v", decoded, err)
	}
	standardRoundTrip(compressed, []byte("hello"))
	for _, size := range []int{65535, 65536} {
		raw := bytes.Repeat([]byte{byte(size)}, size)
		encoded, err := deterministicGZIP(raw)
		if err != nil {
			t.Fatal(err)
		}
		blocks := (size + 65534) / 65535
		wantSize := 10 + 5*blocks + size + 8
		if len(encoded) != wantSize {
			t.Fatalf("size=%d encoded=%d want=%d", size, len(encoded), wantSize)
		}
		if size == 65535 && encoded[10] != 0x01 {
			t.Fatal("single maximum block was not final")
		}
		if size == 65536 && (encoded[10] != 0x00 || encoded[10+5+65535] != 0x01) {
			t.Fatal("65536-byte boundary did not use one maximal and one final block")
		}
		decoded, err := strictGunzip(encoded, int64(size))
		if err != nil || !bytes.Equal(decoded, raw) {
			t.Fatalf("boundary size=%d roundtrip error=%v", size, err)
		}
		standardRoundTrip(encoded, raw)
	}
	archiveSize, err := deterministicArchiveSize(map[string][]byte{"a": []byte("b")})
	if err != nil || archiveSize != 2071 {
		t.Fatalf("canonical one-file USTAR+stored-gzip size=%d error=%v", archiveSize, err)
	}
}

func TestDeterministicGZIPCapacityRejectsIntegerOverflow(t *testing.T) {
	tests := []struct {
		name       string
		dataLength int
		want       int
	}{
		{name: "empty", dataLength: 0, want: 23},
		{name: "one byte", dataLength: 1, want: 24},
		{name: "one full block", dataLength: 65535, want: 65558},
		{name: "two blocks", dataLength: 65536, want: 65564},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := deterministicGZIPCapacity(test.dataLength)
			if err != nil || got != test.want {
				t.Fatalf("deterministicGZIPCapacity(%d)=%d, %v; want %d", test.dataLength, got, err, test.want)
			}
		})
	}
	if _, err := deterministicGZIPCapacity(-1); err == nil {
		t.Fatal("negative gzip input length was accepted")
	}

	maxInt := int(^uint(0) >> 1)
	low, high := 0, maxInt
	for low < high {
		delta := high - low
		midpoint := low + delta/2
		if delta%2 != 0 {
			midpoint++
		}
		if _, err := deterministicGZIPCapacity(midpoint); err == nil {
			low = midpoint
		} else {
			high = midpoint - 1
		}
	}
	largest := low
	capacity, err := deterministicGZIPCapacity(largest)
	if err != nil || capacity > maxInt {
		t.Fatalf("largest safe gzip capacity input=%d capacity=%d error=%v", largest, capacity, err)
	}
	if largest == maxInt {
		t.Fatal("gzip capacity boundary unexpectedly equals max int")
	}
	if _, err := deterministicGZIPCapacity(largest + 1); err == nil {
		t.Fatalf("gzip input immediately above safe capacity boundary %d was accepted", largest)
	}
}

func TestBundleV2RejectsDigestCollisionAndResourceLimitDescriptors(t *testing.T) {
	fixture := newEvidenceFixture(t)
	evidence, err := Assemble(fixture.contract, fixture.authorization, fixture.manifest, fixture.ci, fixture.publisher, fixture.observation, fixture.attestationBundle)
	if err != nil {
		t.Fatal(err)
	}
	firstDocument := []byte(fixture.attestationBundle.Entries[0].DocumentJSON)
	firstDigest := fixture.attestationBundle.Entries[0].DocumentSHA256
	collidingDigest := func(data []byte) string {
		if bytes.Equal(data, firstDocument) {
			return firstDigest
		}
		return firstDigest
	}
	if _, err := buildBundleWithDigest(evidence, fixture.contract, collidingDigest); ErrorCode(err) != CodeDigestMismatch || !strings.Contains(err.Error(), "digest collision") {
		t.Fatalf("digest collision error=%v code=%q", err, ErrorCode(err))
	}

	files, err := BuildBundle(evidence, fixture.contract)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := ParseBundle(files.Root)
	if err != nil {
		t.Fatal(err)
	}
	bundle.Objects[0].UncompressedSize = MaxBundleObjectUncompressed + 1
	resealBundleForTest(t, &bundle)
	files.Root = mustMarshalBundle(t, bundle)
	if _, err := VerifyBundle(files, fixture.contract); err == nil {
		t.Fatal("oversized decompression descriptor was accepted")
	}

	_, _, validFiles := newBundleFixture(t)
	invalidSchema, err := ParseBundle(validFiles.Root)
	if err != nil {
		t.Fatal(err)
	}
	invalidSchema.SchemaVersion++
	resealBundleForTest(t, &invalidSchema)
	validFiles.Root = mustMarshalBundle(t, invalidSchema)
	if _, err := VerifyBundle(validFiles, fixture.contract); err == nil {
		t.Fatal("unsupported bundle schema version was accepted")
	}
}

func TestBundleIndexEnforcesObjectCountAndAggregateBoundaries(t *testing.T) {
	_, _, files := newBundleFixture(t)
	base, err := ParseBundle(files.Root)
	if err != nil {
		t.Fatal(err)
	}
	descriptors := func(count int, rawSize, compressedSize int64) []BundleObject {
		items := make([]BundleObject, count)
		for index := range items {
			items[index] = BundleObject{
				SHA256: fmt.Sprintf("%064x", index+1), MediaType: RawJSONMedia, Encoding: BundleEncodingGZIP,
				UncompressedSize: rawSize, CompressedSize: compressedSize, CompressedSHA256: strings.Repeat("a", 64),
			}
		}
		return items
	}
	valid := base
	valid.Objects = descriptors(MaxBundleObjects, 1, 24)
	if err := validateBundleIndex(valid); err != nil {
		t.Fatalf("exact %d-object boundary rejected: %v", MaxBundleObjects, err)
	}
	tooMany := valid
	tooMany.Objects = descriptors(MaxBundleObjects+1, 1, 24)
	if err := validateBundleIndex(tooMany); err == nil {
		t.Fatal("65-object bundle index was accepted")
	}
	overRaw := base
	overRaw.Objects = descriptors(5, MaxBundleObjectUncompressed, 24)
	overRaw.Objects[4].UncompressedSize = 1
	if err := validateBundleIndex(overRaw); err == nil {
		t.Fatal("aggregate uncompressed bytes above 64 MiB were accepted")
	}
	overCompressed := base
	overCompressed.Objects = descriptors(MaxBundleObjects, 1<<20, (1<<20)+(2<<10))
	if err := validateBundleIndex(overCompressed); err == nil {
		t.Fatal("aggregate compressed bytes above the stored-gzip envelope were accepted")
	}
}

func TestBundleV2OfflineVerificationIgnoresNetworkAndCredentialEnvironment(t *testing.T) {
	fixture, evidence, files := newBundleFixture(t)
	t.Setenv("PATH", t.TempDir())
	t.Setenv("GH_TOKEN", "sentinel-must-not-be-read")
	t.Setenv("GITHUB_TOKEN", "sentinel-must-not-be-read")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "sentinel-must-not-be-read")
	verified, err := VerifyBundle(files, fixture.contract)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := MarshalJSON(evidence)
	if err != nil {
		t.Fatal(err)
	}
	reconstructed, err := MarshalJSON(verified)
	if err != nil || !bytes.Equal(reconstructed, legacy) {
		t.Fatalf("offline reconstruction mismatch: %v", err)
	}
}

func TestBundleV2CleanExportReplaysThroughCLIWithNetworkAndCredentialsDenied(t *testing.T) {
	fixture, evidence, files := newBundleFixture(t)
	root := t.TempDir()
	bundleDirectory := filepath.Join(root, "bundle")
	if err := os.MkdirAll(filepath.Join(bundleDirectory, BundleObjectStoreDirectory), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundleDirectory, BundleRootName), files.Root, 0o600); err != nil {
		t.Fatal(err)
	}
	for name, data := range files.Objects {
		if err := os.WriteFile(filepath.Join(bundleDirectory, filepath.FromSlash(name)), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	legacy, err := MarshalJSON(evidence)
	if err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(root, "release-evidence.json")
	if err := os.WriteFile(legacyPath, legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	repositoryRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	contractPath := filepath.Join(repositoryRoot, "release", "contract.v1.json")
	goBinary, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	checker := filepath.Join(root, "releasecheck")
	build := exec.Command(goBinary, "build", "-trimpath", "-o", checker, "./cmd/releasecheck")
	build.Dir = repositoryRoot
	build.Env = os.Environ()
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build releasecheck: %v\n%s", err, output)
	}
	emptyPath := filepath.Join(root, "no-network-commands")
	if err := os.Mkdir(emptyPath, 0o700); err != nil {
		t.Fatal(err)
	}
	offlineEnvironment := []string{
		"PATH=" + emptyPath,
		"GH_TOKEN=sentinel-gh-must-not-appear",
		"GITHUB_TOKEN=sentinel-github-must-not-appear",
		"AWS_SECRET_ACCESS_KEY=sentinel-cloud-must-not-appear",
		"TMPDIR=" + root,
	}
	verify := exec.Command(checker, "evidence", "bundle-verify", "--contract", contractPath, "--bundle-dir", bundleDirectory, "--json")
	verify.Env = offlineEnvironment
	verifyOutput, err := verify.CombinedOutput()
	if err != nil {
		t.Fatalf("offline CLI verify: %v\n%s", err, verifyOutput)
	}
	if bytes.Contains(verifyOutput, []byte("sentinel")) {
		t.Fatalf("credential sentinel reached checker output: %s", verifyOutput)
	}
	var verification struct {
		OK             bool   `json:"ok"`
		Decision       string `json:"decision"`
		Repository     string `json:"repository"`
		ReleaseVersion string `json:"release_version"`
	}
	if err := json.Unmarshal(verifyOutput, &verification); err != nil || !verification.OK || verification.Decision != "pass" || verification.Repository != fixture.authorization.Repository || verification.ReleaseVersion != fixture.authorization.ReleaseVersion {
		t.Fatalf("verification=%+v err=%v output=%s", verification, err, verifyOutput)
	}
	parityPath := filepath.Join(root, "parity.json")
	parity := exec.Command(checker, "evidence", "bundle-parity", "--contract", contractPath, "--legacy", legacyPath, "--bundle-dir", bundleDirectory, "--output", parityPath, "--json")
	parity.Env = offlineEnvironment
	parityOutput, err := parity.CombinedOutput()
	if err != nil {
		t.Fatalf("offline CLI parity: %v\n%s", err, parityOutput)
	}
	if bytes.Contains(parityOutput, []byte("sentinel")) {
		t.Fatalf("credential sentinel reached parity output: %s", parityOutput)
	}
	var parityDocument ParityResult
	if err := json.Unmarshal(parityOutput, &parityDocument); err != nil || !parityDocument.OK || !parityDocument.ReconstructedByteExact || parityDocument.Result != "pass" {
		t.Fatalf("parity=%+v err=%v output=%s", parityDocument, err, parityOutput)
	}
	changedContract := contractWithChangedPublisherWorkflow(t, fixture.contract)
	changedContractBytes, err := json.MarshalIndent(changedContract, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	changedContractPath := filepath.Join(root, "contract-semantic-mismatch.json")
	if err := os.WriteFile(changedContractPath, append(changedContractBytes, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	type failureDocument struct {
		OK    bool `json:"ok"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	failureCodes := make(map[string]string)
	failureStatuses := make(map[string]int)
	commands := map[string][]string{
		"v1": {"evidence", "verify", "--contract", changedContractPath, "--input", legacyPath, "--json"},
		"v2": {"evidence", "bundle-verify", "--contract", changedContractPath, "--bundle-dir", bundleDirectory, "--json"},
	}
	for format, args := range commands {
		command := exec.Command(checker, args...)
		command.Env = offlineEnvironment
		output, runErr := command.CombinedOutput()
		if runErr == nil {
			t.Fatalf("%s semantic mismatch unexpectedly passed: %s", format, output)
		}
		if bytes.Contains(output, []byte("sentinel")) {
			t.Fatalf("credential sentinel reached %s failure output: %s", format, output)
		}
		failureStatuses[format] = runErr.(*exec.ExitError).ExitCode()
		var document failureDocument
		if err := json.Unmarshal(output, &document); err != nil || document.OK || document.Error.Code == "" {
			t.Fatalf("%s failure=%+v parse=%v output=%s", format, document, err, output)
		}
		failureCodes[format] = document.Error.Code
	}
	if failureStatuses["v1"] != failureStatuses["v2"] || failureCodes["v1"] != failureCodes["v2"] || failureCodes["v1"] != CodeDigestMismatch {
		t.Fatalf("CLI semantic failure parity: statuses=%v codes=%v", failureStatuses, failureCodes)
	}
	genesisPath := filepath.Join(root, "genesis.v1.json")
	genesisCreate := exec.Command(checker, "evidence", "genesis-create", "--contract", contractPath, "--bundle-dir", bundleDirectory, "--output", genesisPath)
	genesisCreate.Env = offlineEnvironment
	genesisCreateOutput, err := genesisCreate.CombinedOutput()
	if err != nil {
		t.Fatalf("offline CLI genesis create: %v\n%s", err, genesisCreateOutput)
	}
	genesisVerify := exec.Command(checker, "evidence", "genesis-verify", "--contract", contractPath, "--input", genesisPath, "--bundle-dir", bundleDirectory, "--json")
	genesisVerify.Env = offlineEnvironment
	genesisVerifyOutput, err := genesisVerify.CombinedOutput()
	if err != nil {
		t.Fatalf("offline CLI genesis verify: %v\n%s", err, genesisVerifyOutput)
	}
	if bytes.Contains(genesisCreateOutput, []byte("sentinel")) || bytes.Contains(genesisVerifyOutput, []byte("sentinel")) {
		t.Fatalf("credential sentinel reached genesis output: create=%s verify=%s", genesisCreateOutput, genesisVerifyOutput)
	}
	var genesisDocument struct {
		OK                  bool   `json:"ok"`
		Decision            string `json:"decision"`
		BundleTupleVerified bool   `json:"bundle_tuple_verified"`
		EvidenceRunID       int64  `json:"evidence_run_id"`
	}
	if err := json.Unmarshal(genesisVerifyOutput, &genesisDocument); err != nil || !genesisDocument.OK || genesisDocument.Decision != "pass" || !genesisDocument.BundleTupleVerified || genesisDocument.EvidenceRunID != 333333 {
		t.Fatalf("genesis verification=%+v err=%v output=%s", genesisDocument, err, genesisVerifyOutput)
	}
}

func TestBundleV2V0015SizedFixtureMeetsMeasuredTargets(t *testing.T) {
	fixture := newEvidenceFixture(t)
	for kindOffset, targetSize := range []int{15330, 253307} {
		var entries []ghAttestationVerificationEntry
		if err := decodeStrict([]byte(fixture.attestationBundle.Entries[kindOffset].DocumentJSON), &entries); err != nil {
			t.Fatal(err)
		}
		entries[0].Attestation.Bundle.VerificationMaterial.Certificate.RawBytes = ""
		base, err := json.Marshal(entries)
		if err != nil {
			t.Fatal(err)
		}
		fillSize := targetSize - len(base)
		if fillSize <= 0 {
			t.Fatalf("target document size %d is below structural JSON size %d", targetSize, len(base))
		}
		entries[0].Attestation.Bundle.VerificationMaterial.Certificate.RawBytes = deterministicBase64Like(fillSize, uint64(kindOffset+1))
		document, err := json.Marshal(entries)
		if err != nil || len(document) != targetSize {
			t.Fatalf("shape document size=%d want=%d err=%v", len(document), targetSize, err)
		}
		for entryIndex := kindOffset; entryIndex < len(fixture.attestationBundle.Entries); entryIndex += 2 {
			setAttestationDocumentBytes(fixture, entryIndex, document)
		}
	}
	evidence, err := Assemble(fixture.contract, fixture.authorization, fixture.manifest, fixture.ci, fixture.publisher, fixture.observation, fixture.attestationBundle)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := MarshalJSON(evidence)
	if err != nil {
		t.Fatal(err)
	}
	files, err := BuildBundle(evidence, fixture.contract)
	if err != nil {
		t.Fatal(err)
	}
	metrics, err := MeasureBundle(legacy, files, map[string][]byte{
		"index.md":                bytes.Repeat([]byte{'i'}, 4122),
		"metrics-comparison.json": bytes.Repeat([]byte{'j'}, 2129),
		"metrics-comparison.md":   bytes.Repeat([]byte{'m'}, 693),
	}, fixture.contract)
	if err != nil {
		t.Fatal(err)
	}
	parity, err := VerifyParity(legacy, files, fixture.contract)
	if err != nil {
		t.Fatal(err)
	}
	parityBytes, err := MarshalJSON(parity)
	if err != nil {
		t.Fatal(err)
	}
	encodedMetrics, err := MarshalJSON(metrics)
	if err != nil {
		t.Fatal(err)
	}
	wantMetadata := int64(len(files.Root)) + metrics.AuxiliaryBytes + int64(len(parityBytes)) + int64(len(encodedMetrics))
	if len(legacy) < 1_300_000 || metrics.AuxiliaryBytes != 6944 || metrics.ParityBytes != int64(len(parityBytes)) ||
		metrics.StorageMetricsSelfBytes != int64(len(encodedMetrics)) || metrics.CompactDurableMetadataBytes != wantMetadata ||
		metrics.CompactRootAttemptLogicalBytes != 2*wantMetadata+metrics.CompactObjectCompressedBytes ||
		metrics.CompactUniqueGitBlobBytes != wantMetadata+metrics.CompactObjectCompressedBytes ||
		metrics.CompactOfflineReconstructedBytes != wantMetadata+metrics.CompactObjectUncompressedBytes ||
		metrics.CompactRootIndexBytes >= MaxBundleRootBytes || metrics.LogicalPayloadReductionPermille < 600 || !metrics.TargetsMet {
		t.Fatalf("realistic metrics=%+v legacy=%d", metrics, len(legacy))
	}
	t.Logf("v0.0.15-sized fixture: legacy_root=%d compact_root=%d objects_raw=%d objects_gzip=%d logical_before=%d logical_after=%d reduction_permille=%d export_tar_gzip=%d",
		metrics.LegacyRootJSONBytes, metrics.CompactRootIndexBytes,
		metrics.CompactObjectUncompressedBytes, metrics.CompactObjectCompressedBytes,
		metrics.LegacyRootAttemptLogicalBytes, metrics.CompactRootAttemptLogicalBytes,
		metrics.LogicalPayloadReductionPermille, metrics.CompactDeterministicExportTarGZIPBytes)
}

func TestBundleMetricsFailClosedBelowTargetsAndAtRootBoundary(t *testing.T) {
	fixture, evidence, files := newBundleFixture(t)
	legacy, err := MarshalJSON(evidence)
	if err != nil {
		t.Fatal(err)
	}
	// Unchanged auxiliary payload dominates this deliberately adverse
	// measurement, so the required 60% reduction is not met.
	if _, err := MeasureBundle(legacy, files, map[string][]byte{
		"index.md":                bytes.Repeat([]byte{'x'}, 2<<20),
		"metrics-comparison.json": []byte("{}\n"),
		"metrics-comparison.md":   []byte("# metrics\n"),
	}, fixture.contract); err == nil {
		t.Fatal("below-target storage measurement returned pass")
	}
	if storageTargetsMet(MaxBundleRootBytes, 600) {
		t.Fatal("root equal to the strict below-150-KiB boundary was accepted")
	}
	if !storageTargetsMet(MaxBundleRootBytes-1, 600) || !storageTargetsMet(1, 600) || storageTargetsMet(1, 599) {
		t.Fatal("storage target boundary predicate is incorrect")
	}
}

func TestStorageMetricsSelfSizeRejectsDigitBoundaryAmbiguity(t *testing.T) {
	base, err := MarshalJSON(StorageMetrics{StorageMetricsSelfBytes: 10_000})
	if err != nil {
		t.Fatal(err)
	}
	padding := 10_000 - len(base)
	if padding <= 0 {
		t.Fatalf("storage metrics fixture unexpectedly exceeds digit-boundary target: %d", len(base))
	}
	_, err = storageMetricsFixedPoint(func(candidate int64) StorageMetrics {
		return StorageMetrics{StorageMetricsSelfBytes: candidate, Result: strings.Repeat("x", padding)}
	})
	if err == nil || !strings.Contains(err.Error(), "2 fixed points") {
		t.Fatalf("digit-boundary ambiguity was not rejected: %v", err)
	}
}

func TestBundleV2RejectsSecretClassRootField(t *testing.T) {
	_, _, files := newBundleFixture(t)
	trimmed := bytes.TrimSpace(files.Root)
	secretField := append(append([]byte(nil), trimmed[:len(trimmed)-1]...), []byte(`,"token":"sentinel"}`)...)
	if _, err := ParseBundle(secretField); err == nil {
		t.Fatal("secret-class unknown root field was accepted")
	}
}

func newBundleFixture(t *testing.T) (*evidenceFixture, Evidence, BundleFiles) {
	t.Helper()
	fixture := newEvidenceFixture(t)
	evidence, err := Assemble(fixture.contract, fixture.authorization, fixture.manifest, fixture.ci, fixture.publisher, fixture.observation, fixture.attestationBundle)
	if err != nil {
		t.Fatal(err)
	}
	files, err := BuildBundle(evidence, fixture.contract)
	if err != nil {
		t.Fatal(err)
	}
	return fixture, evidence, files
}

func cloneBundleFiles(files BundleFiles) BundleFiles {
	clone := BundleFiles{Root: append([]byte(nil), files.Root...), Objects: make(map[string][]byte, len(files.Objects))}
	for name, data := range files.Objects {
		clone.Objects[name] = append([]byte(nil), data...)
	}
	return clone
}

func equalObjectMaps(left, right map[string][]byte) bool {
	if len(left) != len(right) {
		return false
	}
	for name, data := range left {
		if !bytes.Equal(data, right[name]) {
			return false
		}
	}
	return true
}

func resealBundleForTest(t *testing.T, bundle *Bundle) {
	t.Helper()
	bundle.BundleSHA256 = ""
	digest, err := BundleSHA256(*bundle)
	if err != nil {
		t.Fatal(err)
	}
	bundle.BundleSHA256 = digest
}

func mustMarshalBundle(t *testing.T, bundle Bundle) []byte {
	t.Helper()
	encoded, err := MarshalJSON(bundle)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestBundleRootStrictlyRejectsUnknownNestedObjectFields(t *testing.T) {
	_, _, files := newBundleFixture(t)
	var value map[string]any
	if err := json.Unmarshal(files.Root, &value); err != nil {
		t.Fatal(err)
	}
	objects := value["objects"].([]any)
	objects[0].(map[string]any)["password"] = "sentinel"
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseBundle(append(encoded, '\n')); err == nil {
		t.Fatal("unknown nested secret field was accepted")
	}
	_ = os.Getenv("GH_TOKEN") // compile-time reminder: production verifier never reads it.
}

func TestDeterministicExportRejectsReservedAndCrossPlatformTraversalPaths(t *testing.T) {
	_, _, files := newBundleFixture(t)
	for _, name := range []string{
		BundleRootName,
		BundleObjectStoreDirectory + "/" + strings.Repeat("a", 64) + ".gz",
		"..", ".", `..\escape`, `C:\escape`, "bad\x00name", "bad\nname",
	} {
		t.Run(strings.ReplaceAll(name, "/", "_"), func(t *testing.T) {
			if _, err := deterministicExportArchiveSize(files, map[string][]byte{name: []byte("x")}); err == nil {
				t.Fatalf("unsafe or reserved export path %q was accepted", name)
			}
		})
	}
}

func deterministicBase64Like(length int, seed uint64) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	output := make([]byte, length)
	state := seed | 1
	for index := range output {
		state ^= state << 13
		state ^= state >> 7
		state ^= state << 17
		output[index] = alphabet[state&63]
	}
	return string(output)
}
