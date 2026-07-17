#!/usr/bin/env bash
set -euo pipefail
export LC_ALL=C

# Release evidence is public repository data. Keep transport diagnostics from
# accidentally enabling credential-bearing HTTP traces in a workflow log.
unset GH_DEBUG GIT_TRACE GIT_TRACE_CURL GIT_CURL_VERBOSE

SCRIPT_DIR=$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=scripts/release/lib.sh
source "$SCRIPT_DIR/lib.sh"

usage() {
  printf 'usage: %s vMAJOR.MINOR.PATCH SOURCE_SHA OWNER/REPO RELEASE_EVIDENCE_JSON INDEX_MD METRICS_COMPARISON_JSON METRICS_COMPARISON_MD\n' "$(basename "$0")" >&2
  exit 2
}

cleanup() {
  if [[ -n ${scratch_dir:-} && -d "$scratch_dir" ]]; then
    rm -rf -- "$scratch_dir"
  fi
}

emit_result() {
  local commit_sha=$1
  local state=$2
  printf 'commit_sha=%s\n' "$commit_sha"
  printf 'branch=%s\n' "$branch"
  printf 'state=%s\n' "$state"
  printf 'path_prefix=%s\n' "$tuple_prefix"
  printf 'publisher_run_id=%s\n' "$publisher_run_id"
  printf 'publisher_run_attempt=%s\n' "$publisher_run_attempt"
  printf 'repair_mode=%s\n' "$publisher_repair_mode"
}

api_get() {
  local endpoint=$1
  local output=$2
  if ! gh api "$endpoint" >"$output"; then
    release_die "GitHub API request failed"
  fi
}

# Sets ref_state to present or absent. Only an explicit HTTP 404 is absence;
# authentication, rate-limit, transport, and unknown responses fail closed.
probe_ref() {
  local response="$scratch_dir/ref-probe-response"
  local error="$scratch_dir/ref-probe-error"
  local http_status

  if gh api --include "$ref_endpoint" >"$response" 2>"$error"; then
    ref_state=present
    return
  fi

  http_status=$(awk '
    /^HTTP\/[0-9.]+ [0-9][0-9][0-9]( |$)/ { status = $2 }
    match($0, /\(HTTP [0-9][0-9][0-9]\)/) { status = substr($0, RSTART + 6, 3) }
    END { print status }
  ' "$response" "$error")
  if [[ "$http_status" == "404" ]]; then
    ref_state=absent
    return
  fi
  if [[ -n "$http_status" ]]; then
    release_die "GitHub reference query failed (HTTP $http_status)"
  fi
  release_die "GitHub reference query failed (no HTTP response)"
}

read_ref_sha() {
  local output=$1
  local response="$scratch_dir/ref.json"
  local sha
  api_get "$ref_endpoint" "$response"
  sha=$(jq -er --arg ref "$full_ref" '
    select(
      type == "object" and
      .ref == $ref and
      .object.type == "commit" and
      (.object.sha | type == "string" and test("^[0-9a-f]{40}$"))
    ) |
    .object.sha
  ' "$response") || release_die "GitHub returned an invalid evidence reference"
  printf -v "$output" '%s' "$sha"
}

read_commit_tree() {
  local commit=$1
  local output=${2:-}
  local response="$scratch_dir/commit-$commit.json"
  local tree_sha
  api_get "repos/$repository/git/commits/$commit" "$response"
  tree_sha=$(jq -er --arg commit "$commit" '
    select(
      type == "object" and
      .sha == $commit and
      (.tree | type == "object") and
      (.tree.sha | type == "string" and test("^[0-9a-f]{40}$")) and
      (.parents | type == "array")
    ) |
    .tree.sha
  ' "$response") || release_die "GitHub returned invalid commit data"
  if [[ -n "$output" ]]; then
    printf -v "$output" '%s' "$tree_sha"
  fi
}

read_tree() {
  local tree_sha=$1
  local output=$2
  api_get "repos/$repository/git/trees/$tree_sha?recursive=1" "$output"
  jq -e '
    type == "object" and
    .truncated == false and
    (.tree | type == "array") and
    all(.tree[];
      type == "object" and
      (.path | type == "string" and length > 0) and
      (.mode | type == "string") and
      (.type | type == "string") and
      (.sha | type == "string" and test("^[0-9a-f]{40}$"))
    ) and
    (([.tree[].path] | length) == ([.tree[].path] | unique | length))
  ' "$output" >/dev/null || release_die "GitHub returned an invalid or truncated tree"
}

classify_version_directory() {
  local tree_file=$1
  jq -er \
    --arg prefix "$remote_prefix" \
    --arg current_tuple "$tuple_relative" \
    --argjson names "$remote_names_json" \
    --argjson ancestors "$remote_ancestors_json" '
      def lower: ascii_downcase;
      def relative($prefix): .path[($prefix | length) + 1:];
      def parts($prefix): relative($prefix) | split("/");
      def safe_run_component:
        if test("^run-[1-9][0-9]*$") then
          .[4:] as $number |
          (($number | length) < 16 or (($number | length) == 16 and $number <= "9007199254740991"))
        else false
        end;
      def safe_attempt_component:
        if test("^attempt-[1-9][0-9]*$") then
          .[8:] as $number |
          (($number | length) < 10 or (($number | length) == 10 and $number <= "2147483647"))
        else false
        end;
      ([.tree[] | select((.path | lower) as $path | any($ancestors[]; (. | lower) == $path))]) as $ancestor_entries |
      ([.tree[] | select(
        ((.path | lower) as $path | any($ancestors[]; (. | lower) == $path)) or
        ((.path | lower) | startswith(($prefix + "/" | lower)))
      )]) as $relevant_entries |
      ([.tree[] | select(.path | startswith($prefix + "/"))]) as $version_entries |
      ([.tree[] | select(.path == $prefix)]) as $version_roots |
      ([ $version_entries[] |
        select(relative($prefix) as $path | any($names[]; . == $path))
      ]) as $root_files |
      ([ $version_entries[] |
        select(relative($prefix) as $path | any($names[]; . == $path) | not)
      ]) as $lineage_entries |
      ([ $lineage_entries[] |
        select((parts($prefix) | length) == 2 and .type == "tree") |
        relative($prefix)
      ]) as $run_directories |
      ([ $lineage_entries[] |
        select((parts($prefix) | length) == 3 and .type == "tree") |
        relative($prefix)
      ]) as $attempt_directories |
      ([ $lineage_entries[] |
        select((parts($prefix) | length) == 4 and .type == "blob") |
        (parts($prefix)[0:3] | join("/"))
      ] | unique) as $file_directories |
      ([ $attempt_directories[] | split("/")[0:2] | join("/") ] | unique) as $attempt_run_directories |
      if any($ancestor_entries[];
          (.path as $path | any($ancestors[]; . == $path) | not) or
          .type != "tree" or .mode != "040000")
        or (([$relevant_entries[].path | lower] | length) != ([$relevant_entries[].path | lower] | unique | length))
      then "invalid"
      elif ($version_entries | length) == 0 and ($version_roots | length) == 0
      then "absent"
      elif
        ([ $ancestor_entries[].path ] | sort) == ($ancestors | sort) and
        ($version_roots | length) == 1 and
        ($version_roots[0].type == "tree" and $version_roots[0].mode == "040000") and
        ([ $root_files[] | relative($prefix) ] | sort) == ($names | sort) and
        all($root_files[]; .type == "blob" and .mode == "100644") and
        ($attempt_directories | length) > 0 and
        ($file_directories | sort) == ($attempt_directories | sort) and
        ($attempt_run_directories | sort) == ($run_directories | sort) and
        ([ $lineage_entries[] | select((parts($prefix) | length) == 1) | relative($prefix) ] == ["publisher-runs"]) and
        all($lineage_entries[];
          (parts($prefix)) as $parts |
          if ($parts | length) == 1 then
            $parts[0] == "publisher-runs" and .type == "tree" and .mode == "040000"
          elif ($parts | length) == 2 then
            $parts[0] == "publisher-runs" and ($parts[1] | safe_run_component) and
            .type == "tree" and .mode == "040000"
          elif ($parts | length) == 3 then
            $parts[0] == "publisher-runs" and ($parts[1] | safe_run_component) and
            ($parts[2] | safe_attempt_component) and .type == "tree" and .mode == "040000"
          elif ($parts | length) == 4 then
            $parts[0] == "publisher-runs" and ($parts[1] | safe_run_component) and
            ($parts[2] | safe_attempt_component) and
            ($parts[3] as $name | any($names[]; . == $name)) and
            .type == "blob" and .mode == "100644"
          else false
          end
        ) and
        all($attempt_directories[]; . as $directory |
          ([ $lineage_entries[] |
            select(relative($prefix) | startswith($directory + "/")) |
            select((parts($prefix) | length) == 4) |
            parts($prefix)[3]
          ] | sort) == ($names | sort)
        ) and
        any($attempt_directories[]; . as $directory |
          all($names[]; . as $name |
            ([ $root_files[] | select(relative($prefix) == $name) | .sha ][0]) ==
            ([ $lineage_entries[] | select(relative($prefix) == ($directory + "/" + $name)) | .sha ][0])
          )
        )
      then
        if any($attempt_directories[]; . == $current_tuple)
        then "tuple_complete"
        else "tuple_absent"
        end
      else "invalid"
      end
  ' "$tree_file" || release_die "cannot classify the remote evidence directory"
}

blob_sha_for_path() {
  local tree_file=$1
  local path=$2
  local output=$3
  local sha
  sha=$(jq -er --arg path "$path" '
    [.tree[] | select(.path == $path and .type == "blob" and .mode == "100644")] |
    select(length == 1) |
    .[0].sha |
    select(test("^[0-9a-f]{40}$"))
  ' "$tree_file") || release_die "remote evidence tree entry is invalid"
  printf -v "$output" '%s' "$sha"
}

verify_remote_blob() {
  local blob_sha=$1
  local expected=$2
  local label=$3
  local response="$scratch_dir/blob-$blob_sha.json"
  local decoded="$scratch_dir/decoded-$blob_sha"
  local encoded="$scratch_dir/encoded-$blob_sha"
  local canonical="$scratch_dir/canonical-$blob_sha"
  local declared_size expected_size actual_size

  api_get "repos/$repository/git/blobs/$blob_sha" "$response"
  jq -e --arg sha "$blob_sha" '
    type == "object" and
    .sha == $sha and
    .encoding == "base64" and
    (.size | type == "number" and floor == . and . >= 0 and . <= 8388608) and
    (.content | type == "string")
  ' "$response" >/dev/null || release_die "GitHub returned invalid blob metadata for $label"
  declared_size=$(jq -er '.size' "$response") || release_die "GitHub returned invalid blob size for $label"
  expected_size=$(wc -c <"$expected" | tr -d '[:space:]')
  [[ "$expected_size" =~ ^[0-9]+$ && "$declared_size" == "$expected_size" ]] ||
    release_die "published evidence size differs for $label"
  # GitHub's Git Blobs API line-wraps base64 content. Remove only transport
  # CR/LF, decode it, then require an exact canonical round trip. jq's decoder
  # alone accepts some trailing garbage and non-canonical padding.
  if ! jq -er '.content' "$response" | tr -d '\r\n' >"$encoded" ||
    ! jq -Rerj '@base64d' "$encoded" >"$decoded" ||
    ! jq -nj --rawfile content "$decoded" '$content | @base64' >"$canonical" ||
    ! cmp -s -- "$encoded" "$canonical"; then
    release_die "GitHub returned invalid blob content for $label"
  fi
  actual_size=$(wc -c <"$decoded" | tr -d '[:space:]')
  [[ "$actual_size" =~ ^[0-9]+$ && "$actual_size" == "$declared_size" ]] ||
    release_die "remote blob size mismatch for $label"
  cmp -s -- "$expected" "$decoded" || release_die "published evidence differs for $label"
}

verify_remote_tuple() {
  local tree_file=$1
  local index blob_sha
  for index in "${!tuple_paths[@]}"; do
    blob_sha_for_path "$tree_file" "${tuple_paths[$index]}" blob_sha
    verify_remote_blob "$blob_sha" "${local_files[$index]}" "${remote_names[$index]}"
  done
}

verify_remote_initial() {
  local tree_file=$1
  local index blob_sha
  for index in "${!root_paths[@]}"; do
    blob_sha_for_path "$tree_file" "${root_paths[$index]}" blob_sha
    verify_remote_blob "$blob_sha" "${local_files[$index]}" "root ${remote_names[$index]}"
  done
  verify_remote_tuple "$tree_file"
}

assert_ref_unchanged() {
  local expected=$1
  local observed
  probe_ref
  [[ "$ref_state" == "present" ]] || release_die "evidence reference changed during publication"
  read_ref_sha observed
  [[ "$observed" == "$expected" ]] || release_die "evidence reference changed during publication"
}

create_blob() {
  local path=$1
  local output=$2
  local payload="$scratch_dir/blob-payload.json"
  local response="$scratch_dir/blob-create-response.json"
  local sha

  jq -n --rawfile content "$path" '{encoding:"base64", content:($content | @base64)}' >"$payload" ||
    release_die "cannot encode local evidence"
  if ! gh api --method POST "repos/$repository/git/blobs" --input "$payload" >"$response"; then
    release_die "cannot create evidence blob"
  fi
  sha=$(jq -er '.sha | select(type == "string" and test("^[0-9a-f]{40}$"))' "$response") ||
    release_die "GitHub returned invalid created blob data"
  printf -v "$output" '%s' "$sha"
}

create_tree() {
  local output=$1
  local payload="$scratch_dir/tree-payload.json"
  local response="$scratch_dir/tree-create-response.json"
  local sha

  jq -n \
    --arg base_tree "$base_tree" \
    --argjson paths "$mutation_paths_json" \
    --argjson shas "$mutation_shas_json" '
      ($paths | length) as $length |
      select($length > 0 and $length == ($shas | length)) |
      {
        base_tree: $base_tree,
        tree: [range(0; $length) as $index |
          {path:$paths[$index], mode:"100644", type:"blob", sha:$shas[$index]}
        ]
      }
    ' >"$payload" || release_die "cannot build evidence tree request"
  if ! gh api --method POST "repos/$repository/git/trees" --input "$payload" >"$response"; then
    release_die "cannot create evidence tree"
  fi
  sha=$(jq -er '.sha | select(type == "string" and test("^[0-9a-f]{40}$"))' "$response") ||
    release_die "GitHub returned invalid created tree data"
  printf -v "$output" '%s' "$sha"
}

create_commit() {
  local tree_sha=$1
  local output=$2
  local payload="$scratch_dir/commit-payload.json"
  local response="$scratch_dir/commit-create-response.json"
  local sha

  jq -n --arg message "chore(evidence): publish $version" --arg tree "$tree_sha" --arg parent "$base_commit" \
    '{message:$message, tree:$tree, parents:[$parent]}' >"$payload" ||
    release_die "cannot build evidence commit request"
  if ! gh api --method POST "repos/$repository/git/commits" --input "$payload" >"$response"; then
    release_die "cannot create evidence commit"
  fi
  sha=$(jq -er '.sha | select(type == "string" and test("^[0-9a-f]{40}$"))' "$response") ||
    release_die "GitHub returned invalid created commit data"
  printf -v "$output" '%s' "$sha"
}

fast_forward_ref() {
  local commit=$1
  local payload="$scratch_dir/update-ref-payload.json"
  local response="$scratch_dir/update-ref-response.json"
  # force:false is an explicit no-force update. GitHub rejects a concurrent
  # non-fast-forward race instead of overwriting it.
  jq -n --arg sha "$commit" '{sha:$sha, force:false}' >"$payload" ||
    release_die "cannot build evidence reference update"
  if ! gh api --method PATCH "repos/$repository/git/refs/heads/$branch" --input "$payload" >"$response"; then
    release_die "cannot fast-forward evidence reference"
  fi
}

validate_local_file() {
  local path=$1
  local label=$2
  local size
  release_require_regular_file "$path"
  size=$(wc -c <"$path" | tr -d '[:space:]')
  [[ "$size" =~ ^[0-9]+$ && "$size" -gt 0 && "$size" -le 8388608 ]] ||
    release_die "$label must be non-empty and no larger than 8 MiB"
}

[[ $# -eq 7 ]] || usage
version=$1
source_sha=$2
repository=$3
release_evidence_json=$4
index_md=$5
metrics_comparison_json=$6
metrics_comparison_md=$7

release_require_version "$version"
[[ "$source_sha" =~ ^[0-9a-f]{40}$ ]] || release_die "source SHA must be exactly 40 lowercase hexadecimal characters"
release_require_repository "$repository"
release_require_command gh
release_require_command jq
release_require_command cmp

validate_local_file "$release_evidence_json" "release evidence JSON"
validate_local_file "$index_md" "release evidence index"
validate_local_file "$metrics_comparison_json" "metrics comparison JSON"
validate_local_file "$metrics_comparison_md" "metrics comparison Markdown"

evidence_identity=$(jq -cer --arg version "$version" --arg source_sha "$source_sha" --arg repository "$repository" '
  select(
  type == "object" and
  .schema_id == "env-vault.release-evidence.v1" and
  .schema_version == 1 and
  .repository == $repository and
  .release_version == $version and
  .source_sha == $source_sha and
  .result == "pass" and
  (.evidence_sha256 | type == "string" and test("^[0-9a-f]{64}$")) and
  (.publisher_metrics | type == "object") and
  .publisher_metrics.schema_id == "env-vault.release-metrics.v1" and
  (.publisher_metrics.run_id | type == "number" and floor == . and . >= 1 and . <= 9007199254740991) and
  (.publisher_metrics.attempt | type == "number" and floor == . and . >= 1 and . <= 2147483647) and
  .publisher_metrics.workflow_name == "build-binaries" and
  .publisher_metrics.head_sha == $source_sha and
  .publisher_metrics.conclusion == "success" and
  (.publisher_repair_mode | type == "string" and
    (. == "none" or . == "release-assets" or . == "homebrew" or . == "health")) and
  (if .publisher_repair_mode == "none"
   then .publisher_metrics.event == "push"
   else .publisher_metrics.event == "workflow_dispatch"
   end)) |
  {
    run_id: .publisher_metrics.run_id,
    attempt: .publisher_metrics.attempt,
    repair_mode: .publisher_repair_mode
  }
' "$release_evidence_json") || release_die "release evidence JSON identity, publisher tuple, or schema is invalid"
publisher_run_id=$(jq -er '.run_id | tostring' <<<"$evidence_identity") ||
  release_die "release evidence publisher run ID is invalid"
publisher_run_attempt=$(jq -er '.attempt | tostring' <<<"$evidence_identity") ||
  release_die "release evidence publisher run attempt is invalid"
publisher_repair_mode=$(jq -er '.repair_mode' <<<"$evidence_identity") ||
  release_die "release evidence publisher repair mode is invalid"
[[ "$publisher_run_id" =~ ^[1-9][0-9]{0,15}$ ]] || release_die "release evidence publisher run ID is unsafe"
[[ "$publisher_run_attempt" =~ ^[1-9][0-9]{0,9}$ ]] || release_die "release evidence publisher run attempt is unsafe"

jq -e --arg source_sha "$source_sha" '
  type == "object" and
  .schema_id == "env-vault.release-metrics-comparison.v1" and
  .schema_version == 1 and
  ([.scenarios[].scenario] == ["main_ci", "pr_ci", "publisher"]) and
  ([.scenarios[] | select(.scenario == "main_ci" or .scenario == "publisher") | .current_head_sha] | all(. == $source_sha))
' "$metrics_comparison_json" >/dev/null || release_die "metrics comparison JSON identity or schema is invalid"

index_contents=$(<"$index_md")
[[ "$index_contents" == "# env-vault $version release evidence"$'\n'* ]] ||
  release_die "release evidence index has an invalid heading"
[[ "$index_contents" == *"- Source SHA: \`$source_sha\`"* ]] ||
  release_die "release evidence index has an invalid source SHA"
metrics_markdown_contents=$(<"$metrics_comparison_md")
[[ "$metrics_markdown_contents" == "# Release pipeline metrics comparison"$'\n'* ]] ||
  release_die "metrics comparison Markdown has an invalid heading"
unset index_contents metrics_markdown_contents

branch=release-evidence
full_ref="refs/heads/$branch"
ref_endpoint="repos/$repository/git/ref/heads/$branch"
remote_prefix="evidence/releases/$version"
remote_names=(release-evidence.json index.md metrics-comparison.json metrics-comparison.md)
local_files=("$release_evidence_json" "$index_md" "$metrics_comparison_json" "$metrics_comparison_md")
tuple_relative="publisher-runs/run-$publisher_run_id/attempt-$publisher_run_attempt"
tuple_prefix="$remote_prefix/$tuple_relative"
root_paths=(
  "$remote_prefix/${remote_names[0]}"
  "$remote_prefix/${remote_names[1]}"
  "$remote_prefix/${remote_names[2]}"
  "$remote_prefix/${remote_names[3]}"
)
tuple_paths=(
  "$tuple_prefix/${remote_names[0]}"
  "$tuple_prefix/${remote_names[1]}"
  "$tuple_prefix/${remote_names[2]}"
  "$tuple_prefix/${remote_names[3]}"
)
remote_names_json=$(jq -cn \
  --arg name0 "${remote_names[0]}" --arg name1 "${remote_names[1]}" \
  --arg name2 "${remote_names[2]}" --arg name3 "${remote_names[3]}" \
  '[$name0,$name1,$name2,$name3]')
remote_ancestors_json=$(jq -cn --arg root evidence --arg releases evidence/releases --arg version "$remote_prefix" \
  '[$root,$releases,$version]')

scratch_dir=$(mktemp -d "${TMPDIR:-/tmp}/env-vault-release-evidence.XXXXXX")
trap cleanup EXIT
base_commit=''
base_tree=''
created_blob=''
created_tree=''
created_commit=''
staged_commit_tree=''
verified_tree=''

# The exact source commit must exist before any write. It is also the parent of
# the first evidence-branch commit, keeping the first publication source-bound.
read_commit_tree "$source_sha"

probe_ref
[[ "$ref_state" == "present" ]] ||
  release_die "evidence reference must be bootstrapped at the exact source SHA"
read_ref_sha base_commit
read_commit_tree "$base_commit" base_tree

base_tree_file="$scratch_dir/base-tree.json"
read_tree "$base_tree" "$base_tree_file"
directory_state=$(classify_version_directory "$base_tree_file")
case "$directory_state" in
  tuple_complete)
    verify_remote_tuple "$base_tree_file"
    assert_ref_unchanged "$base_commit"
    emit_result "$base_commit" unchanged
    exit 0
    ;;
  tuple_absent)
    initialize_version=false
    ;;
  absent)
    initialize_version=true
    ;;
  invalid)
    release_die "remote release evidence lineage is partial, conflicting, unsafe, or incompatible"
    ;;
  *)
    release_die "unknown remote release evidence state"
    ;;
esac

# Close the observation window immediately before creating Git objects. A
# second exact check protects the eventual reference mutation as well.
assert_ref_unchanged "$base_commit"

created_blobs=()
for index in "${!local_files[@]}"; do
  create_blob "${local_files[$index]}" created_blob
  verify_remote_blob "$created_blob" "${local_files[$index]}" "${remote_names[$index]}"
  created_blobs+=("$created_blob")
done

mutation_paths=("${tuple_paths[@]}")
mutation_shas=("${created_blobs[@]}")
if [[ "$initialize_version" == "true" ]]; then
  mutation_paths=("${root_paths[@]}" "${mutation_paths[@]}")
  mutation_shas=("${created_blobs[@]}" "${mutation_shas[@]}")
fi
mutation_paths_json=$(jq -cn --args '$ARGS.positional' "${mutation_paths[@]}") ||
  release_die "cannot encode evidence mutation paths"
mutation_shas_json=$(jq -cn --args '$ARGS.positional' "${mutation_shas[@]}") ||
  release_die "cannot encode evidence mutation blobs"
create_tree created_tree
staged_tree_file="$scratch_dir/staged-tree.json"
read_tree "$created_tree" "$staged_tree_file"
[[ "$(classify_version_directory "$staged_tree_file")" == "tuple_complete" ]] ||
  release_die "created evidence lineage tree is incomplete"
if [[ "$initialize_version" == "true" ]]; then
  verify_remote_initial "$staged_tree_file"
else
  verify_remote_tuple "$staged_tree_file"
fi
create_commit "$created_tree" created_commit
read_commit_tree "$created_commit" staged_commit_tree
staged_commit_response="$scratch_dir/commit-$created_commit.json"
jq -e --arg parent "$base_commit" --arg tree "$created_tree" '
  .tree.sha == $tree and
  ([.parents[].sha] == [$parent])
' "$staged_commit_response" >/dev/null || release_die "created evidence commit has an invalid parent or tree"
[[ "$staged_commit_tree" == "$created_tree" ]] || release_die "created evidence commit tree changed"

assert_ref_unchanged "$base_commit"
fast_forward_ref "$created_commit"
result_state=updated

assert_ref_unchanged "$created_commit"
created_commit_response="$scratch_dir/commit-$created_commit.json"
read_commit_tree "$created_commit" verified_tree
jq -e --arg parent "$base_commit" --arg tree "$created_tree" '
  .tree.sha == $tree and
  ([.parents[].sha] == [$parent])
' "$created_commit_response" >/dev/null || release_die "created evidence commit has an invalid parent or tree"
[[ "$verified_tree" == "$created_tree" ]] || release_die "created evidence commit tree changed"

verified_tree_file="$scratch_dir/verified-tree.json"
read_tree "$verified_tree" "$verified_tree_file"
[[ "$(classify_version_directory "$verified_tree_file")" == "tuple_complete" ]] ||
  release_die "created evidence lineage tree is incomplete"
if [[ "$initialize_version" == "true" ]]; then
  verify_remote_initial "$verified_tree_file"
else
  verify_remote_tuple "$verified_tree_file"
fi
assert_ref_unchanged "$created_commit"
emit_result "$created_commit" "$result_state"
