package tests

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
)

const (
	releaseTestRepository = "example/env-vault"
	releaseTestVersion    = "v1.2.3"
	lightweightCommitSHA  = "1111111111111111111111111111111111111111"
	annotatedCommitSHA    = "2222222222222222222222222222222222222222"
)

var releaseTestArchives = []string{
	"env-vault-linux-amd64.tar.gz",
	"env-vault-linux-arm64.tar.gz",
	"env-vault-darwin-amd64.tar.gz",
	"env-vault-darwin-arm64.tar.gz",
	"env-vault-windows-amd64.zip",
}

func TestResolveTagSHAClassifiesGitHubResponses(t *testing.T) {
	fakeBin := installAPIFakeGH(t)
	tests := []struct {
		name       string
		mode       string
		wantStatus int
		wantOutput string
	}{
		{
			name:       "lightweight tag",
			mode:       "tag-lightweight",
			wantStatus: 0,
			wantOutput: lightweightCommitSHA,
		},
		{
			name:       "annotated tag chain",
			mode:       "tag-annotated",
			wantStatus: 0,
			wantOutput: annotatedCommitSHA,
		},
		{
			name:       "not found",
			mode:       "tag-404",
			wantStatus: 4,
			wantOutput: "tag ref not found",
		},
		{
			name:       "service unavailable",
			mode:       "tag-503",
			wantStatus: 1,
			wantOutput: "HTTP 503",
		},
		{
			name:       "network failure",
			mode:       "tag-network",
			wantStatus: 1,
			wantOutput: "no HTTP response",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output, status := runReleaseScript(
				t,
				"../scripts/release/resolve-tag-sha.sh",
				[]string{releaseTestVersion, releaseTestRepository},
				map[string]string{
					"FAKE_GH_MODE": test.mode,
					"PATH":         fakeBin + string(os.PathListSeparator) + os.Getenv("PATH"),
					"TMPDIR":       t.TempDir(),
				},
			)
			if status != test.wantStatus {
				t.Fatalf("exit status=%d, want %d\n%s", status, test.wantStatus, output)
			}
			if test.wantStatus == 0 {
				if got := strings.TrimSpace(output); got != test.wantOutput {
					t.Fatalf("resolved SHA=%q, want %q", got, test.wantOutput)
				}
			} else if !strings.Contains(output, test.wantOutput) {
				t.Fatalf("output does not contain %q:\n%s", test.wantOutput, output)
			}
		})
	}
}

func TestGetReleaseStateClassifiesGitHubResponses(t *testing.T) {
	fakeBin := installAPIFakeGH(t)
	tests := []struct {
		name       string
		mode       string
		wantStatus int
		wantOutput string
	}{
		{
			name:       "existing release",
			mode:       "release-present",
			wantStatus: 0,
			wantOutput: releaseTestVersion + "|false|false",
		},
		{
			name:       "not found",
			mode:       "release-404",
			wantStatus: 4,
			wantOutput: "GitHub Release not found",
		},
		{
			name:       "service unavailable",
			mode:       "release-503",
			wantStatus: 1,
			wantOutput: "HTTP 503",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output, status := runReleaseScript(
				t,
				"../scripts/release/get-release-state.sh",
				[]string{releaseTestVersion, releaseTestRepository},
				map[string]string{
					"FAKE_GH_MODE": test.mode,
					"PATH":         fakeBin + string(os.PathListSeparator) + os.Getenv("PATH"),
					"TMPDIR":       t.TempDir(),
				},
			)
			if status != test.wantStatus {
				t.Fatalf("exit status=%d, want %d\n%s", status, test.wantStatus, output)
			}
			if test.wantStatus == 0 {
				if got := strings.TrimSpace(output); got != test.wantOutput {
					t.Fatalf("release state=%q, want %q", got, test.wantOutput)
				}
			} else if !strings.Contains(output, test.wantOutput) {
				t.Fatalf("output does not contain %q:\n%s", test.wantOutput, output)
			}
		})
	}
}

func TestArtifactAttestationStateIsIdempotentAndFailClosed(t *testing.T) {
	tests := []struct {
		name         string
		mode         string
		wantStatus   int
		wantOutput   string
		wantAPICalls int
		wantVerifies int
	}{
		{
			name:         "complete evidence verifies and performs no mutation",
			mode:         "complete",
			wantStatus:   0,
			wantOutput:   "complete|complete",
			wantAPICalls: 10,
			wantVerifies: 10,
		},
		{
			name:         "explicitly missing SBOM evidence requests repair",
			mode:         "sbom-missing",
			wantStatus:   0,
			wantOutput:   "complete|missing",
			wantAPICalls: 10,
			wantVerifies: 5,
		},
		{
			name:         "existing SBOM does not duplicate missing provenance",
			mode:         "provenance-missing",
			wantStatus:   0,
			wantOutput:   "missing|complete",
			wantAPICalls: 10,
			wantVerifies: 5,
		},
		{
			name:         "API failure is not treated as missing",
			mode:         "api-503",
			wantStatus:   1,
			wantOutput:   "HTTP 503",
			wantAPICalls: 1,
		},
		{
			name:         "network failure is not treated as missing",
			mode:         "network",
			wantStatus:   1,
			wantOutput:   "no HTTP response",
			wantAPICalls: 1,
		},
		{
			name:         "unverifiable existing evidence is fatal",
			mode:         "verify-invalid",
			wantStatus:   1,
			wantOutput:   "fake gh: attestation verification failed",
			wantAPICalls: 5,
			wantVerifies: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			assetDir := filepath.Join(root, "assets")
			fakeBin := filepath.Join(root, "bin")
			callLog := filepath.Join(root, "gh-calls.log")
			makeDirectory(t, assetDir)
			writeReleaseAssetFixture(t, assetDir)
			installAttestationFakeGH(t, fakeBin)

			output, status := runReleaseScript(
				t,
				"../scripts/release/artifact-attestation-state.sh",
				[]string{
					assetDir,
					releaseTestRepository,
					releaseTestRepository + "/.github/workflows/build-binaries.yml",
					lightweightCommitSHA,
				},
				map[string]string{
					"FAKE_ATTEST_MODE": test.mode,
					"FAKE_GH_CALL_LOG": callLog,
					"PATH":             fakeBin + string(os.PathListSeparator) + os.Getenv("PATH"),
					"TMPDIR":           root,
				},
			)
			if status != test.wantStatus {
				t.Fatalf("exit status=%d, want %d\n%s", status, test.wantStatus, output)
			}
			if test.wantStatus == 0 {
				if got := strings.TrimSpace(output); got != test.wantOutput {
					t.Fatalf("state=%q, want %q", got, test.wantOutput)
				}
			} else if !strings.Contains(output, test.wantOutput) {
				t.Fatalf("output does not contain %q:\n%s", test.wantOutput, output)
			}

			calls := readOptionalFile(t, callLog)
			if got := strings.Count(calls, "api --method GET"); got != test.wantAPICalls {
				t.Fatalf("API calls=%d, want %d\n%s", got, test.wantAPICalls, calls)
			}
			if got := strings.Count(calls, "attestation verify"); got != test.wantVerifies {
				t.Fatalf("verification calls=%d, want %d\n%s", got, test.wantVerifies, calls)
			}
			if test.wantVerifies > 0 {
				for _, snippet := range []string{
					"--repo example/env-vault",
					"--signer-workflow example/env-vault/.github/workflows/build-binaries.yml",
					"--source-digest " + lightweightCommitSHA,
				} {
					if !strings.Contains(calls, snippet) {
						t.Fatalf("verification calls missing %q:\n%s", snippet, calls)
					}
				}
			}
			if test.mode == "complete" {
				if got := strings.Count(calls, "--predicate-type https://spdx.dev/Document/v2.3"); got != 5 {
					t.Fatalf("SPDX verification calls=%d, want 5\n%s", got, calls)
				}
				for _, predicate := range []string{
					"predicate_type=https://slsa.dev/provenance/v1",
					"predicate_type=https://spdx.dev/Document/v2.3",
				} {
					if got := strings.Count(calls, predicate); got != 5 {
						t.Fatalf("attestation API filter %q calls=%d, want 5\n%s", predicate, got, calls)
					}
				}
			}
			if test.mode == "sbom-missing" && strings.Contains(calls, "--predicate-type https://spdx.dev/Document/v2.3") {
				t.Fatalf("missing SBOM predicate must not verify or duplicate an absent SBOM attestation:\n%s", calls)
			}
			if test.mode == "provenance-missing" {
				if got := strings.Count(calls, "--predicate-type https://spdx.dev/Document/v2.3"); got != 5 {
					t.Fatalf("existing SBOM verification calls=%d, want 5\n%s", got, calls)
				}
			}
		})
	}
}

func TestReconcileReleaseAssets(t *testing.T) {
	tests := []struct {
		name          string
		missing       []string
		corrupt       string
		divergentPair bool
		extra         string
		wantUploads   []string
		wantStatus    int
		wantInOutput  string
		verifySuccess bool
	}{
		{
			name:          "complete release performs zero uploads",
			wantStatus:    0,
			verifySuccess: true,
		},
		{
			name:          "archive only uploads checksum",
			missing:       []string{releaseTestArchives[0] + ".sha256"},
			wantUploads:   []string{releaseTestArchives[0] + ".sha256"},
			wantStatus:    0,
			verifySuccess: true,
		},
		{
			name:          "matching checksum only uploads archive",
			missing:       []string{releaseTestArchives[0]},
			wantUploads:   []string{releaseTestArchives[0]},
			wantStatus:    0,
			verifySuccess: true,
		},
		{
			name:          "absent pair uploads archive and checksum",
			missing:       []string{releaseTestArchives[0], releaseTestArchives[0] + ".sha256"},
			wantUploads:   []string{releaseTestArchives[0], releaseTestArchives[0] + ".sha256"},
			wantStatus:    0,
			verifySuccess: true,
		},
		{
			name:         "checksum mismatch is fatal",
			missing:      []string{releaseTestArchives[0]},
			corrupt:      releaseTestArchives[0] + ".sha256",
			wantStatus:   1,
			wantInOutput: "existing release checksum differs from verified promotion",
		},
		{
			name:          "internally valid but divergent remote pair is fatal before upload",
			divergentPair: true,
			wantStatus:    1,
			wantInOutput:  "existing release archive differs from verified promotion",
		},
		{
			name:         "unexpected remote asset is fatal",
			extra:        "unexpected-debug-binary",
			wantStatus:   1,
			wantInOutput: "unexpected release asset",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			localDir := filepath.Join(root, "local")
			remoteDir := filepath.Join(root, "remote")
			verifiedDir := filepath.Join(root, "verified")
			fakeBin := filepath.Join(root, "bin")
			tmpDir := filepath.Join(root, "tmp")
			callLog := filepath.Join(root, "gh-calls.log")
			uploadLog := filepath.Join(root, "gh-uploads.log")

			makeDirectory(t, localDir)
			makeDirectory(t, remoteDir)
			makeDirectory(t, tmpDir)
			writeReleaseAssetFixture(t, localDir)
			copyReleaseAssetFixture(t, localDir, remoteDir)
			for _, name := range test.missing {
				if err := os.Remove(filepath.Join(remoteDir, name)); err != nil {
					t.Fatalf("remove remote fixture %s: %v", name, err)
				}
			}
			if test.corrupt != "" {
				badChecksum := strings.Repeat("0", 64) + "  " + strings.TrimSuffix(test.corrupt, ".sha256") + "\n"
				if err := os.WriteFile(filepath.Join(remoteDir, test.corrupt), []byte(badChecksum), 0o644); err != nil {
					t.Fatalf("corrupt remote checksum: %v", err)
				}
			}
			if test.divergentPair {
				archive := releaseTestArchives[0]
				contents := []byte("different but internally consistent remote archive\n")
				if err := os.WriteFile(filepath.Join(remoteDir, archive), contents, 0o644); err != nil {
					t.Fatalf("write divergent remote archive: %v", err)
				}
				digest := sha256.Sum256(contents)
				checksum := fmt.Sprintf("%x  %s\n", digest, archive)
				if err := os.WriteFile(filepath.Join(remoteDir, archive+".sha256"), []byte(checksum), 0o644); err != nil {
					t.Fatalf("write divergent remote checksum: %v", err)
				}
			}
			if test.extra != "" {
				if err := os.WriteFile(filepath.Join(remoteDir, test.extra), []byte("unexpected\n"), 0o644); err != nil {
					t.Fatalf("write unexpected remote asset: %v", err)
				}
			}
			installReleaseAssetsFakeGH(t, fakeBin)

			output, status := runReleaseScript(
				t,
				"../scripts/release/reconcile-release-assets.sh",
				[]string{releaseTestVersion, localDir, verifiedDir, releaseTestRepository},
				map[string]string{
					"FAKE_GH_CALL_LOG":   callLog,
					"FAKE_GH_REMOTE_DIR": remoteDir,
					"FAKE_GH_UPLOAD_LOG": uploadLog,
					"PATH":               fakeBin + string(os.PathListSeparator) + os.Getenv("PATH"),
					"TMPDIR":             tmpDir,
				},
			)
			if status != test.wantStatus {
				t.Fatalf("exit status=%d, want %d\n%s", status, test.wantStatus, output)
			}
			if test.wantInOutput != "" && !strings.Contains(output, test.wantInOutput) {
				t.Fatalf("output does not contain %q:\n%s", test.wantInOutput, output)
			}

			calls := readOptionalFile(t, callLog)
			if strings.Contains(calls, "--clobber") {
				t.Fatalf("reconciliation passed forbidden --clobber option:\n%s", calls)
			}
			gotUploads := nonemptyLines(readOptionalFile(t, uploadLog))
			sort.Strings(gotUploads)
			wantUploads := slices.Clone(test.wantUploads)
			sort.Strings(wantUploads)
			if !slices.Equal(gotUploads, wantUploads) {
				t.Fatalf("uploaded assets=%v, want %v\n%s", gotUploads, wantUploads, output)
			}

			if test.verifySuccess {
				assertReleaseAssetDirectory(t, remoteDir)
				assertReleaseAssetDirectory(t, verifiedDir)
			}
		})
	}
}

func installAPIFakeGH(t *testing.T) string {
	t.Helper()
	binDir := t.TempDir()
	script := `#!/usr/bin/env bash
set -euo pipefail

mode=${FAKE_GH_MODE:?FAKE_GH_MODE is required}
[[ ${1:-} == api ]] || {
  printf 'fake gh: unsupported command: %s\n' "$*" >&2
  exit 90
}

shift
include=false
if [[ ${1:-} == --include ]]; then
  include=true
  shift
fi
endpoint=${1:-}

if [[ $include == true ]]; then
  case "$mode" in
    tag-404|release-404)
      printf 'HTTP/2 404 Not Found\r\n\r\n'
      exit 1
      ;;
    tag-503|release-503)
      printf 'HTTP/2 503 Service Unavailable\r\n\r\n'
      exit 1
      ;;
    tag-network)
      printf 'dial tcp: network is unreachable\n' >&2
      exit 1
      ;;
    *)
      printf 'HTTP/2 200 OK\r\n\r\n'
      exit 0
      ;;
  esac
fi

case "$mode:$endpoint" in
  tag-lightweight:repos/example/env-vault/git/ref/tags/v1.2.3)
    printf 'commit\t1111111111111111111111111111111111111111\n'
    ;;
  tag-annotated:repos/example/env-vault/git/ref/tags/v1.2.3)
    printf 'tag\taaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n'
    ;;
  tag-annotated:repos/example/env-vault/git/tags/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa)
    printf 'tag\tbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\n'
    ;;
  tag-annotated:repos/example/env-vault/git/tags/bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb)
    printf 'commit\t2222222222222222222222222222222222222222\n'
    ;;
  release-present:repos/example/env-vault/releases/tags/v1.2.3)
    printf 'v1.2.3\tfalse\tfalse\n'
    ;;
  *)
    printf 'fake gh: unsupported API request in mode %s: %s\n' "$mode" "$endpoint" >&2
    exit 91
    ;;
esac
`
	writeExecutable(t, filepath.Join(binDir, "gh"), script)
	return binDir
}

func installReleaseAssetsFakeGH(t *testing.T, binDir string) {
	t.Helper()
	makeDirectory(t, binDir)
	script := `#!/usr/bin/env bash
set -euo pipefail

remote_dir=${FAKE_GH_REMOTE_DIR:?FAKE_GH_REMOTE_DIR is required}
call_log=${FAKE_GH_CALL_LOG:?FAKE_GH_CALL_LOG is required}
upload_log=${FAKE_GH_UPLOAD_LOG:?FAKE_GH_UPLOAD_LOG is required}
printf '%s\n' "$*" >> "$call_log"
for argument in "$@"; do
  if [[ $argument == --clobber ]]; then
    printf 'fake gh: --clobber is forbidden\n' >&2
    exit 92
  fi
done

if [[ ${1:-} == api ]]; then
  shopt -s nullglob
  for path in "$remote_dir"/*; do
    [[ -f $path && ! -L $path ]] || {
      printf 'fake gh: non-regular remote asset: %s\n' "$path" >&2
      exit 93
    }
    basename -- "$path"
  done
  exit 0
fi

[[ ${1:-} == release ]] || {
  printf 'fake gh: unsupported command: %s\n' "$*" >&2
  exit 94
}
operation=${2:-}
version=${3:-}
[[ $version == v1.2.3 ]] || {
  printf 'fake gh: unexpected version: %s\n' "$version" >&2
  exit 95
}
shift 3

case "$operation" in
  download)
    destination=''
    patterns=()
    while (($#)); do
      case "$1" in
        --repo)
          [[ ${2:-} == example/env-vault ]] || exit 96
          shift 2
          ;;
        --dir)
          destination=${2:-}
          shift 2
          ;;
        --pattern)
          patterns+=("${2:-}")
          shift 2
          ;;
        *)
          printf 'fake gh: unsupported download argument: %s\n' "$1" >&2
          exit 97
          ;;
      esac
    done
    [[ -n $destination && ${#patterns[@]} -gt 0 ]] || exit 98
    mkdir -p -- "$destination"
    for name in "${patterns[@]}"; do
      [[ -f "$remote_dir/$name" && ! -L "$remote_dir/$name" ]] || {
        printf 'fake gh: remote asset not found: %s\n' "$name" >&2
        exit 99
      }
      cp -- "$remote_dir/$name" "$destination/$name"
    done
    ;;
  upload)
    paths=()
    while (($#)); do
      case "$1" in
        --repo)
          [[ ${2:-} == example/env-vault ]] || exit 100
          shift 2
          ;;
        *)
          paths+=("$1")
          shift
          ;;
      esac
    done
    [[ ${#paths[@]} -gt 0 ]] || exit 101
    for path in "${paths[@]}"; do
      [[ -f $path && ! -L $path ]] || exit 102
      name=$(basename -- "$path")
      [[ ! -e "$remote_dir/$name" ]] || {
        printf 'fake gh: refusing to overwrite remote asset: %s\n' "$name" >&2
        exit 103
      }
      cp -- "$path" "$remote_dir/$name"
      printf '%s\n' "$name" >> "$upload_log"
    done
    ;;
  *)
    printf 'fake gh: unsupported release operation: %s\n' "$operation" >&2
    exit 104
    ;;
esac
`
	writeExecutable(t, filepath.Join(binDir, "gh"), script)
}

func installAttestationFakeGH(t *testing.T, binDir string) {
	t.Helper()
	makeDirectory(t, binDir)
	script := `#!/usr/bin/env bash
set -euo pipefail

mode=${FAKE_ATTEST_MODE:?FAKE_ATTEST_MODE is required}
call_log=${FAKE_GH_CALL_LOG:?FAKE_GH_CALL_LOG is required}
printf '%s\n' "$*" >> "$call_log"

case ${1:-} in
  api)
    predicate=''
    previous=''
    for argument in "$@"; do
      if [[ $previous == -f && $argument == predicate_type=* ]]; then
        predicate=${argument#predicate_type=}
      fi
      previous=$argument
    done
    case "$mode" in
      sbom-missing)
        if [[ $predicate == https://spdx.dev/Document/v2.3 ]]; then
          printf 'gh: Not Found (HTTP 404)\n' >&2
          exit 1
        fi
        ;;
      provenance-missing)
        if [[ $predicate == https://slsa.dev/provenance/v1 ]]; then
          printf 'gh: Not Found (HTTP 404)\n' >&2
          exit 1
        fi
        ;;
      api-503)
        printf 'gh: Service Unavailable (HTTP 503)\n' >&2
        exit 1
        ;;
      network)
        printf 'dial tcp: network is unreachable\n' >&2
        exit 1
        ;;
    esac
    printf '1\n'
    ;;
  attestation)
    [[ ${2:-} == verify ]] || exit 90
    if [[ $mode == verify-invalid ]]; then
      printf 'fake gh: attestation verification failed\n' >&2
      exit 1
    fi
    ;;
  *)
    printf 'fake gh: unsupported command: %s\n' "$*" >&2
    exit 91
    ;;
esac
`
	writeExecutable(t, filepath.Join(binDir, "gh"), script)
}

func runReleaseScript(t *testing.T, script string, args []string, overrides map[string]string) (string, int) {
	t.Helper()
	commandArgs := append([]string{script}, args...)
	cmd := exec.Command("bash", commandArgs...)
	cmd.Env = environmentWithOverrides(overrides)
	output, err := cmd.CombinedOutput()
	if err == nil {
		return string(output), 0
	}
	exitError, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("run %s: %v\n%s", script, err, output)
	}
	return string(output), exitError.ExitCode()
}

func environmentWithOverrides(overrides map[string]string) []string {
	environment := make([]string, 0, len(os.Environ())+len(overrides))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if _, overridden := overrides[key]; !overridden {
			environment = append(environment, entry)
		}
	}
	keys := make([]string, 0, len(overrides))
	for key := range overrides {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		environment = append(environment, key+"="+overrides[key])
	}
	return environment
}

func writeReleaseAssetFixture(t *testing.T, directory string) {
	t.Helper()
	for _, archive := range releaseTestArchives {
		contents := []byte("release fixture for " + archive + "\n")
		if err := os.WriteFile(filepath.Join(directory, archive), contents, 0o644); err != nil {
			t.Fatalf("write archive fixture %s: %v", archive, err)
		}
		digest := sha256.Sum256(contents)
		checksum := fmt.Sprintf("%x  %s\n", digest, archive)
		if err := os.WriteFile(filepath.Join(directory, archive+".sha256"), []byte(checksum), 0o644); err != nil {
			t.Fatalf("write checksum fixture %s: %v", archive, err)
		}
	}
}

func copyReleaseAssetFixture(t *testing.T, source, destination string) {
	t.Helper()
	for _, name := range releaseTestAssetNames() {
		contents, err := os.ReadFile(filepath.Join(source, name))
		if err != nil {
			t.Fatalf("read fixture %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(destination, name), contents, 0o644); err != nil {
			t.Fatalf("copy fixture %s: %v", name, err)
		}
	}
}

func assertReleaseAssetDirectory(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read asset directory %s: %v", directory, err)
	}
	wantNames := releaseTestAssetNames()
	gotNames := make([]string, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			t.Fatalf("stat asset %s: %v", entry.Name(), err)
		}
		if !info.Mode().IsRegular() {
			t.Fatalf("asset %s is not a regular file", entry.Name())
		}
		gotNames = append(gotNames, entry.Name())
	}
	sort.Strings(gotNames)
	if !slices.Equal(gotNames, wantNames) {
		t.Fatalf("asset names=%v, want exactly %v", gotNames, wantNames)
	}

	for _, archive := range releaseTestArchives {
		contents, err := os.ReadFile(filepath.Join(directory, archive))
		if err != nil {
			t.Fatalf("read archive %s: %v", archive, err)
		}
		digest := sha256.Sum256(contents)
		wantChecksum := fmt.Sprintf("%x  %s\n", digest, archive)
		checksum, err := os.ReadFile(filepath.Join(directory, archive+".sha256"))
		if err != nil {
			t.Fatalf("read checksum %s: %v", archive, err)
		}
		if string(checksum) != wantChecksum {
			t.Fatalf("checksum for %s=%q, want %q", archive, checksum, wantChecksum)
		}
	}
}

func releaseTestAssetNames() []string {
	names := make([]string, 0, len(releaseTestArchives)*2)
	for _, archive := range releaseTestArchives {
		names = append(names, archive, archive+".sha256")
	}
	sort.Strings(names)
	return names
}

func writeExecutable(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o755); err != nil {
		t.Fatalf("write executable %s: %v", path, err)
	}
}

func makeDirectory(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("create directory %s: %v", path, err)
	}
}

func readOptionalFile(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err == nil {
		return string(contents)
	}
	if os.IsNotExist(err) {
		return ""
	}
	t.Fatalf("read %s: %v", path, err)
	return ""
}

func nonemptyLines(contents string) []string {
	var lines []string
	for _, line := range strings.Split(contents, "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}
