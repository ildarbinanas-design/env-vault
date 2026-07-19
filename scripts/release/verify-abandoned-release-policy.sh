#!/usr/bin/env bash
set -euo pipefail
set +x
export LC_ALL=C
export GH_HOST=github.com
export GH_PROMPT_DISABLED=1
unset GH_DEBUG GH_ENTERPRISE_TOKEN GITHUB_ENTERPRISE_TOKEN
unset GIT_TRACE GIT_TRACE_CURL GIT_CURL_VERBOSE GIT_TRACE_PACKET

SCRIPT_DIR=$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=scripts/release/lib.sh
source "$SCRIPT_DIR/lib.sh"
release_require_typed_contract_projection

usage() {
  printf 'usage: %s RELEASE_VERSION RELEASE_SOURCE_SHA OUTPUT_JSON\n' "$(basename "$0")" >&2
  exit 2
}

cleanup() {
  if [[ -n ${snapshot_dir:-} && -d $snapshot_dir ]]; then
    rm -rf -- "$snapshot_dir"
  fi
  if [[ -n ${temporary_output:-} && (-e $temporary_output || -L $temporary_output) ]]; then
    rm -f -- "$temporary_output"
  fi
}

[[ $# -eq 3 ]] || usage
release_version=$1
release_source_sha=$2
output=$3
repository=${GITHUB_REPOSITORY:-}
releasecheck=${RELEASECHECK:-}

release_require_repository "$repository"
release_require_version "$release_version"
[[ "$release_source_sha" =~ ^[0-9a-f]{40}$ ]] || release_die "release source SHA is malformed"
[[ -n "$releasecheck" && -f "$releasecheck" && -x "$releasecheck" && ! -L "$releasecheck" ]] ||
  release_die "RELEASECHECK must name an executable regular file"
[[ -n "$output" && "$output" != '-' && "$output" != *$'\n'* && "$output" != *$'\r'* ]] ||
  release_die "abandoned-release observation output path is malformed"
[[ ! -e "$output" && ! -L "$output" ]] || release_die "refusing to overwrite abandoned-release observation"
output_directory=$(dirname -- "$output")
output_name=$(basename -- "$output")
[[ -d "$output_directory" && ! -L "$output_directory" && -n "$output_name" ]] ||
  release_die "abandoned-release observation output directory is invalid"

for command in date gh git jq mktemp mv rm sleep; do
  release_require_command "$command"
done

umask 077
snapshot_dir=$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/env-vault-abandoned-release.XXXXXX")
temporary_output=$(mktemp "$output_directory/.${output_name}.abandoned-release.XXXXXX")
trap cleanup EXIT

(
  unset GH_TOKEN GITHUB_TOKEN GH_ENTERPRISE_TOKEN GITHUB_ENTERPRISE_TOKEN
  "$releasecheck" validate-contract --contract "$RELEASE_CONTRACT_PATH" --json
) > "$snapshot_dir/contract-validation.json"
jq -e '
  .schema_id == "env-vault.contract-validation.v1" and .schema_version == 1 and .ok == true and
  .release_contract_schema == "env-vault.release-contract.v2" and
  (.semantic_contract_sha256 | type == "string" and test("^[0-9a-f]{64}$"))
' "$snapshot_dir/contract-validation.json" >/dev/null || release_die "offline release contract validation is incomplete"

policy=$(jq -ce '
  .version_policy.release_please_recovery |
  select(.state == "complete") |
  select(.completed_release_source_sha == "6206b472cda81f7a87656055d8eb6627c26a0fef") |
  select(.abandoned_version == "v0.0.12") |
  select(.abandoned_source_sha | type == "string" and test("^[0-9a-f]{40}$")) |
  select(.generated_release_pr_number == 31) |
  select(.generated_release_pr_head_sha | type == "string" and test("^[0-9a-f]{40}$")) |
  select(.resume_version == "v0.0.13") |
  select(.pending_label == "autorelease: pending") |
  select(.abandoned_label == "autorelease: abandoned") |
  select(.tagged_label == "autorelease: tagged") |
  select(.tag_must_not_exist == true and .github_release_must_not_exist == true) |
  select(.reason_code == "PRETAG_AUTHORIZATION_MISSING")
' "$RELEASE_CONTRACT_PATH") || release_die "abandoned-release contract policy is invalid"

contract_sha=$(jq -er '.semantic_contract_sha256' "$snapshot_dir/contract-validation.json")
abandoned_version=$(jq -er '.abandoned_version' <<< "$policy")
boundary_sha=$(jq -er '.abandoned_source_sha' <<< "$policy")
pr_number=$(jq -er '.generated_release_pr_number' <<< "$policy")
pr_head_sha=$(jq -er '.generated_release_pr_head_sha' <<< "$policy")
pending_label=$(jq -er '.pending_label' <<< "$policy")
abandoned_label=$(jq -er '.abandoned_label' <<< "$policy")
tagged_label=$(jq -er '.tagged_label' <<< "$policy")
reason_code=$(jq -er '.reason_code' <<< "$policy")

[[ "$release_source_sha" != "$boundary_sha" && "$release_source_sha" != "$pr_head_sha" ]] ||
  release_die "release source is not distinct from the abandoned incident"
[[ "$(git rev-parse HEAD)" == "$release_source_sha" ]] || release_die "checkout is not the exact release source"

[[ "$repository" == "$RELEASE_SOURCE_REPOSITORY" ]] ||
  release_die "repository differs from the typed operational release projection"
# These values describe the immutable v0.0.12/PR #31 incident, not the
# current operational naming policy. Keep the historical trust domain explicit
# so a future product, branch, or App rename cannot rewrite the old predicate.
historical_base_branch=main
historical_head_branch=release-please--branches--main--components--env-vault
historical_author='env-vault-release-planning[bot]'
historical_title="chore(main): release env-vault ${abandoned_version}"

"$SCRIPT_DIR/gh-api-read.sh" "$snapshot_dir/pr.json" "repos/$repository/pulls/$pr_number"
jq -e \
  --arg repository "$repository" \
  --arg branch "$historical_head_branch" \
  --arg base "$historical_base_branch" \
  --arg head "$pr_head_sha" \
  --arg boundary "$boundary_sha" \
  --arg title "$historical_title" \
  --arg author "$historical_author" \
  --arg pending "$pending_label" \
  --arg abandoned "$abandoned_label" \
  --arg tagged "$tagged_label" \
  --argjson pr "$pr_number" '
    .number == $pr and .state == "closed" and .merged == true and .draft == false and
    (.merged_at | type == "string" and try fromdateiso8601 != null) and
    .merge_commit_sha == $boundary and .title == $title and .user.login == $author and
    .base.ref == $base and .base.repo.full_name == $repository and
    .head.ref == $branch and .head.repo.full_name == $repository and .head.sha == $head and
    ([.labels[].name] | index($abandoned) != null) and
    ([.labels[].name] | index($pending) == null) and
    ([.labels[].name] | index($tagged) == null)
  ' "$snapshot_dir/pr.json" >/dev/null || release_die "abandoned release PR state is not exact"

"$SCRIPT_DIR/gh-api-read.sh" "$snapshot_dir/compare.json" \
  "repos/$repository/compare/${boundary_sha}...${release_source_sha}"
jq -e --arg boundary "$boundary_sha" --arg source "$release_source_sha" \
  --arg api_url "https://api.github.com/repos/${repository}/compare/${boundary_sha}...${release_source_sha}" \
  --arg html_url "https://github.com/${repository}/compare/${boundary_sha}...${release_source_sha}" '
  .url == $api_url and .html_url == $html_url and
  .base_commit.sha == $boundary and .merge_base_commit.sha == $boundary and .behind_by == 0 and
  (
    (.status == "identical" and .ahead_by == 0 and $source == $boundary) or
    (.status == "ahead" and (.ahead_by | type == "number" and . > 0 and floor == .))
  )
' "$snapshot_dir/compare.json" >/dev/null || release_die "abandoned boundary is not an ancestor of the release source"

require_absence() {
  local kind=$1 script=$2 attempt status
  local -a delays=(1 2 4 8)
  for attempt in 1 2 3 4 5; do
    set +e
    "$script" "$abandoned_version" "$repository" >/dev/null 2>"$snapshot_dir/${kind}-error"
    status=$?
    set -e
    case "$status" in
      4) return 0 ;;
      0) release_die "abandoned ${abandoned_version} unexpectedly has a ${kind}" ;;
      1)
        if [[ "$attempt" != 5 ]]; then sleep "${delays[attempt - 1]}"; continue; fi
        ;;
      *) release_die "${kind} absence probe returned unsupported status ${status}" ;;
    esac
  done
  release_die "could not prove ${kind} absence after 5 attempts"
}

require_absence tag "$SCRIPT_DIR/resolve-tag-sha.sh"
require_absence release "$SCRIPT_DIR/get-release-state.sh"

observed_at=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
jq -n \
  --arg state abandoned \
  --arg version "$abandoned_version" \
  --arg source "$boundary_sha" \
  --arg head "$pr_head_sha" \
  --arg merged_at "$(jq -er '.merged_at' "$snapshot_dir/pr.json")" \
  --arg title "$historical_title" \
  --arg author "$historical_author" \
  --arg base "$historical_base_branch" \
  --arg repository "$repository" \
  --arg reason "$reason_code" \
  --arg observed_at "$observed_at" \
  --arg contract_sha "$contract_sha" \
  --argjson labels "$(jq -c '[.labels[].name] | sort | unique' "$snapshot_dir/pr.json")" \
  --argjson pr "$pr_number" '
    {state:$state,version:$version,source_sha:$source,
     generated_release_pr:{number:$pr,head_sha:$head,merge_sha:$source,merged_at:$merged_at},
     pull_request_state:"closed",pull_request_merged:true,pull_request_title:$title,
     pull_request_author:$author,base_ref:$base,base_repository:$repository,labels:$labels,
     boundary_is_ancestor_of_release:true,tag_exists:false,github_release_exists:false,
     reason_code:$reason,observed_at:$observed_at,semantic_contract_sha256:$contract_sha}
  ' > "$temporary_output"

jq -e --arg version "$abandoned_version" --arg source "$boundary_sha" --arg reason "$reason_code" '
  .state == "abandoned" and .version == $version and .source_sha == $source and
  .generated_release_pr.merge_sha == $source and .pull_request_state == "closed" and
  .pull_request_merged == true and .boundary_is_ancestor_of_release == true and
  .tag_exists == false and .github_release_exists == false and .reason_code == $reason and
  (.semantic_contract_sha256 | type == "string" and test("^[0-9a-f]{64}$")) and
  (.observed_at | type == "string" and try fromdateiso8601 != null)
' "$temporary_output" >/dev/null || release_die "abandoned-release observation is incomplete"

mv -f -- "$temporary_output" "$output"
temporary_output=''
trap - EXIT
rm -rf -- "$snapshot_dir"
snapshot_dir=''
