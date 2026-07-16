package releaseevidence

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ildarbinanas-design/env-vault/internal/e2ebaseline"
	"github.com/ildarbinanas-design/env-vault/internal/releasecontract"
	"github.com/ildarbinanas-design/env-vault/internal/releasemetrics"
	"github.com/ildarbinanas-design/env-vault/internal/releasepromotion"
	"github.com/ildarbinanas-design/env-vault/internal/releasesettings"
)

const (
	testRepository = "ildarbinanas-design/env-vault"
	testVersion    = "v9.8.7"
	testSource     = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testCIRun      = int64(424242)
	testAttempt    = 3
	testPublisher  = int64(525252)
)

var testTime = time.Date(2026, 7, 16, 8, 30, 0, 0, time.UTC)

type evidenceFixture struct {
	contract          releasecontract.Contract
	authorization     Authorization
	manifest          releasepromotion.Manifest
	ci                releasemetrics.Metrics
	publisher         releasemetrics.Metrics
	observation       Observation
	attestationBundle AttestationVerificationBundle
}

func TestAssembleVerifyAndRenderDeterministically(t *testing.T) {
	fixture := newEvidenceFixture(t)
	evidence, err := Assemble(fixture.contract, fixture.authorization, fixture.manifest, fixture.ci, fixture.publisher, fixture.observation, fixture.attestationBundle)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(evidence, fixture.contract); err != nil {
		t.Fatal(err)
	}
	if len(evidence.Assets) != 10 || len(evidence.Attestations) != 5 {
		t.Fatalf("asset/attestation counts=%d/%d", len(evidence.Assets), len(evidence.Attestations))
	}
	if len(evidence.AttestationVerificationBundle.Entries) != 10 {
		t.Fatalf("attestation verification bundle entries=%d", len(evidence.AttestationVerificationBundle.Entries))
	}
	if evidence.Promotion.ManifestSHA256 != fixture.manifest.ManifestSHA256 || evidence.Promotion.Manifest.ManifestSHA256 != fixture.manifest.ManifestSHA256 {
		t.Fatal("promotion manifest was not embedded with its exact digest")
	}
	if evidence.Authorization.GeneratedReleasePR.Number != fixture.authorization.GeneratedReleasePR.Number || evidence.Authorization.EvidenceWorkflow.RunID != fixture.authorization.EvidenceWorkflow.RunID {
		t.Fatal("release authorization was not embedded")
	}
	if evidence.RepositoryReleaseSettings.ProofSHA256 != fixture.observation.RepositoryReleaseSettings.ProofSHA256 {
		t.Fatal("repository release-settings proof was not embedded")
	}
	if evidence.PublisherRepairMode != "none" {
		t.Fatalf("publisher repair mode=%q, want none", evidence.PublisherRepairMode)
	}
	first, err := Markdown(evidence, fixture.contract)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Markdown(evidence, fixture.contract)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) || !bytes.Contains(first, []byte("| Publisher | 525252 / 1 |")) || !bytes.Contains(first, []byte("525252` / `1` / `none")) || !bytes.Contains(first, []byte("v0.0.8")) {
		t.Fatalf("Markdown index is incomplete or nondeterministic:\n%s", first)
	}

	// Assemble must not retain caller-owned slices or maps.
	fixture.manifest.Assets[0].SHA256 = strings.Repeat("0", 64)
	fixture.authorization.GeneratedReleasePR.HeadSHA = strings.Repeat("0", 40)
	fixture.observation.Attestations[0].AssetSHA256 = strings.Repeat("1", 64)
	fixture.observation.RepositoryReleaseSettings.Inputs.MainRuleset.DocumentJSON = "{}"
	fixture.attestationBundle.Entries[0].DocumentJSON = "[]"
	if err := Verify(evidence, fixture.contract); err != nil {
		t.Fatalf("assembled evidence changed through caller aliasing: %v", err)
	}
}

func TestEvidenceFailsClosedOnIncompleteOrInconsistentState(t *testing.T) {
	base := newEvidenceFixture(t)
	tests := map[string]func(*evidenceFixture){
		"missing publisher repair mode": func(f *evidenceFixture) {
			f.observation.PublisherRepairMode = ""
		},
		"repair mode on tag push": func(f *evidenceFixture) {
			f.observation.PublisherRepairMode = "health"
		},
		"missing attestation": func(f *evidenceFixture) {
			f.observation.Attestations = f.observation.Attestations[:4]
		},
		"missing attestation verification document": func(f *evidenceFixture) {
			f.attestationBundle.Entries = f.attestationBundle.Entries[:9]
		},
		"attestation verification bundle source mismatch": func(f *evidenceFixture) {
			f.attestationBundle.SourceSHA = strings.Repeat("b", 40)
		},
		"attestation verification document digest mismatch": func(f *evidenceFixture) {
			f.attestationBundle.Entries[0].DocumentJSON += "\n"
		},
		"typed attestation document digest mismatch": func(f *evidenceFixture) {
			f.observation.Attestations[0].Provenance.DocumentSHA256 = strings.Repeat("f", 64)
		},
		"case variant asset": func(f *evidenceFixture) {
			f.observation.Release.Assets[0].Name = strings.ToUpper(f.observation.Release.Assets[0].Name)
		},
		"publisher source mismatch": func(f *evidenceFixture) {
			f.publisher.HeadSHA = strings.Repeat("b", 40)
		},
		"CI attempt mismatch": func(f *evidenceFixture) {
			f.ci.RunID++
		},
		"metrics aggregate tampered": func(f *evidenceFixture) {
			f.publisher.AggregateRunnerSeconds++
		},
		"metrics retries tampered": func(f *evidenceFixture) {
			f.publisher.RetryCount++
		},
		"attestation signer mismatch": func(f *evidenceFixture) {
			f.observation.Attestations[0].Provenance.SignerWorkflow = "other/repo/.github/workflows/build-binaries.yml"
		},
		"Homebrew PR CI wrong head": func(f *evidenceFixture) {
			f.observation.Homebrew.PRHeadCI.HeadSHA = strings.Repeat("c", 40)
		},
		"Homebrew post-merge CI follows moving tap head": func(f *evidenceFixture) {
			f.observation.Homebrew.PostMergeCI.HeadSHA = f.observation.Homebrew.TapSHA
		},
		"Homebrew merge ancestry unproven": func(f *evidenceFixture) {
			f.observation.Homebrew.MergeIsAncestorOfTap = false
		},
		"failed tag gained release": func(f *evidenceFixture) {
			f.observation.BlockedVersions[0].ReleaseExists = true
		},
		"health result tampered": func(f *evidenceFixture) {
			f.observation.Health.HomebrewExact = false
		},
		"published asset reordered": func(f *evidenceFixture) {
			f.observation.Release.Assets[0], f.observation.Release.Assets[1] = f.observation.Release.Assets[1], f.observation.Release.Assets[0]
		},
		"promotion self digest tampered": func(f *evidenceFixture) {
			f.manifest.ManifestSHA256 = strings.Repeat("f", 64)
		},
		"authorization version mismatch": func(f *evidenceFixture) {
			f.authorization.ReleaseVersion = "v9.8.6"
		},
		"authorization schema mismatch": func(f *evidenceFixture) {
			f.authorization.SchemaID = ObservationSchemaID
		},
		"authorization planning source mismatch": func(f *evidenceFixture) {
			f.authorization.PlanningWorkflow.HeadSHA = strings.Repeat("b", 40)
		},
		"authorization planning workflow mismatch": func(f *evidenceFixture) {
			f.authorization.PlanningWorkflow.File = "build-binaries.yml"
		},
		"authorization generated PR head invalid": func(f *evidenceFixture) {
			f.authorization.GeneratedReleasePR.HeadSHA = strings.Repeat("A", 40)
		},
		"authorization generated PR merge source mismatch": func(f *evidenceFixture) {
			f.authorization.GeneratedReleasePR.MergeSHA = strings.Repeat("b", 40)
		},
		"authorization generated PR merge time invalid": func(f *evidenceFixture) {
			f.authorization.GeneratedReleasePR.MergedAt = "yesterday"
		},
		"authorization confirmation body mismatch": func(f *evidenceFixture) {
			f.authorization.Confirmation.BodySHA256 = strings.Repeat("0", 64)
		},
		"authorization confirmation actor is not trusted": func(f *evidenceFixture) {
			f.authorization.Confirmation.ActorAssociation = "CONTRIBUTOR"
		},
		"authorization confirmation URL mismatch": func(f *evidenceFixture) {
			f.authorization.Confirmation.URL = "https://example.invalid/comment"
		},
		"authorization confirmation created after merge": func(f *evidenceFixture) {
			f.authorization.Confirmation.CreatedAt = "2026-07-16T08:31:00Z"
			f.authorization.Confirmation.UpdatedAt = "2026-07-16T08:31:00Z"
		},
		"authorization confirmation created at merge second": func(f *evidenceFixture) {
			f.authorization.Confirmation.CreatedAt = f.authorization.GeneratedReleasePR.MergedAt
		},
		"authorization confirmation edited after merge": func(f *evidenceFixture) {
			f.authorization.Confirmation.UpdatedAt = "2026-07-16T08:31:00Z"
		},
		"authorization confirmation edited at merge second": func(f *evidenceFixture) {
			f.authorization.Confirmation.UpdatedAt = f.authorization.GeneratedReleasePR.MergedAt
		},
		"authorization PR CI failed": func(f *evidenceFixture) {
			f.authorization.ReleasePRCI.Conclusion = "failure"
		},
		"authorization PR CI head invalid": func(f *evidenceFixture) {
			f.authorization.ReleasePRCI.HeadSHA = "merge"
		},
		"authorization PR CI generated head mismatch": func(f *evidenceFixture) {
			f.authorization.ReleasePRCI.GeneratedReleasePRHeadSHA = strings.Repeat("d", 40)
		},
		"authorization PR CI number mismatch": func(f *evidenceFixture) {
			f.authorization.ReleasePRCI.PullRequestNumber++
		},
		"authorization PR CI workflow mismatch": func(f *evidenceFixture) {
			f.authorization.ReleasePRCI.File = "build-binaries.yml"
		},
		"authorization PR CI event mismatch": func(f *evidenceFixture) {
			f.authorization.ReleasePRCI.Event = "push"
		},
		"authorization evidence attempt missing": func(f *evidenceFixture) {
			f.authorization.EvidenceWorkflow.RunAttempt = 0
		},
		"authorization evidence identity mismatch": func(f *evidenceFixture) {
			f.authorization.EvidenceWorkflow.ID = "publisher"
		},
		"repository settings proof omitted": func(f *evidenceFixture) {
			f.observation.RepositoryReleaseSettings = releasesettings.Proof{}
		},
		"repository settings exact repository mismatch": func(f *evidenceFixture) {
			f.observation.RepositoryReleaseSettings.Tuple.Repository = "other/env-vault"
			resealRepositorySettingsProof(t, &f.observation.RepositoryReleaseSettings)
		},
		"repository settings exact version mismatch": func(f *evidenceFixture) {
			f.observation.RepositoryReleaseSettings.Tuple.ReleaseVersion = "v9.8.6"
			resealRepositorySettingsProof(t, &f.observation.RepositoryReleaseSettings)
		},
		"repository settings exact source mismatch": func(f *evidenceFixture) {
			f.observation.RepositoryReleaseSettings.Tuple.SourceSHA = strings.Repeat("b", 40)
			resealRepositorySettingsProof(t, &f.observation.RepositoryReleaseSettings)
		},
		"repository settings planning run mismatch": func(f *evidenceFixture) {
			f.observation.RepositoryReleaseSettings.Tuple.PlanningRunID++
			resealRepositorySettingsProof(t, &f.observation.RepositoryReleaseSettings)
		},
		"repository settings planning attempt mismatch": func(f *evidenceFixture) {
			f.observation.RepositoryReleaseSettings.Tuple.PlanningRunAttempt++
			resealRepositorySettingsProof(t, &f.observation.RepositoryReleaseSettings)
		},
		"repository settings raw document tampered": func(f *evidenceFixture) {
			f.observation.RepositoryReleaseSettings.Inputs.TagRuleset.DocumentJSON += "\n"
			resealRepositorySettingsProof(t, &f.observation.RepositoryReleaseSettings)
		},
		"repository settings schema absent from contract": func(f *evidenceFixture) {
			delete(f.contract.Schemas, "repository_release_settings_proof")
		},
		"repository settings checked after observation": func(f *evidenceFixture) {
			f.observation.RepositoryReleaseSettings.Tuple.CheckedAt = "2026-07-16T09:21:00Z"
			resealRepositorySettingsProof(t, &f.observation.RepositoryReleaseSettings)
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			fixture := cloneFixture(t, base)
			mutate(fixture)
			if _, err := Assemble(fixture.contract, fixture.authorization, fixture.manifest, fixture.ci, fixture.publisher, fixture.observation, fixture.attestationBundle); err == nil {
				t.Fatal("inconsistent release state was accepted")
			}
		})
	}
}

func TestEvidenceBindsWorkflowDispatchRepairMode(t *testing.T) {
	fixture := newEvidenceFixture(t)
	fixture.publisher.Event = "workflow_dispatch"
	fixture.observation.PublisherRepairMode = "health"
	evidence, err := Assemble(fixture.contract, fixture.authorization, fixture.manifest, fixture.ci, fixture.publisher, fixture.observation, fixture.attestationBundle)
	if err != nil {
		t.Fatalf("assemble exact health repair evidence: %v", err)
	}
	if evidence.PublisherRepairMode != "health" {
		t.Fatalf("publisher repair mode=%q, want health", evidence.PublisherRepairMode)
	}
	if err := Verify(evidence, fixture.contract); err != nil {
		t.Fatalf("verify exact health repair evidence: %v", err)
	}
}

func TestEvidenceAcceptsOnlyBoundedRunLevelClockSkew(t *testing.T) {
	fixture := newEvidenceFixture(t)
	fixture.publisher.CreatedAt = "2026-07-16T09:00:01Z"
	fixture.publisher.QueueSeconds = 0
	fixture.publisher.Jobs[0].StartedAt = "2026-07-16T09:00:01Z"
	fixture.publisher.Jobs[0].RunnerSeconds = 599
	fixture.publisher.AggregateRunnerSeconds = 599
	if _, err := Assemble(fixture.contract, fixture.authorization, fixture.manifest, fixture.ci, fixture.publisher, fixture.observation, fixture.attestationBundle); err != nil {
		t.Fatalf("bounded publisher clock skew was rejected: %v", err)
	}

	fixture.publisher.CreatedAt = "2026-07-16T09:00:03Z"
	if _, err := Assemble(fixture.contract, fixture.authorization, fixture.manifest, fixture.ci, fixture.publisher, fixture.observation, fixture.attestationBundle); err == nil {
		t.Fatal("publisher clock skew beyond two seconds was accepted")
	}

	fixture.publisher.CreatedAt = "2026-07-16T09:00:01Z"
	fixture.publisher.Jobs[0].StartedAt = "2026-07-16T09:00:00Z"
	if _, err := Assemble(fixture.contract, fixture.authorization, fixture.manifest, fixture.ci, fixture.publisher, fixture.observation, fixture.attestationBundle); err == nil {
		t.Fatal("publisher job predating attempt creation was accepted")
	}
}

func TestEvidenceSelfDigestRejectsPostAssemblyTampering(t *testing.T) {
	fixture := newEvidenceFixture(t)
	evidence, err := Assemble(fixture.contract, fixture.authorization, fixture.manifest, fixture.ci, fixture.publisher, fixture.observation, fixture.attestationBundle)
	if err != nil {
		t.Fatal(err)
	}
	evidence.Homebrew.FormulaSHA256 = strings.Repeat("b", 64)
	if err := Verify(evidence, fixture.contract); err == nil {
		t.Fatal("post-assembly tampering was accepted")
	}

	fixture = cloneFixture(t, fixture)
	evidence, err = Assemble(fixture.contract, fixture.authorization, fixture.manifest, fixture.ci, fixture.publisher, fixture.observation, fixture.attestationBundle)
	if err != nil {
		t.Fatal(err)
	}
	evidence.EvidenceSHA256 = strings.Repeat("0", 64)
	if code := ErrorCode(Verify(evidence, fixture.contract)); code != CodeDigestMismatch {
		t.Fatalf("self-digest error code=%q, want %q", code, CodeDigestMismatch)
	}

	fixture = newEvidenceFixture(t)
	evidence, err = Assemble(fixture.contract, fixture.authorization, fixture.manifest, fixture.ci, fixture.publisher, fixture.observation, fixture.attestationBundle)
	if err != nil {
		t.Fatal(err)
	}
	evidence.Authorization.PlanningWorkflow.HeadSHA = strings.Repeat("b", 40)
	evidence.EvidenceSHA256, err = EvidenceSHA256(evidence)
	if err != nil {
		t.Fatal(err)
	}
	if code := ErrorCode(Verify(evidence, fixture.contract)); code != CodeSourceMismatch {
		t.Fatalf("replayed authorization error code=%q, want %q", code, CodeSourceMismatch)
	}

	fixture = newEvidenceFixture(t)
	evidence, err = Assemble(fixture.contract, fixture.authorization, fixture.manifest, fixture.ci, fixture.publisher, fixture.observation, fixture.attestationBundle)
	if err != nil {
		t.Fatal(err)
	}
	var rawEntries []ghAttestationVerificationEntry
	if err := decodeStrict([]byte(evidence.AttestationVerificationBundle.Entries[0].DocumentJSON), &rawEntries); err != nil {
		t.Fatal(err)
	}
	rawEntries[0].VerificationResult.Signature.Certificate.BuildSignerURI = "https://github.com/" + testRepository + "/.github/workflows/build-binaries.yml@refs/heads/main"
	tamperedDocument, err := json.Marshal(rawEntries)
	if err != nil {
		t.Fatal(err)
	}
	tamperedDigest := sha256.Sum256(tamperedDocument)
	tamperedDigestText := hex.EncodeToString(tamperedDigest[:])
	evidence.AttestationVerificationBundle.Entries[0].DocumentJSON = string(tamperedDocument)
	evidence.AttestationVerificationBundle.Entries[0].DocumentSHA256 = tamperedDigestText
	evidence.Attestations[0].Provenance.DocumentSHA256 = tamperedDigestText
	evidence.EvidenceSHA256, err = EvidenceSHA256(evidence)
	if err != nil {
		t.Fatal(err)
	}
	if code := ErrorCode(Verify(evidence, fixture.contract)); code != CodeAttestationVerificationFailed {
		t.Fatalf("replayed attestation bundle error code=%q, want %q", code, CodeAttestationVerificationFailed)
	}

	fixture = newEvidenceFixture(t)
	evidence, err = Assemble(fixture.contract, fixture.authorization, fixture.manifest, fixture.ci, fixture.publisher, fixture.observation, fixture.attestationBundle)
	if err != nil {
		t.Fatal(err)
	}
	evidence.RepositoryReleaseSettings.Tuple.PlanningRunAttempt++
	resealRepositorySettingsProof(t, &evidence.RepositoryReleaseSettings)
	evidence.EvidenceSHA256, err = EvidenceSHA256(evidence)
	if err != nil {
		t.Fatal(err)
	}
	if code := ErrorCode(Verify(evidence, fixture.contract)); code != CodeSourceMismatch {
		t.Fatalf("replayed repository settings tuple error code=%q, want %q", code, CodeSourceMismatch)
	}
}

func TestHealthProofCannotBeSilentlyResealed(t *testing.T) {
	fixture := newEvidenceFixture(t)
	if err := SealHealthProof(&fixture.observation.Health); err == nil {
		t.Fatal("already sealed health proof was resealed")
	}
}

func TestStrictEvidenceInputsRejectUnknownDuplicateCaseVariantAndTrailingJSON(t *testing.T) {
	fixture := newEvidenceFixture(t)
	observation, err := MarshalJSON(fixture.observation)
	if err != nil {
		t.Fatal(err)
	}
	trimmed := bytes.TrimSpace(observation)
	unknown := append(append([]byte{}, trimmed[:len(trimmed)-1]...), []byte(`,"token":"secret"}`)...)
	if _, err := ParseObservation(unknown); err == nil {
		t.Fatal("unknown field was accepted")
	}
	duplicate := append([]byte(`{"schema_id":"duplicate",`), trimmed[1:]...)
	if _, err := ParseObservation(duplicate); err == nil {
		t.Fatal("duplicate field was accepted")
	}
	caseVariant := append([]byte(`{"SCHEMA_ID":"duplicate",`), trimmed[1:]...)
	if _, err := ParseObservation(caseVariant); err == nil {
		t.Fatal("case-variant duplicate field was accepted")
	}
	if _, err := ParseObservation(append(append([]byte{}, trimmed...), []byte(` {}`)...)); err == nil {
		t.Fatal("trailing JSON document was accepted")
	}
}

func TestStrictAuthorizationRejectsUnknownDuplicateCaseVariantAndTrailingJSON(t *testing.T) {
	fixture := newEvidenceFixture(t)
	encoded, err := MarshalJSON(fixture.authorization)
	if err != nil {
		t.Fatal(err)
	}
	if parsed, err := ParseAuthorization(encoded); err != nil || parsed.GeneratedReleasePR != fixture.authorization.GeneratedReleasePR {
		t.Fatalf("valid authorization parse=%+v err=%v", parsed, err)
	}
	trimmed := bytes.TrimSpace(encoded)
	unknown := append(append([]byte{}, trimmed[:len(trimmed)-1]...), []byte(`,"token":"secret"}`)...)
	if _, err := ParseAuthorization(unknown); err == nil {
		t.Fatal("unknown authorization field was accepted")
	}
	duplicate := append([]byte(`{"schema_id":"duplicate",`), trimmed[1:]...)
	if _, err := ParseAuthorization(duplicate); err == nil {
		t.Fatal("duplicate authorization field was accepted")
	}
	caseVariant := append([]byte(`{"SCHEMA_ID":"duplicate",`), trimmed[1:]...)
	if _, err := ParseAuthorization(caseVariant); err == nil {
		t.Fatal("case-variant authorization field was accepted")
	}
	if _, err := ParseAuthorization(append(append([]byte{}, trimmed...), []byte(` {}`)...)); err == nil {
		t.Fatal("trailing authorization JSON was accepted")
	}
}

func TestStrictAttestationVerificationBundleRejectsUnknownDuplicateCaseVariantAndTrailingJSON(t *testing.T) {
	fixture := newEvidenceFixture(t)
	encoded, err := MarshalJSON(fixture.attestationBundle)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseAttestationVerificationBundle(encoded)
	if err != nil || len(parsed.Entries) != 10 {
		t.Fatalf("valid bundle parse entries=%d err=%v", len(parsed.Entries), err)
	}
	trimmed := bytes.TrimSpace(encoded)
	unknown := append(append([]byte{}, trimmed[:len(trimmed)-1]...), []byte(`,"token":"secret"}`)...)
	if _, err := ParseAttestationVerificationBundle(unknown); err == nil {
		t.Fatal("unknown bundle field was accepted")
	}
	duplicate := append([]byte(`{"schema_id":"duplicate",`), trimmed[1:]...)
	if _, err := ParseAttestationVerificationBundle(duplicate); err == nil {
		t.Fatal("duplicate bundle field was accepted")
	}
	caseVariant := append([]byte(`{"SCHEMA_ID":"duplicate",`), trimmed[1:]...)
	if _, err := ParseAttestationVerificationBundle(caseVariant); err == nil {
		t.Fatal("case-variant bundle field was accepted")
	}
	if _, err := ParseAttestationVerificationBundle(append(append([]byte{}, trimmed...), []byte(` {}`)...)); err == nil {
		t.Fatal("trailing bundle JSON was accepted")
	}
}

func TestAttestationVerificationBundleRejectsAdversarialRawGHDocuments(t *testing.T) {
	base := newEvidenceFixture(t)
	tests := map[string]func(*evidenceFixture){
		"wrong predicate": func(f *evidenceFixture) {
			mutateAttestationDocument(t, f, 0, func(entries []ghAttestationVerificationEntry) {
				entries[0].VerificationResult.Statement.PredicateType = SBOMPredicate
			})
		},
		"wrong workflow SHA": func(f *evidenceFixture) {
			mutateAttestationDocument(t, f, 0, func(entries []ghAttestationVerificationEntry) {
				entries[0].VerificationResult.Signature.Certificate.GitHubWorkflowSHA = strings.Repeat("b", 40)
			})
		},
		"wrong source digest": func(f *evidenceFixture) {
			mutateAttestationDocument(t, f, 0, func(entries []ghAttestationVerificationEntry) {
				entries[0].VerificationResult.Signature.Certificate.SourceRepositoryDigest = strings.Repeat("b", 40)
			})
		},
		"wrong repository": func(f *evidenceFixture) {
			mutateAttestationDocument(t, f, 0, func(entries []ghAttestationVerificationEntry) {
				entries[0].VerificationResult.Signature.Certificate.GitHubWorkflowRepository = "other/repository"
			})
		},
		"branch signer instead of exact tag": func(f *evidenceFixture) {
			mutateAttestationDocument(t, f, 0, func(entries []ghAttestationVerificationEntry) {
				entries[0].VerificationResult.Signature.Certificate.BuildSignerURI = "https://github.com/" + testRepository + "/.github/workflows/build-binaries.yml@refs/heads/main"
			})
		},
		"wrong run repository": func(f *evidenceFixture) {
			mutateAttestationDocument(t, f, 0, func(entries []ghAttestationVerificationEntry) {
				entries[0].VerificationResult.Signature.Certificate.RunInvocationURI = "https://github.com/other/repository/actions/runs/525252/attempts/1"
			})
		},
		"run without attempt": func(f *evidenceFixture) {
			mutateAttestationDocument(t, f, 0, func(entries []ghAttestationVerificationEntry) {
				entries[0].VerificationResult.Signature.Certificate.RunInvocationURI = "https://github.com/" + testRepository + "/actions/runs/525252"
			})
		},
		"missing archive subject": func(f *evidenceFixture) {
			mutateAttestationDocument(t, f, 0, func(entries []ghAttestationVerificationEntry) {
				entries[0].VerificationResult.Statement.Subject = entries[0].VerificationResult.Statement.Subject[:4]
			})
		},
		"duplicate archive subject": func(f *evidenceFixture) {
			mutateAttestationDocument(t, f, 0, func(entries []ghAttestationVerificationEntry) {
				entries[0].VerificationResult.Statement.Subject[4] = entries[0].VerificationResult.Statement.Subject[0]
			})
		},
		"wrong archive digest": func(f *evidenceFixture) {
			mutateAttestationDocument(t, f, 0, func(entries []ghAttestationVerificationEntry) {
				entries[0].VerificationResult.Statement.Subject[0].Digest.SHA256 = strings.Repeat("f", 64)
			})
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			fixture := cloneFixture(t, base)
			mutate(fixture)
			if _, err := Assemble(fixture.contract, fixture.authorization, fixture.manifest, fixture.ci, fixture.publisher, fixture.observation, fixture.attestationBundle); ErrorCode(err) != CodeAttestationVerificationFailed {
				t.Fatalf("raw gh document error=%v code=%q", err, ErrorCode(err))
			}
		})
	}

	t.Run("one invalid verification among several", func(t *testing.T) {
		fixture := cloneFixture(t, base)
		var entries []ghAttestationVerificationEntry
		if err := decodeStrict([]byte(fixture.attestationBundle.Entries[0].DocumentJSON), &entries); err != nil {
			t.Fatal(err)
		}
		valid := entries[0]
		invalid := valid
		invalid.VerificationResult.Signature.Certificate.SourceRepositoryDigest = strings.Repeat("b", 40)
		setTypedAttestationDocument(t, fixture, 0, []ghAttestationVerificationEntry{valid, invalid})
		if _, err := Assemble(fixture.contract, fixture.authorization, fixture.manifest, fixture.ci, fixture.publisher, fixture.observation, fixture.attestationBundle); ErrorCode(err) != CodeAttestationVerificationFailed {
			t.Fatalf("invalid non-selected raw entry error=%v code=%q", err, ErrorCode(err))
		}
	})
}

func TestAttestationVerificationBundleRejectsUnknownDuplicateAndCaseVariantRawFields(t *testing.T) {
	tests := map[string]func(string) string{
		"unknown": func(document string) string {
			return strings.Replace(document, `[{"attestation":`, `[{"unexpected":true,"attestation":`, 1)
		},
		"unknown certificate field": func(document string) string {
			return strings.Replace(document, `"githubWorkflowSHA":`, `"unexpectedCertificateField":true,"githubWorkflowSHA":`, 1)
		},
		"duplicate": func(document string) string {
			return strings.Replace(document, `[{"attestation":`, `[{"attestation":{},"attestation":`, 1)
		},
		"case variant": func(document string) string {
			return strings.Replace(document, `[{"attestation":`, `[{"Attestation":{},"attestation":`, 1)
		},
		"trailing JSON": func(document string) string {
			return document + ` {}`
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			fixture := newEvidenceFixture(t)
			document := mutate(fixture.attestationBundle.Entries[0].DocumentJSON)
			if document == fixture.attestationBundle.Entries[0].DocumentJSON {
				t.Fatal("test mutation did not change the raw document")
			}
			setAttestationDocumentBytes(fixture, 0, []byte(document))
			if _, err := Assemble(fixture.contract, fixture.authorization, fixture.manifest, fixture.ci, fixture.publisher, fixture.observation, fixture.attestationBundle); ErrorCode(err) != CodeAttestationVerificationFailed {
				t.Fatalf("strict raw field error=%v code=%q", err, ErrorCode(err))
			}
		})
	}
}

func TestAttestationVerificationSelectionUsesMaximumRunThenAttempt(t *testing.T) {
	fixture := newEvidenceFixture(t)
	var entries []ghAttestationVerificationEntry
	if err := decodeStrict([]byte(fixture.attestationBundle.Entries[0].DocumentJSON), &entries); err != nil {
		t.Fatal(err)
	}
	withInvocation := func(entry ghAttestationVerificationEntry, runID int64, attempt int) ghAttestationVerificationEntry {
		entry.VerificationResult.Signature.Certificate.RunInvocationURI = fmt.Sprintf("https://github.com/%s/actions/runs/%d/attempts/%d", testRepository, runID, attempt)
		return entry
	}
	entries = []ghAttestationVerificationEntry{
		withInvocation(entries[0], 700, 99),
		withInvocation(entries[0], 800, 2),
		withInvocation(entries[0], 800, 4),
	}
	setTypedAttestationDocument(t, fixture, 0, entries)
	fixture.observation.Attestations[0].Provenance.WorkflowRunID = 800
	fixture.observation.Attestations[0].Provenance.WorkflowRunAttempt = 4
	if _, err := Assemble(fixture.contract, fixture.authorization, fixture.manifest, fixture.ci, fixture.publisher, fixture.observation, fixture.attestationBundle); err != nil {
		t.Fatalf("deterministically selected newest run/attempt was rejected: %v", err)
	}
	fixture.observation.Attestations[0].Provenance.WorkflowRunAttempt = 2
	if _, err := Assemble(fixture.contract, fixture.authorization, fixture.manifest, fixture.ci, fixture.publisher, fixture.observation, fixture.attestationBundle); ErrorCode(err) != CodeAttestationVerificationFailed {
		t.Fatalf("non-maximal typed selection error=%v code=%q", err, ErrorCode(err))
	}
}

func newEvidenceFixture(t *testing.T) *evidenceFixture {
	t.Helper()
	contractPath := filepath.Join("..", "..", "release", "contract.v1.json")
	contract, err := releasecontract.LoadFile(contractPath)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	proofPaths := make([]string, 0, len(contract.Platforms))
	proofs := make([]releasepromotion.PlatformProof, 0, len(contract.Platforms))
	for _, platform := range contract.Platforms {
		archiveData := []byte("archive bytes for " + platform.ID)
		archive := filepath.Join(root, platform.Archive)
		writeFile(t, archive, archiveData, 0o600)
		archiveDigest := sha256.Sum256(archiveData)
		checksum := filepath.Join(root, platform.Checksum)
		writeFile(t, checksum, []byte(hex.EncodeToString(archiveDigest[:])+"  "+platform.Archive+"\n"), 0o600)
		binaryDir := filepath.Join(root, platform.ID)
		if err := os.Mkdir(binaryDir, 0o700); err != nil {
			t.Fatal(err)
		}
		binary := filepath.Join(binaryDir, platform.Binary)
		script := fmt.Sprintf("#!/bin/sh\ncase \"$*\" in\n  --version|version) printf '%s\\n' ;;\n  'version --json') printf '%%s\\n' '%s' ;;\n  *) exit 2 ;;\nesac\n", testVersion, fmt.Sprintf(`{"ok":true,"command":"version","timestamp":%q,"data":{"version":%q},"warnings":[],"error":null}`, testTime.Format(time.RFC3339), testVersion))
		writeFile(t, binary, []byte(script), 0o700)
		binaryDigest := sha256.Sum256([]byte(script))
		versionEvidence := releasepromotion.LiteralVersionEvidence{
			SchemaID: releasepromotion.LiteralVersionEvidenceSchema, SchemaVersion: releasepromotion.SchemaVersion,
			PlatformID: platform.ID, SourceSHA: testSource, ReleaseVersion: testVersion,
			Repository: testRepository, RunID: testCIRun, RunAttempt: testAttempt,
			Binary: releasepromotion.FileDigest{Name: platform.Binary, Size: int64(len(script)), SHA256: hex.EncodeToString(binaryDigest[:])},
			Commands: []releasepromotion.LiteralVersionCommand{
				{Surface: "flag", Args: []string{"--version"}, Stdout: testVersion + "\n", Stderr: "", ExitCode: 0},
				{Surface: "command", Args: []string{"version"}, Stdout: testVersion + "\n", Stderr: "", ExitCode: 0},
				{Surface: "json", Args: []string{"version", "--json"}, Stdout: fmt.Sprintf(`{"ok":true,"command":"version","timestamp":%q,"data":{"version":%q},"warnings":[],"error":null}`+"\n", testTime.Format(time.RFC3339), testVersion), Stderr: "", ExitCode: 0},
			},
			Results: releasepromotion.LiteralVersionResults{Flag: testVersion, Command: testVersion, JSON: testVersion}, Result: "pass",
		}
		versionEvidence.EvidenceSHA256, err = releasepromotion.LiteralVersionEvidenceSHA256(versionEvidence)
		if err != nil {
			t.Fatal(err)
		}
		versionResults := filepath.Join(binaryDir, platform.ID+"-version-results.json")
		writeJSON(t, versionResults, versionEvidence)
		proof, err := releasepromotion.RecordPlatform(releasepromotion.RecordOptions{
			ContractPath: contractPath, PlatformID: platform.ID, SourceSHA: testSource,
			ReleaseVersion: testVersion, Repository: testRepository, RunID: testCIRun,
			RunAttempt: testAttempt, ArchivePath: archive, ChecksumPath: checksum, BinaryPath: binary,
			VersionResultsPath: versionResults,
		})
		if err != nil {
			t.Fatal(err)
		}
		proofPath := filepath.Join(root, platform.ID+"-proof.json")
		writeJSON(t, proofPath, proof)
		proofPaths = append(proofPaths, proofPath)
		proofs = append(proofs, proof)
	}

	ciWorkflow, _ := contract.WorkflowByID("ci")
	sourceProof := releasepromotion.SourceQualityProof{
		SchemaID: contract.Schemas["source_quality_proof"], SchemaVersion: releasepromotion.SchemaVersion,
		SourceSHA: testSource, ReleaseVersion: testVersion, Repository: testRepository,
		Workflow:     releasepromotion.Workflow{ID: ciWorkflow.ID, Name: ciWorkflow.Name, File: ciWorkflow.File, RunID: testCIRun, RunAttempt: testAttempt, Event: "push", HeadSHA: testSource},
		ObservedJobs: releasepromotion.SourceQualityObservedJobs{SourceQuality: "success", LicenseMatrix: "success"},
	}
	if err := releasepromotion.SealSourceQualityProof(&sourceProof, contract); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(root, "source-quality.json")
	writeJSON(t, sourcePath, sourceProof)

	matrix := makeMatrix(t, contract, proofs)
	matrixPath := filepath.Join(root, "matrix.json")
	writeJSON(t, matrixPath, matrix)
	manifest, err := releasepromotion.Assemble(releasepromotion.AssembleOptions{
		ContractPath: contractPath, SourceSHA: testSource, ReleaseVersion: testVersion,
		Repository: testRepository, RunID: testCIRun, RunAttempt: testAttempt,
		SourceQualityPath: sourcePath, MatrixProofPath: matrixPath, PlatformProofPaths: proofPaths,
		CreatedAt: testTime,
	})
	if err != nil {
		t.Fatal(err)
	}

	ci := validMetrics(testCIRun, testAttempt, ciWorkflow.Name, "push", "2026-07-16T08:00:00Z", "2026-07-16T08:01:00Z", "2026-07-16T08:10:00Z")
	publisherWorkflow, _ := contract.WorkflowByID("publisher")
	publisher := validMetrics(testPublisher, 1, publisherWorkflow.Name, "push", "2026-07-16T09:00:00Z", "2026-07-16T09:00:00Z", "2026-07-16T09:10:00Z")
	observation := makeObservation(t, contract, manifest, publisher)
	attestationBundle := makeAttestationVerificationBundle(t, contract, manifest, publisher, &observation)
	authorization := makeAuthorization(t, contract, manifest)
	observation.RepositoryReleaseSettings = makeRepositoryReleaseSettingsProof(t, authorization)
	return &evidenceFixture{contract: contract, authorization: authorization, manifest: manifest, ci: ci, publisher: publisher, observation: observation, attestationBundle: attestationBundle}
}

func makeRepositoryReleaseSettingsProof(t *testing.T, authorization Authorization) releasesettings.Proof {
	t.Helper()
	proof, err := releasesettings.Seal(releasesettings.Tuple{
		Repository:         authorization.Repository,
		SourceSHA:          authorization.ReleaseSourceSHA,
		ReleaseVersion:     authorization.ReleaseVersion,
		PlanningRunID:      authorization.PlanningWorkflow.RunID,
		PlanningRunAttempt: authorization.PlanningWorkflow.RunAttempt,
		CheckedAt:          "2026-07-16T08:45:00Z",
	}, releasesettings.RawInputs{
		MergeSettings:   []byte(`{"data":{"repository":{"defaultBranchRef":{"name":"main"},"mergeCommitAllowed":false,"rebaseMergeAllowed":false,"squashMergeAllowed":true,"squashMergeCommitTitle":"PR_TITLE","squashMergeCommitMessage":"PR_BODY"}}}`),
		RulesetPages:    []byte(`[[{"id":7,"name":"Protect env-vault main","target":"branch","source_type":"Repository","enforcement":"active"},{"id":8,"name":"Protect env-vault release tags","target":"tag","source_type":"Repository","enforcement":"active"},{"id":9,"name":"Protect env-vault release evidence","target":"branch","source_type":"Repository","enforcement":"active"}]]`),
		MainRuleset:     []byte(fmt.Sprintf(`{"id":7,"name":"Protect env-vault main","target":"branch","source_type":"Repository","source":%q,"enforcement":"active","bypass_actors":[],"current_user_can_bypass":"never","conditions":{"ref_name":{"include":["refs/heads/main"],"exclude":[]}},"rules":[{"type":"deletion"},{"type":"non_fast_forward"},{"type":"pull_request","parameters":{"required_review_thread_resolution":true,"allowed_merge_methods":["squash"]}},{"type":"required_status_checks","parameters":{"strict_required_status_checks_policy":true,"do_not_enforce_on_create":false,"required_status_checks":[{"context":"quality-gate","integration_id":15368},{"context":"pr-title","integration_id":15368},{"context":"Dependency review","integration_id":15368},{"context":"Analyze (go)","integration_id":15368},{"context":"Analyze (actions)","integration_id":15368}]}}]}`, authorization.Repository)),
		TagRuleset:      []byte(fmt.Sprintf(`{"id":8,"name":"Protect env-vault release tags","target":"tag","source_type":"Repository","source":%q,"enforcement":"active","bypass_actors":[],"current_user_can_bypass":"never","conditions":{"ref_name":{"include":["refs/tags/v*"],"exclude":[]}},"rules":[{"type":"deletion"},{"type":"update"}]}`, authorization.Repository)),
		EvidenceRuleset: []byte(fmt.Sprintf(`{"id":9,"name":"Protect env-vault release evidence","target":"branch","source_type":"Repository","source":%q,"enforcement":"active","bypass_actors":[],"current_user_can_bypass":"never","conditions":{"ref_name":{"include":["refs/heads/release-evidence"],"exclude":[]}},"rules":[{"type":"deletion"},{"type":"non_fast_forward"}]}`, authorization.Repository)),
	})
	if err != nil {
		t.Fatal(err)
	}
	return proof
}

func resealRepositorySettingsProof(t testing.TB, proof *releasesettings.Proof) {
	t.Helper()
	digest, err := releasesettings.ProofSHA256(*proof)
	if err != nil {
		t.Fatal(err)
	}
	proof.ProofSHA256 = digest
}

func makeAuthorization(t *testing.T, contract releasecontract.Contract, manifest releasepromotion.Manifest) Authorization {
	t.Helper()
	planning, ok := contract.WorkflowByID("planning")
	if !ok {
		t.Fatal("planning workflow missing from contract")
	}
	evidenceWorkflow, ok := contract.WorkflowByID("release_evidence")
	if !ok {
		t.Fatal("release evidence workflow missing from contract")
	}
	ciWorkflow, ok := contract.WorkflowByID("ci")
	if !ok {
		t.Fatal("CI workflow missing from contract")
	}
	prHeadSHA := strings.Repeat("f", 40)
	prNumber := int64(21)
	canonicalBody := fmt.Sprintf("ПОДТВЕРЖДАЮ RELEASE %s PR #%d SHA %s", manifest.ReleaseVersion, prNumber, prHeadSHA)
	bodyDigest := sha256.Sum256([]byte(canonicalBody))
	return Authorization{
		SchemaID: AuthorizationSchemaID, SchemaVersion: AuthorizationSchemaVersion,
		Repository: manifest.Repository, ReleaseVersion: manifest.ReleaseVersion,
		GeneratedReleasePR: GeneratedReleasePRIdentity{
			Number: prNumber, HeadSHA: prHeadSHA, MergeSHA: manifest.SourceSHA, MergedAt: testTime.Format(time.RFC3339),
		},
		Confirmation: ReleaseConfirmation{
			CommentID: 818181, URL: fmt.Sprintf("https://github.com/%s/pull/%d#issuecomment-818181", manifest.Repository, prNumber),
			Actor: "ildarbinanas-design", ActorAssociation: "OWNER",
			CreatedAt: "2026-07-16T08:00:00Z", UpdatedAt: "2026-07-16T08:00:00Z",
			BodySHA256: hex.EncodeToString(bodyDigest[:]),
		},
		ReleaseSourceSHA: manifest.SourceSHA,
		PlanningWorkflow: CompletedContractWorkflow{
			ID: planning.ID, Name: planning.Name, File: planning.File,
			RunID: 313131, RunAttempt: 2, HeadSHA: manifest.SourceSHA, Conclusion: "success",
		},
		ReleasePRCI: ReleasePRCIIdentity{
			ID: ciWorkflow.ID, Name: ciWorkflow.Name, File: ciWorkflow.File,
			RunID: 323232, RunAttempt: 1, Event: "pull_request", HeadSHA: strings.Repeat("e", 40),
			PullRequestNumber: prNumber, GeneratedReleasePRHeadSHA: prHeadSHA, Conclusion: "success",
		},
		EvidenceWorkflow: ContractWorkflowInvocation{
			ID: evidenceWorkflow.ID, Name: evidenceWorkflow.Name, File: evidenceWorkflow.File,
			RunID: 333333, RunAttempt: 1,
		},
		Result: "pass",
	}
}

func makeMatrix(t *testing.T, contract releasecontract.Contract, proofs []releasepromotion.PlatformProof) e2ebaseline.MatrixProof {
	t.Helper()
	run := e2ebaseline.RunIdentity{
		CommitSHA: testSource, RunID: strconvInt(testCIRun), RunAttempt: strconvInt(int64(testAttempt)),
		Repository: testRepository, RunURL: fmt.Sprintf("https://github.com/%s/actions/runs/%d", testRepository, testCIRun),
	}
	matrix := e2ebaseline.MatrixProof{
		SchemaID: e2ebaseline.MatrixProofSchemaID, SchemaVersion: e2ebaseline.MatrixProofSchemaVersion,
		Mode: "validate-matrix", Status: "pass", Phase: "candidate", SuiteHash: strings.Repeat("9", 64),
		Run: run, GeneratedAt: testTime, Checks: []e2ebaseline.ProofCheck{{Name: "deep report validation", Status: "pass"}},
	}
	for index, platform := range contract.Platforms {
		raw := make(map[string]string)
		for _, name := range e2ebaseline.RequiredProofEvidenceFiles() {
			raw[name] = strings.Repeat("e", 64)
		}
		evidence := e2ebaseline.PlatformProof{
			ID: platform.ID, Phase: matrix.Phase, Run: run, SuiteHash: matrix.SuiteHash,
			GOOS: platform.GOOS, GOARCH: platform.GOARCH, GoVersion: "go1.26.5", GotestsumVersion: "v1.13.0",
			SubjectKind: "artifact", BinarySHA256: proofs[index].Binary.SHA256,
			Artifact:       e2ebaseline.ArtifactProof{Archive: platform.Archive, Checksum: platform.Checksum, Format: platform.ArchiveFormat, SHA256: proofs[index].Archive.SHA256, ChecksumVerified: true},
			ContractSHA256: strings.Repeat("d", 64), MetadataSHA256: strings.Repeat("f", 64), LeakSHA256: strings.Repeat("1", 64), EvidenceSHA256: raw,
			StatementCoveragePercent: 75, Counts: e2ebaseline.Counts{Passed: 1}, ExpectedSkips: []string{},
			CriticalScenarios: []e2ebaseline.ScenarioExpectation{{ID: "CLI_VERSION_FORMS", Result: "pass"}},
			Leak:              e2ebaseline.LeakExpectation{Status: "pass", FilesScanned: 5, RegistryRecords: 1},
		}
		if err := e2ebaseline.SealPlatformProof(&evidence); err != nil {
			t.Fatal(err)
		}
		matrix.Platforms = append(matrix.Platforms, platform.ID)
		matrix.PlatformEvidence = append(matrix.PlatformEvidence, evidence)
	}
	if err := matrix.Validate(contract); err != nil {
		t.Fatal(err)
	}
	return matrix
}

func validMetrics(runID int64, attempt int, workflow, event, created, started, completed string) releasemetrics.Metrics {
	createdAt, _ := time.Parse(time.RFC3339, created)
	startedAt, _ := time.Parse(time.RFC3339, started)
	completedAt, _ := time.Parse(time.RFC3339, completed)
	duration := int64(completedAt.Sub(startedAt) / time.Second)
	return releasemetrics.Metrics{
		SchemaID: releasemetrics.SchemaID, InputSchema: "gh-run-view.v1", RunID: runID, Attempt: attempt,
		WorkflowName: workflow, Event: event, HeadSHA: testSource, Conclusion: "success",
		CreatedAt: created, StartedAt: started, CompletedAt: completed,
		QueueSeconds: int64(startedAt.Sub(createdAt) / time.Second), WallSeconds: duration,
		JobCount: 1, AggregateRunnerSeconds: duration, RetryCount: attempt - 1,
		CriticalPath:            releasemetrics.CriticalPath{Method: "observed_terminal_span", Seconds: duration, TerminalJob: "quality-gate", Note: "Observed makespan."},
		ArtifactTransferSeconds: releasemetrics.TransferMetric{Steps: []string{}},
		CacheTransferSeconds:    releasemetrics.TransferMetric{Steps: []string{}},
		Jobs:                    []releasemetrics.JobMetric{{ID: runID + 1, Name: "quality-gate", Conclusion: "success", StartedAt: started, CompletedAt: completed, RunnerSeconds: duration}},
	}
}

func makeObservation(t *testing.T, contract releasecontract.Contract, manifest releasepromotion.Manifest, publisher releasemetrics.Metrics) Observation {
	t.Helper()
	releaseAssets := make([]ObservedAsset, 0, len(manifest.Assets))
	assetByName := make(map[string]releasepromotion.FileDigest, len(manifest.Assets))
	for _, asset := range manifest.Assets {
		releaseAssets = append(releaseAssets, ObservedAsset{Name: asset.Name, Size: asset.Size, SHA256: asset.SHA256})
		assetByName[asset.Name] = asset
	}
	publisherWorkflow, _ := contract.WorkflowByID("publisher")
	signer := testRepository + "/.github/workflows/" + publisherWorkflow.File
	attestations := make([]ArchiveAttestation, 0, len(contract.Platforms))
	for _, platform := range contract.Platforms {
		base := AttestationVerification{
			Verified: true, Repository: testRepository, SignerWorkflow: signer, SourceSHA: testSource,
			WorkflowRunID: publisher.RunID, WorkflowRunAttempt: publisher.Attempt, VerifiedAt: "2026-07-16T09:12:00Z",
		}
		provenance, sbom := base, base
		provenance.PredicateType, sbom.PredicateType = ProvenancePredicate, SBOMPredicate
		attestations = append(attestations, ArchiveAttestation{AssetName: platform.Archive, AssetSHA256: assetByName[platform.Archive].SHA256, Provenance: provenance, SBOM: sbom})
	}
	app, _ := contract.AppByID("homebrew_tap")
	prHead := strings.Repeat("b", 40)
	mergeSHA := strings.Repeat("c", 40)
	tapSHA := strings.Repeat("e", 40)
	homebrew := HomebrewObservation{
		Repository: app.Repository, FormulaPath: "Formula/env-vault.rb", FormulaSHA256: strings.Repeat("d", 64),
		Version: testVersion, VersionMonotonic: true, PRNumber: 42,
		PRURL: "https://github.com/" + app.Repository + "/pull/42", PRHeadSHA: prHead, PRMergeSHA: mergeSHA, TapSHA: tapSHA, MergeIsAncestorOfTap: true,
		PRHeadCI:    ExternalWorkflowRun{RunID: 626262, RunAttempt: 1, Workflow: app.CIWorkflowFile, Event: "pull_request", HeadSHA: prHead, Conclusion: "success", URL: "https://github.com/" + app.Repository + "/actions/runs/626262"},
		PostMergeCI: ExternalWorkflowRun{RunID: 727272, RunAttempt: 2, Workflow: app.CIWorkflowFile, Event: "push", HeadSHA: mergeSHA, Conclusion: "success", URL: "https://github.com/" + app.Repository + "/actions/runs/727272"},
	}
	health := HealthProof{
		SchemaID: HealthProofSchemaID, SchemaVersion: ObservationSchemaVersion,
		Repository: testRepository, ReleaseVersion: testVersion, SourceSHA: testSource,
		PublisherRunID: publisher.RunID, PublisherRunAttempt: publisher.Attempt, CheckedAt: "2026-07-16T09:15:00Z",
		TagExactSource: true, ReleasePublished: true, AssetsExact: true, AttestationsExact: true,
		HomebrewExact: true, HomebrewPRHeadCISuccess: true, HomebrewPostMergeCISuccess: true,
		BlockedVersionPolicyExact: true, Result: "pass",
	}
	if err := SealHealthProof(&health); err != nil {
		t.Fatal(err)
	}
	blocked := make([]BlockedVersionObservation, 0, len(contract.VersionPolicy.BlockedVersions))
	for _, policy := range contract.VersionPolicy.BlockedVersions {
		blocked = append(blocked, BlockedVersionObservation{Version: policy.Version, TagSHA: policy.TagSHA, TagExists: policy.TagMustRemain, ReleaseExists: false})
	}
	return Observation{
		SchemaID: ObservationSchemaID, SchemaVersion: ObservationSchemaVersion,
		Repository: testRepository, ReleaseVersion: testVersion, SourceSHA: testSource,
		PublisherRepairMode: "none", ObservedAt: "2026-07-16T09:20:00Z",
		Tag:          TagObservation{Name: testVersion, RefSHA: testSource, TargetSHA: testSource, Immutable: true, RulesetProtected: true},
		Release:      ReleaseObservation{State: "published", URL: "https://github.com/" + testRepository + "/releases/tag/" + testVersion, TagName: testVersion, TargetSHA: testSource, PublishedAt: "2026-07-16T09:11:00Z", NoClobberVerified: true, Assets: releaseAssets},
		Attestations: attestations, Homebrew: homebrew, BlockedVersions: blocked, Health: health,
	}
}

func makeAttestationVerificationBundle(t *testing.T, contract releasecontract.Contract, manifest releasepromotion.Manifest, publisher releasemetrics.Metrics, observation *Observation) AttestationVerificationBundle {
	t.Helper()
	publisherWorkflow, ok := contract.WorkflowByID("publisher")
	if !ok {
		t.Fatal("publisher workflow missing from contract")
	}
	wantSignerURI := "https://github.com/" + manifest.Repository + "/.github/workflows/" + publisherWorkflow.File + "@refs/tags/" + manifest.ReleaseVersion
	subjects := make([]ghStatementSubject, 0, len(contract.Platforms))
	archiveDigests := make(map[string]string, len(contract.Platforms))
	for _, asset := range manifest.Assets {
		archiveDigests[asset.Name] = asset.SHA256
	}
	for _, platform := range contract.Platforms {
		subjects = append(subjects, ghStatementSubject{Name: platform.Archive, Digest: ghStatementSubjectDigest{SHA256: archiveDigests[platform.Archive]}})
	}

	bundle := AttestationVerificationBundle{
		SchemaID: AttestationVerificationBundleSchemaID, SchemaVersion: AttestationVerificationBundleSchemaVersion,
		Repository: manifest.Repository, SourceSHA: manifest.SourceSHA,
	}
	for platformIndex, platform := range contract.Platforms {
		for _, expectation := range []struct {
			kind         string
			predicate    string
			verification *AttestationVerification
		}{
			{kind: "provenance", predicate: ProvenancePredicate, verification: &observation.Attestations[platformIndex].Provenance},
			{kind: "sbom", predicate: SBOMPredicate, verification: &observation.Attestations[platformIndex].SBOM},
		} {
			entry := ghAttestationVerificationEntry{}
			entry.Attestation.Bundle.MediaType = ghBundleMediaType
			entry.Attestation.Bundle.VerificationMaterial.Certificate.RawBytes = "base64-certificate"
			entry.Attestation.Bundle.DSSEEnvelope = ghDSSEEnvelope{Payload: "base64-payload", PayloadType: inTotoPayloadType, Signatures: []ghDSSESignature{{Sig: "base64-signature"}}}
			entry.VerificationResult.MediaType = ghVerificationResultMediaType
			entry.VerificationResult.Signature.Certificate = ghVerifiedCertificate{
				GitHubWorkflowSHA: manifest.SourceSHA, GitHubWorkflowRepository: manifest.Repository,
				SourceRepositoryDigest: manifest.SourceSHA, BuildSignerURI: wantSignerURI,
				RunInvocationURI: fmt.Sprintf("https://github.com/%s/actions/runs/%d/attempts/%d", manifest.Repository, publisher.RunID, publisher.Attempt),
			}
			entry.VerificationResult.Statement = ghStatement{
				Type: inTotoStatementType, Subject: append([]ghStatementSubject(nil), subjects...),
				PredicateType: expectation.predicate, Predicate: json.RawMessage(`{}`),
			}
			document, err := json.Marshal([]ghAttestationVerificationEntry{entry})
			if err != nil {
				t.Fatal(err)
			}
			documentDigest := sha256.Sum256(document)
			digest := hex.EncodeToString(documentDigest[:])
			expectation.verification.DocumentSHA256 = digest
			bundle.Entries = append(bundle.Entries, AttestationVerificationBundleEntry{
				AssetName: platform.Archive, Kind: expectation.kind, PredicateType: expectation.predicate,
				DocumentSHA256: digest, DocumentJSON: string(document),
			})
		}
	}
	return bundle
}

func writeJSON(t *testing.T, filename string, value any) {
	t.Helper()
	data, err := releasepromotion.MarshalJSON(value)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filename, data, 0o600)
}

func mutateAttestationDocument(t *testing.T, fixture *evidenceFixture, entryIndex int, mutate func([]ghAttestationVerificationEntry)) {
	t.Helper()
	var entries []ghAttestationVerificationEntry
	if err := decodeStrict([]byte(fixture.attestationBundle.Entries[entryIndex].DocumentJSON), &entries); err != nil {
		t.Fatal(err)
	}
	mutate(entries)
	setTypedAttestationDocument(t, fixture, entryIndex, entries)
}

func setTypedAttestationDocument(t *testing.T, fixture *evidenceFixture, entryIndex int, entries []ghAttestationVerificationEntry) {
	t.Helper()
	document, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	setAttestationDocumentBytes(fixture, entryIndex, document)
}

func setAttestationDocumentBytes(fixture *evidenceFixture, entryIndex int, document []byte) {
	digest := sha256.Sum256(document)
	digestText := hex.EncodeToString(digest[:])
	fixture.attestationBundle.Entries[entryIndex].DocumentJSON = string(document)
	fixture.attestationBundle.Entries[entryIndex].DocumentSHA256 = digestText
	archiveIndex := entryIndex / 2
	if entryIndex%2 == 0 {
		fixture.observation.Attestations[archiveIndex].Provenance.DocumentSHA256 = digestText
	} else {
		fixture.observation.Attestations[archiveIndex].SBOM.DocumentSHA256 = digestText
	}
}

func writeFile(t *testing.T, filename string, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(filename, data, mode); err != nil {
		t.Fatal(err)
	}
}

func strconvInt(value int64) string { return fmt.Sprintf("%d", value) }

func cloneFixture(t *testing.T, fixture *evidenceFixture) *evidenceFixture {
	t.Helper()
	contract, err := cloneJSON(fixture.contract)
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := cloneJSON(fixture.authorization)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := cloneJSON(fixture.manifest)
	if err != nil {
		t.Fatal(err)
	}
	ci, err := cloneJSON(fixture.ci)
	if err != nil {
		t.Fatal(err)
	}
	publisher, err := cloneJSON(fixture.publisher)
	if err != nil {
		t.Fatal(err)
	}
	observation, err := cloneJSON(fixture.observation)
	if err != nil {
		t.Fatal(err)
	}
	attestationBundle, err := cloneJSON(fixture.attestationBundle)
	if err != nil {
		t.Fatal(err)
	}
	return &evidenceFixture{contract: contract, authorization: authorization, manifest: manifest, ci: ci, publisher: publisher, observation: observation, attestationBundle: attestationBundle}
}
