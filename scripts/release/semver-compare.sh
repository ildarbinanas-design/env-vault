#!/usr/bin/env bash
set -euo pipefail
export LC_ALL=C

usage() {
  printf 'usage: %s MAJOR.MINOR.PATCH MAJOR.MINOR.PATCH\n' "$(basename "$0")" >&2
  exit 2
}

[[ $# -eq 2 ]] || usage
left=$1
right=$2
semver_pattern='^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'
[[ "$left" =~ $semver_pattern && "$right" =~ $semver_pattern ]] || {
  printf 'release: versions must match MAJOR.MINOR.PATCH\n' >&2
  exit 1
}

IFS=. read -r -a left_parts <<< "$left"
IFS=. read -r -a right_parts <<< "$right"
for index in 0 1 2; do
  left_part=${left_parts[$index]}
  right_part=${right_parts[$index]}
  if (( ${#left_part} < ${#right_part} )); then
    printf '%s\n' -1
    exit 0
  fi
  if (( ${#left_part} > ${#right_part} )); then
    printf '%s\n' 1
    exit 0
  fi
  if [[ "$left_part" < "$right_part" ]]; then
    printf '%s\n' -1
    exit 0
  fi
  if [[ "$left_part" > "$right_part" ]]; then
    printf '%s\n' 1
    exit 0
  fi
done
printf '%s\n' 0
