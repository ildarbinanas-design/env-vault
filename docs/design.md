# Design

## Architecture

env-vault is a Go CLI with a small package boundary:

- `internal/cli`: Cobra command wiring and global flags.
- `internal/config`: YAML config schema, paths, validation, mapping parser, and
  cross-platform profile transaction lock.
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

Production storage uses `github.com/99designs/keyring` with an explicit allowlist: macOS Keychain, Secret Service, KWallet, Windows Credential Manager, and `pass`. `pass` is kept after the platform keychain backends so discovery still prefers the native OS stores first. `keyring.FileBackend`, plaintext/env-file storage, and Passwork are not production backends.

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

Secret names allow letters, digits, dot, underscore, dash, slash, and at-sign.
Slash-separated hierarchy is preserved, but absolute paths, empty components,
`.`/`..` components, backslashes, colon, newline, and control characters are
rejected. Service names use the same path-safety rules, and the production
keyring adapter repeats both validations before opening a backend. Environment
variables must match `[A-Za-z_][A-Za-z0-9_]*`; names that differ only by case
are treated as the same portable target because Windows environment names are
case-insensitive.

Config saves reject a symlink target and publish a mode `0600` temporary sibling
with `fsync` followed by atomic rename. This prevents truncation, partial reads,
and writes through a tracked config symlink. Profile create/add/remove wrap the
complete load, mutation, validation, and atomic save in an exclusive lock from
`github.com/gofrs/flock v0.12.1`, the verified version that retains the project's
Go 1.22 baseline. The adjacent `<config>.lock` file is created with mode `0600`,
rechecked as a non-symlink regular file, and intentionally kept after unlock so
all processes continue to coordinate on one inode. Acquisition retries every
25 milliseconds for at most five seconds (or the caller's earlier deadline),
then returns `CONFIG_LOCKED`. Dry runs do not create a lock. A requested secret
existence check completes before the config transaction begins.

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

## Release Artifact Builds

Release publication is owned by `build-binaries`. A strict `vX.Y.Z` tag push
starts a release, while a manual dispatch from the default branch may supply an
optional `vX.Y.Z` input. A dispatch without a version is build-only. A manual
release creates its tag with the workflow-scoped `GITHUB_TOKEN`, avoiding a
second recursive tag workflow.

Both pull-request CI and releases call `reusable-quality.yml`. Every release
waits for unit tests, vet, race tests, smoke tests, a pinned native
`go-licenses` matrix on Linux, macOS, and Windows, and all platform builds. It
publishes exactly five archives and five matching SHA-256 files. The version is
injected into each binary through Go linker flags.

Darwin release artifacts support macOS 15+ and are built on macOS GitHub-hosted
runners with `CGO_ENABLED=1` because the macOS Keychain backend requires
CGO-enabled darwin binaries. Linux and Windows artifact builds remain
`CGO_ENABLED=0`.

After the GitHub Release succeeds, the workflow generates a combined SPDX SBOM
and GitHub provenance/SBOM attestations for all five archives without changing
the exact ten-asset Release contract. It then generates declarative
`on_macos`/`on_linux` and `on_arm`/`on_intel` URL/checksum blocks. The formula
declares macOS Sequoia as its minimum and installs the archived README, license,
and third-party notices as documentation. A short-lived, repository-scoped
GitHub App token creates or reuses `release/env-vault-vX.Y.Z` in `homebrew-tap`.
The generated pull request changes only `Formula/env-vault.rb` and carries a
marker binding the version, source SHA, and formula digest. The workflow waits for
`test-formula.yml` with `event=pull_request` and the exact PR head SHA before a
squash merge that is guarded by the same head SHA. It then waits for the
workflow with `event=push`, the exact merged/default-branch SHA, and a successful
conclusion. Style, installation, and the installed exact version therefore form
an automated release gate rather than a follow-up operator check.

Only the `homebrew` job declares `environment: release` and can read
`TAP_APP_CLIENT_ID` and `TAP_APP_PRIVATE_KEY`. Build-only, build, Release,
supply-chain, and `health` jobs cannot read those values. The `health` repair is
read-only: it verifies the tag, Release, checksums, attestations, generated
formula, and the exact tap default-branch push run using public repository state
and its read-only workflow token. Required external settings and credential
rotation procedures are documented in `docs/release-external-settings.md`.

A separate manually dispatched `audit-release-app.yml` workflow is the only
non-release consumer of that environment. It requests a metadata-only token,
fails unless the App installation contains exactly `homebrew-tap`, and relies
on the token action's post-step revocation. Run it after installation or key
changes and before the next publication.
