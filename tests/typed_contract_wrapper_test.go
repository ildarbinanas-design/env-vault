package tests

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseContractCheckerOutputsAreStableAcrossEquivalentBuilds(t *testing.T) {
	directory := t.TempDir()
	checkers := []string{filepath.Join(directory, "releasecheck-a"), filepath.Join(directory, "releasecheck-b")}
	for _, checker := range checkers {
		command := exec.Command("go", "build", "-trimpath", "-o", checker, "../cmd/releasecheck")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("build equivalent checker: %v\n%s", err, output)
		}
	}
	firstBinary, err := os.ReadFile(checkers[0])
	if err != nil {
		t.Fatal(err)
	}
	secondBinary, err := os.ReadFile(checkers[1])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBinary, secondBinary) {
		t.Fatal("equivalent same-checkout checker builds differ")
	}
	commands := [][]string{
		{"--contract", "../release/contract.v2.json", "--version", "--json"},
		{"contract", "operational", "--contract", "../release/contract.v2.json", "--json"},
	}
	for _, arguments := range commands {
		first, err := exec.Command(checkers[0], arguments...).Output()
		if err != nil {
			t.Fatalf("run first equivalent checker %v: %v", arguments, err)
		}
		second, err := exec.Command(checkers[1], arguments...).Output()
		if err != nil {
			t.Fatalf("run second equivalent checker %v: %v", arguments, err)
		}
		if !bytes.Equal(first, second) {
			t.Fatalf("equivalent checker output differs for %v", arguments)
		}
		if len(first) == 0 || first[len(first)-1] != '\n' {
			t.Fatalf("checker output lacks one terminal record newline for %v", arguments)
		}
	}
}

func TestWithTypedContractWrapperRequiresCommandBeforeBuild(t *testing.T) {
	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	makeDirectory(t, binDir)
	buildLog := filepath.Join(root, "go-build.log")
	writeExecutable(t, filepath.Join(binDir, "go"), `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "${FAKE_GO_BUILD_LOG:?}"
exit 97
`)

	output, status := runReleaseScript(t, "../scripts/release/with-typed-contract.sh", nil, map[string]string{
		"FAKE_GO_BUILD_LOG": buildLog,
		"PATH":              binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"TMPDIR":            root,
	})
	if status != 2 || !strings.Contains(output, "usage:") {
		t.Fatalf("status=%d output=%q, want usage before build", status, output)
	}
	if calls := readOptionalFile(t, buildLog); calls != "" {
		t.Fatalf("no-command invocation built tooling:\n%s", calls)
	}
}

func TestWithTypedContractWrapperBuildsOneCheckerOverridesCallerAndCleansUp(t *testing.T) {
	for _, test := range []struct {
		name       string
		mode       string
		wantStatus int
	}{
		{name: "success", mode: "success", wantStatus: 0},
		{name: "command failure", mode: "failure", wantStatus: 42},
		{name: "termination signal", mode: "signal", wantStatus: 143},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			binDir := filepath.Join(root, "bin")
			makeDirectory(t, binDir)
			buildLog := filepath.Join(root, "go-build.log")
			observation := filepath.Join(root, "observation.txt")
			releasecheckTemplate := filepath.Join(root, "releasecheck-template")
			command := filepath.Join(root, "observe-contract")

			writeExecutable(t, releasecheckTemplate, `#!/usr/bin/env bash
set -euo pipefail
if [[ ${1:-} == --contract && ${3:-} == --version && ${4:-} == --json ]]; then
  cat -- "${FAKE_VERSION_FILE:?}"
elif [[ ${1:-} == contract && ${2:-} == operational && ${3:-} == --contract && ${5:-} == --json ]]; then
  cat -- "${FAKE_PROJECTION_FILE:?}"
elif [[ ${1:-} == validate-contract && ${2:-} == --contract && ${4:-} == --json ]]; then
  printf '%s\n' '{"ok":true}'
else
  printf 'fake releasecheck: unsupported invocation: %s\n' "$*" >&2
  exit 91
fi
`)
			writeExecutable(t, filepath.Join(binDir, "go"), `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "${FAKE_GO_BUILD_LOG:?}"
output=''
while [[ $# -gt 0 ]]; do
  if [[ $1 == -o ]]; then
    shift
    output=${1:-}
    break
  fi
  shift
done
[[ -n $output ]]
cp -- "${FAKE_RELEASECHECK_TEMPLATE:?}" "$output"
chmod 0700 "$output"
`)
			writeExecutable(t, command, `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$RELEASECHECK" "$RELEASE_CONTRACT_CHECKER" "$RELEASE_CONTRACT_VERSION_FILE" "$RELEASE_CONTRACT_PROJECTION_FILE" > "${WRAPPER_OBSERVATION:?}"
[[ "$RELEASECHECK" != /caller/releasecheck ]]
[[ "$RELEASE_CONTRACT_CHECKER" == "$RELEASECHECK" ]]
[[ "$RELEASE_CONTRACT_CHECKER" != /caller/contract-checker ]]
[[ "$RELEASE_CONTRACT_VERSION_FILE" != /caller/version.json ]]
[[ "$RELEASE_CONTRACT_PROJECTION_FILE" != /caller/projection.json ]]
cmp -- "$RELEASE_CONTRACT_VERSION_FILE" "${FAKE_VERSION_FILE:?}"
cmp -- "$RELEASE_CONTRACT_PROJECTION_FILE" "${FAKE_PROJECTION_FILE:?}"
source "${TEST_REPOSITORY_ROOT:?}/scripts/release/lib.sh"
release_require_typed_contract_projection
case "${WRAPPER_MODE:?}" in
  success) exit 0 ;;
  failure) exit 42 ;;
  signal) kill -TERM "$PPID"; exit 0 ;;
  *) exit 92 ;;
esac
`)

			output, status := runReleaseScript(t, "../scripts/release/with-typed-contract.sh", []string{command}, map[string]string{
				"FAKE_GO_BUILD_LOG":                buildLog,
				"FAKE_PROJECTION_FILE":             os.Getenv("RELEASE_CONTRACT_PROJECTION_FILE"),
				"FAKE_RELEASECHECK_TEMPLATE":       releasecheckTemplate,
				"FAKE_VERSION_FILE":                os.Getenv("RELEASE_CONTRACT_VERSION_FILE"),
				"PATH":                             binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
				"RELEASECHECK":                     "/caller/releasecheck",
				"RELEASE_CONTRACT_CHECKER":         "/caller/contract-checker",
				"RELEASE_CONTRACT_PROJECTION_FILE": "/caller/projection.json",
				"RELEASE_CONTRACT_VERSION_FILE":    "/caller/version.json",
				"TEST_REPOSITORY_ROOT":             filepath.Clean(".."),
				"TMPDIR":                           root,
				"WRAPPER_MODE":                     test.mode,
				"WRAPPER_OBSERVATION":              observation,
			})
			if status != test.wantStatus {
				t.Fatalf("status=%d, want %d\n%s", status, test.wantStatus, output)
			}
			calls := nonemptyLines(readOptionalFile(t, buildLog))
			if len(calls) != 1 || !strings.Contains(calls[0], "build -trimpath -o") || !strings.HasSuffix(calls[0], " ./cmd/releasecheck") {
				t.Fatalf("checker builds=%v, want one exact releasecheck build", calls)
			}
			paths := nonemptyLines(readOptionalFile(t, observation))
			if len(paths) != 4 {
				t.Fatalf("typed wrapper observation=%v", paths)
			}
			for _, path := range paths {
				if _, err := os.Lstat(path); !os.IsNotExist(err) {
					t.Fatalf("wrapper temporary path survived %s: %s (%v)", test.mode, path, err)
				}
			}
			if _, err := os.Lstat(filepath.Dir(paths[0])); !os.IsNotExist(err) {
				t.Fatalf("wrapper temporary directory survived %s: %s (%v)", test.mode, filepath.Dir(paths[0]), err)
			}
		})
	}
}
