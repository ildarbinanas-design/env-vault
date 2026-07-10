#!/bin/sh
set -eu

tool_version="v2.0.1"
module_path="github.com/ildarbinanas-design/env-vault"
allowed_licenses="Apache-2.0,BSD-2-Clause,BSD-3-Clause,ISC,MIT"
tool_dir="$(mktemp -d)"
gobin="$tool_dir"
tool_path="$tool_dir/go-licenses"

case "$(go env GOHOSTOS)" in
  windows)
    gobin="$(cygpath -w "$tool_dir")"
    tool_path="$tool_dir/go-licenses.exe"
    ;;
  darwin | linux) ;;
  *)
    echo "unsupported license-check host: $(go env GOHOSTOS)" >&2
    exit 1
    ;;
esac

cleanup() {
  rm -rf "$tool_dir"
}
trap cleanup EXIT HUP INT TERM

GOBIN="$gobin" go install "github.com/google/go-licenses/v2@${tool_version}"
"$tool_path" check ./cmd/env-vault \
  --ignore "$module_path" \
  --allowed_licenses "$allowed_licenses"

echo "license check passed with go-licenses ${tool_version}"
