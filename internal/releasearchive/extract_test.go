package releasearchive

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ildarbinanas-design/env-vault/internal/releasecontract"
)

var (
	testReleaseContract = mustLoadReleaseContract()
	releaseArchives     = archiveSpecs(testReleaseContract)
)

func mustLoadReleaseContract() releasecontract.Contract {
	contract, err := releasecontract.LoadFile(filepath.Join("..", "..", filepath.FromSlash(releasecontract.CanonicalPath)))
	if err != nil {
		panic(err)
	}
	return contract
}

type archiveEntry struct {
	name     string
	kind     byte
	mode     os.FileMode
	content  string
	linkname string
}

func TestExtractAllValidReleaseArchives(t *testing.T) {
	inputDir := t.TempDir()
	outputDir := filepath.Join(t.TempDir(), "extracted")

	for _, spec := range releaseArchives {
		entries := []archiveEntry{
			{name: spec.root + "/", kind: tar.TypeDir, mode: 0o755},
			{name: spec.root + "/README.md", kind: tar.TypeReg, mode: 0o644, content: "release documentation\n"},
		}
		if spec.format == formatZip {
			writeZipArchive(t, filepath.Join(inputDir, spec.name), entries)
		} else {
			writeTarArchive(t, filepath.Join(inputDir, spec.name), entries)
		}
		if err := os.WriteFile(filepath.Join(inputDir, spec.name+".sha256"), []byte("sidecar\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if err := ExtractAll(inputDir, outputDir, testReleaseContract); err != nil {
		t.Fatalf("ExtractAll() error = %v", err)
	}
	for _, spec := range releaseArchives {
		content, err := os.ReadFile(filepath.Join(outputDir, spec.root, "README.md"))
		if err != nil {
			t.Fatalf("read %s payload: %v", spec.name, err)
		}
		if got, want := string(content), "release documentation\n"; got != want {
			t.Fatalf("%s payload = %q, want %q", spec.name, got, want)
		}
	}
}

func TestTarRejectsTraversal(t *testing.T) {
	spec := releaseArchives[0]
	archivePath := filepath.Join(t.TempDir(), spec.name)
	writeTarArchive(t, archivePath, []archiveEntry{
		{name: spec.root + "/../../outside", kind: tar.TypeReg, mode: 0o644, content: "bad"},
	})

	err := extractOneForTest(t, archivePath, defaultLimits)
	requireErrorContains(t, err, "double-dot sequences in paths are not allowed")
}

func TestTarRejectsDoubleDotWithinFilename(t *testing.T) {
	spec := releaseArchives[0]
	archivePath := filepath.Join(t.TempDir(), spec.name)
	writeTarArchive(t, archivePath, []archiveEntry{
		{name: spec.root + "/env..vault", kind: tar.TypeReg, mode: 0o644, content: "bad"},
	})

	err := extractOneForTest(t, archivePath, defaultLimits)
	requireErrorContains(t, err, "double-dot sequences in paths are not allowed")
}

func TestTarRejectsAbsolutePath(t *testing.T) {
	spec := releaseArchives[0]
	archivePath := filepath.Join(t.TempDir(), spec.name)
	writeTarArchive(t, archivePath, []archiveEntry{
		{name: "/tmp/outside", kind: tar.TypeReg, mode: 0o644, content: "bad"},
	})

	err := extractOneForTest(t, archivePath, defaultLimits)
	requireErrorContains(t, err, "absolute path is not allowed")
}

func TestTarRejectsSymbolicLink(t *testing.T) {
	spec := releaseArchives[0]
	archivePath := filepath.Join(t.TempDir(), spec.name)
	writeTarArchive(t, archivePath, []archiveEntry{
		{name: spec.root + "/link", kind: tar.TypeSymlink, mode: 0o777, linkname: "/etc/passwd"},
	})

	err := extractOneForTest(t, archivePath, defaultLimits)
	requireErrorContains(t, err, "symbolic links are not allowed")
}

func TestTarRejectsHardLink(t *testing.T) {
	spec := releaseArchives[0]
	archivePath := filepath.Join(t.TempDir(), spec.name)
	writeTarArchive(t, archivePath, []archiveEntry{
		{name: spec.root + "/target", kind: tar.TypeReg, mode: 0o644, content: "target"},
		{name: spec.root + "/hardlink", kind: tar.TypeLink, mode: 0o644, linkname: spec.root + "/target"},
	})

	err := extractOneForTest(t, archivePath, defaultLimits)
	requireErrorContains(t, err, "hard links are not allowed")
}

func TestTarRejectsSpecialFile(t *testing.T) {
	spec := releaseArchives[0]
	archivePath := filepath.Join(t.TempDir(), spec.name)
	writeTarArchive(t, archivePath, []archiveEntry{
		{name: spec.root + "/device", kind: tar.TypeChar, mode: 0o600},
	})

	err := extractOneForTest(t, archivePath, defaultLimits)
	requireErrorContains(t, err, "unsupported tar entry type")
}

func TestTarRejectsOversizedFile(t *testing.T) {
	spec := releaseArchives[0]
	archivePath := filepath.Join(t.TempDir(), spec.name)
	writeTarArchive(t, archivePath, []archiveEntry{
		{name: spec.root + "/binary", kind: tar.TypeReg, mode: 0o755, content: "1234"},
	})

	err := extractOneForTest(t, archivePath, extractionLimits{
		entriesPerArchive: 4,
		fileBytes:         3,
		archiveBytes:      10,
		totalBytes:        10,
	})
	requireErrorContains(t, err, "file size 4 exceeds limit 3")
}

func TestTarRejectsFileDirectoryCollision(t *testing.T) {
	spec := releaseArchives[0]
	archivePath := filepath.Join(t.TempDir(), spec.name)
	writeTarArchive(t, archivePath, []archiveEntry{
		{name: spec.root + "/collision", kind: tar.TypeReg, mode: 0o644, content: "file"},
		{name: spec.root + "/collision/", kind: tar.TypeDir, mode: 0o755},
	})

	err := extractOneForTest(t, archivePath, defaultLimits)
	requireErrorContains(t, err, "file/directory collision")
}

func TestZipRejectsTraversal(t *testing.T) {
	spec := releaseArchives[len(releaseArchives)-1]
	archivePath := filepath.Join(t.TempDir(), spec.name)
	writeZipArchive(t, archivePath, []archiveEntry{
		{name: spec.root + "/../outside", kind: tar.TypeReg, mode: 0o644, content: "bad"},
	})

	err := extractOneForTest(t, archivePath, defaultLimits)
	requireErrorContains(t, err, "double-dot sequences in paths are not allowed")
}

func TestZipRejectsDoubleDotWithinFilename(t *testing.T) {
	spec := releaseArchives[len(releaseArchives)-1]
	archivePath := filepath.Join(t.TempDir(), spec.name)
	writeZipArchive(t, archivePath, []archiveEntry{
		{name: spec.root + "/env..vault.exe", kind: tar.TypeReg, mode: 0o644, content: "bad"},
	})

	err := extractOneForTest(t, archivePath, defaultLimits)
	requireErrorContains(t, err, "double-dot sequences in paths are not allowed")
}

func TestZipRejectsWindowsAbsolutePath(t *testing.T) {
	spec := releaseArchives[len(releaseArchives)-1]
	archivePath := filepath.Join(t.TempDir(), spec.name)
	writeZipArchive(t, archivePath, []archiveEntry{
		{name: "C:/outside", kind: tar.TypeReg, mode: 0o644, content: "bad"},
	})

	err := extractOneForTest(t, archivePath, defaultLimits)
	requireErrorContains(t, err, "absolute path is not allowed")
}

func TestZipRejectsSymbolicLink(t *testing.T) {
	spec := releaseArchives[len(releaseArchives)-1]
	archivePath := filepath.Join(t.TempDir(), spec.name)
	writeZipArchive(t, archivePath, []archiveEntry{
		{name: spec.root + "/link", kind: tar.TypeSymlink, mode: 0o777, content: "/etc/passwd"},
	})

	err := extractOneForTest(t, archivePath, defaultLimits)
	requireErrorContains(t, err, "symbolic links are not allowed")
}

func TestZipRejectsOversizedFile(t *testing.T) {
	spec := releaseArchives[len(releaseArchives)-1]
	archivePath := filepath.Join(t.TempDir(), spec.name)
	writeZipArchive(t, archivePath, []archiveEntry{
		{name: spec.root + "/env-vault.exe", kind: tar.TypeReg, mode: 0o755, content: "1234"},
	})

	err := extractOneForTest(t, archivePath, extractionLimits{
		entriesPerArchive: 4,
		fileBytes:         3,
		archiveBytes:      10,
		totalBytes:        10,
	})
	requireErrorContains(t, err, "file size 4 exceeds limit 3")
}

func TestZipRejectsDuplicateEntry(t *testing.T) {
	spec := releaseArchives[len(releaseArchives)-1]
	archivePath := filepath.Join(t.TempDir(), spec.name)
	entryName := spec.root + "/env-vault.exe"
	writeZipArchive(t, archivePath, []archiveEntry{
		{name: entryName, kind: tar.TypeReg, mode: 0o755, content: "first"},
		{name: entryName, kind: tar.TypeReg, mode: 0o755, content: "second"},
	})

	err := extractOneForTest(t, archivePath, defaultLimits)
	requireErrorContains(t, err, "duplicate archive entry")
}

func TestRejectsWrongArchiveName(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "env-vault-plan9-amd64.tar.gz")
	if err := os.WriteFile(archivePath, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	err := ExtractArchive(archivePath, filepath.Join(t.TempDir(), "output"), testReleaseContract)
	requireErrorContains(t, err, "unsupported release archive name")
}

func TestRejectsWrongArchiveRoot(t *testing.T) {
	spec := releaseArchives[0]
	archivePath := filepath.Join(t.TempDir(), spec.name)
	writeTarArchive(t, archivePath, []archiveEntry{
		{name: "env-vault-other/env-vault", kind: tar.TypeReg, mode: 0o755, content: "binary"},
	})

	err := extractOneForTest(t, archivePath, defaultLimits)
	requireErrorContains(t, err, `path must be rooted at "`+spec.root+`"`)
}

func TestRejectsNonEmptyOutputDirectory(t *testing.T) {
	spec := releaseArchives[0]
	archivePath := filepath.Join(t.TempDir(), spec.name)
	writeTarArchive(t, archivePath, []archiveEntry{
		{name: spec.root + "/env-vault", kind: tar.TypeReg, mode: 0o755, content: "binary"},
	})
	outputDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(outputDir, "keep"), []byte("do not overwrite"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := ExtractArchive(archivePath, outputDir, testReleaseContract)
	requireErrorContains(t, err, "output directory must be empty")
	content, readErr := os.ReadFile(filepath.Join(outputDir, "keep"))
	if readErr != nil || string(content) != "do not overwrite" {
		t.Fatalf("existing output was changed: content %q, error %v", content, readErr)
	}
}

func TestRejectsAggregateSizeLimit(t *testing.T) {
	spec := releaseArchives[0]
	archivePath := filepath.Join(t.TempDir(), spec.name)
	writeTarArchive(t, archivePath, []archiveEntry{
		{name: spec.root + "/one", kind: tar.TypeReg, mode: 0o644, content: "123"},
		{name: spec.root + "/two", kind: tar.TypeReg, mode: 0o644, content: "456"},
	})

	err := extractOneForTest(t, archivePath, extractionLimits{
		entriesPerArchive: 4,
		fileBytes:         4,
		archiveBytes:      10,
		totalBytes:        5,
	})
	requireErrorContains(t, err, "total extracted size exceeds limit 5")
}

func TestRejectsEntryCountLimit(t *testing.T) {
	spec := releaseArchives[len(releaseArchives)-1]
	archivePath := filepath.Join(t.TempDir(), spec.name)
	writeZipArchive(t, archivePath, []archiveEntry{
		{name: spec.root + "/one", kind: tar.TypeReg, mode: 0o644, content: "1"},
		{name: spec.root + "/two", kind: tar.TypeReg, mode: 0o644, content: "2"},
	})

	err := extractOneForTest(t, archivePath, extractionLimits{
		entriesPerArchive: 1,
		fileBytes:         4,
		archiveBytes:      10,
		totalBytes:        10,
	})
	requireErrorContains(t, err, "entry count exceeds limit 1")
}

func extractOneForTest(t *testing.T, archivePath string, limits extractionLimits) error {
	t.Helper()
	spec, ok := archiveSpecByName(filepath.Base(archivePath), releaseArchives)
	if !ok {
		t.Fatalf("test archive has unsupported name %q", filepath.Base(archivePath))
	}
	outputDir := filepath.Join(t.TempDir(), "output")
	if err := prepareOutputDirectory(outputDir); err != nil {
		t.Fatal(err)
	}
	return extractArchive(spec, archivePath, outputDir, newExtractionState(limits))
}

func writeTarArchive(t *testing.T, filename string, entries []archiveEntry) {
	t.Helper()
	file, err := os.Create(filename)
	if err != nil {
		t.Fatal(err)
	}
	gzipWriter := gzip.NewWriter(file)
	tarWriter := tar.NewWriter(gzipWriter)
	for _, entry := range entries {
		header := &tar.Header{
			Name:     entry.name,
			Mode:     int64(entry.mode.Perm()),
			Typeflag: entry.kind,
			Linkname: entry.linkname,
		}
		if entry.kind == tar.TypeReg || entry.kind == tar.TypeRegA {
			header.Size = int64(len(entry.content))
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatalf("write tar header %q: %v", entry.name, err)
		}
		if header.Size > 0 {
			if _, err := io.Copy(tarWriter, strings.NewReader(entry.content)); err != nil {
				t.Fatalf("write tar content %q: %v", entry.name, err)
			}
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeZipArchive(t *testing.T, filename string, entries []archiveEntry) {
	t.Helper()
	file, err := os.Create(filename)
	if err != nil {
		t.Fatal(err)
	}
	zipWriter := zip.NewWriter(file)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name}
		mode := entry.mode
		switch entry.kind {
		case tar.TypeDir:
			mode |= os.ModeDir
		case tar.TypeSymlink:
			mode |= os.ModeSymlink
		}
		header.SetMode(mode)
		writer, err := zipWriter.CreateHeader(header)
		if err != nil {
			t.Fatalf("write zip header %q: %v", entry.name, err)
		}
		if entry.content != "" {
			if _, err := io.Copy(writer, strings.NewReader(entry.content)); err != nil {
				t.Fatalf("write zip content %q: %v", entry.name, err)
			}
		}
	}
	if err := zipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func requireErrorContains(t *testing.T, err error, substring string) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want substring %q", substring)
	}
	if !strings.Contains(err.Error(), substring) {
		t.Fatalf("error = %q, want substring %q", err, substring)
	}
}
