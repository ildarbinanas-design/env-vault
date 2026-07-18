package releasesettings

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ildarbinanas-design/env-vault/internal/releasecontract"
	"github.com/ildarbinanas-design/env-vault/internal/strictjson"
)

const githubActionsIntegrationID int64 = 15368

var (
	shaPattern        = regexp.MustCompile(`^[0-9a-f]{40}$`)
	digestPattern     = regexp.MustCompile(`^[0-9a-f]{64}$`)
	repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
)

type rulesetIdentity struct {
	Name   string
	Target string
}

type settingsPolicy struct {
	Repository     string
	DefaultBranch  string
	MainRef        string
	TagRef         string
	Rulesets       []rulesetIdentity
	RequiredChecks []string
}

// Seal validates untrusted saved observations, binds their exact bytes to the
// tuple, and returns a self-digested proof.
func Seal(contract releasecontract.Contract, tuple Tuple, raw RawInputs) (Proof, error) {
	policy, err := settingsPolicyFromContract(contract)
	if err != nil {
		return Proof{}, err
	}
	if err := validateTuple(tuple); err != nil {
		return Proof{}, err
	}
	if tuple.Repository != policy.Repository {
		return Proof{}, fail(CodeTupleMismatch, "repository settings tuple differs from the release contract repository", nil)
	}
	inputs, err := validateRawInputs(tuple.Repository, raw, policy)
	if err != nil {
		return Proof{}, err
	}
	proof := Proof{
		SchemaID: SchemaID, SchemaVersion: SchemaVersion, Tuple: tuple,
		Inputs: inputs,
		Result: ResultPass,
	}
	proof.ProofSHA256, err = ProofSHA256(proof)
	if err != nil {
		return Proof{}, fail(CodeInputInvalid, "compute repository settings proof digest", err)
	}
	return proof, nil
}

// Check validates the same untrusted saved observations as Seal without
// creating a release-tuple proof. It performs no network access.
func Check(contract releasecontract.Contract, repository string, raw RawInputs) (CheckResult, error) {
	policy, err := settingsPolicyFromContract(contract)
	if err != nil {
		return CheckResult{}, err
	}
	if repository != policy.Repository {
		return CheckResult{}, fail(CodeTupleMismatch, "repository differs from the release contract repository", nil)
	}
	if _, err := validateRawInputs(repository, raw, policy); err != nil {
		return CheckResult{}, err
	}
	return CheckResult{
		SchemaID: CheckSchemaID, SchemaVersion: SchemaVersion, OK: true,
		Repository: repository, Result: ResultPass,
	}, nil
}

// Verify replays the complete offline decision and requires the caller's
// independently known tuple to equal the sealed tuple exactly.
func Verify(contract releasecontract.Contract, proof Proof, expected Tuple) error {
	policy, err := settingsPolicyFromContract(contract)
	if err != nil {
		return err
	}
	if proof.SchemaID != SchemaID || proof.SchemaVersion != SchemaVersion || proof.Result != ResultPass {
		return fail(CodeInputInvalid, "repository settings proof schema or result is invalid", nil)
	}
	if err := validateTuple(expected); err != nil {
		return err
	}
	wantDigest, err := ProofSHA256(proof)
	if err != nil {
		return fail(CodeInputInvalid, "compute repository settings proof digest", err)
	}
	if !digestPattern.MatchString(proof.ProofSHA256) || proof.ProofSHA256 != wantDigest {
		return fail(CodeDigestMismatch, "repository settings proof self-digest differs", nil)
	}
	if !reflect.DeepEqual(proof.Tuple, expected) {
		return fail(CodeTupleMismatch, "repository settings proof tuple differs from the expected exact tuple", nil)
	}
	if expected.Repository != policy.Repository {
		return fail(CodeTupleMismatch, "repository settings tuple differs from the release contract repository", nil)
	}
	if err := validateInputs(proof.Tuple.Repository, proof.Inputs, policy); err != nil {
		return err
	}
	return nil
}

func validateTuple(tuple Tuple) error {
	if err := validateRepository(tuple.Repository); err != nil {
		return err
	}
	if !shaPattern.MatchString(tuple.SourceSHA) || !releasecontract.IsVersion(tuple.ReleaseVersion) || tuple.PlanningRunID <= 0 || tuple.PlanningRunAttempt <= 0 {
		return fail(CodeInputInvalid, "source, version, planning run, or attempt is invalid", nil)
	}
	checkedAt, err := time.Parse(time.RFC3339, tuple.CheckedAt)
	if err != nil || checkedAt.Location() != time.UTC || checkedAt.Format(time.RFC3339) != tuple.CheckedAt {
		return fail(CodeInputInvalid, "checked_at must be canonical UTC RFC3339 without fractional seconds", err)
	}
	return nil
}

func validateRepository(repository string) error {
	parts := strings.Split(repository, "/")
	if !repositoryPattern.MatchString(repository) || len(parts) != 2 || parts[0] == "." || parts[0] == ".." || parts[1] == "." || parts[1] == ".." || strings.HasSuffix(parts[1], ".git") {
		return fail(CodeInputInvalid, "repository is not canonical owner/name", nil)
	}
	return nil
}

func validateRawInputs(repository string, raw RawInputs, policy settingsPolicy) (Inputs, error) {
	if err := validateRepository(repository); err != nil {
		return Inputs{}, err
	}
	documents := make([]Document, 5)
	for index, data := range [][]byte{
		raw.MergeSettings, raw.RulesetPages, raw.MainRuleset,
		raw.TagRuleset, raw.EvidenceRuleset,
	} {
		document, err := sealDocument(data)
		if err != nil {
			return Inputs{}, err
		}
		documents[index] = document
	}
	inputs := Inputs{
		MergeSettings: documents[0], RulesetPages: documents[1],
		MainRuleset: documents[2], TagRuleset: documents[3],
		EvidenceRuleset: documents[4],
	}
	if err := validateInputs(repository, inputs, policy); err != nil {
		return Inputs{}, err
	}
	return inputs, nil
}

func validateInputs(repository string, inputs Inputs, policy settingsPolicy) error {
	documents := []struct {
		name     string
		document Document
	}{
		{"merge_settings", inputs.MergeSettings},
		{"ruleset_pages", inputs.RulesetPages},
		{"main_ruleset", inputs.MainRuleset},
		{"tag_ruleset", inputs.TagRuleset},
		{"evidence_ruleset", inputs.EvidenceRuleset},
	}
	for _, entry := range documents {
		if err := validateDocumentBinding(entry.name, entry.document); err != nil {
			return err
		}
	}
	graphqlIDs, err := validateMergeSettings([]byte(inputs.MergeSettings.DocumentJSON), repository, policy)
	if err != nil {
		return err
	}
	restIDs, err := validateRulesetPages([]byte(inputs.RulesetPages.DocumentJSON), repository, policy.Rulesets)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(graphqlIDs, restIDs) {
		return fail(CodePolicyInvalid, "GraphQL and REST canonical ruleset IDs differ", nil)
	}
	if err := validateMainRuleset([]byte(inputs.MainRuleset.DocumentJSON), repository, restIDs[policy.Rulesets[0].Name], policy); err != nil {
		return err
	}
	if err := validateTagRuleset([]byte(inputs.TagRuleset.DocumentJSON), repository, restIDs[policy.Rulesets[1].Name], policy); err != nil {
		return err
	}
	if err := validateEvidenceRuleset([]byte(inputs.EvidenceRuleset.DocumentJSON), repository, restIDs[policy.Rulesets[2].Name], policy); err != nil {
		return err
	}
	return nil
}

func validateDocumentBinding(name string, document Document) error {
	data := []byte(document.DocumentJSON)
	if len(data) == 0 || len(data) > maxJSONBytes || !utf8.Valid(data) || !digestPattern.MatchString(document.SHA256) {
		return fail(CodeInputInvalid, fmt.Sprintf("%s binding is incomplete or invalid", name), nil)
	}
	digest := sha256.Sum256(data)
	if document.SHA256 != hex.EncodeToString(digest[:]) {
		return fail(CodeDigestMismatch, fmt.Sprintf("%s exact-byte SHA-256 differs", name), nil)
	}
	return nil
}

type mergeSettingsResponse struct {
	Data   mergeSettingsData `json:"data"`
	Errors json.RawMessage   `json:"errors,omitempty"`
}

type mergeSettingsData struct {
	Repository *mergeSettingsRepository `json:"repository"`
}

type mergeSettingsRepository struct {
	DefaultBranchRef         *defaultBranchRef `json:"defaultBranchRef"`
	MergeCommitAllowed       *bool             `json:"mergeCommitAllowed"`
	RebaseMergeAllowed       *bool             `json:"rebaseMergeAllowed"`
	SquashMergeAllowed       *bool             `json:"squashMergeAllowed"`
	SquashMergeCommitTitle   *string           `json:"squashMergeCommitTitle"`
	SquashMergeCommitMessage *string           `json:"squashMergeCommitMessage"`
	Rulesets                 *graphqlRulesets  `json:"rulesets"`
}

type defaultBranchRef struct {
	Name *string `json:"name"`
}

type graphqlRulesets struct {
	TotalCount *int                   `json:"totalCount"`
	Nodes      *[]*graphqlRulesetNode `json:"nodes"`
	PageInfo   *graphqlPageInfo       `json:"pageInfo"`
}

type graphqlRulesetNode struct {
	DatabaseID   *int64                        `json:"databaseId"`
	Name         *string                       `json:"name"`
	Target       *string                       `json:"target"`
	Enforcement  *string                       `json:"enforcement"`
	Source       *graphqlRulesetSource         `json:"source"`
	BypassActors *graphqlBypassActorConnection `json:"bypassActors"`
}

type graphqlRulesetSource struct {
	TypeName      *string `json:"__typename"`
	NameWithOwner *string `json:"nameWithOwner"`
}

type graphqlBypassActorConnection struct {
	TotalCount *int `json:"totalCount"`
}

type graphqlPageInfo struct {
	HasNextPage *bool `json:"hasNextPage"`
}

func validateMergeSettings(data []byte, repositoryName string, policy settingsPolicy) (map[string]int64, error) {
	var response mergeSettingsResponse
	if err := strictjson.Decode(data, maxJSONBytes, &response); err != nil {
		return nil, fail(CodeInputInvalid, "strictly decode merge settings", err)
	}
	if len(response.Errors) != 0 && !bytes.Equal(bytes.TrimSpace(response.Errors), []byte("null")) {
		return nil, fail(CodePolicyInvalid, "GraphQL merge settings response contains errors", nil)
	}
	repository := response.Data.Repository
	if repository == nil || repository.DefaultBranchRef == nil || repository.DefaultBranchRef.Name == nil || *repository.DefaultBranchRef.Name != policy.DefaultBranch ||
		repository.SquashMergeAllowed == nil || !*repository.SquashMergeAllowed ||
		repository.MergeCommitAllowed == nil || *repository.MergeCommitAllowed ||
		repository.RebaseMergeAllowed == nil || *repository.RebaseMergeAllowed ||
		repository.SquashMergeCommitTitle == nil || *repository.SquashMergeCommitTitle != "PR_TITLE" ||
		repository.SquashMergeCommitMessage == nil || *repository.SquashMergeCommitMessage != "PR_BODY" {
		return nil, fail(CodePolicyInvalid, "repository merge settings do not preserve the canonical squash-only policy", nil)
	}
	return validateGraphQLRulesets(repository.Rulesets, repositoryName, policy.Rulesets)
}

func validateGraphQLRulesets(connection *graphqlRulesets, repository string, rulesets []rulesetIdentity) (map[string]int64, error) {
	if connection == nil || connection.TotalCount == nil || *connection.TotalCount != len(rulesets) ||
		connection.Nodes == nil || len(*connection.Nodes) != len(rulesets) ||
		connection.PageInfo == nil || connection.PageInfo.HasNextPage == nil || *connection.PageInfo.HasNextPage {
		return nil, fail(CodePolicyInvalid, "GraphQL ruleset inventory must be complete, unpaginated, and contain exactly three rulesets", nil)
	}
	ids := make(map[string]int64, len(rulesets))
	for _, node := range *connection.Nodes {
		if node == nil || node.DatabaseID == nil || *node.DatabaseID <= 0 || node.Name == nil || node.Target == nil || node.Enforcement == nil ||
			node.Source == nil || node.Source.TypeName == nil || *node.Source.TypeName != "Repository" || node.Source.NameWithOwner == nil || *node.Source.NameWithOwner != repository ||
			node.BypassActors == nil || node.BypassActors.TotalCount == nil || *node.BypassActors.TotalCount != 0 {
			return nil, fail(CodePolicyInvalid, "GraphQL ruleset node is incomplete, foreign, or bypassable", nil)
		}
		var expected *rulesetIdentity
		for index := range rulesets {
			if rulesets[index].Name == *node.Name {
				expected = &rulesets[index]
				break
			}
		}
		if expected == nil || *node.Target != strings.ToUpper(expected.Target) || *node.Enforcement != "ACTIVE" {
			return nil, fail(CodePolicyInvalid, fmt.Sprintf("GraphQL ruleset %q is not canonical and active", *node.Name), nil)
		}
		if _, duplicate := ids[*node.Name]; duplicate {
			return nil, fail(CodePolicyInvalid, fmt.Sprintf("GraphQL canonical ruleset %q is duplicated", *node.Name), nil)
		}
		ids[*node.Name] = *node.DatabaseID
	}
	if len(ids) != len(rulesets) {
		return nil, fail(CodePolicyInvalid, "GraphQL ruleset inventory omits a canonical ruleset", nil)
	}
	seenIDs := make(map[int64]bool, len(ids))
	for _, expected := range rulesets {
		id, found := ids[expected.Name]
		if !found || seenIDs[id] {
			return nil, fail(CodePolicyInvalid, "GraphQL canonical rulesets do not have distinct positive IDs", nil)
		}
		seenIDs[id] = true
	}
	return ids, nil
}

type rulesetSummary struct {
	ID                   int64           `json:"id"`
	Name                 string          `json:"name"`
	Target               string          `json:"target"`
	SourceType           string          `json:"source_type"`
	Source               string          `json:"source,omitempty"`
	Enforcement          string          `json:"enforcement"`
	NodeID               string          `json:"node_id,omitempty"`
	Links                *rulesetLinks   `json:"_links,omitempty"`
	CreatedAt            string          `json:"created_at,omitempty"`
	UpdatedAt            string          `json:"updated_at,omitempty"`
	BypassActors         *[]bypassActor  `json:"bypass_actors,omitempty"`
	CurrentUserCanBypass *string         `json:"current_user_can_bypass,omitempty"`
	Conditions           json.RawMessage `json:"conditions,omitempty"`
	Rules                json.RawMessage `json:"rules,omitempty"`
}

type rulesetLinks struct {
	Self rulesetLink `json:"self"`
	HTML rulesetLink `json:"html"`
}

type rulesetLink struct {
	Href string `json:"href"`
}

func validateRulesetPages(data []byte, repository string, rulesets []rulesetIdentity) (map[string]int64, error) {
	var pages [][]rulesetSummary
	if err := strictjson.Decode(data, maxJSONBytes, &pages); err != nil {
		return nil, fail(CodeInputInvalid, "strictly decode slurped ruleset pages", err)
	}
	ids := make(map[string]int64, len(rulesets))
	counts := make(map[string]int, len(rulesets))
	for _, page := range pages {
		for _, summary := range page {
			for _, expected := range rulesets {
				if summary.Name == expected.Name && summary.Target == expected.Target && summary.SourceType == "Repository" && summary.Enforcement == "active" {
					if summary.ID <= 0 || (summary.Source != "" && summary.Source != repository) {
						return nil, fail(CodePolicyInvalid, fmt.Sprintf("canonical ruleset %q has an invalid ID or source", expected.Name), nil)
					}
					counts[expected.Name]++
					ids[expected.Name] = summary.ID
				}
			}
		}
	}
	for _, expected := range rulesets {
		if counts[expected.Name] != 1 {
			return nil, fail(CodePolicyInvalid, fmt.Sprintf("canonical active ruleset %q count=%d, want 1", expected.Name, counts[expected.Name]), nil)
		}
	}
	if ids[rulesets[0].Name] == ids[rulesets[1].Name] || ids[rulesets[0].Name] == ids[rulesets[2].Name] || ids[rulesets[1].Name] == ids[rulesets[2].Name] {
		return nil, fail(CodePolicyInvalid, "canonical rulesets do not have distinct IDs", nil)
	}
	return ids, nil
}

type bypassActor struct {
	ActorID    *int64 `json:"actor_id"`
	ActorType  string `json:"actor_type"`
	BypassMode string `json:"bypass_mode"`
}

type rulesetDetail struct {
	ID                   int64           `json:"id"`
	Name                 string          `json:"name"`
	Target               string          `json:"target"`
	SourceType           string          `json:"source_type"`
	Source               string          `json:"source"`
	Enforcement          string          `json:"enforcement"`
	BypassActors         json.RawMessage `json:"bypass_actors,omitempty"`
	CurrentUserCanBypass *string         `json:"current_user_can_bypass"`
	Conditions           *conditions     `json:"conditions"`
	Rules                *[]rule         `json:"rules"`
	NodeID               string          `json:"node_id,omitempty"`
	Links                *rulesetLinks   `json:"_links,omitempty"`
	CreatedAt            string          `json:"created_at,omitempty"`
	UpdatedAt            string          `json:"updated_at,omitempty"`
}

type conditions struct {
	RefName *refNameCondition `json:"ref_name"`
}

type refNameCondition struct {
	Include *[]string `json:"include"`
	Exclude *[]string `json:"exclude"`
}

type rule struct {
	Type       string          `json:"type"`
	Parameters json.RawMessage `json:"parameters,omitempty"`
}

type pullRequestParameters struct {
	RequiredApprovingReviewCount      *int               `json:"required_approving_review_count,omitempty"`
	DismissStaleReviewsOnPush         *bool              `json:"dismiss_stale_reviews_on_push,omitempty"`
	RequireCodeOwnerReview            *bool              `json:"require_code_owner_review,omitempty"`
	RequireLastPushApproval           *bool              `json:"require_last_push_approval,omitempty"`
	RequiredReviewThreadResolution    *bool              `json:"required_review_thread_resolution"`
	AllowedMergeMethods               *[]string          `json:"allowed_merge_methods"`
	RequiredReviewers                 *[]json.RawMessage `json:"required_reviewers"`
	AutomaticCopilotCodeReviewEnabled *bool              `json:"automatic_copilot_code_review_enabled,omitempty"`
	CopilotCodeReviewCount            *int               `json:"copilot_code_review_count,omitempty"`
}

type requiredStatusChecksParameters struct {
	RequiredStatusChecks             *[]requiredStatusCheck `json:"required_status_checks"`
	StrictRequiredStatusChecksPolicy *bool                  `json:"strict_required_status_checks_policy"`
	DoNotEnforceOnCreate             *bool                  `json:"do_not_enforce_on_create"`
}

type requiredStatusCheck struct {
	Context       string `json:"context"`
	IntegrationID *int64 `json:"integration_id"`
}

func validateMainRuleset(data []byte, repository string, expectedID int64, policy settingsPolicy) error {
	detail, err := decodeRulesetDetail(data)
	if err != nil {
		return err
	}
	if err := validateDetailIdentity(detail, expectedID, policy.Rulesets[0], repository, []string{policy.MainRef}); err != nil {
		return err
	}
	rules := *detail.Rules
	if len(rules) != 4 {
		return fail(CodePolicyInvalid, fmt.Sprintf("main ruleset has %d rules, want exactly 4", len(rules)), nil)
	}
	byType, err := uniqueRules(rules, []string{"deletion", "non_fast_forward", "pull_request", "required_status_checks"})
	if err != nil {
		return err
	}
	if len(byType["deletion"].Parameters) != 0 || len(byType["non_fast_forward"].Parameters) != 0 {
		return fail(CodePolicyInvalid, "main deletion and non-fast-forward rules must not have parameters", nil)
	}
	var pull pullRequestParameters
	if err := strictjson.Decode(byType["pull_request"].Parameters, maxJSONBytes, &pull); err != nil {
		return fail(CodeInputInvalid, "strictly decode pull-request rule parameters", err)
	}
	if pull.RequiredReviewThreadResolution == nil || !*pull.RequiredReviewThreadResolution || pull.AllowedMergeMethods == nil || !reflect.DeepEqual(*pull.AllowedMergeMethods, []string{"squash"}) || pull.RequiredReviewers == nil || len(*pull.RequiredReviewers) != 0 {
		return fail(CodePolicyInvalid, "main pull-request rule must resolve threads, allow only squash, and have an explicit empty required-reviewers list", nil)
	}
	var checks requiredStatusChecksParameters
	if err := strictjson.Decode(byType["required_status_checks"].Parameters, maxJSONBytes, &checks); err != nil {
		return fail(CodeInputInvalid, "strictly decode required-status-check parameters", err)
	}
	if checks.StrictRequiredStatusChecksPolicy == nil || !*checks.StrictRequiredStatusChecksPolicy || checks.DoNotEnforceOnCreate == nil || *checks.DoNotEnforceOnCreate || checks.RequiredStatusChecks == nil {
		return fail(CodePolicyInvalid, "main required-status-check policy is incomplete or non-strict", nil)
	}
	actualChecks := make([]string, 0, len(*checks.RequiredStatusChecks))
	for _, check := range *checks.RequiredStatusChecks {
		if check.IntegrationID == nil || *check.IntegrationID != githubActionsIntegrationID {
			return fail(CodePolicyInvalid, fmt.Sprintf("required check %q is not bound to GitHub Actions", check.Context), nil)
		}
		actualChecks = append(actualChecks, check.Context)
	}
	sort.Strings(actualChecks)
	if !reflect.DeepEqual(actualChecks, policy.RequiredChecks) {
		return fail(CodePolicyInvalid, fmt.Sprintf("main required checks=%q, want exact canonical set", actualChecks), nil)
	}
	return nil
}

func settingsPolicyFromContract(contract releasecontract.Contract) (settingsPolicy, error) {
	if err := contract.Validate(); err != nil {
		return settingsPolicy{}, fail(CodeInputInvalid, "release contract is invalid", err)
	}
	names := make([]string, 0, len(contract.MainRequiredChecks))
	for _, check := range contract.MainRequiredChecks {
		names = append(names, check.Name)
	}
	sort.Strings(names)
	product := contract.Naming.Product
	source, ok := contract.RepositoryByID("source")
	if !ok {
		return settingsPolicy{}, fail(CodeInputInvalid, "release contract has no source repository identity", nil)
	}
	tagPrefix := contract.VersionPolicy.TagPrefix
	if contract.SchemaID == releasecontract.LegacySchemaID && contract.SchemaVersion == releasecontract.LegacySchemaVersion {
		tagPrefix = "v"
	}
	return settingsPolicy{
		Repository:    source.FullName,
		DefaultBranch: source.DefaultBranch,
		MainRef:       "refs/heads/" + source.DefaultBranch,
		TagRef:        "refs/tags/" + tagPrefix + "*",
		Rulesets: []rulesetIdentity{
			{Name: "Protect " + product + " " + source.DefaultBranch, Target: "branch"},
			{Name: "Protect " + product + " release tags", Target: "tag"},
			{Name: "Protect " + product + " release evidence", Target: "branch"},
		},
		RequiredChecks: names,
	}, nil
}

func validateTagRuleset(data []byte, repository string, expectedID int64, policy settingsPolicy) error {
	detail, err := decodeRulesetDetail(data)
	if err != nil {
		return err
	}
	if err := validateDetailIdentity(detail, expectedID, policy.Rulesets[1], repository, []string{policy.TagRef}); err != nil {
		return err
	}
	if _, err := uniqueRules(*detail.Rules, []string{"deletion", "update"}); err != nil {
		return err
	}
	for _, item := range *detail.Rules {
		if len(item.Parameters) != 0 {
			return fail(CodePolicyInvalid, "tag protection rules must not have parameters", nil)
		}
	}
	return nil
}

func validateEvidenceRuleset(data []byte, repository string, expectedID int64, policy settingsPolicy) error {
	detail, err := decodeRulesetDetail(data)
	if err != nil {
		return err
	}
	if err := validateDetailIdentity(detail, expectedID, policy.Rulesets[2], repository, []string{"refs/heads/release-evidence"}); err != nil {
		return err
	}
	if _, err := uniqueRules(*detail.Rules, []string{"deletion", "non_fast_forward"}); err != nil {
		return err
	}
	for _, item := range *detail.Rules {
		if len(item.Parameters) != 0 {
			return fail(CodePolicyInvalid, "evidence protection rules must not have parameters", nil)
		}
	}
	return nil
}

func decodeRulesetDetail(data []byte) (rulesetDetail, error) {
	var detail rulesetDetail
	if err := strictjson.Decode(data, maxJSONBytes, &detail); err != nil {
		return rulesetDetail{}, fail(CodeInputInvalid, "strictly decode ruleset detail", err)
	}
	return detail, nil
}

func validateDetailIdentity(detail rulesetDetail, expectedID int64, expected rulesetIdentity, repository string, include []string) error {
	if detail.ID != expectedID || detail.Name != expected.Name || detail.Target != expected.Target || detail.SourceType != "Repository" || detail.Source != repository || detail.Enforcement != "active" {
		return fail(CodePolicyInvalid, fmt.Sprintf("ruleset %q identity is not canonical", expected.Name), nil)
	}
	if len(detail.BypassActors) != 0 {
		var actors []bypassActor
		if err := strictjson.Decode(detail.BypassActors, maxJSONBytes, &actors); err != nil {
			return fail(CodeInputInvalid, fmt.Sprintf("strictly decode ruleset %q bypass actors", expected.Name), err)
		}
		if actors == nil || len(actors) != 0 {
			return fail(CodePolicyInvalid, fmt.Sprintf("ruleset %q bypass actors, when present, must be an empty array", expected.Name), nil)
		}
	}
	if detail.CurrentUserCanBypass == nil || *detail.CurrentUserCanBypass != "never" {
		return fail(CodePolicyInvalid, fmt.Sprintf("ruleset %q current-user bypass policy must be present and never", expected.Name), nil)
	}
	if detail.Conditions == nil || detail.Conditions.RefName == nil || detail.Conditions.RefName.Include == nil || detail.Conditions.RefName.Exclude == nil || !reflect.DeepEqual(*detail.Conditions.RefName.Include, include) || len(*detail.Conditions.RefName.Exclude) != 0 {
		return fail(CodePolicyInvalid, fmt.Sprintf("ruleset %q ref condition is not exact", expected.Name), nil)
	}
	if detail.Rules == nil {
		return fail(CodePolicyInvalid, fmt.Sprintf("ruleset %q rules field is missing", expected.Name), nil)
	}
	return nil
}

func uniqueRules(rules []rule, expected []string) (map[string]rule, error) {
	if len(rules) != len(expected) {
		return nil, fail(CodePolicyInvalid, fmt.Sprintf("rule types count=%d, want %d", len(rules), len(expected)), nil)
	}
	allowed := make(map[string]bool, len(expected))
	for _, name := range expected {
		allowed[name] = true
	}
	result := make(map[string]rule, len(rules))
	for _, item := range rules {
		if !allowed[item.Type] || item.Type == "" {
			return nil, fail(CodePolicyInvalid, fmt.Sprintf("rule type %q is not canonical", item.Type), nil)
		}
		if _, duplicate := result[item.Type]; duplicate {
			return nil, fail(CodePolicyInvalid, fmt.Sprintf("rule type %q is duplicated", item.Type), nil)
		}
		result[item.Type] = item
	}
	return result, nil
}
