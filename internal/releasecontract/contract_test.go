package releasecontract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCanonicalContract(t *testing.T) {
	contract, err := LoadCanonical(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if len(contract.Platforms) != 5 || len(contract.Assets) != 10 {
		t.Fatalf("platforms=%d assets=%d", len(contract.Platforms), len(contract.Assets))
	}
	if got := contract.Matrix().Include[4]; got.ID != "windows-amd64" || got.Binary != "env-vault.exe" || got.ArchiveFormat != "zip" {
		t.Fatalf("Windows matrix entry=%+v", got)
	}
	if len(contract.Capabilities.Observation) == 0 || len(contract.Capabilities.Mutation) == 0 {
		t.Fatal("observation and mutation capabilities must both be explicit")
	}
	for _, code := range []string{
		"dispatch_release_assets_repair", "dispatch_homebrew_repair", "dispatch_health_repair",
		"dispatch_legacy_rebuild",
	} {
		if !contains(contract.ActionCodes, code) {
			t.Fatalf("required operator action code %q is absent", code)
		}
	}
	if contract.Schemas["promotion_platform"] != "env-vault.promotion-platform.v1" || contract.Schemas["legacy_rebuild_plan"] != "env-vault.legacy-rebuild-plan.v1" {
		t.Fatal("operator and promotion protocol codes must come from the release contract")
	}
	for _, capability := range []string{"release.repair.apply", "release.legacy_rebuild.apply"} {
		if !containsMutationCapability(contract.Capabilities.Mutation, capability) {
			t.Fatalf("mutation capability %q must be explicit", capability)
		}
	}
	for _, capability := range []string{
		"release.plan", "release.status", "release.watch", "release.verify", "release.metrics",
		"release.repair.plan", "release.legacy_rebuild.plan",
	} {
		if !containsObservationCapability(contract.Capabilities.Observation, capability) {
			t.Fatalf("GET-only observation capability %q must be explicit", capability)
		}
	}
	for _, code := range []string{"REPOSITORY_NOT_ACCESSIBLE", "ATTESTATION_VERIFICATION_FAILED"} {
		if !contains(contract.ErrorCodes, code) {
			t.Fatalf("operator error code %q is absent", code)
		}
	}
	if tap, ok := contract.AppByID("homebrew_tap"); !ok || tap.CIWorkflowFile != "test-formula.yml" || tap.CIWorkflowName != "test-formula" {
		t.Fatalf("Homebrew tap CI identity=%+v found=%v", tap, ok)
	}
	if planning, ok := contract.AppByID("release_planning"); !ok || planning.AuditWorkflow != "planning_app_audit" {
		t.Fatalf("release planning audit workflow identity=%+v found=%v", planning, ok)
	}
	if evidence, ok := contract.WorkflowByID("release_evidence"); !ok || evidence.File != "release-evidence.yml" || evidence.Name != "release-evidence" {
		t.Fatalf("release evidence workflow identity=%+v found=%v", evidence, ok)
	}
	if publisher, ok := contract.WorkflowByID("publisher"); !ok || publisher.Name != "build-binaries" || publisher.File != "build-binaries.yml" {
		t.Fatalf("publisher workflow identity=%+v found=%v", publisher, ok)
	}
	if contract.RepairModes[0].Rebuilds || contract.RepairModes[1].Rebuilds {
		t.Fatal("steady-state none/release-assets promotion must not rebuild")
	}
	legacy, ok := contract.LegacyVersion("v0.0.7")
	if !ok || legacy.TagSHA != "4fbae380747e75a1f59498adbd76ccf5791e0480" || !legacy.LiteralVersionSupported || contract.VersionPolicy.LegacyRebuild.GoVersion != "1.22.12" || contract.VersionPolicy.LegacyRebuild.PublicationEligible {
		t.Fatalf("legacy v0.0.7 policy=%+v found=%v", legacy, ok)
	}
	if _, ok := contract.LegacyVersion("v0.0.8"); ok {
		t.Fatal("v0.0.8 must not enter the legacy rebuild path")
	}
}

func TestIsVersionUsesStrictCanonicalPolicy(t *testing.T) {
	for _, value := range []string{"v0.0.9", "v1.20.300"} {
		if !IsVersion(value) {
			t.Fatalf("canonical version %q rejected", value)
		}
	}
	for _, value := range []string{"0.0.9", "v01.2.3", "v1.2", "v1.2.3-rc.1"} {
		if IsVersion(value) {
			t.Fatalf("non-canonical version %q accepted", value)
		}
	}
}

func containsMutationCapability(capabilities []MutationCapability, wanted string) bool {
	for _, capability := range capabilities {
		if capability.ID == wanted {
			return true
		}
	}
	return false
}

func containsObservationCapability(capabilities []ObservationCapability, wanted string) bool {
	for _, capability := range capabilities {
		if capability.ID == wanted && capability.Transport == "github_api" && len(capability.HTTPMethods) == 1 && capability.HTTPMethods[0] == "GET" {
			return true
		}
	}
	return false
}

func TestLoadFileRejectsUnknownAndWeakenedMutationFields(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(CanonicalPath)))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	write := func(value any) string {
		t.Helper()
		filename := filepath.Join(t.TempDir(), "contract.json")
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, encoded, 0o600); err != nil {
			t.Fatal(err)
		}
		return filename
	}

	document["unknown"] = true
	if _, err := LoadFile(write(document)); err == nil {
		t.Fatal("unknown field was accepted")
	}
	delete(document, "unknown")
	capabilities := document["capabilities"].(map[string]any)
	mutations := capabilities["mutation"].([]any)
	mutations[0].(map[string]any)["dry_run_default"] = false
	if _, err := LoadFile(write(document)); err == nil {
		t.Fatal("mutation without dry-run default was accepted")
	}
}

func TestValidateRejectsAssetDriftAndBlockedVersionChange(t *testing.T) {
	contract, err := LoadCanonical(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	contract.Assets[0] = "replacement.tar.gz"
	if err := contract.Validate(); err == nil {
		t.Fatal("asset drift was accepted")
	}
	contract, _ = LoadCanonical(filepath.Join("..", ".."))
	contract.VersionPolicy.BlockedVersions[0].GitHubReleaseMustNotExist = false
	if err := contract.Validate(); err == nil {
		t.Fatal("v0.0.8 GitHub Release was permitted")
	}
	contract, _ = LoadCanonical(filepath.Join("..", ".."))
	contract.VersionPolicy.LegacyRebuild.PublicationEligible = true
	if err := contract.Validate(); err == nil {
		t.Fatal("legacy diagnostic artifacts were made publication eligible")
	}
	contract, _ = LoadCanonical(filepath.Join("..", ".."))
	for index := range contract.Apps {
		if contract.Apps[index].ID == "homebrew_tap" {
			contract.Apps[index].CIWorkflowFile = ""
		}
	}
	if err := contract.Validate(); err == nil {
		t.Fatal("Homebrew tap App without exact CI workflow identity was accepted")
	}
}
