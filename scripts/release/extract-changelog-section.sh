#!/usr/bin/env bash
set -euo pipefail
export LC_ALL=C

SCRIPT_DIR=$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=scripts/release/lib.sh
source "$SCRIPT_DIR/lib.sh"

usage() {
  printf 'usage: %s vMAJOR.MINOR.PATCH CHANGELOG\n' "$(basename "$0")" >&2
  exit 2
}

[[ $# -eq 2 ]] || usage
version=$1
changelog=$2
release_require_version "$version"
release_require_regular_file "$changelog"
target=${version#"$RELEASE_TAG_PREFIX"}

awk -v target="$target" '
  function is_target_heading(line) {
    prefix = "## [" target "]"
    return index(line, prefix) == 1
  }

  /^## / {
    if (capture) {
      capture = 0
    }
    if (is_target_heading($0)) {
      found++
      capture = 1
      next
    }
  }

  capture {
    body[++count] = $0
  }

  END {
    if (found != 1) {
      exit 4
    }
    first = 1
    while (first <= count && body[first] == "") {
      first++
    }
    last = count
    while (last >= first && body[last] == "") {
      last--
    }
    if (last < first) {
      exit 4
    }
    for (i = first; i <= last; i++) {
      print body[i]
    }
  }
' "$changelog"
