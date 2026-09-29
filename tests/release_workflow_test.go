package tests

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// releaseWorkflowFile is the ADR 0011 release pipeline.
const releaseWorkflowFile = "release.yml"

// attestAction creates SLSA build provenance for the listed subjects.
const attestAction = "actions/attest@1e69f48acb82d1966a394da916b4c1698aa569d6"

// strictVerification lists the flags ADR 0012 requires on every attestation
// check in the release workflow.
var strictVerification = []string{
	`--repo "$GITHUB_REPOSITORY"`,
	`--signer-workflow "$GITHUB_REPOSITORY/.github/workflows/release.yml"`,
	"--source-ref refs/heads/main",
	`--source-digest "$RELEASE_SHA"`,
	"--deny-self-hosted-runners",
}

func TestReleaseWorkflowPublishesOnlyTheTaggedMainCommit(t *testing.T) {
	path := filepath.Join("..", ".github", "workflows", releaseWorkflowFile)
	wf := readWorkflow(t, path)
	raw := readFile(t, path)

	events := make([]string, 0, len(wf.On))
	for event := range wf.On {
		events = append(events, event)
	}
	sort.Strings(events)
	if !slices.Equal(events, []string{"pull_request", "push", "workflow_dispatch"}) {
		t.Fatalf("release workflow events=%v", events)
	}
	var push pushTrigger
	pushNode := wf.On["push"]
	if err := pushNode.Decode(&push); err != nil || !slices.Equal(push.Branches, []string{"main"}) || len(push.Tags) != 0 {
		t.Fatalf("release workflow must run on pushes to main only and never on a tag: %+v %v", push, err)
	}
	var pullRequest struct {
		Paths []string `yaml:"paths"`
	}
	pullRequestNode := wf.On["pull_request"]
	if err := pullRequestNode.Decode(&pullRequest); err != nil || !slices.Equal(pullRequest.Paths, []string{
		".github/workflows/release.yml",
		"scripts/release/package-archives.sh",
		"scripts/release/package-release.sh",
		"scripts/release/homebrew-formula.sh",
		"scripts/backend-smoke.sh",
	}) {
		t.Fatalf("release workflow pull request paths=%v %v", pullRequest.Paths, err)
	}
	assertPermissions(t, "release", wf.Permissions, map[string]string{"contents": "read"})
	if wf.Concurrency.Group != "release-${{ github.ref }}" || wf.Concurrency.Queue != "max" ||
		wf.Concurrency.CancelInProgress.Value || wf.Concurrency.CancelInProgress.Expression != "" {
		t.Fatalf("release concurrency=%+v, want queued runs that are never cancelled", wf.Concurrency)
	}
	for _, forbidden := range []string{"--clobber", "-X ", "-X=", "pull_request_target", "always()", "continue-on-error", "--admin", "or true"} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("release workflow contains %q", forbidden)
		}
	}
	// Every error message ends its step.
	if messages := regexp.MustCompile(`>&2;`).FindAllStringIndex(raw, -1); len(messages) != len(regexp.MustCompile(`>&2; exit 1(; \}| ;;)`).FindAllStringIndex(raw, -1)) {
		t.Fatal("release workflow prints an error without failing its step")
	}
	for name, want := range map[string]map[string]string{
		"build": {
			"GOTOOLCHAIN": "local",
			"RELEASE":     "${{ needs.release-please.outputs.release == 'true' }}",
			"RELEASE_TAG": "${{ needs.release-please.outputs.tag }}",
			"RELEASE_SHA": "${{ needs.release-please.outputs.sha }}",
		},
		"publish": {"RELEASE_TAG": "${{ needs.release-please.outputs.tag }}", "RELEASE_SHA": "${{ needs.release-please.outputs.sha }}"},
		"verify":  {"RELEASE_TAG": "${{ needs.release-please.outputs.tag }}", "RELEASE_SHA": "${{ needs.release-please.outputs.sha }}"},
		"tap": {
			"RELEASE_TAG":    "${{ needs.release-please.outputs.tag }}",
			"TAP_REPOSITORY": "ildarbinanas-design/homebrew-tap",
			"FORMULA_PATH":   "Formula/env-vault.rb",
		},
	} {
		if !mapsEqual(wf.Jobs[name].Env, want) {
			t.Fatalf("%s env=%v, want %v", name, wf.Jobs[name].Env, want)
		}
	}
	// Only the tap's pull request lookup may treat a failure as an answer.
	if strings.Count(raw, "|| true") != 1 || !strings.Contains(raw, `--json state --jq .state 2>/dev/null || true)"`) {
		t.Fatal("release workflow ignores a failure outside the tap's pull request lookup")
	}

	jobNames := make([]string, 0, len(wf.Jobs))
	for name := range wf.Jobs {
		jobNames = append(jobNames, name)
	}
	sort.Strings(jobNames)
	if !slices.Equal(jobNames, []string{"build", "package", "publish", "release-please", "tap", "verify"}) {
		t.Fatalf("release jobs=%v", jobNames)
	}
	wantEnvironments := map[string]string{"release-please": "release-planning", "publish": "release", "tap": "release"}
	wantSecrets := map[string]string{"release-please": "secrets.RELEASE_PLANNING_TOKEN", "tap": "secrets.HOMEBREW_TAP_TOKEN"}
	secretUses := 0
	for name, job := range wf.Jobs {
		switch name {
		case "publish":
			assertPermissions(t, name, job.Permissions, map[string]string{"contents": "write", "id-token": "write", "attestations": "write"})
		case "verify":
			assertPermissions(t, name, job.Permissions, map[string]string{"contents": "read", "attestations": "read"})
		default:
			if len(job.Permissions) != 0 {
				t.Fatalf("release job %s widens permissions: %v", name, job.Permissions)
			}
		}
		if job.Environment != wantEnvironments[name] {
			t.Fatalf("release job %s environment=%q, want %q", name, job.Environment, wantEnvironments[name])
		}
		for _, step := range job.Steps {
			values := make([]string, 0, len(step.With)+len(step.Env)+1)
			for _, value := range step.With {
				values = append(values, value)
			}
			for _, value := range step.Env {
				values = append(values, value)
			}
			values = append(values, step.Run)
			for _, value := range values {
				if !strings.Contains(value, "secrets.") {
					continue
				}
				secretUses++
				if value != "${{ "+wantSecrets[name]+" }}" {
					t.Fatalf("release job %s step %q uses a secret it must not: %s", name, step.Name, value)
				}
			}
		}
	}
	if strings.Count(raw, "secrets.") != secretUses {
		t.Fatalf("release workflow references secrets outside step inputs and environment variables")
	}

	planning := wf.Jobs["release-please"]
	if planning.If != "github.event_name == 'push' && github.ref == 'refs/heads/main'" {
		t.Fatalf("release-please runs on %q, want pushes to main only", planning.If)
	}
	if step := planning.Steps[0]; step.Uses != releasePleaseAction || !mapsEqual(step.With, map[string]string{
		"token":         "${{ secrets.RELEASE_PLANNING_TOKEN }}",
		"target-branch": "main",
		"config-file":   "release-please-config.json",
		"manifest-file": ".release-please-manifest.json",
	}) {
		t.Fatalf("release-please action=%q with=%v", step.Uses, step.With)
	}
	find := namedStep(t, planning, "Find a draft release for this commit")
	if !containsAll(find.Run,
		`previous="$(git show HEAD^:.release-please-manifest.json | jq -er '."."')"`,
		`refs="$(git ls-remote origin "refs/tags/$tag" "refs/tags/$tag^{}")"`,
		`release=false`,
		`[[ "$tagged" == "$GITHUB_SHA" ]]`,
		`gh release view "$tag" --repo "$GITHUB_REPOSITORY" --json isDraft --jq .isDraft`,
		`elif [[ "$version" != "$previous" ]]; then`,
		`printf 'sha=%s\n' "$GITHUB_SHA"`) {
		t.Fatalf("only the run for the tagged commit may build the release, and a release commit without its tag must fail: %s", find.Run)
	}
	if checkout := planning.Steps[1]; checkout.Uses != checkoutAction || checkout.With["fetch-depth"] != "2" {
		t.Fatalf("release-please must check out the commit with its parent to see a version change: %v", checkout.With)
	}
	if !mapsEqual(planning.Outputs, map[string]string{
		"release": "${{ steps.find.outputs.release }}",
		"tag":     "${{ steps.find.outputs.tag }}",
		"sha":     "${{ steps.find.outputs.sha }}",
	}) {
		t.Fatalf("release-please outputs=%v", planning.Outputs)
	}

	contract := readReleaseContract(t)
	platforms := make(map[string]contractPlatform, len(contract.Platforms))
	for _, platform := range contract.Platforms {
		platforms[platform.ID] = platform
	}
	build := wf.Jobs["build"]
	if !slices.Equal([]string(build.Needs), []string{"release-please"}) ||
		build.If != "${{ !cancelled() && (github.event_name != 'push' || needs.release-please.outputs.release == 'true') }}" {
		t.Fatalf("build needs=%v if=%q", build.Needs, build.If)
	}
	var matrix workflowMatrix
	if err := build.Strategy.Matrix.Decode(&matrix); err != nil {
		t.Fatalf("decode build matrix: %v", err)
	}
	if len(matrix.Include) != len(platforms) {
		t.Fatalf("build matrix has %d targets, contract has %d", len(matrix.Include), len(platforms))
	}
	seen := map[string]bool{}
	for _, target := range matrix.Include {
		if seen[target["id"]] {
			t.Fatalf("build matrix repeats target %q", target["id"])
		}
		seen[target["id"]] = true
		platform, ok := platforms[target["id"]]
		if !ok {
			t.Fatalf("build matrix target %q is not a contract platform", target["id"])
		}
		want := map[string]string{
			"id": platform.ID, "runner": platform.Runner, "goos": platform.GOOS,
			"goarch": platform.GOARCH, "cgo": platform.CGO, "binary": platform.Binary,
		}
		if !mapsEqual(target, want) {
			t.Fatalf("build matrix target %v, want contract platform %v", target, want)
		}
	}
	for _, name := range []string{"build", "package", "publish", "tap"} {
		if checkout := wf.Jobs[name].Steps[0]; checkout.Uses != checkoutAction {
			t.Fatalf("%s job must start with the pinned checkout", name)
		}
	}
	for name, job := range wf.Jobs {
		for _, step := range job.Steps {
			if strings.HasPrefix(step.Uses, "actions/checkout@") && (step.Uses != checkoutAction || step.With["persist-credentials"] != "false") {
				t.Fatalf("%s checks out with %q and persist-credentials=%q", name, step.Uses, step.With["persist-credentials"])
			}
		}
	}
	if build.Steps[0].With["fetch-depth"] != "0" {
		t.Fatal("build checkout must fetch tags so Go stamps the release version")
	}
	tagCheck := namedStep(t, build, "Check that the tag points to this commit")
	if tagCheck.If != "env.RELEASE == 'true'" || !containsAll(tagCheck.Run,
		`[[ "$(git rev-parse HEAD)" == "$RELEASE_SHA" ]]`,
		`tagged="$(git rev-parse --verify --quiet "refs/tags/$RELEASE_TAG^{commit}")"`,
		`[[ "$tagged" == "$RELEASE_SHA" ]]`) {
		t.Fatalf("build must stop unless the tag points to the checked-out release commit: %s", tagCheck.Run)
	}
	setupGo := 0
	for _, step := range build.Steps {
		if step.Uses == setupGoAction {
			setupGo++
			if step.With["go-version-file"] != "go.mod" || step.With["cache"] != "false" {
				t.Fatalf("build must set up Go from go.mod without a module cache: %v", step.With)
			}
		}
	}
	if setupGo != 1 {
		t.Fatalf("build sets up Go %d times, want once", setupGo)
	}
	buildStep := namedStep(t, build, "Build and check the build information")
	if !containsAll(buildStep.Run,
		`binary="$temp/release/$TARGET/$BINARY"`,
		`go build -trimpath -ldflags="-s -w" -o "$binary" ./cmd/env-vault`,
		`modified="$(awk '$1 == "build" && $2 ~ /^vcs\.modified=/ { print substr($2, 14); exit }' <<< "$info")"`,
		`[[ -n "$modified" ]] || { echo "build information has no VCS stamp" >&2; exit 1; }`,
		`[[ "$modified" == "false" ]] || { echo "build information reports a modified tree" >&2; exit 1; }`,
		`module_version="$(awk '$1 == "mod" { print $3; exit }' <<< "$info")"`,
		`reported="$("$binary" --version | awk '{ print $1; exit }')"`,
		`[[ -n "$module_version" && "$reported" == "$module_version" ]] ||`,
		`{ echo "--version reports '$reported', build information has '$module_version'" >&2; exit 1; }`,
		`if [[ "$RELEASE" == "true" ]]; then`,
		`[[ "$module_version" == "$RELEASE_TAG" ]] ||`) {
		t.Fatalf("build step must build outside the checkout, reject a modified tree and compare --version with Go build information and the tag: %s", buildStep.Run)
	}
	assertStepOrder(t, build, "Check that the tag points to this commit", "Build and check the build information", "Upload the binary", "Smoke-test the real OS secret store")
	if upload := namedStep(t, build, "Upload the binary"); upload.Uses != uploadArtifactAction || upload.With["name"] != "binary-${{ matrix.id }}" || upload.With["overwrite"] != "true" {
		t.Fatalf("build uploads %q as %q with overwrite=%q", upload.Uses, upload.With["name"], upload.With["overwrite"])
	}
	if smoke := namedStep(t, build, "Smoke-test the real OS secret store"); !strings.Contains(smoke.Run, "scripts/backend-smoke.sh") {
		t.Fatal("build job must smoke-test the real OS secret store")
	}

	pack := wf.Jobs["package"]
	if pack.If != "${{ !cancelled() && github.event_name != 'push' && needs.build.result == 'success' }}" || !slices.Equal([]string(pack.Needs), []string{"build"}) {
		t.Fatalf("package runs on %q after %v, want pull requests and manual runs after build", pack.If, pack.Needs)
	}
	if step := namedStep(t, pack, "Package deterministically"); step.Run != `scripts/release/package-release.sh "$RUNNER_TEMP/downloads" "$RUNNER_TEMP/archives"` {
		t.Fatalf("package step=%q", step.Run)
	}
	// Every flag the release verifies with must be checked on pull requests.
	flags := namedStep(t, pack, "Check the attestation verifier's flags")
	checked := regexp.MustCompile(`for flag in ([^;]*);`).FindStringSubmatch(flags.Run)
	if checked == nil || !containsAll(flags.Run, `help="$(gh attestation verify --help)"`, `grep -qe "$flag" <<< "$help"`) {
		t.Fatalf("pull requests must check the gh attestation flags: %s", flags.Run)
	}
	for _, verify := range strictVerification {
		if flag := strings.Fields(verify)[0]; !slices.Contains(strings.Fields(checked[1]), flag) {
			t.Fatalf("pull requests do not check that gh offers %s", flag)
		}
	}

	publish := wf.Jobs["publish"]
	if publish.If != "needs.release-please.outputs.release == 'true'" || !slices.Equal([]string(publish.Needs), []string{"release-please", "build"}) {
		t.Fatalf("publish runs on %q after %v", publish.If, publish.Needs)
	}
	for _, name := range []string{"package", "publish"} {
		if download := namedStep(t, wf.Jobs[name], "Download the binaries"); download.Uses != downloadAction || download.With["pattern"] != "binary-*" {
			t.Fatalf("%s downloads %q with pattern %q", name, download.Uses, download.With["pattern"])
		}
	}
	if step := namedStep(t, publish, "Package deterministically"); step.Run != `scripts/release/package-release.sh "$RUNNER_TEMP/downloads" "$RUNNER_TEMP/archives" "$RUNNER_TEMP/subjects"` {
		t.Fatalf("publish package step=%q", step.Run)
	}
	attest := namedStep(t, publish, "Attest the archives and the binaries")
	if attest.Uses != attestAction || !containsAll(attest.With["subject-path"],
		"${{ runner.temp }}/archives/env-vault-*.tar.gz",
		"${{ runner.temp }}/archives/env-vault-*.zip",
		"${{ runner.temp }}/subjects/env-vault-*") {
		t.Fatalf("attestation=%q with=%v", attest.Uses, attest.With)
	}
	assertStepOrder(t, publish, "Package deterministically", "Attest the archives and the binaries", "Verify the attestations", "Upload to the draft release and publish it")
	if verify := namedStep(t, publish, "Verify the attestations"); !containsAll(verify.Run, append([]string{"gh attestation verify"}, strictVerification...)...) {
		t.Fatalf("publish must verify every attestation with the ADR 0012 flags: %s", verify.Run)
	}
	if upload := namedStep(t, publish, "Upload to the draft release and publish it"); !containsAll(upload.Run,
		`if [[ "$draft" != "true" ]]; then`,
		`(( ${#pending[@]} == 0 )) ||`,
		`cmp -s "$file" "$existing/$name"`,
		`gh release upload "$RELEASE_TAG" --repo "$GITHUB_REPOSITORY" "${pending[@]}"`,
		`gh release edit "$RELEASE_TAG" --repo "$GITHUB_REPOSITORY" --draft=false`) {
		t.Fatalf("publish must upload to the draft without replacing assets and then publish it: %s", upload.Run)
	}

	verify := wf.Jobs["verify"]
	if !slices.Equal([]string(verify.Needs), []string{"release-please", "publish"}) || verify.If != "" {
		t.Fatalf("verify needs=%v if=%q, want it only after every earlier job succeeded", verify.Needs, verify.If)
	}
	if step := namedStep(t, verify, "Verify the published release"); !containsAll(step.Run, append([]string{
		`.draft == false and .immutable == true`,
		`gh api "repos/$GITHUB_REPOSITORY/commits/$RELEASE_TAG" --jq .sha`,
		`sha256sum -c ./*.sha256`,
		"gh attestation verify",
	}, strictVerification...)...) {
		t.Fatalf("verify must check immutability, the tag, the checksums and the attestations: %s", step.Run)
	}

	tap := wf.Jobs["tap"]
	if !slices.Equal([]string(tap.Needs), []string{"release-please", "verify"}) || tap.If != "" || tap.Env["TAP_REPOSITORY"] != "ildarbinanas-design/homebrew-tap" {
		t.Fatalf("tap needs=%v if=%q env=%v, want it only after a verified release", tap.Needs, tap.If, tap.Env)
	}
	if step := namedStep(t, tap, "Generate the formula from the published release"); !strings.Contains(step.Run, `scripts/release/homebrew-formula.sh "$RELEASE_TAG" "$assets" "$RUNNER_TEMP/env-vault.rb"`) {
		t.Fatalf("tap must generate the formula from the published release: %s", step.Run)
	}
	pr := namedStep(t, tap, "Open the tap pull request with auto-merge")
	if !containsAll(pr.Run, `gh pr merge "$branch" --repo "$TAP_REPOSITORY" --auto --squash`, `--base main --head "$branch"`) ||
		strings.Count(pr.Run, "gh pr merge") != 1 {
		t.Fatalf("tap must merge only through auto-merge after the required check: %s", pr.Run)
	}

	for _, script := range []string{"package-archives.sh", "package-release.sh"} {
		source := readFile(t, "../scripts/release/"+script)
		match := regexp.MustCompile(`(?m)^targets=\(([^)]*)\)$`).FindStringSubmatch(source)
		if match == nil {
			t.Fatalf("%s does not declare its targets", script)
		}
		scriptTargets := strings.Fields(match[1])
		sort.Strings(scriptTargets)
		contractTargets := make([]string, 0, len(platforms))
		for id := range platforms {
			contractTargets = append(contractTargets, id)
		}
		sort.Strings(contractTargets)
		if !slices.Equal(scriptTargets, contractTargets) {
			t.Fatalf("%s targets=%v, contract platforms=%v", script, scriptTargets, contractTargets)
		}
	}
}

func TestPackageArchivesIsDeterministicAndKeepsTheLayout(t *testing.T) {
	if out, err := exec.Command("tar", "--version").Output(); err != nil || !bytes.Contains(out, []byte("GNU tar")) {
		t.Skip("GNU tar is not available")
	}
	for _, tool := range []string{"python3", "sha256sum", "gzip"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not available", tool)
		}
	}
	contract := readReleaseContract(t)
	work := t.TempDir()
	binaries := filepath.Join(work, "binaries")
	for _, platform := range contract.Platforms {
		directory := filepath.Join(binaries, platform.ID)
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, platform.Binary), []byte("binary-"+platform.ID), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// An odd epoch shows the two-second rounding of zip timestamps.
	const epoch = int64(1790000001)
	run := func(output string) ([]byte, error) {
		cmd := exec.Command("bash", "scripts/release/package-archives.sh", binaries, output)
		cmd.Dir = ".."
		cmd.Env = append(os.Environ(), "SOURCE_DATE_EPOCH="+strconv.FormatInt(epoch, 10))
		return cmd.CombinedOutput()
	}
	first := filepath.Join(work, "first")
	second := filepath.Join(work, "second")
	for _, output := range []string{first, second} {
		if out, err := run(output); err != nil {
			t.Fatalf("package-archives.sh: %v\n%s", err, out)
		}
	}
	if out, err := run(first); err == nil || !strings.Contains(string(out), "refusing to reuse") {
		t.Fatalf("package-archives.sh reused an existing output directory: %v\n%s", err, out)
	}

	entries, err := os.ReadDir(first)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2*len(contract.Platforms) {
		t.Fatalf("output has %d files, want %d", len(entries), 2*len(contract.Platforms))
	}
	for _, platform := range contract.Platforms {
		archive := readBytes(t, filepath.Join(first, platform.Archive))
		if !bytes.Equal(archive, readBytes(t, filepath.Join(second, platform.Archive))) {
			t.Fatalf("%s differs between two runs over the same inputs", platform.Archive)
		}
		sum := sha256.Sum256(archive)
		wantSidecar := hex.EncodeToString(sum[:]) + "  " + platform.Archive + "\n"
		if got := string(readBytes(t, filepath.Join(first, platform.Checksum))); got != wantSidecar {
			t.Fatalf("%s=%q, want %q", platform.Checksum, got, wantSidecar)
		}

		prefix := "env-vault-" + platform.ID + "/"
		want := map[string]int64{
			prefix + "LICENSE":                0o644,
			prefix + "README.md":              0o644,
			prefix + "THIRD_PARTY_NOTICES.md": 0o644,
			prefix + platform.Binary:          0o755,
		}
		got := archiveEntries(t, platform.Archive, archive, epoch)
		if strings.HasSuffix(platform.Archive, ".tar.gz") {
			want[prefix] = 0o755
		}
		if !mapsEqual(got, want) {
			t.Fatalf("%s entries=%v, want %v", platform.Archive, got, want)
		}
	}
}

func TestPackageReleaseArrangesDownloadsAndAttestationSubjects(t *testing.T) {
	if out, err := exec.Command("tar", "--version").Output(); err != nil || !bytes.Contains(out, []byte("GNU tar")) {
		t.Skip("GNU tar is not available")
	}
	for _, tool := range []string{"python3", "sha256sum", "gzip", "git"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not available", tool)
		}
	}
	contract := readReleaseContract(t)
	work := t.TempDir()
	downloads := filepath.Join(work, "downloads")
	for _, platform := range contract.Platforms {
		directory := filepath.Join(downloads, "binary-"+platform.ID)
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, platform.Binary), []byte("binary-"+platform.ID), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run := func(args ...string) ([]byte, error) {
		cmd := exec.Command("bash", append([]string{"scripts/release/package-release.sh"}, args...)...)
		cmd.Dir = ".."
		cmd.Env = append(os.Environ(), "GITHUB_STEP_SUMMARY="+filepath.Join(work, "summary.md"))
		return cmd.CombinedOutput()
	}
	archives := filepath.Join(work, "archives")
	subjects := filepath.Join(work, "subjects")
	if out, err := run(downloads, archives, subjects); err != nil {
		t.Fatalf("package-release.sh: %v\n%s", err, out)
	}
	entries, err := os.ReadDir(archives)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2*len(contract.Platforms) {
		t.Fatalf("archives has %d files, want %d", len(entries), 2*len(contract.Platforms))
	}
	summary := string(readBytes(t, filepath.Join(work, "summary.md")))
	for _, platform := range contract.Platforms {
		if _, err := os.Stat(filepath.Join(archives, platform.Archive)); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(summary, "  "+platform.Archive+"\n") {
			t.Fatalf("summary lacks the checksum of %s:\n%s", platform.Archive, summary)
		}
		subject := "env-vault-" + platform.ID
		if strings.HasSuffix(platform.Binary, ".exe") {
			subject += ".exe"
		}
		if got := string(readBytes(t, filepath.Join(subjects, subject))); got != "binary-"+platform.ID {
			t.Fatalf("subject %s holds %q", subject, got)
		}
	}
	if !strings.Contains(summary, "GNU tar") || !strings.Contains(summary, "zlib") {
		t.Fatalf("summary lacks the tool versions:\n%s", summary)
	}

	if out, err := run(downloads, filepath.Join(work, "again"), subjects); err == nil || !strings.Contains(string(out), "refusing to reuse") {
		t.Fatalf("package-release.sh reused an attestation subject directory: %v\n%s", err, out)
	}
	if err := os.Remove(filepath.Join(downloads, "binary-windows-amd64", "env-vault.exe")); err != nil {
		t.Fatal(err)
	}
	if out, err := run(downloads, filepath.Join(work, "missing")); err == nil || !strings.Contains(string(out), "missing") {
		t.Fatalf("package-release.sh accepted a missing binary: %v\n%s", err, out)
	}
}

func TestHomebrewFormulaPinsTheVerifiedArchives(t *testing.T) {
	if _, err := exec.LookPath("sha256sum"); err != nil {
		t.Skip("sha256sum is not available")
	}
	targets := []string{"darwin-arm64", "darwin-amd64", "linux-arm64", "linux-amd64"}
	write := func(t *testing.T) (string, map[string]string) {
		t.Helper()
		assets := t.TempDir()
		sums := map[string]string{}
		for _, target := range targets {
			archive := "env-vault-" + target + ".tar.gz"
			data := []byte("archive-" + target)
			sum := sha256.Sum256(data)
			sums[target] = hex.EncodeToString(sum[:])
			if err := os.WriteFile(filepath.Join(assets, archive), data, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(assets, archive+".sha256"), []byte(sums[target]+"  "+archive+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return assets, sums
	}
	generate := func(tag, assets, output string) ([]byte, error) {
		cmd := exec.Command("bash", "scripts/release/homebrew-formula.sh", tag, assets, output)
		cmd.Dir = ".."
		return cmd.CombinedOutput()
	}

	assets, sums := write(t)
	output := filepath.Join(t.TempDir(), "env-vault.rb")
	if out, err := generate("v1.2.3", assets, output); err != nil {
		t.Fatalf("homebrew-formula.sh: %v\n%s", err, out)
	}
	url := func(target string) string {
		return "https://github.com/ildarbinanas-design/env-vault/releases/download/v1.2.3/env-vault-" + target + ".tar.gz"
	}
	want := `class EnvVault < Formula
  desc "Secure environment variable vault for running commands with profiles"
  homepage "https://github.com/ildarbinanas-design/env-vault"
  version "1.2.3"
  license "MIT"

  on_macos do
    depends_on macos: :sequoia

    on_arm do
      url "` + url("darwin-arm64") + `"
      sha256 "` + sums["darwin-arm64"] + `"
    end

    on_intel do
      url "` + url("darwin-amd64") + `"
      sha256 "` + sums["darwin-amd64"] + `"
    end
  end

  on_linux do
    on_arm do
      url "` + url("linux-arm64") + `"
      sha256 "` + sums["linux-arm64"] + `"
    end

    on_intel do
      url "` + url("linux-amd64") + `"
      sha256 "` + sums["linux-amd64"] + `"
    end
  end

  def install
    bin.install "env-vault"
    doc.install %w[README.md LICENSE THIRD_PARTY_NOTICES.md]
  end

  test do
    assert_match "v#{version}", shell_output("#{bin}/env-vault --version")
  end
end
`
	if got := string(readBytes(t, output)); got != want {
		t.Fatalf("formula:\n%s\nwant:\n%s", got, want)
	}

	for name, mutate := range map[string]func(t *testing.T, assets string){
		"archive changed": func(t *testing.T, assets string) {
			if err := os.WriteFile(filepath.Join(assets, "env-vault-linux-amd64.tar.gz"), []byte("tampered"), 0o644); err != nil {
				t.Fatal(err)
			}
		},
		"sidecar names another file": func(t *testing.T, assets string) {
			if err := os.WriteFile(filepath.Join(assets, "env-vault-darwin-arm64.tar.gz.sha256"), []byte(sums["darwin-arm64"]+"  other.tar.gz\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		},
		"sidecar has a second line": func(t *testing.T, assets string) {
			line := sums["darwin-amd64"] + "  env-vault-darwin-amd64.tar.gz\n"
			if err := os.WriteFile(filepath.Join(assets, "env-vault-darwin-amd64.tar.gz.sha256"), []byte(line+line), 0o644); err != nil {
				t.Fatal(err)
			}
		},
		"archive missing": func(t *testing.T, assets string) {
			if err := os.Remove(filepath.Join(assets, "env-vault-linux-arm64.tar.gz")); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			assets, _ := write(t)
			mutate(t, assets)
			output := filepath.Join(t.TempDir(), "env-vault.rb")
			if out, err := generate("v1.2.3", assets, output); err == nil {
				t.Fatalf("homebrew-formula.sh accepted a bad release: %s", out)
			}
			if _, err := os.Stat(output); !os.IsNotExist(err) {
				t.Fatalf("a failed run left a formula behind: %v", err)
			}
		})
	}
	if out, err := generate("1.2.3", assets, filepath.Join(t.TempDir(), "env-vault.rb")); err == nil {
		t.Fatalf("homebrew-formula.sh accepted a tag without v: %s", out)
	}
}

// archiveEntries returns each entry's mode after checking that every entry has
// the fixed owner and timestamp and that the binary holds the fixture bytes.
func archiveEntries(t *testing.T, name string, archive []byte, epoch int64) map[string]int64 {
	t.Helper()
	entries := map[string]int64{}
	checkBinary := func(entry string, read func() []byte) {
		base := filepath.Base(entry)
		if base == "env-vault" || base == "env-vault.exe" {
			want := "binary-" + strings.TrimSuffix(strings.TrimPrefix(filepath.Dir(entry), "env-vault-"), "/")
			if got := string(read()); got != want {
				t.Fatalf("%s: %s holds %q, want %q", name, entry, got, want)
			}
		}
	}
	if strings.HasSuffix(name, ".zip") {
		reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		// Zip stores times in two-second steps, so an odd epoch rounds down.
		for _, file := range reader.File {
			if file.Modified.Unix() != epoch&^1 {
				t.Fatalf("%s: %s modified %v, want %d", name, file.Name, file.Modified, epoch&^1)
			}
			entries[file.Name] = int64(file.Mode().Perm())
			checkBinary(file.Name, func() []byte {
				opened, err := file.Open()
				if err != nil {
					t.Fatal(err)
				}
				defer opened.Close()
				data, err := io.ReadAll(opened)
				if err != nil {
					t.Fatal(err)
				}
				return data
			})
		}
		return entries
	}
	compressed, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if compressed.Name != "" || !compressed.ModTime.IsZero() {
		t.Fatalf("%s: gzip header keeps name %q or time %v", name, compressed.Name, compressed.ModTime)
	}
	reader := tar.NewReader(compressed)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if header.Uid != 0 || header.Gid != 0 || header.ModTime.Unix() != epoch {
			t.Fatalf("%s: %s owner %d:%d time %v, want 0:0 at %d", name, header.Name, header.Uid, header.Gid, header.ModTime, epoch)
		}
		entries[header.Name] = header.Mode & 0o777
		checkBinary(header.Name, func() []byte {
			data, err := io.ReadAll(reader)
			if err != nil {
				t.Fatal(err)
			}
			return data
		})
	}
	return entries
}

func readBytes(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func mapsEqual[V comparable](got, want map[string]V) bool {
	if len(got) != len(want) {
		return false
	}
	for key, value := range want {
		if got[key] != value {
			return false
		}
	}
	return true
}
