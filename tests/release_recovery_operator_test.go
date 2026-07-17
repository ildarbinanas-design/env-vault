package tests

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

const (
	recoveryBoundarySHA = "a0eb82cb1fc4fa486ff2032d50ddedf6bccdbb8b"
	recoveryPRHeadSHA   = "c7169946d9c430209928266d95be7629c93d5878"
)

func TestReconcileAbandonedReleasePullRequest(t *testing.T) {
	releasecheck := credentialRejectingReleasecheck(t, buildReleasecheck(t))
	fixture, env, state, mutations := newRecoveryOperatorFixture(t, releasecheck)

	outputPath := filepath.Join(fixture, "recovery-evidence.json")
	output, err := runReleaseAutomationScriptEnv(t, fixture, env,
		"reconcile-abandoned-release-pr.sh", outputPath)
	if err != nil {
		t.Fatalf("reconcile abandoned release PR: %v\n%s", err, output)
	}

	var evidence struct {
		SchemaID           string `json:"schema_id"`
		OK                 bool   `json:"ok"`
		AbandonedVersion   string `json:"abandoned_version"`
		AbandonedSourceSHA string `json:"abandoned_source_sha"`
		GeneratedReleasePR struct {
			Number   int    `json:"number"`
			HeadSHA  string `json:"head_sha"`
			MergeSHA string `json:"merge_sha"`
		} `json:"generated_release_pr"`
		Lifecycle struct {
			LabelsBefore []string `json:"labels_before"`
			LabelsAfter  []string `json:"labels_after"`
		} `json:"lifecycle"`
		TagExists           bool   `json:"tag_exists"`
		GitHubReleaseExists bool   `json:"github_release_exists"`
		ActionCode          string `json:"action_code"`
		ReasonCode          string `json:"reason_code"`
		Result              string `json:"result"`
	}
	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &evidence); err != nil {
		t.Fatalf("decode recovery evidence: %v", err)
	}
	if evidence.SchemaID != "env-vault.release-please-recovery.v1" || !evidence.OK ||
		evidence.AbandonedVersion != "v0.0.12" || evidence.AbandonedSourceSHA != recoveryBoundarySHA ||
		evidence.GeneratedReleasePR.Number != 31 || evidence.GeneratedReleasePR.HeadSHA != recoveryPRHeadSHA ||
		evidence.GeneratedReleasePR.MergeSHA != recoveryBoundarySHA || evidence.TagExists ||
		evidence.GitHubReleaseExists || evidence.ActionCode != "mark_release_pr_abandoned" ||
		evidence.ReasonCode != "PRETAG_AUTHORIZATION_MISSING" || evidence.Result != "pass" ||
		!slices.Equal(evidence.Lifecycle.LabelsBefore, []string{"autorelease: pending"}) ||
		!slices.Equal(evidence.Lifecycle.LabelsAfter, []string{"autorelease: abandoned"}) {
		t.Fatalf("recovery evidence is not exact: %+v", evidence)
	}
	assertRecoveryState(t, state, `[{"name":"autorelease: abandoned"}]`)
	assertRecoveryMutations(t, mutations, "POST abandoned\nDELETE pending\n")

	secondOutput := filepath.Join(fixture, "recovery-evidence-resumed.json")
	output, err = runReleaseAutomationScriptEnv(t, fixture, env,
		"reconcile-abandoned-release-pr.sh", secondOutput)
	if err != nil {
		t.Fatalf("resume exact abandoned state: %v\n%s", err, output)
	}
	assertRecoveryState(t, state, `[{"name":"autorelease: abandoned"}]`)
	assertRecoveryMutations(t, mutations, "POST abandoned\nDELETE pending\n")
}

func TestReconcileAbandonedReleasePullRequestReconcilesAmbiguousWrites(t *testing.T) {
	releasecheck := credentialRejectingReleasecheck(t, buildReleasecheck(t))
	fixture, env, state, mutations := newRecoveryOperatorFixture(t, releasecheck)
	env = append(env, "FAKE_POST_AMBIGUOUS=true", "FAKE_DELETE_AMBIGUOUS=true")

	output, err := runReleaseAutomationScriptEnv(t, fixture, env,
		"reconcile-abandoned-release-pr.sh", filepath.Join(fixture, "evidence.json"))
	if err != nil {
		t.Fatalf("reconcile ambiguous label writes: %v\n%s", err, output)
	}
	assertRecoveryState(t, state, `[{"name":"autorelease: abandoned"}]`)
	assertRecoveryMutations(t, mutations, "POST abandoned\nDELETE pending\n")
}

func TestReconcileAbandonedReleasePullRequestFailsClosedAfterMutation(t *testing.T) {
	releasecheck := credentialRejectingReleasecheck(t, buildReleasecheck(t))
	for name, override := range map[string]string{
		"tag appears":     "FAKE_TAG_AFTER_MUTATION=true",
		"release appears": "FAKE_RELEASE_AFTER_MUTATION=true",
		"main advances":   "FAKE_MAIN_ADVANCES_AFTER_MUTATION=true",
	} {
		t.Run(name, func(t *testing.T) {
			fixture, env, _, mutations := newRecoveryOperatorFixture(t, releasecheck)
			env = append(env, override)
			outputPath := filepath.Join(fixture, "unsafe-evidence.json")
			output, err := runReleaseAutomationScriptEnv(t, fixture, env,
				"reconcile-abandoned-release-pr.sh", outputPath)
			if err == nil {
				t.Fatalf("post-mutation race unexpectedly succeeded: %s", output)
			}
			assertRecoveryMutations(t, mutations, "POST abandoned\nDELETE pending\n")
			if _, statErr := os.Lstat(outputPath); !os.IsNotExist(statErr) {
				t.Fatalf("failed reconciliation left evidence: %v", statErr)
			}
		})
	}
}

func TestReconcileAbandonedReleasePullRequestDoesNotRetryAmbiguousUnappliedWrites(t *testing.T) {
	releasecheck := credentialRejectingReleasecheck(t, buildReleasecheck(t))
	for name, tc := range map[string]struct {
		override      string
		wantMutations string
	}{
		"post":   {override: "FAKE_POST_AMBIGUOUS_NO_APPLY=true", wantMutations: "POST abandoned\n"},
		"delete": {override: "FAKE_DELETE_AMBIGUOUS_NO_APPLY=true", wantMutations: "POST abandoned\nDELETE pending\n"},
	} {
		t.Run(name, func(t *testing.T) {
			fixture, env, _, mutations := newRecoveryOperatorFixture(t, releasecheck)
			env = append(env, tc.override)
			outputPath := filepath.Join(fixture, "unsafe-evidence.json")
			if output, err := runReleaseAutomationScriptEnv(t, fixture, env,
				"reconcile-abandoned-release-pr.sh", outputPath); err == nil {
				t.Fatalf("unapplied ambiguous %s unexpectedly succeeded: %s", name, output)
			}
			assertRecoveryMutations(t, mutations, tc.wantMutations)
			if _, statErr := os.Lstat(outputPath); !os.IsNotExist(statErr) {
				t.Fatalf("failed reconciliation left evidence: %v", statErr)
			}
		})
	}
}

func TestReconcileAbandonedReleasePullRequestPinsGitHubHost(t *testing.T) {
	releasecheck := credentialRejectingReleasecheck(t, buildReleasecheck(t))
	fixture, env, _, _ := newRecoveryOperatorFixture(t, releasecheck)
	env = append(env,
		"GH_HOST=attacker.example",
		"GH_ENTERPRISE_TOKEN=must-not-reach-gh",
		"GITHUB_ENTERPRISE_TOKEN=must-not-reach-gh",
		"GH_DEBUG=api",
	)

	output, err := runReleaseAutomationScriptEnv(t, fixture, env,
		"reconcile-abandoned-release-pr.sh", filepath.Join(fixture, "evidence.json"))
	if err != nil {
		t.Fatalf("pinned GitHub host recovery: %v\n%s", err, output)
	}
}

func TestReconcileAbandonedReleasePullRequestFailsClosedBeforeMutation(t *testing.T) {
	releasecheck := credentialRejectingReleasecheck(t, buildReleasecheck(t))
	tests := []struct {
		name     string
		override string
	}{
		{name: "wrong PR head", override: "FAKE_PR_HEAD_SHA=" + strings.Repeat("1", 40)},
		{name: "wrong merge source", override: "FAKE_PR_MERGE_SHA=" + strings.Repeat("2", 40)},
		{name: "wrong title", override: "FAKE_PR_TITLE=chore(main): release env-vault v0.0.99"},
		{name: "wrong author", override: "FAKE_PR_AUTHOR=github-actions[bot]"},
		{name: "boundary not ancestor", override: "FAKE_COMPARE_STATUS=diverged"},
		{name: "compare response wrong head", override: "FAKE_COMPARE_HEAD_SHA=" + strings.Repeat("5", 40)},
		{name: "tag exists", override: "FAKE_TAG_EXISTS=true"},
		{name: "release exists", override: "FAKE_RELEASE_EXISTS=true"},
		{name: "tagged lifecycle", override: `FAKE_INITIAL_LABELS=[{"name":"autorelease: tagged"}]`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fixture, env, state, mutations := newRecoveryOperatorFixture(t, releasecheck)
			env = append(env, tc.override)
			if strings.HasPrefix(tc.override, "FAKE_INITIAL_LABELS=") {
				initial := strings.TrimPrefix(tc.override, "FAKE_INITIAL_LABELS=")
				if err := os.WriteFile(state, []byte(initial+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			output, err := runReleaseAutomationScriptEnv(t, fixture, env,
				"reconcile-abandoned-release-pr.sh", filepath.Join(fixture, "evidence.json"))
			if err == nil {
				t.Fatalf("unsafe recovery unexpectedly succeeded: %s", output)
			}
			if !strings.Contains(output, "release:") {
				t.Fatalf("failure is not structured: %q", output)
			}
			assertRecoveryMutations(t, mutations, "")
		})
	}
}

func TestVerifyAbandonedReleasePolicy(t *testing.T) {
	releasecheck := credentialRejectingReleasecheck(t, buildReleasecheck(t))
	fixture, env, state, _ := newRecoveryOperatorFixture(t, releasecheck)
	if err := os.WriteFile(state, []byte(`[{"name":"autorelease: abandoned"}]`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sourceSHA := strings.TrimSpace(runRecoveryGit(t, fixture, "rev-parse", "HEAD"))
	outputPath := filepath.Join(fixture, "abandoned-release.json")
	output, err := runReleaseAutomationScriptEnv(t, fixture, env,
		"verify-abandoned-release-policy.sh", "v0.0.13", sourceSHA, outputPath)
	if err != nil {
		t.Fatalf("verify abandoned release policy: %v\n%s", err, output)
	}
	var observation struct {
		State                       string   `json:"state"`
		Version                     string   `json:"version"`
		SourceSHA                   string   `json:"source_sha"`
		Labels                      []string `json:"labels"`
		BoundaryIsAncestorOfRelease bool     `json:"boundary_is_ancestor_of_release"`
		TagExists                   bool     `json:"tag_exists"`
		GitHubReleaseExists         bool     `json:"github_release_exists"`
		ReasonCode                  string   `json:"reason_code"`
	}
	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &observation); err != nil {
		t.Fatal(err)
	}
	if observation.State != "abandoned" || observation.Version != "v0.0.12" ||
		observation.SourceSHA != recoveryBoundarySHA || !observation.BoundaryIsAncestorOfRelease ||
		observation.TagExists || observation.GitHubReleaseExists ||
		observation.ReasonCode != "PRETAG_AUTHORIZATION_MISSING" ||
		!slices.Equal(observation.Labels, []string{"autorelease: abandoned"}) {
		t.Fatalf("abandoned-release observation is not exact: %+v", observation)
	}

	if output, err := runReleaseAutomationScriptEnv(t, fixture, env,
		"verify-abandoned-release-policy.sh", "v0.0.13", sourceSHA, outputPath); err == nil {
		t.Fatalf("abandoned-release proof clobbered an existing output: %s", output)
	}
}

func TestVerifyAbandonedReleasePolicyFailsClosed(t *testing.T) {
	releasecheck := credentialRejectingReleasecheck(t, buildReleasecheck(t))
	tests := []struct {
		name       string
		version    string
		source     string
		override   string
		labelState string
	}{
		{name: "wrong resume version", version: "v0.0.14"},
		{name: "wrong source checkout", version: "v0.0.13", source: strings.Repeat("1", 40)},
		{name: "pending lifecycle", version: "v0.0.13", labelState: `[{"name":"autorelease: pending"}]`},
		{name: "tagged lifecycle", version: "v0.0.13", labelState: `[{"name":"autorelease: abandoned"},{"name":"autorelease: tagged"}]`},
		{name: "boundary not ancestor", version: "v0.0.13", override: "FAKE_COMPARE_STATUS=diverged"},
		{name: "compare response wrong head", version: "v0.0.13", override: "FAKE_COMPARE_HEAD_SHA=" + strings.Repeat("5", 40)},
		{name: "tag exists", version: "v0.0.13", override: "FAKE_TAG_EXISTS=true"},
		{name: "release exists", version: "v0.0.13", override: "FAKE_RELEASE_EXISTS=true"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fixture, env, state, _ := newRecoveryOperatorFixture(t, releasecheck)
			labels := tc.labelState
			if labels == "" {
				labels = `[{"name":"autorelease: abandoned"}]`
			}
			if err := os.WriteFile(state, []byte(labels+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if tc.override != "" {
				env = append(env, tc.override)
			}
			source := tc.source
			if source == "" {
				source = strings.TrimSpace(runRecoveryGit(t, fixture, "rev-parse", "HEAD"))
			}
			outputPath := filepath.Join(fixture, "unsafe-observation.json")
			output, err := runReleaseAutomationScriptEnv(t, fixture, env,
				"verify-abandoned-release-policy.sh", tc.version, source, outputPath)
			if err == nil {
				t.Fatalf("unsafe abandoned-release state unexpectedly succeeded: %s", output)
			}
			if _, statErr := os.Lstat(outputPath); !os.IsNotExist(statErr) {
				t.Fatalf("failed verifier left an output: %v", statErr)
			}
		})
	}
}

func newRecoveryOperatorFixture(t *testing.T, releasecheck string) (string, []string, string, string) {
	t.Helper()
	fixture := t.TempDir()
	config, err := os.ReadFile(filepath.Join("..", "release-please-config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture, "release-please-config.json"), config, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture, ".release-please-manifest.json"), []byte("{\n  \".\": \"0.0.12\"\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runRecoveryGit(t, fixture, "init", "-q")
	runRecoveryGit(t, fixture, "config", "user.name", "Recovery Test")
	runRecoveryGit(t, fixture, "config", "user.email", "recovery@example.invalid")
	runRecoveryGit(t, fixture, "add", "release-please-config.json", ".release-please-manifest.json")
	runRecoveryGit(t, fixture, "commit", "-q", "-m", "fixture")
	mainSHA := strings.TrimSpace(runRecoveryGit(t, fixture, "rev-parse", "HEAD"))

	commandDir := t.TempDir()
	ghPath := filepath.Join(commandDir, "gh")
	if err := os.WriteFile(ghPath, []byte(recoveryFakeGH), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(commandDir, "sleep"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(fixture, "labels.json")
	if err := os.WriteFile(state, []byte(`[{"name":"autorelease: pending"}]`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mutations := filepath.Join(fixture, "mutations.log")
	if err := os.WriteFile(mutations, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	runnerTemp := filepath.Join(fixture, "runner-temp")
	if err := os.Mkdir(runnerTemp, 0o700); err != nil {
		t.Fatal(err)
	}
	env := []string{
		"GITHUB_REPOSITORY=ildarbinanas-design/env-vault",
		"GH_TOKEN=transport-only-test-token",
		"RELEASECHECK=" + releasecheck,
		"RUNNER_TEMP=" + runnerTemp,
		"FAKE_MAIN_SHA=" + mainSHA,
		"FAKE_LABEL_STATE=" + state,
		"FAKE_MUTATION_LOG=" + mutations,
		"PATH=" + commandDir + string(os.PathListSeparator) + os.Getenv("PATH"),
	}
	return fixture, env, state, mutations
}

func runRecoveryGit(t *testing.T, directory string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = directory
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return string(output)
}

func assertRecoveryState(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var gotValue, wantValue any
	if err := json.Unmarshal(data, &gotValue); err != nil {
		t.Fatalf("decode recovery label state: %v", err)
	}
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatalf("decode wanted recovery label state: %v", err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf("recovery label state=%s, want %s", data, want)
	}
}

func assertRecoveryMutations(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Fatalf("recovery mutations=%q, want %q", data, want)
	}
}

const recoveryFakeGH = `#!/usr/bin/env bash
set -euo pipefail
[[ ${GH_HOST:-} == github.com ]] || { printf 'unsafe GH_HOST=%s\n' "${GH_HOST:-}" >&2; exit 1; }
[[ -z ${GH_ENTERPRISE_TOKEN:-} && -z ${GITHUB_ENTERPRISE_TOKEN:-} && -z ${GH_DEBUG:-} ]] || {
  printf 'unsafe gh ambient environment\n' >&2
  exit 1
}
args="$*"
state=${FAKE_LABEL_STATE:?}
mutations=${FAKE_MUTATION_LOG:?}
main_sha=${FAKE_MAIN_SHA:?}
boundary=a0eb82cb1fc4fa486ff2032d50ddedf6bccdbb8b
head=${FAKE_PR_HEAD_SHA:-c7169946d9c430209928266d95be7629c93d5878}
merge=${FAKE_PR_MERGE_SHA:-$boundary}
title=${FAKE_PR_TITLE:-chore(main): release env-vault v0.0.12}
author=${FAKE_PR_AUTHOR:-env-vault-release-planning[bot]}

if [[ "$args" == *"issues/31/labels"* && "$args" == *"--method POST"* ]]; then
  if [[ ${FAKE_POST_AMBIGUOUS_NO_APPLY:-false} == true ]]; then
    printf 'POST abandoned\n' >> "$mutations"
    exit 1
  fi
  jq 'if any(.[]; .name == "autorelease: abandoned") then . else . + [{name:"autorelease: abandoned"}] end' \
    "$state" > "$state.tmp"
  mv "$state.tmp" "$state"
  printf 'POST abandoned\n' >> "$mutations"
  [[ ${FAKE_POST_AMBIGUOUS:-false} != true ]]
  exit
fi
if [[ "$args" == *"issues/31/labels/autorelease%3A%20pending"* && "$args" == *"--method DELETE"* ]]; then
  if [[ ${FAKE_DELETE_AMBIGUOUS_NO_APPLY:-false} == true ]]; then
    printf 'DELETE pending\n' >> "$mutations"
    exit 1
  fi
  jq '[.[] | select(.name != "autorelease: pending")]' "$state" > "$state.tmp"
  mv "$state.tmp" "$state"
  printf 'DELETE pending\n' >> "$mutations"
  [[ ${FAKE_DELETE_AMBIGUOUS:-false} != true ]]
  exit
fi
if [[ "$args" == *"git/ref/heads/main"* ]]; then
  observed_main=$main_sha
  if [[ ${FAKE_MAIN_ADVANCES_AFTER_MUTATION:-false} == true && -s "$mutations" ]]; then
    observed_main=4444444444444444444444444444444444444444
  fi
  jq -cn --arg sha "$observed_main" '{object:{type:"commit",sha:$sha}}'
  exit 0
fi
if [[ "$args" == *"compare/${boundary}...${main_sha}"* ]]; then
  status=${FAKE_COMPARE_STATUS:-ahead}
  compare_head=${FAKE_COMPARE_HEAD_SHA:-$main_sha}
  merge_base=$boundary
  behind=0
  if [[ "$status" != ahead && "$status" != identical ]]; then
    merge_base=3333333333333333333333333333333333333333
    behind=1
  fi
  jq -cn --arg base "$boundary" --arg head "$compare_head" --arg status "$status" \
    --arg url "https://api.github.com/repos/ildarbinanas-design/env-vault/compare/${boundary}...${compare_head}" \
    --arg html_url "https://github.com/ildarbinanas-design/env-vault/compare/${boundary}...${compare_head}" \
    --arg merge_base "$merge_base" --argjson behind "$behind" \
    '{url:$url,html_url:$html_url,status:$status,ahead_by:3,behind_by:$behind,base_commit:{sha:$base},
      merge_base_commit:{sha:$merge_base},commits:[{sha:"1111111111111111111111111111111111111111"},{sha:"2222222222222222222222222222222222222222"},{sha:$head}]}'
  exit 0
fi
if [[ "$args" == *"pulls/31"* ]]; then
  labels=$(cat "$state")
  jq -cn \
    --argjson labels "$labels" \
    --arg head "$head" \
    --arg merge "$merge" \
    --arg title "$title" \
    --arg author "$author" \
    '{number:31,state:"closed",merged:true,draft:false,merged_at:"2026-07-16T22:06:40Z",
      merge_commit_sha:$merge,title:$title,user:{login:$author},labels:$labels,
      base:{ref:"main",repo:{full_name:"ildarbinanas-design/env-vault"}},
      head:{ref:"release-please--branches--main--components--env-vault",sha:$head,
        repo:{full_name:"ildarbinanas-design/env-vault"}}}'
  exit 0
fi
if [[ "$args" == *"git/ref/tags/v0.0.12"* ]]; then
  if [[ ${FAKE_TAG_EXISTS:-false} == true || (${FAKE_TAG_AFTER_MUTATION:-false} == true && -s "$mutations") ]]; then
    if [[ "$args" == *"--jq"* ]]; then
      printf 'commit\t%s\n' "$boundary"
    else
      printf 'HTTP/2.0 200 OK\n\n'
      jq -cn --arg sha "$boundary" '{object:{type:"commit",sha:$sha}}'
    fi
    exit 0
  fi
  printf 'HTTP/2.0 404 Not Found\n' >&2
  exit 1
fi
if [[ "$args" == *"releases/tags/v0.0.12"* ]]; then
  if [[ ${FAKE_RELEASE_EXISTS:-false} == true || (${FAKE_RELEASE_AFTER_MUTATION:-false} == true && -s "$mutations") ]]; then
    if [[ "$args" == *"--jq"* ]]; then
      printf 'v0.0.12\tfalse\tfalse\n'
    else
      printf 'HTTP/2.0 200 OK\n\n'
      printf '%s\n' '{"tag_name":"v0.0.12","draft":false,"prerelease":false}'
    fi
    exit 0
  fi
  printf 'HTTP/2.0 404 Not Found\n' >&2
  exit 1
fi

printf 'unexpected gh invocation: %s\n' "$args" >&2
exit 1
`
