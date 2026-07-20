// Package actionsartifact validates the checked Actions artifact lifecycle
// policy against local workflow source. It deliberately contains no GitHub
// transport, credentials, live artifact inventory, or mutation code.
package actionsartifact

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/ildarbinanas-design/env-vault/internal/strictjson"
	"gopkg.in/yaml.v3"
)

const (
	PolicySchemaID          = "env-vault.actions-artifact-policy.v1"
	PolicySchemaVersion     = 1
	ValidationSchemaID      = "env-vault.actions-artifact-policy-validation.v1"
	ValidationSchemaVersion = 1
	CanonicalPolicyPath     = "release/actions-artifact-policy.v1.json"
	ExpectedUploadSiteCount = 23
	SupportedUploadAction   = "actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a"
	RunAttemptExpression    = "${{ github.run_attempt }}"

	maxPolicyBytes   = 1 << 20
	maxWorkflowBytes = 4 << 20
)

var (
	policyIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	jobIDPattern    = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	consumerPattern = regexp.MustCompile(`^[a-z][a-z0-9.-]*$`)

	supportedRetentionDays = []int{7, 14, 30, 90}
	supportedWorkflows     = []string{
		"bootstrap-release-assets.yml",
		"build-binaries.yml",
		"legacy-rebuild.yml",
		"publish-homebrew-bridge.yml",
		"release-evidence.yml",
		"release-please.yml",
		"reusable-quality.yml",
	}
	supportedClasses = []string{
		"abandoned-release-policy",
		"attempt-classification",
		"codeql-sarif",
		"e2e-baseline",
		"e2e-candidate",
		"e2e-reporter",
		"evidence-candidate",
		"homebrew-bridge",
		"legacy-diagnostic",
		"native-release",
		"operational-contract",
		"promotion-manifest",
		"promotion-platform",
		"publisher-bundle",
		"release-assets-bootstrap",
		"release-evidence",
		"release-observation",
		"release-please-recovery",
		"release-settings",
		"spdx-sbom",
	}
)

// Policy is the reviewed, local registry of every upload-artifact site.
type Policy struct {
	SchemaID               string        `json:"schema_id"`
	SchemaVersion          int           `json:"schema_version"`
	UploadAction           string        `json:"upload_action"`
	SupportedRetentionDays []int         `json:"supported_retention_days"`
	Sites                  []PolicySite  `json:"sites"`
	Patterns               []NamePattern `json:"patterns"`
}

// PolicySite binds one stable policy key to one exact workflow upload step.
// Consumers and RetentionRationale keep the lifecycle decision reviewable;
// they are policy, not dynamically inferred deletion authority.
type PolicySite struct {
	ID                 string   `json:"id"`
	Workflow           string   `json:"workflow"`
	Job                string   `json:"job"`
	Step               string   `json:"step"`
	Class              string   `json:"class"`
	PatternID          string   `json:"pattern_id"`
	ArtifactName       string   `json:"artifact_name"`
	RetentionDays      int      `json:"retention_days"`
	Consumers          []string `json:"consumers"`
	RetentionRationale string   `json:"retention_rationale"`
}

// NamePattern is durable lineage knowledge. A name match alone is never
// sufficient: classification also requires repository, workflow path, exact
// producer run/head, timestamps, and one resolved run attempt.
type NamePattern struct {
	ID                         string   `json:"id"`
	Class                      string   `json:"class"`
	Lifecycle                  string   `json:"lifecycle"`
	WorkflowPaths              []string `json:"workflow_paths"`
	NameRegex                  string   `json:"name_regex"`
	AttemptResolution          string   `json:"attempt_resolution"`
	AttemptCapture             int      `json:"attempt_capture"`
	ReferencedVersionCapture   int      `json:"referenced_version_capture"`
	ReferencedSourceSHACapture int      `json:"referenced_source_sha_capture"`
	Examples                   []string `json:"examples"`
	DependencyRepairRationale  string   `json:"dependency_repair_rationale"`
}

// Validation is deterministic proof that policy and checked workflow source
// have the same complete upload surface.
type Validation struct {
	SchemaID          string               `json:"schema_id"`
	SchemaVersion     int                  `json:"schema_version"`
	OK                bool                 `json:"ok"`
	PolicySchemaID    string               `json:"policy_schema_id"`
	PolicySHA256      string               `json:"policy_sha256"`
	UploadSiteCount   int                  `json:"upload_site_count"`
	WorkflowCount     int                  `json:"workflow_count"`
	ClassCount        int                  `json:"class_count"`
	RetentionTiers    []RetentionTierCount `json:"retention_tiers"`
	WorkflowFiles     []string             `json:"workflow_files"`
	ValidatedSiteKeys []string             `json:"validated_site_keys"`
}

type RetentionTierCount struct {
	Days      int `json:"days"`
	SiteCount int `json:"site_count"`
}

type workflowDocument struct {
	Jobs map[string]workflowJob `yaml:"jobs"`
}

type workflowJob struct {
	Steps []workflowStep `yaml:"steps"`
}

type workflowStep struct {
	Name string            `yaml:"name"`
	Uses string            `yaml:"uses"`
	With map[string]string `yaml:"with"`
}

type workflowSite struct {
	Workflow      string
	Job           string
	Step          string
	UploadAction  string
	ArtifactName  string
	RetentionDays int
}

// LoadPolicyFile performs a bounded, strict decode and validates the complete
// registry before any workflow source is inspected.
func LoadPolicyFile(filename string) (Policy, error) {
	data, err := readLimitedFile(filename, maxPolicyBytes)
	if err != nil {
		return Policy{}, fmt.Errorf("read Actions artifact policy: %w", err)
	}
	var policy Policy
	if err := strictjson.Decode(data, maxPolicyBytes, &policy); err != nil {
		return Policy{}, fmt.Errorf("decode Actions artifact policy: %w", err)
	}
	if err := policy.Validate(); err != nil {
		return Policy{}, err
	}
	return policy, nil
}

// Validate rejects unsupported policy evolution, duplicate keys or locations,
// non-canonical ordering, and incomplete lifecycle rationale.
func (policy Policy) Validate() error {
	if policy.SchemaID != PolicySchemaID || policy.SchemaVersion != PolicySchemaVersion {
		return fmt.Errorf("Actions artifact policy must be %s version %d", PolicySchemaID, PolicySchemaVersion)
	}
	if policy.UploadAction != SupportedUploadAction {
		return fmt.Errorf("unsupported upload action %q", policy.UploadAction)
	}
	if !equalInts(policy.SupportedRetentionDays, supportedRetentionDays) {
		return fmt.Errorf("supported_retention_days must be exactly %v", supportedRetentionDays)
	}
	patterns, err := validateNamePatterns(policy.Patterns)
	if err != nil {
		return err
	}
	seenIDs := make(map[string]bool, len(policy.Sites))
	seenLocations := make(map[string]bool, len(policy.Sites))
	seenWorkflows := make(map[string]bool, len(supportedWorkflows))
	seenTiers := make(map[int]bool, len(supportedRetentionDays))
	for index, site := range policy.Sites {
		if err := validatePolicySite(site); err != nil {
			return fmt.Errorf("sites[%d]: %w", index, err)
		}
		pattern, ok := patterns[site.PatternID]
		if !ok {
			return fmt.Errorf("sites[%d]: unknown pattern_id %q", index, site.PatternID)
		}
		if pattern.Class != site.Class {
			return fmt.Errorf("sites[%d]: pattern %q class %q does not match site class %q", index, site.PatternID, pattern.Class, site.Class)
		}
		if seenIDs[site.ID] {
			return fmt.Errorf("duplicate policy key %q", site.ID)
		}
		seenIDs[site.ID] = true
		location := siteLocation(site.Workflow, site.Job, site.Step)
		if seenLocations[location] {
			return fmt.Errorf("duplicate upload site %q", location)
		}
		seenLocations[location] = true
		seenWorkflows[site.Workflow] = true
		seenTiers[site.RetentionDays] = true
		if index > 0 && comparePolicySites(policy.Sites[index-1], site) >= 0 {
			return fmt.Errorf("sites must be in canonical workflow/job/step/id order: %q must sort before %q", site.ID, policy.Sites[index-1].ID)
		}
	}
	if len(policy.Sites) != ExpectedUploadSiteCount {
		return fmt.Errorf("policy has %d upload sites, want exactly %d", len(policy.Sites), ExpectedUploadSiteCount)
	}
	for _, workflow := range supportedWorkflows {
		if !seenWorkflows[workflow] {
			return fmt.Errorf("policy is missing supported workflow %q", workflow)
		}
	}
	for _, days := range supportedRetentionDays {
		if !seenTiers[days] {
			return fmt.Errorf("policy does not use supported retention tier %d", days)
		}
	}
	return nil
}

// ValidateWorkflowDirectory compares the complete local YAML upload surface to
// the checked policy. Unknown workflows and sites fail before a proof is made.
func ValidateWorkflowDirectory(policy Policy, workflowDirectory string) (Validation, error) {
	if err := policy.Validate(); err != nil {
		return Validation{}, err
	}
	actualSites, err := scanWorkflowDirectory(workflowDirectory)
	if err != nil {
		return Validation{}, err
	}
	policyByLocation := make(map[string]PolicySite, len(policy.Sites))
	for _, site := range policy.Sites {
		policyByLocation[siteLocation(site.Workflow, site.Job, site.Step)] = site
	}
	seenPolicyLocations := make(map[string]bool, len(actualSites))
	for _, actual := range actualSites {
		if !containsString(supportedWorkflows, actual.Workflow) {
			return Validation{}, fmt.Errorf("unknown upload workflow %q", actual.Workflow)
		}
		location := siteLocation(actual.Workflow, actual.Job, actual.Step)
		expected, ok := policyByLocation[location]
		if !ok {
			return Validation{}, fmt.Errorf("unknown upload site %q", location)
		}
		seenPolicyLocations[location] = true
		if actual.UploadAction != policy.UploadAction {
			return Validation{}, fmt.Errorf("upload site %q uses %q, want %q", location, actual.UploadAction, policy.UploadAction)
		}
		if actual.ArtifactName != expected.ArtifactName {
			return Validation{}, fmt.Errorf("upload site %q artifact name drifted: got %q, want %q", location, actual.ArtifactName, expected.ArtifactName)
		}
		if !strings.Contains(actual.ArtifactName, RunAttemptExpression) {
			return Validation{}, fmt.Errorf("upload site %q artifact name is not current-attempt-qualified", location)
		}
		if actual.RetentionDays != expected.RetentionDays {
			return Validation{}, fmt.Errorf("upload site %q retention is %d days, want %d", location, actual.RetentionDays, expected.RetentionDays)
		}
	}
	for location := range policyByLocation {
		if !seenPolicyLocations[location] {
			return Validation{}, fmt.Errorf("policy upload site %q is absent from workflow source", location)
		}
	}
	if len(actualSites) != ExpectedUploadSiteCount {
		return Validation{}, fmt.Errorf("workflow source has %d upload sites, want exactly %d", len(actualSites), ExpectedUploadSiteCount)
	}

	digest, err := CanonicalSHA256(policy)
	if err != nil {
		return Validation{}, err
	}
	return buildValidation(policy, digest), nil
}

// CanonicalSHA256 hashes the validated semantic policy encoding, independent
// of whitespace in the checked JSON file.
func CanonicalSHA256(policy Policy) (string, error) {
	if err := policy.Validate(); err != nil {
		return "", err
	}
	data, err := json.Marshal(policy)
	if err != nil {
		return "", fmt.Errorf("encode canonical Actions artifact policy: %w", err)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func validatePolicySite(site PolicySite) error {
	if !policyIDPattern.MatchString(site.ID) {
		return fmt.Errorf("invalid policy key %q", site.ID)
	}
	if !containsString(supportedWorkflows, site.Workflow) {
		return fmt.Errorf("unknown workflow %q", site.Workflow)
	}
	if !jobIDPattern.MatchString(site.Job) {
		return fmt.Errorf("invalid job %q", site.Job)
	}
	if strings.TrimSpace(site.Step) != site.Step || site.Step == "" || len(site.Step) > 200 {
		return errors.New("step must be a non-empty canonical string of at most 200 bytes")
	}
	if !containsString(supportedClasses, site.Class) {
		return fmt.Errorf("unknown artifact class %q", site.Class)
	}
	if !policyIDPattern.MatchString(site.PatternID) {
		return fmt.Errorf("invalid pattern_id %q", site.PatternID)
	}
	if strings.TrimSpace(site.ArtifactName) != site.ArtifactName || site.ArtifactName == "" || len(site.ArtifactName) > 512 {
		return errors.New("artifact_name must be a non-empty canonical string of at most 512 bytes")
	}
	if !strings.Contains(site.ArtifactName, RunAttemptExpression) {
		return errors.New("artifact_name must contain the exact current run-attempt expression")
	}
	if !containsInt(supportedRetentionDays, site.RetentionDays) {
		return fmt.Errorf("unsupported retention_days %d", site.RetentionDays)
	}
	if len(site.Consumers) == 0 || len(site.Consumers) > 32 {
		return errors.New("consumers must contain 1..32 reviewed identifiers")
	}
	for index, consumer := range site.Consumers {
		if !consumerPattern.MatchString(consumer) {
			return fmt.Errorf("invalid consumer %q", consumer)
		}
		if index > 0 && site.Consumers[index-1] >= consumer {
			return fmt.Errorf("consumers must be sorted and unique: %q", consumer)
		}
	}
	if strings.TrimSpace(site.RetentionRationale) != site.RetentionRationale || site.RetentionRationale == "" || len(site.RetentionRationale) > 500 {
		return errors.New("retention_rationale must be a non-empty canonical string of at most 500 bytes")
	}
	return nil
}

func scanWorkflowDirectory(directory string) ([]workflowSite, error) {
	info, err := os.Lstat(directory)
	if err != nil {
		return nil, fmt.Errorf("stat workflow directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("workflow directory must be a real directory")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("read workflow directory: %w", err)
	}
	var sites []workflowSite
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || (filepath.Ext(name) != ".yml" && filepath.Ext(name) != ".yaml") {
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("workflow file %q must not be a symlink", name)
		}
		fileSites, err := scanWorkflowFile(filepath.Join(directory, name), name)
		if err != nil {
			return nil, err
		}
		sites = append(sites, fileSites...)
	}
	sort.Slice(sites, func(i, j int) bool { return compareWorkflowSites(sites[i], sites[j]) < 0 })
	for index := 1; index < len(sites); index++ {
		if compareWorkflowSites(sites[index-1], sites[index]) == 0 {
			return nil, fmt.Errorf("duplicate workflow upload location %q", siteLocation(sites[index].Workflow, sites[index].Job, sites[index].Step))
		}
	}
	return sites, nil
}

func scanWorkflowFile(filename, workflow string) ([]workflowSite, error) {
	data, err := readLimitedFile(filename, maxWorkflowBytes)
	if err != nil {
		return nil, fmt.Errorf("read workflow %q: %w", workflow, err)
	}
	var document workflowDocument
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("decode workflow %q: %w", workflow, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, fmt.Errorf("decode workflow %q: multiple YAML documents are not allowed", workflow)
		}
		return nil, fmt.Errorf("decode workflow %q: %w", workflow, err)
	}

	jobs := make([]string, 0, len(document.Jobs))
	for job := range document.Jobs {
		jobs = append(jobs, job)
	}
	sort.Strings(jobs)
	var sites []workflowSite
	for _, job := range jobs {
		for _, step := range document.Jobs[job].Steps {
			if !strings.HasPrefix(step.Uses, "actions/upload-artifact") {
				continue
			}
			if strings.TrimSpace(step.Name) == "" {
				return nil, fmt.Errorf("workflow %q job %q has an unnamed upload-artifact site", workflow, job)
			}
			artifactName, ok := step.With["name"]
			if !ok || strings.TrimSpace(artifactName) == "" {
				return nil, fmt.Errorf("upload site %q has no explicit artifact name", siteLocation(workflow, job, step.Name))
			}
			retentionText, ok := step.With["retention-days"]
			if !ok {
				return nil, fmt.Errorf("upload site %q has no explicit retention-days", siteLocation(workflow, job, step.Name))
			}
			retention, err := strconv.Atoi(retentionText)
			if err != nil || !containsInt(supportedRetentionDays, retention) {
				return nil, fmt.Errorf("upload site %q has unsupported literal retention-days %q", siteLocation(workflow, job, step.Name), retentionText)
			}
			sites = append(sites, workflowSite{
				Workflow: workflow, Job: job, Step: step.Name, UploadAction: step.Uses,
				ArtifactName: artifactName, RetentionDays: retention,
			})
		}
	}
	return sites, nil
}

func buildValidation(policy Policy, digest string) Validation {
	tierCounts := make(map[int]int, len(supportedRetentionDays))
	classes := make(map[string]bool, len(supportedClasses))
	keys := make([]string, 0, len(policy.Sites))
	for _, site := range policy.Sites {
		tierCounts[site.RetentionDays]++
		classes[site.Class] = true
		keys = append(keys, site.ID)
	}
	tiers := make([]RetentionTierCount, 0, len(supportedRetentionDays))
	for _, days := range supportedRetentionDays {
		tiers = append(tiers, RetentionTierCount{Days: days, SiteCount: tierCounts[days]})
	}
	workflows := append([]string(nil), supportedWorkflows...)
	return Validation{
		SchemaID: ValidationSchemaID, SchemaVersion: ValidationSchemaVersion, OK: true,
		PolicySchemaID: policy.SchemaID, PolicySHA256: digest,
		UploadSiteCount: len(policy.Sites), WorkflowCount: len(workflows), ClassCount: len(classes),
		RetentionTiers: tiers, WorkflowFiles: workflows, ValidatedSiteKeys: keys,
	}
}

func comparePolicySites(left, right PolicySite) int {
	return strings.Compare(
		siteLocation(left.Workflow, left.Job, left.Step)+"\x00"+left.ID,
		siteLocation(right.Workflow, right.Job, right.Step)+"\x00"+right.ID,
	)
}

func compareWorkflowSites(left, right workflowSite) int {
	return strings.Compare(siteLocation(left.Workflow, left.Job, left.Step), siteLocation(right.Workflow, right.Job, right.Step))
}

func siteLocation(workflow, job, step string) string {
	return workflow + "/" + job + "/" + step
}

func readLimitedFile(filename string, limit int) ([]byte, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || len(data) > limit {
		return nil, fmt.Errorf("file size %d is outside 1..%d", len(data), limit)
	}
	return data, nil
}

func containsString(values []string, value string) bool {
	index := sort.SearchStrings(values, value)
	return index < len(values) && values[index] == value
}

func containsInt(values []int, value int) bool {
	index := sort.SearchInts(values, value)
	return index < len(values) && values[index] == value
}

func equalInts(left, right []int) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
