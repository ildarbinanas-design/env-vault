#!/usr/bin/env bash

# Shared fail-closed predicates for both the legacy v1 listener and the v2
# publisher. Callers own transport and traversal; these helpers keep namespace,
# controlled-blob, and inherited-path semantics identical across both routes.

release_evidence_validate_namespace() {
  local tree_file=$1
  local v1_names_json=$2
  local v2_names_json=$3
  local validator=$4
  jq -e --argjson v1 "$v1_names_json" --argjson v2 "$v2_names_json" \
    -f "$validator" "$tree_file" >/dev/null
}

release_evidence_assert_controlled_blobs_preserved() {
  local parent_tree_file=$1
  local child_tree_file=$2
  jq -e -n --slurpfile parent "$parent_tree_file" --slurpfile child "$child_tree_file" '
    def controlled:
      .path == "evidence/genesis.v1.json" or
      (.path | startswith("evidence/releases/")) or
      (.path | startswith("evidence/objects/sha256/"));
    all($parent[0].tree[];
      if .type == "blob" and controlled then
        . as $entry |
        any($child[0].tree[];
          .path == $entry.path and .type == $entry.type and .mode == $entry.mode and .sha == $entry.sha)
      else true end)
  ' >/dev/null
}

release_evidence_non_evidence_projection() {
  local tree_file=$1
  jq -c '[.tree[] | select(
    ((.path|ascii_downcase) == "evidence" or
     (.path|ascii_downcase) == "evidence/releases" or ((.path|ascii_downcase)|startswith("evidence/releases/")) or
     (.path|ascii_downcase) == "evidence/objects" or ((.path|ascii_downcase)|startswith("evidence/objects/")) or
     (.path|ascii_downcase) == "evidence/genesis.v1.json") | not)] | sort_by(.path)' "$tree_file"
}

release_evidence_anchor_count() {
  local tree_file=$1
  jq '[.tree[] | select((.path|ascii_downcase) == "evidence/genesis.v1.json")]|length' "$tree_file"
}

release_evidence_version_entry_count() {
  local tree_file=$1
  jq '[.tree[] | select((.path|ascii_downcase)|startswith("evidence/releases/v"))]|length' "$tree_file"
}

release_evidence_controlled_entry_count() {
  local tree_file=$1
  jq '[.tree[] | select(
    (.path|ascii_downcase) == "evidence/releases" or
    ((.path|ascii_downcase)|startswith("evidence/releases/")) or
    (.path|ascii_downcase) == "evidence/objects" or
    ((.path|ascii_downcase)|startswith("evidence/objects/")) or
    (.path|ascii_downcase) == "evidence/genesis.v1.json")]|length' "$tree_file"
}
