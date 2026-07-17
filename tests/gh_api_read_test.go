package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseTransportLauncherFallbackBuildsForHelpAndCleansUp(t *testing.T) {
	temporary := t.TempDir()
	output, status := runReleaseScript(t, "../scripts/release/releasetransport.sh", []string{"help"}, map[string]string{
		"RELEASE_TRANSPORT_BIN": "",
		"TMPDIR":                temporary,
	})
	if status != 0 || !strings.HasPrefix(output, "usage:\n") {
		t.Fatalf("status=%d output=%q", status, output)
	}
	entries, err := os.ReadDir(temporary)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("launcher left temporary build artifacts: %v", entries)
	}
}

func TestReleaseTransportLauncherRejectsUntrustedPrebuiltFiles(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(root, "transport-link")
	if err := os.Symlink(target, symlink); err != nil {
		t.Fatal(err)
	}
	nonExecutable := filepath.Join(root, "transport-noexec")
	if err := os.WriteFile(nonExecutable, []byte("not executable\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{"symlink": symlink, "non-executable": nonExecutable} {
		t.Run(name, func(t *testing.T) {
			output, status := runReleaseScript(t, "../scripts/release/releasetransport.sh", []string{"help"}, map[string]string{
				"RELEASE_TRANSPORT_BIN": path,
			})
			if status != 2 || !strings.Contains(output, `"schema_id":"env-vault.github-transport-error.v1"`) ||
				!strings.Contains(output, `"code":"INPUT_INVALID"`) || strings.Contains(output, path) {
				t.Fatalf("status=%d output=%q", status, output)
			}
		})
	}
}

func TestGHAPIReadUsageIsVersionedJSON(t *testing.T) {
	output, status := runReleaseScript(t, "../scripts/release/gh-api-read.sh", nil, nil)
	if status != 2 || !strings.Contains(output, `"schema_id":"env-vault.github-transport-error.v1"`) ||
		!strings.Contains(output, `"code":"INPUT_INVALID"`) || strings.Contains(output, "usage:") {
		t.Fatalf("status=%d output=%q", status, output)
	}
}

func TestGHAPIReadPublishesStrictResponseWithoutClobberWindow(t *testing.T) {
	root := t.TempDir()
	bin, state := installGHAPIReadFake(t, root)
	outputPath := filepath.Join(root, "snapshot.json")

	output, status := runReleaseScript(t, "../scripts/release/gh-api-read.sh", []string{
		outputPath,
		"--paginate", "--slurp",
		"--method", "GET",
		"repos/example/env-vault/actions/runs",
		"--raw-field", "per_page=100",
	}, ghAPIReadEnvironment(bin, state, "success"))
	if status != 0 || output != "" {
		t.Fatalf("status=%d output=%q, want silent success", status, output)
	}
	contents, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "[{\"total_count\":1,\"workflow_runs\":[{\"id\":1}]}]\n" {
		t.Fatalf("published response=%q", contents)
	}
	calls := nonemptyLines(readOptionalFile(t, filepath.Join(state, "gh-calls.log")))
	if len(calls) != 1 {
		t.Fatalf("gh calls=%v, want one network read", calls)
	}
	wantCall := "api --include --hostname github.com --method GET --header Accept: application/vnd.github+json --header X-GitHub-Api-Version: 2022-11-28 repos/example/env-vault/actions/runs --raw-field per_page=100"
	if calls[0] != wantCall {
		t.Fatalf("gh argument forwarding=%q, want %q", calls[0], wantCall)
	}
}

func TestGHAPIReadMapsAuthenticationFailureAndLeavesNoOutput(t *testing.T) {
	root := t.TempDir()
	bin, state := installGHAPIReadFake(t, root)
	outputPath := filepath.Join(root, "snapshot.json")

	output, status := runReleaseScript(t, "../scripts/release/gh-api-read.sh", []string{
		outputPath, "repos/example/env-vault/git/ref/heads/main",
	}, ghAPIReadEnvironment(bin, state, "auth"))
	if status != 5 || !strings.Contains(output, `"code":"AUTH_REQUIRED"`) {
		t.Fatalf("status=%d output=%q, want stable authentication failure", status, output)
	}
	if _, err := os.Lstat(outputPath); !os.IsNotExist(err) {
		t.Fatalf("failed read created output: %v", err)
	}
	if calls := nonemptyLines(readOptionalFile(t, filepath.Join(state, "gh-calls.log"))); len(calls) != 1 {
		t.Fatalf("gh calls=%v, want one non-retriable read", calls)
	}
}

func TestGHAPIReadRejectsExistingOutputBeforeTransport(t *testing.T) {
	root := t.TempDir()
	bin, state := installGHAPIReadFake(t, root)
	outputPath := filepath.Join(root, "snapshot.json")
	if err := os.WriteFile(outputPath, []byte("trusted prior snapshot\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	output, status := runReleaseScript(t, "../scripts/release/gh-api-read.sh", []string{
		outputPath, "repos/example/env-vault/git/ref/heads/main",
	}, ghAPIReadEnvironment(bin, state, "success"))
	if status != 6 || !strings.Contains(output, `"code":"OUTPUT_FAILED"`) {
		t.Fatalf("status=%d output=%q, want no-clobber failure", status, output)
	}
	contents, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "trusted prior snapshot\n" {
		t.Fatalf("existing output changed: %q", contents)
	}
	if calls := readOptionalFile(t, filepath.Join(state, "gh-calls.log")); calls != "" {
		t.Fatalf("existing output reached network transport:\n%s", calls)
	}
}

func TestGHAPIReadRejectsMutationShapesBeforeTransport(t *testing.T) {
	tests := map[string][]string{
		"explicit post":           {"--method", "POST", "repos/example/env-vault/issues"},
		"method override":         {"--method", "GET", "--method", "GET", "repos/example/env-vault"},
		"implicit field post":     {"repos/example/env-vault/issues", "--raw-field", "title=unsafe"},
		"request body":            {"repos/example/env-vault/issues", "--input", "payload.json"},
		"graphql":                 {"graphql", "-f", "query=query { viewer { login } }"},
		"graphql path":            {"/graphql"},
		"graphql URL":             {"https://api.github.com/graphql"},
		"custom host":             {"--hostname=attacker.invalid", "repos/example/env-vault"},
		"absolute endpoint":       {"https://attacker.invalid/repos/example/env-vault"},
		"verbose equals":          {"--verbose=true", "repos/example/env-vault"},
		"silent equals":           {"--silent=true", "repos/example/env-vault"},
		"cache":                   {"--cache", "1h", "repos/example/env-vault"},
		"cache equals":            {"--cache=1h", "repos/example/env-vault"},
		"multiple REST endpoints": {"repos/example/env-vault", "repos/example/env-vault/issues"},
	}
	for name, arguments := range tests {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			bin, state := installGHAPIReadFake(t, root)
			outputPath := filepath.Join(root, "snapshot.json")
			arguments = append([]string{outputPath}, arguments...)
			_, status := runReleaseScript(t, "../scripts/release/gh-api-read.sh", arguments,
				ghAPIReadEnvironment(bin, state, "success"))
			if status != 2 {
				t.Fatalf("status=%d, want policy/usage status 2", status)
			}
			if calls := readOptionalFile(t, filepath.Join(state, "gh-calls.log")); calls != "" {
				t.Fatalf("rejected request reached gh transport:\n%s", calls)
			}
		})
	}
}

func installGHAPIReadFake(t *testing.T, root string) (bin, state string) {
	t.Helper()
	bin = filepath.Join(root, "bin")
	state = filepath.Join(root, "state")
	makeDirectory(t, bin)
	makeDirectory(t, state)
	writeExecutable(t, filepath.Join(bin, "gh"), `#!/usr/bin/env bash
set -euo pipefail
if [[ ${1:-} == --version ]]; then
  printf 'gh version 2.80.0 (2026-01-01)\nhttps://github.com/cli/cli/releases/tag/v2.80.0\n'
  exit 0
fi
if [[ ${1:-} == api && ${2:-} == --help ]]; then
  printf '%s\n' 'OPTIONS: --include --hostname --method --header --raw-field'
  exit 0
fi
[[ ${1:-} == api ]] || { echo "expected gh api transport" >&2; exit 90; }
if [[ -n ${GH_DEBUG:-} || -n ${GIT_TRACE:-} || -n ${GIT_TRACE_CURL:-} || -n ${GIT_CURL_VERBOSE:-} || -n ${GIT_TRACE_PACKET:-} ]]; then
  echo "debug or trace environment leaked into gh" >&2
  exit 91
fi
[[ ${GH_HOST:-} == github.com ]] || { echo "GitHub host was not pinned" >&2; exit 93; }
[[ ${GH_TOKEN:-} == fake-gh-token ]] || { echo "GitHub transport credential was not retained" >&2; exit 94; }
[[ -z ${GH_ENTERPRISE_TOKEN:-} && -z ${GITHUB_ENTERPRISE_TOKEN:-} ]] || {
  echo "enterprise credentials leaked into github.com transport" >&2
  exit 95
}
state=${FAKE_GH_READ_STATE:?}
mode=${FAKE_GH_READ_MODE:?}
count=0
[[ ! -f "$state/count" ]] || read -r count < "$state/count"
count=$((count + 1))
printf '%s\n' "$count" > "$state/count"
printf '%s\n' "$*" >> "$state/gh-calls.log"
case "$mode" in
  success)
    printf 'HTTP/2 200 OK\r\nContent-Type: application/vnd.github+json\r\nX-GitHub-Api-Version-Selected: 2022-11-28\r\n\r\n'
    printf '{"total_count":1,"workflow_runs":[{"id":%s}]}\n' "$count"
    ;;
  auth)
    printf 'HTTP/2 401 Unauthorized\r\nContent-Type: application/vnd.github+json\r\nX-GitHub-Api-Version-Selected: 2022-11-28\r\n\r\n'
    printf '{"message":"Bad credentials"}\n'
    exit 1
    ;;
  *)
    echo "unsupported fake mode" >&2
    exit 92
    ;;
esac
`)
	return bin, state
}

func ghAPIReadEnvironment(bin, state, mode string) map[string]string {
	return map[string]string{
		"FAKE_GH_READ_MODE":       mode,
		"FAKE_GH_READ_STATE":      state,
		"GH_DEBUG":                "api",
		"GH_HOST":                 "attacker.invalid",
		"GH_TOKEN":                "fake-gh-token",
		"GH_ENTERPRISE_TOKEN":     "fake-enterprise-token",
		"GITHUB_ENTERPRISE_TOKEN": "fake-github-enterprise-token",
		"GIT_TRACE":               "1",
		"GIT_TRACE_CURL":          "1",
		"GIT_CURL_VERBOSE":        "1",
		"GIT_TRACE_PACKET":        "1",
		"PATH":                    bin + string(os.PathListSeparator) + os.Getenv("PATH"),
	}
}
