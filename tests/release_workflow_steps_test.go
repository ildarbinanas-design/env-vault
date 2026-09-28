package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// These tests run release.yml's run blocks the way the runner's bash shell
// does, with a fake gh first on PATH and local git repositories, so that the
// fail-closed paths and the resume-by-re-run paths are exercised before a real
// release reaches them.

func requireReleaseStepTools(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the release run blocks run on Linux runners")
	}
	for _, tool := range []string{"bash", "git", "jq", "awk", "cmp", "sha256sum", "base64"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not available", tool)
		}
	}
	out, err := exec.Command("bash", "-c", "echo ${BASH_VERSINFO[0]}").Output()
	if major, _ := strconv.Atoi(strings.TrimSpace(string(out))); err != nil || major < 4 {
		t.Skip("the release run blocks need bash 4 or newer")
	}
	if err := exec.Command("base64", "-w0", "/dev/null").Run(); err != nil {
		t.Skip("the release run blocks need GNU base64")
	}
}

func releaseStepScript(t *testing.T, job, step string) string {
	t.Helper()
	wf := readWorkflow(t, filepath.Join("..", ".github", "workflows", releaseWorkflowFile))
	return namedStep(t, wf.Jobs[job], step).Run
}

// runReleaseStep runs script in dir with fakeGH as gh and returns the combined
// output and whether the script succeeded.
func runReleaseStep(t *testing.T, script, dir, fakeGH string, env map[string]string) (string, bool) {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte("#!/usr/bin/env bash\nset -euo pipefail\n"+fakeGH), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "--noprofile", "--norc", "-eo", "pipefail", "-c", script)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GITHUB_REPOSITORY=ildarbinanas-design/env-vault",
		"GH_TOKEN=fake")
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err == nil
}

func stepGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.invalid",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.invalid")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestReleaseFindStepBuildsOnlyTheTaggedReleaseCommit(t *testing.T) {
	requireReleaseStepTools(t)
	script := releaseStepScript(t, "release-please", "Find a draft release for this commit")
	const fakeGH = `[[ "$1 $2" == "release view" ]] || { echo "unexpected gh $*" >&2; exit 2; }
case "${FAKE_DRAFT:-}" in
  true | false) echo "$FAKE_DRAFT" ;;
  *) echo "release not found" >&2; exit 1 ;;
esac
`
	for _, tc := range []struct {
		name    string
		release bool   // the last commit changes the version
		tag     string // where the version's tag is: "", "head", "annotated" or "parent"
		draft   string // what gh reports as isDraft, or "" when the release cannot be read
		want    string // the release output, or "" when the step must fail
		failure string
	}{
		{name: "ordinary commit", want: "false"},
		{name: "ordinary commit after a release", tag: "parent", want: "false"},
		{name: "release commit with its draft", release: true, tag: "head", draft: "true", want: "true"},
		{name: "annotated tag on the release commit", release: true, tag: "annotated", draft: "true", want: "true"},
		{name: "re-run after publishing", release: true, tag: "head", draft: "false", want: "false"},
		{name: "unreadable release", release: true, tag: "head", failure: "its release cannot be read"},
		{name: "release commit without its tag", release: true, failure: "but the tag points to 'nothing'"},
		{name: "tag made by hand on another commit", release: true, tag: "parent", draft: "true", failure: "but the tag points to"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			origin := filepath.Join(dir, "origin.git")
			work := filepath.Join(dir, "work")
			stepGit(t, dir, "init", "-q", "--bare", origin)
			stepGit(t, dir, "init", "-q", "-b", "main", work)
			manifest := filepath.Join(work, ".release-please-manifest.json")
			if err := os.WriteFile(manifest, []byte(`{".": "0.3.4"}`+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			stepGit(t, work, "add", ".")
			stepGit(t, work, "commit", "-q", "-m", "one")
			version := "0.3.4"
			if tc.release {
				version = "0.4.0"
			}
			if err := os.WriteFile(manifest, []byte(`{".": "`+version+`"}`+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(work, "change"), []byte(tc.name), 0o644); err != nil {
				t.Fatal(err)
			}
			stepGit(t, work, "add", ".")
			stepGit(t, work, "commit", "-q", "-m", "two")
			stepGit(t, work, "remote", "add", "origin", origin)
			stepGit(t, work, "push", "-q", "origin", "main")
			tag := "v" + version
			switch tc.tag {
			case "head":
				stepGit(t, work, "tag", tag, "HEAD")
			case "annotated":
				stepGit(t, work, "tag", "-a", "-m", "by hand", tag, "HEAD")
			case "parent":
				stepGit(t, work, "tag", tag, "HEAD^")
			}
			if tc.tag != "" {
				stepGit(t, work, "push", "-q", "origin", tag)
			}

			output := filepath.Join(dir, "output")
			head := stepGit(t, work, "rev-parse", "HEAD")
			out, ok := runReleaseStep(t, script, work, fakeGH, map[string]string{
				"GITHUB_SHA": head, "GITHUB_OUTPUT": output, "FAKE_DRAFT": tc.draft,
			})
			if tc.want == "" {
				if ok || !strings.Contains(out, tc.failure) {
					t.Fatalf("find succeeded=%v, want a failure containing %q:\n%s", ok, tc.failure, out)
				}
				return
			}
			if !ok {
				t.Fatalf("find failed:\n%s", out)
			}
			got := readFile(t, output)
			if want := "release=" + tc.want + "\ntag=" + tag + "\nsha=" + head + "\n"; got != want {
				t.Fatalf("find outputs=%q, want %q", got, want)
			}
		})
	}
}

// releaseAssetNames are the ten files of a release.
func releaseAssetNames() []string {
	var names []string
	for _, target := range []string{"linux-amd64", "linux-arm64", "darwin-amd64", "darwin-arm64", "windows-amd64"} {
		archive := "env-vault-" + target + ".tar.gz"
		if strings.HasPrefix(target, "windows-") {
			archive = "env-vault-" + target + ".zip"
		}
		names = append(names, archive, archive+".sha256")
	}
	return names
}

func TestReleasePublishStepNeverReplacesAssetsAndResumes(t *testing.T) {
	requireReleaseStepTools(t)
	script := releaseStepScript(t, "publish", "Upload to the draft release and publish it")
	const fakeGH = `state="$FAKE_GH_STATE"
printf '%s\n' "$*" >> "$state/calls"
case "$1 $2" in
  "release view")
    [[ "$3" == "$RELEASE_TAG" ]] || exit 2
    if [[ " $* " == *" --json isDraft "* ]]; then cat "$state/draft"; exit 0; fi
    if [[ " $* " == *" --json assets "* ]]; then ls -1 "$state/assets"; exit 0; fi
    ;;
  "release download")
    pattern="" dir=""
    while (( $# )); do
      case "$1" in
        --pattern) pattern="$2"; shift 2 ;;
        --dir) dir="$2"; shift 2 ;;
        *) shift ;;
      esac
    done
    cp "$state/assets/$pattern" "$dir/$pattern"
    exit 0
    ;;
  "release upload")
    shift 3
    while (( $# )); do
      if [[ "$1" == --repo ]]; then shift 2; continue; fi
      name="$(basename "$1")"
      [[ ! -e "$state/assets/$name" ]] || { echo "$name already exists" >&2; exit 1; }
      cp "$1" "$state/assets/$name"
      shift
    done
    exit 0
    ;;
  "release edit")
    [[ " $* " == *" --draft=false "* ]] || exit 2
    echo false > "$state/draft"
    exit 0
    ;;
esac
echo "unexpected gh $*" >&2
exit 2
`
	names := releaseAssetNames()
	for _, tc := range []struct {
		name       string
		draft      bool
		uploaded   int  // assets an earlier attempt uploaded
		different  bool // the first uploaded asset has other bytes
		unexpected bool // the draft also holds a file the release does not build
		failure    string
		uploads    int // files the step must upload
		publishes  bool
	}{
		{name: "empty draft", draft: true, uploads: 10, publishes: true},
		{name: "re-run after a partial upload", draft: true, uploaded: 3, uploads: 7, publishes: true},
		{name: "uploaded asset with other bytes", draft: true, uploaded: 3, different: true, failure: "already exists with different bytes"},
		{name: "draft with an unexpected asset", draft: true, uploaded: 3, unexpected: true, failure: "has an unexpected asset env-vault-darwin-arm64-fixed.tar.gz"},
		{name: "re-run after publishing", uploaded: 10},
		{name: "published without an asset", uploaded: 9, failure: "is published without env-vault-windows-amd64.zip.sha256"},
		{name: "published asset with other bytes", uploaded: 10, different: true, failure: "already exists with different bytes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			temp := t.TempDir()
			state := t.TempDir()
			archives := filepath.Join(temp, "archives")
			assets := filepath.Join(state, "assets")
			for _, directory := range []string{archives, assets} {
				if err := os.MkdirAll(directory, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			for index, name := range names {
				if err := os.WriteFile(filepath.Join(archives, name), []byte("bytes of "+name), 0o644); err != nil {
					t.Fatal(err)
				}
				if index < tc.uploaded {
					contents := "bytes of " + name
					if tc.different && index == 0 {
						contents = "other bytes"
					}
					if err := os.WriteFile(filepath.Join(assets, name), []byte(contents), 0o644); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := os.WriteFile(filepath.Join(state, "draft"), []byte(strconv.FormatBool(tc.draft)+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if tc.unexpected {
				if err := os.WriteFile(filepath.Join(assets, "env-vault-darwin-arm64-fixed.tar.gz"), []byte("by hand"), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			out, ok := runReleaseStep(t, script, temp, fakeGH, map[string]string{
				"RUNNER_TEMP": temp, "RELEASE_TAG": "v0.4.0", "FAKE_GH_STATE": state,
			})
			calls := readFile(t, filepath.Join(state, "calls"))
			if tc.failure != "" {
				if ok || !strings.Contains(out, tc.failure) {
					t.Fatalf("publish succeeded=%v, want a failure containing %q:\n%s", ok, tc.failure, out)
				}
				if strings.Contains(calls, "release upload") || strings.Contains(calls, "release edit") {
					t.Fatalf("a failed publish changed the release:\n%s", calls)
				}
				return
			}
			if !ok {
				t.Fatalf("publish failed:\n%s", out)
			}
			uploads := 0
			for _, line := range strings.Split(calls, "\n") {
				if strings.HasPrefix(line, "release upload") {
					uploads += strings.Count(line, filepath.Join(temp, "archives")+"/")
				}
			}
			if uploads != tc.uploads || strings.Contains(calls, "release edit") != tc.publishes {
				t.Fatalf("publish uploaded %d files (want %d), published=%v (want %v):\n%s",
					uploads, tc.uploads, strings.Contains(calls, "release edit"), tc.publishes, calls)
			}
			for _, name := range names {
				if got := readFile(t, filepath.Join(assets, name)); got != "bytes of "+name {
					t.Fatalf("release asset %s=%q after publish", name, got)
				}
			}
			if got := readFile(t, filepath.Join(state, "draft")); got != "false\n" {
				t.Fatalf("release draft=%q after publish, want false", got)
			}
		})
	}
}

func TestReleaseTapStepOpensOnePullRequestAndResumes(t *testing.T) {
	requireReleaseStepTools(t)
	script := releaseStepScript(t, "tap", "Open the tap pull request with auto-merge")
	const fakeGH = `state="$FAKE_GH_STATE"
printf '%s\n' "$*" >> "$state/calls"
blob() { sha256sum "$1" | cut -c1-40; }
content() { printf '{"sha":"%s","content":"%s"}\n' "$(blob "$1")" "$(base64 -w0 "$1")"; }
if [[ "$1" == api ]]; then
  shift
  method=GET path="" jq="" ref="" sha="" branch="" body=""
  while (( $# )); do
    case "$1" in
      --method) method="$2"; shift 2 ;;
      --jq) jq="$2"; shift 2 ;;
      -f)
        case "${2%%=*}" in
          ref) ref="${2#*=}" ;;
          sha) sha="${2#*=}" ;;
          branch) branch="${2#*=}" ;;
          content) body="${2#*=}" ;;
        esac
        shift 2
        ;;
      *) path="$1"; shift ;;
    esac
  done
  formula="repos/$TAP_REPOSITORY/contents/$FORMULA_PATH"
  case "$method $path" in
    "GET $formula?ref=main")
      [[ "$jq" == .content ]] || exit 2
      base64 -w0 "$state/main.rb"
      ;;
    "GET repos/$TAP_REPOSITORY/branches/env-vault-$RELEASE_TAG")
      [[ -f "$state/branch.rb" ]] || { echo '{"message":"Branch not found"}'; exit 1; }
      ;;
    "GET repos/$TAP_REPOSITORY/git/ref/heads/main")
      [[ "$jq" == .object.sha ]] || exit 2
      echo base-commit
      ;;
    "POST repos/$TAP_REPOSITORY/git/refs")
      [[ "$ref" == "refs/heads/env-vault-$RELEASE_TAG" && "$sha" == base-commit && ! -f "$state/branch.rb" ]] || exit 2
      cp "$state/main.rb" "$state/branch.rb"
      ;;
    "GET repos/$TAP_REPOSITORY/compare/main...env-vault-$RELEASE_TAG")
      ahead=0 files="[]"
      if ! cmp -s "$state/main.rb" "$state/branch.rb"; then ahead=1 files="[{\"filename\":\"$FORMULA_PATH\"}]"; fi
      if [[ -f "$state/extra" ]]; then
        ahead=$((ahead + 1))
        files="$(jq -c --arg extra "$(cat "$state/extra")" '. + [{filename: $extra}]' <<< "$files")"
      fi
      if [[ -f "$state/ahead" ]]; then ahead="$(cat "$state/ahead")"; fi
      printf '{"ahead_by":%d,"files":%s}\n' "$ahead" "$files"
      ;;
    "GET $formula?ref=base-commit") content "$state/main.rb" ;;
    "GET $formula?ref=env-vault-$RELEASE_TAG") content "$state/branch.rb" ;;
    "PUT $formula")
      [[ "$branch" == "env-vault-$RELEASE_TAG" && "$sha" == "$(blob "$state/branch.rb")" ]] || { echo "stale update" >&2; exit 2; }
      base64 -d <<< "$body" > "$state/branch.rb"
      ;;
    *) echo "unexpected gh api $method $path" >&2; exit 2 ;;
  esac
  exit 0
fi
case "$1 $2" in
  "pr view") [[ -f "$state/pr" ]] || { echo "no pull requests found" >&2; exit 1; }; cat "$state/pr" ;;
  "pr create") echo OPEN > "$state/pr" ;;
  "pr merge") [[ " $* " == *" --auto --squash "* ]] || exit 2; echo auto > "$state/merge" ;;
  *) echo "unexpected gh $*" >&2; exit 2 ;;
esac
`
	const released, previous = "formula for v0.4.0\n", "formula for v0.3.4\n"
	for _, tc := range []struct {
		name      string
		main      string // the formula on the tap's main
		branch    string // the formula on the release branch, or "" without a branch
		pr        string // the pull request state, or "" without a pull request
		extra     string // another file the release branch changes
		ahead     string // the release branch's commits ahead of main, if not derived
		failure   string
		mutations []string // the gh calls that change the tap, in order
	}{
		{name: "tap already current", main: released},
		{name: "new release", main: previous, mutations: []string{"api --method POST", "api --method PUT", "pr create", "pr merge"}},
		{name: "re-run before the formula commit", main: previous, branch: previous, mutations: []string{"api --method PUT", "pr create", "pr merge"}},
		{name: "re-run with an open pull request", main: previous, branch: released, pr: "OPEN", mutations: []string{"pr merge"}},
		{name: "branch with another formula", main: previous, branch: "formula by hand\n", failure: "exists with a different formula"},
		{name: "branch made in advance with other changes", main: previous, branch: previous, extra: ".github/workflows/test-formula.yml", failure: "changes more than the formula"},
		{name: "re-run with other changes on the branch", main: previous, branch: released, pr: "OPEN", extra: ".github/workflows/test-formula.yml", failure: "changes more than the formula"},
		{name: "branch with a second formula commit", main: previous, branch: released, pr: "OPEN", ahead: "2", failure: "changes more than the formula"},
		{name: "closed pull request", main: previous, branch: released, pr: "CLOSED", failure: "is CLOSED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			temp := t.TempDir()
			state := t.TempDir()
			files := map[string]string{
				filepath.Join(temp, "env-vault.rb"): released,
				filepath.Join(state, "main.rb"):     tc.main,
				filepath.Join(state, "calls"):       "",
			}
			if tc.branch != "" {
				files[filepath.Join(state, "branch.rb")] = tc.branch
			}
			if tc.pr != "" {
				files[filepath.Join(state, "pr")] = tc.pr + "\n"
			}
			if tc.extra != "" {
				files[filepath.Join(state, "extra")] = tc.extra
			}
			if tc.ahead != "" {
				files[filepath.Join(state, "ahead")] = tc.ahead
			}
			for path, contents := range files {
				if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			out, ok := runReleaseStep(t, script, temp, fakeGH, map[string]string{
				"RUNNER_TEMP": temp, "RELEASE_TAG": "v0.4.0", "FAKE_GH_STATE": state,
				"TAP_REPOSITORY": "ildarbinanas-design/homebrew-tap", "FORMULA_PATH": "Formula/env-vault.rb",
			})
			var mutations []string
			for _, line := range strings.Split(readFile(t, filepath.Join(state, "calls")), "\n") {
				for _, mutation := range []string{"api --method POST", "api --method PUT", "pr create", "pr merge"} {
					if strings.HasPrefix(line, mutation) {
						mutations = append(mutations, mutation)
					}
				}
			}
			if tc.failure != "" {
				if ok || !strings.Contains(out, tc.failure) {
					t.Fatalf("tap succeeded=%v, want a failure containing %q:\n%s", ok, tc.failure, out)
				}
				if len(mutations) != 0 {
					t.Fatalf("a failed tap step changed the tap: %v", mutations)
				}
				return
			}
			if !ok {
				t.Fatalf("tap failed:\n%s", out)
			}
			if !slices.Equal(mutations, tc.mutations) {
				t.Fatalf("tap changes=%v, want %v:\n%s", mutations, tc.mutations, out)
			}
			if len(tc.mutations) > 0 {
				if got := readFile(t, filepath.Join(state, "branch.rb")); got != released {
					t.Fatalf("tap branch formula=%q, want the released formula", got)
				}
			}
		})
	}
}

// fakeAttestationVerify answers gh attestation verify: it requires exactly the
// ADR 0012 flags, fails for the files listed in $FAKE_GH_STATE/unattested and
// records every file it verified.
const fakeAttestationVerify = `if [[ "$1 $2" == "attestation verify" ]]; then
  file="$3"
  shift 3
  expected=(--repo "$GITHUB_REPOSITORY" --signer-workflow "$GITHUB_REPOSITORY/.github/workflows/release.yml"
    --source-ref refs/heads/main --source-digest "$RELEASE_SHA" --deny-self-hosted-runners)
  [[ "$*" == "${expected[*]}" ]] || { echo "unexpected verification flags: $*" >&2; exit 3; }
  if grep -qxF "$(basename "$file")" "$FAKE_GH_STATE/unattested" 2>/dev/null; then
    echo "no matching attestation" >&2
    exit 1
  fi
  basename "$file" >> "$FAKE_GH_STATE/verified"
  exit 0
fi
`

const releaseTestSHA = "1fd6638295fb616189e66da7cc110cf4831a3d94"

// writeReleaseFiles writes the five archives with their checksum sidecars.
func writeReleaseFiles(t *testing.T, directory string) {
	t.Helper()
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range releaseAssetNames() {
		if strings.HasSuffix(name, ".sha256") {
			continue
		}
		path := filepath.Join(directory, name)
		if err := os.WriteFile(path, []byte("bytes of "+name), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command("sha256sum", path).Output()
		if err != nil {
			t.Fatal(err)
		}
		digest := strings.Fields(string(out))[0]
		if err := os.WriteFile(path+".sha256", []byte(digest+"  "+name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func verifiedFiles(t *testing.T, state string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(state, "verified"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	files := strings.Fields(string(data))
	slices.Sort(files)
	return files
}

func releaseArchiveNames() []string {
	var archives []string
	for _, name := range releaseAssetNames() {
		if !strings.HasSuffix(name, ".sha256") {
			archives = append(archives, name)
		}
	}
	slices.Sort(archives)
	return archives
}

func TestReleasePublishVerifiesEveryArchiveAndBinary(t *testing.T) {
	requireReleaseStepTools(t)
	script := releaseStepScript(t, "publish", "Verify the attestations")
	for _, tc := range []struct {
		name       string
		unattested string
	}{
		{name: "all attested"},
		{name: "unattested binary", unattested: "env-vault-windows-amd64.exe"},
		{name: "unattested archive", unattested: "env-vault-darwin-arm64.tar.gz"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			temp := t.TempDir()
			state := t.TempDir()
			writeReleaseFiles(t, filepath.Join(temp, "archives"))
			subjects := filepath.Join(temp, "subjects")
			if err := os.MkdirAll(subjects, 0o755); err != nil {
				t.Fatal(err)
			}
			var want []string
			for _, target := range []string{"linux-amd64", "linux-arm64", "darwin-amd64", "darwin-arm64", "windows-amd64"} {
				binary := "env-vault-" + target
				if strings.HasPrefix(target, "windows-") {
					binary += ".exe"
				}
				if err := os.WriteFile(filepath.Join(subjects, binary), []byte(binary), 0o755); err != nil {
					t.Fatal(err)
				}
				want = append(want, binary)
			}
			want = append(want, releaseArchiveNames()...)
			slices.Sort(want)
			if tc.unattested != "" {
				if err := os.WriteFile(filepath.Join(state, "unattested"), []byte(tc.unattested+"\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			out, ok := runReleaseStep(t, script, temp, fakeAttestationVerify+"echo \"unexpected gh $*\" >&2\nexit 2\n", map[string]string{
				"RUNNER_TEMP": temp, "RELEASE_SHA": releaseTestSHA, "FAKE_GH_STATE": state,
			})
			if tc.unattested != "" {
				if ok || !strings.Contains(out, "no matching attestation") {
					t.Fatalf("publish accepted the unattested %s:\n%s", tc.unattested, out)
				}
				return
			}
			if !ok {
				t.Fatalf("attestation check failed:\n%s", out)
			}
			if got := verifiedFiles(t, state); !slices.Equal(got, want) {
				t.Fatalf("verified %v, want every archive and binary %v", got, want)
			}
		})
	}
}

func TestReleaseVerifyStepChecksThePublishedRelease(t *testing.T) {
	requireReleaseStepTools(t)
	script := releaseStepScript(t, "verify", "Verify the published release")
	const fakeGH = fakeAttestationVerify + `state="$FAKE_GH_STATE"
case "$1" in
  api)
    case "$2" in
      "repos/$GITHUB_REPOSITORY/releases/tags/$RELEASE_TAG") cat "$state/release.json" ;;
      "repos/$GITHUB_REPOSITORY/commits/$RELEASE_TAG")
        [[ "$3 $4" == "--jq .sha" ]] || exit 2
        cat "$state/tagged"
        ;;
      *) echo "unexpected gh api $2" >&2; exit 2 ;;
    esac
    ;;
  release)
    [[ "$2 $3 $4 $5 $6" == "download $RELEASE_TAG --repo $GITHUB_REPOSITORY --dir" ]] || exit 2
    mkdir -p "$7"
    cp "$state/assets/"* "$7/"
    ;;
  *) echo "unexpected gh $*" >&2; exit 2 ;;
esac
`
	for _, tc := range []struct {
		name    string
		release string
		tagged  string
		change  func(t *testing.T, assets string)
		failure string
	}{
		{name: "published immutable release"},
		{name: "draft", release: `{"draft":true,"immutable":false}`, failure: "is not published as an immutable release"},
		{name: "mutable release", release: `{"draft":false,"immutable":false}`, failure: "is not published as an immutable release"},
		{name: "tag on another commit", tagged: "0000000000000000000000000000000000000000", failure: "not " + releaseTestSHA},
		{name: "missing checksum", change: func(t *testing.T, assets string) {
			if err := os.Remove(filepath.Join(assets, "env-vault-linux-arm64.tar.gz.sha256")); err != nil {
				t.Fatal(err)
			}
		}, failure: "differ from the expected ten"},
		{name: "extra asset", change: func(t *testing.T, assets string) {
			if err := os.WriteFile(filepath.Join(assets, "env-vault-darwin-arm64-fixed.tar.gz"), []byte("by hand"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, failure: "differ from the expected ten"},
		{name: "archive that does not match its checksum", change: func(t *testing.T, assets string) {
			if err := os.WriteFile(filepath.Join(assets, "env-vault-darwin-amd64.tar.gz"), []byte("other bytes"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, failure: "FAILED"},
		{name: "unattested archive", change: func(t *testing.T, assets string) {
			if err := os.WriteFile(filepath.Join(filepath.Dir(assets), "unattested"), []byte("env-vault-windows-amd64.zip\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, failure: "no matching attestation"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			temp := t.TempDir()
			state := t.TempDir()
			assets := filepath.Join(state, "assets")
			writeReleaseFiles(t, assets)
			if tc.change != nil {
				tc.change(t, assets)
			}
			release, tagged := tc.release, tc.tagged
			if release == "" {
				release = `{"draft":false,"immutable":true}`
			}
			if tagged == "" {
				tagged = releaseTestSHA
			}
			for name, contents := range map[string]string{"release.json": release + "\n", "tagged": tagged + "\n"} {
				if err := os.WriteFile(filepath.Join(state, name), []byte(contents), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			out, ok := runReleaseStep(t, script, temp, fakeGH, map[string]string{
				"RUNNER_TEMP": temp, "RELEASE_TAG": "v0.4.0", "RELEASE_SHA": releaseTestSHA, "FAKE_GH_STATE": state,
			})
			if tc.failure != "" {
				if ok || !strings.Contains(out, tc.failure) {
					t.Fatalf("verify succeeded=%v, want a failure containing %q:\n%s", ok, tc.failure, out)
				}
				return
			}
			if !ok {
				t.Fatalf("verify failed:\n%s", out)
			}
			if got, want := verifiedFiles(t, state), releaseArchiveNames(); !slices.Equal(got, want) {
				t.Fatalf("verified %v, want every archive %v", got, want)
			}
		})
	}
}
