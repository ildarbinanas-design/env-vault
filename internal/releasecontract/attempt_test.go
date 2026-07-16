package releasecontract

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

const testRunID int64 = 4242

var testSourceSHA = strings.Repeat("a", 40)

func TestClassifyAttemptCompleteSuccess(t *testing.T) {
	contract := loadCanonicalForTest(t)
	result, err := ClassifyAttempt(testRunJSON(t, "completed", "success", 3), testArtifactsJSON(t, contract, 3, nil), contract)
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK || !result.MatrixComplete || result.ActionCode != "none" || result.ReasonCode != "ATTEMPT_MATRIX_COMPLETE" {
		t.Fatalf("classification=%+v", result)
	}
	if result.ReleaseContractSchema != SchemaID || len(result.SemanticContractSHA256) != 64 {
		t.Fatalf("contract identity=%+v", result)
	}
	if result.Repository != "ildarbinanas-design/env-vault" || result.HeadRepository != result.Repository || result.Event != "push" || result.HeadBranch != "main" || result.WorkflowName != "ci" || result.WorkflowPath != ".github/workflows/ci.yml" {
		t.Fatalf("run identity=%+v", result)
	}
	if len(result.ExpectedTargets) != 5 || len(result.ObservedTargets) != 5 || len(result.ExpectedArtifacts) != 10 || len(result.ObservedArtifacts) != 10 {
		t.Fatalf("matrix counts: %+v", result)
	}
	assertFailedOnlyRerunProhibited(t, result)
}

func TestClassifyAttemptCompletedSuccessIncompleteRerunsAll(t *testing.T) {
	contract := loadCanonicalForTest(t)
	result, err := ClassifyAttempt(
		testRunJSON(t, "completed", "success", 4),
		testArtifactsJSON(t, contract, 4, func(artifacts *[]map[string]any) { *artifacts = (*artifacts)[:8] }),
		contract,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.OK || result.MatrixComplete || result.ActionCode != "rerun_all_jobs" || result.ReasonCode != "ATTEMPT_MATRIX_INCOMPLETE" {
		t.Fatalf("classification=%+v", result)
	}
	if len(result.MissingTargets) != 1 || result.MissingTargets[0] != "windows-amd64" || len(result.MissingArtifacts) != 2 {
		t.Fatalf("missing matrix=%+v", result)
	}
	assertFailedOnlyRerunProhibited(t, result)
}

func TestClassifyAttemptFailedCompleteRequiresInspectionWithoutRerun(t *testing.T) {
	contract := loadCanonicalForTest(t)
	result, err := ClassifyAttempt(testRunJSON(t, "completed", "failure", 1), testArtifactsJSON(t, contract, 1, nil), contract)
	if err != nil {
		t.Fatal(err)
	}
	if result.OK || !result.MatrixComplete || result.ActionCode != "inspect_failure" || result.ReasonCode != "CI_ATTEMPT_FAILED" {
		t.Fatalf("classification=%+v", result)
	}
	assertFailedOnlyRerunProhibited(t, result)
}

func TestClassifyAttemptStillRunningWaits(t *testing.T) {
	contract := loadCanonicalForTest(t)
	result, err := ClassifyAttempt(testRunJSON(t, "in_progress", "", 1), []byte(`{"total_count":0,"artifacts":[]}`), contract)
	if err != nil {
		t.Fatal(err)
	}
	if result.ActionCode != "wait_attempt" || result.ReasonCode != "ATTEMPT_NOT_COMPLETED" || result.MatrixComplete {
		t.Fatalf("classification=%+v", result)
	}
	assertFailedOnlyRerunProhibited(t, result)
}

func TestClassifyAttemptDoesNotMixAttempts(t *testing.T) {
	contract := loadCanonicalForTest(t)
	oldOnly := testArtifactsJSON(t, contract, 2, nil)
	result, err := ClassifyAttempt(testRunJSON(t, "completed", "success", 3), oldOnly, contract)
	if err != nil {
		t.Fatal(err)
	}
	if result.ActionCode != "rerun_all_jobs" || len(result.MissingTargets) != 5 || len(result.ObservedArtifacts) != 0 {
		t.Fatalf("old artifacts satisfied current attempt: %+v", result)
	}

	currentWithOld := testArtifactsJSON(t, contract, 3, func(artifacts *[]map[string]any) {
		var old map[string]any
		if err := json.Unmarshal(testArtifactsJSON(t, contract, 2, nil), &old); err != nil {
			t.Fatal(err)
		}
		oldArtifact := old["artifacts"].([]any)[0].(map[string]any)
		oldArtifact["id"] = int64(999)
		*artifacts = append(*artifacts, oldArtifact)
	})
	result, err = ClassifyAttempt(testRunJSON(t, "completed", "success", 3), currentWithOld, contract)
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK || !result.MatrixComplete {
		t.Fatalf("old-attempt artifact polluted exact current attempt: %+v", result)
	}
}

func TestClassifyAttemptAcceptsCompleteSlurpedPagination(t *testing.T) {
	contract := loadCanonicalForTest(t)
	var response map[string]any
	if err := json.Unmarshal(testArtifactsJSON(t, contract, 3, nil), &response); err != nil {
		t.Fatal(err)
	}
	all := response["artifacts"].([]any)
	pages := []map[string]any{
		{"total_count": len(all), "artifacts": all[:4]},
		{"total_count": len(all), "artifacts": all[4:]},
	}
	paginated, err := json.Marshal(pages)
	if err != nil {
		t.Fatal(err)
	}
	result, err := ClassifyAttempt(testRunJSON(t, "completed", "success", 3), paginated, contract)
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK || !result.MatrixComplete {
		t.Fatalf("paginated classification=%+v", result)
	}
	pages[1]["artifacts"] = all[4:9]
	incomplete, _ := json.Marshal(pages)
	if _, err := ClassifyAttempt(testRunJSON(t, "completed", "success", 3), incomplete, contract); ErrorCode(err) != "INPUT_INCOMPLETE" {
		t.Fatalf("incomplete pagination err=%v code=%s", err, ErrorCode(err))
	}
}

func TestClassifyAttemptDetectsDuplicateExpiredAndUnexpected(t *testing.T) {
	contract := loadCanonicalForTest(t)
	data := testArtifactsJSON(t, contract, 5, func(artifacts *[]map[string]any) {
		duplicate := cloneMap((*artifacts)[0])
		duplicate["id"] = int64(999)
		*artifacts = append(*artifacts, duplicate)
		(*artifacts)[1]["expired"] = true
		*artifacts = append(*artifacts, map[string]any{
			"id": int64(1000), "name": "env-vault-release-linux-386-attempt-5", "expired": false,
			"workflow_run": map[string]any{"id": testRunID, "head_sha": testSourceSHA},
		})
	})
	result, err := ClassifyAttempt(testRunJSON(t, "completed", "success", 5), data, contract)
	if err != nil {
		t.Fatal(err)
	}
	if result.MatrixComplete || result.ActionCode != "rerun_all_jobs" || len(result.DuplicateArtifacts) != 1 || len(result.ExpiredArtifacts) != 1 || len(result.UnexpectedArtifacts) != 1 {
		t.Fatalf("classification=%+v", result)
	}
}

func TestClassifyAttemptRejectsInvalidOrIncompatibleSnapshots(t *testing.T) {
	contract := loadCanonicalForTest(t)
	validRun := testRunJSON(t, "completed", "success", 1)
	validArtifacts := testArtifactsJSON(t, contract, 1, nil)
	tests := map[string]struct {
		run       []byte
		artifacts []byte
		code      string
	}{
		"wrong workflow": {
			run:       replaceJSONField(t, validRun, "path", ".github/workflows/build-binaries.yml"),
			artifacts: validArtifacts, code: "ATTEMPT_STATE_INCONSISTENT",
		},
		"wrong workflow name": {
			run:       replaceJSONField(t, validRun, "name", "other"),
			artifacts: validArtifacts, code: "ATTEMPT_STATE_INCONSISTENT",
		},
		"workflow path with ref suffix": {
			run:       replaceJSONField(t, validRun, "path", ".github/workflows/ci.yml@refs/heads/main"),
			artifacts: validArtifacts, code: "ATTEMPT_STATE_INCONSISTENT",
		},
		"wrong repository": {
			run:       replaceNestedJSONField(t, validRun, "repository", "full_name", "attacker/env-vault"),
			artifacts: validArtifacts, code: "ATTEMPT_STATE_INCONSISTENT",
		},
		"wrong head repository": {
			run:       replaceNestedJSONField(t, validRun, "head_repository", "full_name", "attacker/env-vault"),
			artifacts: validArtifacts, code: "ATTEMPT_STATE_INCONSISTENT",
		},
		"wrong event": {
			run:       replaceJSONField(t, validRun, "event", "workflow_dispatch"),
			artifacts: validArtifacts, code: "ATTEMPT_STATE_INCONSISTENT",
		},
		"wrong branch": {
			run:       replaceJSONField(t, validRun, "head_branch", "release"),
			artifacts: validArtifacts, code: "ATTEMPT_STATE_INCONSISTENT",
		},
		"unknown status": {
			run:       replaceJSONField(t, validRun, "status", "mysterious"),
			artifacts: validArtifacts, code: "ATTEMPT_STATE_INCONSISTENT",
		},
		"success while running": {
			run:       testRunJSONWithConclusion(t, "in_progress", "success", 1),
			artifacts: validArtifacts, code: "ATTEMPT_STATE_INCONSISTENT",
		},
		"incomplete pagination": {
			run: validRun,
			artifacts: func() []byte {
				var value map[string]any
				if err := json.Unmarshal(validArtifacts, &value); err != nil {
					t.Fatal(err)
				}
				value["total_count"] = 100
				encoded, _ := json.Marshal(value)
				return encoded
			}(),
			code: "INPUT_INCOMPLETE",
		},
		"artifact source mismatch": {
			run: validRun,
			artifacts: testArtifactsJSON(t, contract, 1, func(artifacts *[]map[string]any) {
				(*artifacts)[0]["workflow_run"].(map[string]any)["head_sha"] = strings.Repeat("b", 40)
			}),
			code: "ATTEMPT_STATE_INCONSISTENT",
		},
		"duplicate input field": {
			run:       []byte(`{"id":4242,"id":4242,"run_attempt":1,"status":"completed","conclusion":"success","head_sha":"` + testSourceSHA + `","path":".github/workflows/ci.yml"}`),
			artifacts: validArtifacts, code: "INPUT_INVALID",
		},
		"case variant identity": {
			run:       []byte(`{"ID":4242,"run_attempt":1,"status":"completed","conclusion":"success","head_sha":"` + testSourceSHA + `","path":".github/workflows/ci.yml"}`),
			artifacts: validArtifacts, code: "INPUT_INCOMPLETE",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := ClassifyAttempt(test.run, test.artifacts, contract)
			if err == nil {
				t.Fatal("invalid snapshot was accepted")
			}
			if code := ErrorCode(err); code != test.code {
				t.Fatalf("error=%v code=%q want=%q", err, code, test.code)
			}
		})
	}
}

func testRunJSON(t *testing.T, status, conclusion string, attempt int) []byte {
	t.Helper()
	if conclusion == "" {
		return testRunJSONWithRawConclusion(t, status, nil, attempt)
	}
	return testRunJSONWithRawConclusion(t, status, conclusion, attempt)
}

func testRunJSONWithConclusion(t *testing.T, status, conclusion string, attempt int) []byte {
	t.Helper()
	return testRunJSONWithRawConclusion(t, status, conclusion, attempt)
}

func testRunJSONWithRawConclusion(t *testing.T, status string, conclusion any, attempt int) []byte {
	t.Helper()
	encoded, err := json.Marshal(map[string]any{
		"id": testRunID, "run_attempt": attempt, "status": status, "conclusion": conclusion,
		"head_sha": testSourceSHA, "head_branch": "main", "event": "push", "name": "ci",
		"path":             ".github/workflows/ci.yml",
		"repository":       map[string]any{"full_name": "ildarbinanas-design/env-vault"},
		"head_repository":  map[string]any{"full_name": "ildarbinanas-design/env-vault"},
		"api_future_field": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func testArtifactsJSON(t *testing.T, contract Contract, attempt int, mutate func(*[]map[string]any)) []byte {
	t.Helper()
	artifacts := make([]map[string]any, 0, 10)
	id := int64(1)
	for _, platform := range contract.Platforms {
		for _, name := range expectedNames(contract, platform.ID, attempt) {
			artifacts = append(artifacts, map[string]any{
				"id": id, "name": name, "expired": false, "api_future_field": "ignored",
				"workflow_run": map[string]any{"id": testRunID, "head_sha": testSourceSHA},
			})
			id++
		}
	}
	if mutate != nil {
		mutate(&artifacts)
	}
	encoded, err := json.Marshal(map[string]any{"total_count": len(artifacts), "artifacts": artifacts})
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func replaceJSONField(t *testing.T, data []byte, key string, value any) []byte {
	t.Helper()
	var object map[string]any
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatal(err)
	}
	object[key] = value
	encoded, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func replaceNestedJSONField(t *testing.T, data []byte, objectKey, key string, value any) []byte {
	t.Helper()
	var object map[string]any
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatal(err)
	}
	nested, ok := object[objectKey].(map[string]any)
	if !ok {
		t.Fatalf("%s is not an object", objectKey)
	}
	nested[key] = value
	encoded, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func cloneMap(source map[string]any) map[string]any {
	clone := make(map[string]any, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}

func assertFailedOnlyRerunProhibited(t *testing.T, result AttemptClassification) {
	t.Helper()
	if result.RerunFailedJobsAllowed || len(result.ProhibitedActions) != 1 || result.ProhibitedActions[0] != "rerun_failed_jobs" {
		t.Fatalf("failed-only rerun was not explicitly prohibited: %+v", result)
	}
}

func TestOfflineErrorSupportsErrorsAs(t *testing.T) {
	err := inputError("INPUT_INVALID", "field", errors.New("bad"))
	var offline *OfflineError
	if !errors.As(err, &offline) || offline.Code != "INPUT_INVALID" {
		t.Fatalf("typed error=%v", err)
	}
}
