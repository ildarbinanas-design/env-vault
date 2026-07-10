#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=scripts/release/lib.sh
source "$SCRIPT_DIR/lib.sh"

# Exit status 4 means that GitHub explicitly returned HTTP 404 for the tag ref.
# Every other API, authentication, transport, and parsing failure returns 1.
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

probe_dir=$(mktemp -d "${TMPDIR:-/tmp}/env-vault-tag-probe.XXXXXX")
trap cleanup EXIT
probe_headers="$probe_dir/response"
probe_error="$probe_dir/error"

if ! gh api --include "repos/$repository/git/ref/tags/$version" >"$probe_headers" 2>"$probe_error"; then
  http_status=$(LC_ALL=C awk '
    /^HTTP\/[0-9.]+ [0-9][0-9][0-9]( |$)/ { status = $2 }
    match($0, /\(HTTP [0-9][0-9][0-9]\)/) { status = substr($0, RSTART + 6, 3) }
    END { print status }
  ' "$probe_headers" "$probe_error")
  if [[ "$http_status" == "404" ]]; then
    printf 'release: tag ref not found: %s\n' "$version" >&2
    exit 4
  fi
  if [[ -n "$http_status" ]]; then
    release_die "failed to query tag ref (HTTP $http_status)"
  fi
  release_die "failed to query tag ref (no HTTP response)"
fi

record=$(gh api "repos/$repository/git/ref/tags/$version" --jq '[.object.type, .object.sha] | @tsv')
depth=0

while :; do
  object_type=''
  object_sha=''
  extra=''
  IFS=$'\t' read -r object_type object_sha extra <<< "$record"
  [[ -n "$object_type" && -n "$object_sha" && -z "$extra" ]] ||
    release_die "GitHub returned malformed tag data"
  [[ "$object_sha" =~ ^([0-9a-f]{40}|[0-9a-f]{64})$ ]] ||
    release_die "GitHub returned a malformed object SHA"

  case "$object_type" in
    commit)
      printf '%s\n' "$object_sha"
      exit 0
      ;;
    tag)
      depth=$((depth + 1))
      [[ $depth -le 16 ]] || release_die "annotated tag chain is too deep"
      record=$(gh api "repos/$repository/git/tags/$object_sha" --jq '[.object.type, .object.sha] | @tsv')
      ;;
    *)
      release_die "tag does not resolve to a commit"
      ;;
  esac
done
