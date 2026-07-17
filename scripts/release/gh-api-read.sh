#!/usr/bin/env bash
set -euo pipefail
set +x
export LC_ALL=C
unset DEBUG CLICOLOR_FORCE GH_DEBUG GH_HOST GH_ENTERPRISE_TOKEN GITHUB_ENTERPRISE_TOKEN
unset GH_ENTERPRISE_HOST GH_FORCE_TTY GH_TELEMETRY GIT_TRACE GIT_TRACE_CURL
unset GIT_CURL_VERBOSE GIT_TRACE_PACKET
export GH_PROMPT_DISABLED=1 GH_NO_UPDATE_NOTIFIER=1 NO_COLOR=1

usage() {
  printf '%s\n' '{"schema_id":"env-vault.github-transport-error.v1","schema_version":1,"ok":false,"error":{"code":"INPUT_INVALID","message":"read adapter arguments are incomplete","retriable":false,"attempts":0}}' >&2
  exit 2
}

[[ $# -ge 2 ]] || usage

script_dir=$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
exec "$script_dir/releasetransport.sh" read "$@"
