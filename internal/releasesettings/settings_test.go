package releasesettings

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestSealAndVerifyExactRepositorySettingsProof(t *testing.T) {
	tuple := validTuple()
	proof, err := Seal(tuple, validRawInputs())
	if err != nil {
		t.Fatalf("seal valid settings: %v", err)
	}
	if proof.SchemaID != SchemaID || proof.SchemaVersion != SchemaVersion || proof.Result != ResultPass || proof.ProofSHA256 == "" {
		t.Fatalf("unexpected sealed proof: %+v", proof)
	}
	if err := Verify(proof, tuple); err != nil {
		t.Fatalf("verify valid settings: %v", err)
	}

	encoded, err := MarshalJSON(proof)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseProof(encoded)
	if err != nil {
		t.Fatalf("parse sealed proof: %v", err)
	}
	if err := Verify(parsed, tuple); err != nil {
		t.Fatalf("verify parsed settings: %v", err)
	}
}

func TestVerifyRejectsTamperEvenWhenOuterDigestIsRecomputed(t *testing.T) {
	tuple := validTuple()
	proof, err := Seal(tuple, validRawInputs())
	if err != nil {
		t.Fatal(err)
	}
	proof.Inputs.TagRuleset.DocumentJSON = strings.Replace(proof.Inputs.TagRuleset.DocumentJSON, `"update"`, `"non_fast_forward"`, 1)
	proof.ProofSHA256, err = ProofSHA256(proof)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(proof, tuple); ErrorCode(err) != CodeDigestMismatch {
		t.Fatalf("tampered embedded bytes error=%v code=%q, want %s", err, ErrorCode(err), CodeDigestMismatch)
	}

	data := []byte(proof.Inputs.TagRuleset.DocumentJSON)
	digest := sha256.Sum256(data)
	proof.Inputs.TagRuleset.SHA256 = hex.EncodeToString(digest[:])
	proof.ProofSHA256, err = ProofSHA256(proof)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(proof, tuple); ErrorCode(err) != CodePolicyInvalid {
		t.Fatalf("tampered policy error=%v code=%q, want %s", err, ErrorCode(err), CodePolicyInvalid)
	}
}

func TestVerifyRejectsWrongExpectedTuple(t *testing.T) {
	tuple := validTuple()
	proof, err := Seal(tuple, validRawInputs())
	if err != nil {
		t.Fatal(err)
	}
	wrong := tuple
	wrong.PlanningRunAttempt++
	if err := Verify(proof, wrong); ErrorCode(err) != CodeTupleMismatch {
		t.Fatalf("wrong tuple error=%v code=%q, want %s", err, ErrorCode(err), CodeTupleMismatch)
	}
}

func TestSealRejectsOmittedBypassFieldsOnEveryRuleset(t *testing.T) {
	for _, document := range []string{"main", "tag", "evidence"} {
		for _, field := range []string{"bypass_actors", "current_user_can_bypass"} {
			t.Run(document+"/"+field, func(t *testing.T) {
				raw := validRawInputs()
				var target *[]byte
				switch document {
				case "main":
					target = &raw.MainRuleset
				case "tag":
					target = &raw.TagRuleset
				case "evidence":
					target = &raw.EvidenceRuleset
				}
				needle := `,"bypass_actors":[]`
				if field == "current_user_can_bypass" {
					needle = `,"current_user_can_bypass":"never"`
				}
				changed := strings.Replace(string(*target), needle, "", 1)
				if changed == string(*target) {
					t.Fatalf("fixture did not contain %s", field)
				}
				*target = []byte(changed)
				if _, err := Seal(validTuple(), raw); ErrorCode(err) != CodePolicyInvalid {
					t.Fatalf("omitted %s error=%v code=%q, want %s", field, err, ErrorCode(err), CodePolicyInvalid)
				}
			})
		}
	}
}

func TestStrictFieldIdentityForProofAndRawSettings(t *testing.T) {
	proof, err := Seal(validTuple(), validRawInputs())
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := MarshalJSON(proof)
	if err != nil {
		t.Fatal(err)
	}
	nonCanonicalProof := strings.Replace(string(encoded), `"proof_sha256"`, `"Proof_SHA256"`, 1)
	if _, err := ParseProof([]byte(nonCanonicalProof)); ErrorCode(err) != CodeInputInvalid {
		t.Fatalf("non-canonical proof error=%v code=%q", err, ErrorCode(err))
	}

	raw := validRawInputs()
	raw.MergeSettings = []byte(strings.Replace(string(raw.MergeSettings), `"mergeCommitAllowed"`, `"MergeCommitAllowed"`, 1))
	if _, err := Seal(validTuple(), raw); ErrorCode(err) != CodeInputInvalid {
		t.Fatalf("non-canonical raw field error=%v code=%q", err, ErrorCode(err))
	}
}

func TestSealRejectsDuplicateCanonicalRulesetAndUnsafeMergePolicy(t *testing.T) {
	raw := validRawInputs()
	duplicate := `,{"id":10,"name":"Protect env-vault main","target":"branch","source_type":"Repository","enforcement":"active"}`
	raw.RulesetPages = []byte(strings.Replace(string(raw.RulesetPages), `}]]`, `}`+duplicate+`]]`, 1))
	if _, err := Seal(validTuple(), raw); ErrorCode(err) != CodePolicyInvalid {
		t.Fatalf("duplicate ruleset error=%v code=%q", err, ErrorCode(err))
	}

	raw = validRawInputs()
	raw.MergeSettings = []byte(strings.Replace(string(raw.MergeSettings), `"rebaseMergeAllowed":false`, `"rebaseMergeAllowed":true`, 1))
	if _, err := Seal(validTuple(), raw); ErrorCode(err) != CodePolicyInvalid {
		t.Fatalf("unsafe merge policy error=%v code=%q", err, ErrorCode(err))
	}
}

func validTuple() Tuple {
	return Tuple{
		Repository:         "example/env-vault",
		SourceSHA:          strings.Repeat("a", 40),
		ReleaseVersion:     "v0.0.9",
		PlanningRunID:      29475939348,
		PlanningRunAttempt: 2,
		CheckedAt:          "2026-07-16T12:34:56Z",
	}
}

func validRawInputs() RawInputs {
	return RawInputs{
		MergeSettings:   []byte(`{"data":{"repository":{"defaultBranchRef":{"name":"main"},"mergeCommitAllowed":false,"rebaseMergeAllowed":false,"squashMergeAllowed":true,"squashMergeCommitTitle":"PR_TITLE","squashMergeCommitMessage":"PR_BODY"}}}`),
		RulesetPages:    []byte(`[[{"id":7,"name":"Protect env-vault main","target":"branch","source_type":"Repository","enforcement":"active"},{"id":8,"name":"Protect env-vault release tags","target":"tag","source_type":"Repository","enforcement":"active"},{"id":9,"name":"Protect env-vault release evidence","target":"branch","source_type":"Repository","enforcement":"active"}]]`),
		MainRuleset:     []byte(`{"id":7,"name":"Protect env-vault main","target":"branch","source_type":"Repository","source":"example/env-vault","enforcement":"active","bypass_actors":[],"current_user_can_bypass":"never","conditions":{"ref_name":{"include":["refs/heads/main"],"exclude":[]}},"rules":[{"type":"deletion"},{"type":"non_fast_forward"},{"type":"pull_request","parameters":{"required_review_thread_resolution":true,"allowed_merge_methods":["squash"]}},{"type":"required_status_checks","parameters":{"strict_required_status_checks_policy":true,"do_not_enforce_on_create":false,"required_status_checks":[{"context":"quality-gate","integration_id":15368},{"context":"pr-title","integration_id":15368},{"context":"Dependency review","integration_id":15368},{"context":"Analyze (go)","integration_id":15368},{"context":"Analyze (actions)","integration_id":15368}]}}]}`),
		TagRuleset:      []byte(`{"id":8,"name":"Protect env-vault release tags","target":"tag","source_type":"Repository","source":"example/env-vault","enforcement":"active","bypass_actors":[],"current_user_can_bypass":"never","conditions":{"ref_name":{"include":["refs/tags/v*"],"exclude":[]}},"rules":[{"type":"deletion"},{"type":"update"}]}`),
		EvidenceRuleset: []byte(`{"id":9,"name":"Protect env-vault release evidence","target":"branch","source_type":"Repository","source":"example/env-vault","enforcement":"active","bypass_actors":[],"current_user_can_bypass":"never","conditions":{"ref_name":{"include":["refs/heads/release-evidence"],"exclude":[]}},"rules":[{"type":"deletion"},{"type":"non_fast_forward"}]}`),
	}
}
