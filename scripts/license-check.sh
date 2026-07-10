#!/bin/sh
set -eu

tool_version="v2.0.1"
module_path="github.com/ildarbinanas-design/env-vault"
allowed_licenses="Apache-2.0,BSD-2-Clause,BSD-3-Clause,ISC,MIT"
tool_dir="$(mktemp -d "${TMPDIR:-/tmp}/env-vault-go-licenses.XXXXXX")"

cleanup() {
  rm -rf "$tool_dir"
}
trap cleanup EXIT HUP INT TERM

GOBIN="$tool_dir" go install "github.com/google/go-licenses/v2@${tool_version}"
"$tool_dir/go-licenses" check ./cmd/env-vault \
  --ignore "$module_path" \
  --allowed_licenses "$allowed_licenses"

echo "license check passed with go-licenses ${tool_version}"
