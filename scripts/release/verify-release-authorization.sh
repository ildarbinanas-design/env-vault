#!/usr/bin/env bash
set -euo pipefail
export LC_ALL=C

SCRIPT_DIR=$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=scripts/release/lib.sh
source "$SCRIPT_DIR/lib.sh"

usage() {
  printf 'usage: %s vMAJOR.MINOR.PATCH COMMIT LABEL_STATE\n' "$(basename "$0")" >&2
  exit 2
}

cleanup() {
  if [[ -n ${probe_dir:-} && -d "$probe_dir" ]]; then
    rm -rf -- "$probe_dir"
  fi
}

[[ $# -eq 3 ]] || usage
version=$1
source_sha=$2
label_state=$3
repository=${GITHUB_REPOSITORY:-}
expected_app_slug=${RELEASE_APP_SLUG:-}
authorization_output=${RELEASE_AUTHORIZATION_OUTPUT:-}

release_require_version "$version"
release_require_repository "$repository"
[[ "$source_sha" =~ ^[0-9a-f]{40}$ ]] || release_die "source commit SHA is malformed"
[[ "$expected_app_slug" =~ ^[a-z0-9][a-z0-9-]*$ ]] || release_die "release App slug is missing or malformed"
case "$label_state" in
  prepublish|tagged) ;;
  *) release_die "release pull request label state is unsupported" ;;
esac
release_require_command gh
release_require_command jq

probe_dir=$(mktemp -d "${TMPDIR:-/tmp}/env-vault-release-authorization.XXXXXX")
trap cleanup EXIT

repository_json="$probe_dir/repository.json"
"$SCRIPT_DIR/gh-api-read.sh" "$repository_json" "repos/$repository"
default_branch=$(jq -er '.default_branch | select(type == "string" and length > 0)' "$repository_json") ||
  release_die "GitHub returned a malformed default branch"
[[ "$default_branch" == "main" ]] || release_die "release authorization requires main as the default branch"

main_ref="$probe_dir/main-ref.json"
"$SCRIPT_DIR/gh-api-read.sh" "$main_ref" "repos/$repository/git/ref/heads/$default_branch"
main_sha=$(jq -er '.object.sha | select(test("^[0-9a-f]{40}$"))' "$main_ref") ||
  release_die "GitHub returned a malformed main commit SHA"

comparison="$probe_dir/comparison.json"
"$SCRIPT_DIR/gh-api-read.sh" "$comparison" "repos/$repository/compare/$source_sha...$main_sha"
comparison_status=$(jq -er '.status | select(. == "ahead" or . == "identical")' "$comparison") ||
  release_die "release commit is not contained in current main"
[[ -n "$comparison_status" ]] || release_die "release commit ancestry is unknown"

source_manifest="$probe_dir/source-manifest.json"
"$SCRIPT_DIR/gh-api-read.sh" "$source_manifest" \
  --header 'Accept: application/vnd.github.raw+json' \
  "repos/$repository/contents/.release-please-manifest.json?ref=$source_sha"
source_manifest_version=$(jq -er 'if type == "object" and (keys == ["."]) and ((.["."] | type) == "string") then .["."] else empty end' "$source_manifest") ||
  release_die "exact release source manifest is malformed"
[[ "v$source_manifest_version" == "$version" ]] ||
  release_die "release version does not match the exact release source manifest"

main_manifest="$probe_dir/main-manifest.json"
"$SCRIPT_DIR/gh-api-read.sh" "$main_manifest" \
  --header 'Accept: application/vnd.github.raw+json' \
  "repos/$repository/contents/.release-please-manifest.json?ref=$main_sha"
main_manifest_version=$(jq -er 'if type == "object" and (keys == ["."]) and ((.["."] | type) == "string") then .["."] else empty end' "$main_manifest") ||
  release_die "current main release manifest is malformed"
if [[ "$label_state" == "prepublish" ]]; then
  [[ "v$main_manifest_version" == "$version" ]] ||
    release_die "release version does not match the current main manifest before tag creation"
else
  main_comparison=$("$SCRIPT_DIR/semver-compare.sh" "$main_manifest_version" "${version#v}")
  [[ "$main_comparison" == "0" || "$main_comparison" == "1" ]] ||
    release_die "current main release manifest predates the immutable tagged release"
fi

workflow_runs="$probe_dir/workflow-runs.json"
"$SCRIPT_DIR/gh-api-read.sh" "$workflow_runs" --method GET \
  "repos/$repository/actions/workflows/ci.yml/runs" \
  --raw-field "head_sha=$source_sha" \
  --raw-field "branch=$default_branch" \
  --raw-field 'event=push' \
  --raw-field 'status=completed' \
  --raw-field 'per_page=100'
jq -e --arg sha "$source_sha" --arg branch "$default_branch" '
  [.workflow_runs[] |
    select(
      .head_sha == $sha and
      .head_branch == $branch and
      .event == "push" and
      .conclusion == "success"
    )
  ] | length >= 1
' "$workflow_runs" >/dev/null || release_die "release commit has no successful main ci push run"

pulls="$probe_dir/pulls.json"
"$SCRIPT_DIR/gh-api-read.sh" "$pulls" \
  --paginate \
  --slurp \
  --header 'Accept: application/vnd.github+json' \
  "repos/$repository/commits/$source_sha/pulls?per_page=100"

expected_title="chore(main): release env-vault $version"
expected_branch="release-please--branches--main--components--env-vault"
expected_author="${expected_app_slug}[bot]"
release_pr="$probe_dir/release-pr.json"
jq -e \
  --arg sha "$source_sha" \
  --arg repository "$repository" \
  --arg branch "$default_branch" \
  --arg head "$expected_branch" \
  --arg author "$expected_author" \
  --arg title "$expected_title" \
  --arg label_state "$label_state" '
    [.[][] |
      select(
        .state == "closed" and
        .merged_at != null and
        .merge_commit_sha == $sha and
        .base.ref == $branch and
        .base.repo.full_name == $repository and
        .head.ref == $head and
        (.head.sha | type == "string" and test("^[0-9a-f]{40}$")) and
        .head.repo.full_name == $repository and
        .user.login == $author and
        .title == $title and
        ((.body // "") | contains("Merging this unchanged reviewed pull request after the required exact tuple confirmation authorizes publication once its merge commit passes main CI.")) and
        ((.body // "") | contains("This PR was generated with Release Please.")) and
        ([.labels[].name] as $labels |
          if $label_state == "tagged" then
            ($labels | index("autorelease: tagged") != null) and
            ($labels | index("autorelease: pending") == null)
          else
            ($labels | index("autorelease: pending") != null) or
            ($labels | index("autorelease: tagged") != null)
          end
        )
      )
    ] |
    if length == 1 then .[0] else empty end
  ' "$pulls" > "$release_pr" ||
  release_die "exactly one generated merged Release Please pull request is required"

pr_number=$(jq -er '.number | select(type == "number" and . > 0 and floor == .)' "$release_pr") ||
  release_die "generated release pull request number is malformed"
pr_head_sha=$(jq -er '.head.sha | select(test("^[0-9a-f]{40}$"))' "$release_pr") ||
  release_die "generated release pull request head SHA is malformed"
merged_at=$(jq -er '.merged_at | select(type == "string") | fromdateiso8601 | todateiso8601' "$release_pr") ||
  release_die "generated release pull request merge time is malformed"

canonical_body="ПОДТВЕРЖДАЮ RELEASE $version PR #$pr_number SHA $pr_head_sha"
comments="$probe_dir/comments.json"
"$SCRIPT_DIR/gh-api-read.sh" "$comments" \
  --paginate \
  --slurp \
  --header 'Accept: application/vnd.github+json' \
  "repos/$repository/issues/$pr_number/comments?per_page=100"

confirmation="$probe_dir/confirmation.json"
jq -e \
  --arg body "$canonical_body" \
  --arg merged_at "$merged_at" \
  --arg repository "$repository" \
  --argjson pr_number "$pr_number" '
    [.[][] |
      select(
        (.id | type == "number" and . > 0 and floor == .) and
        .body == $body and
        .user.type == "User" and
        (.user.login | type == "string" and test("^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$")) and
        (.author_association == "OWNER" or .author_association == "MEMBER") and
        (.created_at | type == "string") and
        (.updated_at | type == "string") and
        ((.created_at | fromdateiso8601) < ($merged_at | fromdateiso8601)) and
        ((.updated_at | fromdateiso8601) < ($merged_at | fromdateiso8601)) and
        .html_url == ("https://github.com/" + $repository + "/pull/" + ($pr_number | tostring) + "#issuecomment-" + (.id | tostring))
      )
    ] |
    if length == 1 then .[0] else empty end
  ' "$comments" > "$confirmation" ||
  release_die "exactly one exact pre-merge release confirmation comment from a repository owner or member is required"

canonical_body_file="$probe_dir/canonical-body.txt"
printf '%s' "$canonical_body" > "$canonical_body_file"
body_sha256=$(release_sha256_file "$canonical_body_file")

if [[ -n "$authorization_output" ]]; then
  [[ ! -e "$authorization_output" && ! -L "$authorization_output" ]] ||
    release_die "refusing to overwrite release authorization output"
  authorization_parent=$(dirname -- "$authorization_output")
  [[ -d "$authorization_parent" && ! -L "$authorization_parent" ]] ||
    release_die "release authorization output directory is invalid"
  jq -n \
    --arg repository "$repository" \
    --arg version "$version" \
    --arg source_sha "$source_sha" \
    --arg head_sha "$pr_head_sha" \
    --arg merged_at "$merged_at" \
    --arg body_sha256 "$body_sha256" \
    --argjson pr_number "$pr_number" \
    --slurpfile confirmation "$confirmation" '
      {
        repository: $repository,
        release_version: $version,
        release_source_sha: $source_sha,
        generated_release_pr: {
          number: $pr_number,
          head_sha: $head_sha,
          merge_sha: $source_sha,
          merged_at: $merged_at
        },
        confirmation: {
          comment_id: $confirmation[0].id,
          url: $confirmation[0].html_url,
          actor: $confirmation[0].user.login,
          actor_association: $confirmation[0].author_association,
          created_at: $confirmation[0].created_at,
          updated_at: $confirmation[0].updated_at,
          body_sha256: $body_sha256
        },
        result: "pass"
      }
    ' > "$authorization_output"
  release_require_regular_file "$authorization_output"
fi

printf '%s\n' "$pr_number"
