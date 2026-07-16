// Package e2esuite defines a stable semantic identity for the black-box E2E
// suite. Its scope deliberately includes the semantic runner, normalization,
// and report validation code: those programs decide what a passing report
// means and therefore remain part of the release guarantee.
package e2esuite

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
)

const (
	// SchemaID is domain-separated into every hash. Changing the input rules
	// requires a new schema instead of silently changing an existing digest.
	SchemaID = "env-vault.e2e-semantic-suite.v1"
	Version  = 1
)

var reporterPinLine = regexp.MustCompile(`^([ \t]*(?:gotestsumModuleVersion|gotestsumVersion)[ \t]*=[ \t]*)"[^"\r\n]*"([ \t]*(?://[^\r\n]*)?)$`)

// Hash returns the semantic suite hash for repositoryRoot. The input is every
// regular file under e2e except generated reports:
//
//   - e2e/reports contains generated output and is never source input.
//
// Test implementations, scenario declarations, platform helpers, the test
// helper, execution, normalization, renderers, and validators remain covered.
// Only the two reporter-pin string values in tooling.go are canonicalized;
// executable code cannot hide beside them. Paths and LF-normalized bytes are
// hashed in sorted order so the result is portable across native runners.
func Hash(repositoryRoot string) (string, error) {
	root := filepath.Join(repositoryRoot, "e2e")
	var files []string
	err := filepath.WalkDir(root, func(filename string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, filename)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if entry.IsDir() {
			if relative == "reports" {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("semantic suite input must not be a symlink: %s", relative)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("semantic suite input must be a regular file: %s", relative)
		}
		files = append(files, relative)
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("enumerate semantic E2E suite: %w", err)
	}
	if len(files) == 0 {
		return "", errors.New("semantic E2E suite has no files")
	}
	sort.Strings(files)
	hash := sha256.New()
	_, _ = io.WriteString(hash, SchemaID)
	_, _ = hash.Write([]byte{0})
	for _, relative := range files {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil {
			return "", fmt.Errorf("hash semantic suite file %s: %w", relative, err)
		}
		_, _ = io.WriteString(hash, relative)
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write(canonicalFileBytes(relative, data))
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// CanonicalBytes normalizes Git's platform-dependent text materialization.
func CanonicalBytes(data []byte) []byte {
	return bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
}

func canonicalFileBytes(relative string, data []byte) []byte {
	data = CanonicalBytes(data)
	if relative != "cmd/e2e-runner/tooling.go" {
		return data
	}
	lines := bytes.Split(data, []byte{'\n'})
	for index, line := range lines {
		lines[index] = reporterPinLine.ReplaceAll(line, []byte(`${1}"<REPORTER_PIN>"${2}`))
	}
	return bytes.Join(lines, []byte{'\n'})
}
