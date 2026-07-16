package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ildarbinanas-design/env-vault/internal/releasesettings"
)

func TestSettingsCheckCLIValidatesSavedResponsesOffline(t *testing.T) {
	args := settingsCheckArgs(t, validSettingsCheckInputs())
	args = append(args, "--json")
	var stdout, stderr bytes.Buffer
	if code := run(args, &stdout, &stderr); code != exitOK {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	var result releasesettings.CheckResult
	decodeOneJSON(t, stdout.Bytes(), &result)
	if !result.OK || result.SchemaID != releasesettings.CheckSchemaID || result.SchemaVersion != releasesettings.SchemaVersion || result.Repository != "example/env-vault" || result.Result != releasesettings.ResultPass {
		t.Fatalf("check result=%+v", result)
	}
	if stderr.Len() != 0 {
		t.Fatalf("unexpected stderr=%q", stderr.String())
	}
}

func TestSettingsCheckCLIFailsClosedWithStableCode(t *testing.T) {
	inputs := validSettingsCheckInputs()
	inputs.MainRuleset = []byte(strings.Replace(string(inputs.MainRuleset), `,"required_reviewers":[]`, "", 1))
	args := settingsCheckArgs(t, inputs)
	args = append(args, "--json")
	var stdout, stderr bytes.Buffer
	if code := run(args, &stdout, &stderr); code != exitSnapshotInvalid {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	var failure errorDocument
	decodeOneJSON(t, stdout.Bytes(), &failure)
	if failure.OK || failure.Error.Code != releasesettings.CodePolicyInvalid {
		t.Fatalf("failure=%+v", failure)
	}
}

func settingsCheckArgs(t *testing.T, inputs releasesettings.RawInputs) []string {
	t.Helper()
	return []string{
		"settings", "check", "--contract", canonicalContractPath(t), "--repository", "example/env-vault",
		"--merge-settings", writeTestFile(t, inputs.MergeSettings),
		"--ruleset-pages", writeTestFile(t, inputs.RulesetPages),
		"--main-ruleset", writeTestFile(t, inputs.MainRuleset),
		"--tag-ruleset", writeTestFile(t, inputs.TagRuleset),
		"--evidence-ruleset", writeTestFile(t, inputs.EvidenceRuleset),
	}
}

func validSettingsCheckInputs() releasesettings.RawInputs {
	graphqlRulesets := `{"totalCount":3,"nodes":[` +
		`{"databaseId":7,"name":"Protect env-vault main","target":"BRANCH","enforcement":"ACTIVE","source":{"__typename":"Repository","nameWithOwner":"example/env-vault"},"bypassActors":{"totalCount":0}},` +
		`{"databaseId":8,"name":"Protect env-vault release tags","target":"TAG","enforcement":"ACTIVE","source":{"__typename":"Repository","nameWithOwner":"example/env-vault"},"bypassActors":{"totalCount":0}},` +
		`{"databaseId":9,"name":"Protect env-vault release evidence","target":"BRANCH","enforcement":"ACTIVE","source":{"__typename":"Repository","nameWithOwner":"example/env-vault"},"bypassActors":{"totalCount":0}}],` +
		`"pageInfo":{"hasNextPage":false}}`
	return releasesettings.RawInputs{
		MergeSettings:   []byte(`{"data":{"repository":{"defaultBranchRef":{"name":"main"},"mergeCommitAllowed":false,"rebaseMergeAllowed":false,"squashMergeAllowed":true,"squashMergeCommitTitle":"PR_TITLE","squashMergeCommitMessage":"PR_BODY","rulesets":` + graphqlRulesets + `}}}`),
		RulesetPages:    []byte(`[[{"id":7,"name":"Protect env-vault main","target":"branch","source_type":"Repository","enforcement":"active"},{"id":8,"name":"Protect env-vault release tags","target":"tag","source_type":"Repository","enforcement":"active"},{"id":9,"name":"Protect env-vault release evidence","target":"branch","source_type":"Repository","enforcement":"active"}]]`),
		MainRuleset:     []byte(`{"id":7,"name":"Protect env-vault main","target":"branch","source_type":"Repository","source":"example/env-vault","enforcement":"active","current_user_can_bypass":"never","conditions":{"ref_name":{"include":["refs/heads/main"],"exclude":[]}},"rules":[{"type":"deletion"},{"type":"non_fast_forward"},{"type":"pull_request","parameters":{"required_review_thread_resolution":true,"allowed_merge_methods":["squash"],"required_reviewers":[]}},{"type":"required_status_checks","parameters":{"strict_required_status_checks_policy":true,"do_not_enforce_on_create":false,"required_status_checks":[{"context":"quality-gate","integration_id":15368},{"context":"pr-title","integration_id":15368},{"context":"Dependency review","integration_id":15368},{"context":"Analyze (go)","integration_id":15368},{"context":"Analyze (actions)","integration_id":15368}]}}]}`),
		TagRuleset:      []byte(`{"id":8,"name":"Protect env-vault release tags","target":"tag","source_type":"Repository","source":"example/env-vault","enforcement":"active","current_user_can_bypass":"never","conditions":{"ref_name":{"include":["refs/tags/v*"],"exclude":[]}},"rules":[{"type":"deletion"},{"type":"update"}]}`),
		EvidenceRuleset: []byte(`{"id":9,"name":"Protect env-vault release evidence","target":"branch","source_type":"Repository","source":"example/env-vault","enforcement":"active","current_user_can_bypass":"never","conditions":{"ref_name":{"include":["refs/heads/release-evidence"],"exclude":[]}},"rules":[{"type":"deletion"},{"type":"non_fast_forward"}]}`),
	}
}
