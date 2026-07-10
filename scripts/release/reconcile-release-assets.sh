#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=scripts/release/lib.sh
source "$SCRIPT_DIR/lib.sh"

usage() {
  printf 'usage: %s [vMAJOR.MINOR.PATCH] [LOCAL_ASSET_DIR] [VERIFIED_DOWNLOAD_DIR] [OWNER/REPO]\n' "$(basename "$0")" >&2
  exit 2
}

cleanup() {
  if [[ -n ${work_dir:-} && -d "$work_dir" ]]; then
    rm -rf -- "$work_dir"
  fi
  if [[ ${remove_verified_dir:-false} == true && -n ${verified_dir:-} && -d "$verified_dir" ]]; then
    rm -rf -- "$verified_dir"
  fi
}

remote_count() {
  local name=$1
  LC_ALL=C grep -Fxc -- "$name" "$remote_names" || true
}

download_remote_pair_members() {
  local destination=$1
  shift
  local name
  local -a args
  args=(release download "$version" --repo "$repository" --dir "$destination")
  for name in "$@"; do
    args+=(--pattern "$name")
  done
  gh "${args[@]}"
}

[[ $# -le 4 ]] || usage
version=${1:-${VERSION:-}}
local_dir=${2:-${RELEASE_ASSET_DIR:-dist}}
verified_dir=${3:-${RELEASE_VERIFIED_DIR:-}}
repository=${4:-${GITHUB_REPOSITORY:-}}
[[ -n "$repository" && -n "$version" && -n "$local_dir" ]] || usage

release_require_repository "$repository"
release_require_version "$version"
release_require_command gh
[[ -d "$local_dir" && ! -L "$local_dir" ]] ||
  release_die "local asset directory not found: $local_dir"

work_dir=$(mktemp -d "${TMPDIR:-/tmp}/env-vault-release-reconcile.XXXXXX")
remove_verified_dir=false
trap cleanup EXIT
remote_names="$work_dir/remote-assets.txt"
gh api "repos/$repository/releases/tags/$version" --jq '.assets[].name' > "$remote_names"

for asset in "${RELEASE_ASSETS[@]}"; do
  count=$(remote_count "$asset")
  [[ "$count" == "0" || "$count" == "1" ]] ||
    release_die "release contains duplicate asset names: $asset"
done

for archive in "${RELEASE_ARCHIVES[@]}"; do
  checksum="$archive.sha256"
  archive_count=$(remote_count "$archive")
  checksum_count=$(remote_count "$checksum")
  pair_dir="$work_dir/$archive"
  mkdir "$pair_dir"

  if [[ "$archive_count" == "1" && "$checksum_count" == "1" ]]; then
    download_remote_pair_members "$pair_dir" "$archive" "$checksum"
    release_verify_checksum_pair "$pair_dir/$archive" "$pair_dir/$checksum"
  elif [[ "$archive_count" == "1" ]]; then
    download_remote_pair_members "$pair_dir" "$archive"
    release_write_checksum_pair "$pair_dir/$archive" "$pair_dir/$checksum"
    gh release upload "$version" "$pair_dir/$checksum" --repo "$repository"
  elif [[ "$checksum_count" == "1" ]]; then
    download_remote_pair_members "$pair_dir" "$checksum"
    release_verify_checksum_pair "$local_dir/$archive" "$pair_dir/$checksum"
    gh release upload "$version" "$local_dir/$archive" --repo "$repository"
  else
    release_verify_checksum_pair "$local_dir/$archive" "$local_dir/$checksum"
    gh release upload "$version" "$local_dir/$archive" "$local_dir/$checksum" --repo "$repository"
  fi
done

if [[ -z "$verified_dir" ]]; then
  verified_dir=$(mktemp -d "${TMPDIR:-/tmp}/env-vault-release-verified.XXXXXX")
  rmdir "$verified_dir"
  remove_verified_dir=true
fi

"$SCRIPT_DIR/download-release-assets.sh" "$version" "$verified_dir" "$repository"
printf 'release assets reconciled and verified for %s\n' "$version"
