#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=scripts/release/lib.sh
source "$SCRIPT_DIR/lib.sh"

usage() {
  printf 'usage: %s [ASSET_DIRECTORY] [OWNER/REPO] [SIGNER_WORKFLOW] [SOURCE_SHA] [all|provenance|sbom]\n' "$(basename "$0")" >&2
  exit 2
}

[[ $# -le 5 ]] || usage
asset_directory=${1:-${RELEASE_ASSET_DIR:-}}
repository=${2:-${GITHUB_REPOSITORY:-}}
signer_workflow=${3:-${SIGNER_WORKFLOW:-${repository:+$repository/.github/workflows/build-binaries.yml}}}
source_sha=${4:-${SOURCE_SHA:-}}
mode=${5:-all}
[[ -n "$asset_directory" && -n "$repository" && -n "$signer_workflow" && -n "$source_sha" ]] || usage

release_require_repository "$repository"
[[ "$signer_workflow" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+/\.github/workflows/[A-Za-z0-9_.-]+\.ya?ml$ ]] ||
  release_die "signer workflow must identify OWNER/REPO/.github/workflows/FILE.yml"
[[ "$source_sha" =~ ^([0-9a-f]{40}|[0-9a-f]{64})$ ]] ||
  release_die "source SHA must contain 40 or 64 lowercase hexadecimal characters"
case "$mode" in
  all|provenance|sbom) ;;
  *) usage ;;
esac
release_require_command gh
release_verify_asset_directory "$asset_directory"

for archive in "${RELEASE_ARCHIVES[@]}"; do
  artifact="$asset_directory/$archive"
  if [[ "$mode" == "all" || "$mode" == "provenance" ]]; then
    gh attestation verify "$artifact" \
      --repo "$repository" \
      --signer-workflow "$signer_workflow" \
      --source-digest "$source_sha" \
      >/dev/null
  fi
  if [[ "$mode" == "all" || "$mode" == "sbom" ]]; then
    gh attestation verify "$artifact" \
      --repo "$repository" \
      --signer-workflow "$signer_workflow" \
      --source-digest "$source_sha" \
      --predicate-type https://spdx.dev/Document/v2.3 \
      >/dev/null
  fi
done

printf 'verified %s attestations for %s release archives at source %s\n' \
  "$mode" "${#RELEASE_ARCHIVES[@]}" "$source_sha"
