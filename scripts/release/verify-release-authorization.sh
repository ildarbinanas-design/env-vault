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
gh api "repos/$repository" > "$repository_json"
default_branch=$(jq -er '.default_branch | select(type == "string" and length > 0)' "$repository_json") ||
  release_die "GitHub returned a malformed default branch"
[[ "$default_branch" == "main" ]] || release_die "release authorization requires main as the default branch"

main_ref="$probe_dir/main-ref.json"
gh api "repos/$repository/git/ref/heads/$default_branch" > "$main_ref"
main_sha=$(jq -er '.object.sha | select(test("^[0-9a-f]{40}$"))' "$main_ref") ||
  release_die "GitHub returned a malformed main commit SHA"

comparison="$probe_dir/comparison.json"
gh api "repos/$repository/compare/$source_sha...$main_sha" > "$comparison"
comparison_status=$(jq -er '.status | select(. == "ahead" or . == "identical")' "$comparison") ||
  release_die "release commit is not contained in current main"
[[ -n "$comparison_status" ]] || release_die "release commit ancestry is unknown"

manifest="$probe_dir/manifest.json"
gh api \
  --header 'Accept: application/vnd.github.raw+json' \
  "repos/$repository/contents/.release-please-manifest.json?ref=$main_sha" > "$manifest"
manifest_version=$(jq -er 'if type == "object" and (keys == ["."]) and ((.["."] | type) == "string") then .["."] else empty end' "$manifest") ||
  release_die "current main release manifest is malformed"
[[ "v$manifest_version" == "$version" ]] ||
  release_die "release version does not match the current main manifest"

workflow_runs="$probe_dir/workflow-runs.json"
gh api --method GET \
  "repos/$repository/actions/workflows/ci.yml/runs" \
  --raw-field "head_sha=$source_sha" \
  --raw-field "branch=$default_branch" \
  --raw-field 'event=push' \
  --raw-field 'status=completed' \
  --raw-field 'per_page=100' > "$workflow_runs"
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
gh api \
  --paginate \
  --slurp \
  --header 'Accept: application/vnd.github+json' \
  "repos/$repository/commits/$source_sha/pulls?per_page=100" > "$pulls"

expected_title="chore(main): release env-vault $version"
expected_branch="release-please--branches--main--components--env-vault"
expected_author="${expected_app_slug}[bot]"
pr_number=$(jq -er \
  --arg sha "$source_sha" \
  --arg repository "$repository" \
  --arg branch "$default_branch" \
  --arg head "$expected_branch" \
  --arg author "$expected_author" \
  --arg title "$expected_title" \
  --arg label_state "$label_state" '
    [.[][] |
      select(
        .merged_at != null and
        .merge_commit_sha == $sha and
        .base.ref == $branch and
        .base.repo.full_name == $repository and
        .head.ref == $head and
        .head.repo.full_name == $repository and
        .user.login == $author and
        .title == $title and
        ((.body // "") | contains("Merging this reviewed pull request authorizes publication of this exact version after the merge commit passes main CI.")) and
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
    if length == 1 then .[0].number else empty end |
    select(type == "number" and . > 0 and floor == .)
  ' "$pulls") || release_die "exactly one generated merged Release Please pull request is required"

printf '%s\n' "$pr_number"
