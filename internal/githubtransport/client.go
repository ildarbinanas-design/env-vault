package githubtransport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	maxAttempts          = 5
	maxPages             = 100
	maxTotalRequests     = maxAttempts * maxPages
	maxTotalRetryWait    = 120 * time.Second
	maxStdoutBytes       = 64 << 20
	maxStderrBytes       = 256 << 10
	maxEvidenceBlobBytes = 17 << 20
)

type ReadRequest struct {
	Endpoint string
	Accept   string
	Fields   []string
	Paginate bool
	Slurp    bool
	AllowRaw bool
}

type CommandResult struct {
	Stdout []byte
	Stderr []byte
	Err    error
}

type CommandRunner interface {
	Run(context.Context, []string, []string) CommandResult
}

type InputCommandRunner interface {
	RunInput(context.Context, []string, []string, []byte) CommandResult
}

type ExecRunner struct{ Path string }

func (r ExecRunner) Run(ctx context.Context, args, environment []string) CommandResult {
	return r.run(ctx, args, environment, nil)
}

func (r ExecRunner) RunInput(ctx context.Context, args, environment []string, input []byte) CommandResult {
	return r.run(ctx, args, environment, input)
}

func (r ExecRunner) run(ctx context.Context, args, environment []string, input []byte) CommandResult {
	path := r.Path
	if path == "" {
		path = "gh"
	}
	command := exec.CommandContext(ctx, path, args...)
	command.Env = environment
	if input != nil {
		command.Stdin = bytes.NewReader(input)
	}
	stdout := &boundedBuffer{limit: maxStdoutBytes}
	stderr := &boundedBuffer{limit: maxStderrBytes}
	command.Stdout, command.Stderr = stdout, stderr
	err := command.Run()
	if stdout.overflow || stderr.overflow {
		err = errors.New("gh output exceeded the transport limit")
	}
	return CommandResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes(), Err: err}
}

type Client struct {
	Runner            CommandRunner
	Sleep             func(context.Context, time.Duration) error
	Now               func() time.Time
	capabilityMu      sync.Mutex
	capabilityChecked bool
	capabilityError   *TransportError
	capabilityVersion string
	mutationInput     bool
}

func NewClient() *Client {
	return &Client{Runner: ExecRunner{}, Sleep: sleepContext, Now: time.Now}
}

func (c *Client) Preflight(ctx context.Context) (CapabilitiesDocument, *TransportError) {
	if capabilityErr := c.checkCapabilities(ctx); capabilityErr != nil {
		return CapabilitiesDocument{}, capabilityErr
	}
	c.capabilityMu.Lock()
	ghVersion := c.capabilityVersion
	mutationInput := c.mutationInput
	c.capabilityMu.Unlock()
	capabilities := []string{"strict_raw_rest_get", "complete_pagination", "actions_attempt_identity", "git_blob_exact_bytes", "atomic_no_clobber_output"}
	if mutationInput {
		capabilities = append(capabilities, "one_shot_git_data_mutation")
	}
	return CapabilitiesDocument{
		SchemaID: CapabilitiesSchemaID, SchemaVersion: 1, OK: true,
		TransportVersion: TransportVersion, GitHubAPIVersion: APIVersion,
		Host: Host, GHVersion: ghVersion, MaxAttemptsPerPage: maxAttempts, MaxPages: maxPages,
		MaxTotalRequests: maxTotalRequests, MaxTotalRetryWaitSecs: int(maxTotalRetryWait / time.Second),
		Capabilities: capabilities,
	}, nil
}

func (c *Client) Read(ctx context.Context, request ReadRequest) ([]byte, *TransportError) {
	result, transportErr := c.read(ctx, request)
	return result.Body, transportErr
}

type readResult struct {
	Body       []byte
	HTTPStatus int
	ServerDate string
}

func (c *Client) read(ctx context.Context, request ReadRequest) (readResult, *TransportError) {
	if err := validateReadRequest(request); err != nil {
		return readResult{}, &TransportError{Code: "INPUT_INVALID", Message: err.Error(), Attempts: 0}
	}
	pagination, err := newPaginationScope(request)
	if err != nil {
		return readResult{}, &TransportError{Code: "INPUT_INVALID", Message: err.Error(), Attempts: 0}
	}
	if capabilityErr := c.checkCapabilities(ctx); capabilityErr != nil {
		return readResult{}, capabilityErr
	}
	endpoint := request.Endpoint
	currentPage := pagination.initialPage
	pages := make([][]byte, 0, 1)
	seenPages := map[string]bool{}
	seenEndpoints := map[string]bool{}
	totalWait := time.Duration(0)
	for page := 1; page <= maxPages; page++ {
		if seenEndpoints[endpoint] {
			return readResult{}, &TransportError{Code: "PAGINATION_INVALID", Message: "pagination endpoint loop", Attempts: 1}
		}
		seenEndpoints[endpoint] = true
		pageRequest := request
		if page > 1 {
			pageRequest.Fields = nil
		}
		pageResult, transportErr := c.readPage(ctx, endpoint, pageRequest, totalWait, pagination, currentPage)
		totalWait += pageResult.Wait
		if transportErr != nil {
			return readResult{}, transportErr
		}
		fingerprint := string(pageResult.Body)
		if seenPages[fingerprint] {
			return readResult{}, &TransportError{Code: "PAGINATION_INVALID", Message: "duplicate pagination page", Attempts: 1}
		}
		seenPages[fingerprint] = true
		pages = append(pages, pageResult.Body)
		if !request.Paginate {
			return readResult{Body: pageResult.Body, HTTPStatus: pageResult.HTTPStatus, ServerDate: pageResult.ServerDate}, nil
		}
		if pageResult.Next == "" {
			body, paginationErr := slurpPages(pages)
			return readResult{Body: body, HTTPStatus: pageResult.HTTPStatus, ServerDate: pageResult.ServerDate}, paginationErr
		}
		endpoint = pageResult.Next
		currentPage = pageResult.NextPage
	}
	return readResult{}, &TransportError{Code: "PAGINATION_INVALID", Message: "pagination exceeded 100 pages", Attempts: 1}
}

func (c *Client) Observe(ctx context.Context, endpoint string) (ObservationDocument, *TransportError) {
	result, transportErr := c.read(ctx, ReadRequest{Endpoint: endpoint})
	if transportErr != nil {
		return ObservationDocument{}, transportErr
	}
	if !regexp.MustCompile(`^(?:Mon|Tue|Wed|Thu|Fri|Sat|Sun), [0-9]{2} (?:Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec) [0-9]{4} [0-9]{2}:[0-9]{2}:[0-9]{2} GMT$`).MatchString(result.ServerDate) {
		return ObservationDocument{}, &TransportError{Code: "MALFORMED_RESPONSE", Message: "GitHub Date header is missing or malformed", HTTPStatus: result.HTTPStatus, Attempts: 1}
	}
	parsedDate, err := time.Parse("Mon, 02 Jan 2006 15:04:05 GMT", result.ServerDate)
	if err != nil || parsedDate.Weekday().String()[:3] != result.ServerDate[:3] {
		return ObservationDocument{}, &TransportError{Code: "MALFORMED_RESPONSE", Message: "GitHub Date header is missing or malformed", HTTPStatus: result.HTTPStatus, Attempts: 1, Cause: err}
	}
	digest := sha256.Sum256(result.Body)
	return ObservationDocument{
		SchemaID: ObservationSchemaID, SchemaVersion: 1, OK: true, Endpoint: endpoint,
		HTTPStatus: result.HTTPStatus, ServerDate: result.ServerDate, BodySHA256: hex.EncodeToString(digest[:]),
	}, nil
}

func (c *Client) checkCapabilities(ctx context.Context) *TransportError {
	c.capabilityMu.Lock()
	defer c.capabilityMu.Unlock()
	if c.capabilityChecked {
		return c.capabilityError
	}
	c.capabilityChecked = true
	version := c.Runner.Run(ctx, []string{"--version"}, SanitizedEnvironment())
	versionMatch := regexp.MustCompile(`^gh version ([0-9]+\.[0-9]+\.[0-9]+) \([0-9]{4}-[0-9]{2}-[0-9]{2}\)\r?\n`).FindSubmatch(version.Stdout)
	if version.Err != nil || versionMatch == nil {
		c.capabilityError = &TransportError{Code: "CLI_CAPABILITY_DRIFT", Message: "gh version is unavailable or malformed"}
		return c.capabilityError
	}
	c.capabilityVersion = string(versionMatch[1])
	help := c.Runner.Run(ctx, []string{"api", "--help"}, SanitizedEnvironment())
	if help.Err != nil || !containsAll(string(help.Stdout), "--include", "--hostname", "--method", "--header", "--raw-field") {
		c.capabilityError = &TransportError{Code: "CLI_CAPABILITY_DRIFT", Message: "gh api lacks required transport flags"}
		return c.capabilityError
	}
	c.mutationInput = strings.Contains(string(help.Stdout), "--input")
	return nil
}

type pageReadResult struct {
	Body       []byte
	Next       string
	NextPage   uint64
	Wait       time.Duration
	HTTPStatus int
	ServerDate string
}

func (c *Client) readPage(ctx context.Context, endpoint string, request ReadRequest, priorWait time.Duration, pagination paginationScope, currentPage uint64) (pageReadResult, *TransportError) {
	waited := time.Duration(0)
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		accept := request.Accept
		if accept == "" {
			accept = "application/vnd.github+json"
		}
		args := []string{"api", "--include", "--hostname", Host, "--method", "GET", "--header", "Accept: " + accept, "--header", "X-GitHub-Api-Version: " + APIVersion}
		args = append(args, endpoint)
		for _, field := range request.Fields {
			args = append(args, "--raw-field", field)
		}
		result := c.Runner.Run(ctx, args, sanitizedEnvironment())
		response, parseErr := parseHTTPResponse(result.Stdout)
		if parseErr == nil && response.SelectedVersion != APIVersion {
			return pageReadResult{Wait: waited}, &TransportError{Code: "CLI_CAPABILITY_DRIFT", Message: "GitHub selected an unexpected API version", HTTPStatus: response.Status, Attempts: attempt}
		}
		var classified *TransportError
		if parseErr == nil {
			classified = validateReviewedResponse(response, request, attempt)
		}
		if classified == nil {
			classified = classifyResult(result, response, parseErr, attempt, c.Now())
		}
		if classified == nil {
			next, nextPage, err := nextEndpoint(response.Link, pagination, currentPage)
			if err != nil {
				return pageReadResult{Wait: waited}, &TransportError{Code: "PAGINATION_INVALID", Message: err.Error(), HTTPStatus: response.Status, Attempts: attempt}
			}
			return pageReadResult{Body: response.Body, Next: next, NextPage: nextPage, Wait: waited, HTTPStatus: response.Status, ServerDate: response.Headers["date"]}, nil
		}
		if !classified.Retriable || attempt == maxAttempts {
			if attempt == maxAttempts && classified.Retriable {
				classified.Retriable = false
				classified.Message += " after bounded retry attempts were exhausted"
			}
			return pageReadResult{Wait: waited}, classified
		}
		delay := classified.RetryAfter
		if !classified.RetryDelaySet {
			delay = time.Duration(1<<uint(attempt-1)) * time.Second
		}
		if priorWait+waited+delay > maxTotalRetryWait {
			classified.Retriable = false
			classified.Message = "retry wait exceeds bounded transport budget"
			return pageReadResult{Wait: waited}, classified
		}
		if err := c.Sleep(ctx, delay); err != nil {
			return pageReadResult{Wait: waited}, &TransportError{Code: "TRANSPORT_FAILED", Message: "transport retry wait was cancelled", Retriable: false, Attempts: attempt, Cause: err}
		}
		waited += delay
	}
	panic("unreachable")
}

func validateReviewedResponse(response httpResponse, request ReadRequest, attempt int) *TransportError {
	if request.AllowRaw && response.Status >= 200 && response.Status < 300 {
		if !isReviewedRawContentType(response.ContentType) {
			return &TransportError{Code: "MALFORMED_RESPONSE", Message: "GitHub raw response content type is not the reviewed media type", HTTPStatus: response.Status, Attempts: attempt}
		}
		return nil
	}
	if !isReviewedJSONContentType(response.ContentType) {
		return &TransportError{Code: "MALFORMED_RESPONSE", Message: "GitHub response content type is not the reviewed JSON media type", HTTPStatus: response.Status, Attempts: attempt}
	}
	if len(response.Body) == 0 {
		return &TransportError{Code: "MALFORMED_RESPONSE", Message: "GitHub returned an empty JSON response body", HTTPStatus: response.Status, Retriable: true, Attempts: attempt}
	}
	if err := validateVendorJSON(response.Body); err != nil {
		return &TransportError{Code: "MALFORMED_RESPONSE", Message: "GitHub returned malformed or non-canonical JSON", HTTPStatus: response.Status, Retriable: true, Attempts: attempt, Cause: err}
	}
	if response.Status < 200 || response.Status >= 300 {
		object, err := exactObject(response.Body)
		if err != nil {
			return &TransportError{Code: "MALFORMED_RESPONSE", Message: "GitHub error response is not an object", HTTPStatus: response.Status, Attempts: attempt, Cause: err}
		}
		if _, err := exactString(object, "message"); err != nil {
			return &TransportError{Code: "MALFORMED_RESPONSE", Message: "GitHub error response has no exact non-empty message", HTTPStatus: response.Status, Attempts: attempt, Cause: err}
		}
	}
	return nil
}

type httpResponse struct {
	Status          int
	Headers         map[string]string
	Body            []byte
	SelectedVersion string
	Link            string
	ContentType     string
}

var statusLine = regexp.MustCompile(`^HTTP/(?:1\.[01]|2(?:\.0)?|3(?:\.0)?) ([0-9]{3})(?: |$)`)

func parseHTTPResponse(data []byte) (httpResponse, error) {
	separator := bytes.Index(data, []byte("\r\n\r\n"))
	separatorLength := 4
	if separator < 0 {
		separator = bytes.Index(data, []byte("\n\n"))
		separatorLength = 2
	}
	if separator < 0 {
		return httpResponse{}, errors.New("complete HTTP response headers were not observed")
	}
	headerBytes := bytes.ReplaceAll(data[:separator], []byte("\r\n"), []byte("\n"))
	lines := strings.Split(string(headerBytes), "\n")
	if len(lines) == 0 {
		return httpResponse{}, errors.New("HTTP status line is absent")
	}
	match := statusLine.FindStringSubmatch(lines[0])
	if match == nil {
		return httpResponse{}, errors.New("HTTP status line is malformed")
	}
	status, _ := strconv.Atoi(match[1])
	headers := map[string]string{}
	for _, line := range lines[1:] {
		name, value, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(name) == "" {
			return httpResponse{}, errors.New("HTTP response header is malformed")
		}
		key := strings.ToLower(strings.TrimSpace(name))
		if _, duplicate := headers[key]; duplicate {
			return httpResponse{}, fmt.Errorf("duplicate HTTP response header %q", key)
		}
		headers[key] = strings.TrimSpace(value)
	}
	return httpResponse{
		Status: status, Headers: headers, Body: append([]byte(nil), data[separator+separatorLength:]...),
		SelectedVersion: headers["x-github-api-version-selected"],
		Link:            headers["link"], ContentType: headers["content-type"],
	}, nil
}

func isReviewedJSONContentType(value string) bool {
	mediaType, parameters, err := mime.ParseMediaType(value)
	return err == nil && reviewedMediaParameters(parameters) && (strings.EqualFold(mediaType, "application/json") || strings.EqualFold(mediaType, "application/vnd.github+json"))
}

func isReviewedRawContentType(value string) bool {
	mediaType, parameters, err := mime.ParseMediaType(value)
	return err == nil && reviewedMediaParameters(parameters) && strings.EqualFold(mediaType, "application/vnd.github.raw+json")
}

func reviewedMediaParameters(parameters map[string]string) bool {
	if len(parameters) == 0 {
		return true
	}
	charset, present := parameters["charset"]
	return len(parameters) == 1 && present && strings.EqualFold(charset, "utf-8")
}

func classifyResult(result CommandResult, response httpResponse, parseErr error, attempt int, now time.Time) *TransportError {
	if parseErr != nil {
		return &TransportError{Code: "TRANSPORT_FAILED", Message: "GitHub transport did not return a complete HTTP response", Retriable: true, Attempts: attempt, Cause: result.Err}
	}
	if response.Status >= 200 && response.Status < 300 && result.Err == nil {
		return nil
	}
	errorValue := &TransportError{HTTPStatus: response.Status, Attempts: attempt, Cause: result.Err}
	switch response.Status {
	case 401:
		errorValue.Code, errorValue.Message = "AUTH_REQUIRED", "GitHub authentication is required"
	case 404:
		errorValue.Code, errorValue.Message = "REMOTE_NOT_FOUND", "GitHub resource was not found"
	case 429:
		errorValue.Code, errorValue.Message = "RATE_LIMITED", "GitHub rate limit was reached"
		errorValue.RetryAfter, errorValue.Retriable = retryDelay(response.Headers, now, true)
		errorValue.RetryDelaySet = errorValue.Retriable
	case 403:
		delay, validRateWait := retryDelay(response.Headers, now, false)
		secondaryLimit := strings.Contains(strings.ToLower(string(response.Body)+string(result.Stderr)), "secondary rate limit")
		isRateLimit := response.Headers["retry-after"] != "" || response.Headers["x-ratelimit-remaining"] == "0" || secondaryLimit
		if isRateLimit {
			if secondaryLimit && response.Headers["retry-after"] == "" && response.Headers["x-ratelimit-remaining"] != "0" {
				delay, validRateWait = 60*time.Second, true
			}
			errorValue.Code, errorValue.Message, errorValue.Retriable = "RATE_LIMITED", "GitHub rate limit was reached", validRateWait
			errorValue.RetryAfter = delay
			errorValue.RetryDelaySet = validRateWait
		} else {
			errorValue.Code, errorValue.Message = "AUTH_FORBIDDEN", "GitHub authorization is insufficient"
		}
	default:
		if response.Status >= 500 && response.Status <= 599 {
			errorValue.Code, errorValue.Message, errorValue.Retriable = "TRANSPORT_FAILED", "GitHub returned a transient server response", true
			if response.Headers["retry-after"] != "" {
				errorValue.RetryAfter, errorValue.RetryDelaySet = retryDelay(response.Headers, now, false)
				if !errorValue.RetryDelaySet {
					errorValue.Retriable = false
					errorValue.Message = "GitHub returned malformed retry metadata"
				}
			}
		} else {
			errorValue.Code, errorValue.Message = "REMOTE_STATE_UNKNOWN", "GitHub returned an unsupported HTTP response"
		}
	}
	return errorValue
}

func containsAll(value string, needles ...string) bool {
	for _, needle := range needles {
		if !strings.Contains(value, needle) {
			return false
		}
	}
	return true
}

func retryDelay(headers map[string]string, now time.Time, rateLimited bool) (time.Duration, bool) {
	if value := headers["retry-after"]; value != "" {
		seconds, err := strconv.Atoi(value)
		if err == nil && seconds >= 0 {
			return time.Duration(seconds) * time.Second, true
		}
		return 0, false
	}
	if headers["x-ratelimit-remaining"] == "0" {
		if headers["x-ratelimit-reset"] == "" {
			return 0, false
		}
		reset, err := strconv.ParseInt(headers["x-ratelimit-reset"], 10, 64)
		if err == nil {
			delay := time.Unix(reset, 0).Sub(now)
			if delay < 0 {
				return 0, true
			}
			return delay, true
		}
		return 0, false
	}
	if rateLimited {
		return 60 * time.Second, true
	}
	return 0, false
}

func validateReadRequest(request ReadRequest) error {
	if err := validateEndpoint(request.Endpoint); err != nil {
		return err
	}
	if request.Paginate != request.Slurp {
		return errors.New("complete pagination requires --paginate and --slurp together")
	}
	if strings.ContainsAny(request.Accept, "\r\n") {
		return errors.New("Accept value contains a line break")
	}
	if request.AllowRaw {
		if request.Accept != "application/vnd.github.raw+json" {
			return errors.New("raw content reads require the exact reviewed media type")
		}
	} else if request.Accept != "" && request.Accept != "application/vnd.github+json" {
		return errors.New("strict REST JSON reads require the reviewed JSON media type")
	}
	parsedEndpoint, _ := url.Parse(request.Endpoint)
	endpointQuery, _ := url.ParseQuery(parsedEndpoint.RawQuery)
	seenFieldNames := make(map[string]bool, len(endpointQuery)+len(request.Fields))
	for name, values := range endpointQuery {
		canonical := strings.ToLower(name)
		if len(values) != 1 || seenFieldNames[canonical] {
			return errors.New("endpoint query contains duplicate or ambiguous names")
		}
		seenFieldNames[canonical] = true
	}
	for _, field := range request.Fields {
		name, value, ok := strings.Cut(field, "=")
		canonical := strings.ToLower(name)
		if !ok || !regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`).MatchString(name) || containsUnsafeQueryValue(value) || seenFieldNames[canonical] {
			return errors.New("query field is malformed")
		}
		seenFieldNames[canonical] = true
	}
	return nil
}

func validateEndpoint(endpoint string) error {
	if endpoint == "" || strings.Contains(strings.ToLower(endpoint), "graphql") {
		return errors.New("one relative non-GraphQL endpoint is required")
	}
	for _, value := range endpoint {
		if value < 0x20 || value == 0x7f || value == '\\' {
			return errors.New("endpoint contains a forbidden control or path character")
		}
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "" || parsed.Host != "" || parsed.User != nil || parsed.Opaque != "" || parsed.Fragment != "" || strings.HasPrefix(parsed.EscapedPath(), "/") {
		return errors.New("absolute endpoints and custom hosts are forbidden")
	}
	rawSegments := strings.Split(parsed.EscapedPath(), "/")
	decodedSegments := make([]string, len(rawSegments))
	for index, rawSegment := range rawSegments {
		decoded, decodeErr := url.PathUnescape(rawSegment)
		if decodeErr != nil || decoded == "" || decoded == "." || decoded == ".." || strings.ContainsAny(decoded, "/\\") {
			return errors.New("endpoint contains an unsafe path segment")
		}
		for _, value := range decoded {
			if value < 0x20 || value == 0x7f {
				return errors.New("endpoint contains an unsafe path segment")
			}
		}
		decodedSegments[index] = decoded
	}
	if len(decodedSegments) == 0 {
		return errors.New("endpoint is outside the release transport allowlist")
	}
	switch decodedSegments[0] {
	case "repos":
		if len(decodedSegments) < 3 || !validRepository(decodedSegments[1]+"/"+decodedSegments[2]) {
			return errors.New("repository endpoint has an invalid owner/name identity")
		}
	case "user", "installation":
	default:
		return errors.New("endpoint is outside the release transport allowlist")
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return errors.New("endpoint query is malformed")
	}
	seenQueryNames := make(map[string]bool, len(query))
	for name, values := range query {
		canonical := strings.ToLower(name)
		if name == "" || len(values) != 1 || seenQueryNames[canonical] || containsUnsafeQueryValue(name) {
			return errors.New("endpoint query contains an unsafe name")
		}
		seenQueryNames[canonical] = true
		for _, value := range values {
			if containsUnsafeQueryValue(value) {
				return errors.New("endpoint query contains an unsafe value")
			}
		}
	}
	return nil
}

func containsUnsafeQueryValue(value string) bool {
	if strings.Contains(value, "\\") {
		return true
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return true
		}
	}
	return false
}

func SanitizedEnvironment() []string {
	forbidden := map[string]bool{"DEBUG": true, "CLICOLOR_FORCE": true, "GH_DEBUG": true, "GH_HOST": true, "GH_ENTERPRISE_TOKEN": true, "GITHUB_ENTERPRISE_TOKEN": true, "GH_ENTERPRISE_HOST": true, "GH_FORCE_TTY": true, "GH_NO_UPDATE_NOTIFIER": true, "GH_PROMPT_DISABLED": true, "GH_TELEMETRY": true, "NO_COLOR": true, "GIT_TRACE": true, "GIT_TRACE_CURL": true, "GIT_CURL_VERBOSE": true, "GIT_TRACE_PACKET": true}
	environment := make([]string, 0, len(os.Environ())+1)
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if !forbidden[name] {
			environment = append(environment, entry)
		}
	}
	return append(environment, "GH_HOST="+Host, "GH_PROMPT_DISABLED=1", "GH_NO_UPDATE_NOTIFIER=1", "NO_COLOR=1")
}

type paginationScope struct {
	path        string
	invariants  map[string]string
	initialPage uint64
}

func newPaginationScope(request ReadRequest) (paginationScope, error) {
	parsed, _ := url.Parse(request.Endpoint)
	query, _ := url.ParseQuery(parsed.RawQuery)
	canonical := make(map[string]string, len(query)+len(request.Fields))
	for name, values := range query {
		canonical[name] = values[0]
	}
	for _, field := range request.Fields {
		name, value, _ := strings.Cut(field, "=")
		canonical[name] = value
	}

	scope := paginationScope{
		path:        parsed.EscapedPath(),
		invariants:  make(map[string]string, len(canonical)),
		initialPage: 1,
	}
	for name, value := range canonical {
		switch {
		case name == "page":
			page, err := parsePaginationInteger("page", value, ^uint64(0))
			if err != nil {
				return paginationScope{}, err
			}
			scope.initialPage = page
		case strings.EqualFold(name, "page"):
			return paginationScope{}, errors.New("pagination page control has non-canonical spelling")
		case name == "per_page":
			if _, err := parsePaginationInteger("per_page", value, 100); err != nil {
				return paginationScope{}, err
			}
			scope.invariants[name] = value
		case strings.EqualFold(name, "per_page"):
			return paginationScope{}, errors.New("pagination per_page control has non-canonical spelling")
		default:
			scope.invariants[name] = value
		}
	}
	return scope, nil
}

func parsePaginationInteger(name, value string, maximum uint64) (uint64, error) {
	if !regexp.MustCompile(`^[1-9][0-9]*$`).MatchString(value) {
		return 0, fmt.Errorf("pagination %s control is not a canonical positive integer", name)
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil || parsed > maximum {
		return 0, fmt.Errorf("pagination %s control is outside the reviewed range", name)
	}
	return parsed, nil
}

func nextEndpoint(link string, scope paginationScope, currentPage uint64) (string, uint64, error) {
	if link == "" {
		return "", 0, nil
	}
	next := ""
	nextPage := uint64(0)
	for _, part := range strings.Split(link, ",") {
		section := strings.TrimSpace(part)
		left, right := strings.Index(section, "<"), strings.Index(section, ">")
		if left != 0 || right <= left+1 || right+2 > len(section) || section[right+1] != ';' {
			return "", 0, errors.New("pagination next link is malformed")
		}
		relation := strings.TrimSpace(section[right+2:])
		if !regexp.MustCompile(`^rel="(?:next|prev|first|last)"$`).MatchString(relation) {
			return "", 0, errors.New("pagination relation is malformed or unsupported")
		}
		if relation != `rel="next"` {
			continue
		}
		parsed, err := url.Parse(section[left+1 : right])
		if err != nil || parsed.Scheme != "https" || parsed.Host != "api.github.com" || parsed.User != nil || parsed.Fragment != "" {
			return "", 0, errors.New("pagination next link has an untrusted origin")
		}
		nextPath := strings.TrimPrefix(parsed.EscapedPath(), "/")
		if nextPath != scope.path {
			return "", 0, errors.New("pagination next link changed the endpoint path")
		}
		if next != "" {
			return "", 0, errors.New("pagination response contains multiple next links")
		}
		next = nextPath
		if parsed.RawQuery != "" {
			next += "?" + parsed.RawQuery
		}
		if err := validateEndpoint(next); err != nil {
			return "", 0, fmt.Errorf("pagination next link is unsafe: %w", err)
		}
		query, err := url.ParseQuery(parsed.RawQuery)
		if err != nil {
			return "", 0, errors.New("pagination next link query is malformed")
		}
		for name, value := range scope.invariants {
			values, ok := query[name]
			if !ok || len(values) != 1 || values[0] != value {
				return "", 0, errors.New("pagination next link changed or dropped the initial query scope")
			}
		}
		for name := range query {
			if name != "page" {
				if _, ok := scope.invariants[name]; !ok {
					return "", 0, errors.New("pagination next link added an unreviewed query field")
				}
			}
		}
		pageValues, ok := query["page"]
		if !ok || len(pageValues) != 1 {
			return "", 0, errors.New("pagination next link has no unique page control")
		}
		page, err := parsePaginationInteger("page", pageValues[0], ^uint64(0))
		if err != nil || currentPage == ^uint64(0) || page != currentPage+1 {
			return "", 0, errors.New("pagination next link page control does not advance exactly once")
		}
		nextPage = page
	}
	return next, nextPage, nil
}

func sanitizedEnvironment() []string { return SanitizedEnvironment() }

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type boundedBuffer struct {
	data     bytes.Buffer
	limit    int
	overflow bool
}

func (b *boundedBuffer) Write(data []byte) (int, error) {
	if b.data.Len()+len(data) > b.limit {
		remaining := b.limit - b.data.Len()
		if remaining > 0 {
			_, _ = b.data.Write(data[:remaining])
		}
		b.overflow = true
		return len(data), nil
	}
	return b.data.Write(data)
}

func (b *boundedBuffer) Bytes() []byte { return b.data.Bytes() }

func slurpPages(pages [][]byte) ([]byte, *TransportError) {
	values := make([]any, 0, len(pages))
	for _, page := range pages {
		var value any
		decoder := json.NewDecoder(bytes.NewReader(page))
		decoder.UseNumber()
		if err := decoder.Decode(&value); err != nil {
			return nil, &TransportError{Code: "MALFORMED_RESPONSE", Message: "paginated response is not JSON", Attempts: 1, Cause: err}
		}
		values = append(values, value)
	}
	if err := validateCompletePages(values); err != nil {
		return nil, &TransportError{Code: "PAGINATION_INVALID", Message: err.Error(), Attempts: 1}
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return nil, &TransportError{Code: "MALFORMED_RESPONSE", Message: "cannot encode paginated response", Attempts: 1, Cause: err}
	}
	return append(encoded, '\n'), nil
}

func validateCompletePages(pages []any) error {
	seenIDs := map[string]bool{}
	totalCount := int64(-1)
	observed := int64(0)
	for pageIndex, page := range pages {
		items, declared, err := paginationItems(page)
		if err != nil {
			return fmt.Errorf("page %d: %w", pageIndex+1, err)
		}
		if declared >= 0 {
			if totalCount >= 0 && totalCount != declared {
				return errors.New("pagination total_count changed between pages")
			}
			totalCount = declared
		}
		for _, item := range items {
			object, ok := item.(map[string]any)
			if !ok {
				return errors.New("paginated item is not an object")
			}
			id, present := object["id"]
			if !present {
				return errors.New("paginated item has no exact id")
			}
			idNumber, ok := id.(json.Number)
			if !ok {
				return errors.New("paginated item id is not a number")
			}
			parsedID, err := strconv.ParseInt(idNumber.String(), 10, 64)
			if err != nil || parsedID <= 0 {
				return errors.New("paginated item id is not an integer")
			}
			if seenIDs[idNumber.String()] {
				return fmt.Errorf("duplicate paginated item id %s", idNumber.String())
			}
			seenIDs[idNumber.String()] = true
			observed++
		}
	}
	if totalCount >= 0 && totalCount != observed {
		return fmt.Errorf("pagination is truncated: total_count=%d observed=%d", totalCount, observed)
	}
	return nil
}

func paginationItems(page any) ([]any, int64, error) {
	if array, ok := page.([]any); ok {
		return array, -1, nil
	}
	object, ok := page.(map[string]any)
	if !ok {
		return nil, -1, errors.New("page is neither an array nor an object")
	}
	var items []any
	collectionCount := 0
	for _, name := range []string{"artifacts", "workflow_runs", "jobs", "repositories", "attestations", "items"} {
		value, present := object[name]
		if !present {
			continue
		}
		array, ok := value.([]any)
		if !ok {
			return nil, -1, fmt.Errorf("%s is not an array", name)
		}
		items = array
		collectionCount++
	}
	if collectionCount != 1 {
		return nil, -1, errors.New("page does not contain exactly one supported collection")
	}
	declared := int64(-1)
	if value, present := object["total_count"]; present {
		number, ok := value.(json.Number)
		if !ok {
			return nil, -1, errors.New("total_count is not a number")
		}
		parsed, err := strconv.ParseInt(number.String(), 10, 64)
		if err != nil || parsed < 0 {
			return nil, -1, errors.New("total_count is not a non-negative integer")
		}
		declared = parsed
	}
	if declared < 0 {
		return nil, -1, errors.New("supported paginated object has no total_count")
	}
	return items, declared, nil
}
