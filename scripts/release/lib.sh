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

release_require_typed_contract_projection() {
  [[ ${RELEASE_CONTRACT_PROJECTION_SOURCE:-} == "typed" ]] ||
    release_die "mutation requires a typed operational release projection"
  jq -e '
    .schema_id == "env-vault.release-contract-operational.v2" and
    .schema_version == 2 and
    .contract_schema_id == "env-vault.release-contract.v2" and
    .contract_schema_version == 2 and
    (.contract_semantic_sha256 | type == "string" and test("^[0-9a-f]{64}$")) and
    (.contract_file_sha256 | type == "string" and test("^[0-9a-f]{64}$"))
  ' <<< "$RELEASE_CONTRACT_PROJECTION_JSON" >/dev/null ||
    release_die "typed operational release projection binding is invalid"
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

release_write_asset_names() {
  local release_state=$1
  local output=$2

  release_require_regular_file "$release_state"
  [[ ! -e "$output" ]] || release_die "refusing to overwrite release asset inventory: $output"
  jq -s -e '
    length == 1 and
    (.[0] | type) == "object" and
    (.[0].assets | type) == "array" and
    (.[0].assets | length) <= 100 and
    all(.[0].assets[];
      type == "object" and
      (.name | type) == "string" and
      (.name | test("^[A-Za-z0-9][A-Za-z0-9._-]*$"))
    )
  ' "$release_state" >/dev/null || release_die "GitHub returned malformed release asset data"
  (
    umask 077
    jq -s -r '.[0].assets[].name' "$release_state" >"$output"
  ) || {
    rm -f -- "$output"
    release_die "GitHub returned malformed release asset data"
  }
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
  local archive_name checksum_name line_count byte_count line line_bytes terminator_bytes
  local checksum_re expected_hash referenced_name actual_hash
  local LC_ALL=C

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

  byte_count=$(wc -c <"$checksum_file") ||
    release_die "cannot measure checksum for $archive_name"
  byte_count=${byte_count//[[:space:]]/}
  [[ "$byte_count" =~ ^[1-9][0-9]*$ ]] ||
    release_die "checksum for $archive_name has an invalid byte count"

  line=''
  terminator_bytes=0
  if IFS= read -r line <"$checksum_file"; then
    terminator_bytes=1
    # Bash read removes the LF from either native line ending but retains the
    # terminal CR from CRLF. Normalize exactly that known terminator before
    # applying the same checksum/name grammar.
    if [[ "$line" == *$'\r' ]]; then
      line=${line%$'\r'}
      terminator_bytes=2
    fi
  fi
  line_bytes=${#line}
  [[ "$line_bytes" =~ ^[0-9]+$ && "$byte_count" -eq $((line_bytes + terminator_bytes)) ]] ||
    release_die "checksum for $archive_name contains unsupported raw bytes"

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
  printf '%s  %s\n' "$hash" "$archive_name" >"$checksum_file"
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
    <<< "$RELEASE_CONTRACT_PROJECTION_JSON") || release_die "release contract has no platform: $platform"
  printf '%s\n' "$archive"
}

release_homebrew_archive_for_target() {
  local goos=$1
  local goarch=$2
  jq -er --arg goos "$goos" --arg goarch "$goarch" '
    . as $contract |
    [.platforms[] | select(.goos == $goos and .goarch == $goarch and (.id as $id | $contract.homebrew.platforms | index($id) != null))] |
    select(length == 1) | .[0].archive
  ' <<< "$RELEASE_CONTRACT_PROJECTION_JSON" || release_die "Homebrew target is missing from the release contract: $goos/$goarch"
}

_release_load_contract() {
  local library_dir archives assets projection_source contract_file_sha256
  local local_projection
  library_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
  RELEASE_CONTRACT_PATH="$library_dir/../../release/contract.v2.json"
  [[ -f "$RELEASE_CONTRACT_PATH" && ! -L "$RELEASE_CONTRACT_PATH" ]] ||
    release_die "validated release contract not found"
  release_require_command jq
  contract_file_sha256=$(release_sha256_file "$RELEASE_CONTRACT_PATH")
  if [[ -n ${RELEASE_CONTRACT_PROJECTION_FILE:-} || -n ${RELEASE_CONTRACT_VERSION_FILE:-} ]]; then
    [[ -n ${RELEASE_CONTRACT_PROJECTION_FILE:-} && -n ${RELEASE_CONTRACT_VERSION_FILE:-} ]] ||
      release_die "typed projection and checker identity files must be supplied together"
    release_require_regular_file "$RELEASE_CONTRACT_PROJECTION_FILE"
    release_require_regular_file "$RELEASE_CONTRACT_VERSION_FILE"
    [[ -n ${RELEASE_CONTRACT_CHECKER:-} ]] ||
      release_die "typed operational release projection requires a local release contract checker"
    release_require_regular_file "$RELEASE_CONTRACT_CHECKER"
    [[ -x "$RELEASE_CONTRACT_CHECKER" ]] ||
      release_die "local release contract checker is not executable"
    release_require_command cmp
    RELEASE_CONTRACT_PROJECTION_JSON=$(
      verification_dir=$(mktemp -d "${TMPDIR:-/tmp}/env-vault-contract-check.XXXXXX") || exit 1
      [[ -d "$verification_dir" && ! -L "$verification_dir" ]] || exit 1
      trap 'rm -rf -- "$verification_dir"' EXIT
      "$RELEASE_CONTRACT_CHECKER" validate-contract \
        --contract "$RELEASE_CONTRACT_PATH" --json >/dev/null || {
        printf 'release: local release contract is invalid\n' >&2
        exit 1
      }
      "$RELEASE_CONTRACT_CHECKER" --contract "$RELEASE_CONTRACT_PATH" --version --json \
        > "$verification_dir/version.json" || exit 1
      cmp -s -- "$RELEASE_CONTRACT_VERSION_FILE" "$verification_dir/version.json" || {
        printf 'release: release checker identity differs from the trusted local checker\n' >&2
        exit 1
      }
      "$RELEASE_CONTRACT_CHECKER" contract operational \
        --contract "$RELEASE_CONTRACT_PATH" --json > "$verification_dir/projection.json" || exit 1
      cmp -s -- "$RELEASE_CONTRACT_PROJECTION_FILE" "$verification_dir/projection.json" || {
        printf 'release: typed operational release projection differs from the trusted local contract\n' >&2
        exit 1
      }
      jq -ceS -s 'select(length == 1) | .[0] | select(type == "object")' \
        "$verification_dir/projection.json"
    ) || release_die "trusted local release contract corroboration failed"
    projection_source=typed
  else
    # Standalone/offline helpers may run before a checker is built. Keep their
    # one parser boundary here: a strict projection of canonical v2. CI and
    # mutation workflows supply the typed files above.
    projection_source=jq-fallback
    local_projection=$(jq -ceS -s --arg file_sha256 "$contract_file_sha256" '
      select(length == 1) | .[0] | select(type == "object")
      | select(.schema_id == "env-vault.release-contract.v2" and .schema_version == 2)
      | {
          schema_id:"env-vault.release-contract-operational.v2", schema_version:2,
          contract_schema_id:.schema_id, contract_schema_version:.schema_version,
          contract_semantic_sha256:"", contract_file_sha256:$file_sha256,
          repositories,
          version:{pattern:.version_policy.pattern,tag_prefix:.version_policy.tag_prefix,release_please:.version_policy.release_please},
          naming,platforms,assets,homebrew,workflows,concurrency,apps,main_required_checks
        }
      | select(
          (.repositories.source.full_name | type == "string") and
          (.repositories.source.default_branch | type == "string") and
          (.repositories.homebrew_tap.full_name | type == "string") and
          (.version.pattern | type == "string") and
          (.platforms | type == "array" and length == 5) and
          (.assets | type == "array" and length == 10) and
          (.apps | type == "array" and length == 2) and
          (.workflows | type == "array" and length == 12) and
          (.main_required_checks | type == "array" and length == 5)
        )
    ' "$RELEASE_CONTRACT_PATH") || release_die "strict operational release projection fallback failed"
    RELEASE_CONTRACT_PROJECTION_JSON=$local_projection
  fi
  RELEASE_CONTRACT_PROJECTION_SOURCE=$projection_source
  RELEASE_VERSION_PATTERN=$(jq -er '.version.pattern | select(type == "string" and length > 0)' <<< "$RELEASE_CONTRACT_PROJECTION_JSON") ||
    release_die "release contract version policy is invalid"
  archives=$(jq -er '.platforms | map(.archive) | select(length == 5) | .[]' <<< "$RELEASE_CONTRACT_PROJECTION_JSON") ||
    release_die "release contract platform archives are invalid"
  assets=$(jq -er '.assets | select(length == 10) | .[]' <<< "$RELEASE_CONTRACT_PROJECTION_JSON") ||
    release_die "release contract asset list is invalid"
  RELEASE_SOURCE_REPOSITORY=$(jq -er '.repositories.source.full_name' <<< "$RELEASE_CONTRACT_PROJECTION_JSON") ||
    release_die "source repository identity is invalid"
  RELEASE_SOURCE_DEFAULT_BRANCH=$(jq -er '.repositories.source.default_branch' <<< "$RELEASE_CONTRACT_PROJECTION_JSON") ||
    release_die "source default branch identity is invalid"
  RELEASE_PRODUCT=$(jq -er '.naming.product' <<< "$RELEASE_CONTRACT_PROJECTION_JSON") ||
    release_die "release product identity is invalid"
  RELEASE_TAG_PREFIX=$(jq -er '.version.tag_prefix' <<< "$RELEASE_CONTRACT_PROJECTION_JSON") ||
    release_die "release tag prefix identity is invalid"
  RELEASE_PLEASE_COMPONENT=$(jq -er '.version.release_please.component' <<< "$RELEASE_CONTRACT_PROJECTION_JSON") ||
    release_die "Release Please component identity is invalid"
  RELEASE_PLEASE_TARGET_BRANCH=$(jq -er '.version.release_please.target_branch' <<< "$RELEASE_CONTRACT_PROJECTION_JSON") ||
    release_die "Release Please target branch identity is invalid"
  RELEASE_PLEASE_BRANCH=$(jq -er '.version.release_please.branch' <<< "$RELEASE_CONTRACT_PROJECTION_JSON") ||
    release_die "Release Please branch identity is invalid"
  RELEASE_PLEASE_MANIFEST_KEY=$(jq -er '.version.release_please.manifest_key' <<< "$RELEASE_CONTRACT_PROJECTION_JSON") ||
    release_die "Release Please manifest key identity is invalid"
  RELEASE_PLEASE_CONFIG_PATH=$(jq -er '.version.release_please.config_path' <<< "$RELEASE_CONTRACT_PROJECTION_JSON") ||
    release_die "Release Please config path identity is invalid"
  RELEASE_PLEASE_MANIFEST_PATH=$(jq -er '.version.release_please.manifest_path' <<< "$RELEASE_CONTRACT_PROJECTION_JSON") ||
    release_die "Release Please manifest path identity is invalid"
  RELEASE_PENDING_LABEL=$(jq -er '.version.release_please.pending_label' <<< "$RELEASE_CONTRACT_PROJECTION_JSON") ||
    release_die "Release Please pending label identity is invalid"
  RELEASE_TAGGED_LABEL=$(jq -er '.version.release_please.tagged_label' <<< "$RELEASE_CONTRACT_PROJECTION_JSON") ||
    release_die "Release Please tagged label identity is invalid"
  RELEASE_ABANDONED_LABEL=$(jq -er '.version.release_please.abandoned_label' <<< "$RELEASE_CONTRACT_PROJECTION_JSON") ||
    release_die "Release Please abandoned label identity is invalid"
  RELEASE_CI_WORKFLOW_FILE=$(jq -er '[.workflows[] | select(.id == "ci")] | select(length == 1) | .[0].file' <<< "$RELEASE_CONTRACT_PROJECTION_JSON") ||
    release_die "CI workflow identity is invalid"
  RELEASE_CI_WORKFLOW_NAME=$(jq -er '[.workflows[] | select(.id == "ci")] | select(length == 1) | .[0].name' <<< "$RELEASE_CONTRACT_PROJECTION_JSON") ||
    release_die "CI workflow name is invalid"
  RELEASE_CI_WORKFLOW_PATH=".github/workflows/$RELEASE_CI_WORKFLOW_FILE"
  RELEASE_PLANNING_WORKFLOW_FILE=$(jq -er '[.workflows[] | select(.id == "planning")] | select(length == 1) | .[0].file' <<< "$RELEASE_CONTRACT_PROJECTION_JSON") ||
    release_die "release-planning workflow identity is invalid"
  RELEASE_PLANNING_WORKFLOW_NAME=$(jq -er '[.workflows[] | select(.id == "planning")] | select(length == 1) | .[0].name' <<< "$RELEASE_CONTRACT_PROJECTION_JSON") ||
    release_die "release-planning workflow name is invalid"
  RELEASE_PLANNING_WORKFLOW_PATH=".github/workflows/$RELEASE_PLANNING_WORKFLOW_FILE"
  RELEASE_PUBLISHER_WORKFLOW_FILE=$(jq -er '[.workflows[] | select(.id == "publisher")] | select(length == 1) | .[0].file' <<< "$RELEASE_CONTRACT_PROJECTION_JSON") ||
    release_die "publisher workflow identity is invalid"
  RELEASE_PUBLISHER_WORKFLOW_NAME=$(jq -er '[.workflows[] | select(.id == "publisher")] | select(length == 1) | .[0].name' <<< "$RELEASE_CONTRACT_PROJECTION_JSON") ||
    release_die "publisher workflow name is invalid"
  RELEASE_PUBLISHER_WORKFLOW_PATH=".github/workflows/$RELEASE_PUBLISHER_WORKFLOW_FILE"
  RELEASE_ASSETS_BOOTSTRAP_WORKFLOW_FILE=$(jq -er '[.workflows[] | select(.id == "release_assets_bootstrap")] | select(length == 1) | .[0].file' <<< "$RELEASE_CONTRACT_PROJECTION_JSON") ||
    release_die "release-assets bootstrap workflow identity is invalid"
  RELEASE_ASSETS_BOOTSTRAP_WORKFLOW_NAME=$(jq -er '[.workflows[] | select(.id == "release_assets_bootstrap")] | select(length == 1) | .[0].name' <<< "$RELEASE_CONTRACT_PROJECTION_JSON") ||
    release_die "release-assets bootstrap workflow name is invalid"
  RELEASE_ASSETS_BOOTSTRAP_WORKFLOW_PATH=".github/workflows/$RELEASE_ASSETS_BOOTSTRAP_WORKFLOW_FILE"
  RELEASE_HOMEBREW_BRIDGE_WORKFLOW_FILE=$(jq -er '[.workflows[] | select(.id == "homebrew_bridge")] | select(length == 1) | .[0].file' <<< "$RELEASE_CONTRACT_PROJECTION_JSON") ||
    release_die "Homebrew bridge workflow identity is invalid"
  RELEASE_HOMEBREW_BRIDGE_WORKFLOW_NAME=$(jq -er '[.workflows[] | select(.id == "homebrew_bridge")] | select(length == 1) | .[0].name' <<< "$RELEASE_CONTRACT_PROJECTION_JSON") ||
    release_die "Homebrew bridge workflow name is invalid"
  RELEASE_HOMEBREW_BRIDGE_WORKFLOW_PATH=".github/workflows/$RELEASE_HOMEBREW_BRIDGE_WORKFLOW_FILE"
  RELEASE_EVIDENCE_WORKFLOW_FILE=$(jq -er '[.workflows[] | select(.id == "release_evidence")] | select(length == 1) | .[0].file' <<< "$RELEASE_CONTRACT_PROJECTION_JSON") ||
    release_die "release-evidence workflow identity is invalid"
  RELEASE_EVIDENCE_WORKFLOW_NAME=$(jq -er '[.workflows[] | select(.id == "release_evidence")] | select(length == 1) | .[0].name' <<< "$RELEASE_CONTRACT_PROJECTION_JSON") ||
    release_die "release-evidence workflow name is invalid"
  RELEASE_EVIDENCE_WORKFLOW_PATH=".github/workflows/$RELEASE_EVIDENCE_WORKFLOW_FILE"
  RELEASE_PR_TITLE_PREFIX="chore($RELEASE_PLEASE_TARGET_BRANCH): release $RELEASE_PRODUCT "
  RELEASE_PR_HEADER="Merging this unchanged reviewed pull request after the required exact tuple confirmation authorizes publication once its merge commit passes $RELEASE_SOURCE_DEFAULT_BRANCH CI."
  RELEASE_HOMEBREW_TAP_REPOSITORY=$(jq -er '.repositories.homebrew_tap.full_name' <<< "$RELEASE_CONTRACT_PROJECTION_JSON") ||
    release_die "Homebrew tap repository identity is invalid"
  RELEASE_HOMEBREW_TAP_DEFAULT_BRANCH=$(jq -er '.repositories.homebrew_tap.default_branch' <<< "$RELEASE_CONTRACT_PROJECTION_JSON") ||
    release_die "Homebrew tap default branch identity is invalid"
  RELEASE_HOMEBREW_FORMULA_NAME=$(jq -er '.homebrew.formula_name' <<< "$RELEASE_CONTRACT_PROJECTION_JSON") ||
    release_die "Homebrew formula name is invalid"
  RELEASE_HOMEBREW_FORMULA_PATH=$(jq -er '.homebrew.formula_path' <<< "$RELEASE_CONTRACT_PROJECTION_JSON") ||
    release_die "Homebrew formula path is invalid"
  RELEASE_HOMEBREW_HOMEPAGE_TEMPLATE=$(jq -er '.homebrew.homepage_url_template' <<< "$RELEASE_CONTRACT_PROJECTION_JSON") ||
    release_die "Homebrew homepage template is invalid"
  RELEASE_HOMEBREW_DOWNLOAD_TEMPLATE=$(jq -er '.homebrew.release_download_url_template' <<< "$RELEASE_CONTRACT_PROJECTION_JSON") ||
    release_die "Homebrew download URL template is invalid"
  RELEASE_PLANNING_APP_SLUG=$(jq -er '[.apps[] | select(.id == "release_planning")] | select(length == 1) | .[0].slug' <<< "$RELEASE_CONTRACT_PROJECTION_JSON") ||
    release_die "release-planning App identity is invalid"
  RELEASE_HOMEBREW_APP=$(jq -cer '[.apps[] | select(.id == "homebrew_tap")] | select(length == 1) | .[0]' <<< "$RELEASE_CONTRACT_PROJECTION_JSON") ||
    release_die "Homebrew App identity is invalid"
  RELEASE_ARCHIVES=()
  while IFS= read -r archive; do
    RELEASE_ARCHIVES+=("$archive")
  done <<<"$archives"
  RELEASE_ASSETS=()
  while IFS= read -r asset; do
    RELEASE_ASSETS+=("$asset")
  done <<<"$assets"
  [[ ${#RELEASE_ARCHIVES[@]} -eq 5 && ${#RELEASE_ASSETS[@]} -eq 10 ]] ||
    release_die "release contract matrix is incomplete"
  readonly RELEASE_CONTRACT_PATH RELEASE_CONTRACT_PROJECTION_JSON RELEASE_CONTRACT_PROJECTION_SOURCE RELEASE_VERSION_PATTERN
  readonly RELEASE_SOURCE_REPOSITORY RELEASE_SOURCE_DEFAULT_BRANCH RELEASE_HOMEBREW_TAP_REPOSITORY
  readonly RELEASE_HOMEBREW_TAP_DEFAULT_BRANCH
  readonly RELEASE_HOMEBREW_FORMULA_NAME RELEASE_HOMEBREW_FORMULA_PATH
  readonly RELEASE_HOMEBREW_HOMEPAGE_TEMPLATE RELEASE_HOMEBREW_DOWNLOAD_TEMPLATE
  readonly RELEASE_PRODUCT RELEASE_TAG_PREFIX RELEASE_PLEASE_COMPONENT RELEASE_PLEASE_TARGET_BRANCH
  readonly RELEASE_PLEASE_BRANCH RELEASE_PLEASE_MANIFEST_KEY RELEASE_PLEASE_CONFIG_PATH RELEASE_PLEASE_MANIFEST_PATH
  readonly RELEASE_PENDING_LABEL RELEASE_TAGGED_LABEL RELEASE_ABANDONED_LABEL
  readonly RELEASE_CI_WORKFLOW_FILE RELEASE_CI_WORKFLOW_NAME RELEASE_CI_WORKFLOW_PATH
  readonly RELEASE_PLANNING_WORKFLOW_FILE RELEASE_PLANNING_WORKFLOW_NAME RELEASE_PLANNING_WORKFLOW_PATH
  readonly RELEASE_PUBLISHER_WORKFLOW_FILE RELEASE_PUBLISHER_WORKFLOW_NAME RELEASE_PUBLISHER_WORKFLOW_PATH
  readonly RELEASE_ASSETS_BOOTSTRAP_WORKFLOW_FILE RELEASE_ASSETS_BOOTSTRAP_WORKFLOW_NAME RELEASE_ASSETS_BOOTSTRAP_WORKFLOW_PATH
  readonly RELEASE_HOMEBREW_BRIDGE_WORKFLOW_FILE RELEASE_HOMEBREW_BRIDGE_WORKFLOW_NAME RELEASE_HOMEBREW_BRIDGE_WORKFLOW_PATH
  readonly RELEASE_EVIDENCE_WORKFLOW_FILE RELEASE_EVIDENCE_WORKFLOW_NAME RELEASE_EVIDENCE_WORKFLOW_PATH
  readonly RELEASE_PR_TITLE_PREFIX RELEASE_PR_HEADER
  readonly RELEASE_PLANNING_APP_SLUG RELEASE_HOMEBREW_APP
  readonly -a RELEASE_ARCHIVES RELEASE_ASSETS
}

_release_load_contract
unset -f _release_load_contract
