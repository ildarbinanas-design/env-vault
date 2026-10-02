#!/usr/bin/env sh
set -eu
ROOT_DIR="$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT INT TERM
umask 077
cd "$ROOT_DIR"
go build -o "$TMP_DIR/env-vault" ./cmd/env-vault
python3 scripts/smoke.py "$TMP_DIR/env-vault"
