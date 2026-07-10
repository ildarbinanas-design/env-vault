#!/usr/bin/env bash
set -euo pipefail
export LC_ALL=C

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=scripts/release/lib.sh
source "$SCRIPT_DIR/lib.sh"

usage() {
  printf 'usage: %s OWNER/REPO PR_NUMBER EXPECTED_HEAD_SHA\n' "$(basename "$0")" >&2
  exit 2
}

require_nonnegative_integer() {
  local name=$1
  local value=$2
  [[ "$value" =~ ^(0|[1-9][0-9]{0,8})$ ]] ||
    release_die "$name must be a non-negative integer with at most 9 digits"
}

load_pull_request() {
  local output

  output=$(gh pr view "$pr_number" \
    --repo "$repository" \
    --json number,state,headRefOid,baseRefName,isDraft,mergeCommit \
    --jq '[.number, .state, .headRefOid, .baseRefName, (.isDraft | tostring), (.mergeCommit.oid // "-")] | map(tostring) | join("|")') ||
    release_die "cannot inspect Homebrew pull request"

  [[ -n "$output" && "$output" != *$'\n'* ]] ||
    release_die "GitHub returned malformed pull request data"

  returned_number=''
  pr_state=''
  pr_head_sha=''
  pr_base=''
  pr_is_draft=''
  pr_merge_sha=''
  extra=''
  IFS='|' read -r returned_number pr_state pr_head_sha pr_base pr_is_draft pr_merge_sha extra <<< "$output"

  [[ "$returned_number" =~ ^[1-9][0-9]*$ && "$returned_number" == "$pr_number" && -z "$extra" ]] ||
    release_die "GitHub returned malformed pull request data"
  [[ "$pr_state" == "OPEN" || "$pr_state" == "MERGED" || "$pr_state" == "CLOSED" ]] ||
    release_die "GitHub returned malformed pull request state"
  [[ "$pr_head_sha" =~ ^[0-9a-f]{40}$ ]] ||
    release_die "GitHub returned a malformed pull request head SHA"
  [[ "$pr_head_sha" == "$expected_head_sha" ]] ||
    release_die "pull request head SHA changed"
  [[ "$pr_base" == "main" ]] ||
    release_die "pull request base branch is not main"
  [[ "$pr_is_draft" == "false" ]] ||
    release_die "pull request is a draft"
}

[[ $# -eq 3 ]] || usage
repository=$1
pr_number=$2
expected_head_sha=$3

release_require_repository "$repository"
[[ "$pr_number" =~ ^[1-9][0-9]*$ ]] || release_die "pull request number must be a positive integer"
[[ "$expected_head_sha" =~ ^[0-9a-f]{40}$ ]] ||
  release_die "expected head SHA must contain exactly 40 lowercase hexadecimal characters"
[[ -n ${GH_TOKEN:-} ]] || release_die "GH_TOKEN is required to merge a Homebrew pull request"
release_require_command gh
release_require_command sleep

timeout_input=${HOMEBREW_PR_MERGE_TIMEOUT_SECONDS:-300}
interval_input=${HOMEBREW_PR_MERGE_INTERVAL_SECONDS:-5}
require_nonnegative_integer "merge timeout" "$timeout_input"
require_nonnegative_integer "merge poll interval" "$interval_input"
timeout_seconds=$((10#$timeout_input))
interval_seconds=$((10#$interval_input))

load_pull_request
[[ "$pr_state" == "OPEN" ]] || release_die "pull request is not open"
[[ "$pr_merge_sha" == "-" ]] ||
  release_die "open pull request unexpectedly has a merge commit"

if ! gh pr merge "$pr_number" \
  --repo "$repository" \
  --squash \
  --match-head-commit "$expected_head_sha" >&2; then
  release_die "cannot squash-merge Homebrew pull request"
fi

started_at=$SECONDS
while true; do
  load_pull_request

  case "$pr_state" in
    MERGED)
      [[ "$pr_merge_sha" =~ ^[0-9a-f]{40}$ ]] ||
        release_die "merged pull request has an invalid merge commit SHA"
      printf '%s\n' "$pr_merge_sha"
      exit 0
      ;;
    CLOSED)
      release_die "pull request closed without merging"
      ;;
    OPEN)
      [[ "$pr_merge_sha" == "-" ]] ||
        release_die "open pull request unexpectedly has a merge commit"
      ;;
  esac

  elapsed=$((SECONDS - started_at))
  if ((elapsed >= timeout_seconds)); then
    release_die "timed out after ${timeout_seconds}s waiting for pull request merge"
  fi

  remaining=$((timeout_seconds - elapsed))
  sleep_for=$interval_seconds
  if ((sleep_for > remaining)); then
    sleep_for=$remaining
  fi
  if ((sleep_for > 0)); then
    sleep "$sleep_for"
  fi
done
