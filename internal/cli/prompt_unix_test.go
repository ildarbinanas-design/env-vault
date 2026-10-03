//go:build darwin || linux

package cli

import (
	"io"
	"os"
	"os/exec"
	"os/signal"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/ildarbinanas-design/env-vault/internal/bundle"
	"github.com/ildarbinanas-design/env-vault/internal/testutil"
)

// This subprocess calls only the input functions. It cannot open any backend,
// and no value or passphrase is supplied for the interrupted-prompt cases.
func TestHiddenPromptHelper(t *testing.T) {
	kind := os.Getenv("ENV_VAULT_PROMPT_TEST_HELPER")
	if kind == "" {
		return
	}
	if strings.HasSuffix(kind, "-ignored") {
		signal.Ignore(syscall.SIGINT)
		kind = strings.TrimSuffix(kind, "-ignored")
	}
	app := newApp(os.Stdin, io.Discard, io.Discard)
	var value []byte
	var err error
	if kind == "secret" {
		value, err = app.readSecret(false)
	} else if kind == "confirmation" {
		app.passphraseReader = app.terminalPassphrase("export")
		value, err = app.readPassphrase("export", true)
	} else {
		value, err = app.terminalPassphrase("import")("Passphrase: ")
	}
	bundle.Wipe(value)
	if err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func TestHiddenPromptsRestoreTerminalOnSignals(t *testing.T) {
	for _, kind := range []string{"secret", "passphrase", "confirmation"} {
		for _, termination := range []string{"ctrl-c", "sigterm", "ignored-interrupt", "completed"} {
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
				case "ctrl-c":
					wantSignal = syscall.SIGINT
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
	for _, kind := range []string{"secret", "passphrase"} {
		t.Run(kind, func(t *testing.T) {
			controller, terminal := openPseudoTerminal(t)
			child := exec.Command(os.Args[0], "-test.run=^TestHiddenPromptHelper$")
			child.Env = append(os.Environ(), "ENV_VAULT_PROMPT_TEST_HELPER="+kind)
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
			if _, err := controller.Write([]byte(testutil.EphemeralValue(t))); err != nil {
				t.Fatal("unable to type partial input")
			}
			if err := child.Process.Signal(syscall.SIGTERM); err != nil {
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
