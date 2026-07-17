// Package releaseevidence collects and validates exact-run release evidence
// and renders its compact, generated Markdown index.
package releaseevidence

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ildarbinanas-design/env-vault/internal/releasecontract"
	"github.com/ildarbinanas-design/env-vault/internal/releasepromotion"
)

const SchemaID = "env-vault.release-evidence.v1"

var (
	shaPattern    = regexp.MustCompile(`^[0-9a-f]{40}$`)
	digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
	codePattern   = regexp.MustCompile(`^[a-z][a-z0-9_.-]*$`)
)

type Evidence struct {
	SchemaID      string         `json:"schema_id"`
	GeneratedAt   string         `json:"generated_at"`
	TaskID        string         `json:"task_id"`
	ObjectiveCode string         `json:"objective_code"`
	Repository    Repository     `json:"repository"`
	Release       ReleaseResult  `json:"release_result"`
	Changes       []string       `json:"change_codes"`
	Checks        []Check        `json:"checks"`
	Guarantees    []Guarantee    `json:"guarantees"`
	WorkflowRuns  []WorkflowRun  `json:"workflow_runs"`
	ResidualRisks []ResidualRisk `json:"residual_risks"`
}

type Repository struct {
	Name      string `json:"name"`
	BeforeSHA string `json:"before_sha"`
	AfterSHA  string `json:"after_sha"`
}

type ReleaseResult struct {
	Status        string                     `json:"status"`
	Version       string                     `json:"version,omitempty"`
	SourceSHA     string                     `json:"source_sha,omitempty"`
	PullRequest   *PullRequest               `json:"pull_request,omitempty"`
	TagSHA        string                     `json:"tag_sha,omitempty"`
	GitHubRelease string                     `json:"github_release"`
	Assets        []Asset                    `json:"assets"`
	Attestations  []Attestation              `json:"attestations"`
	Homebrew      *Homebrew                  `json:"homebrew,omitempty"`
	Promotion     *releasepromotion.Manifest `json:"promotion,omitempty"`
	Publication   *PublicationSnapshot       `json:"publication_snapshot,omitempty"`
}

type PublicationSnapshot struct {
	GitHubRelease PublicationState `json:"github_release"`
	Assets        PublicationState `json:"assets"`
	Attestations  PublicationState `json:"attestations"`
	Homebrew      PublicationState `json:"homebrew"`
}

type PublicationState struct {
	State      string `json:"state"`
	ReasonCode string `json:"reason_code"`
}

type PullRequest struct {
	Number  int    `json:"number"`
	HeadSHA string `json:"head_sha"`
}

type Asset struct {
	Name      string `json:"name"`
	SHA256    string `json:"sha256"`
	SourceSHA string `json:"source_sha"`
}

type Attestation struct {
	Kind           string   `json:"kind"`
	SubjectSHA256s []string `json:"subject_sha256s"`
	SourceSHA      string   `json:"source_sha"`
	Workflow       string   `json:"workflow"`
	RunID          int64    `json:"run_id"`
	RunAttempt     int      `json:"run_attempt"`
}

type Homebrew struct {
	State          string       `json:"state"`
	PullRequest    int          `json:"pull_request"`
	PRHeadSHA      string       `json:"pr_head_sha"`
	MergeSHA       string       `json:"merge_sha"`
	TapVersion     string       `json:"tap_version"`
	PRHeadCI       RunReference `json:"pr_head_ci"`
	PostMergeTapCI RunReference `json:"post_merge_tap_ci"`
}

type RunReference struct {
	RunID      int64  `json:"run_id"`
	RunAttempt int    `json:"run_attempt"`
	HeadSHA    string `json:"head_sha"`
	Conclusion string `json:"conclusion"`
}

type Check struct {
	ID      string `json:"id"`
	Command string `json:"command"`
	Result  string `json:"result"`
	Source  string `json:"source"`
}

type Guarantee struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	Evidence string `json:"evidence"`
}

type WorkflowRun struct {
	Workflow                string           `json:"workflow"`
	Event                   string           `json:"event"`
	RepairMode              string           `json:"repair_mode,omitempty"`
	RepairStateDigest       string           `json:"repair_state_digest,omitempty"`
	RunID                   int64            `json:"run_id"`
	RunAttempt              int              `json:"run_attempt"`
	HeadSHA                 string           `json:"head_sha"`
	Conclusion              string           `json:"conclusion"`
	QueueSeconds            int64            `json:"queue_seconds"`
	WallSeconds             int64            `json:"wall_seconds"`
	JobCount                int              `json:"job_count"`
	ExecutedJobCount        int              `json:"executed_job_count"`
	AttemptRunnerSeconds    int64            `json:"attempt_runner_seconds"`
	AggregateRunnerSeconds  int64            `json:"aggregate_runner_seconds"`
	RetryCount              int              `json:"retry_count"`
	CriticalPathSeconds     int64            `json:"critical_path_seconds"`
	ArtifactTransferSeconds int64            `json:"artifact_transfer_seconds"`
	CacheSeconds            int64            `json:"cache_seconds"`
	TimingComplete          bool             `json:"timing_complete"`
	UnavailableReasonCodes  []string         `json:"unavailable_reason_codes"`
	Attempts                []AttemptMetrics `json:"attempts"`
}

type AttemptMetrics struct {
	Attempt       int   `json:"attempt"`
	JobCount      int   `json:"job_count"`
	RunnerSeconds int64 `json:"runner_seconds"`
}

type ResidualRisk struct {
	Code   string `json:"code"`
	Status string `json:"status"`
}

func Load(filename string) (Evidence, error) {
	return load(filename, nil)
}

func LoadWithContract(filename string, contract releasecontract.Contract) (Evidence, error) {
	return load(filename, &contract)
}

func load(filename string, contract *releasecontract.Contract) (Evidence, error) {
	file, err := os.Open(filename)
	if err != nil {
		return Evidence{}, err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var evidence Evidence
	if err := decoder.Decode(&evidence); err != nil {
		return Evidence{}, err
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Evidence{}, errors.New("evidence contains trailing JSON data")
	}
	if contract == nil {
		if err := evidence.Validate(); err != nil {
			return Evidence{}, err
		}
	} else if err := evidence.Validate(*contract); err != nil {
		return Evidence{}, err
	}
	return evidence, nil
}

func (e Evidence) Validate(contracts ...releasecontract.Contract) error {
	var contract *releasecontract.Contract
	if len(contracts) > 1 {
		return errors.New("at most one release contract may be supplied")
	}
	if len(contracts) == 1 {
		contract = &contracts[0]
		if err := contract.Validate(); err != nil {
			return fmt.Errorf("release contract: %w", err)
		}
	}
	var problems []string
	add := func(format string, values ...any) { problems = append(problems, fmt.Sprintf(format, values...)) }
	if e.SchemaID != SchemaID {
		add("schema_id must be %s", SchemaID)
	}
	if _, err := time.Parse(time.RFC3339, e.GeneratedAt); err != nil {
		add("generated_at must be RFC3339")
	}
	if !codePattern.MatchString(e.TaskID) || !codePattern.MatchString(e.ObjectiveCode) {
		add("task_id and objective_code must be stable codes")
	}
	if !validRepository(e.Repository.Name) || !validSHA(e.Repository.BeforeSHA) || !validSHA(e.Repository.AfterSHA) {
		add("repository identity or before/after SHA is invalid")
	}
	if err := validateRelease(e.Release, contract, e.Repository.Name); err != nil {
		add("release_result: %v", err)
	}
	if err := validateCodes(e.Changes, "change"); err != nil {
		add("%v", err)
	}
	seenChecks := map[string]bool{}
	for _, check := range e.Checks {
		if !codePattern.MatchString(check.ID) || seenChecks[check.ID] || strings.TrimSpace(check.Command) == "" || !oneOf(check.Result, "pass", "fail", "unknown", "not_run") || !oneOf(check.Source, "local", "github_api", "artifact") {
			add("check %q is invalid or duplicated", check.ID)
		}
		seenChecks[check.ID] = true
	}
	seenGuarantees := map[string]bool{}
	for _, guarantee := range e.Guarantees {
		if !codePattern.MatchString(guarantee.ID) || seenGuarantees[guarantee.ID] || !oneOf(guarantee.Status, "preserved", "improved", "unknown", "not_applicable") || !codePattern.MatchString(guarantee.Evidence) {
			add("guarantee %q is invalid or duplicated", guarantee.ID)
		}
		seenGuarantees[guarantee.ID] = true
	}
	for _, run := range e.WorkflowRuns {
		if !codePattern.MatchString(run.Workflow) || !validWorkflowRunProtocol(run) || run.RunID <= 0 || run.RunAttempt <= 0 || !validSHA(run.HeadSHA) || !oneOf(run.Conclusion, "success", "failure", "cancelled", "in_progress") || run.QueueSeconds < 0 || run.WallSeconds < 0 || run.JobCount <= 0 || run.ExecutedJobCount < run.JobCount || run.AttemptRunnerSeconds < 0 || run.AggregateRunnerSeconds < run.AttemptRunnerSeconds || run.RetryCount < 0 || run.CriticalPathSeconds < 0 || run.ArtifactTransferSeconds < 0 || run.CacheSeconds < 0 || validateReasonCodes(run.UnavailableReasonCodes) != nil || (run.TimingComplete && len(run.UnavailableReasonCodes) != 0) || validateAttemptMetrics(run) != nil {
			add("workflow run %d/%d is invalid", run.RunID, run.RunAttempt)
		}
	}
	seenRisks := map[string]bool{}
	for _, risk := range e.ResidualRisks {
		if !codePattern.MatchString(risk.Code) || seenRisks[risk.Code] || !oneOf(risk.Status, "open", "accepted", "mitigated", "blocked") {
			add("residual risk %q is invalid or duplicated", risk.Code)
		}
		seenRisks[risk.Code] = true
	}
	if len(e.Checks) == 0 || len(e.Guarantees) == 0 {
		add("checks and guarantees must be non-empty")
	}
	if len(problems) != 0 {
		sort.Strings(problems)
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func validWorkflowRunProtocol(run WorkflowRun) bool {
	switch run.Event {
	case "push":
		return run.RepairStateDigest == "" && (run.RepairMode == "" || run.RepairMode == "none")
	case "workflow_dispatch":
		return oneOf(run.RepairMode, "release-assets", "homebrew", "health") && validDigest(run.RepairStateDigest)
	default:
		return false
	}
}

func validateAttemptMetrics(run WorkflowRun) error {
	if run.RunAttempt <= 0 || len(run.Attempts) == 0 || len(run.Attempts) != run.RunAttempt {
		return errors.New("attempt metrics count does not match run attempt")
	}
	totalJobs, totalSeconds := 0, int64(0)
	for index, attempt := range run.Attempts {
		if attempt.Attempt != index+1 || attempt.JobCount <= 0 || attempt.RunnerSeconds < 0 {
			return errors.New("attempt metrics are incomplete or non-canonical")
		}
		totalJobs += attempt.JobCount
		totalSeconds += attempt.RunnerSeconds
	}
	if totalJobs != run.ExecutedJobCount || totalSeconds != run.AggregateRunnerSeconds || run.Attempts[len(run.Attempts)-1].JobCount != run.JobCount || run.Attempts[len(run.Attempts)-1].RunnerSeconds != run.AttemptRunnerSeconds {
		return errors.New("attempt metrics totals do not match workflow totals")
	}
	return nil
}

func validateReasonCodes(values []string) error {
	seen := map[string]bool{}
	for _, value := range values {
		if !codePattern.MatchString(value) || seen[value] {
			return errors.New("invalid or duplicated unavailable reason code")
		}
		seen[value] = true
	}
	return nil
}

func Index(directory string) ([]byte, error) {
	return index(directory, nil)
}

func IndexWithContract(directory string, contract releasecontract.Contract) ([]byte, error) {
	return index(directory, &contract)
}

type evidenceIndexItem struct {
	filename string
	evidence Evidence
}

func index(directory string, contract *releasecontract.Contract) ([]byte, error) {
	items, err := loadIndexItems(directory, contract, "")
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, errors.New("no *.evidence.json files found")
	}
	return renderIndex(items), nil
}

func indexWithEvidence(directory, filename string, evidence Evidence, contract *releasecontract.Contract) ([]byte, error) {
	if filepath.Base(filename) != filename || !strings.HasSuffix(filename, ".evidence.json") {
		return nil, errors.New("generated evidence filename is invalid")
	}
	if contract == nil {
		if err := evidence.Validate(); err != nil {
			return nil, err
		}
	} else if err := evidence.Validate(*contract); err != nil {
		return nil, err
	}
	items, err := loadIndexItems(directory, contract, filename)
	if err != nil {
		return nil, err
	}
	items = append(items, evidenceIndexItem{filename: filename, evidence: evidence})
	return renderIndex(items), nil
}

func loadIndexItems(directory string, contract *releasecontract.Contract, excludedFilename string) ([]evidenceIndexItem, error) {
	entries, err := filepath.Glob(filepath.Join(directory, "*.evidence.json"))
	if err != nil {
		return nil, err
	}
	items := make([]evidenceIndexItem, 0, len(entries))
	for _, filename := range entries {
		if filepath.Base(filename) == excludedFilename {
			continue
		}
		var evidence Evidence
		if contract == nil {
			evidence, err = Load(filename)
		} else {
			evidence, err = LoadWithContract(filename, *contract)
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", filename, err)
		}
		items = append(items, evidenceIndexItem{filename: filepath.Base(filename), evidence: evidence})
	}
	return items, nil
}

func renderIndex(items []evidenceIndexItem) []byte {
	sort.Slice(items, func(i, j int) bool {
		if items[i].evidence.GeneratedAt == items[j].evidence.GeneratedAt {
			return items[i].filename < items[j].filename
		}
		return items[i].evidence.GeneratedAt > items[j].evidence.GeneratedAt
	})
	var output bytes.Buffer
	output.WriteString("# Machine evidence index\n\n")
	output.WriteString("Generated deterministically by `go run ./cmd/release-evidence index`. JSON files are authoritative.\n\n")
	output.WriteString("| Evidence | Generated UTC | Task | Before | After | Release | Runs | Executed jobs | Runner seconds | Checks | Guarantees |\n")
	output.WriteString("|---|---|---|---|---|---|---:|---:|---:|---:|---:|\n")
	for _, item := range items {
		executedJobs, runnerSeconds := 0, int64(0)
		for _, run := range item.evidence.WorkflowRuns {
			executedJobs += run.ExecutedJobCount
			runnerSeconds += run.AggregateRunnerSeconds
		}
		fmt.Fprintf(&output, "| [%s](%s) | `%s` | `%s` | `%s` | `%s` | `%s` | %d | %d | %d | %d | %d |\n",
			item.filename, item.filename, item.evidence.GeneratedAt, item.evidence.TaskID,
			shortSHA(item.evidence.Repository.BeforeSHA), shortSHA(item.evidence.Repository.AfterSHA),
			item.evidence.Release.Status, len(item.evidence.WorkflowRuns), executedJobs, runnerSeconds, len(item.evidence.Checks), len(item.evidence.Guarantees))
	}
	return output.Bytes()
}

func validateRelease(release ReleaseResult, contract *releasecontract.Contract, repository string) error {
	if !oneOf(release.Status, "implementation_only", "pending_authorization", "published", "failed") || !oneOf(release.GitHubRelease, "absent", "present", "not_checked") {
		return errors.New("status or GitHub Release state is invalid")
	}
	if release.Version != "" && !releasecontract.IsVersion(release.Version) {
		return errors.New("version is invalid")
	}
	if release.SourceSHA != "" && !validSHA(release.SourceSHA) || release.TagSHA != "" && !validSHA(release.TagSHA) {
		return errors.New("source or tag SHA is invalid")
	}
	if release.PullRequest != nil && (release.PullRequest.Number <= 0 || !validSHA(release.PullRequest.HeadSHA)) {
		return errors.New("pull request tuple is invalid")
	}
	if release.Publication != nil {
		for _, state := range []PublicationState{release.Publication.GitHubRelease, release.Publication.Assets, release.Publication.Attestations, release.Publication.Homebrew} {
			if !oneOf(state.State, "present", "absent", "incomplete", "unknown") || !codePattern.MatchString(state.ReasonCode) {
				return errors.New("publication snapshot state or reason code is invalid")
			}
		}
	}
	assetNames := map[string]bool{}
	for _, asset := range release.Assets {
		if filepath.Base(asset.Name) != asset.Name || asset.Name == "" || assetNames[asset.Name] || !validDigest(asset.SHA256) || !validSHA(asset.SourceSHA) {
			return fmt.Errorf("asset %q is invalid or duplicated", asset.Name)
		}
		assetNames[asset.Name] = true
	}
	attestationSubjects := map[string]map[string]bool{"provenance": {}, "sbom": {}}
	for _, attestation := range release.Attestations {
		if !oneOf(attestation.Kind, "provenance", "sbom") || len(attestation.SubjectSHA256s) == 0 || !validSHA(attestation.SourceSHA) || attestation.Workflow == "" || attestation.RunID <= 0 || attestation.RunAttempt <= 0 {
			return errors.New("attestation identity is invalid")
		}
		for _, subject := range attestation.SubjectSHA256s {
			if !validDigest(subject) || attestationSubjects[attestation.Kind][subject] {
				return errors.New("attestation subject is invalid or duplicated")
			}
			attestationSubjects[attestation.Kind][subject] = true
		}
	}
	if release.Status == "published" {
		if !releasecontract.IsVersion(release.Version) || !validSHA(release.SourceSHA) || release.TagSHA != release.SourceSHA || release.GitHubRelease != "present" || len(release.Assets) != 10 || release.Homebrew == nil || release.Promotion == nil {
			return errors.New("published release lacks exact source/tag, ten assets, two attestations, or Homebrew evidence")
		}
		if release.Publication != nil && (release.Publication.GitHubRelease.State != "present" || release.Publication.Assets.State != "present" || release.Publication.Attestations.State != "present" || release.Publication.Homebrew.State != "present") {
			return errors.New("published release publication snapshot is incomplete")
		}
		archives, err := releaseArchiveDigests(release.Assets, contract)
		if err != nil {
			return err
		}
		for _, asset := range release.Assets {
			if asset.SourceSHA != release.SourceSHA {
				return fmt.Errorf("asset %q is not bound to the exact release source", asset.Name)
			}
		}
		for name, digest := range archives {
			if !attestationSubjects["provenance"][digest] || !attestationSubjects["sbom"][digest] {
				return fmt.Errorf("archive %q lacks provenance or SBOM attestation evidence", name)
			}
		}
		for _, attestation := range release.Attestations {
			if attestation.SourceSHA != release.SourceSHA {
				return errors.New("attestation is not bound to the exact release source")
			}
		}
		if err := validateHomebrew(*release.Homebrew, release.Version); err != nil {
			return err
		}
		if err := validatePromotion(*release.Promotion, release, repository, contract); err != nil {
			return err
		}
	}
	if release.Status == "failed" && release.Publication == nil {
		return errors.New("failed release lacks an explicit partial publication snapshot")
	}
	return nil
}

func validatePromotion(manifest releasepromotion.Manifest, release ReleaseResult, repository string, contract *releasecontract.Contract) error {
	if contract == nil {
		return errors.New("published promotion evidence requires the release contract")
	}
	if manifest.SchemaID != releasepromotion.ManifestSchemaID || manifest.SchemaVersion != 1 || manifest.Result != "pass" || manifest.SourceSHA != release.SourceSHA || manifest.ReleaseVersion != release.Version || manifest.Repository != repository || manifest.Workflow.ID != "ci" || manifest.Workflow.Event != "push" || manifest.Workflow.HeadSHA != release.SourceSHA || manifest.Workflow.RunID <= 0 || manifest.Workflow.RunAttempt <= 0 || manifest.ContractSchema != releasecontract.SchemaID || !validDigest(manifest.ContractSHA256) || !validDigest(manifest.SuiteHash) || !validDigest(manifest.ManifestSHA256) {
		return errors.New("promotion manifest does not bind the exact release source/version/CI tuple")
	}
	if _, err := time.Parse(time.RFC3339, manifest.CreatedAt); err != nil {
		return errors.New("promotion manifest creation timestamp is invalid")
	}
	ci, ok := workflowForContract(*contract, "ci")
	if !ok || manifest.Workflow.Name != ci.Name || manifest.Workflow.File != ci.File || len(manifest.Platforms) != len(contract.Platforms) {
		return errors.New("promotion workflow or platform matrix differs from the release contract")
	}
	if manifest.SourceQuality.Module != "pass" || manifest.SourceQuality.Test != "pass" || manifest.SourceQuality.Vet != "pass" || manifest.SourceQuality.Smoke != "pass" || manifest.SourceQuality.Race != "pass" || len(manifest.SourceQuality.Licenses) != 3 || manifest.SourceQuality.Licenses["linux"] != "pass" || manifest.SourceQuality.Licenses["darwin"] != "pass" || manifest.SourceQuality.Licenses["windows"] != "pass" {
		return errors.New("promotion source-quality gates are incomplete")
	}
	assets := map[string]string{}
	for _, asset := range release.Assets {
		assets[asset.Name] = asset.SHA256
	}
	for index, platform := range contract.Platforms {
		evidence := manifest.Platforms[index]
		if evidence.SchemaID != releasepromotion.PlatformSchemaID || evidence.PlatformID != platform.ID || evidence.GOOS != platform.GOOS || evidence.GOARCH != platform.GOARCH || evidence.SourceSHA != release.SourceSHA || evidence.ReleaseVersion != release.Version || evidence.Repository != repository || evidence.RunID != manifest.Workflow.RunID || evidence.RunAttempt != manifest.Workflow.RunAttempt || evidence.SuiteHash != manifest.SuiteHash || evidence.ArtifactName != "env-vault-release-"+platform.ID+"-attempt-"+strconv.Itoa(evidence.RunAttempt) || evidence.E2EArtifact != "env-vault-e2e-candidate-"+platform.ID+"-attempt-"+strconv.Itoa(evidence.RunAttempt) || evidence.Archive.Name != platform.Archive || evidence.Checksum.Name != platform.Checksum || evidence.Archive.SHA256 != assets[platform.Archive] || evidence.Checksum.SHA256 != assets[platform.Checksum] || !validDigest(evidence.BinarySHA256) || !validDigest(evidence.Metadata.SHA256) || evidence.LiteralVersion.Flag != "pass" || evidence.LiteralVersion.Command != "pass" || evidence.LiteralVersion.JSON != "pass" || evidence.Contracts.Count <= 0 || !validDigest(evidence.Contracts.SHA256) || !validDigest(evidence.Coverage.SHA256) || evidence.Coverage.FloorPercent <= 0 || evidence.Coverage.StatementPercent < evidence.Coverage.FloorPercent || evidence.Coverage.CriticalTotal <= 0 || evidence.Coverage.CriticalCovered != evidence.Coverage.CriticalTotal || !validDigest(evidence.Leak.SHA256) || evidence.Leak.Status != "pass" || evidence.Leak.Detected || evidence.Leak.Occurrences != 0 || evidence.Leak.RegistryRecords <= 0 || strings.TrimSpace(evidence.GoVersion) == "" || strings.TrimSpace(evidence.BinaryGo) == "" || strings.TrimSpace(evidence.Gotestsum) == "" || evidence.Result != "pass" {
			return fmt.Errorf("promotion platform %q lacks literal-version, contract, coverage, leak, or exact artifact evidence", platform.ID)
		}
	}
	copy := manifest
	copy.ManifestSHA256 = ""
	data, err := json.Marshal(copy)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) != manifest.ManifestSHA256 {
		return errors.New("promotion manifest digest is invalid")
	}
	return nil
}

func workflowForContract(contract releasecontract.Contract, id string) (releasecontract.Workflow, bool) {
	for _, workflow := range contract.Workflows {
		if workflow.ID == id {
			return workflow, true
		}
	}
	return releasecontract.Workflow{}, false
}

func releaseArchiveDigests(assets []Asset, contract *releasecontract.Contract) (map[string]string, error) {
	byName := make(map[string]string, len(assets))
	for _, asset := range assets {
		byName[asset.Name] = asset.SHA256
	}
	archives := make(map[string]string, 5)
	if contract != nil {
		if len(byName) != len(contract.Assets) {
			return nil, errors.New("release assets do not match the release contract")
		}
		for _, name := range contract.Assets {
			if _, ok := byName[name]; !ok {
				return nil, fmt.Errorf("contract asset %q is missing", name)
			}
		}
		for _, platform := range contract.Platforms {
			archives[platform.Archive] = byName[platform.Archive]
		}
		return archives, nil
	}
	for name, digest := range byName {
		if strings.HasSuffix(name, ".sha256") {
			archive := strings.TrimSuffix(name, ".sha256")
			if _, ok := byName[archive]; !ok {
				return nil, fmt.Errorf("checksum asset %q has no archive", name)
			}
			continue
		}
		if _, ok := byName[name+".sha256"]; !ok {
			return nil, fmt.Errorf("archive asset %q has no checksum", name)
		}
		archives[name] = digest
	}
	if len(archives) != 5 {
		return nil, fmt.Errorf("release must contain five archive/checksum pairs, observed %d", len(archives))
	}
	return archives, nil
}

func validateHomebrew(homebrew Homebrew, version string) error {
	if homebrew.State != "complete" || homebrew.PullRequest <= 0 || !validSHA(homebrew.PRHeadSHA) || !validSHA(homebrew.MergeSHA) || homebrew.TapVersion != strings.TrimPrefix(version, "v") {
		return errors.New("Homebrew state or exact PR/commit/version tuple is invalid")
	}
	for _, run := range []RunReference{homebrew.PRHeadCI, homebrew.PostMergeTapCI} {
		if run.RunID <= 0 || run.RunAttempt <= 0 || !validSHA(run.HeadSHA) || run.Conclusion != "success" {
			return errors.New("Homebrew PR-head or post-merge CI evidence is invalid")
		}
	}
	if homebrew.PRHeadCI.HeadSHA != homebrew.PRHeadSHA || homebrew.PostMergeTapCI.HeadSHA != homebrew.MergeSHA {
		return errors.New("Homebrew CI does not bind the exact PR head and merge commit")
	}
	return nil
}

func validSHA(value string) bool {
	if !shaPattern.MatchString(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validDigest(value string) bool { return digestPattern.MatchString(value) }

func validRepository(value string) bool {
	parts := strings.Split(value, "/")
	return len(parts) == 2 && codePattern.MatchString(parts[0]) && codePattern.MatchString(parts[1])
}

func validateCodes(values []string, kind string) error {
	if len(values) == 0 {
		return fmt.Errorf("%s codes are empty", kind)
	}
	seen := map[string]bool{}
	for _, value := range values {
		if !codePattern.MatchString(value) || seen[value] {
			return fmt.Errorf("%s code %q is invalid or duplicated", kind, value)
		}
		seen[value] = true
	}
	return nil
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func shortSHA(value string) string {
	if len(value) < 12 {
		return value
	}
	return value[:12]
}
