#!/usr/bin/env bash
set -euo pipefail

usage() {
  printf 'usage: %s [OWNER/REPO] [WORKFLOW.yml] [COMMIT_SHA] [pull_request|push] [TIMEOUT_SECONDS] [INTERVAL_SECONDS]\n' "$(basename "$0")" >&2
  exit 2
}

tap_ci_die() {
  printf 'tap-ci: %s\n' "$*" >&2
  exit 1
}

require_nonnegative_integer() {
  local name=$1
  local value=$2
  [[ "$value" =~ ^(0|[1-9][0-9]{0,8})$ ]] ||
    tap_ci_die "$name must be a non-negative integer with at most 9 digits"
}

cleanup() {
  if [[ -n ${probe_dir:-} && -d "$probe_dir" ]]; then
    rm -rf -- "$probe_dir"
  fi
}

[[ $# -le 6 ]] || usage

repository=${1:-${TAP_CI_REPOSITORY:-}}
workflow=${2:-${TAP_CI_WORKFLOW:-test-formula.yml}}
commit_sha=${3:-${TAP_CI_SHA:-}}
event=${4:-${TAP_CI_EVENT:-}}
timeout_input=${5:-${TAP_CI_TIMEOUT_SECONDS:-900}}
interval_input=${6:-${TAP_CI_INTERVAL_SECONDS:-10}}

[[ -n "$repository" && -n "$workflow" && -n "$commit_sha" && -n "$event" ]] || usage
[[ "$repository" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]] ||
  tap_ci_die "repository must have the form OWNER/REPO"
[[ "$workflow" =~ ^[A-Za-z0-9][A-Za-z0-9_.-]*\.ya?ml$ ]] ||
  tap_ci_die "workflow must be a root workflow filename ending in .yml or .yaml"
[[ "$commit_sha" =~ ^[0-9a-f]{40}$ ]] ||
  tap_ci_die "commit SHA must contain exactly 40 lowercase hexadecimal characters"
[[ "$event" == "pull_request" || "$event" == "push" ]] ||
  tap_ci_die "event must be pull_request or push"
require_nonnegative_integer "timeout" "$timeout_input"
require_nonnegative_integer "interval" "$interval_input"

command -v gh >/dev/null 2>&1 || tap_ci_die "required command not found: gh"
command -v sleep >/dev/null 2>&1 || tap_ci_die "required command not found: sleep"

timeout_seconds=$((10#$timeout_input))
interval_seconds=$((10#$interval_input))
endpoint="repos/$repository/actions/workflows/$workflow/runs"
expected_url_prefix="https://github.com/$repository/actions/runs/"
probe_dir=$(mktemp -d "${TMPDIR:-/tmp}/env-vault-tap-ci.XXXXXX")
probe_error="$probe_dir/error"
trap cleanup EXIT

# The REST filters narrow the response server-side. The jq expression repeats
# the SHA/event checks and emits a single delimited record so an unexpected or
# malformed API response can never be mistaken for a successful run.
jq_filter="def malformed: error(\"ENV_VAULT_MALFORMED_WORKFLOW_RUNS_RESPONSE\"); "
jq_filter+="if (type != \"object\") or ((.workflow_runs | type) != \"array\") then malformed else "
jq_filter+="[.workflow_runs[] | select(.head_sha == \"$commit_sha\" and .event == \"$event\")] as \$runs | "
jq_filter+="if (\$runs | length) == 0 then \"NONE\" else "
jq_filter+="(\$runs | map(if "
jq_filter+="((.id | type) == \"number\") and ((.head_sha | type) == \"string\") and "
jq_filter+="((.event | type) == \"string\") and ((.status | type) == \"string\") and "
jq_filter+="(((.conclusion | type) == \"string\") or ((.conclusion | type) == \"null\")) and "
jq_filter+="((.html_url | type) == \"string\") "
jq_filter+="then . else malformed end) | max_by(.id)) as \$run | "
jq_filter+="[\"RUN\", (\$run.id | tostring), \$run.head_sha, \$run.event, \$run.status, "
jq_filter+="(\$run.conclusion // \"\"), \$run.html_url] | join(\"|\") end end"

started_at=$SECONDS
last_state="not queried"

while true; do
  : > "$probe_error"
  if ! response=$(gh api --method GET "$endpoint" \
    -f "head_sha=$commit_sha" \
    -f "event=$event" \
    -F per_page=100 \
    --jq "$jq_filter" 2>"$probe_error"); then
    if grep -Fq 'ENV_VAULT_MALFORMED_WORKFLOW_RUNS_RESPONSE' "$probe_error"; then
      tap_ci_die "GitHub returned malformed workflow run data"
    fi

    http_status=$(LC_ALL=C awk '
      match($0, /\(HTTP [0-9][0-9][0-9]\)/) { status = substr($0, RSTART + 6, 3) }
      END { print status }
    ' "$probe_error")
    if [[ -n "$http_status" ]]; then
      tap_ci_die "GitHub Actions API request failed (HTTP $http_status)"
    fi
    if LC_ALL=C grep -Eiq 'dial (tcp|udp)|network is unreachable|connection (refused|reset)|could not resolve|temporary failure in name resolution|TLS handshake timeout|i/o timeout|context deadline exceeded|unexpected EOF' "$probe_error"; then
      tap_ci_die "GitHub Actions API network failure (no HTTP response)"
    fi
    tap_ci_die "GitHub CLI request failed without an HTTP status"
  fi

  [[ -n "$response" && "$response" != *$'\n'* ]] ||
    tap_ci_die "GitHub returned malformed workflow run data"

  if [[ "$response" == "NONE" ]]; then
    last_state="no matching run"
    printf 'tap-ci: no matching run yet for workflow=%s sha=%s event=%s\n' \
      "$workflow" "$commit_sha" "$event" >&2
  else
    marker=''
    run_id=''
    returned_sha=''
    returned_event=''
    status=''
    conclusion=''
    run_url=''
    extra=''
    IFS='|' read -r marker run_id returned_sha returned_event status conclusion run_url extra <<< "$response"
    [[ "$marker" == "RUN" && "$run_id" =~ ^[1-9][0-9]*$ && -z "$extra" ]] ||
      tap_ci_die "GitHub returned malformed workflow run data"
    [[ "$returned_sha" == "$commit_sha" && "$returned_event" == "$event" ]] ||
      tap_ci_die "GitHub returned a workflow run for an unexpected SHA or event"
    [[ "$run_url" == "$expected_url_prefix$run_id" ]] ||
      tap_ci_die "GitHub returned a malformed workflow run URL"

    case "$status" in
      queued)
        [[ -z "$conclusion" ]] || tap_ci_die "queued workflow run has an unexpected conclusion"
        last_state="queued"
        printf 'tap-ci: matching run is queued (id=%s)\n' "$run_id" >&2
        ;;
      in_progress)
        [[ -z "$conclusion" ]] || tap_ci_die "in-progress workflow run has an unexpected conclusion"
        last_state="in_progress"
        printf 'tap-ci: matching run is in progress (id=%s)\n' "$run_id" >&2
        ;;
      requested|waiting|pending)
        [[ -z "$conclusion" ]] || tap_ci_die "$status workflow run has an unexpected conclusion"
        last_state=$status
        printf 'tap-ci: matching run has pending status %s (id=%s)\n' "$status" "$run_id" >&2
        ;;
      completed)
        [[ -n "$conclusion" ]] || tap_ci_die "completed workflow run has no conclusion"
        if [[ "$conclusion" == "success" ]]; then
          printf '%s\n' "$run_url"
          exit 0
        fi
        tap_ci_die "matching workflow run completed unsuccessfully: conclusion=$conclusion id=$run_id url=$run_url"
        ;;
      *)
        tap_ci_die "GitHub returned an unknown workflow run status: $status"
        ;;
    esac
  fi

  elapsed=$((SECONDS - started_at))
  if ((elapsed >= timeout_seconds)); then
    tap_ci_die "timed out after ${timeout_seconds}s waiting for workflow=$workflow sha=$commit_sha event=$event (last state: $last_state)"
  fi

  remaining=$((timeout_seconds - elapsed))
  sleep_for=$interval_seconds
  if ((sleep_for > remaining)); then
    sleep_for=$remaining
  fi
  if ((sleep_for > 0)); then
    sleep "$sleep_for"
  fi
done
