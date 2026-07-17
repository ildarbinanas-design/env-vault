#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=scripts/release/lib.sh
source "$SCRIPT_DIR/lib.sh"

# Exit status 4 means GitHub explicitly returned HTTP 404 for the release.
usage() {
  printf 'usage: %s [vMAJOR.MINOR.PATCH] [OWNER/REPO]\n' "$(basename "$0")" >&2
  exit 2
}

cleanup() {
  if [[ -n ${probe_dir:-} && -d "$probe_dir" ]]; then
    rm -rf -- "$probe_dir"
  fi
}

[[ $# -le 2 ]] || usage
version=${1:-${VERSION:-}}
repository=${2:-${GITHUB_REPOSITORY:-}}
[[ -n "$repository" && -n "$version" ]] || usage

release_require_repository "$repository"
release_require_version "$version"
release_require_command gh
release_require_command jq

probe_dir=$(mktemp -d "${TMPDIR:-/tmp}/env-vault-release-probe.XXXXXX")
trap cleanup EXIT
response="$probe_dir/release.json"
endpoint="repos/$repository/releases/tags/$version"

set +e
"$SCRIPT_DIR/gh-api-read.sh" "$response" "$endpoint"
status=$?
set -e
case "$status" in
  0) ;;
  4)
    printf 'release: GitHub Release not found: %s\n' "$version" >&2
    exit 4
    ;;
  *) release_die "failed to query GitHub Release" ;;
esac

record=$(jq -er '
  select(type == "object" and (.tag_name | type) == "string" and
    (.draft | type) == "boolean" and (.prerelease | type) == "boolean") |
  [.tag_name, (.draft | tostring), (.prerelease | tostring)] | @tsv
' "$response") ||
  release_die "GitHub returned malformed release data"
tag_name=''
is_draft=''
is_prerelease=''
extra=''
IFS=$'\t' read -r tag_name is_draft is_prerelease extra <<< "$record"
[[ "$tag_name" == "$version" && -z "$extra" ]] || release_die "GitHub returned malformed release data"
[[ "$is_draft" == "true" || "$is_draft" == "false" ]] || release_die "GitHub returned malformed draft state"
[[ "$is_prerelease" == "true" || "$is_prerelease" == "false" ]] || release_die "GitHub returned malformed prerelease state"
printf '%s|%s|%s\n' "$tag_name" "$is_draft" "$is_prerelease"
