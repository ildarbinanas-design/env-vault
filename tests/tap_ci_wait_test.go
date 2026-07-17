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
	tapCIRepository = "example/homebrew-tap"
	tapCIWorkflow   = "test-formula.yml"
	tapCISHA        = "3333333333333333333333333333333333333333"
	tapCIRunURL     = "https://github.com/example/homebrew-tap/actions/runs/12345"
)

func TestTapCIWaitsForExactSuccessfulRun(t *testing.T) {
	root := t.TempDir()
	fakeBin, stateFile, callLog := installTapCIFakeGH(t, root)
	identityOutput := filepath.Join(root, "tap-ci-identity.json")

	stdout, stderr, status := runTapCIWait(
		t,
		[]string{tapCIRepository, tapCIWorkflow, tapCISHA, "pull_request", "5", "0"},
		map[string]string{
			"FAKE_TAP_CI_MODE":       "sequence-success",
			"FAKE_TAP_CI_STATE":      stateFile,
			"FAKE_GH_CALL_LOG":       callLog,
			"PATH":                   fakeBin + string(os.PathListSeparator) + os.Getenv("PATH"),
			"TAP_CI_IDENTITY_OUTPUT": identityOutput,
			"TMPDIR":                 root,
		},
	)
	if status != 0 {
		t.Fatalf("exit status=%d, want 0\nstdout:\n%s\nstderr:\n%s", status, stdout, stderr)
	}
	if stdout != tapCIRunURL+"\n" {
		t.Fatalf("stdout=%q, want exactly one run URL", stdout)
	}
	if got := strings.Count(stdout+stderr, tapCIRunURL); got != 1 {
		t.Fatalf("successful run URL occurrences=%d, want exactly 1\nstdout:\n%s\nstderr:\n%s", got, stdout, stderr)
	}
	for _, diagnostic := range []string{
		"no matching run yet",
		"matching run is queued",
		"matching run is in progress",
	} {
		if !strings.Contains(stderr, diagnostic) {
			t.Fatalf("stderr missing %q:\n%s", diagnostic, stderr)
		}
	}

	calls := readOptionalFile(t, callLog)
	for _, exactArgument := range []string{
		"api --include --hostname github.com --method GET",
		"--raw-field head_sha=" + tapCISHA,
		"--raw-field event=pull_request",
		"--raw-field per_page=100",
	} {
		if !strings.Contains(calls, exactArgument) {
			t.Fatalf("gh calls missing %q:\n%s", exactArgument, calls)
		}
	}
	if strings.Contains(calls, "--jq") {
		t.Fatalf("transport must leave strict projection offline:\n%s", calls)
	}
	identity := readOptionalFile(t, identityOutput)
	for _, expected := range []string{
		`"schema_id":"env-vault.github-actions-identity.v1"`,
		`"run_id":12345`, `"run_attempt":1`,
		`"workflow_path":".github/workflows/test-formula.yml"`,
		`"event":"pull_request"`, `"head_sha":"` + tapCISHA + `"`,
	} {
		if !strings.Contains(identity, expected) {
			t.Fatalf("typed identity missing %q:\n%s", expected, identity)
		}
	}
}

func TestTapCITypedIdentityOutputIsNoClobber(t *testing.T) {
	root := t.TempDir()
	fakeBin, stateFile, callLog := installTapCIFakeGH(t, root)
	identityOutput := filepath.Join(root, "tap-ci-identity.json")
	if err := os.WriteFile(identityOutput, []byte("sentinel\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, status := runTapCIWait(t,
		[]string{tapCIRepository, tapCIWorkflow, tapCISHA, "pull_request", "5", "0"},
		map[string]string{
			"FAKE_TAP_CI_MODE":       "success",
			"FAKE_TAP_CI_STATE":      stateFile,
			"FAKE_GH_CALL_LOG":       callLog,
			"PATH":                   fakeBin + string(os.PathListSeparator) + os.Getenv("PATH"),
			"TAP_CI_IDENTITY_OUTPUT": identityOutput,
			"TMPDIR":                 root,
		})
	if status == 0 || stdout != "" || !strings.Contains(stderr, "typed identity output already exists") {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	if got := readOptionalFile(t, identityOutput); got != "sentinel\n" {
		t.Fatalf("existing typed identity output changed: %q", got)
	}
}

func TestTapCIAcceptsEnvironmentInputsAndPushEvent(t *testing.T) {
	root := t.TempDir()
	fakeBin, stateFile, callLog := installTapCIFakeGH(t, root)
	stdout, stderr, status := runTapCIWait(t, nil, map[string]string{
		"FAKE_TAP_CI_MODE":        "success",
		"FAKE_TAP_CI_STATE":       stateFile,
		"FAKE_GH_CALL_LOG":        callLog,
		"PATH":                    fakeBin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"TMPDIR":                  root,
		"TAP_CI_REPOSITORY":       tapCIRepository,
		"TAP_CI_WORKFLOW":         tapCIWorkflow,
		"TAP_CI_SHA":              tapCISHA,
		"TAP_CI_EVENT":            "push",
		"TAP_CI_TIMEOUT_SECONDS":  "0",
		"TAP_CI_INTERVAL_SECONDS": "0",
	})
	if status != 0 || stdout != tapCIRunURL+"\n" || stderr != "" {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	if calls := readOptionalFile(t, callLog); !strings.Contains(calls, "--raw-field event=push") {
		t.Fatalf("gh call does not use the push event:\n%s", calls)
	}
}

func TestTapCIFailsClosedForRunAndTransportStates(t *testing.T) {
	tests := []struct {
		name       string
		mode       string
		timeout    string
		wantOutput string
	}{
		{name: "terminal failure", mode: "terminal-failure", timeout: "5", wantOutput: "completed unsuccessfully: conclusion=failure"},
		{name: "no run timeout", mode: "no-run", timeout: "0", wantOutput: "last state: no matching run"},
		{name: "queued timeout", mode: "queued", timeout: "0", wantOutput: "last state: queued"},
		{name: "malformed structured output", mode: "malformed-output", timeout: "5", wantOutput: "PAGINATION_INVALID"},
		{name: "malformed API object", mode: "malformed-jq", timeout: "5", wantOutput: "PAGINATION_INVALID"},
		{name: "wrong SHA is not a match", mode: "wrong-sha", timeout: "0", wantOutput: "last state: no matching run"},
		{name: "completed without conclusion", mode: "missing-conclusion", timeout: "5", wantOutput: "completed workflow run has no conclusion"},
		{name: "unknown status", mode: "unknown-status", timeout: "5", wantOutput: "unknown workflow run status"},
		{name: "HTTP API failure", mode: "api-503", timeout: "5", wantOutput: "TRANSPORT_FAILED"},
		{name: "network failure", mode: "network", timeout: "5", wantOutput: "TRANSPORT_FAILED"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			fakeBin, stateFile, callLog := installTapCIFakeGH(t, root)
			stdout, stderr, status := runTapCIWait(
				t,
				[]string{tapCIRepository, tapCIWorkflow, tapCISHA, "pull_request", test.timeout, "0"},
				map[string]string{
					"FAKE_TAP_CI_MODE":  test.mode,
					"FAKE_TAP_CI_STATE": stateFile,
					"FAKE_GH_CALL_LOG":  callLog,
					"PATH":              fakeBin + string(os.PathListSeparator) + os.Getenv("PATH"),
					"TMPDIR":            root,
				},
			)
			if status == 0 {
				t.Fatalf("unexpected success\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
			}
			if stdout != "" {
				t.Fatalf("failure stdout=%q, want empty", stdout)
			}
			if !strings.Contains(stderr, test.wantOutput) {
				t.Fatalf("stderr missing %q:\n%s", test.wantOutput, stderr)
			}
		})
	}
}

func TestTapCIRejectsInvalidInputsBeforeCallingGitHub(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantOutput string
	}{
		{name: "repository", args: []string{"not-a-repository", tapCIWorkflow, tapCISHA, "push", "1", "0"}, wantOutput: "repository must have the form"},
		{name: "workflow path", args: []string{tapCIRepository, "../test-formula.yml", tapCISHA, "push", "1", "0"}, wantOutput: "workflow must be a root workflow filename"},
		{name: "short SHA", args: []string{tapCIRepository, tapCIWorkflow, "abc123", "push", "1", "0"}, wantOutput: "exactly 40 lowercase"},
		{name: "event", args: []string{tapCIRepository, tapCIWorkflow, tapCISHA, "workflow_dispatch", "1", "0"}, wantOutput: "event must be pull_request or push"},
		{name: "negative timeout", args: []string{tapCIRepository, tapCIWorkflow, tapCISHA, "push", "-1", "0"}, wantOutput: "timeout must be a non-negative integer"},
		{name: "fractional interval", args: []string{tapCIRepository, tapCIWorkflow, tapCISHA, "push", "1", "0.5"}, wantOutput: "interval must be a non-negative integer"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			fakeBin, stateFile, callLog := installTapCIFakeGH(t, root)
			stdout, stderr, status := runTapCIWait(t, test.args, map[string]string{
				"FAKE_TAP_CI_MODE":  "success",
				"FAKE_TAP_CI_STATE": stateFile,
				"FAKE_GH_CALL_LOG":  callLog,
				"PATH":              fakeBin + string(os.PathListSeparator) + os.Getenv("PATH"),
				"TMPDIR":            root,
			})
			if status == 0 || stdout != "" || !strings.Contains(stderr, test.wantOutput) {
				t.Fatalf("status=%d stdout=%q stderr=%q, want failure containing %q", status, stdout, stderr, test.wantOutput)
			}
			if calls := readOptionalFile(t, callLog); calls != "" {
				t.Fatalf("invalid input called gh:\n%s", calls)
			}
		})
	}
}

func installTapCIFakeGH(t *testing.T, root string) (binDir, stateFile, callLog string) {
	t.Helper()
	binDir = filepath.Join(root, "bin")
	stateFile = filepath.Join(root, "state")
	callLog = filepath.Join(root, "gh-calls.log")
	makeDirectory(t, binDir)
	writeExecutable(t, filepath.Join(binDir, "gh"), `#!/usr/bin/env bash
set -euo pipefail

if [[ ${1:-} == --version ]]; then
  printf 'gh version 2.80.0 (2026-01-01)\n'
  exit 0
fi
if [[ ${1:-} == api && ${2:-} == --help ]]; then
  printf '%s\n' 'OPTIONS: --include --hostname --method --header --raw-field'
  exit 0
fi
mode=${FAKE_TAP_CI_MODE:?}
state_file=${FAKE_TAP_CI_STATE:?}
call_log=${FAKE_GH_CALL_LOG:?}
printf '%s\n' "$*" >> "$call_log"

count=0
if [[ -f $state_file ]]; then
  read -r count < "$state_file"
fi
count=$((count + 1))
printf '%s\n' "$count" > "$state_file"

sha=3333333333333333333333333333333333333333
url=https://github.com/example/homebrew-tap/actions/runs/12345
event=pull_request
for argument in "$@"; do
  case "$argument" in
    event=push) event=push ;;
  esac
done

headers() {
  printf 'HTTP/2 200 OK\r\nContent-Type: application/vnd.github+json\r\nX-GitHub-Api-Version-Selected: 2022-11-28\r\n\r\n'
}

record() {
  local returned_sha=$1 returned_event=$2 status=$3 conclusion=$4
  local head_ref=release/env-vault-v1.2.3
  [[ $returned_event == push ]] && head_ref=main
  printf '%s\n' "$returned_event" > "$state_file.event"
  printf '%s\n' "$head_ref" > "$state_file.head-ref"
  headers
  if [[ -n $conclusion ]]; then
    conclusion_json=\"$conclusion\"
  else
    conclusion_json=null
  fi
  printf '{"total_count":1,"workflow_runs":[{"id":12345,"run_attempt":1,"repository":{"full_name":"example/homebrew-tap"},"head_repository":{"full_name":"example/homebrew-tap"},"head_sha":"%s","head_branch":"%s","path":".github/workflows/test-formula.yml","event":"%s","status":"%s","conclusion":%s,"html_url":"%s"}]}\n' \
    "$returned_sha" "$head_ref" "$returned_event" "$status" "$conclusion_json" "$url"
}

none() {
  headers
  printf '{"total_count":0,"workflow_runs":[]}\n'
}

if [[ "$*" == *"repos/example/homebrew-tap/actions/runs/12345/attempts/1"* ]]; then
  selected_event=$(<"$state_file.event")
  selected_head_ref=$(<"$state_file.head-ref")
  headers
  printf '{"id":12345,"run_attempt":1,"repository":{"full_name":"example/homebrew-tap"},"head_repository":{"full_name":"example/homebrew-tap"},"head_sha":"%s","head_branch":"%s","path":".github/workflows/test-formula.yml","event":"%s","status":"completed","conclusion":"success","html_url":"%s","name":"custom diagnostic title"}\n' \
    "$sha" "$selected_head_ref" "$selected_event" "$url"
  exit 0
fi

case "$mode" in
  sequence-success)
    case "$count" in
      1) none ;;
      2) record "$sha" "$event" queued '' ;;
      3) record "$sha" "$event" in_progress '' ;;
      *) record "$sha" "$event" completed success ;;
    esac
    ;;
  success) record "$sha" "$event" completed success ;;
  terminal-failure) record "$sha" "$event" completed failure ;;
  no-run) none ;;
  queued) record "$sha" "$event" queued '' ;;
  malformed-output)
    headers
    printf '{"total_count":1,"workflow_runs":"BROKEN"}\n'
    ;;
  malformed-jq)
    headers
    printf '{"total_count":1,"workflow_runs":[{"id":"bad","head_sha":"%s","event":"%s","status":"completed","conclusion":"success","html_url":"%s"}]}\n' "$sha" "$event" "$url"
    ;;
  wrong-sha) record 4444444444444444444444444444444444444444 "$event" completed success ;;
  missing-conclusion) record "$sha" "$event" completed '' ;;
  unknown-status) record "$sha" "$event" surprising '' ;;
  api-503)
    printf 'HTTP/2 503 Service Unavailable\r\nContent-Type: application/vnd.github+json\r\nRetry-After: 0\r\nX-GitHub-Api-Version-Selected: 2022-11-28\r\n\r\n{"message":"Service Unavailable"}\n'
    exit 1
    ;;
  network)
    printf 'dial tcp: network is unreachable\n' >&2
    exit 1
    ;;
  *)
    printf 'fake gh: unsupported mode: %s\n' "$mode" >&2
    exit 90
    ;;
esac
`)
	return binDir, stateFile, callLog
}

func runTapCIWait(t *testing.T, args []string, overrides map[string]string) (string, string, int) {
	t.Helper()
	commandArgs := append([]string{"../scripts/release/wait-tap-ci.sh"}, args...)
	cmd := exec.Command("bash", commandArgs...)
	cmd.Env = environmentWithOverrides(overrides)
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
		t.Fatalf("run wait-tap-ci.sh: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	return stdout.String(), stderr.String(), exitError.ExitCode()
}
