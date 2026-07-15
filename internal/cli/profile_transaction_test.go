package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofrs/flock"

	"github.com/ildarbinanas-design/env-vault/internal/config"
	apperrors "github.com/ildarbinanas-design/env-vault/internal/errors"
)

func TestProfileMutationsUsePersistentTransactionLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	for _, args := range [][]string{
		{"--json", "--config", path, "profile", "create", "dev"},
		{"--json", "--config", path, "profile", "add", "dev", "team/token:TEAM_TOKEN"},
		{"--json", "--config", path, "profile", "remove", "dev", "TEAM_TOKEN"},
	} {
		var stdout, stderr bytes.Buffer
		if code := Run(args, strings.NewReader(""), &stdout, &stderr); code != 0 {
			t.Fatalf("Run(%v) code=%d stdout=%s stderr=%s", args, code, stdout.String(), stderr.String())
		}
	}

	lockInfo, err := os.Lstat(path + ".lock")
	if err != nil {
		t.Fatalf("persistent transaction lock: %v", err)
	}
	if !lockInfo.Mode().IsRegular() {
		t.Fatalf("lock mode=%v, want regular", lockInfo.Mode())
	}
	if runtime.GOOS != "windows" && lockInfo.Mode().Perm() != 0o600 {
		t.Fatalf("lock permissions=%#o, want 0600", lockInfo.Mode().Perm())
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := len(loaded.Profiles["dev"].Secrets); got != 0 {
		t.Fatalf("profile remove did not persist: %#v", loaded.Profiles["dev"].Secrets)
	}
}

func TestConcurrentProfileAddsRetainEveryMapping(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := config.Empty()
	cfg.Profiles["dev"] = config.Profile{}
	if err := config.Save(path, cfg); err != nil {
		t.Fatalf("initial Save: %v", err)
	}

	const workers = 12
	start := make(chan struct{})
	errors := make(chan error, workers)
	var wait sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		worker := worker
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			mapping := fmt.Sprintf("secret-%02d:TOKEN_%02d", worker, worker)
			var stdout, stderr bytes.Buffer
			code := Run([]string{"--json", "--config", path, "profile", "add", "dev", mapping}, strings.NewReader(""), &stdout, &stderr)
			if code != 0 {
				errors <- fmt.Errorf("worker %d code=%d stdout=%s stderr=%s", worker, code, stdout.String(), stderr.String())
			}
		}()
	}
	close(start)
	wait.Wait()
	close(errors)
	for err := range errors {
		t.Fatal(err)
	}

	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("final Load: %v", err)
	}
	profile := loaded.Profiles["dev"]
	if len(profile.Secrets) != workers {
		t.Fatalf("lost concurrent profile update: got %d mappings, want %d: %#v", len(profile.Secrets), workers, profile.Secrets)
	}
	seen := make(map[string]bool, workers)
	for _, mapping := range profile.Secrets {
		seen[mapping.Env] = true
	}
	for worker := 0; worker < workers; worker++ {
		env := fmt.Sprintf("TOKEN_%02d", worker)
		if !seen[env] {
			t.Fatalf("lost mapping %s: %#v", env, profile.Secrets)
		}
	}
}

func TestProfileDryRunDoesNotCreateConfigLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	var stdout, stderr bytes.Buffer
	code := Run([]string{"--dry-run", "--json", "--config", path, "profile", "create", "dev"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	for _, candidate := range []string{path, path + ".lock"} {
		if _, err := os.Lstat(candidate); !os.IsNotExist(err) {
			t.Fatalf("dry-run created %s: %v", candidate, err)
		}
	}
}

func TestProfileCheckSecretFinishesBeforeConfigTransaction(t *testing.T) {
	setupTestBackend(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := config.Empty()
	cfg.Profiles["dev"] = config.Profile{}
	if err := config.Save(path, cfg); err != nil {
		t.Fatalf("initial Save: %v", err)
	}

	holder := flock.New(path+".lock", flock.SetPermissions(0o600))
	locked, err := holder.TryLock()
	if err != nil || !locked {
		t.Fatalf("hold config lock: locked=%v err=%v", locked, err)
	}
	t.Cleanup(func() { _ = holder.Close() })

	var stdout, stderr bytes.Buffer
	started := time.Now()
	code := Run([]string{"--json", "--config", path, "profile", "add", "dev", "missing:TOKEN", "--check-secret"}, strings.NewReader(""), &stdout, &stderr)
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("backend check waited for config transaction lock: %s", elapsed)
	}
	if code != apperrors.ExitMissingSecret {
		t.Fatalf("code=%d, want missing-secret=%d stdout=%s stderr=%s", code, apperrors.ExitMissingSecret, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), `"code":"MISSING_SECRET"`) {
		t.Fatalf("missing structured backend result: %s", stdout.String())
	}
}
