// Package releasecontract loads and validates the declarative release contract.
package releasecontract

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	SchemaID       = "env-vault.release-contract.v1"
	SchemaVersion  = 1
	CanonicalPath  = "release/contract.v1.json"
	MatrixSchemaID = "env-vault.release-contract-matrix.v1"
	blockedVersion = "v0.0.8"
	blockedTagSHA  = "1d094f9e4a3e0343e713d4126f6118a8a9e98e2d"
	versionPattern = `^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`
)

var (
	idPattern        = regexp.MustCompile(`^[a-z][a-z0-9_.-]*$`)
	errorCodePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
	workflowFile     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*\.yml$`)
	versionRegexp    = regexp.MustCompile(versionPattern)
)

type Contract struct {
	SchemaID      string            `json:"schema_id"`
	SchemaVersion int               `json:"schema_version"`
	VersionPolicy VersionPolicy     `json:"version_policy"`
	Platforms     []Platform        `json:"platforms"`
	Assets        []string          `json:"assets"`
	Workflows     []Workflow        `json:"workflows"`
	Apps          []App             `json:"apps"`
	Capabilities  Capabilities      `json:"capabilities"`
	ReleaseStages []ReleaseStage    `json:"release_stages"`
	RepairModes   []RepairMode      `json:"repair_modes"`
	ActionCodes   []string          `json:"action_codes"`
	ErrorCodes    []string          `json:"error_codes"`
	Schemas       map[string]string `json:"schemas"`
}

type VersionPolicy struct {
	Pattern         string              `json:"pattern"`
	BlockedVersions []BlockedVersion    `json:"blocked_versions"`
	LegacyRebuild   LegacyRebuildPolicy `json:"legacy_rebuild"`
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

type App struct {
	ID             string `json:"id"`
	Slug           string `json:"slug"`
	Repository     string `json:"repository"`
	Environment    string `json:"environment"`
	AuditWorkflow  string `json:"audit_workflow"`
	CIWorkflowFile string `json:"ci_workflow_file,omitempty"`
	CIWorkflowName string `json:"ci_workflow_name,omitempty"`
}

// ObservationCapability cannot carry mutation-plan fields by construction.
type ObservationCapability struct {
	ID          string   `json:"id"`
	Transport   string   `json:"transport"`
	HTTPMethods []string `json:"http_methods"`
}

// MutationCapability always declares the fail-closed apply contract.
type MutationCapability struct {
	ID                          string `json:"id"`
	DryRunDefault               bool   `json:"dry_run_default"`
	RequiresPlanDigest          bool   `json:"requires_plan_digest"`
	RequiresRemotePreconditions bool   `json:"requires_remote_preconditions"`
	Idempotent                  bool   `json:"idempotent"`
}

type Capabilities struct {
	Observation []ObservationCapability `json:"observation"`
	Mutation    []MutationCapability    `json:"mutation"`
}

type ReleaseStage struct {
	ID            string `json:"id"`
	Ordinal       int    `json:"ordinal"`
	Workflow      string `json:"workflow"`
	StateMutating bool   `json:"state_mutating"`
}

type RepairMode struct {
	Code          string `json:"code"`
	ResumeStage   string `json:"resume_stage"`
	Rebuilds      bool   `json:"rebuilds"`
	StateMutating bool   `json:"state_mutating"`
}

type Matrix struct {
	Include []Platform `json:"include"`
}

func LoadFile(filename string) (Contract, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return Contract{}, fmt.Errorf("read release contract: %w", err)
	}
	var contract Contract
	if err := decodeStrict(data, &contract); err != nil {
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

// IsVersion applies the canonical strict vMAJOR.MINOR.PATCH policy used by
// the validated declarative contract.
func IsVersion(value string) bool {
	return versionRegexp.MatchString(value)
}

func (c Contract) Validate() error {
	var problems []string
	add := func(format string, values ...any) { problems = append(problems, fmt.Sprintf(format, values...)) }
	if c.SchemaID != SchemaID || c.SchemaVersion != SchemaVersion {
		add("schema must be %s version %d", SchemaID, SchemaVersion)
	}
	if c.VersionPolicy.Pattern != versionPattern {
		add("version policy pattern is not the canonical strict SemVer expression")
	} else if _, err := regexp.Compile(c.VersionPolicy.Pattern); err != nil {
		add("version policy pattern is invalid: %v", err)
	}
	if err := validateBlockedVersions(c.VersionPolicy.BlockedVersions); err != nil {
		add("blocked versions: %v", err)
	}
	if err := validateLegacyRebuild(c.VersionPolicy.LegacyRebuild); err != nil {
		add("legacy rebuild: %v", err)
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
	platformIDs := make(map[string]bool)
	derivedAssets := make(map[string]bool)
	if len(c.Platforms) != 5 {
		add("platform count=%d, want 5", len(c.Platforms))
	}
	for index, platform := range c.Platforms {
		if err := validatePlatform(platform); err != nil {
			add("platform %d: %v", index, err)
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
	if !workflowIDs["legacy_rebuild"] {
		add("required workflow legacy_rebuild is missing")
	}
	if !workflowIDs["release_evidence"] {
		add("required workflow release_evidence is missing")
	}
	appIDs, appSlugs := map[string]bool{}, map[string]bool{}
	for index, app := range c.Apps {
		if !idPattern.MatchString(app.ID) || !idPattern.MatchString(app.Slug) || !validRepository(app.Repository) || strings.TrimSpace(app.Environment) == "" || !workflowIDs[app.AuditWorkflow] {
			add("app %d has invalid ID, slug, repository, environment, or audit workflow", index)
		}
		if app.ID == "homebrew_tap" {
			if !workflowFile.MatchString(app.CIWorkflowFile) || strings.TrimSpace(app.CIWorkflowName) == "" {
				add("homebrew_tap app must define a valid CI workflow file and name")
			}
		} else if app.CIWorkflowFile != "" || app.CIWorkflowName != "" {
			add("app %q must not define Homebrew tap CI workflow identity", app.ID)
		}
		if appIDs[app.ID] || appSlugs[app.Slug] {
			add("app %q duplicates an ID or slug", app.ID)
		}
		appIDs[app.ID], appSlugs[app.Slug] = true, true
	}
	capabilityIDs := make(map[string]bool)
	for _, capability := range c.Capabilities.Observation {
		if !idPattern.MatchString(capability.ID) || capability.Transport != "github_api" || len(capability.HTTPMethods) != 1 || capability.HTTPMethods[0] != "GET" {
			add("observation capability %q must be GitHub API GET-only", capability.ID)
		}
		if capabilityIDs[capability.ID] {
			add("capability %q is duplicated", capability.ID)
		}
		capabilityIDs[capability.ID] = true
	}
	for _, capability := range c.Capabilities.Mutation {
		if !idPattern.MatchString(capability.ID) || !capability.DryRunDefault || !capability.RequiresPlanDigest || !capability.RequiresRemotePreconditions || !capability.Idempotent {
			add("mutation capability %q lacks dry-run, digest, precondition, or idempotency guarantee", capability.ID)
		}
		if capabilityIDs[capability.ID] {
			add("capability %q is duplicated", capability.ID)
		}
		capabilityIDs[capability.ID] = true
	}
	for _, required := range []string{
		"release.plan", "release.status", "release.watch", "release.verify", "release.metrics",
		"release.repair.plan", "release.legacy_rebuild.plan",
	} {
		if !capabilityIDs[required] {
			add("required observation capability %s is missing", required)
		}
	}
	for _, required := range []string{"release.repair.apply", "release.legacy_rebuild.apply"} {
		if !capabilityIDs[required] {
			add("required mutation capability %s is missing", required)
		}
	}
	for _, required := range []string{"REPOSITORY_NOT_ACCESSIBLE", "ATTESTATION_VERIFICATION_FAILED", "TAP_CI_ATTEMPT_FAILED", "PROMOTION_ARTIFACT_INVENTORY_INVALID"} {
		if !contains(c.ErrorCodes, required) {
			add("error code %s is required", required)
		}
	}
	stageIDs := make(map[string]bool)
	for index, stage := range c.ReleaseStages {
		if !idPattern.MatchString(stage.ID) || stage.Ordinal != index+1 || !workflowIDs[stage.Workflow] {
			add("release stage %d has invalid ID, ordinal, or workflow reference", index)
		}
		if stageIDs[stage.ID] {
			add("release stage %q is duplicated", stage.ID)
		}
		stageIDs[stage.ID] = true
	}
	modeCodes := make(map[string]bool)
	for _, mode := range c.RepairModes {
		if !idPattern.MatchString(mode.Code) || !stageIDs[mode.ResumeStage] {
			add("repair mode %q has invalid code or stage", mode.Code)
		}
		if modeCodes[mode.Code] {
			add("repair mode %q is duplicated", mode.Code)
		}
		modeCodes[mode.Code] = true
	}
	for _, required := range []string{"none", "release-assets", "homebrew", "health"} {
		if !modeCodes[required] {
			add("required repair mode %q is missing", required)
		}
	}
	if err := validateCodes(c.ActionCodes, idPattern, "action"); err != nil {
		add("%v", err)
	}
	if err := validateCodes(c.ErrorCodes, errorCodePattern, "error"); err != nil {
		add("%v", err)
	}
	for _, required := range []string{
		"rerun_all_jobs", "rerun_tap_pr_ci_all_jobs", "rerun_tap_post_merge_ci_all_jobs", "wait_tap_ci", "replan_publisher", "dispatch_legacy_rebuild",
		"dispatch_release_assets_repair", "dispatch_homebrew_repair", "dispatch_health_repair",
	} {
		if !contains(c.ActionCodes, required) {
			add("action code %s is required", required)
		}
	}
	requiredSchemas := map[string]string{
		"release_contract":          SchemaID,
		"release_contract_matrix":   MatrixSchemaID,
		"e2e_baseline":              "env-vault.e2e-baseline.v2",
		"e2e_matrix_proof":          "env-vault.e2e-matrix-proof.v1",
		"e2e_baseline_verification": "env-vault.e2e-baseline-verification.v1",
		"release_evidence":          "env-vault.release-evidence.v1",
		"release_status":            "env-vault.release-status.v1",
		"release_plan":              "env-vault.release-plan.v1",
		"release_verify":            "env-vault.release-verify.v1",
		"release_metrics":           "env-vault.release-metrics.v1",
		"release_repair_apply":      "env-vault.release-repair-apply.v1",
		"promotion_platform":        "env-vault.promotion-platform.v1",
		"promotion_manifest":        "env-vault.promotion-manifest.v1",
		"repair_plan":               "env-vault.release-repair-plan.v1",
		"legacy_rebuild_plan":       "env-vault.legacy-rebuild-plan.v1",
		"legacy_rebuild_apply":      "env-vault.legacy-rebuild-apply.v1",
	}
	for name, expected := range requiredSchemas {
		if c.Schemas[name] != expected {
			add("required schema %q must be %q", name, expected)
		}
	}
	for name, schema := range c.Schemas {
		if !idPattern.MatchString(name) || !regexp.MustCompile(`^env-vault\.[a-z0-9-]+\.v[1-9][0-9]*$`).MatchString(schema) {
			add("schema entry %q=%q is invalid", name, schema)
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return errors.New("invalid release contract: " + strings.Join(problems, "; "))
	}
	return nil
}

func validateBlockedVersions(blocked []BlockedVersion) error {
	seen := make(map[string]bool)
	foundRequired := false
	for _, item := range blocked {
		if !IsVersion(item.Version) || !validSHA(item.TagSHA) || !idPattern.MatchString(item.ReasonCode) || seen[item.Version] {
			return fmt.Errorf("entry for %q is malformed or duplicated", item.Version)
		}
		seen[item.Version] = true
		if item.Version == blockedVersion {
			foundRequired = item.TagSHA == blockedTagSHA && item.TagMustRemain && item.GitHubReleaseMustNotExist
		}
	}
	if !foundRequired {
		return errors.New("v0.0.8 must remain pinned to its failed tag without a GitHub Release")
	}
	return nil
}

func validateLegacyRebuild(policy LegacyRebuildPolicy) error {
	if policy.GoVersion != "1.22.12" || policy.PublicationEligible {
		return errors.New("diagnostic toolchain must be Go 1.22.12 and publication must remain disabled")
	}
	if len(policy.Versions) != 7 {
		return fmt.Errorf("version count=%d, want 7", len(policy.Versions))
	}
	seen := make(map[string]bool, len(policy.Versions))
	for _, item := range policy.Versions {
		if !IsVersion(item.Version) || !validSHA(item.TagSHA) || seen[item.Version] || item.Version == blockedVersion {
			return fmt.Errorf("entry for %q is malformed, duplicated, or blocked", item.Version)
		}
		seen[item.Version] = true
	}
	for patch := 1; patch <= 7; patch++ {
		version := fmt.Sprintf("v0.0.%d", patch)
		itemFound := false
		for _, item := range policy.Versions {
			if item.Version == version {
				itemFound = true
				if item.LiteralVersionSupported != (patch >= 4) {
					return fmt.Errorf("literal version support for %s does not match the tag contract", version)
				}
				break
			}
		}
		if !itemFound {
			return fmt.Errorf("required version %s is missing", version)
		}
	}
	return nil
}

func validatePlatform(platform Platform) error {
	if !idPattern.MatchString(platform.ID) || platform.ID != platform.GOOS+"-"+platform.GOARCH || strings.TrimSpace(platform.Runner) == "" {
		return errors.New("invalid ID, target, or runner")
	}
	if platform.CGO != "0" && platform.CGO != "1" {
		return errors.New("cgo must be string 0 or 1")
	}
	base := "env-vault-" + platform.ID
	wantFormat, wantBinary := "tar.gz", "env-vault"
	if platform.GOOS == "windows" {
		wantFormat, wantBinary = "zip", "env-vault.exe"
	}
	if platform.ArchiveFormat != wantFormat || platform.Archive != base+"."+wantFormat || platform.Checksum != platform.Archive+".sha256" || platform.Binary != wantBinary {
		return errors.New("archive, checksum, format, or binary is not derived from the target")
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

func validSHA(value string) bool {
	if len(value) != 40 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
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

func decodeStrict(data []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
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
