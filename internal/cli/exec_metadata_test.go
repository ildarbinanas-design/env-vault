package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	apperrors "github.com/ildarbinanas-design/env-vault/internal/errors"
	"github.com/ildarbinanas-design/env-vault/internal/output"
)

const (
	execMetadataStdout = "exec child stdout\n"
	execMetadataStderr = "exec child stderr\n"
)

func TestExecSuccessPreservesStatusWhenMetadataFails(t *testing.T) {
	setupExecMetadata(t)
	for _, quiet := range []bool{false, true} {
		for _, verbose := range []bool{false, true} {
			t.Run(fmt.Sprintf("quiet=%v/verbose=%v", quiet, verbose), func(t *testing.T) {
				dir := t.TempDir()
				marker := filepath.Join(dir, "started")
				args := []string{"--output", dir}
				if quiet {
					args = append(args, "--quiet")
				}
				if verbose {
					args = append(args, "--verbose")
				}
				args = append(args, execMetadataArgs("streams", marker, "", "")...)
				var stdout, stderr bytes.Buffer
				code := Run(args, strings.NewReader(""), &stdout, &stderr)
				assertExecStartedOnce(t, marker)
				if stdout.String() != execMetadataStdout {
					t.Fatal("child stdout changed")
				}
				if code != 0 {
					t.Fatalf("successful child returned env-vault status %d, want 0", code)
				}
				assertExecMetadataStderr(t, stderr.String(), execMetadataStderr, verbose)
				if info, err := os.Stat(dir); err != nil || !info.IsDir() {
					t.Fatal("metadata failure changed the output directory")
				}
			})
		}
	}
}

func TestExecMetadataConflictStopsChild(t *testing.T) {
	setupExecMetadata(t)
	for _, target := range []string{"config", "lock", "alias"} {
		t.Run(target, func(t *testing.T) {
			dir := t.TempDir()
			cfg := filepath.Join(dir, "config.yaml")
			original := []byte("version: 1\nprofiles: {}\n")
			if err := os.WriteFile(cfg, original, 0o600); err != nil {
				t.Fatal(err)
			}
			path := cfg
			switch target {
			case "lock":
				path += ".lock"
				if err := os.WriteFile(path, original, 0o600); err != nil {
					t.Fatal(err)
				}
			case "alias":
				path = filepath.Join(dir, "alias.json")
				if err := os.Link(cfg, path); err != nil {
					t.Skipf("hard links unavailable: %v", err)
				}
			}
			marker := filepath.Join(dir, "started")
			args := append([]string{"--json", "--config", cfg, "--output", path}, execMetadataArgs("silent", marker, "", "")...)
			var stdout, stderr bytes.Buffer
			code := Run(args, strings.NewReader(""), &stdout, &stderr)
			assertExecMetadataError(t, code, stdout.Bytes(), apperrors.ExitUsage, apperrors.CodeUsage)
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("conflicting output path did not stop the child")
			}
			for _, protected := range []string{cfg, path} {
				data, err := os.ReadFile(protected)
				if err != nil || !bytes.Equal(data, original) {
					t.Fatal("preflight changed a protected file")
				}
			}
		})
	}
}

func TestExecSuccessPreservesAliasCreatedByChild(t *testing.T) {
	setupExecMetadata(t)
	for _, verbose := range []bool{false, true} {
		t.Run(fmt.Sprintf("verbose=%v", verbose), func(t *testing.T) {
			dir := t.TempDir()
			cfg, path := filepath.Join(dir, "config.yaml"), filepath.Join(dir, "metadata.json")
			original := []byte("version: 1\nprofiles: {}\n")
			if err := os.WriteFile(cfg, original, 0o600); err != nil {
				t.Fatal(err)
			}
			// Check hard-link support, then remove the alias before preflight.
			if err := os.Link(cfg, path); err != nil {
				t.Skipf("hard links unavailable: %v", err)
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(dir, "started")
			args := []string{"--quiet", "--config", cfg, "--output", path}
			if verbose {
				args = append(args, "--verbose")
			}
			args = append(args, execMetadataArgs("link", marker, cfg, path)...)
			var stdout, stderr bytes.Buffer
			if code := Run(args, strings.NewReader(""), &stdout, &stderr); code != 0 {
				t.Fatalf("post-exec guard changed successful child status to %d", code)
			}
			assertExecStartedOnce(t, marker)
			assertExecMetadataStderr(t, stderr.String(), "", verbose)
			if stdout.Len() != 0 {
				t.Fatal("guard failure added stdout")
			}
			for _, protected := range []string{cfg, path} {
				data, err := os.ReadFile(protected)
				if err != nil || !bytes.Equal(data, original) {
					t.Fatal("post-exec guard changed protected bytes")
				}
			}
			configInfo, configErr := os.Stat(cfg)
			outputInfo, outputErr := os.Stat(path)
			if configErr != nil || outputErr != nil || !os.SameFile(configInfo, outputInfo) {
				t.Fatal("post-exec guard replaced the child's file alias")
			}
		})
	}
}

func TestExecMetadataFailurePreservesNonzeroChildStatus(t *testing.T) {
	setupExecMetadata(t)
	for _, verbose := range []bool{false, true} {
		t.Run(fmt.Sprintf("verbose=%v", verbose), func(t *testing.T) {
			dir := t.TempDir()
			marker := filepath.Join(dir, "started")
			args := []string{"--json", "--output", dir}
			if verbose {
				args = append(args, "--verbose")
			}
			args = append(args, execMetadataArgs("exit7", marker, "", "")...)
			var stdout, stderr bytes.Buffer
			if code := Run(args, strings.NewReader(""), &stdout, &stderr); code != 7 {
				t.Fatalf("child status changed to %d, want 7", code)
			}
			assertExecStartedOnce(t, marker)
			if stdout.String() != execMetadataStdout {
				t.Fatal("failed child stdout changed or includes an envelope")
			}
			assertExecMetadataStderr(t, stderr.String(), execMetadataStderr, verbose)
		})
	}
}

func TestExecMetadataOtherErrorsRemainFailures(t *testing.T) {
	setupExecMetadata(t)
	for _, kind := range []string{"version", "dry-run", "missing-executable"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			marker := filepath.Join(dir, "started")
			args := []string{"--json", "--output", dir}
			wantCode, wantError := apperrors.ExitRuntimeError, apperrors.CodeRuntimeError
			switch kind {
			case "version":
				args = append(args, "version")
			case "dry-run":
				args = append(args, "--dry-run")
				args = append(args, execMetadataArgs("silent", marker, "", "")...)
			case "missing-executable":
				args = append(args, "exec", "--", filepath.Join(dir, "missing-executable"))
				wantCode, wantError = apperrors.ExitCommandNotFound, apperrors.CodeCommandNotFound
			}
			var stdout, stderr bytes.Buffer
			code := Run(args, strings.NewReader(""), &stdout, &stderr)
			assertExecMetadataError(t, code, stdout.Bytes(), wantCode, wantError)
			if stderr.Len() != 0 {
				t.Fatal("non-verbose machine error added stderr")
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("child ran before an error or during dry-run")
			}
		})
	}
}

type execMetadataFailingWriter struct{ err error }

func (w execMetadataFailingWriter) Write([]byte) (int, error) { return 0, w.err }

func TestExecSuccessDoesNotSuppressStdoutErrors(t *testing.T) {
	setupExecMetadata(t)
	for _, mode := range []string{"--json", "--jsonl"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			marker, path := filepath.Join(dir, "started"), filepath.Join(dir, "metadata.json")
			var stderr bytes.Buffer
			stdout := execMetadataFailingWriter{errors.New("stdout unavailable")}
			args := append([]string{mode, "--output", path}, execMetadataArgs("silent", marker, "", "")...)
			code := Run(args, strings.NewReader(""), stdout, &stderr)
			assertExecStartedOnce(t, marker)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			// The existing error renderer records RUNTIME_ERROR after the
			// success envelope could not be written to stdout.
			assertExecMetadataError(t, code, data, apperrors.ExitRuntimeError, apperrors.CodeRuntimeError)
		})
	}
}

func assertExecMetadataError(t *testing.T, code int, data []byte, wantCode int, wantError string) {
	t.Helper()
	if code != wantCode {
		t.Fatalf("status=%d, want %d", code, wantCode)
	}
	var env output.Envelope
	if err := json.Unmarshal(data, &env); err != nil || env.OK || env.Error == nil || env.Error.Code != wantError {
		t.Fatalf("expected structured %s", wantError)
	}
}

func setupExecMetadata(t *testing.T) {
	t.Helper()
	setupTestBackend(t)
	t.Chdir(t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("APPDATA", t.TempDir())
}

func execMetadataArgs(mode, marker, source, output string) []string {
	return []string{"exec", "--", os.Args[0], "-test.run=^TestExecMetadataChild$", "--", mode, marker, source, output}
}

func assertExecStartedOnce(t *testing.T, marker string) {
	t.Helper()
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != "started\n" {
		t.Fatal("child did not run exactly once")
	}
}

func assertExecMetadataStderr(t *testing.T, got, child string, verbose bool) {
	t.Helper()
	if !strings.HasPrefix(got, child) {
		t.Fatal("child stderr changed")
	}
	diagnostic := strings.TrimPrefix(got, child)
	if !verbose {
		if diagnostic != "" {
			t.Fatal("metadata failure added non-verbose diagnostics")
		}
		return
	}
	if !strings.HasPrefix(diagnostic, "OUTPUT_WRITE_FAILED: ") || strings.Count(diagnostic, "OUTPUT_WRITE_FAILED:") != 1 || strings.Contains(diagnostic, "RUNTIME_ERROR") {
		t.Fatal("expected only one verbose metadata diagnostic")
	}
}

// TestExecMetadataChild is a portable, secret-free child process. Appending a
// marker makes a repeated launch observable instead of overwriting its evidence.
func TestExecMetadataChild(t *testing.T) {
	for i, arg := range os.Args {
		if arg != "--" || i+4 >= len(os.Args) {
			continue
		}
		mode, marker, source, output := os.Args[i+1], os.Args[i+2], os.Args[i+3], os.Args[i+4]
		file, err := os.OpenFile(marker, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			os.Exit(97)
		}
		_, err = file.WriteString("started\n")
		closeErr := file.Close()
		if err != nil || closeErr != nil {
			os.Exit(97)
		}
		switch mode {
		case "streams", "exit7":
			fmt.Fprint(os.Stdout, execMetadataStdout)
			fmt.Fprint(os.Stderr, execMetadataStderr)
		case "link":
			if err := os.Link(source, output); err != nil {
				os.Exit(98)
			}
		case "silent":
		default:
			os.Exit(99)
		}
		if mode == "exit7" {
			os.Exit(7)
		}
		os.Exit(0)
	}
}
