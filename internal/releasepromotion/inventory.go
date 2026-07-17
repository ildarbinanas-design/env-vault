package releasepromotion

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/ildarbinanas-design/env-vault/internal/releasecontract"
)

const InventoryErrorCode = "PROMOTION_ARTIFACT_INVENTORY_INVALID"

// InventoryOptions binds the remote Actions run and artifact inventory to the
// exact CI attempt which produced the release promotion manifest. The caller
// must fetch both JSON documents immediately before the state-changing action.
type InventoryOptions struct {
	ContractPath  string
	RunPath       string
	ArtifactsPath string
	SourceSHA     string
	Repository    string
	RunID         int64
	RunAttempt    int
	Branch        string
}

type actionsRun struct {
	ID             int64  `json:"id"`
	RunAttempt     int    `json:"run_attempt"`
	HeadSHA        string `json:"head_sha"`
	HeadBranch     string `json:"head_branch"`
	HeadRepository *struct {
		FullName string `json:"full_name"`
	} `json:"head_repository"`
	Event      string `json:"event"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	Name       string `json:"name"`
	Path       string `json:"path"`
}

type artifactInventory struct {
	TotalCount *int              `json:"total_count"`
	Artifacts  []actionsArtifact `json:"artifacts"`
}

type actionsArtifact struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Expired     *bool  `json:"expired"`
	WorkflowRun *struct {
		ID      int64  `json:"id"`
		HeadSHA string `json:"head_sha"`
	} `json:"workflow_run"`
}

// ValidateInventory fails closed unless the successful exact CI run still has
// one unexpired promotion manifest and one unexpired release artifact for each
// native platform. Artifacts from another workflow attempt are never accepted.
func ValidateInventory(options InventoryOptions) error {
	if !shaPattern.MatchString(options.SourceSHA) || !validRepository(options.Repository) || options.RunID <= 0 || options.RunAttempt <= 0 || strings.TrimSpace(options.Branch) == "" {
		return inventoryError("INPUT_INVALID", "source SHA, repository, run ID, run attempt, or branch is invalid")
	}
	contract, err := releasecontract.LoadFile(options.ContractPath)
	if err != nil {
		return inventoryError("CONTRACT_INVALID", err.Error())
	}
	workflow, ok := contract.WorkflowByID("ci")
	if !ok {
		return inventoryError("CONTRACT_INVALID", "CI workflow is absent from the release contract")
	}

	var run actionsRun
	if err := decodeAPIFile(options.RunPath, &run); err != nil {
		return inventoryError("RUN_RESPONSE_INVALID", err.Error())
	}
	if run.HeadRepository == nil || run.ID != options.RunID || run.RunAttempt != options.RunAttempt || run.HeadSHA != options.SourceSHA || run.HeadBranch != options.Branch || run.HeadRepository.FullName != options.Repository || run.Event != "push" || run.Status != "completed" || run.Conclusion != "success" || run.Name != workflow.Name || run.Path != path.Join(".github/workflows", workflow.File) {
		return inventoryError("RUN_IDENTITY_MISMATCH", "workflow run does not match the exact successful CI source/run/attempt identity")
	}

	var inventory artifactInventory
	if err := decodeAPIFile(options.ArtifactsPath, &inventory); err != nil {
		return inventoryError("ARTIFACT_RESPONSE_INVALID", err.Error())
	}
	if inventory.TotalCount == nil || *inventory.TotalCount < 0 || *inventory.TotalCount > 100 || *inventory.TotalCount != len(inventory.Artifacts) {
		return inventoryError("ARTIFACT_RESPONSE_INCOMPLETE", "artifact response is paginated, truncated, or malformed")
	}

	expected := map[string]bool{
		fmt.Sprintf("env-vault-promotion-%s-attempt-%d", options.SourceSHA, options.RunAttempt): false,
	}
	for _, platform := range contract.Platforms {
		expected[fmt.Sprintf("env-vault-release-%s-attempt-%d", platform.ID, options.RunAttempt)] = false
	}
	attemptSuffix := fmt.Sprintf("-attempt-%d", options.RunAttempt)
	for _, artifact := range inventory.Artifacts {
		_, exactExpected := expected[artifact.Name]
		currentAttemptRelease := strings.HasPrefix(artifact.Name, "env-vault-release-") && strings.HasSuffix(artifact.Name, attemptSuffix)
		if !exactExpected && !currentAttemptRelease {
			continue
		}
		if !exactExpected {
			return inventoryError("UNEXPECTED_ARTIFACT", fmt.Sprintf("unexpected current-attempt release artifact %q", artifact.Name))
		}
		if expected[artifact.Name] {
			return inventoryError("DUPLICATE_ARTIFACT", fmt.Sprintf("artifact %q occurs more than once", artifact.Name))
		}
		if artifact.ID <= 0 || artifact.Expired == nil || *artifact.Expired || artifact.WorkflowRun == nil || artifact.WorkflowRun.ID != options.RunID || artifact.WorkflowRun.HeadSHA != options.SourceSHA {
			return inventoryError("ARTIFACT_IDENTITY_MISMATCH", fmt.Sprintf("artifact %q is expired or does not belong to the exact source run", artifact.Name))
		}
		expected[artifact.Name] = true
	}

	var missing []string
	for name, found := range expected {
		if !found {
			missing = append(missing, name)
		}
	}
	if len(missing) != 0 {
		sort.Strings(missing)
		return inventoryError("ARTIFACT_MATRIX_INCOMPLETE", "missing exact artifacts: "+strings.Join(missing, ","))
	}
	return nil
}

func decodeAPIFile(filename string, destination any) error {
	data, err := os.ReadFile(filename)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("JSON response contains trailing data")
	}
	return nil
}

func inventoryError(reason, detail string) error {
	return fmt.Errorf("%s:%s: %s", InventoryErrorCode, reason, detail)
}
