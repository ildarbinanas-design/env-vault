package actionsartifact

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCanonicalPolicyMatchesCompleteWorkflowSurface(t *testing.T) {
	policy := loadCanonicalPolicy(t)
	workflowDirectory := repositoryPath(t, ".github", "workflows")
	first, err := ValidateWorkflowDirectory(policy, workflowDirectory)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ValidateWorkflowDirectory(policy, workflowDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("validation is not deterministic:\nfirst=%+v\nsecond=%+v", first, second)
	}
	if !first.OK || first.SchemaID != ValidationSchemaID || first.SchemaVersion != ValidationSchemaVersion {
		t.Fatalf("validation identity=%+v", first)
	}
	if first.UploadSiteCount != 23 || first.WorkflowCount != 7 || first.ClassCount != 18 || len(first.PolicySHA256) != 64 {
		t.Fatalf("validation summary=%+v", first)
	}
	wantTiers := []RetentionTierCount{{Days: 7, SiteCount: 3}, {Days: 14, SiteCount: 10}, {Days: 30, SiteCount: 5}, {Days: 90, SiteCount: 5}}
	if !reflect.DeepEqual(first.RetentionTiers, wantTiers) {
		t.Fatalf("retention tiers=%v want=%v", first.RetentionTiers, wantTiers)
	}
	if len(first.ValidatedSiteKeys) != ExpectedUploadSiteCount || first.ValidatedSiteKeys[0] != "release-assets-bootstrap" || first.ValidatedSiteKeys[len(first.ValidatedSiteKeys)-1] != "e2e-reporter-windows-amd64" {
		t.Fatalf("validated keys=%v", first.ValidatedSiteKeys)
	}
}

func TestCanonicalPolicyConsumerGraphIsSemanticallyComplete(t *testing.T) {
	policy := loadCanonicalPolicy(t)
	want := map[string][]string{
		"release-assets-bootstrap":   {"operator.repair-audit", "publish-homebrew-bridge.homebrew-bridge"},
		"release-observation":        {"operator.release-audit", "release-evidence.assemble"},
		"operational-contract":       {"build-binaries.health", "build-binaries.homebrew", "build-binaries.promotion", "build-binaries.release", "build-binaries.supply-chain"},
		"publisher-bundle":           {"bootstrap-release-assets.bootstrap", "build-binaries.release", "operator.repair-audit"},
		"spdx-sbom":                  {"operator.supply-chain-audit"},
		"legacy-diagnostic":          {"operator.legacy-diagnostic-audit"},
		"homebrew-bridge":            {"operator.homebrew-dispatch-audit", "operator.repair-audit"},
		"evidence-candidate":         {"release-evidence.publish"},
		"release-evidence-v2":        {"offline-replay.audit"},
		"release-evidence-v1":        {"offline-replay.audit"},
		"attempt-classification":     {"operator.release-planning-audit", "operator.rerun-audit"},
		"release-settings":           {"build-binaries.health", "build-binaries.metadata", "operator.release-audit", "release-evidence.assemble"},
		"abandoned-release-policy":   {"operator.release-planning-audit"},
		"e2e-baseline":               {"operator.quality-audit"},
		"promotion-manifest":         {"bootstrap-release-assets.bootstrap", "build-binaries.promotion", "release-evidence.assemble", "release-please.plan"},
		"e2e-candidate":              {"operator.quality-audit", "reusable-quality.e2e-gate"},
		"promotion-platform":         {"bootstrap-release-assets.bootstrap", "build-binaries.promotion", "release-evidence.assemble", "release-please.inspect", "release-please.plan", "reusable-quality.e2e-gate"},
		"native-release":             {"bootstrap-release-assets.bootstrap", "build-binaries.promotion", "release-evidence.assemble", "release-please.inspect", "release-please.plan"},
		"e2e-reporter-darwin-amd64":  {"reusable-quality.native"},
		"e2e-reporter-darwin-arm64":  {"reusable-quality.native"},
		"e2e-reporter-linux-amd64":   {"reusable-quality.native"},
		"e2e-reporter-linux-arm64":   {"reusable-quality.native"},
		"e2e-reporter-windows-amd64": {"reusable-quality.native"},
	}
	if len(want) != ExpectedUploadSiteCount {
		t.Fatalf("test consumer graph has %d sites", len(want))
	}
	for _, site := range policy.Sites {
		consumers, ok := want[site.ID]
		if !ok || !reflect.DeepEqual(site.Consumers, consumers) {
			t.Fatalf("site %q consumers=%v want=%v", site.ID, site.Consumers, consumers)
		}
		delete(want, site.ID)
	}
	if len(want) != 0 {
		t.Fatalf("policy is missing consumer graph sites %v", want)
	}
}

func TestPolicyFailsClosedOnUnknownDuplicateAndNonCanonicalEntries(t *testing.T) {
	canonical := loadCanonicalPolicy(t)
	tests := []struct {
		name    string
		mutate  func(*Policy)
		message string
	}{
		{
			name: "unknown workflow",
			mutate: func(policy *Policy) {
				policy.Sites[0].Workflow = "unknown.yml"
			},
			message: "unknown workflow",
		},
		{
			name: "unknown class",
			mutate: func(policy *Policy) {
				policy.Sites[0].Class = "unknown-class"
			},
			message: "unknown artifact class",
		},
		{
			name: "duplicate policy key",
			mutate: func(policy *Policy) {
				policy.Sites[1].ID = policy.Sites[0].ID
			},
			message: "duplicate policy key",
		},
		{
			name: "duplicate upload site",
			mutate: func(policy *Policy) {
				policy.Sites[1].Workflow = policy.Sites[0].Workflow
				policy.Sites[1].Job = policy.Sites[0].Job
				policy.Sites[1].Step = policy.Sites[0].Step
			},
			message: "duplicate upload site",
		},
		{
			name: "unsupported retention",
			mutate: func(policy *Policy) {
				policy.Sites[0].RetentionDays = 8
			},
			message: "unsupported retention_days",
		},
		{
			name: "not attempt qualified",
			mutate: func(policy *Policy) {
				policy.Sites[0].ArtifactName = strings.ReplaceAll(policy.Sites[0].ArtifactName, "-attempt-"+RunAttemptExpression, "")
			},
			message: "current run-attempt expression",
		},
		{
			name: "noncanonical site order",
			mutate: func(policy *Policy) {
				policy.Sites[0], policy.Sites[1] = policy.Sites[1], policy.Sites[0]
			},
			message: "canonical workflow/job/step/id order",
		},
		{
			name: "unsorted consumers",
			mutate: func(policy *Policy) {
				policy.Sites[3].Consumers = []string{"publisher.release", "bootstrap-release-assets.repair"}
			},
			message: "consumers must be sorted",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy := clonePolicy(t, canonical)
			test.mutate(&policy)
			err := policy.Validate()
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("error=%v want substring %q", err, test.message)
			}
		})
	}
}

func TestPolicyDuplicateEntryFailsBeforeCountDrift(t *testing.T) {
	policy := loadCanonicalPolicy(t)
	policy.Sites = append(policy.Sites, clonePolicySite(policy.Sites[0]))
	err := policy.Validate()
	if err == nil || !strings.Contains(err.Error(), "duplicate policy key") {
		t.Fatalf("error=%v", err)
	}
}

func TestWorkflowValidationFailsClosedOnSourceDrift(t *testing.T) {
	canonical := loadCanonicalPolicy(t)
	tests := []struct {
		name    string
		mutate  func(*testing.T, string)
		message string
	}{
		{
			name: "retention mismatch",
			mutate: func(t *testing.T, directory string) {
				mutateWorkflow(t, directory, "legacy-rebuild.yml", "retention-days: 7", "retention-days: 14")
			},
			message: "retention is 14 days, want 7",
		},
		{
			name: "artifact name drift",
			mutate: func(t *testing.T, directory string) {
				mutateWorkflow(t, directory, "legacy-rebuild.yml", "env-vault-legacy-diagnostic-", "env-vault-renamed-legacy-diagnostic-")
			},
			message: "artifact name drifted",
		},
		{
			name: "unknown upload site",
			mutate: func(t *testing.T, directory string) {
				mutateWorkflow(t, directory, "legacy-rebuild.yml", "      - name: Upload diagnostic-only result", "      - name: Upload unregistered diagnostic\n        uses: "+SupportedUploadAction+"\n        with:\n          name: env-vault-unregistered-attempt-${{ github.run_attempt }}\n          path: diagnostic\n          retention-days: 7\n\n      - name: Upload diagnostic-only result")
			},
			message: "unknown upload site",
		},
		{
			name: "unknown upload workflow",
			mutate: func(t *testing.T, directory string) {
				data := []byte("name: unknown\njobs:\n  observe:\n    runs-on: ubuntu-latest\n    steps:\n      - name: Upload unknown artifact\n        uses: " + SupportedUploadAction + "\n        with:\n          name: env-vault-unknown-attempt-${{ github.run_attempt }}\n          path: result.json\n          retention-days: 7\n")
				if err := os.WriteFile(filepath.Join(directory, "unknown.yml"), data, 0o600); err != nil {
					t.Fatal(err)
				}
			},
			message: "unknown upload workflow",
		},
		{
			name: "nonliteral retention",
			mutate: func(t *testing.T, directory string) {
				mutateWorkflow(t, directory, "legacy-rebuild.yml", "retention-days: 7", "retention-days: ${{ inputs.retention_days }}")
			},
			message: "unsupported literal retention-days",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := copyWorkflowDirectory(t)
			test.mutate(t, directory)
			_, err := ValidateWorkflowDirectory(canonical, directory)
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("error=%v want substring %q", err, test.message)
			}
		})
	}
}

func TestLoadPolicyRejectsUnknownAndDuplicateJSONFields(t *testing.T) {
	canonical, err := os.ReadFile(repositoryPath(t, CanonicalPolicyPath))
	if err != nil {
		t.Fatal(err)
	}
	tests := [][]byte{
		[]byte(strings.Replace(string(canonical), `"schema_id":`, `"unknown":true,"schema_id":`, 1)),
		[]byte(strings.Replace(string(canonical), `"schema_id":`, `"Schema_ID":"env-vault.actions-artifact-policy.v1","schema_id":`, 1)),
	}
	for index, data := range tests {
		filename := filepath.Join(t.TempDir(), "policy.json")
		if err := os.WriteFile(filename, data, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadPolicyFile(filename); err == nil {
			t.Fatalf("case %d unexpectedly accepted", index)
		}
	}
}

func loadCanonicalPolicy(t *testing.T) Policy {
	t.Helper()
	policy, err := LoadPolicyFile(repositoryPath(t, CanonicalPolicyPath))
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

func clonePolicy(t *testing.T, policy Policy) Policy {
	t.Helper()
	data, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	var clone Policy
	if err := json.Unmarshal(data, &clone); err != nil {
		t.Fatal(err)
	}
	return clone
}

func clonePolicySite(site PolicySite) PolicySite {
	clone := site
	clone.Consumers = append([]string(nil), site.Consumers...)
	return clone
}

func repositoryPath(t *testing.T, elements ...string) string {
	t.Helper()
	parts := append([]string{"..", ".."}, elements...)
	path, err := filepath.Abs(filepath.Join(parts...))
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func copyWorkflowDirectory(t *testing.T) string {
	t.Helper()
	source := repositoryPath(t, ".github", "workflows")
	destination := filepath.Join(t.TempDir(), "workflows")
	if err := os.Mkdir(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(source)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(source, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(destination, entry.Name()), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return destination
}

func mutateWorkflow(t *testing.T, directory, name, old, replacement string) {
	t.Helper()
	filename := filepath.Join(directory, name)
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), old) != 1 {
		t.Fatalf("workflow %s occurrence count for %q is %d", name, old, strings.Count(string(data), old))
	}
	data = []byte(strings.Replace(string(data), old, replacement, 1))
	if err := os.WriteFile(filename, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
