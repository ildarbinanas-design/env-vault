package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ildarbinanas-design/env-vault/internal/actionsartifact"
)

func TestCollectorUsesCheckedReadAdapterWithPrebuiltTransportAndExplicitStablePages(t *testing.T) {
	fixtures := writeCollectorFixtures(t)
	logFile := filepath.Join(t.TempDir(), "transport.log")
	fake := writeFakeTransport(t)
	// The collector still executes gh-api-read.sh. RELEASE_TRANSPORT_BIN is
	// consumed only by that checked wrapper, which normalizes every fake call to
	// the transport's read OUTPUT ENDPOINT contract.
	t.Setenv("RELEASE_TRANSPORT_BIN", fake)
	t.Setenv("FAKE_FIXTURES", fixtures)
	t.Setenv("FAKE_LOG", logFile)
	t.Setenv("FAKE_DRIFT", "0")
	t.Setenv("FAKE_MAX_TOTAL", "0")
	output := filepath.Join(t.TempDir(), "collection")
	var stdout, stderr bytes.Buffer
	if status := run(context.Background(), []string{"--output", output, "--repository", "example/env-vault"}, &stdout, &stderr); status != 0 {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout.String(), stderr.String())
	}
	if stdout.String() != "collected Actions artifacts: repositories=1 artifacts=101 runs=101 attempts=1\n" || stderr.Len() != 0 {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	logData, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(logData)), "\n")
	if len(lines) != 10 { // repository + initial/final pages for both resources + one attempt
		t.Fatalf("transport calls=%d\n%s", len(lines), logData)
	}
	pageCalls := map[string]int{}
	for _, line := range lines {
		fields := strings.Split(line, "\t")
		if len(fields) != 3 {
			t.Fatalf("malformed transport log line %q", line)
		}
		method, endpoint := fields[0], fields[1]
		if method != "read" || strings.Contains(endpoint, "/repositories/") || strings.Contains(endpoint, "--paginate") {
			t.Fatalf("non-read or canonical-link-unsafe call %q", line)
		}
		if strings.Contains(endpoint, "/actions/artifacts") || strings.Contains(endpoint, "/actions/runs?") {
			if !strings.Contains(endpoint, "per_page=100&page=") {
				t.Fatalf("paged endpoint is not explicit: %q", endpoint)
			}
		}
		if strings.Contains(endpoint, "/actions/artifacts?") || strings.Contains(endpoint, "/actions/runs?") {
			pageCalls[endpoint]++
		}
	}
	for _, endpoint := range []string{
		"repos/example/env-vault/actions/artifacts?per_page=100&page=1",
		"repos/example/env-vault/actions/artifacts?per_page=100&page=2",
		"repos/example/env-vault/actions/runs?per_page=100&page=1",
		"repos/example/env-vault/actions/runs?per_page=100&page=2",
	} {
		if pageCalls[endpoint] != 2 {
			t.Fatalf("page %q calls=%d want=2", endpoint, pageCalls[endpoint])
		}
	}
	policy, err := actionsartifact.LoadPolicyFile(filepath.Join("..", "..", actionsartifact.CanonicalPolicyPath))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := actionsartifact.AssembleSnapshot(output, policy)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ArtifactCount != 101 || snapshot.RunCount != 101 || snapshot.AttemptCount != 1 || snapshot.Repositories[0].Artifacts.PageItemCounts[1] != 1 {
		t.Fatalf("snapshot summary=%+v", snapshot)
	}
}

func TestCollectorRejectsStablePageMutationWithoutPublishingIndex(t *testing.T) {
	fixtures := writeCollectorFixtures(t)
	fake := writeFakeTransport(t)
	t.Setenv("RELEASE_TRANSPORT_BIN", fake)
	t.Setenv("FAKE_FIXTURES", fixtures)
	t.Setenv("FAKE_LOG", filepath.Join(t.TempDir(), "transport.log"))
	t.Setenv("FAKE_DRIFT", "1")
	t.Setenv("FAKE_MAX_TOTAL", "0")
	output := filepath.Join(t.TempDir(), "collection")
	var stdout, stderr bytes.Buffer
	if status := run(context.Background(), []string{"--output", output, "--repository", "example/env-vault"}, &stdout, &stderr); status != 1 {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "final run reread: page 2 changed during global final reread") {
		t.Fatalf("stderr=%q", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(output, "collection.json")); !os.IsNotExist(err) {
		t.Fatalf("collection index must not publish after drift: %v", err)
	}
	logData, err := os.ReadFile(os.Getenv("FAKE_LOG"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(logData)), "\n")
	attemptIndex, firstFinalIndex := -1, -1
	for index, line := range lines {
		fields := strings.Split(line, "\t")
		if len(fields) == 3 && strings.Contains(fields[1], "/attempts/1") {
			attemptIndex = index
		}
		if len(fields) == 3 && strings.Contains(fields[2], "-final.json") && firstFinalIndex == -1 {
			firstFinalIndex = index
		}
	}
	if attemptIndex == -1 || firstFinalIndex == -1 || attemptIndex >= firstFinalIndex {
		t.Fatalf("global final reread began before attempt fetch: %s", logData)
	}
}

func TestCollectorRejectsMaxIntTotalBeforePaginationArithmetic(t *testing.T) {
	fixtures := writeCollectorFixtures(t)
	t.Setenv("RELEASE_TRANSPORT_BIN", writeFakeTransport(t))
	t.Setenv("FAKE_FIXTURES", fixtures)
	t.Setenv("FAKE_LOG", filepath.Join(t.TempDir(), "transport.log"))
	t.Setenv("FAKE_DRIFT", "0")
	t.Setenv("FAKE_MAX_TOTAL", "1")
	output := filepath.Join(t.TempDir(), "collection")
	var stdout, stderr bytes.Buffer
	if status := run(context.Background(), []string{"--output", output, "--repository", "example/env-vault"}, &stdout, &stderr); status != 1 || !strings.Contains(stderr.String(), "exceeds item bound") {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(output, "collection.json")); !os.IsNotExist(err) {
		t.Fatalf("collection index must not publish after overflow input: %v", err)
	}
}

func writeFakeTransport(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-transport")
	script := `#!/bin/sh
set -eu
[ "$#" -eq 3 ]
[ "$1" = read ]
output=$2
endpoint=$3
printf '%s\t%s\t%s\n' "$1" "$endpoint" "${output##*/}" >> "$FAKE_LOG"
case "$endpoint" in
  'repos/example/env-vault') source_file="$FAKE_FIXTURES/repository.json" ;;
  'repos/example/env-vault/actions/artifacts?per_page=100&page=1')
    if [ "$FAKE_MAX_TOTAL" = 1 ]; then
      source_file="$FAKE_FIXTURES/artifacts-max-total.json"
    else
      source_file="$FAKE_FIXTURES/artifacts-page-001.json"
    fi ;;
  'repos/example/env-vault/actions/artifacts?per_page=100&page=2') source_file="$FAKE_FIXTURES/artifacts-page-002.json" ;;
  'repos/example/env-vault/actions/runs?per_page=100&page=1') source_file="$FAKE_FIXTURES/runs-page-001.json" ;;
  'repos/example/env-vault/actions/runs?per_page=100&page=2')
    if [ "$FAKE_DRIFT" = 1 ] && [ "${output##*/}" = page-002-final.json ]; then
      source_file="$FAKE_FIXTURES/runs-page-002-drift.json"
    else
      source_file="$FAKE_FIXTURES/runs-page-002.json"
    fi ;;
  'repos/example/env-vault/actions/runs/100/attempts/1') source_file="$FAKE_FIXTURES/attempt-100-1.json" ;;
  *) exit 91 ;;
esac
[ ! -e "$output" ]
cp "$source_file" "$output"
chmod 600 "$output"
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeCollectorFixtures(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	writeCollectorJSON(t, filepath.Join(directory, "repository.json"), map[string]any{"id": 10, "full_name": "example/env-vault"})
	artifacts := make([]any, 0, 101)
	for index := 0; index < 101; index++ {
		artifacts = append(artifacts, map[string]any{
			"id": int64(1000 + index), "name": "env-vault-release-linux-amd64-attempt-1",
			"digest": "sha256:" + strings.Repeat("f", 64), "size_in_bytes": index + 1,
			"created_at": "2026-01-01T00:05:00Z", "updated_at": "2026-01-01T00:05:00Z",
			"expires_at": "2030-01-01T00:00:00Z", "expired": false,
			"workflow_run": map[string]any{"id": 100, "repository_id": 10, "head_repository_id": 10, "head_branch": "main", "head_sha": strings.Repeat("a", 40)},
		})
	}
	pageOne := collectorJSON(t, map[string]any{"total_count": 101, "artifacts": artifacts[:100]})
	writeCollectorBytes(t, filepath.Join(directory, "artifacts-page-001.json"), pageOne)
	writeCollectorBytes(t, filepath.Join(directory, "artifacts-max-total.json"), []byte(`{"total_count":9223372036854775807,"artifacts":[]}`))
	writeCollectorJSON(t, filepath.Join(directory, "artifacts-page-002.json"), map[string]any{"total_count": 101, "artifacts": artifacts[100:]})

	runs := make([]any, 0, 101)
	for id := int64(100); id <= 200; id++ {
		runs = append(runs, collectorRun(id, 1))
	}
	writeCollectorJSON(t, filepath.Join(directory, "runs-page-001.json"), map[string]any{"total_count": 101, "workflow_runs": runs[:100]})
	runsPageTwo := collectorJSON(t, map[string]any{"total_count": 101, "workflow_runs": runs[100:]})
	writeCollectorBytes(t, filepath.Join(directory, "runs-page-002.json"), runsPageTwo)
	writeCollectorBytes(t, filepath.Join(directory, "runs-page-002-drift.json"), append(append([]byte(nil), runsPageTwo...), '\n'))
	writeCollectorJSON(t, filepath.Join(directory, "attempt-100-1.json"), collectorRun(100, 1))
	return directory
}

func collectorRun(id int64, attempt int) map[string]any {
	return map[string]any{
		"id": id, "run_attempt": attempt, "path": ".github/workflows/ci.yml", "head_sha": strings.Repeat("a", 40), "head_branch": "main",
		"event": "push", "status": "completed", "conclusion": "success", "created_at": "2026-01-01T00:00:00Z",
		"run_started_at": "2026-01-01T00:01:00Z", "updated_at": "2026-01-01T00:10:00Z",
		"repository":      map[string]any{"id": 10, "full_name": "example/env-vault"},
		"head_repository": map[string]any{"id": 10, "full_name": "example/env-vault"},
	}
}

func writeCollectorJSON(t *testing.T, filename string, value any) {
	t.Helper()
	writeCollectorBytes(t, filename, collectorJSON(t, value))
}

func collectorJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func writeCollectorBytes(t *testing.T, filename string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filename, data, 0o600); err != nil {
		t.Fatal(fmt.Errorf("write fixture: %w", err))
	}
}
