#!/usr/bin/env bash
set -euo pipefail
set +x
export LC_ALL=C

usage() {
  printf 'usage: %s COMMAND [ARG ...]\n' "$(basename "$0")" >&2
  exit 2
}

[[ $# -gt 0 ]] || usage

SCRIPT_DIR=$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
REPOSITORY_ROOT=$(CDPATH='' cd -- "$SCRIPT_DIR/../.." && pwd)
release_contract_tmp=$(mktemp -d "${TMPDIR:-/tmp}/env-vault-release-contract.XXXXXX")
cleanup() {
  rm -rf -- "$release_contract_tmp"
}
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

releasecheck="$release_contract_tmp/releasecheck"
version_file="$release_contract_tmp/releasecheck-version.json"
projection_file="$release_contract_tmp/release-contract-operational.json"

command -v go >/dev/null 2>&1 || {
  printf 'release: required command not found: go\n' >&2
  exit 1
}
(cd "$REPOSITORY_ROOT" && go build -trimpath -o "$releasecheck" ./cmd/releasecheck)
"$releasecheck" --contract "$REPOSITORY_ROOT/release/contract.v2.json" --version --json > "$version_file"
"$releasecheck" contract operational \
  --contract "$REPOSITORY_ROOT/release/contract.v2.json" --json > "$projection_file"

export RELEASECHECK=$releasecheck
export RELEASE_CONTRACT_CHECKER=$releasecheck
export RELEASE_CONTRACT_VERSION_FILE=$version_file
export RELEASE_CONTRACT_PROJECTION_FILE=$projection_file
"$@"
