#!/usr/bin/env bash
set -euo pipefail
set +x
export LC_ALL=C
unset GH_DEBUG GH_HOST GH_ENTERPRISE_TOKEN GITHUB_ENTERPRISE_TOKEN
unset GIT_TRACE GIT_TRACE_CURL GIT_CURL_VERBOSE GIT_TRACE_PACKET

usage() {
  printf 'usage: %s OUTPUT GH_API_ARGUMENT...\n' "$(basename "$0")" >&2
  exit 2
}

die_usage() {
  printf 'gh-api-read: %s\n' "$*" >&2
  exit 2
}

# shellcheck disable=SC2329 # Invoked by the EXIT trap after the temp file exists.
cleanup() {
  if [[ -n ${temporary_output:-} && (-e $temporary_output || -L $temporary_output) ]]; then
    rm -f -- "$temporary_output"
  fi
}

[[ $# -ge 2 ]] || usage
output=$1
shift

[[ -n "$output" && "$output" != '-' && "$output" != *$'\n'* && "$output" != *$'\r'* ]] ||
  die_usage "output path is malformed"
[[ ! -e "$output" || (-f "$output" && ! -L "$output") ]] ||
  die_usage "output must be absent or an existing regular non-symlink file"

output_directory=$(dirname -- "$output")
output_name=$(basename -- "$output")
[[ -d "$output_directory" && ! -L "$output_directory" && -n "$output_name" ]] ||
  die_usage "output directory is invalid"

arguments=("$@")
explicit_method=''
method_count=0
has_fields=false
endpoint_count=0
for ((index = 0; index < ${#arguments[@]}; index++)); do
  argument=${arguments[index]}
  case "$argument" in
    --method|-X)
      ((index + 1 < ${#arguments[@]})) || die_usage "$argument requires a value"
      explicit_method=${arguments[index + 1]}
      method_count=$((method_count + 1))
      [[ "$explicit_method" =~ ^[Gg][Ee][Tt]$ ]] || die_usage "only explicit GET requests are allowed"
      ((index += 1))
      ;;
    --method=*)
      explicit_method=${argument#--method=}
      method_count=$((method_count + 1))
      [[ "$explicit_method" =~ ^[Gg][Ee][Tt]$ ]] || die_usage "only explicit GET requests are allowed"
      ;;
    -X?*)
      explicit_method=${argument#-X}
      method_count=$((method_count + 1))
      [[ "$explicit_method" =~ ^[Gg][Ee][Tt]$ ]] || die_usage "only explicit GET requests are allowed"
      ;;
    --input|--input=*)
      die_usage "request bodies are forbidden"
      ;;
    --hostname|--hostname=*|--silent|--silent=*|--verbose|--verbose=*|--cache|--cache=*)
      die_usage "$argument is forbidden"
      ;;
    --field|-F|--raw-field|-f)
      ((index + 1 < ${#arguments[@]})) || die_usage "$argument requires a value"
      has_fields=true
      ((index += 1))
      ;;
    --field=*|--raw-field=*|-F?*|-f?*)
      has_fields=true
      ;;
    http://*|https://*|*/graphql*)
      die_usage "only relative github.com REST endpoints are allowed"
      ;;
    repos/*|user|user/memberships/*)
      endpoint_count=$((endpoint_count + 1))
      ;;
  esac
done

[[ "$endpoint_count" == 1 ]] || die_usage "exactly one relative github.com REST endpoint is required"
[[ "$method_count" -le 1 ]] || die_usage "the request method may be specified at most once"

explicit_get=false
[[ "$explicit_method" =~ ^[Gg][Ee][Tt]$ ]] && explicit_get=true
if [[ "$has_fields" == true && "$explicit_get" != true ]]; then
  die_usage "field parameters require an explicit GET method"
fi

for command in gh mktemp mv rm sleep; do
  command -v "$command" >/dev/null 2>&1 || die_usage "required command not found: $command"
done

umask 077
temporary_output=$(mktemp "$output_directory/.${output_name}.gh-api-read.XXXXXX") || {
  printf 'gh-api-read: cannot create a temporary output file\n' >&2
  exit 1
}
trap cleanup EXIT

delays=(1 2 4 8)
for attempt in 1 2 3 4 5; do
  if GH_HOST=github.com gh api "${arguments[@]}" > "$temporary_output"; then
    if [[ -s "$temporary_output" ]]; then
      mv -f -- "$temporary_output" "$output"
      temporary_output=''
      trap - EXIT
      exit 0
    fi
    printf 'gh-api-read: GitHub API returned an empty read response\n' >&2
  fi
  if [[ "$attempt" != 5 ]]; then
    : > "$temporary_output"
    sleep "${delays[attempt - 1]}"
  fi
done

printf 'gh-api-read: read-only GitHub API request failed after 5 attempts\n' >&2
exit 1
