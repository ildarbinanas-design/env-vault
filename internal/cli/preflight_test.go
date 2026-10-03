package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ildarbinanas-design/env-vault/internal/bundle"
	apperrors "github.com/ildarbinanas-design/env-vault/internal/errors"
	"github.com/ildarbinanas-design/env-vault/internal/output"
	"github.com/ildarbinanas-design/env-vault/internal/platform"
	"github.com/ildarbinanas-design/env-vault/internal/testutil"
)

func TestMetadataNeverReplacesAContainer(t *testing.T) {
	newTransferEnv(t)
	setSecret(t, "preflight/token", testutil.EphemeralValue(t))
	source := filepath.Join(t.TempDir(), "original.evv")
	mustRunCLI(t, "", transferPassphrase, "export", "--out", source)
	raw, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		args     func(string) []string
		existing bool
	}{
		{"export success", func(p string) []string { return []string{"export", "--out", p} }, false},
		{"export existing error", func(p string) []string { return []string{"export", "--out", p} }, true},
		{"export force", func(p string) []string { return []string{"export", "--out", p, "--force"} }, true},
		{"export dry run", func(p string) []string { return []string{"export", "--out", p, "--dry-run"} }, true},
		{"import success", func(p string) []string { return []string{"import", p} }, true},
		{"import bad policy", func(p string) []string { return []string{"import", p, "--on-conflict", "invalid"} }, true},
		{"import dry run", func(p string) []string { return []string{"import", p, "--dry-run"} }, true},
		{"import argument error", func(p string) []string { return []string{"import", p, "extra"} }, true},
		{"import flag error", func(p string) []string { return []string{"import", p, "--unknown"} }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "container.evv")
			if tc.existing {
				if err := os.WriteFile(path, raw, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var stdout, stderr bytes.Buffer
			app := newApp(strings.NewReader(""), &stdout, &stderr)
			app.passphraseReader = func(string) ([]byte, error) {
				t.Fatal("collision reached the passphrase prompt")
				return nil, nil
			}
			args := append([]string{"--json", "--output", path}, tc.args(path)...)
			if code := app.run(args); code != apperrors.ExitUsage {
				t.Fatalf("exit=%d", code)
			}
			var env output.Envelope
			if err := json.Unmarshal(stdout.Bytes(), &env); err != nil || env.Error == nil || env.Error.Code != apperrors.CodeUsage {
				t.Fatal("missing structured usage error")
			}
			after, err := os.ReadFile(path)
			if tc.existing {
				if err != nil || !bytes.Equal(after, raw) {
					t.Fatal("container was changed")
				}
			} else if !os.IsNotExist(err) {
				t.Fatal("preflight created a container or metadata")
			}
		})
	}
}

func TestMetadataProtectsConfigPaths(t *testing.T) {
	setupTestBackend(t)
	t.Chdir(t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("APPDATA", t.TempDir())
	global, err := platform.UserConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{".env-vault.yaml", global, filepath.Join(t.TempDir(), "explicit.yaml")} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		original := []byte("version: 1\nprofiles:\n  dev: {}\n")
		if err := os.WriteFile(path, original, 0o600); err != nil {
			t.Fatal(err)
		}
		for _, args := range [][]string{
			{"profile", "create", "next"},
			{"profile", "create", "next", "--dry-run"},
			{"profile", "show", "missing"},
			{"doctor", "extra"},
			{"version"},
			{"--unknown"},
		} {
			full := append([]string{"--json", "--config", path, "--output", path}, args...)
			var stdout, stderr bytes.Buffer
			if code := Run(full, strings.NewReader(""), &stdout, &stderr); code != 2 {
				t.Fatalf("%v: exit %d", args, code)
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(original, after) {
				t.Fatal("config was changed")
			}
		}
		// The conventional config paths stay protected without --config.
		if path == ".env-vault.yaml" || path == global {
			var stdout, stderr bytes.Buffer
			if code := Run([]string{"version", "--output", path}, strings.NewReader(""), &stdout, &stderr); code != 2 {
				t.Fatalf("default config: exit %d", code)
			}
		}
	}
}

func TestMetadataDetectsPathAliases(t *testing.T) {
	setupTestBackend(t)
	root := t.TempDir()
	t.Chdir(root)
	if err := os.Mkdir("real", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "real"), "alias"); err != nil {
		t.Skipf("directory symlinks unavailable: %v", err)
	}
	for _, existing := range []bool{false, true} {
		path := filepath.Join(root, "real", "container.evv")
		original := []byte("container metadata fixture")
		if existing {
			if err := os.WriteFile(path, original, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		var stdout, stderr bytes.Buffer
		if code := Run([]string{"export", "--out", "real/container.evv", "--output", "alias/container.evv", "--json", "--dry-run"}, strings.NewReader(""), &stdout, &stderr); code != 2 {
			t.Fatalf("exit=%d", code)
		}
		if existing {
			after, _ := os.ReadFile(path)
			if !bytes.Equal(original, after) {
				t.Fatal("aliased container changed")
			}
		} else if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("aliased target created")
		}
	}
	if same, err := sameFilePath("real/../real/container.evv", filepath.Join(root, "real", "container.evv")); err != nil || !same {
		t.Fatal("relative alias missed")
	}
	if err := os.Link("real/container.evv", "hardlink.evv"); err != nil {
		t.Skipf("hardlinks unavailable: %v", err)
	}
	if same, err := sameFilePath("hardlink.evv", "real/container.evv"); err != nil || !same {
		t.Fatal("hardlink alias missed")
	}
}

func TestMetadataDetectsSymlinkBeforeParentTraversal(t *testing.T) {
	setupTestBackend(t)
	root := t.TempDir()
	t.Chdir(root)
	if err := os.MkdirAll("real/deep", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "real", "deep"), "alias"); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	for _, path := range []string{"container.evv", "missing/container.evv"} {
		// filepath.Join would clean away the traversal this test exercises.
		left := "alias/../" + path
		right := "real/" + path
		if same, err := sameFilePath(left, right); err != nil || !same {
			t.Fatalf("symlink parent traversal missed: %v", err)
		}
		var stdout, stderr bytes.Buffer
		if code := Run([]string{"export", "--out", left, "--output", right, "--dry-run", "--json"}, strings.NewReader(""), &stdout, &stderr); code != 2 {
			t.Fatalf("exit=%d", code)
		}
	}
}

func TestMetadataCannotReplaceConfigLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"profile", "create", "dev", "--config", path, "--output", path + ".lock", "--json"}, strings.NewReader(""), &stdout, &stderr); code != 2 {
		t.Fatalf("exit=%d", code)
	}
	for _, candidate := range []string{path, path + ".lock"} {
		if _, err := os.Stat(candidate); !os.IsNotExist(err) {
			t.Fatal("preflight changed config or lock")
		}
	}
}

func TestMetadataDetectsMissingCaseAlias(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("platform does not use conservative case-alias protection")
	}
	setupTestBackend(t)
	root := t.TempDir()
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"export", "--out", filepath.Join(root, "Container.evv"), "--output", filepath.Join(root, "container.evv"), "--dry-run", "--json"}, strings.NewReader(""), &stdout, &stderr); code != 2 {
		t.Fatalf("exit=%d", code)
	}
	if !strings.Contains(stdout.String(), "Metadata output conflicts") {
		t.Fatal("missing case alias was not detected")
	}
}

func TestImportAuthErrorWritesOnlySeparateMetadata(t *testing.T) {
	newTransferEnv(t)
	setSecret(t, "preflight/token", testutil.EphemeralValue(t))
	root := t.TempDir()
	container, metadata := filepath.Join(root, "container.evv"), filepath.Join(root, "metadata.json")
	mustRunCLI(t, "", transferPassphrase, "export", "--out", container)
	before, err := os.ReadFile(container)
	if err != nil {
		t.Fatal(err)
	}
	code, _, _ := runCLI(t, "", testutil.EphemeralValue(t), "import", container, "--output", metadata, "--json")
	if code != apperrors.ExitConfigInvalid {
		t.Fatalf("exit=%d", code)
	}
	after, err := os.ReadFile(container)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("authentication failure changed the container")
	}
	raw, err := os.ReadFile(metadata)
	if err != nil {
		t.Fatal(err)
	}
	var env output.Envelope
	if err := json.Unmarshal(raw, &env); err != nil || env.Error == nil || env.Error.Code != apperrors.CodeBundleAuthFailed {
		t.Fatal("missing authentication error metadata")
	}
}

func TestMetadataRechecksNewFilesystemAliases(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("native filename normalization regression")
	}
	newTransferEnv(t)
	setSecret(t, "preflight/token", testutil.EphemeralValue(t))
	root := t.TempDir()
	out, metadata := filepath.Join(root, "caf\u00e9.evv"), filepath.Join(root, "cafe\u0301.evv")
	if runtime.GOOS == "windows" {
		out, metadata = filepath.Join(root, "container.evv"), filepath.Join(root, "container.evv.")
	}
	// Verify this volume actually aliases these spellings, then remove the
	// probe so the command must handle a newly created file's identity.
	if err := os.WriteFile(out, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	l, le := os.Stat(out)
	r, re := os.Stat(metadata)
	if le != nil || re != nil || !os.SameFile(l, r) {
		t.Skip("volume does not normalize this filename alias")
	}
	if err := os.Remove(out); err != nil {
		t.Fatal(err)
	}
	code, _, _ := runCLI(t, "", transferPassphrase, "export", "--out", out, "--output", metadata, "--json")
	if code != apperrors.ExitUsage {
		t.Fatalf("exit=%d", code)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	// A successful decryption proves metadata did not replace the container.
	payload, err := bundle.Open(raw, []byte(transferPassphrase))
	if err != nil {
		t.Fatal("new filename alias replaced encrypted container")
	}
	for _, entry := range payload.Secrets {
		bundle.Wipe(entry.Value)
	}
}
