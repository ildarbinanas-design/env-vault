package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ildarbinanas-design/env-vault/internal/config"
	apperrors "github.com/ildarbinanas-design/env-vault/internal/errors"
)

func TestProfileRemoveKeepsUnrelatedMappings(t *testing.T) {
	for _, tc := range []struct {
		selector, env string
		removeIndex   int
	}{
		{"SHARED", "SHARED", 0}, {"SHARED:OTHER", "OTHER", 1},
	} {
		for _, dryRun := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/dry=%v", tc.selector, dryRun), func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "config.yaml")
				mappings := []config.SecretMapping{
					{Name: "different", Env: "SHARED", Required: true},
					{Name: "SHARED", Env: "OTHER", Required: true},
					{Name: "SHARED", Env: "THIRD", Required: true},
					{Name: "keeper", Env: "KEEP", Required: false},
				}
				cfg := config.Empty()
				cfg.Profiles["dev"] = config.Profile{Secrets: mappings}
				if err := config.Save(path, cfg); err != nil {
					t.Fatal(err)
				}
				args := []string{"--json", "--config", path, "profile", "remove", "dev", tc.selector}
				if dryRun {
					args = append(args, "--dry-run")
				}
				var stdout, stderr bytes.Buffer
				if code := Run(args, strings.NewReader(""), &stdout, &stderr); code != 0 {
					t.Fatalf("exit=%d stderr=%s", code, stderr.String())
				}
				var result struct {
					OK   bool `json:"ok"`
					Data struct {
						Env    string `json:"env"`
						DryRun bool   `json:"dry_run"`
					} `json:"data"`
				}
				if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if !result.OK || result.Data.Env != tc.env || result.Data.DryRun != dryRun {
					t.Fatalf("incorrect removal response: %s", stdout.String())
				}
				loaded, err := config.Load(path)
				if err != nil {
					t.Fatal(err)
				}
				want := append([]config.SecretMapping(nil), mappings...)
				if !dryRun {
					want = append(want[:tc.removeIndex], want[tc.removeIndex+1:]...)
				}
				if !reflect.DeepEqual(loaded.Profiles["dev"].Secrets, want) {
					t.Fatal("removal changed unrelated mappings")
				}
			})
		}
	}
}

func TestProfileDryRunReportsPlannedActions(t *testing.T) {
	for _, command := range []string{"create", "add", "remove"} {
		for _, asJSON := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/json=%v", command, asJSON), func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "config.yaml")
				cfg := config.Empty()
				cfg.Profiles["dev"] = config.Profile{Secrets: []config.SecretMapping{{Name: "existing", Env: "EXISTING", Required: true}}}
				if err := config.Save(path, cfg); err != nil {
					t.Fatal(err)
				}
				before, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				args := []string{"--dry-run", "--config", path, "profile", command}
				var want string
				switch command {
				case "create":
					args = append(args, "new")
					want = fmt.Sprintf("dry run: profile new would be created (%s)\n", path)
				case "add":
					args = append(args, "dev", "new:NEW")
					want = "dry run: profile dev would add NEW\n"
				case "remove":
					args = append(args, "dev", "EXISTING")
					want = "dry run: profile dev would remove EXISTING\n"
				}
				if asJSON {
					args = append(args, "--json")
				}
				var stdout, stderr bytes.Buffer
				if code := Run(args, strings.NewReader(""), &stdout, &stderr); code != 0 {
					t.Fatalf("exit=%d stderr=%s", code, stderr.String())
				}
				if asJSON {
					var result struct {
						OK      bool   `json:"ok"`
						Command string `json:"command"`
						Data    struct {
							DryRun bool `json:"dry_run"`
						} `json:"data"`
					}
					if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
						t.Fatal(err)
					}
					if !result.OK || result.Command != "profile_"+command || !result.Data.DryRun {
						t.Fatalf("incorrect dry-run JSON: %s", stdout.String())
					}
				} else if stdout.String() != want {
					t.Fatalf("output=%q, want %q", stdout.String(), want)
				}
				after, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(before, after) {
					t.Fatal("dry-run changed config")
				}
				if _, err := os.Lstat(path + ".lock"); !os.IsNotExist(err) {
					t.Fatalf("dry-run created lock: %v", err)
				}
			})
		}
	}
}

func TestProfileCreateDryRunRejectsUnsafeTargets(t *testing.T) {
	for _, kind := range []string{"existing symlink", "dangling symlink", "directory"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			path, target := filepath.Join(root, "config.yaml"), filepath.Join(root, "target.yaml")
			before := []byte("version: 1\nprofiles: {}\n")
			switch kind {
			case "existing symlink":
				if err := os.WriteFile(target, before, 0o600); err != nil {
					t.Fatal(err)
				}
				fallthrough
			case "dangling symlink":
				if err := os.Symlink(target, path); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			case "directory":
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			assertProfileDryRunInvalidTarget(t, path)
			info, err := os.Lstat(path)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "directory" {
				if !info.IsDir() {
					t.Fatal("directory target changed")
				}
			} else if info.Mode()&os.ModeSymlink == 0 {
				t.Fatal("symlink target changed")
			}
			if kind == "existing symlink" {
				after, err := os.ReadFile(target)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(after, before) {
					t.Fatal("symlink destination changed")
				}
			} else if _, err := os.Lstat(target); !os.IsNotExist(err) {
				t.Fatalf("unexpected destination created: %v", err)
			}
		})
	}
}

func TestProfileCreateDryRunDoesNotCreateParentDirectory(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "missing")
	path := filepath.Join(parent, "config.yaml")
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"--dry-run", "--json", "--config", path, "profile", "create", "dev"}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("exit=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Lstat(parent); !os.IsNotExist(err) {
		t.Fatalf("dry-run created parent: %v", err)
	}
}

func assertProfileDryRunInvalidTarget(t *testing.T, path string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := Run([]string{"--dry-run", "--json", "--config", path, "profile", "create", "dev"}, strings.NewReader(""), &stdout, &stderr)
	var result struct {
		OK    bool `json:"ok"`
		Error *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if code != apperrors.ExitConfigInvalid || result.OK || result.Error == nil || result.Error.Code != apperrors.CodeConfigInvalid {
		t.Errorf("unsafe target: exit=%d response=%s", code, stdout.String())
	}
	if _, err := os.Lstat(path + ".lock"); !os.IsNotExist(err) {
		t.Errorf("dry-run created lock: %v", err)
	}
}
