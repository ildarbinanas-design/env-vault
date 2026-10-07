//go:build darwin || linux

package e2e_test

import (
	"fmt"
	"io"
	"os"
	"reflect"
	"time"

	"golang.org/x/sys/unix"
)

func testHiddenSecretInput(sc *scenario) {
	value := sc.newSizedSentinel(6050)
	line := []byte(value + "\n")
	defer clear(line)
	wantExit(sc.t, runHiddenSecret(sc, line), 0)
	assertHiddenSecretValue(sc, value)

	// The fixed bytes are terminal editing keys and Unicode syntax, not a
	// secret fixture. Every retained or erased word is registered at runtime.
	edited := []byte(sc.newSentinel() + "\x15" + value + "界\x7f\xff\x08\u2003" + sc.newSentinel() + "\u2002\x17\x7f\x04\r")
	defer clear(edited)
	wantExit(sc.t, runHiddenSecret(sc, edited), 0)
	assertHiddenSecretValue(sc, value)

	boundary := sc.newSizedSentinel(65536)
	line = []byte(boundary + "\n")
	defer clear(line)
	wantExit(sc.t, runHiddenSecret(sc, line), 0)
	assertHiddenSecretValue(sc, boundary)
	before := fileSHA256(sc.t, sc.store)
	// Editing after overflow must not make the rejected line acceptable.
	oversized := []byte(sc.newSizedSentinel(65537) + "\x15" + sc.newSentinel() + "\n")
	defer clear(oversized)
	wantErrorCode(sc, runHiddenSecret(sc, oversized), 2, "SECRET_TOO_LARGE")
	if fileSHA256(sc.t, sc.store) != before {
		sc.t.Fatal("overflowed hidden input changed the private test store")
	}
	assertHiddenSecretValue(sc, boundary)
	wantExit(sc.t, sc.run("--json", "secret", "delete", "hidden-input", "--confirm", "hidden-input"), 0)
}

func assertHiddenSecretValue(sc *scenario, value string) {
	result := sc.run("exec", "--secret", "hidden-input:TOKEN", "--", sc.suite.helper, "env",
		"--expect-hash", "TOKEN="+sha256Text(value), "--expect-length", fmt.Sprintf("TOKEN=%d", len(value)))
	wantExit(sc.t, result, 0)
	if result.Stdout != "env-ok\n" || result.Stderr != "" {
		sc.t.Fatal("hidden-input delivery length/digest check failed")
	}
}

func runHiddenSecret(sc *scenario, input []byte) commandResult {
	sc.t.Helper()
	controller, terminal := openPromptTerminal(sc)
	original, err := unix.IoctlGetTermios(int(terminal.Fd()), e2ePromptReadTermios)
	if err != nil || original.Lflag&unix.ECHO == 0 {
		sc.t.Fatal("unable to inspect the initial prompt terminal")
	}
	launched, err := launchEnvVault(sc, runOptions{terminal: terminal}, "--json", "secret", "set", "hidden-input", "--verify")
	if err != nil {
		sc.t.Fatal("unable to start the binary with terminal input")
	}
	deadline := time.NewTimer(defaultTimeout)
	defer deadline.Stop()
	poll := time.NewTicker(time.Millisecond)
	defer poll.Stop()
	for {
		state, err := unix.IoctlGetTermios(int(terminal.Fd()), e2ePromptReadTermios)
		if err != nil {
			sc.t.Fatal("unable to inspect the hidden prompt")
		}
		if state.Lflag&unix.ECHO == 0 {
			break
		}
		select {
		case <-launched.finished:
			_ = finishLaunched(sc, launched, defaultTimeout, true)
			sc.t.Fatal("hidden prompt exited before disabling echo")
		case <-deadline.C:
			sc.t.Fatal("hidden prompt did not disable echo")
		case <-poll.C:
		}
	}
	// Initialize a duplicate as pollable, so the large write and its cleanup
	// stay bounded even if a regression stops the child consuming input.
	fd, err := unix.Dup(int(controller.Fd()))
	if err != nil {
		sc.t.Fatal("unable to prepare the terminal writer")
	}
	unix.CloseOnExec(fd)
	if err := unix.SetNonblock(fd, true); err != nil {
		_ = unix.Close(fd)
		sc.t.Fatal("unable to make the terminal writer pollable")
	}
	writer := os.NewFile(uintptr(fd), "e2e-prompt-writer")
	defer writer.Close()
	if err := writer.SetWriteDeadline(time.Now().Add(defaultTimeout)); err != nil {
		sc.t.Fatal("unable to bound the terminal write")
	}
	written := make(chan error, 1)
	go func() {
		n, err := writer.Write(input)
		if err == nil && n != len(input) {
			err = io.ErrShortWrite
		}
		written <- err
	}()
	select {
	case err := <-written:
		if err != nil {
			sc.t.Fatal("hidden-input terminal write failed")
		}
	case <-time.After(defaultTimeout + time.Second):
		_ = writer.Close()
		select {
		case <-written:
		case <-time.After(time.Second):
			sc.t.Fatal("hidden-input writer cleanup timed out")
		}
		sc.t.Fatal("hidden-input terminal write timed out")
	}
	result := finishLaunched(sc, launched, defaultTimeout, true)
	after, err := unix.IoctlGetTermios(int(controller.Fd()), e2ePromptReadTermios)
	if err != nil || !reflect.DeepEqual(original, after) {
		sc.t.Fatal("hidden-input command did not restore the terminal")
	}
	var echoed [256]byte
	defer clear(echoed[:])
	controllerFD := int(controller.Fd())
	if err := unix.SetNonblock(controllerFD, true); err != nil {
		sc.t.Fatal("unable to inspect terminal output without blocking")
	}
	n, readErr := unix.Read(controllerFD, echoed[:])
	if n > 0 || (readErr != nil && readErr != unix.EAGAIN) {
		sc.t.Fatal("hidden-input terminal unexpectedly produced output")
	}
	return result
}
