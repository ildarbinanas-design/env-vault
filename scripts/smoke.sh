#!/usr/bin/env sh

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)" || exit 1
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/env-vault-smoke.XXXXXX")" || exit 1
cleanup() {
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT INT TERM

BIN="$TMP_DIR/env-vault"
CONFIG="$TMP_DIR/config.yaml"
OUT="$TMP_DIR/out.txt"
META="$TMP_DIR/meta.json"
EXPECTED="$TMP_DIR/expected.txt"
SECRET_VALUE="$(od -An -N32 -tx1 /dev/urandom | tr -d ' \n')" || exit 1
STORE_ROOT="${ENV_VAULT_TEST_STORE:-$TMP_DIR}"

if [ -d "$STORE_ROOT" ]; then
  STORE="$STORE_ROOT/store.gob"
else
  STORE="$STORE_ROOT"
fi

if [ -z "$SECRET_VALUE" ]; then
  printf '%s\n' "failed to generate ephemeral test value" >&2
  exit 1
fi

umask 077
printf '%s' "$SECRET_VALUE" >"$EXPECTED" || exit 1

cd "$ROOT_DIR" || exit 1
go build -o "$BIN" ./cmd/env-vault || exit 1

env_vault() {
  ENV_VAULT_BACKEND=test \
    ENV_VAULT_ALLOW_INSECURE_TEST_BACKEND=1 \
    ENV_VAULT_TEST_STORE="$STORE" \
    "$BIN" "$@"
}

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
  : >"$OUT" || exit 1
  "$@" >"$OUT" 2>&1
}

capture_with_secret_stdin() {
  : >"$OUT" || exit 1
  printf '%s\n' "$SECRET_VALUE" | "$@" >"$OUT" 2>&1
}

capture_with_secret_stdin env_vault --config "$CONFIG" secret set nexus-token --stdin || exit 1
assert_no_secret_outputs

capture env_vault --config "$CONFIG" profile create dev || exit 1
assert_no_secret_outputs

capture env_vault --config "$CONFIG" profile add dev nexus-token:NPM_TOKEN || exit 1
assert_no_secret_outputs

env_vault --config "$CONFIG" exec dev -- sh -c 'expected="$(cat "$1")"; test "$NPM_TOKEN" = "$expected"' sh "$EXPECTED" >"$OUT" 2>&1 || exit 1
assert_no_secret_outputs

capture env_vault --json --config "$CONFIG" secret check nexus-token || exit 1
assert_no_secret_outputs
grep -F '"ok":true' "$OUT" >/dev/null || exit 1
grep -F 'nexus-token' "$OUT" >/dev/null || exit 1

capture env_vault --jsonl --config "$CONFIG" secret check nexus-token || exit 1
assert_no_secret_outputs
grep -F '"ok":true' "$OUT" >/dev/null || exit 1
grep -F 'nexus-token' "$OUT" >/dev/null || exit 1

if env_vault --json --config "$CONFIG" secret check missing-secret >"$OUT" 2>&1; then
  printf '%s\n' "missing-secret check unexpectedly succeeded" >&2
  exit 1
fi
assert_no_secret_outputs
grep -F 'MISSING_SECRET' "$OUT" >/dev/null || exit 1

capture env_vault --dry-run --json --output "$META" --config "$CONFIG" exec dev -- sh -c 'exit 42' || exit 1
assert_no_secret_outputs
grep -F '"dry_run":true' "$META" >/dev/null || exit 1

printf '%s\n' "smoke ok"
