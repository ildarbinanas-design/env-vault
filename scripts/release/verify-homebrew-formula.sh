#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=scripts/release/lib.sh
source "$SCRIPT_DIR/lib.sh"

usage() {
  printf 'usage: %s [vMAJOR.MINOR.PATCH] [ASSET_DIR] [FORMULA]\n' "$(basename "$0")" >&2
  exit 2
}

cleanup() {
  if [[ -n ${work_dir:-} && -d "$work_dir" ]]; then
    rm -rf -- "$work_dir"
  fi
}

[[ $# -le 3 ]] || usage
version=${1:-${VERSION:-}}
asset_dir=${2:-${RELEASE_ASSET_DIR:-dist}}
formula=${3:-${HOMEBREW_FORMULA:-$RELEASE_HOMEBREW_FORMULA_PATH}}
[[ -n "$version" && -n "$asset_dir" && -n "$formula" ]] || usage

release_require_version "$version"
release_require_regular_file "$formula"

work_dir=$(mktemp -d "${TMPDIR:-/tmp}/env-vault-formula-verify.XXXXXX")
trap cleanup EXIT
expected="$work_dir/$RELEASE_HOMEBREW_FORMULA_NAME.rb"
"$SCRIPT_DIR/generate-homebrew-formula.sh" "$version" "$asset_dir" "$expected" >/dev/null

cmp -s "$expected" "$formula" ||
  release_die "Homebrew formula does not exactly match release $version"

if command -v ruby >/dev/null 2>&1; then
  ruby -c "$formula" >/dev/null
fi

printf 'verified Homebrew formula for %s\n' "$version"
