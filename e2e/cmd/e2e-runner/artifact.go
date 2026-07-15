package main

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	maxArtifactCompressedBytes = 128 << 20
	maxArtifactEntries         = 64
	maxArtifactFileBytes       = 128 << 20
	maxArtifactTotalBytes      = 256 << 20
)

type artifactEvidence struct {
	Path             string `json:"path,omitempty"`
	ChecksumPath     string `json:"checksum_path,omitempty"`
	SHA256           string `json:"sha256,omitempty"`
	ChecksumVerified bool   `json:"checksum_verified"`
	Format           string `json:"format,omitempty"`
}

func prepareSubjectBinary(repoRoot, privateDir string, opts runOptions) (string, artifactEvidence, commandResult, error) {
	if opts.binary != "" {
		binary, err := requireRegularBinary(opts.binary)
		return binary, artifactEvidence{}, commandResult{Name: "prebuilt-binary", ExitCode: boolExitCode(err == nil)}, err
	}
	if opts.artifact != "" {
		binary, evidence, err := verifyAndExtractArtifact(opts.artifact, opts.checksum, filepath.Join(privateDir, "artifact"))
		return binary, evidence, commandResult{Name: "verify-release-artifact", ExitCode: boolExitCode(err == nil)}, err
	}

	binaryName := "env-vault"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	binary := filepath.Join(privateDir, "bin", binaryName)
	result := runCommand(commandSpec{
		name:       "go",
		args:       []string{"build", "-trimpath", "-o", binary, "./cmd/env-vault"},
		dir:        repoRoot,
		env:        environment(nil),
		timeout:    opts.commandTimeout,
		stdoutPath: filepath.Join(privateDir, "release-build.stdout"),
		stderrPath: filepath.Join(privateDir, "release-build.stderr"),
	})
	if result.ExitCode != 0 {
		return "", artifactEvidence{}, result, fmt.Errorf("release-like build failed with exit code %d", result.ExitCode)
	}
	binary, err := requireRegularBinary(binary)
	return binary, artifactEvidence{}, result, err
}

func boolExitCode(ok bool) int {
	if ok {
		return 0
	}
	return 1
}

func requireRegularBinary(filename string) (string, error) {
	abs, err := filepath.Abs(filename)
	if err != nil {
		return "", fmt.Errorf("resolve binary path: %w", err)
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return "", fmt.Errorf("inspect binary: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", fmt.Errorf("binary must be a regular non-symlink file: %s", abs)
	}
	if info.Size() <= 0 {
		return "", fmt.Errorf("binary is empty: %s", abs)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		return "", fmt.Errorf("binary is not executable: %s", abs)
	}
	return abs, nil
}

func verifyAndExtractArtifact(archivePath, checksumPath, outputDir string) (string, artifactEvidence, error) {
	abs, err := filepath.Abs(archivePath)
	if err != nil {
		return "", artifactEvidence{}, fmt.Errorf("resolve artifact path: %w", err)
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return "", artifactEvidence{}, fmt.Errorf("inspect artifact: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", artifactEvidence{}, fmt.Errorf("artifact must be a regular non-symlink file: %s", abs)
	}
	if info.Size() <= 0 || info.Size() > maxArtifactCompressedBytes {
		return "", artifactEvidence{}, fmt.Errorf("artifact compressed size %d is outside the allowed range", info.Size())
	}

	base := filepath.Base(abs)
	wantBase := "env-vault-" + expectedGOOS() + "-" + expectedGOARCH()
	format := ""
	switch {
	case base == wantBase+".tar.gz":
		format = "tar.gz"
	case base == wantBase+".zip":
		format = "zip"
	default:
		return "", artifactEvidence{}, fmt.Errorf("artifact %q does not match native platform %s-%s", base, expectedGOOS(), expectedGOARCH())
	}
	if expectedGOOS() == "windows" && format != "zip" {
		return "", artifactEvidence{}, errors.New("Windows release artifact must be zip")
	}
	if expectedGOOS() != "windows" && format != "tar.gz" {
		return "", artifactEvidence{}, errors.New("non-Windows release artifact must be tar.gz")
	}

	digest, err := sha256File(abs)
	if err != nil {
		return "", artifactEvidence{}, err
	}
	evidence := artifactEvidence{Path: filepath.ToSlash(abs), SHA256: digest, Format: format}
	if checksumPath == "" {
		candidate := abs + ".sha256"
		if _, statErr := os.Lstat(candidate); statErr == nil {
			checksumPath = candidate
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return "", evidence, fmt.Errorf("inspect checksum sidecar: %w", statErr)
		}
	}
	if checksumPath != "" {
		checksumAbs, err := filepath.Abs(checksumPath)
		if err != nil {
			return "", evidence, fmt.Errorf("resolve checksum path: %w", err)
		}
		if err := verifyChecksumSidecar(checksumAbs, base, digest); err != nil {
			return "", evidence, err
		}
		evidence.ChecksumPath = filepath.ToSlash(checksumAbs)
		evidence.ChecksumVerified = true
	}

	if err := os.MkdirAll(outputDir, 0o700); err != nil {
		return "", evidence, fmt.Errorf("create artifact extraction directory: %w", err)
	}
	root := wantBase
	if format == "zip" {
		err = extractZipArtifact(abs, outputDir, root)
	} else {
		err = extractTarGzArtifact(abs, outputDir, root)
	}
	if err != nil {
		return "", evidence, err
	}
	binaryName := "env-vault"
	if expectedGOOS() == "windows" {
		binaryName += ".exe"
	}
	binary := filepath.Join(outputDir, root, binaryName)
	if expectedGOOS() != "windows" {
		if err := os.Chmod(binary, 0o700); err != nil {
			return "", evidence, fmt.Errorf("make extracted binary executable: %w", err)
		}
	}
	verified, err := requireRegularBinary(binary)
	if err != nil {
		return "", evidence, fmt.Errorf("verify extracted artifact binary: %w", err)
	}
	return verified, evidence, nil
}

func expectedGOOS() string {
	return firstNonEmpty(os.Getenv("ENV_VAULT_E2E_GOOS"), runtime.GOOS)
}

func expectedGOARCH() string {
	return firstNonEmpty(os.Getenv("ENV_VAULT_E2E_GOARCH"), runtime.GOARCH)
}

func verifyChecksumSidecar(filename, artifactBase, actual string) error {
	info, err := os.Lstat(filename)
	if err != nil {
		return fmt.Errorf("inspect checksum sidecar: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > 4096 {
		return fmt.Errorf("checksum sidecar must be a small regular non-symlink file: %s", filename)
	}
	data, err := os.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("read checksum sidecar: %w", err)
	}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	var records []string
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			records = append(records, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan checksum sidecar: %w", err)
	}
	if len(records) != 1 {
		return fmt.Errorf("checksum sidecar must contain exactly one non-empty record")
	}
	fields := strings.Fields(records[0])
	if len(fields) != 2 || len(fields[0]) != sha256.Size*2 {
		return fmt.Errorf("checksum sidecar has an invalid record")
	}
	if _, err := hex.DecodeString(fields[0]); err != nil {
		return fmt.Errorf("checksum sidecar has an invalid SHA-256")
	}
	named := strings.TrimPrefix(fields[1], "*")
	if named != artifactBase || !strings.EqualFold(fields[0], actual) {
		return fmt.Errorf("artifact checksum verification failed")
	}
	return nil
}

func sha256File(filename string) (string, error) {
	file, err := os.Open(filename)
	if err != nil {
		return "", fmt.Errorf("open %s for hashing: %w", filename, err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("hash %s: %w", filename, err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func extractTarGzArtifact(filename, outputDir, root string) error {
	file, err := os.Open(filename)
	if err != nil {
		return fmt.Errorf("open tar.gz artifact: %w", err)
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("open gzip stream: %w", err)
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	entries := 0
	var total int64
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar entry: %w", err)
		}
		// Keep this source-local guard in addition to safeArchiveName's
		// canonical checks. It conservatively rejects every traversal-like
		// spelling and makes the archive-to-filesystem boundary explicit to
		// static data-flow analysis before the entry reaches any path helper.
		if strings.Contains(header.Name, "..") {
			return fmt.Errorf("artifact entry %q contains a forbidden double-dot sequence", header.Name)
		}
		entries++
		if entries > maxArtifactEntries {
			return fmt.Errorf("artifact contains more than %d entries", maxArtifactEntries)
		}
		name, err := safeArchiveName(header.Name, root)
		if err != nil {
			return err
		}
		target := filepath.Join(outputDir, filepath.FromSlash(name))
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o700); err != nil {
				return fmt.Errorf("create artifact directory: %w", err)
			}
		case tar.TypeReg, tar.TypeRegA:
			if header.Size < 0 || header.Size > maxArtifactFileBytes || total+header.Size > maxArtifactTotalBytes {
				return fmt.Errorf("artifact entry %q exceeds extraction limits", name)
			}
			total += header.Size
			if err := extractRegularFile(target, io.LimitReader(reader, header.Size), header.Size); err != nil {
				return err
			}
		default:
			return fmt.Errorf("artifact entry %q has forbidden type %d", name, header.Typeflag)
		}
	}
	return nil
}

func extractZipArtifact(filename, outputDir, root string) error {
	reader, err := zip.OpenReader(filename)
	if err != nil {
		return fmt.Errorf("open zip artifact: %w", err)
	}
	defer reader.Close()
	if len(reader.File) > maxArtifactEntries {
		return fmt.Errorf("artifact contains more than %d entries", maxArtifactEntries)
	}
	var total int64
	for _, entry := range reader.File {
		// See the equivalent tar guard above. This must remain before the entry
		// name is propagated to any filesystem helper.
		if strings.Contains(entry.Name, "..") {
			return fmt.Errorf("artifact entry %q contains a forbidden double-dot sequence", entry.Name)
		}
		name, err := safeArchiveName(entry.Name, root)
		if err != nil {
			return err
		}
		if entry.Mode()&os.ModeSymlink != 0 || (!entry.FileInfo().IsDir() && !entry.Mode().IsRegular()) {
			return fmt.Errorf("artifact entry %q is not a regular file or directory", name)
		}
		target := filepath.Join(outputDir, filepath.FromSlash(name))
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o700); err != nil {
				return fmt.Errorf("create artifact directory: %w", err)
			}
			continue
		}
		size := int64(entry.UncompressedSize64)
		if size < 0 || size > maxArtifactFileBytes || total+size > maxArtifactTotalBytes {
			return fmt.Errorf("artifact entry %q exceeds extraction limits", name)
		}
		total += size
		source, err := entry.Open()
		if err != nil {
			return fmt.Errorf("open zip entry %q: %w", name, err)
		}
		extractErr := extractRegularFile(target, io.LimitReader(source, size), size)
		closeErr := source.Close()
		if extractErr != nil {
			return extractErr
		}
		if closeErr != nil {
			return fmt.Errorf("close zip entry %q: %w", name, closeErr)
		}
	}
	return nil
}

func safeArchiveName(name, root string) (string, error) {
	if strings.Contains(name, "..") || strings.Contains(name, "\\") || strings.ContainsRune(name, '\x00') || path.IsAbs(name) {
		return "", fmt.Errorf("artifact entry has unsafe path %q", name)
	}
	cleaned := path.Clean(name)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("artifact entry has unsafe path %q", name)
	}
	if cleaned != root && !strings.HasPrefix(cleaned, root+"/") {
		return "", fmt.Errorf("artifact entry %q is outside expected root %q", name, root)
	}
	return cleaned, nil
}

func extractRegularFile(filename string, source io.Reader, size int64) error {
	if err := os.MkdirAll(filepath.Dir(filename), 0o700); err != nil {
		return fmt.Errorf("create artifact parent directory: %w", err)
	}
	file, err := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create artifact file: %w", err)
	}
	written, copyErr := io.Copy(file, source)
	closeErr := file.Close()
	if copyErr != nil {
		return fmt.Errorf("extract artifact file: %w", copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close artifact file: %w", closeErr)
	}
	if written != size {
		return fmt.Errorf("artifact file size mismatch: wrote %d, expected %d", written, size)
	}
	return nil
}
