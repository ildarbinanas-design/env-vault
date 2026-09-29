package output

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	apperrors "github.com/ildarbinanas-design/env-vault/internal/errors"
)

func TestOutputFileReplacesTargetWithPrivateMode(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "meta.json")
	if err := os.WriteFile(path, []byte(`{"ok":true,"command":"old"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := statOpenFile(t, path)
	renderer := New(&bytes.Buffer{}, &bytes.Buffer{}, Options{Quiet: true, OutputPath: path})
	if err := renderer.Success("secret_check", map[string]any{"name": "nexus-token"}, nil); err != nil {
		t.Fatalf("success: %v", err)
	}
	if env := readOutputFile(t, path); !env.OK || env.Command != "secret_check" {
		t.Fatalf("output file envelope: %#v", env)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// A reader of the old file never sees a truncated one: the file is
	// replaced, not rewritten in place.
	if os.SameFile(before, info) {
		t.Fatal("the output file was rewritten in place instead of replaced")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("output file mode=%v, want 0600", info.Mode().Perm())
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("directory holds %d entries, want only the output file", len(entries))
	}
}

func TestOutputFileRefusesSymlink(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	original := []byte("not env-vault metadata\n")
	if err := os.WriteFile(target, original, 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "meta.json")
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}
	renderer := New(&bytes.Buffer{}, &bytes.Buffer{}, Options{Quiet: true, OutputPath: path})
	if err := renderer.Success("version", map[string]any{"version": "test"}, nil); err == nil {
		t.Fatal("success wrote through a symlinked --output")
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, original) {
		t.Fatal("the symlink's target changed")
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the symlink was replaced: mode=%v", info.Mode())
	}
}

func TestCommandFailedWritesOnlyTheOutputFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "meta.json")
	var stdout, stderr bytes.Buffer
	New(&stdout, &stderr, Options{JSON: true, OutputPath: path}).CommandFailed("exec", map[string]any{"exit_code": 7}, 7, "")
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("stdout=%q stderr=%q, want both empty", stdout.String(), stderr.String())
	}
	env := readOutputFile(t, path)
	if env.OK || env.Command != "exec" || env.Error == nil || env.Error.Code != apperrors.CodeCommandFailed || env.Error.Message != "Command exited with status 7" {
		t.Fatalf("output file envelope: %#v", env)
	}

	New(&stdout, &stderr, Options{OutputPath: path}).CommandFailed("exec", nil, 143, "SIGTERM")
	if env := readOutputFile(t, path); env.Error == nil || env.Error.Message != "Command was killed by SIGTERM (status 143)" {
		t.Fatalf("output file envelope for a signal: %#v", env)
	}
}

func TestCommandFailedReportsAnUnwritableFileOnlyWhenVerbose(t *testing.T) {
	t.Parallel()
	// An existing directory cannot be replaced by the output file.
	dir := t.TempDir()
	for _, verbose := range []bool{false, true} {
		var stdout, stderr bytes.Buffer
		New(&stdout, &stderr, Options{OutputPath: dir, Verbose: verbose}).CommandFailed("exec", nil, 3, "")
		if stdout.Len() != 0 {
			t.Fatalf("verbose=%v stdout=%q", verbose, stdout.String())
		}
		if got := strings.Contains(stderr.String(), "OUTPUT_WRITE_FAILED:"); got != verbose {
			t.Fatalf("verbose=%v stderr=%q", verbose, stderr.String())
		}
	}
}

// statOpenFile describes the file at path through a handle that is closed
// before it returns. On Windows, os.Stat reads the file's identity only when
// os.SameFile asks, by path, and would then describe the replacement instead.
func statOpenFile(t *testing.T, path string) os.FileInfo {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func readOutputFile(t *testing.T, path string) Envelope {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatalf("decode output file: %v", err)
	}
	return env
}
