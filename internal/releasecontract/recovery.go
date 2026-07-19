package releasecontract

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const maxRecoveryInputBytes = 1 << 20

// ReleasePleaseRecoveryCheck is the versioned machine result consumed by the
// thin release workflow after both Release Please files have been saved
// locally. It deliberately contains no remote state or credentials.
type ReleasePleaseRecoveryCheck struct {
	SchemaID                  string `json:"schema_id"`
	SchemaVersion             int    `json:"schema_version"`
	OK                        bool   `json:"ok"`
	State                     string `json:"state"`
	AbandonedVersion          string `json:"abandoned_version"`
	AbandonedSourceSHA        string `json:"abandoned_source_sha"`
	GeneratedReleasePRNumber  int    `json:"generated_release_pr_number"`
	GeneratedReleasePRHeadSHA string `json:"generated_release_pr_head_sha"`
	ResumeVersion             string `json:"resume_version"`
	PendingLabel              string `json:"pending_label"`
	AbandonedLabel            string `json:"abandoned_label"`
	TaggedLabel               string `json:"tagged_label"`
	TagMustNotExist           bool   `json:"tag_must_not_exist"`
	GitHubReleaseMustNotExist bool   `json:"github_release_must_not_exist"`
	ReasonCode                string `json:"reason_code"`
	SemanticContractSHA256    string `json:"semantic_contract_sha256"`
	CompletedReleaseSourceSHA string `json:"completed_release_source_sha,omitempty"`
}

type releasePleaseConfig struct {
	Schema               string                          `json:"$schema"`
	LastReleaseSHA       json.RawMessage                 `json:"last-release-sha,omitempty"`
	SeparatePullRequests *bool                           `json:"separate-pull-requests"`
	Packages             map[string]releasePleasePackage `json:"packages"`
}

type releasePleasePackage struct {
	ReleaseType             string                       `json:"release-type"`
	PackageName             string                       `json:"package-name"`
	Component               string                       `json:"component"`
	ChangelogPath           string                       `json:"changelog-path"`
	SkipGitHubRelease       *bool                        `json:"skip-github-release"`
	IncludeVInTag           *bool                        `json:"include-v-in-tag"`
	IncludeComponentInTag   *bool                        `json:"include-component-in-tag"`
	PullRequestTitlePattern string                       `json:"pull-request-title-pattern"`
	PullRequestHeader       string                       `json:"pull-request-header"`
	PullRequestFooter       string                       `json:"pull-request-footer"`
	ChangelogSections       []releasePleaseChangeSection `json:"changelog-sections"`
	ExtraFiles              []releasePleaseExtraFile     `json:"extra-files"`
}

type releasePleaseChangeSection struct {
	Type    string `json:"type"`
	Section string `json:"section"`
	Hidden  *bool  `json:"hidden"`
}

type releasePleaseExtraFile struct {
	Type string `json:"type"`
	Path string `json:"path"`
}

// CheckReleasePleaseRecoveryFiles validates the checked-in Release Please
// config and manifest without network access. The strict decoder rejects
// unknown, duplicate, and case-variant fields at every level.
func CheckReleasePleaseRecoveryFiles(contract Contract, configFilename, manifestFilename string) (ReleasePleaseRecoveryCheck, error) {
	if err := contract.Validate(); err != nil {
		return ReleasePleaseRecoveryCheck{}, err
	}
	configData, err := readLimitedFile(configFilename, maxRecoveryInputBytes)
	if err != nil {
		return ReleasePleaseRecoveryCheck{}, fmt.Errorf("read release-please config: %w", err)
	}
	manifestData, err := readLimitedFile(manifestFilename, maxRecoveryInputBytes)
	if err != nil {
		return ReleasePleaseRecoveryCheck{}, fmt.Errorf("read release-please manifest: %w", err)
	}
	return CheckReleasePleaseRecovery(contract, configData, manifestData)
}

// CheckReleasePleaseRecovery validates in-memory snapshots. It is exported so
// adversarial contract tests can prove the same parser used by the CLI.
func CheckReleasePleaseRecovery(contract Contract, configData, manifestData []byte) (ReleasePleaseRecoveryCheck, error) {
	if err := contract.Validate(); err != nil {
		return ReleasePleaseRecoveryCheck{}, err
	}
	var config releasePleaseConfig
	if err := decodeJSON(configData, &config, true); err != nil {
		return ReleasePleaseRecoveryCheck{}, fmt.Errorf("decode release-please config: %w", err)
	}
	if err := validateCanonicalReleasePleaseConfig(contract, config); err != nil {
		return ReleasePleaseRecoveryCheck{}, fmt.Errorf("validate release-please config: %w", err)
	}
	var manifest map[string]json.RawMessage
	if err := decodeJSON(manifestData, &manifest, true); err != nil {
		return ReleasePleaseRecoveryCheck{}, fmt.Errorf("decode release-please manifest: %w", err)
	}
	manifestKey := contract.VersionPolicy.ReleasePlease.ManifestKey
	if len(manifest) != 1 {
		return ReleasePleaseRecoveryCheck{}, errors.New("release-please manifest must contain exactly the contract package key")
	}
	manifestVersionRaw, ok := manifest[manifestKey]
	if !ok {
		return ReleasePleaseRecoveryCheck{}, errors.New("release-please manifest contract package key is missing")
	}
	manifestVersion, err := requiredJSONString(manifestVersionRaw, "manifest package version")
	if err != nil {
		return ReleasePleaseRecoveryCheck{}, err
	}

	recovery := contract.VersionPolicy.ReleasePleaseRecovery
	switch recovery.State {
	case "active":
		lastReleaseSHA, err := requiredJSONString(config.LastReleaseSHA, "last-release-sha")
		if err != nil {
			return ReleasePleaseRecoveryCheck{}, err
		}
		if lastReleaseSHA != recovery.AbandonedSourceSHA {
			return ReleasePleaseRecoveryCheck{}, errors.New("last-release-sha does not equal the abandoned source SHA")
		}
		if manifestVersion != strings.TrimPrefix(recovery.AbandonedVersion, contract.VersionPolicy.TagPrefix) {
			return ReleasePleaseRecoveryCheck{}, errors.New("active recovery requires the abandoned contract version in the manifest")
		}
	case "complete":
		if config.LastReleaseSHA != nil {
			return ReleasePleaseRecoveryCheck{}, errors.New("complete recovery requires last-release-sha to be absent")
		}
		if !versionAtLeast(manifestVersion, recovery.ResumeVersion) {
			return ReleasePleaseRecoveryCheck{}, errors.New("complete recovery requires a manifest version at or above the resume version")
		}
	default:
		return ReleasePleaseRecoveryCheck{}, fmt.Errorf("unsupported recovery state %q", recovery.State)
	}

	digest, err := SemanticSHA256(contract)
	if err != nil {
		return ReleasePleaseRecoveryCheck{}, err
	}
	return ReleasePleaseRecoveryCheck{
		SchemaID:                  ReleasePleaseRecoveryCheckSchemaID,
		SchemaVersion:             1,
		OK:                        true,
		State:                     recovery.State,
		AbandonedVersion:          recovery.AbandonedVersion,
		AbandonedSourceSHA:        recovery.AbandonedSourceSHA,
		GeneratedReleasePRNumber:  recovery.GeneratedReleasePRNumber,
		GeneratedReleasePRHeadSHA: recovery.GeneratedReleasePRHeadSHA,
		ResumeVersion:             recovery.ResumeVersion,
		PendingLabel:              recovery.PendingLabel,
		AbandonedLabel:            recovery.AbandonedLabel,
		TaggedLabel:               recovery.TaggedLabel,
		TagMustNotExist:           recovery.TagMustNotExist,
		GitHubReleaseMustNotExist: recovery.GitHubReleaseMustNotExist,
		ReasonCode:                recovery.ReasonCode,
		SemanticContractSHA256:    digest,
		CompletedReleaseSourceSHA: recovery.CompletedReleaseSourceSHA,
	}, nil
}

func versionAtLeast(actual, floor string) bool {
	if !IsVersion("v"+actual) || !IsVersion(floor) {
		return false
	}
	actualParts := strings.Split(actual, ".")
	floorParts := strings.Split(strings.TrimPrefix(floor, "v"), ".")
	for index := range actualParts {
		if len(actualParts[index]) != len(floorParts[index]) {
			return len(actualParts[index]) > len(floorParts[index])
		}
		if actualParts[index] != floorParts[index] {
			return actualParts[index] > floorParts[index]
		}
	}
	return true
}

func validateCanonicalReleasePleaseConfig(contract Contract, config releasePleaseConfig) error {
	if config.Schema != "https://raw.githubusercontent.com/googleapis/release-please/v17.6.0/schemas/config.json" {
		return errors.New("$schema must pin Release Please v17.6.0")
	}
	if config.SeparatePullRequests == nil || !*config.SeparatePullRequests {
		return errors.New("separate-pull-requests must be present and true")
	}
	policy := contract.VersionPolicy.ReleasePlease
	if len(config.Packages) != 1 {
		return errors.New("packages must contain exactly the contract manifest key")
	}
	pkg, ok := config.Packages[policy.ManifestKey]
	if !ok {
		return errors.New("contract package is missing")
	}
	if pkg.ReleaseType != "go" || pkg.PackageName != contract.Naming.Product || pkg.Component != policy.Component || pkg.ChangelogPath != "CHANGELOG.md" {
		return errors.New("root package release identity is invalid")
	}
	if pkg.SkipGitHubRelease == nil || !*pkg.SkipGitHubRelease || pkg.IncludeVInTag == nil || !*pkg.IncludeVInTag || pkg.IncludeComponentInTag == nil || *pkg.IncludeComponentInTag {
		return errors.New("root package tag and publication controls are invalid or missing")
	}
	if pkg.PullRequestTitlePattern != "chore${scope}: release "+contract.Naming.Product+" "+contract.VersionPolicy.TagPrefix+"${version}" {
		return errors.New("pull-request-title-pattern is invalid")
	}
	if pkg.PullRequestHeader != "Merging this unchanged reviewed pull request after the required exact tuple confirmation authorizes publication once its merge commit passes "+contract.Repositories.Source.DefaultBranch+" CI." {
		return errors.New("pull-request-header is invalid")
	}
	if pkg.PullRequestFooter != "This PR was generated with Release Please." {
		return errors.New("pull-request-footer is invalid")
	}
	wantSections := []releasePleaseChangeSection{
		{Type: "feat", Section: "Features"},
		{Type: "fix", Section: "Bug Fixes"},
		{Type: "build", Section: "Build System"},
		{Type: "ci", Section: "Continuous Integration"},
		{Type: "docs", Section: "Documentation"},
		{Type: "test", Section: "Tests"},
		{Type: "refactor", Section: "Refactoring"},
		{Type: "perf", Section: "Performance"},
		{Type: "revert", Section: "Reverts"},
	}
	if len(pkg.ChangelogSections) != len(wantSections) {
		return errors.New("changelog-sections must contain the canonical ordered set")
	}
	for index, want := range wantSections {
		got := pkg.ChangelogSections[index]
		if got.Type != want.Type || got.Section != want.Section || got.Hidden == nil || *got.Hidden {
			return fmt.Errorf("changelog section %d is invalid or incomplete", index)
		}
	}
	if len(pkg.ExtraFiles) != 1 || pkg.ExtraFiles[0].Type != "generic" || pkg.ExtraFiles[0].Path != "README.md" {
		return errors.New("extra-files must contain only generic README.md")
	}
	return nil
}

func requiredJSONString(raw json.RawMessage, name string) (string, error) {
	if len(raw) == 0 {
		return "", fmt.Errorf("%s is missing", name)
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", fmt.Errorf("%s must not be null", name)
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", fmt.Errorf("%s must be a string: %w", name, err)
	}
	if value == "" {
		return "", fmt.Errorf("%s must not be empty", name)
	}
	return value, nil
}
