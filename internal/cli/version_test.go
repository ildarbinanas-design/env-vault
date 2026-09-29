package cli

import (
	"bytes"
	"encoding/json"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
)

const testCommit = "1fd6638295fb616189e66da7cc110cf4831a3d94"

// stubBuildInfo makes the version command read info instead of the test
// binary's own build information.
func stubBuildInfo(t *testing.T, info *debug.BuildInfo) {
	t.Helper()
	old := readBuildInfo
	readBuildInfo = func() (*debug.BuildInfo, bool) { return info, info != nil }
	t.Cleanup(func() { readBuildInfo = old })
}

func releaseBuildInfo(version string, modified bool) *debug.BuildInfo {
	info := &debug.BuildInfo{Main: debug.Module{Path: "github.com/ildarbinanas-design/env-vault", Version: version}}
	info.Settings = []debug.BuildSetting{
		{Key: "vcs", Value: "git"},
		{Key: "vcs.revision", Value: testCommit},
		{Key: "vcs.time", Value: "2026-09-27T10:16:51Z"},
		{Key: "vcs.modified", Value: map[bool]string{true: "true", false: "false"}[modified]},
	}
	return info
}

func runVersion(t *testing.T, args ...string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if code := Run(args, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("%v code=%d stderr=%s", args, code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("%v wrote to stderr: %q", args, stderr.String())
	}
	return stdout.String()
}

func versionData(t *testing.T, args ...string) map[string]any {
	t.Helper()
	var envelope struct {
		OK      bool           `json:"ok"`
		Command string         `json:"command"`
		Data    map[string]any `json:"data"`
	}
	output := runVersion(t, args...)
	if err := json.Unmarshal([]byte(output), &envelope); err != nil {
		t.Fatalf("decode %v output %q: %v", args, output, err)
	}
	if !envelope.OK || envelope.Command != "version" {
		t.Fatalf("%v envelope=%+v", args, envelope)
	}
	return envelope.Data
}

func TestVersionPrintsTheVersionCommitAndDate(t *testing.T) {
	stubBuildInfo(t, releaseBuildInfo("v0.4.0", false))

	const want = "v0.4.0 (1fd6638, 2026-09-27)\n"
	if got := runVersion(t, "--version"); got != want {
		t.Fatalf("--version output=%q, want %q", got, want)
	}
	if got := runVersion(t, "version"); got != want {
		t.Fatalf("version output=%q, want %q", got, want)
	}
}

func TestVersionJSONCarriesTheBuildInformation(t *testing.T) {
	stubBuildInfo(t, releaseBuildInfo("v0.4.0", false))

	want := map[string]any{
		"version":     "v0.4.0",
		"commit":      testCommit,
		"commit_time": "2026-09-27T10:16:51Z",
		"modified":    false,
		"go":          runtime.Version(),
		"platform":    runtime.GOOS + "/" + runtime.GOARCH,
	}
	for _, args := range [][]string{{"--json", "--version"}, {"--json", "version"}} {
		got := versionData(t, args...)
		if len(got) != len(want) {
			t.Fatalf("%v data=%v, want exactly %v", args, got, want)
		}
		for key, value := range want {
			if got[key] != value {
				t.Fatalf("%v data[%q]=%v, want %v", args, key, got[key], value)
			}
		}
	}
}

func TestVersionOfAModifiedTreeReportsIt(t *testing.T) {
	stubBuildInfo(t, releaseBuildInfo("v0.4.1-0.20260928101010-1fd6638295fb+dirty", true))

	if got, want := runVersion(t, "--version"), "v0.4.1-0.20260928101010-1fd6638295fb+dirty (1fd6638, 2026-09-27)\n"; got != want {
		t.Fatalf("--version output=%q, want %q", got, want)
	}
	if got := versionData(t, "--json", "version")["modified"]; got != true {
		t.Fatalf("modified=%v, want true", got)
	}
}

func TestVersionWithoutVCSInformationHasNoCommit(t *testing.T) {
	stubBuildInfo(t, &debug.BuildInfo{Main: debug.Module{Version: "v0.4.0"}})

	if got, want := runVersion(t, "--version"), "v0.4.0\n"; got != want {
		t.Fatalf("--version output=%q, want %q", got, want)
	}
	data := versionData(t, "--json", "version")
	for _, key := range []string{"commit", "commit_time", "modified"} {
		if value, ok := data[key]; ok {
			t.Fatalf("data[%q]=%v, want no commit fields without VCS information", key, value)
		}
	}
}

func TestVersionOfADevelopmentBuildIsDev(t *testing.T) {
	for name, info := range map[string]*debug.BuildInfo{
		"no build information": nil,
		"devel module version": {Main: debug.Module{Version: "(devel)"}},
		"empty module version": {},
	} {
		t.Run(name, func(t *testing.T) {
			stubBuildInfo(t, info)
			if got := runVersion(t, "--version"); got != "dev\n" {
				t.Fatalf("--version output=%q, want %q", got, "dev\n")
			}
		})
	}
}

func TestVersionOfThisTestBinaryIsUsable(t *testing.T) {
	build := currentBuild()
	if build.Version == "" || build.Version == "(devel)" {
		t.Fatalf("version=%q, want a usable value", build.Version)
	}
	if build.Go != runtime.Version() {
		t.Fatalf("go=%q, want %q", build.Go, runtime.Version())
	}
}
