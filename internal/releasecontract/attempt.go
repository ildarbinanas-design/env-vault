package releasecontract

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

const maxSnapshotBytes = 8 << 20

// OfflineError is a stable, machine-classifiable local input error.
type OfflineError struct {
	Code  string
	Field string
	Err   error
}

func (e *OfflineError) Error() string {
	if e.Field == "" {
		return fmt.Sprintf("%s: %v", e.Code, e.Err)
	}
	return fmt.Sprintf("%s: %s: %v", e.Code, e.Field, e.Err)
}

func (e *OfflineError) Unwrap() error { return e.Err }

func ErrorCode(err error) string {
	var offline *OfflineError
	if errors.As(err, &offline) {
		return offline.Code
	}
	return "INPUT_INVALID"
}

// AttemptClassification is derived only from saved run and artifact JSON.
// It never contains an API endpoint or credential-bearing value.
type AttemptClassification struct {
	SchemaID               string   `json:"schema_id"`
	SchemaVersion          int      `json:"schema_version"`
	OK                     bool     `json:"ok"`
	ReleaseContractSchema  string   `json:"release_contract_schema"`
	SemanticContractSHA256 string   `json:"semantic_contract_sha256"`
	RunID                  int64    `json:"run_id"`
	Attempt                int      `json:"attempt"`
	SourceSHA              string   `json:"source_sha"`
	Repository             string   `json:"repository"`
	HeadRepository         string   `json:"head_repository"`
	Event                  string   `json:"event"`
	HeadBranch             string   `json:"head_branch"`
	WorkflowID             string   `json:"workflow_id"`
	WorkflowName           string   `json:"workflow_name"`
	WorkflowPath           string   `json:"workflow_path"`
	RunStatus              string   `json:"run_status"`
	RunConclusion          string   `json:"run_conclusion,omitempty"`
	MatrixComplete         bool     `json:"matrix_complete"`
	ExpectedTargets        []string `json:"expected_targets"`
	ObservedTargets        []string `json:"observed_targets"`
	MissingTargets         []string `json:"missing_targets"`
	ExpectedArtifacts      []string `json:"expected_artifacts"`
	ObservedArtifacts      []string `json:"observed_artifacts"`
	MissingArtifacts       []string `json:"missing_artifacts"`
	UnexpectedArtifacts    []string `json:"unexpected_artifacts"`
	DuplicateArtifacts     []string `json:"duplicate_artifacts"`
	ExpiredArtifacts       []string `json:"expired_artifacts"`
	ActionCode             string   `json:"action_code"`
	ReasonCode             string   `json:"reason_code"`
	RerunFailedJobsAllowed bool     `json:"rerun_failed_jobs_allowed"`
	ProhibitedActions      []string `json:"prohibited_actions"`
}

type rawWorkflowRun struct {
	ID             *int64          `json:"id"`
	RunAttempt     *int            `json:"run_attempt"`
	Status         *string         `json:"status"`
	Conclusion     json.RawMessage `json:"conclusion"`
	HeadSHA        *string         `json:"head_sha"`
	HeadBranch     *string         `json:"head_branch"`
	Event          *string         `json:"event"`
	Name           *string         `json:"name"`
	Path           *string         `json:"path"`
	Repository     *rawRepository  `json:"repository"`
	HeadRepository *rawRepository  `json:"head_repository"`
}

type rawRepository struct {
	FullName *string `json:"full_name"`
}

type rawArtifactsResponse struct {
	TotalCount *int          `json:"total_count"`
	Artifacts  []rawArtifact `json:"artifacts"`
}

type rawArtifact struct {
	ID          *int64          `json:"id"`
	Name        *string         `json:"name"`
	Expired     *bool           `json:"expired"`
	WorkflowRun *rawArtifactRun `json:"workflow_run"`
}

type rawArtifactRun struct {
	ID      *int64  `json:"id"`
	HeadSHA *string `json:"head_sha"`
}

type parsedRun struct {
	id             int64
	attempt        int
	status         string
	conclusion     string
	headSHA        string
	repository     string
	headRepository string
	event          string
	headBranch     string
	workflowName   string
	workflowPath   string
}

// ClassifyAttemptFiles classifies one fully saved REST workflow-run response
// and one fully saved REST run-artifacts response.
func ClassifyAttemptFiles(runFilename, artifactsFilename string, contract Contract) (AttemptClassification, error) {
	runData, err := readLimitedFile(runFilename, maxSnapshotBytes)
	if err != nil {
		return AttemptClassification{}, inputError("INPUT_INVALID", "run", err)
	}
	artifactsData, err := readLimitedFile(artifactsFilename, maxSnapshotBytes)
	if err != nil {
		return AttemptClassification{}, inputError("INPUT_INVALID", "artifacts", err)
	}
	return ClassifyAttempt(runData, artifactsData, contract)
}

// ClassifyAttempt is the byte-oriented, offline classifier core.
func ClassifyAttempt(runData, artifactsData []byte, contract Contract) (AttemptClassification, error) {
	if err := contract.Validate(); err != nil {
		return AttemptClassification{}, inputError("CONTRACT_INVALID", "contract", err)
	}
	run, err := parseRun(runData, contract)
	if err != nil {
		return AttemptClassification{}, err
	}
	artifacts, err := parseArtifacts(artifactsData, run)
	if err != nil {
		return AttemptClassification{}, err
	}
	classification := classify(run, artifacts, contract)
	digest, err := SemanticSHA256(contract)
	if err != nil {
		return AttemptClassification{}, inputError("CONTRACT_INVALID", "contract", err)
	}
	classification.ReleaseContractSchema = contract.SchemaID
	classification.SemanticContractSHA256 = digest
	if !contract.HasActionCode(classification.ActionCode) || !contains(contract.ReasonCodes, classification.ReasonCode) {
		return AttemptClassification{}, inputError("CONTRACT_INVALID", "codes", errors.New("classifier code is absent from release contract"))
	}
	return classification, nil
}

func parseRun(data []byte, contract Contract) (parsedRun, error) {
	var raw rawWorkflowRun
	if err := decodeKnownJSON(data, &raw); err != nil {
		return parsedRun{}, inputError("INPUT_INVALID", "run", err)
	}
	if raw.ID == nil || *raw.ID <= 0 {
		return parsedRun{}, inputError("INPUT_INCOMPLETE", "run.id", errors.New("positive id is required"))
	}
	if raw.RunAttempt == nil || *raw.RunAttempt <= 0 {
		return parsedRun{}, inputError("INPUT_INCOMPLETE", "run.run_attempt", errors.New("positive attempt is required"))
	}
	if raw.Status == nil || !supportedRunStatus(*raw.Status) {
		return parsedRun{}, inputError("ATTEMPT_STATE_INCONSISTENT", "run.status", errors.New("status is absent or unknown"))
	}
	if raw.HeadSHA == nil || !isSHA(*raw.HeadSHA) {
		return parsedRun{}, inputError("INPUT_INCOMPLETE", "run.head_sha", errors.New("lowercase full source SHA is required"))
	}
	workflow, ok := contract.WorkflowByID("ci")
	expectedRepository, repositoryOK := releaseRepository(contract)
	expectedPath := ".github/workflows/" + workflow.File
	if !ok || raw.Path == nil || *raw.Path != expectedPath {
		return parsedRun{}, inputError("ATTEMPT_STATE_INCONSISTENT", "run.path", errors.New("run is not the contract CI workflow"))
	}
	if raw.Name == nil || *raw.Name != workflow.Name {
		return parsedRun{}, inputError("ATTEMPT_STATE_INCONSISTENT", "run.name", errors.New("run name is not the contract CI workflow"))
	}
	if !repositoryOK || raw.Repository == nil || raw.Repository.FullName == nil || *raw.Repository.FullName != expectedRepository {
		return parsedRun{}, inputError("ATTEMPT_STATE_INCONSISTENT", "run.repository.full_name", errors.New("run is not bound to the contract repository"))
	}
	if raw.HeadRepository == nil || raw.HeadRepository.FullName == nil || *raw.HeadRepository.FullName != expectedRepository {
		return parsedRun{}, inputError("ATTEMPT_STATE_INCONSISTENT", "run.head_repository.full_name", errors.New("run head is not owned by the contract repository"))
	}
	if raw.Event == nil || *raw.Event != "push" {
		return parsedRun{}, inputError("ATTEMPT_STATE_INCONSISTENT", "run.event", errors.New("run is not a push"))
	}
	if raw.HeadBranch == nil || *raw.HeadBranch != "main" {
		return parsedRun{}, inputError("ATTEMPT_STATE_INCONSISTENT", "run.head_branch", errors.New("run is not on main"))
	}
	conclusion, present, err := nullableString(raw.Conclusion)
	if err != nil || !present {
		return parsedRun{}, inputError("INPUT_INCOMPLETE", "run.conclusion", errors.New("conclusion field is required and must be string or null"))
	}
	if *raw.Status == "completed" {
		if conclusion == "" || !supportedConclusion(conclusion) {
			return parsedRun{}, inputError("ATTEMPT_STATE_INCONSISTENT", "run.conclusion", errors.New("completed run has absent or unknown conclusion"))
		}
	} else if conclusion != "" {
		return parsedRun{}, inputError("ATTEMPT_STATE_INCONSISTENT", "run.conclusion", errors.New("non-completed run has a conclusion"))
	}
	return parsedRun{
		id: *raw.ID, attempt: *raw.RunAttempt, status: *raw.Status, conclusion: conclusion,
		headSHA: *raw.HeadSHA, repository: expectedRepository, headRepository: expectedRepository,
		event: "push", headBranch: "main", workflowName: workflow.Name, workflowPath: expectedPath,
	}, nil
}

func parseArtifacts(data []byte, run parsedRun) ([]rawArtifact, error) {
	var pages []rawArtifactsResponse
	if strings.HasPrefix(strings.TrimSpace(string(data)), "[") {
		if err := decodeKnownJSON(data, &pages); err != nil {
			return nil, inputError("INPUT_INVALID", "artifacts", err)
		}
		if len(pages) == 0 {
			return nil, inputError("INPUT_INCOMPLETE", "artifacts", errors.New("paginated response is empty"))
		}
	} else {
		var response rawArtifactsResponse
		if err := decodeKnownJSON(data, &response); err != nil {
			return nil, inputError("INPUT_INVALID", "artifacts", err)
		}
		pages = []rawArtifactsResponse{response}
	}
	artifacts := make([]rawArtifact, 0)
	total := -1
	for pageIndex, response := range pages {
		if response.TotalCount == nil || response.Artifacts == nil {
			return nil, inputError("INPUT_INCOMPLETE", fmt.Sprintf("artifacts[%d]", pageIndex), errors.New("total_count and artifacts array are required"))
		}
		if *response.TotalCount < 0 || (total >= 0 && *response.TotalCount != total) {
			return nil, inputError("INPUT_INCOMPLETE", fmt.Sprintf("artifacts[%d].total_count", pageIndex), errors.New("paginated total_count is invalid or inconsistent"))
		}
		total = *response.TotalCount
		artifacts = append(artifacts, response.Artifacts...)
	}
	if total != len(artifacts) {
		return nil, inputError("INPUT_INCOMPLETE", "artifacts.total_count", errors.New("saved response does not contain every paginated artifact"))
	}
	seenIDs := make(map[int64]bool)
	for index, artifact := range artifacts {
		field := "artifacts[" + strconv.Itoa(index) + "]"
		if artifact.ID == nil || *artifact.ID <= 0 || seenIDs[*artifact.ID] {
			return nil, inputError("INPUT_INVALID", field+".id", errors.New("positive unique id is required"))
		}
		seenIDs[*artifact.ID] = true
		if artifact.Name == nil || *artifact.Name == "" || *artifact.Name != strings.TrimSpace(*artifact.Name) || len(*artifact.Name) > 256 {
			return nil, inputError("INPUT_INVALID", field+".name", errors.New("non-empty canonical artifact name is required"))
		}
		if artifact.Expired == nil {
			return nil, inputError("INPUT_INCOMPLETE", field+".expired", errors.New("expired state is required"))
		}
		if artifact.WorkflowRun == nil || artifact.WorkflowRun.ID == nil || artifact.WorkflowRun.HeadSHA == nil || *artifact.WorkflowRun.ID != run.id || *artifact.WorkflowRun.HeadSHA != run.headSHA {
			return nil, inputError("ATTEMPT_STATE_INCONSISTENT", field+".workflow_run", errors.New("artifact is not bound to the exact run and source SHA"))
		}
	}
	return artifacts, nil
}

func classify(run parsedRun, artifacts []rawArtifact, contract Contract) AttemptClassification {
	result := AttemptClassification{
		SchemaID: ClassificationSchemaID, SchemaVersion: 1,
		RunID: run.id, Attempt: run.attempt, SourceSHA: run.headSHA,
		Repository: run.repository, HeadRepository: run.headRepository, Event: run.event, HeadBranch: run.headBranch,
		WorkflowID: "ci", WorkflowName: run.workflowName, WorkflowPath: run.workflowPath,
		RunStatus: run.status, RunConclusion: run.conclusion,
		ExpectedTargets: []string{}, ObservedTargets: []string{}, MissingTargets: []string{},
		ExpectedArtifacts: []string{}, ObservedArtifacts: []string{}, MissingArtifacts: []string{},
		UnexpectedArtifacts: []string{}, DuplicateArtifacts: []string{}, ExpiredArtifacts: []string{},
		RerunFailedJobsAllowed: false, ProhibitedActions: []string{"rerun_failed_jobs"},
	}
	wanted := make(map[string]string, len(contract.Platforms)*2)
	for _, platform := range contract.Platforms {
		result.ExpectedTargets = append(result.ExpectedTargets, platform.ID)
		for _, name := range expectedNames(contract, platform.ID, run.attempt) {
			result.ExpectedArtifacts = append(result.ExpectedArtifacts, name)
			wanted[name] = platform.ID
		}
	}
	allCounts := make(map[string]int)
	liveCounts := make(map[string]int)
	currentPrefixes, currentSuffixes := currentArtifactEnvelopes(contract.Naming, run.attempt)
	for _, artifact := range artifacts {
		name := *artifact.Name
		if _, expected := wanted[name]; expected {
			allCounts[name]++
			if *artifact.Expired {
				result.ExpiredArtifacts = append(result.ExpiredArtifacts, name)
			} else {
				liveCounts[name]++
			}
			continue
		}
		for index := range currentPrefixes {
			if strings.HasPrefix(name, currentPrefixes[index]) && strings.HasSuffix(name, currentSuffixes[index]) {
				result.UnexpectedArtifacts = append(result.UnexpectedArtifacts, name)
				break
			}
		}
	}
	missingTargets := make(map[string]bool)
	completeTargets := make(map[string]int)
	for _, name := range result.ExpectedArtifacts {
		target := wanted[name]
		if liveCounts[name] == 1 && allCounts[name] == 1 {
			result.ObservedArtifacts = append(result.ObservedArtifacts, name)
			completeTargets[target]++
		} else {
			missingTargets[target] = true
			if liveCounts[name] == 0 {
				result.MissingArtifacts = append(result.MissingArtifacts, name)
			}
		}
		if allCounts[name] > 1 {
			result.DuplicateArtifacts = append(result.DuplicateArtifacts, name)
		}
	}
	for _, target := range result.ExpectedTargets {
		if missingTargets[target] {
			result.MissingTargets = append(result.MissingTargets, target)
		} else if completeTargets[target] == 2 {
			result.ObservedTargets = append(result.ObservedTargets, target)
		}
	}
	for _, values := range [][]string{
		result.ExpectedTargets, result.ObservedTargets, result.MissingTargets,
		result.ExpectedArtifacts, result.ObservedArtifacts, result.MissingArtifacts,
		result.UnexpectedArtifacts, result.DuplicateArtifacts, result.ExpiredArtifacts,
	} {
		sort.Strings(values)
	}
	result.MatrixComplete = len(result.ExpectedTargets) == 5 && len(result.ExpectedArtifacts) == 10 &&
		len(result.MissingTargets) == 0 && len(result.MissingArtifacts) == 0 &&
		len(result.UnexpectedArtifacts) == 0 && len(result.DuplicateArtifacts) == 0 && len(result.ExpiredArtifacts) == 0
	switch {
	case run.status != "completed":
		result.ActionCode = "wait_attempt"
		result.ReasonCode = "ATTEMPT_NOT_COMPLETED"
	case !result.MatrixComplete:
		result.ActionCode = "rerun_all_jobs"
		result.ReasonCode = "ATTEMPT_MATRIX_INCOMPLETE"
	case run.conclusion != "success":
		result.ActionCode = "inspect_failure"
		result.ReasonCode = "CI_ATTEMPT_FAILED"
	default:
		result.OK = true
		result.ActionCode = "none"
		result.ReasonCode = "ATTEMPT_MATRIX_COMPLETE"
	}
	return result
}

func expectedNames(contract Contract, platform string, attempt int) []string {
	values := map[string]string{"platform": platform, "attempt": strconv.Itoa(attempt)}
	artifact, _ := contract.RenderName(contract.Naming.PlatformArtifactTemplate, values)
	evidence, _ := contract.RenderName(contract.Naming.PlatformEvidenceTemplate, values)
	return []string{artifact, evidence}
}

func currentArtifactEnvelopes(naming Naming, attempt int) ([]string, []string) {
	templates := []string{naming.PlatformArtifactTemplate, naming.PlatformEvidenceTemplate}
	prefixes, suffixes := make([]string, 0, len(templates)), make([]string, 0, len(templates))
	for _, template := range templates {
		prefix, suffix, _ := strings.Cut(template, "{platform}")
		prefixes = append(prefixes, prefix)
		suffixes = append(suffixes, strings.ReplaceAll(suffix, "{attempt}", strconv.Itoa(attempt)))
	}
	return prefixes, suffixes
}

func nullableString(raw json.RawMessage) (string, bool, error) {
	if len(raw) == 0 {
		return "", false, nil
	}
	if string(raw) == "null" {
		return "", true, nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", true, err
	}
	return value, true, nil
}

func supportedRunStatus(status string) bool {
	switch status {
	case "requested", "waiting", "pending", "queued", "in_progress", "completed":
		return true
	default:
		return false
	}
}

func supportedConclusion(conclusion string) bool {
	switch conclusion {
	case "success", "failure", "neutral", "cancelled", "skipped", "timed_out", "action_required", "startup_failure", "stale":
		return true
	default:
		return false
	}
}

func releaseRepository(contract Contract) (string, bool) {
	for _, app := range contract.Apps {
		if app.ID == "release_planning" {
			return app.Repository, true
		}
	}
	return "", false
}

func inputError(code, field string, err error) error {
	return &OfflineError{Code: code, Field: field, Err: err}
}
