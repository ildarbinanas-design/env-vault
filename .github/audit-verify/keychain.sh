#!/usr/bin/env bash
# macOS Keychain ACL experiments. Nothing is clicked; a dialog shows up as HUNG.
# Usage: keychain.sh <brew-installed env-vault> <source checkout>
. "$(dirname "$0")/lib.sh"
BIN=$(python3 -c 'import os,sys;print(os.path.realpath(sys.argv[1]))' "$1"); SRC=$2; T=20
SUF=$(openssl rand -hex 4)
KC="$RUNNER_TEMP/audit-$SUF.keychain-db"; KCPW=$(openssl rand -hex 16)
security create-keychain -p "$KCPW" "$KC"
security set-keychain-settings "$KC"
security unlock-keychain -p "$KCPW" "$KC"
ORIG=$(security list-keychains -d user | tr -d '"' | xargs)
security list-keychains -d user -s "$KC" $ORIG
security default-keychain -d user -s "$KC"
export SECRET_VALUE
cleanup() { security delete-keychain "$KC" >/dev/null 2>&1 || true; }
trap cleanup EXIT
sum_line "### Keychain ACL experiments ($(sw_vers -productVersion) $(uname -m)), dedicated keychain, T=${T}s"
sum_line "| step | result |"; sum_line "|---|---|"
sum_line "| codesign brew binary | $(codesign -dv "$BIN" 2>&1 | grep -E 'Signature|flags' | tr '\n' ' ') |"

# A. Items created by env-vault itself (TrustedApplications = [] in keyring v1.2.2)
A="auditA-$SUF"; SECRET_VALUE=$(openssl rand -hex 24)
run_stdin A1-set-create "$T" "$BIN" --json secret set --stdin "$A"
run_watch A2-check "$T" "$BIN" --json secret check "$A"
run_watch A3-list "$T" "$BIN" --json secret list
exec_hash A4-exec "$T" "$BIN" exec --secret "$A:AUDIT_VAR"
SECRET_VALUE=$(openssl rand -hex 24)
run_stdin A5-set-overwrite "$T" "$BIN" --json secret set --stdin "$A"
run_stdin A6-set-overwrite-verify "$T" "$BIN" --json secret set --stdin --verify "$A"
run_watch A7-delete "$T" "$BIN" --json secret delete "$A" --confirm "$A"
sum_line "| A partition/ACL dump (names only) | $(security dump-keychain -a "$KC" 2>/dev/null | grep -c 'applications' ) application lines |"

# B. Pre-created item, trusted app = binary path (-T), created through security -i (value via stdin, not argv)
B="auditB-$SUF"; SECRET_VALUE=$(openssl rand -hex 24)
printf 'add-generic-password -s env-vault -a %s -l %s -w %s -T %s %s\n' "$B" "$B" "$SECRET_VALUE" "$BIN" "$KC" | security -i >/dev/null 2>"$OUT/B0.err"; sum_line "| B0-precreate -T | exit=$? |"
run_watch B1-check "$T" "$BIN" --json secret check "$B"
exec_hash B2-exec "$T" "$BIN" exec --secret "$B:AUDIT_VAR"
# B'. same item after adding partition IDs for unsigned/ad-hoc code
security set-generic-password-partition-list -s env-vault -a "$B" -S "apple-tool:,apple:,unsigned:" -k "$KCPW" "$KC" >/dev/null 2>"$OUT/B3.err"; sum_line "| B3-partition-list unsigned: | exit=$? |"
exec_hash B4-exec-after-partition "$T" "$BIN" exec --secret "$B:AUDIT_VAR"
SECRET_VALUE=$(openssl rand -hex 24)
run_stdin B5-set-overwrite-verify "$T" "$BIN" --json secret set --stdin --verify "$B"
exec_hash B6-exec-after-overwrite "$T" "$BIN" exec --secret "$B:AUDIT_VAR"

# C. Pre-created item, -A (any application) + partition list
C="auditC-$SUF"; SECRET_VALUE=$(openssl rand -hex 24)
printf 'add-generic-password -s env-vault -a %s -l %s -w %s -A %s\n' "$C" "$C" "$SECRET_VALUE" "$KC" | security -i >/dev/null 2>"$OUT/C0.err"; sum_line "| C0-precreate -A | exit=$? |"
exec_hash C1-exec "$T" "$BIN" exec --secret "$C:AUDIT_VAR"
security set-generic-password-partition-list -s env-vault -a "$C" -S "apple-tool:,apple:,unsigned:" -k "$KCPW" "$KC" >/dev/null 2>"$OUT/C2.err"; sum_line "| C2-partition-list | exit=$? |"
exec_hash C3-exec-after-partition "$T" "$BIN" exec --secret "$C:AUDIT_VAR"

# D. KeychainTrustApplication=true on a temporary build, then a rebuild (brew upgrade emulation)
cd "$SRC" || exit 1
sed -i '' 's/KeychainSynchronizable: false,/KeychainSynchronizable: false, KeychainTrustApplication: true,/' internal/secretstore/keyring/keyring.go
grep -c 'KeychainTrustApplication: true' internal/secretstore/keyring/keyring.go | sed 's/^/patched lines: /'
go build -trimpath -ldflags="-s -w -X github.com/ildarbinanas-design/env-vault/internal/cli.Version=v0.0.0-audit1" -o "$RUNNER_TEMP/ev-trust1" ./cmd/env-vault
go build -trimpath -ldflags="-s -w -X github.com/ildarbinanas-design/env-vault/internal/cli.Version=v0.0.0-audit2" -o "$RUNNER_TEMP/ev-trust2" ./cmd/env-vault
git checkout -- internal/secretstore/keyring/keyring.go
cp "$RUNNER_TEMP/ev-trust1" "$RUNNER_TEMP/ev-samepath"
sum_line "| D codesign trust1 | $(codesign -dv "$RUNNER_TEMP/ev-trust1" 2>&1 | grep -E 'Signature|flags' | tr '\n' ' ') |"
D="auditD-$SUF"; SECRET_VALUE=$(openssl rand -hex 24)
run_stdin D1-trust-set "$T" "$RUNNER_TEMP/ev-trust1" --json secret set --stdin "$D"
exec_hash D2-trust-exec-same-binary "$T" "$RUNNER_TEMP/ev-trust1" exec --secret "$D:AUDIT_VAR"
run_stdin D3-trust-set-verify-same-binary "$T" "$RUNNER_TEMP/ev-trust1" --json secret set --stdin --verify "$D"
exec_hash D4-trust-exec-copied-same-bytes "$T" "$RUNNER_TEMP/ev-samepath" exec --secret "$D:AUDIT_VAR"
exec_hash D5-trust-exec-rebuilt-binary "$T" "$RUNNER_TEMP/ev-trust2" exec --secret "$D:AUDIT_VAR"
exec_hash D6-trust-exec-brew-binary "$T" "$BIN" exec --secret "$D:AUDIT_VAR"
leak_scan
