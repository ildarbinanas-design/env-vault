# Design

## Architecture

env-vault is a Go CLI with a small package boundary:

- `internal/cli`: Cobra command wiring and global flags.
- `internal/config`: YAML config schema, paths, validation, and mapping parser.
- `internal/secretstore`: backend-neutral secret interface and non-secret fingerprinting.
- `internal/secretstore/keyring`: production OS keychain backend using `github.com/99designs/keyring`.
- `internal/secretstore/teststore`: explicitly gated insecure backend for tests only.
- `internal/runner`: exec resolver, env collision checks, process launch, exit-code propagation, and signal forwarding.
- `internal/output`: human, JSON, JSONL, and `--output` envelope rendering.
- `internal/redact`: last-resort string redaction for diagnostics.
- `internal/errors`: structured error contract.
- `internal/platform`: config path and platform helpers.

## SecretStore Interface

The interface supports `Set`, `Get`, `Exists`, `Delete`, and `List`. Commands never expose `Get` to the user. `Get` exists only so `exec` can inject values into the child environment.

The public metadata fingerprint is not derived from secret values:

```text
sha256(service + "\x00" + secretName), truncated to 16 hex chars
```

## Config Schema

```yaml
version: 1
profiles:
  dev:
    description: local development
    secrets:
      - name: nexus-token
        env: NPM_TOKEN
        required: true
```

Secret names allow letters, digits, dot, underscore, dash, slash, and at-sign. Colon, newline, and control characters are rejected. Environment variables must match `[A-Za-z_][A-Za-z0-9_]*`.

## Exec Flow

1. Validate the `--` delimiter and child argv.
2. Load profile mappings if a profile is supplied.
3. Parse direct `--secret <secret-name:ENV_NAME>` mappings.
4. Validate duplicate mappings and environment collisions.
5. Resolve all required secrets before spawning the child.
6. Build the child environment from inherited env or `--clean-env`.
7. Spawn the command directly without a shell.
8. Inherit child stdin/stdout/stderr by default.
9. Best-effort forward process signals.
10. Propagate the child exit code where possible.

`env-vault exec ... -- bash -lc ...` is allowed because the user explicitly supplied the shell.

## Output Schema

Success:

```json
{"ok":true,"command":"secret_set","timestamp":"RFC3339","data":{},"warnings":[],"error":null}
```

Error:

```json
{"ok":false,"command":"exec","timestamp":"RFC3339","data":null,"warnings":[],"error":{"code":"MISSING_SECRET","message":"Missing secret: nexus-token","remediation":"Run: env-vault secret set nexus-token"}}
```

Human errors use the same fields: `code`, `message`, and `remediation`.

## Dry Run

`--dry-run` validates without mutation or child execution.

- `secret set` validates name and backend selection but does not read or store input.
- profile mutations validate and report planned metadata but do not write config.
- `exec` validates mappings, missing secrets, env collisions, and child argv but does not inject values or run the child.

## Generic Scope

env-vault is generic because local automation often mixes package registries, SaaS APIs, CI emulation, private services, and development tools. The core abstraction is `secret-name -> ENV_NAME`, not a cloud provider.

## Module Path

The public module path is:

```text
github.com/ildarbinanas-design/env-vault
```
