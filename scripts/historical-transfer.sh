#!/usr/bin/env bash
# Deliberately fixed: changing this baseline requires an explicit reviewed edit.
set -euo pipefail
baseline=a79a02b6c079ccaa136b634ea6329a626f316b85
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
mkdir "$work/source"
git -C "$work/source" init -q
git -C "$work/source" fetch --quiet --depth=1 https://github.com/ildarbinanas-design/env-vault.git "$baseline"
[[ $(git -C "$work/source" rev-parse FETCH_HEAD) == "$baseline" ]]
git -C "$work/source" checkout --quiet --detach FETCH_HEAD
(cd "$work/source" && go build -o "$work/baseline" ./cmd/env-vault)
go build -o "$work/current" ./cmd/env-vault
ENV_VAULT_HISTORICAL_TEST=1 ENV_VAULT_BASELINE_CLI="$work/baseline" \
  ENV_VAULT_CURRENT_CLI="$work/current" \
  go test ./internal/cli -run '^TestHistoricalTransfer$' -count=1 -timeout=2m -v
