#!/usr/bin/env sh
set -eu

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/env-vault-smoke.XXXXXX")"
cleanup() {
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT INT TERM

BIN="$TMP_DIR/env-vault"
CONFIG="$TMP_DIR/config.yaml"
STORE="$TMP_DIR/store.gob"
OUT="$TMP_DIR/out.txt"
META="$TMP_DIR/meta.json"
EXPECTED="$TMP_DIR/expected.txt"
SECRET_VALUE="$(od -An -N32 -tx1 /dev/urandom | tr -d ' \n')"

if [ -z "$SECRET_VALUE" ]; then
  printf '%s\n' "failed to generate ephemeral test value" >&2
  exit 1
fi

umask 077
printf '%s' "$SECRET_VALUE" >"$EXPECTED"

cd "$ROOT_DIR"
go build -o "$BIN" ./cmd/env-vault

export ENV_VAULT_BACKEND=test
export ENV_VAULT_ALLOW_INSECURE_TEST_BACKEND=1
export ENV_VAULT_TEST_STORE="$STORE"

assert_no_secret_file() {
  file="$1"
  label="$2"
  if [ ! -f "$file" ]; then
    return 0
  fi
  if grep -F -- "$SECRET_VALUE" "$file" >/dev/null 2>&1; then
    printf '%s\n' "generated sensitive value leaked to $label" >&2
    exit 1
  fi
}

assert_no_secret_outputs() {
  assert_no_secret_file "$OUT" "captured command output"
  assert_no_secret_file "$META" "metadata output"
  assert_no_secret_file "$ROOT_DIR/evidence_bundle.md" "evidence bundle"
}

capture() {
  : >"$OUT"
  "$@" >"$OUT" 2>&1
}

capture_with_secret_stdin() {
  : >"$OUT"
  printf '%s\n' "$SECRET_VALUE" | "$@" >"$OUT" 2>&1
}

capture_with_secret_stdin "$BIN" --config "$CONFIG" secret set nexus-token --stdin
assert_no_secret_outputs

capture "$BIN" --config "$CONFIG" profile create dev
assert_no_secret_outputs

capture "$BIN" --config "$CONFIG" profile add dev nexus-token:NPM_TOKEN
assert_no_secret_outputs

"$BIN" --config "$CONFIG" exec dev -- sh -c 'expected="$(cat "$1")"; test "$NPM_TOKEN" = "$expected"' sh "$EXPECTED" >"$OUT" 2>&1
assert_no_secret_outputs

capture "$BIN" --json --config "$CONFIG" secret check nexus-token
assert_no_secret_outputs
grep -F '"ok":true' "$OUT" >/dev/null
grep -F 'nexus-token' "$OUT" >/dev/null

capture "$BIN" --jsonl --config "$CONFIG" secret check nexus-token
assert_no_secret_outputs
grep -F '"ok":true' "$OUT" >/dev/null
grep -F 'nexus-token' "$OUT" >/dev/null

if "$BIN" --json --config "$CONFIG" secret check missing-secret >"$OUT" 2>&1; then
  printf '%s\n' "missing-secret check unexpectedly succeeded" >&2
  exit 1
fi
assert_no_secret_outputs
grep -F 'MISSING_SECRET' "$OUT" >/dev/null

capture "$BIN" --dry-run --json --output "$META" --config "$CONFIG" exec dev -- sh -c 'exit 42'
assert_no_secret_outputs
grep -F '"dry_run":true' "$META" >/dev/null

printf '%s\n' "smoke ok"
