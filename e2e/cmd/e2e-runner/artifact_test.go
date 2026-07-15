package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type tarFixtureEntry struct {
	name     string
	typeflag byte
	linkname string
	data     []byte
}

type zipFixtureEntry struct {
	name string
	mode os.FileMode
	data []byte
}

func TestArchiveExtractRejectsUnsafePaths(t *testing.T) {
	const root = "env-vault-test-amd64"
	tests := []struct {
		name string
		path string
	}{
		{name: "parent traversal", path: "../escape"},
		{name: "root traversal", path: root + "/../escape"},
		{name: "double dot filename", path: root + "/env..vault"},
		{name: "absolute", path: "/absolute"},
		{name: "backslash", path: root + `\escape`},
		{name: "wrong root", path: "other-root/env-vault"},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			if _, err := safeArchiveName(test.path, root); err == nil {
				t.Fatalf("safeArchiveName(%q) accepted an unsafe path", test.path)
			}

			tarPath := filepath.Join(t.TempDir(), "artifact.tar.gz")
			writeTarFixture(t, tarPath, []tarFixtureEntry{{name: test.path, typeflag: tar.TypeReg, data: []byte("x")}})
			if err := extractTarGzArtifact(tarPath, t.TempDir(), root); err == nil {
				t.Fatalf("tar extractor accepted unsafe path %q", test.path)
			}

			zipPath := filepath.Join(t.TempDir(), "artifact.zip")
			writeZipFixture(t, zipPath, []zipFixtureEntry{{name: test.path, mode: 0o600, data: []byte("x")}})
			if err := extractZipArtifact(zipPath, t.TempDir(), root); err == nil {
				t.Fatalf("zip extractor accepted unsafe path %q", test.path)
			}
		})
	}
}

func TestTarExtractRejectsLinks(t *testing.T) {
	const root = "env-vault-test-amd64"
	for _, test := range []struct {
		name     string
		typeflag byte
	}{
		{name: "symlink", typeflag: tar.TypeSymlink},
		{name: "hardlink", typeflag: tar.TypeLink},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			archive := filepath.Join(t.TempDir(), "artifact.tar.gz")
			writeTarFixture(t, archive, []tarFixtureEntry{{
				name:     root + "/env-vault",
				typeflag: test.typeflag,
				linkname: root + "/target",
			}})
			if err := extractTarGzArtifact(archive, t.TempDir(), root); err == nil {
				t.Fatalf("tar extractor accepted %s entry", test.name)
			}
		})
	}
}

func TestZipExtractRejectsSymlink(t *testing.T) {
	const root = "env-vault-test-amd64"
	archive := filepath.Join(t.TempDir(), "artifact.zip")
	writeZipFixture(t, archive, []zipFixtureEntry{{
		name: root + "/env-vault",
		mode: os.ModeSymlink | 0o777,
		data: []byte("target"),
	}})
	if err := extractZipArtifact(archive, t.TempDir(), root); err == nil {
		t.Fatal("zip extractor accepted a symlink entry")
	}
}

func TestArchiveExtractRejectsDuplicateEntries(t *testing.T) {
	const root = "env-vault-test-amd64"
	name := root + "/env-vault"

	t.Run("tar", func(t *testing.T) {
		archive := filepath.Join(t.TempDir(), "artifact.tar.gz")
		writeTarFixture(t, archive, []tarFixtureEntry{
			{name: name, typeflag: tar.TypeReg, data: []byte("first")},
			{name: name, typeflag: tar.TypeReg, data: []byte("second")},
		})
		if err := extractTarGzArtifact(archive, t.TempDir(), root); err == nil {
			t.Fatal("tar extractor accepted duplicate entries")
		}
	})

	t.Run("zip", func(t *testing.T) {
		archive := filepath.Join(t.TempDir(), "artifact.zip")
		writeZipFixture(t, archive, []zipFixtureEntry{
			{name: name, mode: 0o600, data: []byte("first")},
			{name: name, mode: 0o600, data: []byte("second")},
		})
		if err := extractZipArtifact(archive, t.TempDir(), root); err == nil {
			t.Fatal("zip extractor accepted duplicate entries")
		}
	})
}

func TestArchiveExtractEnforcesEntryLimit(t *testing.T) {
	const root = "env-vault-test-amd64"
	tarEntries := make([]tarFixtureEntry, 0, maxArtifactEntries+1)
	zipEntries := make([]zipFixtureEntry, 0, maxArtifactEntries+1)
	for index := 0; index <= maxArtifactEntries; index++ {
		name := fmt.Sprintf("%s/dir-%03d/", root, index)
		tarEntries = append(tarEntries, tarFixtureEntry{name: name, typeflag: tar.TypeDir})
		zipEntries = append(zipEntries, zipFixtureEntry{name: name, mode: os.ModeDir | 0o700})
	}

	t.Run("tar", func(t *testing.T) {
		archive := filepath.Join(t.TempDir(), "artifact.tar.gz")
		writeTarFixture(t, archive, tarEntries)
		if err := extractTarGzArtifact(archive, t.TempDir(), root); err == nil {
			t.Fatalf("tar extractor accepted more than %d entries", maxArtifactEntries)
		}
	})

	t.Run("zip", func(t *testing.T) {
		archive := filepath.Join(t.TempDir(), "artifact.zip")
		writeZipFixture(t, archive, zipEntries)
		if err := extractZipArtifact(archive, t.TempDir(), root); err == nil {
			t.Fatalf("zip extractor accepted more than %d entries", maxArtifactEntries)
		}
	})
}

func TestArchiveExtractEnforcesFileLimit(t *testing.T) {
	const root = "env-vault-test-amd64"

	t.Run("tar", func(t *testing.T) {
		archive := filepath.Join(t.TempDir(), "artifact.tar.gz")
		writeOversizedTarFixture(t, archive, root+"/env-vault", maxArtifactFileBytes+1)
		if err := extractTarGzArtifact(archive, t.TempDir(), root); err == nil {
			t.Fatalf("tar extractor accepted file larger than %d bytes", maxArtifactFileBytes)
		}
	})

	t.Run("zip", func(t *testing.T) {
		archive := filepath.Join(t.TempDir(), "artifact.zip")
		writeOversizedZipFixture(t, archive, root+"/env-vault", uint64(maxArtifactFileBytes+1))
		if err := extractZipArtifact(archive, t.TempDir(), root); err == nil {
			t.Fatalf("zip extractor accepted file larger than %d bytes", maxArtifactFileBytes)
		}
	})
}

func TestVerifyChecksumSidecar(t *testing.T) {
	const artifactBase = "env-vault-linux-amd64.tar.gz"
	actual := strings.Repeat("a", sha256.Size*2)

	tests := []struct {
		name    string
		record  string
		wantErr bool
	}{
		{name: "valid", record: actual + " *" + artifactBase + "\n"},
		{name: "wrong filename", record: actual + "  other.tar.gz\n", wantErr: true},
		{name: "wrong digest", record: strings.Repeat("b", sha256.Size*2) + "  " + artifactBase + "\n", wantErr: true},
		{name: "invalid digest", record: strings.Repeat("z", sha256.Size*2) + "  " + artifactBase + "\n", wantErr: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "artifact.sha256")
			if err := os.WriteFile(path, []byte(test.record), 0o600); err != nil {
				t.Fatal(err)
			}
			err := verifyChecksumSidecar(path, artifactBase, actual)
			if (err != nil) != test.wantErr {
				t.Fatalf("verifyChecksumSidecar() error=%v, wantErr=%t", err, test.wantErr)
			}
		})
	}

	t.Run("symlink", func(t *testing.T) {
		directory := t.TempDir()
		target := filepath.Join(directory, "target.sha256")
		link := filepath.Join(directory, "artifact.sha256")
		if err := os.WriteFile(target, []byte(actual+"  "+artifactBase+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, link); err != nil {
			t.Skipf("symlink creation is unavailable: %v", err)
		}
		if err := verifyChecksumSidecar(link, artifactBase, actual); err == nil {
			t.Fatal("checksum verifier accepted a symlink")
		}
	})
}

func TestExtractValidTarAndZip(t *testing.T) {
	const root = "env-vault-test-amd64"
	const contents = "portable-binary-fixture"

	t.Run("tar", func(t *testing.T) {
		archive := filepath.Join(t.TempDir(), "artifact.tar.gz")
		writeTarFixture(t, archive, []tarFixtureEntry{
			{name: root + "/", typeflag: tar.TypeDir},
			{name: root + "/env-vault", typeflag: tar.TypeReg, data: []byte(contents)},
		})
		output := t.TempDir()
		if err := extractTarGzArtifact(archive, output, root); err != nil {
			t.Fatalf("extract valid tar: %v", err)
		}
		assertFixtureContents(t, filepath.Join(output, root, "env-vault"), contents)
	})

	t.Run("zip", func(t *testing.T) {
		archive := filepath.Join(t.TempDir(), "artifact.zip")
		writeZipFixture(t, archive, []zipFixtureEntry{
			{name: root + "/", mode: os.ModeDir | 0o700},
			{name: root + "/env-vault", mode: 0o600, data: []byte(contents)},
		})
		output := t.TempDir()
		if err := extractZipArtifact(archive, output, root); err != nil {
			t.Fatalf("extract valid zip: %v", err)
		}
		assertFixtureContents(t, filepath.Join(output, root, "env-vault"), contents)
	})
}

func TestVerifyAndExtractValidNativeArtifact(t *testing.T) {
	t.Setenv("ENV_VAULT_E2E_GOOS", runtime.GOOS)
	t.Setenv("ENV_VAULT_E2E_GOARCH", runtime.GOARCH)
	root := "env-vault-" + runtime.GOOS + "-" + runtime.GOARCH
	binaryName := "env-vault"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	const contents = "native-binary-fixture"
	directory := t.TempDir()
	archive := filepath.Join(directory, root+".tar.gz")
	if runtime.GOOS == "windows" {
		archive = filepath.Join(directory, root+".zip")
		writeZipFixture(t, archive, []zipFixtureEntry{
			{name: root + "/", mode: os.ModeDir | 0o700},
			{name: root + "/" + binaryName, mode: 0o700, data: []byte(contents)},
		})
	} else {
		writeTarFixture(t, archive, []tarFixtureEntry{
			{name: root + "/", typeflag: tar.TypeDir},
			{name: root + "/" + binaryName, typeflag: tar.TypeReg, data: []byte(contents)},
		})
	}
	digest, err := sha256File(archive)
	if err != nil {
		t.Fatal(err)
	}
	checksum := archive + ".sha256"
	if err := os.WriteFile(checksum, []byte(digest+"  "+filepath.Base(archive)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	binary, evidence, err := verifyAndExtractArtifact(archive, "", filepath.Join(directory, "output"))
	if err != nil {
		t.Fatalf("verify and extract valid native artifact: %v", err)
	}
	assertFixtureContents(t, binary, contents)
	if !evidence.ChecksumVerified || evidence.SHA256 != digest || evidence.ChecksumPath != filepath.ToSlash(checksum) {
		t.Fatalf("unexpected artifact evidence: %+v", evidence)
	}
}

func writeTarFixture(t *testing.T, filename string, entries []tarFixtureEntry) {
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
			Mode:     0o600,
			Typeflag: entry.typeflag,
			Linkname: entry.linkname,
			Size:     int64(len(entry.data)),
		}
		if entry.typeflag == tar.TypeDir {
			header.Mode = 0o700
			header.Size = 0
		}
		if entry.typeflag == tar.TypeSymlink || entry.typeflag == tar.TypeLink {
			header.Size = 0
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatalf("write tar header %q: %v", entry.name, err)
		}
		if len(entry.data) > 0 {
			if _, err := tarWriter.Write(entry.data); err != nil {
				t.Fatalf("write tar entry %q: %v", entry.name, err)
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

func writeZipFixture(t *testing.T, filename string, entries []zipFixtureEntry) {
	t.Helper()
	file, err := os.Create(filename)
	if err != nil {
		t.Fatal(err)
	}
	zipWriter := zip.NewWriter(file)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Store}
		header.SetMode(entry.mode)
		writer, err := zipWriter.CreateHeader(header)
		if err != nil {
			t.Fatalf("write zip header %q: %v", entry.name, err)
		}
		if len(entry.data) > 0 {
			if _, err := writer.Write(entry.data); err != nil {
				t.Fatalf("write zip entry %q: %v", entry.name, err)
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

func writeOversizedTarFixture(t *testing.T, filename, name string, size int64) {
	t.Helper()
	file, err := os.Create(filename)
	if err != nil {
		t.Fatal(err)
	}
	gzipWriter := gzip.NewWriter(file)
	tarWriter := tar.NewWriter(gzipWriter)
	if err := tarWriter.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Typeflag: tar.TypeReg, Size: size}); err != nil {
		t.Fatal(err)
	}
	// The extractor rejects the declared size before reading file contents, so
	// an intentionally truncated body keeps this boundary test fast and small.
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeOversizedZipFixture(t *testing.T, filename, name string, size uint64) {
	t.Helper()
	file, err := os.Create(filename)
	if err != nil {
		t.Fatal(err)
	}
	zipWriter := zip.NewWriter(file)
	header := &zip.FileHeader{
		Name:               name,
		Method:             zip.Store,
		UncompressedSize64: size,
		CompressedSize64:   0,
	}
	header.SetMode(0o600)
	if _, err := zipWriter.CreateRaw(header); err != nil {
		t.Fatal(err)
	}
	if err := zipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func assertFixtureContents(t *testing.T, filename, want string) {
	t.Helper()
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Fatalf("fixture contents=%q, want %q", data, want)
	}
}
