package main

import (
	buildinfo "debug/buildinfo"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"time"
)

func resolveGotestsum(repoRoot, explicitPath, checksumPath, goVersion string, timeout time.Duration) (gotestsumCommand, commandResult, error) {
	candidates := []string{}
	if explicitPath != "" {
		candidates = append(candidates, explicitPath)
	} else {
		if path, err := exec.LookPath("gotestsum"); err == nil {
			candidates = append(candidates, path)
		}
		if output, result := commandOutput("go", []string{"env", "GOPATH"}, repoRoot, environment(nil), timeout); result.ExitCode == 0 {
			for _, workspace := range filepath.SplitList(strings.TrimSpace(string(output))) {
				candidate := filepath.Join(workspace, "bin", reporterExecutableName())
				if _, err := os.Lstat(candidate); err == nil && !containsString(candidates, candidate) {
					candidates = append(candidates, candidate)
				}
			}
		}
	}
	if len(candidates) == 0 {
		err := errors.New("exact gotestsum reporter is not installed; network fallback is disabled")
		return gotestsumCommand{}, failedReporterProbe("gotestsum-resolution", err), err
	}

	var rejected []string
	for _, candidate := range candidates {
		command, probe, err := verifyGotestsumCandidate(candidate, checksumPath, goVersion, timeout)
		if err == nil {
			return command, probe, nil
		}
		rejected = append(rejected, err.Error())
		if explicitPath != "" {
			return gotestsumCommand{}, probe, err
		}
	}
	err := fmt.Errorf("no exact gotestsum reporter passed verification: %s", strings.Join(rejected, "; "))
	return gotestsumCommand{}, failedReporterProbe("gotestsum-resolution", err), err
}

func verifyGotestsumCandidate(filename, checksumPath, goVersion string, timeout time.Duration) (gotestsumCommand, commandResult, error) {
	binary, err := requireRegularBinary(filename)
	if err != nil {
		return gotestsumCommand{}, failedReporterProbe("gotestsum-file", err), err
	}
	if checksumPath != "" {
		digest, digestErr := sha256File(binary)
		if digestErr != nil {
			return gotestsumCommand{}, failedReporterProbe("gotestsum-checksum", digestErr), digestErr
		}
		if checksumErr := verifyChecksumSidecar(checksumPath, filepath.Base(binary), digest); checksumErr != nil {
			return gotestsumCommand{}, failedReporterProbe("gotestsum-checksum", checksumErr), checksumErr
		}
	}
	info, err := buildinfo.ReadFile(binary)
	if err != nil {
		err = fmt.Errorf("read gotestsum build information: %w", err)
		return gotestsumCommand{}, failedReporterProbe("gotestsum-build-info", err), err
	}
	if err := validateGotestsumBuildInfo(info, goVersion, expectedGOOS(), expectedGOARCH()); err != nil {
		return gotestsumCommand{}, failedReporterProbe("gotestsum-build-info", err), err
	}

	output, probe := commandOutput(binary, []string{"--version"}, filepath.Dir(binary), environment(nil), timeout)
	want := "gotestsum version " + gotestsumVersion + "\n"
	if probe.ExitCode != 0 || string(output) != want {
		err := fmt.Errorf("gotestsum --version must equal %q", strings.TrimSuffix(want, "\n"))
		if probe.ExitCode == 0 {
			probe.ExitCode = 1
		}
		probe.Error = err.Error()
		return gotestsumCommand{}, probe, err
	}
	return gotestsumCommand{name: binary}, probe, nil
}

func validateGotestsumBuildInfo(info *debug.BuildInfo, goVersion, goos, goarch string) error {
	if info == nil {
		return errors.New("gotestsum build information is absent")
	}
	if info.GoVersion != goVersion {
		return fmt.Errorf("gotestsum Go version %q does not match runner %q", info.GoVersion, goVersion)
	}
	const modulePath = "gotest.tools/gotestsum"
	if info.Path != modulePath || info.Main.Path != modulePath || info.Main.Version != gotestsumVersion || info.Main.Sum != gotestsumModuleSum {
		return errors.New("gotestsum main module path, version, or checksum is not exact")
	}
	if info.Main.Replace != nil {
		return errors.New("gotestsum main module contains replacement metadata")
	}
	for _, dependency := range info.Deps {
		if dependency != nil && dependency.Replace != nil {
			return fmt.Errorf("gotestsum dependency %s contains replacement metadata", dependency.Path)
		}
	}
	settings := make(map[string]string, len(info.Settings))
	for _, setting := range info.Settings {
		if _, duplicate := settings[setting.Key]; duplicate {
			return fmt.Errorf("gotestsum build setting %q is duplicated", setting.Key)
		}
		settings[setting.Key] = setting.Value
	}
	for key, want := range map[string]string{
		"CGO_ENABLED": "0",
		"GOOS":        goos,
		"GOARCH":      goarch,
	} {
		if settings[key] != want {
			return fmt.Errorf("gotestsum build setting %s=%q, want %q", key, settings[key], want)
		}
	}
	return nil
}

func failedReporterProbe(name string, err error) commandResult {
	now := time.Now().UTC()
	return commandResult{Name: name, StartedAt: now, EndedAt: now, ExitCode: 1, Error: err.Error()}
}

func reporterExecutableName() string {
	if runtime.GOOS == "windows" {
		return "gotestsum.exe"
	}
	return "gotestsum"
}
