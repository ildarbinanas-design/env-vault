package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ildarbinanas-design/env-vault/internal/actionsartifact"
	"github.com/ildarbinanas-design/env-vault/internal/releasecontract"
)

func TestArtifactsValidatePolicyCLIIsDeterministic(t *testing.T) {
	args := []string{
		"artifacts", "validate-policy",
		"--policy", canonicalRepositoryFile(t, actionsartifact.CanonicalPolicyPath),
		"--workflow-dir", canonicalRepositoryFile(t, ".github/workflows"),
		"--json",
	}
	var first, second, stderr bytes.Buffer
	if code := run(args, &first, &stderr); code != exitOK {
		t.Fatalf("first code=%d stderr=%s stdout=%s", code, stderr.String(), first.String())
	}
	stderr.Reset()
	if code := run(args, &second, &stderr); code != exitOK {
		t.Fatalf("second code=%d stderr=%s stdout=%s", code, stderr.String(), second.String())
	}
	if first.String() != second.String() {
		t.Fatalf("nondeterministic output:\nfirst=%s\nsecond=%s", first.String(), second.String())
	}
	var document actionsartifact.Validation
	decodeOneJSON(t, first.Bytes(), &document)
	if !document.OK || document.SchemaID != actionsartifact.ValidationSchemaID || document.UploadSiteCount != 23 || document.WorkflowCount != 7 {
		t.Fatalf("validation=%+v", document)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr=%q", stderr.String())
	}
}

func TestArtifactsValidatePolicyCLIFailsClosed(t *testing.T) {
	data, err := os.ReadFile(canonicalRepositoryFile(t, actionsartifact.CanonicalPolicyPath))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	sites := raw["sites"].([]any)
	sites[0].(map[string]any)["class"] = "unknown-class"
	data, err = json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	policyPath := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(policyPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"artifacts", "validate-policy",
		"--policy", policyPath,
		"--workflow-dir", canonicalRepositoryFile(t, ".github/workflows"),
		"--json",
	}, &stdout, &stderr)
	if code != exitSnapshotInvalid {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	var failure errorDocument
	decodeOneJSON(t, stdout.Bytes(), &failure)
	if failure.OK || failure.Error.Code != "INPUT_INVALID" || !strings.Contains(failure.Error.Message, "unknown artifact class") {
		t.Fatalf("failure=%+v", failure)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr=%q", stderr.String())
	}
}

func TestArtifactsCommandRequiresExactSubcommand(t *testing.T) {
	for _, args := range [][]string{{"artifacts"}, {"artifacts", "unknown"}, {"artifacts", "validate-policy", "extra"}} {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != exitUsage || !strings.Contains(stderr.String(), "artifacts validate-policy") {
			t.Fatalf("args=%v code=%d stderr=%q", args, code, stderr.String())
		}
	}
}

func TestArtifactsValidateAndClassifyCLIRequireExplicitFreshness(t *testing.T) {
	policyPath := canonicalRepositoryFile(t, actionsartifact.CanonicalPolicyPath)
	policy, err := actionsartifact.LoadPolicyFile(policyPath)
	if err != nil {
		t.Fatal(err)
	}
	var pattern actionsartifact.NamePattern
	for _, candidate := range policy.Patterns {
		if candidate.ID == "native-release-current" {
			pattern = candidate
			break
		}
	}
	sha := strings.Repeat("a", 40)
	snapshot := actionsartifact.Snapshot{
		SchemaID: actionsartifact.SnapshotSchemaID, SchemaVersion: actionsartifact.SnapshotSchemaVersion,
		ObservedStartedAt: "2026-01-01T00:20:00Z", ObservedFinishedAt: "2026-01-01T00:20:30Z",
		Repositories: []actionsartifact.SnapshotRepository{{
			Repository: "example/env-vault", RepositoryID: 10,
			Artifacts:        actionsartifact.PaginationProof{TotalCount: 1, PageCount: 1, PageItemCounts: []int{1}, PageSHA256: []string{strings.Repeat("1", 64)}, FinalPageSHA256: []string{strings.Repeat("1", 64)}},
			Runs:             actionsartifact.PaginationProof{TotalCount: 1, PageCount: 1, PageItemCounts: []int{1}, PageSHA256: []string{strings.Repeat("2", 64)}, FinalPageSHA256: []string{strings.Repeat("2", 64)}},
			AttemptDocuments: 1, ArtifactCount: 1, ArtifactBytes: 10, RunCount: 1, AttemptCount: 1,
		}},
		Artifacts: []actionsartifact.SnapshotArtifact{{
			Repository: "example/env-vault", ArtifactID: 1, Name: "env-vault-release-linux-amd64-attempt-1",
			Digest: "sha256:" + strings.Repeat("f", 64), SizeInBytes: 10,
			CreatedAt: "2026-01-01T00:05:00Z", UpdatedAt: "2026-01-01T00:05:00Z", ExpiresAt: "2026-01-02T00:00:00Z",
			ProducerRunID: 100, ProducerRunAttempt: 1, WorkflowPath: ".github/workflows/ci.yml", HeadSHA: sha, HeadBranch: "main", HeadRepositoryID: 10,
			PolicyPattern: pattern.ID, Class: pattern.Class, Lifecycle: pattern.Lifecycle, DependencyRationale: pattern.DependencyRepairRationale,
		}},
		Runs: []actionsartifact.SnapshotRun{{
			Repository: "example/env-vault", RunID: 100, CurrentAttempt: 1, WorkflowPath: ".github/workflows/ci.yml",
			HeadSHA: sha, HeadBranch: "main", HeadRepository: "example/env-vault", HeadRepositoryID: 10,
			Event: "push", Status: "completed", Conclusion: "success", CreatedAt: "2026-01-01T00:00:00Z", RunStartedAt: "2026-01-01T00:01:00Z", UpdatedAt: "2026-01-01T00:10:00Z",
		}},
		Attempts: []actionsartifact.SnapshotAttempt{{
			Repository: "example/env-vault", RunID: 100, RunAttempt: 1, WorkflowPath: ".github/workflows/ci.yml",
			HeadSHA: sha, HeadBranch: "main", HeadRepository: "example/env-vault", HeadRepositoryID: 10,
			Event: "push", Status: "completed", Conclusion: "success", CreatedAt: "2026-01-01T00:00:00Z", RunStartedAt: "2026-01-01T00:01:00Z", UpdatedAt: "2026-01-01T00:10:00Z",
		}},
		ArtifactCount: 1, ArtifactBytes: 10, RunCount: 1, AttemptCount: 1,
	}
	snapshotPath := filepath.Join(t.TempDir(), "snapshot.json")
	writeTestJSON(t, snapshotPath, snapshot)
	var stdout, stderr bytes.Buffer
	validateArgs := []string{"artifacts", "validate-snapshot", "--policy", policyPath, "--snapshot", snapshotPath, "--now", "2026-01-01T00:25:00Z", "--max-age", "1h", "--json"}
	if code := run(validateArgs, &stdout, &stderr); code != exitOK {
		t.Fatalf("validate code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	var validation artifactSnapshotValidation
	decodeOneJSON(t, stdout.Bytes(), &validation)
	if !validation.OK || validation.ArtifactCount != 1 || len(validation.SnapshotSemanticSHA256) != 64 {
		t.Fatalf("validation=%+v", validation)
	}

	stdout.Reset()
	stderr.Reset()
	classifyArgs := []string{"artifacts", "classify", "--policy", policyPath, "--snapshot", snapshotPath, "--scope", "scope.json", "--now", "2026-01-01T00:25:00Z", "--max-age", "1h", "--output", "manifest.json"}
	if code := run(classifyArgs, &stdout, &stderr); code != exitUsage || !strings.Contains(stderr.String(), "--live-collection") {
		t.Fatalf("classify without replay collection code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"artifacts", "validate-snapshot", "--policy", policyPath, "--snapshot", snapshotPath}, &stdout, &stderr); code != exitUsage {
		t.Fatalf("freshness omission code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestArtifactFreshnessCLIRejectsWindowOverOneHour(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"artifacts", "validate-snapshot",
		"--snapshot", "unused.json",
		"--now", "2026-01-01T00:25:00Z",
		"--max-age", "1h0m0.000000001s",
	}, &stdout, &stderr)
	if code != exitSnapshotInvalid || stdout.Len() != 0 || !strings.Contains(stderr.String(), "no greater than 1h") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestArtifactsClassifyRequiresByteExactLiveCollectionReplay(t *testing.T) {
	policyPath := canonicalRepositoryFile(t, actionsartifact.CanonicalPolicyPath)
	policy, err := actionsartifact.LoadPolicyFile(policyPath)
	if err != nil {
		t.Fatal(err)
	}
	now := mustParseCLITime(t, "2026-01-01T00:25:00Z")
	snapshot := replayGateSnapshot()
	snapshotPath := filepath.Join(t.TempDir(), "snapshot.json")
	writeTestJSON(t, snapshotPath, snapshot)
	liveRoot := writeReplayGateCollection(t, snapshot, policy, now)
	_, _, scope, err := actionsartifact.DeriveLiveDecisionScope(liveRoot, snapshot, policy, now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	scopePath := filepath.Join(t.TempDir(), "scope.json")
	writeTestJSON(t, scopePath, scope)
	args := []string{
		"artifacts", "classify", "--policy", policyPath, "--snapshot", snapshotPath,
		"--scope", scopePath, "--live-collection", liveRoot,
		"--now", "2026-01-01T00:25:00Z", "--max-age", "1h", "--output", filepath.Join(t.TempDir(), "manifest.json"),
	}
	var stdout, stderr bytes.Buffer
	if code := run(args, &stdout, &stderr); code != exitOK {
		t.Fatalf("valid replay code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	forged := scope
	forged.LiveObservationSemanticSHA256 = strings.Repeat("9", 64)
	forgedPath := filepath.Join(t.TempDir(), "forged-scope.json")
	writeTestJSON(t, forgedPath, forged)
	args[7] = forgedPath
	args[len(args)-1] = filepath.Join(t.TempDir(), "forged-manifest.json")
	stdout.Reset()
	stderr.Reset()
	if code := run(args, &stdout, &stderr); code != exitSnapshotInvalid || !strings.Contains(stderr.String(), "does not equal checked live-collection replay") {
		t.Fatalf("forged replay code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestArtifactsManifestPackageCLIIsOfflineNoClobberAndReconstructsExactly(t *testing.T) {
	policy, err := actionsartifact.LoadPolicyFile(canonicalRepositoryFile(t, actionsartifact.CanonicalPolicyPath))
	if err != nil {
		t.Fatal(err)
	}
	now := mustParseCLITime(t, "2026-01-01T00:25:00Z")
	snapshot := replayGateSnapshot()
	liveRoot := writeReplayGateCollection(t, snapshot, policy, now)
	_, _, scope, err := actionsartifact.DeriveLiveDecisionScope(liveRoot, snapshot, policy, now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := actionsartifact.Classify(snapshot, scope, policy, now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	manifestData, err := actionsartifact.MarshalCanonical(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(manifestPath, manifestData, 0o600); err != nil {
		t.Fatal(err)
	}
	repositoryRoot := t.TempDir()
	t.Setenv("PATH", "")
	t.Setenv("GH_TOKEN", "SENTINEL_MUST_NOT_BE_READ")
	t.Setenv("GITHUB_TOKEN", "SENTINEL_MUST_NOT_BE_READ")
	var stdout, stderr bytes.Buffer
	createArgs := []string{"artifacts", "package-manifest", "--manifest", manifestPath, "--repository-root", repositoryRoot}
	if code := run(createArgs, &stdout, &stderr); code != exitOK || stderr.Len() != 0 || !strings.Contains(stdout.String(), manifest.SemanticSHA256) {
		t.Fatalf("create code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := run(createArgs, &stdout, &stderr); code != exitSnapshotInvalid || !strings.Contains(stderr.String(), "already exists") {
		t.Fatalf("rewrite code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}

	reconstructed := filepath.Join(t.TempDir(), "reconstructed.json")
	stdout.Reset()
	stderr.Reset()
	verifyArgs := []string{
		"artifacts", "verify-manifest-package", "--repository-root", repositoryRoot,
		"--manifest-sha256", manifest.SemanticSHA256, "--compare-manifest", manifestPath,
		"--manifest-output", reconstructed, "--json",
	}
	if code := run(verifyArgs, &stdout, &stderr); code != exitOK || stderr.Len() != 0 {
		t.Fatalf("verify code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	var summary actionsartifact.ManifestPackageSummary
	decodeOneJSON(t, stdout.Bytes(), &summary)
	if summary.SchemaID != actionsartifact.ManifestPackageSummarySchemaID || summary.ManifestSemanticSHA256 != manifest.SemanticSHA256 || summary.Totals != manifest.Totals {
		t.Fatalf("summary=%+v", summary)
	}
	reconstructedData, err := os.ReadFile(reconstructed)
	if err != nil || !bytes.Equal(reconstructedData, manifestData) {
		t.Fatalf("reconstructed manifest is not byte exact: error=%v", err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := run(verifyArgs[:len(verifyArgs)-1], &stdout, &stderr); code != exitInternal || !strings.Contains(stderr.String(), "not a new writable file") {
		t.Fatalf("manifest-output rewrite code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func replayGateSnapshot() actionsartifact.Snapshot {
	return actionsartifact.Snapshot{
		SchemaID: actionsartifact.SnapshotSchemaID, SchemaVersion: actionsartifact.SnapshotSchemaVersion,
		ObservedStartedAt: "2026-01-01T00:20:00Z", ObservedFinishedAt: "2026-01-01T00:20:30Z",
		Repositories: []actionsartifact.SnapshotRepository{
			{Repository: "ildarbinanas-design/env-vault", RepositoryID: 10, Artifacts: replayGatePagination("1"), Runs: replayGatePagination("2")},
			{Repository: "ildarbinanas-design/homebrew-tap", RepositoryID: 20, Artifacts: replayGatePagination("3"), Runs: replayGatePagination("4")},
		},
		Artifacts: []actionsartifact.SnapshotArtifact{}, Runs: []actionsartifact.SnapshotRun{}, Attempts: []actionsartifact.SnapshotAttempt{},
	}
}

func replayGatePagination(digit string) actionsartifact.PaginationProof {
	return actionsartifact.PaginationProof{TotalCount: 0, PageCount: 1, PageItemCounts: []int{0}, PageSHA256: []string{strings.Repeat(digit, 64)}, FinalPageSHA256: []string{strings.Repeat(digit, 64)}}
}

func writeReplayGateCollection(t *testing.T, snapshot actionsartifact.Snapshot, policy actionsartifact.Policy, now time.Time) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "live")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	snapshotDigest, err := actionsartifact.SnapshotSemanticSHA256(snapshot, policy, now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	collection := actionsartifact.LiveRawCollection{
		SchemaID: actionsartifact.LiveCollectionSchemaID, SchemaVersion: actionsartifact.LiveCollectionSchemaVersion,
		ObservedStartedAt: "2026-01-01T00:21:00Z", ObservedFinishedAt: "2026-01-01T00:22:00Z",
		SnapshotSemanticSHA256: snapshotDigest, ReleaseRepository: "ildarbinanas-design/env-vault",
	}
	for index, repository := range []struct {
		name string
		id   int64
		sha  string
	}{
		{"ildarbinanas-design/env-vault", 10, strings.Repeat("c", 40)},
		{"ildarbinanas-design/homebrew-tap", 20, strings.Repeat("d", 40)},
	} {
		directory := fmt.Sprintf("repository-%03d", index+1)
		writeReplayPair(t, root, directory+"/repository.json", replayJSON(t, map[string]any{"id": repository.id, "full_name": repository.name, "default_branch": "main"}))
		writeReplayPair(t, root, directory+"/default-ref.json", replayJSON(t, map[string]any{"ref": "refs/heads/main", "object": map[string]any{"type": "commit", "sha": repository.sha}}))
		writeReplayPair(t, root, directory+"/default-branch.json", replayJSON(t, map[string]any{"name": "main", "protected": true, "commit": map[string]any{"sha": repository.sha}}))
		pulls := []byte("[]")
		runs := []byte(`{"total_count":0,"workflow_runs":[]}`)
		writeReplayPair(t, root, directory+"/pull-requests/pages/page-001.json", pulls)
		writeReplayPair(t, root, directory+"/runs/page-001.json", runs)
		stable := actionsartifact.RawStableReleaseCollection{}
		if index == 0 {
			contractBytes := mustReadReplay(t, canonicalRepositoryFile(t, releasecontract.CanonicalPath))
			writeReplayPair(t, root, directory+"/contract-content.json", replayJSON(t, map[string]any{
				"type": "file", "path": releasecontract.CanonicalPath, "sha": strings.Repeat("7", 40), "size": len(contractBytes),
				"encoding": "base64", "content": base64.StdEncoding.EncodeToString(contractBytes),
			}))
			absence, err := actionsartifact.MarshalCanonical(actionsartifact.StableReleaseAbsenceProof{
				SchemaID: "env-vault.github-exact-absence.v1", SchemaVersion: 1, Repository: repository.name,
				Endpoint: "repos/ildarbinanas-design/env-vault/releases/latest", ReasonCode: "REMOTE_NOT_FOUND", TransportExit: 4,
			})
			if err != nil {
				t.Fatal(err)
			}
			writeReplayPair(t, root, directory+"/release/latest.json", absence)
			stable = actionsartifact.RawStableReleaseCollection{Designated: true, State: actionsartifact.StableReleaseAbsent}
		}
		collection.Repositories = append(collection.Repositories, actionsartifact.LiveRawCollectionRepository{
			Repository: repository.name, RepositoryID: repository.id, Directory: directory, DefaultBranch: "main",
			PullRequests:       actionsartifact.ArrayPaginationProof{ItemCount: 0, PageCount: 1, PageItemCounts: []int{0}, PageSHA256: []string{replayDigest(pulls)}, FinalPageSHA256: []string{replayDigest(pulls)}},
			Runs:               actionsartifact.PaginationProof{TotalCount: 0, PageCount: 1, PageItemCounts: []int{0}, PageSHA256: []string{replayDigest(runs)}, FinalPageSHA256: []string{replayDigest(runs)}},
			PullRequestNumbers: []int64{}, AttemptDocuments: []actionsartifact.CollectedAttemptDocument{}, StableRelease: stable,
		})
	}
	for _, repository := range collection.Repositories {
		for _, relative := range replayExpectedFiles(repository) {
			data := mustReadReplay(t, filepath.Join(root, filepath.FromSlash(relative)))
			collection.Files = append(collection.Files, actionsartifact.RawFileProof{Path: relative, Bytes: int64(len(data)), SHA256: replayDigest(data)})
		}
	}
	sort.Slice(collection.Files, func(i, j int) bool { return collection.Files[i].Path < collection.Files[j].Path })
	data, err := actionsartifact.MarshalCanonical(collection)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "collection.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

func replayExpectedFiles(repository actionsartifact.LiveRawCollectionRepository) []string {
	prefix := repository.Directory
	values := []string{
		prefix + "/repository.json", prefix + "/repository-final.json",
		prefix + "/default-ref.json", prefix + "/default-ref-final.json",
		prefix + "/default-branch.json", prefix + "/default-branch-final.json",
		prefix + "/pull-requests/pages/page-001.json", prefix + "/pull-requests/pages/page-001-final.json",
		prefix + "/runs/page-001.json", prefix + "/runs/page-001-final.json",
	}
	if repository.StableRelease.Designated {
		values = append(values,
			prefix+"/contract-content.json", prefix+"/contract-content-final.json",
			prefix+"/release/latest.json", prefix+"/release/latest-final.json",
		)
	}
	return values
}

func writeReplayPair(t *testing.T, root, relative string, data []byte) {
	t.Helper()
	for _, value := range []string{relative, strings.TrimSuffix(relative, ".json") + "-final.json"} {
		filename := filepath.Join(root, filepath.FromSlash(value))
		if err := os.MkdirAll(filepath.Dir(filename), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func replayJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func replayDigest(data []byte) string {
	digest := sha256.Sum256(data)
	return fmt.Sprintf("%x", digest[:])
}

func mustReadReplay(t *testing.T, filename string) []byte {
	t.Helper()
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func writeTestJSON(t *testing.T, filename string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustParseCLITime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
