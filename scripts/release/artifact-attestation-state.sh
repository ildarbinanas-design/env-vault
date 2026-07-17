#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=scripts/release/lib.sh
source "$SCRIPT_DIR/lib.sh"

usage() {
  printf 'usage: %s [ASSET_DIRECTORY] [OWNER/REPO] [SIGNER_WORKFLOW] [SOURCE_SHA]\n' "$(basename "$0")" >&2
  exit 2
}

cleanup() {
  if [[ -n ${probe_dir:-} && -d "$probe_dir" ]]; then
    rm -rf -- "$probe_dir"
  fi
}

[[ $# -le 4 ]] || usage
asset_directory=${1:-${RELEASE_ASSET_DIR:-}}
repository=${2:-${GITHUB_REPOSITORY:-}}
signer_workflow=${3:-${SIGNER_WORKFLOW:-${repository:+$repository/.github/workflows/build-binaries.yml}}}
source_sha=${4:-${SOURCE_SHA:-}}
[[ -n "$asset_directory" && -n "$repository" && -n "$signer_workflow" && -n "$source_sha" ]] || usage

release_require_repository "$repository"
[[ "$signer_workflow" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+/\.github/workflows/[A-Za-z0-9_.-]+\.ya?ml$ ]] ||
  release_die "signer workflow must identify OWNER/REPO/.github/workflows/FILE.yml"
[[ "$source_sha" =~ ^([0-9a-f]{40}|[0-9a-f]{64})$ ]] ||
  release_die "source SHA must contain 40 or 64 lowercase hexadecimal characters"
release_require_command gh
release_require_command jq
release_verify_asset_directory "$asset_directory"

probe_dir=$(mktemp -d "${TMPDIR:-/tmp}/env-vault-attestation-probe.XXXXXX")
trap cleanup EXIT

probe_attestation() {
  local archive=$1
  local predicate=$2
  local digest endpoint count response error status

  digest=$(release_sha256_file "$asset_directory/$archive")
  endpoint="repos/$repository/attestations/sha256:$digest"
  response="$probe_dir/attestation-$archive-${predicate##*/}.json"
  error="$probe_dir/attestation-$archive-${predicate##*/}.error"
  if "$SCRIPT_DIR/gh-api-read.sh" "$response" --method GET "$endpoint" \
    -f "predicate_type=$predicate" \
    -f per_page=1 2>"$error"; then
    status=0
  else
    status=$?
  fi
  case "$status" in
    0) ;;
    4) return 4 ;;
    *)
      LC_ALL=C cat -- "$error" >&2
      release_die "failed to query $predicate attestation for $archive"
      ;;
  esac

  count=$(jq -er 'select(type == "object") | .attestations | select(type == "array") | length' "$response") ||
    release_die "GitHub returned malformed attestation data for $archive"

  [[ "$count" =~ ^[0-9]+$ ]] ||
    release_die "GitHub returned a malformed attestation count for $archive"
  [[ "$count" != "0" ]] || return 4
}

predicate_state() {
  local predicate=$1
  local mode=$2
  local archive probe_status
  local missing=false

  for archive in "${RELEASE_ARCHIVES[@]}"; do
    set +e
    probe_attestation "$archive" "$predicate"
    probe_status=$?
    set -e
    case "$probe_status" in
      0) ;;
      4) missing=true ;;
      *) exit "$probe_status" ;;
    esac
  done

  if [[ "$missing" == "true" ]]; then
    printf 'missing\n'
    return 0
  fi

  # A digest/predicate match may belong to a different source or signer. Such
  # an attestation is not publication evidence, but it also must not block the
  # creation of the exact tuple. The API inventory calls above already proved
  # the endpoint was reachable; final exact verification remains a mandatory
  # fail-closed step after creation/reuse.
  if "$SCRIPT_DIR/verify-artifact-attestations.sh" \
    "$asset_directory" "$repository" "$signer_workflow" "$source_sha" "$mode" \
    >/dev/null 2>"$probe_dir/verify-$mode-error"; then
    printf 'complete\n'
  else
    printf 'missing\n'
  fi
}

provenance_state=$(predicate_state https://slsa.dev/provenance/v1 provenance)
sbom_state=$(predicate_state https://spdx.dev/Document/v2.3 sbom)
printf '%s|%s\n' "$provenance_state" "$sbom_state"
