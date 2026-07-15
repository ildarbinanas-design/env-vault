package e2e_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

type launchedCommand struct {
	cmd      *exec.Cmd
	args     []string
	stdout   bytes.Buffer
	stderr   bytes.Buffer
	mu       sync.Mutex
	waitErr  error
	finished chan struct{}
}

func launchEnvVault(sc *scenario, options runOptions, args ...string) (*launchedCommand, error) {
	if options.cwd == "" {
		options.cwd = sc.root
	}
	cmd := exec.Command(sc.suite.binary, args...)
	configureProcess(cmd)
	cmd.Dir = options.cwd
	cmd.Env = sc.baseEnv(options.env, options.unset)
	cmd.Stdin = bytes.NewReader(options.stdin)
	launched := &launchedCommand{cmd: cmd, args: append([]string(nil), args...), finished: make(chan struct{})}
	cmd.Stdout = &launched.stdout
	cmd.Stderr = &launched.stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go func() {
		err := cmd.Wait()
		launched.mu.Lock()
		launched.waitErr = err
		launched.mu.Unlock()
		close(launched.finished)
	}()
	sc.t.Cleanup(func() { _ = stopLaunched(sc, launched) })
	return launched, nil
}

func finishLaunched(sc *scenario, launched *launchedCommand, timeout time.Duration, record bool) commandResult {
	sc.t.Helper()
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	timedOut := false
	select {
	case <-launched.finished:
	case <-timer.C:
		timedOut = true
		_ = terminateProcessTree(launched.cmd)
		cleanupTimer := time.NewTimer(5 * time.Second)
		defer cleanupTimer.Stop()
		select {
		case <-launched.finished:
		case <-cleanupTimer.C:
			sc.t.Fatalf("asynchronous process tree did not exit within 5s after timeout termination")
		}
	}
	launched.mu.Lock()
	waitErr := launched.waitErr
	launched.mu.Unlock()
	result := commandResult{
		ExitCode: exitCode(launched.cmd, waitErr),
		Stdout:   launched.stdout.String(),
		Stderr:   launched.stderr.String(),
		TimedOut: timedOut,
	}
	sc.assertNoSentinel("asynchronous stdout", []byte(result.Stdout))
	sc.assertNoSentinel("asynchronous stderr", []byte(result.Stderr))
	if record {
		sc.recordContract(launched.args, result)
	}
	return result
}

func stopLaunched(sc *scenario, launched *launchedCommand) commandResult {
	sc.t.Helper()
	select {
	case <-launched.finished:
		return finishLaunched(sc, launched, 5*time.Second, false)
	default:
	}
	_ = terminateProcessTree(launched.cmd)
	return finishLaunched(sc, launched, 5*time.Second, false)
}

func waitForFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		info, err := os.Stat(path)
		if err == nil && info.Mode().IsRegular() {
			return
		}
		if err != nil && !os.IsNotExist(err) {
			t.Fatalf("poll readiness marker: %v", err)
		}
		select {
		case <-deadline.C:
			t.Fatalf("timed out waiting for readiness marker %s", filepath.Base(path))
		case <-ticker.C:
		}
	}
}

func testConcurrencyProfileMutations(sc *scenario) {
	config := filepath.Join(sc.root, "concurrent.yaml")
	wantExit(sc.t, sc.run("--json", "--config", config, "profile", "create", "dev"), 0)

	const workers = 16
	launched := make([]*launchedCommand, 0, workers)
	for worker := 0; worker < workers; worker++ {
		mapping := fmt.Sprintf("secret-%02d:TOKEN_%02d", worker, worker)
		command, err := launchEnvVault(sc, runOptions{}, "--json", "--config", config, "profile", "add", "dev", mapping)
		if err != nil {
			for _, prior := range launched {
				_ = stopLaunched(sc, prior)
			}
			sc.t.Fatalf("start concurrent profile mutation %d: %v", worker, err)
		}
		launched = append(launched, command)
	}
	for worker, command := range launched {
		result := finishLaunched(sc, command, 10*time.Second, true)
		if result.ExitCode != 0 || result.TimedOut {
			sc.t.Fatalf("concurrent mutation %d failed: exit=%d timeout=%t stdout=%q stderr=%q", worker, result.ExitCode, result.TimedOut, result.Stdout, result.Stderr)
		}
	}

	show := sc.run("--json", "--config", config, "profile", "show", "dev")
	wantExit(sc.t, show, 0)
	envs := decodeProfileEnvs(sc.t, show)
	want := make([]string, workers)
	for worker := range want {
		want[worker] = fmt.Sprintf("TOKEN_%02d", worker)
	}
	sort.Strings(want)
	if strings.Join(envs, "\n") != strings.Join(want, "\n") {
		sc.t.Fatalf("lost concurrent mappings: got=%v want=%v", envs, want)
	}
	data := sc.scanFile(config, "concurrent config")
	if len(data) == 0 { // profile show above is the public validity check.
		sc.t.Fatal("concurrent config is empty")
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(config), ".concurrent.yaml.tmp-*"))
	if err != nil || len(matches) != 0 {
		sc.t.Fatalf("atomic temp files remain after concurrency: %v err=%v", matches, err)
	}
}

func testLockTimeoutCrashIntegrity(sc *scenario) {
	config := filepath.Join(sc.root, "locked.yaml")
	wantExit(sc.t, sc.run("--json", "--config", config, "profile", "create", "dev"), 0)
	holder := startLockHolder(sc, config+".lock")

	lockStarted := time.Now()
	locked := sc.runWith(runOptions{timeout: 8 * time.Second}, "--json", "--config", config, "profile", "add", "dev", "locked:TOKEN_LOCKED")
	lockDuration := time.Since(lockStarted)
	wantExit(sc.t, locked, 5)
	if got := parseEnvelope(sc.t, locked); got.Error == nil || got.Error.Code != "CONFIG_LOCKED" {
		sc.t.Fatalf("expected CONFIG_LOCKED, got %#v", got)
	}
	if lockDuration < 4*time.Second || lockDuration >= 8*time.Second {
		sc.t.Fatalf("lock timeout duration=%s, want bounded wait in [4s,8s)", lockDuration)
	}

	// Kill a second waiting mutation. It must not partially replace the config.
	waiter, err := launchEnvVault(sc, runOptions{}, "--json", "--config", config, "profile", "add", "dev", "crashed:TOKEN_CRASHED")
	if err != nil {
		sc.t.Fatalf("start killed lock waiter: %v", err)
	}
	_ = stopLaunched(sc, waiter)
	_ = stopLaunched(sc, holder)

	show := sc.run("--json", "--config", config, "profile", "show", "dev")
	wantExit(sc.t, show, 0)
	if envs := decodeProfileEnvs(sc.t, show); len(envs) != 0 {
		sc.t.Fatalf("killed waiter changed config: %v", envs)
	}

	// Kill an active env-vault writer after its new YAML has been synced and
	// closed but before atomic rename. The hook is inaccessible without the
	// already-required test-backend triple gate and two temp-contained paths.
	if err := os.MkdirAll(sc.storeRoot, 0o700); err != nil {
		sc.t.Fatalf("create gated hook store directory: %v", err)
	}
	ready := filepath.Join(sc.tmp, "active-writer-ready")
	continuePath := filepath.Join(sc.tmp, "active-writer-continue")
	active, err := launchEnvVault(sc, runOptions{env: map[string]string{
		"ENV_VAULT_E2E_CONFIG_SAVE_READY":    ready,
		"ENV_VAULT_E2E_CONFIG_SAVE_CONTINUE": continuePath,
	}}, "--json", "--config", config, "profile", "add", "dev", "crashed-active:TOKEN_CRASHED_ACTIVE")
	if err != nil {
		sc.t.Fatalf("start active config writer: %v", err)
	}
	waitForFile(sc.t, ready, 5*time.Second)
	_ = terminateProcessTree(active.cmd)
	activeResult := finishLaunched(sc, active, 5*time.Second, true)
	if activeResult.ExitCode == 0 || activeResult.TimedOut {
		sc.t.Fatalf("active writer was not terminated at the crash boundary: exit=%d timeout=%t", activeResult.ExitCode, activeResult.TimedOut)
	}
	afterCrash := sc.run("--json", "--config", config, "profile", "show", "dev")
	wantExit(sc.t, afterCrash, 0)
	if envs := decodeProfileEnvs(sc.t, afterCrash); len(envs) != 0 {
		sc.t.Fatalf("active writer crash changed the previously committed config: %v", envs)
	}

	// OS lock release after the holder crash must allow a normal mutation.
	wantExit(sc.t, sc.run("--json", "--config", config, "profile", "add", "dev", "recovered:TOKEN_RECOVERED"), 0)
	wantExit(sc.t, sc.run("--json", "--config", config, "profile", "show", "dev"), 0)
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(config + ".lock"); err != nil || info.Mode().Perm() != 0o600 {
			sc.t.Fatalf("lock permissions after recovery: mode=%v err=%v", infoMode(info), err)
		}
	}
}

func startLockHolder(sc *scenario, lockPath string) *launchedCommand {
	sc.t.Helper()
	ready := filepath.Join(sc.root, fmt.Sprintf("lock-ready-%d", len(sc.observations)))
	cmd := exec.Command(sc.suite.helper, "hold-lock", "--path", lockPath, "--ready", ready)
	configureProcess(cmd)
	cmd.Dir = sc.root
	cmd.Env = sc.baseEnv(nil, nil)
	launched := &launchedCommand{cmd: cmd, args: []string{"<testhelper>", "hold-lock"}, finished: make(chan struct{})}
	cmd.Stdout = &launched.stdout
	cmd.Stderr = &launched.stderr
	if err := cmd.Start(); err != nil {
		sc.t.Fatalf("start lock holder: %v", err)
	}
	go func() {
		err := cmd.Wait()
		launched.mu.Lock()
		launched.waitErr = err
		launched.mu.Unlock()
		close(launched.finished)
	}()
	sc.t.Cleanup(func() { _ = stopLaunched(sc, launched) })
	waitForFile(sc.t, ready, 5*time.Second)
	return launched
}
