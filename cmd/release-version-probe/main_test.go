package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRunBoundedScrubsCredentialEnvironment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is Unix-only")
	}
	t.Setenv("GH_TOKEN", "must-not-reach-probed-binary")
	t.Setenv("OPENAI_API_KEY", "must-not-reach-probed-binary")
	script := writeExecutable(t, `#!/bin/sh
if [ -n "${GH_TOKEN+x}" ] || [ -n "${OPENAI_API_KEY+x}" ]; then
  exit 91
fi
case "$*" in
  --version|version) printf 'v1.2.3\n' ;;
  'version --json') printf '%s\n' '{"ok":true,"command":"version","timestamp":"2026-07-16T00:00:00Z","data":{"version":"v1.2.3"},"warnings":[],"error":null}' ;;
  *) exit 92 ;;
esac
`)
	flagResult, err := runBounded(script, "flag", []string{"--version"})
	if err != nil {
		t.Fatal(err)
	}
	if flagResult.Stdout != "v1.2.3\n" || flagResult.Stderr != "" || flagResult.ExitCode != 0 {
		t.Fatalf("result=%+v", flagResult)
	}
	jsonResult, err := runBounded(script, "json", []string{"version", "--json"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(jsonResult.Stdout, `"version":"v1.2.3"`) {
		t.Fatalf("JSON result=%+v", jsonResult)
	}
}

func TestRunBoundedRejectsOversizedOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is Unix-only")
	}
	script := writeExecutable(t, "#!/bin/sh\nyes x | head -c 70000\n")
	if _, err := runBounded(script, "flag", []string{"--version"}); err == nil || !strings.Contains(err.Error(), "64 KiB") {
		t.Fatalf("oversized output error=%v", err)
	}
}

func TestDigestBinaryRejectsSymlink(t *testing.T) {
	target := writeExecutable(t, "#!/bin/sh\nexit 0\n")
	link := filepath.Join(t.TempDir(), "binary")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := digestBinary(link); err == nil {
		t.Fatal("symlink binary was accepted")
	}
}

func TestRunBoundedStopsWaitingForDescendantOutputHandles(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	oldDelay := processWaitDelay
	processWaitDelay = 100 * time.Millisecond
	t.Cleanup(func() { processWaitDelay = oldDelay })
	pidPath := filepath.Join(t.TempDir(), "descendant.pid")
	t.Cleanup(func() {
		data, readErr := os.ReadFile(pidPath)
		if readErr != nil {
			return
		}
		pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
		if parseErr != nil {
			return
		}
		if process, findErr := os.FindProcess(pid); findErr == nil {
			_ = process.Kill()
		}
	})

	started := time.Now()
	_, err = runBounded(executable, "test-helper", []string{
		"-test.run=^TestProbeDescendantHelper$", "--", "spawn", pidPath,
	})
	elapsed := time.Since(started)
	if err == nil || (!errors.Is(err, exec.ErrWaitDelay) && !strings.Contains(err.Error(), "output handles")) {
		t.Fatalf("descendant output-handle error=%v", err)
	}
	if elapsed >= 5*time.Second {
		t.Fatalf("inherited output handles delayed return for %s", elapsed)
	}
}

func TestProbeDescendantHelper(t *testing.T) {
	args := argumentsAfterDoubleDash(os.Args)
	if len(args) == 0 {
		return
	}
	switch args[0] {
	case "spawn":
		if len(args) != 2 {
			t.Fatalf("spawn helper args=%q", args)
		}
		child := exec.Command(os.Args[0], "-test.run=^TestProbeDescendantHelper$", "--", "hold")
		child.Stdout = os.Stdout
		child.Stderr = os.Stderr
		if err := child.Start(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(args[1], []byte(strconv.Itoa(child.Process.Pid)), 0o600); err != nil {
			_ = child.Process.Kill()
			t.Fatal(err)
		}
	case "hold":
		if len(args) != 1 {
			t.Fatalf("hold helper args=%q", args)
		}
		time.Sleep(3 * time.Second)
	default:
		t.Fatalf("unknown helper mode %q", args[0])
	}
}

func argumentsAfterDoubleDash(args []string) []string {
	for index, arg := range args {
		if arg == "--" {
			return args[index+1:]
		}
	}
	return nil
}

func writeExecutable(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "probe-target")
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}
