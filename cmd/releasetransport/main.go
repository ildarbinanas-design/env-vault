package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/ildarbinanas-design/env-vault/internal/githubtransport"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(runContext(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	return runContext(context.Background(), args, stdout, stderr)
}

func runContext(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return writeUsageError(stderr, "a transport subcommand is required")
	}
	switch args[0] {
	case "preflight":
		return runPreflight(ctx, args[1:], stdout, stderr)
	case "read":
		return runRead(ctx, args[1:], stderr)
	case "actions":
		return runActions(ctx, args[1:], stderr)
	case "git-blob":
		return runBlob(ctx, args[1:], stderr)
	case "contents":
		return runContents(ctx, args[1:], stderr)
	case "rest":
		return runREST(ctx, args[1:], stderr)
	case "help", "--help", "-h":
		fmt.Fprint(stdout, usage())
		return githubtransport.ExitOK
	default:
		return writeUsageError(stderr, "transport subcommand is unsupported")
	}
}

func runContents(ctx context.Context, args []string, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "read" {
		return writeUsageError(stderr, "contents read subcommand is required")
	}
	set := flag.NewFlagSet("contents read", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	output := set.String("output", "", "exact content output")
	repository := set.String("repository", "", "owner/repository")
	path := set.String("path", "", "relative repository path")
	ref := set.String("ref", "", "exact commit SHA")
	if err := set.Parse(args[1:]); err != nil || set.NArg() != 0 || *output == "" {
		return writeUsageError(stderr, "contents read arguments are invalid")
	}
	if err := githubtransport.ValidateOutputPath(*output); err != nil {
		return writeError(stderr, &githubtransport.TransportError{Code: "OUTPUT_FAILED", Message: err.Error()}, githubtransport.ExitOutput)
	}
	data, transportErr := githubtransport.NewClient().ReadContents(ctx, *repository, *path, *ref)
	if transportErr != nil {
		return writeError(stderr, transportErr, exitForTransportError(transportErr))
	}
	if err := githubtransport.WriteNoClobber(*output, data); err != nil {
		return writeError(stderr, &githubtransport.TransportError{Code: "OUTPUT_FAILED", Message: err.Error()}, githubtransport.ExitOutput)
	}
	return githubtransport.ExitOK
}

func runREST(ctx context.Context, args []string, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "observe" {
		return writeUsageError(stderr, "rest observe subcommand is required")
	}
	set := flag.NewFlagSet("rest observe", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	output := set.String("output", "", "typed observation output")
	endpoint := set.String("endpoint", "", "relative REST endpoint")
	if err := set.Parse(args[1:]); err != nil || set.NArg() != 0 || *output == "" {
		return writeUsageError(stderr, "rest observe arguments are invalid")
	}
	if err := githubtransport.ValidateOutputPath(*output); err != nil {
		return writeError(stderr, &githubtransport.TransportError{Code: "OUTPUT_FAILED", Message: err.Error()}, githubtransport.ExitOutput)
	}
	document, transportErr := githubtransport.NewClient().Observe(ctx, *endpoint)
	if transportErr != nil {
		return writeError(stderr, transportErr, exitForTransportError(transportErr))
	}
	return writeDocument(*output, document, stderr)
}

func runActions(ctx context.Context, args []string, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "identity" {
		return writeUsageError(stderr, "actions identity subcommand is required")
	}
	set := flag.NewFlagSet("actions identity", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	output := set.String("output", "", "output JSON")
	repository := set.String("repository", "", "owner/repository")
	runID := set.Int64("run-id", 0, "workflow run ID")
	attempt := set.Int("run-attempt", 0, "workflow run attempt")
	workflowPath := set.String("workflow-path", "", "stable workflow path")
	event := set.String("event", "", "event")
	headSHA := set.String("head-sha", "", "exact head SHA")
	headRef := set.String("head-ref", "", "exact head ref")
	status := set.String("status", "completed", "expected status")
	conclusion := set.String("conclusion", "success", "expected conclusion")
	jobID := set.Int64("job-id", 0, "required-check job ID")
	jobName := set.String("job-name", "", "required-check job name")
	jobURL := set.String("job-url", "", "required-check canonical URL")
	if err := set.Parse(args[1:]); err != nil || set.NArg() != 0 || *output == "" {
		return writeUsageError(stderr, "actions identity arguments are invalid")
	}
	if err := githubtransport.ValidateOutputPath(*output); err != nil {
		return writeError(stderr, &githubtransport.TransportError{Code: "OUTPUT_FAILED", Message: err.Error()}, githubtransport.ExitOutput)
	}
	document, transportErr := githubtransport.NewClient().ResolveActionsIdentity(ctx, githubtransport.ActionsIdentityOptions{
		Repository: *repository, RunID: *runID, RunAttempt: *attempt, WorkflowPath: *workflowPath,
		Event: *event, HeadSHA: *headSHA, HeadRef: *headRef, Status: *status, Conclusion: *conclusion,
		JobID: *jobID, JobName: *jobName, JobURL: *jobURL,
	})
	if transportErr != nil {
		return writeError(stderr, transportErr, exitForTransportError(transportErr))
	}
	return writeDocument(*output, document, stderr)
}

func runBlob(ctx context.Context, args []string, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "verify" {
		return writeUsageError(stderr, "git-blob verify subcommand is required")
	}
	set := flag.NewFlagSet("git-blob verify", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	output := set.String("output", "", "output JSON")
	repository := set.String("repository", "", "owner/repository")
	sha := set.String("sha", "", "blob SHA")
	expected := set.String("expected-file", "", "expected local file")
	if err := set.Parse(args[1:]); err != nil || set.NArg() != 0 || *output == "" {
		return writeUsageError(stderr, "git-blob verify arguments are invalid")
	}
	if err := githubtransport.ValidateOutputPath(*output); err != nil {
		return writeError(stderr, &githubtransport.TransportError{Code: "OUTPUT_FAILED", Message: err.Error()}, githubtransport.ExitOutput)
	}
	document, transportErr := githubtransport.NewClient().VerifyBlob(ctx, *repository, *sha, *expected)
	if transportErr != nil {
		return writeError(stderr, transportErr, exitForTransportError(transportErr))
	}
	return writeDocument(*output, document, stderr)
}

func writeDocument(path string, document any, stderr io.Writer) int {
	encoded, err := githubtransport.MarshalDocument(document)
	if err == nil {
		err = githubtransport.WriteNoClobber(path, encoded)
	}
	if err != nil {
		return writeError(stderr, &githubtransport.TransportError{Code: "OUTPUT_FAILED", Message: err.Error()}, githubtransport.ExitOutput)
	}
	return githubtransport.ExitOK
}

func runPreflight(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("preflight", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	output := set.String("output", "-", "output path or -")
	if err := set.Parse(args); err != nil || set.NArg() != 0 {
		return writeUsageError(stderr, "preflight arguments are invalid")
	}
	if *output != "-" {
		if err := githubtransport.ValidateOutputPath(*output); err != nil {
			return writeError(stderr, &githubtransport.TransportError{Code: "OUTPUT_FAILED", Message: err.Error()}, githubtransport.ExitOutput)
		}
	}
	document, transportErr := githubtransport.NewClient().Preflight(ctx)
	if transportErr != nil {
		return writeError(stderr, transportErr, exitForTransportError(transportErr))
	}
	encoded, err := githubtransport.MarshalDocument(document)
	if err != nil {
		return writeError(stderr, &githubtransport.TransportError{Code: "OUTPUT_FAILED", Message: err.Error()}, githubtransport.ExitOutput)
	}
	if *output == "-" {
		if _, err := stdout.Write(encoded); err != nil {
			return writeError(stderr, &githubtransport.TransportError{Code: "OUTPUT_FAILED", Message: err.Error()}, githubtransport.ExitOutput)
		}
		return githubtransport.ExitOK
	}
	if err := githubtransport.WriteNoClobber(*output, encoded); err != nil {
		return writeError(stderr, &githubtransport.TransportError{Code: "OUTPUT_FAILED", Message: err.Error()}, githubtransport.ExitOutput)
	}
	return githubtransport.ExitOK
}

func runRead(ctx context.Context, args []string, stderr io.Writer) int {
	if len(args) < 2 {
		return writeUsageError(stderr, "read arguments are incomplete")
	}
	output := args[0]
	request, err := parseReadArguments(args[1:])
	if err != nil {
		return writeError(stderr, &githubtransport.TransportError{Code: "INPUT_INVALID", Message: err.Error()}, githubtransport.ExitUsage)
	}
	if err := githubtransport.ValidateOutputPath(output); err != nil {
		return writeError(stderr, &githubtransport.TransportError{Code: "OUTPUT_FAILED", Message: err.Error()}, githubtransport.ExitOutput)
	}
	data, transportErr := githubtransport.NewClient().Read(ctx, request)
	if transportErr != nil {
		return writeError(stderr, transportErr, exitForTransportError(transportErr))
	}
	if err := githubtransport.WriteNoClobber(output, data); err != nil {
		return writeError(stderr, &githubtransport.TransportError{Code: "OUTPUT_FAILED", Message: err.Error()}, githubtransport.ExitOutput)
	}
	return githubtransport.ExitOK
}

func parseReadArguments(args []string) (githubtransport.ReadRequest, error) {
	request := githubtransport.ReadRequest{}
	methodSeen := false
	for index := 0; index < len(args); index++ {
		argument := args[index]
		switch argument {
		case "--paginate":
			request.Paginate = true
		case "--slurp":
			request.Slurp = true
		case "--method", "-X":
			index++
			if index >= len(args) || !strings.EqualFold(args[index], "GET") || methodSeen {
				return request, fmt.Errorf("only one explicit GET method is allowed")
			}
			methodSeen = true
		case "--header":
			index++
			if index >= len(args) {
				return request, fmt.Errorf("--header requires a value")
			}
			if request.Accept != "" {
				return request, fmt.Errorf("Accept header may be supplied once")
			}
			name, value, ok := strings.Cut(args[index], ":")
			if !ok || !strings.EqualFold(strings.TrimSpace(name), "Accept") {
				return request, fmt.Errorf("only Accept is allowed")
			}
			request.Accept = strings.TrimSpace(value)
		case "--raw-field", "-f", "--field", "-F":
			index++
			if index >= len(args) {
				return request, fmt.Errorf("%s requires a value", argument)
			}
			request.Fields = append(request.Fields, args[index])
		default:
			if strings.HasPrefix(argument, "--method=") {
				if methodSeen || !strings.EqualFold(strings.TrimPrefix(argument, "--method="), "GET") {
					return request, fmt.Errorf("only one explicit GET method is allowed")
				}
				methodSeen = true
			} else if strings.HasPrefix(argument, "--header=") {
				if request.Accept != "" {
					return request, fmt.Errorf("Accept header may be supplied once")
				}
				name, value, ok := strings.Cut(strings.TrimPrefix(argument, "--header="), ":")
				if !ok || !strings.EqualFold(strings.TrimSpace(name), "Accept") {
					return request, fmt.Errorf("only Accept is allowed")
				}
				request.Accept = strings.TrimSpace(value)
			} else if strings.HasPrefix(argument, "--raw-field=") || strings.HasPrefix(argument, "--field=") {
				request.Fields = append(request.Fields, argument[strings.Index(argument, "=")+1:])
			} else if strings.HasPrefix(argument, "-") {
				return request, fmt.Errorf("unsupported read option")
			} else if request.Endpoint == "" {
				request.Endpoint = argument
			} else {
				return request, fmt.Errorf("exactly one endpoint is required")
			}
		}
	}
	if len(request.Fields) != 0 && !methodSeen {
		return request, fmt.Errorf("query fields require an explicit GET method")
	}
	return request, nil
}

func exitForTransportError(transportErr *githubtransport.TransportError) int {
	switch transportErr.Code {
	case "INPUT_INVALID":
		return githubtransport.ExitUsage
	case "CLI_CAPABILITY_DRIFT":
		return githubtransport.ExitCapability
	case "REMOTE_NOT_FOUND":
		return githubtransport.ExitNotFound
	default:
		return githubtransport.ExitRemote
	}
}

func writeError(writer io.Writer, transportErr *githubtransport.TransportError, exit int) int {
	_ = json.NewEncoder(writer).Encode(githubtransport.ErrorDocumentFor(transportErr))
	return exit
}

func writeUsageError(writer io.Writer, message string) int {
	return writeError(writer, &githubtransport.TransportError{Code: "INPUT_INVALID", Message: message}, githubtransport.ExitUsage)
}

func usage() string {
	return "usage:\n  releasetransport preflight --output PATH|-\n  releasetransport read OUTPUT [--paginate --slurp] [--method GET] [-f key=value] ENDPOINT\n  releasetransport contents read --output PATH --repository OWNER/REPO --path PATH --ref SHA\n  releasetransport rest observe --output PATH --endpoint ENDPOINT\n  releasetransport actions identity --output PATH --repository OWNER/REPO --run-id ID --run-attempt N --workflow-path PATH --event EVENT --head-sha SHA --head-ref REF [--job-id ID --job-name NAME --job-url URL]\n  releasetransport git-blob verify --output PATH --repository OWNER/REPO --sha SHA --expected-file PATH\n"
}
