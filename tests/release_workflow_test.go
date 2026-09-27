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

func TestReleaseWorkflowBuildsAndPackagesWithoutPublishing(t *testing.T) {
	path := filepath.Join("..", ".github", "workflows", releaseWorkflowFile)
	wf := readWorkflow(t, path)
	raw := readFile(t, path)

	events := make([]string, 0, len(wf.On))
	for event := range wf.On {
		events = append(events, event)
	}
	sort.Strings(events)
	if !slices.Equal(events, []string{"pull_request", "workflow_dispatch"}) {
		t.Fatalf("release workflow events=%v, want only pull_request and workflow_dispatch until the ADR 0011 switch", events)
	}
	assertPermissions(t, "release", wf.Permissions, map[string]string{"contents": "read"})
	for _, marker := range []string{"secrets.", "id-token", "attestations:", "contents: write", "environment:"} {
		if strings.Contains(raw, marker) {
			t.Fatalf("release workflow gains publishing capability %q before the ADR 0011 switch", marker)
		}
	}
	jobNames := make([]string, 0, len(wf.Jobs))
	for name, job := range wf.Jobs {
		jobNames = append(jobNames, name)
		if len(job.Permissions) != 0 || job.Environment != "" {
			t.Fatalf("release job %s widens permissions or enters an environment", name)
		}
	}
	sort.Strings(jobNames)
	if !slices.Equal(jobNames, []string{"build", "package"}) {
		t.Fatalf("release jobs=%v, want build and package", jobNames)
	}

	contract := readReleaseContract(t)
	platforms := make(map[string]contractPlatform, len(contract.Platforms))
	for _, platform := range contract.Platforms {
		platforms[platform.ID] = platform
	}
	build := wf.Jobs["build"]
	var matrix workflowMatrix
	if err := build.Strategy.Matrix.Decode(&matrix); err != nil {
		t.Fatalf("decode build matrix: %v", err)
	}
	if len(matrix.Include) != len(platforms) {
		t.Fatalf("build matrix has %d targets, contract has %d", len(matrix.Include), len(platforms))
	}
	for _, target := range matrix.Include {
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

	if checkout := build.Steps[0]; checkout.Uses != checkoutAction {
		t.Fatalf("build job starts with %q, want the pinned checkout", checkout.Uses)
	} else if checkout.With["fetch-depth"] != "0" {
		t.Fatal("build checkout must fetch tags so Go stamps the release version")
	}
	buildStep := namedStep(t, build, "Build and check the build information")
	if !containsAll(buildStep.Run,
		`go build -trimpath -ldflags="-s -w" -o "$binary" ./cmd/env-vault`,
		`$2 == "vcs.modified=false"`,
		`"$binary" --version`) {
		t.Fatalf("build step must build outside the checkout and compare --version with Go build information: %s", buildStep.Run)
	}
	if strings.Contains(buildStep.Run, "-X ") || strings.Contains(buildStep.Run, "-X=") {
		t.Fatal("the release version must come from Go build information, not -ldflags -X")
	}
	if smoke := namedStep(t, build, "Smoke-test the real OS secret store"); !strings.Contains(smoke.Run, "scripts/backend-smoke.sh") {
		t.Fatal("build job must smoke-test the real OS secret store")
	}

	pack := wf.Jobs["package"]
	if !slices.Equal([]string(pack.Needs), []string{"build"}) {
		t.Fatalf("package needs=%v, want build", pack.Needs)
	}
	packageStep := namedStep(t, pack, "Package deterministically")
	if strings.Count(packageStep.Run, "scripts/release/package-archives.sh") != 2 ||
		!containsAll(packageStep.Run, `git log -1 --format=%ct HEAD`, `diff -r`, `sha256sum -c`) {
		t.Fatalf("package step must package twice from the commit time and compare: %s", packageStep.Run)
	}

	script := readFile(t, "../scripts/release/package-archives.sh")
	match := regexp.MustCompile(`(?m)^targets=\(([^)]*)\)$`).FindStringSubmatch(script)
	if match == nil {
		t.Fatal("package-archives.sh does not declare its targets")
	}
	scriptTargets := strings.Fields(match[1])
	sort.Strings(scriptTargets)
	contractTargets := make([]string, 0, len(platforms))
	for id := range platforms {
		contractTargets = append(contractTargets, id)
	}
	sort.Strings(contractTargets)
	if !slices.Equal(scriptTargets, contractTargets) {
		t.Fatalf("package-archives.sh targets=%v, contract platforms=%v", scriptTargets, contractTargets)
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

	const epoch = int64(1790000000)
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
		for _, file := range reader.File {
			if file.Modified.Unix() != epoch {
				t.Fatalf("%s: %s modified %v, want %d", name, file.Name, file.Modified, epoch)
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
