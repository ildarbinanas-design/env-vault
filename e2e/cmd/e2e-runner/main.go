// Command e2e-runner builds (or verifies) an env-vault binary, runs the
// black-box E2E suite, and emits deterministic CI reports.
//
// The command uses the Go standard library plus checked-in offline
// contract/evidence helpers. It executes an exact checksum-pinned gotestsum
// binary, but never downloads or resolves that reporter from the network.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/ildarbinanas-design/env-vault/internal/releasecontract"
)

const (
	reportSchemaVersion   = "1"
	defaultSentinelPrefix = "ENV_VAULT_E2E_SENTINEL_"
)

type runOptions struct {
	phase              string
	binary             string
	artifact           string
	checksum           string
	reporter           string
	reporterChecksum   string
	reportsRoot        string
	testPackage        string
	scenariosPath      string
	helperPackage      string
	commandTimeout     time.Duration
	testTimeout        time.Duration
	burnInCount        int
	lockingBurnInCount int
	lockingPattern     string
	coverageFloor      float64
	runnerOS           string
}

type matrixOptions struct {
	contractPath       string
	reportsRoot        string
	phase              string
	required           string
	expectedCommit     string
	expectedRunID      string
	expectedRunURL     string
	expectedRunAttempt string
	expectedRepository string
	expectedReporter   string
}

func main() {
	if err := realMain(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "e2e-runner:", err)
		var status exitStatusError
		if errors.As(err, &status) && status.code > 0 && status.code <= 255 {
			os.Exit(status.code)
		}
		os.Exit(1)
	}
}

func realMain(args []string) error {
	mode := "run"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		mode = args[0]
		args = args[1:]
	}
	switch mode {
	case "run":
		opts, err := parseRunFlags(args)
		if err != nil {
			return err
		}
		return runSuite(opts)
	case "validate-matrix":
		opts, err := parseMatrixFlags(args)
		if err != nil {
			return err
		}
		return validateMatrix(opts)
	case "help", "-h", "--help":
		printUsage()
		return nil
	default:
		return fmt.Errorf("unknown mode %q (want run or validate-matrix)", mode)
	}
}

func parseRunFlags(args []string) (runOptions, error) {
	var opts runOptions
	fs := flag.NewFlagSet("e2e-runner run", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&opts.phase, "phase", "", "report phase: baseline or candidate")
	fs.StringVar(&opts.binary, "binary", "", "prebuilt native env-vault binary")
	fs.StringVar(&opts.artifact, "artifact", "", "native release .tar.gz or .zip artifact")
	fs.StringVar(&opts.checksum, "checksum", "", "optional SHA-256 sidecar for --artifact")
	fs.StringVar(&opts.reporter, "reporter", "", "exact prebuilt native gotestsum binary")
	fs.StringVar(&opts.reporterChecksum, "reporter-checksum", "", "SHA-256 sidecar for --reporter")
	fs.StringVar(&opts.reportsRoot, "reports", "reports/e2e", "root report directory")
	fs.StringVar(&opts.testPackage, "test-package", "./e2e", "Go package containing the E2E suite")
	fs.StringVar(&opts.scenariosPath, "scenarios", "e2e/scenarios.json", "feature/scenario manifest")
	fs.StringVar(&opts.helperPackage, "helper-package", "", "subprocess helper package (auto-detected when empty)")
	fs.DurationVar(&opts.commandTimeout, "command-timeout", 30*time.Minute, "hard deadline for build/report commands")
	fs.DurationVar(&opts.testTimeout, "test-timeout", 15*time.Minute, "go test timeout for each suite execution")
	fs.IntVar(&opts.burnInCount, "burn-in-count", envInt("ENV_VAULT_E2E_BURN_IN_COUNT", 3), "full-suite shuffle burn-in count")
	fs.IntVar(&opts.lockingBurnInCount, "locking-burn-in-count", envInt("ENV_VAULT_E2E_LOCKING_BURN_IN_COUNT", 5), "concurrency/locking shuffle burn-in count")
	fs.StringVar(&opts.lockingPattern, "locking-pattern", `TestE2E/(?i)(concurr|lock|atomic|crash)`, "-run expression for concurrency/locking burn-in")
	fs.Float64Var(&opts.coverageFloor, "coverage-floor", 0, "minimum statement coverage percentage (0 records baseline only)")
	fs.StringVar(&opts.runnerOS, "runner-os", firstNonEmpty(os.Getenv("RUNNER_OS"), runtime.GOOS), "runner OS label recorded in metadata")
	if err := fs.Parse(args); err != nil {
		return runOptions{}, err
	}
	if fs.NArg() != 0 {
		return runOptions{}, fmt.Errorf("unexpected positional arguments: %s", strings.Join(fs.Args(), " "))
	}
	if opts.phase != "baseline" && opts.phase != "candidate" {
		return runOptions{}, errors.New("--phase must be baseline or candidate")
	}
	if opts.binary != "" && opts.artifact != "" {
		return runOptions{}, errors.New("--binary and --artifact are mutually exclusive")
	}
	if opts.checksum != "" && opts.artifact == "" {
		return runOptions{}, errors.New("--checksum requires --artifact")
	}
	if opts.reporterChecksum != "" && opts.reporter == "" {
		return runOptions{}, errors.New("--reporter-checksum requires --reporter")
	}
	if opts.commandTimeout <= 0 || opts.testTimeout <= 0 {
		return runOptions{}, errors.New("timeouts must be positive")
	}
	if opts.burnInCount < 3 || opts.lockingBurnInCount < 5 {
		return runOptions{}, errors.New("full-suite burn-in must be at least 3 and locking burn-in at least 5; rerun suppression is not supported")
	}
	if opts.coverageFloor < 0 || opts.coverageFloor > 100 {
		return runOptions{}, errors.New("--coverage-floor must be between 0 and 100")
	}
	return opts, nil
}

func parseMatrixFlags(args []string) (matrixOptions, error) {
	opts := matrixOptions{contractPath: releasecontract.CanonicalPath}
	fs := flag.NewFlagSet("e2e-runner validate-matrix", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&opts.contractPath, "contract", opts.contractPath, "release contract defining the exact ordered native platform matrix")
	fs.StringVar(&opts.reportsRoot, "reports", "", "downloaded report/artifact root")
	fs.StringVar(&opts.phase, "phase", "", "required report phase: baseline or candidate")
	fs.StringVar(&opts.expectedCommit, "expected-commit", "", "exact commit SHA expected in every report")
	fs.StringVar(&opts.expectedRunID, "expected-run-id", "", "exact GitHub Actions run ID expected in every report")
	fs.StringVar(&opts.expectedRunURL, "expected-run-url", "", "exact GitHub Actions run URL expected in every report")
	fs.StringVar(&opts.expectedRunAttempt, "expected-run-attempt", "", "exact GitHub Actions run attempt expected in every report")
	fs.StringVar(&opts.expectedRepository, "expected-repository", "", "exact owner/repository expected in every report")
	fs.StringVar(&opts.expectedReporter, "expected-reporter", gotestsumVersion, "exact gotestsum version expected in every report")
	if err := fs.Parse(args); err != nil {
		return matrixOptions{}, err
	}
	if fs.NArg() != 0 {
		return matrixOptions{}, fmt.Errorf("unexpected positional arguments: %s", strings.Join(fs.Args(), " "))
	}
	if opts.reportsRoot == "" {
		return matrixOptions{}, errors.New("--reports is required")
	}
	if opts.phase != "baseline" && opts.phase != "candidate" {
		return matrixOptions{}, errors.New("--phase must be baseline or candidate")
	}
	if opts.expectedCommit == "" || opts.expectedRunID == "" || opts.expectedRunURL == "" || opts.expectedRunAttempt == "" || opts.expectedRepository == "" || opts.expectedReporter == "" {
		return matrixOptions{}, errors.New("--expected-commit, run ID/URL/attempt, repository, and reporter are required")
	}
	if !validGitCommitSHA(opts.expectedCommit) {
		return matrixOptions{}, errors.New("--expected-commit must be a full Git commit SHA")
	}
	if opts.expectedRunID != "local" && !numericRunID(opts.expectedRunID) {
		return matrixOptions{}, errors.New("--expected-run-id must be numeric or local")
	}
	if opts.expectedRunID == "local" && (opts.expectedRunURL != "local" || opts.expectedRunAttempt != "local" || opts.expectedRepository != "local") {
		return matrixOptions{}, errors.New("local matrix identity requires local URL, attempt, and repository")
	}
	contract, err := releasecontract.LoadFile(opts.contractPath)
	if err != nil {
		return matrixOptions{}, fmt.Errorf("load release contract: %w", err)
	}
	required := make([]string, 0, len(contract.Platforms))
	for _, platform := range contract.Platforms {
		required = append(required, platform.ID)
	}
	opts.required = strings.Join(required, ",")
	return opts, nil
}

func printUsage() {
	fmt.Fprintln(os.Stdout, `Usage:
  go run ./e2e/cmd/e2e-runner run --phase baseline [--binary PATH | --artifact PATH]
  go run ./e2e/cmd/e2e-runner validate-matrix --contract release/contract.v1.json --reports DIR --phase baseline [exact run identity flags]

The run mode always executes the release-like suite, a separately instrumented
coverage suite, a shuffled full-suite burn-in, and a targeted locking burn-in.
The validate-matrix mode derives its exact ordered platform set from the release contract.`)
}

func envInt(name string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return n
}

func numericRunID(value string) bool {
	n, err := strconv.ParseUint(value, 10, 64)
	return err == nil && n > 0
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func parseCSV(value string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item != "" && !seen[item] {
			seen[item] = true
			result = append(result, item)
		}
	}
	return result
}

func requiredPlatforms() []string {
	return []string{"linux-amd64", "linux-arm64", "darwin-amd64", "darwin-arm64", "windows-amd64"}
}
