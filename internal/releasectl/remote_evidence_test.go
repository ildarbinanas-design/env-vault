package releasectl

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/url"
	"strings"
	"testing"
	"time"
)

func fixtureAttestations(assets []releaseAsset, predicateType string) []remoteAttestation {
	contract := mustTestReleaseContract()
	digests := map[string]string{}
	for _, asset := range assets {
		digests[asset.Name] = strings.TrimPrefix(asset.Digest, "sha256:")
	}
	subjects := make([]inTotoSubject, 0, len(contract.Platforms))
	for _, platform := range contract.Platforms {
		subjects = append(subjects, inTotoSubject{
			Name: platform.Archive, Digest: map[string]string{"sha256": digests[platform.Archive]},
		})
	}
	predicate := any(map[string]any{"SPDXID": "SPDXRef-DOCUMENT", "spdxVersion": "SPDX-2.3"})
	if predicateType == provenancePredicateType {
		predicate = map[string]any{
			"buildDefinition": map[string]any{
				"externalParameters": map[string]any{"workflow": map[string]any{
					"repository": "https://github.com/" + testRepository,
					"path":       ".github/workflows/build-binaries.yml",
				}},
				"resolvedDependencies": []any{map[string]any{
					"uri":    "git+https://github.com/" + testRepository + "@refs/heads/main",
					"digest": map[string]string{"gitCommit": testSHA},
				}},
			},
			"runDetails": map[string]any{"builder": map[string]string{"id": "https://github.com/actions/runner"}},
		}
	}
	predicateJSON, err := json.Marshal(predicate)
	if err != nil {
		panic(err)
	}
	statementJSON, err := json.Marshal(inTotoStatement{
		Type: inTotoStatementType, Subject: subjects, PredicateType: predicateType, Predicate: predicateJSON,
	})
	if err != nil {
		panic(err)
	}
	certificate := fixtureFulcioCertificate()
	return []remoteAttestation{{
		RepositoryID: 1,
		Bundle: sigstoreBundle{
			MediaType: sigstoreBundleMediaType,
			DSSEEnvelope: dsseEnvelope{
				PayloadType: inTotoPayloadType, Payload: base64.StdEncoding.EncodeToString(statementJSON),
				Signatures: []dsseSignature{{Signature: base64.StdEncoding.EncodeToString([]byte("signature"))}},
			},
			VerificationMaterial: verificationMaterial{
				Certificate: struct {
					RawBytes string `json:"rawBytes"`
				}{RawBytes: base64.StdEncoding.EncodeToString(certificate)},
				TlogEntries: []json.RawMessage{json.RawMessage(`{"logIndex":1}`)},
			},
		},
	}}
}

func fixtureFulcioCertificate() []byte {
	seed := make([]byte, ed25519.SeedSize)
	for index := range seed {
		seed[index] = byte(index + 1)
	}
	privateKey := ed25519.NewKeyFromSeed(seed)
	workflowURI, err := url.Parse("https://github.com/" + testRepository + "/.github/workflows/build-binaries.yml@refs/heads/main")
	if err != nil {
		panic(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC),
		NotAfter:     time.Date(2026, 7, 17, 0, 0, 0, 0, time.UTC),
		URIs:         []*url.URL{workflowURI},
		ExtraExtensions: []pkix.Extension{
			{Id: fulcioSourceSHAOID, Value: []byte(testSHA)},
			{Id: fulcioRepositoryOID, Value: []byte(testRepository)},
			{Id: fulcioInvocationURIOID, Value: []byte("https://github.com/" + testRepository + "/actions/runs/900/attempts/2")},
		},
	}
	certificate, err := x509.CreateCertificate(rand.Reader, template, template, privateKey.Public(), privateKey)
	if err != nil {
		panic(err)
	}
	return certificate
}

func TestSnapshotCurrentRemoteEvidenceBindsDigestsAttestationsAndTapCI(t *testing.T) {
	github := successfulFixture()
	doc, err := (collector{github: github, clock: fixedClock()}).snapshot(context.Background(), query{
		Repository: testRepository, Version: testVersion, SourceSHA: testSHA,
	})
	if err != nil {
		t.Fatal(err)
	}
	if doc.Overall.State != "succeeded" || !doc.Stages.GitHubRelease.Release.Assets.DigestComplete || len(doc.Stages.GitHubRelease.Release.Assets.Digests) != 10 {
		t.Fatalf("release evidence=%+v overall=%+v", doc.Stages.GitHubRelease, doc.Overall)
	}
	attestations := doc.Stages.SupplyChain.Attestations
	if attestations == nil || !attestations.Complete || attestations.SourceSHA != testSHA || len(attestations.Records) != 2 {
		t.Fatalf("attestations=%+v", attestations)
	}
	for _, record := range attestations.Records {
		if record.SourceSHA != testSHA || record.Workflow != testRepository+"/.github/workflows/build-binaries.yml" || record.RunID != 900 || record.RunAttempt != 2 || len(record.Subjects) != 5 {
			t.Fatalf("attestation record=%+v", record)
		}
	}
	homebrew := doc.Stages.Homebrew.Homebrew
	if homebrew == nil || !homebrew.Complete || !homebrew.MergeOnDefaultBranch || homebrew.PullRequest == nil || homebrew.PullRequest.HeadSHA != tapHeadSHA || homebrew.PullRequest.MergeSHA != tapMergeSHA || !shaPattern.MatchString(homebrew.PullRequest.FormulaBlobSHA) || homebrew.PRHeadCI == nil || homebrew.PRHeadCI.HeadSHA != tapHeadSHA || homebrew.PostMergeCI == nil || homebrew.PostMergeCI.HeadSHA != tapMergeSHA {
		t.Fatalf("homebrew=%+v", homebrew)
	}
}

func TestSnapshotRejectsPostMergeTapFormulaDifferentFromExactPRHead(t *testing.T) {
	github := successfulFixture()
	github.tapFormulaByRef = map[string][]byte{tapMergeSHA: []byte("changed after review\n")}
	doc, err := (collector{github: github, clock: fixedClock()}).snapshot(context.Background(), query{
		Repository: testRepository, Version: testVersion, SourceSHA: testSHA,
	})
	if err != nil {
		t.Fatal(err)
	}
	if doc.Overall.State != "inconsistent" || doc.Stages.Homebrew.Reason != "homebrew_merge_formula_mismatch" || doc.Stages.Homebrew.Homebrew == nil || doc.Stages.Homebrew.Homebrew.Complete {
		t.Fatalf("post-merge formula drift was accepted: %+v", doc)
	}
}

func TestSnapshotHistoricalSupplyChainSuccessCannotReplaceMissingCurrentAttestation(t *testing.T) {
	github := successfulFixture()
	github.attestations = map[string][]remoteAttestation{sbomPredicateType: nil}
	doc, err := (collector{github: github, clock: fixedClock()}).snapshot(context.Background(), query{
		Repository: testRepository, Version: testVersion, SourceSHA: testSHA,
	})
	if err != nil {
		t.Fatal(err)
	}
	if doc.Overall.State != "inconsistent" || doc.Stages.SupplyChain.State != stateInconsistent || doc.Stages.SupplyChain.Reason != "attestations_incomplete" || doc.Stages.SupplyChain.Attestations == nil || doc.Stages.SupplyChain.Attestations.Complete {
		t.Fatalf("status=%+v", doc)
	}
}

func TestSnapshotHistoricalHomebrewSuccessCannotReplaceFailedCurrentPRHeadCI(t *testing.T) {
	github := successfulFixture()
	run := completedTapRun(403, "pull_request", "release/env-vault-"+testVersion, tapHeadSHA)
	run.Conclusion = "failure"
	github.tapPRRuns, github.tapPRRunsSet = []workflowRun{run}, true
	doc, err := (collector{github: github, clock: fixedClock()}).snapshot(context.Background(), query{
		Repository: testRepository, Version: testVersion, SourceSHA: testSHA,
	})
	if err != nil {
		t.Fatal(err)
	}
	if doc.Overall.State != "inconsistent" || doc.Stages.Homebrew.State != stateInconsistent || doc.Stages.Homebrew.Reason != "homebrew_pr_head_ci_not_successful" || doc.Stages.Homebrew.Homebrew == nil || doc.Stages.Homebrew.Homebrew.PRHeadCI == nil {
		t.Fatalf("status=%+v", doc)
	}
}

func TestSnapshotMalformedReleaseDigestIsUnknownNotMissing(t *testing.T) {
	github := successfulFixture()
	github.release.Assets[0].Digest = ""
	_, err := (collector{github: github, clock: fixedClock()}).snapshot(context.Background(), query{
		Repository: testRepository, Version: testVersion, SourceSHA: testSHA,
	})
	var observationErr *observationError
	if !errors.As(err, &observationErr) || observationErr.code != "MALFORMED_RESPONSE" {
		t.Fatalf("err=%T %v", err, err)
	}
}

func TestVerifyReleaseRequiresCurrentStructuredEvidence(t *testing.T) {
	contract := mustTestReleaseContract()
	request := query{Repository: testRepository, Version: testVersion, SourceSHA: testSHA}
	completeFixture := successfulFixture()
	complete := verifyRelease(context.Background(), request, contract, completeFixture, fixedClock())
	if !complete.OK || complete.Outcome != "pass" || complete.Status == nil || complete.Status.Stages.SupplyChain.Attestations == nil || complete.Status.Stages.Homebrew.Homebrew == nil {
		t.Fatalf("complete verification=%+v", complete)
	}
	if completeFixture.verificationCalls != 1 {
		t.Fatalf("cryptographic verification calls=%d, want 1", completeFixture.verificationCalls)
	}

	missing := successfulFixture()
	missing.attestations = map[string][]remoteAttestation{provenancePredicateType: nil}
	failed := verifyRelease(context.Background(), request, contract, missing, fixedClock())
	if failed.OK || failed.Outcome != "fail" || failed.ReasonCode != "supply_chain_inconsistent" {
		t.Fatalf("missing current evidence was accepted: %+v", failed)
	}

	unavailable := successfulFixture()
	subjects, err := archiveSubjects(contract, compareAssets(unavailable.release.Assets, contract.Assets))
	if err != nil {
		t.Fatal(err)
	}
	probeDigest := subjects[0].SHA256
	endpoint := "repos/" + testRepository + "/attestations/sha256:" + probeDigest
	unavailable.errors[endpoint] = &apiError{Code: "API_UNAVAILABLE", Endpoint: endpoint, Retryable: true}
	unknown := verifyRelease(context.Background(), request, contract, unavailable, fixedClock())
	if unknown.OK || unknown.Outcome != "unknown" || unknown.Error == nil || unknown.Error.Code != "API_UNAVAILABLE" {
		t.Fatalf("unavailable evidence API was not fail-closed: %+v", unknown)
	}
}

type getterWithoutAttestationVerifier struct{ githubGetter }

func TestVerifyReleaseFailsClosedForInvalidOrUnavailableCryptographicVerifier(t *testing.T) {
	contract := mustTestReleaseContract()
	request := query{Repository: testRepository, Version: testVersion, SourceSHA: testSHA}

	invalid := successfulFixture()
	invalid.verificationErr = &attestationVerificationFailure{cause: errors.New("invalid DSSE signature")}
	failed := verifyRelease(context.Background(), request, contract, invalid, fixedClock())
	if failed.OK || failed.Outcome != "fail" || failed.ReasonCode != "attestation_crypto_verification_failed" || failed.Error == nil || failed.Error.Code != "ATTESTATION_VERIFICATION_FAILED" {
		t.Fatalf("invalid cryptographic evidence was accepted: %+v", failed)
	}

	unavailable := verifyRelease(context.Background(), request, contract, getterWithoutAttestationVerifier{githubGetter: successfulFixture()}, fixedClock())
	if unavailable.OK || unavailable.Outcome != "unknown" || unavailable.ReasonCode != "attestation_crypto_verifier_unavailable" || unavailable.Error == nil || unavailable.Error.Code != "DEPENDENCY_MISSING" {
		t.Fatalf("missing cryptographic verifier was accepted: %+v", unavailable)
	}
}
