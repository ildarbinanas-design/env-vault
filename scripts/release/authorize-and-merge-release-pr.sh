#!/usr/bin/env bash
set -euo pipefail
set +x
export LC_ALL=C
unset GH_DEBUG GIT_TRACE GIT_TRACE_CURL GIT_CURL_VERBOSE GIT_TRACE_PACKET

SCRIPT_DIR=$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=scripts/release/lib.sh
source "$SCRIPT_DIR/lib.sh"

usage() {
  printf 'usage: %s vMAJOR.MINOR.PATCH PR_NUMBER EXACT_HEAD_SHA\n' "$(basename "$0")" >&2
  exit 2
}

cleanup() {
  if [[ -n ${probe_dir:-} && -d $probe_dir ]]; then
    rm -rf -- "$probe_dir"
  fi
}

require_positive_integer() {
  local name=$1
  local value=$2
  [[ "$value" =~ ^[1-9][0-9]{0,8}$ ]] || release_die "$name must be a positive integer with at most 9 digits"
}

# Retry only read-only API observations through the shared atomic transport
# helper. Content creation and merge mutations are never blindly retried
# because a transport failure can arrive after GitHub has committed the write.
read_gh_api() {
  "$SCRIPT_DIR/gh-api-read.sh" "$@"
}

load_pull_request() {
  local output=$1
  read_gh_api "$output" "repos/$repository/pulls/$pr_number" ||
    release_die "cannot inspect generated release pull request"
}

require_open_exact_pull_request() {
  local input=$1
  jq -e \
    --arg repository "$repository" \
    --arg branch "$release_branch" \
    --arg author "${release_app_slug}[bot]" \
    --arg title "chore(main): release env-vault $version" \
    --arg head "$expected_head_sha" \
    --argjson number "$pr_number" '
      .number == $number and
      .state == "open" and
      .merged == false and
      .draft == false and
      .base.ref == "main" and
      .base.repo.full_name == $repository and
      (.base.sha | type == "string" and test("^[0-9a-f]{40}$")) and
      .head.ref == $branch and
      .head.repo.full_name == $repository and
      .head.sha == $head and
      .user.login == $author and
      .title == $title and
      ((.body // "") | contains("Merging this unchanged reviewed pull request after the required exact tuple confirmation authorizes publication once its merge commit passes main CI.")) and
      ((.body // "") | contains("This PR was generated with Release Please.")) and
      ([.labels[].name] | index("autorelease: pending") != null) and
      ([.labels[].name] | index("autorelease: tagged") == null)
    ' "$input" >/dev/null || release_die "generated release pull request tuple or provenance changed"
}

require_exact_merged_pull_request() {
  local input=$1
  jq -e \
    --arg repository "$repository" \
    --arg branch "$release_branch" \
    --arg author "${release_app_slug}[bot]" \
    --arg title "chore(main): release env-vault $version" \
    --arg head "$expected_head_sha" \
    --arg base "$initial_base_sha" \
    --argjson number "$pr_number" '
      .number == $number and
      .state == "closed" and
      .merged == true and
      .draft == false and
      .base.ref == "main" and
      .base.repo.full_name == $repository and
      .base.sha == $base and
      .head.ref == $branch and
      .head.repo.full_name == $repository and
      .head.sha == $head and
      .user.login == $author and
      .title == $title and
      ((.body // "") | contains("Merging this unchanged reviewed pull request after the required exact tuple confirmation authorizes publication once its merge commit passes main CI.")) and
      ((.body // "") | contains("This PR was generated with Release Please.")) and
      ([.labels[].name] as $labels |
        [($labels | index("autorelease: pending") != null),
         ($labels | index("autorelease: tagged") != null)] |
        map(select(. == true)) | length == 1
      ) and
      (.merge_commit_sha | type == "string" and test("^[0-9a-f]{40}$")) and
      (.merged_at | type == "string" and try fromdateiso8601 != null)
    ' "$input" >/dev/null || release_die "generated release pull request is not an exact resumable merge"
}

require_validated_base_contract() {
  local remote_contract="$probe_dir/base-release-contract.json"
  local validation="$probe_dir/release-contract-validation.json"
  local checker=${RELEASECHECK:-}

  "$SCRIPT_DIR/releasetransport.sh" contents read --output "$remote_contract" --repository "$repository" \
    --path release/contract.v1.json --ref "$initial_base_sha" ||
    release_die "cannot load the release contract from the exact pull request base"
  cmp "$RELEASE_CONTRACT_PATH" "$remote_contract" >/dev/null ||
    release_die "local release contract differs from the exact pull request base"

  if [[ -z "$checker" ]]; then
    release_require_command go
    checker="$probe_dir/releasecheck"
    (cd "$SCRIPT_DIR/../.." && go build -trimpath -o "$checker" ./cmd/releasecheck) ||
      release_die "cannot build the offline release contract checker"
  fi
  [[ -f "$checker" && ! -L "$checker" && -x "$checker" ]] ||
    release_die "offline release contract checker is not an executable regular file"
  env -i "$checker" validate-contract --contract "$RELEASE_CONTRACT_PATH" --json > "$validation" ||
    release_die "offline release contract validation failed"
  jq -e '
    .schema_id == "env-vault.contract-validation.v1" and
    .schema_version == 1 and
    .ok == true and
    .release_contract_schema == "env-vault.release-contract.v1" and
    (.semantic_contract_sha256 | type == "string" and test("^[0-9a-f]{64}$")) and
    .platform_count == 5 and
    .asset_count == 10
  ' "$validation" >/dev/null || release_die "offline release contract validation result is malformed"
}

require_current_base() {
  local input=$1
  local phase=$2
  local main_ref="$probe_dir/main-ref-$phase.json"
  local observed_base main_sha

  observed_base=$(jq -er '.base.sha | select(test("^[0-9a-f]{40}$"))' "$input") ||
    release_die "generated release pull request base SHA is malformed"
  [[ "$observed_base" == "$initial_base_sha" ]] ||
    release_die "generated release pull request base SHA changed"
  read_gh_api "$main_ref" "repos/$repository/git/ref/heads/main" ||
    release_die "cannot inspect current main SHA"
  main_sha=$(jq -er '.object.sha | select(type == "string" and test("^[0-9a-f]{40}$"))' "$main_ref") ||
    release_die "current main SHA is malformed"
  [[ "$main_sha" =~ ^[0-9a-f]{40}$ ]] || release_die "current main SHA is malformed"
  [[ "$main_sha" == "$initial_base_sha" ]] ||
    release_die "main changed after release authorization began"
}

require_verified_proposal() {
  local output proposal_sha proposal_base proposal_version attempt verified=false

  for attempt in 1 2 3; do
    if output=$(GITHUB_REPOSITORY="$repository" RELEASE_APP_SLUG="$release_app_slug" \
      "$SCRIPT_DIR/verify-release-proposal.sh" "$pr_number" "$expected_head_sha"); then
      verified=true
      break
    fi
    [[ "$attempt" == "3" ]] || sleep 2
  done
  [[ "$verified" == "true" ]] || release_die "generated release proposal verification failed"
  [[ $(grep -Fxc 'proposal=true' <<< "$output") -eq 1 ]] ||
    release_die "generated release proposal is not uniquely available"
  proposal_sha=$(sed -n 's/^proposal_sha=//p' <<< "$output")
  proposal_base=$(sed -n 's/^proposal_base_sha=//p' <<< "$output")
  proposal_version=$(sed -n 's/^version=//p' <<< "$output")
  [[ "$proposal_sha" == "$expected_head_sha" &&
     "$proposal_base" == "$initial_base_sha" &&
     "$proposal_version" == "$version" ]] ||
    release_die "generated release proposal differs from the authorized tuple"
}

require_trusted_viewer() {
  local owner_login owner_type viewer_login viewer_type

  read_gh_api "$probe_dir/viewer.json" user || release_die "cannot inspect the authenticated GitHub user"
  read_gh_api "$probe_dir/repository.json" "repos/$repository" || release_die "cannot inspect the GitHub repository"
  viewer_login=$(jq -er '.login | select(type == "string" and test("^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$"))' \
    "$probe_dir/viewer.json") || release_die "authenticated GitHub user login is malformed"
  viewer_type=$(jq -er '.type | select(type == "string")' "$probe_dir/viewer.json") ||
    release_die "authenticated GitHub user type is malformed"
  [[ "$viewer_type" == "User" ]] || release_die "release authorization must be recorded by a GitHub User"
  owner_login=$(jq -er '.owner.login | select(type == "string" and length > 0)' "$probe_dir/repository.json") ||
    release_die "GitHub repository owner login is malformed"
  owner_type=$(jq -er '.owner.type | select(. == "User" or . == "Organization")' "$probe_dir/repository.json") ||
    release_die "GitHub repository owner type is unsupported"
  case "$owner_type" in
    User)
      [[ "$viewer_login" == "$owner_login" ]] ||
        release_die "authenticated GitHub user is not the repository owner"
      ;;
    Organization)
      read_gh_api "$probe_dir/membership.json" "user/memberships/orgs/$owner_login" ||
        release_die "cannot verify active organization membership"
      jq -e --arg organization "$owner_login" '
        .state == "active" and
        .organization.login == $organization and
        (.role == "admin" or .role == "member")
      ' "$probe_dir/membership.json" >/dev/null ||
        release_die "authenticated GitHub user is not an active organization member"
      ;;
  esac
}

load_green_checks() {
  local output=$1
  local raw="$output.raw"
  local attempt loaded=false quality_link run_tail run_id job_id run_attempt
  for attempt in 1 2 3; do
    if gh pr checks "$pr_number" --repo "$repository" --required \
      --json name,state,bucket,link,workflow,event > "$raw"; then
      loaded=true
      break
    fi
    [[ "$attempt" == "3" ]] || sleep 2
  done
  [[ "$loaded" == "true" ]] ||
    release_die "generated release pull request required checks are not all successful"
  jq -e --argjson expected "$main_required_checks" '
    select(type == "array" and length == ($expected | length)) |
    select(all(.[];
      type == "object" and
      ((keys | sort) == ["bucket", "event", "link", "name", "state", "workflow"]) and
      (.name | type == "string" and length > 0) and
      (.workflow | type == "string" and length > 0) and
      (.event | type == "string" and length > 0) and
      (.link | type == "string" and startswith("https://github.com/")) and
      .state == "SUCCESS" and
      .bucket == "pass"
    )) |
    select((map({name, workflow, event}) | sort_by([.name, .workflow, .event])) == $expected) |
    map({name, workflow, event, state, bucket, link}) |
    sort_by([.name, .workflow, .event, .link])
  ' "$raw" > "$output" ||
    release_die "generated release pull request required checks are incomplete or malformed"

  quality_link=$(jq -er '
    [.[] | select(.name == "quality-gate" and .workflow == "ci" and .event == "pull_request")] |
    select(length == 1) | .[0].link
  ' "$output") || release_die "generated release pull request has no unique quality-gate check"
  run_tail=${quality_link#"https://github.com/$repository/actions/runs/"}
  if [[ "$run_tail" =~ ^([1-9][0-9]*)/job/([1-9][0-9]*)$ ]]; then
    run_id=${BASH_REMATCH[1]}
    job_id=${BASH_REMATCH[2]}
  else
    release_die "generated release pull request quality-gate URL is malformed"
  fi
  read_gh_api "$output.quality-run.json" "repos/$repository/actions/runs/$run_id" ||
    release_die "cannot inspect generated release pull request quality-gate run"
  run_attempt=$(jq -er --arg head "$expected_head_sha" --arg branch "$release_branch" \
    --arg repository "$repository" --argjson run_id "$run_id" '
      select(
        .id == $run_id and
        .repository.full_name == $repository and
        .head_repository.full_name == $repository and
        .head_sha == $head and
        .head_branch == $branch and
        .event == "pull_request" and
        .path == ".github/workflows/ci.yml" and
        .status == "completed" and
        .conclusion == "success" and
        (.run_attempt | type == "number" and . > 0 and floor == .)
      ) | .run_attempt
    ' "$output.quality-run.json") || release_die "generated release pull request quality-gate run is malformed"
  "$SCRIPT_DIR/releasetransport.sh" actions identity \
    --output "$output.quality-identity.json" \
    --repository "$repository" \
    --run-id "$run_id" \
    --run-attempt "$run_attempt" \
    --workflow-path .github/workflows/ci.yml \
    --event pull_request \
    --head-sha "$expected_head_sha" \
    --head-ref "$release_branch" \
    --job-id "$job_id" \
    --job-name quality-gate \
    --job-url "$quality_link" || release_die "generated release pull request quality-gate typed identity mismatch"
}

load_matching_confirmations() {
  local pages=$1
  local matches=$2

  read_gh_api "$pages" --paginate --slurp --header 'Accept: application/vnd.github+json' \
    "repos/$repository/issues/$pr_number/comments?per_page=100" ||
    release_die "cannot inspect generated release pull request comments"
  jq -e \
    --arg body "$canonical_body" \
    --arg repository "$repository" \
    --argjson number "$pr_number" '
      select(type == "array" and all(.[]; type == "array")) |
      [.[][]] as $comments |
      select(all($comments[];
        type == "object" and
        (.id | type == "number" and . > 0 and floor == .) and
        (.body | type == "string") and
        (.user | type == "object") and
        (.user.login | type == "string" and length > 0) and
        (.user.type | type == "string" and length > 0) and
        (.author_association | type == "string" and length > 0) and
        (.created_at | type == "string" and try fromdateiso8601 != null) and
        (.updated_at | type == "string" and try fromdateiso8601 != null) and
        (.html_url | type == "string" and length > 0)
      )) |
      [$comments[] |
        select(.body == $body and (.author_association == "OWNER" or .author_association == "MEMBER"))
      ] as $candidates |
      select(all($candidates[];
        .user.type == "User" and
        (.user.login | test("^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$")) and
        .html_url == ("https://github.com/" + $repository + "/pull/" + ($number | tostring) + "#issuecomment-" + (.id | tostring))
      )) |
      $candidates | sort_by(.id)
    ' "$pages" > "$matches" || release_die "GitHub returned malformed release confirmation comments"
}

require_one_confirmation() {
  local matches=$1
  local count
  count=$(jq -er 'length' "$matches") || release_die "release confirmation count is malformed"
  [[ "$count" == "1" ]] ||
    release_die "exactly one exact release confirmation comment from a repository owner or member is required"
}

observe_github_date() {
  local output=$1
  local date_value
  "$SCRIPT_DIR/releasetransport.sh" rest observe --output "$output" \
    --endpoint "repos/$repository/issues/comments/$confirmation_id" ||
    release_die "cannot observe the release confirmation comment"
  date_value=$(jq -er '
    select(type == "object" and .schema_id == "env-vault.github-rest-observation.v1" and
      .schema_version == 1 and .ok == true and .http_status == 200 and
      (.server_date | type) == "string" and
      (.body_sha256 | type) == "string" and (.body_sha256 | test("^[0-9a-f]{64}$"))) |
    .server_date
  ' "$output") ||
    release_die "GitHub response Date header is missing or malformed"
  printf '%s\n' "$date_value"
}

http_date_epoch() {
  local value=$1
  jq -en --arg value "$value" '
    $value |
    strptime("%a, %d %b %Y %H:%M:%S GMT") |
    mktime |
    select(type == "number" and floor == .)
  ' || release_die "GitHub response Date header cannot be parsed"
}

load_confirmation_identity() {
  local matches=$1
  confirmation_id=$(jq -er '.[0].id | select(type == "number" and . > 0 and floor == .)' "$matches") ||
    release_die "release confirmation comment ID is malformed"
  confirmation_created_at=$(jq -er '.[0].created_at' "$matches") ||
    release_die "release confirmation creation time is malformed"
  confirmation_updated_at=$(jq -er '.[0].updated_at' "$matches") ||
    release_die "release confirmation update time is malformed"
}

require_confirmation_precedes_merge() {
  local merged_at=$1
  jq -en \
    --arg created_at "$confirmation_created_at" \
    --arg updated_at "$confirmation_updated_at" \
    --arg merged_at "$merged_at" '
      ($created_at | fromdateiso8601) < ($merged_at | fromdateiso8601) and
      ($updated_at | fromdateiso8601) < ($merged_at | fromdateiso8601)
    ' >/dev/null || release_die "release confirmation was not recorded strictly before merge"
}

require_merge_in_main() {
  local merge_sha=$1
  local main_ref main_comparison
  local merge_in_main=false main_sha comparison_status attempt

  for attempt in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24 25 26 27 28 29 30; do
    main_ref="$probe_dir/main-after-merge-$attempt.json"
    main_comparison="$probe_dir/main-after-merge-comparison-$attempt.json"
    if ! read_gh_api "$main_ref" "repos/$repository/git/ref/heads/main"; then
      sleep 2
      continue
    fi
    main_sha=$(jq -er '.object.sha | select(type == "string" and test("^[0-9a-f]{40}$"))' "$main_ref") ||
      release_die "main SHA after release merge is malformed"
    if ! read_gh_api "$main_comparison" "repos/$repository/compare/$merge_sha...$main_sha"; then
      sleep 2
      continue
    fi
    comparison_status=$(jq -er '.status | select(. == "ahead" or . == "behind" or . == "diverged" or . == "identical")' \
      "$main_comparison") || release_die "release merge ancestry response is malformed"
    if [[ "$comparison_status" == "ahead" || "$comparison_status" == "identical" ]]; then
      merge_in_main=true
      break
    fi
    if [[ "$main_sha" != "$initial_base_sha" ]]; then
      release_die "current main conflicts with the exact release merge commit"
    fi
    sleep 2
  done
  [[ "$merge_in_main" == "true" ]] ||
    release_die "timed out waiting for the exact release merge commit to appear in main"
}

[[ $# -eq 3 ]] || usage
version=$1
pr_number=$2
expected_head_sha=$3

release_require_version "$version"
require_positive_integer "pull request number" "$pr_number"
[[ "$expected_head_sha" =~ ^[0-9a-f]{40}$ ]] ||
  release_die "expected head SHA must contain exactly 40 lowercase hexadecimal characters"
release_require_command gh
release_require_command jq
release_require_command sleep
release_require_command cmp
release_require_command env

repository=${GITHUB_REPOSITORY:-}
if [[ -z "$repository" ]]; then
  repository=$(gh repo view --json nameWithOwner --jq '.nameWithOwner') ||
    release_die "cannot resolve the current GitHub repository"
fi
release_require_repository "$repository"

release_app_slug=$(jq -er --arg repository "$repository" '
  [.apps[] | select(.id == "release_planning" and .repository == $repository)] |
  select(length == 1) | .[0].slug |
  select(type == "string" and test("^[a-z0-9][a-z0-9-]*$"))
' "$RELEASE_CONTRACT_PATH") || release_die "release planning App identity is missing from the contract"
main_required_checks=$(jq -cer '
  .main_required_checks |
  select(type == "array" and length > 0) |
  select(all(.[];
    type == "object" and
    ((keys | sort) == ["event", "name", "workflow"]) and
    (.name | type == "string" and length > 0) and
    (.workflow | type == "string" and length > 0) and
    (.event | type == "string" and length > 0)
  )) |
  select((map([.name, .workflow, .event]) | unique | length) == length) |
  sort_by([.name, .workflow, .event])
' "$RELEASE_CONTRACT_PATH") || release_die "main required checks are missing or malformed in the contract"
release_branch=release-please--branches--main--components--env-vault
canonical_body="ПОДТВЕРЖДАЮ RELEASE $version PR #$pr_number SHA $expected_head_sha"

probe_dir=$(mktemp -d "${TMPDIR:-/tmp}/env-vault-release-authorize-merge.XXXXXX")
trap cleanup EXIT

require_trusted_viewer

pr_before="$probe_dir/pr-before.json"
load_pull_request "$pr_before"
initial_base_sha=$(jq -er '.base.sha | select(test("^[0-9a-f]{40}$"))' "$pr_before") ||
  release_die "generated release pull request base SHA is malformed"
require_validated_base_contract

if jq -e '.state == "closed" and .merged == true' "$pr_before" >/dev/null; then
  require_exact_merged_pull_request "$pr_before"
  require_verified_proposal
  load_green_checks "$probe_dir/checks-resumed.json"
  load_matching_confirmations "$probe_dir/comments-resumed.json" "$probe_dir/confirmations-resumed.json"
  require_one_confirmation "$probe_dir/confirmations-resumed.json"
  load_confirmation_identity "$probe_dir/confirmations-resumed.json"
  merge_sha=$(jq -er '.merge_commit_sha' "$pr_before") ||
    release_die "generated release pull request merge SHA is malformed"
  merged_at=$(jq -er '.merged_at | fromdateiso8601 | todateiso8601' "$pr_before") ||
    release_die "generated release pull request merge time is malformed"
  require_confirmation_precedes_merge "$merged_at"
  require_merge_in_main "$merge_sha"
  printf '%s\n' "$merge_sha"
  exit 0
fi

require_open_exact_pull_request "$pr_before"
require_current_base "$pr_before" before
require_verified_proposal
checks_before="$probe_dir/checks-before.json"
load_green_checks "$checks_before"

comment_pages="$probe_dir/comments-before.json"
confirmation_matches="$probe_dir/confirmations-before.json"
load_matching_confirmations "$comment_pages" "$confirmation_matches"
confirmation_count=$(jq -er 'length' "$confirmation_matches") ||
  release_die "release confirmation count is malformed"
case "$confirmation_count" in
  0)
    if ! gh api --method POST "repos/$repository/issues/$pr_number/comments" \
      --raw-field "body=$canonical_body" > "$probe_dir/comment-created.json"; then
      # A transport failure can arrive after GitHub committed the write. Do
      # not blindly retry a content-creation request: reconcile once by
      # listing the canonical trusted comment instead.
      load_matching_confirmations "$probe_dir/comments-reconcile.json" "$probe_dir/confirmations-reconcile.json"
      reconcile_count=$(jq -er 'length' "$probe_dir/confirmations-reconcile.json") ||
        release_die "release confirmation reconciliation count is malformed"
      [[ "$reconcile_count" == "1" ]] ||
        release_die "cannot reconcile an ambiguous release confirmation write"
    fi
    ;;
  1)
    ;;
  *)
    release_die "multiple exact release confirmation comments already exist"
    ;;
esac

comment_pages="$probe_dir/comments-recorded.json"
confirmation_matches="$probe_dir/confirmations-recorded.json"
load_matching_confirmations "$comment_pages" "$confirmation_matches"
require_one_confirmation "$confirmation_matches"
load_confirmation_identity "$confirmation_matches"

anchor_date=$(observe_github_date "$probe_dir/date-anchor.json")
anchor_epoch=$(http_date_epoch "$anchor_date")
confirmation_epoch=$(jq -en \
  --arg created_at "$confirmation_created_at" \
  --arg updated_at "$confirmation_updated_at" '
    [$created_at, $updated_at] |
    map(fromdateiso8601) |
    max |
    select(type == "number" and floor == .)
  ') || release_die "release confirmation timestamps cannot be parsed"
advanced_date=''
for observation_attempt in 1 2 3 4 5 6 7 8 9 10; do
  sleep 1
  observed_date=$(observe_github_date "$probe_dir/date-observed-$observation_attempt.json")
  observed_epoch=$(http_date_epoch "$observed_date")
  if ((observed_epoch > anchor_epoch && observed_epoch > confirmation_epoch)); then
    advanced_date=$observed_date
    break
  fi
done
[[ -n "$advanced_date" ]] ||
  release_die "GitHub did not expose a later server second before merge"

pr_final="$probe_dir/pr-final.json"
load_pull_request "$pr_final"
require_open_exact_pull_request "$pr_final"
require_current_base "$pr_final" final
require_verified_proposal
checks_final="$probe_dir/checks-final.json"
load_green_checks "$checks_final"
cmp "$checks_before" "$checks_final" >/dev/null ||
  release_die "required check identities changed before merge"

comment_pages="$probe_dir/comments-final.json"
confirmation_final="$probe_dir/confirmations-final.json"
load_matching_confirmations "$comment_pages" "$confirmation_final"
require_one_confirmation "$confirmation_final"
jq -e \
  --argjson id "$confirmation_id" \
  --arg created_at "$confirmation_created_at" \
  --arg updated_at "$confirmation_updated_at" '
    .[0].id == $id and
    .[0].created_at == $created_at and
    .[0].updated_at == $updated_at
  ' "$confirmation_final" >/dev/null ||
  release_die "release confirmation changed before merge"

set +e
gh pr merge "$pr_number" --repo "$repository" --squash \
  --match-head-commit "$expected_head_sha" >&2
merge_status=$?
set -e

merged=false
for merge_observation_attempt in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24 25 26 27 28 29 30; do
  pr_merged="$probe_dir/pr-merged-$merge_observation_attempt.json"
  if ! read_gh_api "$pr_merged" "repos/$repository/pulls/$pr_number"; then
    sleep 2
    continue
  fi
  jq -e --arg head "$expected_head_sha" --arg base "$initial_base_sha" --argjson number "$pr_number" '
    .number == $number and
    (.state == "open" or .state == "closed") and
    (.merged | type == "boolean") and
    .head.sha == $head and
    .base.sha == $base
  ' "$pr_merged" >/dev/null ||
    release_die "generated release pull request tuple changed while reconciling merge"
  if jq -e --arg head "$expected_head_sha" --arg base "$initial_base_sha" --argjson number "$pr_number" '
      .number == $number and
      .state == "closed" and
      .merged == true and
      .head.sha == $head and
      .base.sha == $base and
      (.merge_commit_sha | type == "string" and test("^[0-9a-f]{40}$")) and
      (.merged_at | type == "string" and try fromdateiso8601 != null)
    ' "$pr_merged" >/dev/null; then
    merged=true
    break
  fi
  sleep 2
done
if [[ "$merged" != "true" ]]; then
  if [[ "$merge_status" == "0" ]]; then
    release_die "timed out waiting for the exact generated release pull request merge"
  fi
  release_die "cannot reconcile the failed generated release pull request merge request"
fi
merge_sha=$(jq -er '.merge_commit_sha' "$pr_merged") ||
  release_die "generated release pull request merge SHA is malformed"
merged_at=$(jq -er '.merged_at | select(type == "string") | fromdateiso8601 | todateiso8601' "$pr_merged") ||
  release_die "generated release pull request merge time is malformed"
load_matching_confirmations "$probe_dir/comments-post-merge.json" "$probe_dir/confirmations-post-merge.json"
require_one_confirmation "$probe_dir/confirmations-post-merge.json"
cmp "$confirmation_final" "$probe_dir/confirmations-post-merge.json" >/dev/null ||
  release_die "release confirmation changed after merge"
require_confirmation_precedes_merge "$merged_at"
require_merge_in_main "$merge_sha"

printf '%s\n' "$merge_sha"
