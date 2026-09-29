package cli

import (
	"runtime"
	"runtime/debug"
)

// readBuildInfo is replaced in tests.
var readBuildInfo = debug.ReadBuildInfo

// buildVersion describes the running binary. Every value comes from the build
// information the Go toolchain embeds (ADR 0011): the main module version, which
// Go stamps from the tag when the checkout has one, and the VCS stamps. It helps
// diagnosis but proves nothing, because a tampered binary can print anything;
// the release attestation is the proof (ADR 0012).
type buildVersion struct {
	Version    string
	Commit     string
	CommitTime string
	Modified   bool
	Go         string
	Platform   string
}

func currentBuild() buildVersion {
	build := buildVersion{
		Version:  "dev",
		Go:       runtime.Version(),
		Platform: runtime.GOOS + "/" + runtime.GOARCH,
	}
	info, ok := readBuildInfo()
	if !ok {
		return build
	}
	if version := info.Main.Version; version != "" && version != "(devel)" {
		build.Version = version
	}
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			build.Commit = setting.Value
		case "vcs.time":
			build.CommitTime = setting.Value
		case "vcs.modified":
			build.Modified = setting.Value == "true"
		}
	}
	return build
}

// data is the version command's machine-readable result. A build without VCS
// information, such as one from the module proxy, has no commit fields.
func (b buildVersion) data() map[string]any {
	data := map[string]any{
		"version":  b.Version,
		"go":       b.Go,
		"platform": b.Platform,
	}
	if b.Commit != "" {
		data["commit"] = b.Commit
		data["commit_time"] = b.CommitTime
		data["modified"] = b.Modified
	}
	return data
}
