package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ildarbinanas-design/env-vault/internal/bundle"
	apperrors "github.com/ildarbinanas-design/env-vault/internal/errors"
	"github.com/ildarbinanas-design/env-vault/internal/secretstore"
	"github.com/ildarbinanas-design/env-vault/internal/secretstore/teststore"
	"github.com/ildarbinanas-design/env-vault/internal/testutil"
)

// limitedStore gives a store the value limit of Windows Credential Manager:
// it reports the limit and, like the real backend, refuses a larger value.
type limitedStore struct {
	secretstore.Store
	limit int
}

func (s limitedStore) MaxValueBytes() int { return s.limit }

func (s limitedStore) Set(ctx context.Context, service, name string, value []byte) error {
	if s.limit > 0 && len(value) > s.limit {
		return secretstore.ErrValueTooLarge
	}
	return s.Store.Set(ctx, service, name, value)
}

func TestRefuseOversizedValues(t *testing.T) {
	entries := []bundle.SecretEntry{
		{Service: secretstore.DefaultService, Name: "small", Value: valueLimitTestInput(t, 4)},
		{Service: secretstore.DefaultService, Name: "large", Value: valueLimitTestInput(t, 5)},
	}
	err := refuseOversizedValues(limitedStore{limit: 4}, entries)
	appErr, ok := apperrors.From(err)
	if !ok || appErr.Code != apperrors.CodeSecretTooLarge || appErr.ExitCode != apperrors.ExitUsage || appErr.Message != "Secret large exceeds the size limit" {
		t.Fatalf("err=%v, want SECRET_TOO_LARGE for large", err)
	}
	for name, store := range map[string]secretstore.Store{
		"a limit every value fits": limitedStore{limit: 5},
		"no known limit":           limitedStore{limit: 0},
		"a store without limits":   struct{ secretstore.Store }{},
	} {
		if err := refuseOversizedValues(store, entries); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

type valueLimitRecorder struct {
	secretstore.Store
	existsCalls, setCalls, getCalls, deleteCalls, listCalls int
	borrowedValue                                           []byte
}

func (s *valueLimitRecorder) Exists(ctx context.Context, service, name string) (bool, error) {
	s.existsCalls++
	return s.Store.Exists(ctx, service, name)
}

func (s *valueLimitRecorder) Set(ctx context.Context, service, name string, value []byte) error {
	s.setCalls++
	s.borrowedValue = value
	return s.Store.Set(ctx, service, name, value)
}

func (s *valueLimitRecorder) Get(ctx context.Context, service, name string) ([]byte, error) {
	s.getCalls++
	return s.Store.Get(ctx, service, name)
}

func (s *valueLimitRecorder) Delete(ctx context.Context, service, name string) error {
	s.deleteCalls++
	return s.Store.Delete(ctx, service, name)
}

func (s *valueLimitRecorder) List(ctx context.Context, service string) ([]secretstore.Metadata, error) {
	s.listCalls++
	return s.Store.List(ctx, service)
}

func (s *valueLimitRecorder) calls() int {
	return s.existsCalls + s.setCalls + s.getCalls + s.deleteCalls + s.listCalls
}

func valueLimitTestInput(t *testing.T, size int) []byte {
	t.Helper()
	value := make([]byte, size)
	for offset := 0; offset < len(value); {
		offset += copy(value[offset:], testutil.EphemeralValue(t))
	}
	t.Cleanup(func() { bundle.Wipe(value) })
	return value
}

func TestRefuseOversizedValuesEffectiveLimit(t *testing.T) {
	for _, tc := range []struct {
		name  string
		store secretstore.Store
		limit int
	}{
		{"absent-limit", struct{ secretstore.Store }{}, secretstore.MaxValueBytes},
		{"zero-limit", limitedStore{limit: 0}, secretstore.MaxValueBytes},
		{"negative-limit", limitedStore{limit: -1}, secretstore.MaxValueBytes},
		{"larger-limit", limitedStore{limit: secretstore.MaxValueBytes + 1}, secretstore.MaxValueBytes},
		{"smaller-limit", limitedStore{limit: 2560}, 2560},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entries := []bundle.SecretEntry{{Service: secretstore.DefaultService, Name: "boundary", Value: valueLimitTestInput(t, tc.limit)}}
			if err := refuseOversizedValues(tc.store, entries); err != nil {
				t.Fatal("value at the effective limit was refused")
			}
			entries = append(entries, bundle.SecretEntry{Service: secretstore.DefaultService, Name: "oversized", Value: valueLimitTestInput(t, tc.limit+1)})
			appErr, ok := apperrors.From(refuseOversizedValues(tc.store, entries))
			if !ok || appErr.Code != apperrors.CodeSecretTooLarge || appErr.ExitCode != 2 || appErr.Message != "Secret oversized exceeds the size limit" {
				t.Fatal("oversized import preflight returned the wrong error")
			}
			if appErr.Remediation != fmt.Sprintf("Store a shorter value (at most %d bytes per secret)", tc.limit) {
				t.Fatal("size remediation did not name the effective limit")
			}
		})
	}
}

func TestSecretSetCommonValueLimitBoundary(t *testing.T) {
	testSecretSetValueLimitBoundary(t, secretstore.MaxValueBytes, false)
}

func TestSecretSetWindowsValueLimitBoundary(t *testing.T) {
	testSecretSetValueLimitBoundary(t, 2560, true)
}

func testSecretSetValueLimitBoundary(t *testing.T, limit int, backendLimit bool) {
	t.Helper()
	for _, size := range []int{limit, limit + 1} {
		t.Run(fmt.Sprintf("bytes-%d", size), func(t *testing.T) {
			for _, suffix := range []struct{ name, bytes string }{{"no-suffix", ""}, {"lf", "\n"}, {"crlf", "\r\n"}} {
				t.Run(suffix.name, func(t *testing.T) {
					newTransferEnv(t)
					value := valueLimitTestInput(t, size)
					input := append(append([]byte(nil), value...), suffix.bytes...)
					t.Cleanup(func() { bundle.Wipe(input) })
					var stdout, stderr bytes.Buffer
					app := newApp(bytes.NewReader(input), &stdout, &stderr)
					recorder := &valueLimitRecorder{}
					factories := 0
					app.wrapStore = func(store secretstore.Store) secretstore.Store {
						factories++
						recorder.Store = store
						if backendLimit {
							return limitedStore{Store: recorder, limit: limit}
						}
						return recorder
					}
					code := app.run([]string{"--json", "secret", "set", "boundary", "--stdin"})
					assertLimitOutputDoesNotContain(t, stdout.Bytes(), stderr.Bytes(), value)
					if size > limit {
						assertLimitError(t, code, stdout.String()+stderr.String(), limit)
						if recorder.calls() != 0 || (!backendLimit && factories != 0) || (backendLimit && factories != 1) {
							t.Fatal("oversized secret reached a backend operation or the wrong factory boundary")
						}
						return
					}
					if code != 0 || factories != 1 || recorder.existsCalls != 1 || recorder.setCalls != 1 {
						t.Fatal("value at the limit was not stored normally")
					}
					assertLimitStoredValue(t, "boundary", value)
					assertLimitBytesWiped(t, recorder.borrowedValue[:cap(recorder.borrowedValue)])
				})
			}
		})
	}
}

func TestSecretSetWindowsOversizedValueRefusedBeforeBackend(t *testing.T) {
	for _, mode := range []string{"human", "json", "jsonl"} {
		t.Run(mode, func(t *testing.T) {
			newTransferEnv(t)
			value := valueLimitTestInput(t, 2561)
			var stdout, stderr bytes.Buffer
			app := newApp(bytes.NewReader(value), &stdout, &stderr)
			recorder := &valueLimitRecorder{}
			app.wrapStore = func(store secretstore.Store) secretstore.Store {
				recorder.Store = store
				return limitedStore{Store: recorder, limit: 2560}
			}
			args := []string{"secret", "set", "boundary", "--stdin"}
			if mode != "human" {
				args = append([]string{"--" + mode}, args...)
			}
			code := app.run(args)
			assertLimitOutputDoesNotContain(t, stdout.Bytes(), stderr.Bytes(), value)
			assertLimitError(t, code, stdout.String()+stderr.String(), 2560)
			if recorder.calls() != 0 {
				t.Fatal("oversized Windows value reached a backend operation")
			}
		})
	}
}

func TestSecretSetOversizedStdinRefusedBeforeStoreFactory(t *testing.T) {
	for _, suffix := range []string{"", "\n", "\r\n", "\r", "\n\n"} {
		t.Run(fmt.Sprintf("suffix-%x", suffix), func(t *testing.T) {
			newTransferEnv(t)
			value := valueLimitTestInput(t, secretstore.MaxValueBytes+1)
			input := append(append([]byte(nil), value...), suffix...)
			defer bundle.Wipe(input)
			var stdout, stderr bytes.Buffer
			app := newApp(bytes.NewReader(input), &stdout, &stderr)
			app.wrapStore = func(secretstore.Store) secretstore.Store {
				t.Fatal("oversized stdin constructed a store")
				return nil
			}
			code := app.run([]string{"--json", "secret", "set", "boundary", "--stdin"})
			assertLimitOutputDoesNotContain(t, stdout.Bytes(), stderr.Bytes(), value)
			assertLimitError(t, code, stdout.String()+stderr.String(), secretstore.MaxValueBytes)
		})
	}
}

func TestSecretSetOversizedReplacementPreservesExistingValue(t *testing.T) {
	newTransferEnv(t)
	previous := valueLimitTestInput(t, 43)
	seedLimitValue(t, "boundary", previous)
	var stdout, stderr bytes.Buffer
	app := newApp(bytes.NewReader(valueLimitTestInput(t, secretstore.MaxValueBytes+1)), &stdout, &stderr)
	app.wrapStore = func(secretstore.Store) secretstore.Store {
		t.Fatal("oversized replacement constructed a store")
		return nil
	}
	code := app.run([]string{"--json", "secret", "set", "boundary", "--stdin"})
	assertLimitError(t, code, stdout.String()+stderr.String(), secretstore.MaxValueBytes)
	assertLimitStoredValue(t, "boundary", previous)
}

func TestReadSecretStdinReadBoundAndLineEnding(t *testing.T) {
	input := valueLimitTestInput(t, secretstore.MaxValueBytes+257)
	reader := bytes.NewReader(input)
	value, err := newApp(reader, io.Discard, io.Discard).readSecret(true)
	defer bundle.Wipe(value)
	appErr, ok := apperrors.From(err)
	if !ok || appErr.Code != apperrors.CodeSecretTooLarge || value != nil || len(input)-reader.Len() != secretstore.MaxValueBytes+3 {
		t.Fatal("stdin did not stop at the bounded read or refused with the wrong result")
	}
	for _, suffix := range []string{"\n", "\r\n", "\r", "\n\n"} {
		t.Run(fmt.Sprintf("suffix-%x", suffix), func(t *testing.T) {
			prefix := valueLimitTestInput(t, 43)
			input := append(append([]byte(nil), prefix...), suffix...)
			defer bundle.Wipe(input)
			got, err := newApp(bytes.NewReader(input), io.Discard, io.Discard).readSecret(true)
			defer bundle.Wipe(got)
			want := input
			if strings.HasSuffix(suffix, "\n") {
				want = want[:len(want)-1]
				if len(want) > 0 && want[len(want)-1] == '\r' {
					want = want[:len(want)-1]
				}
			}
			if err != nil || !bytes.Equal(got, want) {
				t.Fatal("length/digest mismatch")
			}
			assertLimitBytesWiped(t, got[len(got):cap(got)])
		})
	}
}

type failedValueInput struct{ value, borrowed []byte }

func (r *failedValueInput) Read(p []byte) (int, error) {
	n := copy(p, r.value)
	r.borrowed = p[:n]
	return n, io.ErrUnexpectedEOF
}

func TestReadSecretStdinWipesInputOnReadFailure(t *testing.T) {
	input := &failedValueInput{value: valueLimitTestInput(t, 43)}
	value, err := newApp(input, io.Discard, io.Discard).readSecret(true)
	defer bundle.Wipe(value)
	appErr, ok := apperrors.From(err)
	if !ok || appErr.Code != apperrors.CodeRuntimeError || value != nil {
		t.Fatal("stdin read failure returned the wrong result")
	}
	assertLimitBytesWiped(t, input.borrowed)
}

func TestImportCommonValueLimitBoundary(t *testing.T) {
	testImportValueLimitBoundary(t, secretstore.MaxValueBytes, false)
}

func TestImportWindowsValueLimitBoundary(t *testing.T) {
	testImportValueLimitBoundary(t, 2560, true)
}

func testImportValueLimitBoundary(t *testing.T, limit int, backendLimit bool) {
	t.Helper()
	for _, size := range []int{limit, limit + 1} {
		t.Run(fmt.Sprintf("bytes-%d", size), func(t *testing.T) {
			newTransferEnv(t)
			value := valueLimitTestInput(t, size)
			container, passphrase := valueLimitContainer(t, []bundle.SecretEntry{{Service: secretstore.DefaultService, Name: "boundary", Value: value}})
			code, output, recorder := runValueLimitImport(t, container, passphrase, limit, backendLimit)
			if size > limit {
				assertLimitError(t, code, output, limit)
				if recorder.setCalls != 0 {
					t.Fatal("oversized import performed a write")
				}
			} else {
				if code != 0 || recorder.setCalls != 1 {
					t.Fatal("import at the effective limit was refused")
				}
				assertLimitStoredValue(t, "boundary", value)
			}
		})
	}
}

func TestImportRefusesAnOversizedValueBeforeAnyWrite(t *testing.T) {
	for _, limit := range []int{secretstore.MaxValueBytes, 2560} {
		t.Run(fmt.Sprintf("limit-%d", limit), func(t *testing.T) {
			small, oversized := valueLimitTestInput(t, 43), valueLimitTestInput(t, limit+1)
			container, passphrase := valueLimitContainer(t, []bundle.SecretEntry{
				{Service: secretstore.DefaultService, Name: "a-small", Value: small},
				{Service: secretstore.DefaultService, Name: "b-large", Value: oversized},
			})
			for _, mode := range []string{"normal", "dry-run", "overwrite", "skip"} {
				t.Run(mode, func(t *testing.T) {
					newTransferEnv(t)
					previous := valueLimitTestInput(t, 43)
					var args []string
					if mode == "dry-run" {
						args = []string{"--dry-run"}
					} else if mode == "overwrite" || mode == "skip" {
						seedLimitValue(t, "b-large", previous)
						args = []string{"--on-conflict", mode}
					}
					code, output, recorder := runValueLimitImport(t, container, passphrase, limit, limit < secretstore.MaxValueBytes, args...)
					if mode == "skip" {
						if code != 0 || recorder.setCalls != 1 {
							t.Fatal("skip did not exempt the existing oversized entry")
						}
						assertLimitStoredValue(t, "a-small", small)
					} else {
						assertLimitError(t, code, output, limit)
						if recorder.setCalls != 0 {
							t.Fatal("import wrote before refusing the oversized entry")
						}
						if value, ok := storedValue(t, secretstore.DefaultService, "a-small"); ok {
							bundle.Wipe(value)
							t.Fatal("import stored an entry from a refused batch")
						}
					}
					if mode == "skip" || mode == "overwrite" {
						assertLimitStoredValue(t, "b-large", previous)
					}
				})
			}
		})
	}
}

func valueLimitContainer(t *testing.T, entries []bundle.SecretEntry) (string, []byte) {
	t.Helper()
	passphrase := valueLimitTestInput(t, 43)
	// Build ciphertext directly: restricted secret set cannot seed oversized
	// entries whose import preflight is under test.
	raw, err := bundle.Seal(bundle.Payload{Secrets: entries}, passphrase, bundle.Options{})
	if err != nil {
		t.Fatal("unable to generate the encrypted test container")
	}
	path := filepath.Join(t.TempDir(), "values.evb")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal("unable to save the encrypted test container")
	}
	return path, passphrase
}

func runValueLimitImport(t *testing.T, container string, passphrase []byte, limit int, backendLimit bool, extra ...string) (int, string, *valueLimitRecorder) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	app := newApp(bytes.NewReader(nil), &stdout, &stderr)
	app.passphraseReader = func(string) ([]byte, error) { return append([]byte(nil), passphrase...), nil }
	recorder := &valueLimitRecorder{}
	app.wrapStore = func(store secretstore.Store) secretstore.Store {
		recorder.Store = store
		if backendLimit {
			return limitedStore{Store: recorder, limit: limit}
		}
		return recorder
	}
	args := append([]string{"--json", "import", container}, extra...)
	code := app.run(args)
	assertLimitOutputDoesNotContain(t, stdout.Bytes(), stderr.Bytes(), passphrase)
	return code, stdout.String() + stderr.String(), recorder
}

func seedLimitValue(t *testing.T, name string, value []byte) {
	t.Helper()
	store, err := teststore.NewFromEnv("test")
	if err != nil {
		t.Fatal("unable to open gated disposable test store")
	}
	if err := store.Set(context.Background(), secretstore.DefaultService, name, value); err != nil {
		t.Fatal("unable to seed disposable test value")
	}
}

func assertLimitStoredValue(t *testing.T, name string, want []byte) {
	t.Helper()
	got, ok := storedValue(t, secretstore.DefaultService, name)
	defer bundle.Wipe(got)
	if !ok || !bytes.Equal(got, want) {
		t.Fatal("length/digest mismatch")
	}
}

func assertLimitOutputDoesNotContain(t *testing.T, stdout, stderr, value []byte) {
	t.Helper()
	if len(value) != 0 && (bytes.Contains(stdout, value) || bytes.Contains(stderr, value)) {
		t.Fatal("size-limit output contains generated sensitive input")
	}
}

func assertLimitError(t *testing.T, code int, output string, limit int) {
	t.Helper()
	if code != 2 || (!strings.Contains(output, `"code":"SECRET_TOO_LARGE"`) && !strings.Contains(output, "code=SECRET_TOO_LARGE")) {
		t.Fatal("oversized value did not report SECRET_TOO_LARGE with exit 2")
	}
	if !strings.Contains(output, fmt.Sprintf("at most %d bytes", limit)) || strings.Contains(output, "Windows") {
		t.Fatal("size remediation did not accurately name the applicable limit")
	}
}

func assertLimitBytesWiped(t *testing.T, value []byte) {
	t.Helper()
	for _, b := range value {
		if b != 0 {
			t.Fatal("owned secret-input allocation was not wiped")
		}
	}
}
