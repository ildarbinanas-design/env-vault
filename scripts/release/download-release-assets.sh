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
release_require_command jq

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

remote_names="$staging/.remote-asset-names"
release_state="$staging/.release-state.json"
"$SCRIPT_DIR/gh-api-read.sh" "$release_state" "repos/$repository/releases/tags/$version"
release_write_asset_names "$release_state" "$remote_names"
remote_count=$(LC_ALL=C awk 'END { print NR }' "$remote_names")
[[ "$remote_count" == "${#RELEASE_ASSETS[@]}" ]] ||
  release_die "release must contain exactly ${#RELEASE_ASSETS[@]} assets"
while IFS= read -r remote_name; do
  release_is_expected_asset "$remote_name" || release_die "unexpected release asset: $remote_name"
done <"$remote_names"
for asset in "${RELEASE_ASSETS[@]}"; do
  count=$(LC_ALL=C grep -Fxc -- "$asset" "$remote_names" || true)
  [[ "$count" == "1" ]] || release_die "release asset is missing or duplicated: $asset"
done
rm -f -- "$remote_names" "$release_state"

download_patterns=()
for asset in "${RELEASE_ASSETS[@]}"; do
  download_patterns+=(--pattern "$asset")
done
gh release download "$version" --repo "$repository" --dir "$staging" "${download_patterns[@]}"

release_verify_asset_directory "$staging"

if [[ -d "$destination" ]]; then
  rmdir "$destination"
fi
mv "$staging" "$destination"
staging=''
trap - EXIT

printf 'verified %s release assets for %s\n' "${#RELEASE_ASSETS[@]}" "$version"
