package releasectl

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ildarbinanas-design/env-vault/internal/releasecontract"
)

const (
	sigstoreBundleMediaType = "application/vnd.dev.sigstore.bundle.v0.3+json"
	inTotoPayloadType       = "application/vnd.in-toto+json"
	inTotoStatementType     = "https://in-toto.io/Statement/v1"
	provenancePredicateType = "https://slsa.dev/provenance/v1"
	sbomPredicateType       = "https://spdx.dev/Document/v2.3"
)

var (
	releaseAssetDigestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	digestPattern             = regexp.MustCompile(`^[0-9a-f]{64}$`)
	gitRefPattern             = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)
	fulcioSourceSHAOID        = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 3}
	fulcioRepositoryOID       = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 5}
	fulcioInvocationURIOID    = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 21}
)

type releaseAssetEvidence struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type attestationEvidence struct {
	Complete             bool                        `json:"complete"`
	ReasonCode           string                      `json:"reason_code"`
	SourceSHA            string                      `json:"source_sha"`
	SignerWorkflow       string                      `json:"signer_workflow"`
	ExpectedSubjectCount int                         `json:"expected_subject_count"`
	Records              []attestationRecordEvidence `json:"records"`
	MissingPredicates    []string                    `json:"missing_predicates"`
}

type attestationRecordEvidence struct {
	Kind          string                       `json:"kind"`
	PredicateType string                       `json:"predicate_type"`
	SourceSHA     string                       `json:"source_sha"`
	Workflow      string                       `json:"workflow"`
	RunID         int64                        `json:"run_id"`
	RunAttempt    int                          `json:"run_attempt"`
	Subjects      []attestationSubjectEvidence `json:"subjects"`
}

type attestationSubjectEvidence struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}

type attestationsResponse struct {
	Attestations []remoteAttestation `json:"attestations"`
}

type remoteAttestation struct {
	RepositoryID int64          `json:"repository_id"`
	Bundle       sigstoreBundle `json:"bundle"`
}

type sigstoreBundle struct {
	MediaType            string               `json:"mediaType"`
	DSSEEnvelope         dsseEnvelope         `json:"dsseEnvelope"`
	VerificationMaterial verificationMaterial `json:"verificationMaterial"`
}

type dsseEnvelope struct {
	PayloadType string          `json:"payloadType"`
	Payload     string          `json:"payload"`
	Signatures  []dsseSignature `json:"signatures"`
}

type dsseSignature struct {
	Signature string `json:"sig"`
}

type verificationMaterial struct {
	Certificate struct {
		RawBytes string `json:"rawBytes"`
	} `json:"certificate"`
	TlogEntries []json.RawMessage `json:"tlogEntries"`
}

type inTotoStatement struct {
	Type          string          `json:"_type"`
	Subject       []inTotoSubject `json:"subject"`
	PredicateType string          `json:"predicateType"`
	Predicate     json.RawMessage `json:"predicate"`
}

type inTotoSubject struct {
	Name   string            `json:"name"`
	Digest map[string]string `json:"digest"`
}

type provenancePredicate struct {
	BuildDefinition struct {
		ExternalParameters struct {
			Workflow struct {
				Repository string `json:"repository"`
				Path       string `json:"path"`
			} `json:"workflow"`
		} `json:"externalParameters"`
		ResolvedDependencies []struct {
			URI    string            `json:"uri"`
			Digest map[string]string `json:"digest"`
		} `json:"resolvedDependencies"`
	} `json:"buildDefinition"`
}

type homebrewEvidence struct {
	Complete             bool                  `json:"complete"`
	ReasonCode           string                `json:"reason_code"`
	Repository           string                `json:"repository"`
	DeterministicBranch  string                `json:"deterministic_branch"`
	DefaultBranch        string                `json:"default_branch,omitempty"`
	PullRequest          *homebrewPullEvidence `json:"pull_request,omitempty"`
	PRHeadCI             *runEvidence          `json:"pr_head_ci,omitempty"`
	PostMergeCI          *runEvidence          `json:"post_merge_ci,omitempty"`
	MergeOnDefaultBranch bool                  `json:"merge_on_default_branch"`
}

type homebrewPullEvidence struct {
	Number         int       `json:"number"`
	URL            string    `json:"url"`
	State          string    `json:"state"`
	Title          string    `json:"title"`
	HeadSHA        string    `json:"head_sha"`
	MergeSHA       string    `json:"merge_sha,omitempty"`
	MergedAt       time.Time `json:"merged_at,omitempty"`
	FormulaSHA256  string    `json:"formula_sha256,omitempty"`
	FormulaBlobSHA string    `json:"formula_blob_sha,omitempty"`
}

type pullRequestResponse struct {
	Number         int        `json:"number"`
	HTMLURL        string     `json:"html_url"`
	State          string     `json:"state"`
	Title          string     `json:"title"`
	Body           string     `json:"body"`
	MergedAt       *time.Time `json:"merged_at"`
	MergeCommitSHA string     `json:"merge_commit_sha"`
	Head           pullRef    `json:"head"`
	Base           pullRef    `json:"base"`
}

type pullRef struct {
	Ref        string `json:"ref"`
	SHA        string `json:"sha"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repo"`
}

type compareResponse struct {
	Status          string `json:"status"`
	MergeBaseCommit struct {
		SHA string `json:"sha"`
	} `json:"merge_base_commit"`
}

type contentsResponse struct {
	Type     string `json:"type"`
	Encoding string `json:"encoding"`
	Size     int64  `json:"size"`
	SHA      string `json:"sha"`
	Path     string `json:"path"`
	Content  string `json:"content"`
}

func (c collector) observeAttestations(
	ctx context.Context,
	repository string,
	sourceSHA string,
	contract releasecontract.Contract,
	assets assetEvidence,
) (attestationEvidence, error) {
	signerWorkflow := repository + "/.github/workflows/" + workflowFile(contract, "publisher")
	expectedSubjects, err := archiveSubjects(contract, assets)
	if err != nil {
		return attestationEvidence{}, &observationError{code: "MALFORMED_RESPONSE", operation: "release_asset_digests", cause: err}
	}
	evidence := attestationEvidence{
		ReasonCode: "attestations_incomplete", SourceSHA: sourceSHA, SignerWorkflow: signerWorkflow,
		ExpectedSubjectCount: len(expectedSubjects), Records: []attestationRecordEvidence{}, MissingPredicates: []string{},
	}
	if len(expectedSubjects) == 0 {
		return evidence, nil
	}
	probeDigest := expectedSubjects[0].SHA256
	for _, predicate := range []struct {
		kind string
		uri  string
	}{{kind: "provenance", uri: provenancePredicateType}, {kind: "sbom", uri: sbomPredicateType}} {
		records, found, getErr := c.getAttestations(ctx, repository, probeDigest, predicate.uri)
		if getErr != nil {
			return attestationEvidence{}, getErr
		}
		if !found {
			evidence.MissingPredicates = append(evidence.MissingPredicates, predicate.kind)
			continue
		}
		matching := []attestationRecordEvidence{}
		for _, record := range records {
			parsed, parseErr := parseAttestation(record, repository, predicate.kind, predicate.uri, probeDigest)
			if parseErr != nil {
				return attestationEvidence{}, &observationError{code: "MALFORMED_RESPONSE", operation: "artifact_attestations", cause: parseErr}
			}
			if parsed.SourceSHA == sourceSHA && parsed.Workflow == signerWorkflow && sameAttestationSubjects(parsed.Subjects, expectedSubjects) {
				matching = append(matching, parsed)
			}
		}
		if len(matching) == 0 {
			evidence.MissingPredicates = append(evidence.MissingPredicates, predicate.kind)
			continue
		}
		sort.Slice(matching, func(i, j int) bool {
			if matching[i].RunID == matching[j].RunID {
				return matching[i].RunAttempt > matching[j].RunAttempt
			}
			return matching[i].RunID > matching[j].RunID
		})
		evidence.Records = append(evidence.Records, matching[0])
	}
	sort.Strings(evidence.MissingPredicates)
	sort.Slice(evidence.Records, func(i, j int) bool { return evidence.Records[i].Kind < evidence.Records[j].Kind })
	evidence.Complete = len(evidence.Records) == 2 && len(evidence.MissingPredicates) == 0
	if evidence.Complete {
		evidence.ReasonCode = "attestations_complete"
	}
	return evidence, nil
}

func archiveSubjects(contract releasecontract.Contract, assets assetEvidence) ([]attestationSubjectEvidence, error) {
	byName := make(map[string]string, len(assets.Digests))
	for _, asset := range assets.Digests {
		if !digestPattern.MatchString(asset.SHA256) || byName[asset.Name] != "" {
			return nil, errors.New("release asset digest is missing or duplicated")
		}
		byName[asset.Name] = asset.SHA256
	}
	result := make([]attestationSubjectEvidence, 0, len(contract.Platforms))
	for _, platform := range contract.Platforms {
		digest := byName[platform.Archive]
		if digest == "" {
			return nil, fmt.Errorf("archive digest is missing for %s", platform.Archive)
		}
		result = append(result, attestationSubjectEvidence{Name: platform.Archive, SHA256: digest})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

func (c collector) getAttestations(ctx context.Context, repository, digest, predicateType string) ([]remoteAttestation, bool, error) {
	endpoint := "repos/" + repository + "/attestations/sha256:" + digest
	var response attestationsResponse
	if err := c.github.Get(ctx, endpoint, map[string]string{"predicate_type": predicateType, "per_page": "100"}, &response); err != nil {
		return nil, false, err
	}
	if response.Attestations == nil {
		return nil, false, malformed(endpoint, "attestations is missing")
	}
	if len(response.Attestations) >= 100 {
		return nil, false, malformed(endpoint, "attestation pagination is incomplete")
	}
	if len(response.Attestations) == 0 {
		return nil, false, nil
	}
	return response.Attestations, true, nil
}

func parseAttestation(
	record remoteAttestation,
	repository string,
	kind string,
	predicateType string,
	queriedDigest string,
) (attestationRecordEvidence, error) {
	if record.RepositoryID <= 0 || record.Bundle.MediaType != sigstoreBundleMediaType ||
		record.Bundle.DSSEEnvelope.PayloadType != inTotoPayloadType || record.Bundle.DSSEEnvelope.Payload == "" ||
		len(record.Bundle.DSSEEnvelope.Signatures) == 0 || len(record.Bundle.VerificationMaterial.TlogEntries) == 0 {
		return attestationRecordEvidence{}, errors.New("attestation bundle identity is incomplete")
	}
	for _, signature := range record.Bundle.DSSEEnvelope.Signatures {
		if signature.Signature == "" {
			return attestationRecordEvidence{}, errors.New("attestation bundle signature is missing")
		}
		if _, err := base64.StdEncoding.DecodeString(signature.Signature); err != nil {
			return attestationRecordEvidence{}, errors.New("attestation bundle signature is malformed")
		}
	}
	for _, entry := range record.Bundle.VerificationMaterial.TlogEntries {
		if len(entry) == 0 || string(entry) == "null" || !json.Valid(entry) {
			return attestationRecordEvidence{}, errors.New("attestation transparency log entry is malformed")
		}
	}
	payload, err := base64.StdEncoding.DecodeString(record.Bundle.DSSEEnvelope.Payload)
	if err != nil {
		return attestationRecordEvidence{}, errors.New("attestation payload is not base64")
	}
	var statement inTotoStatement
	if err := decodeStrictJSON(payload, &statement); err != nil {
		return attestationRecordEvidence{}, fmt.Errorf("decode attestation statement: %w", err)
	}
	if statement.Type != inTotoStatementType || statement.PredicateType != predicateType || len(statement.Subject) == 0 || len(statement.Predicate) == 0 || string(statement.Predicate) == "null" {
		return attestationRecordEvidence{}, errors.New("attestation statement identity is malformed")
	}
	subjects := make([]attestationSubjectEvidence, 0, len(statement.Subject))
	seenSubjects := map[string]struct{}{}
	queriedDigestFound := false
	for _, subject := range statement.Subject {
		digest := subject.Digest["sha256"]
		if subject.Name == "" || len(subject.Digest) != 1 || !digestPattern.MatchString(digest) {
			return attestationRecordEvidence{}, errors.New("attestation subject is malformed")
		}
		key := subject.Name + "\x00" + digest
		if _, duplicate := seenSubjects[key]; duplicate {
			return attestationRecordEvidence{}, errors.New("attestation subject is duplicated")
		}
		seenSubjects[key] = struct{}{}
		queriedDigestFound = queriedDigestFound || digest == queriedDigest
		subjects = append(subjects, attestationSubjectEvidence{Name: subject.Name, SHA256: digest})
	}
	if !queriedDigestFound {
		return attestationRecordEvidence{}, errors.New("attestation does not contain the queried digest")
	}
	sort.Slice(subjects, func(i, j int) bool { return subjects[i].Name < subjects[j].Name })

	certificateDER, err := base64.StdEncoding.DecodeString(record.Bundle.VerificationMaterial.Certificate.RawBytes)
	if err != nil || len(certificateDER) == 0 {
		return attestationRecordEvidence{}, errors.New("attestation certificate is malformed")
	}
	certificate, err := x509.ParseCertificate(certificateDER)
	if err != nil {
		return attestationRecordEvidence{}, errors.New("attestation certificate cannot be parsed")
	}
	sourceSHA, err := requiredCertificateExtension(certificate, fulcioSourceSHAOID)
	if err != nil || !shaPattern.MatchString(sourceSHA) {
		return attestationRecordEvidence{}, errors.New("attestation certificate source SHA is malformed")
	}
	certificateRepository, err := requiredCertificateExtension(certificate, fulcioRepositoryOID)
	if err != nil || !repositoryPattern.MatchString(certificateRepository) {
		return attestationRecordEvidence{}, errors.New("attestation certificate repository is malformed")
	}
	invocationURI, err := requiredCertificateExtension(certificate, fulcioInvocationURIOID)
	if err != nil {
		return attestationRecordEvidence{}, errors.New("attestation certificate run identity is missing")
	}
	runID, runAttempt, err := parseInvocationURI(invocationURI, certificateRepository)
	if err != nil {
		return attestationRecordEvidence{}, err
	}
	workflow := ""
	workflowPrefix := "https://github.com/" + certificateRepository + "/.github/workflows/"
	for _, uri := range certificate.URIs {
		candidate := uri.String()
		if !strings.HasPrefix(candidate, workflowPrefix) {
			continue
		}
		pathAndRef := strings.TrimPrefix(candidate, "https://github.com/")
		path, _, found := strings.Cut(pathAndRef, "@refs/")
		if !found || workflow != "" {
			return attestationRecordEvidence{}, errors.New("attestation certificate workflow URI is malformed or duplicated")
		}
		workflow = path
	}
	if workflow == "" {
		return attestationRecordEvidence{}, errors.New("attestation certificate workflow URI is missing")
	}
	if certificateRepository != repository {
		// This is a valid but unrelated repository attestation. Preserve its
		// parsed identity so it cannot satisfy the exact repository predicate.
		workflow = certificateRepository + "/.github/workflows/unrelated"
	}
	if kind == "provenance" {
		var predicate provenancePredicate
		if err := json.Unmarshal(statement.Predicate, &predicate); err != nil {
			return attestationRecordEvidence{}, fmt.Errorf("decode provenance predicate: %w", err)
		}
		if predicate.BuildDefinition.ExternalParameters.Workflow.Repository != "https://github.com/"+certificateRepository ||
			certificateRepository+"/"+predicate.BuildDefinition.ExternalParameters.Workflow.Path != workflow {
			return attestationRecordEvidence{}, errors.New("provenance workflow parameters are malformed")
		}
		resolved := false
		for _, dependency := range predicate.BuildDefinition.ResolvedDependencies {
			if dependency.Digest["gitCommit"] == sourceSHA && strings.HasPrefix(dependency.URI, "git+https://github.com/"+certificateRepository+"@refs/") {
				resolved = true
			}
		}
		if !resolved {
			return attestationRecordEvidence{}, errors.New("provenance source dependency does not match the certificate source")
		}
	} else if kind == "sbom" {
		var predicate map[string]json.RawMessage
		if err := json.Unmarshal(statement.Predicate, &predicate); err != nil || predicate == nil {
			return attestationRecordEvidence{}, errors.New("SBOM predicate is malformed")
		}
		var documentID, spdxVersion string
		if err := json.Unmarshal(predicate["SPDXID"], &documentID); err != nil || documentID != "SPDXRef-DOCUMENT" {
			return attestationRecordEvidence{}, errors.New("SBOM document identity is malformed")
		}
		if err := json.Unmarshal(predicate["spdxVersion"], &spdxVersion); err != nil || spdxVersion != "SPDX-2.3" {
			return attestationRecordEvidence{}, errors.New("SBOM document version is malformed")
		}
	}
	return attestationRecordEvidence{
		Kind: kind, PredicateType: predicateType, SourceSHA: sourceSHA, Workflow: workflow,
		RunID: runID, RunAttempt: runAttempt, Subjects: subjects,
	}, nil
}

func requiredCertificateExtension(certificate *x509.Certificate, oid asn1.ObjectIdentifier) (string, error) {
	match := ""
	for _, extension := range certificate.Extensions {
		if !extension.Id.Equal(oid) {
			continue
		}
		if match != "" {
			return "", errors.New("certificate extension is duplicated")
		}
		value := ""
		var decoded string
		if rest, err := asn1.Unmarshal(extension.Value, &decoded); err == nil && len(rest) == 0 {
			value = decoded
		} else if utf8.Valid(extension.Value) {
			value = string(extension.Value)
		}
		if value == "" || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\x00\r\n") {
			return "", errors.New("certificate extension value is malformed")
		}
		match = value
	}
	if match == "" {
		return "", errors.New("certificate extension is missing")
	}
	return match, nil
}

func parseInvocationURI(value, repository string) (int64, int, error) {
	prefix := "https://github.com/" + repository + "/actions/runs/"
	if !strings.HasPrefix(value, prefix) {
		return 0, 0, errors.New("attestation invocation URI repository is inconsistent")
	}
	remainder := strings.TrimPrefix(value, prefix)
	runText, attemptText, found := strings.Cut(remainder, "/attempts/")
	if !found || runText == "" || attemptText == "" || strings.Contains(attemptText, "/") {
		return 0, 0, errors.New("attestation invocation URI is malformed")
	}
	runID, runErr := strconv.ParseInt(runText, 10, 64)
	attempt, attemptErr := strconv.Atoi(attemptText)
	if runErr != nil || attemptErr != nil || runID <= 0 || attempt <= 0 {
		return 0, 0, errors.New("attestation invocation URI run identity is malformed")
	}
	return runID, attempt, nil
}

func sameAttestationSubjects(left, right []attestationSubjectEvidence) bool {
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

func (c collector) observeHomebrew(
	ctx context.Context,
	repository string,
	version string,
	sourceSHA string,
	contract releasecontract.Contract,
) (homebrewEvidence, error) {
	tapApp, ok := contract.AppByID("homebrew_tap")
	if !ok {
		return homebrewEvidence{}, &observationError{code: "CONTRACT_INVALID", operation: releasecontract.CanonicalPath, cause: errors.New("homebrew_tap app is missing")}
	}
	branch := "release/env-vault-" + version
	evidence := homebrewEvidence{
		ReasonCode: "homebrew_evidence_incomplete", Repository: tapApp.Repository,
		DeterministicBranch: branch,
	}
	repositoryEndpoint := "repos/" + tapApp.Repository
	var tapRepository repositoryResponse
	if err := c.github.Get(ctx, repositoryEndpoint, nil, &tapRepository); err != nil {
		return homebrewEvidence{}, err
	}
	if tapRepository.FullName != tapApp.Repository || !validGitRef(tapRepository.DefaultBranch) {
		return homebrewEvidence{}, malformed(repositoryEndpoint, "tap repository identity or default branch is malformed")
	}
	evidence.DefaultBranch = tapRepository.DefaultBranch

	owner, _, _ := strings.Cut(tapApp.Repository, "/")
	pullsEndpoint := "repos/" + tapApp.Repository + "/pulls"
	var pulls []pullRequestResponse
	if err := c.github.Get(ctx, pullsEndpoint, map[string]string{
		"state": "all", "head": owner + ":" + branch, "base": tapRepository.DefaultBranch, "per_page": "100",
	}, &pulls); err != nil {
		return homebrewEvidence{}, err
	}
	if pulls == nil {
		return homebrewEvidence{}, malformed(pullsEndpoint, "pull request list is missing")
	}
	if len(pulls) >= 100 {
		return homebrewEvidence{}, malformed(pullsEndpoint, "pull request pagination is incomplete")
	}
	for _, pull := range pulls {
		if err := validateHomebrewPullResponse(pull, tapApp.Repository, branch, tapRepository.DefaultBranch); err != nil {
			return homebrewEvidence{}, malformed(pullsEndpoint, err.Error())
		}
	}
	if len(pulls) == 0 {
		evidence.ReasonCode = "homebrew_pr_missing"
		return evidence, nil
	}
	if len(pulls) != 1 {
		evidence.ReasonCode = "homebrew_pr_duplicate"
		return evidence, nil
	}
	pull := pulls[0]
	pullEvidence := homebrewPullEvidence{
		Number: pull.Number, URL: pull.HTMLURL, State: pull.State, Title: pull.Title, HeadSHA: pull.Head.SHA,
	}
	evidence.PullRequest = &pullEvidence
	formulaDigest, identityOK := exactHomebrewPullIdentity(pull, repository, tapApp.Repository, version, sourceSHA, branch, tapRepository.DefaultBranch)
	pullEvidence.FormulaSHA256 = formulaDigest
	evidence.PullRequest = &pullEvidence
	if !identityOK {
		evidence.ReasonCode = "homebrew_pr_identity_mismatch"
		return evidence, nil
	}
	headFormulaDigest, headFormulaBlob, contentErr := c.getTapFormula(ctx, tapApp.Repository, pull.Head.SHA)
	if contentErr != nil {
		return homebrewEvidence{}, contentErr
	}
	pullEvidence.FormulaBlobSHA = headFormulaBlob
	evidence.PullRequest = &pullEvidence
	if headFormulaDigest != formulaDigest {
		evidence.ReasonCode = "homebrew_pr_formula_digest_mismatch"
		return evidence, nil
	}
	prRun, found, err := c.findRun(ctx, tapApp.Repository, tapApp.CIWorkflowFile, map[string]string{
		"event": "pull_request", "head_sha": pull.Head.SHA, "per_page": "100",
	}, func(run workflowRun) bool {
		return run.Event == "pull_request" && run.HeadBranch == branch && run.HeadSHA == pull.Head.SHA
	})
	if err != nil {
		return homebrewEvidence{}, err
	}
	if !found {
		evidence.ReasonCode = "homebrew_pr_head_ci_missing"
		return evidence, nil
	}
	evidence.PRHeadCI = evidenceFromRun(prRun)
	prState, stateErr := stateFromStatus(prRun.Status, prRun.Conclusion)
	if stateErr != nil {
		return homebrewEvidence{}, stateErr
	}
	if prState != stateSucceeded {
		evidence.ReasonCode = "homebrew_pr_head_ci_not_successful"
		return evidence, nil
	}
	if pull.State != "closed" || pull.MergedAt == nil || !shaPattern.MatchString(pull.MergeCommitSHA) {
		evidence.ReasonCode = "homebrew_pr_not_merged"
		return evidence, nil
	}
	pullEvidence.MergeSHA = pull.MergeCommitSHA
	pullEvidence.MergedAt = pull.MergedAt.UTC()
	evidence.PullRequest = &pullEvidence
	mergeFormulaDigest, mergeFormulaBlob, contentErr := c.getTapFormula(ctx, tapApp.Repository, pull.MergeCommitSHA)
	if contentErr != nil {
		return homebrewEvidence{}, contentErr
	}
	if mergeFormulaDigest != headFormulaDigest || mergeFormulaBlob != headFormulaBlob {
		evidence.ReasonCode = "homebrew_merge_formula_mismatch"
		return evidence, nil
	}

	compareEndpoint := "repos/" + tapApp.Repository + "/compare/" + pull.MergeCommitSHA + "..." + tapRepository.DefaultBranch
	var comparison compareResponse
	if err := c.github.Get(ctx, compareEndpoint, nil, &comparison); err != nil {
		return homebrewEvidence{}, err
	}
	if comparison.Status == "" || !shaPattern.MatchString(comparison.MergeBaseCommit.SHA) {
		return homebrewEvidence{}, malformed(compareEndpoint, "tap comparison identity is malformed")
	}
	if comparison.Status != "ahead" && comparison.Status != "identical" && comparison.Status != "behind" && comparison.Status != "diverged" {
		return homebrewEvidence{}, malformed(compareEndpoint, "tap comparison status is malformed")
	}
	evidence.MergeOnDefaultBranch = (comparison.Status == "ahead" || comparison.Status == "identical") && comparison.MergeBaseCommit.SHA == pull.MergeCommitSHA
	if !evidence.MergeOnDefaultBranch {
		evidence.ReasonCode = "homebrew_merge_not_on_default_branch"
		return evidence, nil
	}

	pushRun, found, err := c.findRun(ctx, tapApp.Repository, tapApp.CIWorkflowFile, map[string]string{
		"event": "push", "head_sha": pull.MergeCommitSHA, "branch": tapRepository.DefaultBranch, "per_page": "100",
	}, func(run workflowRun) bool {
		return run.Event == "push" && run.HeadBranch == tapRepository.DefaultBranch && run.HeadSHA == pull.MergeCommitSHA
	})
	if err != nil {
		return homebrewEvidence{}, err
	}
	if !found {
		evidence.ReasonCode = "homebrew_post_merge_ci_missing"
		return evidence, nil
	}
	evidence.PostMergeCI = evidenceFromRun(pushRun)
	pushState, stateErr := stateFromStatus(pushRun.Status, pushRun.Conclusion)
	if stateErr != nil {
		return homebrewEvidence{}, stateErr
	}
	if pushState != stateSucceeded {
		evidence.ReasonCode = "homebrew_post_merge_ci_not_successful"
		return evidence, nil
	}
	evidence.Complete = true
	evidence.ReasonCode = "homebrew_evidence_complete"
	return evidence, nil
}

func validateHomebrewPullResponse(pull pullRequestResponse, repository, branch, defaultBranch string) error {
	if pull.Number <= 0 || pull.HTMLURL == "" || pull.Title == "" || pull.Body == "" ||
		(pull.State != "open" && pull.State != "closed") || pull.Head.Ref == "" || !shaPattern.MatchString(pull.Head.SHA) ||
		pull.Head.Repository.FullName == "" || pull.Base.Ref == "" || !shaPattern.MatchString(pull.Base.SHA) || pull.Base.Repository.FullName == "" {
		return errors.New("pull request identity is malformed")
	}
	if pull.Head.Ref != branch || pull.Head.Repository.FullName != repository || pull.Base.Ref != defaultBranch || pull.Base.Repository.FullName != repository {
		return errors.New("pull request response does not match the requested deterministic branch")
	}
	expectedURL := "https://github.com/" + repository + "/pull/" + strconv.Itoa(pull.Number)
	if pull.HTMLURL != expectedURL {
		return errors.New("pull request URL is malformed")
	}
	if pull.MergedAt != nil && (!shaPattern.MatchString(pull.MergeCommitSHA) || pull.State != "closed") {
		return errors.New("merged pull request identity is malformed")
	}
	return nil
}

func exactHomebrewPullIdentity(
	pull pullRequestResponse,
	sourceRepository string,
	tapRepository string,
	version string,
	sourceSHA string,
	branch string,
	defaultBranch string,
) (string, bool) {
	if pull.Title != "env-vault "+version || pull.Head.Ref != branch || pull.Head.Repository.FullName != tapRepository ||
		pull.Base.Ref != defaultBranch || pull.Base.Repository.FullName != tapRepository {
		return "", false
	}
	prefix := "Automated Homebrew formula update for env-vault " + version + ".\n\n" +
		"Source release: https://github.com/" + sourceRepository + "/releases/tag/" + version + "\n\n" +
		"<!-- env-vault-release version=" + version + " source_sha=" + sourceSHA + " formula_sha256="
	if !strings.HasPrefix(pull.Body, prefix) || !strings.HasSuffix(pull.Body, " -->") {
		return "", false
	}
	digest := strings.TrimSuffix(strings.TrimPrefix(pull.Body, prefix), " -->")
	if !digestPattern.MatchString(digest) || pull.Body != prefix+digest+" -->" {
		return "", false
	}
	return digest, true
}

func validGitRef(value string) bool {
	return gitRefPattern.MatchString(value) && !strings.Contains(value, "..") && !strings.Contains(value, "//") && !strings.HasSuffix(value, "/")
}

func (c collector) getTapFormula(ctx context.Context, repository, ref string) (string, string, error) {
	endpoint := "repos/" + repository + "/contents/Formula/env-vault.rb"
	var response contentsResponse
	if err := c.github.Get(ctx, endpoint, map[string]string{"ref": ref}, &response); err != nil {
		return "", "", err
	}
	if response.Type != "file" || response.Encoding != "base64" || response.Path != "Formula/env-vault.rb" ||
		response.Size <= 0 || !shaPattern.MatchString(response.SHA) || response.Content == "" {
		return "", "", malformed(endpoint, "tap formula content identity is malformed")
	}
	content, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(response.Content, "\n", ""))
	if err != nil || int64(len(content)) != response.Size {
		return "", "", malformed(endpoint, "tap formula content is malformed")
	}
	blobSHA := gitBlobSHA1(content)
	if blobSHA != response.SHA {
		return "", "", malformed(endpoint, "tap formula git blob digest is inconsistent")
	}
	digest := sha256.Sum256(content)
	return fmt.Sprintf("%x", digest[:]), blobSHA, nil
}

func gitBlobSHA1(content []byte) string {
	prefix := []byte("blob " + strconv.Itoa(len(content)) + "\x00")
	digest := sha1.New()
	_, _ = digest.Write(prefix)
	_, _ = digest.Write(content)
	return fmt.Sprintf("%x", digest.Sum(nil))
}
