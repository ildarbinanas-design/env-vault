package releasecontract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCanonicalContract(t *testing.T) {
	contract := loadCanonicalForTest(t)
	if contract.SchemaID != "env-vault.release-contract.v2" || contract.SchemaVersion != 2 || CanonicalPath != "release/contract.v2.json" {
		t.Fatalf("canonical contract identity=%s/%d path=%s", contract.SchemaID, contract.SchemaVersion, CanonicalPath)
	}
	if contract.Evolution.PreviousSchemaID != "env-vault.release-contract.v1" ||
		contract.Evolution.PreviousSchemaVersion != 1 ||
		contract.Evolution.PreviousSemanticSHA256 != "6b83efee82bf8a0d9c1fcc3f491f313dee3dd29f31f0837b27051c7c65e61ef5" {
		t.Fatalf("contract evolution=%+v", contract.Evolution)
	}
	if len(contract.Platforms) != 5 || len(contract.Assets) != 10 {
		t.Fatalf("platforms=%d assets=%d", len(contract.Platforms), len(contract.Assets))
	}
	windows, ok := contract.PlatformByID("windows-amd64")
	if !ok || windows.Binary != "env-vault.exe" || windows.ArchiveFormat != "zip" || windows.Runner != "windows-latest" {
		t.Fatalf("Windows contract=%+v found=%v", windows, ok)
	}
	if _, ok := contract.PlatformByID("linux-386"); ok {
		t.Fatal("undeclared target was found")
	}
	if got := contract.Matrix().Include; len(got) != 5 || got[0].ID != "linux-amd64" {
		t.Fatalf("matrix=%+v", got)
	}
	for _, code := range []string{"rerun_all_jobs", "inspect_failure", "rerun_tap_pr_ci_all_jobs", "dispatch_legacy_rebuild", "mark_release_pr_abandoned"} {
		if !contract.HasActionCode(code) {
			t.Fatalf("required action code %q absent", code)
		}
	}
	for _, code := range []string{"ATTEMPT_MATRIX_INCOMPLETE", "INPUT_INCOMPLETE", "SCHEMA_UNSUPPORTED"} {
		if !contract.HasErrorCode(code) {
			t.Fatalf("required error code %q absent", code)
		}
	}
	if !contains(contract.ReasonCodes, "PRETAG_AUTHORIZATION_MISSING") {
		t.Fatal("recovery reason code is absent")
	}
	if contract.Schemas["source_quality_proof"] != "env-vault.source-quality-proof.v1" {
		t.Fatal("source-quality proof schema is not canonical")
	}
	if contract.Schemas["repository_release_settings_proof"] != "env-vault.repository-release-settings-proof.v1" {
		t.Fatal("repository release-settings proof schema is not canonical")
	}
	if contract.Schemas["repository_release_settings_check"] != "env-vault.repository-release-settings-check.v1" {
		t.Fatal("repository release-settings check schema is not canonical")
	}
	if contract.Schemas["release_please_recovery"] != ReleasePleaseRecoverySchemaID || contract.Schemas["release_please_recovery_check"] != ReleasePleaseRecoveryCheckSchemaID {
		t.Fatal("release-please recovery schemas are not canonical")
	}
	recovery := contract.VersionPolicy.ReleasePleaseRecovery
	if completedReleaseSource013 != "6206b472cda81f7a87656055d8eb6627c26a0fef" {
		t.Fatalf("checker completion pin=%q", completedReleaseSource013)
	}
	if recovery.State != "complete" || recovery.AbandonedVersion != "v0.0.12" || recovery.AbandonedSourceSHA != "a0eb82cb1fc4fa486ff2032d50ddedf6bccdbb8b" ||
		recovery.GeneratedReleasePRNumber != 31 || recovery.GeneratedReleasePRHeadSHA != "c7169946d9c430209928266d95be7629c93d5878" || recovery.ResumeVersion != "v0.0.13" ||
		recovery.PendingLabel != "autorelease: pending" || recovery.AbandonedLabel != "autorelease: abandoned" || recovery.TaggedLabel != "autorelease: tagged" ||
		!recovery.TagMustNotExist || !recovery.GitHubReleaseMustNotExist || recovery.ReasonCode != "PRETAG_AUTHORIZATION_MISSING" || recovery.CompletedReleaseSourceSHA != completedReleaseSource013 {
		t.Fatalf("release-please recovery=%+v", recovery)
	}
	wantChecks := []RequiredCheck{
		{Name: "Analyze (actions)", Workflow: "CodeQL", Event: "dynamic"},
		{Name: "Analyze (go)", Workflow: "CodeQL", Event: "dynamic"},
		{Name: "Dependency review", Workflow: "Dependency review", Event: "pull_request"},
		{Name: "pr-title", Workflow: "pr-title", Event: "pull_request"},
		{Name: "quality-gate", Workflow: "ci", Event: "pull_request"},
	}
	if !reflect.DeepEqual(contract.MainRequiredChecks, wantChecks) {
		t.Fatalf("main required checks=%+v, want %+v", contract.MainRequiredChecks, wantChecks)
	}
	legacy, ok := contract.LegacyVersion("v0.0.7")
	if !ok || legacy.TagSHA != "4fbae380747e75a1f59498adbd76ccf5791e0480" || !legacy.LiteralVersionSupported || contract.VersionPolicy.LegacyRebuild.PublicationEligible {
		t.Fatalf("legacy policy=%+v found=%v", legacy, ok)
	}
	if _, ok := contract.LegacyVersion("v0.0.8"); ok {
		t.Fatal("failed v0.0.8 tag entered legacy rebuild policy")
	}
	if got := contract.VersionPolicy.BlockedVersions; len(got) != 4 ||
		got[0].Version != blockedVersion008 || got[0].TagSHA != blockedTagSHA008 ||
		got[1].Version != blockedVersion009 || got[1].TagSHA != blockedTagSHA009 ||
		got[2].Version != blockedVersion010 || got[2].TagSHA != blockedTagSHA010 ||
		got[3].Version != blockedVersion011 || got[3].TagSHA != blockedTagSHA011 {
		t.Fatalf("blocked failed-tag policy=%+v", got)
	}
}

func TestCanonicalContractOwnsOperationalReleaseIdentities(t *testing.T) {
	contract := loadCanonicalForTest(t)
	if contract.Repositories.Source.FullName != "ildarbinanas-design/env-vault" || contract.Repositories.Source.DefaultBranch != "main" ||
		contract.Repositories.HomebrewTap.FullName != "ildarbinanas-design/homebrew-tap" || contract.Repositories.HomebrewTap.DefaultBranch != "main" {
		t.Fatalf("repositories=%+v", contract.Repositories)
	}
	if contract.VersionPolicy.TagPrefix != "v" || contract.VersionPolicy.ReleasePlease.Component != "env-vault" ||
		contract.VersionPolicy.ReleasePlease.TargetBranch != contract.Repositories.Source.DefaultBranch ||
		contract.VersionPolicy.ReleasePlease.Branch != "release-please--branches--main--components--env-vault" ||
		contract.VersionPolicy.ReleasePlease.ManifestKey != "." ||
		contract.VersionPolicy.ReleasePlease.ConfigPath != "release-please-config.json" ||
		contract.VersionPolicy.ReleasePlease.ManifestPath != ".release-please-manifest.json" {
		t.Fatalf("version policy=%+v", contract.VersionPolicy)
	}
	if contract.Homebrew.FormulaName != "env-vault" || contract.Homebrew.FormulaPath != "Formula/env-vault.rb" ||
		contract.Homebrew.HomepageURLTemplate != "https://github.com/{repository}" ||
		contract.Homebrew.ReleaseDownloadURLTemplate != "https://github.com/{repository}/releases/download/{version}/{asset}" ||
		!reflect.DeepEqual(contract.Homebrew.Platforms, []string{"darwin-arm64", "darwin-amd64", "linux-arm64", "linux-amd64"}) {
		t.Fatalf("homebrew=%+v", contract.Homebrew)
	}
	if contract.Concurrency.Release.Group != "env-vault-release" || contract.Concurrency.Release.CancelInProgress || contract.Concurrency.Release.Queue != "max" ||
		!reflect.DeepEqual(contract.Concurrency.Release.Workflows, []string{"planning", "publisher", "release_assets_bootstrap", "homebrew_bridge", "release_evidence"}) ||
		!contract.Concurrency.CI.CancelInProgress {
		t.Fatalf("concurrency=%+v", contract.Concurrency)
	}
	wantPlatforms := []Platform{
		{ID: "linux-amd64", Runner: "ubuntu-latest", GOOS: "linux", GOARCH: "amd64", CGO: "0", Archive: "env-vault-linux-amd64.tar.gz", Checksum: "env-vault-linux-amd64.tar.gz.sha256", ArchiveFormat: "tar.gz", Binary: "env-vault"},
		{ID: "linux-arm64", Runner: "ubuntu-24.04-arm", GOOS: "linux", GOARCH: "arm64", CGO: "0", Archive: "env-vault-linux-arm64.tar.gz", Checksum: "env-vault-linux-arm64.tar.gz.sha256", ArchiveFormat: "tar.gz", Binary: "env-vault"},
		{ID: "darwin-amd64", Runner: "macos-15-intel", GOOS: "darwin", GOARCH: "amd64", CGO: "1", Archive: "env-vault-darwin-amd64.tar.gz", Checksum: "env-vault-darwin-amd64.tar.gz.sha256", ArchiveFormat: "tar.gz", Binary: "env-vault"},
		{ID: "darwin-arm64", Runner: "macos-15", GOOS: "darwin", GOARCH: "arm64", CGO: "1", Archive: "env-vault-darwin-arm64.tar.gz", Checksum: "env-vault-darwin-arm64.tar.gz.sha256", ArchiveFormat: "tar.gz", Binary: "env-vault"},
		{ID: "windows-amd64", Runner: "windows-latest", GOOS: "windows", GOARCH: "amd64", CGO: "0", Archive: "env-vault-windows-amd64.zip", Checksum: "env-vault-windows-amd64.zip.sha256", ArchiveFormat: "zip", Binary: "env-vault.exe"},
	}
	if !reflect.DeepEqual(contract.Platforms, wantPlatforms) {
		t.Fatalf("platforms=%+v", contract.Platforms)
	}
	wantWorkflows := []Workflow{
		{ID: "ci", Name: "ci", File: "ci.yml", Events: []string{"push", "pull_request", "workflow_dispatch"}, Jobs: []string{"quality", "quality-gate"}},
		{ID: "quality", Name: "reusable-quality", File: "reusable-quality.yml", Events: []string{"workflow_call"}, Jobs: []string{"resolve", "source-quality", "license", "native", "e2e-gate"}},
		{ID: "planning", Name: "release-please", File: "release-please.yml", Events: []string{"workflow_run"}, Jobs: []string{"inspect", "rerun-incomplete-attempt", "plan"}},
		{ID: "publisher", Name: "build-binaries", File: "build-binaries.yml", Events: []string{"workflow_dispatch", "push"}, Jobs: []string{"metadata", "preflight", "promotion", "release", "supply_chain", "homebrew", "health"}},
		{ID: "release_assets_bootstrap", Name: "bootstrap-release-assets", File: "bootstrap-release-assets.yml", Events: []string{"workflow_dispatch"}, Jobs: []string{"bootstrap"}},
		{ID: "homebrew_bridge", Name: "publish-homebrew-bridge", File: "publish-homebrew-bridge.yml", Events: []string{"workflow_dispatch"}, Jobs: []string{"homebrew_bridge"}},
		{ID: "release_evidence", Name: "release-evidence", File: "release-evidence.yml", Events: []string{"workflow_run"}, Jobs: []string{"assemble", "publish"}},
		{ID: "legacy_rebuild", Name: "legacy-rebuild", File: "legacy-rebuild.yml", Events: []string{"workflow_dispatch"}, Jobs: []string{"resolve", "diagnostic"}},
		{ID: "planning_app_audit", Name: "audit-release-planning-app", File: "audit-release-planning-app.yml", Events: []string{"workflow_dispatch"}, Jobs: []string{"scope"}},
		{ID: "tap_app_audit", Name: "audit-release-app", File: "audit-release-app.yml", Events: []string{"workflow_dispatch"}, Jobs: []string{"scope"}},
		{ID: "dependency_review", Name: "Dependency review", File: "dependency-review.yml", Events: []string{"pull_request"}, Jobs: []string{"dependency-review"}},
		{ID: "pr_title", Name: "pr-title", File: "pr-title.yml", Events: []string{"pull_request"}, Jobs: []string{"pr-title"}},
	}
	if !reflect.DeepEqual(contract.Workflows, wantWorkflows) {
		t.Fatalf("workflows=%+v", contract.Workflows)
	}
	wantApps := []App{
		{ID: "release_planning", Slug: "env-vault-release-planning", RepositoryID: "source", Environment: "release-planning", AuditWorkflow: "planning_app_audit"},
		{ID: "homebrew_tap", Slug: "env-vault-tap-release", RepositoryID: "homebrew_tap", Environment: "release", AuditWorkflow: "tap_app_audit", CIWorkflowFile: "test-formula.yml", CIWorkflowName: "test-formula"},
	}
	if !reflect.DeepEqual(contract.Apps, wantApps) {
		t.Fatalf("apps=%+v", contract.Apps)
	}
	wantRepairActions := []RepairAction{
		{ID: "rerun-ci-attempt", ActionCode: "rerun_all_jobs", ResumeStage: "source_quality", Rebuilds: true, PublicationEligible: true},
		{ID: "release-assets", ActionCode: "dispatch_release_assets_repair", ResumeStage: "publication", PublicationEligible: true},
		{ID: "homebrew", ActionCode: "dispatch_homebrew_repair", ResumeStage: "homebrew", PublicationEligible: true},
		{ID: "health", ActionCode: "dispatch_health_repair", ResumeStage: "health", PublicationEligible: true},
		{ID: "legacy-rebuild-diagnostic", ActionCode: "dispatch_legacy_rebuild", ResumeStage: "exact_version_artifact_quality", Rebuilds: true},
	}
	if !reflect.DeepEqual(contract.AllowedRepairActions, wantRepairActions) {
		t.Fatalf("repair actions=%+v", contract.AllowedRepairActions)
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

func TestLoadFileRejectsUnknownDuplicateAndTrailingJSON(t *testing.T) {
	canonical := readCanonicalForTest(t)
	completedLine := `      "completed_release_source_sha": "6206b472cda81f7a87656055d8eb6627c26a0fef",`
	withoutCompleted := []byte(strings.Replace(string(canonical), completedLine+"\n", "", 1))
	tests := map[string][]byte{
		"unknown":           []byte(strings.TrimSuffix(string(canonical), "\n}") + ",\n  \"unknown\": true\n}\n"),
		"case variant":      []byte(strings.Replace(string(canonical), `"schema_id":`, `"Schema_ID":`, 1)),
		"nested variant":    []byte(strings.Replace(string(canonical), `"archive_prefix":`, `"Archive_Prefix":`, 1)),
		"duplicate":         []byte(strings.Replace(string(canonical), `"schema_id": "env-vault.release-contract.v2",`, `"schema_id": "env-vault.release-contract.v2", "schema_id": "env-vault.release-contract.v2",`, 1)),
		"completed missing": withoutCompleted,
		"completed null": []byte(strings.Replace(string(canonical),
			`"completed_release_source_sha": "6206b472cda81f7a87656055d8eb6627c26a0fef"`, `"completed_release_source_sha": null`, 1)),
		"completed wrong": []byte(strings.Replace(string(canonical),
			"6206b472cda81f7a87656055d8eb6627c26a0fef", strings.Repeat("e", 40), 1)),
		"completed duplicate":    []byte(strings.Replace(string(canonical), completedLine, completedLine+"\n"+completedLine, 1)),
		"completed case variant": []byte(strings.Replace(string(canonical), `"completed_release_source_sha"`, `"Completed_Release_Source_SHA"`, 1)),
		"rollback active":        []byte(strings.Replace(string(withoutCompleted), `"state": "complete"`, `"state": "active"`, 1)),
		"trailing":               append(append([]byte(nil), canonical...), []byte("{}")...),
		"repository null": []byte(strings.Replace(string(canonical),
			`"source": {`, `"source": null, "ignored_source": {`, 1)),
		"repository alias": []byte(strings.Replace(string(canonical),
			`"default_branch": "main"`, `"defaultBranch": "main"`, 1)),
		"workflow events null": []byte(strings.Replace(string(canonical),
			`"events": [
        "push",
        "pull_request",
        "workflow_dispatch"
      ]`, `"events": null`, 1)),
		"evolution downgrade": []byte(strings.Replace(string(canonical),
			`"previous_schema_version": 1`, `"previous_schema_version": 0`, 1)),
		"required false null": []byte(strings.Replace(string(canonical),
			`"cancel_in_progress": false`, `"cancel_in_progress": null`, 1)),
		"required true null": []byte(strings.Replace(string(canonical),
			`"cancel_in_progress": true`, `"cancel_in_progress": null`, 1)),
		"required int null": []byte(strings.Replace(string(canonical),
			`"schema_version": 2`, `"schema_version": null`, 1)),
		"required string null": []byte(strings.Replace(string(canonical),
			`"tag_prefix": "v"`, `"tag_prefix": null`, 1)),
		"required array null": []byte(strings.Replace(string(canonical),
			`"platforms": [`, `"platforms": null, "ignored_platforms": [`, 1)),
		"required map null": []byte(strings.Replace(string(canonical),
			`"schemas": {`, `"schemas": null, "ignored_schemas": {`, 1)),
		"missing required false": []byte(strings.Replace(string(canonical),
			`      "cancel_in_progress": false,
`, "", 1)),
		"missing required nested field": []byte(strings.Replace(string(canonical),
			`    "manifest_key": ".",
`, "", 1)),
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadFile(writeTempFile(t, data)); err == nil {
				t.Fatal("invalid JSON was accepted")
			}
		})
	}
}

func TestCanonicalLoaderRejectsArchivedV1Downgrade(t *testing.T) {
	if _, err := LoadFile(filepath.Join("..", "..", "release", "contract.v1.json")); err == nil {
		t.Fatal("archival v1 contract was accepted as the operational contract")
	}
	canonical := readCanonicalForTest(t)
	for name, data := range map[string][]byte{
		"predecessor digest": []byte(strings.Replace(string(canonical),
			"6b83efee82bf8a0d9c1fcc3f491f313dee3dd29f31f0837b27051c7c65e61ef5",
			strings.Repeat("f", 64), 1)),
		"schema id": []byte(strings.Replace(string(canonical),
			"env-vault.release-contract.v2", "env-vault.release-contract.v1", 1)),
		"schema version": []byte(strings.Replace(string(canonical),
			`"schema_version": 2`, `"schema_version": 1`, 1)),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadFile(writeTempFile(t, data)); err == nil {
				t.Fatal("contract downgrade/tamper was accepted")
			}
		})
	}
}

func TestSemanticSHAIgnoresFormattingAndObjectOrder(t *testing.T) {
	contract := loadCanonicalForTest(t)
	want, err := SemanticSHA256(contract)
	if err != nil {
		t.Fatal(err)
	}
	var generic map[string]any
	if err := json.Unmarshal(readCanonicalForTest(t), &generic); err != nil {
		t.Fatal(err)
	}
	reordered, err := json.Marshal(generic)
	if err != nil {
		t.Fatal(err)
	}
	other, err := LoadFile(writeTempFile(t, reordered))
	if err != nil {
		t.Fatal(err)
	}
	got, err := SemanticSHA256(other)
	if err != nil {
		t.Fatal(err)
	}
	if got != want || len(got) != 64 {
		t.Fatalf("semantic hashes got=%q want=%q", got, want)
	}
}

func TestSemanticSHACommitsToOperationalListOrder(t *testing.T) {
	canonical := loadCanonicalForTest(t)
	wantDigest, err := SemanticSHA256(canonical)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("workflows", func(t *testing.T) {
		contract := canonical
		contract.Workflows = append([]Workflow(nil), canonical.Workflows...)
		contract.Workflows[0], contract.Workflows[1] = contract.Workflows[1], contract.Workflows[0]
		if err := contract.Validate(); err != nil {
			t.Fatalf("coordinated workflow reordering should remain structurally valid: %v", err)
		}
		got, err := SemanticSHA256(contract)
		if err != nil {
			t.Fatal(err)
		}
		if got == wantDigest {
			t.Fatal("workflow order did not change the semantic contract digest")
		}
	})

	t.Run("platforms and adjacent assets", func(t *testing.T) {
		contract := canonical
		contract.Platforms = append([]Platform(nil), canonical.Platforms...)
		contract.Assets = append([]string(nil), canonical.Assets...)
		contract.Platforms[0], contract.Platforms[1] = contract.Platforms[1], contract.Platforms[0]
		firstPair := append([]string(nil), contract.Assets[0:2]...)
		copy(contract.Assets[0:2], contract.Assets[2:4])
		copy(contract.Assets[2:4], firstPair)
		if err := contract.Validate(); err != nil {
			t.Fatalf("coordinated platform/asset reordering should remain structurally valid: %v", err)
		}
		got, err := SemanticSHA256(contract)
		if err != nil {
			t.Fatal(err)
		}
		if got == wantDigest {
			t.Fatal("platform/asset order did not change the semantic contract digest")
		}
		matrix := contract.Matrix().Include
		if matrix[0].ID != canonical.Platforms[1].ID || matrix[1].ID != canonical.Platforms[0].ID {
			t.Fatalf("matrix did not preserve contract order: %+v", matrix)
		}
	})
}

func TestValidateRejectsGuaranteeWeakening(t *testing.T) {
	tests := map[string]func(*Contract){
		"asset drift":    func(c *Contract) { c.Assets[0] = "replacement.tar.gz" },
		"invalid target": func(c *Contract) { c.Platforms[1].Runner = "" },
		"failed tag release": func(c *Contract) {
			c.VersionPolicy.BlockedVersions[0].GitHubReleaseMustNotExist = false
		},
		"missing failed tag": func(c *Contract) {
			c.VersionPolicy.BlockedVersions = c.VersionPolicy.BlockedVersions[:1]
		},
		"legacy publication": func(c *Contract) { c.VersionPolicy.LegacyRebuild.PublicationEligible = true },
		"invalid recovery state": func(c *Contract) {
			c.VersionPolicy.ReleasePleaseRecovery.State = "unknown"
		},
		"recovery can tag abandoned": func(c *Contract) {
			c.VersionPolicy.ReleasePleaseRecovery.TagMustNotExist = false
		},
		"recovery can release abandoned": func(c *Contract) {
			c.VersionPolicy.ReleasePleaseRecovery.GitHubReleaseMustNotExist = false
		},
		"recovery PR number drift": func(c *Contract) {
			c.VersionPolicy.ReleasePleaseRecovery.GeneratedReleasePRNumber = 32
		},
		"recovery PR head drift": func(c *Contract) {
			c.VersionPolicy.ReleasePleaseRecovery.GeneratedReleasePRHeadSHA = strings.Repeat("f", 40)
		},
		"wrong completed recovery source": func(c *Contract) {
			c.VersionPolicy.ReleasePleaseRecovery.CompletedReleaseSourceSHA = strings.Repeat("e", 40)
		},
		"rollback recovery to active": func(c *Contract) {
			c.VersionPolicy.ReleasePleaseRecovery.State = "active"
			c.VersionPolicy.ReleasePleaseRecovery.CompletedReleaseSourceSHA = ""
		},
		"artifact naming": func(c *Contract) {
			c.Naming.PlatformArtifactTemplate = "env-vault-release-{platform}"
		},
		"artifact naming control": func(c *Contract) {
			c.Naming.PlatformArtifactTemplate = "env-vault-\n{platform}-{attempt}"
		},
		"duplicate repair":   func(c *Contract) { c.AllowedRepairActions[1].ID = c.AllowedRepairActions[0].ID },
		"repair rebuild":     func(c *Contract) { c.AllowedRepairActions[1].Rebuilds = true },
		"legacy can publish": func(c *Contract) { c.AllowedRepairActions[4].PublicationEligible = true },
		"mutating stage weakened": func(c *Contract) {
			c.ReleaseStages[2].StateMutating = false
		},
		"action code separator": func(c *Contract) { c.ActionCodes[2] = "rerun-all-jobs" },
		"missing recovery action": func(c *Contract) {
			c.ActionCodes = c.ActionCodes[:len(c.ActionCodes)-1]
		},
		"app slug underscore": func(c *Contract) { c.Apps[0].Slug = "env_vault_release_planning" },
		"missing reason":      func(c *Contract) { c.ReasonCodes = c.ReasonCodes[1:] },
		"missing recovery reason": func(c *Contract) {
			c.ReasonCodes = c.ReasonCodes[:len(c.ReasonCodes)-1]
		},
		"missing required check": func(c *Contract) {
			c.MainRequiredChecks = c.MainRequiredChecks[:len(c.MainRequiredChecks)-1]
		},
		"invalid required check event": func(c *Contract) {
			c.MainRequiredChecks[0].Event = "schedule"
		},
		"duplicate required check": func(c *Contract) {
			c.MainRequiredChecks[1] = c.MainRequiredChecks[0]
		},
		"reordered target": func(c *Contract) {
			c.Platforms[0], c.Platforms[1] = c.Platforms[1], c.Platforms[0]
		},
		"duplicate target": func(c *Contract) { c.Platforms[1] = c.Platforms[0] },
		"reordered asset": func(c *Contract) {
			c.Assets[0], c.Assets[1] = c.Assets[1], c.Assets[0]
		},
		"duplicate asset":    func(c *Contract) { c.Assets[1] = c.Assets[0] },
		"duplicate workflow": func(c *Contract) { c.Workflows[1] = c.Workflows[0] },
		"invalid source repository": func(c *Contract) {
			c.Repositories.Source.FullName = "env-vault"
		},
		"source default branch":     func(c *Contract) { c.Repositories.Source.DefaultBranch = "trunk" },
		"same tap repository":       func(c *Contract) { c.Repositories.HomebrewTap = c.Repositories.Source },
		"app repository id":         func(c *Contract) { c.Apps[0].RepositoryID = "homebrew_tap" },
		"formula path":              func(c *Contract) { c.Homebrew.FormulaPath = "Formula/other.rb" },
		"release please branch":     func(c *Contract) { c.VersionPolicy.ReleasePlease.Branch = "release/other" },
		"tag prefix":                func(c *Contract) { c.VersionPolicy.TagPrefix = "release-" },
		"duplicate workflow event":  func(c *Contract) { c.Workflows[0].Events[1] = c.Workflows[0].Events[0] },
		"duplicate workflow job":    func(c *Contract) { c.Workflows[0].Jobs[1] = c.Workflows[0].Jobs[0] },
		"missing release workflow":  func(c *Contract) { c.Concurrency.Release.Workflows = c.Concurrency.Release.Workflows[:2] },
		"missing Homebrew platform": func(c *Contract) { c.Homebrew.Platforms = c.Homebrew.Platforms[:3] },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			contract := loadCanonicalForTest(t)
			mutate(&contract)
			if err := contract.Validate(); err == nil {
				t.Fatal("weakened contract was accepted")
			}
		})
	}
}

func TestRenderName(t *testing.T) {
	contract := loadCanonicalForTest(t)
	got, err := contract.RenderName(contract.Naming.PlatformArtifactTemplate, map[string]string{
		"platform": "windows-amd64",
		"attempt":  "12",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != "env-vault-release-windows-amd64-attempt-12" {
		t.Fatalf("rendered name=%q", got)
	}
	manifest, err := contract.RenderName(contract.Naming.PromotionManifestTemplate, map[string]string{
		"source_sha": strings.Repeat("a", 40),
		"attempt":    "2",
	})
	if err != nil || manifest != "env-vault-promotion-"+strings.Repeat("a", 40)+"-attempt-2" {
		t.Fatalf("manifest=%q err=%v", manifest, err)
	}
	for name, values := range map[string]map[string]string{
		"path":             {"platform": "../windows-amd64", "attempt": "1"},
		"zero attempt":     {"platform": "windows-amd64", "attempt": "0"},
		"unknown platform": {"platform": "linux-386", "attempt": "1"},
		"extra value":      {"platform": "windows-amd64", "attempt": "1", "source_sha": strings.Repeat("a", 40)},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := contract.RenderName(contract.Naming.PlatformArtifactTemplate, values); err == nil {
				t.Fatal("invalid name input was accepted")
			}
		})
	}
	if _, err := RenderName("prefix-{unknown}", map[string]string{"unknown": "value"}); err == nil {
		t.Fatal("unknown placeholder was accepted")
	}
}

func loadCanonicalForTest(t *testing.T) Contract {
	t.Helper()
	contract, err := LoadCanonical(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return contract
}

func readCanonicalForTest(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(CanonicalPath)))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func writeTempFile(t *testing.T, data []byte) string {
	t.Helper()
	filename := filepath.Join(t.TempDir(), "input.json")
	if err := os.WriteFile(filename, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return filename
}
