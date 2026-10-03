package atomicfile

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

func TestWriteNewCreatesCompleteFileWithRestrictedMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "bundle.evb")
	data := bytes.Repeat([]byte("complete payload"), 4096)
	if err := WriteNew(path, data); err != nil {
		t.Fatalf("WriteNew: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("published file is incomplete or changed")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("Stat: %v", err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("mode = %v, want 0600", info.Mode().Perm())
		}
	}
	assertNoTemporarySibling(t, filepath.Dir(path))
}

func TestWriteNewPreservesExistingFile(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "bundle.evb")
	original := []byte("another writer's completed file")
	if err := os.WriteFile(path, original, 0o640); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if err := WriteNew(path, []byte("replacement")); !errors.Is(err, os.ErrExist) {
		t.Fatalf("WriteNew error = %v, want os.ErrExist", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if !bytes.Equal(got, original) || !os.SameFile(before, after) || before.Mode() != after.Mode() {
		t.Fatal("WriteNew changed the existing file")
	}
	assertNoTemporarySibling(t, directory)
}

func TestWriteNewRejectsUnsafeTargets(t *testing.T) {
	for _, kind := range []string{"directory", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			directory := t.TempDir()
			path := filepath.Join(directory, "bundle.evb")
			outside := filepath.Join(directory, "outside")
			original := []byte("untouched")
			if err := os.WriteFile(outside, original, 0o600); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			if kind == "directory" {
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatalf("Mkdir: %v", err)
				}
			} else if err := os.Symlink(outside, path); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			if err := WriteNew(path, []byte("payload")); !IsUnsafeTarget(err) {
				t.Fatalf("WriteNew error = %v, want unsafe target", err)
			}
			got, err := os.ReadFile(outside)
			if err != nil {
				t.Fatalf("ReadFile: %v", err)
			}
			if !bytes.Equal(got, original) {
				t.Fatal("WriteNew changed the symlink destination")
			}
			assertNoTemporarySibling(t, directory)
		})
	}
}

func TestWriteNewConcurrentWritersPublishExactlyOneCompleteFile(t *testing.T) {
	const writers = 16
	directory := t.TempDir()
	path := filepath.Join(directory, "bundle.evb")
	var results [writers]error
	var wait sync.WaitGroup
	start := make(chan struct{})
	for i := range writers {
		wait.Go(func() {
			<-start
			results[i] = WriteNew(path, bytes.Repeat([]byte{byte(i)}, 64<<10))
		})
	}
	close(start)
	wait.Wait()
	winner := -1
	for i, err := range results {
		if err == nil {
			if winner != -1 {
				t.Fatal("more than one writer succeeded")
			}
			winner = i
		} else if !errors.Is(err, os.ErrExist) {
			t.Fatalf("writer %d: %v, want os.ErrExist", i, err)
		}
	}
	if winner == -1 {
		t.Fatal("no writer succeeded")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(got, bytes.Repeat([]byte{byte(winner)}, 64<<10)) {
		t.Fatal("published file does not match the successful writer")
	}
	assertNoTemporarySibling(t, directory)
}

func TestWriteNewPreservesLongPathSupport(t *testing.T) {
	directory := t.TempDir()
	for range 20 {
		directory = filepath.Join(directory, "nested-directory")
	}
	path := filepath.Join(directory, "bundle.evb")
	payload := []byte("complete file")
	if err := WriteNew(path, payload); err != nil {
		t.Fatalf("WriteNew long path: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("published file is incomplete or changed")
	}
	assertNoTemporarySibling(t, directory)
}

func TestLinkFallbackRetainsExclusivePublication(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "bundle.evb")
	original := []byte("first complete file")
	if err := write(path, original, linkNew); err != nil {
		t.Fatalf("publish via hard-link fallback: %v", err)
	}
	if err := write(path, []byte("replacement"), linkNew); !errors.Is(err, os.ErrExist) {
		t.Fatalf("hard-link fallback error = %v, want os.ErrExist", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(got, original) {
		t.Fatal("hard-link fallback replaced the existing file")
	}
	assertNoTemporarySibling(t, directory)
}
