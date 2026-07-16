#!/usr/bin/env bash
# Shared release helpers. This file is intended to be sourced by the other
# scripts in this directory.

release_die() {
  printf 'release: %s\n' "$*" >&2
  exit 1
}

release_require_command() {
  command -v "$1" >/dev/null 2>&1 || release_die "required command not found: $1"
}

release_require_version() {
  local version=$1
  [[ "$version" =~ $RELEASE_VERSION_PATTERN ]] ||
    release_die "version must match vMAJOR.MINOR.PATCH"
}

release_readme_version_line() {
  local version=$1
  release_require_version "$version"
  printf "Current version: \`%s\`. <!-- x-release-please-version -->\n" "$version"
}

release_require_repository() {
  local repository=$1
  [[ "$repository" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]] ||
    release_die "repository must have the form OWNER/REPO"
}

release_require_regular_file() {
  local path=$1
  [[ -f "$path" && ! -L "$path" ]] || release_die "expected a regular file: $path"
}

release_archive_names() {
  printf '%s\n' "${RELEASE_ARCHIVES[@]}"
}

release_asset_names() {
  printf '%s\n' "${RELEASE_ASSETS[@]}"
}

release_is_expected_asset() {
  local candidate=$1
  local expected
  for expected in "${RELEASE_ASSETS[@]}"; do
    [[ "$candidate" == "$expected" ]] && return 0
  done
  return 1
}

release_sha256_file() {
  local path=$1
  local output hash
  release_require_regular_file "$path"

  if command -v sha256sum >/dev/null 2>&1; then
    output=$(sha256sum -- "$path") || release_die "sha256sum failed for $(basename "$path")"
  elif command -v shasum >/dev/null 2>&1; then
    output=$(shasum -a 256 -- "$path") || release_die "shasum failed for $(basename "$path")"
  else
    release_die "neither sha256sum nor shasum is available"
  fi

  hash=${output%%[[:space:]]*}
  [[ "$hash" =~ ^[0-9a-f]{64}$ ]] || release_die "checksum tool returned malformed output"
  printf '%s\n' "$hash"
}

release_verify_checksum_pair() {
  local archive=$1
  local checksum_file=$2
  local archive_name checksum_name line_count line checksum_re expected_hash referenced_name actual_hash

  release_require_regular_file "$archive"
  release_require_regular_file "$checksum_file"
  archive_name=$(basename "$archive")
  checksum_name=$(basename "$checksum_file")
  [[ "$checksum_name" == "${archive_name}.sha256" ]] ||
    release_die "checksum filename does not match archive: $checksum_name"

  line_count=$(LC_ALL=C awk 'END { print NR }' "$checksum_file") ||
    release_die "cannot read checksum for $archive_name"
  [[ "$line_count" == "1" ]] ||
    release_die "checksum for $archive_name must contain exactly one record"

  line=''
  IFS= read -r line < "$checksum_file" || true
  checksum_re='^([0-9a-f]{64}) ([ *])([^[:space:]]+)$'
  [[ "$line" =~ $checksum_re ]] ||
    release_die "checksum for $archive_name has an invalid format"
  expected_hash=${BASH_REMATCH[1]}
  referenced_name=${BASH_REMATCH[3]}
  [[ "$referenced_name" == "$archive_name" ]] ||
    release_die "checksum record names a different archive: $archive_name"

  actual_hash=$(release_sha256_file "$archive")
  [[ "$actual_hash" == "$expected_hash" ]] ||
    release_die "checksum mismatch for $archive_name"
}

release_write_checksum_pair() {
  local archive=$1
  local checksum_file=$2
  local archive_name hash

  release_require_regular_file "$archive"
  archive_name=$(basename "$archive")
  [[ "$(basename "$checksum_file")" == "${archive_name}.sha256" ]] ||
    release_die "checksum filename does not match archive: $(basename "$checksum_file")"
  [[ ! -e "$checksum_file" ]] || release_die "refusing to overwrite checksum file: $checksum_file"

  hash=$(release_sha256_file "$archive")
  printf '%s  %s\n' "$hash" "$archive_name" > "$checksum_file"
  release_verify_checksum_pair "$archive" "$checksum_file"
}

release_verify_asset_directory() {
  local directory=$1
  local archive entry name
  local -a entries

  [[ -d "$directory" && ! -L "$directory" ]] ||
    release_die "expected an asset directory: $directory"

  (
    shopt -s dotglob nullglob
    entries=("$directory"/*)
    [[ ${#entries[@]} -eq ${#RELEASE_ASSETS[@]} ]] ||
      release_die "asset directory must contain exactly ${#RELEASE_ASSETS[@]} files"
    for entry in "${entries[@]}"; do
      [[ -f "$entry" && ! -L "$entry" ]] ||
        release_die "asset directory contains a non-regular entry"
      name=$(basename "$entry")
      release_is_expected_asset "$name" ||
        release_die "unexpected release asset: $name"
    done
  )

  for archive in "${RELEASE_ARCHIVES[@]}"; do
    release_verify_checksum_pair "$directory/$archive" "$directory/$archive.sha256"
  done
}

release_archive_for_platform() {
  local platform=$1
  local archive
  archive=$(jq -er --arg platform "$platform" \
    '[.platforms[] | select(.id == $platform)] | select(length == 1) | .[0].archive' \
    "$RELEASE_CONTRACT_PATH") || release_die "release contract has no platform: $platform"
  printf '%s\n' "$archive"
}

_release_load_contract() {
  local library_dir archives assets
  library_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
  RELEASE_CONTRACT_PATH="$library_dir/../../release/contract.v1.json"
  [[ -f "$RELEASE_CONTRACT_PATH" && ! -L "$RELEASE_CONTRACT_PATH" ]] ||
    release_die "validated release contract not found"
  release_require_command jq
  RELEASE_VERSION_PATTERN=$(jq -er '.version_policy.pattern | select(type == "string" and length > 0)' "$RELEASE_CONTRACT_PATH") ||
    release_die "release contract version policy is invalid"
  archives=$(jq -er '.platforms | map(.archive) | select(length == 5) | .[]' "$RELEASE_CONTRACT_PATH") ||
    release_die "release contract platform archives are invalid"
  assets=$(jq -er '.assets | select(length == 10) | .[]' "$RELEASE_CONTRACT_PATH") ||
    release_die "release contract asset list is invalid"
  RELEASE_ARCHIVES=()
  while IFS= read -r archive; do
    RELEASE_ARCHIVES+=("$archive")
  done <<< "$archives"
  RELEASE_ASSETS=()
  while IFS= read -r asset; do
    RELEASE_ASSETS+=("$asset")
  done <<< "$assets"
  [[ ${#RELEASE_ARCHIVES[@]} -eq 5 && ${#RELEASE_ASSETS[@]} -eq 10 ]] ||
    release_die "release contract matrix is incomplete"
  readonly RELEASE_CONTRACT_PATH RELEASE_VERSION_PATTERN
  readonly -a RELEASE_ARCHIVES RELEASE_ASSETS
}

_release_load_contract
unset -f _release_load_contract
