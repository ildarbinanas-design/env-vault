package releasectl

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"
)

const (
	exitSuccess          = 0
	exitReleaseFailure   = 1
	exitUsage            = 2
	exitObservationError = 3
	exitWatchTimeout     = 4
	defaultStatusTimeout = 2 * time.Minute
	defaultWatchTimeout  = 3 * time.Hour
)

type dependencies struct {
	github githubGetter
	clock  clock
}

func Run(args []string, stdout, stderr io.Writer) int {
	return run(args, stdout, stderr, dependencies{
		github: ghClient{runner: execRunner{}},
		clock:  realClock{},
	})
}

func run(args []string, stdout, stderr io.Writer, deps dependencies) int {
	if len(args) < 2 || args[0] != "release" || (args[1] != "status" && args[1] != "watch") {
		return usage(stderr, "usage: releasectl release <status|watch> --version vX.Y.Z [--repo OWNER/REPO] [--source-sha SHA] --json")
	}
	watch := args[1] == "watch"
	flags := flag.NewFlagSet("releasectl release "+args[1], flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	version := flags.String("version", "", "release version")
	repository := flags.String("repo", defaultRepository, "GitHub repository")
	sourceSHA := flags.String("source-sha", "", "expected release source commit")
	jsonOutput := flags.Bool("json", false, "write one versioned JSON document")
	var interval *time.Duration
	defaultTimeout := defaultStatusTimeout
	if watch {
		interval = flags.Duration("interval", 30*time.Second, "watch poll interval")
		defaultTimeout = defaultWatchTimeout
	}
	timeout := flags.Duration("timeout", defaultTimeout, "observation timeout")
	if err := flags.Parse(args[2:]); err != nil {
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
	if watch {
		if interval == nil || *interval <= 0 {
			return usage(stderr, "interval must be greater than zero")
		}
		return runWatch(context.Background(), stdout, request, *interval, *timeout, deps)
	}
	return runStatus(context.Background(), stdout, request, *timeout, deps)
}

func runStatus(ctx context.Context, stdout io.Writer, request query, timeout time.Duration, deps dependencies) int {
	statusCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	collector := collector{github: deps.github, clock: deps.clock}
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
	collector := collector{github: deps.github, clock: deps.clock}
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
		if doc.Overall != nil && doc.Overall.Terminal {
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
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(doc)
}

func usage(stderr io.Writer, message string) int {
	fmt.Fprintln(stderr, "releasectl:", message)
	return exitUsage
}
