#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=scripts/release/lib.sh
source "$SCRIPT_DIR/lib.sh"

usage() {
  printf 'usage: %s VERSION SOURCE_SHA LOCAL_ASSET_DIR ARCHIVE RELEASE_ID OUTPUT OWNER/REPO\n' "$(basename "$0")" >&2
  exit 2
}

cleanup() {
  if [[ -n ${work_dir:-} && -d "$work_dir" ]]; then
    rm -rf -- "$work_dir"
  fi
  if [[ -n ${output_temp:-} && -e "$output_temp" ]]; then
    rm -f -- "$output_temp"
  fi
}

download_remote_members() {
  local destination=$1
  shift
  local name
  local -a patterns=()
  for name in "$@"; do
    patterns+=(--pattern "$name")
  done
  gh release download "$version" --repo "$repository" --dir "$destination" "${patterns[@]}" >&2
}

snapshot_pair_inventory() {
  local label=$1
  local state="$work_dir/${label}-release.json"
  local names="$work_dir/${label}-assets.txt"
  "$SCRIPT_DIR/gh-api-read.sh" "$state" "repos/$repository/releases/tags/$version"
  jq -e --arg version "$version" --argjson release_id "$release_id" '
    type == "object" and
    .id == $release_id and
    .tag_name == $version and
    .draft == false and
    .prerelease == false
  ' "$state" >/dev/null || release_die "GitHub Release identity or publication state changed"
  release_write_asset_names "$state" "$names"
  local remote_name expected count
  while IFS= read -r remote_name; do
    release_is_expected_asset "$remote_name" || release_die "unexpected release asset during bootstrap: $remote_name"
  done <"$names"
  for expected in "${RELEASE_ASSETS[@]}"; do
    count=$(LC_ALL=C grep -Fxc -- "$expected" "$names" || true)
    [[ "$count" == "0" || "$count" == "1" ]] ||
      release_die "duplicate release asset during bootstrap: $expected"
  done
  printf '%s\n' "$names"
}

upload_pair_member_once() {
  local asset=$1
  local before_names=$2
  local mutation_status post_names expected before_count after_count verify_dir

  [[ "$(LC_ALL=C grep -Fxc -- "$asset" "$before_names" || true)" == "0" ]] ||
    release_die "bootstrap asset already exists before mutation: $asset"
  set +e
  gh release upload "$version" "$local_dir/$asset" --repo "$repository" >&2
  mutation_status=$?
  set -e

  post_names=$(snapshot_pair_inventory "post-${asset}")
  for expected in "${RELEASE_ASSETS[@]}"; do
    before_count=$(LC_ALL=C grep -Fxc -- "$expected" "$before_names" || true)
    after_count=$(LC_ALL=C grep -Fxc -- "$expected" "$post_names" || true)
    if [[ "$expected" == "$asset" ]]; then
      [[ "$before_count" == "0" && "$after_count" == "1" ]] ||
        release_die "bootstrap upload has no exact postcondition: $asset"
    else
      [[ "$after_count" == "$before_count" ]] ||
        release_die "release asset inventory changed concurrently during bootstrap: $expected"
    fi
  done

  verify_dir="$work_dir/post-${asset}-bytes"
  mkdir "$verify_dir"
  download_remote_members "$verify_dir" "$asset"
  cmp -s -- "$local_dir/$asset" "$verify_dir/$asset" ||
    release_die "bootstrap upload differs from verified promotion bytes: $asset"
  if [[ "$mutation_status" != "0" ]]; then
    printf 'reconciled ambiguous bootstrap upload from exact remote bytes: %s\n' "$asset" >&2
  fi
  printf '%s\n' "$post_names"
}

[[ $# -eq 7 ]] || usage
version=$1
source_sha=$2
local_dir=$3
archive=$4
release_id=$5
output=$6
repository=$7
checksum="$archive.sha256"

release_require_typed_contract_projection
release_require_version "$version"
release_require_repository "$repository"
[[ "$repository" == "$RELEASE_SOURCE_REPOSITORY" ]] ||
  release_die "repository differs from the typed release contract"
[[ "$source_sha" =~ ^[0-9a-f]{40}$ ]] || release_die "source SHA must be a full 40-character commit SHA"
[[ "$release_id" =~ ^[1-9][0-9]*$ ]] || release_die "release ID must be a positive integer"
[[ -d "$local_dir" && ! -L "$local_dir" ]] || release_die "local asset directory not found: $local_dir"
[[ ! -e "$output" ]] || release_die "refusing to overwrite bootstrap result: $output"
release_require_command gh
release_require_command jq
release_require_command cmp
release_verify_asset_directory "$local_dir"

archive_allowed=false
for candidate in "${RELEASE_ARCHIVES[@]}"; do
  [[ "$candidate" == "$archive" ]] && archive_allowed=true
done
[[ "$archive_allowed" == "true" ]] || release_die "bootstrap archive is not in the release contract: $archive"

work_dir=$(mktemp -d "${TMPDIR:-/tmp}/env-vault-release-bootstrap.XXXXXX")
output_temp=''
trap cleanup EXIT

tag_state="$work_dir/tag-ref.json"
"$SCRIPT_DIR/gh-api-read.sh" "$tag_state" "repos/$repository/git/ref/tags/$version"
jq -e --arg source_sha "$source_sha" '
  type == "object" and
  (.object | type) == "object" and
  .object.type == "commit" and
  .object.sha == $source_sha
' "$tag_state" >/dev/null || release_die "immutable lightweight tag does not resolve to the exact source SHA"

initial_names=$(snapshot_pair_inventory initial)
[[ "$(LC_ALL=C awk 'END { print NR }' "$initial_names")" == "0" ]] ||
  release_die "bootstrap requires an existing Release with exactly zero assets"

archive_names=$(upload_pair_member_once "$archive" "$initial_names")
checksum_names=$(upload_pair_member_once "$checksum" "$archive_names")
[[ "$(LC_ALL=C awk 'END { print NR }' "$checksum_names")" == "2" &&
"$(LC_ALL=C grep -Fxc -- "$archive" "$checksum_names" || true)" == "1" &&
"$(LC_ALL=C grep -Fxc -- "$checksum" "$checksum_names" || true)" == "1" ]] ||
  release_die "bootstrap did not produce exactly the reviewed archive/checksum pair"

verified_dir="$work_dir/final-pair"
mkdir "$verified_dir"
download_remote_members "$verified_dir" "$archive" "$checksum"
cmp -s -- "$local_dir/$archive" "$verified_dir/$archive" || release_die "final bootstrap archive differs"
cmp -s -- "$local_dir/$checksum" "$verified_dir/$checksum" || release_die "final bootstrap checksum differs"
release_verify_checksum_pair "$verified_dir/$archive" "$verified_dir/$checksum"

archive_sha256=$(release_sha256_file "$local_dir/$archive")
checksum_sha256=$(release_sha256_file "$local_dir/$checksum")
output_parent=$(dirname "$output")
[[ -d "$output_parent" && ! -L "$output_parent" ]] || release_die "bootstrap output parent is not a directory"
output_temp=$(mktemp "$output_parent/.env-vault-bootstrap-result.XXXXXX")
chmod 0600 "$output_temp"
jq -n \
  --arg repository "$repository" \
  --arg version "$version" \
  --arg source_sha "$source_sha" \
  --argjson release_id "$release_id" \
  --arg archive "$archive" \
  --arg archive_sha256 "$archive_sha256" \
  --arg checksum "$checksum" \
  --arg checksum_sha256 "$checksum_sha256" '
  {
    schema_id: "env-vault.release-assets-bootstrap-pair.v1",
    schema_version: 1,
    ok: true,
    repository: $repository,
    version: $version,
    source_sha: $source_sha,
    release_id: $release_id,
    assets: [
      {name: $archive, sha256: $archive_sha256},
      {name: $checksum, sha256: $checksum_sha256}
    ]
  }
' >"$output_temp"
mv "$output_temp" "$output"
output_temp=''
trap - EXIT
rm -rf -- "$work_dir"
printf 'bootstrapped and verified exact release asset pair for %s\n' "$version"
