//go:build !windows

package runner

import (
	"os"
	"path/filepath"
	"testing"

	apperrors "github.com/ildarbinanas-design/env-vault/internal/errors"
)

func TestNonExecutablePathEntryReturns126(t *testing.T) {
	directory := t.TempDir()
	name := "env-vault-nonexecutable-test"
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory)
	for _, command := range []string{name, path} {
		err := (CommandRunner{}).Validate([]string{command})
		appErr, ok := apperrors.From(err)
		if !ok || appErr.Code != apperrors.CodeCommandNotExecutable || appErr.ExitCode != 126 {
			t.Fatalf("Validate: %v, want COMMAND_NOT_EXECUTABLE/126", err)
		}
	}
}

func TestPathSearchStillFindsLaterExecutable(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	const name = "env-vault-path-test"
	for directory, mode := range map[string]os.FileMode{first: 0o600, second: 0o700} {
		if err := os.WriteFile(filepath.Join(directory, name), nil, mode); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", first+string(os.PathListSeparator)+second)
	if err := (CommandRunner{}).Validate([]string{name}); err != nil {
		t.Fatal(err)
	}
}

func TestPathSearchPreservesErrDot(t *testing.T) {
	t.Chdir(t.TempDir())
	const name = "env-vault-dot-test"
	if err := os.WriteFile(name, nil, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", ".")
	t.Setenv("GODEBUG", "execerrdot=1")
	err := (CommandRunner{}).Validate([]string{name})
	appErr, ok := apperrors.From(err)
	if !ok || appErr.Code != apperrors.CodeCommandNotFound || appErr.ExitCode != 127 {
		t.Fatalf("Validate: %v, want relative PATH rejection", err)
	}
}

func TestEmptyCommandIsNotAPathDirectory(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	err := (CommandRunner{}).Validate([]string{""})
	appErr, ok := apperrors.From(err)
	if !ok || appErr.Code != apperrors.CodeCommandNotFound || appErr.ExitCode != 127 {
		t.Fatalf("Validate: %v, want COMMAND_NOT_FOUND/127", err)
	}
}
