// Package githubtransport is the release-only GitHub REST transport boundary.
// It invokes gh for credential-aware network I/O and never stores credentials.
package githubtransport

import (
	"encoding/json"
	"time"
)

const (
	TransportVersion = "1.3.0"
	APIVersion       = "2022-11-28"
	Host             = "github.com"

	CapabilitiesSchemaID      = "env-vault.github-transport-capabilities.v2"
	CapabilitiesSchemaVersion = 2
	ErrorSchemaID             = "env-vault.github-transport-error.v1"
	ActionsIdentitySchemaID   = "env-vault.github-actions-identity.v1"
	BlobIdentitySchemaID      = "env-vault.github-blob-identity.v1"
	ObservationSchemaID       = "env-vault.github-rest-observation.v1"
	MutationSchemaID          = "env-vault.github-mutation-outcome.v1"
)

const (
	ExitOK         = 0
	ExitUsage      = 2
	ExitCapability = 3
	ExitNotFound   = 4
	ExitRemote     = 5
	ExitOutput     = 6
)

type CapabilitiesDocument struct {
	SchemaID                  string   `json:"schema_id"`
	SchemaVersion             int      `json:"schema_version"`
	OK                        bool     `json:"ok"`
	TransportVersion          string   `json:"transport_version"`
	GitHubAPIVersion          string   `json:"github_api_version"`
	Host                      string   `json:"host"`
	GHVersion                 string   `json:"gh_version"`
	MaxAttemptsPerPage        int      `json:"max_attempts_per_page"`
	MaxPages                  int      `json:"max_pages"`
	MaxTotalRequests          int      `json:"max_total_requests"`
	MaxTotalRetryWaitSecs     int      `json:"max_total_retry_wait_seconds"`
	MaxRequestSeconds         int      `json:"max_request_seconds"`
	MaxOperationSeconds       int      `json:"max_operation_seconds"`
	MaxAggregateResponseBytes int64    `json:"max_aggregate_response_bytes"`
	Capabilities              []string `json:"capabilities"`
}

type ErrorDocument struct {
	SchemaID      string    `json:"schema_id"`
	SchemaVersion int       `json:"schema_version"`
	OK            bool      `json:"ok"`
	Error         ErrorInfo `json:"error"`
}

type ErrorInfo struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	HTTPStatus int    `json:"http_status,omitempty"`
	Retriable  bool   `json:"retriable"`
	Attempts   int    `json:"attempts"`
}

type TransportError struct {
	Code          string
	Message       string
	HTTPStatus    int
	Retriable     bool
	Attempts      int
	RetryAfter    time.Duration
	RetryDelaySet bool
	Cause         error
}

func (e *TransportError) Error() string { return e.Message }
func (e *TransportError) Unwrap() error { return e.Cause }

func ErrorDocumentFor(err *TransportError) ErrorDocument {
	return ErrorDocument{
		SchemaID: ErrorSchemaID, SchemaVersion: 1, OK: false,
		Error: ErrorInfo{Code: err.Code, Message: err.Message, HTTPStatus: err.HTTPStatus, Retriable: err.Retriable, Attempts: err.Attempts},
	}
}

type RunIdentity struct {
	Repository     string `json:"repository"`
	HeadRepository string `json:"head_repository"`
	RunID          int64  `json:"run_id"`
	RunAttempt     int    `json:"run_attempt"`
	WorkflowPath   string `json:"workflow_path"`
	Event          string `json:"event"`
	HeadSHA        string `json:"head_sha"`
	HeadRef        string `json:"head_ref"`
	Status         string `json:"status"`
	Conclusion     string `json:"conclusion"`
	CanonicalURL   string `json:"canonical_url"`
	DiagnosticName string `json:"diagnostic_name,omitempty"`
}

type JobIdentity struct {
	JobID                  int64  `json:"job_id"`
	RunID                  int64  `json:"run_id"`
	RunAttempt             int    `json:"run_attempt"`
	HeadSHA                string `json:"head_sha"`
	CheckName              string `json:"check_name"`
	DiagnosticWorkflowName string `json:"diagnostic_workflow_name,omitempty"`
	Status                 string `json:"status"`
	Conclusion             string `json:"conclusion"`
	CanonicalURL           string `json:"canonical_url"`
}

type ActionsIdentityDocument struct {
	SchemaID      string       `json:"schema_id"`
	SchemaVersion int          `json:"schema_version"`
	OK            bool         `json:"ok"`
	Run           RunIdentity  `json:"run"`
	Job           *JobIdentity `json:"job,omitempty"`
}

type BlobIdentityDocument struct {
	SchemaID       string `json:"schema_id"`
	SchemaVersion  int    `json:"schema_version"`
	OK             bool   `json:"ok"`
	Repository     string `json:"repository"`
	SHA            string `json:"sha"`
	Encoding       string `json:"encoding"`
	DeclaredSize   int64  `json:"declared_size"`
	DecodedSHA256  string `json:"decoded_sha256"`
	ExpectedSHA256 string `json:"expected_sha256"`
}

type ObservationDocument struct {
	SchemaID      string `json:"schema_id"`
	SchemaVersion int    `json:"schema_version"`
	OK            bool   `json:"ok"`
	Endpoint      string `json:"endpoint"`
	HTTPStatus    int    `json:"http_status"`
	ServerDate    string `json:"server_date"`
	BodySHA256    string `json:"body_sha256"`
}

type MutationDocument struct {
	SchemaID      string          `json:"schema_id"`
	SchemaVersion int             `json:"schema_version"`
	OK            bool            `json:"ok"`
	Outcome       string          `json:"outcome"`
	Method        string          `json:"method"`
	Endpoint      string          `json:"endpoint"`
	HTTPStatus    int             `json:"http_status"`
	BodySHA256    string          `json:"body_sha256,omitempty"`
	Body          json.RawMessage `json:"body,omitempty"`
	ErrorCode     string          `json:"error_code"`
}
