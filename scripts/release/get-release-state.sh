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

probe_dir=$(mktemp -d "${TMPDIR:-/tmp}/env-vault-release-probe.XXXXXX")
trap cleanup EXIT
probe_headers="$probe_dir/response"
probe_error="$probe_dir/error"
endpoint="repos/$repository/releases/tags/$version"

if ! gh api --include "$endpoint" >"$probe_headers" 2>"$probe_error"; then
  http_status=$(LC_ALL=C awk '
    /^HTTP\/[0-9.]+ [0-9][0-9][0-9]( |$)/ { status = $2 }
    match($0, /\(HTTP [0-9][0-9][0-9]\)/) { status = substr($0, RSTART + 6, 3) }
    END { print status }
  ' "$probe_headers" "$probe_error")
  if [[ "$http_status" == "404" ]]; then
    printf 'release: GitHub Release not found: %s\n' "$version" >&2
    exit 4
  fi
  if [[ -n "$http_status" ]]; then
    release_die "failed to query GitHub Release (HTTP $http_status)"
  fi
  release_die "failed to query GitHub Release (no HTTP response)"
fi

record=$(gh api "$endpoint" --jq '[.tag_name, (.draft | tostring), (.prerelease | tostring)] | @tsv')
tag_name=''
is_draft=''
is_prerelease=''
extra=''
IFS=$'\t' read -r tag_name is_draft is_prerelease extra <<< "$record"
[[ "$tag_name" == "$version" && -z "$extra" ]] || release_die "GitHub returned malformed release data"
[[ "$is_draft" == "true" || "$is_draft" == "false" ]] || release_die "GitHub returned malformed draft state"
[[ "$is_prerelease" == "true" || "$is_prerelease" == "false" ]] || release_die "GitHub returned malformed prerelease state"
printf '%s|%s|%s\n' "$tag_name" "$is_draft" "$is_prerelease"
