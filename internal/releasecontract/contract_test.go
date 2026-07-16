package releasecontract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCanonicalContract(t *testing.T) {
	contract := loadCanonicalForTest(t)
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
	for _, code := range []string{"rerun_all_jobs", "inspect_failure", "rerun_tap_pr_ci_all_jobs", "dispatch_legacy_rebuild"} {
		if !contract.HasActionCode(code) {
			t.Fatalf("required action code %q absent", code)
		}
	}
	for _, code := range []string{"ATTEMPT_MATRIX_INCOMPLETE", "INPUT_INCOMPLETE", "SCHEMA_UNSUPPORTED"} {
		if !contract.HasErrorCode(code) {
			t.Fatalf("required error code %q absent", code)
		}
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
	tests := map[string][]byte{
		"unknown":        []byte(strings.TrimSuffix(string(canonical), "\n}") + ",\n  \"unknown\": true\n}\n"),
		"case variant":   []byte(strings.Replace(string(canonical), `"schema_id":`, `"Schema_ID":`, 1)),
		"nested variant": []byte(strings.Replace(string(canonical), `"archive_prefix":`, `"Archive_Prefix":`, 1)),
		"duplicate":      []byte(strings.Replace(string(canonical), `"schema_id": "env-vault.release-contract.v1",`, `"schema_id": "env-vault.release-contract.v1", "schema_id": "env-vault.release-contract.v1",`, 1)),
		"trailing":       append(append([]byte(nil), canonical...), []byte("{}")...),
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadFile(writeTempFile(t, data)); err == nil {
				t.Fatal("invalid JSON was accepted")
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

func TestValidateRejectsGuaranteeWeakening(t *testing.T) {
	tests := map[string]func(*Contract){
		"asset drift":  func(c *Contract) { c.Assets[0] = "replacement.tar.gz" },
		"target drift": func(c *Contract) { c.Platforms[1].Runner = "ubuntu-latest" },
		"failed tag release": func(c *Contract) {
			c.VersionPolicy.BlockedVersions[0].GitHubReleaseMustNotExist = false
		},
		"missing failed tag": func(c *Contract) {
			c.VersionPolicy.BlockedVersions = c.VersionPolicy.BlockedVersions[:1]
		},
		"legacy publication": func(c *Contract) { c.VersionPolicy.LegacyRebuild.PublicationEligible = true },
		"artifact naming": func(c *Contract) {
			c.Naming.PlatformArtifactTemplate = "env-vault-release-{platform}"
		},
		"repair rebuild":        func(c *Contract) { c.AllowedRepairActions[1].Rebuilds = true },
		"action code separator": func(c *Contract) { c.ActionCodes[2] = "rerun-all-jobs" },
		"app slug underscore":   func(c *Contract) { c.Apps[0].Slug = "env_vault_release_planning" },
		"missing reason":        func(c *Contract) { c.ReasonCodes = c.ReasonCodes[1:] },
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
