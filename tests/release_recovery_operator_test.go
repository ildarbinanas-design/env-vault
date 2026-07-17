package tests

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const (
	recoveryBoundarySHA = "a0eb82cb1fc4fa486ff2032d50ddedf6bccdbb8b"
)

func TestVerifyAbandonedReleasePolicy(t *testing.T) {
	releasecheck := credentialRejectingReleasecheck(t, buildReleasecheck(t))
	fixture, env, state, _ := newRecoveryOperatorFixture(t, releasecheck)
	if err := os.WriteFile(state, []byte(`[{"name":"autorelease: abandoned"}]`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sourceSHA := strings.TrimSpace(runRecoveryGit(t, fixture, "rev-parse", "HEAD"))
	outputPath := filepath.Join(fixture, "abandoned-release.json")
	output, err := runReleaseAutomationScriptEnv(t, fixture, env,
		"verify-abandoned-release-policy.sh", "v0.0.14", sourceSHA, outputPath)
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
		"verify-abandoned-release-policy.sh", "v0.0.14", sourceSHA, outputPath); err == nil {
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
		{name: "malformed release version", version: "0.0.14"},
		{name: "wrong source checkout", version: "v0.0.14", source: strings.Repeat("1", 40)},
		{name: "pending lifecycle", version: "v0.0.14", labelState: `[{"name":"autorelease: pending"}]`},
		{name: "tagged lifecycle", version: "v0.0.14", labelState: `[{"name":"autorelease: abandoned"},{"name":"autorelease: tagged"}]`},
		{name: "wrong PR head", version: "v0.0.14", override: "FAKE_PR_HEAD_SHA=" + strings.Repeat("1", 40)},
		{name: "wrong PR merge source", version: "v0.0.14", override: "FAKE_PR_MERGE_SHA=" + strings.Repeat("2", 40)},
		{name: "wrong PR title", version: "v0.0.14", override: "FAKE_PR_TITLE=chore(main): release env-vault v0.0.99"},
		{name: "wrong PR author", version: "v0.0.14", override: "FAKE_PR_AUTHOR=github-actions[bot]"},
		{name: "boundary not ancestor", version: "v0.0.14", override: "FAKE_COMPARE_STATUS=diverged"},
		{name: "compare response wrong head", version: "v0.0.14", override: "FAKE_COMPARE_HEAD_SHA=" + strings.Repeat("5", 40)},
		{name: "tag exists", version: "v0.0.14", override: "FAKE_TAG_EXISTS=true"},
		{name: "release exists", version: "v0.0.14", override: "FAKE_RELEASE_EXISTS=true"},
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
	if err := os.WriteFile(filepath.Join(fixture, ".release-please-manifest.json"), []byte("{\n  \".\": \"0.0.13\"\n}\n"), 0o600); err != nil {
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

const recoveryFakeGH = `#!/usr/bin/env bash
set -euo pipefail
[[ ${GH_HOST:-} == github.com ]] || { printf 'unsafe GH_HOST=%s\n' "${GH_HOST:-}" >&2; exit 1; }
[[ -z ${GH_ENTERPRISE_TOKEN:-} && -z ${GITHUB_ENTERPRISE_TOKEN:-} && -z ${GH_DEBUG:-} ]] || {
  printf 'unsafe gh ambient environment\n' >&2
  exit 1
}
if [[ ${1:-} == --version ]]; then
  printf 'gh version 2.80.0 (2026-01-01)\nhttps://github.com/cli/cli/releases/tag/v2.80.0\n'
  exit 0
fi
if [[ ${1:-} == api && ${2:-} == --help ]]; then
  printf '%s\n' 'OPTIONS: --include --hostname --method --header --raw-field'
  exit 0
fi
http_json() {
  local status=$1 reason=$2
  printf 'HTTP/2 %s %s\r\n' "$status" "$reason"
  printf 'Content-Type: application/vnd.github+json\r\n'
  printf 'X-GitHub-Api-Version-Selected: 2022-11-28\r\n\r\n'
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
  http_json 200 OK
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
  http_json 200 OK
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
  http_json 200 OK
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
    http_json 200 OK
    jq -cn --arg sha "$boundary" '{object:{type:"commit",sha:$sha}}'
    exit 0
  fi
  http_json 404 'Not Found'
  printf '%s\n' '{"message":"Not Found"}'
  exit 1
fi
if [[ "$args" == *"releases/tags/v0.0.12"* ]]; then
  if [[ ${FAKE_RELEASE_EXISTS:-false} == true || (${FAKE_RELEASE_AFTER_MUTATION:-false} == true && -s "$mutations") ]]; then
    http_json 200 OK
    printf '%s\n' '{"tag_name":"v0.0.12","draft":false,"prerelease":false}'
    exit 0
  fi
  http_json 404 'Not Found'
  printf '%s\n' '{"message":"Not Found"}'
  exit 1
fi

printf 'unexpected gh invocation: %s\n' "$args" >&2
exit 1
`
