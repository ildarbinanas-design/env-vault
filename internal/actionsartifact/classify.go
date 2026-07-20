package actionsartifact

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	DecisionScopeSchemaID      = "env-vault.actions-artifact-decision-scope.v1"
	DecisionScopeSchemaVersion = 1
	DecisionManifestSchemaID   = "env-vault.actions-artifact-decision-manifest.v1"
	DecisionManifestVersion    = 1

	DecisionKeep   = "keep"
	DecisionDelete = "delete"

	ReasonKeepStableRelease = "KEEP_STABLE_RELEASE"
	ReasonKeepRepairInput   = "KEEP_REPAIR_INPUT"
	ReasonKeepAdditional    = "KEEP_ADDITIONAL_IDENTITY"
	ReasonKeepProtectedMain = "KEEP_PROTECTED_MAIN"
	ReasonKeepOpenPRHead    = "KEEP_OPEN_PR_HEAD"
	ReasonKeepDurable       = "KEEP_DURABLE"
	ReasonKeepSystemManaged = "KEEP_SYSTEM_MANAGED"
	ReasonKeepNotProven     = "KEEP_NOT_PROVEN_SUPERSEDED"
	ReasonDeleteSuperseded  = "DELETE_SUPERSEDED"
)

var releaseVersionPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z][0-9A-Za-z.-]*)?$`)

// DecisionScope is deliberately separate from the collected inventory. Its
// live identities are supplied by an operator and never inferred as defaults.
type DecisionScope struct {
	SchemaID                      string                    `json:"schema_id"`
	SchemaVersion                 int                       `json:"schema_version"`
	ObservedAt                    string                    `json:"observed_at"`
	SnapshotSemanticSHA256        string                    `json:"snapshot_semantic_sha256"`
	PolicySemanticSHA256          string                    `json:"policy_semantic_sha256"`
	LiveObservationSemanticSHA256 string                    `json:"live_observation_semantic_sha256"`
	RepairProofSemanticSHA256     string                    `json:"repair_proof_semantic_sha256"`
	Repositories                  []RepositoryDecisionScope `json:"repositories"`
}

type RepositoryDecisionScope struct {
	Repository               string                `json:"repository"`
	ProtectedMainSHA         string                `json:"protected_main_sha"`
	StableRelease            StableReleaseIdentity `json:"stable_release"`
	OpenPullRequests         []PullRequestHead     `json:"open_pull_requests"`
	RepairBoundary           RepairBoundary        `json:"repair_boundary"`
	AdditionalKeepIdentities []ExactKeepIdentity   `json:"additional_keep_identities"`
	DeleteEligibleIdentities []ExactKeepIdentity   `json:"delete_eligible_identities"`
	ImmutableKeepArtifactIDs []int64               `json:"immutable_keep_artifact_ids"`
}

type StableReleaseIdentity struct {
	Enabled   *bool  `json:"enabled"`
	Version   string `json:"version"`
	SourceSHA string `json:"source_sha"`
}

type PullRequestHead struct {
	Number  int64  `json:"number"`
	HeadSHA string `json:"head_sha"`
}

// Closed is a pointer so strict decoding can distinguish an explicit false
// from an omitted value. When the repair boundary is open, at least one exact
// producer identity is mandatory.
type RepairBoundary struct {
	Closed     *bool               `json:"closed"`
	Identities []ExactKeepIdentity `json:"identities"`
}

// ExactKeepIdentity binds one exact producer attempt, never a name prefix,
// branch, workflow family, or guessed current attempt. Repair/additional lists
// use it as keep authority; the delete list uses it as positive supersession
// authority.
type ExactKeepIdentity struct {
	ProducerRunID      int64  `json:"producer_run_id"`
	ProducerRunAttempt int    `json:"producer_run_attempt"`
	WorkflowPath       string `json:"workflow_path"`
	HeadSHA            string `json:"head_sha"`
}

type DecisionManifest struct {
	SchemaID                      string           `json:"schema_id"`
	SchemaVersion                 int              `json:"schema_version"`
	SnapshotSemanticSHA256        string           `json:"snapshot_semantic_sha256"`
	ScopeSemanticSHA256           string           `json:"scope_semantic_sha256"`
	PolicySemanticSHA256          string           `json:"policy_semantic_sha256"`
	LiveObservationSemanticSHA256 string           `json:"live_observation_semantic_sha256"`
	RepairProofSemanticSHA256     string           `json:"repair_proof_semantic_sha256"`
	Records                       []DecisionRecord `json:"records"`
	Totals                        DecisionTotals   `json:"totals"`
	SemanticSHA256                string           `json:"semantic_sha256"`
}

type DecisionRecord struct {
	Repository          string `json:"repository"`
	ArtifactID          int64  `json:"artifact_id"`
	Name                string `json:"name"`
	ArtifactDigest      string `json:"artifact_digest"`
	ProducerRunID       int64  `json:"producer_run_id"`
	ProducerRunAttempt  int    `json:"producer_run_attempt"`
	WorkflowPath        string `json:"workflow_path"`
	HeadSHA             string `json:"head_sha"`
	ReferencedVersion   string `json:"referenced_version"`
	ReferencedSourceSHA string `json:"referenced_source_sha"`
	SizeInBytes         int64  `json:"size_in_bytes"`
	CreatedAt           string `json:"created_at"`
	UpdatedAt           string `json:"updated_at"`
	ExpiresAt           string `json:"expires_at"`
	Expired             bool   `json:"expired"`
	Class               string `json:"class"`
	PolicyPattern       string `json:"policy_pattern"`
	DependencyRationale string `json:"dependency_repair_rationale"`
	Lifecycle           string `json:"lifecycle"`
	Decision            string `json:"decision"`
	ReasonCode          string `json:"reason_code"`
}

type DecisionTotals struct {
	Before        ArtifactTotals `json:"before"`
	Keep          ArtifactTotals `json:"keep"`
	Delete        ArtifactTotals `json:"delete"`
	ExpectedAfter ArtifactTotals `json:"expected_after"`
}

type ArtifactTotals struct {
	Count int   `json:"count"`
	Bytes int64 `json:"bytes"`
}

// ValidateDecisionScope rejects omissions, stale current-state observations,
// unknown repositories, and non-exact authorities that do not bind to this
// snapshot. Stage 3 must independently prove that main, latest release, and
// the complete open-PR set are current before supplying this document.
func ValidateDecisionScope(scope DecisionScope, snapshot Snapshot, now time.Time, maxAge time.Duration) error {
	if scope.SchemaID != DecisionScopeSchemaID || scope.SchemaVersion != DecisionScopeSchemaVersion {
		return fmt.Errorf("decision scope must be %s version %d", DecisionScopeSchemaID, DecisionScopeSchemaVersion)
	}
	if err := validateFreshnessWindow(now, maxAge); err != nil {
		return err
	}
	observed, err := parseCanonicalTime(scope.ObservedAt)
	if err != nil {
		return fmt.Errorf("decision scope observed_at: %w", err)
	}
	snapshotFinished, err := parseCanonicalTime(snapshot.ObservedFinishedAt)
	if err != nil {
		return fmt.Errorf("snapshot observed_finished_at: %w", err)
	}
	if observed.Before(snapshotFinished) || observed.After(now) || now.Sub(observed) > maxAge {
		return errors.New("decision scope observation is stale, future-dated, or predates snapshot collection")
	}
	actualSnapshotDigest, err := snapshotSemanticSHA256(snapshot)
	if err != nil || scope.SnapshotSemanticSHA256 != actualSnapshotDigest || !sha256Pattern.MatchString(scope.PolicySemanticSHA256) || !sha256Pattern.MatchString(scope.LiveObservationSemanticSHA256) || !sha256Pattern.MatchString(scope.RepairProofSemanticSHA256) {
		return errors.New("decision scope snapshot, policy, live-observation, or repair-proof digest binding is invalid")
	}
	if len(scope.Repositories) != len(snapshot.Repositories) || len(scope.Repositories) == 0 {
		return errors.New("decision scope repositories must exactly cover the snapshot")
	}
	snapshotRepositories := make(map[string]SnapshotRepository, len(snapshot.Repositories))
	for _, repository := range snapshot.Repositories {
		snapshotRepositories[repository.Repository] = repository
	}
	attempts := make(map[string]SnapshotAttempt, len(snapshot.Attempts))
	for _, attempt := range snapshot.Attempts {
		attempts[attemptKey(attempt.Repository, attempt.RunID, attempt.RunAttempt)] = attempt
	}
	artifactsByAttempt := make(map[string]bool, len(snapshot.Artifacts))
	for _, artifact := range snapshot.Artifacts {
		artifactsByAttempt[attemptKey(artifact.Repository, artifact.ProducerRunID, artifact.ProducerRunAttempt)] = true
	}

	for index, repository := range scope.Repositories {
		snapshotRepository, exists := snapshotRepositories[repository.Repository]
		if !exists {
			return fmt.Errorf("repositories[%d] is not present in the snapshot", index)
		}
		if index > 0 && scope.Repositories[index-1].Repository >= repository.Repository {
			return errors.New("decision scope repositories must be sorted and unique")
		}
		if !shaPattern.MatchString(repository.ProtectedMainSHA) {
			return fmt.Errorf("repositories[%d] has invalid protected-main identity", index)
		}
		if repository.StableRelease.Enabled == nil {
			return fmt.Errorf("repositories[%d].stable_release.enabled must be explicit", index)
		}
		if *repository.StableRelease.Enabled {
			if !releaseVersionPattern.MatchString(repository.StableRelease.Version) || !shaPattern.MatchString(repository.StableRelease.SourceSHA) {
				return fmt.Errorf("repositories[%d] has invalid enabled stable-release identity", index)
			}
		} else if repository.StableRelease.Version != "" || repository.StableRelease.SourceSHA != "" {
			return fmt.Errorf("repositories[%d] disabled stable release must not contain a fake version/source tuple", index)
		}
		if repository.OpenPullRequests == nil {
			return fmt.Errorf("repositories[%d].open_pull_requests must be explicitly present", index)
		}
		if repository.RepairBoundary.Identities == nil {
			return fmt.Errorf("repositories[%d].repair_boundary.identities must be explicitly present", index)
		}
		if repository.AdditionalKeepIdentities == nil {
			return fmt.Errorf("repositories[%d].additional_keep_identities must be explicitly present", index)
		}
		if repository.DeleteEligibleIdentities == nil {
			return fmt.Errorf("repositories[%d].delete_eligible_identities must be explicitly present", index)
		}
		if repository.ImmutableKeepArtifactIDs == nil {
			return fmt.Errorf("repositories[%d].immutable_keep_artifact_ids must be explicitly present", index)
		}
		runHeads := make(map[string]bool)
		for _, run := range snapshot.Runs {
			if run.Repository == repository.Repository {
				runHeads[run.HeadSHA] = true
			}
		}
		referencedSources := make(map[string]bool)
		for _, artifact := range snapshot.Artifacts {
			if artifact.Repository != repository.Repository {
				continue
			}
			if artifact.ReferencedSourceSHA != "" {
				referencedSources[artifact.ReferencedSourceSHA] = true
			}
			if *repository.StableRelease.Enabled {
				if artifact.ReferencedSourceSHA == repository.StableRelease.SourceSHA && artifact.ReferencedVersion != "" && artifact.ReferencedVersion != repository.StableRelease.Version {
					return fmt.Errorf("repositories[%d] artifact %d references the stable source with a different version", index, artifact.ArtifactID)
				}
				if artifact.ReferencedVersion == repository.StableRelease.Version && artifact.ReferencedSourceSHA != "" && artifact.ReferencedSourceSHA != repository.StableRelease.SourceSHA {
					return fmt.Errorf("repositories[%d] artifact %d references the stable version with a different source", index, artifact.ArtifactID)
				}
				if artifact.HeadSHA == repository.StableRelease.SourceSHA && artifact.ReferencedVersion != "" && artifact.ReferencedVersion != repository.StableRelease.Version {
					return fmt.Errorf("repositories[%d] artifact %d is produced from the stable source with a different version", index, artifact.ArtifactID)
				}
			}
		}
		for prIndex, pullRequest := range repository.OpenPullRequests {
			if pullRequest.Number < 1 || !shaPattern.MatchString(pullRequest.HeadSHA) {
				return fmt.Errorf("repositories[%d].open_pull_requests[%d] is invalid", index, prIndex)
			}
			if prIndex > 0 && repository.OpenPullRequests[prIndex-1].Number >= pullRequest.Number {
				return fmt.Errorf("repositories[%d].open_pull_requests must be sorted and unique by number", index)
			}
		}
		if snapshotRepository.ArtifactCount > 0 {
			if !runHeads[repository.ProtectedMainSHA] {
				return fmt.Errorf("repositories[%d] protected-main SHA is not corroborated by the complete run snapshot", index)
			}
			if *repository.StableRelease.Enabled && !runHeads[repository.StableRelease.SourceSHA] && !referencedSources[repository.StableRelease.SourceSHA] {
				return fmt.Errorf("repositories[%d] stable source SHA is not corroborated by runs or artifact references", index)
			}
			for prIndex, pullRequest := range repository.OpenPullRequests {
				if !runHeads[pullRequest.HeadSHA] {
					return fmt.Errorf("repositories[%d].open_pull_requests[%d] head SHA is not corroborated by the complete run snapshot", index, prIndex)
				}
			}
		}
		if repository.RepairBoundary.Closed == nil {
			return fmt.Errorf("repositories[%d].repair_boundary.closed must be explicit", index)
		}
		if *repository.RepairBoundary.Closed && len(repository.RepairBoundary.Identities) != 0 {
			return fmt.Errorf("repositories[%d] has a closed repair boundary with identities", index)
		}
		if !*repository.RepairBoundary.Closed && len(repository.RepairBoundary.Identities) == 0 {
			return fmt.Errorf("repositories[%d] has an open repair boundary without exact identities", index)
		}
		if err := validateExactKeepIdentities(repository.Repository, fmt.Sprintf("repositories[%d].repair_boundary.identities", index), repository.RepairBoundary.Identities, attempts, artifactsByAttempt); err != nil {
			return err
		}
		if err := validateExactKeepIdentities(repository.Repository, fmt.Sprintf("repositories[%d].additional_keep_identities", index), repository.AdditionalKeepIdentities, attempts, artifactsByAttempt); err != nil {
			return err
		}
		if err := validateExactKeepIdentities(repository.Repository, fmt.Sprintf("repositories[%d].delete_eligible_identities", index), repository.DeleteEligibleIdentities, attempts, artifactsByAttempt); err != nil {
			return err
		}
		repairKeys := make(map[string]bool, len(repository.RepairBoundary.Identities))
		for _, identity := range repository.RepairBoundary.Identities {
			repairKeys[exactKeepIdentityKey(identity)] = true
		}
		for identityIndex, identity := range repository.AdditionalKeepIdentities {
			if repairKeys[exactKeepIdentityKey(identity)] {
				return fmt.Errorf("repositories[%d].additional_keep_identities[%d] overlaps the repair boundary", index, identityIndex)
			}
		}
		additionalKeys := make(map[string]bool, len(repository.AdditionalKeepIdentities))
		for _, identity := range repository.AdditionalKeepIdentities {
			additionalKeys[exactKeepIdentityKey(identity)] = true
		}
		openPullRequestHeads := make(map[string]bool, len(repository.OpenPullRequests))
		for _, pullRequest := range repository.OpenPullRequests {
			openPullRequestHeads[pullRequest.HeadSHA] = true
		}
		for identityIndex, identity := range repository.DeleteEligibleIdentities {
			key := exactKeepIdentityKey(identity)
			if repairKeys[key] || additionalKeys[key] {
				return fmt.Errorf("repositories[%d].delete_eligible_identities[%d] overlaps an exact keep authority", index, identityIndex)
			}
			if identity.HeadSHA == repository.ProtectedMainSHA {
				return fmt.Errorf("repositories[%d].delete_eligible_identities[%d] conflicts with protected-main keep authority", index, identityIndex)
			}
			if *repository.StableRelease.Enabled && identity.HeadSHA == repository.StableRelease.SourceSHA {
				return fmt.Errorf("repositories[%d].delete_eligible_identities[%d] conflicts with stable-release keep authority", index, identityIndex)
			}
			if openPullRequestHeads[identity.HeadSHA] {
				return fmt.Errorf("repositories[%d].delete_eligible_identities[%d] conflicts with open-PR keep authority", index, identityIndex)
			}
			hasSuperseded := false
			for _, artifact := range snapshot.Artifacts {
				if artifact.Repository != repository.Repository || artifact.ProducerRunID != identity.ProducerRunID || artifact.ProducerRunAttempt != identity.ProducerRunAttempt {
					continue
				}
				if *repository.StableRelease.Enabled && artifact.ReferencedSourceSHA == repository.StableRelease.SourceSHA {
					return fmt.Errorf("repositories[%d].delete_eligible_identities[%d] conflicts with a referenced stable-source artifact", index, identityIndex)
				}
				if artifact.Lifecycle == LifecycleSupersededEligible {
					hasSuperseded = true
				}
			}
			if !hasSuperseded {
				return fmt.Errorf("repositories[%d].delete_eligible_identities[%d] has no superseded-eligible artifact", index, identityIndex)
			}
		}
		expectedImmutable := immutableKeepArtifactIDs(snapshot, repository)
		if len(repository.ImmutableKeepArtifactIDs) != len(expectedImmutable) {
			return fmt.Errorf("repositories[%d].immutable_keep_artifact_ids does not equal the derived authority", index)
		}
		for immutableIndex, artifactID := range repository.ImmutableKeepArtifactIDs {
			if artifactID < 1 || (immutableIndex > 0 && repository.ImmutableKeepArtifactIDs[immutableIndex-1] >= artifactID) || artifactID != expectedImmutable[immutableIndex] {
				return fmt.Errorf("repositories[%d].immutable_keep_artifact_ids does not equal the sorted derived authority", index)
			}
		}
	}
	return nil
}

// Classify emits a complete, deterministic decision record for every
// artifact. Unknown or ambiguous inventory has already failed validation and
// can never fall through to delete.
func Classify(snapshot Snapshot, scope DecisionScope, policy Policy, now time.Time, maxAge time.Duration) (DecisionManifest, error) {
	if err := ValidateSnapshot(snapshot, policy, now, maxAge); err != nil {
		return DecisionManifest{}, err
	}
	if err := ValidateDecisionScope(scope, snapshot, now, maxAge); err != nil {
		return DecisionManifest{}, err
	}
	snapshotDigest, err := snapshotSemanticSHA256(snapshot)
	if err != nil {
		return DecisionManifest{}, err
	}
	scopeDigest, err := DecisionScopeSemanticSHA256(scope, snapshot, now, maxAge)
	if err != nil {
		return DecisionManifest{}, err
	}
	policyDigest, err := CanonicalSHA256(policy)
	if err != nil {
		return DecisionManifest{}, err
	}
	if scope.PolicySemanticSHA256 != policyDigest {
		return DecisionManifest{}, errors.New("decision scope policy digest does not match the checked policy")
	}

	manifest := DecisionManifest{
		SchemaID: DecisionManifestSchemaID, SchemaVersion: DecisionManifestVersion,
		SnapshotSemanticSHA256: snapshotDigest, ScopeSemanticSHA256: scopeDigest, PolicySemanticSHA256: policyDigest,
		LiveObservationSemanticSHA256: scope.LiveObservationSemanticSHA256, RepairProofSemanticSHA256: scope.RepairProofSemanticSHA256,
		Records: make([]DecisionRecord, 0, len(snapshot.Artifacts)),
	}
	scopeByRepository := make(map[string]RepositoryDecisionScope, len(scope.Repositories))
	for _, repository := range scope.Repositories {
		scopeByRepository[repository.Repository] = repository
	}
	for _, artifact := range snapshot.Artifacts {
		repositoryScope := scopeByRepository[artifact.Repository]
		decision, reason := classifyArtifact(artifact, repositoryScope)
		record := DecisionRecord{
			Repository: artifact.Repository, ArtifactID: artifact.ArtifactID, Name: artifact.Name, ArtifactDigest: artifact.Digest,
			ProducerRunID: artifact.ProducerRunID, ProducerRunAttempt: artifact.ProducerRunAttempt,
			WorkflowPath: artifact.WorkflowPath, HeadSHA: artifact.HeadSHA,
			ReferencedVersion: artifact.ReferencedVersion, ReferencedSourceSHA: artifact.ReferencedSourceSHA,
			SizeInBytes: artifact.SizeInBytes,
			CreatedAt:   artifact.CreatedAt, UpdatedAt: artifact.UpdatedAt, ExpiresAt: artifact.ExpiresAt, Expired: artifact.Expired,
			Class: artifact.Class, PolicyPattern: artifact.PolicyPattern, DependencyRationale: artifact.DependencyRationale,
			Lifecycle: artifact.Lifecycle, Decision: decision, ReasonCode: reason,
		}
		manifest.Records = append(manifest.Records, record)
		if err := addTotals(&manifest.Totals.Before, artifact.SizeInBytes); err != nil {
			return DecisionManifest{}, err
		}
		switch decision {
		case DecisionKeep:
			if err := addTotals(&manifest.Totals.Keep, artifact.SizeInBytes); err != nil {
				return DecisionManifest{}, err
			}
		case DecisionDelete:
			if err := addTotals(&manifest.Totals.Delete, artifact.SizeInBytes); err != nil {
				return DecisionManifest{}, err
			}
		default:
			return DecisionManifest{}, fmt.Errorf("internal unknown decision %q", decision)
		}
	}
	manifest.Totals.ExpectedAfter = manifest.Totals.Keep
	digest, err := decisionManifestSemanticSHA256(manifest)
	if err != nil {
		return DecisionManifest{}, err
	}
	manifest.SemanticSHA256 = digest
	return manifest, nil
}

// ValidateDecisionManifest replays classification and compares the complete
// semantic document, catching record omission, overlap, total drift, or digest
// substitution.
func ValidateDecisionManifest(manifest DecisionManifest, snapshot Snapshot, scope DecisionScope, policy Policy, now time.Time, maxAge time.Duration) error {
	if manifest.SchemaID != DecisionManifestSchemaID || manifest.SchemaVersion != DecisionManifestVersion {
		return fmt.Errorf("decision manifest must be %s version %d", DecisionManifestSchemaID, DecisionManifestVersion)
	}
	seen := make(map[string]string, len(manifest.Records))
	for index, record := range manifest.Records {
		key := producerKey(record.Repository, record.ArtifactID)
		if previous, exists := seen[key]; exists {
			if previous != record.Decision {
				return fmt.Errorf("artifact %s/%d appears in both keep and delete decisions", record.Repository, record.ArtifactID)
			}
			return fmt.Errorf("duplicate manifest artifact %s/%d", record.Repository, record.ArtifactID)
		}
		seen[key] = record.Decision
		if index > 0 && compareRunIdentity(manifest.Records[index-1].Repository, manifest.Records[index-1].ArtifactID, record.Repository, record.ArtifactID) >= 0 {
			return errors.New("manifest records must be sorted and unique by repository/artifact_id")
		}
	}
	expected, err := Classify(snapshot, scope, policy, now, maxAge)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(manifest, expected) {
		return errors.New("decision manifest does not equal replayed classification or totals do not reconcile")
	}
	return nil
}

func classifyArtifact(artifact SnapshotArtifact, scope RepositoryDecisionScope) (string, string) {
	if artifact.Lifecycle == LifecycleSystemManaged {
		return DecisionKeep, ReasonKeepSystemManaged
	}
	if artifact.Lifecycle == LifecycleDurable {
		return DecisionKeep, ReasonKeepDurable
	}
	if *scope.StableRelease.Enabled && (artifact.HeadSHA == scope.StableRelease.SourceSHA || artifact.ReferencedSourceSHA == scope.StableRelease.SourceSHA) {
		return DecisionKeep, ReasonKeepStableRelease
	}
	for _, identity := range scope.RepairBoundary.Identities {
		if artifact.ProducerRunID == identity.ProducerRunID && artifact.ProducerRunAttempt == identity.ProducerRunAttempt && artifact.WorkflowPath == identity.WorkflowPath && artifact.HeadSHA == identity.HeadSHA {
			return DecisionKeep, ReasonKeepRepairInput
		}
	}
	for _, identity := range scope.AdditionalKeepIdentities {
		if artifact.ProducerRunID == identity.ProducerRunID && artifact.ProducerRunAttempt == identity.ProducerRunAttempt && artifact.WorkflowPath == identity.WorkflowPath && artifact.HeadSHA == identity.HeadSHA {
			return DecisionKeep, ReasonKeepAdditional
		}
	}
	if artifact.HeadSHA == scope.ProtectedMainSHA {
		return DecisionKeep, ReasonKeepProtectedMain
	}
	for _, pullRequest := range scope.OpenPullRequests {
		if artifact.HeadSHA == pullRequest.HeadSHA {
			return DecisionKeep, ReasonKeepOpenPRHead
		}
	}
	for _, identity := range scope.DeleteEligibleIdentities {
		if artifact.ProducerRunID == identity.ProducerRunID && artifact.ProducerRunAttempt == identity.ProducerRunAttempt && artifact.WorkflowPath == identity.WorkflowPath && artifact.HeadSHA == identity.HeadSHA {
			if artifact.Lifecycle == LifecycleSupersededEligible {
				return DecisionDelete, ReasonDeleteSuperseded
			}
			return DecisionKeep, ReasonKeepNotProven
		}
	}
	return DecisionKeep, ReasonKeepNotProven
}

func addTotals(totals *ArtifactTotals, bytes int64) error {
	if bytes < 0 || totals.Bytes > math.MaxInt64-bytes || totals.Count == math.MaxInt {
		return errors.New("decision total overflow")
	}
	totals.Count++
	totals.Bytes += bytes
	return nil
}

func DecisionScopeSemanticSHA256(scope DecisionScope, snapshot Snapshot, now time.Time, maxAge time.Duration) (string, error) {
	if err := ValidateDecisionScope(scope, snapshot, now, maxAge); err != nil {
		return "", err
	}
	return semanticSHA256(scope)
}

func snapshotSemanticSHA256(snapshot Snapshot) (string, error) {
	return semanticSHA256(snapshot)
}

func decisionManifestSemanticSHA256(manifest DecisionManifest) (string, error) {
	payload := struct {
		SchemaID                      string           `json:"schema_id"`
		SchemaVersion                 int              `json:"schema_version"`
		SnapshotSemanticSHA256        string           `json:"snapshot_semantic_sha256"`
		ScopeSemanticSHA256           string           `json:"scope_semantic_sha256"`
		PolicySemanticSHA256          string           `json:"policy_semantic_sha256"`
		LiveObservationSemanticSHA256 string           `json:"live_observation_semantic_sha256"`
		RepairProofSemanticSHA256     string           `json:"repair_proof_semantic_sha256"`
		Records                       []DecisionRecord `json:"records"`
		Totals                        DecisionTotals   `json:"totals"`
	}{
		SchemaID: manifest.SchemaID, SchemaVersion: manifest.SchemaVersion,
		SnapshotSemanticSHA256: manifest.SnapshotSemanticSHA256, ScopeSemanticSHA256: manifest.ScopeSemanticSHA256,
		PolicySemanticSHA256:          manifest.PolicySemanticSHA256,
		LiveObservationSemanticSHA256: manifest.LiveObservationSemanticSHA256, RepairProofSemanticSHA256: manifest.RepairProofSemanticSHA256,
		Records: manifest.Records, Totals: manifest.Totals,
	}
	return semanticSHA256(payload)
}

func semanticSHA256(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode canonical Actions artifact document: %w", err)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func compareExactKeepIdentity(left, right ExactKeepIdentity) int {
	if left.ProducerRunID != right.ProducerRunID {
		if left.ProducerRunID < right.ProducerRunID {
			return -1
		}
		return 1
	}
	if left.ProducerRunAttempt != right.ProducerRunAttempt {
		return left.ProducerRunAttempt - right.ProducerRunAttempt
	}
	if left.WorkflowPath != right.WorkflowPath {
		return strings.Compare(left.WorkflowPath, right.WorkflowPath)
	}
	return strings.Compare(left.HeadSHA, right.HeadSHA)
}

func validateExactKeepIdentities(repository, field string, identities []ExactKeepIdentity, attempts map[string]SnapshotAttempt, artifactsByAttempt map[string]bool) error {
	for index, identity := range identities {
		if identity.ProducerRunID < 1 || identity.ProducerRunAttempt < 1 || identity.ProducerRunAttempt > MaxRunAttempts || validateWorkflowPath(identity.WorkflowPath) != nil || !shaPattern.MatchString(identity.HeadSHA) {
			return fmt.Errorf("%s[%d] is invalid", field, index)
		}
		if index > 0 && compareExactKeepIdentity(identities[index-1], identity) >= 0 {
			return fmt.Errorf("%s must be canonically sorted and unique", field)
		}
		key := attemptKey(repository, identity.ProducerRunID, identity.ProducerRunAttempt)
		attempt, ok := attempts[key]
		if !ok || !artifactsByAttempt[key] {
			return fmt.Errorf("%s[%d] does not bind to an artifact-producing snapshot attempt", field, index)
		}
		if attempt.WorkflowPath != identity.WorkflowPath || attempt.HeadSHA != identity.HeadSHA {
			return fmt.Errorf("%s[%d] path/head does not corroborate the exact attempt", field, index)
		}
	}
	return nil
}

func exactKeepIdentityKey(identity ExactKeepIdentity) string {
	return fmt.Sprintf("%d\x00%d\x00%s\x00%s", identity.ProducerRunID, identity.ProducerRunAttempt, identity.WorkflowPath, identity.HeadSHA)
}

func sortDecisionScope(scope *DecisionScope) {
	sort.Slice(scope.Repositories, func(i, j int) bool { return scope.Repositories[i].Repository < scope.Repositories[j].Repository })
	for index := range scope.Repositories {
		repository := &scope.Repositories[index]
		sort.Slice(repository.OpenPullRequests, func(i, j int) bool {
			return repository.OpenPullRequests[i].Number < repository.OpenPullRequests[j].Number
		})
		sort.Slice(repository.RepairBoundary.Identities, func(i, j int) bool {
			return compareExactKeepIdentity(repository.RepairBoundary.Identities[i], repository.RepairBoundary.Identities[j]) < 0
		})
		sort.Slice(repository.AdditionalKeepIdentities, func(i, j int) bool {
			return compareExactKeepIdentity(repository.AdditionalKeepIdentities[i], repository.AdditionalKeepIdentities[j]) < 0
		})
		sort.Slice(repository.DeleteEligibleIdentities, func(i, j int) bool {
			return compareExactKeepIdentity(repository.DeleteEligibleIdentities[i], repository.DeleteEligibleIdentities[j]) < 0
		})
		sort.Slice(repository.ImmutableKeepArtifactIDs, func(i, j int) bool {
			return repository.ImmutableKeepArtifactIDs[i] < repository.ImmutableKeepArtifactIDs[j]
		})
	}
}

func immutableKeepArtifactIDs(snapshot Snapshot, scope RepositoryDecisionScope) []int64 {
	openHeads := make(map[string]bool, len(scope.OpenPullRequests))
	for _, pullRequest := range scope.OpenPullRequests {
		openHeads[pullRequest.HeadSHA] = true
	}
	exactKeep := make(map[string]bool, len(scope.RepairBoundary.Identities)+len(scope.AdditionalKeepIdentities))
	for _, identity := range scope.RepairBoundary.Identities {
		exactKeep[exactKeepIdentityKey(identity)] = true
	}
	for _, identity := range scope.AdditionalKeepIdentities {
		exactKeep[exactKeepIdentityKey(identity)] = true
	}
	result := make([]int64, 0)
	for _, artifact := range snapshot.Artifacts {
		if artifact.Repository != scope.Repository {
			continue
		}
		identity := ExactKeepIdentity{ProducerRunID: artifact.ProducerRunID, ProducerRunAttempt: artifact.ProducerRunAttempt, WorkflowPath: artifact.WorkflowPath, HeadSHA: artifact.HeadSHA}
		stable := scope.StableRelease.Enabled != nil && *scope.StableRelease.Enabled && (artifact.HeadSHA == scope.StableRelease.SourceSHA || artifact.ReferencedSourceSHA == scope.StableRelease.SourceSHA)
		if artifact.Lifecycle == LifecycleDurable || artifact.Lifecycle == LifecycleSystemManaged || stable || artifact.HeadSHA == scope.ProtectedMainSHA || openHeads[artifact.HeadSHA] || exactKeep[exactKeepIdentityKey(identity)] {
			result = append(result, artifact.ArtifactID)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}
