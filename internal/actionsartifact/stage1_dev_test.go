package actionsartifact

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ildarbinanas-design/env-vault/internal/strictjson"
)

// This development-only proof intentionally consumes an external Stage 1
// directory and never copies it into the repository. It proves lineage
// coverage only. Full strict assembly/classification still requires the raw
// attempt documents collected by actionsartifactcollect.
func TestDevelopmentStage1InventoryHasNoUnknownLineage(t *testing.T) {
	directory := os.Getenv("ENV_VAULT_STAGE1_ACTIONS_DIR")
	if directory == "" {
		t.Skip("ENV_VAULT_STAGE1_ACTIONS_DIR is not set")
	}
	policy := loadCanonicalPolicy(t)
	type developmentArtifact struct {
		Name        string `json:"name"`
		SizeInBytes int64  `json:"size_in_bytes"`
		WorkflowRun struct {
			ID int64 `json:"id"`
		} `json:"workflow_run"`
	}
	type developmentRun struct {
		ID   int64  `json:"id"`
		Path string `json:"path"`
	}
	load := func(filename string, destination any) {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(directory, filename))
		if err != nil {
			t.Fatal(err)
		}
		if err := strictjson.Validate(data, MaxSnapshotBytes); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, destination); err != nil {
			t.Fatal(err)
		}
	}
	var artifacts []developmentArtifact
	var runs []developmentRun
	load("env-vault-artifacts.json", &artifacts)
	load("env-vault-workflow-runs.json", &runs)
	runPaths := make(map[int64]string, len(runs))
	for _, run := range runs {
		if run.ID < 1 || run.Path == "" || runPaths[run.ID] != "" {
			t.Fatalf("invalid or duplicate development run %d", run.ID)
		}
		runPaths[run.ID] = run.Path
	}
	lifecycleCounts := make(map[string]int)
	lifecycleBytes := make(map[string]int64)
	var totalBytes int64
	for index, artifact := range artifacts {
		path := runPaths[artifact.WorkflowRun.ID]
		if path == "" {
			t.Fatalf("artifacts[%d] has no fully collected producer run", index)
		}
		match, err := matchNameValidated(policy, path, artifact.Name)
		if err != nil {
			t.Fatalf("artifacts[%d]: %v", index, err)
		}
		lifecycleCounts[match.Pattern.Lifecycle]++
		lifecycleBytes[match.Pattern.Lifecycle] += artifact.SizeInBytes
		totalBytes += artifact.SizeInBytes
	}
	t.Logf("stage1 lineage coverage artifacts=%d bytes=%d lifecycle_counts=%v lifecycle_bytes=%v", len(artifacts), totalBytes, lifecycleCounts, lifecycleBytes)
}
