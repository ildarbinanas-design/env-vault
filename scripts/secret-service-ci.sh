#!/usr/bin/env bash
# Linux CI only. Mount compiled test tools; never mount a user's home/keychain.
set -euo pipefail
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
CGO_ENABLED=0 go build -o "$work/env-vault" ./cmd/env-vault
CGO_ENABLED=0 go test -c -o "$work/keyring.test" ./internal/secretstore/keyring
# Debian owns the daemon and D-Bus versions in this integration scenario.
timeout 300 docker run --rm -v "$work:/tools:ro" debian:trixie-slim bash -ec '
  apt-get update -qq
  apt-get install -y -qq --no-install-recommends dbus-daemon gnome-keyring python3 >/dev/null
  root=$(mktemp -d)
  export HOME="$root/home" XDG_DATA_HOME="$root/data" XDG_RUNTIME_DIR="$root/run"
  mkdir -p "$HOME" "$XDG_DATA_HOME" "$XDG_RUNTIME_DIR"
  chmod 700 "$XDG_RUNTIME_DIR"
  export ENV_VAULT_SS_DISPOSABLE=1 ENV_VAULT_TEST_CLI=/tools/env-vault
  unset ENV_VAULT_BACKEND ENV_VAULT_ALLOW_INSECURE_TEST_BACKEND ENV_VAULT_TEST_STORE
  dbus-run-session -- bash -ec '\''
    gnome-keyring-daemon --foreground --components=secrets >/dev/null 2>&1 &
    daemon=$!
    trap "kill $daemon 2>/dev/null || true" EXIT
    /tools/keyring.test -test.v -test.run "^TestSecretService" -test.timeout=2m
  '\''
'
