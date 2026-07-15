#!/usr/bin/env bash
set -euo pipefail
export LC_ALL=C

SCRIPT_DIR=$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=scripts/release/lib.sh
source "$SCRIPT_DIR/lib.sh"

usage() {
  printf 'usage: %s PR_NUMBER\n' "$(basename "$0")" >&2
  exit 2
}

[[ $# -eq 1 ]] || usage
pr_number=$1
repository=${GITHUB_REPOSITORY:-}

[[ "$pr_number" =~ ^[1-9][0-9]*$ ]] || release_die "pull request number is malformed"
release_require_repository "$repository"
release_require_command gh
release_require_command jq

read_labels() {
  local payload
  payload=$(gh api "repos/$repository/issues/$pr_number/labels?per_page=100")
  jq -cer '
    select(type == "array" and length < 100) |
    [.[].name] |
    select(all(.[]; type == "string"))
  ' <<< "$payload"
}

labels=$(read_labels) || release_die "release pull request labels are malformed or require pagination"
if ! jq -e 'index("autorelease: tagged") != null' <<< "$labels" >/dev/null; then
  gh api --method POST \
    "repos/$repository/issues/$pr_number/labels" \
    --raw-field 'labels[]=autorelease: tagged' \
    --silent
fi
if jq -e 'index("autorelease: pending") != null' <<< "$labels" >/dev/null; then
  gh api --method DELETE \
    "repos/$repository/issues/$pr_number/labels/autorelease%3A%20pending" \
    --silent
fi

final_labels=$(read_labels) || release_die "final release pull request labels are malformed or require pagination"
jq -e '
  index("autorelease: tagged") != null and
  index("autorelease: pending") == null
' <<< "$final_labels" >/dev/null || release_die "release pull request labels were not reconciled"
