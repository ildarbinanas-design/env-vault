#!/usr/bin/env bash
# Full CLI scenario against the platform's production keychain backend.
# Usage: scenario.sh <env-vault binary> <timeout seconds>
. "$(dirname "$0")/lib.sh"
BIN=$1; T=${2:-30}
SUF=$(openssl rand -hex 4); N="audit-$SUF"
export SECRET_VALUE; SECRET_VALUE=$(openssl rand -hex 24)
WORK="$RUNNER_TEMP/scn-$SUF"; mkdir -p "$WORK"; cd "$WORK" || exit 1
cleanup() { "$BIN" secret delete "$N" --confirm "$N" >/dev/null 2>&1 || true; }
trap cleanup EXIT
sum_line "### scenario on $RUNNER_OS/$RUNNER_ARCH, backend=default"
sum_line "| step | result |"; sum_line "|---|---|"
run_watch s00-version "$T" "$BIN" --json version; show_json s00-version
run_watch s01-doctor "$T" "$BIN" --json doctor; show_json s01-doctor
run_stdin s02-set-create "$T" "$BIN" --json secret set --stdin "$N"; show_json s02-set-create
run_watch s03-check "$T" "$BIN" --json secret check "$N"; show_json s03-check
run_watch s04-list "$T" "$BIN" --json secret list; show_json s04-list
exec_hash s05-exec-direct "$T" "$BIN" exec --secret "$N:AUDIT_VAR"
SECRET_VALUE=$(openssl rand -hex 24)
run_stdin s06-set-overwrite-verify "$T" "$BIN" --json secret set --stdin --verify "$N"; show_json s06-set-overwrite-verify
exec_hash s07-exec-after-overwrite "$T" "$BIN" exec --secret "$N:AUDIT_VAR"
run_watch s08-profile-create "$T" "$BIN" --json profile create auditp --local; show_json s08-profile-create
run_watch s09-profile-add "$T" "$BIN" --json profile add auditp "$N:AUDIT_VAR" --check-secret; show_json s09-profile-add
run_watch s10-profile-show "$T" "$BIN" --json profile show auditp; show_json s10-profile-show
exec_hash s11-exec-profile "$T" "$BIN" exec auditp
run_watch s12-exec-exitcode "$T" "$BIN" exec --secret "$N:AUDIT_VAR" -- "$PY" -c 'import sys; sys.exit(7)'
run_watch s13-delete "$T" "$BIN" --json secret delete "$N" --confirm "$N"; show_json s13-delete
run_watch s14-check-after-delete "$T" "$BIN" --json secret check "$N"; show_json s14-check-after-delete
# the config file must contain only mappings
if grep -F -q -- "$SECRET_VALUE" .env-vault.yaml 2>/dev/null; then sum_line "| config contains value | YES |"; else sum_line "| config contains value | no |"; fi
leak_scan
