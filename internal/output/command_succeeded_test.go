package output

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCommandSucceededPreservesOutputContract(t *testing.T) {
	t.Parallel()
	for _, format := range []string{"human", "json", "jsonl"} {
		for _, quiet := range []bool{false, true} {
			for _, verbose := range []bool{false, true} {
				for _, target := range []string{"writable", "directory", "guard"} {
					name := fmt.Sprintf("%s/quiet=%v/verbose=%v/%s", format, quiet, verbose, target)
					t.Run(name, func(t *testing.T) {
						t.Parallel()
						path := filepath.Join(t.TempDir(), "meta.json")
						previous := []byte("previous public metadata\n")
						if target == "directory" {
							if err := os.Mkdir(path, 0o700); err != nil {
								t.Fatal(err)
							}
						} else if err := os.WriteFile(path, previous, 0o600); err != nil {
							t.Fatal(err)
						}
						before := statOpenFile(t, path)
						guardCalls := 0
						var stdout, stderr bytes.Buffer
						renderer := New(&stdout, &stderr, Options{
							JSON:       format == "json",
							JSONL:      format == "jsonl",
							Quiet:      quiet,
							Verbose:    verbose,
							OutputPath: path,
							ValidateOutputPath: func() error {
								guardCalls++
								if target == "guard" {
									return errors.New("protected output path")
								}
								return nil
							},
						})
						renderer.now = func() time.Time {
							return time.Date(2026, time.October, 4, 12, 0, 0, 0, time.UTC)
						}
						data := map[string]any{"dry_run": false}
						if err := renderer.CommandSucceeded("exec", data, nil); err != nil {
							t.Fatalf("command succeeded: %v", err)
						}
						if guardCalls != 1 {
							t.Fatalf("publication guard called %d times, want 1", guardCalls)
						}

						// Exact bytes protect all envelope fields and one-line framing,
						// including empty warnings and the absence of exit_code: 0.
						wantEnvelope := "{\"ok\":true,\"command\":\"exec\",\"timestamp\":\"2026-10-04T12:00:00Z\",\"data\":{\"dry_run\":false},\"warnings\":[],\"error\":null}\n"
						wantStdout := wantEnvelope
						if format == "human" {
							wantStdout = ""
						}
						if stdout.String() != wantStdout {
							t.Fatalf("stdout = %q, want %q", stdout.String(), wantStdout)
						}
						wantStderr := ""
						if verbose && target == "directory" {
							wantStderr = "OUTPUT_WRITE_FAILED: path is not a regular file\n"
						} else if verbose && target == "guard" {
							wantStderr = "OUTPUT_WRITE_FAILED: protected output path\n"
						}
						if stderr.String() != wantStderr {
							t.Fatalf("stderr = %q, want %q", stderr.String(), wantStderr)
						}

						after := statOpenFile(t, path)
						if target != "writable" && !os.SameFile(before, after) {
							t.Fatal("failed publication replaced the original target")
						}
						if target == "directory" {
							if !after.IsDir() {
								t.Fatal("output directory was replaced")
							}
							return
						}
						got, err := os.ReadFile(path)
						if err != nil {
							t.Fatal(err)
						}
						wantFile := []byte(wantEnvelope)
						if target == "guard" {
							wantFile = previous
						}
						if !bytes.Equal(got, wantFile) {
							t.Fatal("output file content differs from the expected metadata")
						}
					})
				}
			}
		}
	}
}

func TestCommandSucceededReturnsStdoutError(t *testing.T) {
	t.Parallel()
	for _, jsonl := range []bool{false, true} {
		for _, fileFails := range []bool{false, true} {
			t.Run(fmt.Sprintf("jsonl=%v/file-fails=%v", jsonl, fileFails), func(t *testing.T) {
				t.Parallel()
				stdoutErr := errors.New("stdout unavailable")
				stdout := &commandSucceededFailWriter{err: stdoutErr}
				path := filepath.Join(t.TempDir(), "meta.json")
				if fileFails {
					path = t.TempDir()
				}
				guardCalls := 0
				renderer := New(stdout, &bytes.Buffer{}, Options{
					JSON:       !jsonl,
					JSONL:      jsonl,
					Quiet:      true,
					Verbose:    true,
					OutputPath: path,
					ValidateOutputPath: func() error {
						guardCalls++
						return nil
					},
				})
				if err := renderer.CommandSucceeded("exec", map[string]any{"dry_run": false}, nil); !errors.Is(err, stdoutErr) {
					t.Fatalf("error = %v, want stdout failure", err)
				}
				if stdout.calls != 1 || guardCalls != 1 {
					t.Fatalf("stdout writes=%d publication guards=%d, want 1 each", stdout.calls, guardCalls)
				}
			})
		}
	}
}

func TestCommandSucceededIgnoresDiagnosticWriteError(t *testing.T) {
	t.Parallel()
	var stdout bytes.Buffer
	stderr := &commandSucceededFailWriter{err: errors.New("stderr unavailable")}
	renderer := New(&stdout, stderr, Options{JSON: true, Verbose: true, OutputPath: t.TempDir()})
	if err := renderer.CommandSucceeded("exec", nil, nil); err != nil {
		t.Fatalf("diagnostic write changed success: %v", err)
	}
	if stderr.calls != 1 || stdout.Len() == 0 {
		t.Fatalf("diagnostic writes=%d stdout bytes=%d, want one diagnostic and success output", stderr.calls, stdout.Len())
	}
}

func TestSuccessStillReturnsPublicationError(t *testing.T) {
	t.Parallel()
	for _, command := range []string{"version", "exec"} {
		t.Run(command, func(t *testing.T) {
			t.Parallel()
			guardErr := errors.New("protected output path")
			guardCalls := 0
			var stdout, stderr bytes.Buffer
			renderer := New(&stdout, &stderr, Options{
				JSON: true, Verbose: true, OutputPath: filepath.Join(t.TempDir(), "meta.json"),
				ValidateOutputPath: func() error {
					guardCalls++
					return guardErr
				},
			})
			data := map[string]any{"version": "test", "dry_run": true}
			if err := renderer.Success(command, data, nil); !errors.Is(err, guardErr) {
				t.Fatalf("error = %v, want publication guard failure", err)
			}
			if guardCalls != 1 || stdout.Len() != 0 || stderr.Len() != 0 {
				t.Fatalf("guard calls=%d stdout bytes=%d stderr bytes=%d, want one guard and no output", guardCalls, stdout.Len(), stderr.Len())
			}
		})
	}
}

type commandSucceededFailWriter struct {
	err   error
	calls int
}

func (w *commandSucceededFailWriter) Write([]byte) (int, error) {
	w.calls++
	return 0, w.err
}
