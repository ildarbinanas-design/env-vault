# Design

## Architecture

env-vault is a Go CLI with a small package boundary:

- `internal/cli`: Cobra command wiring and global flags.
- `internal/config`: YAML config schema, paths, validation, mapping parser, and
  cross-platform profile transaction lock.
- `internal/secretstore`: backend-neutral secret interface and non-secret fingerprinting.
- `internal/bundle`: the encrypted transfer container. This is the only package
  that performs cryptography, and it depends on no other env-vault package.
- `internal/atomicfile`: symlink-safe `0600` publication of a file through a
  synced temporary sibling.
- `internal/secretstore/keyring`: production OS keychain backend using `github.com/99designs/keyring`.
- `internal/secretstore/teststore`: explicitly gated insecure backend for tests only.
- `internal/runner`: exec resolver, env collision checks, process launch, exit-code propagation, and signal forwarding.
- `internal/output`: human, JSON, JSONL, and `--output` envelope rendering.
- `internal/errors`: structured error contract.
- `internal/platform`: config path and platform helpers.

## SecretStore Interface

The interface supports `Set`, `Get`, `Exists`, `Delete`, and `List`. Commands never expose `Get` to the user. `Get` supplies values for child environment injection, encrypted export, and explicit write verification. `check` and `list` use metadata only.

Production storage uses `github.com/99designs/keyring` with an explicit allowlist: macOS Keychain, Secret Service, KWallet, Windows Credential Manager, and `pass`. `pass` is kept after the platform keychain backends so discovery still prefers the native OS stores first. `keyring.FileBackend`, plaintext/env-file storage, and Passwork are not production backends.

The public record ID identifies a stored record and is not derived from the
secret value:

```text
sha256(service + "\x00" + secretName), truncated to 16 hex chars
```

It is therefore identical before and after an overwrite and gives nothing to
an offline guess of a low-entropy value. JSON reports it as `record_id` and,
until a later minor release, as the deprecated alias `fingerprint`.
`secret set` reports `created` or `overwritten`; `--verify` reads the value
back and compares it in constant time, failing with `SECRET_UNVERIFIED` on a
mismatch. That error, and a backend error during the read-back, report that
the backend acknowledged the write, with `created` or `overwritten` based on
the existence check before the write. This is not an atomic create/overwrite
result. Persistence is unconfirmed: a previous value may have been replaced,
and the command does not roll back the write.
`Exists` answers from the backend key listing that `List` uses, so
`secret check` never decrypts a value. On Windows Credential Manager, which
matches target names without regard to case, it compares secret names the same
way. A store can also report the largest value its backend keeps
(`ValueLimiter`: 2560 bytes on Credential Manager). The keyring store's `Set`
refuses a larger value before it reaches the backend, which `secret set`
reports as `SECRET_TOO_LARGE`, and `import` refuses a container before its
first write. A value-derived digest, a keyed MAC, and
backend modification times were rejected for #77: the first allows offline
guessing and all of them either read every value for metadata commands or are
not available on every production backend.

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
keyring adapter repeats both validations before opening a backend. With `pass`,
service names cannot contain slashes because they share one path with the
secret name; a slashed service never selects `pass` from the default backend
list. Environment
variables must match `[A-Za-z_][A-Za-z0-9_]*`; names that differ only by case
are treated as the same portable target because Windows environment names are
case-insensitive.

Config saves reject a symlink target and publish a mode `0600` temporary sibling
with `fsync` followed by a same-directory replace. The replace is atomic on Unix;
on Windows, where the Go standard library does not promise rename atomicity,
env-vault retries only transient sharing/access violations on reads and
replacement within a fixed deadline and never removes the prior file first.
This prevents truncation, partial reads, and writes through a tracked config
symlink. Profile
create/add/remove wrap the
complete load, mutation, validation, and same-directory save in an exclusive
lock from
`github.com/gofrs/flock` (the version pinned in `go.mod`), verified by the
unchanged cross-platform E2E contract. The adjacent `<config>.lock` file is created with mode `0600`,
rechecked as a non-symlink regular file, and intentionally kept after unlock so
all processes continue to coordinate on one inode. Acquisition retries every
25 milliseconds for at most five seconds (or the caller's earlier deadline),
then returns `CONFIG_LOCKED`. Dry runs do not create a lock. A requested secret
existence check completes before the config transaction begins.

## Doctor

`doctor` is a read-only diagnostic: it never mutates config and never takes
the profile lock (`config.LoadForRead`, no `flock` acquisition). It reports
`config_path`, `config_exists`, the resolved `backend` (`keyring`, `pass`, or
`test`), and `test_backend`. A config load failure still returns the standard
error envelope; a secret-backend initialization or listing failure is
non-fatal and surfaces as a `warnings` entry instead, so a backend problem
alone does not fail the command. It uses the same success/error envelope as
every other command (see Output Schema) and never prints a secret value.

## Exec Flow

1. Validate the `--` delimiter and child argv.
2. Load profile mappings if a profile is supplied.
3. Parse direct `--secret <secret-name:ENV_NAME>` mappings.
4. Validate duplicate mappings and environment collisions.
5. Resolve all required secrets before spawning the child.
6. Build the child environment from inherited env or `--clean-env`.
7. Spawn the command directly without a shell.
8. Inherit child stdin/stdout/stderr by default.
9. On Unix, forward SIGTERM, SIGHUP, SIGINT, and SIGQUIT to the child. While
   env-vault and the child are both in the terminal's foreground process
   group, SIGINT and SIGQUIT are not forwarded: the terminal already delivered
   them to the child, and forwarding would deliver each one twice. env-vault
   cannot tell a keypress from `kill`, so a SIGINT or SIGQUIT sent only to
   env-vault in that situation is not forwarded either. A SIGHUP or SIGINT
   inherited as ignored (as under `nohup`) is not subscribed to, so it stays
   ignored for env-vault and the child alike.
10. Propagate the child exit code. On Unix, a child killed by SIGHUP, SIGINT,
    SIGTERM, or SIGKILL ends env-vault with the same signal, so a calling shell
    loop stops as it would without env-vault. Other signals, a SIGHUP or SIGINT
    inherited as ignored, and running as PID 1 exit with 128+n instead. A
    failed child is recorded in the `--output` file as `COMMAND_FAILED` (see
    Output Schema); stdout and stderr carry only the child's own output,
    except that `--verbose` reports `OUTPUT_WRITE_FAILED` if the file cannot
    be written. The previous record then stays.

`env-vault exec ... -- bash -lc ...` is allowed because the user explicitly supplied the shell.

## Transfer Container

`export` seals stored secret values; `import` restores them. The format is
documented in
[ADR 0010](adr/0010-encrypted-secret-transfer-container.md).

```json
{
  "schema": "env-vault.bundle.v1",
  "version": 1,
  "created_at": "2026-08-14T10:00:00Z",
  "tool_version": "v0.1.0",
  "kdf": {
    "algorithm": "argon2id",
    "salt": "<base64, 16 bytes>",
    "time": 3,
    "memory_kib": 65536,
    "parallelism": 4,
    "key_length": 32
  },
  "cipher": { "algorithm": "aes-256-gcm", "nonce": "<base64, 12 bytes>" },
  "payload": "<base64 ciphertext with tag>"
}
```

The header is the additional authenticated data. It carries no secret names:
those are inside the ciphertext. The cleartext header still reveals the
creation time, tool version and encryption parameters; the file length is also
visible. Declared key-derivation parameters are
bounds-checked before a key is derived — `memory_kib` in [8192, 1048576],
`time` in [1, 10], `parallelism` in [1, 8], `key_length` exactly 32, salt 16
bytes, nonce 12 bytes, ciphertext at most 16 MiB, file at most 24 MiB — so a
declared memory cost is capped at 1 GiB before authentication. This bounds the
allocation; it does not make an untrusted container cheap to open. Salt and
nonce are drawn fresh per export.

Sealing enforces the same size limits before key derivation and encryption.
It counts the serialized payload plus the 16-byte GCM tag, then the complete
JSON envelope including the base64 ciphertext and escaped header fields.
Oversized exports return `BUNDLE_INVALID` before creating or replacing the
output file; the container format and opening limits remain unchanged.

Both the container and decrypted payload must contain exactly one JSON document.
Only whitespace may follow it. Unknown fields and repeated fields, including
escaped spellings and case aliases, are rejected at every object level.

The plaintext is a JSON object holding one entry per secret, each with its
keychain service, name, and base64 value. Entries are unique by
the exact `(service, name)` pair in the portable format. Before the first
write, import also checks the selected backend's identity. Windows compares
the complete `keyring:<service>:<name>` TargetName with the operating system's
case-insensitive ordinal comparison, verified against native Credential
Manager tests. A collision inside the container returns `BUNDLE_INVALID` for
every conflict policy. Other backends keep their existing identity rules.

The container carries values only. Profile mappings are not included because
`.env-vault.yaml` holds no values and is already portable through the
repository.

Neither command reads or writes a config file. Export covers
`secretstore.DefaultService` plus any service named with `--with-services`,
which accepts a comma-separated list and may be repeated; a keychain cannot
enumerate services, so custom ones must be named. The flag is deliberately not
called `--service`: on `secret set` that name replaces the default service,
whereas here the default is always included and the listed services are added
to it. Import applies `--on-conflict fail|skip|overwrite` per secret. Identity,
metadata, conflict, and size checks complete before any `Set`. This preflight
is not a transaction: a later write can fail, and another process can change
the backend between checks and writes.

### Native record identity

The Secret Service adapter retains the dependency's collection paths, `profile`
attribute, and JSON Item encoding for existing records. All operations use
that collection and attribute; labels remain display text. Missing attributes
are ignored, missing records return absence, and metadata or access errors
remain backend errors. Multiple physical records for one identity return
`BACKEND_UNAVAILABLE` with guidance to inspect the keychain; env-vault never
chooses or repairs a duplicate. Metadata listing does not call `GetSecret`.

The Windows adapter projects only generic credential metadata from
`CredEnumerateW` with flags zero, preserves enumeration errors, and checks the
complete namespace/service boundary. `ERROR_NOT_FOUND` alone means an empty
listing. The 2560-byte value limit and existing TargetName encoding remain.

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

A command that `exec` ran and that failed is recorded only in the `--output`
file. Unlike other errors, it keeps the exec metadata in `data`, with the
status in `exit_code` and, for a signal, its name in `signal`:

```json
{"ok":false,"command":"exec","timestamp":"RFC3339","data":{"argv":["make","test"],"clean_env":false,"dry_run":false,"exit_code":143,"override_env":false,"secret_count":1,"secrets":[{"env":"NEXUS_TOKEN","fingerprint":"<record id>","name":"nexus-token","record_id":"<record id>"}],"signal":"SIGTERM"},"warnings":[],"error":{"code":"COMMAND_FAILED","message":"Command was killed by SIGTERM (status 143)","remediation":"Inspect the command's output"}}
```

### Metadata files

`--output` must use a separate path from configs, their lock files, and input
or output transfer containers. Conflicts return `USAGE` without writing to that
path. Malformed flags leave metadata files untouched because their paths could
not be fully checked.

The target must be a regular file or a new path. Missing directories are
created with mode `0700`; a synced mode-`0600` temporary sibling is renamed into
place. The directory must be writable, and the file belongs to whoever ran
env-vault. Symlinks, devices and pipes are refused. On Windows, replacement
blocked by another program's open handle is retried for up to one second.

## Version

`version` and `--version` print the version, short commit and commit date.
`--json version` adds the full commit, commit time, modified-tree flag, Go
version and platform. These describe the Go build information, also inspectable
with `go version -m`.

A checkout between releases reports a pseudo-version, ending in `+dirty` when
the build tree was modified. A build without Git information omits commit
fields; one without a module version reports `dev`. This output helps diagnosis
but cannot authenticate a binary. Use the pinned attestation verification in
[README.md](../README.md#manual-download--linux-and-windows) for provenance.

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

The module requires the exact stable Go 1.26.8 patch. That version was selected
from the official [Go release history](https://go.dev/doc/devel/release), and
the migration follows the [Go 1.26 release notes](https://go.dev/doc/go1.26).
CI reads the version from `go.mod`, so the compiler recorded in every artifact
is the compiler that actually ran its checks.

## Release Artifact Builds

Use the latest published GitHub Release. The failed immutable tags v0.0.8
through v0.0.11 intentionally have no Release; v0.3.3 was never tagged or
published, and its changes shipped in v0.3.4.

Releases follow [ADR 0011](adr/0011-minimal-release-pipeline.md) and
[ADR 0012](adr/0012-attestation-verification-pins-release-workflow.md).
`.github/workflows/release.yml` is the only workflow that creates tags,
releases, or Homebrew updates, and no workflow runs on a tag.

On every push to `main`, the `release-please` job runs Release Please with
`RELEASE_PLANNING_TOKEN` from the `release-planning` environment. It keeps the
release pull request current. When a release pull request has merged, it tags
the merge commit and opens a draft GitHub Release. Runs on `main` queue and are
never cancelled, but GitHub does not guarantee their order, so the job then
looks for a draft release whose tag points to the run's own commit. Only that
run builds, which binds every attestation to the exact released commit.

The `build` job checks out that commit with its tags and fails unless the tag
points to it. It builds the five targets without `-ldflags -X` and requires the
Go build information to report an unmodified tree and the tag as the module
version, which `--version` must print. It uploads each binary before it runs
anything else, then smoke-tests the platform's real secret store.

Darwin release artifacts support macOS 15+ and are built on macOS GitHub-hosted
runners with `CGO_ENABLED=1` because the macOS Keychain backend requires
CGO-enabled darwin binaries. Linux and Windows artifact builds remain
`CGO_ENABLED=0`.

The `publish` job runs in the `release` environment and is the only job with
write permissions: `contents: write` for the release, and `id-token: write` and
`attestations: write` for the attestations. It packages the five archives
deterministically and attests the archives and the binaries. It verifies the
attestations with the release workflow, `main`, the release commit, and
GitHub-hosted runners pinned. It uploads the ten files to the draft without
ever replacing an asset, then publishes it. Immutable releases then lock the
assets and the tag.

The `verify` job downloads the published assets, checks their checksums and
attestations, and requires the release to be published, immutable, and tagged
at the release commit. Only then does the `tap` job, also in the `release`
environment, generate the formula from the published archives. It opens a pull
request in `homebrew-tap` with `HOMEBREW_TAP_TOKEN` and enables auto-merge. The
tap's ruleset requires its `test` check, which runs style, audit, installation,
and version checks and compares every url and sha256 with the published
checksums.

The two release tokens are independent fine-grained tokens.
`RELEASE_PLANNING_TOKEN` is scoped to `env-vault`, and only the
`release-please` job references it. `HOMEBREW_TAP_TOKEN` is scoped to
`homebrew-tap`, and only the `tap` job references it; the workflow tests
enforce both. Pull request and manual runs build and package without
publishing and receive neither token. Release automation uses no GitHub App.

The release audit trail is the GitHub Releases page, the attestations, and git
and pull-request history. The append-only evidence ledger that earlier releases
published was retired on 2026-07-30. The owner removed its `release-evidence`
branch and protecting ruleset on 2026-10-03; neither is part of the current
pipeline. Historical ADRs describe the old design, and the code that replayed
the ledger is at tag `pre-trim-2026-07-30`. Existing evidence artifacts remain
frozen history. Required
external settings and credential rotation procedures are documented in
`docs/release-external-settings.md`.
