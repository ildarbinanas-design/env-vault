package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
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

	repositoryRoot, err := findRepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	binary, evidence, err := verifyAndExtractArtifact(repositoryRoot, archive, "", filepath.Join(directory, "output"))
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
