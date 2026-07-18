package githubtransport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type scriptedRunner struct {
	responses []CommandResult
	apiCalls  [][]string
	allCalls  [][]string
	inputs    [][]byte
}

func (r *scriptedRunner) RunInput(ctx context.Context, args []string, environment []string, input []byte) CommandResult {
	r.inputs = append(r.inputs, append([]byte(nil), input...))
	return r.Run(ctx, args, environment)
}

func (r *scriptedRunner) Run(_ context.Context, args []string, _ []string) CommandResult {
	r.allCalls = append(r.allCalls, append([]string(nil), args...))
	if len(args) == 1 && args[0] == "--version" {
		return CommandResult{Stdout: []byte("gh version 2.96.0 (2026-07-02)\nhttps://github.com/cli/cli/releases/tag/v2.96.0\n")}
	}
	if len(args) == 2 && args[0] == "api" && args[1] == "--help" {
		return CommandResult{Stdout: []byte("--include --hostname --method --header --raw-field --input\n")}
	}
	r.apiCalls = append(r.apiCalls, append([]string(nil), args...))
	if len(r.responses) == 0 {
		return CommandResult{Err: errors.New("unexpected transport call")}
	}
	result := r.responses[0]
	r.responses = r.responses[1:]
	return result
}

func liveResponse(status int, extraHeaders, body string) CommandResult {
	statusText := map[int]string{200: "OK", 401: "Unauthorized", 403: "Forbidden", 404: "Not Found", 429: "Too Many Requests", 500: "Server Error", 503: "Service Unavailable"}[status]
	return CommandResult{Stdout: []byte(fmt.Sprintf(
		"HTTP/2 %d %s\r\nContent-Type: application/json; charset=utf-8\r\nDate: Fri, 17 Jul 2026 12:00:00 GMT\r\nVary: Accept, Accept-Encoding\r\nX-GitHub-Api-Version-Selected: 2022-11-28\r\n%s\r\n%s",
		status, statusText, extraHeaders, body,
	))}
}

func testClient(runner *scriptedRunner, sleeps *[]time.Duration) *Client {
	return &Client{
		Runner: runner,
		Now:    func() time.Time { return time.Unix(1000, 0) },
		Sleep: func(_ context.Context, delay time.Duration) error {
			*sleeps = append(*sleeps, delay)
			return nil
		},
	}
}

func TestReadPreservesRawBodyAndPinsTransport(t *testing.T) {
	runner := &scriptedRunner{responses: []CommandResult{liveResponse(200, "", "{\"ok\":true}\r\n")}}
	var sleeps []time.Duration
	data, err := testClient(runner, &sleeps).Read(context.Background(), ReadRequest{Endpoint: "repos/example/repo"})
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "{\"ok\":true}\r\n" {
		t.Fatalf("body bytes changed: %q", data)
	}
	call := strings.Join(runner.apiCalls[0], " ")
	for _, required := range []string{"api --include --hostname github.com --method GET", "Accept: application/vnd.github+json", "X-GitHub-Api-Version: 2022-11-28", "repos/example/repo"} {
		if !strings.Contains(call, required) {
			t.Fatalf("call %q lacks %q", call, required)
		}
	}
	if strings.Count(call, "Accept:") != 1 {
		t.Fatalf("Accept header count in %q", call)
	}
}

func TestNonPaginatedReadDoesNotInterpretInformationalLinkMetadata(t *testing.T) {
	observed := "Link: <https://docs.github.com/en/rest/about-the-rest-api/api-versions>; rel=\"deprecation\"; type=\"text/html\"\r\n" +
		"Deprecation: Tue, 10 Mar 2026 00:00:00 GMT\r\n" +
		"Sunset: Fri, 10 Mar 2028 00:00:00 GMT\r\n"
	for name, header := range map[string]string{
		"observed deprecation metadata": observed,
		"malformed irrelevant link":     "Link: not-a-link-value\r\n",
	} {
		t.Run(name, func(t *testing.T) {
			runner := &scriptedRunner{responses: []CommandResult{liveResponse(200, header, `{"attestations":[{"id":1}]}`)}}
			var sleeps []time.Duration
			data, transportErr := testClient(runner, &sleeps).Read(context.Background(), ReadRequest{
				Endpoint: "repos/example/repo/attestations/sha256:abc", Fields: []string{"predicate_type=https://slsa.dev/provenance/v1"},
			})
			if transportErr != nil || string(data) != `{"attestations":[{"id":1}]}` || len(runner.apiCalls) != 1 {
				t.Fatalf("data=%s error=%+v calls=%d", data, transportErr, len(runner.apiCalls))
			}
		})
	}
}

func TestReadRetriesSeriallyAndHonorsRetryAfter(t *testing.T) {
	first := liveResponse(429, "Retry-After: 7\r\n", `{"message":"rate limited"}`)
	first.Err = errors.New("gh exited 1")
	runner := &scriptedRunner{responses: []CommandResult{first, liveResponse(200, "", `{"ok":true}`)}}
	var sleeps []time.Duration
	data, err := testClient(runner, &sleeps).Read(context.Background(), ReadRequest{Endpoint: "repos/example/repo"})
	if err != nil || string(data) != `{"ok":true}` {
		t.Fatalf("data=%q err=%v", data, err)
	}
	if len(runner.apiCalls) != 2 || fmt.Sprint(sleeps) != "[7s]" {
		t.Fatalf("calls=%d sleeps=%v", len(runner.apiCalls), sleeps)
	}
}

func TestReadHonorsExplicitZeroRetryAfterWithoutBackoff(t *testing.T) {
	first := failureResponse(429, "Retry-After: 0\r\n", `{"message":"rate limited"}`)
	runner := &scriptedRunner{responses: []CommandResult{first, liveResponse(200, "", `{"ok":true}`)}}
	var sleeps []time.Duration
	_, err := testClient(runner, &sleeps).Read(context.Background(), ReadRequest{Endpoint: "repos/example/repo"})
	if err != nil || len(sleeps) != 1 || sleeps[0] != 0 {
		t.Fatalf("error=%v sleeps=%v", err, sleeps)
	}
}

func TestReadHonorsExplicitZeroRetryAfterForTransientServerResponse(t *testing.T) {
	first := failureResponse(503, "Retry-After: 0\r\n", `{"message":"temporarily unavailable"}`)
	runner := &scriptedRunner{responses: []CommandResult{first, liveResponse(200, "", `{"ok":true}`)}}
	var sleeps []time.Duration
	_, err := testClient(runner, &sleeps).Read(context.Background(), ReadRequest{Endpoint: "repos/example/repo"})
	if err != nil || len(sleeps) != 1 || sleeps[0] != 0 {
		t.Fatalf("error=%v sleeps=%v", err, sleeps)
	}
}

func TestReadBoundsRateLimitAttemptsAndWaitBudget(t *testing.T) {
	t.Run("terminal attempts", func(t *testing.T) {
		responses := make([]CommandResult, maxAttempts)
		for index := range responses {
			responses[index] = failureResponse(429, "Retry-After: 0\r\n", `{"message":"rate limited"}`)
		}
		runner := &scriptedRunner{responses: responses}
		var sleeps []time.Duration
		_, transportErr := testClient(runner, &sleeps).Read(context.Background(), ReadRequest{Endpoint: "repos/example/repo"})
		if transportErr == nil || transportErr.Code != "RATE_LIMITED" || transportErr.Retriable || transportErr.Attempts != maxAttempts || len(runner.apiCalls) != maxAttempts {
			t.Fatalf("error=%+v calls=%d", transportErr, len(runner.apiCalls))
		}
	})
	t.Run("wait budget", func(t *testing.T) {
		responses := make([]CommandResult, maxAttempts)
		for index := range responses {
			responses[index] = failureResponse(429, "Retry-After: 31\r\n", `{"message":"rate limited"}`)
		}
		runner := &scriptedRunner{responses: responses}
		var sleeps []time.Duration
		_, transportErr := testClient(runner, &sleeps).Read(context.Background(), ReadRequest{Endpoint: "repos/example/repo"})
		if transportErr == nil || transportErr.Code != "RATE_LIMITED" || transportErr.Retriable || transportErr.Attempts != 4 || len(runner.apiCalls) != 4 || len(sleeps) != 3 {
			t.Fatalf("error=%+v calls=%d sleeps=%v", transportErr, len(runner.apiCalls), sleeps)
		}
	})
}

func TestPaginatedReadRejectsSaturatedResetWithoutAccumulatedWaitOverflow(t *testing.T) {
	next := `<https://api.github.com/repos/example/repo/items?per_page=1&page=2>; rel="next"`
	runner := &scriptedRunner{responses: []CommandResult{
		failureResponse(429, "Retry-After: 1\r\n", `{"message":"rate limited"}`),
		liveResponse(200, "Link: "+next+"\r\n", `[{"id":1}]`),
		failureResponse(429, "X-RateLimit-Remaining: 0\r\nX-RateLimit-Reset: 9223372036854775807\r\n", `{"message":"rate limited"}`),
	}}
	var sleeps []time.Duration
	_, transportErr := testClient(runner, &sleeps).Read(context.Background(), ReadRequest{
		Endpoint: "repos/example/repo/items?per_page=1", Paginate: true, Slurp: true,
	})
	if transportErr == nil || transportErr.Code != "RATE_LIMITED" || transportErr.Retriable ||
		!strings.Contains(transportErr.Message, "bounded transport budget") || len(runner.apiCalls) != 3 || fmt.Sprint(sleeps) != "[1s]" {
		t.Fatalf("error=%+v calls=%d sleeps=%v", transportErr, len(runner.apiCalls), sleeps)
	}
}

func TestReadFailsOnCLICapabilityDriftBeforeAPI(t *testing.T) {
	runner := &scriptedRunner{}
	runner.allCalls = nil
	// Override the scripted runner's recognized version by wrapping it.
	drift := &capabilityDriftRunner{scriptedRunner: runner}
	var sleeps []time.Duration
	client := &Client{Runner: drift, Now: time.Now, Sleep: func(context.Context, time.Duration) error { return nil }}
	_, err := client.Read(context.Background(), ReadRequest{Endpoint: "repos/example/repo"})
	if err == nil || err.Code != "CLI_CAPABILITY_DRIFT" || len(runner.apiCalls) != 0 {
		t.Fatalf("error=%+v apiCalls=%d", err, len(runner.apiCalls))
	}
	_ = sleeps
}

func TestReadRejectsTraversalAndAmbiguousEndpointsBeforeCapabilityOrNetwork(t *testing.T) {
	for name, endpoint := range map[string]string{
		"dot segments":           "repos/../../user",
		"encoded dot segments":   "repos/%2e%2e/%2e%2e/user",
		"encoded slash":          "repos/example/repo/%2Fuser",
		"backslash":              `repos/example/repo\user`,
		"encoded backslash":      "repos/example/repo/%5cuser",
		"empty owner":            "repos//repo/actions/runs",
		"dot owner":              "repos/./repo/actions/runs",
		"query control":          "repos/example/repo?cursor=%0a",
		"fragment":               "repos/example/repo#user",
		"encoded absolute owner": "repos/%2e%2e/repo",
	} {
		t.Run(name, func(t *testing.T) {
			runner := &scriptedRunner{}
			var sleeps []time.Duration
			_, transportErr := testClient(runner, &sleeps).Read(context.Background(), ReadRequest{Endpoint: endpoint})
			if transportErr == nil || transportErr.Code != "INPUT_INVALID" {
				t.Fatalf("error=%+v", transportErr)
			}
			if len(runner.allCalls) != 0 {
				t.Fatalf("invalid endpoint reached capability/network transport: %v", runner.allCalls)
			}
		})
	}
}

type capabilityDriftRunner struct{ scriptedRunner *scriptedRunner }

func (r *capabilityDriftRunner) Run(ctx context.Context, args []string, env []string) CommandResult {
	if len(args) == 1 && args[0] == "--version" {
		return CommandResult{Stdout: []byte("gh version future\n")}
	}
	return r.scriptedRunner.Run(ctx, args, env)
}

func TestReadClassifiesFailuresWithoutConvertingUnknownToAbsence(t *testing.T) {
	tests := []struct {
		name      string
		result    CommandResult
		code      string
		status    int
		calls     int
		retriable bool
	}{
		{"401", failureResponse(401, "", `{"message":"bad credentials"}`), "AUTH_REQUIRED", 401, 1, false},
		{"403", failureResponse(403, "", `{"message":"forbidden"}`), "AUTH_FORBIDDEN", 403, 1, false},
		{"404", failureResponse(404, "", `{"message":"not found"}`), "REMOTE_NOT_FOUND", 404, 1, false},
		{"malformed retry after", failureResponse(403, "Retry-After: later\r\n", `{"message":"secondary rate limit"}`), "RATE_LIMITED", 403, 1, false},
		{"overflowing retry after", failureResponse(403, "Retry-After: 9223372036854775807\r\n", `{"message":"secondary rate limit"}`), "RATE_LIMITED", 403, 1, false},
		{"incomplete HTTP", CommandResult{Stdout: []byte(`{"partial":`), Err: errors.New("dns failure")}, "TRANSPORT_FAILED", 0, 5, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			responses := make([]CommandResult, test.calls)
			for index := range responses {
				responses[index] = test.result
			}
			runner := &scriptedRunner{responses: responses}
			var sleeps []time.Duration
			_, transportErr := testClient(runner, &sleeps).Read(context.Background(), ReadRequest{Endpoint: "repos/example/repo"})
			if transportErr == nil || transportErr.Code != test.code || transportErr.HTTPStatus != test.status || transportErr.Retriable != test.retriable {
				t.Fatalf("error=%+v", transportErr)
			}
			if len(runner.apiCalls) != test.calls {
				t.Fatalf("calls=%d want=%d", len(runner.apiCalls), test.calls)
			}
		})
	}
}

func failureResponse(status int, headers, body string) CommandResult {
	response := liveResponse(status, headers, body)
	response.Err = errors.New("gh exited 1")
	return response
}

func TestReadRejectsMalformedDuplicateAndCaseVariantJSON(t *testing.T) {
	for name, body := range map[string]string{
		"malformed":           `{"id":`,
		"duplicate":           `{"id":1,"id":2}`,
		"case variant":        `{"id":1,"ID":2}`,
		"nested duplicate":    `{"outer":{"id":1,"id":2}}`,
		"nested case variant": `{"outer":[{"id":1,"ID":2}]}`,
		"trailing":            `{"id":1} true`,
	} {
		t.Run(name, func(t *testing.T) {
			responses := make([]CommandResult, 5)
			for index := range responses {
				responses[index] = liveResponse(200, "", body)
			}
			runner := &scriptedRunner{responses: responses}
			var sleeps []time.Duration
			_, transportErr := testClient(runner, &sleeps).Read(context.Background(), ReadRequest{Endpoint: "repos/example/repo"})
			if transportErr == nil || transportErr.Code != "MALFORMED_RESPONSE" || len(runner.apiCalls) != 5 {
				t.Fatalf("error=%+v calls=%d", transportErr, len(runner.apiCalls))
			}
		})
	}
}

func TestReadRejectsUnreviewedJSONMediaTypes(t *testing.T) {
	for name, contentType := range map[string]string{
		"substring":         "text/notjson",
		"bogus application": "application/notjson",
		"missing":           "",
		"wrong charset":     "application/json; charset=iso-8859-1",
		"extra parameter":   "application/json; charset=utf-8; boundary=unreviewed",
	} {
		t.Run(name, func(t *testing.T) {
			response := liveResponse(200, "", `{"id":1}`)
			response.Stdout = bytes.Replace(response.Stdout, []byte("Content-Type: application/json; charset=utf-8"), []byte("Content-Type: "+contentType), 1)
			runner := &scriptedRunner{responses: []CommandResult{response}}
			var sleeps []time.Duration
			_, transportErr := testClient(runner, &sleeps).Read(context.Background(), ReadRequest{Endpoint: "repos/example/repo"})
			if transportErr == nil || transportErr.Code != "MALFORMED_RESPONSE" || transportErr.Retriable || len(runner.apiCalls) != 1 {
				t.Fatalf("error=%+v calls=%d", transportErr, len(runner.apiCalls))
			}
		})
	}
}

func TestReadNeverConvertsMalformedErrorEnvelopeToExactRemoteState(t *testing.T) {
	for name, testCase := range map[string]struct {
		status      int
		contentType string
		body        string
		calls       int
	}{
		"404 wrong media": {status: 404, contentType: "text/html", body: "not found", calls: 1},
		"404 empty":       {status: 404, contentType: "application/json", body: "", calls: 5},
		"404 malformed":   {status: 404, contentType: "application/json", body: `{"message":`, calls: 5},
		"404 duplicate":   {status: 404, contentType: "application/json", body: `{"message":"a","message":"b"}`, calls: 5},
		"404 array":       {status: 404, contentType: "application/json", body: `[]`, calls: 1},
		"404 no message":  {status: 404, contentType: "application/json", body: `{}`, calls: 1},
		"429 malformed":   {status: 429, contentType: "application/json", body: `{"message":`, calls: 5},
		"503 malformed":   {status: 503, contentType: "application/json", body: `{"message":`, calls: 5},
	} {
		t.Run(name, func(t *testing.T) {
			response := failureResponse(testCase.status, "", testCase.body)
			response.Stdout = bytes.Replace(response.Stdout, []byte("Content-Type: application/json; charset=utf-8"), []byte("Content-Type: "+testCase.contentType), 1)
			responses := make([]CommandResult, testCase.calls)
			for index := range responses {
				responses[index] = response
			}
			runner := &scriptedRunner{responses: responses}
			var sleeps []time.Duration
			_, transportErr := testClient(runner, &sleeps).Read(context.Background(), ReadRequest{Endpoint: "repos/example/repo"})
			if transportErr == nil || transportErr.Code != "MALFORMED_RESPONSE" || transportErr.Retriable || transportErr.Code == "REMOTE_NOT_FOUND" || len(runner.apiCalls) != testCase.calls {
				t.Fatalf("error=%+v calls=%d", transportErr, len(runner.apiCalls))
			}
		})
	}
}

func TestReadRequiresCompleteUniquePaginationAndDoesNotRepeatFields(t *testing.T) {
	next := `<https://api.github.com/repos/example/repo/actions/runs?event=push&per_page=1&page=2>; rel="next"`
	runner := &scriptedRunner{responses: []CommandResult{
		liveResponse(200, "Link: "+next+"\r\n", `{"total_count":2,"workflow_runs":[{"id":1}]}`),
		liveResponse(200, "", `{"total_count":2,"workflow_runs":[{"id":2}]}`),
	}}
	var sleeps []time.Duration
	data, transportErr := testClient(runner, &sleeps).Read(context.Background(), ReadRequest{
		Endpoint: "repos/example/repo/actions/runs?per_page=1", Fields: []string{"event=push"}, Paginate: true, Slurp: true,
	})
	if transportErr != nil || !strings.HasPrefix(string(data), "[") {
		t.Fatalf("data=%q error=%v", data, transportErr)
	}
	if strings.Contains(strings.Join(runner.apiCalls[1], " "), "--raw-field event=push") {
		t.Fatalf("fields repeated on Link-derived page: %v", runner.apiCalls[1])
	}

	for name, testCase := range map[string]struct {
		second  string
		message string
	}{
		"duplicate": {second: `{"total_count":2,"workflow_runs":[{"id":1}]}`, message: "duplicate pagination page"},
		"truncated": {second: `{"total_count":3,"workflow_runs":[{"id":2}]}`, message: "total_count changed"},
	} {
		t.Run(name, func(t *testing.T) {
			runner := &scriptedRunner{responses: []CommandResult{
				liveResponse(200, "Link: "+next+"\r\n", `{"total_count":2,"workflow_runs":[{"id":1}]}`),
				liveResponse(200, "", testCase.second),
			}}
			var sleeps []time.Duration
			_, err := testClient(runner, &sleeps).Read(context.Background(), ReadRequest{
				Endpoint: "repos/example/repo/actions/runs?per_page=1", Fields: []string{"event=push"}, Paginate: true, Slurp: true,
			})
			if err == nil || err.Code != "PAGINATION_INVALID" || !strings.Contains(err.Message, testCase.message) || len(runner.apiCalls) != 2 {
				t.Fatalf("error=%+v calls=%d", err, len(runner.apiCalls))
			}
		})
	}
}

func TestPaginationIgnoresWellFormedInformationalLinkRelations(t *testing.T) {
	next := `<https://api.github.com/repos/example/repo/actions/runs?event=push&per_page=1&page=2>; rel="next"`
	deprecation := `<https://docs.github.com/en/rest/about-the-rest-api/api-versions>; rel="deprecation"; type="text/html"; title="API versions, deprecation schedule"`
	relativeDeprecation := `<../about-the-rest-api/api-versions>; rel="deprecation"; reviewed`
	multilingualDeprecation := `<../docs>; rel="deprecation"; hreflang=en; hreflang=ru`
	anchoredAmbiguous := `<https://api.github.com/repos/example/repo/actions/runs?event=push&per_page=1&page=99>; rel="next"; rel="deprecation"; anchor="https://api.github.com/repos/example/other"`
	for name, firstLink := range map[string]string{
		"informational before next":               deprecation + ", " + next,
		"informational after next":                next + ", " + deprecation,
		"relative informational":                  relativeDeprecation + ", " + next,
		"repeated informational target attribute": multilingualDeprecation + ", " + next,
		"anchored ambiguous relation":             anchoredAmbiguous + ", " + next,
	} {
		t.Run(name, func(t *testing.T) {
			runner := &scriptedRunner{responses: []CommandResult{
				liveResponse(200, "Link: "+firstLink+"\r\nDeprecation: Tue, 10 Mar 2026 00:00:00 GMT\r\nSunset: Fri, 10 Mar 2028 00:00:00 GMT\r\n", `{"total_count":2,"workflow_runs":[{"id":1}]}`),
				liveResponse(200, "Link: "+deprecation+"\r\n", `{"total_count":2,"workflow_runs":[{"id":2}]}`),
			}}
			var sleeps []time.Duration
			data, transportErr := testClient(runner, &sleeps).Read(context.Background(), ReadRequest{
				Endpoint: "repos/example/repo/actions/runs?per_page=1", Fields: []string{"event=push"}, Paginate: true, Slurp: true,
			})
			if transportErr != nil || len(runner.apiCalls) != 2 || !strings.Contains(string(data), `"id":2`) {
				t.Fatalf("data=%s error=%+v calls=%d", data, transportErr, len(runner.apiCalls))
			}
		})
	}
}

func TestPaginationNeverFollowsAlternateAnchorContext(t *testing.T) {
	anchoredNext := `<https://api.github.com/repos/example/repo/actions/runs?per_page=1&page=2>; rel="next"; anchor="https://api.github.com/repos/example/other"`
	for name, testCase := range map[string]struct {
		totalCount int
		wantError  bool
	}{
		"complete current context":  {totalCount: 1},
		"truncated current context": {totalCount: 2, wantError: true},
	} {
		t.Run(name, func(t *testing.T) {
			runner := &scriptedRunner{responses: []CommandResult{
				liveResponse(200, "Link: "+anchoredNext+"\r\n", fmt.Sprintf(`{"total_count":%d,"workflow_runs":[{"id":1}]}`, testCase.totalCount)),
			}}
			var sleeps []time.Duration
			data, transportErr := testClient(runner, &sleeps).Read(context.Background(), ReadRequest{
				Endpoint: "repos/example/repo/actions/runs?per_page=1", Paginate: true, Slurp: true,
			})
			if testCase.wantError {
				if transportErr == nil || transportErr.Code != "PAGINATION_INVALID" || !strings.Contains(transportErr.Message, "truncated") {
					t.Fatalf("data=%s error=%+v", data, transportErr)
				}
			} else if transportErr != nil || !strings.Contains(string(data), `"id":1`) {
				t.Fatalf("data=%s error=%+v", data, transportErr)
			}
			if len(runner.apiCalls) != 1 {
				t.Fatalf("anchored next was followed: calls=%d", len(runner.apiCalls))
			}
		})
	}
}

func TestPaginationRejectsMalformedOrAmbiguousLinkRelations(t *testing.T) {
	base := "https://api.github.com/repos/example/repo/actions/runs?page=2"
	for name, link := range map[string]string{
		"duplicate rel parameter":   `<` + base + `>; rel="next"; rel="deprecation"`,
		"next mixed with other rel": `<` + base + `>; rel="next deprecation"`,
		"duplicate next token":      `<` + base + `>; rel="next next"`,
		"noncanonical next":         `<` + base + `>; rel="Next"`,
		"unterminated quoted value": `<https://docs.github.com/en/rest>; rel="deprecation"; title="broken, <` + base + `>; rel="next"`,
		"empty link value":          `<https://docs.github.com/en/rest>; rel="deprecation", , <` + base + `>; rel="next"`,
	} {
		t.Run(name, func(t *testing.T) {
			runner := &scriptedRunner{responses: []CommandResult{
				liveResponse(200, "Link: "+link+"\r\n", `{"total_count":2,"workflow_runs":[{"id":1}]}`),
			}}
			var sleeps []time.Duration
			_, transportErr := testClient(runner, &sleeps).Read(context.Background(), ReadRequest{
				Endpoint: "repos/example/repo/actions/runs", Paginate: true, Slurp: true,
			})
			if transportErr == nil || transportErr.Code != "PAGINATION_INVALID" || len(runner.apiCalls) != 1 {
				t.Fatalf("error=%+v calls=%d", transportErr, len(runner.apiCalls))
			}
		})
	}
}

func TestPaginationPreservesCanonicalInitialQueryScope(t *testing.T) {
	base := "https://api.github.com/repos/example/repo/actions/runs?event=push&per_page=1"
	for name, query := range map[string]string{
		"dropped filter":          "per_page=1&page=2",
		"changed filter":          "event=pull_request&per_page=1&page=2",
		"added unreviewed filter": "event=push&per_page=1&status=completed&page=2",
		"duplicate filter":        "event=push&event=push&per_page=1&page=2",
		"case variant filter":     "Event=push&event=push&per_page=1&page=2",
		"changed per_page":        "event=push&per_page=100&page=2",
		"skipped page":            "event=push&per_page=1&page=3",
		"non-canonical page":      "event=push&per_page=1&page=02",
	} {
		t.Run(name, func(t *testing.T) {
			link := `<` + strings.Split(base, "?")[0] + `?` + query + `>; rel="next"`
			runner := &scriptedRunner{responses: []CommandResult{
				liveResponse(200, "Link: "+link+"\r\n", `{"total_count":2,"workflow_runs":[{"id":1}]}`),
			}}
			var sleeps []time.Duration
			_, transportErr := testClient(runner, &sleeps).Read(context.Background(), ReadRequest{
				Endpoint: "repos/example/repo/actions/runs?per_page=1", Fields: []string{"event=push"}, Paginate: true, Slurp: true,
			})
			if transportErr == nil || transportErr.Code != "PAGINATION_INVALID" || len(runner.apiCalls) != 1 {
				t.Fatalf("error=%+v calls=%d", transportErr, len(runner.apiCalls))
			}
		})
	}
}

func TestPaginationAcceptsExactPageProgression(t *testing.T) {
	runner := &scriptedRunner{responses: []CommandResult{
		liveResponse(200, "Link: <https://api.github.com/repos/example/repo/actions/runs?event=push&per_page=1&page=2>; rel=\"next\"\r\n", `{"total_count":3,"workflow_runs":[{"id":1}]}`),
		liveResponse(200, "Link: <https://api.github.com/repos/example/repo/actions/runs?event=push&per_page=1&page=3>; rel=\"next\"\r\n", `{"total_count":3,"workflow_runs":[{"id":2}]}`),
		liveResponse(200, "", `{"total_count":3,"workflow_runs":[{"id":3}]}`),
	}}
	var sleeps []time.Duration
	data, transportErr := testClient(runner, &sleeps).Read(context.Background(), ReadRequest{
		Endpoint: "repos/example/repo/actions/runs?per_page=1", Fields: []string{"event=push"}, Paginate: true, Slurp: true,
	})
	if transportErr != nil || len(runner.apiCalls) != 3 || !strings.Contains(string(data), `"id":3`) {
		t.Fatalf("data=%s error=%+v calls=%d", data, transportErr, len(runner.apiCalls))
	}
}

func TestReadEnforcesPerRequestAndEndToEndDeadlines(t *testing.T) {
	t.Run("per request", func(t *testing.T) {
		runner := &deadlineRunner{}
		client := &Client{
			Runner:           runner,
			Now:              time.Now,
			Sleep:            func(context.Context, time.Duration) error { return nil },
			requestTimeout:   40 * time.Millisecond,
			operationTimeout: 500 * time.Millisecond,
		}
		_, transportErr := client.Read(context.Background(), ReadRequest{Endpoint: "repos/example/repo"})
		if transportErr == nil || transportErr.Code != "TRANSPORT_FAILED" || transportErr.Attempts != maxAttempts {
			t.Fatalf("error=%+v", transportErr)
		}
		if len(runner.apiDeadlines) != maxAttempts {
			t.Fatalf("API deadline count=%d want=%d", len(runner.apiDeadlines), maxAttempts)
		}
		for _, remaining := range runner.apiDeadlines {
			if remaining <= 0 || remaining > 60*time.Millisecond {
				t.Fatalf("request deadline remaining=%s", remaining)
			}
		}
	})

	t.Run("operation", func(t *testing.T) {
		response := failureResponse(429, "Retry-After: 60\r\n", `{"message":"rate limited"}`)
		runner := &scriptedRunner{responses: []CommandResult{response}}
		client := &Client{
			Runner:           runner,
			Now:              time.Now,
			Sleep:            sleepContext,
			requestTimeout:   500 * time.Millisecond,
			operationTimeout: 50 * time.Millisecond,
		}
		started := time.Now()
		_, transportErr := client.Read(context.Background(), ReadRequest{Endpoint: "repos/example/repo"})
		if transportErr == nil || transportErr.Code != "TRANSPORT_FAILED" || !strings.Contains(transportErr.Message, "cancelled") || time.Since(started) > time.Second {
			t.Fatalf("error=%+v elapsed=%s", transportErr, time.Since(started))
		}
	})
}

func TestReadNeverTrustsCompleteLookingResponseAfterRequestDeadline(t *testing.T) {
	runner := &completeResponseAfterDeadlineRunner{}
	client := &Client{
		Runner: runner, Now: time.Now,
		Sleep:          func(context.Context, time.Duration) error { return nil },
		requestTimeout: 40 * time.Millisecond, operationTimeout: 500 * time.Millisecond,
	}
	data, transportErr := client.Read(context.Background(), ReadRequest{Endpoint: "repos/example/repo"})
	if len(data) != 0 || transportErr == nil || transportErr.Code != "TRANSPORT_FAILED" ||
		transportErr.Attempts != maxAttempts || transportErr.Retriable || runner.apiCalls != maxAttempts {
		t.Fatalf("data=%q error=%+v calls=%d", data, transportErr, runner.apiCalls)
	}
}

func TestCapabilityProbesAreInsideRequestOperationAndCallerDeadlines(t *testing.T) {
	t.Run("per request", func(t *testing.T) {
		runner := &capabilityBudgetRunner{blockVersion: true}
		client := &Client{Runner: runner, Now: time.Now, Sleep: sleepContext, requestTimeout: 40 * time.Millisecond, operationTimeout: 500 * time.Millisecond}
		started := time.Now()
		_, transportErr := client.Preflight(context.Background())
		if transportErr == nil || transportErr.Code != "TRANSPORT_FAILED" || time.Since(started) > time.Second ||
			len(runner.deadlines) != 1 || runner.deadlines[0] <= 0 || runner.deadlines[0] > 60*time.Millisecond {
			t.Fatalf("error=%+v elapsed=%s deadlines=%v", transportErr, time.Since(started), runner.deadlines)
		}
	})

	t.Run("end to end includes both probes", func(t *testing.T) {
		runner := &capabilityBudgetRunner{versionDelay: 50 * time.Millisecond, blockHelp: true}
		client := &Client{Runner: runner, Now: time.Now, Sleep: sleepContext, requestTimeout: 500 * time.Millisecond, operationTimeout: 100 * time.Millisecond}
		started := time.Now()
		_, transportErr := client.Preflight(context.Background())
		if transportErr == nil || transportErr.Code != "TRANSPORT_FAILED" || time.Since(started) > time.Second ||
			len(runner.deadlines) != 2 || runner.deadlines[1] <= 0 || runner.deadlines[1] >= 80*time.Millisecond {
			t.Fatalf("error=%+v elapsed=%s deadlines=%v", transportErr, time.Since(started), runner.deadlines)
		}
	})

	t.Run("caller earlier deadline", func(t *testing.T) {
		runner := &capabilityBudgetRunner{blockVersion: true}
		client := &Client{Runner: runner, Now: time.Now, Sleep: sleepContext, requestTimeout: 500 * time.Millisecond, operationTimeout: time.Second}
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
		defer cancel()
		started := time.Now()
		_, transportErr := client.Preflight(ctx)
		if transportErr == nil || transportErr.Code != "TRANSPORT_FAILED" || time.Since(started) > time.Second ||
			len(runner.deadlines) != 1 || runner.deadlines[0] > 60*time.Millisecond {
			t.Fatalf("error=%+v elapsed=%s deadlines=%v", transportErr, time.Since(started), runner.deadlines)
		}
	})
}

func TestCapabilitySingleflightWaiterHonorsDeadlineAndSuccessCaches(t *testing.T) {
	runner := &singleflightCapabilityRunner{versionStarted: make(chan struct{}), releaseVersion: make(chan struct{})}
	client := &Client{Runner: runner, Now: time.Now, Sleep: sleepContext, requestTimeout: 2 * time.Second, operationTimeout: 5 * time.Second}
	ownerResult := make(chan *TransportError, 1)
	go func() {
		_, transportErr := client.Preflight(context.Background())
		ownerResult <- transportErr
	}()
	<-runner.versionStarted

	waiterCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, transportErr := client.Preflight(waiterCtx); transportErr == nil || transportErr.Code != "TRANSPORT_FAILED" || time.Since(started) > time.Second {
		t.Fatalf("waiter error=%+v elapsed=%s", transportErr, time.Since(started))
	}
	close(runner.releaseVersion)
	if transportErr := <-ownerResult; transportErr != nil {
		t.Fatalf("owner error=%+v", transportErr)
	}
	if _, transportErr := client.Preflight(context.Background()); transportErr != nil {
		t.Fatalf("cached preflight error=%+v", transportErr)
	}
	versionCalls, helpCalls := runner.calls()
	if versionCalls != 1 || helpCalls != 1 {
		t.Fatalf("capability calls version=%d help=%d, want one successful singleflight", versionCalls, helpCalls)
	}
}

func TestCapabilitySingleflightOwnerCancellationIsNotCached(t *testing.T) {
	runner := &transientCapabilityRunner{}
	client := &Client{Runner: runner, Now: time.Now, Sleep: sleepContext, requestTimeout: 500 * time.Millisecond, operationTimeout: 2 * time.Second}
	ownerCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, transportErr := client.Preflight(ownerCtx); transportErr == nil || transportErr.Code != "TRANSPORT_FAILED" || time.Since(started) > time.Second {
		t.Fatalf("cancelled owner error=%+v elapsed=%s", transportErr, time.Since(started))
	}
	if _, transportErr := client.Preflight(context.Background()); transportErr != nil {
		t.Fatalf("later capability owner did not recover: %+v", transportErr)
	}
	if _, transportErr := client.Preflight(context.Background()); transportErr != nil {
		t.Fatalf("successful capability result was not cached: %+v", transportErr)
	}
	versionCalls, helpCalls := runner.calls()
	if versionCalls != 2 || helpCalls != 1 {
		t.Fatalf("capability calls version=%d help=%d, want transient owner plus one cached success", versionCalls, helpCalls)
	}
}

func TestReadRejectsAggregateResponseBytesBeforeSlurp(t *testing.T) {
	next := `<https://api.github.com/repos/example/repo/actions/runs?per_page=1&page=2>; rel="next"`
	firstBody := `{"total_count":2,"workflow_runs":[{"id":1,"padding":"aaaaaaaa"}]}`
	secondBody := `{"total_count":2,"workflow_runs":[{"id":2,"padding":"bbbbbbbb"}]}`
	runner := &scriptedRunner{responses: []CommandResult{
		liveResponse(200, "Link: "+next+"\r\n", firstBody),
		liveResponse(200, "", secondBody),
	}}
	var sleeps []time.Duration
	client := testClient(runner, &sleeps)
	client.maxAggregateResponseBytes = 100
	_, transportErr := client.Read(context.Background(), ReadRequest{
		Endpoint: "repos/example/repo/actions/runs?per_page=1", Paginate: true, Slurp: true,
	})
	if transportErr == nil || transportErr.Code != "TRANSPORT_FAILED" || !strings.Contains(transportErr.Message, "aggregate response") || len(runner.apiCalls) != 2 {
		t.Fatalf("error=%+v calls=%d", transportErr, len(runner.apiCalls))
	}

	exactRunner := &scriptedRunner{responses: []CommandResult{
		liveResponse(200, "Link: "+next+"\r\n", firstBody),
		liveResponse(200, "", secondBody),
	}}
	exactClient := testClient(exactRunner, &sleeps)
	exactClient.maxAggregateResponseBytes = int64(len(firstBody) + len(secondBody))
	if _, exactErr := exactClient.Read(context.Background(), ReadRequest{
		Endpoint: "repos/example/repo/actions/runs?per_page=1", Paginate: true, Slurp: true,
	}); exactErr != nil {
		t.Fatalf("exact aggregate boundary error=%+v", exactErr)
	}
}

func TestReadChargesEveryAttemptAgainstAggregateResponseBudget(t *testing.T) {
	for name, responses := range map[string][]CommandResult{
		"malformed JSON retries": {
			liveResponse(200, "", `{"id":`),
			liveResponse(200, "", `{"id":`),
		},
		"rate limit retry": {
			failureResponse(429, "Retry-After: 0\r\n", `{"message":"rate limited"}`),
			liveResponse(200, "", `{"ok":true}`),
		},
		"incomplete HTTP retries": {
			{Stdout: []byte(`{"partial":`), Err: errors.New("network failure")},
			{Stdout: []byte(`{"partial":`), Err: errors.New("network failure")},
		},
	} {
		t.Run(name, func(t *testing.T) {
			firstBytes := int64(len(responses[0].Stdout))
			if parsed, err := parseHTTPResponse(responses[0].Stdout); err == nil {
				firstBytes = int64(len(parsed.Body))
			}
			secondBytes := int64(len(responses[1].Stdout))
			if parsed, err := parseHTTPResponse(responses[1].Stdout); err == nil {
				secondBytes = int64(len(parsed.Body))
			}
			runner := &scriptedRunner{responses: responses}
			var sleeps []time.Duration
			client := testClient(runner, &sleeps)
			client.maxAggregateResponseBytes = firstBytes + secondBytes - 1
			_, transportErr := client.Read(context.Background(), ReadRequest{Endpoint: "repos/example/repo"})
			if transportErr == nil || transportErr.Code != "TRANSPORT_FAILED" ||
				!strings.Contains(transportErr.Message, "aggregate response bytes") ||
				transportErr.Attempts != 2 || len(runner.apiCalls) != 2 {
				t.Fatalf("error=%+v calls=%d", transportErr, len(runner.apiCalls))
			}
		})
	}
}

func TestReadEnforcesNonPaginatedAggregateLimitAndResetsPerPublicOperation(t *testing.T) {
	body := `{"id":1}`
	for name, limit := range map[string]int64{"exact": int64(len(body)), "plus one": int64(len(body) - 1)} {
		t.Run(name, func(t *testing.T) {
			runner := &scriptedRunner{responses: []CommandResult{liveResponse(200, "", body)}}
			var sleeps []time.Duration
			client := testClient(runner, &sleeps)
			client.maxAggregateResponseBytes = limit
			_, transportErr := client.Read(context.Background(), ReadRequest{Endpoint: "repos/example/repo"})
			if name == "exact" && transportErr != nil {
				t.Fatalf("exact boundary error=%+v", transportErr)
			}
			if name == "plus one" && (transportErr == nil || transportErr.Code != "TRANSPORT_FAILED") {
				t.Fatalf("over-boundary error=%+v", transportErr)
			}
		})
	}

	runner := &scriptedRunner{responses: []CommandResult{liveResponse(200, "", body), liveResponse(200, "", body)}}
	var sleeps []time.Duration
	client := testClient(runner, &sleeps)
	client.maxAggregateResponseBytes = int64(len(body))
	for index := 0; index < 2; index++ {
		if _, transportErr := client.Read(context.Background(), ReadRequest{Endpoint: "repos/example/repo"}); transportErr != nil {
			t.Fatalf("independent public read %d reused aggregate budget: %+v", index+1, transportErr)
		}
	}
}

type deadlineRunner struct {
	apiDeadlines []time.Duration
}

type completeResponseAfterDeadlineRunner struct{ apiCalls int }

func (runner *completeResponseAfterDeadlineRunner) Run(ctx context.Context, args []string, _ []string) CommandResult {
	if len(args) == 1 && args[0] == "--version" {
		return CommandResult{Stdout: []byte("gh version 2.96.0 (2026-07-02)\n")}
	}
	if len(args) == 2 && args[0] == "api" && args[1] == "--help" {
		return CommandResult{Stdout: []byte("--include --hostname --method --header --raw-field --input\n")}
	}
	runner.apiCalls++
	<-ctx.Done()
	response := liveResponse(200, "", `{"id":1}`)
	response.Err = ctx.Err()
	return response
}

type capabilityBudgetRunner struct {
	versionDelay time.Duration
	blockVersion bool
	blockHelp    bool
	deadlines    []time.Duration
}

type singleflightCapabilityRunner struct {
	mu             sync.Mutex
	versionStarted chan struct{}
	releaseVersion chan struct{}
	versionCalls   int
	helpCalls      int
}

type transientCapabilityRunner struct {
	mu           sync.Mutex
	versionCalls int
	helpCalls    int
}

func (runner *transientCapabilityRunner) Run(ctx context.Context, args []string, _ []string) CommandResult {
	if len(args) == 1 && args[0] == "--version" {
		runner.mu.Lock()
		runner.versionCalls++
		call := runner.versionCalls
		runner.mu.Unlock()
		if call == 1 {
			<-ctx.Done()
			return CommandResult{Err: ctx.Err()}
		}
		return CommandResult{Stdout: []byte("gh version 2.96.0 (2026-07-02)\n")}
	}
	if len(args) == 2 && args[0] == "api" && args[1] == "--help" {
		runner.mu.Lock()
		runner.helpCalls++
		runner.mu.Unlock()
		return CommandResult{Stdout: []byte("--include --hostname --method --header --raw-field --input\n")}
	}
	return CommandResult{Err: errors.New("unexpected API call")}
}

func (runner *transientCapabilityRunner) calls() (int, int) {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	return runner.versionCalls, runner.helpCalls
}

func (runner *singleflightCapabilityRunner) Run(ctx context.Context, args []string, _ []string) CommandResult {
	if len(args) == 1 && args[0] == "--version" {
		runner.mu.Lock()
		runner.versionCalls++
		if runner.versionCalls == 1 {
			close(runner.versionStarted)
		}
		runner.mu.Unlock()
		select {
		case <-runner.releaseVersion:
			return CommandResult{Stdout: []byte("gh version 2.96.0 (2026-07-02)\n")}
		case <-ctx.Done():
			return CommandResult{Err: ctx.Err()}
		}
	}
	if len(args) == 2 && args[0] == "api" && args[1] == "--help" {
		runner.mu.Lock()
		runner.helpCalls++
		runner.mu.Unlock()
		return CommandResult{Stdout: []byte("--include --hostname --method --header --raw-field --input\n")}
	}
	return CommandResult{Err: errors.New("unexpected API call")}
}

func (runner *singleflightCapabilityRunner) calls() (int, int) {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	return runner.versionCalls, runner.helpCalls
}

func (runner *capabilityBudgetRunner) Run(ctx context.Context, args []string, _ []string) CommandResult {
	deadline, ok := ctx.Deadline()
	if !ok {
		return CommandResult{Err: errors.New("capability probe had no deadline")}
	}
	runner.deadlines = append(runner.deadlines, time.Until(deadline))
	if len(args) == 1 && args[0] == "--version" {
		if runner.blockVersion {
			<-ctx.Done()
			return CommandResult{Err: ctx.Err()}
		}
		if runner.versionDelay > 0 {
			timer := time.NewTimer(runner.versionDelay)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return CommandResult{Err: ctx.Err()}
			case <-timer.C:
			}
		}
		return CommandResult{Stdout: []byte("gh version 2.96.0 (2026-07-02)\n")}
	}
	if len(args) == 2 && args[0] == "api" && args[1] == "--help" {
		if runner.blockHelp {
			<-ctx.Done()
			return CommandResult{Err: ctx.Err()}
		}
		return CommandResult{Stdout: []byte("--include --hostname --method --header --raw-field --input\n")}
	}
	return CommandResult{Err: errors.New("unexpected API call")}
}

func (runner *deadlineRunner) Run(ctx context.Context, args []string, _ []string) CommandResult {
	if len(args) == 1 && args[0] == "--version" {
		return CommandResult{Stdout: []byte("gh version 2.96.0 (2026-07-02)\n")}
	}
	if len(args) == 2 && args[0] == "api" && args[1] == "--help" {
		return CommandResult{Stdout: []byte("--include --hostname --method --header --raw-field --input\n")}
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return CommandResult{Err: errors.New("API request had no deadline")}
	}
	runner.apiDeadlines = append(runner.apiDeadlines, time.Until(deadline))
	<-ctx.Done()
	return CommandResult{Err: ctx.Err()}
}

func TestPaginationRejectsUnsafeOrAmbiguousNextLinkBeforeSecondRequest(t *testing.T) {
	base := "https://api.github.com/repos/example/repo/actions/runs"
	for name, link := range map[string]string{
		"custom host":        `<https://attacker.invalid/repos/example/repo/actions/runs?page=2>; rel="next"`,
		"changed repository": `<https://api.github.com/repos/example/other/actions/runs?page=2>; rel="next"`,
		"changed path":       `<https://api.github.com/repos/example/repo/actions/jobs?page=2>; rel="next"`,
		"encoded control":    `<` + base + `?page=2&cursor=%0a>; rel="next"`,
		"encoded backslash":  `<` + base + `?page=2&cursor=%5cunsafe>; rel="next"`,
		"duplicate query":    `<` + base + `?page=2&page=3>; rel="next"`,
		"case query":         `<` + base + `?page=2&Page=3>; rel="next"`,
		"duplicate next":     `<` + base + `?page=2>; rel="next", <` + base + `?page=3>; rel="next"`,
	} {
		t.Run(name, func(t *testing.T) {
			runner := &scriptedRunner{responses: []CommandResult{
				liveResponse(200, "Link: "+link+"\r\n", `{"total_count":2,"workflow_runs":[{"id":1}]}`),
			}}
			var sleeps []time.Duration
			_, transportErr := testClient(runner, &sleeps).Read(context.Background(), ReadRequest{
				Endpoint: "repos/example/repo/actions/runs", Paginate: true, Slurp: true,
			})
			if transportErr == nil || transportErr.Code != "PAGINATION_INVALID" || len(runner.apiCalls) != 1 {
				t.Fatalf("error=%+v calls=%d", transportErr, len(runner.apiCalls))
			}
		})
	}
}

func TestWriteNoClobberAndPreflightPolicy(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "snapshot.json")
	if err := WriteNoClobber(path, []byte("one\n")); err != nil {
		t.Fatal(err)
	}
	if err := WriteNoClobber(path, []byte("two\n")); err == nil {
		t.Fatal("existing output was clobbered")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "one\n" {
		t.Fatalf("existing output changed: %q", data)
	}
	matches, _ := filepath.Glob(filepath.Join(root, ".*.releasetransport.*"))
	if len(matches) != 0 {
		t.Fatalf("temporary files remain: %v", matches)
	}

	symlink := filepath.Join(root, "output-link")
	if err := os.Symlink(path, symlink); err != nil {
		t.Fatal(err)
	}
	if err := ValidateOutputPath(symlink); err == nil {
		t.Fatal("output symlink accepted")
	}
	realDirectory := filepath.Join(root, "real")
	if err := os.Mkdir(realDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	directoryLink := filepath.Join(root, "directory-link")
	if err := os.Symlink(realDirectory, directoryLink); err != nil {
		t.Fatal(err)
	}
	if err := ValidateOutputPath(filepath.Join(directoryLink, "new.json")); err == nil {
		t.Fatal("symlink output directory accepted")
	}

	interrupted := filepath.Join(root, "interrupted.json")
	if err := writeNoClobber(interrupted, []byte("candidate\n"), func(string) error { return errors.New("interrupted") }); err == nil {
		t.Fatal("interrupted publication succeeded")
	}
	if _, err := os.Lstat(interrupted); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("interrupted output exists: %v", err)
	}

	raced := filepath.Join(root, "raced.json")
	if err := writeNoClobber(raced, []byte("candidate\n"), func(string) error { return os.WriteFile(raced, []byte("racer\n"), 0o600) }); err == nil {
		t.Fatal("publication race succeeded")
	}
	racedData, _ := os.ReadFile(raced)
	if string(racedData) != "racer\n" {
		t.Fatalf("race output was clobbered: %q", racedData)
	}
	matches, _ = filepath.Glob(filepath.Join(root, ".*.releasetransport.*"))
	if len(matches) != 0 {
		t.Fatalf("temporary files remain after adversarial cases: %v", matches)
	}
}

func TestPreflightAdvertisesOptionalOneShotMutationCapability(t *testing.T) {
	runner := &scriptedRunner{}
	var sleeps []time.Duration
	document, transportErr := testClient(runner, &sleeps).Preflight(context.Background())
	if transportErr != nil || !containsAll(strings.Join(document.Capabilities, " "),
		"one_shot_git_data_mutation", "bounded_request_time", "bounded_operation_time", "bounded_aggregate_response_bytes") {
		t.Fatalf("preflight=%+v error=%v", document, transportErr)
	}
	if document.SchemaID != "env-vault.github-transport-capabilities.v2" || document.SchemaVersion != 2 || document.TransportVersion != "1.2.0" ||
		document.MaxRequestSeconds != 60 || document.MaxOperationSeconds != 300 || document.MaxAggregateResponseBytes != 256<<20 {
		t.Fatalf("preflight numerical bounds=%+v", document)
	}
}

func TestObserveProjectsOnlySafeServerMetadata(t *testing.T) {
	runner := &scriptedRunner{responses: []CommandResult{liveResponse(200, "X-OAuth-Scopes: repo\r\n", `{"id":7}`)}}
	var sleeps []time.Duration
	document, err := testClient(runner, &sleeps).Observe(context.Background(), "repos/example/repo/issues/comments/7")
	if err != nil || document.ServerDate != "Fri, 17 Jul 2026 12:00:00 GMT" || document.HTTPStatus != 200 || len(document.BodySHA256) != 64 {
		t.Fatalf("document=%+v error=%v", document, err)
	}
	encoded, _ := MarshalDocument(document)
	if strings.Contains(strings.ToLower(string(encoded)), "oauth") || strings.Contains(string(encoded), "repo") && !strings.Contains(string(encoded), "example/repo") {
		t.Fatalf("unsafe response headers leaked: %s", encoded)
	}
}

func TestObserveRequiresExactGMTHTTPDate(t *testing.T) {
	response := liveResponse(200, "", `{"id":7}`)
	response.Stdout = bytes.Replace(response.Stdout, []byte("Fri, 17 Jul 2026 12:00:00 GMT"), []byte("Fri, 17 Jul 2026 12:00:00 UTC"), 1)
	runner := &scriptedRunner{responses: []CommandResult{response}}
	var sleeps []time.Duration
	_, transportErr := testClient(runner, &sleeps).Observe(context.Background(), "repos/example/repo/issues/comments/7")
	if transportErr == nil || transportErr.Code != "MALFORMED_RESPONSE" {
		t.Fatalf("error=%+v", transportErr)
	}
}

func TestObserveRejectsCalendarDateWithWrongWeekday(t *testing.T) {
	response := liveResponse(200, "", `{"id":7}`)
	response.Stdout = bytes.Replace(response.Stdout, []byte("Fri, 17 Jul 2026 12:00:00 GMT"), []byte("Thu, 17 Jul 2026 12:00:00 GMT"), 1)
	runner := &scriptedRunner{responses: []CommandResult{response}}
	var sleeps []time.Duration
	_, transportErr := testClient(runner, &sleeps).Observe(context.Background(), "repos/example/repo/issues/comments/7")
	if transportErr == nil || transportErr.Code != "MALFORMED_RESPONSE" {
		t.Fatalf("error=%+v", transportErr)
	}
}

func TestReadRejectsDuplicateAmbiguousAndUnsafeFieldsBeforeNetwork(t *testing.T) {
	for name, request := range map[string]ReadRequest{
		"duplicate":        {Endpoint: "repos/example/repo", Fields: []string{"event=push", "event=pull_request"}},
		"case variant":     {Endpoint: "repos/example/repo", Fields: []string{"event=push", "Event=pull_request"}},
		"endpoint overlap": {Endpoint: "repos/example/repo?per_page=1", Fields: []string{"per_page=100"}},
		"control":          {Endpoint: "repos/example/repo", Fields: []string{"event=push\nunsafe"}},
		"backslash":        {Endpoint: "repos/example/repo", Fields: []string{`event=push\unsafe`}},
	} {
		t.Run(name, func(t *testing.T) {
			runner := &scriptedRunner{}
			var sleeps []time.Duration
			_, transportErr := testClient(runner, &sleeps).Read(context.Background(), request)
			if transportErr == nil || transportErr.Code != "INPUT_INVALID" || len(runner.allCalls) != 0 {
				t.Fatalf("error=%+v calls=%v", transportErr, runner.allCalls)
			}
		})
	}
}
