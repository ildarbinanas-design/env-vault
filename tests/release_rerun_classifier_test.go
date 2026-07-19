package tests

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
)

const rerunClassificationToken = "token-must-never-appear-in-output"

func TestRerunClassifiedAttemptInvokesOnlyFullRerun(t *testing.T) {
	root := t.TempDir()
	classification := writeRerunClassification(t, root, validRerunClassification(t))
	logPath := filepath.Join(root, "gh.log")
	path := installRerunFakeGH(t, root, logPath)

	output, status := runRerunHelper(t, classification, "ildarbinanas-design/env-vault", path)
	if status != 0 {
		t.Fatalf("status=%d, want 0\n%s", status, output)
	}
	if strings.Contains(output, rerunClassificationToken) {
		t.Fatalf("helper exposed a token: %q", output)
	}
	arguments := nonemptyRerunLines(readRerunFile(t, logPath))
	want := []string{"run", "rerun", "29475939348", "--repo", "ildarbinanas-design/env-vault"}
	if !slices.Equal(arguments, want) {
		t.Fatalf("gh arguments=%q, want exactly %q", arguments, want)
	}
	if slices.Contains(arguments, "--failed") {
		t.Fatalf("full rerun unexpectedly used --failed: %q", arguments)
	}
}

func TestRerunClassifiedAttemptFailsClosedWithoutCallingGH(t *testing.T) {
	valid := validRerunClassification(t)
	validJSON := marshalRerunJSON(t, valid)

	tests := []struct {
		name       string
		document   []byte
		repository string
	}{
		{name: "malformed JSON", document: []byte(`{"schema_id":`)},
		{name: "multiple documents", document: append(append([]byte{}, validJSON...), append([]byte("\n"), validJSON...)...)},
		{name: "unknown field", document: mutateRerunDocument(t, valid, func(value map[string]any) { value["transport_hint"] = "failed" })},
		{name: "case variant field", document: addRerunRawField(t, validJSON, `"Action_Code":"rerun_all_jobs"`)},
		{name: "duplicate field", document: duplicateRerunActionField(t, validJSON)},
		{name: "complete matrix", document: mutateRerunDocument(t, valid, func(value map[string]any) { value["matrix_complete"] = true })},
		{name: "wrong action", document: mutateRerunDocument(t, valid, func(value map[string]any) { value["action_code"] = "rerun_failed_jobs" })},
		{name: "unknown reason", document: mutateRerunDocument(t, valid, func(value map[string]any) { value["reason_code"] = "UNKNOWN" })},
		{name: "generic CI failure", document: mutateRerunDocument(t, valid, func(value map[string]any) { value["reason_code"] = "CI_ATTEMPT_FAILED" })},
		{name: "failed only allowed", document: mutateRerunDocument(t, valid, func(value map[string]any) { value["rerun_failed_jobs_allowed"] = true })},
		{name: "missing prohibition", document: mutateRerunDocument(t, valid, func(value map[string]any) { value["prohibited_actions"] = []string{} })},
		{name: "ambiguous prohibition", document: mutateRerunDocument(t, valid, func(value map[string]any) {
			value["prohibited_actions"] = []string{"rerun_failed_jobs", "rerun_all_jobs"}
		})},
		{name: "invalid run ID", document: mutateRerunDocument(t, valid, func(value map[string]any) { value["run_id"] = 0 })},
		{name: "fractional attempt", document: mutateRerunDocument(t, valid, func(value map[string]any) { value["attempt"] = 2.5 })},
		{name: "classification repository mismatch", document: mutateRerunDocument(t, valid, func(value map[string]any) { value["repository"] = "attacker/env-vault" })},
		{name: "head repository mismatch", document: mutateRerunDocument(t, valid, func(value map[string]any) { value["head_repository"] = "attacker/env-vault" })},
		{name: "wrong event", document: mutateRerunDocument(t, valid, func(value map[string]any) { value["event"] = "workflow_dispatch" })},
		{name: "wrong branch", document: mutateRerunDocument(t, valid, func(value map[string]any) { value["head_branch"] = "release" })},
		{name: "wrong workflow path", document: mutateRerunDocument(t, valid, func(value map[string]any) { value["workflow_path"] = ".github/workflows/other.yml" })},
		{name: "no incomplete evidence", document: mutateRerunDocument(t, valid, func(value map[string]any) {
			value["observed_targets"] = value["expected_targets"]
			value["missing_targets"] = []string{}
			value["observed_artifacts"] = value["expected_artifacts"]
			value["missing_artifacts"] = []string{}
		})},
		{name: "invalid repository", document: validJSON, repository: "ildarbinanas-design/env-vault/extra"},
		{name: "foreign canonical repository", document: validJSON, repository: "attacker/env-vault"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			classification := filepath.Join(root, "classification.json")
			if err := os.WriteFile(classification, test.document, 0o600); err != nil {
				t.Fatal(err)
			}
			logPath := filepath.Join(root, "gh.log")
			path := installRerunFakeGH(t, root, logPath)
			repository := test.repository
			if repository == "" {
				repository = "ildarbinanas-design/env-vault"
			}

			output, status := runRerunHelper(t, classification, repository, path)
			if status == 0 {
				t.Fatalf("invalid classification was accepted\n%s", output)
			}
			if strings.Contains(output, rerunClassificationToken) {
				t.Fatalf("helper exposed a token: %q", output)
			}
			if _, err := os.Stat(logPath); !os.IsNotExist(err) {
				t.Fatalf("gh was called for refused input; stat error=%v, log=%q", err, readRerunFile(t, logPath))
			}
		})
	}
}

func validRerunClassification(t *testing.T) map[string]any {
	t.Helper()
	projectionData, err := os.ReadFile(os.Getenv("RELEASE_CONTRACT_PROJECTION_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	var projection struct {
		ContractSemanticSHA256 string `json:"contract_semantic_sha256"`
	}
	if err := json.Unmarshal(projectionData, &projection); err != nil {
		t.Fatal(err)
	}
	if len(projection.ContractSemanticSHA256) != 64 {
		t.Fatal("shared typed projection has a malformed semantic digest")
	}
	targets := []string{"darwin-amd64", "darwin-arm64", "linux-amd64", "linux-arm64", "windows-amd64"}
	var artifacts []string
	for _, target := range targets {
		artifacts = append(artifacts,
			"env-vault-promotion-platform-"+target+"-attempt-3",
			"env-vault-release-"+target+"-attempt-3",
		)
	}
	sort.Strings(artifacts)
	observedArtifacts := slices.Clone(artifacts)
	var missingArtifacts []string
	observedArtifacts = slices.DeleteFunc(observedArtifacts, func(value string) bool {
		if strings.Contains(value, "windows-amd64") {
			missingArtifacts = append(missingArtifacts, value)
			return true
		}
		return false
	})
	sort.Strings(missingArtifacts)

	return map[string]any{
		"schema_id":                 "env-vault.attempt-classification.v1",
		"schema_version":            1,
		"ok":                        false,
		"release_contract_schema":   "env-vault.release-contract.v2",
		"semantic_contract_sha256":  projection.ContractSemanticSHA256,
		"run_id":                    int64(29475939348),
		"attempt":                   3,
		"source_sha":                strings.Repeat("a", 40),
		"repository":                "ildarbinanas-design/env-vault",
		"head_repository":           "ildarbinanas-design/env-vault",
		"event":                     "push",
		"head_branch":               "main",
		"workflow_id":               "ci",
		"workflow_name":             "ci",
		"workflow_path":             ".github/workflows/ci.yml",
		"run_status":                "completed",
		"run_conclusion":            "success",
		"matrix_complete":           false,
		"expected_targets":          targets,
		"observed_targets":          targets[:4],
		"missing_targets":           []string{"windows-amd64"},
		"expected_artifacts":        artifacts,
		"observed_artifacts":        observedArtifacts,
		"missing_artifacts":         missingArtifacts,
		"unexpected_artifacts":      []string{},
		"duplicate_artifacts":       []string{},
		"expired_artifacts":         []string{},
		"action_code":               "rerun_all_jobs",
		"reason_code":               "ATTEMPT_MATRIX_INCOMPLETE",
		"rerun_failed_jobs_allowed": false,
		"prohibited_actions":        []string{"rerun_failed_jobs"},
	}
}

func mutateRerunDocument(t *testing.T, original map[string]any, mutate func(map[string]any)) []byte {
	t.Helper()
	copy := make(map[string]any, len(original))
	for key, value := range original {
		copy[key] = value
	}
	mutate(copy)
	return marshalRerunJSON(t, copy)
}

func marshalRerunJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func addRerunRawField(t *testing.T, document []byte, field string) []byte {
	t.Helper()
	if len(document) == 0 || document[len(document)-1] != '}' {
		t.Fatal("fixture is not an object")
	}
	return append(append(append([]byte{}, document[:len(document)-1]...), ','), append([]byte(field), '}')...)
}

func duplicateRerunActionField(t *testing.T, document []byte) []byte {
	t.Helper()
	needle := []byte(`"action_code":"rerun_all_jobs"`)
	replacement := []byte(`"action_code":"rerun_all_jobs","action_code":"rerun_all_jobs"`)
	if strings.Count(string(document), string(needle)) != 1 {
		t.Fatal("action fixture is not unique")
	}
	return []byte(strings.Replace(string(document), string(needle), string(replacement), 1))
}

func writeRerunClassification(t *testing.T, root string, value any) string {
	t.Helper()
	path := filepath.Join(root, "classification.json")
	if err := os.WriteFile(path, marshalRerunJSON(t, value), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func installRerunFakeGH(t *testing.T, root, logPath string) string {
	t.Helper()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/usr/bin/env bash\nset -euo pipefail\nprintf '%s\\n' \"$@\" >\"$FAKE_GH_LOG\"\n"
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin + string(os.PathListSeparator) + os.Getenv("PATH")
}

func runRerunHelper(t *testing.T, classification, repository, path string) (string, int) {
	t.Helper()
	command := exec.Command("bash", "../scripts/release/rerun-classified-attempt.sh", classification, repository)
	command.Env = append(os.Environ(),
		"PATH="+path,
		"FAKE_GH_LOG="+filepath.Join(filepath.Dir(classification), "gh.log"),
		"GH_TOKEN="+rerunClassificationToken,
	)
	output, err := command.CombinedOutput()
	if err == nil {
		return string(output), 0
	}
	exitError, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("run helper: %v\n%s", err, output)
	}
	return string(output), exitError.ExitCode()
}

func readRerunFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatal(err)
	}
	return string(data)
}

func nonemptyRerunLines(value string) []string {
	var lines []string
	for _, line := range strings.Split(value, "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}
