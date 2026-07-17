// Package releasecontract loads and validates env-vault's offline release
// contract. It deliberately contains no GitHub transport or credential code.
package releasecontract

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
)

const (
	SchemaID       = "env-vault.release-contract.v1"
	SchemaVersion  = 1
	CanonicalPath  = "release/contract.v1.json"
	MatrixSchemaID = "env-vault.release-contract-matrix.v1"

	VersionSchemaID                    = "env-vault.releasecheck-version.v1"
	ErrorSchemaID                      = "env-vault.releasecheck-error.v1"
	ValidationSchemaID                 = "env-vault.contract-validation.v1"
	ClassificationSchemaID             = "env-vault.attempt-classification.v1"
	LegacyQuerySchemaID                = "env-vault.legacy-rebuild-query.v1"
	ReleasePleaseRecoverySchemaID      = "env-vault.release-please-recovery.v1"
	ReleasePleaseRecoveryCheckSchemaID = "env-vault.release-please-recovery-check.v1"

	maxContractBytes  = 1 << 20
	blockedVersion008 = "v0.0.8"
	blockedTagSHA008  = "1d094f9e4a3e0343e713d4126f6118a8a9e98e2d"
	blockedVersion009 = "v0.0.9"
	blockedTagSHA009  = "b8b652dcff41d5f2ab4a9f14bed65ddf1f866c65"
	blockedVersion010 = "v0.0.10"
	blockedTagSHA010  = "591350ea0e9ebb2b9ef7a8f9d89c0e86c251c795"
	blockedVersion011 = "v0.0.11"
	blockedTagSHA011  = "95181260700afdb0bf257b69f490079d2fb6d5f0"
	// This one-time recovery pin records the independently verified v0.0.13
	// release source. Durable v0.0.13 release evidence was skipped, so the pin
	// must not be described as evidence-run success.
	completedReleaseSource013 = "6206b472cda81f7a87656055d8eb6627c26a0fef"
	versionPattern            = `^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`
)

var (
	idPattern         = regexp.MustCompile(`^[a-z][a-z0-9_.-]*$`)
	actionCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	slugPattern       = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	errorCodePattern  = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
	workflowFile      = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*\.yml$`)
	schemaPattern     = regexp.MustCompile(`^env-vault\.[a-z0-9-]+\.v[1-9][0-9]*$`)
	versionRegexp     = regexp.MustCompile(versionPattern)
	shaPattern        = regexp.MustCompile(`^[0-9a-f]{40}$`)
	attemptPattern    = regexp.MustCompile(`^[1-9][0-9]*$`)
)

// Contract is the single declarative source for release identities and
// invariants. The v1 decoder rejects unknown and duplicate fields.
type Contract struct {
	SchemaID             string            `json:"schema_id"`
	SchemaVersion        int               `json:"schema_version"`
	VersionPolicy        VersionPolicy     `json:"version_policy"`
	Naming               Naming            `json:"naming"`
	Platforms            []Platform        `json:"platforms"`
	Assets               []string          `json:"assets"`
	Workflows            []Workflow        `json:"workflows"`
	MainRequiredChecks   []RequiredCheck   `json:"main_required_checks"`
	Apps                 []App             `json:"apps"`
	ReleaseStages        []ReleaseStage    `json:"release_stages"`
	AllowedRepairActions []RepairAction    `json:"allowed_repair_actions"`
	ActionCodes          []string          `json:"action_codes"`
	ReasonCodes          []string          `json:"reason_codes"`
	ErrorCodes           []string          `json:"error_codes"`
	Schemas              map[string]string `json:"schemas"`
}

type VersionPolicy struct {
	Pattern               string                      `json:"pattern"`
	ReleasePleaseRecovery ReleasePleaseRecoveryPolicy `json:"release_please_recovery"`
	BlockedVersions       []BlockedVersion            `json:"blocked_versions"`
	LegacyRebuild         LegacyRebuildPolicy         `json:"legacy_rebuild"`
}

// ReleasePleaseRecoveryPolicy records the one-time, fail-closed recovery from
// a merged Release Please proposal that was deliberately not tagged. The
// incident identity remains immutable when state advances from active to
// complete; only CompletedReleaseSourceSHA is added.
type ReleasePleaseRecoveryPolicy struct {
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
	CompletedReleaseSourceSHA string `json:"completed_release_source_sha,omitempty"`
}

type LegacyRebuildPolicy struct {
	GoVersion           string                 `json:"go_version"`
	PublicationEligible bool                   `json:"publication_eligible"`
	Versions            []LegacyRebuildVersion `json:"versions"`
}

type LegacyRebuildVersion struct {
	Version                 string `json:"version"`
	TagSHA                  string `json:"tag_sha"`
	LiteralVersionSupported bool   `json:"literal_version_supported"`
}

type BlockedVersion struct {
	Version                   string `json:"version"`
	TagSHA                    string `json:"tag_sha"`
	TagMustRemain             bool   `json:"tag_must_remain"`
	GitHubReleaseMustNotExist bool   `json:"github_release_must_not_exist"`
	ReasonCode                string `json:"reason_code"`
}

type Naming struct {
	Product                   string `json:"product"`
	ArchivePrefix             string `json:"archive_prefix"`
	ChecksumSuffix            string `json:"checksum_suffix"`
	PlatformArtifactTemplate  string `json:"platform_artifact_template"`
	PlatformEvidenceTemplate  string `json:"platform_evidence_template"`
	PromotionManifestTemplate string `json:"promotion_manifest_template"`
}

type Platform struct {
	ID            string `json:"id"`
	Runner        string `json:"runner"`
	GOOS          string `json:"goos"`
	GOARCH        string `json:"goarch"`
	CGO           string `json:"cgo"`
	Archive       string `json:"archive"`
	Checksum      string `json:"checksum"`
	ArchiveFormat string `json:"archive_format"`
	Binary        string `json:"binary"`
}

type Workflow struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	File string `json:"file"`
}

type RequiredCheck struct {
	Name     string `json:"name"`
	Workflow string `json:"workflow"`
	Event    string `json:"event"`
}

type App struct {
	ID             string `json:"id"`
	Slug           string `json:"slug"`
	Repository     string `json:"repository"`
	Environment    string `json:"environment"`
	AuditWorkflow  string `json:"audit_workflow"`
	CIWorkflowFile string `json:"ci_workflow_file,omitempty"`
	CIWorkflowName string `json:"ci_workflow_name,omitempty"`
}

type ReleaseStage struct {
	ID            string `json:"id"`
	Ordinal       int    `json:"ordinal"`
	Workflow      string `json:"workflow"`
	StateMutating bool   `json:"state_mutating"`
}

type RepairAction struct {
	ID                  string `json:"id"`
	ActionCode          string `json:"action_code"`
	ResumeStage         string `json:"resume_stage"`
	Rebuilds            bool   `json:"rebuilds"`
	PublicationEligible bool   `json:"publication_eligible"`
}

type Matrix struct {
	Include []Platform `json:"include"`
}

// LoadFile loads at most one MiB and validates the complete v1 contract.
func LoadFile(filename string) (Contract, error) {
	data, err := readLimitedFile(filename, maxContractBytes)
	if err != nil {
		return Contract{}, fmt.Errorf("read release contract: %w", err)
	}
	var contract Contract
	if err := decodeJSON(data, &contract, true); err != nil {
		return Contract{}, fmt.Errorf("decode release contract: %w", err)
	}
	if err := validateReleasePleaseRecoveryEncoding(data, contract.VersionPolicy.ReleasePleaseRecovery); err != nil {
		return Contract{}, fmt.Errorf("decode release contract: %w", err)
	}
	if err := contract.Validate(); err != nil {
		return Contract{}, err
	}
	return contract, nil
}

func LoadCanonical(repositoryRoot string) (Contract, error) {
	return LoadFile(filepath.Join(repositoryRoot, filepath.FromSlash(CanonicalPath)))
}

func (c Contract) Matrix() Matrix {
	return Matrix{Include: append([]Platform(nil), c.Platforms...)}
}

func (c Contract) LegacyVersion(version string) (LegacyRebuildVersion, bool) {
	for _, candidate := range c.VersionPolicy.LegacyRebuild.Versions {
		if candidate.Version == version {
			return candidate, true
		}
	}
	return LegacyRebuildVersion{}, false
}

func (c Contract) AppByID(id string) (App, bool) {
	for _, app := range c.Apps {
		if app.ID == id {
			return app, true
		}
	}
	return App{}, false
}

func (c Contract) WorkflowByID(id string) (Workflow, bool) {
	for _, workflow := range c.Workflows {
		if workflow.ID == id {
			return workflow, true
		}
	}
	return Workflow{}, false
}

func (c Contract) PlatformByID(id string) (Platform, bool) {
	for _, platform := range c.Platforms {
		if platform.ID == id {
			return platform, true
		}
	}
	return Platform{}, false
}

func (c Contract) HasActionCode(code string) bool { return contains(c.ActionCodes, code) }
func (c Contract) HasErrorCode(code string) bool  { return contains(c.ErrorCodes, code) }

// RenderName expands a contract naming template and additionally verifies that
// platform values belong to this contract.
func (c Contract) RenderName(template string, values map[string]string) (string, error) {
	if err := c.Validate(); err != nil {
		return "", err
	}
	if template != c.Naming.PlatformArtifactTemplate && template != c.Naming.PlatformEvidenceTemplate && template != c.Naming.PromotionManifestTemplate {
		return "", errors.New("template is not declared by the release contract")
	}
	if platform, present := values["platform"]; present {
		if _, ok := c.PlatformByID(platform); !ok {
			return "", fmt.Errorf("platform %q is not declared by the release contract", platform)
		}
	}
	return RenderName(template, values)
}

// RenderName expands only the known release placeholders and rejects missing,
// unused, non-canonical, or path-producing values.
func RenderName(template string, values map[string]string) (string, error) {
	if template == "" || filepath.Base(template) != template || len(template) > 256 {
		return "", errors.New("template must be a safe basename")
	}
	allowed := map[string]func(string) bool{
		"platform":   func(value string) bool { return idPattern.MatchString(value) },
		"attempt":    func(value string) bool { return attemptPattern.MatchString(value) },
		"source_sha": isSHA,
	}
	used := make(map[string]bool)
	rendered := template
	for {
		start := strings.IndexByte(rendered, '{')
		if start < 0 {
			break
		}
		endOffset := strings.IndexByte(rendered[start:], '}')
		if endOffset < 0 {
			return "", errors.New("template contains an unterminated placeholder")
		}
		end := start + endOffset
		placeholder := rendered[start+1 : end]
		validator, ok := allowed[placeholder]
		value, provided := values[placeholder]
		if !ok || !provided || !validator(value) {
			return "", fmt.Errorf("placeholder %q is unknown, missing, or invalid", placeholder)
		}
		used[placeholder] = true
		rendered = rendered[:start] + value + rendered[end+1:]
	}
	if strings.ContainsAny(rendered, "{}") {
		return "", errors.New("template contains malformed placeholder syntax")
	}
	for key := range values {
		if !used[key] {
			return "", fmt.Errorf("value for unused placeholder %q", key)
		}
	}
	if rendered == "" || filepath.Base(rendered) != rendered || len(rendered) > 256 {
		return "", errors.New("rendered name is not a safe basename")
	}
	return rendered, nil
}

// IsVersion applies the canonical strict vMAJOR.MINOR.PATCH policy.
func IsVersion(value string) bool { return versionRegexp.MatchString(value) }

// SemanticSHA256 hashes the validated data model, not JSON whitespace or
// object key order. Array order remains semantic.
func SemanticSHA256(c Contract) (string, error) {
	if err := c.Validate(); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("marshal semantic release contract: %w", err)
	}
	digest := sha256.Sum256(canonical)
	return hex.EncodeToString(digest[:]), nil
}

func (c Contract) Validate() error {
	var problems []string
	add := func(format string, values ...any) {
		problems = append(problems, fmt.Sprintf(format, values...))
	}
	if c.SchemaID != SchemaID || c.SchemaVersion != SchemaVersion {
		add("schema must be %s version %d", SchemaID, SchemaVersion)
	}
	if c.VersionPolicy.Pattern != versionPattern {
		add("version policy pattern is not the canonical strict SemVer expression")
	}
	if err := validateReleasePleaseRecovery(c.VersionPolicy.ReleasePleaseRecovery); err != nil {
		add("release-please recovery: %v", err)
	}
	if err := validateBlockedVersions(c.VersionPolicy.BlockedVersions); err != nil {
		add("blocked versions: %v", err)
	}
	if err := validateLegacyRebuild(c.VersionPolicy.LegacyRebuild); err != nil {
		add("legacy rebuild: %v", err)
	}
	if err := validateNaming(c.Naming); err != nil {
		add("naming: %v", err)
	}

	assets := make(map[string]bool)
	if len(c.Assets) != 10 {
		add("assets count=%d, want 10", len(c.Assets))
	}
	for _, asset := range c.Assets {
		if asset == "" || filepath.Base(asset) != asset || assets[asset] {
			add("asset %q is empty, non-canonical, or duplicated", asset)
		}
		assets[asset] = true
	}

	wantPlatforms := map[string]struct {
		runner string
		cgo    string
	}{
		"linux-amd64":   {runner: "ubuntu-latest", cgo: "0"},
		"linux-arm64":   {runner: "ubuntu-24.04-arm", cgo: "0"},
		"darwin-amd64":  {runner: "macos-15-intel", cgo: "1"},
		"darwin-arm64":  {runner: "macos-15", cgo: "1"},
		"windows-amd64": {runner: "windows-latest", cgo: "0"},
	}
	platformIDs := make(map[string]bool)
	derivedAssets := make(map[string]bool)
	if len(c.Platforms) != len(wantPlatforms) {
		add("platform count=%d, want %d", len(c.Platforms), len(wantPlatforms))
	}
	for index, platform := range c.Platforms {
		want, required := wantPlatforms[platform.ID]
		if err := validatePlatform(platform, c.Naming); err != nil {
			add("platform %d: %v", index, err)
		}
		if !required || platform.Runner != want.runner || platform.CGO != want.cgo {
			add("platform %q is not one of the canonical native targets", platform.ID)
		}
		if platformIDs[platform.ID] {
			add("platform ID %q is duplicated", platform.ID)
		}
		platformIDs[platform.ID] = true
		derivedAssets[platform.Archive] = true
		derivedAssets[platform.Checksum] = true
	}
	if !sameSet(assets, derivedAssets) {
		add("assets must equal the five platform archive/checksum pairs")
	}

	workflowIDs := make(map[string]bool)
	workflowNames := make(map[string]bool)
	workflowFiles := make(map[string]bool)
	for index, workflow := range c.Workflows {
		if !idPattern.MatchString(workflow.ID) || strings.TrimSpace(workflow.Name) == "" || !workflowFile.MatchString(workflow.File) {
			add("workflow %d has invalid ID, name, or filename", index)
		}
		if workflowIDs[workflow.ID] || workflowNames[workflow.Name] || workflowFiles[workflow.File] {
			add("workflow %q duplicates an ID, name, or filename", workflow.ID)
		}
		workflowIDs[workflow.ID], workflowNames[workflow.Name], workflowFiles[workflow.File] = true, true, true
	}
	for _, required := range []string{"ci", "quality", "planning", "publisher", "release_evidence", "legacy_rebuild", "planning_app_audit", "tap_app_audit"} {
		if !workflowIDs[required] {
			add("required workflow %q is missing", required)
		}
	}
	if err := validateMainRequiredChecks(c.MainRequiredChecks); err != nil {
		add("main required checks: %v", err)
	}

	appIDs, appSlugs := map[string]bool{}, map[string]bool{}
	for index, app := range c.Apps {
		if !idPattern.MatchString(app.ID) || !slugPattern.MatchString(app.Slug) || !validRepository(app.Repository) || strings.TrimSpace(app.Environment) == "" || !workflowIDs[app.AuditWorkflow] {
			add("app %d has invalid ID, slug, repository, environment, or audit workflow", index)
		}
		if app.ID == "homebrew_tap" {
			if !workflowFile.MatchString(app.CIWorkflowFile) || strings.TrimSpace(app.CIWorkflowName) == "" {
				add("homebrew_tap app must define its exact CI workflow identity")
			}
		} else if app.CIWorkflowFile != "" || app.CIWorkflowName != "" {
			add("app %q must not define Homebrew tap CI identity", app.ID)
		}
		if appIDs[app.ID] || appSlugs[app.Slug] {
			add("app %q duplicates an ID or slug", app.ID)
		}
		appIDs[app.ID], appSlugs[app.Slug] = true, true
	}
	for _, required := range []string{"release_planning", "homebrew_tap"} {
		if !appIDs[required] {
			add("required app %q is missing", required)
		}
	}

	stageIDs := make(map[string]bool)
	wantStages := []struct {
		id       string
		workflow string
		mutating bool
	}{
		{"source_quality", "ci", false},
		{"exact_version_artifact_quality", "quality", false},
		{"planning", "planning", true},
		{"publication", "publisher", true},
		{"supply_chain", "publisher", true},
		{"homebrew", "publisher", true},
		{"health", "publisher", false},
		{"evidence", "release_evidence", true},
	}
	if len(c.ReleaseStages) != len(wantStages) {
		add("release stage count=%d, want %d", len(c.ReleaseStages), len(wantStages))
	}
	for index, stage := range c.ReleaseStages {
		if !idPattern.MatchString(stage.ID) || stage.Ordinal != index+1 || !workflowIDs[stage.Workflow] {
			add("release stage %d has invalid ID, ordinal, or workflow reference", index)
		}
		if index >= len(wantStages) || stage.ID != wantStages[index].id || stage.Workflow != wantStages[index].workflow || stage.StateMutating != wantStages[index].mutating {
			add("release stage %d is not canonical", index)
		}
		if stageIDs[stage.ID] {
			add("release stage %q is duplicated", stage.ID)
		}
		stageIDs[stage.ID] = true
	}

	if err := validateCodes(c.ActionCodes, actionCodePattern, "action"); err != nil {
		add("%v", err)
	}
	if err := validateCodes(c.ReasonCodes, errorCodePattern, "reason"); err != nil {
		add("%v", err)
	}
	if err := validateCodes(c.ErrorCodes, errorCodePattern, "error"); err != nil {
		add("%v", err)
	}
	for _, required := range []string{"none", "wait_attempt", "rerun_all_jobs", "inspect_failure", "rerun_tap_pr_ci_all_jobs", "rerun_tap_post_merge_ci_all_jobs", "dispatch_release_assets_repair", "dispatch_homebrew_repair", "dispatch_health_repair", "dispatch_legacy_rebuild", "mark_release_pr_abandoned"} {
		if !contains(c.ActionCodes, required) {
			add("required action code %q is missing", required)
		}
	}
	for _, required := range []string{"CONTRACT_INVALID", "INPUT_INVALID", "INPUT_INCOMPLETE", "SCHEMA_UNSUPPORTED", "ATTEMPT_NOT_COMPLETED", "ATTEMPT_MATRIX_INCOMPLETE", "ATTEMPT_STATE_INCONSISTENT", "CI_ATTEMPT_FAILED", "SETTINGS_INPUT_INVALID", "SETTINGS_POLICY_INVALID", "SETTINGS_TUPLE_MISMATCH", "SETTINGS_DIGEST_MISMATCH"} {
		if !contains(c.ErrorCodes, required) {
			add("required error code %q is missing", required)
		}
	}
	for _, required := range []string{"ATTEMPT_MATRIX_COMPLETE", "ATTEMPT_NOT_COMPLETED", "ATTEMPT_MATRIX_INCOMPLETE", "CI_ATTEMPT_FAILED", "PRETAG_AUTHORIZATION_MISSING"} {
		if !contains(c.ReasonCodes, required) {
			add("required reason code %q is missing", required)
		}
	}
	if err := validateRepairActions(c.AllowedRepairActions, stageIDs, c.ActionCodes); err != nil {
		add("repair actions: %v", err)
	}

	requiredSchemas := map[string]string{
		"release_contract":                  SchemaID,
		"release_contract_matrix":           MatrixSchemaID,
		"releasecheck_version":              VersionSchemaID,
		"releasecheck_error":                ErrorSchemaID,
		"contract_validation":               ValidationSchemaID,
		"attempt_classification":            ClassificationSchemaID,
		"legacy_rebuild_query":              LegacyQuerySchemaID,
		"legacy_rebuild_diagnostic":         "env-vault.legacy-rebuild-diagnostic.v1",
		"source_quality_proof":              "env-vault.source-quality-proof.v1",
		"literal_version_results":           "env-vault.literal-version-results.v1",
		"promotion_platform":                "env-vault.promotion-platform.v1",
		"promotion_manifest":                "env-vault.promotion-manifest.v1",
		"promotion_verification":            "env-vault.promotion-verification.v1",
		"e2e_baseline":                      "env-vault.e2e-baseline.v2",
		"e2e_matrix_proof":                  "env-vault.e2e-matrix-proof.v1",
		"e2e_baseline_verification":         "env-vault.e2e-baseline-verification.v1",
		"release_observation":               "env-vault.release-observation.v1",
		"release_health_proof":              "env-vault.release-health-proof.v1",
		"repository_release_settings_check": "env-vault.repository-release-settings-check.v1",
		"repository_release_settings_proof": "env-vault.repository-release-settings-proof.v1",
		"release_authorization":             "env-vault.release-authorization.v1",
		"release_please_recovery":           ReleasePleaseRecoverySchemaID,
		"release_please_recovery_check":     ReleasePleaseRecoveryCheckSchemaID,
		"attestation_verification_bundle":   "env-vault.attestation-verification-bundle.v1",
		"release_evidence":                  "env-vault.release-evidence.v1",
		"release_metrics":                   "env-vault.release-metrics.v1",
		"release_metrics_baseline":          "env-vault.release-metrics-baseline.v1",
		"release_metrics_comparison":        "env-vault.release-metrics-comparison.v1",
	}
	for name, expected := range requiredSchemas {
		if c.Schemas[name] != expected {
			add("required schema %q must be %q", name, expected)
		}
	}
	for name, schema := range c.Schemas {
		if !idPattern.MatchString(name) || !schemaPattern.MatchString(schema) {
			add("schema entry %q=%q is invalid", name, schema)
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return errors.New("invalid release contract: " + strings.Join(problems, "; "))
	}
	return nil
}

func validateNaming(n Naming) error {
	if n.Product != "env-vault" || n.ArchivePrefix != n.Product+"-" || n.ChecksumSuffix != ".sha256" {
		return errors.New("product, archive prefix, or checksum suffix is not canonical")
	}
	if n.PlatformArtifactTemplate != "env-vault-release-{platform}-attempt-{attempt}" ||
		n.PlatformEvidenceTemplate != "env-vault-promotion-platform-{platform}-attempt-{attempt}" ||
		n.PromotionManifestTemplate != "env-vault-promotion-{source_sha}-attempt-{attempt}" {
		return errors.New("attempt-scoped artifact templates are not canonical")
	}
	return nil
}

func validateMainRequiredChecks(checks []RequiredCheck) error {
	want := []RequiredCheck{
		{Name: "Analyze (actions)", Workflow: "CodeQL", Event: "dynamic"},
		{Name: "Analyze (go)", Workflow: "CodeQL", Event: "dynamic"},
		{Name: "Dependency review", Workflow: "Dependency review", Event: "pull_request"},
		{Name: "pr-title", Workflow: "pr-title", Event: "pull_request"},
		{Name: "quality-gate", Workflow: "ci", Event: "pull_request"},
	}
	if len(checks) != len(want) {
		return fmt.Errorf("entry count=%d, want %d", len(checks), len(want))
	}
	seenNames := make(map[string]bool, len(checks))
	for index, check := range checks {
		if strings.TrimSpace(check.Name) != check.Name || check.Name == "" ||
			strings.TrimSpace(check.Workflow) != check.Workflow || check.Workflow == "" ||
			(check.Event != "dynamic" && check.Event != "pull_request") || seenNames[check.Name] {
			return fmt.Errorf("entry %d is empty, malformed, or duplicates a check name", index)
		}
		seenNames[check.Name] = true
		if check != want[index] {
			return fmt.Errorf("entry %d does not match the canonical name/workflow/event identity", index)
		}
	}
	return nil
}

func validateReleasePleaseRecovery(policy ReleasePleaseRecoveryPolicy) error {
	if policy.State != "active" && policy.State != "complete" {
		return errors.New("state must be active or complete")
	}
	if policy.AbandonedVersion != "v0.0.12" || policy.AbandonedSourceSHA != "a0eb82cb1fc4fa486ff2032d50ddedf6bccdbb8b" {
		return errors.New("abandoned v0.0.12 identity must remain exact")
	}
	if policy.GeneratedReleasePRNumber != 31 || policy.GeneratedReleasePRHeadSHA != "c7169946d9c430209928266d95be7629c93d5878" {
		return errors.New("generated release PR #31 identity must remain exact")
	}
	if policy.ResumeVersion != "v0.0.13" {
		return errors.New("resume version must remain v0.0.13")
	}
	if policy.PendingLabel != "autorelease: pending" || policy.AbandonedLabel != "autorelease: abandoned" || policy.TaggedLabel != "autorelease: tagged" {
		return errors.New("Release Please transition labels must remain exact")
	}
	if !policy.TagMustNotExist || !policy.GitHubReleaseMustNotExist {
		return errors.New("abandoned version must prohibit both tag and GitHub Release")
	}
	if policy.ReasonCode != "PRETAG_AUTHORIZATION_MISSING" {
		return errors.New("reason code must remain PRETAG_AUTHORIZATION_MISSING")
	}
	if policy.State == "active" {
		if completedReleaseSource013 != "" {
			return errors.New("active recovery is forbidden after the completed release source is pinned")
		}
		if policy.CompletedReleaseSourceSHA != "" {
			return errors.New("active recovery must omit completed release source SHA")
		}
		return nil
	}
	if completedReleaseSource013 == "" {
		return errors.New("complete recovery is disabled until the successful v0.0.13 source SHA is pinned in the checker")
	}
	if policy.CompletedReleaseSourceSHA != completedReleaseSource013 || !isSHA(policy.CompletedReleaseSourceSHA) || policy.CompletedReleaseSourceSHA == policy.AbandonedSourceSHA || policy.CompletedReleaseSourceSHA == policy.GeneratedReleasePRHeadSHA {
		return errors.New("complete recovery requires the checker-pinned successful v0.0.13 source SHA")
	}
	return nil
}

func validateReleasePleaseRecoveryEncoding(data []byte, policy ReleasePleaseRecoveryPolicy) error {
	var envelope struct {
		VersionPolicy struct {
			Recovery map[string]json.RawMessage `json:"release_please_recovery"`
		} `json:"version_policy"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return err
	}
	_, completedPresent := envelope.VersionPolicy.Recovery["completed_release_source_sha"]
	if policy.State == "active" && completedPresent {
		return errors.New("active release-please recovery must omit completed_release_source_sha")
	}
	if policy.State == "complete" && !completedPresent {
		return errors.New("complete release-please recovery must include completed_release_source_sha")
	}
	return nil
}

func validateBlockedVersions(blocked []BlockedVersion) error {
	want := []struct {
		version string
		sha     string
	}{
		{blockedVersion008, blockedTagSHA008},
		{blockedVersion009, blockedTagSHA009},
		{blockedVersion010, blockedTagSHA010},
		{blockedVersion011, blockedTagSHA011},
	}
	if len(blocked) != len(want) {
		return fmt.Errorf("entry count=%d, want %d", len(blocked), len(want))
	}
	for index, expected := range want {
		item := blocked[index]
		if item.Version != expected.version || item.TagSHA != expected.sha || !item.TagMustRemain || !item.GitHubReleaseMustNotExist || item.ReasonCode != "failed_tag_without_release" {
			return fmt.Errorf("%s must remain pinned to its failed tag without a GitHub Release", expected.version)
		}
	}
	return nil
}

func validateLegacyRebuild(policy LegacyRebuildPolicy) error {
	if policy.GoVersion != "1.22.12" || policy.PublicationEligible {
		return errors.New("diagnostic toolchain must be Go 1.22.12 and publication must remain disabled")
	}
	want := []struct {
		version string
		sha     string
		literal bool
	}{
		{"v0.0.1", "b9dd8826b3dca3a0f638df39797cb13d1eb10aa5", false},
		{"v0.0.2", "595bf4fa7ca6a7346400e2243bc3b678f6767c5b", false},
		{"v0.0.3", "4a8b11697d93829c364e0807d83fc87df2a2fd5a", false},
		{"v0.0.4", "765627566f1d5ba175de017fe8ef3614a0408453", true},
		{"v0.0.5", "1d927ce2828153e87399749b48656d8dbc9ce1f4", true},
		{"v0.0.6", "76c9ac760b9d98752d737a1875339ac3ca2de0e5", true},
		{"v0.0.7", "4fbae380747e75a1f59498adbd76ccf5791e0480", true},
	}
	if len(policy.Versions) != len(want) {
		return fmt.Errorf("version count=%d, want %d", len(policy.Versions), len(want))
	}
	for index, item := range policy.Versions {
		if item.Version != want[index].version || item.TagSHA != want[index].sha || item.LiteralVersionSupported != want[index].literal {
			return fmt.Errorf("entry %d does not match the immutable legacy contract", index)
		}
	}
	return nil
}

func validatePlatform(platform Platform, naming Naming) error {
	if !idPattern.MatchString(platform.ID) || platform.ID != platform.GOOS+"-"+platform.GOARCH || strings.TrimSpace(platform.Runner) == "" {
		return errors.New("invalid ID, target, or runner")
	}
	if platform.CGO != "0" && platform.CGO != "1" {
		return errors.New("cgo must be string 0 or 1")
	}
	base := naming.ArchivePrefix + platform.ID
	wantFormat, wantBinary := "tar.gz", naming.Product
	if platform.GOOS == "windows" {
		wantFormat, wantBinary = "zip", naming.Product+".exe"
	}
	if platform.ArchiveFormat != wantFormat || platform.Archive != base+"."+wantFormat || platform.Checksum != platform.Archive+naming.ChecksumSuffix || platform.Binary != wantBinary {
		return errors.New("archive, checksum, format, or binary is not derived from target naming")
	}
	return nil
}

func validateRepairActions(actions []RepairAction, stageIDs map[string]bool, actionCodes []string) error {
	want := map[string]struct {
		code      string
		stage     string
		rebuilds  bool
		publishes bool
	}{
		"rerun-ci-attempt":          {"rerun_all_jobs", "source_quality", true, true},
		"release-assets":            {"dispatch_release_assets_repair", "publication", false, true},
		"homebrew":                  {"dispatch_homebrew_repair", "homebrew", false, true},
		"health":                    {"dispatch_health_repair", "health", false, true},
		"legacy-rebuild-diagnostic": {"dispatch_legacy_rebuild", "exact_version_artifact_quality", true, false},
	}
	if len(actions) != len(want) {
		return fmt.Errorf("action count=%d, want %d", len(actions), len(want))
	}
	seen := make(map[string]bool)
	for _, action := range actions {
		expected, ok := want[action.ID]
		if !ok || seen[action.ID] || !stageIDs[action.ResumeStage] || !contains(actionCodes, action.ActionCode) || action.ActionCode != expected.code || action.ResumeStage != expected.stage || action.Rebuilds != expected.rebuilds || action.PublicationEligible != expected.publishes {
			return fmt.Errorf("action %q is invalid, duplicated, or weakens its guarantee", action.ID)
		}
		seen[action.ID] = true
	}
	return nil
}

func validateCodes(values []string, pattern *regexp.Regexp, kind string) error {
	if len(values) == 0 {
		return fmt.Errorf("%s codes are empty", kind)
	}
	seen := make(map[string]bool)
	for _, value := range values {
		if !pattern.MatchString(value) || seen[value] {
			return fmt.Errorf("%s code %q is invalid or duplicated", kind, value)
		}
		seen[value] = true
	}
	return nil
}

func validRepository(value string) bool {
	parts := strings.Split(value, "/")
	return len(parts) == 2 && idPattern.MatchString(parts[0]) && idPattern.MatchString(parts[1])
}

func sameSet(left, right map[string]bool) bool {
	if len(left) != len(right) {
		return false
	}
	for key := range left {
		if !right[key] {
			return false
		}
	}
	return true
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func readLimitedFile(filename string, limit int64) ([]byte, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("file exceeds %d bytes", limit)
	}
	return data, nil
}

func decodeJSON(data []byte, destination any, strict bool) error {
	if err := rejectDuplicateJSONFields(data); err != nil {
		return err
	}
	if strict {
		var generic any
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		if err := decoder.Decode(&generic); err != nil {
			return err
		}
		if err := validateExactJSONFields(generic, reflect.TypeOf(destination), "$", true); err != nil {
			return err
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if strict {
		decoder.DisallowUnknownFields()
	}
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

// decodeKnownJSON accepts future GitHub response fields but projects only
// exact, case-sensitive fields known by destination. This avoids the standard
// decoder's case-insensitive struct-field fallback for security identities.
func decodeKnownJSON(data []byte, destination any) error {
	if err := rejectDuplicateJSONFields(data); err != nil {
		return err
	}
	var generic any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&generic); err != nil {
		return err
	}
	projected := projectKnownJSONFields(generic, reflect.TypeOf(destination))
	encoded, err := json.Marshal(projected)
	if err != nil {
		return err
	}
	return json.Unmarshal(encoded, destination)
}

func validateExactJSONFields(value any, destination reflect.Type, path string, rejectUnknown bool) error {
	for destination.Kind() == reflect.Pointer {
		destination = destination.Elem()
	}
	if destination == reflect.TypeOf(json.RawMessage{}) {
		return nil
	}
	switch destination.Kind() {
	case reflect.Struct:
		object, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("%s must be a JSON object", path)
		}
		fields := jsonStructFields(destination)
		for key, child := range object {
			field, known := fields[key]
			if !known {
				if rejectUnknown {
					return fmt.Errorf("unknown field %s.%s", path, key)
				}
				continue
			}
			if err := validateExactJSONFields(child, field, path+"."+key, rejectUnknown); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		array, ok := value.([]any)
		if !ok {
			return fmt.Errorf("%s must be a JSON array", path)
		}
		for index, child := range array {
			if err := validateExactJSONFields(child, destination.Elem(), fmt.Sprintf("%s[%d]", path, index), rejectUnknown); err != nil {
				return err
			}
		}
	case reflect.Map:
		object, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("%s must be a JSON object", path)
		}
		for key, child := range object {
			if err := validateExactJSONFields(child, destination.Elem(), path+"."+key, rejectUnknown); err != nil {
				return err
			}
		}
	}
	return nil
}

func projectKnownJSONFields(value any, destination reflect.Type) any {
	for destination.Kind() == reflect.Pointer {
		destination = destination.Elem()
	}
	if destination == reflect.TypeOf(json.RawMessage{}) {
		return value
	}
	switch destination.Kind() {
	case reflect.Struct:
		object, ok := value.(map[string]any)
		if !ok {
			return value
		}
		fields := jsonStructFields(destination)
		projected := make(map[string]any)
		for key, field := range fields {
			if child, present := object[key]; present {
				projected[key] = projectKnownJSONFields(child, field)
			}
		}
		return projected
	case reflect.Slice, reflect.Array:
		array, ok := value.([]any)
		if !ok {
			return value
		}
		projected := make([]any, len(array))
		for index, child := range array {
			projected[index] = projectKnownJSONFields(child, destination.Elem())
		}
		return projected
	case reflect.Map:
		object, ok := value.(map[string]any)
		if !ok {
			return value
		}
		projected := make(map[string]any, len(object))
		for key, child := range object {
			projected[key] = projectKnownJSONFields(child, destination.Elem())
		}
		return projected
	default:
		return value
	}
}

func jsonStructFields(destination reflect.Type) map[string]reflect.Type {
	fields := make(map[string]reflect.Type)
	for index := 0; index < destination.NumField(); index++ {
		field := destination.Field(index)
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name == "" {
			name = field.Name
		}
		if name != "-" {
			fields[name] = field.Type
		}
	}
	return fields
}

func rejectDuplicateJSONFields(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := walkJSONValue(decoder); err != nil {
		return err
	}
	if token, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("unexpected trailing JSON token %v", token)
		}
		return err
	}
	return nil
}

func walkJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]bool)
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("JSON object key is not a string")
			}
			if seen[key] {
				return fmt.Errorf("duplicate JSON field %q", key)
			}
			seen[key] = true
			if err := walkJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return errors.New("malformed JSON object")
		}
	case '[':
		for decoder.More() {
			if err := walkJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return errors.New("malformed JSON array")
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
	return nil
}

func isSHA(value string) bool { return shaPattern.MatchString(value) }
