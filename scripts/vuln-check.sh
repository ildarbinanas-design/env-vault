#!/bin/sh
set -eu

# Reports known vulnerabilities that env-vault's code reaches, in its
# dependencies and in the standard library of the Go toolchain that runs the
# check. CI runs it with the exact Go version from go.mod, the one release.yml
# builds with. It analyzes the build for the host platform, so code that builds
# only for macOS or Windows is not covered.
tool_version="v1.8.0"
tool_dir="$(mktemp -d)"

cleanup() {
  rm -rf "$tool_dir"
}
trap cleanup EXIT HUP INT TERM

GOBIN="$tool_dir" go install "golang.org/x/vuln/cmd/govulncheck@${tool_version}"
"$tool_dir/govulncheck" ./...

echo "vulnerability check passed with govulncheck ${tool_version}"
