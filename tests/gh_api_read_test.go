package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGHAPIReadRetriesTransientReadsAndPublishesAtomically(t *testing.T) {
	root := t.TempDir()
	bin, state := installGHAPIReadFakes(t, root)
	outputPath := filepath.Join(root, "snapshot.json")
	if err := os.WriteFile(outputPath, []byte("preserve until success\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	output, status := runReleaseScript(t, "../scripts/release/gh-api-read.sh", []string{
		outputPath,
		"--paginate", "--slurp",
		"--method", "GET",
		"repos/example/env-vault/actions/runs",
		"--raw-field", "per_page=100",
	}, ghAPIReadEnvironment(bin, state, "transient"))
	if status != 0 || output != "" {
		t.Fatalf("status=%d output=%q, want silent success", status, output)
	}
	contents, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "{\"ok\":true,\"attempt\":3}\n" {
		t.Fatalf("published response=%q", contents)
	}
	calls := nonemptyLines(readOptionalFile(t, filepath.Join(state, "gh-calls.log")))
	if len(calls) != 3 {
		t.Fatalf("gh calls=%v, want 3", calls)
	}
	wantCall := "api --paginate --slurp --method GET repos/example/env-vault/actions/runs --raw-field per_page=100"
	for _, call := range calls {
		if call != wantCall {
			t.Fatalf("gh argument forwarding=%q, want %q", call, wantCall)
		}
	}
	if sleeps := readOptionalFile(t, filepath.Join(state, "sleep-calls.log")); sleeps != "1\n2\n" {
		t.Fatalf("sleep schedule=%q, want deterministic 1,2", sleeps)
	}
	assertNoGHAPIReadTemporaryFiles(t, root)
}

func TestGHAPIReadFailsClosedWithoutClobberingPriorSnapshot(t *testing.T) {
	root := t.TempDir()
	bin, state := installGHAPIReadFakes(t, root)
	outputPath := filepath.Join(root, "snapshot.json")
	if err := os.WriteFile(outputPath, []byte("trusted prior snapshot\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	output, status := runReleaseScript(t, "../scripts/release/gh-api-read.sh", []string{
		outputPath, "repos/example/env-vault/git/ref/heads/main",
	}, ghAPIReadEnvironment(bin, state, "failure"))
	if status != 1 || !strings.Contains(output, "failed after 5 attempts") {
		t.Fatalf("status=%d output=%q, want bounded transport failure", status, output)
	}
	contents, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "trusted prior snapshot\n" {
		t.Fatalf("failed read clobbered prior snapshot: %q", contents)
	}
	if calls := nonemptyLines(readOptionalFile(t, filepath.Join(state, "gh-calls.log"))); len(calls) != 5 {
		t.Fatalf("gh calls=%v, want exactly 5", calls)
	}
	if sleeps := readOptionalFile(t, filepath.Join(state, "sleep-calls.log")); sleeps != "1\n2\n4\n8\n" {
		t.Fatalf("sleep schedule=%q, want deterministic exponential backoff", sleeps)
	}
	assertNoGHAPIReadTemporaryFiles(t, root)
}

func TestGHAPIReadRejectsEmptySuccessWithoutClobberingPriorSnapshot(t *testing.T) {
	root := t.TempDir()
	bin, state := installGHAPIReadFakes(t, root)
	outputPath := filepath.Join(root, "snapshot.json")
	if err := os.WriteFile(outputPath, []byte("trusted prior snapshot\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	output, status := runReleaseScript(t, "../scripts/release/gh-api-read.sh", []string{
		outputPath, "repos/example/env-vault/git/ref/heads/main",
	}, ghAPIReadEnvironment(bin, state, "empty"))
	if status != 1 || strings.Count(output, "empty read response") != 5 || !strings.Contains(output, "failed after 5 attempts") {
		t.Fatalf("status=%d output=%q, want five rejected empty responses", status, output)
	}
	contents, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "trusted prior snapshot\n" {
		t.Fatalf("empty responses clobbered prior snapshot: %q", contents)
	}
	if calls := nonemptyLines(readOptionalFile(t, filepath.Join(state, "gh-calls.log"))); len(calls) != 5 {
		t.Fatalf("gh calls=%v, want exactly 5", calls)
	}
	assertNoGHAPIReadTemporaryFiles(t, root)
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
			bin, state := installGHAPIReadFakes(t, root)
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

func installGHAPIReadFakes(t *testing.T, root string) (bin, state string) {
	t.Helper()
	bin = filepath.Join(root, "bin")
	state = filepath.Join(root, "state")
	makeDirectory(t, bin)
	makeDirectory(t, state)
	writeExecutable(t, filepath.Join(bin, "sleep"), `#!/usr/bin/env bash
set -euo pipefail
case ${1:-} in
  1|2|4|8) ;;
  *) echo "unexpected sleep duration: ${1:-missing}" >&2; exit 1 ;;
esac
printf '%s\n' "$1" >> "${FAKE_GH_READ_STATE:?}/sleep-calls.log"
`)
	writeExecutable(t, filepath.Join(bin, "gh"), `#!/usr/bin/env bash
set -euo pipefail
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
  transient)
    if ((count < 3)); then
      printf '{"partial":'
      exit 1
    fi
    printf '{"ok":true,"attempt":%s}\n' "$count"
    ;;
  failure)
    printf '{"partial":'
    exit 1
    ;;
  empty)
    :
    ;;
  success)
    printf '{"ok":true,"attempt":%s}\n' "$count"
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

func assertNoGHAPIReadTemporaryFiles(t *testing.T, directory string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(directory, ".*.gh-api-read.*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary response files remain: %v", matches)
	}
}
