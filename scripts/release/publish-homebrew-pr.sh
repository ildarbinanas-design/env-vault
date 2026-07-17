#!/usr/bin/env bash
set -euo pipefail
export LC_ALL=C
# Release automation must not inherit HTTP or command tracing that could expose
# credential-bearing headers in a CI log.
unset GH_DEBUG GIT_TRACE GIT_TRACE_CURL GIT_CURL_VERBOSE

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=scripts/release/lib.sh
source "$SCRIPT_DIR/lib.sh"

usage() {
  printf 'usage: %s [--verify-only] vMAJOR.MINOR.PATCH FORMULA OWNER/REPO [WORK_DIR]\n' "$(basename "$0")" >&2
  exit 2
}

cleanup() {
  if [[ -n ${scratch_dir:-} && -d "$scratch_dir" ]]; then
    rm -rf -- "$scratch_dir"
  fi
}

emit_outputs() {
  printf 'branch=%s\n' "$branch"
  printf 'base_branch=%s\n' "$base_branch"
  printf 'base_sha=%s\n' "$base_sha"
  printf 'source_sha=%s\n' "$source_sha"
  printf 'pr_number=%s\n' "$pr_number"
  printf 'pr_url=%s\n' "$pr_url"
  printf 'head_sha=%s\n' "$head_sha"
  printf 'merge_sha=%s\n' "$merge_sha"
  printf 'tap_sha=%s\n' "$tap_sha"
  printf 'state=%s\n' "$state"
  printf 'already_merged=%s\n' "$already_merged"
  printf 'no_op=%s\n' "$no_op"
}

parse_formula_version() {
  local formula=$1
  local parsed line_count

  parsed=$(sed -nE 's/^[[:space:]]*version "([^"]+)"[[:space:]]*$/\1/p' "$formula") ||
    release_die "cannot read Homebrew formula version"
  line_count=$(printf '%s\n' "$parsed" | awk 'NF { count++ } END { print count + 0 }')
  [[ "$line_count" == "1" ]] ||
    release_die "Homebrew formula must contain exactly one version declaration"
  local version_pattern="^${RELEASE_VERSION_PATTERN#^v}"
  [[ "$parsed" =~ $version_pattern ]] ||
    release_die "Homebrew formula contains an invalid version"
  printf '%s\n' "$parsed"
}

require_formula_blob_at_commit() {
  local commit=$1
  local tree_entry mode object_type object_id object_path

  git -C "$work_dir" cat-file -e "${commit}^{commit}" 2>/dev/null ||
    release_die "tap head is not a commit"
  tree_entry=$(git -C "$work_dir" ls-tree "$commit" -- Formula/env-vault.rb) ||
    release_die "cannot inspect formula at tap head"
  read -r mode object_type object_id object_path <<< "$tree_entry"
  [[ "$mode" == "100644" && "$object_type" == "blob" && "$object_id" =~ ^[0-9a-f]{40,64}$ && "$object_path" == "Formula/env-vault.rb" ]] ||
    release_die "tap head must contain Formula/env-vault.rb as a regular file"
}

validate_formula_at_commit() {
  local commit=$1
  local snapshot=$2

  require_formula_blob_at_commit "$commit"
  git -C "$work_dir" show "${commit}:Formula/env-vault.rb" > "$snapshot" ||
    release_die "cannot read formula at tap head"
  cmp -s "$formula" "$snapshot" ||
    release_die "tap head formula does not exactly match the generated formula"
}

validate_branch_content() {
  local commit=$1
  local merge_base changed_paths

  validate_formula_at_commit "$commit" "$scratch_dir/branch-formula.rb"
  merge_base=$(git -C "$work_dir" merge-base "refs/remotes/origin/$base_branch" "$commit") ||
    release_die "release branch does not share history with the tap default branch"
  changed_paths=$(git -C "$work_dir" diff --name-only --no-renames "$merge_base" "$commit") ||
    release_die "cannot inspect release branch changes"
  [[ "$changed_paths" == "Formula/env-vault.rb" ]] ||
    release_die "release branch must change only Formula/env-vault.rb"
}

load_pr() {
  local output line count=0 merge_value

  output=$(gh pr list \
    --repo "$tap_repository" \
    --state all \
    --head "$branch" \
    --base "$base_branch" \
    --limit 100 \
    --json number,url,state,headRefName,headRefOid,baseRefName,title,isDraft,isCrossRepository,mergeCommit \
    --jq '.[] | [.number, .url, .state, .headRefName, .headRefOid, (.mergeCommit.oid // "-"), .baseRefName, .title, (.isDraft | tostring), (.isCrossRepository | tostring)] | @tsv') ||
    release_die "cannot query Homebrew pull requests"

  pr_number=''
  pr_url=''
  pr_state=''
  pr_head_ref=''
  pr_head_sha=''
  pr_merge_sha=''
  pr_base_ref=''
  pr_title=''
  pr_is_draft=''
  pr_is_cross_repository=''

  while IFS= read -r line; do
    [[ -n "$line" ]] || continue
    count=$((count + 1))
    [[ $count -le 1 ]] ||
      release_die "more than one pull request exists for $branch"
    IFS=$'\t' read -r pr_number pr_url pr_state pr_head_ref pr_head_sha merge_value pr_base_ref pr_title pr_is_draft pr_is_cross_repository <<< "$line"
    [[ "$merge_value" == "-" ]] || pr_merge_sha=$merge_value
  done <<< "$output"
}

validate_pr() {
  local files body marker_count exact_marker_count fetched_sha

  [[ "$pr_number" =~ ^[1-9][0-9]*$ ]] || release_die "pull request has an invalid number"
  [[ "$pr_url" =~ ^https://[^[:space:]]+$ ]] || release_die "pull request has an invalid URL"
  [[ "$pr_url" == https://*"/$tap_repository/pull/$pr_number" ]] ||
    release_die "pull request URL does not match the tap repository"
  [[ "$pr_state" == "OPEN" || "$pr_state" == "MERGED" || "$pr_state" == "CLOSED" ]] ||
    release_die "pull request has an invalid state"
  [[ "$pr_head_ref" == "$branch" && "$pr_base_ref" == "$base_branch" ]] ||
    release_die "pull request branches do not match the release branch"
  [[ "$pr_head_sha" =~ ^[0-9a-f]{40,64}$ ]] || release_die "pull request has an invalid head SHA"
  [[ "$pr_title" == "$expected_title" && "$pr_is_draft" == "false" ]] ||
    release_die "pull request metadata does not match the release"
  [[ "$pr_is_cross_repository" == "false" ]] ||
    release_die "pull request must originate from the tap repository"

  if [[ "$pr_state" == "MERGED" ]]; then
    [[ "$pr_merge_sha" =~ ^[0-9a-f]{40,64}$ ]] ||
      release_die "merged pull request has no valid merge SHA"
  elif [[ -n "$pr_merge_sha" ]]; then
    release_die "unmerged pull request unexpectedly has a merge SHA"
  fi

  files=$(gh pr view "$pr_number" \
    --repo "$tap_repository" \
    --json files \
    --jq '.files[].path') ||
    release_die "cannot inspect Homebrew pull request files"
  [[ "$files" == "Formula/env-vault.rb" ]] ||
    release_die "pull request must change only Formula/env-vault.rb"

  body=$(gh pr view "$pr_number" \
    --repo "$tap_repository" \
    --json body \
    --jq '.body') ||
    release_die "cannot inspect Homebrew pull request body"
  marker_count=$(printf '%s\n' "$body" | grep -F -c '<!-- env-vault-release ' || true)
  exact_marker_count=$(printf '%s\n' "$body" | grep -F -x -c "$expected_marker" || true)
  [[ "$marker_count" == "1" && "$exact_marker_count" == "1" ]] ||
    release_die "pull request release marker does not match version, source SHA, and formula digest"

  git -C "$work_dir" fetch --no-tags origin \
    "+refs/pull/$pr_number/head:refs/remotes/origin/pull/$pr_number" >&2 ||
    release_die "cannot fetch Homebrew pull request head"
  fetched_sha=$(git -C "$work_dir" rev-parse "refs/remotes/origin/pull/$pr_number") ||
    release_die "cannot resolve Homebrew pull request head"
  [[ "$fetched_sha" == "$pr_head_sha" ]] ||
    release_die "pull request head SHA does not match its Git ref"
  validate_formula_at_commit "$pr_head_sha" "$scratch_dir/pr-formula.rb"
}

verify_remote_branch() {
  local lines remote_sha fetched_sha

  lines=$(git -C "$work_dir" ls-remote --heads origin "refs/heads/$branch") ||
    release_die "cannot query the remote release branch"
  if [[ -z "$lines" ]]; then
    remote_branch_sha=''
    return
  fi
  [[ $(printf '%s\n' "$lines" | awk 'NF { count++ } END { print count + 0 }') == "1" ]] ||
    release_die "remote release branch is ambiguous"
  read -r remote_sha _ <<< "$lines"
  [[ "$remote_sha" =~ ^[0-9a-f]{40,64}$ ]] ||
    release_die "remote release branch has an invalid SHA"
  git -C "$work_dir" fetch --no-tags origin \
    "+refs/heads/$branch:refs/remotes/origin/$branch" >&2 ||
    release_die "cannot fetch the remote release branch"
  fetched_sha=$(git -C "$work_dir" rev-parse "refs/remotes/origin/$branch") ||
    release_die "cannot resolve the remote release branch"
  [[ "$fetched_sha" == "$remote_sha" ]] ||
    release_die "remote release branch changed while it was fetched"
  validate_branch_content "$remote_sha"
  remote_branch_sha=$remote_sha
}

verify_only=false
if [[ ${1:-} == "--verify-only" ]]; then
  verify_only=true
  shift
elif [[ ${1:-} == --* ]]; then
  usage
fi

[[ $# -ge 3 && $# -le 4 ]] || usage
version=$1
formula=$2
tap_repository=$3
requested_work_dir=${4:-}

release_require_version "$version"
release_require_regular_file "$formula"
release_require_repository "$tap_repository"
release_require_command git
release_require_command cmp

formula=$(cd "$(dirname "$formula")" && pwd -P)/$(basename "$formula")
target_version=${version#v}
formula_version=$(parse_formula_version "$formula")
[[ "$formula_version" == "$target_version" ]] ||
  release_die "generated formula version does not match $version"
source_sha=${SOURCE_SHA:-}
[[ "$source_sha" =~ ^[0-9a-f]{40,64}$ ]] ||
  release_die "SOURCE_SHA must be a lowercase hexadecimal commit SHA"
formula_sha=$(release_sha256_file "$formula")
expected_marker="<!-- env-vault-release version=$version source_sha=$source_sha formula_sha256=$formula_sha -->"

branch="release/env-vault-$version"
git check-ref-format --branch "$branch" >/dev/null 2>&1 ||
  release_die "invalid deterministic release branch"
[[ "$branch" != "main" ]] || release_die "refusing to use main as a release branch"

scratch_dir=$(mktemp -d "${TMPDIR:-/tmp}/env-vault-homebrew-pr.XXXXXX")
trap cleanup EXIT
if [[ -n "$requested_work_dir" ]]; then
  [[ ! -e "$requested_work_dir" && ! -L "$requested_work_dir" ]] ||
    release_die "work directory already exists: $requested_work_dir"
  work_dir=$requested_work_dir
else
  work_dir="$scratch_dir/tap"
fi

if [[ "$verify_only" == "false" ]]; then
  release_require_command gh
  [[ -n ${GH_TOKEN:-} ]] || release_die "GH_TOKEN is required to publish a Homebrew pull request"
  gh auth setup-git >/dev/null || release_die "cannot configure Git authentication"
fi

git clone --no-tags "https://github.com/${tap_repository}.git" "$work_dir" >&2 ||
  release_die "cannot clone Homebrew tap"
base_remote_ref=$(git -C "$work_dir" symbolic-ref --quiet --short refs/remotes/origin/HEAD) ||
  release_die "cannot determine the tap default branch"
[[ "$base_remote_ref" == origin/* ]] || release_die "tap default branch ref is invalid"
base_branch=${base_remote_ref#origin/}
git check-ref-format --branch "$base_branch" >/dev/null 2>&1 ||
  release_die "tap default branch name is invalid"
[[ "$branch" != "$base_branch" ]] || release_die "release branch matches the tap default branch"

git -C "$work_dir" fetch --no-tags origin \
  "+refs/heads/$base_branch:refs/remotes/origin/$base_branch" >&2 ||
  release_die "cannot refresh the tap default branch"
base_sha=$(git -C "$work_dir" rev-parse "refs/remotes/origin/$base_branch") ||
  release_die "cannot resolve the tap default branch"
[[ "$base_sha" =~ ^[0-9a-f]{40,64}$ ]] || release_die "tap default branch has an invalid SHA"
require_formula_blob_at_commit "$base_sha"
git -C "$work_dir" show "${base_sha}:Formula/env-vault.rb" > "$scratch_dir/published-formula.rb" ||
  release_die "tap default branch has no env-vault formula"
published_version=$(parse_formula_version "$scratch_dir/published-formula.rb")
comparison=$("$SCRIPT_DIR/semver-compare.sh" "$target_version" "$published_version")

pr_number=''
pr_url=''
head_sha=''
merge_sha=''
tap_sha=''
state=''
already_merged=false
no_op=false

if [[ "$verify_only" == "true" ]]; then
  [[ "$comparison" == "0" ]] ||
    release_die "tap default version is $published_version, expected $target_version"
  cmp -s "$formula" "$scratch_dir/published-formula.rb" ||
    release_die "tap default formula does not exactly match $version"
  head_sha=$base_sha
  tap_sha=$base_sha
  state=PUBLISHED
  already_merged=true
  no_op=true
  emit_outputs
  exit 0
fi

if [[ "$comparison" == "-1" ]]; then
  release_die "refusing to lower Homebrew from $published_version to $target_version"
fi

expected_title="env-vault $version"
expected_body=$(printf 'Automated Homebrew formula update for env-vault %s.\n\nSource release: https://github.com/ildarbinanas-design/env-vault/releases/tag/%s\n\n%s' "$version" "$version" "$expected_marker")
load_pr

if [[ "$comparison" == "0" ]]; then
  cmp -s "$formula" "$scratch_dir/published-formula.rb" ||
    release_die "published Homebrew formula for $version differs from the generated formula"
  if [[ -n "$pr_number" ]]; then
    validate_pr
    [[ "$pr_state" == "MERGED" ]] ||
      release_die "tap default formula is current but its release pull request is not merged"
    git -C "$work_dir" merge-base --is-ancestor "$pr_merge_sha" "refs/remotes/origin/$base_branch" ||
      release_die "pull request merge SHA is not on the tap default branch"
    head_sha=$pr_head_sha
    merge_sha=$pr_merge_sha
    state=MERGED
  else
    release_die "tap default formula is current but the deterministic release pull request is missing"
  fi
  tap_sha=$base_sha
  already_merged=true
  no_op=true
  emit_outputs
  exit 0
fi

if [[ -n "$pr_number" && "$pr_state" != "OPEN" ]]; then
  release_die "existing pull request for $branch is not open"
fi

verify_remote_branch
mutated=false
if [[ -n "$pr_number" ]]; then
  [[ -n "$remote_branch_sha" ]] ||
    release_die "open pull request has no remote release branch"
  validate_pr
  [[ "$pr_head_sha" == "$remote_branch_sha" ]] ||
    release_die "pull request head does not match the remote release branch"
  head_sha=$pr_head_sha
  merge_sha=''
  tap_sha=''
  state=OPEN
  already_merged=false
  no_op=true
  emit_outputs
  exit 0
fi

if [[ -z "$remote_branch_sha" ]]; then
  git -C "$work_dir" checkout --detach "refs/remotes/origin/$base_branch" >&2
  git -C "$work_dir" switch -c "$branch" >&2
  mkdir -p "$work_dir/Formula"
  install -m 0644 "$formula" "$work_dir/Formula/env-vault.rb"
  git -C "$work_dir" add -- Formula/env-vault.rb
  staged_paths=$(git -C "$work_dir" diff --cached --name-only --no-renames)
  [[ "$staged_paths" == "Formula/env-vault.rb" ]] ||
    release_die "new release branch must change only Formula/env-vault.rb"
  git -C "$work_dir" config user.name "env-vault release bot"
  git -C "$work_dir" config user.email "41898282+github-actions[bot]@users.noreply.github.com"
  git -C "$work_dir" commit -m "env-vault $version" >&2
  head_sha=$(git -C "$work_dir" rev-parse HEAD)
  validate_branch_content "$head_sha"
  git -C "$work_dir" push origin "$head_sha:refs/heads/$branch" >&2 ||
    release_die "release branch push was rejected; refusing to overwrite the remote branch"
  pushed_sha=$(git -C "$work_dir" ls-remote --heads origin "refs/heads/$branch" | awk 'NR == 1 { print $1 }')
  [[ "$pushed_sha" == "$head_sha" ]] ||
    release_die "remote release branch does not match the pushed commit"
  remote_branch_sha=$head_sha
  mutated=true
else
  head_sha=$remote_branch_sha
fi

current_remote_sha=$(git -C "$work_dir" ls-remote --heads origin "refs/heads/$branch" | awk 'NR == 1 { print $1 }')
[[ "$current_remote_sha" == "$remote_branch_sha" ]] ||
  release_die "remote release branch changed before pull request creation"
create_log="$scratch_dir/gh-pr-create.log"
if ! gh pr create \
  --repo "$tap_repository" \
  --base "$base_branch" \
  --head "$branch" \
  --title "$expected_title" \
  --body "$expected_body" > "$create_log" 2>&1; then
  load_pr
  if [[ -z "$pr_number" ]]; then
    release_die "cannot create Homebrew pull request"
  fi
else
  mutated=true
  load_pr
fi
[[ -n "$pr_number" ]] || release_die "created pull request cannot be found"
[[ "$pr_state" == "OPEN" ]] || release_die "created pull request is not open"
validate_pr
[[ "$pr_head_sha" == "$remote_branch_sha" ]] ||
  release_die "created pull request head does not match the remote release branch"

head_sha=$pr_head_sha
merge_sha=''
tap_sha=''
state=OPEN
already_merged=false
if [[ "$mutated" == "true" ]]; then
  no_op=false
else
  no_op=true
fi
emit_outputs
