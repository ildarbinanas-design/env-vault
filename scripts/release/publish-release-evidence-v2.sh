#!/usr/bin/env bash
set -euo pipefail
export LC_ALL=C

# Durable v2 evidence is public repository data. Never allow diagnostic modes
# that can echo request headers or the GitHub credential into workflow logs.
unset GH_DEBUG GIT_TRACE GIT_TRACE_CURL GIT_CURL_VERBOSE

SCRIPT_DIR=$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=scripts/release/lib.sh
source "$SCRIPT_DIR/lib.sh"
release_require_typed_contract_projection
# shellcheck source=scripts/release/evidence-ledger-common.sh
source "$SCRIPT_DIR/evidence-ledger-common.sh"

usage() {
  printf 'usage: publish-release-evidence.sh --format v2 vMAJOR.MINOR.PATCH SOURCE_SHA OWNER/REPO BUNDLE_DIR INDEX_MD METRICS_COMPARISON_JSON METRICS_COMPARISON_MD STORAGE_METRICS_JSON PARITY_JSON GENESIS_JSON\n' >&2
  exit 2
}

cleanup() {
  if [[ -n ${scratch_dir:-} && -d "$scratch_dir" ]]; then
    rm -rf -- "$scratch_dir"
  fi
}

validate_file() {
  local filename=$1
  local label=$2
  local maximum=${3:-16777216}
  local size
  release_require_regular_file "$filename"
  size=$(wc -c <"$filename" | tr -d '[:space:]')
  [[ "$size" =~ ^[1-9][0-9]*$ && "$size" -le "$maximum" ]] ||
    release_die "$label has an unsupported size"
}

api_get() {
  local endpoint=$1
  local output=$2
  "$SCRIPT_DIR/gh-api-read.sh" "$output" "$endpoint" ||
    release_die "GitHub API request failed"
}

probe_ref() {
  ref_probe_sequence=$((ref_probe_sequence + 1))
  local response="$scratch_dir/ref-probe-$ref_probe_sequence.json"
  local error="$scratch_dir/ref-probe-$ref_probe_sequence.error"
  local status
  if "$SCRIPT_DIR/gh-api-read.sh" "$response" "$ref_endpoint" 2>"$error"; then
    ref_state=present
    return
  else
    status=$?
  fi
  if [[ "$status" == "4" ]]; then
    ref_state=absent
    return
  fi
  release_die "GitHub reference query failed"
}

read_ref_sha() {
  local output=$1
  ref_read_sequence=$((ref_read_sequence + 1))
  local response="$scratch_dir/ref-$ref_read_sequence.json"
  local sha
  api_get "$ref_endpoint" "$response"
  sha=$(jq -er --arg ref "$full_ref" '
    select(type == "object" and .ref == $ref and .object.type == "commit") |
    .object.sha | select(type == "string" and test("^[0-9a-f]{40}$"))
  ' "$response") || release_die "GitHub returned an invalid evidence reference"
  printf -v "$output" '%s' "$sha"
}

read_commit() {
  local commit=$1
  local tree_output=${2:-}
  local parents_output=${3:-}
  local message_output=${4:-}
  local fresh=${5:-false}
  local commit_tree_value commit_parents_value commit_message_value cache_index
  if [[ "$fresh" != "true" ]]; then
    for cache_index in "${!commit_cache_keys[@]}"; do
      if [[ "${commit_cache_keys[$cache_index]}" == "$commit" ]]; then
        commit_tree_value=${commit_tree_cache[$cache_index]}
        commit_parents_value=${commit_parents_cache[$cache_index]}
        commit_message_value=${commit_message_cache[$cache_index]}
        if [[ -n "$tree_output" ]]; then printf -v "$tree_output" '%s' "$commit_tree_value"; fi
        if [[ -n "$parents_output" ]]; then printf -v "$parents_output" '%s' "$commit_parents_value"; fi
        if [[ -n "$message_output" ]]; then printf -v "$message_output" '%s' "$commit_message_value"; fi
        return
      fi
    done
  fi
  commit_read_sequence=$((commit_read_sequence + 1))
  local response="$scratch_dir/commit-$commit-$commit_read_sequence.json"
  api_get "repos/$repository/git/commits/$commit" "$response"
  commit_tree_value=$(jq -er --arg commit "$commit" '
    select(type == "object" and .sha == $commit and
      (.tree.sha | type == "string" and test("^[0-9a-f]{40}$")) and
      (.parents | type == "array") and (.message | type == "string" and length > 0) and
      all(.parents[]; type == "object" and (.sha | type == "string" and test("^[0-9a-f]{40}$")))) |
    .tree.sha
  ' "$response") || release_die "GitHub returned invalid commit data"
  commit_parents_value=$(jq -cer '[.parents[].sha]' "$response") || release_die "GitHub returned invalid commit parents"
  commit_message_value=$(jq -er '.message' "$response") || release_die "GitHub returned an invalid commit message"
  if [[ "$fresh" != "true" ]]; then
    commit_cache_keys+=("$commit")
    commit_tree_cache+=("$commit_tree_value")
    commit_parents_cache+=("$commit_parents_value")
    commit_message_cache+=("$commit_message_value")
  fi
  if [[ -n "$tree_output" ]]; then printf -v "$tree_output" '%s' "$commit_tree_value"; fi
  if [[ -n "$parents_output" ]]; then printf -v "$parents_output" '%s' "$commit_parents_value"; fi
  if [[ -n "$message_output" ]]; then printf -v "$message_output" '%s' "$commit_message_value"; fi
}

read_tree() {
  local tree_sha=$1
  local output=$2
  api_get "repos/$repository/git/trees/$tree_sha?recursive=1" "$output"
  jq -e --arg tree "$tree_sha" '
		type == "object" and .sha == $tree and .truncated == false and (.tree | type == "array") and
    all(.tree[]; type == "object" and
      (.path | type == "string" and length > 0) and
      (.mode | type == "string") and (.type | type == "string") and
      (.sha | type == "string" and test("^[0-9a-f]{40}$"))) and
    (([.tree[].path] | length) == ([.tree[].path] | unique | length))
  ' "$output" >/dev/null || release_die "GitHub returned an invalid or truncated tree"
}

assert_source_commit() {
  read_commit "$source_sha" '' '' '' true
}

assert_ref_unchanged() {
  local expected=$1
  local observed
  probe_ref
  [[ "$ref_state" == "present" ]] || release_die "evidence reference changed during publication"
  read_ref_sha observed
  [[ "$observed" == "$expected" ]] || release_die "evidence reference changed during publication"
}

blob_sha_for_path() {
  local tree_file=$1
  local remote_path=$2
  local output=$3
  local sha
  sha=$(jq -er --arg path "$remote_path" '
    [.tree[] | select(.path == $path and .type == "blob" and .mode == "100644")] |
    select(length == 1) | .[0].sha | select(test("^[0-9a-f]{40}$"))
  ' "$tree_file") || release_die "remote evidence tree entry is invalid: $remote_path"
  printf -v "$output" '%s' "$sha"
}

tree_blob_identity() {
  local tree_file=$1
  local remote_path=$2
  local maximum=$3
  local sha_output=$4
  local size_output=$5
  local identity observed_sha observed_size
  identity=$(jq -cer --arg path "$remote_path" --argjson maximum "$maximum" '
    [.tree[] | select(.path == $path and .type == "blob" and .mode == "100644" and
      (.sha | type == "string" and test("^[0-9a-f]{40}$")) and
      (.size | type == "number" and floor == . and . >= 1 and . <= $maximum))] |
    select(length == 1) | {sha:.[0].sha,size:.[0].size}
  ' "$tree_file") || release_die "remote evidence blob identity or declared size is invalid: $remote_path"
  observed_sha=$(jq -er '.sha' <<<"$identity")
  observed_size=$(jq -er '.size|tostring' <<<"$identity")
  printf -v "$sha_output" '%s' "$observed_sha"
  printf -v "$size_output" '%s' "$observed_size"
}

read_tree_blob() {
  local sha=$1
  local declared_size=$2
  local output=$3
  local maximum=$4
  local label=$5
  local observed_size
  mkdir -p -- "$(dirname -- "$output")"
  "$SCRIPT_DIR/releasetransport.sh" git-blob read --output "$output" \
    --repository "$repository" --sha "$sha" ||
    release_die "cannot read anchored ledger $label"
  validate_file "$output" "$label" "$maximum"
  observed_size=$(wc -c <"$output" | tr -d '[:space:]')
  [[ "$observed_size" == "$declared_size" ]] ||
    release_die "anchored ledger $label differs from its Git tree size"
}

verify_remote_blob() {
  local blob_sha=$1
  local expected=$2
  local label=$3
  local expected_digest cache_file document
  expected_digest=$(release_sha256_file "$expected")
  cache_file="$scratch_dir/blob-cache-$blob_sha-$expected_digest"
  if [[ -f "$cache_file" && ! -L "$cache_file" ]]; then return; fi
  blob_verify_sequence=$((blob_verify_sequence + 1))
  document="$scratch_dir/blob-verification-$blob_verify_sequence.json"
  "$SCRIPT_DIR/releasetransport.sh" git-blob verify --output "$document" \
    --repository "$repository" --sha "$blob_sha" --expected-file "$expected" ||
    release_die "published evidence differs for $label"
  jq -e --arg repository "$repository" --arg sha "$blob_sha" --arg digest "$expected_digest" '
    .schema_id == "env-vault.github-blob-identity.v1" and .schema_version == 1 and .ok == true and
    .repository == $repository and .sha == $sha and .encoding == "base64" and
    .decoded_sha256 == $digest and .expected_sha256 == $digest
  ' "$document" >/dev/null || release_die "typed blob verification is malformed for $label"
  : >"$cache_file"
}

mutation_once() {
  local method=$1
  local endpoint=$2
  local payload=$3
  local expected_status=$4
  mutation_sequence=$((mutation_sequence + 1))
  local document="$scratch_dir/mutation-$mutation_sequence.outcome.json"
  local body="$scratch_dir/mutation-$mutation_sequence.json"

  case "$method:$endpoint" in
  "POST:repos/$repository/git/blobs" | "POST:repos/$repository/git/trees" | "POST:repos/$repository/git/commits" | \
    "POST:repos/$repository/git/refs" | "PATCH:repos/$repository/git/refs/heads/$branch") ;;
  *) release_die "GitHub mutation is outside the closed evidence transport allowlist" ;;
  esac

  "$SCRIPT_DIR/releasetransport.sh" rest mutate-once --output "$document" \
    --method "$method" --endpoint "$endpoint" --input "$payload" --expected-status "$expected_status" ||
    release_die "typed one-shot GitHub mutation transport failed before producing an outcome"
  jq -e --arg method "$method" --arg endpoint "$endpoint" '
    .schema_id == "env-vault.github-mutation-outcome.v1" and .schema_version == 1 and
    .method == $method and .endpoint == $endpoint and
    (.outcome == "success" or .outcome == "http_error" or .outcome == "ambiguous") and
    (.http_status | type == "number" and floor == . and . >= 0 and . <= 599) and
    (.error_code | type == "string") and
    (if .outcome == "success" then .ok == true and .error_code == "" else .ok == false and .error_code != "" end)
  ' "$document" >/dev/null || release_die "typed GitHub mutation outcome is malformed"
  mutation_body=$body
  mutation_http_status=$(jq -er '.http_status|tostring' "$document")
  mutation_outcome=$(jq -er '.outcome' "$document")
  if jq -e 'has("body")' "$document" >/dev/null; then
    jq -c '.body' "$document" >"$body"
  else
    printf '{}\n' >"$body"
  fi
}

create_blob() {
  local filename=$1
  local output=$2
  local label=$3
  local payload="$scratch_dir/blob-payload-$mutation_sequence.json"
  local encoded="$scratch_dir/blob-payload-$mutation_sequence.base64"
  local sha reconciliation
  base64 <"$filename" >"$encoded" || release_die "cannot encode $label"
  jq -Rn --rawfile content "$encoded" '$content | gsub("[\\r\\n]"; "") | {encoding:"base64",content:.}' >"$payload" ||
    release_die "cannot encode $label"
  if [[ "$genesis_mode" == "true" ]]; then assert_source_commit; fi
  mutation_once POST "repos/$repository/git/blobs" "$payload" 201
  case "$mutation_outcome" in
  success)
    sha=$(jq -er '.sha | select(type == "string" and test("^[0-9a-f]{40}$"))' "$mutation_body") ||
      release_die "GitHub returned invalid created blob data"
    ;;
  ambiguous)
    sha=$(git hash-object -- "$filename") || release_die "cannot calculate Git blob identity for $label"
    [[ "$sha" =~ ^[0-9a-f]{40}$ ]] || release_die "calculated Git blob identity is malformed"
    reconciliation="$scratch_dir/blob-reconciliation-$mutation_sequence.json"
    "$SCRIPT_DIR/releasetransport.sh" git-blob verify --output "$reconciliation" \
      --repository "$repository" --sha "$sha" --expected-file "$filename" ||
      release_die "cannot reconcile ambiguous evidence blob creation"
    ;;
  *) release_die "cannot create evidence blob for $label" ;;
  esac
  verify_remote_blob "$sha" "$filename" "$label"
  printf -v "$output" '%s' "$sha"
}

create_tree() {
  local output=$1
  local payload="$scratch_dir/tree-payload.json"
  local sha
  if [[ "$genesis_mode" == "true" ]]; then
    jq -n --argjson paths "$mutation_paths_json" --argjson shas "$mutation_shas_json" '
      ($paths|length) as $n | select($n > 0 and $n == ($shas|length)) |
      {tree:[range(0;$n) as $i | {path:$paths[$i],mode:"100644",type:"blob",sha:$shas[$i]}]}
    ' >"$payload" || release_die "cannot build parentless evidence tree request"
  else
    jq -n --arg base_tree "$base_tree" --argjson paths "$mutation_paths_json" --argjson shas "$mutation_shas_json" '
      ($paths|length) as $n | select($n > 0 and $n == ($shas|length)) |
      {base_tree:$base_tree,tree:[range(0;$n) as $i | {path:$paths[$i],mode:"100644",type:"blob",sha:$shas[$i]}]}
		' >"$payload" || release_die "cannot build evidence tree request"
  fi
  if [[ "$genesis_mode" == "true" ]]; then assert_source_commit; fi
  mutation_once POST "repos/$repository/git/trees" "$payload" 201
  [[ "$mutation_outcome" == "success" ]] || release_die "cannot create evidence tree"
  sha=$(jq -er '.sha | select(type == "string" and test("^[0-9a-f]{40}$"))' "$mutation_body") ||
    release_die "GitHub returned invalid created tree data"
  printf -v "$output" '%s' "$sha"
}

create_commit() {
  local tree_sha=$1
  local output=$2
  local payload="$scratch_dir/commit-payload.json"
  local sha
  if [[ "$genesis_mode" == "true" ]]; then
    jq -n --arg message "chore(evidence): create ledger at $version from $source_sha" --arg tree "$tree_sha" \
      '{message:$message,tree:$tree,parents:[]}' >"$payload" || release_die "cannot build parentless evidence commit"
  else
    jq -n --arg message "chore(evidence): publish $version from $source_sha" --arg tree "$tree_sha" --arg parent "$base_commit" \
      '{message:$message,tree:$tree,parents:[$parent]}' >"$payload" || release_die "cannot build evidence commit"
  fi
  if [[ "$genesis_mode" == "true" ]]; then assert_source_commit; fi
  mutation_once POST "repos/$repository/git/commits" "$payload" 201
  [[ "$mutation_outcome" == "success" ]] || release_die "cannot create evidence commit"
  sha=$(jq -er '.sha | select(type == "string" and test("^[0-9a-f]{40}$"))' "$mutation_body") ||
    release_die "GitHub returned invalid created commit data"
  printf -v "$output" '%s' "$sha"
}

exact_ref_create_race() {
  [[ "$mutation_http_status" == "422" ]] || return 1
  jq -e '.message == "Reference already exists"' "$mutation_body" >/dev/null
}

exact_ref_update_race() {
  [[ "$mutation_http_status" == "422" ]] || return 1
  jq -e '
    .message == "Update is not a fast forward" or
    (.message == "Validation Failed" and .errors == [{resource:"Reference",field:"sha",code:"not_fast_forward"}])
  ' "$mutation_body" >/dev/null
}

create_initial_ref() {
  local commit=$1
  local payload="$scratch_dir/create-ref-payload.json"
  jq -n --arg ref "$full_ref" --arg sha "$commit" '{ref:$ref,sha:$sha}' >"$payload" ||
    release_die "cannot build evidence reference creation"
  assert_source_commit
  mutation_once POST "repos/$repository/git/refs" "$payload" 201
  if [[ "$mutation_outcome" == "success" ]]; then
    jq -e --arg ref "$full_ref" --arg sha "$commit" '
      .ref == $ref and .object.type == "commit" and .object.sha == $sha
    ' "$mutation_body" >/dev/null || release_die "GitHub returned invalid created reference data"
    publication_state=created
    return
  fi
  if [[ "$mutation_outcome" != "ambiguous" ]] && ! exact_ref_create_race; then
    release_die "cannot create evidence reference"
  fi
  reconcile_initial_ref
  publication_state=reconciled
}

update_ref() {
  local commit=$1
  local payload="$scratch_dir/update-ref-payload.json"
  assert_ref_unchanged "$base_commit"
  jq -n --arg sha "$commit" '{sha:$sha,force:false}' >"$payload" ||
    release_die "cannot build evidence reference update"
  mutation_once PATCH "repos/$repository/git/refs/heads/$branch" "$payload" 200
  if [[ "$mutation_outcome" == "success" ]]; then
    jq -e --arg ref "$full_ref" --arg sha "$commit" '
      .ref == $ref and .object.type == "commit" and .object.sha == $sha
    ' "$mutation_body" >/dev/null || release_die "GitHub returned invalid updated reference data"
    publication_state=updated
    return
  fi
  if [[ "$mutation_outcome" != "ambiguous" ]] && ! exact_ref_update_race; then
    release_die "cannot fast-forward evidence reference"
  fi
  reconcile_updated_ref
  publication_state=reconciled
}

validate_evidence_namespace() {
  local tree_file=$1
  release_evidence_validate_namespace "$tree_file" "$v1_names_json" "$remote_names_json" \
    "$SCRIPT_DIR/evidence-ledger-tree.jq" ||
    release_die "evidence namespace contains partial, mixed, unsafe, or unrooted lineage"
}

validate_evidence_only_tree() {
  local tree_file=$1
  validate_evidence_namespace "$tree_file"
  jq -e 'all(.tree[];
    .path == "evidence" or .path == "evidence/releases" or (.path | startswith("evidence/releases/")) or
    .path == "evidence/objects" or (.path | startswith("evidence/objects/")) or
    .path == "evidence/genesis.v1.json")' "$tree_file" >/dev/null ||
    release_die "anchored evidence ledger contains non-evidence paths"
}

classify_target_version() {
  local tree_file=$1
  jq -er --arg prefix "$remote_prefix" --arg tuple "$tuple_relative" \
    --argjson v1 "$v1_names_json" --argjson v2 "$remote_names_json" '
    def relative($entry): $entry.path[($prefix|length)+1:];
    ([.tree[] | select(.path == $prefix)]) as $roots |
    ([.tree[] | select(.path | startswith($prefix + "/"))]) as $entries |
    ([ $entries[] | select(.type == "blob" and (relative(.) | contains("/") | not)) | relative(.) ] | sort) as $root_files |
    ([ $entries[] | select(.type == "blob" and (relative(.) | startswith("publisher-runs/"))) ]) as $lineage_files |
    ([ $lineage_files[] | relative(.) | split("/")[0:3] | join("/") ] | unique) as $attempts |
    if ($roots|length) == 0 and ($entries|length) == 0 then "absent"
    elif ($roots|length) != 1 or $roots[0].type != "tree" or $roots[0].mode != "040000" then "invalid"
    elif $root_files == ($v1|sort) then "legacy_version"
    elif $root_files != ($v2|sort) or ($attempts|length) == 0 then "invalid"
    elif any($entries[]; . as $entry |
      (relative($entry) | contains("/") | not) and
      (((relative($entry) == "publisher-runs" and $entry.type == "tree" and $entry.mode == "040000") or
        ($entry.type == "blob" and (relative($entry) as $name | any($v2[]; . == $name)))) | not))
    then "invalid"
    elif any($lineage_files[];
      (relative(.) | split("/")) as $p |
      ($p|length) != 4 or ($p[0] != "publisher-runs") or
      ($p[1] | test("^run-[1-9][0-9]*$") | not) or
      ($p[2] | test("^attempt-[1-9][0-9]*$") | not) or
      ($p[3] as $n | any($v2[]; . == $n) | not))
    then "invalid"
    elif any($attempts[]; . as $a |
      ([ $lineage_files[] | select(relative(.) | startswith($a + "/")) | relative(.) | split("/")[3] ] | sort) != ($v2|sort))
    then "invalid"
    elif any($attempts[]; . == $tuple) then "tuple_complete" else "tuple_absent" end
  ' "$tree_file" || release_die "cannot classify target evidence version"
}

expected_initial_paths_json() {
  local -a blobs directories
  local item parent
  blobs=("$genesis_path" "${root_paths[@]}" "${tuple_paths[@]}" "${object_remote_paths[@]}")
  directories=()
  for item in "${blobs[@]}"; do
    parent=$item
    while [[ "$parent" == */* ]]; do
      parent=${parent%/*}
      directories+=("$parent")
    done
  done
  printf '%s\n' "${blobs[@]}" "${directories[@]}" | LC_ALL=C sort -u | jq -Rsc 'split("\n")[:-1]'
}

verify_initial_tree_local() {
  local tree_file=$1
  local expected actual index blob_sha
  validate_evidence_only_tree "$tree_file"
  expected=$(expected_initial_paths_json)
  actual=$(jq -c '[.tree[].path] | sort' "$tree_file")
  [[ "$actual" == "$expected" ]] || release_die "parentless genesis tree closure differs from the exact candidate"
  blob_sha_for_path "$tree_file" "$genesis_path" blob_sha
  verify_remote_blob "$blob_sha" "$genesis_json" genesis
  for index in "${!root_paths[@]}"; do
    blob_sha_for_path "$tree_file" "${root_paths[$index]}" blob_sha
    verify_remote_blob "$blob_sha" "${local_files[$index]}" "root ${remote_names[$index]}"
    blob_sha_for_path "$tree_file" "${tuple_paths[$index]}" blob_sha
    verify_remote_blob "$blob_sha" "${local_files[$index]}" "tuple ${remote_names[$index]}"
  done
  for index in "${!object_remote_paths[@]}"; do
    blob_sha_for_path "$tree_file" "${object_remote_paths[$index]}" blob_sha
    verify_remote_blob "$blob_sha" "${object_local_files[$index]}" "object ${object_digests[$index]}"
  done
}

verify_target_tuple() {
  local tree_file=$1
  local include_root=$2
  local index blob_sha
  for index in "${!tuple_paths[@]}"; do
    blob_sha_for_path "$tree_file" "${tuple_paths[$index]}" blob_sha
    verify_remote_blob "$blob_sha" "${local_files[$index]}" "tuple ${remote_names[$index]}"
    if [[ "$include_root" == "true" ]]; then
      blob_sha_for_path "$tree_file" "${root_paths[$index]}" blob_sha
      verify_remote_blob "$blob_sha" "${local_files[$index]}" "root ${remote_names[$index]}"
    fi
  done
  for index in "${!object_remote_paths[@]}"; do
    blob_sha_for_path "$tree_file" "${object_remote_paths[$index]}" blob_sha
    verify_remote_blob "$blob_sha" "${object_local_files[$index]}" "object ${object_digests[$index]}"
  done
}

verify_anchored_root() {
  local root_commit=$1
  local root_tree_file=$2
  local validation_sequence=$3
  local directory="$scratch_dir/anchored-root-$validation_sequence-$root_commit"
  local anchor="$directory/genesis.v1.json"
  local bundle_dir="$directory/bundle"
  local first_version first_source first_run first_attempt first_prefix first_tuple index digest path object_sha object_tree_size
  local actual_object_bytes expected_object_bytes observed_object_bytes
  local root_message anchor_size metadata_limit metadata_total root_sha root_size tuple_sha tuple_size
  local -a first_root_paths first_tuple_paths first_object_paths all_blob_paths all_directories root_digests metadata_shas metadata_sizes metadata_limits
  mkdir -p "$bundle_dir/objects/sha256" "$directory/root" "$directory/tuple"
  tree_blob_identity "$root_tree_file" "$genesis_path" 16384 anchor_sha anchor_size
  read_tree_blob "$anchor_sha" "$anchor_size" "$anchor" 16384 "genesis anchor"
  first_version=$(jq -er '.first_release_version | select(test("^v(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)$"))' "$anchor") ||
    release_die "anchored ledger genesis has an invalid first version"
  first_source=$(jq -er '.source_sha | select(test("^[0-9a-f]{40}$"))' "$anchor") || release_die "anchored ledger genesis has an invalid source SHA"
  read_commit "$root_commit" '' '' root_message
  [[ "$root_message" == "chore(evidence): create ledger at $first_version from $first_source" ]] ||
    release_die "anchored root commit message is not exact"
  first_run=$(jq -er '.publisher_run_id | tostring | select(test("^[1-9][0-9]*$"))' "$anchor") || release_die "anchored ledger genesis has an invalid publisher run"
  first_attempt=$(jq -er '.publisher_run_attempt | tostring | select(test("^[1-9][0-9]*$"))' "$anchor") || release_die "anchored ledger genesis has an invalid publisher attempt"
  first_prefix="evidence/releases/$first_version"
  first_tuple="$first_prefix/publisher-runs/run-$first_run/attempt-$first_attempt"
  first_root_paths=()
  first_tuple_paths=()
  metadata_shas=()
  metadata_sizes=()
  metadata_limits=()
  metadata_total=0
  for index in "${!remote_names[@]}"; do
    first_root_paths+=("$first_prefix/${remote_names[$index]}")
    first_tuple_paths+=("$first_tuple/${remote_names[$index]}")
    case "${remote_names[$index]}" in
    release-evidence-bundle.json) metadata_limit=153599 ;;
    storage-metrics.json | parity.json) metadata_limit=65536 ;;
    *) metadata_limit=16777216 ;;
    esac
    tree_blob_identity "$root_tree_file" "${first_root_paths[$index]}" "$metadata_limit" root_sha root_size
    tree_blob_identity "$root_tree_file" "${first_tuple_paths[$index]}" "$metadata_limit" tuple_sha tuple_size
    [[ "$root_sha" == "$tuple_sha" && "$root_size" == "$tuple_size" ]] ||
      release_die "anchored root and first attempt blob identities differ"
    metadata_total=$((metadata_total + root_size))
    [[ "$metadata_total" -le 67108864 ]] || release_die "anchored metadata aggregate exceeds the supported limit"
    metadata_shas+=("$root_sha")
    metadata_sizes+=("$root_size")
    metadata_limits+=("$metadata_limit")
  done
  for index in "${!remote_names[@]}"; do
    read_tree_blob "${metadata_shas[$index]}" "${metadata_sizes[$index]}" \
      "$directory/root/${remote_names[$index]}" "${metadata_limits[$index]}" "root ${remote_names[$index]}"
  done
  cp "$directory/root/release-evidence-bundle.json" "$bundle_dir/release-evidence-bundle.json"
  validate_file "$bundle_dir/release-evidence-bundle.json" "anchored root bundle" 153599
  jq -e '
		.schema_id == "env-vault.release-evidence-bundle.v2" and .schema_version == 2 and
		(.objects | type == "array" and length >= 1 and length <= 64) and
		([.objects[].sha256] as $digests |
		  all($digests[]; type == "string" and test("^[0-9a-f]{64}$")) and
		  $digests == ($digests | sort) and ($digests | length) == ($digests | unique | length)) and
		all(.objects[];
		  .encoding == "gzip" and
		  (.media_type == "application/json" or .media_type == "application/vnd.env-vault.release-evidence-core.v2+json") and
		  (.uncompressed_size | type == "number" and floor == . and . >= 1 and . <= 16777216) and
		  (.compressed_size | type == "number" and floor == . and . >= 1 and . <= 16779264) and
		  (.compressed_sha256 | type == "string" and test("^[0-9a-f]{64}$"))) and
		([.objects[].uncompressed_size] | add) <= 67108864 and
		([.objects[].compressed_size] | add) <= 67174400
	' "$bundle_dir/release-evidence-bundle.json" >/dev/null ||
    release_die "anchored root bundle object inventory is invalid"
  local root_digest_list="$directory/root-digests.txt"
  jq -er '.objects[]?.sha256 | select(test("^[0-9a-f]{64}$"))' "$bundle_dir/release-evidence-bundle.json" >"$root_digest_list" ||
    release_die "anchored root bundle has an invalid object inventory"
  root_digests=()
  while IFS= read -r digest; do root_digests+=("$digest"); done <"$root_digest_list"
  [[ ${#root_digests[@]} -gt 0 ]] || release_die "anchored root bundle has no objects"
  first_object_paths=()
  actual_object_bytes=0
  for digest in "${root_digests[@]}"; do
    path="evidence/objects/sha256/$digest.gz"
    first_object_paths+=("$path")
    expected_object_bytes=$(jq -er --arg digest "$digest" '.objects[] | select(.sha256 == $digest) | .compressed_size' "$bundle_dir/release-evidence-bundle.json") ||
      release_die "anchored object descriptor is missing"
    tree_blob_identity "$root_tree_file" "$path" 16779264 object_sha object_tree_size
    [[ "$object_tree_size" == "$expected_object_bytes" ]] ||
      release_die "anchored object Git tree size differs from its descriptor"
    read_tree_blob "$object_sha" "$object_tree_size" "$bundle_dir/objects/sha256/$digest.gz" \
      16779264 "object $digest"
    observed_object_bytes=$(wc -c <"$bundle_dir/objects/sha256/$digest.gz" | tr -d '[:space:]')
    [[ "$observed_object_bytes" == "$expected_object_bytes" ]] || release_die "anchored object size differs from its descriptor"
    actual_object_bytes=$((actual_object_bytes + observed_object_bytes))
    [[ "$actual_object_bytes" -le 67174400 ]] || release_die "anchored object aggregate exceeds the supported limit"
  done
  "$publisher_check" evidence bundle-verify --bundle-dir "$bundle_dir" >/dev/null || release_die "anchored root bundle is invalid"
  "$publisher_check" evidence genesis-verify --input "$anchor" --bundle-dir "$bundle_dir" >/dev/null || release_die "anchored root genesis is invalid"
  all_blob_paths=("$genesis_path" "${first_root_paths[@]}" "${first_tuple_paths[@]}" "${first_object_paths[@]}")
  all_directories=()
  for path in "${all_blob_paths[@]}"; do
    while [[ "$path" == */* ]]; do
      path=${path%/*}
      all_directories+=("$path")
    done
  done
  expected_root_paths=$(printf '%s\n' "${all_blob_paths[@]}" "${all_directories[@]}" | LC_ALL=C sort -u | jq -Rsc 'split("\n")[:-1]')
  actual_root_paths=$(jq -c '[.tree[].path] | sort' "$root_tree_file")
  [[ "$actual_root_paths" == "$expected_root_paths" ]] || release_die "anchored root tree is not an exact evidence-only closure"
}

assert_controlled_blobs_preserved() {
  local parent_tree_file=$1
  local child_tree_file=$2
  release_evidence_assert_controlled_blobs_preserved "$parent_tree_file" "$child_tree_file" ||
    release_die "evidence lineage rewrote or removed an earlier controlled blob"
}

validate_present_ledger() {
  local tip=$1
  local tip_tree_file=$2
  ledger_validation_sequence=$((ledger_validation_sequence + 1))
  local validation_sequence=$ledger_validation_sequence
  local genesis_count cursor parents tree message depth current_tree_file anchor_sha observed_anchor
  local parent parent_tree parent_tree_file child_tree_file parent_controlled_entries current_non_evidence parent_non_evidence
  local required_seen=false
  genesis_count=$(jq '[.tree[] | select(.path == "evidence/genesis.v1.json" and .type == "blob" and .mode == "100644")] | length' "$tip_tree_file")
  if [[ "$genesis_count" == "0" ]]; then
    ledger_mode=legacy-compatible
    cursor=$tip
    current_tree_file=$tip_tree_file
    depth=0
    while :; do
      depth=$((depth + 1))
      [[ "$depth" -le 64 ]] || release_die "legacy evidence lineage exceeds the 64-commit validation bound"
      [[ -z "$required_ancestor" || "$cursor" != "$required_ancestor" ]] || required_seen=true
      validate_evidence_namespace "$current_tree_file"
      [[ "$(jq '[.tree[] | select((.path|ascii_downcase) == "evidence/genesis.v1.json")]|length' "$current_tree_file")" == "0" ]] ||
        release_die "legacy evidence lineage contains an anchor"
      read_commit "$cursor" tree parents message
      [[ "$(jq 'length' <<<"$parents")" == "1" ]] || release_die "legacy evidence commit is not single-parent"
      parent=$(jq -er '.[0]' <<<"$parents")
      read_commit "$parent" parent_tree ignored_parents ignored_message
      parent_tree_file="$scratch_dir/legacy-parent-tree-$validation_sequence-$depth-$parent.json"
      read_tree "$parent_tree" "$parent_tree_file"
      assert_controlled_blobs_preserved "$parent_tree_file" "$current_tree_file"
      current_non_evidence=$(release_evidence_non_evidence_projection "$current_tree_file")
      parent_non_evidence=$(release_evidence_non_evidence_projection "$parent_tree_file")
      [[ "$current_non_evidence" == "$parent_non_evidence" ]] || release_die "legacy evidence commit changed inherited non-evidence paths"
      parent_controlled_entries=$(release_evidence_controlled_entry_count "$parent_tree_file")
      if [[ "$parent_controlled_entries" == "0" ]]; then break; fi
      cursor=$parent
      current_tree_file=$parent_tree_file
    done
  else
    [[ "$genesis_count" == "1" ]] || release_die "evidence ledger has conflicting genesis anchors"
    ledger_mode=evidence-only-parentless-v1
    anchor_sha=$(jq -er '.tree[] | select(.path == "evidence/genesis.v1.json") | .sha' "$tip_tree_file")
    cursor=$tip
    current_tree_file=$tip_tree_file
    depth=0
    while :; do
      depth=$((depth + 1))
      [[ "$depth" -le 64 ]] || release_die "anchored evidence lineage exceeds the 64-commit validation bound"
      [[ -z "$required_ancestor" || "$cursor" != "$required_ancestor" ]] || required_seen=true
      validate_evidence_only_tree "$current_tree_file"
      observed_anchor=$(jq -er '[.tree[] | select(.path == "evidence/genesis.v1.json" and .type == "blob" and .mode == "100644")] | select(length == 1) | .[0].sha' "$current_tree_file") ||
        release_die "anchored evidence lineage lost its genesis anchor"
      [[ "$observed_anchor" == "$anchor_sha" ]] || release_die "evidence genesis anchor changed after ledger creation"
      read_commit "$cursor" tree parents message
      case "$(jq 'length' <<<"$parents")" in
      0)
        root_commit=$cursor
        root_tree_file=$current_tree_file
        verify_anchored_root "$root_commit" "$root_tree_file" "$validation_sequence"
        break
        ;;
      1)
        child_tree_file=$current_tree_file
        cursor=$(jq -er '.[0]' <<<"$parents")
        read_commit "$cursor" parent_tree ignored_parents ignored_message
        current_tree_file="$scratch_dir/anchored-tree-$validation_sequence-$depth-$cursor.json"
        read_tree "$parent_tree" "$current_tree_file"
        assert_controlled_blobs_preserved "$current_tree_file" "$child_tree_file"
        ;;
      *) release_die "anchored evidence ledger commit is not single-parent" ;;
      esac
    done
  fi
  validated_ledger_depth=$depth
  if [[ -n "$required_ancestor" && "$required_seen" != "true" ]]; then
    release_die "reconciled evidence tip is not a descendant of the exact pre-update base"
  fi
}

validate_published_tip() {
  local commit=$1
  local expected_mode=$2
  local tree parents message tree_file state expected_message
  read_commit "$commit" tree parents message
  if [[ "$expected_mode" == "genesis" ]]; then
    [[ "$(jq -c '.' <<<"$parents")" == "[]" ]] || release_die "created genesis commit is not parentless"
    expected_message="chore(evidence): create ledger at $version from $source_sha"
  else
    expected_message="chore(evidence): publish $version from $source_sha"
  fi
  [[ "$message" == "$expected_message" ]] || release_die "published evidence commit message is not exact"
  published_tip_validation_sequence=$((published_tip_validation_sequence + 1))
  tree_file="$scratch_dir/published-tree-$commit-$published_tip_validation_sequence.json"
  read_tree "$tree" "$tree_file"
  if [[ "$expected_mode" == "genesis" ]]; then
    verify_initial_tree_local "$tree_file"
  else
    validate_present_ledger "$commit" "$tree_file"
    state=$(classify_target_version "$tree_file")
    [[ "$state" == "tuple_complete" ]] || release_die "published evidence tuple is absent after ref mutation"
    verify_target_tuple "$tree_file" "$initialize_version"
  fi
}

reconcile_initial_ref() {
  local observed
  probe_ref
  [[ "$ref_state" == "present" ]] || release_die "ambiguous genesis ref creation did not produce a reference"
  read_ref_sha observed
  validate_published_tip "$observed" genesis
  published_commit=$observed
}

reconcile_updated_ref() {
  local observed
  probe_ref
  [[ "$ref_state" == "present" ]] || release_die "ambiguous ref update removed the evidence reference"
  read_ref_sha observed
  [[ "$observed" != "$base_commit" ]] || release_die "ambiguous ref update left the evidence reference unchanged"
  required_ancestor=$base_commit
  validate_published_tip "$observed" append
  required_ancestor=''
  published_commit=$observed
}

emit_result() {
  local commit=$1
  printf 'commit_sha=%s\n' "$commit"
  printf 'branch=%s\n' "$branch"
  printf 'state=%s\n' "$publication_state"
  printf 'format=v2\n'
  printf 'ledger_mode=%s\n' "$ledger_mode"
  printf 'path_prefix=%s\n' "$tuple_prefix"
  printf 'publisher_run_id=%s\n' "$publisher_run_id"
  printf 'publisher_run_attempt=%s\n' "$publisher_run_attempt"
  printf 'repair_mode=%s\n' "$publisher_repair_mode"
}

[[ $# -eq 10 ]] || usage
version=$1
source_sha=$2
repository=$3
bundle_dir=$4
index_md=$5
metrics_comparison_json=$6
metrics_comparison_md=$7
storage_metrics_json=$8
parity_json=$9
genesis_json=${10}

release_require_version "$version"
[[ "$source_sha" =~ ^[0-9a-f]{40}$ ]] || release_die "source SHA must be exactly 40 lowercase hexadecimal characters"
release_require_repository "$repository"
[[ "$repository" == "$RELEASE_SOURCE_REPOSITORY" ]] || release_die "repository differs from the release contract source"
release_require_command gh
release_require_command jq
release_require_command git
release_require_command base64

publisher_check=${EVIDENCE_PUBLISHER_CHECK:-}
[[ -n "$publisher_check" && -f "$publisher_check" && ! -L "$publisher_check" && -x "$publisher_check" ]] ||
  release_die "EVIDENCE_PUBLISHER_CHECK must name the reviewed executable checker"
[[ -d "$bundle_dir" && ! -L "$bundle_dir" ]] || release_die "bundle directory is invalid"
bundle_root="$bundle_dir/release-evidence-bundle.json"
validate_file "$bundle_root" "bundle root" 153599
validate_file "$index_md" "evidence index"
validate_file "$metrics_comparison_json" "metrics comparison JSON"
validate_file "$metrics_comparison_md" "metrics comparison Markdown"
validate_file "$storage_metrics_json" "storage metrics JSON" 65536
validate_file "$parity_json" "parity JSON" 65536
validate_file "$genesis_json" "genesis JSON" 16384

scratch_dir=$(mktemp -d "${TMPDIR:-/tmp}/env-vault-release-evidence-v2.XXXXXX")
trap cleanup EXIT
capabilities="$scratch_dir/releasecheck-capabilities.json"
"$publisher_check" --version --json >"$capabilities" || release_die "cannot inspect evidence checker capabilities"
jq -e '.supported_schema_versions.release_evidence_bundle == [2] and .supported_schema_versions.release_evidence_genesis == [1]' "$capabilities" >/dev/null ||
  release_die "evidence checker lacks v2/genesis capability"
"$publisher_check" evidence bundle-verify --bundle-dir "$bundle_dir" >/dev/null || release_die "v2 evidence bundle is invalid"
"$publisher_check" evidence genesis-verify --input "$genesis_json" --bundle-dir "$bundle_dir" >/dev/null || release_die "genesis candidate is invalid"

bundle_identity=$(jq -cer --arg repository "$repository" --arg version "$version" --arg source "$source_sha" '
  select(.schema_id == "env-vault.release-evidence-bundle.v2" and .schema_version == 2 and
    .repository == $repository and .release_version == $version and .source_sha == $source and .result == "pass" and
    (.publisher_run_id | type == "number" and floor == . and . >= 1 and . <= 9007199254740991) and
    (.publisher_run_attempt | type == "number" and floor == . and . >= 1 and . <= 2147483647) and
    (.publisher_repair_mode == "none" or .publisher_repair_mode == "release-assets" or .publisher_repair_mode == "homebrew" or .publisher_repair_mode == "health") and
    (.bundle_sha256 | type == "string" and test("^[0-9a-f]{64}$"))) |
  {run_id:.publisher_run_id,attempt:.publisher_run_attempt,repair_mode:.publisher_repair_mode,bundle_sha256:.bundle_sha256}
' "$bundle_root") || release_die "bundle identity or publisher tuple is invalid"
publisher_run_id=$(jq -er '.run_id|tostring' <<<"$bundle_identity")
publisher_run_attempt=$(jq -er '.attempt|tostring' <<<"$bundle_identity")
publisher_repair_mode=$(jq -er '.repair_mode' <<<"$bundle_identity")
bundle_sha256=$(jq -er '.bundle_sha256' <<<"$bundle_identity")

jq -e --arg repository "$repository" --arg version "$version" --arg source "$source_sha" --arg bundle "$bundle_sha256" \
  --arg legacy "$(jq -er '.legacy_canonical_json_sha256' "$bundle_root")" '
  .schema_id == "env-vault.release-evidence-parity.v1" and .schema_version == 1 and .ok == true and
  .repository == $repository and .release_version == $version and .source_sha == $source and
  .bundle_sha256 == $bundle and .legacy_canonical_json_sha256 == $legacy and
  .legacy_decision == "pass" and .bundle_decision == "pass" and
  .legacy_error_code == "" and .bundle_error_code == "" and
  .reconstructed_byte_exact == true and .result == "pass"
' "$parity_json" >/dev/null || release_die "v1/v2 parity result is invalid"
jq -e --arg repository "$repository" --arg version "$version" --arg source "$source_sha" '
  .schema_id == "env-vault.release-evidence-storage-metrics.v1" and .schema_version == 1 and
  .repository == $repository and .release_version == $version and .source_sha == $source and
  .targets_met == true and .result == "pass"
' "$storage_metrics_json" >/dev/null || release_die "storage metrics target gate is invalid"
jq -e --arg source "$source_sha" '
  .schema_id == "env-vault.release-metrics-comparison.v1" and .schema_version == 1 and
  ([.scenarios[].scenario] == ["main_ci","pr_ci","publisher"]) and
  ([.scenarios[] | select(.scenario == "main_ci" or .scenario == "publisher") | .current_head_sha] | all(. == $source))
' "$metrics_comparison_json" >/dev/null || release_die "metrics comparison identity is invalid"
jq -e --arg repository "$repository" --arg version "$version" --arg source "$source_sha" --arg bundle "$bundle_sha256" \
  --argjson run_id "$publisher_run_id" --argjson attempt "$publisher_run_attempt" '
  .schema_id == "env-vault.release-evidence-genesis.v1" and .schema_version == 1 and
  .repository == $repository and .first_release_version == $version and .source_sha == $source and
  .first_bundle_sha256 == $bundle and .publisher_run_id == $run_id and .publisher_run_attempt == $attempt
' "$genesis_json" >/dev/null || release_die "genesis candidate tuple is invalid"
index_contents=$(<"$index_md")
[[ "$index_contents" == "# env-vault $version release evidence"$'\n'* && "$index_contents" == *"- Source SHA: \`$source_sha\`"* ]] ||
  release_die "release evidence index identity is invalid"
metrics_markdown_contents=$(<"$metrics_comparison_md")
[[ "$metrics_markdown_contents" == "# Release pipeline metrics comparison"$'\n'* ]] ||
  release_die "metrics comparison Markdown heading is invalid"
unset index_contents metrics_markdown_contents

object_digest_list="$scratch_dir/object-digests.txt"
jq -er '.objects[].sha256 | select(test("^[0-9a-f]{64}$"))' "$bundle_root" >"$object_digest_list" ||
  release_die "bundle object inventory is invalid"
object_digests=()
while IFS= read -r digest; do object_digests+=("$digest"); done <"$object_digest_list"
[[ ${#object_digests[@]} -gt 0 && ${#object_digests[@]} -le 64 ]] || release_die "bundle object inventory is invalid"
object_local_files=()
object_remote_paths=()
for digest in "${object_digests[@]}"; do
  object_file="$bundle_dir/objects/sha256/$digest.gz"
  validate_file "$object_file" "bundle object $digest" 17825792
  object_local_files+=("$object_file")
  object_remote_paths+=("evidence/objects/sha256/$digest.gz")
done
compact_root_bytes=$(wc -c <"$bundle_root" | tr -d '[:space:]')
auxiliary_bytes=$(($(wc -c <"$index_md") + $(wc -c <"$metrics_comparison_json") + $(wc -c <"$metrics_comparison_md")))
parity_bytes=$(wc -c <"$parity_json" | tr -d '[:space:]')
storage_metrics_bytes=$(wc -c <"$storage_metrics_json" | tr -d '[:space:]')
compact_metadata_bytes=$((compact_root_bytes + auxiliary_bytes + parity_bytes + storage_metrics_bytes))
object_raw_bytes=$(jq '[.objects[].uncompressed_size] | add' "$bundle_root")
object_compressed_bytes=$(jq '[.objects[].compressed_size] | add' "$bundle_root")
compact_logical_bytes=$((2 * compact_metadata_bytes + object_compressed_bytes))
compact_unique_git_blob_bytes=$((compact_metadata_bytes + object_compressed_bytes))
compact_offline_reconstructed_bytes=$((compact_metadata_bytes + object_raw_bytes))
jq -e \
  --argjson root "$compact_root_bytes" --argjson auxiliary "$auxiliary_bytes" \
  --argjson parity "$parity_bytes" --argjson self "$storage_metrics_bytes" --argjson metadata "$compact_metadata_bytes" \
  --argjson count "${#object_digests[@]}" --argjson raw "$object_raw_bytes" --argjson compressed "$object_compressed_bytes" \
  --argjson logical "$compact_logical_bytes" --argjson unique_git "$compact_unique_git_blob_bytes" --argjson offline "$compact_offline_reconstructed_bytes" '
  .compact_root_index_bytes == $root and .auxiliary_bytes == $auxiliary and .parity_bytes == $parity and
  .storage_metrics_self_bytes == $self and .compact_durable_metadata_bytes == $metadata and
  .logical_path_bytes_scope == "git_blob_payload_bytes_per_ledger_path" and
  .unique_git_blob_bytes_scope == "git_blob_payload_bytes_after_object_id_deduplication" and
  .offline_reconstructed_bytes_scope == "durable_metadata_plus_gunzip_object_bytes" and
  .compact_object_count == $count and .compact_object_uncompressed_bytes == $raw and
  .compact_object_compressed_bytes == $compressed and .compact_root_attempt_logical_bytes == $logical and
  .compact_unique_git_blob_bytes == $unique_git and .compact_offline_reconstructed_bytes == $offline and
  .root_target_bytes == 153600 and .reduction_target_permille == 600
' "$storage_metrics_json" >/dev/null || release_die "storage metrics differ from actual compact candidate bytes"

branch=release-evidence
full_ref="refs/heads/$branch"
ref_endpoint="repos/$repository/git/ref/heads/$branch"
genesis_path="evidence/genesis.v1.json"
remote_prefix="evidence/releases/$version"
remote_names=(release-evidence-bundle.json index.md metrics-comparison.json metrics-comparison.md storage-metrics.json parity.json)
local_files=("$bundle_root" "$index_md" "$metrics_comparison_json" "$metrics_comparison_md" "$storage_metrics_json" "$parity_json")
v1_names_json='["index.md","metrics-comparison.json","metrics-comparison.md","release-evidence.json"]'
remote_names_json=$(printf '%s\n' "${remote_names[@]}" | jq -Rsc 'split("\n")[:-1]')
tuple_relative="publisher-runs/run-$publisher_run_id/attempt-$publisher_run_attempt"
tuple_prefix="$remote_prefix/$tuple_relative"
root_paths=()
tuple_paths=()
for name in "${remote_names[@]}"; do
  root_paths+=("$remote_prefix/$name")
  tuple_paths+=("$tuple_prefix/$name")
done

ref_probe_sequence=0
ref_read_sequence=0
commit_read_sequence=0
blob_verify_sequence=0
mutation_sequence=0
published_tip_validation_sequence=0
ledger_validation_sequence=0
validated_ledger_depth=0
commit_cache_keys=()
commit_tree_cache=()
commit_parents_cache=()
commit_message_cache=()
base_commit=''
base_tree=''
published_commit=''
publication_state=''
ledger_mode=''
initialize_version=false
genesis_mode=false
required_ancestor=''

# Establish exact source existence before probing or mutating ledger state.
assert_source_commit
probe_ref
if [[ "$ref_state" == "absent" ]]; then
  genesis_mode=true
  ledger_mode=evidence-only-parentless-v1
  initialize_version=true
else
  read_ref_sha base_commit
  read_commit "$base_commit" base_tree ignored_parents ignored_message
  base_tree_file="$scratch_dir/base-tree.json"
  read_tree "$base_tree" "$base_tree_file"
  validate_present_ledger "$base_commit" "$base_tree_file"
  base_ledger_depth=$validated_ledger_depth
  directory_state=$(classify_target_version "$base_tree_file")
  case "$directory_state" in
  tuple_complete)
    verify_target_tuple "$base_tree_file" false
    assert_ref_unchanged "$base_commit"
    publication_state=unchanged
    emit_result "$base_commit"
    exit 0
    ;;
  tuple_absent) initialize_version=false ;;
  absent) initialize_version=true ;;
  legacy_version) release_die "refusing to convert an existing v1 evidence version directory" ;;
  *) release_die "remote v2 evidence lineage is partial, conflicting, or unsafe" ;;
  esac
  [[ "$base_ledger_depth" -lt 64 ]] ||
    release_die "evidence lineage reached the 64-commit validation bound; refusing to mutate before a reviewed checkpoint migration"
  assert_ref_unchanged "$base_commit"
fi

mutation_paths=()
mutation_shas=()
created_sha=''
existing_object_sha=''
created_tree=''
created_commit=''
observed_tree=''
observed_parents=''
observed_message=''
observed_ref=''

# Metadata bytes are reused at root and attempt paths, while content-addressed
# objects are created only when the current ledger tree does not already bind
# their exact lowercase digest path.
for index in "${!local_files[@]}"; do
  create_blob "${local_files[$index]}" created_sha "${remote_names[$index]}"
  if [[ "$initialize_version" == "true" ]]; then
    mutation_paths+=("${root_paths[$index]}")
    mutation_shas+=("$created_sha")
  fi
  mutation_paths+=("${tuple_paths[$index]}")
  mutation_shas+=("$created_sha")
done

if [[ "$genesis_mode" == "true" ]]; then
  create_blob "$genesis_json" created_sha genesis
  mutation_paths+=("$genesis_path")
  mutation_shas+=("$created_sha")
fi

for index in "${!object_remote_paths[@]}"; do
  object_present=false
  if [[ "$genesis_mode" == "false" ]] && jq -e --arg path "${object_remote_paths[$index]}" '
      any(.tree[]; .path == $path and .type == "blob" and .mode == "100644")
    ' "$base_tree_file" >/dev/null; then
    blob_sha_for_path "$base_tree_file" "${object_remote_paths[$index]}" existing_object_sha
    verify_remote_blob "$existing_object_sha" "${object_local_files[$index]}" "object ${object_digests[$index]}"
    object_present=true
  fi
  if [[ "$object_present" == "false" ]]; then
    create_blob "${object_local_files[$index]}" created_sha "object ${object_digests[$index]}"
    mutation_paths+=("${object_remote_paths[$index]}")
    mutation_shas+=("$created_sha")
  fi
done

mutation_paths_json=$(printf '%s\n' "${mutation_paths[@]}" | jq -Rsc 'split("\n")[:-1]')
mutation_shas_json=$(printf '%s\n' "${mutation_shas[@]}" | jq -Rsc 'split("\n")[:-1]')
create_tree created_tree
created_tree_file="$scratch_dir/created-tree.json"
read_tree "$created_tree" "$created_tree_file"
if [[ "$genesis_mode" == "true" ]]; then
  verify_initial_tree_local "$created_tree_file"
else
  release_evidence_assert_controlled_blobs_preserved "$base_tree_file" "$created_tree_file" ||
    release_die "created evidence tree rewrote or removed an earlier controlled blob"
  if [[ "$ledger_mode" == "evidence-only-parentless-v1" ]]; then
    validate_evidence_only_tree "$created_tree_file"
  else
    validate_evidence_namespace "$created_tree_file"
    current_immutable=$(jq -c '[.tree[] | select(
      ((.path|ascii_downcase) == "evidence" or
       (.path|ascii_downcase) == "evidence/releases" or ((.path|ascii_downcase)|startswith("evidence/releases/")) or
       (.path|ascii_downcase) == "evidence/objects" or ((.path|ascii_downcase)|startswith("evidence/objects/")) or
       (.path|ascii_downcase) == "evidence/genesis.v1.json") | not)] | sort_by(.path)' "$created_tree_file")
    base_immutable=$(jq -c '[.tree[] | select(
      ((.path|ascii_downcase) == "evidence" or
       (.path|ascii_downcase) == "evidence/releases" or ((.path|ascii_downcase)|startswith("evidence/releases/")) or
       (.path|ascii_downcase) == "evidence/objects" or ((.path|ascii_downcase)|startswith("evidence/objects/")) or
       (.path|ascii_downcase) == "evidence/genesis.v1.json") | not)] | sort_by(.path)' "$base_tree_file")
    [[ "$current_immutable" == "$base_immutable" ]] || release_die "created legacy-compatible tree changed immutable historical paths"
  fi
  created_directory_state=$(classify_target_version "$created_tree_file")
  [[ "$created_directory_state" == "tuple_complete" ]] || release_die "created v2 evidence tree is incomplete: $created_directory_state"
  verify_target_tuple "$created_tree_file" "$initialize_version"
fi

create_commit "$created_tree" created_commit
read_commit "$created_commit" observed_tree observed_parents observed_message
[[ "$observed_tree" == "$created_tree" ]] || release_die "created evidence commit tree changed"
if [[ "$genesis_mode" == "true" ]]; then
  [[ "$observed_parents" == "[]" ]] || release_die "created genesis commit is not parentless"
  [[ "$observed_message" == "chore(evidence): create ledger at $version from $source_sha" ]] || release_die "created genesis commit message changed"
  create_initial_ref "$created_commit"
else
  [[ "$observed_parents" == "[\"$base_commit\"]" ]] || release_die "created evidence commit parent changed"
  [[ "$observed_message" == "chore(evidence): publish $version from $source_sha" ]] || release_die "created evidence commit message changed"
  update_ref "$created_commit"
fi

if [[ -z "$published_commit" ]]; then published_commit=$created_commit; fi
validate_published_tip "$published_commit" "$([[ "$genesis_mode" == "true" ]] && printf genesis || printf append)"
probe_ref
[[ "$ref_state" == "present" ]] || release_die "evidence reference disappeared after publication"
read_ref_sha observed_ref
[[ "$observed_ref" == "$published_commit" ]] || release_die "evidence reference changed after publication"
emit_result "$published_commit"
