package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ildarbinanas-design/env-vault/internal/config"
	apperrors "github.com/ildarbinanas-design/env-vault/internal/errors"
	"github.com/ildarbinanas-design/env-vault/internal/platform"
)

func TestInvalidLocalConfigNeverFallsBackToGlobal(t *testing.T) {
	for _, kind := range []string{"dangling symlink", "existing symlink", "symlink loop", "directory", "unreadable file", "unsearchable directory"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("HOME", root)
			t.Setenv("USERPROFILE", root)
			t.Setenv("XDG_CONFIG_HOME", root)
			t.Setenv("APPDATA", root)
			globalPath, err := platform.UserConfigPath()
			if err != nil {
				t.Fatal(err)
			}
			cfg := config.Empty()
			cfg.Profiles["dev"] = config.Profile{}
			if err := config.Save(globalPath, cfg); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(globalPath)
			if err != nil {
				t.Fatal(err)
			}
			work := filepath.Join(root, "work")
			if err := os.Mkdir(work, 0o700); err != nil {
				t.Fatal(err)
			}
			t.Chdir(work)
			local := filepath.Join(work, config.LocalFile)
			switch kind {
			case "dangling symlink", "existing symlink", "symlink loop":
				target := filepath.Join(root, "missing.yaml")
				if kind == "existing symlink" {
					target = globalPath
				}
				if kind == "symlink loop" {
					target = local
				}
				if err := os.Symlink(target, local); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			case "directory":
				if err := os.Mkdir(local, 0o700); err != nil {
					t.Fatal(err)
				}
			case "unreadable file":
				if runtime.GOOS == "windows" {
					t.Skip("Unix permission test")
				}
				if err := os.WriteFile(local, before, 0o000); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(local, 0o600) })
				if _, err := os.ReadFile(local); !os.IsPermission(err) {
					t.Skip("host can read mode 000 file")
				}
			case "unsearchable directory":
				if runtime.GOOS == "windows" {
					t.Skip("Unix permission test")
				}
				if err := os.Chmod(work, 0o000); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(work, 0o700) })
				if _, err := os.Lstat(config.LocalFile); !os.IsPermission(err) {
					t.Skip("host can search mode 000 directory")
				}
			}
			for _, args := range [][]string{
				{"--json", "profile", "add", "dev", "new:TOKEN"},
				{"--json", "profile", "show", "dev"},
				{"--json", "--dry-run", "exec", "dev", "--", os.Args[0]},
			} {
				var stdout, stderr bytes.Buffer
				code := Run(args, strings.NewReader(""), &stdout, &stderr)
				if code != apperrors.ExitConfigInvalid || !strings.Contains(stdout.String(), `"code":"CONFIG_INVALID"`) {
					t.Fatalf("Run(%v): exit=%d stdout=%s stderr=%s", args, code, stdout.String(), stderr.String())
				}
				after, err := os.ReadFile(globalPath)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(before, after) {
					t.Fatal("global config changed after invalid local config")
				}
			}
		})
	}
}

func TestProfileCreateRejectsTrailingYAMLWithoutMutation(t *testing.T) {
	for _, suffix := range []string{"---\nversion: 1\nprofiles: {}\n", "---\n", "---\n[broken", "...\ninvalid trailing content"} {
		t.Run(suffix, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			before := []byte("version: 1\nprofiles: {}\n" + suffix)
			if err := os.WriteFile(path, before, 0o600); err != nil {
				t.Fatal(err)
			}
			for _, dryRun := range []bool{false, true} {
				args := []string{"--json", "--config", path, "profile", "create", "dev"}
				if dryRun {
					args = append(args, "--dry-run")
				}
				var stdout, stderr bytes.Buffer
				code := Run(args, strings.NewReader(""), &stdout, &stderr)
				if code != apperrors.ExitConfigInvalid || !strings.Contains(stdout.String(), `"code":"CONFIG_INVALID"`) {
					t.Fatalf("exit=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
				}
				after, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(before, after) {
					t.Fatal("invalid config changed")
				}
			}
		})
	}
}
