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

repository_owner=${repository%%/*}
repository_name=${repository#*/}
merge_settings=$(mktemp "${TMPDIR:-/tmp}/env-vault-merge-settings.XXXXXX")
ruleset_pages=$(mktemp "${TMPDIR:-/tmp}/env-vault-rulesets.XXXXXX")
ruleset_detail=$(mktemp "${TMPDIR:-/tmp}/env-vault-ruleset.XXXXXX")
tag_ruleset_detail=$(mktemp "${TMPDIR:-/tmp}/env-vault-tag-ruleset.XXXXXX")
evidence_ruleset_detail=$(mktemp "${TMPDIR:-/tmp}/env-vault-evidence-ruleset.XXXXXX")
cleanup() {
  rm -f -- "$merge_settings" "$ruleset_pages" "$ruleset_detail" "$tag_ruleset_detail" "$evidence_ruleset_detail"
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
      }
    }
  ' > "$merge_settings"; then
  release_die "unable to read repository merge settings"
fi
jq -e '
  .errors == null and
  (.data.repository | type == "object") and
  .data.repository.defaultBranchRef.name == "main" and
  .data.repository.squashMergeAllowed == true and
  .data.repository.mergeCommitAllowed == false and
  .data.repository.rebaseMergeAllowed == false and
  .data.repository.squashMergeCommitTitle == "PR_TITLE" and
  .data.repository.squashMergeCommitMessage == "PR_BODY"
' "$merge_settings" >/dev/null ||
  release_die "repository merge settings do not preserve the reviewed release contract"

gh api --paginate --slurp "repos/$repository/rulesets?per_page=100" > "$ruleset_pages"
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

gh api "repos/$repository/rulesets/$ruleset_id" > "$ruleset_detail"
jq -e --arg repository "$repository" '
  .name == "Protect env-vault main" and
  .target == "branch" and
  .source_type == "Repository" and
  .source == $repository and
  .enforcement == "active" and
  (.bypass_actors | type) == "array" and
  .bypass_actors == [] and
  .current_user_can_bypass == "never" and
  .conditions.ref_name.include == ["refs/heads/main"] and
  .conditions.ref_name.exclude == [] and
  ([.rules[] | select(.type == "deletion")] | length == 1) and
  ([.rules[] | select(.type == "non_fast_forward")] | length == 1) and
  ([.rules[] | select(.type == "pull_request")] | length == 1) and
  ([.rules[] | select(.type == "pull_request")][0].parameters |
    .required_review_thread_resolution == true and
    .allowed_merge_methods == ["squash"]
  ) and
  ([.rules[] | select(.type == "required_status_checks")] | length == 1) and
  ([.rules[] | select(.type == "required_status_checks")][0].parameters |
    .strict_required_status_checks_policy == true and
    .do_not_enforce_on_create == false and
    ([
      .required_status_checks[] |
      select(
        .integration_id == 15368 and
        (
          .context == "quality-gate" or
          .context == "pr-title" or
          .context == "Dependency review" or
          .context == "Analyze (go)" or
          .context == "Analyze (actions)"
        )
      ) |
      .context
    ] | unique | length == 5)
  )
' "$ruleset_detail" >/dev/null ||
  release_die "main ruleset does not preserve the reviewed release contract"

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

gh api "repos/$repository/rulesets/$tag_ruleset_id" > "$tag_ruleset_detail"
jq -e --arg repository "$repository" '
  .name == "Protect env-vault release tags" and
  .target == "tag" and
  .source_type == "Repository" and
  .source == $repository and
  .enforcement == "active" and
  (.bypass_actors | type) == "array" and
  .bypass_actors == [] and
  .current_user_can_bypass == "never" and
  .conditions.ref_name.include == ["refs/tags/v*"] and
  .conditions.ref_name.exclude == [] and
  ([.rules[].type] | sort) == ["deletion", "update"]
' "$tag_ruleset_detail" >/dev/null ||
  release_die "release tag ruleset does not prevent moving or deleting published versions"

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

gh api "repos/$repository/rulesets/$evidence_ruleset_id" > "$evidence_ruleset_detail"
jq -e --arg repository "$repository" '
  .name == "Protect env-vault release evidence" and
  .target == "branch" and
  .source_type == "Repository" and
  .source == $repository and
  .enforcement == "active" and
  (.bypass_actors | type) == "array" and
  .bypass_actors == [] and
  .current_user_can_bypass == "never" and
  .conditions.ref_name.include == ["refs/heads/release-evidence"] and
  .conditions.ref_name.exclude == [] and
  ([.rules[].type] | sort) == ["deletion", "non_fast_forward"]
' "$evidence_ruleset_detail" >/dev/null ||
  release_die "release evidence ruleset does not preserve append-only fast-forward publication"

proof_output=${RELEASE_SETTINGS_PROOF_OUTPUT:-}
if [[ -n "$proof_output" ]]; then
  releasecheck=${RELEASECHECK:-}
  source_sha=${RELEASE_SETTINGS_SOURCE_SHA:-}
  version=${RELEASE_SETTINGS_VERSION:-}
  planning_run_id=${RELEASE_SETTINGS_PLANNING_RUN_ID:-}
  planning_run_attempt=${RELEASE_SETTINGS_PLANNING_RUN_ATTEMPT:-}
  [[ -n "$releasecheck" && -f "$releasecheck" && ! -L "$releasecheck" && -x "$releasecheck" ]] ||
    release_die "offline release checker is missing, non-regular, or non-executable"
  [[ "$source_sha" =~ ^[0-9a-f]{40}$ ]] || release_die "settings proof source SHA is malformed"
  release_require_version "$version"
  [[ "$planning_run_id" =~ ^[1-9][0-9]*$ ]] || release_die "settings proof planning run ID is malformed"
  [[ "$planning_run_attempt" =~ ^[1-9][0-9]*$ ]] || release_die "settings proof planning run attempt is malformed"
  checked_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  "$releasecheck" settings seal \
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
fi
