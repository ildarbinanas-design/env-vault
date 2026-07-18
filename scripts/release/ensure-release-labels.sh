#!/usr/bin/env bash
set -euo pipefail
export LC_ALL=C

SCRIPT_DIR=$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=scripts/release/lib.sh
source "$SCRIPT_DIR/lib.sh"
release_require_typed_contract_projection

[[ $# -eq 0 ]] || {
  printf 'usage: %s\n' "$(basename "$0")" >&2
  exit 2
}

repository=${GITHUB_REPOSITORY:-}
release_require_repository "$repository"
[[ "$repository" == "$RELEASE_SOURCE_REPOSITORY" ]] || release_die "repository differs from the release contract source"
release_require_command gh
release_require_command jq

probe_dir=$(mktemp -d "${TMPDIR:-/tmp}/env-vault-release-labels.XXXXXX")
trap 'rm -rf -- "$probe_dir"' EXIT

ensure_label() {
  local name=$1
  local color=$2
  local description=$3
  local encoded_name
  local record response

  encoded_name=$(jq -rn --arg label "$name" '$label | @uri') || release_die "cannot encode lifecycle label"

  gh label create "$name" \
    --repo "$repository" \
    --color "$color" \
    --description "$description" \
    --force
  response="$probe_dir/$color.json"
  "$SCRIPT_DIR/gh-api-read.sh" "$response" "repos/$repository/labels/$encoded_name"
  record=$(jq -er 'select(type == "object") | [.name, .color, .description] | select(all(.[]; type == "string")) | @tsv' "$response") ||
    release_die "GitHub returned malformed label data: $name"
  [[ "$record" == "$name"$'\t'"$color"$'\t'"$description" ]] ||
    release_die "release lifecycle label verification failed: $name"
}

ensure_label \
  "$RELEASE_PENDING_LABEL" \
  'fbca04' \
  'Release Please proposal awaiting reviewed publication'
ensure_label \
  "$RELEASE_TAGGED_LABEL" \
  '0e8a16' \
  'Reviewed Release Please proposal with an exact release tag'
ensure_label \
  "$RELEASE_ABANDONED_LABEL" \
  'b60205' \
  'Merged Release Please proposal permanently abandoned before tagging'
