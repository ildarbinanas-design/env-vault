def version: "v(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)";
def safe_run:
  test("^run-[1-9][0-9]*$") and
  (.[4:] as $n | ($n|length) < 16 or (($n|length) == 16 and $n <= "9007199254740991"));
def safe_attempt:
  test("^attempt-[1-9][0-9]*$") and
  (.[8:] as $n | ($n|length) < 10 or (($n|length) == 10 and $n <= "2147483647"));
([.tree[] | select(
  ((.path|ascii_downcase) == "evidence") or
  ((.path|ascii_downcase) == "evidence/releases") or ((.path|ascii_downcase)|startswith("evidence/releases/")) or
  ((.path|ascii_downcase) == "evidence/objects") or ((.path|ascii_downcase)|startswith("evidence/objects/")) or
  ((.path|ascii_downcase) == "evidence/genesis.v1.json")
)]) as $e |
([ $e[] | select(.type == "tree" and (.path | test("^evidence/releases/" + version + "$"))) | .path ] | sort) as $versions |
([ $e[] | select(.type == "blob" and (.path | test("^evidence/objects/sha256/[0-9a-f]{64}\\.gz$"))) ]) as $objects |
($versions|length) > 0 and
([$e[].path]|length) == ([$e[].path|ascii_downcase]|unique|length) and
([ $e[] | select(.path == "evidence" and .type == "tree" and .mode == "040000") ]|length) == 1 and
([ $e[] | select(.path == "evidence/releases" and .type == "tree" and .mode == "040000") ]|length) == 1 and
([ $e[] | select(.path == "evidence/genesis.v1.json" and .type == "blob" and .mode == "100644") ]|length) <= 1 and
(if ($objects|length) > 0 then
   ([ $e[] | select(.path == "evidence/objects" and .type == "tree" and .mode == "040000") ]|length) == 1 and
   ([ $e[] | select(.path == "evidence/objects/sha256" and .type == "tree" and .mode == "040000") ]|length) == 1
 else
   ([ $e[] | select(.path == "evidence/objects" or .path == "evidence/objects/sha256") ]|length) == 0
 end) and
all($e[];
  if .type == "tree" then .mode == "040000" and (
    .path == "evidence" or .path == "evidence/releases" or .path == "evidence/objects" or
    .path == "evidence/objects/sha256" or
    (.path | test("^evidence/releases/" + version + "$")) or
    (.path | test("^evidence/releases/" + version + "/publisher-runs$")) or
    ((.path | capture("^evidence/releases/" + version + "/publisher-runs/(?<run>run-[^/]+)$")) as $m | $m.run | safe_run) or
    ((.path | capture("^evidence/releases/" + version + "/publisher-runs/(?<run>run-[^/]+)/(?<attempt>attempt-[^/]+)$")) as $m |
      ($m.run | safe_run) and ($m.attempt | safe_attempt))
  ) elif .type == "blob" then .mode == "100644" and (
    .path == "evidence/genesis.v1.json" or
    (.path | test("^evidence/objects/sha256/[0-9a-f]{64}\\.gz$")) or
    ((.path | capture("^evidence/releases/" + version + "/(?<name>[^/]+)$")) as $m |
      any(($v1 + $v2)[]; . == $m.name)) or
    ((.path | capture("^evidence/releases/" + version + "/publisher-runs/(?<run>run-[^/]+)/(?<attempt>attempt-[^/]+)/(?<name>[^/]+)$")) as $m |
      ($m.run | safe_run) and ($m.attempt | safe_attempt) and any(($v1 + $v2)[]; . == $m.name))
  ) else false end)
and all($versions[]; . as $prefix |
  ([ $e[] | select(.type == "blob" and (.path | startswith($prefix + "/"))) |
    .path[($prefix|length)+1:] as $r | select($r | contains("/") | not) | {name:$r,sha:.sha} ] | sort_by(.name)) as $root |
  ([ $root[].name ] | sort) as $root_names |
  (if $root_names == ($v1|sort) then $v1 elif $root_names == ($v2|sort) then $v2 else [] end) as $names |
  ([ $e[] | select(.type == "tree" and (.path | startswith($prefix + "/publisher-runs/run-"))) |
    .path[($prefix|length)+1:] | select(test("^publisher-runs/run-[^/]+$")) ] | sort) as $runs |
  ([ $e[] | select(.type == "tree" and (.path | startswith($prefix + "/publisher-runs/run-"))) |
    .path[($prefix|length)+1:] | select(test("^publisher-runs/run-[^/]+/attempt-[^/]+$")) ] | sort) as $attempts |
  ([ $e[] | select(.type == "blob" and (.path | startswith($prefix + "/publisher-runs/"))) |
    {relative:.path[($prefix|length)+1:],sha:.sha} ]) as $lineage |
  ($names|length) > 0 and
  ([ $e[] | select(.path == ($prefix + "/publisher-runs") and .type == "tree" and .mode == "040000") ]|length) == 1 and
  ($runs|length) > 0 and ($attempts|length) > 0 and
  all($runs[]; split("/")[1] | safe_run) and
  all($attempts[]; (split("/")[1] | safe_run) and (split("/")[2] | safe_attempt)) and
  all($runs[]; . as $run | any($attempts[]; startswith($run + "/"))) and
  all($attempts[]; . as $attempt |
    any($runs[]; . as $run | $attempt | startswith($run + "/")) and
    ([ $lineage[] | select(.relative | startswith($attempt + "/")) | .relative | split("/")[3] ] | sort) == ($names|sort)) and
  any($attempts[]; . as $attempt |
    all($names[]; . as $name |
      ([ $root[] | select(.name == $name) | .sha ][0]) ==
      ([ $lineage[] | select(.relative == ($attempt + "/" + $name)) | .sha ][0])))
)
