#!/usr/bin/env bash
set -euo pipefail

usage() {
  echo "usage: build-e2e-reporters.sh MATRIX_JSON OUTPUT_DIRECTORY" >&2
  exit 2
}

write_sha256() {
  local filename="$1"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$filename"
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$filename"
  else
    echo "no SHA-256 implementation is available" >&2
    return 1
  fi
}

download_tool_modules() {
  if command -v timeout >/dev/null 2>&1; then
    timeout --foreground --signal=TERM --kill-after=15s 2m \
      env GOTOOLCHAIN=local GOWORK=off \
      GOFLAGS='' \
      go -C "$tool_module" mod download
  else
    GOTOOLCHAIN=local GOWORK=off GOFLAGS='' go -C "$tool_module" mod download
  fi
}

[[ $# -eq 2 ]] || usage

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
matrix_path="$1"
output_path="$2"
tool_module="$repo_root/tools/e2e-reporter"
tooling_source="$repo_root/e2e/cmd/e2e-runner/tooling.go"

[[ ! -e "$tool_module/vendor" && ! -L "$tool_module/vendor" ]] || {
  echo "reporter tool module vendor directory is not allowed" >&2
  exit 1
}

[[ -f "$matrix_path" && ! -L "$matrix_path" ]] || {
  echo "native matrix must be a regular non-symlink file: $matrix_path" >&2
  exit 1
}
[[ ! -e "$output_path" && ! -L "$output_path" ]] || {
  echo "reporter output already exists: $output_path" >&2
  exit 1
}

output_parent="$(dirname "$output_path")"
mkdir -p "$output_parent"
output_parent="$(cd "$output_parent" && pwd -P)"
output_path="$output_parent/$(basename "$output_path")"

jq -e '
  .include | type == "array" and length == 5 and
  ([.[].id] | sort) == [
    "darwin-amd64",
    "darwin-arm64",
    "linux-amd64",
    "linux-arm64",
    "windows-amd64"
  ] and
  ([.[].id] | unique | length) == 5 and
  all(.[];
    (.id | type == "string") and
    (.goos | type == "string") and
    (.goarch | type == "string") and
    .id == (.goos + "-" + .goarch)
  )
' "$matrix_path" >/dev/null

downloaded=false
for download_attempt in 1 2 3; do
  if download_tool_modules; then
    downloaded=true
    break
  fi
  echo "gotestsum module download attempt ${download_attempt}/3 failed" >&2
  if [[ $download_attempt -lt 3 ]]; then
    sleep "$download_attempt"
  fi
done
[[ "$downloaded" == true ]] || {
  echo "gotestsum module download exhausted 3 bounded attempts" >&2
  exit 1
}

GOTOOLCHAIN=local GOWORK=off GOFLAGS='' GOPROXY=off go -C "$tool_module" mod verify
GOTOOLCHAIN=local GOWORK=off GOFLAGS='' GOPROXY=off go -C "$tool_module" mod tidy -diff

module_path="gotest.tools/gotestsum"
module_version="$(
  GOTOOLCHAIN=local GOWORK=off GOFLAGS='' GOPROXY=off \
    go -C "$tool_module" list -mod=readonly -m -f '{{.Version}}' "$module_path"
)"
[[ "$module_version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || {
  echo "gotestsum module version is not an exact stable version: $module_version" >&2
  exit 1
}
module_sum="$(
  awk -v module="$module_path" -v version="$module_version" \
    '$1 == module && $2 == version && $3 ~ /^h1:/ { print $3 }' \
    "$tool_module/go.sum"
)"
[[ -n "$module_sum" && "$module_sum" != *$'\n'* ]] || {
  echo "gotestsum module checksum is missing or ambiguous" >&2
  exit 1
}
tool_go_version="$(awk '$1 == "go" { print "go" $2 }' "$tool_module/go.mod")"
current_go_version="$(GOTOOLCHAIN=local GOFLAGS='' go env GOVERSION)"
[[ -n "$tool_go_version" && "$tool_go_version" != *$'\n'* && "$tool_go_version" == "$current_go_version" ]] || {
  echo "tool module Go version does not match the active exact toolchain" >&2
  exit 1
}

runner_pin="$(
  sed -nE 's/^[[:space:]]*gotestsumModuleVersion[[:space:]]*=[[:space:]]*"([^"]+)".*/\1/p' "$tooling_source"
)"
[[ -n "$runner_pin" && "$runner_pin" != *$'\n'* && "$runner_pin" == "${module_path}@${module_version}" ]] || {
  echo "E2E runner reporter pin does not match the isolated tool module" >&2
  exit 1
}
runner_sum="$(
  sed -nE 's/^[[:space:]]*gotestsumModuleSum[[:space:]]*=[[:space:]]*"([^"]+)".*/\1/p' "$tooling_source"
)"
[[ -n "$runner_sum" && "$runner_sum" != *$'\n'* && "$runner_sum" == "$module_sum" ]] || {
  echo "E2E runner reporter checksum does not match the isolated tool module" >&2
  exit 1
}

mkdir "$output_path"
built=0
while IFS=$'\t' read -r platform_id goos goarch; do
  [[ "$platform_id" =~ ^(linux-(amd64|arm64)|darwin-(amd64|arm64)|windows-amd64)$ ]] || {
    echo "unsupported reporter platform: $platform_id" >&2
    exit 1
  }
  binary_name="gotestsum"
  if [[ "$goos" == "windows" ]]; then
    binary_name="gotestsum.exe"
  fi
  platform_dir="$output_path/$platform_id"
  binary_path="$platform_dir/$binary_name"
  mkdir "$platform_dir"

  GOTOOLCHAIN=local GOWORK=off GOFLAGS='' GOPROXY=off CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
    go -C "$tool_module" build \
      -mod=readonly \
      -trimpath \
      -buildvcs=false \
      -ldflags='-s -w' \
      -o "$binary_path" \
      "$module_path"

  [[ -f "$binary_path" && ! -L "$binary_path" ]] || {
    echo "reporter build did not produce a regular binary: $binary_path" >&2
    exit 1
  }
  build_info="$(go version -m "$binary_path")"
  [[ "${build_info%%$'\n'*}" == *": $tool_go_version" ]] || {
    echo "reporter compiler version does not match $tool_go_version" >&2
    exit 1
  }
  grep -Fqx $'\tpath\t'"$module_path" <<<"$build_info"
  awk -F $'\t' -v module="$module_path" -v version="$module_version" -v sum="$module_sum" '
    $2 == "mod" && $3 == module && $4 == version && $5 == sum { matches++ }
    END { exit matches == 1 ? 0 : 1 }
  ' <<<"$build_info"
  if grep -Eq $'^\t=>\t' <<<"$build_info"; then
    echo "reporter build contains replacement metadata" >&2
    exit 1
  fi
  grep -Fqx $'\tbuild\tCGO_ENABLED=0' <<<"$build_info"
  grep -Fqx $'\tbuild\tGOOS='"$goos" <<<"$build_info"
  grep -Fqx $'\tbuild\tGOARCH='"$goarch" <<<"$build_info"

  (
    cd "$platform_dir"
    write_sha256 "$binary_name" >"$binary_name.sha256"
  )
  built=$((built + 1))
done < <(jq -r '.include[] | [.id, .goos, .goarch] | @tsv' "$matrix_path")

[[ $built -eq 5 ]] || {
  echo "reporter bundle contains $built platforms, want 5" >&2
  exit 1
}

echo "built exact gotestsum ${module_version} reporters for ${built} platforms"
