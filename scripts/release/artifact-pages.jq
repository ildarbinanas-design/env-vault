def env_vault_artifacts:
  . as $pages |
  if ($pages | type) != "array" or ($pages | length) == 0 then
    error("artifact pagination must contain at least one page")
  elif all($pages[];
    type == "object" and
    has("total_count") and
    (.total_count | type) == "number" and
    .total_count >= 0 and
    (.total_count | floor) == .total_count and
    has("artifacts") and
    (.artifacts | type) == "array"
  ) | not then
    error("artifact pagination contains a malformed page envelope")
  elif ([$pages[].total_count] | unique | length) != 1 then
    error("artifact pagination total_count values disagree")
  else
    [$pages[] | .artifacts[]] as $artifacts |
    if ($artifacts | length) != $pages[0].total_count then
      error("artifact pagination is incomplete")
    elif ([$artifacts[].id] | length) != ([$artifacts[].id] | unique | length) then
      error("artifact pagination contains duplicate artifact IDs")
    else
      $artifacts
    end
  end;

def env_vault_exact_artifact($name; $run_id; $head_sha):
  [env_vault_artifacts[] | select(
    .name == $name and
    .expired == false and
    .workflow_run.id == $run_id and
    .workflow_run.head_sha == $head_sha
  )] as $matches |
  if ($matches | length) != 1 then
    error("exact artifact is missing or ambiguous")
  else
    $matches[0]
  end;
