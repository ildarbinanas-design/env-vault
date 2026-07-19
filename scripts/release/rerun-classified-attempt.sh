#!/usr/bin/env bash
set -euo pipefail
export LC_ALL=C

SCRIPT_DIR=$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=scripts/release/lib.sh
source "$SCRIPT_DIR/lib.sh"
release_require_typed_contract_projection

usage() {
  printf 'usage: %s CLASSIFICATION.json OWNER/REPO\n' "$(basename "$0")" >&2
  exit 2
}

die() {
  printf 'release: %s\n' "$1" >&2
  exit 1
}

[[ $# -eq 2 ]] || usage
classification_file=$1
repository=$2

[[ -f "$classification_file" && ! -L "$classification_file" ]] ||
  die 'classification must be a regular, non-symlink file'
[[ "$repository" =~ ^[A-Za-z0-9][A-Za-z0-9-]{0,38}/[A-Za-z0-9._-]{1,100}$ ]] ||
  die 'repository must use canonical OWNER/REPO syntax'
repository_name=${repository#*/}
[[ "$repository_name" != '.' && "$repository_name" != '..' ]] ||
  die 'repository must use canonical OWNER/REPO syntax'
[[ "$repository" == "$RELEASE_SOURCE_REPOSITORY" ]] || die 'repository differs from the release contract source'
command -v jq >/dev/null 2>&1 || die 'required command is unavailable: jq'
command -v gh >/dev/null 2>&1 || die 'required command is unavailable: gh'

# Keep every validation pass bound to the same bounded bytes. JSON cannot
# contain an unescaped NUL, so a Bash variable is safe for a valid document.
classification_json=$(<"$classification_file")
[[ -n "$classification_json" && ${#classification_json} -le 1048576 ]] ||
  die 'classification JSON is empty or exceeds the size limit'

# jq's normal object decoder intentionally keeps the last duplicate key. The
# streaming form retains every root occurrence, letting this transport shim
# reject duplicate, case-variant, missing, and unknown fields before decoding.
if ! jq --stream --slurp --exit-status '
  def expected_fields: [
    "action_code",
    "attempt",
    "duplicate_artifacts",
    "expected_artifacts",
    "expected_targets",
    "expired_artifacts",
    "event",
    "head_branch",
    "head_repository",
    "matrix_complete",
    "missing_artifacts",
    "missing_targets",
    "observed_artifacts",
    "observed_targets",
    "ok",
    "prohibited_actions",
    "reason_code",
    "release_contract_schema",
    "repository",
    "rerun_failed_jobs_allowed",
    "run_conclusion",
    "run_id",
    "run_status",
    "schema_id",
    "schema_version",
    "semantic_contract_sha256",
    "source_sha",
    "unexpected_artifacts",
    "workflow_id",
    "workflow_name",
    "workflow_path"
  ] | sort;
  [
    .[]
    | select(length == 2)
    | .[0] as $path
    | select(
        ($path | length) == 1
        or (($path | length) == 2 and $path[1] == 0)
      )
    | $path[0]
  ] as $root_fields
  | ($root_fields | length) == (expected_fields | length)
    and ($root_fields | sort) == expected_fields
    and ($root_fields | map(ascii_downcase) | unique | length) == (expected_fields | length)
' <<<"$classification_json" >/dev/null 2>&1; then
  die 'classification JSON has duplicate, case-variant, missing, or unknown fields'
fi

expected_targets=$(jq -cer '[.platforms[].id] | sort' <<< "$RELEASE_CONTRACT_PROJECTION_JSON") || die 'contract target inventory is invalid'
classification_attempt=$(jq -er '.attempt | select(type == "number" and . == floor and . > 0 and . <= 2147483647)' <<< "$classification_json") ||
  die 'classification attempt identity is invalid'
expected_artifacts=$(jq -cer --arg attempt "$classification_attempt" '
  [
    .platforms[].id as $platform |
    .naming.platform_artifact_template,
    .naming.platform_evidence_template |
    gsub("\\{platform\\}"; $platform) |
    gsub("\\{attempt\\}"; $attempt)
  ] | sort
' <<< "$RELEASE_CONTRACT_PROJECTION_JSON") || die 'contract workflow artifact inventory is invalid'
expected_semantic_sha256=$(jq -er '.contract_semantic_sha256' <<< "$RELEASE_CONTRACT_PROJECTION_JSON") || die 'contract semantic identity is invalid'

if ! run_id=$(jq --slurp --raw-output --exit-status \
  --arg repository "$repository" \
  --arg branch "$RELEASE_SOURCE_DEFAULT_BRANCH" \
  --arg workflow_name "$(jq -er '[.workflows[] | select(.id == "ci")][0].name' <<< "$RELEASE_CONTRACT_PROJECTION_JSON")" \
  --arg workflow_path "$RELEASE_CI_WORKFLOW_PATH" \
  --arg semantic_sha256 "$expected_semantic_sha256" \
  --argjson expected_targets "$expected_targets" \
  --argjson expected_artifacts "$expected_artifacts" '
  def expected_fields: [
    "action_code",
    "attempt",
    "duplicate_artifacts",
    "expected_artifacts",
    "expected_targets",
    "expired_artifacts",
    "event",
    "head_branch",
    "head_repository",
    "matrix_complete",
    "missing_artifacts",
    "missing_targets",
    "observed_artifacts",
    "observed_targets",
    "ok",
    "prohibited_actions",
    "reason_code",
    "release_contract_schema",
    "repository",
    "rerun_failed_jobs_allowed",
    "run_conclusion",
    "run_id",
    "run_status",
    "schema_id",
    "schema_version",
    "semantic_contract_sha256",
    "source_sha",
    "unexpected_artifacts",
    "workflow_id",
    "workflow_name",
    "workflow_path"
  ] | sort;
  def canonical_strings:
    type == "array"
    and all(.[]; type == "string" and length > 0)
    and . == (sort)
    and length == (unique | length);
  def subset_of($values; $universe):
    all($values[]; . as $value | ($universe | index($value)) != null);
  def disjoint_from($values; $universe):
    all($values[]; . as $value | ($universe | index($value)) == null);

  if length != 1 then error("expected one JSON document") else .[0] end
  | . as $c
  | select(
      ($c | type) == "object"
      and ($c | keys) == expected_fields
      and $c.schema_id == "env-vault.attempt-classification.v1"
      and $c.schema_version == 1
      and $c.ok == false
      and $c.release_contract_schema == "env-vault.release-contract.v2"
      and $c.semantic_contract_sha256 == $semantic_sha256
      and ($c.run_id | type == "number" and . == floor and . > 0 and . <= 9007199254740991)
      and ($c.attempt | type == "number" and . == floor and . > 0 and . <= 2147483647)
      and ($c.source_sha | type == "string" and test("^[0-9a-f]{40}$"))
      and $c.repository == $repository
      and $c.head_repository == $repository
      and $c.event == "push"
      and $c.head_branch == $branch
      and $c.workflow_id == "ci"
      and $c.workflow_name == $workflow_name
      and $c.workflow_path == $workflow_path
      and $c.run_status == "completed"
      and ([
        "action_required", "cancelled", "failure", "neutral", "skipped",
        "stale", "startup_failure", "success", "timed_out"
      ] | index($c.run_conclusion)) != null
      and $c.matrix_complete == false
      and ($c.expected_targets | canonical_strings and . == $expected_targets)
      and ($c.observed_targets | canonical_strings)
      and ($c.missing_targets | canonical_strings)
      and (($c.observed_targets + $c.missing_targets) | sort) == $c.expected_targets
      and ($c.expected_artifacts | canonical_strings and . == $expected_artifacts)
      and ($c.observed_artifacts | canonical_strings)
      and ($c.missing_artifacts | canonical_strings)
      and ($c.unexpected_artifacts | canonical_strings)
      and ($c.duplicate_artifacts | canonical_strings)
      and ($c.expired_artifacts | canonical_strings)
      and subset_of($c.observed_artifacts; $c.expected_artifacts)
      and subset_of($c.missing_artifacts; $c.expected_artifacts)
      and subset_of($c.duplicate_artifacts; $c.expected_artifacts)
      and subset_of($c.expired_artifacts; $c.expected_artifacts)
      and disjoint_from($c.unexpected_artifacts; $c.expected_artifacts)
      and (([
        $c.missing_targets,
        $c.missing_artifacts,
        $c.unexpected_artifacts,
        $c.duplicate_artifacts,
        $c.expired_artifacts
      ] | map(length) | add) > 0)
      and $c.action_code == "rerun_all_jobs"
      and $c.reason_code == "ATTEMPT_MATRIX_INCOMPLETE"
      and $c.rerun_failed_jobs_allowed == false
      and $c.prohibited_actions == ["rerun_failed_jobs"]
    )
  | $c.run_id
' <<<"$classification_json" 2>/dev/null); then
  die 'classification JSON does not authorize a full-attempt rerun'
fi

# Omitting --failed is intentional: incomplete matrix artifacts require a new
# coherent workflow attempt, never a mixture of outputs from partial reruns.
exec gh run rerun "$run_id" --repo "$repository"
