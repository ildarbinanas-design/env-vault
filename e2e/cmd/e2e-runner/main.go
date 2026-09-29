// Command e2e-runner builds (or verifies) an env-vault binary, runs the
// black-box E2E suite, and emits deterministic CI reports.
//
// The command uses the Go standard library plus checked-in offline helpers. It
// executes an exact checksum-pinned gotestsum binary, but never downloads or
// resolves that reporter from the network.
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
	case "help", "-h", "--help":
		printUsage()
		return nil
	default:
		return fmt.Errorf("unknown mode %q (want run)", mode)
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

func printUsage() {
	fmt.Fprintln(os.Stdout, `Usage:
  go run ./e2e/cmd/e2e-runner run --phase baseline [--binary PATH | --artifact PATH]

The run mode always executes the release-like suite, a separately instrumented
coverage suite, a shuffled full-suite burn-in, and a targeted locking burn-in.`)
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func requiredPlatforms() []string {
	return []string{"linux-amd64", "linux-arm64", "darwin-amd64", "darwin-arm64", "windows-amd64"}
}
