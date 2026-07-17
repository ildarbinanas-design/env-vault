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
release_require_command jq

probe_dir=$(mktemp -d "${TMPDIR:-/tmp}/env-vault-tag-probe.XXXXXX")
trap cleanup EXIT
response="$probe_dir/tag-ref.json"

set +e
"$SCRIPT_DIR/gh-api-read.sh" "$response" "repos/$repository/git/ref/tags/$version"
status=$?
set -e
case "$status" in
  0) ;;
  4)
    printf 'release: tag ref not found: %s\n' "$version" >&2
    exit 4
    ;;
  *) release_die "failed to query tag ref" ;;
esac

record=$(jq -er '
  select(type == "object" and (.object | type) == "object" and
    (.object.type | type) == "string" and (.object.sha | type) == "string") |
  [.object.type, .object.sha] | @tsv
' "$response") ||
  release_die "GitHub returned malformed tag data"
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
      response="$probe_dir/tag-object-$depth.json"
      if ! "$SCRIPT_DIR/gh-api-read.sh" "$response" "repos/$repository/git/tags/$object_sha"; then
        release_die "failed to query annotated tag object"
      fi
      record=$(jq -er '
        select(type == "object" and (.object | type) == "object" and
          (.object.type | type) == "string" and (.object.sha | type) == "string") |
        [.object.type, .object.sha] | @tsv
      ' "$response") ||
        release_die "GitHub returned malformed annotated tag data"
      ;;
    *)
      release_die "tag does not resolve to a commit"
      ;;
  esac
done
