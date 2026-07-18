package tests

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	homebrewMergeRepository = "ildarbinanas-design/homebrew-tap"
	homebrewMergePR         = "42"
	homebrewMergeHeadSHA    = "5555555555555555555555555555555555555555"
	homebrewMergeCommitSHA  = "6666666666666666666666666666666666666666"
)

func TestHomebrewPRMergeReturnsExactMergeCommit(t *testing.T) {
	root := t.TempDir()
	fakeBin, stateFile, callLog := installHomebrewMergeFakeGH(t, root)

	stdout, stderr, status := runHomebrewPRMerge(t, root, fakeBin, stateFile, callLog, "success", "0", "0")
	if status != 0 {
		t.Fatalf("exit status=%d, want 0\nstdout:\n%s\nstderr:\n%s", status, stdout, stderr)
	}
	if stdout != homebrewMergeCommitSHA+"\n" {
		t.Fatalf("stdout=%q, want exactly the merge commit SHA", stdout)
	}

	calls := readOptionalFile(t, callLog)
	if got := strings.Count(calls, "pr view "+homebrewMergePR); got != 2 {
		t.Fatalf("pull request view calls=%d, want 2\n%s", got, calls)
	}
	mergeCall := "pr merge " + homebrewMergePR + " --repo " + homebrewMergeRepository + " --squash --match-head-commit " + homebrewMergeHeadSHA
	if got := strings.Count(calls, mergeCall); got != 1 {
		t.Fatalf("exact merge calls=%d, want 1\n%s", got, calls)
	}
	for _, forbidden := range []string{"--admin", "--force", "--auto"} {
		if strings.Contains(calls, forbidden) {
			t.Fatalf("merge used forbidden option %q:\n%s", forbidden, calls)
		}
	}
}

func TestHomebrewPRMergeFailsClosed(t *testing.T) {
	tests := []struct {
		name          string
		mode          string
		timeout       string
		wantError     string
		wantMergeCall bool
	}{
		{name: "head mismatch", mode: "head-mismatch", timeout: "0", wantError: "head SHA changed"},
		{name: "merge command failure", mode: "merge-failure", timeout: "0", wantError: "cannot squash-merge", wantMergeCall: true},
		{name: "terminal closed state", mode: "terminal-closed", timeout: "0", wantError: "closed without merging", wantMergeCall: true},
		{name: "malformed initial response", mode: "malformed", timeout: "0", wantError: "malformed pull request data"},
		{name: "malformed merge commit", mode: "malformed-merge", timeout: "0", wantError: "invalid merge commit SHA", wantMergeCall: true},
		{name: "bounded timeout", mode: "timeout", timeout: "0", wantError: "timed out after 0s", wantMergeCall: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			fakeBin, stateFile, callLog := installHomebrewMergeFakeGH(t, root)
			stdout, stderr, status := runHomebrewPRMerge(t, root, fakeBin, stateFile, callLog, test.mode, test.timeout, "0")
			if status == 0 {
				t.Fatalf("unexpected success\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
			}
			if stdout != "" {
				t.Fatalf("failure stdout=%q, want empty", stdout)
			}
			if !strings.Contains(stderr, test.wantError) {
				t.Fatalf("stderr missing %q:\n%s", test.wantError, stderr)
			}

			calls := readOptionalFile(t, callLog)
			hasMergeCall := strings.Contains(calls, "pr merge "+homebrewMergePR)
			if hasMergeCall != test.wantMergeCall {
				t.Fatalf("merge called=%t, want %t\n%s", hasMergeCall, test.wantMergeCall, calls)
			}
			for _, forbidden := range []string{"--admin", "--force", "--auto"} {
				if strings.Contains(calls, forbidden) {
					t.Fatalf("failure path used forbidden option %q:\n%s", forbidden, calls)
				}
			}
		})
	}
}

func TestHomebrewPRMergeRequiresTokenBeforeGitHubAccess(t *testing.T) {
	root := t.TempDir()
	fakeBin, stateFile, callLog := installHomebrewMergeFakeGH(t, root)
	cmd := exec.Command("bash", "../scripts/release/merge-homebrew-pr.sh", homebrewMergeRepository, homebrewMergePR, homebrewMergeHeadSHA)
	cmd.Env = environmentWithOverrides(map[string]string{
		"FAKE_HOMEBREW_MERGE_MODE":  "success",
		"FAKE_HOMEBREW_MERGE_STATE": stateFile,
		"FAKE_GH_CALL_LOG":          callLog,
		"GH_TOKEN":                  "",
		"PATH":                      fakeBin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"TMPDIR":                    root,
	})
	output, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "GH_TOKEN is required") {
		t.Fatalf("status err=%v output=%q, want missing-token failure", err, output)
	}
	if calls := readOptionalFile(t, callLog); calls != "" {
		t.Fatalf("missing token called gh:\n%s", calls)
	}
}

func installHomebrewMergeFakeGH(t *testing.T, root string) (binDir, stateFile, callLog string) {
	t.Helper()
	binDir = filepath.Join(root, "bin")
	stateFile = filepath.Join(root, "state")
	callLog = filepath.Join(root, "gh-calls.log")
	makeDirectory(t, binDir)
	writeExecutable(t, filepath.Join(binDir, "gh"), `#!/usr/bin/env bash
set -euo pipefail

mode=${FAKE_HOMEBREW_MERGE_MODE:?}
state_file=${FAKE_HOMEBREW_MERGE_STATE:?}
call_log=${FAKE_GH_CALL_LOG:?}
printf '%s\n' "$*" >> "$call_log"

head_sha=5555555555555555555555555555555555555555
merge_sha=6666666666666666666666666666666666666666

open_record() {
  printf '42|OPEN|%s|main|false|-\n' "$1"
}

if [[ ${1:-} == pr && ${2:-} == view ]]; then
  count=0
  if [[ -f $state_file ]]; then
    read -r count < "$state_file"
  fi
  count=$((count + 1))
  printf '%s\n' "$count" > "$state_file"

  case "$mode" in
    success)
      if [[ $count -eq 1 ]]; then open_record "$head_sha"; else printf '42|MERGED|%s|main|false|%s\n' "$head_sha" "$merge_sha"; fi
      ;;
    head-mismatch) open_record 7777777777777777777777777777777777777777 ;;
    merge-failure) open_record "$head_sha" ;;
    terminal-closed)
      if [[ $count -eq 1 ]]; then open_record "$head_sha"; else printf '42|CLOSED|%s|main|false|-\n' "$head_sha"; fi
      ;;
    malformed) printf 'BROKEN\n' ;;
    malformed-merge)
      if [[ $count -eq 1 ]]; then open_record "$head_sha"; else printf '42|MERGED|%s|main|false|abc123\n' "$head_sha"; fi
      ;;
    timeout) open_record "$head_sha" ;;
    *) printf 'fake gh: unsupported mode: %s\n' "$mode" >&2; exit 90 ;;
  esac
  exit 0
fi

if [[ ${1:-} == pr && ${2:-} == merge ]]; then
  if [[ $mode == merge-failure ]]; then
    printf 'fake gh: merge rejected\n' >&2
    exit 1
  fi
  exit 0
fi

printf 'fake gh: unsupported command: %s\n' "$*" >&2
exit 91
`)
	return binDir, stateFile, callLog
}

func runHomebrewPRMerge(t *testing.T, root, fakeBin, stateFile, callLog, mode, timeout, interval string) (string, string, int) {
	t.Helper()
	cmd := exec.Command("bash", "../scripts/release/merge-homebrew-pr.sh", homebrewMergeRepository, homebrewMergePR, homebrewMergeHeadSHA)
	cmd.Env = environmentWithOverrides(map[string]string{
		"FAKE_HOMEBREW_MERGE_MODE":           mode,
		"FAKE_HOMEBREW_MERGE_STATE":          stateFile,
		"FAKE_GH_CALL_LOG":                   callLog,
		"GH_TOKEN":                           "1",
		"HOMEBREW_PR_MERGE_INTERVAL_SECONDS": interval,
		"HOMEBREW_PR_MERGE_TIMEOUT_SECONDS":  timeout,
		"PATH":                               fakeBin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"TMPDIR":                             root,
	})
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return stdout.String(), stderr.String(), 0
	}
	exitError, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("run merge-homebrew-pr.sh: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	return stdout.String(), stderr.String(), exitError.ExitCode()
}
