package releasepromotion

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/ildarbinanas-design/env-vault/internal/releasecontract"
)

func TestValidateInventoryRequiresExactUnexpiredSingleAttemptMatrix(t *testing.T) {
	contractPath := filepath.Join("..", "..", filepath.FromSlash(releasecontract.CanonicalPath))
	run := inventoryRunFixture()
	artifacts := inventoryArtifactsFixture(t, contractPath)
	root := t.TempDir()
	runPath := writeJSONFile(t, root, "run.json", run)
	artifactsPath := writeJSONFile(t, root, "artifacts.json", artifacts)
	options := InventoryOptions{
		ContractPath: contractPath, RunPath: runPath, ArtifactsPath: artifactsPath,
		SourceSHA: testSource, Repository: testRepository, RunID: 42, RunAttempt: 3, Branch: "main",
	}
	if err := ValidateInventory(options); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		mutate func(map[string]any, map[string]any)
		reason string
	}{
		{
			name: "run attempt drift", reason: "RUN_IDENTITY_MISMATCH",
			mutate: func(run, _ map[string]any) { run["run_attempt"] = 4 },
		},
		{
			name: "missing target", reason: "ARTIFACT_RESPONSE_INCOMPLETE",
			mutate: func(_ map[string]any, inventory map[string]any) {
				items := inventory["artifacts"].([]any)
				inventory["artifacts"] = items[:len(items)-1]
			},
		},
		{
			name: "expired target", reason: "ARTIFACT_IDENTITY_MISMATCH",
			mutate: func(_ map[string]any, inventory map[string]any) {
				inventory["artifacts"].([]any)[0].(map[string]any)["expired"] = true
			},
		},
		{
			name: "wrong artifact source", reason: "ARTIFACT_IDENTITY_MISMATCH",
			mutate: func(_ map[string]any, inventory map[string]any) {
				inventory["artifacts"].([]any)[0].(map[string]any)["workflow_run"].(map[string]any)["head_sha"] = strings.Repeat("f", 40)
			},
		},
		{
			name: "duplicate target", reason: "DUPLICATE_ARTIFACT",
			mutate: func(_ map[string]any, inventory map[string]any) {
				items := inventory["artifacts"].([]any)
				copy := cloneJSONMap(items[0].(map[string]any))
				copy["id"] = 999
				inventory["artifacts"] = append(items, copy)
				inventory["total_count"] = len(items) + 1
			},
		},
		{
			name: "unexpected current attempt target", reason: "UNEXPECTED_ARTIFACT",
			mutate: func(_ map[string]any, inventory map[string]any) {
				items := inventory["artifacts"].([]any)
				copy := cloneJSONMap(items[0].(map[string]any))
				copy["id"] = 999
				copy["name"] = "env-vault-release-unknown-attempt-3"
				inventory["artifacts"] = append(items, copy)
				inventory["total_count"] = len(items) + 1
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			caseRoot := t.TempDir()
			caseRun := cloneJSONMap(run)
			caseArtifacts := cloneJSONMap(artifacts)
			test.mutate(caseRun, caseArtifacts)
			caseOptions := options
			caseOptions.RunPath = writeJSONFile(t, caseRoot, "run.json", caseRun)
			caseOptions.ArtifactsPath = writeJSONFile(t, caseRoot, "artifacts.json", caseArtifacts)
			err := ValidateInventory(caseOptions)
			if err == nil || !strings.Contains(err.Error(), InventoryErrorCode+":"+test.reason) {
				t.Fatalf("error=%v, want stable reason %s", err, test.reason)
			}
		})
	}
}

func TestValidateInventoryIgnoresOtherAttemptsButNeverUsesThem(t *testing.T) {
	contractPath := filepath.Join("..", "..", filepath.FromSlash(releasecontract.CanonicalPath))
	run := inventoryRunFixture()
	artifacts := inventoryArtifactsFixture(t, contractPath)
	items := artifacts["artifacts"].([]any)
	stale := cloneJSONMap(items[1].(map[string]any))
	stale["id"] = 999
	stale["name"] = "env-vault-release-linux-amd64-attempt-2"
	items = append(items, stale)
	artifacts["artifacts"] = items
	artifacts["total_count"] = len(items)
	root := t.TempDir()
	err := ValidateInventory(InventoryOptions{
		ContractPath:  contractPath,
		RunPath:       writeJSONFile(t, root, "run.json", run),
		ArtifactsPath: writeJSONFile(t, root, "artifacts.json", artifacts),
		SourceSHA:     testSource, Repository: testRepository, RunID: 42, RunAttempt: 3, Branch: "main",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Removing the current-attempt artifact proves the older attempt is not used
	// as a fallback even though it has the same platform identity.
	items = append(items[:1], items[2:]...)
	artifacts["artifacts"] = items
	artifacts["total_count"] = len(items)
	err = ValidateInventory(InventoryOptions{
		ContractPath:  contractPath,
		RunPath:       writeJSONFile(t, root, "run-2.json", run),
		ArtifactsPath: writeJSONFile(t, root, "artifacts-2.json", artifacts),
		SourceSHA:     testSource, Repository: testRepository, RunID: 42, RunAttempt: 3, Branch: "main",
	})
	if err == nil || !strings.Contains(err.Error(), "ARTIFACT_MATRIX_INCOMPLETE") {
		t.Fatalf("error=%v, want current-attempt matrix failure", err)
	}
}

func inventoryRunFixture() map[string]any {
	return map[string]any{
		"id": 42, "run_attempt": 3, "head_sha": testSource, "head_branch": "main",
		"head_repository": map[string]any{"full_name": testRepository},
		"event":           "push", "status": "completed", "conclusion": "success",
		"name": "ci", "path": ".github/workflows/ci.yml", "ignored_future_field": true,
	}
}

func inventoryArtifactsFixture(t *testing.T, contractPath string) map[string]any {
	t.Helper()
	contract, err := releasecontract.LoadFile(contractPath)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{"env-vault-promotion-" + testSource + "-attempt-3"}
	for _, platform := range contract.Platforms {
		names = append(names, "env-vault-release-"+platform.ID+"-attempt-3")
	}
	items := make([]any, 0, len(names)+1)
	for index, name := range names {
		items = append(items, map[string]any{
			"id": index + 1, "name": name, "expired": false,
			"workflow_run": map[string]any{"id": 42, "head_sha": testSource},
		})
	}
	// Platform promotion evidence is intentionally outside the six-artifact
	// publisher inventory and must not be mistaken for the aggregate manifest.
	items = append(items, map[string]any{
		"id": 100, "name": "env-vault-promotion-platform-linux-amd64-attempt-3", "expired": false,
		"workflow_run": map[string]any{"id": 42, "head_sha": testSource},
	})
	return map[string]any{"total_count": len(items), "artifacts": items}
}

func cloneJSONMap(value map[string]any) map[string]any {
	copy := make(map[string]any, len(value))
	for key, item := range value {
		switch typed := item.(type) {
		case map[string]any:
			copy[key] = cloneJSONMap(typed)
		case []any:
			items := make([]any, len(typed))
			for index, nested := range typed {
				if nestedMap, ok := nested.(map[string]any); ok {
					items[index] = cloneJSONMap(nestedMap)
				} else {
					items[index] = nested
				}
			}
			copy[key] = items
		default:
			copy[key] = item
		}
	}
	return copy
}
