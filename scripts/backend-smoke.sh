#!/usr/bin/env bash
# Smoke-test env-vault against the operating system's real secret store:
# Windows Credential Manager, a throwaway macOS keychain, or Linux pass with a
# throwaway GPG key. The E2E suite runs on the gated test backend; this proves
# that the production backends store, read, overwrite, and delete a value.
# Values are random and never printed or passed in argv; the value a child
# sees through exec is compared by SHA-256.
set -euo pipefail

usage() {
  echo "usage: backend-smoke.sh ENV_VAULT_BINARY" >&2
  exit 2
}

[[ $# -eq 1 && -f "$1" && -x "$1" ]] || usage
bin="$(cd "$(dirname "$1")" && pwd -P)/$(basename "$1")"
work="$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/env-vault-smoke.XXXXXX")"
out="$work/out"
mkdir "$out"
name="smoke-$(openssl rand -hex 4)"
first="$(openssl rand -hex 24)"
second="$(openssl rand -hex 24)"
py=python3
command -v python3 >/dev/null 2>&1 || py=python
failures=0
failed_labels=()
cleanup_steps=()

cleanup() {
  local step
  for step in ${cleanup_steps[@]+"${cleanup_steps[@]}"}; do
    eval "$step" >/dev/null 2>&1 || true
  done
  [[ -n "$work" && -d "$work" ]] && rm -rf -- "$work"
}
trap cleanup EXIT

fail() {
  echo "FAIL: $*"
  failures=$((failures + 1))
}

failed_step() {
  failed_labels+=("$1")
  shift
  fail "$@"
}

sha256_of() {
  "$py" -c 'import hashlib, sys; print(hashlib.sha256(sys.stdin.buffer.read()).hexdigest())'
}

# run LABEL SECONDS CMD... runs CMD with stdin passed through and a hard KILL
# deadline, because a keychain prompt nobody answers would block forever.
run() {
  local label=$1 limit=$2 pid waited=0
  shift 2
  "$@" 0<&0 >"$out/$label.out" 2>"$out/$label.err" &
  pid=$!
  while kill -0 "$pid" 2>/dev/null && [[ $waited -lt $limit ]]; do
    sleep 1
    waited=$((waited + 1))
  done
  if kill -0 "$pid" 2>/dev/null; then
    kill -9 "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
    echo "$label: no answer within ${limit}s"
    return 124
  fi
  wait "$pid"
}

expect() {
  local label=$1 want=$2
  shift 2
  local status=0
  run "$label" 60 "$@" || status=$?
  if [[ $status -eq $want ]]; then
    echo "ok: $label (exit $status)"
  else
    failed_step "$label" "$label exited $status, want $want"
  fi
}

set_value() {
  local label=$1 value=$2
  shift 2
  local status=0
  printf '%s' "$value" | run "$label" 60 "$bin" --json secret set --stdin "$@" "$name" || status=$?
  if [[ $status -eq 0 ]]; then
    echo "ok: $label"
  else
    failed_step "$label" "$label exited $status"
  fi
}

expect_exec_value() {
  local label=$1 value=$2 want got status=0
  want="$(printf '%s' "$value" | sha256_of)"
  run "$label" 60 "$bin" exec --secret "$name:ENV_VAULT_SMOKE" -- "$py" -c \
    'import hashlib, os; print(hashlib.sha256(os.environ["ENV_VAULT_SMOKE"].encode()).hexdigest())' </dev/null || status=$?
  got="$(tr -d '\r\n' <"$out/$label.out")"
  if [[ $status -eq 0 && "$got" == "$want" ]]; then
    echo "ok: $label delivered the stored value"
  else
    failed_step "$label" "$label exited $status or delivered a different value"
  fi
}

setup_pass() {
  command -v pass >/dev/null 2>&1 || { echo "pass is not installed" >&2; exit 1; }
  # gpg-agent sockets fail on long paths, so keep GNUPGHOME short.
  GNUPGHOME="$(mktemp -d /tmp/evg.XXXXXX)"
  export GNUPGHOME
  cleanup_steps+=("gpgconf --kill gpg-agent" "rm -rf -- '$GNUPGHOME'")
  chmod 700 "$GNUPGHOME"
  cat >"$work/key" <<'EOF'
%no-protection
Key-Type: eddsa
Key-Curve: ed25519
Subkey-Type: ecdh
Subkey-Curve: cv25519
Name-Real: env-vault smoke
Name-Email: smoke@env-vault.invalid
Expire-Date: 1d
%commit
EOF
  gpg --batch --quiet --gen-key "$work/key" 2>/dev/null
  local fingerprint
  fingerprint="$(gpg --list-keys --with-colons smoke@env-vault.invalid 2>/dev/null | awk -F: '/^fpr:/ { print $10; exit }')"
  export PASSWORD_STORE_DIR="$work/store"
  pass init "$fingerprint" >/dev/null 2>&1
  export ENV_VAULT_BACKEND=pass
}

setup_macos_keychain() {
  local keychain="$work/smoke.keychain-db" password default searched
  password="$(openssl rand -hex 16)"
  default="$(security default-keychain -d user | tr -d ' "')"
  searched="$(security list-keychains -d user | tr -d '"' | xargs)"
  security create-keychain -p "$password" "$keychain"
  cleanup_steps+=("security default-keychain -d user -s '$default'" "security list-keychains -d user -s $searched" "security delete-keychain '$keychain'")
  security set-keychain-settings "$keychain"
  security unlock-keychain -p "$password" "$keychain"
  # shellcheck disable=SC2086 # the search list is a space-separated path list
  security list-keychains -d user -s "$keychain" $searched
  security default-keychain -d user -s "$keychain"
  MACOS_KEYCHAIN="$keychain"
}

# Items env-vault creates trust no application, so reading them opens a
# Keychain prompt nobody can answer here. Create the item for the read checks
# through security -i, trusting this binary; the value goes over stdin, not argv.
macos_precreate() {
  printf 'add-generic-password -s env-vault -a %s -l %s -w %s -T %s %s\n' \
    "$name" "$name" "$first" "$bin" "$MACOS_KEYCHAIN" | security -i >/dev/null 2>"$out/precreate.err" ||
    fail "security add-generic-password failed"
}

case "$(uname -s)" in
  Linux)
    setup_pass
    set_value create "$first"
    ;;
  Darwin)
    setup_macos_keychain
    # Creating, checking, listing, and deleting never read a value, so they
    # must not prompt even for an item env-vault itself created.
    probe="$name"
    name="$probe-created"
    set_value create-new "$first"
    expect check-new 0 "$bin" --json secret check "$name"
    expect delete-new 0 "$bin" --json secret delete "$name" --confirm "$name"
    name="$probe"
    macos_precreate
    ;;
  MINGW* | MSYS* | CYGWIN*)
    cleanup_steps+=("'$bin' secret delete '$name' --confirm '$name'")
    set_value create "$first"
    ;;
  *)
    echo "unsupported operating system: $(uname -s)" >&2
    exit 1
    ;;
esac

expect check 0 "$bin" --json secret check "$name"
expect list 0 "$bin" --json secret list
grep -Fq "\"name\":\"$name\"" "$out/list.out" || fail "list does not show $name"
expect_exec_value exec "$first"
set_value overwrite-verify "$second" --verify
grep -Fq '"action":"overwritten"' "$out/overwrite-verify.out" || fail "overwrite was not reported"
grep -Fq '"verified":true' "$out/overwrite-verify.out" || fail "overwrite was not verified"
expect_exec_value exec-after-overwrite "$second"
expect delete 0 "$bin" --json secret delete "$name" --confirm "$name"
expect check-after-delete 3 "$bin" --json secret check "$name"

# The patterns come from a pipe, so the values never appear in argv.
leaked=false
if grep -rFq -f <(printf '%s\n' "$first" "$second") "$out"; then
  leaked=true
  fail "a stored value appeared in env-vault output"
fi

if [[ $failures -ne 0 ]]; then
  # env-vault's structured errors carry codes and messages, never values;
  # show them only when the scan above found no value in any output.
  if [[ $leaked == false ]]; then
    for label in ${failed_labels[@]+"${failed_labels[@]}"}; do
      echo "--- $label"
      head -c 600 "$out/$label.out" 2>/dev/null || true
      head -c 600 "$out/$label.err" 2>/dev/null || true
      echo
    done
  fi
  echo "backend smoke failed: $failures check(s)"
  exit 1
fi
echo "backend smoke passed on $(uname -s)"
