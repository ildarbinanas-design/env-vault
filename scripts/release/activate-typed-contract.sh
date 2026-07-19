#!/usr/bin/env bash
set -euo pipefail
set +x
export LC_ALL=C

usage() {
  printf 'usage: %s TYPED_CONTRACT_DIRECTORY GITHUB_ENV_FILE\n' "$(basename "$0")" >&2
  exit 2
}

[[ $# -eq 2 ]] || usage
typed_directory=$1
github_env_file=$2

[[ -d "$typed_directory" && ! -L "$typed_directory" ]] || {
  printf 'release: typed contract directory is not a regular directory\n' >&2
  exit 1
}
[[ -f "$github_env_file" && ! -L "$github_env_file" ]] || {
  printf 'release: GitHub environment file is not a regular file\n' >&2
  exit 1
}
[[ "$typed_directory" != *$'\n'* && "$typed_directory" != *$'\r'* &&
   "$github_env_file" != *$'\n'* && "$github_env_file" != *$'\r'* ]] || {
  printf 'release: typed contract paths contain control characters\n' >&2
  exit 1
}

export RELEASE_CONTRACT_VERSION_FILE="$typed_directory/releasecheck-version.json"
export RELEASE_CONTRACT_PROJECTION_FILE="$typed_directory/release-contract-operational.json"

SCRIPT_DIR=$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
REPOSITORY_ROOT=$(CDPATH='' cd -- "$SCRIPT_DIR/../.." && pwd)
export RELEASE_CONTRACT_CHECKER="$typed_directory/release-contract-checker"
[[ ! -e "$RELEASE_CONTRACT_CHECKER" && ! -L "$RELEASE_CONTRACT_CHECKER" ]] || {
  printf 'release: local release contract checker path already exists\n' >&2
  exit 1
}
command -v go >/dev/null 2>&1 || {
  printf 'release: required command not found: go\n' >&2
  exit 1
}
(cd "$REPOSITORY_ROOT" && go build -trimpath -o "$RELEASE_CONTRACT_CHECKER" ./cmd/releasecheck)
[[ -f "$RELEASE_CONTRACT_CHECKER" && ! -L "$RELEASE_CONTRACT_CHECKER" && -x "$RELEASE_CONTRACT_CHECKER" ]] || {
  printf 'release: local release contract checker build is invalid\n' >&2
  exit 1
}
# shellcheck source=scripts/release/lib.sh
source "$SCRIPT_DIR/lib.sh"
release_require_typed_contract_projection

{
  printf 'RELEASE_CONTRACT_CHECKER=%s\n' "$RELEASE_CONTRACT_CHECKER"
  printf 'RELEASE_CONTRACT_VERSION_FILE=%s\n' "$RELEASE_CONTRACT_VERSION_FILE"
  printf 'RELEASE_CONTRACT_PROJECTION_FILE=%s\n' "$RELEASE_CONTRACT_PROJECTION_FILE"
} >> "$github_env_file"
