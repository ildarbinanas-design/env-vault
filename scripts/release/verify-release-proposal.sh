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
[[ "$repository" == "$RELEASE_SOURCE_REPOSITORY" ]] || release_die "repository differs from the release contract"
[[ "$expected_app_slug" =~ ^[a-z0-9][a-z0-9-]*$ ]] || release_die "release App slug is missing or malformed"
release_require_command gh
release_require_command jq

probe_dir=$(mktemp -d "${TMPDIR:-/tmp}/env-vault-release-proposal.XXXXXX")
cleanup() {
  rm -rf -- "$probe_dir"
}
trap cleanup EXIT

owner=${repository%%/*}
branch=$RELEASE_PLEASE_BRANCH
pull="$probe_dir/pull.json"
if [[ -n "$exact_pr_number" ]]; then
  "$SCRIPT_DIR/gh-api-read.sh" "$pull" "repos/$repository/pulls/$exact_pr_number"
  jq -e \
    --arg repository "$repository" \
    --arg branch "$branch" \
    --arg author "${expected_app_slug}[bot]" \
    --arg head "$exact_head_sha" \
    --arg base "$RELEASE_PLEASE_TARGET_BRANCH" \
    --arg header "$RELEASE_PR_HEADER" \
    --arg pending "$RELEASE_PENDING_LABEL" \
    --arg tagged "$RELEASE_TAGGED_LABEL" \
    --argjson number "$exact_pr_number" '
      .number == $number and
      .base.ref == $base and
      .base.repo.full_name == $repository and
      (.base.sha | type == "string" and test("^[0-9a-f]{40}$")) and
      .head.ref == $branch and
      .head.repo.full_name == $repository and
      .head.sha == $head and
      .user.login == $author and
      ((.body // "") | contains($header)) and
      ((.body // "") | contains("This PR was generated with Release Please.")) and
      if .state == "open" then
        .merged == false and
        ([.labels[].name] | index($pending) != null) and
        ([.labels[].name] | index($tagged) == null)
      elif .state == "closed" then
        .merged == true and
        (.merge_commit_sha | type == "string" and test("^[0-9a-f]{40}$")) and
        (.merged_at | type == "string" and try fromdateiso8601 != null) and
        ([.labels[].name] as $labels |
          [($labels | index($pending) != null),
           ($labels | index($tagged) != null)] |
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
    --raw-field "base=$RELEASE_PLEASE_TARGET_BRANCH" \
    --raw-field "head=$owner:$branch" \
    --raw-field 'per_page=100'

  pull_count=$(jq -er '[.[][]] | length' "$pull_pages") || release_die "GitHub returned malformed release pull requests"
  if [[ "$pull_count" == "0" ]]; then
    printf 'proposal=false\n'
    exit 0
  fi
  [[ "$pull_count" == "1" ]] || release_die "exactly one open Release Please pull request is required"

  jq -e --arg repository "$repository" --arg branch "$branch" --arg author "${expected_app_slug}[bot]" --arg base "$RELEASE_PLEASE_TARGET_BRANCH" --arg header "$RELEASE_PR_HEADER" --arg pending "$RELEASE_PENDING_LABEL" --arg tagged "$RELEASE_TAGGED_LABEL" '
    [.[][]][0] |
    select(
      .base.ref == $base and
      .base.repo.full_name == $repository and
      .head.ref == $branch and
      .head.repo.full_name == $repository and
      .user.login == $author and
      ((.body // "") | contains($header)) and
      ((.body // "") | contains("This PR was generated with Release Please.")) and
      ([.labels[].name] | index($pending) != null) and
      ([.labels[].name] | index($tagged) == null)
    )
  ' "$pull_pages" > "$pull" || release_die "open release proposal provenance is invalid"
fi

title=$(jq -er '.title' "$pull") || release_die "release proposal title is malformed"
version_tag=${title#"$RELEASE_PR_TITLE_PREFIX"}
[[ "$title" == "$RELEASE_PR_TITLE_PREFIX$version_tag" && "$version_tag" != "$title" ]] || release_die "release proposal title is not deterministic"
release_require_version "$version_tag"
version=${version_tag#"$RELEASE_TAG_PREFIX"}
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
jq -e --arg manifest "$RELEASE_PLEASE_MANIFEST_PATH" '
  .status == "ahead" and
  .ahead_by == 1 and
  .total_commits == 1 and
  ([.files[] | [.filename, .status]] | sort) == [
    [$manifest, "modified"],
    ["CHANGELOG.md", "modified"],
    ["README.md", "modified"]
  ]
' "$proposal_compare" >/dev/null || release_die "release proposal changed unexpected commits or paths"

tree="$probe_dir/tree.json"
"$SCRIPT_DIR/gh-api-read.sh" "$tree" "repos/$repository/git/trees/$tree_sha?recursive=1"
jq -e --arg manifest "$RELEASE_PLEASE_MANIFEST_PATH" '
  [.tree[] |
    select(
      .path == $manifest or
      .path == "CHANGELOG.md" or
      .path == "README.md"
    ) |
    [.path, .mode, .type]
  ] | sort == [
    [$manifest, "100644", "blob"],
    ["CHANGELOG.md", "100644", "blob"],
    ["README.md", "100644", "blob"]
  ]
' "$tree" >/dev/null || release_die "release proposal metadata files have unsafe modes"

main_ref="$probe_dir/main-ref.json"
"$SCRIPT_DIR/gh-api-read.sh" "$main_ref" "repos/$repository/git/ref/heads/$RELEASE_SOURCE_DEFAULT_BRANCH"
main_sha=$(jq -er '.object.sha | select(test("^[0-9a-f]{40}$"))' "$main_ref") || release_die "GitHub returned a malformed main SHA"
base_compare="$probe_dir/base-compare.json"
"$SCRIPT_DIR/gh-api-read.sh" "$base_compare" "repos/$repository/compare/$parent_sha...$main_sha"
jq -e '.status == "ahead" or .status == "identical"' "$base_compare" >/dev/null ||
  release_die "release proposal base is not contained in current main"

workflow_runs="$probe_dir/workflow-runs.json"
"$SCRIPT_DIR/gh-api-read.sh" "$workflow_runs" --paginate --slurp --method GET \
  "repos/$repository/actions/workflows/$RELEASE_CI_WORKFLOW_FILE/runs" \
  --raw-field "head_sha=$parent_sha" \
  --raw-field "branch=$RELEASE_SOURCE_DEFAULT_BRANCH" \
  --raw-field 'event=push' \
  --raw-field 'status=completed' \
  --raw-field 'per_page=100'
ci_identity=$(jq -cer --arg sha "$parent_sha" --arg repository "$repository" --arg branch "$RELEASE_SOURCE_DEFAULT_BRANCH" --arg workflow_path "$RELEASE_CI_WORKFLOW_PATH" '
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
' "$workflow_runs") || release_die "release proposal base has no strictly identified successful main ci push run"
ci_run_id=$(jq -er '.id' <<< "$ci_identity")
ci_run_attempt=$(jq -er '.run_attempt' <<< "$ci_identity")
"$SCRIPT_DIR/releasetransport.sh" actions identity \
  --output "$probe_dir/main-ci-identity.json" \
  --repository "$repository" \
  --run-id "$ci_run_id" \
  --run-attempt "$ci_run_attempt" \
  --workflow-path "$RELEASE_CI_WORKFLOW_PATH" \
  --event push \
  --head-sha "$parent_sha" \
  --head-ref "$RELEASE_SOURCE_DEFAULT_BRANCH" || release_die "release proposal base CI typed identity mismatch"

manifest="$probe_dir/manifest.json"
"$SCRIPT_DIR/releasetransport.sh" contents read --output "$manifest" --repository "$repository" \
  --path "$RELEASE_PLEASE_MANIFEST_PATH" --ref "$head_sha"
jq -e --arg version "$version" --arg key "$RELEASE_PLEASE_MANIFEST_KEY" '
  type == "object" and
  keys == [$key] and
  .[$key] == $version
' "$manifest" >/dev/null || release_die "release proposal manifest does not match its title"

readme="$probe_dir/README.md"
"$SCRIPT_DIR/releasetransport.sh" contents read --output "$readme" --repository "$repository" \
  --path README.md --ref "$head_sha"
readme_line=$(release_readme_version_line "$RELEASE_TAG_PREFIX$version")
grep -Fqx -- "$readme_line" "$readme" ||
  release_die "release proposal README does not match its manifest"

changelog="$probe_dir/CHANGELOG.md"
"$SCRIPT_DIR/releasetransport.sh" contents read --output "$changelog" --repository "$repository" \
  --path CHANGELOG.md --ref "$head_sha"
"$SCRIPT_DIR/extract-changelog-section.sh" "$RELEASE_TAG_PREFIX$version" "$changelog" >/dev/null ||
  release_die "release proposal changelog section is missing or empty"

printf 'proposal=true\n'
printf 'proposal_sha=%s\n' "$head_sha"
printf 'proposal_base_sha=%s\n' "$parent_sha"
printf 'version=%s%s\n' "$RELEASE_TAG_PREFIX" "$version"
