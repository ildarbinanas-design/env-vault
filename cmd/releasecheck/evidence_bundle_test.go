package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ildarbinanas-design/env-vault/internal/releaseevidence"
)

func TestReadBundleDirectoryRequiresExactCleanExportShape(t *testing.T) {
	tests := map[string]func(*testing.T, string, string){
		"extra top-level file": func(t *testing.T, directory, _ string) {
			if err := os.WriteFile(filepath.Join(directory, "unexpected.txt"), []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"extra empty object directory": func(t *testing.T, directory, _ string) {
			if err := os.Mkdir(filepath.Join(directory, "objects", "extra"), 0o700); err != nil {
				t.Fatal(err)
			}
		},
		"object symlink": func(t *testing.T, _, objectPath string) {
			if err := os.Remove(objectPath); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Base(objectPath), objectPath); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			directory, objectPath := writeMinimalBundleDirectory(t, []releaseevidence.BundleObject{{
				SHA256: strings.Repeat("a", 64), MediaType: releaseevidence.RawJSONMedia,
				Encoding: releaseevidence.BundleEncodingGZIP, UncompressedSize: 1,
				CompressedSize: 1, CompressedSHA256: strings.Repeat("b", 64),
			}}, []int64{1})
			mutate(t, directory, objectPath[0])
			if _, err := readBundleDirectory(directory); err == nil {
				t.Fatal("non-clean exported bundle directory was accepted")
			}
		})
	}

	directory, _ := writeMinimalBundleDirectory(t, []releaseevidence.BundleObject{{
		SHA256: strings.Repeat("a", 64), MediaType: releaseevidence.RawJSONMedia,
		Encoding: releaseevidence.BundleEncodingGZIP, UncompressedSize: 1,
		CompressedSize: 1, CompressedSHA256: strings.Repeat("b", 64),
	}}, []int64{1})
	if _, err := readBundleDirectory(directory); err != nil {
		t.Fatalf("clean directory shape was rejected before semantic verification: %v", err)
	}
}

func TestReadBundleDirectoryRejectsAggregateCompressedBytesBeforeRead(t *testing.T) {
	perObject := int64(33 << 20)
	descriptors := []releaseevidence.BundleObject{
		{SHA256: strings.Repeat("a", 64), MediaType: releaseevidence.RawJSONMedia, Encoding: releaseevidence.BundleEncodingGZIP, UncompressedSize: 1, CompressedSize: perObject, CompressedSHA256: strings.Repeat("c", 64)},
		{SHA256: strings.Repeat("b", 64), MediaType: releaseevidence.RawJSONMedia, Encoding: releaseevidence.BundleEncodingGZIP, UncompressedSize: 1, CompressedSize: perObject, CompressedSHA256: strings.Repeat("d", 64)},
	}
	directory, _ := writeMinimalBundleDirectory(t, descriptors, []int64{perObject, perObject})
	if _, err := readBundleDirectory(directory); err == nil || !strings.Contains(err.Error(), "aggregate") {
		t.Fatalf("aggregate pre-read error=%v", err)
	}
}

func TestWriteBundleDirectoryReservesWithoutClobberAndReaderRejectsIncomplete(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "bundle")
	digest := strings.Repeat("a", 64)
	descriptor := releaseevidence.BundleObject{
		SHA256: digest, MediaType: releaseevidence.RawJSONMedia, Encoding: releaseevidence.BundleEncodingGZIP,
		UncompressedSize: 1, CompressedSize: 1, CompressedSHA256: strings.Repeat("b", 64),
	}
	bundleRoot, err := releaseevidence.MarshalJSON(releaseevidence.Bundle{
		SchemaID: releaseevidence.BundleSchemaID, SchemaVersion: releaseevidence.BundleSchemaVersion, Objects: []releaseevidence.BundleObject{descriptor},
	})
	if err != nil {
		t.Fatal(err)
	}
	files := releaseevidence.BundleFiles{
		Root: bundleRoot, Objects: map[string][]byte{"objects/sha256/" + digest + ".gz": {0x01}},
	}
	emptyTarget := filepath.Join(root, "empty-reservation")
	err = writeBundleDirectoryNoClobberWithHook(emptyTarget, files, func() {
		if err := os.Mkdir(emptyTarget, 0o700); err != nil {
			t.Fatal(err)
		}
	})
	if err == nil {
		t.Fatal("concurrently created empty bundle directory was replaced")
	}
	emptyEntries, readErr := os.ReadDir(emptyTarget)
	if readErr != nil || len(emptyEntries) != 0 {
		t.Fatalf("empty concurrent reservation changed: entries=%d error=%v", len(emptyEntries), readErr)
	}

	foreign := []byte("foreign concurrent owner\n")
	err = writeBundleDirectoryNoClobberWithHook(target, files, func() {
		if err := os.Mkdir(target, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(target, "foreign"), foreign, 0o600); err != nil {
			t.Fatal(err)
		}
	})
	if err == nil {
		t.Fatal("concurrently reserved bundle directory was replaced")
	}
	got, readErr := os.ReadFile(filepath.Join(target, "foreign"))
	if readErr != nil || string(got) != string(foreign) {
		t.Fatalf("foreign concurrent content changed: %q error=%v", got, readErr)
	}

	incomplete, _ := writeMinimalBundleDirectory(t, []releaseevidence.BundleObject{descriptor}, []int64{1})
	if err := os.WriteFile(filepath.Join(incomplete, ".incomplete"), []byte("incomplete\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readBundleDirectory(incomplete); err == nil {
		t.Fatal("bundle reader accepted an incomplete reservation marker")
	}

	completed := filepath.Join(root, "completed")
	if err := writeBundleDirectoryNoClobber(completed, files); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(completed, ".incomplete")); !os.IsNotExist(err) {
		t.Fatalf("completed bundle retained marker: %v", err)
	}
	if _, err := readBundleDirectory(completed); err != nil {
		t.Fatalf("completed exact bundle closure was unreadable: %v", err)
	}
}

func writeMinimalBundleDirectory(t *testing.T, descriptors []releaseevidence.BundleObject, sizes []int64) (string, []string) {
	t.Helper()
	directory := t.TempDir()
	objectDirectory := filepath.Join(directory, filepath.FromSlash(releaseevidence.BundleObjectStoreDirectory))
	if err := os.MkdirAll(objectDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	bundle := releaseevidence.Bundle{
		SchemaID: releaseevidence.BundleSchemaID, SchemaVersion: releaseevidence.BundleSchemaVersion,
		Objects: descriptors,
	}
	root, err := releaseevidence.MarshalJSON(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, releaseevidence.BundleRootName), root, 0o600); err != nil {
		t.Fatal(err)
	}
	paths := make([]string, len(descriptors))
	for index, descriptor := range descriptors {
		relative, err := releaseevidence.BundleObjectRelativePath(descriptor.SHA256)
		if err != nil {
			t.Fatal(err)
		}
		paths[index] = filepath.Join(directory, filepath.FromSlash(relative))
		file, err := os.OpenFile(paths[index], os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		if err := file.Truncate(sizes[index]); err != nil {
			file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return directory, paths
}
