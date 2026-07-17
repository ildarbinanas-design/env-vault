#!/usr/bin/env bash
set -euo pipefail
export LC_ALL=C

SCRIPT_DIR=$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=scripts/release/lib.sh
source "$SCRIPT_DIR/lib.sh"

[[ $# -eq 0 ]] || {
  printf 'usage: %s\n' "$(basename "$0")" >&2
  exit 2
}

repository=${GITHUB_REPOSITORY:-}
release_require_repository "$repository"
release_require_command gh
release_require_command jq

releasecheck=${RELEASECHECK:-}
[[ -n "$releasecheck" && -f "$releasecheck" && ! -L "$releasecheck" && -x "$releasecheck" ]] ||
  release_die "offline release checker is missing, non-regular, or non-executable"

repository_owner=${repository%%/*}
repository_name=${repository#*/}
probe_dir=$(mktemp -d "${TMPDIR:-/tmp}/env-vault-release-settings.XXXXXX")
merge_settings="$probe_dir/merge-settings.json"
ruleset_pages="$probe_dir/rulesets.json"
ruleset_detail="$probe_dir/main-ruleset.json"
tag_ruleset_detail="$probe_dir/tag-ruleset.json"
evidence_ruleset_detail="$probe_dir/evidence-ruleset.json"
cleanup() {
  rm -rf -- "$probe_dir"
}
trap cleanup EXIT

# GraphQL variable references must reach GitHub literally.
# shellcheck disable=SC2016
if ! gh api graphql \
  -f owner="$repository_owner" \
  -f name="$repository_name" \
  -f query='
    query RepositoryReleaseSettings($owner: String!, $name: String!) {
      repository(owner: $owner, name: $name) {
        defaultBranchRef {
          name
        }
        mergeCommitAllowed
        rebaseMergeAllowed
        squashMergeAllowed
        squashMergeCommitTitle
        squashMergeCommitMessage
        rulesets(first: 100, includeParents: false) {
          totalCount
          pageInfo {
            hasNextPage
          }
          nodes {
            databaseId
            name
            enforcement
            target
            source {
              __typename
              ... on Repository {
                nameWithOwner
              }
            }
            bypassActors(first: 1) {
              totalCount
            }
          }
        }
      }
    }
  ' > "$merge_settings"; then
  release_die "unable to read repository merge settings and bypass policy"
fi

"$SCRIPT_DIR/gh-api-read.sh" "$ruleset_pages" --paginate --slurp \
  "repos/$repository/rulesets?per_page=100"
ruleset_id=$(jq -er '
  [
    .[][] |
    select(
      .name == "Protect env-vault main" and
      .target == "branch" and
      .source_type == "Repository" and
      .enforcement == "active"
    )
  ] |
  if length == 1 then .[0].id else empty end |
  select(type == "number" and . > 0 and floor == .)
' "$ruleset_pages") || release_die "exactly one active env-vault main ruleset is required"

"$SCRIPT_DIR/gh-api-read.sh" "$ruleset_detail" "repos/$repository/rulesets/$ruleset_id"

tag_ruleset_id=$(jq -er '
  [
    .[][] |
    select(
      .name == "Protect env-vault release tags" and
      .target == "tag" and
      .source_type == "Repository" and
      .enforcement == "active"
    )
  ] |
  if length == 1 then .[0].id else empty end |
  select(type == "number" and . > 0 and floor == .)
' "$ruleset_pages") || release_die "exactly one active env-vault release tag ruleset is required"

"$SCRIPT_DIR/gh-api-read.sh" "$tag_ruleset_detail" "repos/$repository/rulesets/$tag_ruleset_id"

evidence_ruleset_id=$(jq -er '
  [
    .[][] |
    select(
      .name == "Protect env-vault release evidence" and
      .target == "branch" and
      .source_type == "Repository" and
      .enforcement == "active"
    )
  ] |
  if length == 1 then .[0].id else empty end |
  select(type == "number" and . > 0 and floor == .)
' "$ruleset_pages") || release_die "exactly one active release evidence ruleset is required"

"$SCRIPT_DIR/gh-api-read.sh" "$evidence_ruleset_detail" "repos/$repository/rulesets/$evidence_ruleset_id"

proof_output=${RELEASE_SETTINGS_PROOF_OUTPUT:-}
if [[ -n "$proof_output" ]]; then
  source_sha=${RELEASE_SETTINGS_SOURCE_SHA:-}
  version=${RELEASE_SETTINGS_VERSION:-}
  planning_run_id=${RELEASE_SETTINGS_PLANNING_RUN_ID:-}
  planning_run_attempt=${RELEASE_SETTINGS_PLANNING_RUN_ATTEMPT:-}
  [[ "$source_sha" =~ ^[0-9a-f]{40}$ ]] || release_die "settings proof source SHA is malformed"
  release_require_version "$version"
  [[ "$planning_run_id" =~ ^[1-9][0-9]*$ ]] || release_die "settings proof planning run ID is malformed"
  [[ "$planning_run_attempt" =~ ^[1-9][0-9]*$ ]] || release_die "settings proof planning run attempt is malformed"
  checked_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  env -i "$releasecheck" settings seal \
    --contract "$RELEASE_CONTRACT_PATH" \
    --repository "$repository" \
    --source-sha "$source_sha" \
    --release-version "$version" \
    --planning-run-id "$planning_run_id" \
    --planning-run-attempt "$planning_run_attempt" \
    --checked-at "$checked_at" \
    --merge-settings "$merge_settings" \
    --ruleset-pages "$ruleset_pages" \
    --main-ruleset "$ruleset_detail" \
    --tag-ruleset "$tag_ruleset_detail" \
    --evidence-ruleset "$evidence_ruleset_detail" \
    --output "$proof_output"
else
  env -i "$releasecheck" settings check \
    --contract "$RELEASE_CONTRACT_PATH" \
    --repository "$repository" \
    --merge-settings "$merge_settings" \
    --ruleset-pages "$ruleset_pages" \
    --main-ruleset "$ruleset_detail" \
    --tag-ruleset "$tag_ruleset_detail" \
    --evidence-ruleset "$evidence_ruleset_detail" \
    --json
fi
