package e2e_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

type scenarioCase struct {
	id  string
	run func(*scenario)
}

func TestE2E(t *testing.T) {
	s := newSuite(t)
	tests := []scenarioCase{
		{"CLI_HELP_ROOT", testCLIHelpRoot},
		{"CLI_HELP_SUBCOMMANDS", testCLIHelpSubcommands},
		{"CLI_VERSION_FORMS", testCLIVersionForms},
		{"CLI_ARGUMENT_ERRORS", testCLIArgumentErrors},
		{"TEXT_OUTPUT_CONTRACTS", testTextOutputContracts},
		{"SECRET_LIFECYCLE", testSecretLifecycle},
		{"SECRET_VALIDATION_SECURITY", testSecretValidationSecurity},
		{"PROFILE_LIFECYCLE", testProfileLifecycle},
		{"PROFILE_TARGETS_CHECK_SECRET", testProfileTargetsCheckSecret},
		{"PROFILE_COLLISIONS_PERSISTENCE", testProfileCollisionsPersistence},
		{"PROFILE_ATOMIC_PERMISSIONS", testProfileAtomicPermissions},
		{"PROFILE_SYMLINK_REJECTED", testProfileSymlinkRejected},
		{"EXEC_PROFILE_DIRECT_MULTI", testExecProfileDirectMulti},
		{"EXEC_ENV_MODES", testExecEnvModes},
		{"EXEC_ARG_STREAM_EXIT", testExecArgStreamExit},
		{"EXEC_MISSING_SECRET_NO_CHILD", testExecMissingSecretNoChild},
		{"EXEC_SIGNAL_FORWARDING", testExecSignalForwarding},
		{"DRY_RUN_NO_SIDE_EFFECTS", testDryRunNoSideEffects},
		{"OUTPUT_JSON_JSONL_FILE", testOutputJSONJSONLFile},
		{"DOCTOR_BACKENDS", testDoctorBackends},
		{"CONCURRENCY_PROFILE_MUTATIONS", testConcurrencyProfileMutations},
		{"LOCK_TIMEOUT_CRASH_INTEGRITY", testLockTimeoutCrashIntegrity},
	}
	shuffleScenarioCases(t, tests)
	for _, test := range tests {
		test := test
		t.Run(test.id, func(t *testing.T) {
			test.run(newScenario(t, s, test.id))
		})
	}
}

func testCLIHelpRoot(sc *scenario) {
	result := sc.run("--help")
	wantExit(sc.t, result, 0)
	wantEmpty(sc.t, result.Stderr, "stderr")
	for _, want := range []string{"OS-keychain-backed", "Available Commands:", "secret", "profile", "exec", "doctor", "completion", "help", "--json", "--dry-run"} {
		wantContains(sc.t, result.Stdout, want, "root help")
	}
}

func testCLIHelpSubcommands(sc *scenario) {
	commands := [][]string{
		{"help", "--help"},
		{"help", "secret", "set"},
		{"completion", "--help"},
		{"completion", "bash", "--help"},
		{"completion", "fish", "--help"},
		{"completion", "powershell", "--help"},
		{"completion", "zsh", "--help"},
		{"version", "--help"},
		{"secret", "--help"},
		{"secret", "set", "--help"},
		{"secret", "check", "--help"},
		{"secret", "delete", "--help"},
		{"secret", "list", "--help"},
		{"profile", "--help"},
		{"profile", "create", "--help"},
		{"profile", "add", "--help"},
		{"profile", "remove", "--help"},
		{"profile", "show", "--help"},
		{"exec", "--help"},
		{"doctor", "--help"},
	}
	for _, args := range commands {
		result := sc.run(args...)
		wantExit(sc.t, result, 0)
		wantEmpty(sc.t, result.Stderr, strings.Join(args, " ")+" stderr")
		wantContains(sc.t, result.Stdout, "Usage:", strings.Join(args, " ")+" help")
	}
	setHelp := sc.run("secret", "set", "--help")
	wantContains(sc.t, setHelp.Stdout, "--stdin", "secret set help")
	wantNotContains(sc.t, setHelp.Stdout, "--value", "secret set help")
}

func testCLIVersionForms(sc *scenario) {
	flag := sc.run("--version")
	command := sc.run("version")
	wantExit(sc.t, flag, 0)
	wantExit(sc.t, command, 0)
	wantEmpty(sc.t, flag.Stderr, "--version stderr")
	wantEmpty(sc.t, command.Stderr, "version stderr")
	if strings.TrimSpace(flag.Stdout) == "" || flag.Stdout != command.Stdout {
		sc.t.Fatalf("version forms differ: flag=%q command=%q", flag.Stdout, command.Stdout)
	}
	jsonResult := sc.run("--json", "--version")
	wantExit(sc.t, jsonResult, 0)
	got := parseEnvelope(sc.t, jsonResult)
	if !got.OK || got.Command != "version" || got.Error != nil {
		sc.t.Fatalf("unexpected JSON version envelope: %#v", got)
	}
	if version, ok := parseDataMap(sc.t, got)["version"].(string); !ok || version != strings.TrimSpace(flag.Stdout) {
		sc.t.Fatalf("JSON/text version mismatch")
	}
}

func testCLIArgumentErrors(sc *scenario) {
	tests := []struct {
		args    []string
		command string
		code    string
	}{
		{nil, "root", "USAGE"},
		{[]string{"--unknown"}, "env-vault", "USAGE"},
		{[]string{"secret"}, "secret", "USAGE"},
		{[]string{"profile"}, "profile", "USAGE"},
		{[]string{"secret", "check"}, "secret_check", "USAGE"},
		{[]string{"profile", "create"}, "profile_create", "USAGE"},
		{[]string{"exec", sc.suite.helper}, "exec", "USAGE"},
		{[]string{"--json", "--jsonl", "version"}, "version", "USAGE"},
	}
	for _, test := range tests {
		args := append([]string{"--json"}, test.args...)
		result := sc.run(args...)
		wantExit(sc.t, result, 2)
		wantEmpty(sc.t, result.Stderr, "machine-readable error stderr")
		got := parseEnvelope(sc.t, result)
		if got.OK || got.Command != test.command || got.Error == nil || got.Error.Code != test.code {
			sc.t.Fatalf("args=%v unexpected error envelope: %#v", test.args, got)
		}
	}
	human := sc.run("--unknown")
	wantExit(sc.t, human, 2)
	wantEmpty(sc.t, human.Stdout, "human usage stdout")
	wantContains(sc.t, human.Stderr, "code=USAGE\n", "human usage stderr")
}

func testTextOutputContracts(sc *scenario) {
	const (
		secretName  = "text/token"
		profileName = "text-profile"
		envName     = "TEXT_TOKEN"
	)
	secret := sc.sentinels[0]
	fingerprint := sha256Text("env-vault\x00" + secretName)[:16]

	set := sc.runWith(runOptions{stdin: []byte(secret + "\n")}, "secret", "set", secretName, "--stdin")
	wantExit(sc.t, set, 0)
	wantEmpty(sc.t, set.Stderr, "text secret set stderr")
	wantExact(sc.t, set.Stdout, "secret stored: "+secretName+" (fingerprint: "+fingerprint+")\n", "text secret set stdout")

	check := sc.run("secret", "check", secretName)
	wantExit(sc.t, check, 0)
	wantEmpty(sc.t, check.Stderr, "text secret check stderr")
	wantExact(sc.t, check.Stdout, "secret exists: "+secretName+" (fingerprint: "+fingerprint+")\n", "text secret check stdout")

	list := sc.run("secret", "list")
	wantExit(sc.t, list, 0)
	wantEmpty(sc.t, list.Stderr, "text secret list stderr")
	wantExact(sc.t, list.Stdout, secretName+" "+fingerprint+"\n", "text secret list stdout")

	config := filepath.Join(sc.root, "text", "profiles.yaml")
	create := sc.run("--config", config, "profile", "create", profileName)
	wantExit(sc.t, create, 0)
	wantEmpty(sc.t, create.Stderr, "text profile create stderr")
	wantExact(sc.t, create.Stdout, "profile created: "+profileName+" ("+config+")\n", "text profile create stdout")

	add := sc.run("--config", config, "profile", "add", profileName, secretName+":"+envName)
	wantExit(sc.t, add, 0)
	wantEmpty(sc.t, add.Stderr, "text profile add stderr")
	wantExact(sc.t, add.Stdout, "profile updated: "+profileName+" added "+envName+"\n", "text profile add stdout")

	show := sc.run("--config", config, "profile", "show", profileName)
	wantExit(sc.t, show, 0)
	wantEmpty(sc.t, show.Stderr, "text profile show stderr")
	wantExact(sc.t, show.Stdout, "profile: "+profileName+"\n"+secretName+":"+envName+" required=true\n", "text profile show stdout")

	remove := sc.run("--config", config, "profile", "remove", profileName, envName)
	wantExit(sc.t, remove, 0)
	wantEmpty(sc.t, remove.Stderr, "text profile remove stderr")
	wantExact(sc.t, remove.Stdout, "profile updated: "+profileName+" removed "+envName+"\n", "text profile remove stdout")

	deleted := sc.run("secret", "delete", secretName, "--confirm", secretName)
	wantExit(sc.t, deleted, 0)
	wantEmpty(sc.t, deleted.Stderr, "text secret delete stderr")
	wantExact(sc.t, deleted.Stdout, "secret deleted: "+secretName+"\n", "text secret delete stdout")
}

func testSecretLifecycle(sc *scenario) {
	secret := sc.sentinels[0]
	set := sc.runWith(runOptions{stdin: []byte(secret + "\n")}, "--json", "secret", "set", "team/token", "--stdin")
	wantExit(sc.t, set, 0)
	wantEmpty(sc.t, set.Stderr, "secret set stderr")
	if got := parseEnvelope(sc.t, set); !got.OK || got.Command != "secret_set" || got.Error != nil {
		sc.t.Fatalf("unexpected secret set envelope: %#v", got)
	}

	check := sc.run("--json", "secret", "check", "team/token")
	wantExit(sc.t, check, 0)
	wantEmpty(sc.t, check.Stderr, "secret check stderr")
	if got := parseEnvelope(sc.t, check); !got.OK || got.Command != "secret_check" {
		sc.t.Fatalf("unexpected secret check envelope: %#v", got)
	}

	missing := sc.run("--json", "secret", "check", "missing")
	wantExit(sc.t, missing, 3)
	if got := parseEnvelope(sc.t, missing); got.Error == nil || got.Error.Code != "MISSING_SECRET" {
		sc.t.Fatalf("unexpected missing secret envelope: %#v", got)
	}

	list := sc.run("--json", "secret", "list")
	wantExit(sc.t, list, 0)
	var listData struct {
		Secrets []struct {
			Name        string `json:"name"`
			Fingerprint string `json:"fingerprint"`
		} `json:"secrets"`
	}
	gotList := parseEnvelope(sc.t, list)
	if err := json.Unmarshal(gotList.Data, &listData); err != nil || len(listData.Secrets) != 1 || listData.Secrets[0].Name != "team/token" || len(listData.Secrets[0].Fingerprint) != 16 {
		sc.t.Fatalf("unexpected secret list data: %#v err=%v", listData, err)
	}

	confirmation := sc.run("--json", "secret", "delete", "team/token", "--confirm", "wrong")
	wantExit(sc.t, confirmation, 2)
	if got := parseEnvelope(sc.t, confirmation); got.Error == nil || got.Error.Code != "CONFIRMATION_REQUIRED" {
		sc.t.Fatalf("unexpected confirmation error: %#v", got)
	}

	deleted := sc.run("--json", "secret", "delete", "team/token", "--confirm", "team/token")
	wantExit(sc.t, deleted, 0)
	repeated := sc.run("--json", "secret", "delete", "team/token", "--confirm", "team/token")
	wantExit(sc.t, repeated, 3)
	if got := parseEnvelope(sc.t, repeated); got.Error == nil || got.Error.Code != "MISSING_SECRET" {
		sc.t.Fatalf("unexpected repeated-delete result: %#v", got)
	}

	customService := "e2e/custom-service"
	customSet := sc.runWith(runOptions{stdin: []byte(secret + "\n")}, "--json", "secret", "set", "custom-token", "--stdin", "--service", customService)
	wantExit(sc.t, customSet, 0)
	wantExit(sc.t, sc.run("--json", "secret", "check", "custom-token", "--service", customService), 0)
	defaultList := sc.run("--json", "secret", "list")
	wantExit(sc.t, defaultList, 0)
	if err := json.Unmarshal(parseEnvelope(sc.t, defaultList).Data, &listData); err != nil || len(listData.Secrets) != 0 {
		sc.t.Fatalf("custom-service secret escaped into the default-service list: %#v err=%v", listData, err)
	}
	wantExit(sc.t, sc.run("--json", "secret", "delete", "custom-token", "--service", customService, "--confirm", "custom-token"), 0)
}

func testSecretValidationSecurity(sc *scenario) {
	for _, name := range []string{"", "../escape", "/absolute", "a//b", "bad:name", "bad name", `C:\\escape`} {
		args := []string{"--dry-run", "--json", "secret", "set"}
		if name != "" {
			args = append(args, name)
		}
		args = append(args, "--stdin")
		result := sc.run(args...)
		wantExit(sc.t, result, 2)
		if got := parseEnvelope(sc.t, result); got.Error == nil || got.Error.Code != "USAGE" {
			sc.t.Fatalf("invalid secret name %q was not rejected: %#v", name, got)
		}
	}
	for _, service := range []string{"../outside", "/absolute", "a//b", "a/./b", "a/../b", "line\nbreak", `C:\\escape`} {
		result := sc.run("--dry-run", "--json", "secret", "set", "safe-name", "--service", service)
		wantExit(sc.t, result, 2)
		if got := parseEnvelope(sc.t, result); got.Error == nil || got.Error.Code != "USAGE" {
			sc.t.Fatalf("invalid service %q was not rejected: %#v", service, got)
		}
	}
	unsafeFlag := sc.run("--json", "secret", "set", "safe-name", "--value", "not-a-secret-fixture")
	wantExit(sc.t, unsafeFlag, 2)
	wantContains(sc.t, parseEnvelope(sc.t, unsafeFlag).Error.Message, "unknown flag", "unsafe value-channel rejection")
	positionalValue := sc.run("--json", "secret", "set", "safe-name", "PUBLIC_NOT_A_SECRET")
	wantExit(sc.t, positionalValue, 2)
	if got := parseEnvelope(sc.t, positionalValue); got.Error == nil || got.Error.Code != "USAGE" {
		sc.t.Fatalf("positional value channel was not rejected: %#v", got)
	}
	nonInteractive := sc.run("--json", "secret", "set", "safe-name")
	wantExit(sc.t, nonInteractive, 2)
	if got := parseEnvelope(sc.t, nonInteractive); got.Error == nil || got.Error.Code != "USAGE" || !strings.Contains(got.Error.Message, "requires a terminal") {
		sc.t.Fatalf("non-interactive set without --stdin was not rejected: %#v", got)
	}
	if _, err := os.Stat(sc.store); !os.IsNotExist(err) {
		sc.t.Fatalf("validation accessed test backend: %v", err)
	}
}

func testProfileLifecycle(sc *scenario) {
	config := filepath.Join(sc.root, "config", "profiles.yaml")
	create := sc.run("--json", "--config", config, "profile", "create", "dev")
	wantExit(sc.t, create, 0)
	duplicate := sc.run("--json", "--config", config, "profile", "create", "dev")
	wantExit(sc.t, duplicate, 2)
	if got := parseEnvelope(sc.t, duplicate); got.Error == nil || got.Error.Code != "PROFILE_EXISTS" {
		sc.t.Fatalf("unexpected duplicate-create result: %#v", got)
	}
	for _, mapping := range []string{"team/one:TOKEN_ONE", "team/two:TOKEN_TWO", "team/three:TOKEN_THREE"} {
		wantExit(sc.t, sc.run("--json", "--config", config, "profile", "add", "dev", mapping), 0)
	}
	// Adding an identical mapping is idempotent.
	wantExit(sc.t, sc.run("--json", "--config", config, "profile", "add", "dev", "team/one:TOKEN_ONE"), 0)
	show := sc.run("--json", "--config", config, "profile", "show", "dev")
	wantExit(sc.t, show, 0)
	var data struct {
		Profile string `json:"profile"`
		Secrets []struct {
			Name     string `json:"name"`
			Env      string `json:"env"`
			Required string `json:"required"`
		} `json:"secrets"`
	}
	if err := json.Unmarshal(parseEnvelope(sc.t, show).Data, &data); err != nil || data.Profile != "dev" || len(data.Secrets) != 3 {
		sc.t.Fatalf("unexpected profile show: %#v err=%v", data, err)
	}
	wantExit(sc.t, sc.run("--json", "--config", config, "profile", "remove", "dev", "TOKEN_TWO"), 0)
	wantExit(sc.t, sc.run("--json", "--config", config, "profile", "remove", "dev", "team/three:TOKEN_THREE"), 0)
	missing := sc.run("--json", "--config", config, "profile", "remove", "dev", "TOKEN_MISSING")
	wantExit(sc.t, missing, 5)
	if got := parseEnvelope(sc.t, missing); got.Error == nil || got.Error.Code != "CONFIG_INVALID" {
		sc.t.Fatalf("unexpected missing mapping result: %#v", got)
	}
	final := sc.run("--json", "--config", config, "profile", "show", "dev")
	wantExit(sc.t, final, 0)
	if err := json.Unmarshal(parseEnvelope(sc.t, final).Data, &data); err != nil || len(data.Secrets) != 1 || data.Secrets[0].Env != "TOKEN_ONE" {
		sc.t.Fatalf("profile remove did not persist: %#v err=%v", data, err)
	}
}

func testProfileTargetsCheckSecret(sc *scenario) {
	localPath := filepath.Join(sc.root, ".env-vault.yaml")
	wantExit(sc.t, sc.run("--json", "profile", "create", "default-local"), 0)
	wantExit(sc.t, sc.run("--json", "profile", "create", "explicit-local", "--local"), 0)
	if info, err := os.Lstat(localPath); err != nil || !info.Mode().IsRegular() {
		sc.t.Fatalf("default/local config target missing: mode=%v err=%v", infoMode(info), err)
	}

	globalPath := isolatedUserConfigPath(sc)
	wantExit(sc.t, sc.run("--json", "profile", "create", "global-only", "--global"), 0)
	if info, err := os.Lstat(globalPath); err != nil || !info.Mode().IsRegular() {
		sc.t.Fatalf("global config target missing: mode=%v err=%v", infoMode(info), err)
	}
	// Reads prefer a local config when one exists; the explicit path still
	// provides deterministic access to the isolated global file.
	localPrecedence := sc.run("--json", "profile", "show", "global-only")
	wantExit(sc.t, localPrecedence, 2)
	if got := parseEnvelope(sc.t, localPrecedence); got.Error == nil || got.Error.Code != "PROFILE_NOT_FOUND" {
		sc.t.Fatalf("local read precedence changed: %#v", got)
	}
	wantExit(sc.t, sc.run("--json", "--config", globalPath, "profile", "show", "global-only"), 0)

	mutual := sc.run("--json", "profile", "create", "invalid-target", "--local", "--global")
	wantExit(sc.t, mutual, 2)
	if got := parseEnvelope(sc.t, mutual); got.Error == nil || got.Error.Code != "USAGE" {
		sc.t.Fatalf("local/global mutual exclusion changed: %#v", got)
	}
	explicit := filepath.Join(sc.root, "explicit", "config.yaml")
	wantExit(sc.t, sc.run("--json", "--config", explicit, "profile", "create", "checked"), 0)

	// --check-secret must resolve the backend before entering the config lock.
	holder := startLockHolder(sc, explicit+".lock")
	checkStarted := time.Now()
	missing := sc.runWith(runOptions{timeout: 2 * time.Second}, "--json", "--config", explicit, "profile", "add", "checked", "missing:TOKEN_MISSING", "--check-secret")
	checkDuration := time.Since(checkStarted)
	wantExit(sc.t, missing, 3)
	if got := parseEnvelope(sc.t, missing); got.Error == nil || got.Error.Code != "MISSING_SECRET" {
		sc.t.Fatalf("missing --check-secret result: %#v", got)
	}
	if checkDuration >= 2*time.Second {
		sc.t.Fatalf("--check-secret waited on config lock for %s", checkDuration)
	}
	_ = stopLaunched(sc, holder)
	showEmpty := sc.run("--json", "--config", explicit, "profile", "show", "checked")
	wantExit(sc.t, showEmpty, 0)
	if envs := decodeProfileEnvs(sc.t, showEmpty); len(envs) != 0 {
		sc.t.Fatalf("missing --check-secret mutated config: %v", envs)
	}

	setSecret(sc, "existing", sc.sentinels[0])
	wantExit(sc.t, sc.run("--json", "--config", explicit, "profile", "add", "checked", "existing:TOKEN_EXISTING", "--check-secret"), 0)
	showChecked := sc.run("--json", "--config", explicit, "profile", "show", "checked")
	wantExit(sc.t, showChecked, 0)
	if envs := decodeProfileEnvs(sc.t, showChecked); fmt.Sprint(envs) != fmt.Sprint([]string{"TOKEN_EXISTING"}) {
		sc.t.Fatalf("successful --check-secret did not persist mapping: %v", envs)
	}
}

func isolatedUserConfigPath(sc *scenario) string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(sc.home, "Library", "Application Support", "env-vault", "config.yaml")
	case "windows":
		return filepath.Join(sc.appData, "env-vault", "config.yaml")
	default:
		return filepath.Join(sc.configHome, "env-vault", "config.yaml")
	}
}

func testProfileCollisionsPersistence(sc *scenario) {
	config := filepath.Join(sc.root, "persistent.yaml")
	wantExit(sc.t, sc.run("--json", "--config", config, "profile", "create", "dev"), 0)
	wantExit(sc.t, sc.run("--json", "--config", config, "profile", "add", "dev", "first:TOKEN"), 0)
	for _, mapping := range []string{"second:TOKEN", "second:token"} {
		result := sc.run("--json", "--config", config, "profile", "add", "dev", mapping)
		wantExit(sc.t, result, 5)
		if got := parseEnvelope(sc.t, result); got.Error == nil || got.Error.Code != "CONFIG_INVALID" {
			sc.t.Fatalf("collision %q result: %#v", mapping, got)
		}
	}
	// A fresh binary process must observe the persisted profile.
	show := sc.run("--json", "--config", config, "profile", "show", "dev")
	wantExit(sc.t, show, 0)
	data := parseDataMap(sc.t, parseEnvelope(sc.t, show))
	if data["profile"] != "dev" {
		sc.t.Fatalf("profile persistence failed: %#v", data)
	}
	missing := sc.run("--json", "--config", config, "profile", "show", "absent")
	wantExit(sc.t, missing, 2)
	if got := parseEnvelope(sc.t, missing); got.Error == nil || got.Error.Code != "PROFILE_NOT_FOUND" {
		sc.t.Fatalf("unexpected missing profile result: %#v", got)
	}
}

func testProfileAtomicPermissions(sc *scenario) {
	config := filepath.Join(sc.root, "nested", "config.yaml")
	wantExit(sc.t, sc.run("--json", "--config", config, "profile", "create", "dev"), 0)
	wantExit(sc.t, sc.run("--json", "--config", config, "profile", "add", "dev", "token:TOKEN"), 0)
	for _, path := range []string{config, config + ".lock"} {
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			sc.t.Fatalf("expected regular config artifact %s: mode=%v err=%v", filepath.Base(path), infoMode(info), err)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
			sc.t.Fatalf("%s permissions=%#o, want 0600", filepath.Base(path), info.Mode().Perm())
		}
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(config), ".config.yaml.tmp-*"))
	if err != nil || len(matches) != 0 {
		sc.t.Fatalf("atomic temp files remain: %v err=%v", matches, err)
	}
	// Successful public read proves the atomically replaced YAML is intact.
	wantExit(sc.t, sc.run("--json", "--config", config, "profile", "show", "dev"), 0)
	data := sc.scanFile(config, "saved config")
	wantNotContains(sc.t, string(data), sentinelPrefix, "saved config")
}

func infoMode(info os.FileInfo) os.FileMode {
	if info == nil {
		return 0
	}
	return info.Mode()
}

func testProfileSymlinkRejected(sc *scenario) {
	if runtime.GOOS == "windows" {
		sc.t.Skip("expected platform skip: symlink creation is not guaranteed on Windows runners")
	}
	target := filepath.Join(sc.root, "target.yaml")
	if err := os.WriteFile(target, []byte("version: 1\nprofiles: {}\n"), 0o600); err != nil {
		sc.t.Fatal(err)
	}
	link := filepath.Join(sc.root, "config.yaml")
	if err := os.Symlink(target, link); err != nil {
		sc.t.Fatalf("create config symlink: %v", err)
	}
	result := sc.run("--json", "--config", link, "profile", "create", "dev")
	wantExit(sc.t, result, 5)
	if got := parseEnvelope(sc.t, result); got.Error == nil || got.Error.Code != "CONFIG_INVALID" {
		sc.t.Fatalf("unexpected symlink rejection: %#v", got)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "version: 1\nprofiles: {}\n" {
		sc.t.Fatalf("symlink target was modified: %q err=%v", data, err)
	}

	lockConfig := filepath.Join(sc.root, "safe.yaml")
	lockTarget := filepath.Join(sc.root, "lock-target")
	if err := os.WriteFile(lockTarget, nil, 0o600); err != nil {
		sc.t.Fatal(err)
	}
	if err := os.Symlink(lockTarget, lockConfig+".lock"); err != nil {
		sc.t.Fatal(err)
	}
	lockResult := sc.run("--json", "--config", lockConfig, "profile", "create", "dev")
	wantExit(sc.t, lockResult, 5)
}

func testExecProfileDirectMulti(sc *scenario) {
	first := sc.sentinels[0]
	second := sc.newSentinel()
	setSecret(sc, "first", first)
	setSecret(sc, "second", second)
	config := filepath.Join(sc.root, "exec.yaml")
	wantExit(sc.t, sc.run("--json", "--config", config, "profile", "create", "dev"), 0)
	wantExit(sc.t, sc.run("--json", "--config", config, "profile", "add", "dev", "first:TOKEN_ONE"), 0)

	profile := sc.run("--config", config, "exec", "dev", "--", sc.suite.helper, "env", "--expect-hash", "TOKEN_ONE="+sha256Text(first))
	wantExit(sc.t, profile, 0)
	if profile.Stdout != "env-ok\n" || profile.Stderr != "" {
		sc.t.Fatalf("profile exec streams: stdout=%q stderr=%q", profile.Stdout, profile.Stderr)
	}

	direct := sc.run("exec", "--secret", "first:TOKEN_ONE", "--secret", "second:TOKEN_TWO", "--", sc.suite.helper, "env",
		"--expect-hash", "TOKEN_ONE="+sha256Text(first), "--expect-hash", "TOKEN_TWO="+sha256Text(second))
	wantExit(sc.t, direct, 0)
	wantContains(sc.t, direct.Stdout, "env-ok", "direct multi-secret exec")

	combined := sc.run("--config", config, "exec", "dev", "--secret", "second:TOKEN_TWO", "--", sc.suite.helper, "env",
		"--expect-hash", "TOKEN_ONE="+sha256Text(first), "--expect-hash", "TOKEN_TWO="+sha256Text(second))
	wantExit(sc.t, combined, 0)
}

func testExecEnvModes(sc *scenario) {
	secret := sc.sentinels[0]
	setSecret(sc, "token", secret)
	base := runOptions{env: map[string]string{"TOKEN": "public-existing", "E2E_INHERITED": "visible"}}
	collision := sc.runWith(base, "--json", "exec", "--secret", "token:TOKEN", "--", sc.suite.helper, "env", "--expect-hash", "TOKEN="+sha256Text(secret))
	wantExit(sc.t, collision, 2)
	if got := parseEnvelope(sc.t, collision); got.Error == nil || got.Error.Code != "ENV_COLLISION" {
		sc.t.Fatalf("unexpected env collision: %#v", got)
	}
	override := sc.runWith(base, "exec", "--override-env", "--secret", "token:TOKEN", "--", sc.suite.helper, "env",
		"--expect-hash", "TOKEN="+sha256Text(secret), "--expect-value", "E2E_INHERITED=visible")
	wantExit(sc.t, override, 0)
	clean := sc.runWith(base, "exec", "--clean-env", "--override-env", "--secret", "token:TOKEN", "--", sc.suite.helper, "env",
		"--expect-hash", "TOKEN="+sha256Text(secret), "--expect-absent", "E2E_INHERITED")
	wantExit(sc.t, clean, 0)
}

func testExecArgStreamExit(sc *scenario) {
	args := []string{"plain", "two words", `quote"inside`, "dollar$and;semi", "unicode-Привет", "back\\slash"}
	command := append([]string{"exec", "--", sc.suite.helper, "args"}, args...)
	result := sc.run(command...)
	wantExit(sc.t, result, 0)
	wantEmpty(sc.t, result.Stderr, "args helper stderr")
	var got struct {
		Args []string `json:"args"`
	}
	if err := json.Unmarshal([]byte(result.Stdout), &got); err != nil {
		sc.t.Fatalf("decode helper args: %v", err)
	}
	if fmt.Sprint(got.Args) != fmt.Sprint(args) {
		sc.t.Fatalf("argv changed: got=%q want=%q", got.Args, args)
	}

	stdin := []byte("portable stdin payload\nwith a second line\n")
	streams := sc.runWith(runOptions{stdin: stdin}, "exec", "--", sc.suite.helper, "streams",
		"--stdin-sha256", sha256Text(string(stdin)), "--stdout", "child-stdout\r\n", "--stderr", "child-stderr\r\n", "--exit-code", "17")
	wantExit(sc.t, streams, 17)
	if streams.Stdout != "child-stdout\r\n" || streams.Stderr != "child-stderr\r\n" {
		sc.t.Fatalf("child stream passthrough changed bytes: stdout=%q stderr=%q", streams.Stdout, streams.Stderr)
	}
}

func testExecMissingSecretNoChild(sc *scenario) {
	marker := filepath.Join(sc.root, "child-started")
	missing := sc.run("--json", "exec", "--secret", "missing:TOKEN", "--", sc.suite.helper, "marker", "--path", marker)
	wantExit(sc.t, missing, 3)
	if got := parseEnvelope(sc.t, missing); got.Error == nil || got.Error.Code != "MISSING_SECRET" {
		sc.t.Fatalf("unexpected missing-secret exec result: %#v", got)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		sc.t.Fatalf("child ran despite resolution error: %v", err)
	}
	notFound := sc.run("--json", "exec", "--", "env-vault-e2e-command-that-does-not-exist")
	wantExit(sc.t, notFound, 127)
	if got := parseEnvelope(sc.t, notFound); got.Error == nil || got.Error.Code != "COMMAND_NOT_FOUND" {
		sc.t.Fatalf("unexpected command-not-found result: %#v", got)
	}
	file := filepath.Join(sc.root, "not-executable")
	if err := os.WriteFile(file, []byte("not an executable"), 0o600); err != nil {
		sc.t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		notExecutable := sc.run("--json", "exec", "--", file)
		wantExit(sc.t, notExecutable, 126)
		if got := parseEnvelope(sc.t, notExecutable); got.Error == nil || got.Error.Code != "COMMAND_NOT_EXECUTABLE" {
			sc.t.Fatalf("unexpected non-executable result: %#v", got)
		}
	}
}

func testDryRunNoSideEffects(sc *scenario) {
	secret := sc.sentinels[0]
	drySecret := sc.runWith(runOptions{stdin: []byte(secret + "\n")}, "--dry-run", "--json", "secret", "set", "dry-token", "--stdin")
	wantExit(sc.t, drySecret, 0)
	if data := parseDataMap(sc.t, parseEnvelope(sc.t, drySecret)); data["dry_run"] != true {
		sc.t.Fatalf("secret dry-run metadata: %#v", data)
	}
	if _, err := os.Stat(sc.store); !os.IsNotExist(err) {
		sc.t.Fatalf("secret dry-run created store: %v", err)
	}
	jsonlMeta := filepath.Join(sc.root, "dry-secret-jsonl.json")
	dryJSONL := sc.runWith(runOptions{stdin: []byte(secret + "\n")}, "--dry-run", "--jsonl", "--output", jsonlMeta, "secret", "set", "dry-jsonl-token", "--stdin")
	wantExit(sc.t, dryJSONL, 0)
	jsonlEnvelope := parseEnvelope(sc.t, dryJSONL)
	if !jsonlEnvelope.OK || parseDataMap(sc.t, jsonlEnvelope)["dry_run"] != true {
		sc.t.Fatalf("invalid dry-run JSONL metadata: %#v", jsonlEnvelope)
	}
	metadataFile := sc.scanFile(jsonlMeta, "dry-run JSONL output metadata")
	metadataEnvelope := parseEnvelopeBytes(sc.t, metadataFile, "dry-run JSONL output file")
	if !metadataEnvelope.OK || parseDataMap(sc.t, metadataEnvelope)["dry_run"] != true {
		sc.t.Fatalf("invalid dry-run JSONL output file: %#v", metadataEnvelope)
	}
	if _, err := os.Stat(sc.store); !os.IsNotExist(err) {
		sc.t.Fatalf("secret JSONL dry-run created store: %v", err)
	}
	config := filepath.Join(sc.root, "dry.yaml")
	dryProfile := sc.run("--dry-run", "--json", "--config", config, "profile", "create", "dev")
	wantExit(sc.t, dryProfile, 0)
	for _, path := range []string{config, config + ".lock"} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			sc.t.Fatalf("profile dry-run created %s: %v", filepath.Base(path), err)
		}
	}

	setSecret(sc, "token", secret)
	wantExit(sc.t, sc.run("--json", "--config", config, "profile", "create", "dev"), 0)
	wantExit(sc.t, sc.run("--json", "--config", config, "profile", "add", "dev", "token:TOKEN"), 0)
	storeBefore := fileSHA256(sc.t, sc.store)
	configBefore := fileSHA256(sc.t, config)
	wantExit(sc.t, sc.run("--dry-run", "--json", "secret", "delete", "token", "--confirm", "token"), 0)
	wantExit(sc.t, sc.run("--json", "secret", "check", "token"), 0)
	wantExit(sc.t, sc.run("--dry-run", "--json", "--config", config, "profile", "add", "dev", "other:OTHER"), 0)
	wantExit(sc.t, sc.run("--dry-run", "--json", "--config", config, "profile", "remove", "dev", "TOKEN"), 0)
	if fileSHA256(sc.t, sc.store) != storeBefore || fileSHA256(sc.t, config) != configBefore {
		sc.t.Fatal("dry-run delete/add/remove changed the existing store or config")
	}
	unchanged := sc.run("--json", "--config", config, "profile", "show", "dev")
	wantExit(sc.t, unchanged, 0)
	if envs := decodeProfileEnvs(sc.t, unchanged); fmt.Sprint(envs) != fmt.Sprint([]string{"TOKEN"}) {
		sc.t.Fatalf("dry-run changed mappings: %v", envs)
	}
	marker := filepath.Join(sc.root, "dry-child")
	meta := filepath.Join(sc.root, "dry-meta.json")
	dryExec := sc.run("--dry-run", "--json", "--output", meta, "--config", config, "exec", "dev", "--", sc.suite.helper, "marker", "--path", marker)
	wantExit(sc.t, dryExec, 0)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		sc.t.Fatalf("exec dry-run launched child: %v", err)
	}
	metadata := sc.scanFile(meta, "dry-run metadata")
	got := parseEnvelopeBytes(sc.t, metadata, "dry-run metadata file")
	if parseDataMap(sc.t, got)["dry_run"] != true {
		sc.t.Fatalf("invalid dry-run metadata: %#v", got)
	}
}

func testOutputJSONJSONLFile(sc *scenario) {
	secret := sc.sentinels[0]
	setSecret(sc, "token", secret)
	jsonResult := sc.run("--json", "secret", "check", "token")
	wantExit(sc.t, jsonResult, 0)
	wantEmpty(sc.t, jsonResult.Stderr, "JSON stderr")
	if got := parseEnvelope(sc.t, jsonResult); !got.OK || got.Error != nil {
		sc.t.Fatalf("invalid JSON contract: %#v", got)
	}
	jsonlResult := sc.run("--jsonl", "secret", "check", "token")
	wantExit(sc.t, jsonlResult, 0)
	wantEmpty(sc.t, jsonlResult.Stderr, "JSONL stderr")
	event := parseEnvelope(sc.t, jsonlResult)
	if !event.OK {
		sc.t.Fatalf("decode JSONL event: %#v", event)
	}
	output := filepath.Join(sc.root, "metadata", "check.json")
	fileResult := sc.run("--quiet", "--output", output, "secret", "check", "token")
	wantExit(sc.t, fileResult, 0)
	wantEmpty(sc.t, fileResult.Stdout, "quiet stdout")
	wantEmpty(sc.t, fileResult.Stderr, "quiet stderr")
	fileData := sc.scanFile(output, "output metadata")
	fileEnvelope := parseEnvelopeBytes(sc.t, fileData, "output metadata file")
	if !fileEnvelope.OK || fileEnvelope.Command != "secret_check" {
		sc.t.Fatalf("invalid output file envelope: %#v", fileEnvelope)
	}
	if info, err := os.Stat(output); err != nil || runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		sc.t.Fatalf("output permissions: mode=%v err=%v", infoMode(info), err)
	}
	execOutput := filepath.Join(sc.root, "metadata", "exec.json")
	execResult := sc.run("--quiet", "--output", execOutput, "exec", "--secret", "token:TOKEN", "--", sc.suite.helper, "streams", "--stdout", "exec-child-stdout\n", "--stderr", "exec-child-stderr\n")
	wantExit(sc.t, execResult, 0)
	if execResult.Stdout != "exec-child-stdout\n" || execResult.Stderr != "exec-child-stderr\n" {
		sc.t.Fatalf("exec output-file streams mixed with metadata: stdout=%q stderr=%q", execResult.Stdout, execResult.Stderr)
	}
	execEnvelope := parseEnvelopeBytes(sc.t, sc.scanFile(execOutput, "exec output metadata"), "exec output metadata")
	if !execEnvelope.OK || execEnvelope.Command != "exec" {
		sc.t.Fatalf("invalid exec output metadata: %#v", execEnvelope)
	}

	// A successful command whose metadata cannot be written is a runtime
	// failure. Machine output remains isolated on stdout; diagnostics are
	// opt-in through --verbose.
	badOutput := sc.root // An existing directory cannot be opened as a file.
	runtimeFailure := sc.run("--json", "--output", badOutput, "version")
	wantExit(sc.t, runtimeFailure, 1)
	wantEmpty(sc.t, runtimeFailure.Stderr, "non-verbose runtime error stderr")
	gotRuntime := parseEnvelope(sc.t, runtimeFailure)
	if gotRuntime.OK || gotRuntime.Command != "root" || gotRuntime.Error == nil || gotRuntime.Error.Code != "RUNTIME_ERROR" || gotRuntime.Error.Message != "Unexpected runtime error" {
		sc.t.Fatalf("output-write runtime contract changed: %#v", gotRuntime)
	}

	verboseOutput := filepath.Join(sc.root, "verbose-output-directory")
	if err := os.Mkdir(verboseOutput, 0o700); err != nil {
		sc.t.Fatal(err)
	}
	verboseRuntime := sc.run("--verbose", "--json", "--output", verboseOutput, "version")
	wantExit(sc.t, verboseRuntime, 1)
	gotVerboseRuntime := parseEnvelope(sc.t, verboseRuntime)
	if gotVerboseRuntime.OK || gotVerboseRuntime.Command != "root" || gotVerboseRuntime.Error == nil || gotVerboseRuntime.Error.Code != "RUNTIME_ERROR" {
		sc.t.Fatalf("verbose output-write runtime contract changed: %#v", gotVerboseRuntime)
	}
	wantContains(sc.t, verboseRuntime.Stderr, "OUTPUT_WRITE_FAILED:", "verbose runtime diagnostic")
	wantNotContains(sc.t, verboseRuntime.Stderr, `"code":"RUNTIME_ERROR"`, "verbose runtime stderr")

	verbose := sc.run("--verbose", "--json", "--output", badOutput, "secret", "check", "missing")
	wantExit(sc.t, verbose, 3)
	if got := parseEnvelope(sc.t, verbose); got.Error == nil || got.Error.Code != "MISSING_SECRET" {
		sc.t.Fatalf("verbose error lost original contract: %#v", got)
	}
	wantContains(sc.t, verbose.Stderr, "OUTPUT_WRITE_FAILED:", "verbose diagnostic")
	errorResult := sc.run("--json", "secret", "check", "missing")
	wantExit(sc.t, errorResult, 3)
	wantEmpty(sc.t, errorResult.Stderr, "machine error stderr")
	if got := parseEnvelope(sc.t, errorResult); got.OK || got.Error == nil || got.Error.Code != "MISSING_SECRET" {
		sc.t.Fatalf("invalid structured error: %#v", got)
	}
}

func testDoctorBackends(sc *scenario) {
	secret := sc.sentinels[0]
	healthy := sc.run("--json", "doctor")
	wantExit(sc.t, healthy, 0)
	gotHealthy := parseEnvelope(sc.t, healthy)
	data := parseDataMap(sc.t, gotHealthy)
	if !gotHealthy.OK || data["backend"] != "test" || data["test_backend"] != true || len(gotHealthy.Warnings) != 0 {
		sc.t.Fatalf("unexpected healthy doctor result: data=%#v warnings=%#v", data, gotHealthy.Warnings)
	}
	human := sc.run("doctor")
	wantExit(sc.t, human, 0)
	wantContains(sc.t, human.Stdout, "doctor: ok\n", "doctor text")

	requestedNotAllowed := sc.runWith(runOptions{unset: []string{"ENV_VAULT_ALLOW_INSECURE_TEST_BACKEND"}}, "--json", "doctor")
	wantExit(sc.t, requestedNotAllowed, 0)
	gotRequested := parseEnvelope(sc.t, requestedNotAllowed)
	if len(gotRequested.Warnings) == 0 || parseDataMap(sc.t, gotRequested)["test_backend"] != false {
		sc.t.Fatalf("doctor did not report disallowed test backend: %#v", gotRequested)
	}
	requestedText := sc.runWith(runOptions{unset: []string{"ENV_VAULT_ALLOW_INSECURE_TEST_BACKEND"}}, "doctor")
	wantExit(sc.t, requestedText, 0)
	wantEmpty(sc.t, requestedText.Stderr, "disallowed test backend doctor stderr")
	if requestedText.Stdout != "doctor: ok\nwarning: BACKEND_UNAVAILABLE: Insecure test backend is not explicitly allowed\n" {
		sc.t.Fatalf("disallowed test backend doctor text changed: %q", requestedText.Stdout)
	}

	unsupported := sc.runWith(runOptions{env: map[string]string{"ENV_VAULT_BACKEND": "unsupported"}, unset: []string{"ENV_VAULT_ALLOW_INSECURE_TEST_BACKEND", "ENV_VAULT_TEST_STORE"}}, "--json", "doctor")
	wantExit(sc.t, unsupported, 0)
	gotUnsupported := parseEnvelope(sc.t, unsupported)
	if len(gotUnsupported.Warnings) == 0 {
		sc.t.Fatalf("doctor omitted backend-unavailable warning: %#v", gotUnsupported)
	}
	for _, warning := range gotUnsupported.Warnings {
		wantNotContains(sc.t, warning, secret, "doctor warning")
	}
	unsupportedText := sc.runWith(runOptions{env: map[string]string{"ENV_VAULT_BACKEND": "unsupported"}, unset: []string{"ENV_VAULT_ALLOW_INSECURE_TEST_BACKEND", "ENV_VAULT_TEST_STORE"}}, "doctor")
	wantExit(sc.t, unsupportedText, 0)
	wantEmpty(sc.t, unsupportedText.Stderr, "unsupported backend doctor stderr")
	if unsupportedText.Stdout != "doctor: ok\nwarning: BACKEND_UNAVAILABLE: Unsupported secret backend requested\n" {
		sc.t.Fatalf("unsupported backend doctor text changed: %q", unsupportedText.Stdout)
	}

	// Keeping ENV_VAULT_BACKEND=test while removing one of its mandatory
	// gates forces a safe failure in teststore.NewFromEnv. It cannot fall back
	// to a platform keyring because the test backend remains explicitly
	// requested.
	backendUnavailable := sc.runWith(runOptions{unset: []string{"ENV_VAULT_ALLOW_INSECURE_TEST_BACKEND"}}, "--json", "secret", "check", "unavailable")
	wantExit(sc.t, backendUnavailable, 4)
	wantEmpty(sc.t, backendUnavailable.Stderr, "backend-unavailable machine stderr")
	gotUnavailable := parseEnvelope(sc.t, backendUnavailable)
	if gotUnavailable.OK || gotUnavailable.Command != "secret_check" || gotUnavailable.Error == nil || gotUnavailable.Error.Code != "BACKEND_UNAVAILABLE" {
		sc.t.Fatalf("unexpected backend-unavailable contract: %#v", gotUnavailable)
	}
	if _, err := os.Stat(sc.store); !os.IsNotExist(err) {
		sc.t.Fatalf("incomplete test backend gate touched the store: %v", err)
	}
}

func setSecret(sc *scenario, name, value string) {
	sc.t.Helper()
	result := sc.runWith(runOptions{stdin: []byte(value + "\n")}, "--json", "secret", "set", name, "--stdin")
	wantExit(sc.t, result, 0)
}

func sortedStrings(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}

func decodeProfileEnvs(t *testing.T, result commandResult) []string {
	t.Helper()
	var data struct {
		Secrets []struct {
			Env string `json:"env"`
		} `json:"secrets"`
	}
	if err := json.Unmarshal(parseEnvelope(t, result).Data, &data); err != nil {
		t.Fatalf("decode profile: %v", err)
	}
	envs := make([]string, 0, len(data.Secrets))
	for _, secret := range data.Secrets {
		envs = append(envs, secret.Env)
	}
	return sortedStrings(envs)
}

func wantExact(t *testing.T, got, want, label string) {
	t.Helper()
	if got != want {
		t.Fatalf("%s=%q, want %q", label, got, want)
	}
}
