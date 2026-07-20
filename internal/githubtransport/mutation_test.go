package githubtransport

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMutateOnceReturnsStrictOneShotOutcomesWithoutRetry(t *testing.T) {
	input := filepath.Join(t.TempDir(), "request.json")
	payload := []byte("{\"ref\":\"refs/heads/release-evidence\",\"sha\":\"1111111111111111111111111111111111111111\"}\n")
	if err := os.WriteFile(input, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	tests := map[string]struct {
		response CommandResult
		outcome  string
		code     string
		ok       bool
		status   int
	}{
		"success":                    {mutationResponse(201, `{"ref":"refs/heads/release-evidence","object":{"type":"commit","sha":"2222222222222222222222222222222222222222"}}`, nil), "success", "", true, 201},
		"forbidden is terminal":      {mutationResponse(403, `{"message":"forbidden"}`, errors.New("exit 1")), "http_error", "AUTH_FORBIDDEN", false, 403},
		"unauthorized is terminal":   {mutationResponse(401, `{"message":"unauthorized"}`, errors.New("exit 1")), "http_error", "AUTH_REQUIRED", false, 401},
		"not found is terminal":      {mutationResponse(404, `{"message":"not found"}`, errors.New("exit 1")), "http_error", "REMOTE_NOT_FOUND", false, 404},
		"rate limit is terminal":     {mutationResponse(429, `{"message":"rate limited"}`, errors.New("exit 1")), "http_error", "RATE_LIMITED", false, 429},
		"validation is observable":   {mutationResponse(422, `{"message":"Reference already exists"}`, errors.New("exit 1")), "http_error", "REMOTE_STATE_UNKNOWN", false, 422},
		"server response ambiguous":  {mutationResponse(500, `{"message":"server unavailable"}`, errors.New("exit 1")), "ambiguous", "TRANSPORT_FAILED", false, 500},
		"success plus CLI error":     {mutationResponse(201, `{"ref":"refs/heads/release-evidence","object":{"type":"commit","sha":"2222222222222222222222222222222222222222"}}`, errors.New("exit 1")), "ambiguous", "TRANSPORT_FAILED", false, 201},
		"missing response ambiguous": {CommandResult{Err: errors.New("timeout")}, "ambiguous", "TRANSPORT_FAILED", false, 0},
		"duplicate JSON ambiguous":   {mutationResponse(201, `{"sha":"a","sha":"b"}`, nil), "ambiguous", "MALFORMED_RESPONSE", false, 201},
		"case JSON ambiguous":        {mutationResponse(201, `{"sha":"a","SHA":"b"}`, nil), "ambiguous", "MALFORMED_RESPONSE", false, 201},
		"malformed 403 is terminal":  {mutationResponse(403, `{"message":"a","message":"b"}`, errors.New("exit 1")), "http_error", "MALFORMED_RESPONSE", false, 403},
		"malformed 429 is terminal":  {mutationResponse(429, `{"message":"a","message":"b"}`, errors.New("exit 1")), "http_error", "MALFORMED_RESPONSE", false, 429},
		"malformed 422 is terminal":  {mutationResponse(422, `{"message":"a","message":"b"}`, errors.New("exit 1")), "http_error", "MALFORMED_RESPONSE", false, 422},
		"wrong selected version":     {mutationResponseWithHeaders(201, "X-GitHub-Api-Version-Selected: 2025-01-01\r\n", `{"sha":"a"}`, nil), "ambiguous", "CLI_CAPABILITY_DRIFT", false, 201},
		"missing selected version":   {mutationResponseWithHeaders(201, "", `{"sha":"a"}`, nil), "ambiguous", "CLI_CAPABILITY_DRIFT", false, 201},
		"duplicate selected version": {mutationResponseWithHeaders(201, "X-GitHub-Api-Version-Selected: 2022-11-28\r\nX-GitHub-Api-Version-Selected: 2022-11-28\r\n", `{"sha":"a"}`, nil), "ambiguous", "TRANSPORT_FAILED", false, 0},
		"duplicate header ambiguous": {mutationResponseWithHeaders(201, "Content-Type: application/json\r\n", `{"sha":"a"}`, nil), "ambiguous", "TRANSPORT_FAILED", false, 0},
		"missing content type":       {rawMutationResponse(201, "Date: Fri, 17 Jul 2026 12:00:00 GMT\r\nX-GitHub-Api-Version-Selected: 2022-11-28\r\n", `{"sha":"a"}`, nil), "ambiguous", "MALFORMED_RESPONSE", false, 201},
		"wrong content type":         {rawMutationResponse(201, "Content-Type: text/plain\r\nDate: Fri, 17 Jul 2026 12:00:00 GMT\r\nX-GitHub-Api-Version-Selected: 2022-11-28\r\n", `{"sha":"a"}`, nil), "ambiguous", "MALFORMED_RESPONSE", false, 201},
		"unexpected 2xx ambiguous":   {mutationResponse(202, `{"sha":"a"}`, nil), "ambiguous", "TRANSPORT_FAILED", false, 202},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			runner := &scriptedRunner{responses: []CommandResult{test.response}}
			var sleeps []time.Duration
			document, transportErr := testClient(runner, &sleeps).MutateOnce(context.Background(), MutationRequest{
				Method: "POST", Endpoint: "repos/example/env-vault/git/refs", InputPath: input, ExpectedStatus: 201,
			})
			if transportErr != nil || document.Outcome != test.outcome || document.ErrorCode != test.code || document.OK != test.ok || document.HTTPStatus != test.status {
				t.Fatalf("document=%+v transportErr=%v", document, transportErr)
			}
			if len(runner.apiCalls) != 1 || len(sleeps) != 0 {
				t.Fatalf("mutation calls=%d sleeps=%v, want exactly one attempt", len(runner.apiCalls), sleeps)
			}
			call := strings.Join(runner.apiCalls[0], " ")
			for _, want := range []string{"api --include --hostname github.com --method POST", "X-GitHub-Api-Version: 2022-11-28", "repos/example/env-vault/git/refs", "--input -"} {
				if !strings.Contains(call, want) {
					t.Fatalf("mutation call %q lacks %q", call, want)
				}
			}
			if len(runner.inputs) != 1 || string(runner.inputs[0]) != string(payload) {
				t.Fatalf("mutation stdin snapshots=%q, want exact validated bytes", runner.inputs)
			}
			if test.outcome == "success" && (string(document.Body) != `{"ref":"refs/heads/release-evidence","object":{"type":"commit","sha":"2222222222222222222222222222222222222222"}}` || len(document.BodySHA256) != 64) {
				t.Fatalf("success response body was not exactly captured: %+v", document)
			}
		})
	}
}

func TestMutateOnceTimeoutRemainsOneShotAndAmbiguous(t *testing.T) {
	input := filepath.Join(t.TempDir(), "request.json")
	if err := os.WriteFile(input, []byte("{\"ref\":\"refs/heads/release-evidence\",\"sha\":\"1111111111111111111111111111111111111111\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &mutationDeadlineRunner{}
	client := &Client{Runner: runner, Now: time.Now, Sleep: sleepContext, requestTimeout: 50 * time.Millisecond, operationTimeout: 500 * time.Millisecond}
	started := time.Now()
	document, transportErr := client.MutateOnce(context.Background(), MutationRequest{
		Method: "POST", Endpoint: "repos/example/env-vault/git/refs", InputPath: input, ExpectedStatus: 201,
	})
	if transportErr != nil || document.OK || document.Outcome != "ambiguous" || document.ErrorCode != "TRANSPORT_FAILED" ||
		runner.mutationCalls != 1 || time.Since(started) > time.Second {
		t.Fatalf("document=%+v error=%+v calls=%d elapsed=%s", document, transportErr, runner.mutationCalls, time.Since(started))
	}
}

func TestArtifactDeleteIsBodylessOneShotAndStrictlyTyped(t *testing.T) {
	tests := map[string]struct {
		response CommandResult
		outcome  string
		code     string
		ok       bool
		status   int
	}{
		"empty 204":        {rawMutationResponse(204, "Date: Fri, 17 Jul 2026 12:00:00 GMT\r\nX-GitHub-Api-Version-Selected: 2022-11-28\r\n", "", nil), "success", "", true, 204},
		"204 runner error": {rawMutationResponse(204, "Date: Fri, 17 Jul 2026 12:00:00 GMT\r\nX-GitHub-Api-Version-Selected: 2022-11-28\r\n", "", errors.New("exit 1")), "ambiguous", "TRANSPORT_FAILED", false, 204},
		"204 JSON body":    {rawMutationResponse(204, "Content-Type: application/vnd.github+json\r\nDate: Fri, 17 Jul 2026 12:00:00 GMT\r\nX-GitHub-Api-Version-Selected: 2022-11-28\r\n", `{}`, nil), "ambiguous", "MALFORMED_RESPONSE", false, 204},
		"204 newline body": {rawMutationResponse(204, "Date: Fri, 17 Jul 2026 12:00:00 GMT\r\nX-GitHub-Api-Version-Selected: 2022-11-28\r\n", "\n", nil), "ambiguous", "MALFORMED_RESPONSE", false, 204},
		"404":              {mutationResponse(404, `{"message":"not found"}`, errors.New("exit 1")), "http_error", "REMOTE_NOT_FOUND", false, 404},
		"500":              {mutationResponse(500, `{"message":"server unavailable"}`, errors.New("exit 1")), "ambiguous", "TRANSPORT_FAILED", false, 500},
		"missing response": {CommandResult{Err: errors.New("timeout")}, "ambiguous", "TRANSPORT_FAILED", false, 0},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			runner := &scriptedRunner{responses: []CommandResult{test.response}}
			var sleeps []time.Duration
			document, transportErr := testClient(runner, &sleeps).MutateOnce(context.Background(), MutationRequest{
				Method: "DELETE", Endpoint: "repos/example/env-vault/actions/artifacts/42", ExpectedStatus: 204,
			})
			if transportErr != nil || document.Outcome != test.outcome || document.ErrorCode != test.code || document.OK != test.ok || document.HTTPStatus != test.status {
				t.Fatalf("document=%+v transportErr=%v", document, transportErr)
			}
			if len(runner.apiCalls) != 1 || len(runner.inputs) != 0 || len(sleeps) != 0 {
				t.Fatalf("calls=%d inputs=%d sleeps=%v", len(runner.apiCalls), len(runner.inputs), sleeps)
			}
			call := strings.Join(runner.apiCalls[0], " ")
			if !strings.Contains(call, "api --include --hostname github.com --method DELETE") || !strings.Contains(call, "repos/example/env-vault/actions/artifacts/42") || strings.Contains(call, "--input") {
				t.Fatalf("unexpected DELETE call %q", call)
			}
			if document.Body != nil || document.BodySHA256 != "" {
				t.Fatalf("bodyless deletion retained response bytes: %+v", document)
			}
		})
	}
}

func TestArtifactDeleteAllowlistRejectsAdjacentOrMalformedShapesBeforeTransport(t *testing.T) {
	requests := map[string]MutationRequest{
		"wrong method":        {Method: "POST", Endpoint: "repos/example/env-vault/actions/artifacts/42", ExpectedStatus: 204},
		"wrong status":        {Method: "DELETE", Endpoint: "repos/example/env-vault/actions/artifacts/42", ExpectedStatus: 200},
		"body path":           {Method: "DELETE", Endpoint: "repos/example/env-vault/actions/artifacts/42", ExpectedStatus: 204, InputPath: "empty.json"},
		"zero":                {Method: "DELETE", Endpoint: "repos/example/env-vault/actions/artifacts/0", ExpectedStatus: 204},
		"leading zero":        {Method: "DELETE", Endpoint: "repos/example/env-vault/actions/artifacts/042", ExpectedStatus: 204},
		"negative":            {Method: "DELETE", Endpoint: "repos/example/env-vault/actions/artifacts/-1", ExpectedStatus: 204},
		"overflow":            {Method: "DELETE", Endpoint: "repos/example/env-vault/actions/artifacts/9223372036854775808", ExpectedStatus: 204},
		"query":               {Method: "DELETE", Endpoint: "repos/example/env-vault/actions/artifacts/42?x=1", ExpectedStatus: 204},
		"trailing slash":      {Method: "DELETE", Endpoint: "repos/example/env-vault/actions/artifacts/42/", ExpectedStatus: 204},
		"workflow run":        {Method: "DELETE", Endpoint: "repos/example/env-vault/actions/runs/42", ExpectedStatus: 204},
		"release asset":       {Method: "DELETE", Endpoint: "repos/example/env-vault/releases/assets/42", ExpectedStatus: 204},
		"repository dot path": {Method: "DELETE", Endpoint: "repos/example/../actions/artifacts/42", ExpectedStatus: 204},
	}
	for name, request := range requests {
		t.Run(name, func(t *testing.T) {
			runner := &scriptedRunner{}
			var sleeps []time.Duration
			_, transportErr := testClient(runner, &sleeps).MutateOnce(context.Background(), request)
			if transportErr == nil || transportErr.Code != "INPUT_INVALID" || len(runner.allCalls) != 0 {
				t.Fatalf("error=%+v calls=%v", transportErr, runner.allCalls)
			}
		})
	}
}

func TestMutateOnceRejectsUnreviewedShapeAndInputBeforeTransport(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "request.json")
	if err := os.WriteFile(input, []byte("{\"encoding\":\"base64\",\"content\":\"eA==\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(root, "request-link.json")
	if err := os.Symlink(input, symlink); err != nil {
		t.Fatal(err)
	}
	for name, request := range map[string]MutationRequest{
		"unknown endpoint": {Method: "POST", Endpoint: "repos/example/env-vault/issues", InputPath: input, ExpectedStatus: 201},
		"wrong status":     {Method: "POST", Endpoint: "repos/example/env-vault/git/blobs", InputPath: input, ExpectedStatus: 200},
		"lowercase method": {Method: "post", Endpoint: "repos/example/env-vault/git/blobs", InputPath: input, ExpectedStatus: 201},
		"symlink input":    {Method: "POST", Endpoint: "repos/example/env-vault/git/blobs", InputPath: symlink, ExpectedStatus: 201},
		"dot owner":        {Method: "POST", Endpoint: "repos/./env-vault/git/blobs", InputPath: input, ExpectedStatus: 201},
		"dot repository":   {Method: "POST", Endpoint: "repos/example/../git/blobs", InputPath: input, ExpectedStatus: 201},
	} {
		t.Run(name, func(t *testing.T) {
			runner := &scriptedRunner{}
			var sleeps []time.Duration
			_, transportErr := testClient(runner, &sleeps).MutateOnce(context.Background(), request)
			if transportErr == nil || transportErr.Code != "INPUT_INVALID" || len(runner.allCalls) != 0 {
				t.Fatalf("error=%+v calls=%v", transportErr, runner.allCalls)
			}
		})
	}
}

func TestMutationTransportUsesSanitizedEnvironment(t *testing.T) {
	t.Setenv("GH_DEBUG", "api")
	t.Setenv("GH_HOST", "attacker.invalid")
	t.Setenv("GH_ENTERPRISE_TOKEN", "sentinel-enterprise")
	t.Setenv("GIT_TRACE", "1")
	environment := SanitizedEnvironment()
	joined := strings.Join(environment, "\n")
	for _, forbidden := range []string{"GH_DEBUG=", "attacker.invalid", "sentinel-enterprise", "GIT_TRACE="} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("sanitized environment retained %q", forbidden)
		}
	}
	for _, required := range []string{"GH_HOST=github.com", "GH_PROMPT_DISABLED=1", "GH_NO_UPDATE_NOTIFIER=1", "NO_COLOR=1"} {
		if !strings.Contains(joined, required) {
			t.Fatalf("sanitized environment lacks %q", required)
		}
	}
}

func TestMutationPayloadSchemasAreEndpointSpecificAndClosed(t *testing.T) {
	root := t.TempDir()
	sha := strings.Repeat("1", 40)
	digest := strings.Repeat("a", 64)
	validMetadataPath := "evidence/releases/v1.2.3/release-evidence-bundle.json"
	metadataEntries := func(prefix string) string {
		entries := make([]string, 0, len(v2EvidenceFilenames))
		for _, name := range []string{"release-evidence-bundle.json", "index.md", "metrics-comparison.json", "metrics-comparison.md", "storage-metrics.json", "parity.json"} {
			entries = append(entries, fmt.Sprintf(`{"path":"%s/%s","mode":"100644","type":"blob","sha":"%s"}`, prefix, name, sha))
		}
		return strings.Join(entries, ",")
	}
	rootEntries := metadataEntries("evidence/releases/v1.2.3")
	attemptEntries := metadataEntries("evidence/releases/v1.2.3/publisher-runs/run-42/attempt-1")
	objectEntry := fmt.Sprintf(`{"path":"evidence/objects/sha256/%s.gz","mode":"100644","type":"blob","sha":"%s"}`, digest, sha)
	genesisEntry := fmt.Sprintf(`{"path":"evidence/genesis.v1.json","mode":"100644","type":"blob","sha":"%s"}`, sha)
	tests := map[string]struct {
		method   string
		endpoint string
		status   int
		payload  string
		valid    bool
	}{
		"blob":                     {"POST", "repos/example/env-vault/git/blobs", 201, `{"encoding":"base64","content":"eA=="}`, true},
		"blob unknown field":       {"POST", "repos/example/env-vault/git/blobs", 201, `{"encoding":"base64","content":"eA==","token":"x"}`, false},
		"blob noncanonical":        {"POST", "repos/example/env-vault/git/blobs", 201, `{"encoding":"base64","content":"eB=="}`, false},
		"parentless tree":          {"POST", "repos/example/env-vault/git/trees", 201, fmt.Sprintf(`{"tree":[%s,%s,%s,%s]}`, genesisEntry, rootEntries, attemptEntries, objectEntry), true},
		"parentless no genesis":    {"POST", "repos/example/env-vault/git/trees", 201, fmt.Sprintf(`{"tree":[{"path":"%s","mode":"100644","type":"blob","sha":"%s"}]}`, validMetadataPath, sha), false},
		"append tree":              {"POST", "repos/example/env-vault/git/trees", 201, fmt.Sprintf(`{"base_tree":"%s","tree":[%s,%s]}`, sha, attemptEntries, objectEntry), true},
		"append partial metadata":  {"POST", "repos/example/env-vault/git/trees", 201, fmt.Sprintf(`{"base_tree":"%s","tree":[{"path":"%s","mode":"100644","type":"blob","sha":"%s"},%s]}`, sha, validMetadataPath, sha, objectEntry), false},
		"append rewrites anchor":   {"POST", "repos/example/env-vault/git/trees", 201, fmt.Sprintf(`{"base_tree":"%s","tree":[{"path":"evidence/genesis.v1.json","mode":"100644","type":"blob","sha":"%s"}]}`, sha, sha), false},
		"tree arbitrary path":      {"POST", "repos/example/env-vault/git/trees", 201, fmt.Sprintf(`{"base_tree":"%s","tree":[{"path":"README.md","mode":"100644","type":"blob","sha":"%s"}]}`, sha, sha), false},
		"tree wrong mode":          {"POST", "repos/example/env-vault/git/trees", 201, fmt.Sprintf(`{"base_tree":"%s","tree":[{"path":"%s","mode":"100755","type":"blob","sha":"%s"}]}`, sha, validMetadataPath, sha), false},
		"genesis commit":           {"POST", "repos/example/env-vault/git/commits", 201, fmt.Sprintf(`{"message":"chore(evidence): create ledger at v1.2.3 from %s","tree":"%s","parents":[]}`, sha, sha), true},
		"append commit":            {"POST", "repos/example/env-vault/git/commits", 201, fmt.Sprintf(`{"message":"chore(evidence): publish v1.2.3 from %s","tree":"%s","parents":["%s"]}`, sha, sha, sha), true},
		"commit arbitrary message": {"POST", "repos/example/env-vault/git/commits", 201, fmt.Sprintf(`{"message":"arbitrary","tree":"%s","parents":[]}`, sha), false},
		"commit two parents":       {"POST", "repos/example/env-vault/git/commits", 201, fmt.Sprintf(`{"message":"chore(evidence): publish v1.2.3 from %s","tree":"%s","parents":["%s","%s"]}`, sha, sha, sha, sha), false},
		"ref create":               {"POST", "repos/example/env-vault/git/refs", 201, fmt.Sprintf(`{"ref":"refs/heads/release-evidence","sha":"%s"}`, sha), true},
		"ref arbitrary":            {"POST", "repos/example/env-vault/git/refs", 201, fmt.Sprintf(`{"ref":"refs/heads/main","sha":"%s"}`, sha), false},
		"ref update":               {"PATCH", "repos/example/env-vault/git/refs/heads/release-evidence", 200, fmt.Sprintf(`{"sha":"%s","force":false}`, sha), true},
		"ref forced":               {"PATCH", "repos/example/env-vault/git/refs/heads/release-evidence", 200, fmt.Sprintf(`{"sha":"%s","force":true}`, sha), false},
		"ref force omitted":        {"PATCH", "repos/example/env-vault/git/refs/heads/release-evidence", 200, fmt.Sprintf(`{"sha":"%s"}`, sha), false},
		"commit parents omitted":   {"POST", "repos/example/env-vault/git/commits", 201, fmt.Sprintf(`{"message":"chore(evidence): create ledger at v1.2.3 from %s","tree":"%s"}`, sha, sha), false},
		"empty base tree present":  {"POST", "repos/example/env-vault/git/trees", 201, fmt.Sprintf(`{"base_tree":"","tree":[{"path":"evidence/genesis.v1.json","mode":"100644","type":"blob","sha":"%s"}]}`, sha), false},
		"null base tree present":   {"POST", "repos/example/env-vault/git/trees", 201, fmt.Sprintf(`{"base_tree":null,"tree":[{"path":"evidence/genesis.v1.json","mode":"100644","type":"blob","sha":"%s"}]}`, sha), false},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			input := filepath.Join(root, strings.ReplaceAll(name, " ", "-")+".json")
			if err := os.WriteFile(input, []byte(test.payload+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := validateMutationRequest(MutationRequest{Method: test.method, Endpoint: test.endpoint, InputPath: input, ExpectedStatus: test.status})
			if (err == nil) != test.valid {
				t.Fatalf("valid=%t error=%v", test.valid, err)
			}
		})
	}
}

func TestBlobMutationAcceptsWorstCaseBundleCompressedSizeButNotBeyondTransportBound(t *testing.T) {
	root := t.TempDir()
	for name, size := range map[string]struct {
		size  int
		valid bool
	}{
		"near worst-case gzip":  {size: (16 << 20) + (64 << 10), valid: true},
		"above transport bound": {size: maxEvidenceBlobBytes + 1, valid: false},
	} {
		t.Run(name, func(t *testing.T) {
			raw := bytes.Repeat([]byte{0xa5}, size.size)
			payload := fmt.Sprintf(`{"encoding":"base64","content":"%s"}`, base64.StdEncoding.EncodeToString(raw))
			input := filepath.Join(root, strings.ReplaceAll(name, " ", "-")+".json")
			if err := os.WriteFile(input, []byte(payload), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := validateMutationRequest(MutationRequest{Method: "POST", Endpoint: "repos/example/env-vault/git/blobs", InputPath: input, ExpectedStatus: 201})
			if (err == nil) != size.valid {
				t.Fatalf("valid=%t error=%v", size.valid, err)
			}
		})
	}
}

func TestTreeMutationAcceptsAtMost64ContentAddressedObjects(t *testing.T) {
	sha := strings.Repeat("1", 40)
	base := strings.Repeat("2", 40)
	build := func(objectCount int) mutationTreeRequest {
		entries := make([]mutationTreeEntry, 0, len(v2EvidenceFilenames)+objectCount)
		for _, name := range []string{"release-evidence-bundle.json", "index.md", "metrics-comparison.json", "metrics-comparison.md", "storage-metrics.json", "parity.json"} {
			entries = append(entries, mutationTreeEntry{Path: "evidence/releases/v1.2.3/publisher-runs/run-42/attempt-1/" + name, Mode: "100644", Type: "blob", SHA: sha})
		}
		for index := 1; index <= objectCount; index++ {
			entries = append(entries, mutationTreeEntry{Path: fmt.Sprintf("evidence/objects/sha256/%064x.gz", index), Mode: "100644", Type: "blob", SHA: sha})
		}
		return mutationTreeRequest{BaseTree: &base, Tree: entries}
	}
	if !validTreeMutation(build(64), true) {
		t.Fatal("tree with the exact 64-object boundary was rejected")
	}
	if validTreeMutation(build(65), true) {
		t.Fatal("tree above the 64-object boundary was accepted")
	}
}

func TestMutationSuccessResponsesAreOperationSpecific(t *testing.T) {
	sha := strings.Repeat("1", 40)
	refBody := []byte(fmt.Sprintf(`{"ref":"refs/heads/release-evidence","object":{"type":"commit","sha":"%s"}}`, sha))
	for name, test := range map[string]struct {
		request MutationRequest
		body    []byte
		valid   bool
	}{
		"blob sha":           {MutationRequest{Endpoint: "repos/example/env-vault/git/blobs"}, []byte(fmt.Sprintf(`{"sha":"%s"}`, sha)), true},
		"tree sha":           {MutationRequest{Endpoint: "repos/example/env-vault/git/trees"}, []byte(fmt.Sprintf(`{"sha":"%s"}`, sha)), true},
		"commit sha":         {MutationRequest{Endpoint: "repos/example/env-vault/git/commits"}, []byte(fmt.Sprintf(`{"sha":"%s"}`, sha)), true},
		"ref create":         {MutationRequest{Endpoint: "repos/example/env-vault/git/refs"}, refBody, true},
		"ref update":         {MutationRequest{Endpoint: "repos/example/env-vault/git/refs/heads/release-evidence"}, refBody, true},
		"ref missing object": {MutationRequest{Endpoint: "repos/example/env-vault/git/refs"}, []byte(fmt.Sprintf(`{"sha":"%s"}`, sha)), false},
		"blob malformed sha": {MutationRequest{Endpoint: "repos/example/env-vault/git/blobs"}, []byte(`{"sha":"bad"}`), false},
		"artifact delete":    {MutationRequest{Method: "DELETE", Endpoint: "repos/example/env-vault/actions/artifacts/42", ExpectedStatus: 204}, []byte{}, true},
		"delete whitespace":  {MutationRequest{Method: "DELETE", Endpoint: "repos/example/env-vault/actions/artifacts/42", ExpectedStatus: 204}, []byte("\n"), false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := validMutationSuccessResponse(test.request, test.body); got != test.valid {
				t.Fatalf("valid=%t got=%t", test.valid, got)
			}
		})
	}
}

func TestMutateOnceRequiresInputCapabilityBeforeMutation(t *testing.T) {
	input := filepath.Join(t.TempDir(), "request.json")
	if err := os.WriteFile(input, []byte(`{"ref":"refs/heads/release-evidence","sha":"1111111111111111111111111111111111111111"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &missingInputCapabilityRunner{}
	client := &Client{Runner: runner, Now: time.Now, Sleep: func(context.Context, time.Duration) error { return nil }}
	_, transportErr := client.MutateOnce(context.Background(), MutationRequest{Method: "POST", Endpoint: "repos/example/env-vault/git/refs", InputPath: input, ExpectedStatus: 201})
	if transportErr == nil || transportErr.Code != "CLI_CAPABILITY_DRIFT" || runner.mutationCalls != 0 {
		t.Fatalf("error=%+v mutationCalls=%d", transportErr, runner.mutationCalls)
	}
	preflight, preflightErr := client.Preflight(context.Background())
	if preflightErr != nil || containsAll(strings.Join(preflight.Capabilities, " "), "one_shot_git_data_mutation") {
		t.Fatalf("preflight=%+v error=%v", preflight, preflightErr)
	}
	_, readErr := client.Read(context.Background(), ReadRequest{Endpoint: "repos/example/env-vault"})
	if readErr == nil || readErr.Code == "CLI_CAPABILITY_DRIFT" {
		t.Fatalf("read unexpectedly rejected legacy capability set: %+v", readErr)
	}
}

func TestMutateOnceUsesValidatedInputSnapshot(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "request.json")
	original := []byte(`{"ref":"refs/heads/release-evidence","sha":"1111111111111111111111111111111111111111"}`)
	if err := os.WriteFile(input, original, 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &snapshotMutationRunner{
		scriptedRunner: scriptedRunner{responses: []CommandResult{mutationResponse(201, `{"ref":"refs/heads/release-evidence","object":{"type":"commit","sha":"2222222222222222222222222222222222222222"}}`, nil)}},
		path:           input,
	}
	client := &Client{Runner: runner, Now: time.Now, Sleep: func(context.Context, time.Duration) error { return nil }}
	document, transportErr := client.MutateOnce(context.Background(), MutationRequest{
		Method: "POST", Endpoint: "repos/example/env-vault/git/refs", InputPath: input, ExpectedStatus: 201,
	})
	if transportErr != nil || !document.OK || string(runner.observedInput) != string(original) {
		t.Fatalf("document=%+v error=%v stdin=%q", document, transportErr, runner.observedInput)
	}
	changed, err := os.ReadFile(input)
	if err != nil || string(changed) != `{"ref":"refs/heads/main","sha":"0000000000000000000000000000000000000000"}` {
		t.Fatalf("race fixture did not mutate source path: %q err=%v", changed, err)
	}
}

type snapshotMutationRunner struct {
	scriptedRunner
	path          string
	observedInput []byte
}

type missingInputCapabilityRunner struct{ mutationCalls int }

type mutationDeadlineRunner struct{ mutationCalls int }

func (runner *mutationDeadlineRunner) Run(_ context.Context, args []string, _ []string) CommandResult {
	if len(args) == 1 && args[0] == "--version" {
		return CommandResult{Stdout: []byte("gh version 2.96.0 (2026-07-02)\n")}
	}
	if len(args) == 2 && args[0] == "api" && args[1] == "--help" {
		return CommandResult{Stdout: []byte("--include --hostname --method --header --raw-field --input\n")}
	}
	return CommandResult{Err: errors.New("unexpected non-input call")}
}

func (runner *mutationDeadlineRunner) RunInput(ctx context.Context, _ []string, _ []string, _ []byte) CommandResult {
	runner.mutationCalls++
	<-ctx.Done()
	return CommandResult{Err: ctx.Err()}
}

func (r *missingInputCapabilityRunner) Run(_ context.Context, args []string, _ []string) CommandResult {
	if len(args) == 1 && args[0] == "--version" {
		return CommandResult{Stdout: []byte("gh version 2.96.0 (2026-07-02)\n")}
	}
	if len(args) == 2 && args[0] == "api" && args[1] == "--help" {
		return CommandResult{Stdout: []byte("--include --hostname --method --header --raw-field\n")}
	}
	r.mutationCalls++
	return CommandResult{Err: errors.New("unexpected mutation")}
}

func (r *missingInputCapabilityRunner) RunInput(ctx context.Context, args []string, environment []string, input []byte) CommandResult {
	r.mutationCalls++
	return CommandResult{Err: errors.New("unexpected mutation")}
}

func (r *snapshotMutationRunner) RunInput(ctx context.Context, args []string, environment []string, input []byte) CommandResult {
	r.observedInput = append([]byte(nil), input...)
	_ = os.WriteFile(r.path, []byte(`{"ref":"refs/heads/main","sha":"0000000000000000000000000000000000000000"}`), 0o600)
	return r.scriptedRunner.Run(ctx, args, environment)
}

func mutationResponse(status int, body string, err error) CommandResult {
	return mutationResponseWithHeaders(status, "X-GitHub-Api-Version-Selected: 2022-11-28\r\n", body, err)
}

func mutationResponseWithHeaders(status int, selectedHeader, body string, err error) CommandResult {
	return rawMutationResponse(status, "Content-Type: application/vnd.github+json; charset=utf-8\r\nDate: Fri, 17 Jul 2026 12:00:00 GMT\r\n"+selectedHeader, body, err)
}

func rawMutationResponse(status int, headers, body string, err error) CommandResult {
	statusText := map[int]string{201: "Created", 202: "Accepted", 204: "No Content", 401: "Unauthorized", 403: "Forbidden", 404: "Not Found", 422: "Unprocessable Entity", 429: "Too Many Requests", 500: "Server Error"}[status]
	return CommandResult{Stdout: []byte(fmt.Sprintf("HTTP/2 %d %s\r\n%s\r\n%s", status, statusText, headers, body)), Err: err}
}
