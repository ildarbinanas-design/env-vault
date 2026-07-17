package releasecontract

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckReleasePleaseRecoveryComplete(t *testing.T) {
	contract := loadCanonicalForTest(t)
	config, manifest := readRecoveryInputs(t)
	check, err := CheckReleasePleaseRecovery(contract, config, manifest)
	if err != nil {
		t.Fatal(err)
	}
	recovery := contract.VersionPolicy.ReleasePleaseRecovery
	if !check.OK || check.SchemaID != ReleasePleaseRecoveryCheckSchemaID || check.SchemaVersion != 1 || check.State != "complete" {
		t.Fatalf("check=%+v", check)
	}
	if check.AbandonedVersion != recovery.AbandonedVersion || check.AbandonedSourceSHA != recovery.AbandonedSourceSHA ||
		check.GeneratedReleasePRNumber != recovery.GeneratedReleasePRNumber || check.GeneratedReleasePRHeadSHA != recovery.GeneratedReleasePRHeadSHA ||
		check.ResumeVersion != recovery.ResumeVersion || check.PendingLabel != recovery.PendingLabel || check.AbandonedLabel != recovery.AbandonedLabel ||
		check.TaggedLabel != recovery.TaggedLabel || !check.TagMustNotExist || !check.GitHubReleaseMustNotExist || check.ReasonCode != "PRETAG_AUTHORIZATION_MISSING" ||
		len(check.SemanticContractSHA256) != 64 || check.CompletedReleaseSourceSHA != completedReleaseSource013 {
		t.Fatalf("incident identity=%+v", check)
	}
}

func TestCheckReleasePleaseRecoveryRejectsRollbackToActive(t *testing.T) {
	contract := loadCanonicalForTest(t)
	contract.VersionPolicy.ReleasePleaseRecovery.State = "active"
	contract.VersionPolicy.ReleasePleaseRecovery.CompletedReleaseSourceSHA = ""
	config, _ := readRecoveryInputs(t)
	config = addLastReleaseSHA(config, contract.VersionPolicy.ReleasePleaseRecovery.AbandonedSourceSHA)
	if _, err := CheckReleasePleaseRecovery(contract, config, []byte(`{".":"0.0.13"}`)); err == nil || !strings.Contains(err.Error(), "active recovery is forbidden") {
		t.Fatalf("active rollback with a current manifest was not rejected: %v", err)
	}
}

func TestRecoveryVersionFloor(t *testing.T) {
	for _, version := range []string{"0.0.13", "0.0.14", "0.1.0", "1.0.0", "100000000000000000000.0.0"} {
		if !versionAtLeast(version, "v0.0.13") {
			t.Fatalf("version %s should satisfy recovery floor", version)
		}
	}
	for _, version := range []string{"0.0.12", "00.0.13", "0.0.13-rc.1"} {
		if versionAtLeast(version, "v0.0.13") {
			t.Fatalf("version %s unexpectedly satisfies recovery floor", version)
		}
	}
}

func TestCheckReleasePleaseRecoveryRejectsAdversarialCompleteConfig(t *testing.T) {
	contract := loadCanonicalForTest(t)
	config, manifest := readRecoveryInputs(t)
	abandonedSHA := contract.VersionPolicy.ReleasePleaseRecovery.AbandonedSourceSHA
	schemaLine := `  "$schema": "https://raw.githubusercontent.com/googleapis/release-please/v17.6.0/schemas/config.json",`
	separateLine := `  "separate-pull-requests": true,`
	tests := map[string][]byte{
		"duplicate":            []byte(strings.Replace(string(config), separateLine, separateLine+"\n"+separateLine, 1)),
		"case variant":         []byte(strings.Replace(string(config), `"separate-pull-requests"`, `"Separate-Pull-Requests"`, 1)),
		"unknown":              []byte(strings.Replace(string(config), separateLine, "  \"unknown\": true,\n"+separateLine, 1)),
		"returned exact pin":   addLastReleaseSHA(config, abandonedSHA),
		"returned wrong pin":   addLastReleaseSHA(config, strings.Repeat("f", 40)),
		"returned empty pin":   addLastReleaseSHARaw(config, `""`),
		"returned null pin":    addLastReleaseSHARaw(config, `null`),
		"case variant pin":     []byte(strings.Replace(string(addLastReleaseSHA(config, abandonedSHA)), `"last-release-sha"`, `"Last-Release-SHA"`, 1)),
		"release as root":      []byte(strings.Replace(string(config), schemaLine, schemaLine+"\n  \"release-as\": \"0.0.14\",", 1)),
		"release as package":   []byte(strings.Replace(string(config), `"release-type": "go",`, "\"release-type\": \"go\",\n      \"release-as\": \"0.0.14\",", 1)),
		"missing stable field": []byte(strings.Replace(string(config), separateLine+"\n", "", 1)),
		"null false control":   []byte(strings.Replace(string(config), `"include-component-in-tag": false`, `"include-component-in-tag": null`, 1)),
	}
	for name, candidate := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := CheckReleasePleaseRecovery(contract, candidate, manifest); err == nil {
				t.Fatal("adversarial complete config was accepted")
			}
		})
	}
}

func TestCheckReleasePleaseRecoveryRejectsAdversarialCompleteManifest(t *testing.T) {
	contract := loadCanonicalForTest(t)
	config, _ := readRecoveryInputs(t)
	tests := map[string][]byte{
		"duplicate":    []byte(`{".":"0.0.13",".":"0.0.13"}`),
		"case variant": []byte(`{"Root":"0.0.13"}`),
		"unknown":      []byte(`{".":"0.0.13","other":"0.0.13"}`),
		"missing":      []byte(`{}`),
		"null":         []byte(`{".":null}`),
		"below floor":  []byte(`{".":"0.0.12"}`),
		"leading zero": []byte(`{".":"00.0.13"}`),
		"prerelease":   []byte(`{".":"0.0.13-rc.1"}`),
		"wrong type":   []byte(`{".":13}`),
	}
	for name, manifest := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := CheckReleasePleaseRecovery(contract, config, manifest); err == nil {
				t.Fatal("adversarial complete manifest was accepted")
			}
		})
	}
}

func TestCheckReleasePleaseRecoveryAcceptsManifestAboveCompletedFloor(t *testing.T) {
	contract := loadCanonicalForTest(t)
	config, _ := readRecoveryInputs(t)
	for _, version := range []string{"0.0.14", "0.1.0", "1.0.0"} {
		if _, err := CheckReleasePleaseRecovery(contract, config, []byte(`{".":"`+version+`"}`)); err != nil {
			t.Fatalf("manifest version %s was rejected: %v", version, err)
		}
	}
}

func TestReleasePleaseRecoveryCompleteRequiresExactPinnedSource(t *testing.T) {
	for name, completed := range map[string]string{
		"missing":   "",
		"malformed": "ABC",
		"wrong":     strings.Repeat("e", 40),
		"abandoned": "a0eb82cb1fc4fa486ff2032d50ddedf6bccdbb8b",
		"proposal":  "c7169946d9c430209928266d95be7629c93d5878",
	} {
		t.Run(name, func(t *testing.T) {
			contract := loadCanonicalForTest(t)
			contract.VersionPolicy.ReleasePleaseRecovery.CompletedReleaseSourceSHA = completed
			if err := contract.Validate(); err == nil {
				t.Fatal("invalid completed recovery was accepted")
			}
		})
	}
}

func readRecoveryInputs(t *testing.T) ([]byte, []byte) {
	t.Helper()
	root := filepath.Join("..", "..")
	config, err := os.ReadFile(filepath.Join(root, "release-please-config.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(filepath.Join(root, ".release-please-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	return config, manifest
}

func addLastReleaseSHA(config []byte, sha string) []byte {
	return addLastReleaseSHARaw(config, `"`+sha+`"`)
}

func addLastReleaseSHARaw(config []byte, value string) []byte {
	needle := `  "separate-pull-requests": true,`
	replacement := `  "last-release-sha": ` + value + ",\n" + needle
	return []byte(strings.Replace(string(config), needle, replacement, 1))
}
