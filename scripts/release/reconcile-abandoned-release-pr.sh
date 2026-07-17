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

usage() {
  printf 'usage: %s OUTPUT_JSON\n' "$(basename "$0")" >&2
  exit 2
}

cleanup() {
  if [[ -n ${snapshot_dir:-} && -d "$snapshot_dir" ]]; then
    rm -rf -- "$snapshot_dir"
  fi
  if [[ -n ${temporary_output:-} && (-e $temporary_output || -L $temporary_output) ]]; then
    rm -f -- "$temporary_output"
  fi
}

[[ $# -eq 1 ]] || usage
output=$1
repository=${GITHUB_REPOSITORY:-}
releasecheck=${RELEASECHECK:-}

release_require_repository "$repository"
[[ -n "$releasecheck" && -f "$releasecheck" && -x "$releasecheck" && ! -L "$releasecheck" ]] ||
  release_die "RELEASECHECK must name an executable regular file"
[[ -n "$output" && "$output" != '-' && "$output" != *$'\n'* && "$output" != *$'\r'* ]] ||
  release_die "recovery evidence output path is malformed"
[[ ! -e "$output" && ! -L "$output" ]] ||
  release_die "refusing to overwrite recovery evidence"
output_directory=$(dirname -- "$output")
output_name=$(basename -- "$output")
[[ -d "$output_directory" && ! -L "$output_directory" && -n "$output_name" ]] ||
  release_die "recovery evidence output directory is invalid"

for command in cp date gh git jq mktemp mv rm sleep; do
  release_require_command "$command"
done

umask 077
snapshot_dir=$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/env-vault-release-recovery.XXXXXX")
temporary_output=$(mktemp "$output_directory/.${output_name}.recovery.XXXXXX")
trap cleanup EXIT

(
  unset GH_TOKEN GITHUB_TOKEN GH_ENTERPRISE_TOKEN GITHUB_ENTERPRISE_TOKEN
  "$releasecheck" recovery validate-config \
    --contract "$RELEASE_CONTRACT_PATH" \
    --config release-please-config.json \
    --manifest .release-please-manifest.json \
    --json
) > "$snapshot_dir/config-check.json"

jq -e '
  .schema_id == "env-vault.release-please-recovery-check.v1" and
  .schema_version == 1 and .ok == true and .state == "active" and
  (.semantic_contract_sha256 | type == "string" and test("^[0-9a-f]{64}$")) and
  (.abandoned_version | type == "string") and
  (.abandoned_source_sha | type == "string" and test("^[0-9a-f]{40}$")) and
  (.generated_release_pr_number | type == "number" and . > 0 and floor == .) and
  (.generated_release_pr_head_sha | type == "string" and test("^[0-9a-f]{40}$")) and
  (.resume_version | type == "string") and
  .pending_label == "autorelease: pending" and
  .abandoned_label == "autorelease: abandoned" and
  .tagged_label == "autorelease: tagged" and
  .tag_must_not_exist == true and
  .github_release_must_not_exist == true and
  .reason_code == "PRETAG_AUTHORIZATION_MISSING"
' "$snapshot_dir/config-check.json" >/dev/null ||
  release_die "offline recovery config validation is incomplete"

version=$(jq -er '.abandoned_version' "$snapshot_dir/config-check.json")
boundary_sha=$(jq -er '.abandoned_source_sha' "$snapshot_dir/config-check.json")
pr_number=$(jq -er '.generated_release_pr_number' "$snapshot_dir/config-check.json")
pr_head_sha=$(jq -er '.generated_release_pr_head_sha' "$snapshot_dir/config-check.json")
resume_version=$(jq -er '.resume_version' "$snapshot_dir/config-check.json")
pending_label=$(jq -er '.pending_label' "$snapshot_dir/config-check.json")
abandoned_label=$(jq -er '.abandoned_label' "$snapshot_dir/config-check.json")
tagged_label=$(jq -er '.tagged_label' "$snapshot_dir/config-check.json")
contract_sha=$(jq -er '.semantic_contract_sha256' "$snapshot_dir/config-check.json")
release_app_slug=$(jq -er --arg repository "$repository" '
  [.apps[] | select(.id == "release_planning" and .repository == $repository)] |
  select(length == 1) | .[0].slug |
  select(type == "string" and test("^[a-z0-9][a-z0-9-]*$"))
' "$RELEASE_CONTRACT_PATH")
expected_author="${release_app_slug}[bot]"
expected_title="chore(main): release env-vault ${version}"

release_require_version "$version"
release_require_version "$resume_version"
[[ "$boundary_sha" =~ ^[0-9a-f]{40}$ && "$pr_head_sha" =~ ^[0-9a-f]{40}$ && "$pr_number" =~ ^[1-9][0-9]*$ ]] ||
  release_die "recovery tuple is malformed"

read_main() {
  "$SCRIPT_DIR/gh-api-read.sh" "$snapshot_dir/main-ref.json" \
    "repos/$repository/git/ref/heads/main"
  jq -er 'select(.object.type == "commit") | .object.sha |
    select(type == "string" and test("^[0-9a-f]{40}$"))' \
    "$snapshot_dir/main-ref.json"
}

verify_ancestry() {
  local main_sha=$1
  local api_url="https://api.github.com/repos/${repository}/compare/${boundary_sha}...${main_sha}"
  local html_url="https://github.com/${repository}/compare/${boundary_sha}...${main_sha}"
  "$SCRIPT_DIR/gh-api-read.sh" "$snapshot_dir/compare.json" \
    "repos/$repository/compare/${boundary_sha}...${main_sha}"
  jq -e --arg boundary "$boundary_sha" --arg main "$main_sha" \
    --arg api_url "$api_url" --arg html_url "$html_url" '
    .url == $api_url and .html_url == $html_url and
    .base_commit.sha == $boundary and
    .merge_base_commit.sha == $boundary and
    .behind_by == 0 and
    (
      (.status == "identical" and .ahead_by == 0 and $main == $boundary) or
      (.status == "ahead" and (.ahead_by | type == "number" and . > 0 and floor == .))
    )
  ' "$snapshot_dir/compare.json" >/dev/null ||
    release_die "abandoned release boundary is not an ancestor of current main"
}

read_pr() {
  local destination=$1
  "$SCRIPT_DIR/gh-api-read.sh" "$destination" \
    "repos/$repository/pulls/$pr_number"
}

verify_pr_tuple() {
  local record=$1
  jq -e \
    --arg repository "$repository" \
    --arg version "$version" \
    --arg boundary "$boundary_sha" \
    --arg head "$pr_head_sha" \
    --arg title "$expected_title" \
    --arg author "$expected_author" \
    --arg pending "$pending_label" \
    --arg abandoned "$abandoned_label" \
    --arg tagged "$tagged_label" \
    --argjson pr "$pr_number" '
      .number == $pr and .state == "closed" and .merged == true and .draft == false and
      (.merged_at | type == "string" and length > 0) and
      .merge_commit_sha == $boundary and
      .base.ref == "main" and .base.repo.full_name == $repository and
      .head.sha == $head and .head.repo.full_name == $repository and
      .title == $title and .user.login == $author and
      ([.labels[].name] | all(.[]; type == "string")) and
      ([.labels[].name] | index($tagged) == null) and
      ([.labels[].name] as $labels |
        ($labels | index($pending) != null) or ($labels | index($abandoned) != null))
    ' "$record" >/dev/null ||
    release_die "merged release PR no longer matches the exact abandoned tuple"
}

label_present() {
  local record=$1
  local label=$2
  jq -e --arg label "$label" '[.labels[].name] | index($label) != null' "$record" >/dev/null
}

wait_for_label_state() {
  local require_present=$1
  local require_absent=$2
  local destination=$3
  local attempt
  local -a delays=(1 2 4 8)
  for attempt in 1 2 3 4 5; do
    read_pr "$destination"
    verify_pr_tuple "$destination"
    if label_present "$destination" "$require_present" && ! label_present "$destination" "$require_absent"; then
      return 0
    fi
    if [[ "$attempt" != 5 ]]; then
      sleep "${delays[attempt - 1]}"
    fi
  done
  release_die "release PR labels did not reach the required recovery state"
}

require_remote_absence() {
  local kind=$1
  local script=$2
  local attempt status
  local -a delays=(1 2 4 8)
  for attempt in 1 2 3 4 5; do
    set +e
    "$script" "$version" "$repository" > /dev/null 2> "$snapshot_dir/${kind}-probe-error"
    status=$?
    set -e
    case "$status" in
      4)
        return 0
        ;;
      0)
        release_die "abandoned ${version} unexpectedly has a ${kind}"
        ;;
      1)
        if [[ "$attempt" != 5 ]]; then
          sleep "${delays[attempt - 1]}"
          continue
        fi
        ;;
      *)
        release_die "${kind} absence probe returned unsupported status ${status}"
        ;;
    esac
  done
  release_die "could not prove ${kind} absence after 5 attempts"
}

verify_remote_absence() {
  require_remote_absence tag "$SCRIPT_DIR/resolve-tag-sha.sh"
  require_remote_absence release "$SCRIPT_DIR/get-release-state.sh"
}

main_sha=$(read_main)
local_sha=$(git rev-parse HEAD)
[[ "$local_sha" == "$main_sha" ]] || release_die "recovery checkout is not current main"
if ! git diff --quiet || ! git diff --cached --quiet; then
  release_die "recovery checkout has tracked modifications"
fi
verify_ancestry "$main_sha"
read_pr "$snapshot_dir/pr-before.json"
verify_pr_tuple "$snapshot_dir/pr-before.json"
verify_remote_absence

if ! label_present "$snapshot_dir/pr-before.json" "$abandoned_label"; then
  set +e
  gh api --method POST \
    "repos/$repository/issues/$pr_number/labels" \
    --raw-field "labels[]=$abandoned_label" \
    --silent
  set -e
  wait_for_label_state "$abandoned_label" "$tagged_label" "$snapshot_dir/pr-after-add.json"
else
  cp "$snapshot_dir/pr-before.json" "$snapshot_dir/pr-after-add.json"
fi

if label_present "$snapshot_dir/pr-after-add.json" "$pending_label"; then
  set +e
  gh api --method DELETE \
    "repos/$repository/issues/$pr_number/labels/autorelease%3A%20pending" \
    --silent
  set -e
fi
wait_for_label_state "$abandoned_label" "$pending_label" "$snapshot_dir/pr-after.json"
! label_present "$snapshot_dir/pr-after.json" "$tagged_label" ||
  release_die "abandoned release PR was marked as tagged"

final_main_sha=$(read_main)
[[ "$final_main_sha" == "$main_sha" ]] || release_die "main advanced during abandoned-release reconciliation"
verify_ancestry "$final_main_sha"
read_pr "$snapshot_dir/pr-final.json"
verify_pr_tuple "$snapshot_dir/pr-final.json"
label_present "$snapshot_dir/pr-final.json" "$abandoned_label" ||
  release_die "abandoned release PR lost its recovery label"
! label_present "$snapshot_dir/pr-final.json" "$pending_label" ||
  release_die "abandoned release PR regained its pending label"
! label_present "$snapshot_dir/pr-final.json" "$tagged_label" ||
  release_die "abandoned release PR was marked as tagged"
verify_remote_absence

observed_at=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
jq -n \
  --arg repository "$repository" \
  --arg state active \
  --arg version "$version" \
  --arg boundary "$boundary_sha" \
  --arg head "$pr_head_sha" \
  --arg resume_version "$resume_version" \
  --arg pending "$pending_label" \
  --arg abandoned "$abandoned_label" \
  --arg tagged "$tagged_label" \
  --arg main_sha "$main_sha" \
  --arg contract_sha "$contract_sha" \
  --arg reason_code "$(jq -er '.reason_code' "$snapshot_dir/config-check.json")" \
  --arg observed_at "$observed_at" \
  --arg merged_at "$(jq -er '.merged_at' "$snapshot_dir/pr-final.json")" \
  --argjson pr "$pr_number" \
  --argjson before_labels "$(jq -c '[.labels[].name] | sort' "$snapshot_dir/pr-before.json")" \
  --argjson after_labels "$(jq -c '[.labels[].name] | sort' "$snapshot_dir/pr-final.json")" '
    {
      schema_id:"env-vault.release-please-recovery.v1",
      schema_version:1,
      ok:true,
      repository:$repository,
      state:$state,
      abandoned_version:$version,
      abandoned_source_sha:$boundary,
      generated_release_pr:{number:$pr,head_sha:$head,merge_sha:$boundary,merged_at:$merged_at},
      resume_version:$resume_version,
      lifecycle:{pending_label:$pending,abandoned_label:$abandoned,tagged_label:$tagged,
        labels_before:$before_labels,labels_after:$after_labels},
      current_main_sha:$main_sha,
      boundary_is_ancestor_of_main:true,
      tag_exists:false,
      github_release_exists:false,
      semantic_contract_sha256:$contract_sha,
      action_code:"mark_release_pr_abandoned",
      reason_code:$reason_code,
      observed_at:$observed_at,
      result:"pass"
    }
  ' > "$temporary_output"

jq -e \
  --arg repository "$repository" \
  --arg version "$version" \
  --arg boundary "$boundary_sha" \
  --arg abandoned "$abandoned_label" '
    .schema_id == "env-vault.release-please-recovery.v1" and .schema_version == 1 and
    .ok == true and .repository == $repository and .abandoned_version == $version and
    .abandoned_source_sha == $boundary and .generated_release_pr.merge_sha == $boundary and
    .boundary_is_ancestor_of_main == true and .tag_exists == false and
    .github_release_exists == false and
    (.lifecycle.labels_after | index($abandoned) != null) and
    (.lifecycle.labels_after | index("autorelease: pending") == null) and
    (.lifecycle.labels_after | index("autorelease: tagged") == null) and
    .action_code == "mark_release_pr_abandoned" and
    .reason_code == "PRETAG_AUTHORIZATION_MISSING" and
    .result == "pass"
  ' "$temporary_output" >/dev/null || release_die "recovery evidence is incomplete"

mv -f -- "$temporary_output" "$output"
temporary_output=''
trap - EXIT
rm -rf -- "$snapshot_dir"
snapshot_dir=''
