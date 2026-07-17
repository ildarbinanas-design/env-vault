#!/usr/bin/env bash
set -euo pipefail
set +x
export LC_ALL=C
unset DEBUG CLICOLOR_FORCE GH_DEBUG GH_HOST GH_ENTERPRISE_TOKEN GITHUB_ENTERPRISE_TOKEN
unset GH_ENTERPRISE_HOST GH_FORCE_TTY GH_TELEMETRY GIT_TRACE GIT_TRACE_CURL
unset GIT_CURL_VERBOSE GIT_TRACE_PACKET
export GH_PROMPT_DISABLED=1 GH_NO_UPDATE_NOTIFIER=1 NO_COLOR=1

usage() {
  transport_error INPUT_INVALID 'transport arguments are incomplete' 2
}

transport_error() {
  local code=$1 message=$2 status=$3
  printf '{"schema_id":"env-vault.github-transport-error.v1","schema_version":1,"ok":false,"error":{"code":"%s","message":"%s","retriable":false,"attempts":0}}\n' \
    "$code" "$message" >&2
  exit "$status"
}

[[ $# -ge 1 ]] || usage

script_dir=$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
repository_root=$(CDPATH='' cd -- "$script_dir/../.." && pwd -P)
transport=${RELEASE_TRANSPORT_BIN:-}

if [[ -n "$transport" ]]; then
  [[ -f "$transport" && ! -L "$transport" && -x "$transport" ]] || {
    transport_error INPUT_INVALID 'prebuilt transport must be an executable regular non-symlink file' 2
  }
  exec "$transport" "$@"
fi

command -v go >/dev/null 2>&1 || {
  transport_error CLI_CAPABILITY_DRIFT 'go is required when no prebuilt transport is supplied' 3
}
transport=$(mktemp "${TMPDIR:-/tmp}/env-vault-releasetransport.XXXXXX" 2>/dev/null) || {
  transport_error OUTPUT_FAILED 'cannot allocate the temporary transport binary' 6
}
cleanup() {
  rm -f -- "$transport" 2>/dev/null || true
}
trap cleanup EXIT INT TERM
(cd -- "$repository_root" && go build -trimpath -o "$transport" ./cmd/releasetransport) >/dev/null 2>&1 || {
  transport_error CLI_CAPABILITY_DRIFT 'cannot build the transport binary' 3
}
"$transport" "$@"
