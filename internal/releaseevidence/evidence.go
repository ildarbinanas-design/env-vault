package releaseevidence

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ildarbinanas-design/env-vault/internal/releasecontract"
	"github.com/ildarbinanas-design/env-vault/internal/releasemetrics"
	"github.com/ildarbinanas-design/env-vault/internal/releasepromotion"
	"github.com/ildarbinanas-design/env-vault/internal/releasesettings"
)

const (
	CodeInputInvalid                  = "INPUT_INVALID"
	CodeInputIncomplete               = "INPUT_INCOMPLETE"
	CodeDigestMismatch                = "DIGEST_MISMATCH"
	CodeVersionMismatch               = "VERSION_MISMATCH"
	CodeSourceMismatch                = "SOURCE_MISMATCH"
	CodeAttestationVerificationFailed = "ATTESTATION_VERIFICATION_FAILED"
	CodeHomebrewStateInvalid          = "HOMEBREW_STATE_INVALID"
)

var (
	shaPattern        = regexp.MustCompile(`^[0-9a-f]{40}$`)
	digestPattern     = regexp.MustCompile(`^[0-9a-f]{64}$`)
	repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
)

type CodedError struct {
	Code   string
	Detail string
	Err    error
}

func (e *CodedError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Detail, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Detail)
}

func (e *CodedError) Unwrap() error { return e.Err }

func ErrorCode(err error) string {
	var coded *CodedError
	if errors.As(err, &coded) {
		return coded.Code
	}
	return ""
}

func fail(code, detail string, err error) error {
	return &CodedError{Code: code, Detail: detail, Err: err}
}

// Assemble validates every input before copying it into a canonical evidence
// record. The promotion manifest is embedded so later verification does not
// depend on a mutable or expired workflow artifact.
func Assemble(contract releasecontract.Contract, authorization Authorization, manifest releasepromotion.Manifest, ciMetrics, publisherMetrics releasemetrics.Metrics, observation Observation, attestationBundle AttestationVerificationBundle) (Evidence, error) {
	if err := validateInputs(contract, authorization, manifest, ciMetrics, publisherMetrics, observation, attestationBundle); err != nil {
		return Evidence{}, err
	}

	authorizationCopy, err := cloneJSON(authorization)
	if err != nil {
		return Evidence{}, fail(CodeInputInvalid, "clone release authorization", err)
	}
	manifestCopy, err := cloneJSON(manifest)
	if err != nil {
		return Evidence{}, fail(CodeInputInvalid, "clone promotion manifest", err)
	}
	ciCopy, err := cloneJSON(ciMetrics)
	if err != nil {
		return Evidence{}, fail(CodeInputInvalid, "clone CI metrics", err)
	}
	publisherCopy, err := cloneJSON(publisherMetrics)
	if err != nil {
		return Evidence{}, fail(CodeInputInvalid, "clone publisher metrics", err)
	}
	observationCopy, err := cloneJSON(observation)
	if err != nil {
		return Evidence{}, fail(CodeInputInvalid, "clone release observation", err)
	}
	attestationBundleCopy, err := cloneJSON(attestationBundle)
	if err != nil {
		return Evidence{}, fail(CodeInputInvalid, "clone attestation verification bundle", err)
	}

	evidence := Evidence{
		SchemaID:                      SchemaID,
		SchemaVersion:                 SchemaVersion,
		Repository:                    manifest.Repository,
		ReleaseVersion:                manifest.ReleaseVersion,
		SourceSHA:                     manifest.SourceSHA,
		PublisherRepairMode:           observationCopy.PublisherRepairMode,
		ObservedAt:                    observation.ObservedAt,
		Result:                        "pass",
		Authorization:                 authorizationCopy,
		Tag:                           observationCopy.Tag,
		Release:                       observationCopy.Release,
		Promotion:                     PromotionRecord{ManifestSHA256: manifest.ManifestSHA256, Manifest: manifestCopy},
		CIMetrics:                     ciCopy,
		PublisherMetrics:              publisherCopy,
		Assets:                        append([]releasepromotion.FileDigest(nil), manifest.Assets...),
		Attestations:                  observationCopy.Attestations,
		AttestationVerificationBundle: attestationBundleCopy,
		Homebrew:                      observationCopy.Homebrew,
		BlockedVersions:               observationCopy.BlockedVersions,
		AbandonedRelease:              observationCopy.AbandonedRelease,
		RepositoryReleaseSettings:     observationCopy.RepositoryReleaseSettings,
		Health:                        observationCopy.Health,
	}
	evidence.EvidenceSHA256, err = EvidenceSHA256(evidence)
	if err != nil {
		return Evidence{}, fail(CodeDigestMismatch, "seal release evidence", err)
	}
	return evidence, nil
}

// Verify replays all offline validation, including the embedded promotion
// manifest and the evidence self-digest.
func Verify(evidence Evidence, contract releasecontract.Contract) error {
	if evidence.SchemaID != SchemaID || evidence.SchemaVersion != SchemaVersion || evidence.Result != "pass" {
		return fail(CodeInputInvalid, "release evidence schema or result is invalid", nil)
	}
	if contract.Schemas["release_evidence"] != SchemaID {
		return fail(CodeInputInvalid, "release contract does not declare the evidence schema", nil)
	}
	if err := contract.ValidateHistoricalEvidenceIdentity(evidence.Repository, evidence.ReleaseVersion, evidence.SourceSHA, evidence.EvidenceSHA256); err != nil {
		return fail(CodeInputInvalid, "historical evidence compatibility binding differs", err)
	}
	manifest := evidence.Promotion.Manifest
	if evidence.Promotion.ManifestSHA256 != manifest.ManifestSHA256 {
		return fail(CodeDigestMismatch, "promotion manifest record digest is inconsistent", nil)
	}
	observation := Observation{
		SchemaID: ObservationSchemaID, SchemaVersion: ObservationSchemaVersion,
		Repository: evidence.Repository, ReleaseVersion: evidence.ReleaseVersion,
		SourceSHA: evidence.SourceSHA, PublisherRepairMode: evidence.PublisherRepairMode,
		ObservedAt: evidence.ObservedAt,
		Tag:        evidence.Tag, Release: evidence.Release, Attestations: evidence.Attestations,
		Homebrew: evidence.Homebrew, BlockedVersions: evidence.BlockedVersions,
		AbandonedRelease:          evidence.AbandonedRelease,
		RepositoryReleaseSettings: evidence.RepositoryReleaseSettings, Health: evidence.Health,
	}
	if err := validateInputs(contract, evidence.Authorization, manifest, evidence.CIMetrics, evidence.PublisherMetrics, observation, evidence.AttestationVerificationBundle); err != nil {
		return err
	}
	if evidence.Repository != manifest.Repository || evidence.ReleaseVersion != manifest.ReleaseVersion || evidence.SourceSHA != manifest.SourceSHA {
		return fail(CodeSourceMismatch, "evidence tuple differs from the embedded promotion manifest", nil)
	}
	if len(evidence.Assets) != len(manifest.Assets) {
		return fail(CodeInputIncomplete, "evidence asset inventory is incomplete", nil)
	}
	for index := range manifest.Assets {
		if evidence.Assets[index] != manifest.Assets[index] {
			return fail(CodeDigestMismatch, "evidence asset inventory differs from the promotion manifest", nil)
		}
	}
	want, err := EvidenceSHA256(evidence)
	if err != nil {
		return fail(CodeDigestMismatch, "hash release evidence", err)
	}
	if evidence.EvidenceSHA256 != want || !digestPattern.MatchString(evidence.EvidenceSHA256) {
		return fail(CodeDigestMismatch, "release evidence self-digest mismatch", nil)
	}
	return nil
}

func validateInputs(contract releasecontract.Contract, authorization Authorization, manifest releasepromotion.Manifest, ciMetrics, publisherMetrics releasemetrics.Metrics, observation Observation, attestationBundle AttestationVerificationBundle) error {
	if err := contract.Validate(); err != nil {
		return fail(CodeInputInvalid, "release contract is invalid", err)
	}
	if contract.Schemas["release_evidence"] != SchemaID ||
		contract.Schemas["release_authorization"] != AuthorizationSchemaID ||
		contract.Schemas["release_observation"] != ObservationSchemaID ||
		contract.Schemas["release_health_proof"] != HealthProofSchemaID ||
		contract.Schemas["repository_release_settings_proof"] != releasesettings.SchemaID ||
		contract.Schemas["attestation_verification_bundle"] != AttestationVerificationBundleSchemaID ||
		contract.Schemas["release_metrics"] != releasemetrics.SchemaID {
		return fail(CodeInputInvalid, "release contract schema identities are incompatible", nil)
	}
	if err := validatePromotion(contract, manifest); err != nil {
		return err
	}
	if err := ValidateAuthorization(contract, authorization, manifest); err != nil {
		return err
	}
	ciWorkflow, ok := contract.WorkflowByID("ci")
	if !ok {
		return fail(CodeInputIncomplete, "release contract has no CI workflow", nil)
	}
	if err := validateMetrics(ciMetrics, ciWorkflow.Name, map[string]bool{"push": true}, manifest.SourceSHA); err != nil {
		return fail(CodeInputInvalid, "CI metrics are invalid", err)
	}
	if ciMetrics.RunID != manifest.Workflow.RunID || ciMetrics.Attempt != manifest.Workflow.RunAttempt {
		return fail(CodeSourceMismatch, "CI metrics differ from the promotion attempt", nil)
	}
	publisherWorkflow, ok := contract.WorkflowByID("publisher")
	if !ok {
		return fail(CodeInputIncomplete, "release contract has no publisher workflow", nil)
	}
	if err := validateMetrics(publisherMetrics, publisherWorkflow.Name, map[string]bool{"push": true, "workflow_dispatch": true}, manifest.SourceSHA); err != nil {
		return fail(CodeInputInvalid, "publisher metrics are invalid", err)
	}
	if authorization.ReleaseSourceSHA != publisherMetrics.HeadSHA || authorization.ReleaseSourceSHA != observation.SourceSHA {
		return fail(CodeSourceMismatch, "release authorization differs from publisher or post-release observation source", nil)
	}
	if err := validateObservation(contract, authorization, manifest, publisherMetrics, observation); err != nil {
		return err
	}
	return ValidateAttestationVerificationBundle(contract, manifest, observation.Attestations, attestationBundle)
}

func validatePromotion(contract releasecontract.Contract, manifest releasepromotion.Manifest) error {
	if manifest.SchemaID != contract.Schemas["promotion_manifest"] || manifest.SchemaVersion != releasepromotion.SchemaVersion || manifest.Result != "pass" {
		return fail(CodeInputInvalid, "promotion manifest schema or result is invalid", nil)
	}
	if !releasecontract.IsVersion(manifest.ReleaseVersion) {
		return fail(CodeVersionMismatch, "promotion version is not exact vMAJOR.MINOR.PATCH", nil)
	}
	if !validRepository(manifest.Repository) || !shaPattern.MatchString(manifest.SourceSHA) {
		return fail(CodeSourceMismatch, "promotion repository or source SHA is invalid", nil)
	}
	workflow, ok := contract.WorkflowByID("ci")
	if !ok || manifest.Workflow.ID != workflow.ID || manifest.Workflow.Name != workflow.Name || manifest.Workflow.File != workflow.File || manifest.Workflow.Event != "push" || manifest.Workflow.HeadSHA != manifest.SourceSHA || manifest.Workflow.RunID <= 0 || manifest.Workflow.RunAttempt <= 0 {
		return fail(CodeSourceMismatch, "promotion workflow does not bind exact CI", nil)
	}
	if manifest.ContractSchema != contract.SchemaID || !digestPattern.MatchString(manifest.ContractSHA256) {
		return fail(CodeDigestMismatch, "promotion contract file binding is invalid", nil)
	}
	semantic, err := releasecontract.SemanticSHA256(contract)
	if err != nil {
		return fail(CodeDigestMismatch, "hash semantic release contract", err)
	}
	if manifest.ContractSemanticSHA256 != semantic {
		return fail(CodeDigestMismatch, "promotion semantic contract binding differs", nil)
	}
	if _, err := canonicalTime(manifest.CreatedAt); err != nil {
		return fail(CodeInputInvalid, "promotion created_at is invalid", err)
	}
	if err := releasepromotion.ValidateSourceQualityProof(manifest.SourceQuality.Proof, contract); err != nil {
		return fail(CodeInputInvalid, "source-quality proof is invalid", err)
	}
	if !validFileDigest(manifest.SourceQuality.File) {
		return fail(CodeDigestMismatch, "source-quality file binding is invalid", nil)
	}
	sourceQuality := manifest.SourceQuality.Proof
	if sourceQuality.SourceSHA != manifest.SourceSHA || sourceQuality.ReleaseVersion != manifest.ReleaseVersion || sourceQuality.Repository != manifest.Repository || sourceQuality.Workflow != manifest.Workflow {
		return fail(CodeSourceMismatch, "source-quality proof differs from the manifest tuple", nil)
	}
	if !validFileDigest(manifest.E2EMatrix.File) {
		return fail(CodeDigestMismatch, "E2E matrix file binding is invalid", nil)
	}
	matrix := manifest.E2EMatrix.Proof
	if err := matrix.Validate(contract); err != nil {
		return fail(CodeInputInvalid, "E2E matrix proof is invalid", err)
	}
	if matrix.Phase != "candidate" || matrix.Run.CommitSHA != manifest.SourceSHA || matrix.Run.Repository != manifest.Repository || matrix.Run.RunID != strconv.FormatInt(manifest.Workflow.RunID, 10) || matrix.Run.RunAttempt != strconv.Itoa(manifest.Workflow.RunAttempt) || matrix.SuiteHash != manifest.SemanticSuiteHash {
		return fail(CodeSourceMismatch, "E2E matrix differs from the manifest tuple", nil)
	}
	if err := releasepromotion.ValidateEmbeddedProofFiles(manifest); err != nil {
		return fail(CodeDigestMismatch, "promotion embedded proof file binding is invalid", err)
	}
	if len(manifest.Platforms) != len(contract.Platforms) || len(manifest.Assets) != len(contract.Assets) {
		return fail(CodeInputIncomplete, "promotion platform or asset inventory is incomplete", nil)
	}
	assetByName := make(map[string]releasepromotion.FileDigest, len(contract.Assets))
	for index, platform := range contract.Platforms {
		proof := manifest.Platforms[index]
		if proof.PlatformID != platform.ID {
			return fail(CodeInputInvalid, "promotion platform order differs from the contract", nil)
		}
		if err := releasepromotion.ValidatePlatformProof(proof, contract); err != nil {
			return fail(CodeInputInvalid, "promotion platform proof is invalid", err)
		}
		if proof.SourceSHA != manifest.SourceSHA || proof.ReleaseVersion != manifest.ReleaseVersion || proof.Repository != manifest.Repository || proof.RunID != manifest.Workflow.RunID || proof.RunAttempt != manifest.Workflow.RunAttempt {
			return fail(CodeSourceMismatch, "platform proof differs from the manifest tuple", nil)
		}
		e2e := matrix.PlatformEvidence[index]
		if e2e.ID != platform.ID || e2e.Artifact.Archive != proof.Archive.Name || e2e.Artifact.Checksum != proof.Checksum.Name || e2e.Artifact.SHA256 != proof.Archive.SHA256 || e2e.BinarySHA256 != proof.Binary.SHA256 {
			return fail(CodeDigestMismatch, "native platform proof differs from E2E evidence", nil)
		}
		for _, asset := range []releasepromotion.FileDigest{proof.Archive, proof.Checksum} {
			if _, duplicate := assetByName[asset.Name]; duplicate {
				return fail(CodeInputInvalid, "promotion contains a duplicate asset", nil)
			}
			assetByName[asset.Name] = asset
		}
	}
	for index, name := range contract.Assets {
		asset := manifest.Assets[index]
		if asset.Name != name || !validFileDigest(asset) || assetByName[name] != asset {
			return fail(CodeDigestMismatch, "promotion asset inventory differs from native proofs", nil)
		}
	}
	want, err := releasepromotion.ManifestSHA256(manifest)
	if err != nil {
		return fail(CodeDigestMismatch, "hash promotion manifest", err)
	}
	if manifest.ManifestSHA256 != want || !digestPattern.MatchString(want) {
		return fail(CodeDigestMismatch, "promotion manifest self-digest mismatch", nil)
	}
	return nil
}

func validateObservation(contract releasecontract.Contract, authorization Authorization, manifest releasepromotion.Manifest, publisherMetrics releasemetrics.Metrics, observation Observation) error {
	if observation.SchemaID != contract.Schemas["release_observation"] || observation.SchemaVersion != ObservationSchemaVersion {
		return fail(CodeInputInvalid, "release observation schema is invalid", nil)
	}
	if observation.Repository != manifest.Repository || observation.ReleaseVersion != manifest.ReleaseVersion || observation.SourceSHA != manifest.SourceSHA {
		return fail(CodeSourceMismatch, "release observation differs from the promotion tuple", nil)
	}
	if !validPublisherRepairMode(observation.PublisherRepairMode, publisherMetrics.Event) {
		return fail(CodeInputInvalid, "publisher repair mode does not match the publisher event", nil)
	}
	observedAt, err := canonicalTime(observation.ObservedAt)
	if err != nil {
		return fail(CodeInputInvalid, "observed_at is invalid", err)
	}
	if err := validateTag(manifest, observation.Tag); err != nil {
		return err
	}
	if err := validateRelease(contract, manifest, observation.Release, observedAt); err != nil {
		return err
	}
	if err := validateAttestations(contract, manifest, observation.Attestations, observedAt); err != nil {
		return err
	}
	if err := validateHomebrew(contract, manifest, observation.Homebrew); err != nil {
		return err
	}
	if err := validateBlockedVersions(contract, observation.BlockedVersions); err != nil {
		return err
	}
	if err := validateAbandonedRelease(contract, manifest, observation.AbandonedRelease, observedAt); err != nil {
		return err
	}
	if err := validateRepositoryReleaseSettings(contract, authorization, observation.RepositoryReleaseSettings, observedAt); err != nil {
		return err
	}
	if err := ValidateHealthProof(observation.Health, contract, manifest, publisherMetrics, observedAt); err != nil {
		return err
	}
	return nil
}

func validPublisherRepairMode(mode, event string) bool {
	if event == "push" {
		return mode == "none"
	}
	if event != "workflow_dispatch" {
		return false
	}
	switch mode {
	case "release-assets", "homebrew", "health":
		return true
	default:
		return false
	}
}

func validateRepositoryReleaseSettings(contract releasecontract.Contract, authorization Authorization, proof releasesettings.Proof, observedAt time.Time) error {
	if contract.Schemas["repository_release_settings_proof"] != releasesettings.SchemaID {
		return fail(CodeInputInvalid, "release contract does not declare the repository release-settings proof schema", nil)
	}
	expected := releasesettings.Tuple{
		Repository:         authorization.Repository,
		SourceSHA:          authorization.ReleaseSourceSHA,
		ReleaseVersion:     authorization.ReleaseVersion,
		PlanningRunID:      authorization.PlanningWorkflow.RunID,
		PlanningRunAttempt: authorization.PlanningWorkflow.RunAttempt,
		CheckedAt:          proof.Tuple.CheckedAt,
	}
	if err := releasesettings.Verify(contract, proof, expected); err != nil {
		code := CodeInputInvalid
		switch releasesettings.ErrorCode(err) {
		case releasesettings.CodeDigestMismatch:
			code = CodeDigestMismatch
		case releasesettings.CodeTupleMismatch:
			code = CodeSourceMismatch
		}
		return fail(code, "repository release-settings proof is invalid or differs from the authorization tuple", err)
	}
	checkedAt, err := canonicalTime(proof.Tuple.CheckedAt)
	if err != nil || checkedAt.After(observedAt) {
		return fail(CodeInputInvalid, "repository release-settings proof check time is after the release observation", err)
	}
	return nil
}

func validateTag(manifest releasepromotion.Manifest, tag TagObservation) error {
	if tag.Name != manifest.ReleaseVersion || !shaPattern.MatchString(tag.RefSHA) || tag.TargetSHA != manifest.SourceSHA || !tag.Immutable || !tag.RulesetProtected {
		return fail(CodeSourceMismatch, "tag is not immutable, protected, and exact-source bound", nil)
	}
	return nil
}

func validateRelease(contract releasecontract.Contract, manifest releasepromotion.Manifest, release ReleaseObservation, observedAt time.Time) error {
	wantURL := "https://github.com/" + manifest.Repository + "/releases/tag/" + manifest.ReleaseVersion
	if release.State != "published" || release.URL != wantURL || release.TagName != manifest.ReleaseVersion || release.TargetSHA != manifest.SourceSHA || release.Draft || release.Prerelease || !release.NoClobberVerified {
		return fail(CodeInputInvalid, "GitHub Release state is not exact and stable", nil)
	}
	publishedAt, err := canonicalTime(release.PublishedAt)
	if err != nil || publishedAt.After(observedAt) {
		return fail(CodeInputInvalid, "release published_at is invalid", err)
	}
	promotionCreatedAt, err := canonicalTime(manifest.CreatedAt)
	if err != nil || publishedAt.Before(promotionCreatedAt) {
		return fail(CodeInputInvalid, "release predates its promotion manifest", err)
	}
	if len(release.Assets) != len(contract.Assets) || len(release.Assets) != len(manifest.Assets) {
		return fail(CodeInputIncomplete, "published Release asset inventory is incomplete", nil)
	}
	for index, name := range contract.Assets {
		actual, expected := release.Assets[index], manifest.Assets[index]
		if actual.Name != name || actual.Name != expected.Name || actual.Size != expected.Size || actual.SHA256 != expected.SHA256 || !validObservedAsset(actual) {
			return fail(CodeDigestMismatch, "published Release asset differs from promotion inventory", nil)
		}
	}
	return nil
}

func validateAttestations(contract releasecontract.Contract, manifest releasepromotion.Manifest, attestations []ArchiveAttestation, observedAt time.Time) error {
	if len(attestations) != len(contract.Platforms) {
		return fail(CodeInputIncomplete, "attestation inventory must cover every native archive", nil)
	}
	assets := make(map[string]releasepromotion.FileDigest, len(manifest.Assets))
	for _, asset := range manifest.Assets {
		assets[asset.Name] = asset
	}
	publisher, ok := contract.WorkflowByID("publisher")
	if !ok {
		return fail(CodeInputIncomplete, "release contract has no publisher workflow", nil)
	}
	wantSigner := manifest.Repository + "/.github/workflows/" + publisher.File
	for index, platform := range contract.Platforms {
		actual := attestations[index]
		asset := assets[platform.Archive]
		if actual.AssetName != platform.Archive || actual.AssetSHA256 != asset.SHA256 || !digestPattern.MatchString(actual.AssetSHA256) {
			return fail(CodeDigestMismatch, "attestation subject differs from the promoted archive", nil)
		}
		for _, candidate := range []struct {
			label        string
			verification AttestationVerification
		}{
			{label: "provenance", verification: actual.Provenance},
			{label: "sbom", verification: actual.SBOM},
		} {
			label, verification := candidate.label, candidate.verification
			wantPredicate := ProvenancePredicate
			if label == "sbom" {
				wantPredicate = SBOMPredicate
			}
			verifiedAt, err := canonicalTime(verification.VerifiedAt)
			if err != nil || verifiedAt.After(observedAt) || verification.PredicateType != wantPredicate || !verification.Verified || verification.Repository != manifest.Repository || verification.SignerWorkflow != wantSigner || verification.SourceSHA != manifest.SourceSHA || verification.WorkflowRunID <= 0 || verification.WorkflowRunAttempt <= 0 || !digestPattern.MatchString(verification.DocumentSHA256) {
				return fail(CodeAttestationVerificationFailed, label+" attestation is incomplete or incorrectly bound for "+platform.Archive, err)
			}
		}
	}
	return nil
}

func validateHomebrew(contract releasecontract.Contract, manifest releasepromotion.Manifest, homebrew HomebrewObservation) error {
	app, ok := contract.AppByID("homebrew_tap")
	if !ok {
		return fail(CodeInputIncomplete, "release contract has no Homebrew app", nil)
	}
	repository, repositoryOK := contract.RepositoryByID(app.RepositoryID)
	if !repositoryOK {
		return fail(CodeInputIncomplete, "release contract has no Homebrew repository", nil)
	}
	wantFormula := contract.Homebrew.FormulaPath
	if wantFormula == "" {
		wantFormula = "Formula/" + contract.Naming.Product + ".rb"
	}
	wantPRURL := "https://github.com/" + repository.FullName + "/pull/" + strconv.FormatInt(homebrew.PRNumber, 10)
	if homebrew.Repository != repository.FullName || homebrew.FormulaPath != wantFormula || !digestPattern.MatchString(homebrew.FormulaSHA256) || homebrew.Version != manifest.ReleaseVersion || !homebrew.VersionMonotonic || homebrew.PRNumber <= 0 || homebrew.PRURL != wantPRURL || !shaPattern.MatchString(homebrew.PRHeadSHA) || !shaPattern.MatchString(homebrew.PRMergeSHA) || !shaPattern.MatchString(homebrew.TapSHA) || !homebrew.MergeIsAncestorOfTap {
		return fail(CodeHomebrewStateInvalid, "Homebrew formula or exact PR state is invalid", nil)
	}
	if err := validateExternalRun(homebrew.PRHeadCI, app, repository.FullName, "pull_request", homebrew.PRHeadSHA); err != nil {
		return fail(CodeHomebrewStateInvalid, "Homebrew PR-head CI is invalid", err)
	}
	if err := validateExternalRun(homebrew.PostMergeCI, app, repository.FullName, "push", homebrew.PRMergeSHA); err != nil {
		return fail(CodeHomebrewStateInvalid, "Homebrew post-merge CI is invalid", err)
	}
	return nil
}

func validateExternalRun(run ExternalWorkflowRun, app releasecontract.App, repository, event, headSHA string) error {
	wantURL := "https://github.com/" + repository + "/actions/runs/" + strconv.FormatInt(run.RunID, 10)
	if run.RunID <= 0 || run.RunAttempt <= 0 || run.Workflow != app.CIWorkflowFile || run.Event != event || run.HeadSHA != headSHA || run.Conclusion != "success" || run.URL != wantURL {
		return errors.New("external workflow run does not match the exact head/event/workflow tuple")
	}
	return nil
}

func validateBlockedVersions(contract releasecontract.Contract, observed []BlockedVersionObservation) error {
	blocked := contract.VersionPolicy.BlockedVersions
	if len(observed) != len(blocked) {
		return fail(CodeInputIncomplete, "blocked-version observations are incomplete", nil)
	}
	for index, policy := range blocked {
		actual := observed[index]
		if actual.Version != policy.Version || actual.TagSHA != policy.TagSHA || actual.TagExists != policy.TagMustRemain || (policy.GitHubReleaseMustNotExist && actual.ReleaseExists) || !shaPattern.MatchString(actual.TagSHA) {
			return fail(CodeInputInvalid, "blocked-version state differs from the release contract", nil)
		}
	}
	return nil
}

func validateAbandonedRelease(contract releasecontract.Contract, manifest releasepromotion.Manifest, observed AbandonedReleaseObservation, observedAt time.Time) error {
	policy := contract.VersionPolicy.ReleasePleaseRecovery
	planningApp, ok := contract.AppByID("release_planning")
	if !ok {
		return fail(CodeInputIncomplete, "release contract has no release-planning app", nil)
	}
	wantTitle := "chore(main): release " + contract.Naming.Product + " " + policy.AbandonedVersion
	wantAuthor := planningApp.Slug + "[bot]"
	semanticContractSHA256, err := releasecontract.SemanticSHA256(contract)
	if err != nil {
		return fail(CodeDigestMismatch, "hash abandoned-release contract", err)
	}
	pr := observed.GeneratedReleasePR
	if policy.State == "active" && (manifest.ReleaseVersion != policy.ResumeVersion || manifest.SourceSHA == policy.AbandonedSourceSHA) {
		return fail(CodeVersionMismatch, "active recovery evidence is not bound to the exact resume release", nil)
	}
	if manifest.SourceSHA == policy.AbandonedSourceSHA || manifest.SourceSHA == policy.GeneratedReleasePRHeadSHA {
		return fail(CodeSourceMismatch, "release evidence source is not distinct from the abandoned incident", nil)
	}
	if observed.State != "abandoned" || observed.Version != policy.AbandonedVersion || observed.SourceSHA != policy.AbandonedSourceSHA ||
		pr.Number != int64(policy.GeneratedReleasePRNumber) || pr.HeadSHA != policy.GeneratedReleasePRHeadSHA || pr.MergeSHA != policy.AbandonedSourceSHA ||
		observed.PullRequestState != "closed" || !observed.PullRequestMerged || observed.PullRequestTitle != wantTitle || observed.PullRequestAuthor != wantAuthor ||
		observed.BaseRef != "main" || observed.BaseRepository != manifest.Repository || !observed.BoundaryIsAncestorOfRelease ||
		(policy.TagMustNotExist && observed.TagExists) || (policy.GitHubReleaseMustNotExist && observed.GitHubReleaseExists) || observed.ReasonCode != policy.ReasonCode ||
		observed.SemanticContractSHA256 != semanticContractSHA256 || !digestPattern.MatchString(observed.SemanticContractSHA256) {
		return fail(CodeInputInvalid, "abandoned release state differs from the exact contract incident", nil)
	}
	mergedAt, err := canonicalTime(pr.MergedAt)
	if err != nil || mergedAt.After(observedAt) {
		return fail(CodeInputInvalid, "abandoned release PR merged_at is invalid", err)
	}
	abandonedObservedAt, err := canonicalTime(observed.ObservedAt)
	if err != nil || abandonedObservedAt.Before(mergedAt) || abandonedObservedAt.After(observedAt) {
		return fail(CodeInputInvalid, "abandoned release observed_at is invalid", err)
	}
	if len(observed.Labels) == 0 {
		return fail(CodeInputIncomplete, "abandoned release PR labels are missing", nil)
	}
	foundAbandoned := false
	for index, label := range observed.Labels {
		if label == "" || (index > 0 && observed.Labels[index-1] >= label) {
			return fail(CodeInputInvalid, "abandoned release PR labels must be sorted and unique", nil)
		}
		switch label {
		case policy.AbandonedLabel:
			foundAbandoned = true
		case policy.PendingLabel, policy.TaggedLabel:
			return fail(CodeInputInvalid, "abandoned release PR has a forbidden lifecycle label", nil)
		}
	}
	if !foundAbandoned {
		return fail(CodeInputIncomplete, "abandoned release PR lacks its lifecycle label", nil)
	}
	return nil
}

func ValidateHealthProof(proof HealthProof, contract releasecontract.Contract, manifest releasepromotion.Manifest, publisherMetrics releasemetrics.Metrics, observedAt time.Time) error {
	if proof.SchemaID != contract.Schemas["release_health_proof"] || proof.SchemaVersion != ObservationSchemaVersion || proof.Result != "pass" {
		return fail(CodeInputInvalid, "health proof schema or result is invalid", nil)
	}
	if proof.Repository != manifest.Repository || proof.ReleaseVersion != manifest.ReleaseVersion || proof.SourceSHA != manifest.SourceSHA || proof.PublisherRunID != publisherMetrics.RunID || proof.PublisherRunAttempt != publisherMetrics.Attempt {
		return fail(CodeSourceMismatch, "health proof differs from the exact release/publisher tuple", nil)
	}
	checkedAt, err := canonicalTime(proof.CheckedAt)
	if err != nil || checkedAt.After(observedAt) {
		return fail(CodeInputInvalid, "health proof checked_at is invalid", err)
	}
	if !proof.TagExactSource || !proof.ReleasePublished || !proof.AssetsExact || !proof.AttestationsExact || !proof.HomebrewExact || !proof.HomebrewPRHeadCISuccess || !proof.HomebrewPostMergeCISuccess || !proof.BlockedVersionPolicyExact || !proof.AbandonedReleasePolicyExact {
		return fail(CodeInputIncomplete, "health proof did not pass every release guarantee", nil)
	}
	want, err := HealthProofSHA256(proof)
	if err != nil {
		return fail(CodeDigestMismatch, "hash health proof", err)
	}
	if proof.ProofSHA256 != want || !digestPattern.MatchString(proof.ProofSHA256) {
		return fail(CodeDigestMismatch, "health proof self-digest mismatch", nil)
	}
	return nil
}

func validateMetrics(metrics releasemetrics.Metrics, workflow string, allowedEvents map[string]bool, sourceSHA string) error {
	if err := releasemetrics.Validate(metrics); err != nil {
		return err
	}
	if metrics.WorkflowName != workflow || !allowedEvents[metrics.Event] || metrics.HeadSHA != sourceSHA {
		return errors.New("metrics workflow, event, or source identity is invalid")
	}
	return nil
}

func validFileDigest(file releasepromotion.FileDigest) bool {
	return file.Name != "" && filepath.Base(file.Name) == file.Name && file.Size > 0 && digestPattern.MatchString(file.SHA256)
}

func validObservedAsset(file ObservedAsset) bool {
	return file.Name != "" && filepath.Base(file.Name) == file.Name && file.Size > 0 && digestPattern.MatchString(file.SHA256)
}

func validRepository(repository string) bool {
	return repositoryPattern.MatchString(repository) && !strings.Contains(repository, "..")
}

func canonicalTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil || parsed.Location() != time.UTC || parsed.Format(time.RFC3339) != value {
		return time.Time{}, fmt.Errorf("%q is not canonical UTC RFC3339", value)
	}
	return parsed, nil
}

func cloneJSON[T any](value T) (T, error) {
	var clone T
	data, err := json.Marshal(value)
	if err != nil {
		return clone, err
	}
	if err := json.Unmarshal(data, &clone); err != nil {
		return clone, err
	}
	return clone, nil
}
