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
release_require_command cmp
[[ -d "$local_dir" && ! -L "$local_dir" ]] ||
  release_die "local asset directory not found: $local_dir"

work_dir=$(mktemp -d "${TMPDIR:-/tmp}/env-vault-release-reconcile.XXXXXX")
remove_verified_dir=false
trap cleanup EXIT
remote_names="$work_dir/remote-assets.txt"
gh api "repos/$repository/releases/tags/$version" --jq '.assets[].name' > "$remote_names"

while IFS= read -r remote_name; do
  release_is_expected_asset "$remote_name" || release_die "unexpected release asset: $remote_name"
done < "$remote_names"

for asset in "${RELEASE_ASSETS[@]}"; do
  count=$(remote_count "$asset")
  [[ "$count" == "0" || "$count" == "1" ]] ||
    release_die "release contains duplicate asset names: $asset"
done

# Promotion is the sole byte source. Validate all local pairs and every
# existing remote member before performing the first upload so a mismatch in a
# later platform cannot leave a partially mutated release.
remote_dir="$work_dir/remote"
mkdir "$remote_dir"
for archive in "${RELEASE_ARCHIVES[@]}"; do
  checksum="$archive.sha256"
	[[ -f "$local_dir/$archive" && ! -L "$local_dir/$archive" ]] ||
		release_die "local release archive is missing or unsafe: $archive"
	[[ -f "$local_dir/$checksum" && ! -L "$local_dir/$checksum" ]] ||
		release_die "local release checksum is missing or unsafe: $checksum"
	release_verify_checksum_pair "$local_dir/$archive" "$local_dir/$checksum"

  archive_count=$(remote_count "$archive")
  checksum_count=$(remote_count "$checksum")
	if [[ "$archive_count" == "1" ]]; then
		download_remote_pair_members "$remote_dir" "$archive"
		cmp -s -- "$local_dir/$archive" "$remote_dir/$archive" ||
			release_die "existing release archive differs from verified promotion: $archive"
	fi
	if [[ "$checksum_count" == "1" ]]; then
		download_remote_pair_members "$remote_dir" "$checksum"
		cmp -s -- "$local_dir/$checksum" "$remote_dir/$checksum" ||
			release_die "existing release checksum differs from verified promotion: $checksum"
	fi
	if [[ "$archive_count" == "1" && "$checksum_count" == "1" ]]; then
		release_verify_checksum_pair "$remote_dir/$archive" "$remote_dir/$checksum"
  fi
done

for archive in "${RELEASE_ARCHIVES[@]}"; do
	checksum="$archive.sha256"
	archive_count=$(remote_count "$archive")
	checksum_count=$(remote_count "$checksum")
	if [[ "$archive_count" == "0" ]]; then
		gh release upload "$version" "$local_dir/$archive" --repo "$repository"
	fi
	if [[ "$checksum_count" == "0" ]]; then
		gh release upload "$version" "$local_dir/$checksum" --repo "$repository"
	fi
done

if [[ -z "$verified_dir" ]]; then
  verified_dir=$(mktemp -d "${TMPDIR:-/tmp}/env-vault-release-verified.XXXXXX")
  rmdir "$verified_dir"
  remove_verified_dir=true
fi

"$SCRIPT_DIR/download-release-assets.sh" "$version" "$verified_dir" "$repository"
for asset in "${RELEASE_ASSETS[@]}"; do
	cmp -s -- "$local_dir/$asset" "$verified_dir/$asset" ||
		release_die "published release asset differs from verified promotion: $asset"
done
printf 'release assets reconciled and verified for %s\n' "$version"
