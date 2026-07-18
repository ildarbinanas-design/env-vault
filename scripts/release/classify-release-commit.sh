#!/usr/bin/env bash
set -euo pipefail
export LC_ALL=C

SCRIPT_DIR=$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=scripts/release/lib.sh
source "$SCRIPT_DIR/lib.sh"

usage() {
  printf 'usage: %s COMMIT\n' "$(basename "$0")" >&2
  exit 2
}

[[ $# -eq 1 ]] || usage
release_require_command git
release_require_command jq

commit=$(git rev-parse --verify "$1^{commit}") || release_die "cannot resolve release candidate commit"
[[ "$commit" =~ ^[0-9a-f]{40}$ ]] || release_die "resolved commit SHA is malformed"

read -r -a commits <<< "$(git rev-list --parents -n 1 "$commit")"
[[ ${#commits[@]} -eq 2 ]] || release_die "release candidate must have exactly one parent"
parent=${commits[1]}

manifest=$RELEASE_PLEASE_MANIFEST_PATH
if ! git cat-file -e "$commit:$manifest" 2>/dev/null; then
  release_die "release manifest is missing at candidate commit"
fi

read_manifest_version() {
  local revision=$1
  git show "$revision:$manifest" |
    jq -er --arg key "$RELEASE_PLEASE_MANIFEST_KEY" 'if type == "object" and (keys == [$key]) and ((.[$key] | type) == "string") then .[$key] else empty end'
}

current_version=$(read_manifest_version "$commit") || release_die "candidate release manifest is malformed"
version_pattern="^${RELEASE_VERSION_PATTERN#^$RELEASE_TAG_PREFIX}"
[[ "$current_version" =~ $version_pattern ]] || release_die "candidate manifest version must match MAJOR.MINOR.PATCH"

if ! git cat-file -e "$parent:$manifest" 2>/dev/null; then
  printf 'publish=false\n'
  printf 'source_sha=%s\n' "$commit"
  printf 'version=\n'
  exit 0
fi

parent_version=$(read_manifest_version "$parent") || release_die "parent release manifest is malformed"
[[ "$parent_version" =~ $version_pattern ]] || release_die "parent manifest version must match MAJOR.MINOR.PATCH"

if [[ "$current_version" == "$parent_version" ]]; then
  printf 'publish=false\n'
  printf 'source_sha=%s\n' "$commit"
  printf 'version=\n'
  exit 0
fi

comparison=$("$SCRIPT_DIR/semver-compare.sh" "$current_version" "$parent_version")
[[ "$comparison" == "1" ]] || release_die "release manifest version must increase"

subject=$(git show -s --format=%s "$commit")
expected_subject="$RELEASE_PR_TITLE_PREFIX$RELEASE_TAG_PREFIX$current_version"
subject_suffix=${subject#"$expected_subject"}
[[ "$subject" == "$expected_subject$subject_suffix" && ( -z "$subject_suffix" || "$subject_suffix" =~ ^\ \(\#[1-9][0-9]*\)$ ) ]] ||
  release_die "manifest version changed outside the deterministic release pull request"

changed_paths=()
while IFS= read -r path; do
  changed_paths+=("$path")
done < <(git diff-tree --no-commit-id --name-only -r "$parent" "$commit" | LC_ALL=C sort)
expected_paths=()
while IFS= read -r path; do
  expected_paths+=("$path")
done < <(printf '%s\n' "$RELEASE_PLEASE_MANIFEST_PATH" CHANGELOG.md README.md | LC_ALL=C sort)
[[ ${#changed_paths[@]} -eq ${#expected_paths[@]} ]] ||
  release_die "release commit must change exactly manifest, changelog, and README"
for index in "${!expected_paths[@]}"; do
  [[ "${changed_paths[$index]}" == "${expected_paths[$index]}" ]] ||
    release_die "release commit contains an unexpected path"
  mode=$(git ls-tree "$commit" -- "${expected_paths[$index]}" | awk '{ print $1 }')
  [[ "$mode" == "100644" ]] || release_die "release metadata and documentation must be regular non-executable files"
done

readme_line=$(release_readme_version_line "$RELEASE_TAG_PREFIX$current_version")
git show "$commit:README.md" | grep -Fqx -- "$readme_line" ||
  release_die "README current release does not match the manifest"

changelog_probe=$(mktemp "${TMPDIR:-/tmp}/env-vault-changelog.XXXXXX")
cleanup() {
  rm -f -- "$changelog_probe"
}
trap cleanup EXIT
git show "$commit:CHANGELOG.md" > "$changelog_probe"
"$SCRIPT_DIR/extract-changelog-section.sh" "$RELEASE_TAG_PREFIX$current_version" "$changelog_probe" >/dev/null ||
  release_die "CHANGELOG must contain exactly one non-empty section for the manifest version"

printf 'publish=true\n'
printf 'source_sha=%s\n' "$commit"
printf 'version=%s%s\n' "$RELEASE_TAG_PREFIX" "$current_version"
