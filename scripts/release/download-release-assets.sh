#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=scripts/release/lib.sh
source "$SCRIPT_DIR/lib.sh"

usage() {
  printf 'usage: %s [vMAJOR.MINOR.PATCH] [DESTINATION] [OWNER/REPO]\n' "$(basename "$0")" >&2
  exit 2
}

cleanup() {
  if [[ -n ${staging:-} && -d "$staging" ]]; then
    rm -rf -- "$staging"
  fi
}

[[ $# -le 3 ]] || usage
version=${1:-${VERSION:-}}
destination=${2:-${RELEASE_ASSET_DIR:-}}
repository=${3:-${GITHUB_REPOSITORY:-}}
[[ -n "$repository" && -n "$version" && -n "$destination" ]] || usage

release_require_repository "$repository"
release_require_version "$version"
release_require_command gh

if [[ -e "$destination" ]]; then
  [[ -d "$destination" && ! -L "$destination" ]] ||
    release_die "destination exists and is not a regular directory: $destination"
  (
    shopt -s dotglob nullglob
    entries=("$destination"/*)
    [[ ${#entries[@]} -eq 0 ]] || release_die "destination must be empty: $destination"
  )
fi

parent=$(dirname "$destination")
mkdir -p "$parent"
staging=$(mktemp -d "$parent/.env-vault-release-download.XXXXXX")
trap cleanup EXIT

download_args=(release download "$version" --repo "$repository" --dir "$staging")
for asset in "${RELEASE_ASSETS[@]}"; do
  download_args+=(--pattern "$asset")
done
gh "${download_args[@]}"

release_verify_asset_directory "$staging"

if [[ -d "$destination" ]]; then
  rmdir "$destination"
fi
mv "$staging" "$destination"
staging=''
trap - EXIT

printf 'verified %s release assets for %s\n' "${#RELEASE_ASSETS[@]}" "$version"
