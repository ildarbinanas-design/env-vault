//go:build darwin || linux

package cli

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/ildarbinanas-design/env-vault/internal/bundle"
	apperrors "github.com/ildarbinanas-design/env-vault/internal/errors"
	"github.com/ildarbinanas-design/env-vault/internal/secretstore"
	"github.com/ildarbinanas-design/env-vault/internal/secretstore/teststore"
	"github.com/ildarbinanas-design/env-vault/internal/testutil"
)

// This subprocess reads generated test input and emits only its length/digest.
// The overflow command is gated to a disposable test store and must fail before
// constructing it; other cases call only the input functions.
func TestHiddenPromptHelper(t *testing.T) {
	kind := os.Getenv("ENV_VAULT_PROMPT_TEST_HELPER")
	if kind == "" {
		return
	}
	if strings.HasSuffix(kind, "-ignored") {
		signal.Ignore(syscall.SIGINT)
		kind = strings.TrimSuffix(kind, "-ignored")
	}
	if kind != "secret-overflow-next" {
		// Exercise the real terminal passphrase route regardless of the test
		// runner's environment. These cases never invoke a storage command.
		for _, name := range []string{teststore.BackendEnv, teststore.AllowEnv, teststore.StoreEnv} {
			if err := os.Unsetenv(name); err != nil {
				os.Exit(1)
			}
		}
	}
	app := newApp(os.Stdin, io.Discard, io.Discard)
	var value []byte
	var err error
	if kind == "secret" {
		value, err = app.readSecret(false)
	} else if kind == "secret-empty-eof" {
		value, err = readHiddenSecret(int(os.Stdin.Fd()))
		if !errors.Is(err, io.EOF) || value != nil {
			bundle.Wipe(value)
			os.Exit(1)
		}
		err = nil
	} else if kind == "secret-overflow-next" {
		app.wrapStore = func(store secretstore.Store) secretstore.Store {
			os.Exit(3)
			return store
		}
		root := app.rootCommand()
		root.SetArgs([]string{"secret", "set", "oversized-prompt"})
		appErr, ok := apperrors.From(root.Execute())
		if !ok || appErr.Code != apperrors.CodeSecretTooLarge || appErr.ExitCode != 2 {
			os.Exit(1)
		}
		value, err = app.readSecret(false)
	} else if kind == "confirmation" {
		value, err = app.readPassphrase("export", true)
	} else if kind == "confirmation-invalid" {
		value, err = app.readPassphrase("export", true)
		appErr, ok := apperrors.From(err)
		if !ok || appErr.Code != apperrors.CodePassphraseInvalid || value != nil {
			bundle.Wipe(value)
			os.Exit(1)
		}
		err = nil
	} else {
		value, err = app.terminalPassphrase("import")("Passphrase: ")
	}
	if err != nil {
		bundle.Wipe(value)
		os.Exit(1)
	}
	// Only this non-secret summary crosses the subprocess boundary.
	fmt.Fprintf(os.Stdout, "%d %x\n", len(value), sha256.Sum256(value))
	bundle.Wipe(value)
	os.Exit(0)
}

func TestHiddenPromptPreservesLongInput(t *testing.T) {
	for _, length := range []int{1, 1023, 1024, 1025, 4095, 4096, 6050, 65536} {
		for _, mode := range []string{"single-write", "chunked"} {
			t.Run(fmt.Sprintf("length-%d/%s", length, mode), func(t *testing.T) {
				t.Parallel()
				value := ephemeralPromptInput(t, length)
				t.Cleanup(func() { bundle.Wipe(value) })
				input := append(append([]byte(nil), value...), '\n')
				t.Cleanup(func() { bundle.Wipe(input) })
				chunkSize := len(input)
				if mode == "chunked" {
					chunkSize = 251
				}
				runHiddenPromptInput(t, "secret", input, value, chunkSize)
			})
		}
	}
}

// ephemeralPromptInput generates printable disposable bytes so terminal
// control characters cannot alter the intended length. Values stay in memory.
func ephemeralPromptInput(t *testing.T, length int) []byte {
	t.Helper()
	value := make([]byte, length)
	for offset := 0; offset < len(value); {
		offset += copy(value[offset:], testutil.EphemeralValue(t))
	}
	return value
}

func runHiddenPromptInput(t *testing.T, kind string, input, want []byte, chunkSize int) {
	t.Helper()
	controller, terminal := openPseudoTerminal(t)
	original, err := unix.IoctlGetTermios(int(terminal.Fd()), promptReadTermios)
	if err != nil {
		t.Fatal("unable to inspect terminal state")
	}
	if original.Lflag&unix.ECHO == 0 {
		t.Fatal("test terminal started with echo disabled")
	}
	writer := promptTestWriter(t, controller)
	var output bytes.Buffer
	child := exec.Command(os.Args[0], "-test.run=^TestHiddenPromptHelper$")
	child.Env = append(os.Environ(), "ENV_VAULT_PROMPT_TEST_HELPER="+kind)
	if kind == "secret-overflow-next" {
		child.Env = append(child.Env,
			teststore.BackendEnv+"=test", teststore.AllowEnv+"=1",
			teststore.StoreEnv+"="+filepath.Join(t.TempDir(), "store"))
	}
	child.Stdin = terminal
	child.Stdout, child.Stderr = &output, io.Discard
	if err := child.Start(); err != nil {
		t.Fatal("unable to start hidden prompt")
	}
	finished := make(chan error, 1)
	go func() { finished <- child.Wait() }()
	processDone := false
	t.Cleanup(func() {
		if !processDone {
			_ = child.Process.Kill()
			select {
			case <-finished:
			case <-time.After(5 * time.Second):
				t.Error("prompt process cleanup timed out")
			}
		}
	})
	deadline := time.Now().Add(5 * time.Second)
	for {
		state, err := unix.IoctlGetTermios(int(terminal.Fd()), promptReadTermios)
		if err != nil {
			t.Fatal("unable to inspect hidden prompt state")
		}
		if state.Lflag&unix.ECHO == 0 {
			break
		}
		select {
		case <-finished:
			processDone = true
			t.Fatal("prompt exited before hiding input")
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("prompt did not hide input")
		}
		time.Sleep(time.Millisecond)
	}
	if err := writer.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal("unable to bound terminal write")
	}
	written := make(chan error, 1)
	go func() {
		for remaining := input; len(remaining) > 0; {
			n := min(chunkSize, len(remaining))
			count, err := writer.Write(remaining[:n])
			if err != nil {
				written <- err
				return
			}
			if count != n {
				written <- io.ErrShortWrite
				return
			}
			remaining = remaining[n:]
		}
		written <- nil
	}()
	writerDone := false
	t.Cleanup(func() {
		if !writerDone {
			_ = writer.Close()
			select {
			case <-written:
			case <-time.After(5 * time.Second):
				t.Error("prompt writer cleanup timed out")
			}
		}
	})
	select {
	case err := <-written:
		writerDone = true
		if err != nil {
			t.Fatal("hidden prompt write timed out or failed")
		}
	case <-time.After(6 * time.Second):
		t.Fatal("hidden prompt write timed out")
	}
	select {
	case err := <-finished:
		processDone = true
		if err != nil {
			t.Fatal("completed prompt failed")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("hidden prompt read timed out")
	}
	expected := fmt.Sprintf("%d %x\n", len(want), sha256.Sum256(want))
	if output.String() != expected {
		t.Fatal("length/digest mismatch")
	}
	after, err := unix.IoctlGetTermios(int(controller.Fd()), promptReadTermios)
	if err != nil || !reflect.DeepEqual(original, after) {
		t.Fatal("prompt did not restore terminal state")
	}
}

func promptTestWriter(t *testing.T, controller *os.File) *os.File {
	t.Helper()
	// A separately owned, nonblocking descriptor makes os.File.Write pollable.
	// Its deadline bounds a write even when the reader stops consuming input.
	fd, err := unix.Dup(int(controller.Fd()))
	if err != nil {
		t.Fatal("unable to duplicate terminal writer")
	}
	if err := unix.SetNonblock(fd, true); err != nil {
		_ = unix.Close(fd)
		t.Fatal("unable to prepare terminal writer")
	}
	unix.CloseOnExec(fd)
	writer := os.NewFile(uintptr(fd), "prompt-test-writer")
	t.Cleanup(func() { _ = writer.Close() })
	return writer
}

func writePartialHiddenInput(t *testing.T, controller *os.File, input []byte) {
	t.Helper()
	writer := promptTestWriter(t, controller)
	if err := writer.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal("unable to bound terminal write")
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
			t.Fatal("unable to type partial input")
		}
	case <-time.After(6 * time.Second):
		_ = writer.Close()
		select {
		case <-written:
		case <-time.After(time.Second):
			t.Error("partial-input writer cleanup timed out")
		}
		t.Fatal("partial-input write timed out")
	}
}

func TestHiddenPromptsRestoreTerminalOnSignals(t *testing.T) {
	for _, kind := range []string{"secret", "passphrase", "confirmation"} {
		for _, termination := range []string{"ctrl-c", "long-ctrl-c", "sigterm", "ignored-interrupt", "completed"} {
			t.Run(kind+"/"+termination, func(t *testing.T) {
				controller, terminal := openPseudoTerminal(t)
				original, err := unix.IoctlGetTermios(int(terminal.Fd()), promptReadTermios)
				if err != nil {
					t.Fatal(err)
				}
				if original.Lflag&unix.ECHO == 0 {
					t.Fatal("test terminal started with echo disabled")
				}
				helperKind := kind
				if termination == "ignored-interrupt" {
					helperKind += "-ignored"
				}
				child := exec.Command(os.Args[0], "-test.run=^TestHiddenPromptHelper$")
				child.Env = append(os.Environ(), "ENV_VAULT_PROMPT_TEST_HELPER="+helperKind)
				child.Stdin = terminal
				child.Stdout, child.Stderr = io.Discard, io.Discard
				// Give the child its own terminal session and foreground group,
				// so typing Ctrl+C exercises the kernel's terminal signal path.
				child.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
				if err := child.Start(); err != nil {
					t.Fatal(err)
				}
				finished := make(chan error, 1)
				go func() { finished <- child.Wait() }()
				t.Cleanup(func() { _ = child.Process.Kill() })
				deadline := time.Now().Add(5 * time.Second)
				for {
					state, err := unix.IoctlGetTermios(int(terminal.Fd()), promptReadTermios)
					if err != nil {
						t.Fatal(err)
					}
					if state.Lflag&unix.ECHO == 0 {
						break
					}
					select {
					case <-finished:
						t.Fatal("prompt exited before hiding input")
					default:
					}
					if time.Now().After(deadline) {
						t.Fatal("prompt did not hide input")
					}
					time.Sleep(time.Millisecond)
				}
				wantSignal := syscall.SIGTERM
				switch termination {
				case "ctrl-c", "long-ctrl-c":
					wantSignal = syscall.SIGINT
					if termination == "long-ctrl-c" {
						partial := ephemeralPromptInput(t, 6050)
						t.Cleanup(func() { bundle.Wipe(partial) })
						writePartialHiddenInput(t, controller, partial)
					}
					if _, err := controller.Write([]byte{3}); err != nil {
						t.Fatal(err)
					}
				case "completed":
					// Generated only after echo is disabled; never logged or
					// passed to a backend, argv, environment variable or file.
					input := testutil.EphemeralValue(t) + "\n"
					if kind == "confirmation" {
						input += input
					}
					if _, err := controller.Write([]byte(input)); err != nil {
						t.Fatal("unable to complete hidden prompt")
					}
				default:
					if termination == "ignored-interrupt" {
						if err := child.Process.Signal(syscall.SIGINT); err != nil {
							t.Fatal(err)
						}
						select {
						case <-finished:
							t.Fatal("inherited ignored interrupt terminated prompt")
						case <-time.After(30 * time.Millisecond):
						}
					}
					if err := child.Process.Signal(syscall.SIGTERM); err != nil {
						t.Fatal(err)
					}
				}
				select {
				case err := <-finished:
					if termination == "completed" {
						if err != nil {
							t.Fatal("completed prompt failed")
						}
					} else {
						status, ok := child.ProcessState.Sys().(syscall.WaitStatus)
						if !ok || !status.Signaled() || status.Signal() != wantSignal {
							t.Fatalf("prompt exit did not preserve signal %v", wantSignal)
						}
					}
				case <-time.After(5 * time.Second):
					t.Fatal("prompt did not exit")
				}
				after, err := unix.IoctlGetTermios(int(controller.Fd()), promptReadTermios)
				if err != nil || !reflect.DeepEqual(original, after) {
					t.Fatalf("prompt did not restore terminal state: err=%v original=%#v after=%#v", err, original, after)
				}
			})
		}
	}
}

func TestHiddenPromptDiscardsInterruptedInput(t *testing.T) {
	for _, kind := range []string{"secret", "passphrase", "secret-long", "passphrase-long"} {
		t.Run(kind, func(t *testing.T) {
			controller, terminal := openPseudoTerminal(t)
			child := exec.Command(os.Args[0], "-test.run=^TestHiddenPromptHelper$")
			child.Env = append(os.Environ(), "ENV_VAULT_PROMPT_TEST_HELPER="+strings.TrimSuffix(kind, "-long"))
			child.Stdin = terminal
			child.Stdout, child.Stderr = io.Discard, io.Discard
			// Leave the terminal session alive after this process exits, as an
			// interactive shell would, so it can reveal unflushed input.
			if err := child.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = child.Process.Kill() })
			deadline := time.Now().Add(5 * time.Second)
			for {
				state, err := unix.IoctlGetTermios(int(terminal.Fd()), promptReadTermios)
				if err != nil {
					t.Fatal(err)
				}
				if state.Lflag&unix.ECHO == 0 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("prompt did not hide input")
				}
				time.Sleep(time.Millisecond)
			}
			length := 43
			termination := syscall.SIGTERM
			if strings.HasSuffix(kind, "-long") {
				length = 6050
				termination = syscall.SIGINT
			}
			partial := ephemeralPromptInput(t, length)
			t.Cleanup(func() { bundle.Wipe(partial) })
			writePartialHiddenInput(t, controller, partial)
			if err := child.Process.Signal(termination); err != nil {
				t.Fatal(err)
			}
			finished := make(chan error, 1)
			go func() { finished <- child.Wait() }()
			select {
			case <-finished:
			case <-time.After(5 * time.Second):
				t.Fatal("prompt did not exit")
			}
			// Complete whatever is still queued for the next terminal reader.
			if _, err := controller.Write([]byte{'\n'}); err != nil {
				t.Fatal(err)
			}
			if err := unix.SetNonblock(int(terminal.Fd()), true); err != nil {
				t.Fatal(err)
			}
			var received [256]byte
			defer bundle.Wipe(received[:])
			deadline = time.Now().Add(time.Second)
			for {
				n, err := unix.Read(int(terminal.Fd()), received[:])
				if err == unix.EAGAIN && time.Now().Before(deadline) {
					time.Sleep(time.Millisecond)
					continue
				}
				if err != nil {
					t.Fatal("next terminal read failed")
				}
				if n != 1 || received[0] != '\n' {
					t.Fatal("interrupted input remained available to the next terminal reader")
				}
				break
			}
		})
	}
}
