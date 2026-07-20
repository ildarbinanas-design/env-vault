package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ildarbinanas-design/env-vault/internal/actionsartifact"
	"github.com/ildarbinanas-design/env-vault/internal/releasecontract"
)

func TestLiveCollectorUsesCheckedTransportAndCompletes100And101PullRequestPagination(t *testing.T) {
	for _, count := range []int{100, 101} {
		t.Run(fmt.Sprintf("count-%d", count), func(t *testing.T) {
			fixtures := writeLiveCollectorFixtures(t, count)
			fake, logFile := writeLiveFakeTransport(t, fixtures)
			t.Setenv("RELEASE_TRANSPORT_BIN", fake)
			t.Setenv("LIVE_FIXTURES", fixtures)
			t.Setenv("LIVE_LOG", logFile)
			t.Setenv("LIVE_DRIFT_PAGE2", "0")
			t.Setenv("LIVE_STABLE_PRESENT", "0")
			snapshotPath := writeLiveCollectorSnapshot(t)
			output := filepath.Join(t.TempDir(), "live-collection")
			var stdout, stderr bytes.Buffer
			args := []string{
				"--output", output, "--snapshot", snapshotPath,
				"--policy", filepath.Join("..", "..", actionsartifact.CanonicalPolicyPath),
				"--release-repository", "ildarbinanas-design/env-vault",
				"--repository", "ildarbinanas-design/homebrew-tap",
				"--repository", "ildarbinanas-design/env-vault",
			}
			if status := run(context.Background(), args, &stdout, &stderr); status != 0 {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout.String(), stderr.String())
			}
			if !strings.Contains(stdout.String(), fmt.Sprintf("pull_requests=%d", count)) || !strings.Contains(stdout.String(), "stable_release=absent") || stderr.Len() != 0 {
				t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
			}
			var collection actionsartifact.LiveRawCollection
			if err := json.Unmarshal(mustReadCollector(t, filepath.Join(output, "collection.json")), &collection); err != nil {
				t.Fatal(err)
			}
			if err := collection.Validate(); err != nil {
				t.Fatal(err)
			}
			wantCounts := []int{100, 0}
			if count == 101 {
				wantCounts = []int{100, 1}
			}
			if !reflect.DeepEqual(collection.Repositories[0].PullRequests.PageItemCounts, wantCounts) || collection.Repositories[0].PullRequests.ItemCount != count {
				t.Fatalf("pull-request pagination=%+v", collection.Repositories[0].PullRequests)
			}
			absence := mustReadCollector(t, filepath.Join(output, "repository-001", "release", "latest.json"))
			if _, err := actionsartifact.ParseStableReleaseAbsenceProof(absence, "ildarbinanas-design/env-vault", "repos/ildarbinanas-design/env-vault/releases/latest"); err != nil {
				t.Fatal(err)
			}
			logData := string(mustReadCollector(t, logFile))
			if strings.Contains(logData, "--paginate") || strings.Contains(logData, "/repositories/") || !strings.Contains(logData, "pulls?state=open&per_page=100&page=2") {
				t.Fatalf("checked transport log=%s", logData)
			}
			for _, line := range strings.Split(strings.TrimSpace(logData), "\n") {
				if !strings.HasPrefix(line, "read\t") {
					t.Fatalf("non-read transport invocation %q", line)
				}
			}
		})
	}
}

func TestLiveCollectorTracksPresentAnnotatedStableReleaseThroughGlobalFinalReread(t *testing.T) {
	fixtures := writeLiveCollectorFixtures(t, 100)
	fake, logFile := writeLiveFakeTransport(t, fixtures)
	t.Setenv("RELEASE_TRANSPORT_BIN", fake)
	t.Setenv("LIVE_FIXTURES", fixtures)
	t.Setenv("LIVE_LOG", logFile)
	t.Setenv("LIVE_DRIFT_PAGE2", "0")
	t.Setenv("LIVE_STABLE_PRESENT", "1")
	output := filepath.Join(t.TempDir(), "live-collection")
	var stdout, stderr bytes.Buffer
	status := run(context.Background(), []string{
		"--output", output, "--snapshot", writeLiveCollectorSnapshot(t),
		"--policy", filepath.Join("..", "..", actionsartifact.CanonicalPolicyPath),
		"--release-repository", "ildarbinanas-design/env-vault",
		"--repository", "ildarbinanas-design/env-vault", "--repository", "ildarbinanas-design/homebrew-tap",
	}, &stdout, &stderr)
	if status != 0 || !strings.Contains(stdout.String(), "stable_release=present") || stderr.Len() != 0 {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout.String(), stderr.String())
	}
	var collection actionsartifact.LiveRawCollection
	if err := json.Unmarshal(mustReadCollector(t, filepath.Join(output, "collection.json")), &collection); err != nil {
		t.Fatal(err)
	}
	stable := collection.Repositories[0].StableRelease
	if stable.State != actionsartifact.StableReleasePresent || stable.Version != "v1.2.3" || stable.TagObjectDocuments != 1 {
		t.Fatalf("stable collection=%+v", stable)
	}
	proofs := make(map[string]actionsartifact.RawFileProof, len(collection.Files))
	for _, proof := range collection.Files {
		proofs[proof.Path] = proof
	}
	for _, relative := range []string{
		"repository-001/release/latest.json", "repository-001/release/latest-final.json",
		"repository-001/release/exact.json", "repository-001/release/exact-final.json",
		"repository-001/release/tag-ref.json", "repository-001/release/tag-ref-final.json",
		"repository-001/release/tag-object-01.json", "repository-001/release/tag-object-01-final.json",
	} {
		proof, exists := proofs[relative]
		if !exists || proof.Bytes < 1 || len(proof.SHA256) != 64 {
			t.Fatalf("missing present-stable raw proof %q: %+v", relative, proof)
		}
	}
	logData := string(mustReadCollector(t, logFile))
	for _, endpoint := range []string{
		"repos/ildarbinanas-design/env-vault/releases/latest",
		"repos/ildarbinanas-design/env-vault/releases/tags/v1.2.3",
		"repos/ildarbinanas-design/env-vault/git/ref/tags/v1.2.3",
		"repos/ildarbinanas-design/env-vault/git/tags/" + strings.Repeat("8", 40),
	} {
		if strings.Count(logData, endpoint) != 2 {
			t.Fatalf("endpoint %q was not read exactly initial+final:\n%s", endpoint, logData)
		}
	}
}

func TestLiveCollectorRejectsUnboundAnnotatedTagObject(t *testing.T) {
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
			fixtures := writeLiveCollectorFixtures(t, 0)
			writeCollectorJSON(t, filepath.Join(fixtures, "tag-object.json"), test.payload)
			fake, logFile := writeLiveFakeTransport(t, fixtures)
			t.Setenv("RELEASE_TRANSPORT_BIN", fake)
			t.Setenv("LIVE_FIXTURES", fixtures)
			t.Setenv("LIVE_LOG", logFile)
			t.Setenv("LIVE_DRIFT_PAGE2", "0")
			t.Setenv("LIVE_STABLE_PRESENT", "1")
			output := filepath.Join(t.TempDir(), "live-collection")
			var stdout, stderr bytes.Buffer
			status := run(context.Background(), []string{
				"--output", output, "--snapshot", writeLiveCollectorSnapshot(t),
				"--policy", filepath.Join("..", "..", actionsartifact.CanonicalPolicyPath),
				"--release-repository", "ildarbinanas-design/env-vault",
				"--repository", "ildarbinanas-design/env-vault", "--repository", "ildarbinanas-design/homebrew-tap",
			}, &stdout, &stderr)
			if status != 1 || !strings.Contains(stderr.String(), "stable tag object 1 is malformed") {
				t.Fatalf("tag=%s status=%d stdout=%q stderr=%q", tagSHA, status, stdout.String(), stderr.String())
			}
			if _, err := os.Stat(filepath.Join(output, "collection.json")); !os.IsNotExist(err) {
				t.Fatalf("collection index must not publish for an unbound tag object: %v", err)
			}
		})
	}
}

func TestLiveCollectorAccountsByteBudgetIncrementally(t *testing.T) {
	collector := liveCollector{totalBytes: actionsartifact.MaxLiveCollectionBytes - 1}
	if err := collector.accountBytes(2); err == nil {
		t.Fatal("collector accepted a response beyond the cumulative byte budget")
	}
	if collector.totalBytes != actionsartifact.MaxLiveCollectionBytes-1 {
		t.Fatalf("failed accounting changed total bytes to %d", collector.totalBytes)
	}
	if err := collector.accountBytes(1); err != nil {
		t.Fatalf("collector rejected the exact remaining byte: %v", err)
	}
}

func TestLiveCollectorRejectsNonFirstPullRequestPageDriftAfterAllExactObjects(t *testing.T) {
	fixtures := writeLiveCollectorFixtures(t, 101)
	fake, logFile := writeLiveFakeTransport(t, fixtures)
	t.Setenv("RELEASE_TRANSPORT_BIN", fake)
	t.Setenv("LIVE_FIXTURES", fixtures)
	t.Setenv("LIVE_LOG", logFile)
	t.Setenv("LIVE_DRIFT_PAGE2", "1")
	t.Setenv("LIVE_STABLE_PRESENT", "0")
	output := filepath.Join(t.TempDir(), "live-collection")
	var stdout, stderr bytes.Buffer
	status := run(context.Background(), []string{
		"--output", output, "--snapshot", writeLiveCollectorSnapshot(t),
		"--policy", filepath.Join("..", "..", actionsartifact.CanonicalPolicyPath),
		"--release-repository", "ildarbinanas-design/env-vault",
		"--repository", "ildarbinanas-design/env-vault", "--repository", "ildarbinanas-design/homebrew-tap",
	}, &stdout, &stderr)
	if status != 1 || !strings.Contains(stderr.String(), "global final reread") || !strings.Contains(stderr.String(), "changed bytes") {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(output, "collection.json")); !os.IsNotExist(err) {
		t.Fatalf("collection index must not publish after drift: %v", err)
	}
	logData := strings.Split(strings.TrimSpace(string(mustReadCollector(t, logFile))), "\n")
	lastExact, finalPageTwo := -1, -1
	for index, line := range logData {
		if strings.Contains(line, "/pulls/101\t") {
			lastExact = index
		}
		if strings.Contains(line, "pulls?state=open&per_page=100&page=2") && strings.HasSuffix(line, "page-002-final.json") {
			finalPageTwo = index
		}
	}
	if lastExact < 0 || finalPageTwo < 0 || finalPageTwo <= lastExact {
		t.Fatalf("global final reread began before exact PR collection:\n%s", strings.Join(logData, "\n"))
	}
}

func writeLiveCollectorSnapshot(t *testing.T) string {
	t.Helper()
	finished := time.Now().UTC().Add(-2 * time.Minute)
	started := finished.Add(-time.Minute)
	snapshot := actionsartifact.Snapshot{
		SchemaID: actionsartifact.SnapshotSchemaID, SchemaVersion: actionsartifact.SnapshotSchemaVersion,
		ObservedStartedAt: started.Format(time.RFC3339Nano), ObservedFinishedAt: finished.Format(time.RFC3339Nano),
		Repositories: []actionsartifact.SnapshotRepository{
			{Repository: "ildarbinanas-design/env-vault", RepositoryID: 10, Artifacts: emptyCollectorPagination("1"), Runs: emptyCollectorPagination("2")},
			{Repository: "ildarbinanas-design/homebrew-tap", RepositoryID: 20, Artifacts: emptyCollectorPagination("3"), Runs: emptyCollectorPagination("4")},
		},
		Artifacts: []actionsartifact.SnapshotArtifact{}, Runs: []actionsartifact.SnapshotRun{}, Attempts: []actionsartifact.SnapshotAttempt{},
	}
	path := filepath.Join(t.TempDir(), "snapshot.json")
	writeCollectorJSON(t, path, snapshot)
	return path
}

func emptyCollectorPagination(digit string) actionsartifact.PaginationProof {
	return actionsartifact.PaginationProof{TotalCount: 0, PageCount: 1, PageItemCounts: []int{0}, PageSHA256: []string{strings.Repeat(digit, 64)}, FinalPageSHA256: []string{strings.Repeat(digit, 64)}}
}

func writeLiveCollectorFixtures(t *testing.T, pullRequestCount int) string {
	t.Helper()
	directory := t.TempDir()
	mainSHA := strings.Repeat("c", 40)
	for _, repository := range []struct {
		key  string
		name string
		id   int64
		sha  string
	}{
		{"source", "ildarbinanas-design/env-vault", 10, mainSHA},
		{"tap", "ildarbinanas-design/homebrew-tap", 20, strings.Repeat("d", 40)},
	} {
		writeCollectorJSON(t, filepath.Join(directory, repository.key+"-repository.json"), map[string]any{"id": repository.id, "full_name": repository.name, "default_branch": "main"})
		writeCollectorJSON(t, filepath.Join(directory, repository.key+"-ref.json"), map[string]any{"ref": "refs/heads/main", "object": map[string]any{"type": "commit", "sha": repository.sha}})
		writeCollectorJSON(t, filepath.Join(directory, repository.key+"-branch.json"), map[string]any{"name": "main", "protected": true, "commit": map[string]any{"sha": repository.sha}})
		writeCollectorJSON(t, filepath.Join(directory, repository.key+"-runs.json"), map[string]any{"total_count": 0, "workflow_runs": []any{}})
		writeCollectorJSON(t, filepath.Join(directory, repository.key+"-pulls-page-001.json"), []any{})
	}
	pulls := make([]any, 0, pullRequestCount)
	for number := 1; number <= pullRequestCount; number++ {
		pull := collectorPullRequest(number)
		pulls = append(pulls, pull)
		writeCollectorJSON(t, filepath.Join(directory, fmt.Sprintf("pr-%d.json", number)), pull)
	}
	firstPageCount := pullRequestCount
	if firstPageCount > 100 {
		firstPageCount = 100
	}
	writeCollectorJSON(t, filepath.Join(directory, "source-pulls-page-001.json"), pulls[:firstPageCount])
	writeCollectorJSON(t, filepath.Join(directory, "source-pulls-page-002.json"), pulls[firstPageCount:])
	pageTwo := mustReadCollector(t, filepath.Join(directory, "source-pulls-page-002.json"))
	if err := os.WriteFile(filepath.Join(directory, "source-pulls-page-002-drift.json"), append(pageTwo, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	contractBytes := mustReadCollector(t, filepath.Join("..", "..", releasecontract.CanonicalPath))
	writeCollectorJSON(t, filepath.Join(directory, "contract.json"), map[string]any{
		"type": "file", "path": releasecontract.CanonicalPath, "sha": strings.Repeat("7", 40), "size": len(contractBytes),
		"encoding": "base64", "content": base64.StdEncoding.EncodeToString(contractBytes),
	})
	contract, err := releasecontract.LoadBytes(contractBytes)
	if err != nil {
		t.Fatal(err)
	}
	assets := make([]any, 0, len(contract.Assets))
	for index, name := range contract.Assets {
		assets = append(assets, map[string]any{
			"id": index + 1, "node_id": fmt.Sprintf("asset-node-%d", index+1), "name": name, "label": nil,
			"uploader": map[string]any{"id": 1, "login": "release-bot"}, "content_type": "application/octet-stream", "state": "uploaded",
			"size": index + 1, "digest": "sha256:" + strings.Repeat("a", 64),
			"created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:01Z",
			"browser_download_url": "https://github.com/ildarbinanas-design/env-vault/releases/download/v1.2.3/" + name,
		})
	}
	release := map[string]any{
		"id": 123, "tag_name": "v1.2.3", "target_commitish": "main", "draft": false, "prerelease": false,
		"published_at": "2026-01-01T00:00:02Z", "assets": assets,
	}
	writeCollectorJSON(t, filepath.Join(directory, "release.json"), release)
	writeCollectorJSON(t, filepath.Join(directory, "tag-ref.json"), map[string]any{"ref": "refs/tags/v1.2.3", "object": map[string]any{"type": "tag", "sha": strings.Repeat("8", 40)}})
	writeCollectorJSON(t, filepath.Join(directory, "tag-object.json"), map[string]any{"sha": strings.Repeat("8", 40), "object": map[string]any{"type": "commit", "sha": strings.Repeat("b", 40)}})
	return directory
}

func collectorPullRequest(number int) map[string]any {
	repository := map[string]any{"id": 10, "full_name": "ildarbinanas-design/env-vault"}
	return map[string]any{
		"number": number, "state": "open", "draft": false,
		"base": map[string]any{"ref": "main", "sha": strings.Repeat("c", 40), "repo": repository},
		"head": map[string]any{"ref": fmt.Sprintf("feature-%03d", number), "sha": strings.Repeat("a", 40), "repo": repository},
	}
}

func writeLiveFakeTransport(t *testing.T, fixtures string) (string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-transport")
	logFile := filepath.Join(t.TempDir(), "transport.log")
	mainSHA := strings.Repeat("c", 40)
	script := fmt.Sprintf(`#!/bin/sh
set -eu
[ "$#" -eq 3 ]
[ "$1" = read ]
output=$2
endpoint=$3
printf '%%s\t%%s\t%%s\n' "$1" "$endpoint" "${output##*/}" >> "$LIVE_LOG"
case "$endpoint" in
  'repos/ildarbinanas-design/env-vault') source_file="$LIVE_FIXTURES/source-repository.json" ;;
  'repos/ildarbinanas-design/homebrew-tap') source_file="$LIVE_FIXTURES/tap-repository.json" ;;
  'repos/ildarbinanas-design/env-vault/git/ref/heads/main') source_file="$LIVE_FIXTURES/source-ref.json" ;;
  'repos/ildarbinanas-design/homebrew-tap/git/ref/heads/main') source_file="$LIVE_FIXTURES/tap-ref.json" ;;
  'repos/ildarbinanas-design/env-vault/branches/main') source_file="$LIVE_FIXTURES/source-branch.json" ;;
  'repos/ildarbinanas-design/homebrew-tap/branches/main') source_file="$LIVE_FIXTURES/tap-branch.json" ;;
  'repos/ildarbinanas-design/env-vault/pulls?state=open&per_page=100&page=1') source_file="$LIVE_FIXTURES/source-pulls-page-001.json" ;;
  'repos/ildarbinanas-design/env-vault/pulls?state=open&per_page=100&page=2')
    if [ "$LIVE_DRIFT_PAGE2" = 1 ] && [ "${output##*/}" = page-002-final.json ]; then source_file="$LIVE_FIXTURES/source-pulls-page-002-drift.json"; else source_file="$LIVE_FIXTURES/source-pulls-page-002.json"; fi ;;
  'repos/ildarbinanas-design/homebrew-tap/pulls?state=open&per_page=100&page=1') source_file="$LIVE_FIXTURES/tap-pulls-page-001.json" ;;
  'repos/ildarbinanas-design/env-vault/pulls/'*) number=${endpoint##*/}; source_file="$LIVE_FIXTURES/pr-$number.json" ;;
  'repos/ildarbinanas-design/env-vault/actions/runs?per_page=100&page=1') source_file="$LIVE_FIXTURES/source-runs.json" ;;
  'repos/ildarbinanas-design/homebrew-tap/actions/runs?per_page=100&page=1') source_file="$LIVE_FIXTURES/tap-runs.json" ;;
  'repos/ildarbinanas-design/env-vault/contents/release/contract.v2.json?ref=%s') source_file="$LIVE_FIXTURES/contract.json" ;;
  'repos/ildarbinanas-design/env-vault/releases/latest')
    if [ "$LIVE_STABLE_PRESENT" = 1 ]; then source_file="$LIVE_FIXTURES/release.json"; else exit 4; fi ;;
  'repos/ildarbinanas-design/env-vault/releases/tags/v1.2.3') source_file="$LIVE_FIXTURES/release.json" ;;
  'repos/ildarbinanas-design/env-vault/git/ref/tags/v1.2.3') source_file="$LIVE_FIXTURES/tag-ref.json" ;;
  'repos/ildarbinanas-design/env-vault/git/tags/%s') source_file="$LIVE_FIXTURES/tag-object.json" ;;
  *) exit 91 ;;
esac
[ ! -e "$output" ]
cp "$source_file" "$output"
chmod 600 "$output"
`, mainSHA, strings.Repeat("8", 40))
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path, logFile
}

func writeCollectorJSON(t *testing.T, filename string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustReadCollector(t *testing.T, filename string) []byte {
	t.Helper()
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
