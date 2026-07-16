package releasesettings

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestSealAndVerifyExactRepositorySettingsProof(t *testing.T) {
	tuple := validTuple()
	raw := validRawInputs()
	check, err := Check(tuple.Repository, raw)
	if err != nil {
		t.Fatalf("check valid settings: %v", err)
	}
	if !check.OK || check.SchemaID != CheckSchemaID || check.SchemaVersion != SchemaVersion || check.Repository != tuple.Repository || check.Result != ResultPass {
		t.Fatalf("unexpected check result: %+v", check)
	}
	proof, err := Seal(tuple, raw)
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

func TestRESTDetailAllowsOmittedOrExplicitEmptyBypassActors(t *testing.T) {
	for _, document := range []string{"main", "tag", "evidence"} {
		t.Run(document, func(t *testing.T) {
			raw := validRawInputs()
			target := rulesetDocument(&raw, document)
			changed := strings.Replace(string(*target), `,"bypass_actors":[]`, "", 1)
			if changed == string(*target) {
				t.Fatal("fixture did not contain bypass_actors")
			}
			*target = []byte(changed)
			if _, err := Check(validTuple().Repository, raw); err != nil {
				t.Fatalf("omitted bypass_actors was rejected: %v", err)
			}
		})
	}
}

func TestRESTDetailRejectsUnsafeBypassShapes(t *testing.T) {
	for _, document := range []string{"main", "tag", "evidence"} {
		for name, replacement := range map[string]string{
			"null actors":     `,"bypass_actors":null`,
			"nonempty actors": `,"bypass_actors":[{"actor_id":1,"actor_type":"RepositoryRole","bypass_mode":"always"}]`,
			"wrong actors":    `,"bypass_actors":{}`,
			"missing current": "",
			"null current":    `,"current_user_can_bypass":null`,
			"wrong current":   `,"current_user_can_bypass":"always"`,
		} {
			t.Run(document+"/"+name, func(t *testing.T) {
				raw := validRawInputs()
				target := rulesetDocument(&raw, document)
				needle := `,"bypass_actors":[]`
				if strings.Contains(name, "current") {
					needle = `,"current_user_can_bypass":"never"`
				}
				*target = []byte(strings.Replace(string(*target), needle, replacement, 1))
				if _, err := Check(validTuple().Repository, raw); err == nil {
					t.Fatal("unsafe REST bypass shape was accepted")
				}
			})
		}
	}
}

func TestGraphQLRulesetInventoryFailsClosed(t *testing.T) {
	tests := map[string]func(RawInputs) RawInputs{
		"GraphQL errors": func(raw RawInputs) RawInputs {
			raw.MergeSettings = replaceMerge(raw.MergeSettings, `{"data":`, `{"errors":[{"message":"forbidden"}],"data":`)
			return raw
		},
		"connection omitted": func(raw RawInputs) RawInputs {
			raw.MergeSettings = []byte(strings.Replace(string(raw.MergeSettings), `,"rulesets":`+validGraphQLRulesets(), "", 1))
			return raw
		},
		"paginated": func(raw RawInputs) RawInputs {
			raw.MergeSettings = replaceMerge(raw.MergeSettings, `"hasNextPage":false`, `"hasNextPage":true`)
			return raw
		},
		"null page info": func(raw RawInputs) RawInputs {
			raw.MergeSettings = replaceMerge(raw.MergeSettings, `"pageInfo":{"hasNextPage":false}`, `"pageInfo":null`)
			return raw
		},
		"wrong total": func(raw RawInputs) RawInputs {
			raw.MergeSettings = replaceMerge(raw.MergeSettings, `"totalCount":3`, `"totalCount":4`)
			return raw
		},
		"null total": func(raw RawInputs) RawInputs {
			raw.MergeSettings = replaceMerge(raw.MergeSettings, `"totalCount":3`, `"totalCount":null`)
			return raw
		},
		"null nodes": func(raw RawInputs) RawInputs {
			raw.MergeSettings = replaceMerge(raw.MergeSettings, validGraphQLRulesets(), `{"totalCount":3,"nodes":null,"pageInfo":{"hasNextPage":false}}`)
			return raw
		},
		"null node": func(raw RawInputs) RawInputs {
			raw.MergeSettings = replaceMerge(raw.MergeSettings, `"nodes":[{`, `"nodes":[null,{`)
			return raw
		},
		"duplicate canonical": func(raw RawInputs) RawInputs {
			raw.MergeSettings = replaceMerge(raw.MergeSettings, `"Protect env-vault release tags","target":"TAG"`, `"Protect env-vault main","target":"BRANCH"`)
			return raw
		},
		"foreign source": func(raw RawInputs) RawInputs {
			raw.MergeSettings = replaceMerge(raw.MergeSettings, `"nameWithOwner":"example/env-vault"`, `"nameWithOwner":"other/env-vault"`)
			return raw
		},
		"null source": func(raw RawInputs) RawInputs {
			raw.MergeSettings = replaceMerge(raw.MergeSettings, `"source":{"__typename":"Repository","nameWithOwner":"example/env-vault"}`, `"source":null`)
			return raw
		},
		"bypass actor": func(raw RawInputs) RawInputs {
			raw.MergeSettings = replaceMerge(raw.MergeSettings, `"bypassActors":{"totalCount":0}`, `"bypassActors":{"totalCount":1}`)
			return raw
		},
		"null bypass connection": func(raw RawInputs) RawInputs {
			raw.MergeSettings = replaceMerge(raw.MergeSettings, `"bypassActors":{"totalCount":0}`, `"bypassActors":null`)
			return raw
		},
		"null bypass count": func(raw RawInputs) RawInputs {
			raw.MergeSettings = replaceMerge(raw.MergeSettings, `"bypassActors":{"totalCount":0}`, `"bypassActors":{"totalCount":null}`)
			return raw
		},
		"REST ID disagreement": func(raw RawInputs) RawInputs {
			raw.RulesetPages = []byte(strings.Replace(string(raw.RulesetPages), `"id":7`, `"id":70`, 1))
			return raw
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			raw := mutate(validRawInputs())
			if _, err := Check(validTuple().Repository, raw); err == nil {
				t.Fatal("invalid GraphQL ruleset inventory was accepted by check")
			}
			if _, err := Seal(validTuple(), raw); err == nil {
				t.Fatal("invalid GraphQL ruleset inventory was accepted by seal")
			}
		})
	}
}

func TestPullRequestRequiredReviewersMustBePresentAndEmpty(t *testing.T) {
	for name, replacement := range map[string]string{
		"missing":  "",
		"null":     `,"required_reviewers":null`,
		"nonempty": `,"required_reviewers":[{"repository_role_database_id":5}]`,
		"wrong":    `,"required_reviewers":{}`,
	} {
		t.Run(name, func(t *testing.T) {
			raw := validRawInputs()
			raw.MainRuleset = []byte(strings.Replace(string(raw.MainRuleset), `,"required_reviewers":[]`, replacement, 1))
			if _, err := Check(validTuple().Repository, raw); err == nil {
				t.Fatal("invalid required_reviewers was accepted")
			}
		})
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
		MergeSettings:   []byte(`{"data":{"repository":{"defaultBranchRef":{"name":"main"},"mergeCommitAllowed":false,"rebaseMergeAllowed":false,"squashMergeAllowed":true,"squashMergeCommitTitle":"PR_TITLE","squashMergeCommitMessage":"PR_BODY","rulesets":` + validGraphQLRulesets() + `}}}`),
		RulesetPages:    []byte(`[[{"id":7,"name":"Protect env-vault main","target":"branch","source_type":"Repository","enforcement":"active"},{"id":8,"name":"Protect env-vault release tags","target":"tag","source_type":"Repository","enforcement":"active"},{"id":9,"name":"Protect env-vault release evidence","target":"branch","source_type":"Repository","enforcement":"active"}]]`),
		MainRuleset:     []byte(`{"id":7,"name":"Protect env-vault main","target":"branch","source_type":"Repository","source":"example/env-vault","enforcement":"active","bypass_actors":[],"current_user_can_bypass":"never","conditions":{"ref_name":{"include":["refs/heads/main"],"exclude":[]}},"rules":[{"type":"deletion"},{"type":"non_fast_forward"},{"type":"pull_request","parameters":{"required_review_thread_resolution":true,"allowed_merge_methods":["squash"],"required_reviewers":[]}},{"type":"required_status_checks","parameters":{"strict_required_status_checks_policy":true,"do_not_enforce_on_create":false,"required_status_checks":[{"context":"quality-gate","integration_id":15368},{"context":"pr-title","integration_id":15368},{"context":"Dependency review","integration_id":15368},{"context":"Analyze (go)","integration_id":15368},{"context":"Analyze (actions)","integration_id":15368}]}}]}`),
		TagRuleset:      []byte(`{"id":8,"name":"Protect env-vault release tags","target":"tag","source_type":"Repository","source":"example/env-vault","enforcement":"active","bypass_actors":[],"current_user_can_bypass":"never","conditions":{"ref_name":{"include":["refs/tags/v*"],"exclude":[]}},"rules":[{"type":"deletion"},{"type":"update"}]}`),
		EvidenceRuleset: []byte(`{"id":9,"name":"Protect env-vault release evidence","target":"branch","source_type":"Repository","source":"example/env-vault","enforcement":"active","bypass_actors":[],"current_user_can_bypass":"never","conditions":{"ref_name":{"include":["refs/heads/release-evidence"],"exclude":[]}},"rules":[{"type":"deletion"},{"type":"non_fast_forward"}]}`),
	}
}

func validGraphQLRulesets() string {
	return `{"totalCount":3,"nodes":[` +
		`{"databaseId":7,"name":"Protect env-vault main","target":"BRANCH","enforcement":"ACTIVE","source":{"__typename":"Repository","nameWithOwner":"example/env-vault"},"bypassActors":{"totalCount":0}},` +
		`{"databaseId":8,"name":"Protect env-vault release tags","target":"TAG","enforcement":"ACTIVE","source":{"__typename":"Repository","nameWithOwner":"example/env-vault"},"bypassActors":{"totalCount":0}},` +
		`{"databaseId":9,"name":"Protect env-vault release evidence","target":"BRANCH","enforcement":"ACTIVE","source":{"__typename":"Repository","nameWithOwner":"example/env-vault"},"bypassActors":{"totalCount":0}}],` +
		`"pageInfo":{"hasNextPage":false}}`
}

func rulesetDocument(raw *RawInputs, name string) *[]byte {
	switch name {
	case "main":
		return &raw.MainRuleset
	case "tag":
		return &raw.TagRuleset
	case "evidence":
		return &raw.EvidenceRuleset
	default:
		panic("unknown ruleset fixture")
	}
}

func replaceMerge(input []byte, old, replacement string) []byte {
	changed := strings.Replace(string(input), old, replacement, 1)
	if changed == string(input) {
		panic("merge fixture replacement did not match")
	}
	return []byte(changed)
}
