package tests

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	evidenceSourceSHA = "1111111111111111111111111111111111111111"
	evidenceBaseSHA   = "2222222222222222222222222222222222222222"
	evidenceNewSHA    = "5555555555555555555555555555555555555555"
	evidenceRunID     = int64(4242)
	evidenceAttempt   = 2
)

func TestPublishReleaseEvidenceIsNoClobberAndRaceSafe(t *testing.T) {
	tests := []struct {
		name             string
		mode             string
		wantStatus       int
		wantOutput       string
		wantBlobCreates  int
		wantTreeCreates  int
		wantCommitCreate int
		wantRefCreates   int
		wantRefUpdates   int
		wantReconcileGET int
		repairMode       string
		publisherEvent   string
		ledgerDepth      int
	}{
		{
			name:           "missing evidence branch fails before every mutation",
			mode:           "missing",
			wantStatus:     1,
			wantOutput:     "evidence reference must be bootstrapped at the exact source SHA",
			repairMode:     "none",
			publisherEvent: "push",
		},
		{
			name:             "bootstrapped source ref receives the first evidence commit",
			mode:             "bootstrap",
			wantOutput:       evidencePublishOutput(evidenceNewSHA, "updated", "none"),
			wantBlobCreates:  4,
			wantTreeCreates:  1,
			wantCommitCreate: 1,
			wantRefUpdates:   1,
			repairMode:       "none",
			publisherEvent:   "push",
		},
		{
			name:             "ambiguous blob create is reconciled by deterministic identity without replay",
			mode:             "post-blob-timeout",
			wantOutput:       evidencePublishOutput(evidenceNewSHA, "updated", "none"),
			wantBlobCreates:  4,
			wantTreeCreates:  1,
			wantCommitCreate: 1,
			wantRefUpdates:   1,
			wantReconcileGET: 1,
			repairMode:       "none",
			publisherEvent:   "push",
		},
		{
			name:             "ambiguous blob create with mismatched remote bytes fails before tree and ref",
			mode:             "post-blob-timeout-mismatch",
			wantStatus:       1,
			wantOutput:       "cannot reconcile ambiguous evidence blob creation",
			wantBlobCreates:  1,
			wantReconcileGET: 1,
			repairMode:       "none",
			publisherEvent:   "push",
		},
		{
			name:             "ambiguous blob create with unknown remote outcome fails before tree and ref",
			mode:             "post-blob-timeout-unknown",
			wantStatus:       1,
			wantOutput:       "cannot reconcile ambiguous evidence blob creation",
			wantBlobCreates:  1,
			wantReconcileGET: 1,
			repairMode:       "none",
			publisherEvent:   "push",
		},
		{
			name:             "initialized ledger receives the first tuple for a new version",
			mode:             "new-version",
			wantOutput:       evidencePublishOutput(evidenceNewSHA, "updated", "none"),
			wantBlobCreates:  4,
			wantTreeCreates:  1,
			wantCommitCreate: 1,
			wantRefUpdates:   1,
			repairMode:       "none",
			publisherEvent:   "push",
		},
		{
			name:           "byte-identical evidence is an exact no-op",
			mode:           "noop",
			wantOutput:     evidencePublishOutput(evidenceBaseSHA, "unchanged", "none"),
			repairMode:     "none",
			publisherEvent: "push",
		},
		{
			name:           "depth 64 exact tuple remains readable",
			mode:           "depth64-noop",
			wantOutput:     evidencePublishOutput(evidenceBaseSHA, "unchanged", "none"),
			repairMode:     "none",
			publisherEvent: "push",
			ledgerDepth:    64,
		},
		{
			name:           "depth 64 missing tuple fails before mutation",
			mode:           "depth64-missing",
			wantStatus:     1,
			wantOutput:     "64-commit validation bound",
			repairMode:     "none",
			publisherEvent: "push",
			ledgerDepth:    64,
		},
		{
			name:             "depth 63 permits the final bounded append",
			mode:             "depth63-append",
			wantOutput:       evidencePublishOutput(evidenceNewSHA, "updated", "none"),
			wantBlobCreates:  4,
			wantTreeCreates:  1,
			wantCommitCreate: 1,
			wantRefUpdates:   1,
			repairMode:       "none",
			publisherEvent:   "push",
			ledgerDepth:      63,
		},
		{
			name:           "tree response identity mismatch fails before mutation",
			mode:           "wrong-tree-sha",
			wantStatus:     1,
			wantOutput:     "invalid or truncated tree",
			repairMode:     "none",
			publisherEvent: "push",
		},
		{
			name:            "returned tree cannot drop older evidence before ref update",
			mode:            "dropped-history-tree",
			wantStatus:      1,
			wantOutput:      "rewrote or removed an earlier controlled blob",
			wantBlobCreates: 4,
			wantTreeCreates: 1,
			repairMode:      "none",
			publisherEvent:  "push",
		},
		{
			name:           "mismatch fails before every mutation",
			mode:           "mismatch",
			wantStatus:     1,
			wantOutput:     "published evidence differs for release-evidence.json",
			repairMode:     "none",
			publisherEvent: "push",
		},
		{
			name:             "repair appends a new immutable publisher tuple without comparing root bytes",
			mode:             "append",
			wantOutput:       evidencePublishOutput(evidenceNewSHA, "updated", "health"),
			wantBlobCreates:  4,
			wantTreeCreates:  1,
			wantCommitCreate: 1,
			wantRefUpdates:   1,
			repairMode:       "health",
			publisherEvent:   "workflow_dispatch",
		},
		{
			name:             "legacy repair preserves another complete v2 version and object store",
			mode:             "mixed-v2-append",
			wantOutput:       evidencePublishOutput(evidenceNewSHA, "updated", "health"),
			wantBlobCreates:  4,
			wantTreeCreates:  1,
			wantCommitCreate: 1,
			wantRefUpdates:   1,
			repairMode:       "health",
			publisherEvent:   "workflow_dispatch",
		},
		{
			name:            "malformed wrapped blob is rejected before tree or ref mutation",
			mode:            "invalid-base64",
			wantStatus:      1,
			wantOutput:      "published evidence differs for release-evidence.json",
			wantBlobCreates: 1,
			repairMode:      "none",
			publisherEvent:  "push",
		},
		{
			name:            "trailing base64 garbage is rejected before tree or ref mutation",
			mode:            "trailing-base64-garbage",
			wantStatus:      1,
			wantOutput:      "published evidence differs for release-evidence.json",
			wantBlobCreates: 1,
			repairMode:      "none",
			publisherEvent:  "push",
		},
		{
			name:            "missing base64 padding is rejected before tree or ref mutation",
			mode:            "missing-base64-padding",
			wantStatus:      1,
			wantOutput:      "published evidence differs for release-evidence.json",
			wantBlobCreates: 1,
			repairMode:      "none",
			publisherEvent:  "push",
		},
		{
			name:            "extra base64 padding is rejected before tree or ref mutation",
			mode:            "extra-base64-padding",
			wantStatus:      1,
			wantOutput:      "published evidence differs for release-evidence.json",
			wantBlobCreates: 1,
			repairMode:      "none",
			publisherEvent:  "push",
		},
		{
			name:            "noncanonical base64 pad bits are rejected before tree or ref mutation",
			mode:            "noncanonical-base64-pad-bits",
			wantStatus:      1,
			wantOutput:      "published evidence differs for release-evidence.json",
			wantBlobCreates: 1,
			repairMode:      "none",
			publisherEvent:  "push",
		},
		{
			name:             "concurrent fast-forward race is never forced",
			mode:             "race",
			wantStatus:       1,
			wantOutput:       "cannot fast-forward evidence reference",
			wantBlobCreates:  4,
			wantTreeCreates:  1,
			wantCommitCreate: 1,
			wantRefUpdates:   1,
			repairMode:       "health",
			publisherEvent:   "workflow_dispatch",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			localDir := filepath.Join(root, "local")
			remoteDir := filepath.Join(root, "remote")
			binDir := filepath.Join(root, "bin")
			callLog := filepath.Join(root, "gh-calls.log")
			refState := filepath.Join(root, "ref-state")
			blobCount := filepath.Join(root, "blob-count")
			makeDirectory(t, localDir)
			makeDirectory(t, remoteDir)
			makeDirectory(t, binDir)

			files := writeEvidencePublicationFixturesForPublisher(t, localDir, test.repairMode, test.publisherEvent)
			installEvidenceAPIFakeGH(t, binDir)
			if test.mode == "noop" || test.mode == "depth64-noop" || test.mode == "mismatch" {
				seedEvidenceRemoteBlobs(t, remoteDir, files, test.mode == "mismatch")
			}
			chainFile := writeLegacyEvidenceChainFixture(t, root, test.ledgerDepth)

			output, status := runReleaseScript(
				t,
				"../scripts/release/publish-release-evidence.sh",
				[]string{
					releaseTestVersion,
					evidenceSourceSHA,
					releaseTestRepository,
					files[0], files[1], files[2], files[3],
				},
				map[string]string{
					"FAKE_EVIDENCE_MODE":         test.mode,
					"FAKE_EVIDENCE_BLOB_SHAS":    strings.Join(evidenceBlobSHAs(t, files), ","),
					"FAKE_GH_CALL_LOG":           callLog,
					"FAKE_GH_REMOTE_DIR":         remoteDir,
					"FAKE_GH_REF_STATE":          refState,
					"FAKE_GH_BLOB_COUNT_STATE":   blobCount,
					"FAKE_EVIDENCE_CHAIN_FILE":   chainFile,
					"FAKE_GH_CREATED_TREE_STATE": filepath.Join(root, "created-tree.json"),
					"PATH":                       binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
					"TMPDIR":                     root,
				},
			)
			if status != test.wantStatus {
				t.Fatalf("exit status=%d, want %d\n%s", status, test.wantStatus, output)
			}
			if test.wantStatus == 0 {
				if output != test.wantOutput {
					t.Fatalf("output=%q, want %q", output, test.wantOutput)
				}
			} else if !strings.Contains(output, test.wantOutput) {
				t.Fatalf("output does not contain %q:\n%s", test.wantOutput, output)
			}

			calls := readOptionalFile(t, callLog)
			if strings.Contains(calls, "--method DELETE") || strings.Contains(calls, "force=true") || strings.Contains(calls, "--force") {
				t.Fatalf("evidence publication attempted a destructive ref operation:\n%s", calls)
			}
			assertEvidenceCallCount(t, calls, "--method POST repos/example/env-vault/git/blobs", test.wantBlobCreates)
			assertEvidenceCallCount(t, calls, "--method POST repos/example/env-vault/git/trees", test.wantTreeCreates)
			assertEvidenceCallCount(t, calls, "--method POST repos/example/env-vault/git/commits", test.wantCommitCreate)
			assertEvidenceCallCount(t, calls, "--method POST repos/example/env-vault/git/refs ", test.wantRefCreates)
			assertEvidenceCallCount(t, calls, "--method PATCH repos/example/env-vault/git/refs/heads/release-evidence", test.wantRefUpdates)
			if strings.HasPrefix(test.mode, "post-blob-timeout") {
				firstSHA := evidenceBlobSHAs(t, files)[0]
				assertEvidenceCallCount(t, calls, "repos/example/env-vault/git/blobs/"+firstSHA, test.wantReconcileGET)
			}

			if test.mode == "bootstrap" || test.mode == "post-blob-timeout" || test.mode == "new-version" || test.mode == "append" || test.mode == "depth63-append" {
				for index, sha := range evidenceBlobSHAs(t, files) {
					remote, err := os.ReadFile(filepath.Join(remoteDir, sha))
					if err != nil {
						t.Fatalf("read created remote blob %d: %v", index, err)
					}
					local, err := os.ReadFile(files[index])
					if err != nil {
						t.Fatalf("read local evidence %d: %v", index, err)
					}
					if string(remote) != string(local) {
						t.Fatalf("created remote blob %d differs", index)
					}
				}
				if _, err := os.Stat(refState); err != nil {
					t.Fatalf("created reference state is missing: %v", err)
				}
			}
			if test.mode == "race" {
				if _, err := os.Stat(refState); !os.IsNotExist(err) {
					t.Fatalf("race must not change reference state: %v", err)
				}
			}
			if test.mode == "mixed-v2-append" {
				assertMixedV2HistoryPreserved(t, filepath.Join(root, "created-tree.json"))
			}
		})
	}
}

func assertMixedV2HistoryPreserved(t *testing.T, treePath string) {
	t.Helper()
	data, err := os.ReadFile(treePath)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Tree []struct {
			Path string `json:"path"`
			Mode string `json:"mode"`
			Type string `json:"type"`
			SHA  string `json:"sha"`
		} `json:"tree"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{}
	names := []string{"index.md", "metrics-comparison.json", "metrics-comparison.md", "parity.json", "release-evidence-bundle.json", "storage-metrics.json"}
	for index, name := range names {
		sha := strings.Repeat(string(rune('a'+index)), 40)
		want["evidence/releases/v1.2.2/"+name] = sha
		want["evidence/releases/v1.2.2/publisher-runs/run-51/attempt-1/"+name] = sha
	}
	want["evidence/objects/sha256/"+strings.Repeat("a", 64)+".gz"] = strings.Repeat("f", 40)
	for _, entry := range document.Tree {
		if sha, expected := want[entry.Path]; expected {
			if entry.Type != "blob" || entry.Mode != "100644" || entry.SHA != sha {
				t.Fatalf("mixed v2 entry changed: %+v want sha=%s", entry, sha)
			}
			delete(want, entry.Path)
		}
	}
	if len(want) != 0 {
		t.Fatalf("mixed v2 entries disappeared: %v", want)
	}
}

func TestPublishReleaseEvidenceRejectsLocalSymlinkBeforeTransport(t *testing.T) {
	root := t.TempDir()
	localDir := filepath.Join(root, "local")
	makeDirectory(t, localDir)
	files := writeEvidencePublicationFixtures(t, localDir)
	symlink := filepath.Join(root, "evidence-link.json")
	if err := os.Symlink(files[0], symlink); err != nil {
		t.Fatalf("create evidence symlink: %v", err)
	}
	callLog := filepath.Join(root, "gh-calls.log")
	output, status := runReleaseScript(
		t,
		"../scripts/release/publish-release-evidence.sh",
		[]string{releaseTestVersion, evidenceSourceSHA, releaseTestRepository, symlink, files[1], files[2], files[3]},
		map[string]string{"FAKE_GH_CALL_LOG": callLog, "TMPDIR": root},
	)
	if status != 1 || !strings.Contains(output, "expected a regular file") {
		t.Fatalf("status=%d output=%s", status, output)
	}
	if calls := readOptionalFile(t, callLog); calls != "" {
		t.Fatalf("local validation failure reached transport:\n%s", calls)
	}
}

func TestPublishReleaseEvidenceRejectsMalformedLineageBeforeMutation(t *testing.T) {
	for _, mode := range []string{"partial", "unexpected", "case-collision", "unsafe-numeric"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			localDir := filepath.Join(root, "local")
			remoteDir := filepath.Join(root, "remote")
			binDir := filepath.Join(root, "bin")
			makeDirectory(t, localDir)
			makeDirectory(t, remoteDir)
			makeDirectory(t, binDir)
			files := writeEvidencePublicationFixtures(t, localDir)
			installEvidenceAPIFakeGH(t, binDir)
			callLog := filepath.Join(root, "gh-calls.log")

			output, status := runReleaseScript(
				t,
				"../scripts/release/publish-release-evidence.sh",
				[]string{releaseTestVersion, evidenceSourceSHA, releaseTestRepository, files[0], files[1], files[2], files[3]},
				map[string]string{
					"FAKE_EVIDENCE_MODE":       mode,
					"FAKE_EVIDENCE_BLOB_SHAS":  strings.Join(evidenceBlobSHAs(t, files), ","),
					"FAKE_GH_CALL_LOG":         callLog,
					"FAKE_GH_REMOTE_DIR":       remoteDir,
					"FAKE_GH_REF_STATE":        filepath.Join(root, "ref-state"),
					"FAKE_GH_BLOB_COUNT_STATE": filepath.Join(root, "blob-count"),
					"PATH":                     binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
					"TMPDIR":                   root,
				},
			)
			if status != 1 || !strings.Contains(output, "remote release evidence lineage is partial, conflicting, unsafe, or incompatible") {
				t.Fatalf("status=%d output=%s", status, output)
			}
			calls := readOptionalFile(t, callLog)
			if strings.Contains(calls, "--method POST") || strings.Contains(calls, "--method PATCH") || strings.Contains(calls, "--method DELETE") {
				t.Fatalf("malformed lineage reached mutation transport:\n%s", calls)
			}
		})
	}
}

func TestPublishReleaseEvidenceRejectsInvalidPublisherIdentityBeforeTransport(t *testing.T) {
	tests := []struct {
		name        string
		repairMode  string
		event       string
		replacement string
	}{
		{name: "repair event mismatch", repairMode: "health", event: "push"},
		{name: "unknown repair mode", repairMode: "Health", event: "workflow_dispatch"},
		{name: "zero run ID", repairMode: "none", event: "push", replacement: `"run_id":0`},
		{name: "zero attempt", repairMode: "none", event: "push", replacement: `"attempt":0`},
		{name: "unsafe run ID", repairMode: "none", event: "push", replacement: `"run_id":9007199254740992`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			localDir := filepath.Join(root, "local")
			binDir := filepath.Join(root, "bin")
			makeDirectory(t, localDir)
			makeDirectory(t, binDir)
			files := writeEvidencePublicationFixturesForPublisher(t, localDir, test.repairMode, test.event)
			if test.replacement != "" {
				contents, err := os.ReadFile(files[0])
				if err != nil {
					t.Fatal(err)
				}
				field := `"run_id":4242`
				if strings.Contains(test.replacement, `"attempt"`) {
					field = `"attempt":2`
				}
				contents = []byte(strings.Replace(string(contents), field, test.replacement, 1))
				if err := os.WriteFile(files[0], contents, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			installEvidenceAPIFakeGH(t, binDir)
			callLog := filepath.Join(root, "gh-calls.log")
			output, status := runReleaseScript(
				t,
				"../scripts/release/publish-release-evidence.sh",
				[]string{releaseTestVersion, evidenceSourceSHA, releaseTestRepository, files[0], files[1], files[2], files[3]},
				map[string]string{
					"FAKE_EVIDENCE_MODE":       "noop",
					"FAKE_GH_CALL_LOG":         callLog,
					"FAKE_GH_REMOTE_DIR":       filepath.Join(root, "remote"),
					"FAKE_GH_REF_STATE":        filepath.Join(root, "ref-state"),
					"FAKE_GH_BLOB_COUNT_STATE": filepath.Join(root, "blob-count"),
					"PATH":                     binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
					"TMPDIR":                   root,
				},
			)
			if status != 1 || !strings.Contains(output, "release evidence JSON identity, publisher tuple, or schema is invalid") {
				t.Fatalf("status=%d output=%s", status, output)
			}
			if calls := readOptionalFile(t, callLog); calls != "" {
				t.Fatalf("invalid publisher identity reached transport:\n%s", calls)
			}
		})
	}
}

func writeEvidencePublicationFixtures(t *testing.T, directory string) []string {
	t.Helper()
	return writeEvidencePublicationFixturesForPublisher(t, directory, "none", "push")
}

func writeEvidencePublicationFixturesForPublisher(t *testing.T, directory, repairMode, publisherEvent string) []string {
	t.Helper()
	evidence := fmt.Sprintf(`{"schema_id":"env-vault.release-evidence.v1","schema_version":1,"repository":"%s","release_version":"%s","source_sha":"%s","result":"pass","publisher_repair_mode":"%s","publisher_metrics":{"schema_id":"env-vault.release-metrics.v1","run_id":%d,"attempt":%d,"workflow_name":"build-binaries","event":"%s","head_sha":"%s","conclusion":"success"},"evidence_sha256":"%s"}
`, releaseTestRepository, releaseTestVersion, evidenceSourceSHA, repairMode, evidenceRunID, evidenceAttempt, publisherEvent, evidenceSourceSHA, strings.Repeat("e", 64))
	index := fmt.Sprintf("# env-vault %s release evidence\n\n- Result: `pass`\n- Source SHA: `%s`\n", releaseTestVersion, evidenceSourceSHA)
	comparison := fmt.Sprintf(`{"schema_id":"env-vault.release-metrics-comparison.v1","schema_version":1,"scenarios":[{"scenario":"main_ci","current_head_sha":"%s"},{"scenario":"pr_ci","current_head_sha":"9999999999999999999999999999999999999999"},{"scenario":"publisher","current_head_sha":"%s"}]}
`, evidenceSourceSHA, evidenceSourceSHA)
	markdown := "# Release pipeline metrics comparison\n\n| Scenario | Jobs |\n|---|---:|\n"
	contents := []string{evidence, index, comparison, markdown}
	names := []string{"release-evidence.json", "index.md", "metrics-comparison.json", "metrics-comparison.md"}
	paths := make([]string, len(names))
	for index, name := range names {
		paths[index] = filepath.Join(directory, name)
		if err := os.WriteFile(paths[index], []byte(contents[index]), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return paths
}

func evidencePublishOutput(commitSHA, state, repairMode string) string {
	return fmt.Sprintf(
		"commit_sha=%s\nbranch=release-evidence\nstate=%s\npath_prefix=evidence/releases/%s/publisher-runs/run-%d/attempt-%d\npublisher_run_id=%d\npublisher_run_attempt=%d\nrepair_mode=%s\n",
		commitSHA, state, releaseTestVersion, evidenceRunID, evidenceAttempt, evidenceRunID, evidenceAttempt, repairMode,
	)
}

func seedEvidenceRemoteBlobs(t *testing.T, directory string, local []string, mismatch bool) {
	t.Helper()
	for index, sha := range evidenceBlobSHAs(t, local) {
		contents, err := os.ReadFile(local[index])
		if err != nil {
			t.Fatalf("read local evidence fixture: %v", err)
		}
		if mismatch && index == 0 {
			contents = []byte("different remote evidence\n")
		}
		if err := os.WriteFile(filepath.Join(directory, sha), contents, 0o644); err != nil {
			t.Fatalf("write remote blob fixture: %v", err)
		}
	}
}

func evidenceBlobSHAs(t *testing.T, files []string) []string {
	t.Helper()
	shas := make([]string, len(files))
	for index, path := range files {
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		object := append([]byte(fmt.Sprintf("blob %d\x00", len(contents))), contents...)
		digest := sha1.Sum(object)
		shas[index] = hex.EncodeToString(digest[:])
	}
	return shas
}

func assertEvidenceCallCount(t *testing.T, calls, fragment string, want int) {
	t.Helper()
	if got := strings.Count(calls, fragment); got != want {
		t.Fatalf("calls containing %q=%d, want %d\n%s", fragment, got, want, calls)
	}
}

func installEvidenceAPIFakeGH(t *testing.T, binDir string) {
	t.Helper()
	script := `#!/usr/bin/env bash
set -euo pipefail

if [[ ${1:-} == --version ]]; then
  printf 'gh version 2.80.0 (2026-01-01)\n'
  exit 0
fi
if [[ ${1:-} == api && ${2:-} == --help ]]; then
  printf '%s\n' 'OPTIONS: --include --hostname --method --header --raw-field'
  exit 0
fi
mode=${FAKE_EVIDENCE_MODE:?FAKE_EVIDENCE_MODE is required}
call_log=${FAKE_GH_CALL_LOG:?FAKE_GH_CALL_LOG is required}
remote_dir=${FAKE_GH_REMOTE_DIR:?FAKE_GH_REMOTE_DIR is required}
ref_state=${FAKE_GH_REF_STATE:?FAKE_GH_REF_STATE is required}
blob_count_state=${FAKE_GH_BLOB_COUNT_STATE:?FAKE_GH_BLOB_COUNT_STATE is required}
chain_file=${FAKE_EVIDENCE_CHAIN_FILE:-}
created_tree_state=${FAKE_GH_CREATED_TREE_STATE:-}
IFS=, read -r blob_sha_1 blob_sha_2 blob_sha_3 blob_sha_4 <<< "${FAKE_EVIDENCE_BLOB_SHAS:?}"
blob_shas_json=$(jq -cn --args '$ARGS.positional' "$blob_sha_1" "$blob_sha_2" "$blob_sha_3" "$blob_sha_4")
printf '%s\n' "$*" >> "$call_log"

[[ ${1:-} == api ]] || {
  printf 'fake gh: only api transport is allowed\n' >&2
  exit 90
}
shift

method=GET
include=false
input=''
endpoint=''
while (($#)); do
  case "$1" in
    --include) include=true; shift ;;
    --hostname|--header) shift 2 ;;
    --method) method=${2:-}; shift 2 ;;
    --input) input=${2:-}; shift 2 ;;
    -*) printf 'fake gh: unsupported option: %s\n' "$1" >&2; exit 91 ;;
    *)
      [[ -z $endpoint ]] || { printf 'fake gh: multiple endpoints\n' >&2; exit 91; }
      endpoint=$1
      shift
      ;;
  esac
done

source_sha=1111111111111111111111111111111111111111
source_tree=1010101010101010101010101010101010101010
base_sha=2222222222222222222222222222222222222222
base_tree=3333333333333333333333333333333333333333
new_tree=4444444444444444444444444444444444444444
new_sha=5555555555555555555555555555555555555555
full_ref=refs/heads/release-evidence

branch_present=true
if [[ $mode == missing && ! -f $ref_state ]]; then
  branch_present=false
fi

if [[ $include == true && $method == GET && $endpoint == repos/example/env-vault/git/ref/heads/release-evidence && $branch_present == false ]]; then
  printf 'HTTP/2 404 Not Found\r\nContent-Type: application/vnd.github+json\r\nX-GitHub-Api-Version-Selected: 2022-11-28\r\n\r\n{"message":"Not Found"}\n'
  exit 1
fi

if [[ $include == true ]]; then
  transport_tmp=$(mktemp "${TMPDIR:-/tmp}/fake-evidence-gh.XXXXXX")
  exec 3>&1
  exec >"$transport_tmp"
  finish_transport() {
    status=$?
    trap - EXIT
    exec 1>&3
    if [[ $status == 0 ]]; then
      printf 'HTTP/2 200 OK\r\nContent-Type: application/vnd.github+json; charset=utf-8\r\nX-GitHub-Api-Version-Selected: 2022-11-28\r\n\r\n'
    elif [[ $status == 44 ]]; then
      printf 'HTTP/2 404 Not Found\r\nContent-Type: application/vnd.github+json; charset=utf-8\r\nX-GitHub-Api-Version-Selected: 2022-11-28\r\n\r\n'
    fi
    cat -- "$transport_tmp"
    rm -f -- "$transport_tmp"
    if [[ $status == 44 ]]; then
      exit 1
    fi
    exit "$status"
  }
  trap finish_transport EXIT
fi

emit_ref() {
  local sha=$base_sha
  case $mode in
    bootstrap|post-blob-timeout*|invalid-base64|trailing-base64-garbage|missing-base64-padding|extra-base64-padding|noncanonical-base64-pad-bits)
      sha=$source_sha
      ;;
  esac
  if [[ -f $ref_state ]]; then
    sha=$new_sha
  fi
  jq -n --arg ref "$full_ref" --arg sha "$sha" '{ref:$ref,object:{type:"commit",sha:$sha}}'
}

emit_commit() {
  local sha=$1 tree=$2 parent=$3
  if [[ -n $parent ]]; then
    jq -n --arg sha "$sha" --arg tree "$tree" --arg parent "$parent" '{sha:$sha,tree:{sha:$tree},parents:[{sha:$parent}]}'
  else
    jq -n --arg sha "$sha" --arg tree "$tree" '{sha:$sha,tree:{sha:$tree},parents:[]}'
  fi
}

emit_evidence_tree() {
  local variant=$1 tree_sha=$2
  jq -n --arg variant "$variant" --arg tree_sha "$tree_sha" --argjson current_shas "$blob_shas_json" '
    def dir($path): {path:$path,mode:"040000",type:"tree",sha:"0000000000000000000000000000000000000000"};
    def blob($path;$sha): {path:$path,mode:"100644",type:"blob",sha:$sha};
    def files($prefix;$shas): [
      blob($prefix + "/release-evidence.json";$shas[0]),
      blob($prefix + "/index.md";$shas[1]),
      blob($prefix + "/metrics-comparison.json";$shas[2]),
      blob($prefix + "/metrics-comparison.md";$shas[3])
    ];
    def v2files($prefix;$shas): [
      blob($prefix + "/index.md";$shas[0]),
      blob($prefix + "/metrics-comparison.json";$shas[1]),
      blob($prefix + "/metrics-comparison.md";$shas[2]),
      blob($prefix + "/parity.json";$shas[3]),
      blob($prefix + "/release-evidence-bundle.json";$shas[4]),
      blob($prefix + "/storage-metrics.json";$shas[5])
    ];
    ["6666666666666666666666666666666666666666","7777777777777777777777777777777777777777",
     "8888888888888888888888888888888888888888","9999999999999999999999999999999999999999"] as $prior_shas |
    "evidence/releases/v1.2.3" as $version |
    "evidence/releases/v1.2.2" as $previous_version |
	["aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
	 "cccccccccccccccccccccccccccccccccccccccc","dddddddddddddddddddddddddddddddddddddddd",
	 "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee","ffffffffffffffffffffffffffffffffffffffff"] as $v2_shas |
    [dir("evidence"),dir("evidence/releases"),dir($version)] as $ancestors |
    (files($version;$current_shas)) as $current_root |
    (files($version;$prior_shas)) as $prior_root |
    ([dir($version + "/publisher-runs"),
      dir($version + "/publisher-runs/run-4242"),
      dir($version + "/publisher-runs/run-4242/attempt-2")] +
      files($version + "/publisher-runs/run-4242/attempt-2";$current_shas)) as $current |
    ([dir($version + "/publisher-runs"),
      dir($version + "/publisher-runs/run-41"),
      dir($version + "/publisher-runs/run-41/attempt-1")] +
      files($version + "/publisher-runs/run-41/attempt-1";$prior_shas)) as $prior |
    ([dir($previous_version)] + files($previous_version;$prior_shas) +
      [dir($previous_version + "/publisher-runs"),
       dir($previous_version + "/publisher-runs/run-41"),
       dir($previous_version + "/publisher-runs/run-41/attempt-1")] +
      files($previous_version + "/publisher-runs/run-41/attempt-1";$prior_shas)) as $previous |
	([dir($previous_version)] + v2files($previous_version;$v2_shas) +
	 [dir($previous_version + "/publisher-runs"),
	  dir($previous_version + "/publisher-runs/run-51"),
	  dir($previous_version + "/publisher-runs/run-51/attempt-1")] +
	 v2files($previous_version + "/publisher-runs/run-51/attempt-1";$v2_shas) +
	 [dir("evidence/objects"),dir("evidence/objects/sha256"),
	  blob("evidence/objects/sha256/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.gz";
	       "ffffffffffffffffffffffffffffffffffffffff")]) as $v2_previous |
    ($prior + [dir($version + "/publisher-runs/run-4242"),dir($version + "/publisher-runs/run-4242/attempt-2")] +
      files($version + "/publisher-runs/run-4242/attempt-2";$current_shas)) as $appended |
    if $variant == "current" then {truncated:false,tree:($ancestors + $current_root + $current)}
    elif $variant == "prior" then {truncated:false,tree:($ancestors + $prior_root + $prior)}
    elif $variant == "appended" then {truncated:false,tree:($ancestors + $prior_root + $appended)}
    elif $variant == "previous-version" then
      {truncated:false,tree:([dir("evidence"),dir("evidence/releases")] + $previous)}
    elif $variant == "new-version" then
      {truncated:false,tree:($ancestors + $previous + $current_root + $current)}
	elif $variant == "mixed-base" then
	  {truncated:false,tree:($ancestors + $previous + $prior_root + $prior)}
	elif $variant == "mixed-v2-base" then
	  {truncated:false,tree:($ancestors + $v2_previous + $prior_root + $prior)}
	elif $variant == "mixed-v2-appended" then
	  {truncated:false,tree:($ancestors + $v2_previous + $prior_root + $appended)}
	elif $variant == "dropped-history" then
	  {truncated:false,tree:($ancestors + $prior_root + $appended)}
    elif $variant == "partial" then {truncated:false,tree:($ancestors + $current_root + $current[0:-1])}
    elif $variant == "unexpected" then
      {truncated:false,tree:($ancestors + $current_root + $current +
        [blob($version + "/publisher-runs/run-4242/attempt-2/notes.txt";$current_shas[0])])}
    elif $variant == "case-collision" then
      {truncated:false,tree:($ancestors + $current_root + $current +
        [blob($version + "/publisher-runs/run-4242/attempt-2/Index.md";$current_shas[1])])}
    elif $variant == "unsafe-numeric" then
      {truncated:false,tree:($ancestors + $prior_root +
        [dir($version + "/publisher-runs"),dir($version + "/publisher-runs/run-01"),
         dir($version + "/publisher-runs/run-01/attempt-1")] +
        files($version + "/publisher-runs/run-01/attempt-1";$prior_shas))}
    else error("unsupported tree variant")
    end | . + {sha:$tree_sha}
  '
}

emit_created_evidence_tree() {
  if [[ -n $created_tree_state ]]; then
    emit_evidence_tree "$1" "$2" | tee "$created_tree_state"
  else
    emit_evidence_tree "$1" "$2"
  fi
}

if [[ $method == GET && $endpoint =~ ^repos/example/env-vault/git/commits/([0-9a-f]{40})$ && -n $chain_file && -f $chain_file ]]; then
  chain_commit=${BASH_REMATCH[1]}
  if jq -e --arg sha "$chain_commit" 'has($sha)' "$chain_file" >/dev/null; then
    chain_parent=$(jq -er --arg sha "$chain_commit" '.[$sha]' "$chain_file")
    emit_commit "$chain_commit" "$base_tree" "$chain_parent"
    exit 0
  fi
fi

case "$method:$endpoint" in
  GET:repos/example/env-vault/git/ref/heads/release-evidence)
    [[ $branch_present == true ]] || exit 93
    emit_ref
    ;;
  GET:repos/example/env-vault/git/commits/$source_sha)
    emit_commit "$source_sha" "$source_tree" ''
    ;;
  GET:repos/example/env-vault/git/commits/$base_sha)
    emit_commit "$base_sha" "$base_tree" "$source_sha"
    ;;
  GET:repos/example/env-vault/git/commits/$new_sha)
    if [[ $mode == bootstrap || $mode == post-blob-timeout* ]]; then parent=$source_sha; else parent=$base_sha; fi
    emit_commit "$new_sha" "$new_tree" "$parent"
    ;;
  GET:repos/example/env-vault/git/trees/$source_tree?recursive=1)
    jq -n --arg sha "$source_tree" '{sha:$sha,truncated:false,tree:[]}'
    ;;
  GET:repos/example/env-vault/git/trees/$base_tree?recursive=1)
    case $mode in
      noop|depth64-noop|mismatch) emit_evidence_tree current "$base_tree" ;;
      new-version) emit_evidence_tree previous-version "$base_tree" ;;
      append|race|depth63-append) emit_evidence_tree prior "$base_tree" ;;
	  mixed-v2-append) emit_evidence_tree mixed-v2-base "$base_tree" ;;
      depth64-missing) emit_evidence_tree previous-version "$base_tree" ;;
      wrong-tree-sha) emit_evidence_tree prior "7777777777777777777777777777777777777777" ;;
	  dropped-history-tree) emit_evidence_tree mixed-base "$base_tree" ;;
      partial|unexpected|case-collision|unsafe-numeric) emit_evidence_tree "$mode" "$base_tree" ;;
      *) jq -n --arg sha "$base_tree" '{sha:$sha,truncated:false,tree:[]}' ;;
    esac
    ;;
  GET:repos/example/env-vault/git/trees/$new_tree?recursive=1)
    case $mode in
      bootstrap|post-blob-timeout*) emit_created_evidence_tree current "$new_tree" ;;
      new-version) emit_created_evidence_tree new-version "$new_tree" ;;
	  mixed-v2-append) emit_created_evidence_tree mixed-v2-appended "$new_tree" ;;
	  dropped-history-tree) emit_created_evidence_tree dropped-history "$new_tree" ;;
      *) emit_created_evidence_tree appended "$new_tree" ;;
    esac
    ;;
  GET:repos/example/env-vault/git/blobs/*)
    sha=${endpoint##*/}
    path=$remote_dir/$sha
    if [[ ! -f $path || -L $path ]]; then
      jq -n '{message:"Not Found"}'
      exit 44
    fi
    size=$(wc -c < "$path" | tr -d '[:space:]')
    jq -n --arg sha "$sha" --rawfile content "$path" --argjson size "$size" --arg mode "$mode" '
      def wrap:
        . as $value |
        [range(0; ($value | length); 60) as $offset | $value[$offset:$offset + 60]] |
        join("\n") + "\n";
      def noncanonical_pad_bits:
        . as $value |
        "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/" as $alphabet |
        ($value | length) as $length |
        if ($value | endswith("==")) then
          ($value[$length - 3:$length - 2]) as $character |
          ($alphabet | index($character)) as $index |
          ($index - ($index % 16) + 1) as $replacement |
          $value[0:$length - 3] + $alphabet[$replacement:$replacement + 1] + "=="
        elif ($value | endswith("=")) then
          ($value[$length - 2:$length - 1]) as $character |
          ($alphabet | index($character)) as $index |
          ($index - ($index % 4) + 1) as $replacement |
          $value[0:$length - 2] + $alphabet[$replacement:$replacement + 1] + "="
        else $value + "="
        end;
      ($content | @base64) as $encoded |
      (if $mode == "invalid-base64" then ("!" + $encoded[1:])
       elif $mode == "trailing-base64-garbage" then ($encoded + "!")
       elif $mode == "missing-base64-padding" then
         (if ($encoded | endswith("=")) then ($encoded | sub("=+$"; "")) else $encoded[0:-1] end)
       elif $mode == "extra-base64-padding" then ($encoded + "=")
       elif $mode == "noncanonical-base64-pad-bits" then ($encoded | noncanonical_pad_bits)
       else $encoded
       end) as $transport |
      {sha:$sha,encoding:"base64",size:$size,
       content:($transport | wrap)}
    '
    ;;
  POST:repos/example/env-vault/git/blobs)
    [[ -f $input && ! -L $input ]] || exit 95
    jq -e '.encoding == "base64" and (.content|type == "string")' "$input" >/dev/null || exit 96
    count=0
    if [[ -f $blob_count_state ]]; then count=$(<"$blob_count_state"); fi
    count=$((count + 1))
    printf '%s\n' "$count" > "$blob_count_state"
    case $count in
      1) sha=$blob_sha_1 ;;
      2) sha=$blob_sha_2 ;;
      3) sha=$blob_sha_3 ;;
      4) sha=$blob_sha_4 ;;
      *) exit 97 ;;
    esac
    if [[ $count == 1 && $mode == post-blob-timeout-unknown ]]; then
      exit 75
    fi
    if [[ $count == 1 && $mode == post-blob-timeout-mismatch ]]; then
      printf 'different remote evidence\n' > "$remote_dir/$sha"
      exit 75
    fi
    jq -e -j '.content|@base64d' "$input" > "$remote_dir/$sha"
    if [[ $count == 1 && $mode == post-blob-timeout ]]; then
      exit 75
    fi
    jq -n --arg sha "$sha" '{sha:$sha}'
    ;;
  POST:repos/example/env-vault/git/trees)
    expected_base=$base_tree
    if [[ $mode == bootstrap || $mode == post-blob-timeout* ]]; then expected_base=$source_tree; fi
    if [[ $mode == bootstrap || $mode == post-blob-timeout* || $mode == new-version ]]; then
      jq -e --arg base "$expected_base" --argjson current "$blob_shas_json" '
        .base_tree == $base and
        [.tree[].path] == [
          "evidence/releases/v1.2.3/release-evidence.json",
          "evidence/releases/v1.2.3/index.md",
          "evidence/releases/v1.2.3/metrics-comparison.json",
          "evidence/releases/v1.2.3/metrics-comparison.md",
          "evidence/releases/v1.2.3/publisher-runs/run-4242/attempt-2/release-evidence.json",
          "evidence/releases/v1.2.3/publisher-runs/run-4242/attempt-2/index.md",
          "evidence/releases/v1.2.3/publisher-runs/run-4242/attempt-2/metrics-comparison.json",
          "evidence/releases/v1.2.3/publisher-runs/run-4242/attempt-2/metrics-comparison.md"
        ] and
        [.tree[].sha] == ($current + $current) and
        all(.tree[]; .mode == "100644" and .type == "blob")
      ' "$input" >/dev/null || exit 98
    else
      jq -e --arg base "$expected_base" --argjson current "$blob_shas_json" '
        .base_tree == $base and
        [.tree[].path] == [
          "evidence/releases/v1.2.3/publisher-runs/run-4242/attempt-2/release-evidence.json",
          "evidence/releases/v1.2.3/publisher-runs/run-4242/attempt-2/index.md",
          "evidence/releases/v1.2.3/publisher-runs/run-4242/attempt-2/metrics-comparison.json",
          "evidence/releases/v1.2.3/publisher-runs/run-4242/attempt-2/metrics-comparison.md"
        ] and
        [.tree[].sha] == $current and
        all(.tree[]; .mode == "100644" and .type == "blob")
      ' "$input" >/dev/null || exit 98
    fi
    jq -n --arg sha "$new_tree" '{sha:$sha}'
    ;;
  POST:repos/example/env-vault/git/commits)
    expected_parent=$base_sha
    if [[ $mode == bootstrap || $mode == post-blob-timeout* ]]; then expected_parent=$source_sha; fi
    jq -e --arg parent "$expected_parent" --arg tree "$new_tree" '
      .message == "chore(evidence): publish v1.2.3" and .tree == $tree and .parents == [$parent]
    ' "$input" >/dev/null || exit 99
    jq -n --arg sha "$new_sha" '{sha:$sha}'
    ;;
  PATCH:repos/example/env-vault/git/refs/heads/release-evidence)
    jq -e --arg sha "$new_sha" '. == {sha:$sha,force:false}' "$input" >/dev/null || exit 103
    if [[ $mode == race ]]; then
      printf 'gh: Update is not a fast forward (HTTP 422)\n' >&2
      exit 1
    fi
    [[ $mode == append || $mode == bootstrap || $mode == post-blob-timeout || $mode == new-version || $mode == depth63-append || $mode == mixed-v2-append ]] || exit 102
    : > "$ref_state"
    emit_ref
    ;;
  *)
    printf 'fake gh: unsupported exact API call: %s %s\n' "$method" "$endpoint" >&2
    exit 104
    ;;
esac
`
	writeExecutable(t, filepath.Join(binDir, "gh"), script)
}
