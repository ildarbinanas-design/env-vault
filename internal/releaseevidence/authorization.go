package releaseevidence

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"

	"github.com/ildarbinanas-design/env-vault/internal/releasecontract"
	"github.com/ildarbinanas-design/env-vault/internal/releasepromotion"
)

var githubUserPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)

// ValidateAuthorization fails closed unless the saved checkpoint and all
// relevant workflow identities match the release contract and exact promotion
// tuple. The pull-request CI head is only syntax-checked because GitHub may use
// a synthetic merge SHA for pull_request runs; its pull-request association is
// separately bound to the exact generated PR number and head SHA.
func ValidateAuthorization(contract releasecontract.Contract, authorization Authorization, manifest releasepromotion.Manifest) error {
	if authorization.SchemaID != contract.Schemas["release_authorization"] || authorization.SchemaVersion != AuthorizationSchemaVersion || authorization.Result != "pass" {
		return fail(CodeInputInvalid, "release authorization schema or result is invalid", nil)
	}
	if !validRepository(authorization.Repository) || !releasecontract.IsVersion(authorization.ReleaseVersion) || !shaPattern.MatchString(authorization.ReleaseSourceSHA) || authorization.Repository != manifest.Repository || authorization.ReleaseVersion != manifest.ReleaseVersion || authorization.ReleaseSourceSHA != manifest.SourceSHA {
		return fail(CodeSourceMismatch, "release authorization differs from the promotion tuple", nil)
	}
	generatedPR := authorization.GeneratedReleasePR
	if generatedPR.Number <= 0 || !shaPattern.MatchString(generatedPR.HeadSHA) || generatedPR.MergeSHA != authorization.ReleaseSourceSHA {
		return fail(CodeInputInvalid, "generated release PR identity is invalid", nil)
	}
	mergedAt, err := canonicalTime(generatedPR.MergedAt)
	if err != nil {
		return fail(CodeInputInvalid, "generated release PR merge time is invalid", err)
	}
	confirmation := authorization.Confirmation
	createdAt, createdErr := canonicalTime(confirmation.CreatedAt)
	updatedAt, updatedErr := canonicalTime(confirmation.UpdatedAt)
	canonicalBody := fmt.Sprintf("ПОДТВЕРЖДАЮ RELEASE %s PR #%d SHA %s", authorization.ReleaseVersion, generatedPR.Number, generatedPR.HeadSHA)
	bodyDigest := sha256.Sum256([]byte(canonicalBody))
	expectedBodyDigest := hex.EncodeToString(bodyDigest[:])
	expectedURL := fmt.Sprintf("https://github.com/%s/pull/%d#issuecomment-%d", authorization.Repository, generatedPR.Number, confirmation.CommentID)
	if confirmation.CommentID <= 0 || confirmation.URL != expectedURL || !githubUserPattern.MatchString(confirmation.Actor) ||
		(confirmation.ActorAssociation != "OWNER" && confirmation.ActorAssociation != "MEMBER") ||
		createdErr != nil || updatedErr != nil || createdAt.After(updatedAt) || !createdAt.Before(mergedAt) || !updatedAt.Before(mergedAt) ||
		confirmation.BodySHA256 != expectedBodyDigest {
		return fail(CodeInputInvalid, "exact pre-merge release confirmation is invalid", nil)
	}
	planning, ok := contract.WorkflowByID("planning")
	if !ok {
		return fail(CodeInputIncomplete, "release contract has no planning workflow", nil)
	}
	if err := validateCompletedContractWorkflow(authorization.PlanningWorkflow, planning, authorization.ReleaseSourceSHA); err != nil {
		return fail(CodeSourceMismatch, "planning workflow identity is invalid", err)
	}
	ciWorkflow, ok := contract.WorkflowByID("ci")
	if !ok {
		return fail(CodeInputIncomplete, "release contract has no CI workflow", nil)
	}
	prCI := authorization.ReleasePRCI
	if prCI.ID != ciWorkflow.ID || prCI.Name != ciWorkflow.Name || prCI.File != ciWorkflow.File ||
		prCI.RunID <= 0 || prCI.RunAttempt <= 0 || prCI.Event != "pull_request" || !shaPattern.MatchString(prCI.HeadSHA) ||
		prCI.PullRequestNumber != generatedPR.Number || prCI.GeneratedReleasePRHeadSHA != generatedPR.HeadSHA ||
		prCI.Conclusion != "success" {
		return fail(CodeInputInvalid, "release PR CI identity is invalid", nil)
	}
	evidenceWorkflow, ok := contract.WorkflowByID("release_evidence")
	if !ok {
		return fail(CodeInputIncomplete, "release contract has no evidence workflow", nil)
	}
	if err := validateContractWorkflowInvocation(authorization.EvidenceWorkflow, evidenceWorkflow); err != nil {
		return fail(CodeSourceMismatch, "evidence workflow identity is invalid", err)
	}
	return nil
}

func validateCompletedContractWorkflow(actual CompletedContractWorkflow, expected releasecontract.Workflow, sourceSHA string) error {
	if actual.ID != expected.ID || actual.Name != expected.Name || actual.File != expected.File {
		return fmt.Errorf("workflow contract identity does not match %q", expected.ID)
	}
	if actual.RunID <= 0 || actual.RunAttempt <= 0 || actual.HeadSHA != sourceSHA || !shaPattern.MatchString(actual.HeadSHA) || actual.Conclusion != "success" {
		return fmt.Errorf("workflow run is not a successful exact-source attempt")
	}
	return nil
}

func validateContractWorkflowInvocation(actual ContractWorkflowInvocation, expected releasecontract.Workflow) error {
	if actual.ID != expected.ID || actual.Name != expected.Name || actual.File != expected.File {
		return fmt.Errorf("workflow contract identity does not match %q", expected.ID)
	}
	if actual.RunID <= 0 || actual.RunAttempt <= 0 {
		return fmt.Errorf("workflow invocation run ID and attempt must be positive")
	}
	return nil
}
