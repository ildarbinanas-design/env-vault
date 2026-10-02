"""Portable smoke: random values, stdin input, in-memory output and digest checks."""
import hashlib
import json
import os
from pathlib import Path
import secrets
import subprocess
import sys
import tempfile

with tempfile.TemporaryDirectory(prefix="env-vault-smoke-") as root:
    value = secrets.token_hex(32).encode()
    env = dict(os.environ, ENV_VAULT_BACKEND="test",
               ENV_VAULT_ALLOW_INSECURE_TEST_BACKEND="1",
               ENV_VAULT_TEST_STORE=str(Path(root) / "store"))
    config = str(Path(root) / "config.yaml")

    def run(*args, stdin=b"", want=0):
        result = subprocess.run([sys.argv[1], "--config", config, *args],
                                input=stdin, capture_output=True, env=env,
                                cwd=root, timeout=30, check=False)
        if value in result.stdout + result.stderr:
            raise SystemExit("FAIL: generated sensitive value leaked to command output")
        if result.returncode != want:
            raise SystemExit(f"FAIL: command exit {result.returncode}, expected {want}")
        return result.stdout

    run("secret", "set", "nexus-token", "--stdin", stdin=value)
    run("profile", "create", "dev")
    run("profile", "add", "dev", "nexus-token:NPM_TOKEN")
    digest = run("--quiet", "exec", "dev", "--", sys.executable, "-c",
                 'import hashlib,os; print(hashlib.sha256(os.environ["NPM_TOKEN"].encode()).hexdigest())')
    if digest.strip().decode() != hashlib.sha256(value).hexdigest():
        raise SystemExit("FAIL: exec received a different value")
    for mode in ("--json", "--jsonl"):
        output = json.loads(run(mode, "secret", "check", "nexus-token"))
        if not output["ok"]:
            raise SystemExit("FAIL: secret check did not succeed")
    missing = json.loads(run("--json", "secret", "check", "missing-secret", want=3))
    if missing["error"]["code"] != "MISSING_SECRET":
        raise SystemExit("FAIL: missing-secret code changed")
    metadata = Path(root) / "metadata.json"
    run("--dry-run", "--json", "--output", str(metadata), "exec", "dev",
        "--", sys.executable, "-c", "raise SystemExit(42)")
    raw = metadata.read_bytes()
    if value in raw:
        raise SystemExit("FAIL: generated sensitive value leaked to metadata")
    if not json.loads(raw)["data"]["dry_run"]:
        raise SystemExit("FAIL: dry-run metadata missing")
print("smoke ok")
