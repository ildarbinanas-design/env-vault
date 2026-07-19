#!/usr/bin/env bash
set -euo pipefail
export LC_ALL=C

SCRIPT_DIR=$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=scripts/release/lib.sh
source "$SCRIPT_DIR/lib.sh"
release_require_typed_contract_projection

usage() {
  printf 'usage: %s PR_NUMBER\n' "$(basename "$0")" >&2
  exit 2
}

[[ $# -eq 1 ]] || usage
pr_number=$1
repository=${GITHUB_REPOSITORY:-}

[[ "$pr_number" =~ ^[1-9][0-9]*$ ]] || release_die "pull request number is malformed"
release_require_repository "$repository"
[[ "$repository" == "$RELEASE_SOURCE_REPOSITORY" ]] || release_die "repository differs from the release contract source"
release_require_command gh
release_require_command jq

probe_dir=$(mktemp -d "${TMPDIR:-/tmp}/env-vault-release-label-state.XXXXXX")
trap 'rm -rf -- "$probe_dir"' EXIT
read_labels() {
  local phase=$1
  local payload="$probe_dir/labels-$phase.json"
  "$SCRIPT_DIR/gh-api-read.sh" "$payload" --paginate --slurp \
    "repos/$repository/issues/$pr_number/labels?per_page=100"
  jq -cer '
    select(type == "array" and all(.[]; type == "array")) |
    [ .[][] | .name ] |
    select(all(.[]; type == "string"))
  ' "$payload"
}

labels=$(read_labels initial) || release_die "release pull request labels are malformed or require pagination"
if ! jq -e --arg label "$RELEASE_TAGGED_LABEL" 'index($label) != null' <<< "$labels" >/dev/null; then
  gh api --method POST \
    "repos/$repository/issues/$pr_number/labels" \
    --raw-field "labels[]=$RELEASE_TAGGED_LABEL" \
    --silent
fi
if jq -e --arg label "$RELEASE_PENDING_LABEL" 'index($label) != null' <<< "$labels" >/dev/null; then
  pending_label_uri=$(jq -rn --arg label "$RELEASE_PENDING_LABEL" '$label | @uri')
  gh api --method DELETE \
    "repos/$repository/issues/$pr_number/labels/$pending_label_uri" \
    --silent
fi

final_labels=$(read_labels final) || release_die "final release pull request labels are malformed or require pagination"
jq -e --arg tagged "$RELEASE_TAGGED_LABEL" --arg pending "$RELEASE_PENDING_LABEL" '
  index($tagged) != null and
  index($pending) == null
' <<< "$final_labels" >/dev/null || release_die "release pull request labels were not reconciled"
