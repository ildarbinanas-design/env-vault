package tests

import (
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
		repairMode       string
		publisherEvent   string
	}{
		{
			name:             "creates first evidence branch commit and verifies raw blobs",
			mode:             "create",
			wantOutput:       evidencePublishOutput(evidenceNewSHA, "created", "none"),
			wantBlobCreates:  4,
			wantTreeCreates:  1,
			wantCommitCreate: 1,
			wantRefCreates:   1,
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
			name:           "mismatch fails before every mutation",
			mode:           "mismatch",
			wantStatus:     1,
			wantOutput:     "published evidence size differs for release-evidence.json",
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
			if test.mode == "noop" || test.mode == "mismatch" {
				seedEvidenceRemoteBlobs(t, remoteDir, files, test.mode == "mismatch")
			}

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
					"FAKE_EVIDENCE_MODE":       test.mode,
					"FAKE_GH_CALL_LOG":         callLog,
					"FAKE_GH_REMOTE_DIR":       remoteDir,
					"FAKE_GH_REF_STATE":        refState,
					"FAKE_GH_BLOB_COUNT_STATE": blobCount,
					"PATH":                     binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
					"TMPDIR":                   root,
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

			if test.mode == "create" || test.mode == "append" {
				for index, sha := range evidenceBlobSHAs() {
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
		})
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
	for index, sha := range evidenceBlobSHAs() {
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

func evidenceBlobSHAs() []string {
	return []string{
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"cccccccccccccccccccccccccccccccccccccccc",
		"dddddddddddddddddddddddddddddddddddddddd",
	}
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

mode=${FAKE_EVIDENCE_MODE:?FAKE_EVIDENCE_MODE is required}
call_log=${FAKE_GH_CALL_LOG:?FAKE_GH_CALL_LOG is required}
remote_dir=${FAKE_GH_REMOTE_DIR:?FAKE_GH_REMOTE_DIR is required}
ref_state=${FAKE_GH_REF_STATE:?FAKE_GH_REF_STATE is required}
blob_count_state=${FAKE_GH_BLOB_COUNT_STATE:?FAKE_GH_BLOB_COUNT_STATE is required}
printf '%s\n' "$*" >> "$call_log"

[[ ${1:-} == api ]] || {
  printf 'fake gh: only api transport is allowed\n' >&2
  exit 90
}
shift

method=GET
include=false
input=''
if [[ ${1:-} == --include ]]; then
  include=true
  shift
elif [[ ${1:-} == --method ]]; then
  method=${2:-}
  shift 2
fi
endpoint=${1:-}
shift || true
if [[ ${1:-} == --input ]]; then
  input=${2:-}
  shift 2
fi
[[ $# -eq 0 ]] || {
  printf 'fake gh: unsupported arguments\n' >&2
  exit 91
}

source_sha=1111111111111111111111111111111111111111
source_tree=1010101010101010101010101010101010101010
base_sha=2222222222222222222222222222222222222222
base_tree=3333333333333333333333333333333333333333
new_tree=4444444444444444444444444444444444444444
new_sha=5555555555555555555555555555555555555555
full_ref=refs/heads/release-evidence

branch_present=true
if [[ $mode == create && ! -f $ref_state ]]; then
  branch_present=false
fi

if [[ $include == true ]]; then
  [[ $method == GET && $endpoint == repos/example/env-vault/git/ref/heads/release-evidence ]] || exit 92
  if [[ $branch_present == false ]]; then
    printf 'HTTP/2 404 Not Found\r\n\r\n'
    exit 1
  fi
  printf 'HTTP/2 200 OK\r\n\r\n{}\n'
  exit 0
fi

emit_ref() {
  local sha=$base_sha
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
  local variant=$1
  jq -n --arg variant "$variant" '
    def dir($path): {path:$path,mode:"040000",type:"tree",sha:"0000000000000000000000000000000000000000"};
    def blob($path;$sha): {path:$path,mode:"100644",type:"blob",sha:$sha};
    def files($prefix;$shas): [
      blob($prefix + "/release-evidence.json";$shas[0]),
      blob($prefix + "/index.md";$shas[1]),
      blob($prefix + "/metrics-comparison.json";$shas[2]),
      blob($prefix + "/metrics-comparison.md";$shas[3])
    ];
    ["aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
     "cccccccccccccccccccccccccccccccccccccccc","dddddddddddddddddddddddddddddddddddddddd"] as $current_shas |
    ["6666666666666666666666666666666666666666","7777777777777777777777777777777777777777",
     "8888888888888888888888888888888888888888","9999999999999999999999999999999999999999"] as $prior_shas |
    "evidence/releases/v1.2.3" as $version |
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
    ($prior + [dir($version + "/publisher-runs/run-4242"),dir($version + "/publisher-runs/run-4242/attempt-2")] +
      files($version + "/publisher-runs/run-4242/attempt-2";$current_shas)) as $appended |
    if $variant == "current" then {truncated:false,tree:($ancestors + $current_root + $current)}
    elif $variant == "prior" then {truncated:false,tree:($ancestors + $prior_root + $prior)}
    elif $variant == "appended" then {truncated:false,tree:($ancestors + $prior_root + $appended)}
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
    end
  '
}

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
    if [[ $mode == create ]]; then parent=$source_sha; else parent=$base_sha; fi
    emit_commit "$new_sha" "$new_tree" "$parent"
    ;;
  GET:repos/example/env-vault/git/trees/$source_tree?recursive=1)
    jq -n '{truncated:false,tree:[]}'
    ;;
  GET:repos/example/env-vault/git/trees/$base_tree?recursive=1)
    case $mode in
      noop|mismatch) emit_evidence_tree current ;;
      append|race) emit_evidence_tree prior ;;
      partial|unexpected|case-collision|unsafe-numeric) emit_evidence_tree "$mode" ;;
      *) jq -n '{truncated:false,tree:[]}' ;;
    esac
    ;;
  GET:repos/example/env-vault/git/trees/$new_tree?recursive=1)
    if [[ $mode == create ]]; then emit_evidence_tree current; else emit_evidence_tree appended; fi
    ;;
  GET:repos/example/env-vault/git/blobs/*)
    sha=${endpoint##*/}
    path=$remote_dir/$sha
    [[ -f $path && ! -L $path ]] || exit 94
    size=$(wc -c < "$path" | tr -d '[:space:]')
    jq -n --arg sha "$sha" --rawfile content "$path" --argjson size "$size" \
      '{sha:$sha,encoding:"base64",size:$size,content:($content|@base64)}'
    ;;
  POST:repos/example/env-vault/git/blobs)
    [[ -f $input && ! -L $input ]] || exit 95
    jq -e '.encoding == "base64" and (.content|type == "string")' "$input" >/dev/null || exit 96
    count=0
    if [[ -f $blob_count_state ]]; then count=$(<"$blob_count_state"); fi
    count=$((count + 1))
    printf '%s\n' "$count" > "$blob_count_state"
    case $count in
      1) sha=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa ;;
      2) sha=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb ;;
      3) sha=cccccccccccccccccccccccccccccccccccccccc ;;
      4) sha=dddddddddddddddddddddddddddddddddddddddd ;;
      *) exit 97 ;;
    esac
    jq -e -j '.content|@base64d' "$input" > "$remote_dir/$sha"
    jq -n --arg sha "$sha" '{sha:$sha}'
    ;;
  POST:repos/example/env-vault/git/trees)
    expected_base=$base_tree
    if [[ $mode == create ]]; then expected_base=$source_tree; fi
    if [[ $mode == create ]]; then
      jq -e --arg base "$expected_base" '
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
        [.tree[].sha] == [
          "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
          "cccccccccccccccccccccccccccccccccccccccc","dddddddddddddddddddddddddddddddddddddddd",
          "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
          "cccccccccccccccccccccccccccccccccccccccc","dddddddddddddddddddddddddddddddddddddddd"
        ] and
        all(.tree[]; .mode == "100644" and .type == "blob")
      ' "$input" >/dev/null || exit 98
    else
      jq -e --arg base "$expected_base" '
        .base_tree == $base and
        [.tree[].path] == [
          "evidence/releases/v1.2.3/publisher-runs/run-4242/attempt-2/release-evidence.json",
          "evidence/releases/v1.2.3/publisher-runs/run-4242/attempt-2/index.md",
          "evidence/releases/v1.2.3/publisher-runs/run-4242/attempt-2/metrics-comparison.json",
          "evidence/releases/v1.2.3/publisher-runs/run-4242/attempt-2/metrics-comparison.md"
        ] and
        all(.tree[]; .mode == "100644" and .type == "blob" and (.sha|test("^[a-d]{40}$")))
      ' "$input" >/dev/null || exit 98
    fi
    jq -n --arg sha "$new_tree" '{sha:$sha}'
    ;;
  POST:repos/example/env-vault/git/commits)
    expected_parent=$base_sha
    if [[ $mode == create ]]; then expected_parent=$source_sha; fi
    jq -e --arg parent "$expected_parent" --arg tree "$new_tree" '
      .message == "chore(evidence): publish v1.2.3" and .tree == $tree and .parents == [$parent]
    ' "$input" >/dev/null || exit 99
    jq -n --arg sha "$new_sha" '{sha:$sha}'
    ;;
  POST:repos/example/env-vault/git/refs)
    [[ $mode == create && $branch_present == false ]] || exit 100
    jq -e --arg ref "$full_ref" --arg sha "$new_sha" '. == {ref:$ref,sha:$sha}' "$input" >/dev/null || exit 101
    : > "$ref_state"
    emit_ref
    ;;
  PATCH:repos/example/env-vault/git/refs/heads/release-evidence)
    jq -e --arg sha "$new_sha" '. == {sha:$sha,force:false}' "$input" >/dev/null || exit 103
    if [[ $mode == race ]]; then
      printf 'gh: Update is not a fast forward (HTTP 422)\n' >&2
      exit 1
    fi
    [[ $mode == append ]] || exit 102
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
