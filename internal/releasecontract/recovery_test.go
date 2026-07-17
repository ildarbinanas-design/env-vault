package releasecontract

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckReleasePleaseRecoveryActive(t *testing.T) {
	contract := loadCanonicalForTest(t)
	config, _ := readRecoveryInputs(t)
	manifest := []byte(`{".":"0.0.12"}`)
	check, err := CheckReleasePleaseRecovery(contract, config, manifest)
	if err != nil {
		t.Fatal(err)
	}
	recovery := contract.VersionPolicy.ReleasePleaseRecovery
	if !check.OK || check.SchemaID != ReleasePleaseRecoveryCheckSchemaID || check.SchemaVersion != 1 || check.State != "active" {
		t.Fatalf("check=%+v", check)
	}
	if check.AbandonedVersion != recovery.AbandonedVersion || check.AbandonedSourceSHA != recovery.AbandonedSourceSHA ||
		check.GeneratedReleasePRNumber != recovery.GeneratedReleasePRNumber || check.GeneratedReleasePRHeadSHA != recovery.GeneratedReleasePRHeadSHA ||
		check.ResumeVersion != recovery.ResumeVersion || check.PendingLabel != recovery.PendingLabel || check.AbandonedLabel != recovery.AbandonedLabel ||
		check.TaggedLabel != recovery.TaggedLabel || !check.TagMustNotExist || !check.GitHubReleaseMustNotExist || check.ReasonCode != "PRETAG_AUTHORIZATION_MISSING" ||
		len(check.SemanticContractSHA256) != 64 || check.CompletedReleaseSourceSHA != "" {
		t.Fatalf("incident identity=%+v", check)
	}
}

func TestCheckReleasePleaseRecoveryCompleteFailsBeforePublishedSourceIsPinned(t *testing.T) {
	contract := loadCanonicalForTest(t)
	contract.VersionPolicy.ReleasePleaseRecovery.State = "complete"
	contract.VersionPolicy.ReleasePleaseRecovery.CompletedReleaseSourceSHA = strings.Repeat("e", 40)
	config, _ := readRecoveryInputs(t)
	config = removeLastReleaseSHA(t, config)
	if _, err := CheckReleasePleaseRecovery(contract, config, []byte(`{".":"0.0.13"}`)); err == nil || !strings.Contains(err.Error(), "disabled until") {
		t.Fatalf("un-pinned complete recovery was not rejected: %v", err)
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

func TestCheckReleasePleaseRecoveryRejectsAdversarialConfig(t *testing.T) {
	contract := loadCanonicalForTest(t)
	config, _ := readRecoveryInputs(t)
	manifest := []byte(`{".":"0.0.12"}`)
	sha := contract.VersionPolicy.ReleasePleaseRecovery.AbandonedSourceSHA
	lastLine := `"last-release-sha": "` + sha + `",`
	tests := map[string][]byte{
		"duplicate":            []byte(strings.Replace(string(config), lastLine, lastLine+"\n  "+lastLine, 1)),
		"case variant":         []byte(strings.Replace(string(config), `"last-release-sha"`, `"Last-Release-SHA"`, 1)),
		"unknown":              []byte(strings.Replace(string(config), `"separate-pull-requests": true,`, "\"unknown\": true,\n  \"separate-pull-requests\": true,", 1)),
		"missing":              removeLastReleaseSHA(t, config),
		"null":                 []byte(strings.Replace(string(config), `"`+sha+`"`, `null`, 1)),
		"wrong":                []byte(strings.Replace(string(config), sha, strings.Repeat("f", 40), 1)),
		"release as root":      []byte(strings.Replace(string(config), lastLine, lastLine+"\n  \"release-as\": \"0.0.13\",", 1)),
		"release as package":   []byte(strings.Replace(string(config), `"release-type": "go",`, "\"release-type\": \"go\",\n      \"release-as\": \"0.0.13\",", 1)),
		"missing stable field": []byte(strings.Replace(string(config), "  \"separate-pull-requests\": true,\n", "", 1)),
		"null false control":   []byte(strings.Replace(string(config), `"include-component-in-tag": false`, `"include-component-in-tag": null`, 1)),
	}
	for name, candidate := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := CheckReleasePleaseRecovery(contract, candidate, manifest); err == nil {
				t.Fatal("adversarial config was accepted")
			}
		})
	}
}

func TestCheckReleasePleaseRecoveryRejectsAdversarialManifest(t *testing.T) {
	contract := loadCanonicalForTest(t)
	config, _ := readRecoveryInputs(t)
	tests := map[string][]byte{
		"duplicate":    []byte(`{".":"0.0.12",".":"0.0.12"}`),
		"case variant": []byte(`{"Root":"0.0.12"}`),
		"unknown":      []byte(`{".":"0.0.12","other":"0.0.12"}`),
		"missing":      []byte(`{}`),
		"null":         []byte(`{".":null}`),
		"wrong":        []byte(`{".":"0.0.13"}`),
		"wrong type":   []byte(`{".":12}`),
	}
	for name, manifest := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := CheckReleasePleaseRecovery(contract, config, manifest); err == nil {
				t.Fatal("adversarial manifest was accepted")
			}
		})
	}
}

func TestReleasePleaseRecoveryCompleteRequiresDistinctSource(t *testing.T) {
	for name, completed := range map[string]string{
		"missing":   "",
		"invalid":   "ABC",
		"abandoned": "a0eb82cb1fc4fa486ff2032d50ddedf6bccdbb8b",
		"proposal":  "c7169946d9c430209928266d95be7629c93d5878",
	} {
		t.Run(name, func(t *testing.T) {
			contract := loadCanonicalForTest(t)
			contract.VersionPolicy.ReleasePleaseRecovery.State = "complete"
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

func removeLastReleaseSHA(t *testing.T, config []byte) []byte {
	t.Helper()
	lines := strings.Split(string(config), "\n")
	for index, line := range lines {
		if strings.Contains(line, `"last-release-sha"`) {
			return []byte(strings.Join(append(lines[:index], lines[index+1:]...), "\n"))
		}
	}
	t.Fatal("last-release-sha line not found")
	return nil
}
