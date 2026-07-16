#!/usr/bin/env bash
set -euo pipefail
export LC_ALL=C

SCRIPT_DIR=$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=scripts/release/lib.sh
source "$SCRIPT_DIR/lib.sh"

exact_pr_number=''
exact_head_sha=''
case $# in
  0) ;;
  2)
    exact_pr_number=$1
    exact_head_sha=$2
    [[ "$exact_pr_number" =~ ^[1-9][0-9]{0,8}$ ]] || release_die "exact pull request number is malformed"
    [[ "$exact_head_sha" =~ ^[0-9a-f]{40}$ ]] || release_die "exact pull request head SHA is malformed"
    ;;
  *)
    printf 'usage: %s [PR_NUMBER EXACT_HEAD_SHA]\n' "$(basename "$0")" >&2
    exit 2
    ;;
esac

repository=${GITHUB_REPOSITORY:-}
expected_app_slug=${RELEASE_APP_SLUG:-}
release_require_repository "$repository"
[[ "$expected_app_slug" =~ ^[a-z0-9][a-z0-9-]*$ ]] || release_die "release App slug is missing or malformed"
release_require_command gh
release_require_command jq

probe_dir=$(mktemp -d "${TMPDIR:-/tmp}/env-vault-release-proposal.XXXXXX")
cleanup() {
  rm -rf -- "$probe_dir"
}
trap cleanup EXIT

owner=${repository%%/*}
branch=release-please--branches--main--components--env-vault
pull="$probe_dir/pull.json"
if [[ -n "$exact_pr_number" ]]; then
  "$SCRIPT_DIR/gh-api-read.sh" "$pull" "repos/$repository/pulls/$exact_pr_number"
  jq -e \
    --arg repository "$repository" \
    --arg branch "$branch" \
    --arg author "${expected_app_slug}[bot]" \
    --arg head "$exact_head_sha" \
    --argjson number "$exact_pr_number" '
      .number == $number and
      .base.ref == "main" and
      .base.repo.full_name == $repository and
      (.base.sha | type == "string" and test("^[0-9a-f]{40}$")) and
      .head.ref == $branch and
      .head.repo.full_name == $repository and
      .head.sha == $head and
      .user.login == $author and
      ((.body // "") | contains("Merging this unchanged reviewed pull request after the required exact tuple confirmation authorizes publication once its merge commit passes main CI.")) and
      ((.body // "") | contains("This PR was generated with Release Please.")) and
      if .state == "open" then
        .merged == false and
        ([.labels[].name] | index("autorelease: pending") != null) and
        ([.labels[].name] | index("autorelease: tagged") == null)
      elif .state == "closed" then
        .merged == true and
        (.merge_commit_sha | type == "string" and test("^[0-9a-f]{40}$")) and
        (.merged_at | type == "string" and try fromdateiso8601 != null) and
        ([.labels[].name] as $labels |
          [($labels | index("autorelease: pending") != null),
           ($labels | index("autorelease: tagged") != null)] |
          map(select(. == true)) | length == 1
        )
      else
        false
      end
    ' "$pull" >/dev/null || release_die "exact release proposal provenance is invalid"
else
  pull_pages="$probe_dir/pulls.json"
  "$SCRIPT_DIR/gh-api-read.sh" "$pull_pages" --paginate --slurp --method GET \
    "repos/$repository/pulls" \
    --raw-field 'state=open' \
    --raw-field 'base=main' \
    --raw-field "head=$owner:$branch" \
    --raw-field 'per_page=100'

  pull_count=$(jq -er '[.[][]] | length' "$pull_pages") || release_die "GitHub returned malformed release pull requests"
  if [[ "$pull_count" == "0" ]]; then
    printf 'proposal=false\n'
    exit 0
  fi
  [[ "$pull_count" == "1" ]] || release_die "exactly one open Release Please pull request is required"

  jq -e --arg repository "$repository" --arg branch "$branch" --arg author "${expected_app_slug}[bot]" '
    [.[][]][0] |
    select(
      .base.ref == "main" and
      .base.repo.full_name == $repository and
      .head.ref == $branch and
      .head.repo.full_name == $repository and
      .user.login == $author and
      ((.body // "") | contains("Merging this unchanged reviewed pull request after the required exact tuple confirmation authorizes publication once its merge commit passes main CI.")) and
      ((.body // "") | contains("This PR was generated with Release Please.")) and
      ([.labels[].name] | index("autorelease: pending") != null) and
      ([.labels[].name] | index("autorelease: tagged") == null)
    )
  ' "$pull_pages" > "$pull" || release_die "open release proposal provenance is invalid"
fi

title=$(jq -er '.title' "$pull") || release_die "release proposal title is malformed"
version=$(jq -nr --arg title "$title" '
  $title |
  capture("^chore\\(main\\): release env-vault v(?<version>(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*))$").version
') || release_die "release proposal title is not deterministic"
head_sha=$(jq -er '.head.sha | select(test("^[0-9a-f]{40}$"))' "$pull") || release_die "release proposal head SHA is malformed"

commit="$probe_dir/commit.json"
"$SCRIPT_DIR/gh-api-read.sh" "$commit" "repos/$repository/git/commits/$head_sha"
parent_sha=$(jq -er '
  select((.parents | length) == 1) |
  .parents[0].sha |
  select(test("^[0-9a-f]{40}$"))
' "$commit") || release_die "release proposal must contain exactly one commit"
pull_base_sha=$(jq -er '.base.sha | select(test("^[0-9a-f]{40}$"))' "$pull") ||
  release_die "release proposal base SHA is malformed"
[[ "$pull_base_sha" == "$parent_sha" ]] ||
  release_die "release proposal base is not the exact head commit parent"
jq -e --arg title "$title" '
  (.message | split("\n")[0]) == $title and
  (.tree.sha | test("^[0-9a-f]{40}$"))
' "$commit" >/dev/null || release_die "release proposal commit metadata is invalid"
tree_sha=$(jq -er '.tree.sha' "$commit")

proposal_compare="$probe_dir/proposal-compare.json"
"$SCRIPT_DIR/gh-api-read.sh" "$proposal_compare" "repos/$repository/compare/$parent_sha...$head_sha"
jq -e '
  .status == "ahead" and
  .ahead_by == 1 and
  .total_commits == 1 and
  ([.files[] | [.filename, .status]] | sort) == [
    [".release-please-manifest.json", "modified"],
    ["CHANGELOG.md", "modified"],
    ["README.md", "modified"]
  ]
' "$proposal_compare" >/dev/null || release_die "release proposal changed unexpected commits or paths"

tree="$probe_dir/tree.json"
"$SCRIPT_DIR/gh-api-read.sh" "$tree" "repos/$repository/git/trees/$tree_sha?recursive=1"
jq -e '
  [.tree[] |
    select(
      .path == ".release-please-manifest.json" or
      .path == "CHANGELOG.md" or
      .path == "README.md"
    ) |
    [.path, .mode, .type]
  ] | sort == [
    [".release-please-manifest.json", "100644", "blob"],
    ["CHANGELOG.md", "100644", "blob"],
    ["README.md", "100644", "blob"]
  ]
' "$tree" >/dev/null || release_die "release proposal metadata files have unsafe modes"

main_ref="$probe_dir/main-ref.json"
"$SCRIPT_DIR/gh-api-read.sh" "$main_ref" "repos/$repository/git/ref/heads/main"
main_sha=$(jq -er '.object.sha | select(test("^[0-9a-f]{40}$"))' "$main_ref") || release_die "GitHub returned a malformed main SHA"
base_compare="$probe_dir/base-compare.json"
"$SCRIPT_DIR/gh-api-read.sh" "$base_compare" "repos/$repository/compare/$parent_sha...$main_sha"
jq -e '.status == "ahead" or .status == "identical"' "$base_compare" >/dev/null ||
  release_die "release proposal base is not contained in current main"

workflow_runs="$probe_dir/workflow-runs.json"
"$SCRIPT_DIR/gh-api-read.sh" "$workflow_runs" --method GET \
  "repos/$repository/actions/workflows/ci.yml/runs" \
  --raw-field "head_sha=$parent_sha" \
  --raw-field 'branch=main' \
  --raw-field 'event=push' \
  --raw-field 'status=completed' \
  --raw-field 'per_page=100'
jq -e --arg sha "$parent_sha" '
  [.workflow_runs[] |
    select(
      .head_sha == $sha and
      .head_branch == "main" and
      .event == "push" and
      .conclusion == "success"
    )
  ] | length >= 1
' "$workflow_runs" >/dev/null || release_die "release proposal base has no successful main ci push run"

manifest="$probe_dir/manifest.json"
"$SCRIPT_DIR/gh-api-read.sh" "$manifest" --header 'Accept: application/vnd.github.raw+json' \
  "repos/$repository/contents/.release-please-manifest.json?ref=$head_sha"
jq -e --arg version "$version" '
  type == "object" and
  keys == ["."] and
  .["."] == $version
' "$manifest" >/dev/null || release_die "release proposal manifest does not match its title"

readme="$probe_dir/README.md"
"$SCRIPT_DIR/gh-api-read.sh" "$readme" --header 'Accept: application/vnd.github.raw+json' \
  "repos/$repository/contents/README.md?ref=$head_sha"
readme_line=$(release_readme_version_line "v$version")
grep -Fqx -- "$readme_line" "$readme" ||
  release_die "release proposal README does not match its manifest"

changelog="$probe_dir/CHANGELOG.md"
"$SCRIPT_DIR/gh-api-read.sh" "$changelog" --header 'Accept: application/vnd.github.raw+json' \
  "repos/$repository/contents/CHANGELOG.md?ref=$head_sha"
"$SCRIPT_DIR/extract-changelog-section.sh" "v$version" "$changelog" >/dev/null ||
  release_die "release proposal changelog section is missing or empty"

printf 'proposal=true\n'
printf 'proposal_sha=%s\n' "$head_sha"
printf 'proposal_base_sha=%s\n' "$parent_sha"
printf 'version=v%s\n' "$version"
