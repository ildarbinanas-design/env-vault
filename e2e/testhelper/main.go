// Command testhelper is a portable subprocess used by env-vault's black-box
// E2E tests. It deliberately reports only public, non-secret observations.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"

	"github.com/gofrs/flock"
)

type stringList []string

func (values *stringList) String() string { return strings.Join(*values, ",") }

func (values *stringList) Set(value string) error {
	*values = append(*values, value)
	return nil
}

func main() {
	if len(os.Args) < 2 {
		fail("mode is required")
	}
	var err error
	switch os.Args[1] {
	case "args":
		err = runArgs(os.Args[2:])
	case "env":
		err = runEnv(os.Args[2:])
	case "streams":
		err = runStreams(os.Args[2:])
	case "marker":
		err = runMarker(os.Args[2:])
	case "wait-signal":
		err = runWaitSignal(os.Args[2:])
	case "hold-lock":
		err = runHoldLock(os.Args[2:])
	default:
		err = fmt.Errorf("unknown mode %q", os.Args[1])
	}
	if err != nil {
		fail(err.Error())
	}
}

func runArgs(args []string) error {
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"args": args})
}

func runEnv(args []string) error {
	set := flag.NewFlagSet("env", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	var hashes, values, absent stringList
	set.Var(&hashes, "expect-hash", "NAME=SHA256")
	set.Var(&values, "expect-value", "NAME=VALUE")
	set.Var(&absent, "expect-absent", "NAME")
	if err := set.Parse(args); err != nil {
		return err
	}
	if set.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	for _, spec := range hashes {
		name, want, ok := strings.Cut(spec, "=")
		if !ok || name == "" || len(want) != sha256.Size*2 {
			return fmt.Errorf("invalid --expect-hash specification for %q", name)
		}
		value, exists := os.LookupEnv(name)
		if !exists {
			return fmt.Errorf("expected environment variable %q is absent", name)
		}
		sum := sha256.Sum256([]byte(value))
		if hex.EncodeToString(sum[:]) != want {
			return fmt.Errorf("environment variable %q has an unexpected digest", name)
		}
	}
	for _, spec := range values {
		name, want, ok := strings.Cut(spec, "=")
		if !ok || name == "" {
			return fmt.Errorf("invalid --expect-value specification for %q", name)
		}
		got, exists := os.LookupEnv(name)
		if !exists || got != want {
			return fmt.Errorf("environment variable %q does not match", name)
		}
	}
	for _, name := range absent {
		if _, exists := os.LookupEnv(name); exists {
			return fmt.Errorf("environment variable %q unexpectedly exists", name)
		}
	}
	_, err := fmt.Fprintln(os.Stdout, "env-ok")
	return err
}

func runStreams(args []string) error {
	set := flag.NewFlagSet("streams", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	stdinHash := set.String("stdin-sha256", "", "expected stdin digest")
	stdout := set.String("stdout", "", "public stdout text")
	stderr := set.String("stderr", "", "public stderr text")
	exitCode := set.Int("exit-code", 0, "exit status")
	if err := set.Parse(args); err != nil {
		return err
	}
	if set.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("read stdin: %w", err)
	}
	if *stdinHash != "" {
		sum := sha256.Sum256(input)
		if hex.EncodeToString(sum[:]) != *stdinHash {
			return errors.New("stdin digest mismatch")
		}
	}
	if *stdout != "" {
		if _, err := fmt.Fprint(os.Stdout, *stdout); err != nil {
			return err
		}
	}
	if *stderr != "" {
		if _, err := fmt.Fprint(os.Stderr, *stderr); err != nil {
			return err
		}
	}
	if *exitCode < 0 || *exitCode > 125 {
		return errors.New("exit code must be between 0 and 125")
	}
	os.Exit(*exitCode)
	return nil
}

func runMarker(args []string) error {
	set := flag.NewFlagSet("marker", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	path := set.String("path", "", "marker path")
	if err := set.Parse(args); err != nil {
		return err
	}
	if *path == "" {
		return errors.New("--path is required")
	}
	return writeMarker(*path, "started\n")
}

func runWaitSignal(args []string) error {
	set := flag.NewFlagSet("wait-signal", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	ready := set.String("ready", "", "ready marker")
	received := set.String("received", "", "received-signal marker")
	if err := set.Parse(args); err != nil {
		return err
	}
	if *ready == "" || *received == "" {
		return errors.New("--ready and --received are required")
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, forwardedSignals()...)
	defer signal.Stop(signals)
	if err := writeMarker(*ready, "ready\n"); err != nil {
		return err
	}
	got := <-signals
	return writeMarker(*received, got.String()+"\n")
}

func runHoldLock(args []string) error {
	set := flag.NewFlagSet("hold-lock", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	path := set.String("path", "", "lock path")
	ready := set.String("ready", "", "ready marker")
	if err := set.Parse(args); err != nil {
		return err
	}
	if *path == "" || *ready == "" {
		return errors.New("--path and --ready are required")
	}
	lock := flock.New(*path, flock.SetPermissions(0o600))
	locked, err := lock.TryLock()
	if err != nil {
		return err
	}
	if !locked {
		return errors.New("lock is already held")
	}
	defer lock.Close()
	if err := writeMarker(*ready, strconv.Itoa(os.Getpid())+"\n"); err != nil {
		return err
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, forwardedSignals()...)
	defer signal.Stop(signals)
	<-signals
	return nil
}

func writeMarker(path, value string) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(file, value); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func fail(message string) {
	// Errors intentionally never include environment values or stdin.
	_, _ = fmt.Fprintln(os.Stderr, "testhelper:", message)
	os.Exit(120)
}
