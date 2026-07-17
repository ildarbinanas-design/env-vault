package releaseevidence

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ildarbinanas-design/env-vault/internal/releasecontract"
)

const ImplementationRecordSchemaID = "env-vault.implementation-record.v1"

type ImplementationRecord struct {
	SchemaID      string         `json:"schema_id"`
	TaskID        string         `json:"task_id"`
	ObjectiveCode string         `json:"objective_code"`
	Repository    string         `json:"repository"`
	BeforeSHA     string         `json:"before_sha"`
	Output        string         `json:"output"`
	Changes       []string       `json:"change_codes"`
	Checks        []Check        `json:"checks"`
	Guarantees    []Guarantee    `json:"guarantees"`
	ResidualRisks []ResidualRisk `json:"residual_risks"`
}

type ImplementationArtifacts struct {
	EvidencePath string
	EvidenceJSON []byte
	IndexPath    string
	Index        []byte
}

// ValidateImplementationTree validates checked-in declarations and any
// generated implementation evidence. It deliberately treats command strings as
// inert data and never executes them.
func ValidateImplementationTree(recordsDirectory, evidenceDirectory string, contract releasecontract.Contract) error {
	if err := contract.Validate(); err != nil {
		return err
	}
	recordFiles, err := filepath.Glob(filepath.Join(recordsDirectory, "*.json"))
	if err != nil {
		return err
	}
	if len(recordFiles) == 0 {
		return errors.New("no implementation records found")
	}
	outputs := map[string]ImplementationRecord{}
	for _, filename := range recordFiles {
		data, err := os.ReadFile(filename)
		if err != nil {
			return err
		}
		record, err := decodeImplementationRecord(data)
		if err != nil {
			return fmt.Errorf("%s: %w", filename, err)
		}
		if err := validateImplementationRecordStructure(record, contract); err != nil {
			return fmt.Errorf("%s: %w", filename, err)
		}
		if _, duplicate := outputs[record.Output]; duplicate {
			return fmt.Errorf("implementation evidence output %q is duplicated", record.Output)
		}
		outputs[record.Output] = record
	}
	evidenceFiles, err := filepath.Glob(filepath.Join(evidenceDirectory, "*.evidence.json"))
	if err != nil {
		return err
	}
	for _, filename := range evidenceFiles {
		evidence, err := LoadWithContract(filename, contract)
		if err != nil {
			return fmt.Errorf("%s: %w", filename, err)
		}
		if evidence.Release.Status != "implementation_only" {
			continue
		}
		record, ok := outputs[filepath.Base(filename)]
		if !ok {
			return fmt.Errorf("implementation evidence %q has no checked-in record", filepath.Base(filename))
		}
		if evidence.TaskID != record.TaskID || evidence.ObjectiveCode != record.ObjectiveCode || evidence.Repository.Name != record.Repository || evidence.Repository.BeforeSHA != record.BeforeSHA || !validSHA(evidence.Repository.AfterSHA) {
			return fmt.Errorf("implementation evidence %q differs from its checked-in record identity", filepath.Base(filename))
		}
	}
	return nil
}

type gitCommandRunner interface {
	RunGit(context.Context, ...string) ([]byte, error)
}

type localGitRunner struct{}

func (localGitRunner) RunGit(ctx context.Context, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, "git", args...)
	var stdout bytes.Buffer
	command.Stdout = &stdout
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("git %s failed: %w", args[0], err)
	}
	return stdout.Bytes(), nil
}

func NormalizeImplementationRecord(ctx context.Context, recordPath, candidateSHA string, contract releasecontract.Contract) (ImplementationArtifacts, error) {
	return normalizeImplementationRecord(ctx, recordPath, candidateSHA, contract, localGitRunner{})
}

func normalizeImplementationRecord(ctx context.Context, recordPath, candidateSHA string, contract releasecontract.Contract, git gitCommandRunner) (ImplementationArtifacts, error) {
	if !validSHA(candidateSHA) {
		return ImplementationArtifacts{}, errors.New("candidate SHA must be an exact 40-hex commit")
	}
	recordData, err := os.ReadFile(recordPath)
	if err != nil {
		return ImplementationArtifacts{}, err
	}
	record, err := decodeImplementationRecord(recordData)
	if err != nil {
		return ImplementationArtifacts{}, err
	}
	if err := validateImplementationRecord(record, candidateSHA, contract); err != nil {
		return ImplementationArtifacts{}, err
	}
	rootData, err := git.RunGit(ctx, "rev-parse", "--show-toplevel")
	if err != nil {
		return ImplementationArtifacts{}, err
	}
	root := strings.TrimSpace(string(rootData))
	if !filepath.IsAbs(root) {
		return ImplementationArtifacts{}, errors.New("git repository root is not absolute")
	}
	recordAbsolute, err := filepath.Abs(recordPath)
	if err != nil {
		return ImplementationArtifacts{}, err
	}
	recordRelative, err := filepath.Rel(root, recordAbsolute)
	if err != nil || recordRelative == ".." || strings.HasPrefix(recordRelative, ".."+string(filepath.Separator)) {
		return ImplementationArtifacts{}, errors.New("implementation record is outside the repository")
	}
	recordRelative = filepath.ToSlash(recordRelative)
	outputRelative := filepath.ToSlash(filepath.Join("evidence", record.Output))
	indexRelative := "evidence/README.md"
	if recordRelative == outputRelative || recordRelative == indexRelative {
		return ImplementationArtifacts{}, errors.New("implementation record collides with generated evidence")
	}
	status, err := git.RunGit(ctx, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return ImplementationArtifacts{}, err
	}
	if len(status) != 0 {
		return ImplementationArtifacts{}, errors.New("implementation evidence requires a clean worktree")
	}
	verifiedCandidate, err := git.RunGit(ctx, "rev-parse", "--verify", candidateSHA+"^{commit}")
	if err != nil || strings.TrimSpace(string(verifiedCandidate)) != candidateSHA {
		return ImplementationArtifacts{}, errors.New("candidate SHA is not an exact local commit")
	}
	if _, err := git.RunGit(ctx, "merge-base", "--is-ancestor", record.BeforeSHA, candidateSHA); err != nil || record.BeforeSHA == candidateSHA {
		return ImplementationArtifacts{}, errors.New("before SHA must be a strict ancestor of the candidate commit")
	}
	committedRecord, err := git.RunGit(ctx, "show", candidateSHA+":"+recordRelative)
	if err != nil || !bytes.Equal(committedRecord, recordData) {
		return ImplementationArtifacts{}, errors.New("implementation record is not byte-identical to the candidate commit")
	}
	headData, err := git.RunGit(ctx, "rev-parse", "HEAD")
	if err != nil {
		return ImplementationArtifacts{}, err
	}
	headSHA := strings.TrimSpace(string(headData))
	if headSHA != candidateSHA {
		if err := validateEvidenceOnlyChild(ctx, git, headSHA, candidateSHA, outputRelative, indexRelative); err != nil {
			return ImplementationArtifacts{}, err
		}
	}
	commitTimeData, err := git.RunGit(ctx, "show", "-s", "--format=%cI", candidateSHA)
	if err != nil {
		return ImplementationArtifacts{}, err
	}
	commitTime, err := time.Parse(time.RFC3339, strings.TrimSpace(string(commitTimeData)))
	if err != nil {
		return ImplementationArtifacts{}, errors.New("candidate commit timestamp is invalid")
	}
	evidence := Evidence{
		SchemaID: ImplementationRecordSchemaID,
	}
	// Implementation records normalize into the common authoritative evidence
	// schema; the record schema itself never appears as generated evidence.
	evidence.SchemaID = SchemaID
	evidence.GeneratedAt = commitTime.UTC().Format(time.RFC3339)
	evidence.TaskID = record.TaskID
	evidence.ObjectiveCode = record.ObjectiveCode
	evidence.Repository = Repository{Name: record.Repository, BeforeSHA: record.BeforeSHA, AfterSHA: candidateSHA}
	evidence.Release = ReleaseResult{Status: "implementation_only", GitHubRelease: "not_checked", Assets: []Asset{}, Attestations: []Attestation{}}
	evidence.Changes = append([]string(nil), record.Changes...)
	evidence.Checks = append([]Check(nil), record.Checks...)
	evidence.Guarantees = append([]Guarantee(nil), record.Guarantees...)
	evidence.WorkflowRuns = []WorkflowRun{}
	evidence.ResidualRisks = append([]ResidualRisk(nil), record.ResidualRisks...)
	if err := evidence.Validate(contract); err != nil {
		return ImplementationArtifacts{}, err
	}
	evidenceData, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		return ImplementationArtifacts{}, err
	}
	evidenceData = append(evidenceData, '\n')
	indexData, err := indexWithEvidence(filepath.Join(root, "evidence"), record.Output, evidence, &contract)
	if err != nil {
		return ImplementationArtifacts{}, err
	}
	return ImplementationArtifacts{
		EvidencePath: filepath.Join(root, filepath.FromSlash(outputRelative)), EvidenceJSON: evidenceData,
		IndexPath: filepath.Join(root, filepath.FromSlash(indexRelative)), Index: indexData,
	}, nil
}

func decodeImplementationRecord(data []byte) (ImplementationRecord, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var record ImplementationRecord
	if err := decoder.Decode(&record); err != nil {
		return ImplementationRecord{}, err
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return ImplementationRecord{}, errors.New("implementation record contains trailing JSON data")
	}
	return record, nil
}

func validateImplementationRecord(record ImplementationRecord, candidateSHA string, contract releasecontract.Contract) error {
	if err := validateImplementationRecordStructure(record, contract); err != nil {
		return err
	}
	probe := Evidence{
		SchemaID: SchemaID, GeneratedAt: time.Unix(0, 0).UTC().Format(time.RFC3339), TaskID: record.TaskID, ObjectiveCode: record.ObjectiveCode,
		Repository: Repository{Name: record.Repository, BeforeSHA: record.BeforeSHA, AfterSHA: candidateSHA},
		Release:    ReleaseResult{Status: "implementation_only", GitHubRelease: "not_checked", Assets: []Asset{}, Attestations: []Attestation{}},
		Changes:    record.Changes, Checks: record.Checks, Guarantees: record.Guarantees, WorkflowRuns: []WorkflowRun{}, ResidualRisks: record.ResidualRisks,
	}
	return probe.Validate(contract)
}

func validateImplementationRecordStructure(record ImplementationRecord, contract releasecontract.Contract) error {
	if record.SchemaID != ImplementationRecordSchemaID {
		return errors.New("implementation record schema is invalid")
	}
	if filepath.Base(record.Output) != record.Output || record.Output != record.TaskID+".evidence.json" {
		return errors.New("implementation evidence output must be the task ID plus .evidence.json")
	}
	if len(record.Changes) == 0 || len(record.ResidualRisks) == 0 {
		return errors.New("implementation record must contain changes and residual risks")
	}
	candidateSHA := strings.Repeat("f", 40)
	if record.BeforeSHA == candidateSHA {
		candidateSHA = strings.Repeat("e", 40)
	}
	probe := Evidence{
		SchemaID: SchemaID, GeneratedAt: time.Unix(0, 0).UTC().Format(time.RFC3339), TaskID: record.TaskID, ObjectiveCode: record.ObjectiveCode,
		Repository: Repository{Name: record.Repository, BeforeSHA: record.BeforeSHA, AfterSHA: candidateSHA},
		Release:    ReleaseResult{Status: "implementation_only", GitHubRelease: "not_checked", Assets: []Asset{}, Attestations: []Attestation{}},
		Changes:    record.Changes, Checks: record.Checks, Guarantees: record.Guarantees, WorkflowRuns: []WorkflowRun{}, ResidualRisks: record.ResidualRisks,
	}
	return probe.Validate(contract)
}

func validateEvidenceOnlyChild(ctx context.Context, git gitCommandRunner, headSHA, candidateSHA, outputRelative, indexRelative string) error {
	parentsData, err := git.RunGit(ctx, "rev-list", "--parents", "-n", "1", headSHA)
	if err != nil {
		return err
	}
	parents := strings.Fields(string(parentsData))
	if len(parents) != 2 || parents[0] != headSHA || parents[1] != candidateSHA {
		return errors.New("HEAD is neither the candidate nor its single-parent evidence commit")
	}
	changedData, err := git.RunGit(ctx, "diff", "--name-only", "--no-renames", candidateSHA, headSHA)
	if err != nil {
		return err
	}
	changed := strings.Fields(string(changedData))
	sort.Strings(changed)
	expected := []string{indexRelative, outputRelative}
	sort.Strings(expected)
	if len(changed) != len(expected) || changed[0] != expected[0] || changed[1] != expected[1] {
		return errors.New("candidate child commit contains changes outside generated evidence and index")
	}
	return nil
}
