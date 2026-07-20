package actionsartifact

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestAssembleSnapshotReplaysRawTreeAndAttemptIntervals(t *testing.T) {
	policy := loadCanonicalPolicy(t)
	want := validClassifierSnapshot(t, policy)
	root := writeRawCollectionFixture(t, want)
	got, err := AssembleSnapshot(root, policy)
	if err != nil {
		t.Fatal(err)
	}
	want.Repositories[0].Artifacts = got.Repositories[0].Artifacts
	want.Repositories[0].Runs = got.Repositories[0].Runs
	if !reflect.DeepEqual(got, want) {
		wantJSON, _ := json.Marshal(want)
		gotJSON, _ := json.Marshal(got)
		t.Fatalf("assembled snapshot drift\nwant=%s\n got=%s", wantJSON, gotJSON)
	}
}

func TestAssembleSnapshotAcceptsExactAttemptCreatedAtTwoSecondServerSkew(t *testing.T) {
	policy := loadCanonicalPolicy(t)
	want := validClassifierSnapshot(t, policy)
	root := writeRawCollectionFixture(t, want)
	filename := filepath.Join(root, "repository-001", "attempts", attemptFilename(100, 1))
	var raw map[string]any
	if err := json.Unmarshal(readFixtureFile(t, filename), &raw); err != nil {
		t.Fatal(err)
	}
	const skewedCreatedAt = "2026-01-01T00:01:02Z"
	raw["created_at"] = skewedCreatedAt
	writeJSONFixture(t, filename, raw)

	got, err := AssembleSnapshot(root, policy)
	if err != nil {
		t.Fatal(err)
	}
	want.Attempts[0].CreatedAt = skewedCreatedAt
	want.Repositories[0].Artifacts = got.Repositories[0].Artifacts
	want.Repositories[0].Runs = got.Repositories[0].Runs
	if !reflect.DeepEqual(got, want) {
		wantJSON, _ := json.Marshal(want)
		gotJSON, _ := json.Marshal(got)
		t.Fatalf("assembled skewed-attempt snapshot drift\nwant=%s\n got=%s", wantJSON, gotJSON)
	}
}

func TestAssembleSnapshotRejectsRawProofAndTreeDrift(t *testing.T) {
	policy := loadCanonicalPolicy(t)
	tests := []struct {
		name    string
		mutate  func(*testing.T, string)
		message string
	}{
		{
			name: "stable page byte drift",
			mutate: func(t *testing.T, root string) {
				filename := filepath.Join(root, "repository-001", "artifacts", "page-001-final.json")
				data := readFixtureFile(t, filename)
				data = append(data, ' ')
				if err := os.WriteFile(filename, data, 0o600); err != nil {
					t.Fatal(err)
				}
			},
			message: "differs byte-for-byte",
		},
		{
			name: "unexpected extra page",
			mutate: func(t *testing.T, root string) {
				writeFixtureFile(t, filepath.Join(root, "repository-001", "runs", "page-002.json"), []byte(`{"total_count":0,"workflow_runs":[]}`))
			},
			message: "want exactly",
		},
		{
			name: "duplicate collection field",
			mutate: func(t *testing.T, root string) {
				filename := filepath.Join(root, "collection.json")
				data := readFixtureFile(t, filename)
				data = []byte(strings.Replace(string(data), `"schema_id":`, `"schema_id":"duplicate","schema_id":`, 1))
				if err := os.WriteFile(filename, data, 0o600); err != nil {
					t.Fatal(err)
				}
			},
			message: "duplicate field",
		},
		{
			name: "attempt identity drift",
			mutate: func(t *testing.T, root string) {
				filename := filepath.Join(root, "repository-001", "attempts", attemptFilename(100, 2))
				data := readFixtureFile(t, filename)
				data = []byte(strings.Replace(string(data), `"run_attempt":2`, `"run_attempt":3`, 1))
				if err := os.WriteFile(filename, data, 0o600); err != nil {
					t.Fatal(err)
				}
			},
			message: "identity mismatch",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := writeRawCollectionFixture(t, validClassifierSnapshot(t, policy))
			test.mutate(t, root)
			_, err := AssembleSnapshot(root, policy)
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("error=%v want substring %q", err, test.message)
			}
		})
	}
}

func writeRawCollectionFixture(t *testing.T, snapshot Snapshot) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "raw")
	repositoryRoot := filepath.Join(root, "repository-001")
	for _, directory := range []string{
		root, repositoryRoot, filepath.Join(repositoryRoot, "artifacts"), filepath.Join(repositoryRoot, "runs"), filepath.Join(repositoryRoot, "attempts"),
	} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	repository := snapshot.Repositories[0]
	writeJSONFixture(t, filepath.Join(repositoryRoot, "repository.json"), map[string]any{"id": repository.RepositoryID, "full_name": repository.Repository, "vendor_extension": true})

	artifactValues := make([]any, 0, len(snapshot.Artifacts))
	for _, artifact := range snapshot.Artifacts {
		artifactValues = append(artifactValues, map[string]any{
			"id": artifact.ArtifactID, "name": artifact.Name, "digest": artifact.Digest, "size_in_bytes": artifact.SizeInBytes,
			"created_at": artifact.CreatedAt, "updated_at": artifact.UpdatedAt, "expires_at": artifact.ExpiresAt, "expired": artifact.Expired,
			"workflow_run": map[string]any{
				"id": artifact.ProducerRunID, "repository_id": repository.RepositoryID, "head_repository_id": artifact.HeadRepositoryID,
				"head_sha": artifact.HeadSHA, "head_branch": artifact.HeadBranch,
			},
		})
	}
	artifactPage := marshalFixtureJSON(t, map[string]any{"total_count": len(artifactValues), "artifacts": artifactValues})
	writeFixtureFile(t, filepath.Join(repositoryRoot, "artifacts", "page-001.json"), artifactPage)
	writeFixtureFile(t, filepath.Join(repositoryRoot, "artifacts", "page-001-final.json"), artifactPage)

	runValues := make([]any, 0, len(snapshot.Runs))
	for _, run := range snapshot.Runs {
		runValues = append(runValues, rawRunValue(run, run.CurrentAttempt, run.RunStartedAt, run.UpdatedAt, repository.RepositoryID))
	}
	runPage := marshalFixtureJSON(t, map[string]any{"total_count": len(runValues), "workflow_runs": runValues})
	writeFixtureFile(t, filepath.Join(repositoryRoot, "runs", "page-001.json"), runPage)
	writeFixtureFile(t, filepath.Join(repositoryRoot, "runs", "page-001-final.json"), runPage)

	collectedAttempts := make([]CollectedAttemptDocument, 0, len(snapshot.Attempts))
	runs := make(map[int64]SnapshotRun, len(snapshot.Runs))
	for _, run := range snapshot.Runs {
		runs[run.RunID] = run
	}
	for _, attempt := range snapshot.Attempts {
		collectedAttempts = append(collectedAttempts, CollectedAttemptDocument{RunID: attempt.RunID, RunAttempt: attempt.RunAttempt})
		run := runs[attempt.RunID]
		writeJSONFixture(t, filepath.Join(repositoryRoot, "attempts", attemptFilename(attempt.RunID, attempt.RunAttempt)), rawRunValue(run, attempt.RunAttempt, attempt.RunStartedAt, attempt.UpdatedAt, repository.RepositoryID))
	}
	artifactHash := sha256.Sum256(artifactPage)
	runHash := sha256.Sum256(runPage)
	collection := RawCollection{
		SchemaID: CollectionSchemaID, SchemaVersion: CollectionSchemaVersion,
		ObservedStartedAt: snapshot.ObservedStartedAt, ObservedFinishedAt: snapshot.ObservedFinishedAt,
		Repositories: []RawCollectionRepository{{
			Repository: repository.Repository, RepositoryID: repository.RepositoryID, Directory: "repository-001",
			Artifacts:        PaginationProof{TotalCount: len(artifactValues), PageCount: 1, PageItemCounts: []int{len(artifactValues)}, PageSHA256: []string{hex.EncodeToString(artifactHash[:])}, FinalPageSHA256: []string{hex.EncodeToString(artifactHash[:])}},
			Runs:             PaginationProof{TotalCount: len(runValues), PageCount: 1, PageItemCounts: []int{len(runValues)}, PageSHA256: []string{hex.EncodeToString(runHash[:])}, FinalPageSHA256: []string{hex.EncodeToString(runHash[:])}},
			AttemptDocuments: collectedAttempts,
		}},
	}
	writeJSONFixture(t, filepath.Join(root, "collection.json"), collection)
	return root
}

func rawRunValue(run SnapshotRun, attempt int, started, updated string, repositoryID int64) map[string]any {
	return map[string]any{
		"id": run.RunID, "run_attempt": attempt, "path": run.WorkflowPath, "head_sha": run.HeadSHA, "head_branch": run.HeadBranch,
		"event": run.Event, "status": run.Status, "conclusion": run.Conclusion, "created_at": run.CreatedAt, "run_started_at": started, "updated_at": updated,
		"repository":      map[string]any{"id": repositoryID, "full_name": run.Repository},
		"head_repository": map[string]any{"id": run.HeadRepositoryID, "full_name": run.HeadRepository},
	}
}

func marshalFixtureJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func writeJSONFixture(t *testing.T, filename string, value any) {
	t.Helper()
	writeFixtureFile(t, filename, marshalFixtureJSON(t, value))
}

func writeFixtureFile(t *testing.T, filename string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filename, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFixtureFile(t *testing.T, filename string) []byte {
	t.Helper()
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
