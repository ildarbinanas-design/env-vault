package actionsartifact

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ildarbinanas-design/env-vault/internal/releasecontract"
)

func TestDeriveLiveDecisionScopeDeterministicKeepAndPositiveDeleteAuthority(t *testing.T) {
	policy := loadCanonicalPolicy(t)
	snapshot := liveFixtureSnapshot(t, policy)
	root, _ := writeLiveDeriveFixture(t, snapshot)
	now := mustTime(t, "2026-01-01T00:25:00Z")

	firstObservation, firstRepair, firstScope, err := DeriveLiveDecisionScope(root, snapshot, policy, now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	secondObservation, secondRepair, secondScope, err := DeriveLiveDecisionScope(root, snapshot, policy, now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(firstObservation, secondObservation) || !reflect.DeepEqual(firstRepair, secondRepair) || !reflect.DeepEqual(firstScope, secondScope) {
		t.Fatal("raw replay did not produce byte-stable typed documents")
	}
	firstBytes, _ := MarshalCanonical(firstScope)
	secondBytes, _ := MarshalCanonical(secondScope)
	if !reflect.DeepEqual(firstBytes, secondBytes) || len(firstObservation.SemanticSHA256) != 64 || len(firstRepair.SemanticSHA256) != 64 {
		t.Fatal("canonical scope or semantic digest is unstable")
	}
	if firstRepair.Closed == nil || !*firstRepair.Closed || firstRepair.ActiveRunIdentities == nil || firstRepair.Identities == nil || len(firstRepair.ActiveRunIdentities) != 0 || len(firstRepair.Identities) != 0 {
		t.Fatalf("repair proof=%+v", firstRepair)
	}
	sourceScope := firstScope.Repositories[0]
	if len(sourceScope.DeleteEligibleIdentities) != 1 || sourceScope.DeleteEligibleIdentities[0].ProducerRunID != 100 {
		t.Fatalf("positive delete authority=%+v", sourceScope.DeleteEligibleIdentities)
	}
	if !reflect.DeepEqual(sourceScope.ImmutableKeepArtifactIDs, []int64{2, 3, 4, 5}) {
		t.Fatalf("immutable keep artifact IDs=%v", sourceScope.ImmutableKeepArtifactIDs)
	}
	manifest, err := Classify(snapshot, firstScope, policy, now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Totals.Delete.Count != 1 || manifest.Records[0].ReasonCode != ReasonDeleteSuperseded || manifest.Records[3].ReasonCode != ReasonKeepStableRelease || manifest.Records[4].ReasonCode != ReasonKeepSystemManaged {
		t.Fatalf("manifest decisions=%+v", manifest.Records)
	}
}

func TestDeriveLiveDecisionScopeRejectsRunSetDriftActiveRunAndFinalProjectionDrift(t *testing.T) {
	policy := loadCanonicalPolicy(t)
	snapshot := liveFixtureSnapshot(t, policy)
	now := mustTime(t, "2026-01-01T00:25:00Z")

	t.Run("extra completed run", func(t *testing.T) {
		root, collection := writeLiveDeriveFixture(t, snapshot)
		runs := append([]SnapshotRun(nil), snapshot.Runs[:5]...)
		extra := runs[0]
		extra.RunID = 999
		runs = append(runs, extra)
		data := liveRunPageJSON(t, runs)
		rewriteLivePair(t, root, "repository-001/runs/page-001.json", data)
		collection.Repositories[0].Runs.TotalCount = len(runs)
		collection.Repositories[0].Runs.PageItemCounts[0] = len(runs)
		collection.Repositories[0].Runs.PageSHA256[0] = digestBytes(data)
		collection.Repositories[0].Runs.FinalPageSHA256[0] = digestBytes(data)
		republishLiveCollection(t, root, &collection)
		_, _, _, err := DeriveLiveDecisionScope(root, snapshot, policy, now, time.Hour)
		if err == nil || !strings.Contains(err.Error(), "run set differs") {
			t.Fatalf("error=%v", err)
		}
	})

	t.Run("active run", func(t *testing.T) {
		root, collection := writeLiveDeriveFixture(t, snapshot)
		runs := append([]SnapshotRun(nil), snapshot.Runs[:5]...)
		runs[1].Status = "in_progress"
		data := liveRunPageJSON(t, runs)
		rewriteLivePair(t, root, "repository-001/runs/page-001.json", data)
		collection.Repositories[0].Runs.PageSHA256[0] = digestBytes(data)
		collection.Repositories[0].Runs.FinalPageSHA256[0] = digestBytes(data)
		republishLiveCollection(t, root, &collection)
		_, _, _, err := DeriveLiveDecisionScope(root, snapshot, policy, now, time.Hour)
		if err == nil || !strings.Contains(err.Error(), "active or incomplete") {
			t.Fatalf("error=%v", err)
		}
	})

	t.Run("default ref branch mismatch", func(t *testing.T) {
		root, collection := writeLiveDeriveFixture(t, snapshot)
		branch := map[string]any{"name": "main", "protected": true, "commit": map[string]any{"sha": strings.Repeat("9", 40)}}
		rewriteLivePair(t, root, "repository-001/default-branch.json", mustJSON(t, branch))
		republishLiveCollection(t, root, &collection)
		_, _, _, err := DeriveLiveDecisionScope(root, snapshot, policy, now, time.Hour)
		if err == nil || !strings.Contains(err.Error(), "ref/branch mismatch") {
			t.Fatalf("error=%v", err)
		}
	})

	t.Run("global final reread drift", func(t *testing.T) {
		root, collection := writeLiveDeriveFixture(t, snapshot)
		final := filepath.Join(root, "repository-001", "default-branch-final.json")
		if err := os.WriteFile(final, append(mustRead(t, final), '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
		republishLiveCollection(t, root, &collection)
		_, _, _, err := DeriveLiveDecisionScope(root, snapshot, policy, now, time.Hour)
		if err == nil || !strings.Contains(err.Error(), "final reread differs") {
			t.Fatalf("error=%v", err)
		}
	})
}

func TestDecisionScopeRejectsReferencedStableSourceDeleteAndOmittedImmutableSet(t *testing.T) {
	policy := loadCanonicalPolicy(t)
	snapshot := liveFixtureSnapshot(t, policy)
	root, _ := writeLiveDeriveFixture(t, snapshot)
	now := mustTime(t, "2026-01-01T00:25:00Z")
	_, _, scope, err := DeriveLiveDecisionScope(root, snapshot, policy, now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	bad := cloneScope(t, scope)
	bad.Repositories[0].DeleteEligibleIdentities = append(bad.Repositories[0].DeleteEligibleIdentities, ExactKeepIdentity{
		ProducerRunID: 400, ProducerRunAttempt: 1, WorkflowPath: ".github/workflows/bootstrap-release-assets.yml", HeadSHA: strings.Repeat("e", 40),
	})
	sort.Slice(bad.Repositories[0].DeleteEligibleIdentities, func(i, j int) bool {
		return compareExactKeepIdentity(bad.Repositories[0].DeleteEligibleIdentities[i], bad.Repositories[0].DeleteEligibleIdentities[j]) < 0
	})
	if err := ValidateDecisionScope(bad, snapshot, now, time.Hour); err == nil || !strings.Contains(err.Error(), "referenced stable-source") {
		t.Fatalf("error=%v", err)
	}
	omitted := cloneScope(t, scope)
	omitted.Repositories[0].ImmutableKeepArtifactIDs = nil
	if err := ValidateDecisionScope(omitted, snapshot, now, time.Hour); err == nil || !strings.Contains(err.Error(), "immutable_keep_artifact_ids must be explicitly present") {
		t.Fatalf("error=%v", err)
	}
}

func TestDeriveLiveDecisionScopeRejectsTagCycleAndReleaseSourceMismatch(t *testing.T) {
	policy := loadCanonicalPolicy(t)
	snapshot := liveFixtureSnapshot(t, policy)
	now := mustTime(t, "2026-01-01T00:25:00Z")

	t.Run("annotated tag cycle", func(t *testing.T) {
		root, collection := writeLiveDeriveFixture(t, snapshot)
		tagSHA := strings.Repeat("8", 40)
		rewriteLivePair(t, root, "repository-001/release/tag-ref.json", mustJSON(t, map[string]any{"ref": "refs/tags/v1.2.3", "object": map[string]any{"type": "tag", "sha": tagSHA}}))
		writeLivePair(t, root, "repository-001/release/tag-object-01.json", mustJSON(t, map[string]any{"sha": tagSHA, "object": map[string]any{"type": "tag", "sha": tagSHA}}))
		collection.Repositories[0].StableRelease.TagObjectDocuments = 1
		republishLiveCollection(t, root, &collection)
		_, _, _, err := DeriveLiveDecisionScope(root, snapshot, policy, now, time.Hour)
		if err == nil || !strings.Contains(err.Error(), "cyclic") {
			t.Fatalf("error=%v", err)
		}
	})

	t.Run("release target source mismatch", func(t *testing.T) {
		root, collection := writeLiveDeriveFixture(t, snapshot)
		var release map[string]any
		if err := json.Unmarshal(mustRead(t, filepath.Join(root, "repository-001", "release", "latest.json")), &release); err != nil {
			t.Fatal(err)
		}
		release["target_commitish"] = strings.Repeat("9", 40)
		data := mustJSON(t, release)
		rewriteLivePair(t, root, "repository-001/release/latest.json", data)
		rewriteLivePair(t, root, "repository-001/release/exact.json", data)
		republishLiveCollection(t, root, &collection)
		_, _, _, err := DeriveLiveDecisionScope(root, snapshot, policy, now, time.Hour)
		if err == nil || !strings.Contains(err.Error(), "target/source mismatch") {
			t.Fatalf("error=%v", err)
		}
	})
}

func TestDeriveLiveDecisionScopeRejectsUnboundAnnotatedTagObject(t *testing.T) {
	policy := loadCanonicalPolicy(t)
	snapshot := liveFixtureSnapshot(t, policy)
	now := mustTime(t, "2026-01-01T00:25:00Z")
	tagSHA := strings.Repeat("8", 40)
	commitSHA := strings.Repeat("b", 40)
	tests := []struct {
		name    string
		payload map[string]any
	}{
		{
			name: "missing top-level sha",
			payload: map[string]any{
				"object": map[string]any{"type": "commit", "sha": commitSHA},
			},
		},
		{
			name: "mismatched top-level sha",
			payload: map[string]any{
				"sha": strings.Repeat("9", 40), "object": map[string]any{"type": "commit", "sha": commitSHA},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root, collection := writeLiveDeriveFixture(t, snapshot)
			rewriteLivePair(t, root, "repository-001/release/tag-ref.json", mustJSON(t, map[string]any{
				"ref": "refs/tags/v1.2.3", "object": map[string]any{"type": "tag", "sha": tagSHA},
			}))
			writeLivePair(t, root, "repository-001/release/tag-object-01.json", mustJSON(t, test.payload))
			collection.Repositories[0].StableRelease.TagObjectDocuments = 1
			republishLiveCollection(t, root, &collection)
			_, _, _, err := DeriveLiveDecisionScope(root, snapshot, policy, now, time.Hour)
			if err == nil || !strings.Contains(err.Error(), "top-level sha") {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestLivePullRequestParserRejectsClosedForkNullAndMalformedObjects(t *testing.T) {
	base := map[string]any{
		"number": 7, "state": "open", "draft": false,
		"base": map[string]any{"ref": "main", "sha": strings.Repeat("a", 40), "repo": map[string]any{"id": 10, "full_name": "example/env-vault"}},
		"head": map[string]any{"ref": "feature", "sha": strings.Repeat("b", 40), "repo": map[string]any{"id": 10, "full_name": "example/env-vault"}},
	}
	if _, err := InspectExactPullRequest(mustJSON(t, base), "example/env-vault", 10, 7); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"closed", func(value map[string]any) { value["state"] = "closed" }},
		{"fork", func(value map[string]any) {
			value["head"].(map[string]any)["repo"] = map[string]any{"id": 11, "full_name": "fork/env-vault"}
		}},
		{"null head", func(value map[string]any) { value["head"] = nil }},
		{"null head repo", func(value map[string]any) { value["head"].(map[string]any)["repo"] = nil }},
		{"missing draft", func(value map[string]any) { delete(value, "draft") }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var value map[string]any
			if err := json.Unmarshal(mustJSON(t, base), &value); err != nil {
				t.Fatal(err)
			}
			test.mutate(value)
			if _, err := InspectExactPullRequest(mustJSON(t, value), "example/env-vault", 10, 7); err == nil {
				t.Fatal("malformed pull request was accepted")
			}
		})
	}
}

func TestArrayPaginationProofRequiresExplicitShortTerminalPage(t *testing.T) {
	digest := strings.Repeat("a", 64)
	valid100 := ArrayPaginationProof{ItemCount: 100, PageCount: 2, PageItemCounts: []int{100, 0}, PageSHA256: []string{digest, digest}, FinalPageSHA256: []string{digest, digest}}
	if err := validateArrayPaginationProof(valid100); err != nil {
		t.Fatal(err)
	}
	invalid := valid100
	invalid.PageCount = 1
	invalid.PageItemCounts = []int{100}
	invalid.PageSHA256 = []string{digest}
	invalid.FinalPageSHA256 = []string{digest}
	if err := validateArrayPaginationProof(invalid); err == nil || !strings.Contains(err.Error(), "short terminal") {
		t.Fatalf("error=%v", err)
	}
}

func liveFixtureSnapshot(t *testing.T, policy Policy) Snapshot {
	t.Helper()
	const source = "ildarbinanas-design/env-vault"
	const tap = "ildarbinanas-design/homebrew-tap"
	shas := []string{strings.Repeat("a", 40), strings.Repeat("c", 40), strings.Repeat("b", 40), strings.Repeat("e", 40), strings.Repeat("f", 40)}
	paths := []string{".github/workflows/ci.yml", ".github/workflows/ci.yml", ".github/workflows/build-binaries.yml", ".github/workflows/bootstrap-release-assets.yml", "dynamic/github-code-scanning/codeql"}
	events := []string{"push", "push", "push", "workflow_dispatch", "push"}
	runs := make([]SnapshotRun, 0, 5)
	attempts := make([]SnapshotAttempt, 0, 5)
	for index := 0; index < 5; index++ {
		run := SnapshotRun{
			Repository: source, RunID: int64((index + 1) * 100), CurrentAttempt: 1, WorkflowPath: paths[index],
			HeadSHA: shas[index], HeadBranch: "main", HeadRepository: source, HeadRepositoryID: 10,
			Event: events[index], Status: "completed", Conclusion: "success",
			CreatedAt: "2026-01-01T00:00:00Z", RunStartedAt: "2026-01-01T00:01:00Z", UpdatedAt: "2026-01-01T00:10:00Z",
		}
		runs = append(runs, run)
		attempts = append(attempts, attemptFromRun(run, 1, run.RunStartedAt, run.UpdatedAt))
	}
	names := []string{
		"env-vault-linux-amd64",
		"env-vault-release-linux-amd64-attempt-1",
		"env-vault-release-observation-v1.2.3-attempt-1",
		"env-vault-release-assets-bootstrap-v1.2.3-" + strings.Repeat("b", 40) + "-101-attempt-1",
		"sarif-artifact-go",
	}
	artifacts := make([]SnapshotArtifact, 0, len(names))
	for index, name := range names {
		artifacts = append(artifacts, fixtureArtifact(int64(index+1), int64((index+1)*10), name, runs[index], 1, "2026-01-01T00:05:00Z", policy))
	}
	return Snapshot{
		SchemaID: SnapshotSchemaID, SchemaVersion: SnapshotSchemaVersion,
		ObservedStartedAt: "2026-01-01T00:20:00Z", ObservedFinishedAt: "2026-01-01T00:20:30Z",
		Repositories: []SnapshotRepository{
			{Repository: source, RepositoryID: 10, Artifacts: fixturePagination(5, "1"), Runs: fixturePagination(5, "2"), AttemptDocuments: 5, ArtifactCount: 5, ArtifactBytes: 150, RunCount: 5, AttemptCount: 5},
			{Repository: tap, RepositoryID: 20, Artifacts: fixturePagination(0, "3"), Runs: fixturePagination(0, "4"), AttemptDocuments: 0, ArtifactCount: 0, ArtifactBytes: 0, RunCount: 0, AttemptCount: 0},
		},
		Artifacts: artifacts, Runs: runs, Attempts: attempts,
		ArtifactCount: 5, ArtifactBytes: 150, RunCount: 5, AttemptCount: 5,
	}
}

func fixturePagination(count int, digit string) PaginationProof {
	return PaginationProof{TotalCount: count, PageCount: 1, PageItemCounts: []int{count}, PageSHA256: []string{strings.Repeat(digit, 64)}, FinalPageSHA256: []string{strings.Repeat(digit, 64)}}
}

func writeLiveDeriveFixture(t *testing.T, snapshot Snapshot) (string, LiveRawCollection) {
	t.Helper()
	root := t.TempDir()
	collection := LiveRawCollection{
		SchemaID: LiveCollectionSchemaID, SchemaVersion: LiveCollectionSchemaVersion,
		ObservedStartedAt: "2026-01-01T00:21:00Z", ObservedFinishedAt: "2026-01-01T00:22:00Z",
		ReleaseRepository: "ildarbinanas-design/env-vault",
	}
	policy := loadCanonicalPolicy(t)
	collection.SnapshotSemanticSHA256, _ = snapshotSemanticSHA256(snapshot)

	contractBytes := mustRead(t, filepath.Join("..", "..", releasecontract.CanonicalPath))
	contract, err := releasecontract.LoadBytes(contractBytes)
	if err != nil {
		t.Fatal(err)
	}
	repositories := []struct {
		name string
		id   int64
		sha  string
		runs []SnapshotRun
	}{
		{"ildarbinanas-design/env-vault", 10, strings.Repeat("c", 40), snapshot.Runs},
		{"ildarbinanas-design/homebrew-tap", 20, strings.Repeat("d", 40), []SnapshotRun{}},
	}
	_ = policy
	for index, repository := range repositories {
		directory := "repository-00" + string(rune('1'+index))
		metadata := mustJSON(t, map[string]any{"id": repository.id, "full_name": repository.name, "default_branch": "main"})
		writeLivePair(t, root, directory+"/repository.json", metadata)
		writeLivePair(t, root, directory+"/default-ref.json", mustJSON(t, map[string]any{"ref": "refs/heads/main", "object": map[string]any{"type": "commit", "sha": repository.sha}}))
		writeLivePair(t, root, directory+"/default-branch.json", mustJSON(t, map[string]any{"name": "main", "protected": true, "commit": map[string]any{"sha": repository.sha}}))
		pullPage := []byte("[]")
		writeLivePair(t, root, directory+"/pull-requests/pages/page-001.json", pullPage)
		runPage := liveRunPageJSON(t, repository.runs)
		writeLivePair(t, root, directory+"/runs/page-001.json", runPage)
		documents := make([]CollectedAttemptDocument, 0)
		for _, attempt := range snapshot.Attempts {
			if attempt.Repository != repository.name {
				continue
			}
			documents = append(documents, CollectedAttemptDocument{RunID: attempt.RunID, RunAttempt: attempt.RunAttempt})
			raw := runJSONFromAttempt(attempt, repository.id)
			writeLivePair(t, root, directory+"/attempts/"+attemptFilename(attempt.RunID, attempt.RunAttempt), mustJSON(t, raw))
		}
		stable := RawStableReleaseCollection{}
		if repository.name == collection.ReleaseRepository {
			content := map[string]any{"type": "file", "path": releasecontract.CanonicalPath, "sha": strings.Repeat("7", 40), "size": len(contractBytes), "encoding": "base64", "content": base64.StdEncoding.EncodeToString(contractBytes)}
			writeLivePair(t, root, directory+"/contract-content.json", mustJSON(t, content))
			release := fixtureReleaseJSON(t, contract)
			writeLivePair(t, root, directory+"/release/latest.json", release)
			writeLivePair(t, root, directory+"/release/exact.json", release)
			writeLivePair(t, root, directory+"/release/tag-ref.json", mustJSON(t, map[string]any{"ref": "refs/tags/v1.2.3", "object": map[string]any{"type": "commit", "sha": strings.Repeat("b", 40)}}))
			stable = RawStableReleaseCollection{Designated: true, State: StableReleasePresent, Version: "v1.2.3", TagObjectDocuments: 0}
		}
		collection.Repositories = append(collection.Repositories, LiveRawCollectionRepository{
			Repository: repository.name, RepositoryID: repository.id, Directory: directory, DefaultBranch: "main",
			PullRequests:       ArrayPaginationProof{ItemCount: 0, PageCount: 1, PageItemCounts: []int{0}, PageSHA256: []string{digestBytes(pullPage)}, FinalPageSHA256: []string{digestBytes(pullPage)}},
			Runs:               PaginationProof{TotalCount: len(repository.runs), PageCount: 1, PageItemCounts: []int{len(repository.runs)}, PageSHA256: []string{digestBytes(runPage)}, FinalPageSHA256: []string{digestBytes(runPage)}},
			PullRequestNumbers: []int64{}, AttemptDocuments: documents, StableRelease: stable,
		})
	}
	republishLiveCollection(t, root, &collection)
	return root, collection
}

func fixtureReleaseJSON(t *testing.T, contract releasecontract.Contract) []byte {
	t.Helper()
	assets := make([]any, 0, len(contract.Assets))
	for index, name := range contract.Assets {
		assets = append(assets, map[string]any{
			"id": index + 1, "node_id": "asset-node-" + name, "name": name, "label": nil,
			"uploader": map[string]any{"id": 1, "login": "release-bot"}, "content_type": "application/octet-stream", "state": "uploaded",
			"size": index + 1, "digest": "sha256:" + strings.Repeat("a", 64),
			"created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:01Z",
			"browser_download_url": "https://github.com/ildarbinanas-design/env-vault/releases/download/v1.2.3/" + name,
		})
	}
	return mustJSON(t, map[string]any{
		"id": 123, "tag_name": "v1.2.3", "target_commitish": "main", "draft": false, "prerelease": false,
		"published_at": "2026-01-01T00:00:02Z", "assets": assets,
	})
}

func liveRunPageJSON(t *testing.T, runs []SnapshotRun) []byte {
	t.Helper()
	values := make([]any, 0, len(runs))
	for _, run := range runs {
		values = append(values, runJSONFromSnapshot(run, 10))
	}
	return mustJSON(t, map[string]any{"total_count": len(values), "workflow_runs": values})
}

func runJSONFromSnapshot(run SnapshotRun, repositoryID int64) map[string]any {
	return map[string]any{
		"id": run.RunID, "run_attempt": run.CurrentAttempt, "path": run.WorkflowPath, "head_sha": run.HeadSHA, "head_branch": run.HeadBranch,
		"event": run.Event, "status": run.Status, "conclusion": run.Conclusion, "created_at": run.CreatedAt, "run_started_at": run.RunStartedAt, "updated_at": run.UpdatedAt,
		"repository":      map[string]any{"id": repositoryID, "full_name": run.Repository},
		"head_repository": map[string]any{"id": run.HeadRepositoryID, "full_name": run.HeadRepository},
	}
}

func runJSONFromAttempt(attempt SnapshotAttempt, repositoryID int64) map[string]any {
	return map[string]any{
		"id": attempt.RunID, "run_attempt": attempt.RunAttempt, "path": attempt.WorkflowPath, "head_sha": attempt.HeadSHA, "head_branch": attempt.HeadBranch,
		"event": attempt.Event, "status": attempt.Status, "conclusion": attempt.Conclusion, "created_at": attempt.CreatedAt, "run_started_at": attempt.RunStartedAt, "updated_at": attempt.UpdatedAt,
		"repository":      map[string]any{"id": repositoryID, "full_name": attempt.Repository},
		"head_repository": map[string]any{"id": attempt.HeadRepositoryID, "full_name": attempt.HeadRepository},
	}
}

func writeLivePair(t *testing.T, root, relative string, data []byte) {
	t.Helper()
	for _, path := range []string{relative, strings.TrimSuffix(relative, ".json") + "-final.json"} {
		filename := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(filename), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func rewriteLivePair(t *testing.T, root, relative string, data []byte) {
	t.Helper()
	writeLivePair(t, root, relative, data)
}

func republishLiveCollection(t *testing.T, root string, collection *LiveRawCollection) {
	t.Helper()
	paths := expectedLiveRawFiles(*collection)
	collection.Files = collection.Files[:0]
	for path := range paths {
		data := mustRead(t, filepath.Join(root, filepath.FromSlash(path)))
		collection.Files = append(collection.Files, RawFileProof{Path: path, Bytes: int64(len(data)), SHA256: digestBytes(data)})
	}
	sort.Slice(collection.Files, func(i, j int) bool { return collection.Files[i].Path < collection.Files[j].Path })
	data, err := MarshalCanonical(*collection)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "collection.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLiveCollectionAggregateBudgetIncludesCanonicalIndex(t *testing.T) {
	if err := ValidateLiveCollectionByteBudget(MaxLiveCollectionBytes-1, 1); err != nil {
		t.Fatalf("exact aggregate budget rejected: %v", err)
	}
	if err := ValidateLiveCollectionByteBudget(MaxLiveCollectionBytes, 1); err == nil {
		t.Fatal("aggregate budget accepted raw bytes plus an uncounted index")
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func mustRead(t *testing.T, filename string) []byte {
	t.Helper()
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
