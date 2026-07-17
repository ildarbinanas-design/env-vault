package releaseevidence

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeGitRunner struct{ responses map[string][]byte }

func (runner *fakeGitRunner) RunGit(_ context.Context, args ...string) ([]byte, error) {
	data, ok := runner.responses[strings.Join(args, " ")]
	if !ok {
		return nil, fmt.Errorf("unexpected git command %q", strings.Join(args, " "))
	}
	return data, nil
}

func TestNormalizeImplementationRecordBindsCleanCandidateAndRendersIndex(t *testing.T) {
	root, recordPath, recordData, beforeSHA := implementationRecordFixture(t)
	runner := implementationGitFixture(root, recordData, beforeSHA, testSHA, testSHA)
	artifacts, err := normalizeImplementationRecord(context.Background(), recordPath, testSHA, testReleaseContract(t), runner)
	if err != nil {
		t.Fatal(err)
	}
	if artifacts.EvidencePath != filepath.Join(root, "evidence", "release-pipeline-determinism.evidence.json") || !strings.Contains(string(artifacts.Index), "release-pipeline-determinism.evidence.json") {
		t.Fatalf("artifacts=%+v", artifacts)
	}
	var evidence Evidence
	if err := json.Unmarshal(artifacts.EvidenceJSON, &evidence); err != nil {
		t.Fatal(err)
	}
	if evidence.Repository.BeforeSHA != beforeSHA || evidence.Repository.AfterSHA != testSHA || evidence.GeneratedAt != "2026-07-16T08:00:00Z" || evidence.Release.Status != "implementation_only" {
		t.Fatalf("evidence=%+v", evidence)
	}
	runner.responses["status --porcelain=v1 --untracked-files=all"] = []byte("?? untracked\n")
	if _, err := normalizeImplementationRecord(context.Background(), recordPath, testSHA, testReleaseContract(t), runner); err == nil {
		t.Fatal("dirty worktree was accepted")
	}
}

func TestNormalizeImplementationRecordAllowsOnlyExactEvidenceChild(t *testing.T) {
	root, recordPath, recordData, beforeSHA := implementationRecordFixture(t)
	childSHA := strings.Repeat("c", 40)
	runner := implementationGitFixture(root, recordData, beforeSHA, testSHA, childSHA)
	runner.responses["rev-list --parents -n 1 "+childSHA] = []byte(childSHA + " " + testSHA + "\n")
	runner.responses["diff --name-only --no-renames "+testSHA+" "+childSHA] = []byte("evidence/README.md\nevidence/release-pipeline-determinism.evidence.json\n")
	if _, err := normalizeImplementationRecord(context.Background(), recordPath, testSHA, testReleaseContract(t), runner); err != nil {
		t.Fatal(err)
	}
	runner.responses["diff --name-only --no-renames "+testSHA+" "+childSHA] = []byte("AGENTS.md\nevidence/README.md\nevidence/release-pipeline-determinism.evidence.json\n")
	if _, err := normalizeImplementationRecord(context.Background(), recordPath, testSHA, testReleaseContract(t), runner); err == nil {
		t.Fatal("child commit with non-evidence changes was accepted")
	}
}

func TestValidateImplementationTreeTreatsCommandsAsInertData(t *testing.T) {
	root := t.TempDir()
	records := filepath.Join(root, "evidence", "records")
	evidenceDirectory := filepath.Join(root, "evidence")
	if err := os.MkdirAll(records, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(root, "must-not-exist")
	record := ImplementationRecord{
		SchemaID: ImplementationRecordSchemaID, TaskID: "release-pipeline-determinism", ObjectiveCode: "llm-free-release",
		Repository: "ildarbinanas-design/env-vault", BeforeSHA: strings.Repeat("b", 40), Output: "release-pipeline-determinism.evidence.json",
		Changes:       []string{"automatic-machine-evidence"},
		Checks:        []Check{{ID: "inert-command", Command: "touch " + sentinel, Result: "not_run", Source: "local"}},
		Guarantees:    []Guarantee{{ID: "exact-source", Status: "preserved", Evidence: "inert-command"}},
		ResidualRisks: []ResidualRisk{{Code: "required-ci-pending", Status: "open"}},
	}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(records, "release-pipeline-determinism.v1.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateImplementationTree(records, evidenceDirectory, testReleaseContract(t)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatal("implementation validator executed a command from JSON")
	}

	record.Checks[0].Result = "pass"
	record.Checks[0].Source = "github_api"
	data, err = json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(records, "release-pipeline-determinism.v1.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	// A remote claim is schema-valid data, but validation still never executes
	// or fabricates it. The checked-in task record remains not_run until CI is
	// actually observed and deliberately updated.
	if err := ValidateImplementationTree(records, evidenceDirectory, testReleaseContract(t)); err != nil {
		t.Fatal(err)
	}
}

func implementationRecordFixture(t *testing.T) (string, string, []byte, string) {
	t.Helper()
	root := t.TempDir()
	recordPath := filepath.Join(root, "evidence", "records", "release-pipeline-determinism.v1.json")
	if err := os.MkdirAll(filepath.Dir(recordPath), 0o755); err != nil {
		t.Fatal(err)
	}
	beforeSHA := strings.Repeat("b", 40)
	record := ImplementationRecord{
		SchemaID: ImplementationRecordSchemaID, TaskID: "release-pipeline-determinism", ObjectiveCode: "llm-free-release",
		Repository: "ildarbinanas-design/env-vault", BeforeSHA: beforeSHA, Output: "release-pipeline-determinism.evidence.json",
		Changes:       []string{"automatic-machine-evidence"},
		Checks:        []Check{{ID: "unit", Command: "go test ./internal/releaseevidence", Result: "pass", Source: "local"}},
		Guarantees:    []Guarantee{{ID: "exact-source", Status: "preserved", Evidence: "unit"}},
		ResidualRisks: []ResidualRisk{{Code: "required-ci-pending", Status: "open"}},
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(recordPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return root, recordPath, data, beforeSHA
}

func implementationGitFixture(root string, recordData []byte, beforeSHA, candidateSHA, headSHA string) *fakeGitRunner {
	return &fakeGitRunner{responses: map[string][]byte{
		"rev-parse --show-toplevel":                                                       []byte(root + "\n"),
		"status --porcelain=v1 --untracked-files=all":                                     {},
		"rev-parse --verify " + candidateSHA + "^{commit}":                                []byte(candidateSHA + "\n"),
		"merge-base --is-ancestor " + beforeSHA + " " + candidateSHA:                      {},
		"show " + candidateSHA + ":evidence/records/release-pipeline-determinism.v1.json": recordData,
		"rev-parse HEAD":                       []byte(headSHA + "\n"),
		"show -s --format=%cI " + candidateSHA: []byte("2026-07-16T13:00:00+05:00\n"),
	}}
}
