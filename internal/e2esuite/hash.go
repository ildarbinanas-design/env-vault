// Package e2esuite defines the semantic identity of the black-box E2E suite.
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
	"sort"
	"strings"
)

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
			return fmt.Errorf("suite input must not be a symlink: %s", relative)
		}
		if info.Mode().IsRegular() {
			files = append(files, relative)
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("enumerate E2E suite: %w", err)
	}
	if len(files) == 0 {
		return "", errors.New("E2E suite has no files")
	}
	sort.Strings(files)
	hash := sha256.New()
	for _, relative := range files {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil {
			return "", fmt.Errorf("hash suite file %s: %w", relative, err)
		}
		_, _ = io.WriteString(hash, relative)
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write(canonicalFileBytes(relative, data))
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

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
		trimmed := strings.TrimSpace(string(line))
		if strings.HasPrefix(trimmed, "gotestsumModuleVersion =") || strings.HasPrefix(trimmed, "gotestsumVersion       =") {
			indent := line[:len(line)-len(bytes.TrimLeft(line, " \t"))]
			name := strings.TrimSpace(strings.SplitN(trimmed, "=", 2)[0])
			lines[index] = []byte(fmt.Sprintf("%s%s = \"<REPORTER_PIN>\"", indent, name))
		}
	}
	return bytes.Join(lines, []byte{'\n'})
}
