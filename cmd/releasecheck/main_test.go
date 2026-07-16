package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/ildarbinanas-design/env-vault/internal/releasecontract"
	"github.com/ildarbinanas-design/env-vault/internal/releasemetrics"
	"github.com/ildarbinanas-design/env-vault/internal/releasepromotion"
)

func TestVersionJSONReportsCheckerAndSemanticContract(t *testing.T) {
	contractPath := canonicalContractPath(t)
	oldRevision := sourceRevision
	sourceRevision = strings.Repeat("c", 40)
	t.Cleanup(func() { sourceRevision = oldRevision })
	var stdout, stderr bytes.Buffer
	code := run([]string{"--version", "--contract", contractPath, "--json"}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	var document versionDocument
	decodeOneJSON(t, stdout.Bytes(), &document)
	if !document.OK || document.SchemaID != releasecontract.VersionSchemaID || document.CheckerVersion != checkerVersion {
		t.Fatalf("version document=%+v", document)
	}
	if document.SourceRevision != strings.Repeat("c", 40) || document.SourceModified == nil || *document.SourceModified {
		t.Fatalf("source build identity=%+v", document)
	}
	if document.ReleaseContractSchema != releasecontract.SchemaID || len(document.SemanticContractSHA256) != 64 {
		t.Fatalf("contract identity=%+v", document)
	}
	for _, schema := range []string{"release_contract", "release_contract_matrix", "releasecheck_version", "attempt_classification", "legacy_rebuild_query", "legacy_rebuild_diagnostic", "release_metrics", "release_metrics_baseline", "release_metrics_comparison", "source_quality_proof", "literal_version_results", "e2e_matrix_proof", "promotion_platform", "promotion_manifest", "promotion_verification", "release_observation", "release_health_proof", "release_authorization", "attestation_verification_bundle", "release_evidence", "repository_release_settings_check", "repository_release_settings_proof"} {
		if versions := document.SupportedSchemaVersions[schema]; len(versions) != 1 || versions[0] != 1 {
			t.Fatalf("supported %s versions=%v", schema, versions)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("unexpected stderr=%q", stderr.String())
	}
}

func TestLegacyQueryIsDiagnosticOnlyAndRejectsV008(t *testing.T) {
	contractPath := canonicalContractPath(t)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"legacy", "--version", "v0.0.7", "--contract", contractPath, "--json"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	var document legacyDocument
	decodeOneJSON(t, stdout.Bytes(), &document)
	if !document.OK || document.SchemaID != releasecontract.LegacyQuerySchemaID || document.Version != "v0.0.7" || document.TagSHA != "4fbae380747e75a1f59498adbd76ccf5791e0480" || document.GoVersion != "1.22.12" || document.PublicationEligible || document.ActionCode != "dispatch_legacy_rebuild" {
		t.Fatalf("legacy=%+v", document)
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"legacy", "--version", "v0.0.8", "--contract", contractPath, "--json"}, &stdout, &stderr); code != exitSnapshotInvalid {
		t.Fatalf("v0.0.8 code=%d stderr=%s", code, stderr.String())
	}
	var failure errorDocument
	decodeOneJSON(t, stdout.Bytes(), &failure)
	if failure.Error.Code != "LEGACY_REBUILD_UNSUPPORTED" {
		t.Fatalf("failure=%+v", failure)
	}
}

func TestContractMatrixJSONIsDirectlyConsumable(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"contract", "matrix", "--contract", canonicalContractPath(t), "--json"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	var matrix releasecontract.Matrix
	decodeOneJSON(t, stdout.Bytes(), &matrix)
	if len(matrix.Include) != 5 || matrix.Include[4].ID != "windows-amd64" || matrix.Include[4].Runner != "windows-latest" {
		t.Fatalf("matrix=%+v", matrix)
	}
	var raw map[string]json.RawMessage
	decodeOneJSON(t, stdout.Bytes(), &raw)
	if len(raw) != 1 || raw["include"] == nil {
		t.Fatalf("matrix has non-strategy envelope fields: %s", stdout.String())
	}
}

func TestPromotionSealSourceQualityCLI(t *testing.T) {
	contractPath := canonicalContractPath(t)
	contract, err := releasecontract.LoadFile(contractPath)
	if err != nil {
		t.Fatal(err)
	}
	workflow, ok := contract.WorkflowByID("ci")
	if !ok {
		t.Fatal("CI workflow missing")
	}
	source := strings.Repeat("f", 40)
	proof := releasepromotion.SourceQualityProof{
		SchemaID: contract.Schemas["source_quality_proof"], SchemaVersion: 1,
		SourceSHA: source, ReleaseVersion: "v0.0.9", Repository: "ildarbinanas-design/env-vault",
		Workflow: releasepromotion.Workflow{
			ID: workflow.ID, Name: workflow.Name, File: workflow.File, RunID: 101, RunAttempt: 2,
			Event: "push", HeadSHA: source,
		},
		ObservedJobs: releasepromotion.SourceQualityObservedJobs{SourceQuality: "success", LicenseMatrix: "success"},
	}
	input, err := releasepromotion.MarshalJSON(proof)
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "sealed-source-quality.json")
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"promotion", "seal-source-quality", "--contract", contractPath,
		"--input", writeTestFile(t, input), "--output", output, "--json",
	}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	var sealed releasepromotion.SourceQualityProof
	decodeOneJSON(t, stdout.Bytes(), &sealed)
	if sealed.Result != "pass" || len(sealed.ProofSHA256) != 64 || sealed.Results.Module != "pass" || sealed.Results.Licenses["windows"] != "pass" {
		t.Fatalf("sealed proof=%+v", sealed)
	}
	if err := releasepromotion.ValidateSourceQualityProof(sealed, contract); err != nil {
		t.Fatal(err)
	}
	fileData, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var fromFile releasepromotion.SourceQualityProof
	decodeOneJSON(t, fileData, &fromFile)
	if fromFile.ProofSHA256 != sealed.ProofSHA256 {
		t.Fatal("stdout and no-clobber file contain different proofs")
	}
	stdout.Reset()
	stderr.Reset()
	code = run([]string{
		"promotion", "seal-source-quality", "--contract", contractPath,
		"--input", writeTestFile(t, input), "--output", output, "--json",
	}, &stdout, &stderr)
	if code != exitInternal {
		t.Fatalf("no-clobber code=%d stderr=%s", code, stderr.String())
	}
	var outputFailure errorDocument
	decodeOneJSON(t, stdout.Bytes(), &outputFailure)
	if outputFailure.Error.Code != "OUTPUT_FAILED" {
		t.Fatalf("output failure=%+v", outputFailure)
	}

	proof.ObservedJobs.LicenseMatrix = "failure"
	input, _ = releasepromotion.MarshalJSON(proof)
	stdout.Reset()
	stderr.Reset()
	code = run([]string{
		"promotion", "seal-source-quality", "--contract", contractPath,
		"--input", writeTestFile(t, input), "--output", "-", "--json",
	}, &stdout, &stderr)
	if code != exitSnapshotInvalid {
		t.Fatalf("failure code=%d stderr=%s", code, stderr.String())
	}
	var failure errorDocument
	decodeOneJSON(t, stdout.Bytes(), &failure)
	if failure.Error.Code != "PROMOTION_MANIFEST_INVALID" {
		t.Fatalf("failure=%+v", failure)
	}
}

func TestPromotionCommandRequiresKnownSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"promotion", "unknown"}, &stdout, &stderr); code != exitUsage || !strings.Contains(stderr.String(), "record-platform") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

func TestMetricsCLIStdoutAndFile(t *testing.T) {
	runJSON, err := json.Marshal(validMetricsRun())
	if err != nil {
		t.Fatal(err)
	}
	runPath := writeTestFile(t, runJSON)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"metrics", "--run-json", runPath, "--output", "-"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("stdout code=%d stderr=%s", code, stderr.String())
	}
	var metrics releasemetrics.Metrics
	decodeOneJSON(t, stdout.Bytes(), &metrics)
	if metrics.SchemaID != releasemetrics.SchemaID || metrics.RunID != 77 || metrics.JobCount != 1 || metrics.AggregateRunnerSeconds != 30 {
		t.Fatalf("metrics=%+v", metrics)
	}

	stdout.Reset()
	stderr.Reset()
	output := filepath.Join(t.TempDir(), "metrics.json")
	if code := run([]string{"metrics", "--run-json", runPath, "--output", output}, &stdout, &stderr); code != exitOK {
		t.Fatalf("file code=%d stderr=%s", code, stderr.String())
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	decodeOneJSON(t, data, &metrics)
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("file output stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestMetricsCLIFailsClosed(t *testing.T) {
	invalid := writeTestFile(t, []byte(`{"attempt":1,"unexpected":true}`))
	var stdout, stderr bytes.Buffer
	if code := run([]string{"metrics", "--run-json", invalid, "--output", "-"}, &stdout, &stderr); code != exitSnapshotInvalid {
		t.Fatalf("invalid code=%d stderr=%s", code, stderr.String())
	}
	var document errorDocument
	decodeOneJSON(t, stdout.Bytes(), &document)
	if document.Error.Code != "INPUT_INVALID" || document.OK {
		t.Fatalf("error=%+v", document)
	}

	stdout.Reset()
	stderr.Reset()
	valid, err := json.Marshal(validMetricsRun())
	if err != nil {
		t.Fatal(err)
	}
	if code := run([]string{"metrics", "--run-json", writeTestFile(t, valid), "--output", t.TempDir()}, &stdout, &stderr); code != exitInternal {
		t.Fatalf("output code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "OUTPUT_FAILED") {
		t.Fatalf("output failure=%q", stderr.String())
	}
}

func TestMetricsOutputIsNoClobberAndRejectsSymlink(t *testing.T) {
	valid, err := json.Marshal(validMetricsRun())
	if err != nil {
		t.Fatal(err)
	}
	runPath := writeTestFile(t, valid)
	directory := t.TempDir()
	existing := filepath.Join(directory, "existing.json")
	if err := os.WriteFile(existing, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"metrics", "--run-json", runPath, "--output", existing}, &stdout, &stderr); code != exitInternal {
		t.Fatalf("existing code=%d stderr=%s", code, stderr.String())
	}
	data, err := os.ReadFile(existing)
	if err != nil || string(data) != "preserve" {
		t.Fatalf("existing output changed: data=%q err=%v", data, err)
	}

	target := filepath.Join(directory, "target.json")
	if err := os.WriteFile(target, []byte("target-preserved"), 0o600); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(directory, "metrics.json")
	if err := os.Symlink(target, symlink); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"metrics", "--run-json", runPath, "--output", symlink}, &stdout, &stderr); code != exitInternal {
		t.Fatalf("symlink code=%d stderr=%s", code, stderr.String())
	}
	data, err = os.ReadFile(target)
	if err != nil || string(data) != "target-preserved" {
		t.Fatalf("symlink target changed: data=%q err=%v", data, err)
	}
}

func TestValidateContractJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"validate-contract", "--contract", canonicalContractPath(t), "--json"}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	var document validationDocument
	decodeOneJSON(t, stdout.Bytes(), &document)
	if !document.OK || document.SchemaID != releasecontract.ValidationSchemaID || document.PlatformCount != 5 || document.AssetCount != 10 || len(document.SemanticContractSHA256) != 64 {
		t.Fatalf("validation=%+v", document)
	}
}

func TestClassifyAttemptCLICompleteSuccess(t *testing.T) {
	contractPath := canonicalContractPath(t)
	contract, err := releasecontract.LoadFile(contractPath)
	if err != nil {
		t.Fatal(err)
	}
	runPath, artifactsPath := writeAttemptInputs(t, contract, 7, false)
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"classify-attempt", "--contract", contractPath, "--run", runPath, "--artifacts", artifactsPath, "--json",
	}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	var result releasecontract.AttemptClassification
	decodeOneJSON(t, stdout.Bytes(), &result)
	if !result.OK || result.ActionCode != "none" || !result.MatrixComplete || result.RunID != 9001 || result.Attempt != 7 {
		t.Fatalf("classification=%+v", result)
	}
	if result.RerunFailedJobsAllowed || len(result.ProhibitedActions) != 1 || result.ProhibitedActions[0] != "rerun_failed_jobs" {
		t.Fatalf("failed-only rerun prohibition=%+v", result)
	}
}

func TestClassifyAttemptCLICompletedSuccessIncompleteIsActionRequired(t *testing.T) {
	contractPath := canonicalContractPath(t)
	contract, err := releasecontract.LoadFile(contractPath)
	if err != nil {
		t.Fatal(err)
	}
	runPath, artifactsPath := writeAttemptInputs(t, contract, 8, true)
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"classify-attempt", "--contract", contractPath, "--run", runPath, "--artifacts", artifactsPath, "--json",
	}, &stdout, &stderr)
	if code != exitActionRequired {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	var result releasecontract.AttemptClassification
	decodeOneJSON(t, stdout.Bytes(), &result)
	if result.OK || result.ActionCode != "rerun_all_jobs" || result.ReasonCode != "ATTEMPT_MATRIX_INCOMPLETE" || result.MatrixComplete {
		t.Fatalf("classification=%+v", result)
	}
	if len(result.MissingTargets) != 1 || result.MissingTargets[0] != "windows-amd64" || result.RerunFailedJobsAllowed {
		t.Fatalf("missing/prohibition=%+v", result)
	}
}

func TestCLIInvalidContractAndSnapshotHaveStableExitAndErrorCodes(t *testing.T) {
	invalidContract := writeTestFile(t, []byte(`{"schema_id":"unknown"}`))
	tests := []struct {
		name string
		args []string
		code int
		want string
	}{
		{
			name: "contract", args: []string{"validate-contract", "--contract", invalidContract, "--json"},
			code: exitContractInvalid, want: "CONTRACT_INVALID",
		},
		{
			name: "snapshot", args: []string{
				"classify-attempt", "--contract", canonicalContractPath(t),
				"--run", writeTestFile(t, []byte(`{}`)),
				"--artifacts", writeTestFile(t, []byte(`{"total_count":0,"artifacts":[]}`)), "--json",
			},
			code: exitSnapshotInvalid, want: "INPUT_INCOMPLETE",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(test.args, &stdout, &stderr); code != test.code {
				t.Fatalf("code=%d want=%d stderr=%s", code, test.code, stderr.String())
			}
			var document errorDocument
			decodeOneJSON(t, stdout.Bytes(), &document)
			if document.OK || document.SchemaID != releasecontract.ErrorSchemaID || document.Error.Code != test.want {
				t.Fatalf("error document=%+v", document)
			}
			if stderr.Len() != 0 {
				t.Fatalf("JSON failure wrote stderr=%q", stderr.String())
			}
		})
	}
}

func TestHelpDocumentsOfflineBoundaryAndExitStatuses(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"help"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	for _, text := range []string{
		"never accesses the network or credentials",
		"rerun_failed_jobs_allowed=false",
		"Exit statuses:",
		"4  valid classification requires wait, inspection, or rerun_all_jobs",
		"5  saved input or promotion evidence invalid, incomplete, or inconsistent",
	} {
		if !strings.Contains(stdout.String(), text) {
			t.Fatalf("help missing %q:\n%s", text, stdout.String())
		}
	}
	stdout.Reset()
	stderr.Reset()
	if code := run(nil, &stdout, &stderr); code != exitUsage || !strings.Contains(stderr.String(), "Usage:") {
		t.Fatalf("empty invocation code=%d stderr=%q", code, stderr.String())
	}
}

func TestOutputFailureUsesInternalExit(t *testing.T) {
	var stderr bytes.Buffer
	code := run([]string{"validate-contract", "--contract", canonicalContractPath(t), "--json"}, failingWriter{}, &stderr)
	if code != exitInternal || !strings.Contains(stderr.String(), "write JSON") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func canonicalContractPath(t *testing.T) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "..", filepath.FromSlash(releasecontract.CanonicalPath)))
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func writeAttemptInputs(t *testing.T, contract releasecontract.Contract, attempt int, incomplete bool) (string, string) {
	t.Helper()
	const runID int64 = 9001
	sourceSHA := strings.Repeat("d", 40)
	run, err := json.Marshal(map[string]any{
		"id": runID, "run_attempt": attempt, "status": "completed", "conclusion": "success",
		"head_sha": sourceSHA, "head_branch": "main", "event": "push", "name": "ci",
		"path":            ".github/workflows/ci.yml",
		"repository":      map[string]any{"full_name": "ildarbinanas-design/env-vault"},
		"head_repository": map[string]any{"full_name": "ildarbinanas-design/env-vault"},
	})
	if err != nil {
		t.Fatal(err)
	}
	artifacts := make([]map[string]any, 0, 10)
	id := int64(1)
	for _, platform := range contract.Platforms {
		for _, template := range []string{contract.Naming.PlatformArtifactTemplate, contract.Naming.PlatformEvidenceTemplate} {
			name, err := contract.RenderName(template, map[string]string{
				"platform": platform.ID,
				"attempt":  strconv.Itoa(attempt),
			})
			if err != nil {
				t.Fatal(err)
			}
			artifacts = append(artifacts, map[string]any{
				"id": id, "name": name, "expired": false,
				"workflow_run": map[string]any{"id": runID, "head_sha": sourceSHA},
			})
			id++
		}
	}
	if incomplete {
		artifacts = artifacts[:len(artifacts)-2]
	}
	artifactResponse, err := json.Marshal(map[string]any{"total_count": len(artifacts), "artifacts": artifacts})
	if err != nil {
		t.Fatal(err)
	}
	return writeTestFile(t, run), writeTestFile(t, artifactResponse)
}

func writeTestFile(t *testing.T, data []byte) string {
	t.Helper()
	filename := filepath.Join(t.TempDir(), "input.json")
	if err := os.WriteFile(filename, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return filename
}

func decodeOneJSON(t *testing.T, data []byte, destination any) {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(destination); err != nil {
		t.Fatalf("decode %q: %v", data, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		t.Fatalf("extra JSON in %q: %v", data, err)
	}
}

func validMetricsRun() releasemetrics.GHRun {
	return releasemetrics.GHRun{
		Attempt: 1, Conclusion: "success", CreatedAt: "2026-07-16T06:00:00Z", DatabaseID: 77,
		Event: "push", HeadSHA: strings.Repeat("e", 40), StartedAt: "2026-07-16T06:00:05Z",
		Status: "completed", UpdatedAt: "2026-07-16T06:00:40Z", URL: "https://github.example/runs/77", WorkflowName: "ci",
		Jobs: []releasemetrics.GHJob{{
			CompletedAt: "2026-07-16T06:00:35Z", Conclusion: "success", DatabaseID: 78,
			Name: "quality / source", StartedAt: "2026-07-16T06:00:05Z", Status: "completed",
			URL: "https://github.example/jobs/78", Steps: []releasemetrics.GHStep{{
				CompletedAt: "2026-07-16T06:00:15Z", Conclusion: "success", Name: "Run tests",
				Number: 1, StartedAt: "2026-07-16T06:00:06Z", Status: "completed",
			}},
		}},
	}
}
