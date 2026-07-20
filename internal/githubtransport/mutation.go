package githubtransport

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
)

const (
	maxMutationResponseBytes = 1 << 20
	maxMutationInputBytes    = 32 << 20
	maxMutationObjects       = 64
	maxMutationTreeEntries   = 77 // 2*6 durable metadata paths + genesis + at most 64 objects.
)

type MutationRequest struct {
	Method         string
	Endpoint       string
	InputPath      string
	ExpectedStatus int
}

type mutationBlobRequest struct {
	Encoding string `json:"encoding"`
	Content  string `json:"content"`
}

type mutationTreeRequest struct {
	BaseTree *string             `json:"base_tree,omitempty"`
	Tree     []mutationTreeEntry `json:"tree"`
}

type mutationTreeEntry struct {
	Path string `json:"path"`
	Mode string `json:"mode"`
	Type string `json:"type"`
	SHA  string `json:"sha"`
}

type mutationCommitRequest struct {
	Message string    `json:"message"`
	Tree    string    `json:"tree"`
	Parents *[]string `json:"parents"`
}

type mutationRefCreateRequest struct {
	Ref string `json:"ref"`
	SHA string `json:"sha"`
}

type mutationRefUpdateRequest struct {
	SHA   string `json:"sha"`
	Force *bool  `json:"force"`
}

var (
	gitCreateEndpoint      = regexp.MustCompile(`^repos/([^/]+/[^/]+)/git/(blobs|trees|commits|refs)$`)
	gitUpdateEndpoint      = regexp.MustCompile(`^repos/([^/]+/[^/]+)/git/refs/heads/release-evidence$`)
	artifactDeleteEndpoint = regexp.MustCompile(`^repos/([^/]+/[^/]+)/actions/artifacts/([1-9][0-9]*)$`)
	releaseVersionShape    = regexp.MustCompile(`^v(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)$`)
	commitMessageShape     = regexp.MustCompile(`^chore\(evidence\): (create ledger at|publish) (v(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)) from ([0-9a-f]{40})$`)
	objectPathShape        = regexp.MustCompile(`^evidence/objects/sha256/[0-9a-f]{64}\.gz$`)
	versionRootPath        = regexp.MustCompile(`^evidence/releases/(v(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*))/([^/]+)$`)
	attemptPathShape       = regexp.MustCompile(`^evidence/releases/(v(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*))/publisher-runs/run-([1-9][0-9]*)/attempt-([1-9][0-9]*)/([^/]+)$`)
)

var v2EvidenceFilenames = map[string]bool{
	"release-evidence-bundle.json": true,
	"index.md":                     true,
	"metrics-comparison.json":      true,
	"metrics-comparison.md":        true,
	"storage-metrics.json":         true,
	"parity.json":                  true,
}

// MutateOnce executes one reviewed Git-data mutation attempt. It never retries.
// Indeterminate transport/server outcomes are returned as typed "ambiguous"
// observations so callers may perform one fresh read-only reconciliation.
func (c *Client) MutateOnce(ctx context.Context, request MutationRequest) (MutationDocument, *TransportError) {
	ctx, cancel := c.operationContext(ctx)
	defer cancel()
	input, err := validateMutationRequest(request)
	if err != nil {
		return MutationDocument{}, &TransportError{Code: "INPUT_INVALID", Message: err.Error()}
	}
	if capabilityErr := c.checkCapabilities(ctx); capabilityErr != nil {
		return MutationDocument{}, capabilityErr
	}
	c.capabilityMu.Lock()
	mutationInput := c.mutationInput
	c.capabilityMu.Unlock()
	bodyless := input == nil
	if !bodyless && !mutationInput {
		return MutationDocument{}, &TransportError{Code: "CLI_CAPABILITY_DRIFT", Message: "gh api lacks bounded standard-input support required for mutation"}
	}
	runner, ok := c.Runner.(InputCommandRunner)
	if !bodyless && !ok {
		return MutationDocument{}, &TransportError{Code: "CLI_CAPABILITY_DRIFT", Message: "mutation runner lacks bounded standard-input support"}
	}
	args := []string{"api", "--include", "--hostname", Host, "--method", request.Method,
		"--header", "Accept: application/vnd.github+json", "--header", "X-GitHub-Api-Version: " + APIVersion,
		request.Endpoint}
	if !bodyless {
		args = append(args, "--input", "-")
	}
	requestCtx, requestCancel := c.requestContext(ctx)
	var result CommandResult
	if bodyless {
		result = c.Runner.Run(requestCtx, args, SanitizedEnvironment())
	} else {
		result = runner.RunInput(requestCtx, args, SanitizedEnvironment(), input)
	}
	if requestCtx.Err() != nil && result.Err == nil {
		result.Err = requestCtx.Err()
	}
	requestCancel()
	document := MutationDocument{
		SchemaID: MutationSchemaID, SchemaVersion: 1, Method: request.Method,
		Endpoint: request.Endpoint, Outcome: "ambiguous", ErrorCode: "TRANSPORT_FAILED",
	}
	if len(result.Stdout) > maxMutationResponseBytes {
		if status := observedStatus(result.Stdout); status >= 400 && status < 500 {
			document.HTTPStatus, document.Outcome, document.ErrorCode = status, "http_error", "MALFORMED_RESPONSE"
		}
		return document, nil
	}
	response, parseErr := parseHTTPResponse(result.Stdout)
	if parseErr != nil {
		if status := observedStatus(result.Stdout); status >= 400 && status < 500 {
			document.HTTPStatus, document.Outcome, document.ErrorCode = status, "http_error", "MALFORMED_RESPONSE"
		}
		return document, nil
	}
	document.HTTPStatus = response.Status
	if response.SelectedVersion != APIVersion {
		document.ErrorCode = "CLI_CAPABILITY_DRIFT"
		if response.Status >= 400 && response.Status < 500 {
			document.Outcome = "http_error"
		}
		return document, nil
	}
	if reviewedErr := validateMutationResponse(request, response); reviewedErr != nil {
		document.ErrorCode = reviewedErr.Code
		if response.Status >= 400 && response.Status < 500 {
			document.Outcome = "http_error"
		}
		return document, nil
	}
	if !bodyless {
		digest := sha256.Sum256(response.Body)
		document.BodySHA256 = hex.EncodeToString(digest[:])
		document.Body = append(document.Body[:0], response.Body...)
	}
	if result.Err == nil && response.Status == request.ExpectedStatus {
		if !validMutationSuccessResponse(request, response.Body) {
			document.Body, document.BodySHA256, document.ErrorCode = nil, "", "MALFORMED_RESPONSE"
			return document, nil
		}
		document.OK, document.Outcome, document.ErrorCode = true, "success", ""
		return document, nil
	}
	if response.Status >= 500 || (response.Status >= 200 && response.Status < 300) || response.Status == request.ExpectedStatus {
		return document, nil
	}
	classified := classifyResult(result, response, nil, 1, c.Now())
	document.Outcome = "http_error"
	if classified != nil {
		document.ErrorCode = classified.Code
	} else {
		document.ErrorCode = "REMOTE_STATE_UNKNOWN"
	}
	return document, nil
}

func validMutationSuccessResponse(request MutationRequest, body []byte) bool {
	if artifactDeleteEndpoint.MatchString(request.Endpoint) {
		return request.Method == "DELETE" && request.ExpectedStatus == 204 && len(body) == 0
	}
	object, err := exactObject(body)
	if err != nil {
		return false
	}
	if strings.HasSuffix(request.Endpoint, "/git/refs") || strings.Contains(request.Endpoint, "/git/refs/heads/") {
		ref, refErr := exactString(object, "ref")
		rawObject, present := object["object"]
		if refErr != nil || ref != "refs/heads/release-evidence" || !present {
			return false
		}
		nested, nestedErr := exactObject(rawObject)
		typeValue, typeErr := exactString(nested, "type")
		sha, shaErr := exactString(nested, "sha")
		return nestedErr == nil && typeErr == nil && typeValue == "commit" && shaErr == nil && shaPattern.MatchString(sha)
	}
	sha, err := exactString(object, "sha")
	return err == nil && shaPattern.MatchString(sha)
}

func validateMutationResponse(request MutationRequest, response httpResponse) *TransportError {
	if artifactDeleteEndpoint.MatchString(request.Endpoint) && response.Status >= 200 && response.Status < 300 {
		if response.Status == 204 && len(response.Body) == 0 {
			return nil
		}
		return &TransportError{Code: "MALFORMED_RESPONSE", Message: "GitHub artifact deletion did not return an exactly empty 204 response", HTTPStatus: response.Status, Attempts: 1}
	}
	return validateReviewedResponse(response, ReadRequest{}, 1)
}

func validateMutationRequest(request MutationRequest) ([]byte, error) {
	operation := ""
	repository := ""
	if match := gitCreateEndpoint.FindStringSubmatch(request.Endpoint); match != nil && request.Method == "POST" && request.ExpectedStatus == 201 {
		repository, operation = match[1], match[2]
	} else if match := gitUpdateEndpoint.FindStringSubmatch(request.Endpoint); match != nil && request.Method == "PATCH" && request.ExpectedStatus == 200 {
		repository, operation = match[1], "ref-update"
	} else if match := artifactDeleteEndpoint.FindStringSubmatch(request.Endpoint); match != nil && request.Method == "DELETE" && request.ExpectedStatus == 204 {
		repository, operation = match[1], "artifact-delete"
		artifactID, artifactErr := strconv.ParseInt(match[2], 10, 64)
		if artifactErr != nil || artifactID < 1 || request.InputPath != "" {
			return nil, errors.New("artifact deletion requires one canonical positive artifact ID and no request body")
		}
	} else {
		return nil, errors.New("mutation method, endpoint, or expected status is outside the closed allowlist")
	}
	if !validRepository(repository) {
		return nil, errors.New("mutation repository is invalid")
	}
	if operation == "artifact-delete" {
		return nil, nil
	}
	data, err := readStableMutationInput(request.InputPath)
	if err != nil {
		return nil, err
	}
	switch operation {
	case "blobs":
		var value mutationBlobRequest
		if err := decodeStrict(data, &value); err != nil || value.Encoding != "base64" {
			return nil, errors.New("blob mutation payload is invalid")
		}
		decoded, err := base64.StdEncoding.Strict().DecodeString(value.Content)
		if err != nil || len(decoded) == 0 || len(decoded) > maxEvidenceBlobBytes || base64.StdEncoding.EncodeToString(decoded) != value.Content {
			return nil, errors.New("blob mutation content is not canonical bounded base64")
		}
	case "trees":
		object, objectErr := exactObject(data)
		_, baseTreePresent := object["base_tree"]
		var value mutationTreeRequest
		if objectErr != nil || decodeStrict(data, &value) != nil || !validTreeMutation(value, baseTreePresent) {
			return nil, errors.New("tree mutation payload is invalid")
		}
	case "commits":
		var value mutationCommitRequest
		if err := decodeStrict(data, &value); err != nil || !validCommitMutation(value) {
			return nil, errors.New("commit mutation payload is invalid")
		}
	case "refs":
		var value mutationRefCreateRequest
		if err := decodeStrict(data, &value); err != nil || value.Ref != "refs/heads/release-evidence" || !shaPattern.MatchString(value.SHA) {
			return nil, errors.New("reference creation payload is invalid")
		}
	case "ref-update":
		var value mutationRefUpdateRequest
		if err := decodeStrict(data, &value); err != nil || !shaPattern.MatchString(value.SHA) || value.Force == nil || *value.Force {
			return nil, errors.New("reference update payload is invalid")
		}
	default:
		return nil, errors.New("mutation operation is invalid")
	}
	return data, nil
}

func readStableMutationInput(filename string) ([]byte, error) {
	before, err := os.Lstat(filename)
	if err != nil || before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() || before.Size() <= 0 || before.Size() > maxMutationInputBytes {
		return nil, errors.New("mutation input is not a bounded regular file")
	}
	file, err := os.Open(filename)
	if err != nil {
		return nil, errors.New("mutation input cannot be opened")
	}
	defer file.Close()
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) || after.Size() != before.Size() {
		return nil, errors.New("mutation input changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxMutationInputBytes+1))
	if err != nil || int64(len(data)) != before.Size() {
		return nil, errors.New("mutation input cannot be read stably")
	}
	return data, nil
}

func validTreeMutation(value mutationTreeRequest, baseTreePresent bool) bool {
	if baseTreePresent != (value.BaseTree != nil) || len(value.Tree) == 0 || len(value.Tree) > maxMutationTreeEntries || (value.BaseTree != nil && !shaPattern.MatchString(*value.BaseTree)) {
		return false
	}
	seen := make(map[string]bool, len(value.Tree))
	genesisCount := 0
	objectCount := 0
	rootVersion := ""
	rootNames := make(map[string]bool, len(v2EvidenceFilenames))
	attemptTuple := ""
	attemptVersion := ""
	attemptNames := make(map[string]bool, len(v2EvidenceFilenames))
	for _, entry := range value.Tree {
		if entry.Mode != "100644" || entry.Type != "blob" || !shaPattern.MatchString(entry.SHA) || !validEvidenceMutationPath(entry.Path) {
			return false
		}
		folded := strings.ToLower(entry.Path)
		if seen[folded] {
			return false
		}
		seen[folded] = true
		if entry.Path == "evidence/genesis.v1.json" {
			genesisCount++
		}
		if objectPathShape.MatchString(entry.Path) {
			objectCount++
			continue
		}
		if match := versionRootPath.FindStringSubmatch(entry.Path); match != nil {
			if rootVersion != "" && rootVersion != match[1] {
				return false
			}
			rootVersion = match[1]
			rootNames[match[2]] = true
			continue
		}
		if match := attemptPathShape.FindStringSubmatch(entry.Path); match != nil {
			tuple := strings.Join(match[1:4], "/")
			if attemptTuple != "" && attemptTuple != tuple {
				return false
			}
			attemptTuple, attemptVersion = tuple, match[1]
			attemptNames[match[4]] = true
		}
	}
	if objectCount > maxMutationObjects || len(attemptNames) != len(v2EvidenceFilenames) ||
		(len(rootNames) != 0 && (len(rootNames) != len(v2EvidenceFilenames) || rootVersion != attemptVersion)) {
		return false
	}
	if value.BaseTree == nil {
		return genesisCount == 1 && len(rootNames) == len(v2EvidenceFilenames) && objectCount > 0
	}
	return genesisCount == 0
}

func validEvidenceMutationPath(value string) bool {
	if value == "evidence/genesis.v1.json" || objectPathShape.MatchString(value) {
		return true
	}
	if match := versionRootPath.FindStringSubmatch(value); match != nil {
		return releaseVersionShape.MatchString(match[1]) && v2EvidenceFilenames[match[2]]
	}
	if match := attemptPathShape.FindStringSubmatch(value); match != nil {
		run, runErr := strconv.ParseUint(match[2], 10, 64)
		attempt, attemptErr := strconv.ParseUint(match[3], 10, 32)
		return releaseVersionShape.MatchString(match[1]) && runErr == nil && run > 0 && run <= 9007199254740991 &&
			attemptErr == nil && attempt > 0 && attempt <= 2147483647 && v2EvidenceFilenames[match[4]]
	}
	return false
}

func validCommitMutation(value mutationCommitRequest) bool {
	if !shaPattern.MatchString(value.Tree) || value.Parents == nil || len(*value.Parents) > 1 {
		return false
	}
	match := commitMessageShape.FindStringSubmatch(value.Message)
	if match == nil || !releaseVersionShape.MatchString(match[2]) {
		return false
	}
	if len(*value.Parents) == 0 {
		return match[1] == "create ledger at"
	}
	return match[1] == "publish" && shaPattern.MatchString((*value.Parents)[0])
}

func observedStatus(data []byte) int {
	line := string(data)
	if index := strings.IndexAny(line, "\r\n"); index >= 0 {
		line = line[:index]
	}
	match := statusLine.FindStringSubmatch(line)
	if match == nil {
		return 0
	}
	status, err := strconv.Atoi(match[1])
	if err != nil {
		return 0
	}
	return status
}
