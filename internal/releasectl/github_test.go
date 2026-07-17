package releasectl

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

type runnerStub struct {
	stdout []byte
	stderr []byte
	err    error
	args   []string
	input  []byte
}

func (r *runnerStub) RunInput(_ context.Context, args []string, input []byte) ([]byte, []byte, error) {
	r.args = append([]string(nil), args...)
	r.input = append([]byte(nil), input...)
	return r.stdout, r.stderr, r.err
}

func (r *runnerStub) Run(_ context.Context, args []string) ([]byte, []byte, error) {
	r.args = append([]string(nil), args...)
	return r.stdout, r.stderr, r.err
}

func TestGHClientUsesOnlyGETAndStableQueryOrder(t *testing.T) {
	runner := &runnerStub{stdout: []byte(`{"value":"ok"}`)}
	var target struct {
		Value string `json:"value"`
	}
	err := (ghClient{runner: runner}).Get(context.Background(), "repos/example/env-vault", map[string]string{
		"zeta":  "last",
		"alpha": "first",
	}, &target)
	if err != nil {
		t.Fatal(err)
	}
	if target.Value != "ok" {
		t.Fatalf("target=%+v", target)
	}
	wantPrefix := []string{"api", "--method", "GET"}
	if !reflect.DeepEqual(runner.args[:len(wantPrefix)], wantPrefix) {
		t.Fatalf("args=%v", runner.args)
	}
	if hostname := indexOf(runner.args, "--hostname"); hostname < 0 || hostname+1 >= len(runner.args) || runner.args[hostname+1] != "github.com" {
		t.Fatalf("GitHub API hostname is not pinned: %v", runner.args)
	}
	if indexOf(runner.args, "X-GitHub-Api-Version: 2026-03-10") < 0 {
		t.Fatalf("GitHub API version does not provide exact workflow-dispatch run details: %v", runner.args)
	}
	joined := strings.Join(runner.args, " ")
	if strings.Contains(joined, " POST ") || strings.Contains(joined, " PATCH ") || strings.Contains(joined, " DELETE ") || strings.Contains(joined, " PUT ") {
		t.Fatalf("mutating method in args=%v", runner.args)
	}
	alpha := indexOf(runner.args, "alpha=first")
	zeta := indexOf(runner.args, "zeta=last")
	if alpha < 0 || zeta < 0 || alpha >= zeta {
		t.Fatalf("query fields are not sorted: %v", runner.args)
	}
}

func TestGHClientClassifiesHTTPAndTransportFailures(t *testing.T) {
	tests := []struct {
		name      string
		stderr    string
		wantCode  string
		wantHTTP  int
		retryable bool
	}{
		{name: "not found", stderr: "gh: Not Found (HTTP 404)", wantCode: "NOT_FOUND", wantHTTP: 404},
		{name: "auth", stderr: "gh: Bad credentials (HTTP 401)", wantCode: "AUTH_REQUIRED", wantHTTP: 401},
		{name: "forbidden", stderr: "gh: forbidden (HTTP 403)", wantCode: "AUTH_FORBIDDEN", wantHTTP: 403},
		{name: "primary rate limit", stderr: "gh: API rate limit exceeded (HTTP 403)", wantCode: "RATE_LIMITED", wantHTTP: 403, retryable: true},
		{name: "too many requests", stderr: "gh: too many requests (HTTP 429)", wantCode: "RATE_LIMITED", wantHTTP: 429, retryable: true},
		{name: "server", stderr: "gh: unavailable (HTTP 503)", wantCode: "API_UNAVAILABLE", wantHTTP: 503, retryable: true},
		{name: "network", stderr: "could not resolve host", wantCode: "API_UNAVAILABLE", retryable: true},
		{name: "gh network", stderr: "error connecting to api.github.com\ncheck your internet connection", wantCode: "API_UNAVAILABLE", retryable: true},
		{name: "auth without status", stderr: "please run: gh auth login", wantCode: "AUTH_REQUIRED"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := &runnerStub{stderr: []byte(test.stderr), err: errors.New("exit status 1")}
			var target any
			err := (ghClient{runner: runner}).Get(context.Background(), "repos/example/env-vault", nil, &target)
			var apiErr *apiError
			if !errors.As(err, &apiErr) {
				t.Fatalf("err=%T %v", err, err)
			}
			if apiErr.Code != test.wantCode || apiErr.HTTPStatus != test.wantHTTP || apiErr.Retryable != test.retryable {
				t.Fatalf("api error=%+v", apiErr)
			}
		})
	}
}

func TestGHClientRejectsMalformedOrMultipleJSONValues(t *testing.T) {
	for _, body := range []string{"{", "{} {}"} {
		runner := &runnerStub{stdout: []byte(body)}
		var target any
		err := (ghClient{runner: runner}).Get(context.Background(), "repos/example/env-vault", nil, &target)
		var apiErr *apiError
		if !errors.As(err, &apiErr) || apiErr.Code != "MALFORMED_RESPONSE" {
			t.Fatalf("body=%q err=%T %v", body, err, err)
		}
	}
}

func TestGHClientMutationCapabilityUsesExplicitMethodAndStdinJSON(t *testing.T) {
	runner := &runnerStub{}
	client := ghClient{runner: runner}
	body := struct {
		AllowRebase bool `json:"allow_rebase_merge"`
	}{AllowRebase: false}
	endpoint := "repos/example/env-vault"
	if err := client.Mutate(context.Background(), "PATCH", endpoint, body, nil); err != nil {
		t.Fatal(err)
	}
	if indexOf(runner.args, "PATCH") < 0 || indexOf(runner.args, "--input") < 0 || runner.args[len(runner.args)-1] != endpoint {
		t.Fatalf("args=%v", runner.args)
	}
	if indexOf(runner.args, "X-GitHub-Api-Version: 2026-03-10") < 0 {
		t.Fatalf("mutation transport uses an API version without exact dispatch identity: %v", runner.args)
	}
	if string(runner.input) != `{"allow_rebase_merge":false}` {
		t.Fatalf("input=%q", runner.input)
	}
	if err := client.Mutate(context.Background(), "DELETE", endpoint, nil, nil); err == nil {
		t.Fatal("DELETE must not be available to the operator mutation transport")
	}
}

func TestSafeGitHubEnvironmentDropsTraceFlagsButKeepsToken(t *testing.T) {
	got := safeGitHubEnvironment([]string{
		"GH_TOKEN=token-sentinel",
		"GH_DEBUG=api",
		"GH_HOST=attacker.invalid",
		"GIT_TRACE=1",
		"GIT_TRACE_CURL=1",
		"GIT_CURL_VERBOSE=1",
		"PATH=/bin",
	})
	want := []string{"GH_TOKEN=token-sentinel", "PATH=/bin"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("environment=%v, want %v", got, want)
	}
}

func indexOf(values []string, wanted string) int {
	for i, value := range values {
		if value == wanted {
			return i
		}
	}
	return -1
}
