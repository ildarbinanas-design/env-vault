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

settings=$(gh api "repos/$repository")
jq -e '
  .default_branch == "main" and
  .allow_squash_merge == true and
  .allow_merge_commit == false and
  .allow_rebase_merge == false and
  .squash_merge_commit_title == "PR_TITLE" and
  .squash_merge_commit_message == "PR_BODY"
' <<< "$settings" >/dev/null ||
  release_die "repository merge settings do not preserve the reviewed release contract"

ruleset_pages=$(mktemp "${TMPDIR:-/tmp}/env-vault-rulesets.XXXXXX")
ruleset_detail=$(mktemp "${TMPDIR:-/tmp}/env-vault-ruleset.XXXXXX")
tag_ruleset_detail=$(mktemp "${TMPDIR:-/tmp}/env-vault-tag-ruleset.XXXXXX")
cleanup() {
  rm -f -- "$ruleset_pages" "$ruleset_detail" "$tag_ruleset_detail"
}
trap cleanup EXIT

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
          .context == "Dependency review" or
          .context == "Analyze (go)" or
          .context == "Analyze (actions)"
        )
      ) |
      .context
    ] | unique | length == 4)
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
  .current_user_can_bypass == "never" and
  .conditions.ref_name.include == ["refs/tags/v*"] and
  .conditions.ref_name.exclude == [] and
  ([.rules[].type] | sort) == ["deletion", "update"]
' "$tag_ruleset_detail" >/dev/null ||
  release_die "release tag ruleset does not prevent moving or deleting published versions"
