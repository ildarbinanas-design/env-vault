package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ildarbinanas-design/env-vault/internal/releasearchive"
	"github.com/ildarbinanas-design/env-vault/internal/releasecontract"
)

const maxArtifactCompressedBytes = 128 << 20

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
		binary, evidence, err := verifyAndExtractArtifact(repoRoot, opts.artifact, opts.checksum, filepath.Join(privateDir, "artifact"))
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

func verifyAndExtractArtifact(repoRoot, archivePath, checksumPath, outputDir string) (string, artifactEvidence, error) {
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

	contract, err := releasecontract.LoadCanonical(repoRoot)
	if err != nil {
		return "", evidence, fmt.Errorf("load release contract for artifact extraction: %w", err)
	}
	if err := releasearchive.ExtractArchive(abs, outputDir, contract); err != nil {
		return "", evidence, fmt.Errorf("extract release artifact: %w", err)
	}
	root := wantBase
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
