package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ildarbinanas-design/env-vault/internal/output"
	"github.com/ildarbinanas-design/env-vault/internal/secretstore"
	"github.com/ildarbinanas-design/env-vault/internal/testutil"
)

// verifyFailureStore reports success without storing the input. Its read-back
// models a backend whose reported write cannot be verified.
type verifyFailureStore struct {
	secretstore.Store
	service, name string
	written       []byte
	existed       bool
	readValue     []byte
	readErr       error
	calls         []string
	argsMatch     bool
	valueMatches  bool
}

func (s *verifyFailureStore) record(call, service, name string) {
	s.calls = append(s.calls, call)
	s.argsMatch = s.argsMatch && service == s.service && name == s.name
}

func (s *verifyFailureStore) Exists(_ context.Context, service, name string) (bool, error) {
	s.record("exists", service, name)
	return s.existed, nil
}

func (s *verifyFailureStore) Set(_ context.Context, service, name string, value []byte) error {
	s.record("set", service, name)
	s.valueMatches = s.valueMatches && bytes.Equal(value, s.written)
	return nil
}

func (s *verifyFailureStore) Get(_ context.Context, service, name string) ([]byte, error) {
	s.record("get", service, name)
	// Verification clears the returned buffer, so keep the fixture separate.
	return append([]byte(nil), s.readValue...), s.readErr
}

func (s *verifyFailureStore) Delete(_ context.Context, service, name string) error {
	s.record("delete", service, name)
	return nil
}

func (s *verifyFailureStore) List(_ context.Context, service string) ([]secretstore.Metadata, error) {
	s.record("list", service, s.name)
	return nil, nil
}

func TestSecretSetVerifyFailureReportsActionWithoutLeakingValues(t *testing.T) {
	states := []struct {
		action  string
		existed bool
	}{
		{action: "created"},
		{action: "overwritten", existed: true},
	}
	outcomes := []struct {
		name, code, suffix, remediation string
		exit                            int
		readErr                         error
	}{
		{
			name: "mismatch", code: "SECRET_UNVERIFIED", exit: 1,
			suffix:      "did not return the value written",
			remediation: "Re-run secret set to write it again, then env-vault doctor if it persists",
		},
		{
			name: "missing", code: "SECRET_UNVERIFIED", exit: 1, readErr: secretstore.ErrNotFound,
			suffix:      "did not return the value written",
			remediation: "Re-run secret set to write it again, then env-vault doctor if it persists",
		},
		{
			name: "backend", code: "BACKEND_UNAVAILABLE", exit: 4, readErr: secretstore.ErrUnavailable,
			suffix: "failed", remediation: secretstore.DefaultBackendRemediation,
		},
		{
			name: "timeout", code: "BACKEND_UNAVAILABLE", exit: 4, readErr: secretstore.ErrTimeout,
			suffix: "failed", remediation: secretstore.TimeoutBackendRemediation,
		},
		{
			name: "unreadable", code: "BACKEND_UNAVAILABLE", exit: 4, readErr: secretstore.ErrUnreadable,
			suffix: "failed", remediation: secretstore.UnreadableBackendRemediation,
		},
	}
	for _, state := range states {
		for _, outcome := range outcomes {
			for _, mode := range []string{"human", "json", "jsonl"} {
				t.Run(state.action+"/"+outcome.name+"/"+mode, func(t *testing.T) {
					storePath := setupTestBackend(t)
					written := testutil.EphemeralValue(t)
					previous := testutil.EphemeralValue(t)
					for previous == written {
						previous = testutil.EphemeralValue(t)
					}
					name := "verify-" + rand.Text()
					service := "verify-service-" + rand.Text()
					store := &verifyFailureStore{
						service: service, name: name, written: []byte(written), existed: state.existed,
						argsMatch: true, valueMatches: true,
					}
					if outcome.readErr == nil {
						store.readValue = []byte(previous)
					} else {
						// A backend's private cause may contain values. Neither the
						// renderer nor test diagnostics may disclose that cause.
						store.readErr = errors.Join(outcome.readErr, errors.New(written), errors.New(previous))
					}
					var stdout, stderr bytes.Buffer
					app := newApp(strings.NewReader(written+"\n"), &stdout, &stderr)
					wrapCalls := 0
					app.wrapStore = func(base secretstore.Store) secretstore.Store {
						wrapCalls++
						store.Store = base
						return store
					}
					outputPath := filepath.Join(t.TempDir(), "result.json")
					args := []string{"--verbose", "--output", outputPath}
					if mode != "human" {
						args = append(args, "--"+mode)
					}
					args = append(args, "secret", "set", name, "--service", service, "--stdin", "--verify")
					code := app.run(args)
					for _, value := range []string{written, previous} {
						testutil.AssertNotContains(t, "verify failure stdout", stdout.String(), value)
						testutil.AssertNotContains(t, "verify failure stderr", stderr.String(), value)
						testutil.AssertFileNotContains(t, "verify failure output file", outputPath, value)
					}
					if code != outcome.exit {
						t.Fatalf("exit=%d, want %d", code, outcome.exit)
					}
					if wrapCalls != 1 || !slices.Equal(store.calls, []string{"exists", "set", "get"}) {
						t.Fatal("expected one store with Exists, Set, then Get and no rollback")
					}
					if !store.argsMatch || !store.valueMatches {
						t.Fatal("store did not receive the requested service, name, and value")
					}
					if _, err := os.Stat(storePath); !errors.Is(err, os.ErrNotExist) {
						t.Fatal("verification test unexpectedly persisted the test store")
					}
					want := output.ErrorObject{
						Code: outcome.code, Remediation: outcome.remediation,
						Message: "Backend reported success for secret " + name + " (action: " + state.action + "), but reading it back " + outcome.suffix,
					}
					fileData, err := os.ReadFile(outputPath)
					if err != nil {
						t.Fatal("could not read verification error output file")
					}
					assertVerifyFailureEnvelope(t, fileData, want)
					if mode == "human" {
						if stdout.Len() != 0 {
							t.Fatal("human verification failure wrote to stdout")
						}
						wantText := "code=" + want.Code + "\nmessage=" + want.Message + "\nremediation=" + want.Remediation + "\n"
						if stderr.String() != wantText {
							t.Fatal("human verification error did not match the expected fields")
						}
					} else {
						if stderr.Len() != 0 || bytes.Count(stdout.Bytes(), []byte("\n")) != 1 {
							t.Fatal("machine verification failure must write one envelope to stdout only")
						}
						assertVerifyFailureEnvelope(t, stdout.Bytes(), want)
					}
				})
			}
		}
	}
}

func assertVerifyFailureEnvelope(t *testing.T, data []byte, want output.ErrorObject) {
	t.Helper()
	var env output.Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatal("verification error is not a valid JSON envelope")
	}
	if env.OK || env.Command != "secret_set" || env.Data != nil || env.Error == nil {
		t.Fatal("verification error has unexpected envelope fields")
	}
	if env.Error.Code != want.Code {
		t.Fatal("verification error code changed")
	}
	if env.Error.Message != want.Message {
		t.Fatal("verification error message does not qualify the requested action")
	}
	if env.Error.Remediation != want.Remediation {
		t.Fatal("verification error remediation changed")
	}
}
