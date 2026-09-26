#!/usr/bin/env bash
# Shared helpers for the temporary audit verification workflow.
# Never print a secret value: values live only in shell variables and the
# keychain; results are exit codes, hashes compared in-shell, and yes/no.
set -u
OUT="${RUNNER_TEMP:-/tmp}/audit-out"; mkdir -p "$OUT"
PY="${PY:-python3}"
sum_line() { echo "$*" | tee -a "${GITHUB_STEP_SUMMARY:-/dev/null}"; }
# run_watch LABEL SECONDS CMD... : run with a KILL watchdog; report exit or HUNG.
run_watch() {
  local label=$1 t=$2; shift 2
  "$@" >"$OUT/$label.out" 2>"$OUT/$label.err" </dev/null &
  local pid=$! i=0
  while kill -0 "$pid" 2>/dev/null && [ "$i" -lt "$t" ]; do sleep 1; i=$((i+1)); done
  if kill -0 "$pid" 2>/dev/null; then
    local sa=no
    if command -v pgrep >/dev/null && pgrep -x SecurityAgent >/dev/null 2>&1; then sa=yes; fi
    kill -9 "$pid" 2>/dev/null; wait "$pid" 2>/dev/null
    sum_line "| $label | HUNG>${t}s (SecurityAgent=$sa) |"
    return 124
  fi
  wait "$pid"; local rc=$?
  sum_line "| $label | exit=$rc |"
  return $rc
}
# run_stdin LABEL SECONDS CMD... : like run_watch but feeds $SECRET_VALUE on stdin.
run_stdin() {
  local label=$1 t=$2; shift 2
  printf '%s' "$SECRET_VALUE" | "$@" >"$OUT/$label.out" 2>"$OUT/$label.err" &
  local pid=$! i=0
  while kill -0 "$pid" 2>/dev/null && [ "$i" -lt "$t" ]; do sleep 1; i=$((i+1)); done
  if kill -0 "$pid" 2>/dev/null; then
    local sa=no
    if command -v pgrep >/dev/null && pgrep -x SecurityAgent >/dev/null 2>&1; then sa=yes; fi
    kill -9 "$pid" 2>/dev/null; wait "$pid" 2>/dev/null
    sum_line "| $label | HUNG>${t}s (SecurityAgent=$sa) |"
    return 124
  fi
  wait "$pid"; local rc=$?
  sum_line "| $label | exit=$rc |"
  return $rc
}
# exec_hash LABEL SECONDS BIN ARGS... : exec child that prints only sha256 of AUDIT_VAR.
exec_hash() {
  local label=$1 t=$2; shift 2
  run_watch "$label" "$t" "$@" -- "$PY" -c 'import os,hashlib;print(hashlib.sha256(os.environ["AUDIT_VAR"].encode()).hexdigest())'
  local rc=$?
  local want got
  want=$(printf '%s' "$SECRET_VALUE" | "$PY" -c 'import sys,hashlib;print(hashlib.sha256(sys.stdin.buffer.read()).hexdigest())')
  got=$(tr -d '\r\n' <"$OUT/$label.out" 2>/dev/null | tail -c 64)
  if [ "$rc" -eq 0 ] && [ "$want" = "$got" ]; then sum_line "| $label value-match | yes |"; else sum_line "| $label value-match | no |"; fi
  return $rc
}
# leak_scan : count occurrences of the value in every captured output file.
leak_scan() {
  local hits=0 f
  for f in "$OUT"/*; do
    [ -f "$f" ] || continue
    if grep -F -q -- "$SECRET_VALUE" "$f"; then hits=$((hits+1)); sum_line "| LEAK in $(basename "$f") | YES |"; fi
  done
  sum_line "| leak-scan files-with-value | $hits |"
}
# show_json LABEL : print a value-free projection of the JSON envelope.
show_json() {
  local f="$OUT/$1.out"
  if grep -F -q -- "$SECRET_VALUE" "$f" "$OUT/$1.err" 2>/dev/null; then echo "$1: output withheld (value present)"; return; fi
  echo "$1 stdout: $(head -c 600 "$f")"; echo "$1 stderr: $(head -c 400 "$OUT/$1.err")"
}
