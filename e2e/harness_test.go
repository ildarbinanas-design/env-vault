package e2e_test

import (
	"bytes"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	mathrand "math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	binaryEnv           = "ENV_VAULT_E2E_BINARY"
	versionEnv          = "ENV_VAULT_E2E_VERSION"
	helperEnv           = "ENV_VAULT_E2E_HELPER"
	contractsDirEnv     = "ENV_VAULT_E2E_CONTRACTS_DIR"
	sentinelRegistryEnv = "ENV_VAULT_E2E_SENTINEL_REGISTRY"
	shuffleScenariosEnv = "ENV_VAULT_E2E_SHUFFLE_SCENARIOS"
	sentinelPrefix      = "ENV_VAULT_E2E_SENTINEL_"
	defaultTimeout      = 10 * time.Second
)

var registryMu sync.Mutex

type suite struct {
	binary      string
	version     string
	helper      string
	root        string
	contracts   string
	registry    string
	passthrough map[string]string
}

type scenario struct {
	t             *testing.T
	suite         *suite
	id            string
	root          string
	home          string
	configHome    string
	appData       string
	profile       string
	tmp           string
	storeRoot     string
	store         string
	sentinels     []string
	observations  []observation
	contractMutex sync.Mutex
}

type runOptions struct {
	stdin   []byte
	env     map[string]string
	unset   []string
	cwd     string
	timeout time.Duration
}

type commandResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
	TimedOut bool
}

type observation struct {
	Ordinal  int      `json:"ordinal"`
	Args     []string `json:"args"`
	ExitCode int      `json:"exit_code"`
	Stdout   string   `json:"stdout"`
	Stderr   string   `json:"stderr"`
	TimedOut bool     `json:"timed_out"`
}

type contractFile struct {
	SchemaVersion int           `json:"schema_version"`
	ScenarioID    string        `json:"scenario_id"`
	Observations  []observation `json:"observations"`
}

type envelope struct {
	OK        bool            `json:"ok"`
	Command   string          `json:"command"`
	Timestamp string          `json:"timestamp"`
	Data      json.RawMessage `json:"data"`
	Warnings  []string        `json:"warnings"`
	Error     *errorObject    `json:"error"`
}

type errorObject struct {
	Code        string `json:"code"`
	Message     string `json:"message"`
	Remediation string `json:"remediation"`
}

func newSuite(t *testing.T) *suite {
	t.Helper()
	binary := os.Getenv(binaryEnv)
	if binary == "" {
		t.Skip(binaryEnv + " is not set; binary-only E2E suite is run by the E2E runner")
	}
	binary = requireExecutable(t, binary, binaryEnv)

	root := t.TempDir()
	helper := os.Getenv(helperEnv)
	if helper == "" {
		helper = filepath.Join(root, executableName("env-vault-e2e-testhelper"))
		buildHelper(t, helper)
	} else {
		helper = requireExecutable(t, helper, helperEnv)
	}

	passthrough := map[string]string{}
	for _, name := range []string{
		"PATH", "PATHEXT", "SYSTEMROOT", "SystemRoot", "WINDIR", "COMSPEC",
		"LD_LIBRARY_PATH", "DYLD_LIBRARY_PATH", "GOCOVERDIR",
	} {
		if value, ok := os.LookupEnv(name); ok {
			passthrough[name] = value
		}
	}
	return &suite{
		binary:      binary,
		version:     os.Getenv(versionEnv),
		helper:      helper,
		root:        root,
		contracts:   os.Getenv(contractsDirEnv),
		registry:    os.Getenv(sentinelRegistryEnv),
		passthrough: passthrough,
	}
}

func buildHelper(t *testing.T, target string) {
	t.Helper()
	cmd := exec.Command("go", "build", "-trimpath", "-o", target, "./testhelper")
	configureProcess(cmd)
	cmd.Dir = packageDir(t)
	cmd.Env = os.Environ()
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Start(); err != nil {
		t.Fatalf("start portable E2E helper build: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timer := time.NewTimer(defaultTimeout * 3)
	defer timer.Stop()
	var err error
	select {
	case err = <-done:
	case <-timer.C:
		_ = terminateProcessTree(cmd)
		cleanupTimer := time.NewTimer(5 * time.Second)
		defer cleanupTimer.Stop()
		select {
		case <-done:
			t.Fatalf("build portable E2E helper timed out")
		case <-cleanupTimer.C:
			t.Fatalf("portable E2E helper build process tree did not exit within 5s after timeout termination")
		}
	}
	if err != nil {
		t.Fatalf("build portable E2E helper: %v\n%s", err, output.Bytes())
	}
}

func packageDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve e2e package directory")
	}
	return filepath.Dir(file)
}

func requireExecutable(t *testing.T, path, label string) string {
	t.Helper()
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatalf("resolve %s: %v", label, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		t.Fatalf("stat %s: %v", label, err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("%s is not a regular file", label)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("%s is not executable", label)
	}
	return abs
}

func executableName(base string) string {
	if runtime.GOOS == "windows" {
		return base + ".exe"
	}
	return base
}

func newScenario(t *testing.T, s *suite, id string) *scenario {
	t.Helper()
	root := t.TempDir()
	sc := &scenario{
		t:          t,
		suite:      s,
		id:         id,
		root:       root,
		home:       filepath.Join(root, "home"),
		configHome: filepath.Join(root, "xdg-config"),
		appData:    filepath.Join(root, "appdata"),
		profile:    filepath.Join(root, "userprofile"),
		tmp:        filepath.Join(root, "tmp"),
	}
	// The child receives TMP/TEMP/TMPDIR=sc.tmp, so the explicitly gated test
	// backend must also live below that child-visible os.TempDir().
	sc.storeRoot = filepath.Join(sc.tmp, "backend")
	sc.store = filepath.Join(sc.storeRoot, "store.gob")
	for _, dir := range []string{sc.home, sc.configHome, sc.appData, sc.profile, sc.tmp} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("create isolated directory: %v", err)
		}
	}
	_ = sc.newSentinel()
	t.Cleanup(func() { sc.scanSavedFiles() })
	return sc
}

func (sc *scenario) newSentinel() string {
	sc.t.Helper()
	random := make([]byte, 24)
	if _, err := cryptorand.Read(random); err != nil {
		sc.t.Fatalf("generate sentinel: %v", err)
	}
	value := sentinelPrefix + hex.EncodeToString(random)
	sc.sentinels = append(sc.sentinels, value)
	if sc.suite.registry != "" {
		sum := sha256.Sum256([]byte(value))
		entry := struct {
			SchemaVersion int    `json:"schema_version"`
			ScenarioID    string `json:"scenario_id"`
			SHA256        string `json:"sha256"`
		}{1, sc.id, hex.EncodeToString(sum[:])}
		data, err := json.Marshal(entry)
		if err != nil {
			sc.t.Fatalf("encode sentinel registry entry: %v", err)
		}
		registryMu.Lock()
		err = appendPrivateFile(sc.suite.registry, append(data, '\n'))
		registryMu.Unlock()
		if err != nil {
			sc.t.Fatalf("write sentinel registry: %v", err)
		}
	}
	return value
}

func appendPrivateFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Write(data); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

func (sc *scenario) baseEnv(extra map[string]string, unset []string) []string {
	values := make(map[string]string, len(sc.suite.passthrough)+16+len(extra))
	for key, value := range sc.suite.passthrough {
		values[key] = value
	}
	for key, value := range map[string]string{
		"HOME":                                  sc.home,
		"XDG_CONFIG_HOME":                       sc.configHome,
		"APPDATA":                               sc.appData,
		"USERPROFILE":                           sc.profile,
		"TMPDIR":                                sc.tmp,
		"TMP":                                   sc.tmp,
		"TEMP":                                  sc.tmp,
		"ENV_VAULT_BACKEND":                     "test",
		"ENV_VAULT_ALLOW_INSECURE_TEST_BACKEND": "1",
		"ENV_VAULT_TEST_STORE":                  sc.store,
		"ENV_VAULT_E2E_CHILD_MARKER":            "public-marker",
	} {
		values[key] = value
	}
	for key, value := range extra {
		values[key] = value
	}
	for _, key := range unset {
		for existing := range values {
			if strings.EqualFold(existing, key) {
				delete(values, existing)
			}
		}
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	env := make([]string, 0, len(keys))
	for _, key := range keys {
		env = append(env, key+"="+values[key])
	}
	return env
}

func (sc *scenario) run(args ...string) commandResult {
	return sc.runWith(runOptions{}, args...)
}

func (sc *scenario) runWith(options runOptions, args ...string) commandResult {
	sc.t.Helper()
	if options.timeout <= 0 {
		options.timeout = defaultTimeout
	}
	if options.cwd == "" {
		options.cwd = sc.root
	}
	cmd := exec.Command(sc.suite.binary, args...)
	configureProcess(cmd)
	cmd.Dir = options.cwd
	cmd.Env = sc.baseEnv(options.env, options.unset)
	cmd.Stdin = bytes.NewReader(options.stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		sc.t.Fatalf("start env-vault binary: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timer := time.NewTimer(options.timeout)
	defer timer.Stop()
	var waitErr error
	timedOut := false
	select {
	case waitErr = <-done:
	case <-timer.C:
		timedOut = true
		_ = terminateProcessTree(cmd)
		cleanupTimer := time.NewTimer(5 * time.Second)
		defer cleanupTimer.Stop()
		select {
		case waitErr = <-done:
		case <-cleanupTimer.C:
			sc.t.Fatalf("env-vault process tree did not exit within 5s after timeout termination")
		}
	}
	result := commandResult{
		ExitCode: exitCode(cmd, waitErr),
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		TimedOut: timedOut,
	}
	sc.assertNoSentinel("stdout", []byte(result.Stdout))
	sc.assertNoSentinel("stderr", []byte(result.Stderr))
	sc.recordContract(args, result)
	return result
}

func exitCode(cmd *exec.Cmd, waitErr error) int {
	if waitErr == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) {
		return exitErr.ExitCode()
	}
	if cmd.ProcessState != nil {
		return cmd.ProcessState.ExitCode()
	}
	return -1
}

func (sc *scenario) recordContract(args []string, result commandResult) {
	sc.t.Helper()
	if sc.suite.contracts == "" {
		return
	}
	sc.contractMutex.Lock()
	defer sc.contractMutex.Unlock()
	observation := observation{
		Ordinal:  len(sc.observations) + 1,
		Args:     sc.normalizeArgs(args),
		ExitCode: result.ExitCode,
		Stdout:   sc.normalizeText(result.Stdout),
		Stderr:   sc.normalizeText(result.Stderr),
		TimedOut: result.TimedOut,
	}
	sc.observations = append(sc.observations, observation)
	contract := contractFile{SchemaVersion: 1, ScenarioID: sc.id, Observations: sc.observations}
	data, err := json.MarshalIndent(contract, "", "  ")
	if err != nil {
		sc.t.Fatalf("encode scenario contract: %v", err)
	}
	data = append(data, '\n')
	sc.assertNoSentinel("normalized contract", data)
	if err := os.MkdirAll(sc.suite.contracts, 0o700); err != nil {
		sc.t.Fatalf("create contracts directory: %v", err)
	}
	path := filepath.Join(sc.suite.contracts, sc.id+".json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		sc.t.Fatalf("write scenario contract: %v", err)
	}
}

func (sc *scenario) normalizeArgs(args []string) []string {
	out := make([]string, len(args))
	for index, arg := range args {
		out[index] = sc.normalizeText(arg)
	}
	return out
}

var (
	timestampPattern = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?Z`)
	versionPattern   = regexp.MustCompile(`\b(?:dev-[0-9a-f]{7,40}(?:-dirty)?|ci-[0-9a-f]{40})\b`)
)

func (sc *scenario) normalizeText(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	trimmed := strings.TrimSpace(value)
	if strings.HasPrefix(trimmed, "{") {
		var decoded any
		if json.Unmarshal([]byte(trimmed), &decoded) == nil {
			decoded = sc.normalizeJSONValue(decoded, "")
			if data, err := json.Marshal(decoded); err == nil {
				if strings.HasSuffix(value, "\n") {
					return string(data) + "\n"
				}
				return string(data)
			}
		}
	}
	return sc.normalizeScalarText(value)
}

func (sc *scenario) normalizeScalarText(value string) string {
	// The helper receives hashes rather than secret values, but those hashes are
	// still derived from each run's random sentinel. Normalize them explicitly
	// so baseline/candidate contracts compare public behavior instead of random
	// fixture identity. Do not normalize the sentinel itself: any raw sentinel
	// reaching a contract must continue to fail the leak assertion below.
	for index, sentinel := range sc.sentinels {
		value = strings.ReplaceAll(value, sha256Text(sentinel), fmt.Sprintf("<SENTINEL_SHA256_%d>", index+1))
	}
	replacements := [][2]string{
		{sc.store, "<TEST_STORE>"},
		{sc.storeRoot, "<TEST_STORE_ROOT>"},
		{sc.suite.binary, "<ENV_VAULT_BINARY>"},
		{sc.suite.helper, "<TEST_HELPER>"},
		{sc.root, "<SCENARIO_ROOT>"},
		{sc.suite.root, "<SUITE_ROOT>"},
	}
	for _, replacement := range replacements {
		for _, path := range []string{replacement[0], filepath.ToSlash(replacement[0])} {
			if path != "" {
				value = strings.ReplaceAll(value, path, replacement[1])
			}
		}
	}
	value = timestampPattern.ReplaceAllString(value, "<TIMESTAMP>")
	if sc.suite.version != "" {
		value = strings.ReplaceAll(value, sc.suite.version, "<VERSION>")
	}
	value = versionPattern.ReplaceAllString(value, "<VERSION>")
	return value
}

func TestContractNormalizationReplacesSentinelDerivedHashes(t *testing.T) {
	sc := &scenario{
		t:     t,
		suite: &suite{},
	}
	sc.newSentinel()
	sc.newSentinel()
	input := "A=" + sha256Text(sc.sentinels[0]) + " B=" + sha256Text(sc.sentinels[1])
	want := "A=<SENTINEL_SHA256_1> B=<SENTINEL_SHA256_2>"
	if got := sc.normalizeScalarText(input); got != want {
		t.Fatalf("normalized hash contract=%q, want %q", got, want)
	}
	if got := sc.normalizeScalarText(sc.sentinels[0]); got != sc.sentinels[0] {
		t.Fatal("raw sentinels must not be normalized because that would mask a leak")
	}
}

func TestContractNormalizationReplacesOnlyExpectedVersion(t *testing.T) {
	sc := &scenario{t: t, suite: &suite{version: "v0.0.9"}}

	if got, want := sc.normalizeScalarText("v0.0.9\n"), "<VERSION>\n"; got != want {
		t.Fatalf("normalized expected version=%q, want %q", got, want)
	}
	if got, want := sc.normalizeScalarText("v0.0.8\n"), "v0.0.8\n"; got != want {
		t.Fatalf("unexpected version was masked: got %q, want %q", got, want)
	}
	if got, want := sc.normalizeScalarText("ci-0123456789abcdef0123456789abcdef01234567"), "<VERSION>"; got != want {
		t.Fatalf("normalized CI version=%q, want %q", got, want)
	}
	input := `{"version":"v0.0.9","unexpected":"v0.0.8"}`
	if got, want := sc.normalizeText(input), `{"unexpected":"v0.0.8","version":"\u003cVERSION\u003e"}`; got != want {
		t.Fatalf("normalized JSON version=%q, want %q", got, want)
	}
}

func shuffleScenarioCases(t *testing.T, tests []scenarioCase) {
	t.Helper()
	if os.Getenv(shuffleScenariosEnv) != "1" {
		return
	}
	var random [8]byte
	if _, err := cryptorand.Read(random[:]); err != nil {
		t.Fatalf("generate scenario shuffle seed: %v", err)
	}
	seed := int64(binary.LittleEndian.Uint64(random[:]) & uint64(^uint64(0)>>1))
	if seed == 0 {
		seed = 1
	}
	t.Logf("ENV_VAULT_E2E_SCENARIO_SHUFFLE_SEED=%d", seed)
	mathrand.New(mathrand.NewSource(seed)).Shuffle(len(tests), func(left, right int) {
		tests[left], tests[right] = tests[right], tests[left]
	})
}

func (sc *scenario) normalizeJSONValue(value any, parentKey string) any {
	switch typed := value.(type) {
	case string:
		return sc.normalizeScalarText(typed)
	case []any:
		for index := range typed {
			typed[index] = sc.normalizeJSONValue(typed[index], parentKey)
		}
		if sc.id == "CONCURRENCY_PROFILE_MUTATIONS" && parentKey == "secrets" {
			// Lock acquisition intentionally races, so the final mapping insertion
			// order is not a public guarantee of this scenario. Canonicalize only
			// this proven-unordered concurrent result; sequential profile/list
			// scenarios preserve and compare their public array order verbatim.
			sort.Slice(typed, func(left, right int) bool {
				leftJSON, _ := json.Marshal(typed[left])
				rightJSON, _ := json.Marshal(typed[right])
				return bytes.Compare(leftJSON, rightJSON) < 0
			})
		}
		return typed
	case map[string]any:
		for key, child := range typed {
			typed[key] = sc.normalizeJSONValue(child, key)
		}
		return typed
	default:
		return typed
	}
}

func TestContractNormalizationCanonicalizesOnlyConcurrentMappings(t *testing.T) {
	input := func() []any {
		return []any{map[string]any{"rank": float64(2)}, map[string]any{"rank": float64(1)}}
	}
	concurrent := (&scenario{id: "CONCURRENCY_PROFILE_MUTATIONS"}).normalizeJSONValue(input(), "secrets").([]any)
	if got := concurrent[0].(map[string]any)["rank"]; got != float64(1) {
		t.Fatalf("concurrent mapping order was not canonicalized: %v", concurrent)
	}
	sequential := (&scenario{id: "PROFILE_LIFECYCLE"}).normalizeJSONValue(input(), "secrets").([]any)
	if got := sequential[0].(map[string]any)["rank"]; got != float64(2) {
		t.Fatalf("sequential public order was unexpectedly normalized: %v", sequential)
	}
}

func (sc *scenario) assertNoSentinel(label string, data []byte) {
	sc.t.Helper()
	for _, sentinel := range sc.sentinels {
		if bytes.Contains(data, []byte(sentinel)) {
			sc.t.Fatalf("security invariant failed: sentinel leaked to %s", label)
		}
	}
}

func (sc *scenario) scanSavedFiles() {
	if sc.t.Failed() {
		// Still scan and report additional leakage without exposing content.
	}
	if err := scanSavedTree(sc.root, sc.store, sc.sentinels); err != nil {
		sc.t.Errorf("security scan: %v", err)
	}
}

func scanSavedTree(root, intentionalStore string, sentinels []string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			for _, sentinel := range sentinels {
				if strings.Contains(target, sentinel) {
					return fmt.Errorf("sentinel leaked to saved symlink target %s", filepath.Base(path))
				}
			}
			// Do not follow links: the symlink-defense scenario intentionally
			// leaves hostile config/lock links behind. Their target text is
			// scanned above, while files below the isolated root are visited by
			// WalkDir through their real paths.
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("saved test path is not a regular file: %s", filepath.Base(path))
		}
		if filepath.Clean(path) == filepath.Clean(intentionalStore) {
			// The exact triple-gated backend file is the one intentional
			// secret-bearing file. Siblings, backups, and temporary files are
			// still scanned fail-closed.
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, sentinel := range sentinels {
			if bytes.Contains(data, []byte(sentinel)) {
				return fmt.Errorf("sentinel leaked to saved test file %s", filepath.Base(path))
			}
		}
		return nil
	})
}

func TestSavedFileScannerCoversTestStoreSiblings(t *testing.T) {
	root := t.TempDir()
	store := filepath.Join(root, "backend", "store.gob")
	if err := os.MkdirAll(filepath.Dir(store), 0o700); err != nil {
		t.Fatal(err)
	}
	sentinel := sentinelPrefix + "scanner_unit"
	if err := os.WriteFile(store, []byte(sentinel), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := scanSavedTree(root, store, []string{sentinel}); err != nil {
		t.Fatalf("exact intentional store must be excluded: %v", err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(store), "unexpected-backup"), []byte(sentinel), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := scanSavedTree(root, store, []string{sentinel}); err == nil {
		t.Fatal("sentinel in test-store sibling was not detected")
	}
	if runtime.GOOS != "windows" {
		backup := filepath.Join(filepath.Dir(store), "unexpected-backup")
		if err := os.Remove(backup); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(root, "fixture-link")
		if err := os.Symlink("benign-target", link); err != nil {
			t.Fatal(err)
		}
		if err := scanSavedTree(root, store, []string{sentinel}); err != nil {
			t.Fatalf("benign symlink fixture rejected: %v", err)
		}
		if err := os.Remove(link); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(sentinel, link); err != nil {
			t.Fatal(err)
		}
		if err := scanSavedTree(root, store, []string{sentinel}); err == nil {
			t.Fatal("sentinel in symlink target was not detected")
		}
	}
}

func (sc *scenario) scanFile(path, label string) []byte {
	sc.t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		sc.t.Fatalf("read %s: %v", label, err)
	}
	sc.assertNoSentinel(label, data)
	return data
}

func wantExit(t *testing.T, result commandResult, code int) {
	t.Helper()
	if result.TimedOut {
		t.Fatalf("command timed out")
	}
	if result.ExitCode != code {
		t.Fatalf("exit code=%d, want %d; stdout=%q stderr=%q", result.ExitCode, code, result.Stdout, result.Stderr)
	}
}

func wantContains(t *testing.T, value, substring, label string) {
	t.Helper()
	if !strings.Contains(value, substring) {
		t.Fatalf("%s does not contain %q: %q", label, substring, value)
	}
}

func wantNotContains(t *testing.T, value, substring, label string) {
	t.Helper()
	if strings.Contains(value, substring) {
		t.Fatalf("%s unexpectedly contains %q", label, substring)
	}
}

func wantEmpty(t *testing.T, value, label string) {
	t.Helper()
	if value != "" {
		t.Fatalf("%s=%q, want empty", label, value)
	}
}

func parseEnvelope(t *testing.T, result commandResult) envelope {
	t.Helper()
	return parseEnvelopeBytes(t, []byte(result.Stdout), "stdout")
}

func parseEnvelopeBytes(t *testing.T, data []byte, label string) envelope {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatalf("decode JSON envelope fields from %s: %v", label, err)
	}
	wantFields := []string{"ok", "command", "timestamp", "data", "warnings", "error"}
	if len(fields) != len(wantFields) {
		t.Fatalf("JSON envelope in %s has fields %v, want %v", label, sortedMapKeys(fields), wantFields)
	}
	for _, field := range wantFields {
		if _, ok := fields[field]; !ok {
			t.Fatalf("JSON envelope in %s is missing field %q", label, field)
		}
	}
	var got envelope
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&got); err != nil {
		t.Fatalf("decode strict JSON envelope from %s: %v", label, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		t.Fatalf("JSON envelope in %s has trailing data: %v", label, err)
	}
	if got.Command == "" || got.Timestamp == "" || got.Warnings == nil {
		t.Fatalf("incomplete JSON envelope: %#v", got)
	}
	if _, err := time.Parse(time.RFC3339, got.Timestamp); err != nil {
		t.Fatalf("invalid envelope timestamp: %q", got.Timestamp)
	}
	dataIsNull := bytes.Equal(bytes.TrimSpace(got.Data), []byte("null"))
	if got.OK && (got.Error != nil || dataIsNull) {
		t.Fatalf("successful envelope has invalid data/error shape: %#v", got)
	}
	if !got.OK && (got.Error == nil || !dataIsNull || got.Error.Code == "" || got.Error.Message == "" || got.Error.Remediation == "") {
		t.Fatalf("error envelope has invalid data/error shape: %#v", got)
	}
	return got
}

func sortedMapKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func parseDataMap(t *testing.T, got envelope) map[string]any {
	t.Helper()
	var data map[string]any
	if err := json.Unmarshal(got.Data, &data); err != nil {
		t.Fatalf("decode envelope data: %v", err)
	}
	return data
}

func sha256Text(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func fileSHA256(t *testing.T, filename string) [sha256.Size]byte {
	t.Helper()
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatalf("hash saved test file %s: %v", filepath.Base(filename), err)
	}
	return sha256.Sum256(data)
}
