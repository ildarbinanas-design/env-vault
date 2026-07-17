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

ensure_label() {
  local name=$1
  local encoded_name=$2
  local color=$3
  local description=$4
  local record

  gh label create "$name" \
    --repo "$repository" \
    --color "$color" \
    --description "$description" \
    --force
  record=$(gh api "repos/$repository/labels/$encoded_name" --jq '[.name, .color, .description] | @tsv')
  [[ "$record" == "$name"$'\t'"$color"$'\t'"$description" ]] ||
    release_die "release lifecycle label verification failed: $name"
}

ensure_label \
  'autorelease: pending' \
  'autorelease%3A%20pending' \
  'fbca04' \
  'Release Please proposal awaiting reviewed publication'
ensure_label \
  'autorelease: tagged' \
  'autorelease%3A%20tagged' \
  '0e8a16' \
  'Reviewed Release Please proposal with an exact release tag'
ensure_label \
  'autorelease: abandoned' \
  'autorelease%3A%20abandoned' \
  'b60205' \
  'Merged Release Please proposal permanently abandoned before tagging'
