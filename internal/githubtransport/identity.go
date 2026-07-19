package githubtransport

import (
	"bytes"
	"context"
	"crypto/sha1" // Git object identity is defined by SHA-1 for this repository.
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
)

var (
	repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	shaPattern        = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

func validRepository(repository string) bool {
	if !repositoryPattern.MatchString(repository) {
		return false
	}
	parts := strings.Split(repository, "/")
	return len(parts) == 2 && parts[0] != "." && parts[0] != ".." && parts[1] != "." && parts[1] != ".."
}

func validRelativePath(path string) bool {
	if path == "" || strings.HasPrefix(path, "/") || strings.Contains(path, "\\") {
		return false
	}
	for _, value := range path {
		if value < 0x20 || value == 0x7f {
			return false
		}
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

type ActionsIdentityOptions struct {
	Repository   string
	RunID        int64
	RunAttempt   int
	WorkflowPath string
	Event        string
	HeadSHA      string
	HeadRef      string
	Status       string
	Conclusion   string
	JobID        int64
	JobName      string
	JobURL       string
}

func (c *Client) ResolveActionsIdentity(ctx context.Context, options ActionsIdentityOptions) (ActionsIdentityDocument, *TransportError) {
	ctx, cancel := c.operationContext(ctx)
	defer cancel()
	normalized, err := normalizeActionsOptions(options)
	if err != nil {
		return ActionsIdentityDocument{}, &TransportError{Code: "INPUT_INVALID", Message: err.Error()}
	}
	options = normalized
	runEndpoint := fmt.Sprintf("repos/%s/actions/runs/%d/attempts/%d", options.Repository, options.RunID, options.RunAttempt)
	runData, transportErr := c.Read(ctx, ReadRequest{Endpoint: runEndpoint})
	if transportErr != nil {
		return ActionsIdentityDocument{}, transportErr
	}
	run, err := parseRunIdentity(runData, options)
	if err != nil {
		return ActionsIdentityDocument{}, &TransportError{Code: "IDENTITY_MISMATCH", Message: err.Error(), Attempts: 1, Cause: err}
	}
	document := ActionsIdentityDocument{SchemaID: ActionsIdentitySchemaID, SchemaVersion: 1, OK: true, Run: run}
	if options.JobID == 0 {
		return document, nil
	}
	jobsEndpoint := fmt.Sprintf("repos/%s/actions/runs/%d/attempts/%d/jobs?per_page=100", options.Repository, options.RunID, options.RunAttempt)
	jobsData, transportErr := c.Read(ctx, ReadRequest{Endpoint: jobsEndpoint, Paginate: true, Slurp: true})
	if transportErr != nil {
		return ActionsIdentityDocument{}, transportErr
	}
	job, err := parseJobIdentity(jobsData, options)
	if err != nil {
		return ActionsIdentityDocument{}, &TransportError{Code: "IDENTITY_MISMATCH", Message: err.Error(), Attempts: 1, Cause: err}
	}
	document.Job = &job
	return document, nil
}

func normalizeActionsOptions(options ActionsIdentityOptions) (ActionsIdentityOptions, error) {
	if !validRepository(options.Repository) || options.RunID <= 0 || options.RunAttempt <= 0 || !shaPattern.MatchString(options.HeadSHA) {
		return options, errors.New("repository, run ID/attempt, and head SHA are required")
	}
	if !regexp.MustCompile(`^\.github/workflows/[A-Za-z0-9][A-Za-z0-9_.-]*\.ya?ml$`).MatchString(options.WorkflowPath) || !validHeadRef(options.HeadRef) {
		return options, errors.New("workflow path and safe head ref are required")
	}
	allowedEvents := map[string]bool{"push": true, "pull_request": true, "workflow_run": true, "workflow_dispatch": true}
	if !allowedEvents[options.Event] {
		return options, errors.New("event is not allowed for release identity")
	}
	if options.Status == "" {
		options.Status = "completed"
	}
	if options.Conclusion == "" {
		options.Conclusion = "success"
	}
	allowedConclusions := map[string]bool{
		"success": true, "failure": true, "cancelled": true, "timed_out": true,
		"action_required": true, "neutral": true, "skipped": true, "startup_failure": true, "stale": true,
	}
	if options.Status != "completed" || !allowedConclusions[options.Conclusion] {
		return options, errors.New("release identity requires a completed run and a known conclusion")
	}
	if options.JobID < 0 || (options.JobID > 0 && (options.JobName == "" || options.JobURL == "")) || (options.JobID == 0 && (options.JobName != "" || options.JobURL != "")) {
		return options, errors.New("job ID, name, and canonical URL must be supplied together")
	}
	if options.JobID > 0 && !validBoundedText(options.JobName, 128) {
		return options, errors.New("job name is unsafe or too long")
	}
	if options.JobID > 0 && options.JobURL != fmt.Sprintf("https://github.com/%s/actions/runs/%d/job/%d", options.Repository, options.RunID, options.JobID) {
		return options, errors.New("job canonical URL does not match repository/run/job identity")
	}
	return options, nil
}

func validHeadRef(value string) bool {
	if len(value) == 0 || len(value) > 255 || !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`).MatchString(value) {
		return false
	}
	if strings.Contains(value, "..") || strings.Contains(value, "@{") || strings.Contains(value, "//") || strings.HasSuffix(value, "/") || strings.HasSuffix(value, ".") {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." || strings.HasSuffix(segment, ".lock") {
			return false
		}
	}
	return true
}

func validBoundedText(value string, maximum int) bool {
	if value == "" || len(value) > maximum {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

func parseRunIdentity(data []byte, options ActionsIdentityOptions) (RunIdentity, error) {
	object, err := exactObject(data)
	if err != nil {
		return RunIdentity{}, err
	}
	id, err := exactInt64(object, "id")
	if err != nil || id != options.RunID {
		return RunIdentity{}, errors.New("run id mismatch")
	}
	attempt, err := exactInt(object, "run_attempt")
	if err != nil || attempt != options.RunAttempt {
		return RunIdentity{}, errors.New("run attempt mismatch")
	}
	repository, err := nestedString(object, "repository", "full_name")
	if err != nil || repository != options.Repository {
		return RunIdentity{}, errors.New("run repository mismatch")
	}
	headRepository, err := nestedString(object, "head_repository", "full_name")
	if err != nil || headRepository != options.Repository {
		return RunIdentity{}, errors.New("run head repository mismatch")
	}
	checks := map[string]string{
		"path": options.WorkflowPath, "event": options.Event, "head_sha": options.HeadSHA,
		"head_branch": options.HeadRef, "status": defaultString(options.Status, "completed"),
		"conclusion": defaultString(options.Conclusion, "success"),
	}
	values := map[string]string{}
	for field, expected := range checks {
		value, err := exactString(object, field)
		if err != nil || value != expected {
			return RunIdentity{}, fmt.Errorf("run %s mismatch", field)
		}
		values[field] = value
	}
	canonicalURL, err := exactString(object, "html_url")
	if err != nil || canonicalURL != fmt.Sprintf("https://github.com/%s/actions/runs/%d", options.Repository, options.RunID) {
		return RunIdentity{}, errors.New("run canonical URL mismatch")
	}
	diagnosticName, err := optionalString(object, "name")
	if err != nil {
		return RunIdentity{}, errors.New("run diagnostic name is malformed")
	}
	return RunIdentity{
		Repository: repository, HeadRepository: headRepository, RunID: id, RunAttempt: attempt,
		WorkflowPath: values["path"], Event: values["event"], HeadSHA: values["head_sha"], HeadRef: values["head_branch"],
		Status: values["status"], Conclusion: values["conclusion"], CanonicalURL: canonicalURL, DiagnosticName: diagnosticName,
	}, nil
}

func parseJobIdentity(data []byte, options ActionsIdentityOptions) (JobIdentity, error) {
	var pages []map[string]json.RawMessage
	if err := decodeStrict(data, &pages); err != nil {
		return JobIdentity{}, fmt.Errorf("decode attempt-qualified jobs pages: %w", err)
	}
	matches := make([]map[string]json.RawMessage, 0, 1)
	seen := map[int64]bool{}
	for _, page := range pages {
		var jobs []map[string]json.RawMessage
		if raw, ok := page["jobs"]; !ok || json.Unmarshal(raw, &jobs) != nil {
			return JobIdentity{}, errors.New("jobs page is malformed")
		}
		for _, job := range jobs {
			id, err := exactInt64(job, "id")
			if err != nil || seen[id] {
				return JobIdentity{}, errors.New("job IDs are malformed or duplicated")
			}
			seen[id] = true
			if id == options.JobID {
				matches = append(matches, job)
			}
		}
	}
	if len(matches) != 1 {
		return JobIdentity{}, errors.New("exact job ID does not occur once in the requested run attempt")
	}
	job := matches[0]
	runID, err := exactInt64(job, "run_id")
	if err != nil || runID != options.RunID {
		return JobIdentity{}, errors.New("job run ID mismatch")
	}
	if rawAttempt, present := job["run_attempt"]; present {
		var attempt json.Number
		decoder := json.NewDecoder(bytes.NewReader(rawAttempt))
		decoder.UseNumber()
		if err := decoder.Decode(&attempt); err != nil || attempt.String() != strconv.Itoa(options.RunAttempt) {
			return JobIdentity{}, errors.New("optional job run attempt mismatch")
		}
	}
	checks := map[string]string{"head_sha": options.HeadSHA, "name": options.JobName, "status": "completed", "conclusion": "success", "html_url": options.JobURL}
	values := map[string]string{}
	for field, expected := range checks {
		value, err := exactString(job, field)
		if err != nil || value != expected {
			return JobIdentity{}, fmt.Errorf("job %s mismatch", field)
		}
		values[field] = value
	}
	diagnosticWorkflowName, err := optionalString(job, "workflow_name")
	if err != nil {
		return JobIdentity{}, errors.New("job diagnostic workflow name is malformed")
	}
	return JobIdentity{
		JobID: options.JobID, RunID: runID, RunAttempt: options.RunAttempt, HeadSHA: values["head_sha"], CheckName: values["name"],
		DiagnosticWorkflowName: diagnosticWorkflowName, Status: values["status"], Conclusion: values["conclusion"], CanonicalURL: values["html_url"],
	}, nil
}

func (c *Client) VerifyBlob(ctx context.Context, repository, sha, expectedFile string) (BlobIdentityDocument, *TransportError) {
	ctx, cancel := c.operationContext(ctx)
	defer cancel()
	if !validRepository(repository) || !shaPattern.MatchString(sha) || expectedFile == "" {
		return BlobIdentityDocument{}, &TransportError{Code: "INPUT_INVALID", Message: "repository, blob SHA, and expected file are required"}
	}
	lstat, err := os.Lstat(expectedFile)
	if err != nil || !lstat.Mode().IsRegular() || lstat.Mode()&os.ModeSymlink != 0 || lstat.Size() < 0 || lstat.Size() > maxEvidenceBlobBytes {
		return BlobIdentityDocument{}, &TransportError{Code: "INPUT_INVALID", Message: "expected blob file must be a bounded regular non-symlink file", Cause: err}
	}
	file, err := os.Open(expectedFile)
	if err != nil {
		return BlobIdentityDocument{}, &TransportError{Code: "INPUT_INVALID", Message: "cannot read expected blob file", Cause: err}
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(lstat, opened) || opened.Size() != lstat.Size() || opened.Size() > maxEvidenceBlobBytes {
		return BlobIdentityDocument{}, &TransportError{Code: "INPUT_INVALID", Message: "expected blob file changed during validation", Cause: err}
	}
	expected, err := io.ReadAll(io.LimitReader(file, maxEvidenceBlobBytes+1))
	if err != nil || len(expected) > maxEvidenceBlobBytes || int64(len(expected)) != opened.Size() {
		return BlobIdentityDocument{}, &TransportError{Code: "INPUT_INVALID", Message: "cannot read bounded expected blob bytes", Cause: err}
	}
	decoded, transportErr := c.ReadBlob(ctx, repository, sha)
	if transportErr != nil {
		return BlobIdentityDocument{}, transportErr
	}
	if !bytes.Equal(decoded, expected) {
		return BlobIdentityDocument{}, &TransportError{Code: "BLOB_MISMATCH", Message: "blob bytes differ from expected file", Attempts: 1}
	}
	digest := sha256.Sum256(decoded)
	expectedDigest := sha256.Sum256(expected)
	return BlobIdentityDocument{
		SchemaID: BlobIdentitySchemaID, SchemaVersion: 1, OK: true, Repository: repository, SHA: sha, Encoding: "base64",
		DeclaredSize: int64(len(decoded)), DecodedSHA256: hex.EncodeToString(digest[:]), ExpectedSHA256: hex.EncodeToString(expectedDigest[:]),
	}, nil
}

// ReadBlob returns exact bounded Git blob bytes through the strict REST
// transport. It is used for large evidence objects that the Contents API does
// not guarantee to inline.
func (c *Client) ReadBlob(ctx context.Context, repository, sha string) ([]byte, *TransportError) {
	ctx, cancel := c.operationContext(ctx)
	defer cancel()
	if !validRepository(repository) || !shaPattern.MatchString(sha) {
		return nil, &TransportError{Code: "INPUT_INVALID", Message: "repository and blob SHA are required"}
	}
	data, transportErr := c.Read(ctx, ReadRequest{Endpoint: fmt.Sprintf("repos/%s/git/blobs/%s", repository, sha)})
	if transportErr != nil {
		return nil, transportErr
	}
	object, err := exactObject(data)
	if err != nil {
		return nil, &TransportError{Code: "BLOB_INVALID", Message: err.Error(), Cause: err, Attempts: 1}
	}
	observedSHA, err := exactString(object, "sha")
	if err != nil || observedSHA != sha {
		return nil, &TransportError{Code: "BLOB_INVALID", Message: "blob SHA mismatch", Attempts: 1}
	}
	encoding, err := exactString(object, "encoding")
	if err != nil || encoding != "base64" {
		return nil, &TransportError{Code: "BLOB_INVALID", Message: "blob encoding must be base64", Attempts: 1}
	}
	size, err := exactNonNegativeInt64(object, "size")
	if err != nil || size > maxEvidenceBlobBytes {
		return nil, &TransportError{Code: "BLOB_INVALID", Message: "blob size is malformed", Attempts: 1}
	}
	content, err := exactAnyString(object, "content")
	if err != nil {
		return nil, &TransportError{Code: "BLOB_INVALID", Message: "blob content is malformed", Attempts: 1}
	}
	unwrapped := strings.NewReplacer("\r", "", "\n", "").Replace(content)
	decoded, err := base64.StdEncoding.Strict().DecodeString(unwrapped)
	if err != nil || base64.StdEncoding.EncodeToString(decoded) != unwrapped {
		return nil, &TransportError{Code: "BLOB_INVALID", Message: "blob content is not canonical base64", Attempts: 1, Cause: err}
	}
	if int64(len(decoded)) != size {
		return nil, &TransportError{Code: "BLOB_INVALID", Message: "blob declared size mismatch", Attempts: 1}
	}
	gitObject := append([]byte(fmt.Sprintf("blob %d\x00", len(decoded))), decoded...)
	gitDigest := sha1.Sum(gitObject)
	if hex.EncodeToString(gitDigest[:]) != sha {
		return nil, &TransportError{Code: "BLOB_INVALID", Message: "blob SHA is inconsistent with decoded Git object bytes", Attempts: 1}
	}
	return decoded, nil
}

func (c *Client) ReadContents(ctx context.Context, repository, path, ref string) ([]byte, *TransportError) {
	ctx, cancel := c.operationContext(ctx)
	defer cancel()
	if !validRepository(repository) || !shaPattern.MatchString(ref) || !validRelativePath(path) {
		return nil, &TransportError{Code: "INPUT_INVALID", Message: "repository, safe relative content path, and exact ref SHA are required"}
	}
	segments := strings.Split(path, "/")
	for index, segment := range segments {
		if segment == "" || segment == "." || segment == ".." {
			return nil, &TransportError{Code: "INPUT_INVALID", Message: "content path contains an unsafe segment"}
		}
		segments[index] = url.PathEscape(segment)
	}
	endpoint := fmt.Sprintf("repos/%s/contents/%s?ref=%s", repository, strings.Join(segments, "/"), url.QueryEscape(ref))
	return c.Read(ctx, ReadRequest{Endpoint: endpoint, Accept: "application/vnd.github.raw+json", AllowRaw: true})
}

func exactObject(data []byte) (map[string]json.RawMessage, error) {
	if err := validateVendorJSON(data); err != nil {
		return nil, err
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil || object == nil {
		return nil, errors.New("response is not a JSON object")
	}
	return object, nil
}

func exactString(object map[string]json.RawMessage, field string) (string, error) {
	value, err := exactAnyString(object, field)
	if err != nil || value == "" {
		return "", fmt.Errorf("field %s is not a non-empty string", field)
	}
	return value, nil
}

func exactAnyString(object map[string]json.RawMessage, field string) (string, error) {
	raw, ok := object[field]
	if !ok {
		return "", fmt.Errorf("field %s is absent", field)
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", fmt.Errorf("field %s is not a string", field)
	}
	return value, nil
}

func exactNonNegativeInt64(object map[string]json.RawMessage, field string) (int64, error) {
	raw, ok := object[field]
	if !ok {
		return 0, fmt.Errorf("field %s is absent", field)
	}
	var number json.Number
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&number); err != nil {
		return 0, err
	}
	value, err := strconv.ParseInt(number.String(), 10, 64)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("field %s is not a non-negative integer", field)
	}
	return value, nil
}

func optionalString(object map[string]json.RawMessage, field string) (string, error) {
	raw, ok := object[field]
	if !ok || bytes.Equal(raw, []byte("null")) {
		return "", nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", err
	}
	return value, nil
}

func exactInt64(object map[string]json.RawMessage, field string) (int64, error) {
	raw, ok := object[field]
	if !ok {
		return 0, fmt.Errorf("field %s is absent", field)
	}
	var number json.Number
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&number); err != nil {
		return 0, err
	}
	value, err := strconv.ParseInt(number.String(), 10, 64)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("field %s is not a positive integer", field)
	}
	return value, nil
}

func exactInt(object map[string]json.RawMessage, field string) (int, error) {
	raw, ok := object[field]
	if !ok {
		return 0, fmt.Errorf("field %s is outside integer range", field)
	}
	var number json.Number
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&number); err != nil {
		return 0, fmt.Errorf("field %s is outside integer range", field)
	}
	// A run attempt is accepted only in the native-int range [1, MaxInt].
	value, err := strconv.Atoi(number.String())
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("field %s is outside integer range", field)
	}
	return value, nil
}

func nestedString(object map[string]json.RawMessage, field, child string) (string, error) {
	raw, ok := object[field]
	if !ok {
		return "", fmt.Errorf("field %s is absent", field)
	}
	var nested map[string]json.RawMessage
	if err := json.Unmarshal(raw, &nested); err != nil || nested == nil {
		return "", fmt.Errorf("field %s is not an object", field)
	}
	return exactString(nested, child)
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
