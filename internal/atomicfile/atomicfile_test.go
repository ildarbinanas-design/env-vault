package atomicfile

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWriteCreatesFileWithRestrictedMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bundle.evb")
	if err := Write(path, []byte("payload")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "payload" {
		t.Fatalf("content = %q, want %q", data, "payload")
	}
	if runtime.GOOS == "windows" {
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("mode = %v, want 0600", mode)
	}
}

func TestWriteReplacesExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bundle.evb")
	if err := os.WriteFile(path, []byte("stale"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := Write(path, []byte("fresh")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "fresh" {
		t.Fatalf("content = %q, want %q", data, "fresh")
	}
}

func TestWriteCreatesMissingDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "deeper", "bundle.evb")
	if err := Write(path, []byte("payload")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("Stat: %v", err)
	}
}

// A symlink at the target must fail instead of writing through to whatever it
// points at. Writing through would let a checkout redirect an export into an
// arbitrary path.
func TestWriteRejectsSymlinkTarget(t *testing.T) {
	directory := t.TempDir()
	outside := filepath.Join(directory, "outside")
	if err := os.WriteFile(outside, []byte("untouched"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	path := filepath.Join(directory, "bundle.evb")
	if err := os.Symlink(outside, path); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	err := Write(path, []byte("payload"))
	if err == nil {
		t.Fatal("Write succeeded through a symlink")
	}
	if !IsUnsafeTarget(err) {
		t.Fatalf("IsUnsafeTarget(%v) = false, want true", err)
	}

	data, readErr := os.ReadFile(outside)
	if readErr != nil {
		t.Fatalf("ReadFile: %v", readErr)
	}
	if string(data) != "untouched" {
		t.Fatalf("symlink destination was modified: %q", data)
	}
}

func TestWriteRejectsDirectoryTarget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bundle.evb")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	err := Write(path, []byte("payload"))
	if err == nil {
		t.Fatal("Write succeeded onto a directory")
	}
	if !IsUnsafeTarget(err) {
		t.Fatalf("IsUnsafeTarget(%v) = false, want true", err)
	}
}

func TestWriteLeavesNoTemporarySibling(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "bundle.evb")
	if err := Write(path, []byte("payload")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	assertNoTemporarySibling(t, directory)
}

func TestWriteRemovesTemporarySiblingAfterFailure(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "bundle.evb")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if err := Write(path, []byte("payload")); err == nil {
		t.Fatal("Write succeeded onto a directory")
	}
	assertNoTemporarySibling(t, directory)
}

func TestValidateTargetAcceptsMissingPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.evb")
	if err := ValidateTarget(path); err != nil {
		t.Fatalf("ValidateTarget: %v", err)
	}
}

func assertNoTemporarySibling(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".tmp-") {
			t.Fatalf("temporary sibling left behind: %s", entry.Name())
		}
	}
}
