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

release_require_version "$version"
release_require_repository "$repository"
[[ "$repository" == "$RELEASE_SOURCE_REPOSITORY" ]] || release_die "repository differs from the release contract"
[[ "$source_sha" =~ ^[0-9a-f]{40}$ ]] || release_die "source commit SHA is malformed"
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
[[ "$default_branch" == "$RELEASE_SOURCE_DEFAULT_BRANCH" ]] || release_die "release authorization default branch differs from the contract"

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
"$SCRIPT_DIR/releasetransport.sh" contents read --output "$source_manifest" --repository "$repository" \
  --path "$RELEASE_PLEASE_MANIFEST_PATH" --ref "$source_sha"
source_manifest_version=$(jq -er --arg key "$RELEASE_PLEASE_MANIFEST_KEY" 'if type == "object" and (keys == [$key]) and ((.[$key] | type) == "string") then .[$key] else empty end' "$source_manifest") ||
  release_die "exact release source manifest is malformed"
[[ "$RELEASE_TAG_PREFIX$source_manifest_version" == "$version" ]] ||
  release_die "release version does not match the exact release source manifest"

main_manifest="$probe_dir/main-manifest.json"
"$SCRIPT_DIR/releasetransport.sh" contents read --output "$main_manifest" --repository "$repository" \
  --path "$RELEASE_PLEASE_MANIFEST_PATH" --ref "$main_sha"
main_manifest_version=$(jq -er --arg key "$RELEASE_PLEASE_MANIFEST_KEY" 'if type == "object" and (keys == [$key]) and ((.[$key] | type) == "string") then .[$key] else empty end' "$main_manifest") ||
  release_die "current main release manifest is malformed"
if [[ "$label_state" == "prepublish" ]]; then
  [[ "$RELEASE_TAG_PREFIX$main_manifest_version" == "$version" ]] ||
    release_die "release version does not match the current main manifest before tag creation"
else
  main_comparison=$("$SCRIPT_DIR/semver-compare.sh" "$main_manifest_version" "${version#"$RELEASE_TAG_PREFIX"}")
  [[ "$main_comparison" == "0" || "$main_comparison" == "1" ]] ||
    release_die "current main release manifest predates the immutable tagged release"
fi

workflow_runs="$probe_dir/workflow-runs.json"
"$SCRIPT_DIR/gh-api-read.sh" "$workflow_runs" --paginate --slurp --method GET \
  "repos/$repository/actions/workflows/$RELEASE_CI_WORKFLOW_FILE/runs" \
  --raw-field "head_sha=$source_sha" \
  --raw-field "branch=$default_branch" \
  --raw-field 'event=push' \
  --raw-field 'status=completed' \
  --raw-field 'per_page=100'
ci_identity=$(jq -cer --arg sha "$source_sha" --arg branch "$default_branch" --arg repository "$repository" --arg workflow_path "$RELEASE_CI_WORKFLOW_PATH" '
  [.[] | .workflow_runs[] |
    select(
      .repository.full_name == $repository and
      .head_repository.full_name == $repository and
      .head_sha == $sha and
      .head_branch == $branch and
      .event == "push" and
      .path == $workflow_path and
      .status == "completed" and
      .conclusion == "success" and
      (.id | type == "number" and . > 0 and floor == .) and
      (.run_attempt | type == "number" and . > 0 and floor == .) and
      .html_url == ("https://github.com/" + $repository + "/actions/runs/" + (.id | tostring))
    )
  ] | select(length >= 1) | max_by(.id)
' "$workflow_runs") || release_die "release commit has no strictly identified successful main ci push run"
ci_run_id=$(jq -er '.id' <<< "$ci_identity")
ci_run_attempt=$(jq -er '.run_attempt' <<< "$ci_identity")
"$SCRIPT_DIR/releasetransport.sh" actions identity \
  --output "$probe_dir/main-ci-identity.json" \
  --repository "$repository" \
  --run-id "$ci_run_id" \
  --run-attempt "$ci_run_attempt" \
  --workflow-path "$RELEASE_CI_WORKFLOW_PATH" \
  --event push \
  --head-sha "$source_sha" \
  --head-ref "$default_branch" || release_die "release commit CI typed identity mismatch"

pulls="$probe_dir/pulls.json"
"$SCRIPT_DIR/gh-api-read.sh" "$pulls" \
  --paginate \
  --slurp \
  --header 'Accept: application/vnd.github+json' \
  "repos/$repository/commits/$source_sha/pulls?per_page=100"

expected_title="$RELEASE_PR_TITLE_PREFIX$version"
expected_branch="$RELEASE_PLEASE_BRANCH"
release_pr="$probe_dir/release-pr.json"
jq -e \
  --arg sha "$source_sha" \
  --arg repository "$repository" \
  --arg branch "$default_branch" \
  --arg head "$expected_branch" \
  --arg title "$expected_title" \
  --arg header "$RELEASE_PR_HEADER" \
  --arg pending "$RELEASE_PENDING_LABEL" \
  --arg tagged "$RELEASE_TAGGED_LABEL" \
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
        .title == $title and
        ((.body // "") | contains($header)) and
        ((.body // "") | contains("This PR was generated with Release Please.")) and
        ([.labels[].name] as $labels |
          if $label_state == "tagged" then
            ($labels | index($tagged) != null) and
            ($labels | index($pending) == null)
          else
            ($labels | index($pending) != null) or
            ($labels | index($tagged) != null)
          end
        )
      )
    ] |
    if length == 1 then .[0] else empty end
  ' "$pulls" > "$release_pr" ||
  release_die "exactly one generated merged Release Please pull request is required"

pr_number=$(jq -er '.number | select(type == "number" and . > 0 and floor == .)' "$release_pr") ||
  release_die "generated release pull request number is malformed"
jq -er '.head.sha | select(test("^[0-9a-f]{40}$"))' "$release_pr" >/dev/null ||
  release_die "generated release pull request head SHA is malformed"

# Merging the generated release pull request is itself the authorization. The
# byte-exact confirmation-comment ceremony was removed on 2026-07-30; the exact
# identity checks above (generated PR, its merge commit, labels, manifest
# version, and the typed successful main CI attempt) remain the release gate.

printf '%s\n' "$pr_number"
