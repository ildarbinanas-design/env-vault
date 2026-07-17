def env_vault_homebrew_state:
  [inputs] as $lines |
  select(($lines | length) == 13) |
  select(all($lines[]; test("^[a-z_]+=.*$"))) |
  [$lines[] | capture("^(?<key>[a-z_]+)=(?<value>.*)$")] as $rows |
  select(([$rows[].key] | sort) == [
    "already_merged",
    "base_branch",
    "base_sha",
    "branch",
    "head_sha",
    "merge_is_ancestor_of_tap",
    "merge_sha",
    "no_op",
    "pr_number",
    "pr_url",
    "source_sha",
    "state",
    "tap_sha"
  ]) |
  reduce $rows[] as $row ({}; .[$row.key] = $row.value);
