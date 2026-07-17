// Package releasepromotion creates and verifies exact-source release promotion
// evidence. A manifest is valid only when every native target was built and
// checked in one workflow run attempt.
package releasepromotion

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ildarbinanas-design/env-vault/internal/releasecontract"
)

const (
	ManifestSchemaID = "env-vault.promotion-manifest.v1"
	PlatformSchemaID = "env-vault.promotion-platform.v1"
)

var (
	shaPattern    = regexp.MustCompile(`^[0-9a-f]{40}$`)
	digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

type Workflow struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	File       string `json:"file"`
	RunID      int64  `json:"run_id"`
	RunAttempt int    `json:"run_attempt"`
	Event      string `json:"event"`
	HeadSHA    string `json:"head_sha"`
}

type FileDigest struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}

type LiteralVersionResults struct {
	Flag    string `json:"flag"`
	Command string `json:"command"`
	JSON    string `json:"json"`
}

type ContractEvidence struct {
	SHA256 string `json:"sha256"`
	Count  int    `json:"count"`
}

type CoverageEvidence struct {
	SHA256           string  `json:"sha256"`
	StatementPercent float64 `json:"statement_percent"`
	FloorPercent     float64 `json:"floor_percent"`
	CriticalCovered  int     `json:"critical_covered"`
	CriticalTotal    int     `json:"critical_total"`
}

type LeakEvidence struct {
	SHA256          string `json:"sha256"`
	Status          string `json:"status"`
	Detected        bool   `json:"detected"`
	Occurrences     int    `json:"occurrences"`
	RegistryRecords int    `json:"registry_records"`
}

type PlatformEvidence struct {
	SchemaID       string                `json:"schema_id"`
	PlatformID     string                `json:"platform_id"`
	GOOS           string                `json:"goos"`
	GOARCH         string                `json:"goarch"`
	SourceSHA      string                `json:"source_sha"`
	ReleaseVersion string                `json:"release_version"`
	Repository     string                `json:"repository"`
	RunID          int64                 `json:"run_id"`
	RunAttempt     int                   `json:"run_attempt"`
	ArtifactName   string                `json:"artifact_name"`
	E2EArtifact    string                `json:"e2e_artifact_name"`
	Archive        FileDigest            `json:"archive"`
	Checksum       FileDigest            `json:"checksum"`
	BinarySHA256   string                `json:"binary_sha256"`
	SuiteHash      string                `json:"suite_hash"`
	Metadata       FileDigest            `json:"metadata"`
	LiteralVersion LiteralVersionResults `json:"literal_version"`
	Contracts      ContractEvidence      `json:"contracts"`
	Coverage       CoverageEvidence      `json:"coverage"`
	Leak           LeakEvidence          `json:"leak"`
	GoVersion      string                `json:"go_version"`
	BinaryGo       string                `json:"binary_go_version"`
	Gotestsum      string                `json:"gotestsum_version"`
	Result         string                `json:"result"`
}

type SourceQuality struct {
	Module   string            `json:"module"`
	Test     string            `json:"test"`
	Vet      string            `json:"vet"`
	Smoke    string            `json:"smoke"`
	Race     string            `json:"race"`
	Licenses map[string]string `json:"licenses"`
}

type Manifest struct {
	SchemaID       string             `json:"schema_id"`
	SchemaVersion  int                `json:"schema_version"`
	SourceSHA      string             `json:"source_sha"`
	ReleaseVersion string             `json:"release_version"`
	Repository     string             `json:"repository"`
	Workflow       Workflow           `json:"workflow"`
	ContractSchema string             `json:"contract_schema"`
	ContractSHA256 string             `json:"contract_sha256"`
	SuiteHash      string             `json:"suite_hash"`
	SourceQuality  SourceQuality      `json:"source_quality"`
	Platforms      []PlatformEvidence `json:"platforms"`
	CreatedAt      string             `json:"created_at"`
	Result         string             `json:"result"`
	ManifestSHA256 string             `json:"manifest_sha256"`
}

type RecordOptions struct {
	ContractPath    string
	PlatformID      string
	SourceSHA       string
	ReleaseVersion  string
	Repository      string
	RunID           int64
	RunAttempt      int
	ArchivePath     string
	ChecksumPath    string
	BinaryPath      string
	ReportsRoot     string
	ArtifactName    string
	E2EArtifactName string
	CoverageFloor   float64
	runBinary       func(string, ...string) ([]byte, error)
}

type AssembleOptions struct {
	ContractPath   string
	SourceSHA      string
	ReleaseVersion string
	Repository     string
	RunID          int64
	RunAttempt     int
	Event          string
	CreatedAt      time.Time
	Evidence       []PlatformEvidence
}

type VerifyOptions struct {
	ContractPath   string
	SourceSHA      string
	ReleaseVersion string
	Repository     string
	RunID          int64
	RunAttempt     int
	ArtifactsRoot  string
}

type reportMetadata struct {
	SchemaVersion            string            `json:"schema_version"`
	Phase                    string            `json:"phase"`
	Status                   string            `json:"status"`
	SubjectKind              string            `json:"subject_kind"`
	Platform                 string            `json:"platform"`
	CommitSHA                string            `json:"commit_sha"`
	GitHubRepository         string            `json:"github_repository"`
	GitHubRunID              string            `json:"github_run_id"`
	GitHubRunAttempt         string            `json:"github_run_attempt"`
	SuiteHash                string            `json:"suite_hash"`
	StatementCoveragePercent float64           `json:"statement_coverage_percent"`
	BinarySHA256             string            `json:"binary_sha256"`
	GoVersion                string            `json:"go_version"`
	BinaryGoVersion          string            `json:"binary_go_version"`
	GotestsumVersion         string            `json:"gotestsum_version"`
	Artifact                 reportArtifact    `json:"artifact"`
	Counts                   reportCounts      `json:"counts"`
	UnexpectedSkips          []json.RawMessage `json:"unexpected_skips"`
	ContractRecords          []json.RawMessage `json:"contract_records"`
	EvidenceSHA256           map[string]string `json:"evidence_sha256"`
}

type reportArtifact struct {
	SHA256           string `json:"sha256"`
	ChecksumVerified bool   `json:"checksum_verified"`
}

type reportCounts struct {
	Failed  int `json:"failed"`
	Skipped int `json:"skipped"`
	Missing int `json:"missing"`
}

type featureCoverage struct {
	SchemaVersion       string            `json:"schema_version"`
	Platform            string            `json:"platform"`
	SuiteHash           string            `json:"suite_hash"`
	CriticalCovered     int               `json:"critical_covered"`
	CriticalTotal       int               `json:"critical_total"`
	CriticalCoveragePct float64           `json:"critical_coverage_percent"`
	MissingCritical     []json.RawMessage `json:"missing_critical"`
	UnexpectedSkips     []json.RawMessage `json:"unexpected_skips"`
}

type leakScan struct {
	SchemaVersion string            `json:"schema_version"`
	Status        string            `json:"status"`
	Detected      bool              `json:"detected"`
	Occurrences   int               `json:"occurrences"`
	RegistryCount int               `json:"registry_records"`
	Findings      []json.RawMessage `json:"findings"`
}

func RecordPlatform(options RecordOptions) (PlatformEvidence, error) {
	_, platform, err := loadPlatform(options.ContractPath, options.PlatformID)
	if err != nil {
		return PlatformEvidence{}, err
	}
	if err := validateRecordIdentity(options.SourceSHA, options.ReleaseVersion, options.Repository, options.RunID, options.RunAttempt); err != nil {
		return PlatformEvidence{}, err
	}
	if options.CoverageFloor <= 0 {
		return PlatformEvidence{}, errors.New("coverage floor must be positive")
	}
	if filepath.Base(options.ArchivePath) != platform.Archive || filepath.Base(options.ChecksumPath) != platform.Checksum {
		return PlatformEvidence{}, errors.New("archive or checksum name differs from the release contract")
	}
	archiveDigest, err := fileSHA256(options.ArchivePath)
	if err != nil {
		return PlatformEvidence{}, err
	}
	checksumDigest, err := fileSHA256(options.ChecksumPath)
	if err != nil {
		return PlatformEvidence{}, err
	}
	if err := verifyChecksum(options.ChecksumPath, platform.Archive, archiveDigest); err != nil {
		return PlatformEvidence{}, err
	}
	binaryDigest, err := fileSHA256(options.BinaryPath)
	if err != nil {
		return PlatformEvidence{}, err
	}
	runner := options.runBinary
	if runner == nil {
		runner = executeBinary
	}
	if err := verifyLiteralVersion(runner, options.BinaryPath, options.ReleaseVersion); err != nil {
		return PlatformEvidence{}, err
	}

	metadataPath, err := uniqueFile(options.ReportsRoot, "metadata.json")
	if err != nil {
		return PlatformEvidence{}, err
	}
	var metadata reportMetadata
	if err := decodeFile(metadataPath, &metadata); err != nil {
		return PlatformEvidence{}, fmt.Errorf("metadata: %w", err)
	}
	if err := validateMetadata(metadata, options, archiveDigest, binaryDigest); err != nil {
		return PlatformEvidence{}, err
	}
	reportDir := filepath.Dir(metadataPath)
	contractsPath := filepath.Join(reportDir, "contracts.json")
	featurePath := filepath.Join(reportDir, "feature-coverage.json")
	leakPath := filepath.Join(reportDir, "leak-scan.json")
	contractsDigest, err := verifiedEvidenceDigest(metadata, reportDir, "contracts.json")
	if err != nil {
		return PlatformEvidence{}, err
	}
	featureDigest, err := verifiedEvidenceDigest(metadata, reportDir, "feature-coverage.json")
	if err != nil {
		return PlatformEvidence{}, err
	}
	if _, err := fileSHA256(contractsPath); err != nil {
		return PlatformEvidence{}, err
	}
	var coverage featureCoverage
	if err := decodeFile(featurePath, &coverage); err != nil {
		return PlatformEvidence{}, fmt.Errorf("feature coverage: %w", err)
	}
	if coverage.SchemaVersion != "1" || coverage.Platform != options.PlatformID || coverage.SuiteHash != metadata.SuiteHash || coverage.CriticalTotal == 0 || coverage.CriticalCovered != coverage.CriticalTotal || coverage.CriticalCoveragePct != 100 || len(coverage.MissingCritical) != 0 || len(coverage.UnexpectedSkips) != 0 {
		return PlatformEvidence{}, errors.New("critical scenario coverage is incomplete")
	}
	var leak leakScan
	if err := decodeFile(leakPath, &leak); err != nil {
		return PlatformEvidence{}, fmt.Errorf("leak scan: %w", err)
	}
	if leak.SchemaVersion != "1" || leak.Status != "pass" || leak.Detected || leak.Occurrences != 0 || leak.RegistryCount <= 0 || len(leak.Findings) != 0 {
		return PlatformEvidence{}, errors.New("leak scan did not pass its fail-closed contract")
	}
	leakDigest, err := fileSHA256(leakPath)
	if err != nil {
		return PlatformEvidence{}, err
	}
	if metadata.StatementCoveragePercent < options.CoverageFloor {
		return PlatformEvidence{}, fmt.Errorf("statement coverage %.2f is below floor %.2f", metadata.StatementCoveragePercent, options.CoverageFloor)
	}
	metadataDigest, err := fileSHA256(metadataPath)
	if err != nil {
		return PlatformEvidence{}, err
	}
	return PlatformEvidence{
		SchemaID:       PlatformSchemaID,
		PlatformID:     platform.ID,
		GOOS:           platform.GOOS,
		GOARCH:         platform.GOARCH,
		SourceSHA:      options.SourceSHA,
		ReleaseVersion: options.ReleaseVersion,
		Repository:     options.Repository,
		RunID:          options.RunID,
		RunAttempt:     options.RunAttempt,
		ArtifactName:   options.ArtifactName,
		E2EArtifact:    options.E2EArtifactName,
		Archive:        FileDigest{Name: platform.Archive, SHA256: archiveDigest},
		Checksum:       FileDigest{Name: platform.Checksum, SHA256: checksumDigest},
		BinarySHA256:   binaryDigest,
		SuiteHash:      metadata.SuiteHash,
		Metadata:       FileDigest{Name: filepath.ToSlash(filepath.Base(metadataPath)), SHA256: metadataDigest},
		LiteralVersion: LiteralVersionResults{Flag: "pass", Command: "pass", JSON: "pass"},
		Contracts:      ContractEvidence{SHA256: contractsDigest, Count: len(metadata.ContractRecords)},
		Coverage: CoverageEvidence{
			SHA256:           featureDigest,
			StatementPercent: metadata.StatementCoveragePercent,
			FloorPercent:     options.CoverageFloor,
			CriticalCovered:  coverage.CriticalCovered,
			CriticalTotal:    coverage.CriticalTotal,
		},
		Leak:      LeakEvidence{SHA256: leakDigest, Status: leak.Status, Detected: leak.Detected, Occurrences: leak.Occurrences, RegistryRecords: leak.RegistryCount},
		GoVersion: metadata.GoVersion, BinaryGo: metadata.BinaryGoVersion, Gotestsum: metadata.GotestsumVersion,
		Result: "pass",
	}, nil
}

func Assemble(options AssembleOptions) (Manifest, error) {
	contract, err := releasecontract.LoadFile(options.ContractPath)
	if err != nil {
		return Manifest{}, err
	}
	if err := validateReleaseIdentity(options.SourceSHA, options.ReleaseVersion, options.Repository, options.RunID, options.RunAttempt); err != nil {
		return Manifest{}, err
	}
	if options.Event != "push" {
		return Manifest{}, errors.New("promotion manifest requires a push CI run")
	}
	if len(options.Evidence) != len(contract.Platforms) {
		return Manifest{}, fmt.Errorf("platform evidence count=%d, want %d", len(options.Evidence), len(contract.Platforms))
	}
	byID := make(map[string]PlatformEvidence, len(options.Evidence))
	var suiteHash string
	for _, evidence := range options.Evidence {
		if evidence.SchemaID != PlatformSchemaID || evidence.Result != "pass" {
			return Manifest{}, fmt.Errorf("platform %q evidence schema or result is invalid", evidence.PlatformID)
		}
		if evidence.SourceSHA != options.SourceSHA || evidence.ReleaseVersion != options.ReleaseVersion || evidence.Repository != options.Repository || evidence.RunID != options.RunID || evidence.RunAttempt != options.RunAttempt {
			return Manifest{}, fmt.Errorf("platform %q belongs to a different source, version, repository, run, or attempt", evidence.PlatformID)
		}
		if byID[evidence.PlatformID].PlatformID != "" {
			return Manifest{}, fmt.Errorf("platform %q is duplicated", evidence.PlatformID)
		}
		if suiteHash == "" {
			suiteHash = evidence.SuiteHash
		} else if suiteHash != evidence.SuiteHash {
			return Manifest{}, errors.New("platform evidence contains different semantic suite hashes")
		}
		byID[evidence.PlatformID] = evidence
	}
	ordered := make([]PlatformEvidence, 0, len(contract.Platforms))
	for _, platform := range contract.Platforms {
		evidence, ok := byID[platform.ID]
		if !ok {
			return Manifest{}, fmt.Errorf("platform %q evidence is missing", platform.ID)
		}
		if err := validatePlatformAgainstContract(evidence, platform); err != nil {
			return Manifest{}, err
		}
		ordered = append(ordered, evidence)
	}
	contractDigest, err := fileSHA256(options.ContractPath)
	if err != nil {
		return Manifest{}, err
	}
	workflow, err := workflowByID(contract, "ci")
	if err != nil {
		return Manifest{}, err
	}
	createdAt := options.CreatedAt.UTC()
	if createdAt.IsZero() {
		return Manifest{}, errors.New("created-at is required")
	}
	manifest := Manifest{
		SchemaID: ManifestSchemaID, SchemaVersion: 1,
		SourceSHA: options.SourceSHA, ReleaseVersion: options.ReleaseVersion, Repository: options.Repository,
		Workflow:       Workflow{ID: workflow.ID, Name: workflow.Name, File: workflow.File, RunID: options.RunID, RunAttempt: options.RunAttempt, Event: options.Event, HeadSHA: options.SourceSHA},
		ContractSchema: releasecontract.SchemaID, ContractSHA256: contractDigest, SuiteHash: suiteHash,
		SourceQuality: passingSourceQuality(), Platforms: ordered,
		CreatedAt: createdAt.Format(time.RFC3339), Result: "pass",
	}
	digest, err := manifestDigest(manifest)
	if err != nil {
		return Manifest{}, err
	}
	manifest.ManifestSHA256 = digest
	return manifest, nil
}

func Verify(manifest Manifest, options VerifyOptions) error {
	contract, err := releasecontract.LoadFile(options.ContractPath)
	if err != nil {
		return err
	}
	if manifest.SchemaID != ManifestSchemaID || manifest.SchemaVersion != 1 || manifest.Result != "pass" {
		return errors.New("promotion manifest schema or result is invalid")
	}
	if err := validateReleaseIdentity(manifest.SourceSHA, manifest.ReleaseVersion, manifest.Repository, manifest.Workflow.RunID, manifest.Workflow.RunAttempt); err != nil {
		return err
	}
	if options.SourceSHA != "" && manifest.SourceSHA != options.SourceSHA || options.ReleaseVersion != "" && manifest.ReleaseVersion != options.ReleaseVersion || options.Repository != "" && manifest.Repository != options.Repository || options.RunID != 0 && manifest.Workflow.RunID != options.RunID || options.RunAttempt != 0 && manifest.Workflow.RunAttempt != options.RunAttempt {
		return errors.New("promotion manifest does not match the expected source/version/repository/run/attempt tuple")
	}
	workflow, err := workflowByID(contract, "ci")
	if err != nil {
		return err
	}
	if manifest.Workflow.ID != workflow.ID || manifest.Workflow.Name != workflow.Name || manifest.Workflow.File != workflow.File || manifest.Workflow.Event != "push" || manifest.Workflow.HeadSHA != manifest.SourceSHA {
		return errors.New("promotion workflow identity is invalid")
	}
	contractDigest, err := fileSHA256(options.ContractPath)
	if err != nil {
		return err
	}
	if manifest.ContractSchema != releasecontract.SchemaID || manifest.ContractSHA256 != contractDigest || !digestPattern.MatchString(manifest.SuiteHash) {
		return errors.New("promotion contract or suite identity is invalid")
	}
	if err := verifySourceQuality(manifest.SourceQuality); err != nil {
		return err
	}
	if len(manifest.Platforms) != len(contract.Platforms) {
		return errors.New("promotion platform matrix is incomplete")
	}
	for index, platform := range contract.Platforms {
		evidence := manifest.Platforms[index]
		if evidence.PlatformID != platform.ID || evidence.SourceSHA != manifest.SourceSHA || evidence.ReleaseVersion != manifest.ReleaseVersion || evidence.Repository != manifest.Repository || evidence.RunID != manifest.Workflow.RunID || evidence.RunAttempt != manifest.Workflow.RunAttempt || evidence.SuiteHash != manifest.SuiteHash {
			return fmt.Errorf("platform %q identity differs from the promotion tuple", platform.ID)
		}
		if err := validatePlatformAgainstContract(evidence, platform); err != nil {
			return err
		}
		if options.ArtifactsRoot != "" {
			if err := verifyDownloadedFile(options.ArtifactsRoot, evidence.Archive); err != nil {
				return err
			}
			if err := verifyDownloadedFile(options.ArtifactsRoot, evidence.Checksum); err != nil {
				return err
			}
		}
	}
	digest, err := manifestDigest(manifest)
	if err != nil {
		return err
	}
	if manifest.ManifestSHA256 != digest {
		return errors.New("promotion manifest digest mismatch")
	}
	return nil
}

func ReadManifest(filename string) (Manifest, error) {
	var manifest Manifest
	if err := decodeFileStrict(filename, &manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func ReadPlatform(filename string) (PlatformEvidence, error) {
	var evidence PlatformEvidence
	if err := decodeFileStrict(filename, &evidence); err != nil {
		return PlatformEvidence{}, err
	}
	return evidence, nil
}

func WriteJSON(filename string, value any) error {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return err
	}
	directory := filepath.Dir(filename)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".promotion-*.tmp")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(buffer.Bytes()); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryName, filename)
}

func loadPlatform(contractPath, platformID string) (releasecontract.Contract, releasecontract.Platform, error) {
	contract, err := releasecontract.LoadFile(contractPath)
	if err != nil {
		return releasecontract.Contract{}, releasecontract.Platform{}, err
	}
	for _, platform := range contract.Platforms {
		if platform.ID == platformID {
			return contract, platform, nil
		}
	}
	return contract, releasecontract.Platform{}, fmt.Errorf("platform %q is not in the release contract", platformID)
}

func validateRecordIdentity(sourceSHA, version, repository string, runID int64, runAttempt int) error {
	if !shaPattern.MatchString(sourceSHA) || (!releasecontract.IsVersion(version) && version != "ci-"+sourceSHA) || !validRepository(repository) || runID <= 0 || runAttempt <= 0 {
		return errors.New("source SHA, exact build version, repository, run ID, or run attempt is invalid")
	}
	return nil
}

func validateReleaseIdentity(sourceSHA, version, repository string, runID int64, runAttempt int) error {
	if !shaPattern.MatchString(sourceSHA) || !releasecontract.IsVersion(version) || !validRepository(repository) || runID <= 0 || runAttempt <= 0 {
		return errors.New("source SHA, exact release version, repository, run ID, or run attempt is invalid")
	}
	return nil
}

func validRepository(repository string) bool {
	parts := strings.Split(repository, "/")
	return len(parts) == 2 && parts[0] != "" && parts[1] != "" && !strings.Contains(repository, "..")
}

func verifyChecksum(checksumPath, archiveName, archiveDigest string) error {
	data, err := os.ReadFile(checksumPath)
	if err != nil {
		return err
	}
	fields := strings.Fields(string(data))
	if len(fields) != 2 || fields[0] != archiveDigest || strings.TrimPrefix(fields[1], "*") != archiveName {
		return errors.New("checksum sidecar does not bind the exact archive name and digest")
	}
	return nil
}

func verifyLiteralVersion(run func(string, ...string) ([]byte, error), binary, version string) error {
	for name, args := range map[string][]string{"--version": {"--version"}, "version": {"version"}} {
		output, err := run(binary, args...)
		if err != nil {
			return fmt.Errorf("literal %s: %w", name, err)
		}
		if string(output) != version+"\n" {
			return fmt.Errorf("literal %s output %q, want exact version line", name, output)
		}
	}
	output, err := run(binary, "--json", "--version")
	if err != nil {
		return fmt.Errorf("JSON version: %w", err)
	}
	var response struct {
		OK      bool   `json:"ok"`
		Command string `json:"command"`
		Data    struct {
			Version string `json:"version"`
		} `json:"data"`
		Error any `json:"error"`
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	if err := decoder.Decode(&response); err != nil {
		return fmt.Errorf("decode JSON version: %w", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("JSON version response contains trailing data")
	}
	if !response.OK || response.Command != "version" || response.Data.Version != version || response.Error != nil {
		return errors.New("JSON version response differs from the exact release version contract")
	}
	return nil
}

func executeBinary(binary string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, args...)
	output, err := command.Output()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, errors.New("literal version command exceeded 30 seconds")
	}
	if err != nil {
		return nil, err
	}
	return bytes.ReplaceAll(output, []byte("\r\n"), []byte("\n")), nil
}

func validateMetadata(metadata reportMetadata, options RecordOptions, archiveDigest, binaryDigest string) error {
	runID, err := strconv.ParseInt(metadata.GitHubRunID, 10, 64)
	if err != nil {
		return errors.New("E2E metadata run ID is invalid")
	}
	attempt, err := strconv.Atoi(metadata.GitHubRunAttempt)
	if err != nil {
		return errors.New("E2E metadata run attempt is invalid")
	}
	if metadata.SchemaVersion != "1" || metadata.Phase != "candidate" || metadata.Status != "pass" || metadata.SubjectKind != "artifact" || metadata.Platform != options.PlatformID || metadata.CommitSHA != options.SourceSHA || metadata.GitHubRepository != options.Repository || runID != options.RunID || attempt != options.RunAttempt || !digestPattern.MatchString(metadata.SuiteHash) || metadata.Artifact.SHA256 != archiveDigest || !metadata.Artifact.ChecksumVerified || metadata.BinarySHA256 != binaryDigest || metadata.Counts.Failed != 0 || metadata.Counts.Skipped != 0 || metadata.Counts.Missing != 0 || len(metadata.UnexpectedSkips) != 0 || len(metadata.ContractRecords) == 0 {
		return errors.New("E2E metadata does not prove the exact artifact/source/run/attempt contract")
	}
	return nil
}

func verifiedEvidenceDigest(metadata reportMetadata, directory, name string) (string, error) {
	expected := metadata.EvidenceSHA256[name]
	if !digestPattern.MatchString(expected) {
		return "", fmt.Errorf("metadata lacks a valid digest for %s", name)
	}
	actual, err := fileSHA256(filepath.Join(directory, name))
	if err != nil {
		return "", err
	}
	if actual != expected {
		return "", fmt.Errorf("%s digest differs from E2E metadata", name)
	}
	return actual, nil
}

func validatePlatformAgainstContract(evidence PlatformEvidence, platform releasecontract.Platform) error {
	if evidence.SchemaID != PlatformSchemaID || evidence.PlatformID != platform.ID || evidence.GOOS != platform.GOOS || evidence.GOARCH != platform.GOARCH || evidence.ArtifactName != "env-vault-release-"+platform.ID+"-attempt-"+strconv.Itoa(evidence.RunAttempt) || evidence.E2EArtifact != "env-vault-e2e-candidate-"+platform.ID+"-attempt-"+strconv.Itoa(evidence.RunAttempt) || evidence.Archive.Name != platform.Archive || evidence.Checksum.Name != platform.Checksum || !digestPattern.MatchString(evidence.Archive.SHA256) || !digestPattern.MatchString(evidence.Checksum.SHA256) || !digestPattern.MatchString(evidence.BinarySHA256) || !digestPattern.MatchString(evidence.Metadata.SHA256) || evidence.LiteralVersion.Flag != "pass" || evidence.LiteralVersion.Command != "pass" || evidence.LiteralVersion.JSON != "pass" || !digestPattern.MatchString(evidence.Contracts.SHA256) || evidence.Contracts.Count <= 0 || !digestPattern.MatchString(evidence.Coverage.SHA256) || evidence.Coverage.FloorPercent <= 0 || evidence.Coverage.StatementPercent < evidence.Coverage.FloorPercent || evidence.Coverage.CriticalTotal <= 0 || evidence.Coverage.CriticalCovered != evidence.Coverage.CriticalTotal || !digestPattern.MatchString(evidence.Leak.SHA256) || evidence.Leak.Status != "pass" || evidence.Leak.Detected || evidence.Leak.Occurrences != 0 || evidence.Leak.RegistryRecords <= 0 || evidence.Result != "pass" {
		return fmt.Errorf("platform %q evidence does not satisfy the release contract", platform.ID)
	}
	return nil
}

func passingSourceQuality() SourceQuality {
	return SourceQuality{Module: "pass", Test: "pass", Vet: "pass", Smoke: "pass", Race: "pass", Licenses: map[string]string{"linux": "pass", "darwin": "pass", "windows": "pass"}}
}

func verifySourceQuality(quality SourceQuality) error {
	if quality.Module != "pass" || quality.Test != "pass" || quality.Vet != "pass" || quality.Smoke != "pass" || quality.Race != "pass" || len(quality.Licenses) != 3 || quality.Licenses["linux"] != "pass" || quality.Licenses["darwin"] != "pass" || quality.Licenses["windows"] != "pass" {
		return errors.New("source-quality evidence is incomplete")
	}
	return nil
}

func workflowByID(contract releasecontract.Contract, id string) (releasecontract.Workflow, error) {
	for _, workflow := range contract.Workflows {
		if workflow.ID == id {
			return workflow, nil
		}
	}
	return releasecontract.Workflow{}, fmt.Errorf("workflow %q is missing from the release contract", id)
}

func manifestDigest(manifest Manifest) (string, error) {
	manifest.ManifestSHA256 = ""
	data, err := json.Marshal(manifest)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func fileSHA256(filename string) (string, error) {
	file, err := os.Open(filename)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", filename, err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("hash %s: %w", filename, err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func uniqueFile(root, base string) (string, error) {
	var matches []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && entry.Name() == base {
			matches = append(matches, path)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(matches)
	if len(matches) != 1 {
		return "", fmt.Errorf("found %d files named %s under %s, want exactly one", len(matches), base, root)
	}
	return matches[0], nil
}

func verifyDownloadedFile(root string, expected FileDigest) error {
	filename, err := uniqueFile(root, expected.Name)
	if err != nil {
		return err
	}
	digest, err := fileSHA256(filename)
	if err != nil {
		return err
	}
	if digest != expected.SHA256 {
		return fmt.Errorf("downloaded %s digest mismatch", expected.Name)
	}
	return nil
}

func decodeFile(filename string, value any) error {
	file, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer file.Close()
	return json.NewDecoder(file).Decode(value)
}

func decodeFileStrict(filename string, value any) error {
	file, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("JSON document contains trailing data")
	}
	return nil
}
