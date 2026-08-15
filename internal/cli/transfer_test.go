package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ildarbinanas-design/env-vault/internal/secretstore"
	"github.com/ildarbinanas-design/env-vault/internal/secretstore/teststore"
	"github.com/ildarbinanas-design/env-vault/internal/testutil"
)

const transferPassphrase = "correct horse battery staple"

type transferEnv struct {
	storePath string
}

// newTransferEnv activates the complete insecure test-backend gate, so no test
// in this file can reach a production keychain.
func newTransferEnv(t *testing.T) *transferEnv {
	t.Helper()
	env := &transferEnv{storePath: filepath.Join(t.TempDir(), "store")}
	t.Setenv(teststore.BackendEnv, "test")
	t.Setenv(teststore.AllowEnv, "1")
	t.Setenv(teststore.StoreEnv, env.storePath)
	return env
}

// useFreshStore simulates arriving on a second machine with an empty keychain.
func (e *transferEnv) useFreshStore(t *testing.T) {
	t.Helper()
	e.storePath = filepath.Join(t.TempDir(), "store")
	t.Setenv(teststore.StoreEnv, e.storePath)
}

func runCLI(t *testing.T, stdin, passphrase string, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	app := newApp(strings.NewReader(stdin), &stdout, &stderr)
	if passphrase != "" {
		// A fresh slice on every call: readPassphrase wipes the confirmation
		// independently of the passphrase it keeps.
		app.passphraseReader = func(string) ([]byte, error) { return []byte(passphrase), nil }
	}
	return app.run(args), stdout.String(), stderr.String()
}

func mustRunCLI(t *testing.T, stdin, passphrase string, args ...string) string {
	t.Helper()
	code, stdout, stderr := runCLI(t, stdin, passphrase, args...)
	if code != 0 {
		t.Fatalf("Run(%v) code=%d stdout=%s stderr=%s", args, code, stdout, stderr)
	}
	return stdout
}

func setSecret(t *testing.T, name, value string, extra ...string) {
	t.Helper()
	args := append([]string{"secret", "set", name, "--stdin"}, extra...)
	mustRunCLI(t, value, "", args...)
}

func storedValue(t *testing.T, service, name string) ([]byte, bool) {
	t.Helper()
	store, err := teststore.NewFromEnv("test")
	if err != nil {
		t.Fatalf("NewFromEnv: %v", err)
	}
	exists, err := store.Exists(context.Background(), service, name)
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if !exists {
		return nil, false
	}
	value, err := store.Get(context.Background(), service, name)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	return value, true
}

type envelope struct {
	OK       bool           `json:"ok"`
	Command  string         `json:"command"`
	Data     map[string]any `json:"data"`
	Warnings []string       `json:"warnings"`
}

func decodeEnvelope(t *testing.T, stdout string) envelope {
	t.Helper()
	var decoded envelope
	if err := json.Unmarshal([]byte(stdout), &decoded); err != nil {
		t.Fatalf("Unmarshal(%q): %v", stdout, err)
	}
	return decoded
}

// secretActions keys the reported entries by "service\x00name", because the
// same secret name may appear under more than one service.
func secretActions(t *testing.T, data map[string]any) map[string]string {
	t.Helper()
	actions := map[string]string{}
	entries, ok := data["secrets"].([]any)
	if !ok {
		t.Fatalf("data.secrets = %#v, want a list", data["secrets"])
	}
	for _, raw := range entries {
		entry := raw.(map[string]any)
		key := entry["service"].(string) + "\x00" + entry["name"].(string)
		actions[key], _ = entry["action"].(string)
	}
	return actions
}

// assertErrorCode accepts either rendering of the error envelope, because
// --json moves it from the human stderr form onto stdout.
func assertErrorCode(t *testing.T, output, want string) {
	t.Helper()
	if strings.Contains(output, "code="+want) || strings.Contains(output, `"code":"`+want+`"`) {
		return
	}
	t.Fatalf("output = %q, want error code %s", output, want)
}

func TestExportImportRoundTripRestoresEveryValue(t *testing.T) {
	env := newTransferEnv(t)
	first := testutil.EphemeralValue(t)
	second := testutil.EphemeralValue(t)
	setSecret(t, "nexus-token", first)
	setSecret(t, "team/deploy-key", second)

	container := filepath.Join(t.TempDir(), "vault.evb")
	stdout := mustRunCLI(t, "", transferPassphrase, "--json", "export", "--out", container)

	if got := decodeEnvelope(t, stdout).Data["secret_count"].(float64); got != 2 {
		t.Fatalf("secret_count = %v, want 2", got)
	}
	for _, value := range []string{first, second} {
		testutil.AssertFileNotContains(t, "container", container, value)
	}

	env.useFreshStore(t)
	if _, exists := storedValue(t, secretstore.DefaultService, "nexus-token"); exists {
		t.Fatal("fresh store already holds a secret")
	}

	mustRunCLI(t, "", transferPassphrase, "--json", "import", container)

	for name, want := range map[string]string{"nexus-token": first, "team/deploy-key": second} {
		restored, exists := storedValue(t, secretstore.DefaultService, name)
		if !exists {
			t.Fatalf("import did not restore %s", name)
		}
		if string(restored) != want {
			t.Fatalf("restored value for %s differs from the exported value", name)
		}
	}
}

// Profile mappings are not part of a container. They live in a config file
// that holds no values and travels with the repository.
func TestExportIgnoresProfileConfiguration(t *testing.T) {
	newTransferEnv(t)
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	setSecret(t, "nexus-token", testutil.EphemeralValue(t))
	mustRunCLI(t, "", "", "--config", configPath, "profile", "create", "dev")
	mustRunCLI(t, "", "", "--config", configPath, "profile", "add", "dev", "nexus-token:NPM_TOKEN")

	container := filepath.Join(t.TempDir(), "vault.evb")
	mustRunCLI(t, "", transferPassphrase, "--json", "export", "--out", container)

	raw, err := os.ReadFile(container)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	// Only distinctive strings are checked. A short token such as the profile
	// name "dev" occurs by chance in base64 ciphertext and would prove
	// nothing; these contain characters outside the base64 alphabet.
	for _, mapping := range []string{"NPM_TOKEN", "nexus-token"} {
		if strings.Contains(string(raw), mapping) {
			t.Fatalf("container discloses %q in the clear", mapping)
		}
	}

	// Importing must not create or touch a config file. The store still holds
	// the secret, so this reimports over itself with an explicit policy.
	targetConfig := filepath.Join(t.TempDir(), "target.yaml")
	mustRunCLI(t, "", transferPassphrase, "--config", targetConfig, "import", container, "--on-conflict", "overwrite")
	if _, err := os.Stat(targetConfig); !os.IsNotExist(err) {
		t.Fatalf("import wrote a config file (err=%v)", err)
	}
}

func TestExportCoversNamedServicesOnlyWhenAsked(t *testing.T) {
	env := newTransferEnv(t)
	const customService = "team/custom"
	setSecret(t, "default-token", testutil.EphemeralValue(t))
	custom := testutil.EphemeralValue(t)
	setSecret(t, "custom-token", custom, "--service", customService)

	container := filepath.Join(t.TempDir(), "default.evb")
	stdout := mustRunCLI(t, "", transferPassphrase, "--json", "export", "--out", container)
	if got := decodeEnvelope(t, stdout).Data["secret_count"].(float64); got != 1 {
		t.Fatalf("default export secret_count = %v, want 1", got)
	}

	withCustom := filepath.Join(t.TempDir(), "all.evb")
	stdout = mustRunCLI(t, "", transferPassphrase, "--json", "export", "--out", withCustom, "--with-services", customService)
	if got := decodeEnvelope(t, stdout).Data["secret_count"].(float64); got != 2 {
		t.Fatalf("named-service export secret_count = %v, want 2", got)
	}

	env.useFreshStore(t)
	mustRunCLI(t, "", transferPassphrase, "import", withCustom)
	restored, exists := storedValue(t, customService, "custom-token")
	if !exists || string(restored) != custom {
		t.Fatal("the named service did not survive the round trip")
	}
	if _, leaked := storedValue(t, secretstore.DefaultService, "custom-token"); leaked {
		t.Fatal("a named-service secret landed in the default service")
	}
}

// Without the test-backend gate and without the in-package seam, the
// passphrase can only come from a terminal. This is the property that keeps a
// passphrase out of shell history and process listings.
func TestPassphrasePromptRequiresTerminal(t *testing.T) {
	container := filepath.Join(t.TempDir(), "vault.evb")
	if err := os.WriteFile(container, []byte("{}"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	code, _, stderr := runCLI(t, "passphrase-on-stdin\n", "", "import", container)
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	assertErrorCode(t, stderr, "USAGE")
	if !strings.Contains(stderr, "requires a terminal") {
		t.Fatalf("stderr = %q, want a terminal requirement", stderr)
	}
}

// The gated stdin path exists so the black-box E2E suite can round-trip
// without a terminal. It is reachable only with the full insecure
// test-backend gate, which newTransferEnv sets.
func TestGatedStdinPassphraseRoundTrip(t *testing.T) {
	env := newTransferEnv(t)
	value := testutil.EphemeralValue(t)
	setSecret(t, "nexus-token", value)

	container := filepath.Join(t.TempDir(), "vault.evb")
	mustRunCLI(t, transferPassphrase+"\n", "", "export", "--out", container)

	env.useFreshStore(t)
	mustRunCLI(t, transferPassphrase+"\n", "", "import", container)

	restored, exists := storedValue(t, secretstore.DefaultService, "nexus-token")
	if !exists || string(restored) != value {
		t.Fatal("gated stdin round trip did not restore the value")
	}
}

func TestExportRefusesExistingContainerWithoutForce(t *testing.T) {
	newTransferEnv(t)
	setSecret(t, "nexus-token", testutil.EphemeralValue(t))

	container := filepath.Join(t.TempDir(), "vault.evb")
	if err := os.WriteFile(container, []byte("existing"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	code, _, stderr := runCLI(t, "", transferPassphrase, "export", "--out", container)
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	assertErrorCode(t, stderr, "USAGE")
	data, err := os.ReadFile(container)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "existing" {
		t.Fatal("export overwrote the existing container")
	}

	mustRunCLI(t, "", transferPassphrase, "export", "--out", container, "--force")
	data, err = os.ReadFile(container)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) == "existing" {
		t.Fatal("--force did not overwrite the container")
	}
}

func TestExportDryRunWritesNothingAndNeverPrompts(t *testing.T) {
	newTransferEnv(t)
	setSecret(t, "nexus-token", testutil.EphemeralValue(t))
	container := filepath.Join(t.TempDir(), "vault.evb")

	var stdout, stderr bytes.Buffer
	app := newApp(strings.NewReader(""), &stdout, &stderr)
	app.passphraseReader = func(string) ([]byte, error) {
		t.Error("dry run asked for a passphrase")
		return []byte(transferPassphrase), nil
	}
	if code := app.run([]string{"--json", "--dry-run", "export", "--out", container}); code != 0 {
		t.Fatalf("code = %d stderr=%s", code, stderr.String())
	}

	if _, err := os.Stat(container); !os.IsNotExist(err) {
		t.Fatalf("dry run created %s", container)
	}
	data := decodeEnvelope(t, stdout.String()).Data
	if data["dry_run"] != true {
		t.Fatalf("dry_run = %#v, want true", data["dry_run"])
	}
	if data["secret_count"].(float64) != 1 {
		t.Fatalf("secret_count = %#v, want 1", data["secret_count"])
	}
}

func TestImportDryRunWritesNothing(t *testing.T) {
	env := newTransferEnv(t)
	setSecret(t, "nexus-token", testutil.EphemeralValue(t))
	container := filepath.Join(t.TempDir(), "vault.evb")
	mustRunCLI(t, "", transferPassphrase, "export", "--out", container)

	env.useFreshStore(t)
	mustRunCLI(t, "", transferPassphrase, "--json", "--dry-run", "import", container)

	if _, exists := storedValue(t, secretstore.DefaultService, "nexus-token"); exists {
		t.Fatal("dry run wrote the secret")
	}
}

func TestExportRejectsEmptyStore(t *testing.T) {
	newTransferEnv(t)

	code, _, stderr := runCLI(t, "", transferPassphrase, "export", "--out", filepath.Join(t.TempDir(), "vault.evb"))
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	assertErrorCode(t, stderr, "USAGE")
}

func TestImportRejectsWrongPassphrase(t *testing.T) {
	env := newTransferEnv(t)
	setSecret(t, "nexus-token", testutil.EphemeralValue(t))
	container := filepath.Join(t.TempDir(), "vault.evb")
	mustRunCLI(t, "", transferPassphrase, "export", "--out", container)

	env.useFreshStore(t)
	code, _, stderr := runCLI(t, "", "an entirely different passphrase", "import", container)
	if code != 5 {
		t.Fatalf("code = %d, want 5", code)
	}
	assertErrorCode(t, stderr, "BUNDLE_AUTH_FAILED")

	if _, exists := storedValue(t, secretstore.DefaultService, "nexus-token"); exists {
		t.Fatal("a failed import wrote a secret")
	}
}

func TestImportRejectsTamperedContainer(t *testing.T) {
	env := newTransferEnv(t)
	setSecret(t, "nexus-token", testutil.EphemeralValue(t))
	container := filepath.Join(t.TempDir(), "vault.evb")
	mustRunCLI(t, "", transferPassphrase, "export", "--out", container)

	raw, err := os.ReadFile(container)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	document["created_at"] = "2020-01-01T00:00:00Z"
	tampered, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := os.WriteFile(container, tampered, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	env.useFreshStore(t)
	code, _, stderr := runCLI(t, "", transferPassphrase, "import", container)
	if code != 5 {
		t.Fatalf("code = %d, want 5", code)
	}
	assertErrorCode(t, stderr, "BUNDLE_AUTH_FAILED")
}

func TestImportRejectsMalformedContainer(t *testing.T) {
	newTransferEnv(t)
	container := filepath.Join(t.TempDir(), "vault.evb")
	if err := os.WriteFile(container, []byte("not a container at all"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	code, _, stderr := runCLI(t, "", transferPassphrase, "import", container)
	if code != 5 {
		t.Fatalf("code = %d, want 5", code)
	}
	assertErrorCode(t, stderr, "BUNDLE_INVALID")
}

func TestImportConflictPolicies(t *testing.T) {
	tests := []struct {
		policy        string
		wantCode      int
		wantErrorCode string
		wantAction    string
		wantKeptValue bool
	}{
		{policy: "fail", wantCode: 2, wantErrorCode: "SECRET_EXISTS"},
		{policy: "skip", wantCode: 0, wantAction: actionSkipped, wantKeptValue: true},
		{policy: "overwrite", wantCode: 0, wantAction: actionOverwritten},
	}

	for _, test := range tests {
		t.Run(test.policy, func(t *testing.T) {
			env := newTransferEnv(t)
			exported := testutil.EphemeralValue(t)
			setSecret(t, "nexus-token", exported)
			container := filepath.Join(t.TempDir(), "vault.evb")
			mustRunCLI(t, "", transferPassphrase, "export", "--out", container)

			// Arrive somewhere that already holds the same secret.
			existing := testutil.EphemeralValue(t)
			env.useFreshStore(t)
			setSecret(t, "nexus-token", existing)

			code, stdout, stderr := runCLI(t, "", transferPassphrase, "--json", "import", container, "--on-conflict", test.policy)
			if code != test.wantCode {
				t.Fatalf("code = %d, want %d stderr=%s", code, test.wantCode, stderr)
			}
			if test.wantErrorCode != "" {
				assertErrorCode(t, stdout+stderr, test.wantErrorCode)
				stored, _ := storedValue(t, secretstore.DefaultService, "nexus-token")
				if string(stored) != existing {
					t.Fatal("a failed import changed the stored value")
				}
				return
			}

			key := secretstore.DefaultService + "\x00" + "nexus-token"
			if got := secretActions(t, decodeEnvelope(t, stdout).Data)[key]; got != test.wantAction {
				t.Fatalf("action = %q, want %q", got, test.wantAction)
			}
			stored, exists := storedValue(t, secretstore.DefaultService, "nexus-token")
			if !exists {
				t.Fatal("secret disappeared")
			}
			if test.wantKeptValue && string(stored) != existing {
				t.Fatal("skip policy replaced the stored value")
			}
			if !test.wantKeptValue && string(stored) != exported {
				t.Fatal("overwrite policy did not replace the stored value")
			}
		})
	}
}

func TestImportSkipStillCreatesMissingSecrets(t *testing.T) {
	env := newTransferEnv(t)
	first := testutil.EphemeralValue(t)
	second := testutil.EphemeralValue(t)
	setSecret(t, "nexus-token", first)
	setSecret(t, "second-token", second)

	container := filepath.Join(t.TempDir(), "vault.evb")
	mustRunCLI(t, "", transferPassphrase, "export", "--out", container)

	env.useFreshStore(t)
	setSecret(t, "nexus-token", "a different stored value")

	stdout := mustRunCLI(t, "", transferPassphrase, "--json", "import", container, "--on-conflict", "skip")

	actions := secretActions(t, decodeEnvelope(t, stdout).Data)
	if got := actions[secretstore.DefaultService+"\x00"+"nexus-token"]; got != actionSkipped {
		t.Fatalf("nexus-token action = %q, want %q", got, actionSkipped)
	}
	if got := actions[secretstore.DefaultService+"\x00"+"second-token"]; got != actionCreated {
		t.Fatalf("second-token action = %q, want %q", got, actionCreated)
	}
	restored, exists := storedValue(t, secretstore.DefaultService, "second-token")
	if !exists || string(restored) != second {
		t.Fatal("skip policy did not create the missing secret")
	}
}

func TestExportRejectsWeakAndMismatchedPassphrases(t *testing.T) {
	newTransferEnv(t)
	setSecret(t, "nexus-token", testutil.EphemeralValue(t))

	t.Run("too short", func(t *testing.T) {
		code, _, stderr := runCLI(t, "", "short", "export", "--out", filepath.Join(t.TempDir(), "vault.evb"))
		if code != 2 {
			t.Fatalf("code = %d, want 2", code)
		}
		assertErrorCode(t, stderr, "PASSPHRASE_INVALID")
	})

	t.Run("mismatched", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		app := newApp(strings.NewReader(""), &stdout, &stderr)
		call := 0
		app.passphraseReader = func(string) ([]byte, error) {
			call++
			if call == 1 {
				return []byte(transferPassphrase), nil
			}
			return []byte(transferPassphrase + " typo"), nil
		}
		if code := app.run([]string{"export", "--out", filepath.Join(t.TempDir(), "vault.evb")}); code != 2 {
			t.Fatalf("code = %d, want 2", code)
		}
		assertErrorCode(t, stderr.String(), "PASSPHRASE_INVALID")
		if !strings.Contains(stderr.String(), "do not match") {
			t.Fatalf("stderr = %q", stderr.String())
		}
	})
}

func TestImportRejectsUnsupportedConflictPolicy(t *testing.T) {
	newTransferEnv(t)
	code, _, stderr := runCLI(t, "", transferPassphrase, "import", filepath.Join(t.TempDir(), "vault.evb"), "--on-conflict", "merge")
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	assertErrorCode(t, stderr, "USAGE")
}

func TestExportRequiresOutFlag(t *testing.T) {
	newTransferEnv(t)
	setSecret(t, "nexus-token", testutil.EphemeralValue(t))

	code, _, stderr := runCLI(t, "", transferPassphrase, "export")
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	assertErrorCode(t, stderr, "USAGE")
}

func TestExportRejectsInvalidServiceName(t *testing.T) {
	newTransferEnv(t)
	setSecret(t, "nexus-token", testutil.EphemeralValue(t))

	// A bare "" yields an empty list rather than an empty entry, so the empty
	// case is exercised as a gap inside a list instead.
	for _, service := range []string{"../escape", "/absolute", "a//b", "team/one,,team/two"} {
		code, _, stderr := runCLI(t, "", transferPassphrase,
			"export", "--out", filepath.Join(t.TempDir(), "vault.evb"), "--with-services", service)
		if code != 2 {
			t.Fatalf("--with-services %q: code = %d, want 2", service, code)
		}
		assertErrorCode(t, stderr, "USAGE")
	}
}

// The flag accepts a comma-separated list as well as repetition, and
// deduplicates against the always-present default service.
func TestExportAcceptsServiceListForms(t *testing.T) {
	env := newTransferEnv(t)
	setSecret(t, "default-token", testutil.EphemeralValue(t))
	first := testutil.EphemeralValue(t)
	second := testutil.EphemeralValue(t)
	setSecret(t, "first-token", first, "--service", "team/one")
	setSecret(t, "second-token", second, "--service", "team/two")

	tests := map[string][]string{
		"comma separated":   {"--with-services", "team/one,team/two"},
		"repeated":          {"--with-services", "team/one", "--with-services", "team/two"},
		"spaced list":       {"--with-services", "team/one, team/two"},
		"redundant default": {"--with-services", "env-vault,team/one,team/two"},
		"duplicate entries": {"--with-services", "team/one,team/one,team/two"},
	}

	for name, flags := range tests {
		t.Run(name, func(t *testing.T) {
			container := filepath.Join(t.TempDir(), "vault.evb")
			args := append([]string{"--json", "export", "--out", container}, flags...)
			stdout := mustRunCLI(t, "", transferPassphrase, args...)

			data := decodeEnvelope(t, stdout).Data
			if got := data["secret_count"].(float64); got != 3 {
				t.Fatalf("secret_count = %v, want 3", got)
			}
			services, ok := data["services"].([]any)
			if !ok || len(services) != 3 {
				t.Fatalf("services = %#v, want three entries", data["services"])
			}
		})
	}

	// One of the forms must genuinely round-trip, not merely be accepted.
	container := filepath.Join(t.TempDir(), "roundtrip.evb")
	mustRunCLI(t, "", transferPassphrase, "export", "--out", container, "--with-services", "team/one,team/two")
	env.useFreshStore(t)
	mustRunCLI(t, "", transferPassphrase, "import", container)

	for service, want := range map[string]string{"team/one": first, "team/two": second} {
		name := map[string]string{"team/one": "first-token", "team/two": "second-token"}[service]
		restored, exists := storedValue(t, service, name)
		if !exists || string(restored) != want {
			t.Fatalf("%s/%s did not survive the round trip", service, name)
		}
	}
}
