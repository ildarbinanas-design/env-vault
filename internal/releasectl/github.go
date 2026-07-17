package releasectl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/ildarbinanas-design/env-vault/internal/githubapi"
)

type commandRunner interface {
	Run(context.Context, []string) ([]byte, []byte, error)
}

// inputCommandRunner is deliberately separate from commandRunner. Observation
// code only receives commandRunner and therefore cannot acquire a request body
// or invoke a mutating API method by accident.
type inputCommandRunner interface {
	RunInput(context.Context, []string, []byte) ([]byte, []byte, error)
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, args []string) ([]byte, []byte, error) {
	return execRunner{}.run(ctx, args, nil)
}

func (execRunner) RunInput(ctx context.Context, args []string, input []byte) ([]byte, []byte, error) {
	return execRunner{}.run(ctx, args, input)
}

func (execRunner) run(ctx context.Context, args []string, input []byte) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Env = safeGitHubEnvironment(os.Environ())
	if input != nil {
		cmd.Stdin = bytes.NewReader(input)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

func safeGitHubEnvironment(environment []string) []string {
	blocked := map[string]struct{}{
		"GH_DEBUG":         {},
		"GH_HOST":          {},
		"GIT_TRACE":        {},
		"GIT_TRACE_CURL":   {},
		"GIT_CURL_VERBOSE": {},
	}
	result := make([]string, 0, len(environment))
	for _, entry := range environment {
		key, _, _ := strings.Cut(entry, "=")
		if _, ok := blocked[key]; ok {
			continue
		}
		result = append(result, entry)
	}
	return result
}

type githubGetter interface {
	Get(context.Context, string, map[string]string, any) error
}

// githubMutator is not embedded into githubGetter. GET-only commands are
// constructed with githubGetter and cannot reach this capability.
type githubMutator interface {
	Mutate(context.Context, string, string, any, any) error
}

type ghClient struct {
	runner commandRunner
}

type apiError struct {
	Code       string
	Endpoint   string
	HTTPStatus int
	Retryable  bool
	cause      error
}

func (e *apiError) Error() string {
	if e.HTTPStatus != 0 {
		return fmt.Sprintf("github api request failed: code=%s status=%d endpoint=%s", e.Code, e.HTTPStatus, e.Endpoint)
	}
	return fmt.Sprintf("github api request failed: code=%s endpoint=%s", e.Code, e.Endpoint)
}

func (e *apiError) Unwrap() error { return e.cause }

func (c ghClient) Get(ctx context.Context, endpoint string, query map[string]string, target any) error {
	args := []string{
		"api",
		"--method", "GET",
		"--hostname", "github.com",
		"--header", "Accept: application/vnd.github+json",
		"--header", "X-GitHub-Api-Version: " + githubapi.Version,
		endpoint,
	}
	keys := make([]string, 0, len(query))
	for key := range query {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		args = append(args, "--raw-field", key+"="+query[key])
	}

	stdout, stderr, err := c.runner.Run(ctx, args)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return &apiError{Code: "API_UNAVAILABLE", Endpoint: endpoint, Retryable: true, cause: ctxErr}
		}
		return classifyAPIError(endpoint, stderr, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(stdout))
	if err := decoder.Decode(target); err != nil {
		return &apiError{Code: "MALFORMED_RESPONSE", Endpoint: endpoint, cause: err}
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return &apiError{Code: "MALFORMED_RESPONSE", Endpoint: endpoint, cause: errors.New("multiple JSON values")}
	} else if !errors.Is(err, io.EOF) {
		return &apiError{Code: "MALFORMED_RESPONSE", Endpoint: endpoint, cause: err}
	}
	return nil
}

func (c ghClient) Mutate(ctx context.Context, method, endpoint string, body, target any) error {
	switch method {
	case "POST", "PATCH", "PUT":
	default:
		return &apiError{Code: "API_REQUEST_FAILED", Endpoint: endpoint, cause: fmt.Errorf("unsupported mutation method %q", method)}
	}
	runner, ok := c.runner.(inputCommandRunner)
	if !ok {
		return &apiError{Code: "DEPENDENCY_MISSING", Endpoint: endpoint, cause: errors.New("mutation transport is unavailable")}
	}
	args := []string{
		"api",
		"--method", method,
		"--hostname", "github.com",
		"--header", "Accept: application/vnd.github+json",
		"--header", "X-GitHub-Api-Version: " + githubapi.Version,
	}
	var input []byte
	if body != nil {
		var err error
		input, err = json.Marshal(body)
		if err != nil {
			return &apiError{Code: "MALFORMED_RESPONSE", Endpoint: endpoint, cause: err}
		}
		args = append(args, "--input", "-")
	}
	args = append(args, endpoint)
	stdout, stderr, err := runner.RunInput(ctx, args, input)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return &apiError{Code: "API_UNAVAILABLE", Endpoint: endpoint, Retryable: true, cause: ctxErr}
		}
		return classifyAPIError(endpoint, stderr, err)
	}
	if target == nil {
		return nil
	}
	return decodeStrictAPIResponse(endpoint, stdout, target)
}

func decodeStrictAPIResponse(endpoint string, data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(target); err != nil {
		return &apiError{Code: "MALFORMED_RESPONSE", Endpoint: endpoint, cause: err}
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return &apiError{Code: "MALFORMED_RESPONSE", Endpoint: endpoint, cause: errors.New("multiple JSON values")}
	} else if !errors.Is(err, io.EOF) {
		return &apiError{Code: "MALFORMED_RESPONSE", Endpoint: endpoint, cause: err}
	}
	return nil
}

var httpStatusPattern = regexp.MustCompile(`(?i)(?:\(HTTP |HTTP/[^ ]+ )([0-9]{3})(?:\)|\s|$)`)

func classifyAPIError(endpoint string, stderr []byte, cause error) error {
	if errors.Is(cause, exec.ErrNotFound) {
		return &apiError{Code: "DEPENDENCY_MISSING", Endpoint: endpoint, cause: cause}
	}
	status := 0
	if match := httpStatusPattern.FindSubmatch(stderr); len(match) == 2 {
		status, _ = strconv.Atoi(string(match[1]))
	}
	lower := strings.ToLower(string(stderr))
	if status == 429 || strings.Contains(lower, "rate limit") || strings.Contains(lower, "abuse detection") {
		return &apiError{Code: "RATE_LIMITED", Endpoint: endpoint, HTTPStatus: status, Retryable: true, cause: cause}
	}
	switch status {
	case 401:
		return &apiError{Code: "AUTH_REQUIRED", Endpoint: endpoint, HTTPStatus: status, cause: cause}
	case 403:
		return &apiError{Code: "AUTH_FORBIDDEN", Endpoint: endpoint, HTTPStatus: status, cause: cause}
	case 404:
		return &apiError{Code: "NOT_FOUND", Endpoint: endpoint, HTTPStatus: status, cause: cause}
	}
	if status >= 500 {
		return &apiError{Code: "API_UNAVAILABLE", Endpoint: endpoint, HTTPStatus: status, Retryable: true, cause: cause}
	}
	for _, fragment := range []string{"not logged into any github hosts", "gh auth login", "bad credentials", "authentication token"} {
		if strings.Contains(lower, fragment) {
			return &apiError{Code: "AUTH_REQUIRED", Endpoint: endpoint, cause: cause}
		}
	}
	for _, fragment := range []string{
		"could not resolve", "network is unreachable", "connection refused", "connection reset",
		"tls handshake timeout", "i/o timeout", "context deadline exceeded", "unexpected eof",
		"error connecting to", "check your internet connection",
	} {
		if strings.Contains(lower, fragment) {
			return &apiError{Code: "API_UNAVAILABLE", Endpoint: endpoint, Retryable: true, cause: cause}
		}
	}
	return &apiError{Code: "API_REQUEST_FAILED", Endpoint: endpoint, HTTPStatus: status, cause: cause}
}

func isNotFound(err error) bool {
	var apiErr *apiError
	return errors.As(err, &apiErr) && apiErr.HTTPStatus == 404
}
