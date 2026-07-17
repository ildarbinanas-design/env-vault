package releasectl

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/ildarbinanas-design/env-vault/internal/releasecontract"
)

const (
	exitSuccess            = 0
	exitReleaseFailure     = 1
	exitUsage              = 2
	exitObservationError   = 3
	exitWatchTimeout       = 4
	exitPreconditionFailed = 5
	exitMutationBlocked    = 6
	defaultStatusTimeout   = 2 * time.Minute
	defaultWatchTimeout    = 3 * time.Hour
)

type dependencies struct {
	github   githubGetter
	mutator  githubMutator
	clock    clock
	contract releasecontract.Contract
}

func Run(args []string, stdout, stderr io.Writer) int {
	client := ghClient{runner: execRunner{}}
	return run(args, stdout, stderr, dependencies{
		github: client, mutator: client, clock: realClock{},
	})
}

func run(args []string, stdout, stderr io.Writer, deps dependencies) int {
	if len(args) < 2 {
		return usage(stderr, rootUsage)
	}
	switch args[0] {
	case "release":
		return runRelease(args[1:], stdout, stderr, deps)
	default:
		return usage(stderr, rootUsage)
	}
}

const rootUsage = "usage: releasectl release <command> [flags] --json"

func runRelease(args []string, stdout, stderr io.Writer, deps dependencies) int {
	if len(args) == 0 {
		return usage(stderr, "usage: releasectl release <plan|status|watch|verify|metrics|repair|legacy-rebuild> [flags] --json")
	}
	switch args[0] {
	case "status", "watch":
		return runStatusOrWatch(args, stdout, stderr, deps)
	case "plan", "verify":
		return runReleaseReadCommand(args, stdout, stderr, deps)
	case "metrics":
		return runReleaseMetricsCommand(args[1:], stdout, stderr, deps)
	case "repair":
		return runReleaseRepairCommand(args[1:], stdout, stderr, deps)
	case "legacy-rebuild":
		return runLegacyRebuildCommand(args[1:], stdout, stderr, deps)
	default:
		return usage(stderr, "usage: releasectl release <plan|status|watch|verify|metrics|repair|legacy-rebuild> [flags] --json")
	}
}

func runStatusOrWatch(args []string, stdout, stderr io.Writer, deps dependencies) int {
	if len(args) == 0 || (args[0] != "status" && args[0] != "watch") {
		return usage(stderr, "usage: releasectl release <status|watch> --version vX.Y.Z [--repo OWNER/REPO] [--source-sha SHA] --json")
	}
	watch := args[0] == "watch"
	flags := flag.NewFlagSet("releasectl release "+args[0], flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	version := flags.String("version", "", "release version")
	repository := flags.String("repo", defaultRepository, "GitHub repository")
	sourceSHA := flags.String("source-sha", "", "expected release source commit")
	contractPath := flags.String("contract", releasecontract.CanonicalPath, "release contract")
	jsonOutput := flags.Bool("json", false, "write one versioned JSON document")
	var interval *time.Duration
	defaultTimeout := defaultStatusTimeout
	if watch {
		interval = flags.Duration("interval", 30*time.Second, "watch poll interval")
		defaultTimeout = defaultWatchTimeout
	}
	timeout := flags.Duration("timeout", defaultTimeout, "observation timeout")
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(stdout, "usage: releasectl release <status|watch> --version vX.Y.Z [--repo OWNER/REPO] [--source-sha SHA] --json")
			return exitSuccess
		}
		return usage(stderr, err.Error())
	}
	if flags.NArg() != 0 {
		return usage(stderr, "unexpected positional arguments")
	}
	if !*jsonOutput {
		return usage(stderr, "--json is required")
	}
	request := query{Repository: *repository, Version: *version, SourceSHA: *sourceSHA}
	if err := validateQuery(request); err != nil {
		return usage(stderr, err.Error())
	}
	if *timeout <= 0 {
		return usage(stderr, "timeout must be greater than zero")
	}
	contract, err := loadOperatorContract(*contractPath)
	if err != nil {
		return writeCommandError(stdout, statusSchema, "CONTRACT_INVALID", "release_contract", exitUsage)
	}
	deps.contract = contract
	if watch {
		if interval == nil || *interval <= 0 {
			return usage(stderr, "interval must be greater than zero")
		}
		return runWatch(context.Background(), stdout, request, *interval, *timeout, deps)
	}
	return runStatus(context.Background(), stdout, request, *timeout, deps)
}

func runReleaseReadCommand(args []string, stdout, stderr io.Writer, deps dependencies) int {
	command := args[0]
	flags := flag.NewFlagSet("releasectl release "+command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	version := flags.String("version", "", "release version")
	repository := flags.String("repo", defaultRepository, "GitHub repository")
	sourceSHA := flags.String("source-sha", "", "expected release source commit")
	contractPath := flags.String("contract", releasecontract.CanonicalPath, "release contract")
	jsonOutput := flags.Bool("json", false, "write one versioned JSON document")
	timeout := flags.Duration("timeout", defaultStatusTimeout, "observation timeout")
	if err := flags.Parse(args[1:]); err != nil {
		return usage(stderr, err.Error())
	}
	if flags.NArg() != 0 || !*jsonOutput {
		return usage(stderr, "--json is required and positional arguments are not accepted")
	}
	request := query{Repository: *repository, Version: *version, SourceSHA: *sourceSHA}
	if err := validateQuery(request); err != nil || *timeout <= 0 {
		if err != nil {
			return usage(stderr, err.Error())
		}
		return usage(stderr, "timeout must be greater than zero")
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	contract, err := loadOperatorContract(*contractPath)
	if err != nil {
		schema := releasePlanSchema
		if command == "verify" {
			schema = releaseVerifySchema
		}
		return writeCommandError(stdout, schema, "CONTRACT_INVALID", "release_contract", exitUsage)
	}
	if command == "plan" {
		doc := buildReleasePlan(ctx, request, contract, deps.github, deps.clock)
		if writeAnyJSON(stdout, doc) != nil {
			return exitObservationError
		}
		if !doc.OK {
			return exitObservationError
		}
		if doc.Overall != nil && (doc.Overall.State == "failed" || doc.Overall.State == "inconsistent") {
			return exitReleaseFailure
		}
		return exitSuccess
	}
	doc := verifyRelease(ctx, request, contract, deps.github, deps.clock)
	if writeAnyJSON(stdout, doc) != nil {
		return exitObservationError
	}
	switch doc.Outcome {
	case "pass":
		return exitSuccess
	case "fail":
		return exitReleaseFailure
	default:
		return exitObservationError
	}
}

func runReleaseMetricsCommand(args []string, stdout, stderr io.Writer, deps dependencies) int {
	flags := flag.NewFlagSet("releasectl release metrics", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	repository := flags.String("repo", defaultRepository, "GitHub repository")
	runID := flags.Int64("run-id", 0, "workflow run ID")
	contractPath := flags.String("contract", releasecontract.CanonicalPath, "release contract")
	jsonOutput := flags.Bool("json", false, "write one versioned JSON document")
	timeout := flags.Duration("timeout", defaultStatusTimeout, "observation timeout")
	if err := flags.Parse(args); err != nil {
		return usage(stderr, err.Error())
	}
	if flags.NArg() != 0 || !*jsonOutput || !repositoryPattern.MatchString(*repository) || *runID <= 0 || *timeout <= 0 {
		return usage(stderr, "metrics requires --repo OWNER/REPO --run-id ID --json and a positive timeout")
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	contract, err := loadOperatorContract(*contractPath)
	if err != nil {
		return writeCommandError(stdout, releaseMetricsSchema, "CONTRACT_INVALID", "release_contract", exitUsage)
	}
	doc, err := collectReleaseMetrics(ctx, *repository, *runID, contract, deps.github, deps.clock)
	if err != nil {
		doc.OK = false
		doc.Error = operatorErrorInfo(err)
	}
	if writeAnyJSON(stdout, doc) != nil {
		return exitObservationError
	}
	if err != nil {
		return exitObservationError
	}
	return exitSuccess
}

func runReleaseRepairCommand(args []string, stdout, stderr io.Writer, deps dependencies) int {
	if len(args) == 0 || (args[0] != "plan" && args[0] != "apply") {
		return usage(stderr, "usage: releasectl release repair <plan|apply> [flags] --json")
	}
	if args[0] == "plan" {
		return runReleaseRepairPlan(args[1:], stdout, stderr, deps)
	}
	return runReleaseRepairApply(args[1:], stdout, stderr, deps)
}

func runReleaseRepairPlan(args []string, stdout, stderr io.Writer, deps dependencies) int {
	flags := flag.NewFlagSet("releasectl release repair plan", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	repository := flags.String("repo", defaultRepository, "GitHub repository")
	runID := flags.Int64("run-id", 0, "workflow run ID")
	version := flags.String("version", "", "exact release version for a publisher-stage repair")
	sourceSHA := flags.String("source-sha", "", "exact release tag source SHA")
	contractPath := flags.String("contract", releasecontract.CanonicalPath, "release contract")
	jsonOutput := flags.Bool("json", false, "write one versioned JSON document")
	timeout := flags.Duration("timeout", defaultStatusTimeout, "observation timeout")
	if err := flags.Parse(args); err != nil {
		return usage(stderr, err.Error())
	}
	ciSelector := *runID > 0 && *version == "" && *sourceSHA == ""
	publisherSelector := *runID == 0 && releasecontract.IsVersion(*version) && shaPattern.MatchString(*sourceSHA)
	if flags.NArg() != 0 || !*jsonOutput || !repositoryPattern.MatchString(*repository) || (!ciSelector && !publisherSelector) || *timeout <= 0 {
		return usage(stderr, "repair plan requires either --run-id ID or --version vX.Y.Z --source-sha SHA, plus --repo OWNER/REPO --json and a positive timeout")
	}
	contract, err := loadOperatorContract(*contractPath)
	if err != nil {
		return writeCommandError(stdout, "env-vault.release-repair-plan.v1", "CONTRACT_INVALID", "release_contract", exitUsage)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	if publisherSelector {
		if _, legacy := contract.LegacyVersion(*version); legacy {
			return writeCommandError(stdout, contract.Schemas["repair_plan"], "LEGACY_REBUILD_UNSUPPORTED", "publisher_repair_legacy_version", exitMutationBlocked)
		}
		for _, blocked := range contract.VersionPolicy.BlockedVersions {
			if blocked.Version == *version {
				return writeCommandError(stdout, contract.Schemas["repair_plan"], "REMOTE_PRECONDITION_FAILED", "publisher_repair_blocked_version", exitMutationBlocked)
			}
		}
		plan, planErr := planPublisherOrTapRepair(ctx, *repository, *version, *sourceSHA, contract, deps.github, deps.clock)
		if planErr != nil {
			info := operatorErrorInfo(planErr)
			if info.Code == "REMOTE_PRECONDITION_FAILED" {
				return writeCommandError(stdout, contract.Schemas["repair_plan"], info.Code, info.Operation, exitPreconditionFailed)
			}
			return writeCommandAPIError(stdout, contract.Schemas["repair_plan"], planErr)
		}
		if writeAnyJSON(stdout, plan) != nil {
			return exitObservationError
		}
		return exitSuccess
	}
	plan, err := planReleaseRepair(ctx, *repository, *runID, contract, deps.github, deps.clock)
	if err != nil {
		return writeCommandAPIError(stdout, "env-vault.release-repair-plan.v1", err)
	}
	if writeAnyJSON(stdout, plan) != nil {
		return exitObservationError
	}
	return exitSuccess
}

func runReleaseRepairApply(args []string, stdout, stderr io.Writer, deps dependencies) int {
	flags := flag.NewFlagSet("releasectl release repair apply", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	planPath := flags.String("plan", "", "versioned repair plan file")
	planDigest := flags.String("plan-digest", "", "exact repair plan SHA-256")
	contractPath := flags.String("contract", releasecontract.CanonicalPath, "release contract")
	apply := flags.Bool("apply", false, "perform the planned mutation")
	jsonOutput := flags.Bool("json", false, "write one versioned JSON document")
	timeout := flags.Duration("timeout", defaultStatusTimeout, "observation timeout")
	if err := flags.Parse(args); err != nil {
		return usage(stderr, err.Error())
	}
	if flags.NArg() != 0 || !*jsonOutput || *planPath == "" || *planDigest == "" || *timeout <= 0 {
		return usage(stderr, "repair apply requires --plan FILE --plan-digest SHA256 --json; --apply opts in to mutation")
	}
	contract, err := loadOperatorContract(*contractPath)
	if err != nil {
		return writeCommandError(stdout, releaseRepairApplySchema, "CONTRACT_INVALID", "release_contract", exitUsage)
	}
	kind, err := readRepairPlanKind(*planPath)
	if err != nil {
		return writeCommandAPIError(stdout, releaseRepairApplySchema, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	if kind == repairKindPublisher {
		plan, readErr := readPublisherRepairPlan(*planPath)
		if readErr != nil {
			return writeCommandAPIError(stdout, releaseRepairApplySchema, readErr)
		}
		doc, code := applyPublisherRepair(ctx, plan, *planDigest, *apply, contract, deps.github, deps.mutator, deps.clock)
		if writeAnyJSON(stdout, doc) != nil {
			return exitObservationError
		}
		return code
	}
	if kind == repairKindTapCI {
		plan, readErr := readTapCIRepairPlan(*planPath)
		if readErr != nil {
			return writeCommandAPIError(stdout, releaseRepairApplySchema, readErr)
		}
		doc, code := applyTapCIRepair(ctx, plan, *planDigest, *apply, contract, deps.github, deps.mutator, deps.clock)
		if writeAnyJSON(stdout, doc) != nil {
			return exitObservationError
		}
		return code
	}
	if kind != repairKindCIAttempt {
		return writeCommandError(stdout, releaseRepairApplySchema, "INPUT_INVALID", "repair_plan_kind", exitUsage)
	}
	plan, err := readRepairPlan(*planPath)
	if err != nil {
		return writeCommandAPIError(stdout, releaseRepairApplySchema, err)
	}
	doc, code := applyReleaseRepair(ctx, plan, *planDigest, *apply, contract, deps.github, deps.mutator, deps.clock)
	if writeAnyJSON(stdout, doc) != nil {
		return exitObservationError
	}
	return code
}

func writeCommandAPIError(stdout io.Writer, schema string, err error) int {
	info := operatorErrorInfo(err)
	code := exitObservationError
	if info.Code == "INPUT_INVALID" || info.Code == "CONTRACT_INVALID" {
		code = exitUsage
	}
	return writeCommandError(stdout, schema, info.Code, info.Operation, code)
}

func writeCommandError(stdout io.Writer, schema, code, operation string, exit int) int {
	doc := struct {
		Schema string     `json:"schema"`
		OK     bool       `json:"ok"`
		Error  *errorInfo `json:"error"`
	}{Schema: schema, Error: &errorInfo{Code: code, Operation: operation}}
	if writeAnyJSON(stdout, doc) != nil {
		return exitObservationError
	}
	return exit
}

func loadOperatorContract(path string) (releasecontract.Contract, error) {
	contract, err := releasecontract.LoadFile(path)
	if err == nil || path != releasecontract.CanonicalPath {
		return contract, err
	}
	resolved, resolveErr := findRepositoryFile(".", path)
	if resolveErr != nil {
		return releasecontract.Contract{}, err
	}
	return releasecontract.LoadFile(resolved)
}

func runStatus(ctx context.Context, stdout io.Writer, request query, timeout time.Duration, deps dependencies) int {
	statusCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	collector := collector{github: deps.github, clock: deps.clock, contract: deps.contract}
	doc, err := collector.snapshot(statusCtx, request)
	if err != nil {
		doc = errorDocument(deps.clock.Now(), request, err)
	}
	if err := writeDocument(stdout, doc); err != nil {
		return exitObservationError
	}
	return exitCodeFor(doc, false)
}

func runWatch(ctx context.Context, stdout io.Writer, request query, interval, timeout time.Duration, deps dependencies) int {
	watchCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	collector := collector{github: deps.github, clock: deps.clock, contract: deps.contract}
	startedAt := deps.clock.Now()
	polls := 0
	var lastValid *document
	for {
		doc, err := collector.snapshot(watchCtx, request)
		polls++
		elapsed := deps.clock.Now().Sub(startedAt)
		if elapsed < 0 {
			elapsed = 0
		}
		if err != nil {
			if retryableObservation(err) && elapsed < timeout && watchCtx.Err() == nil {
				delay := boundedDelay(interval, timeout-elapsed)
				if sleepErr := deps.clock.Sleep(watchCtx, delay); sleepErr == nil {
					continue
				}
				elapsed = deps.clock.Now().Sub(startedAt)
				if elapsed < 0 {
					elapsed = 0
				}
			}
			if elapsed >= timeout || errors.Is(watchCtx.Err(), context.DeadlineExceeded) {
				if lastValid != nil {
					doc = *lastValid
					doc.OK = false
					doc.Error = errorDocument(deps.clock.Now(), request, err).Error
				} else {
					doc = errorDocument(deps.clock.Now(), request, err)
				}
				doc.Watch = &watchInfo{Polls: polls, ElapsedSeconds: int64(elapsed / time.Second), TimedOut: true}
				if writeErr := writeDocument(stdout, doc); writeErr != nil {
					return exitObservationError
				}
				return exitWatchTimeout
			}
			doc = errorDocument(deps.clock.Now(), request, err)
			doc.Watch = &watchInfo{Polls: polls, ElapsedSeconds: int64(elapsed / time.Second)}
			if writeErr := writeDocument(stdout, doc); writeErr != nil {
				return exitObservationError
			}
			return exitObservationError
		}
		lastValid = &doc
		if request.SourceSHA == "" && doc.Identity != nil && doc.Identity.SourceSHA != "" {
			request.SourceSHA = doc.Identity.SourceSHA
		}
		if doc.Overall != nil && doc.Overall.Terminal && (doc.NextAction == nil || doc.NextAction.Code != "wait_tap_ci") {
			doc.Watch = &watchInfo{Polls: polls, ElapsedSeconds: int64(elapsed / time.Second)}
			if err := writeDocument(stdout, doc); err != nil {
				return exitObservationError
			}
			return exitCodeFor(doc, true)
		}
		if elapsed >= timeout {
			doc.Watch = &watchInfo{Polls: polls, ElapsedSeconds: int64(elapsed / time.Second), TimedOut: true}
			if err := writeDocument(stdout, doc); err != nil {
				return exitObservationError
			}
			return exitWatchTimeout
		}
		delay := boundedDelay(interval, timeout-elapsed)
		if err := deps.clock.Sleep(watchCtx, delay); err != nil {
			elapsed = deps.clock.Now().Sub(startedAt)
			if elapsed < 0 {
				elapsed = 0
			}
			doc.Watch = &watchInfo{Polls: polls, ElapsedSeconds: int64(elapsed / time.Second), TimedOut: true}
			if writeErr := writeDocument(stdout, doc); writeErr != nil {
				return exitObservationError
			}
			return exitWatchTimeout
		}
	}
}

func boundedDelay(interval, remaining time.Duration) time.Duration {
	if interval > remaining {
		return remaining
	}
	return interval
}

func retryableObservation(err error) bool {
	var apiErr *apiError
	return errors.As(err, &apiErr) && apiErr.Retryable
}

func writeDocument(writer io.Writer, doc document) error {
	return writeAnyJSON(writer, doc)
}

func writeAnyJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}

func usage(stderr io.Writer, message string) int {
	fmt.Fprintln(stderr, "releasectl:", message)
	return exitUsage
}
