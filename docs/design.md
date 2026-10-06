# Design

The normative behavior of env-vault and of its release pipeline is specified
in [`openspec/specs/`](../openspec/specs/). This document describes the
architecture and the reasons behind it; when it and a specification differ,
the specification and its tests win.

| Specification | Scope |
|---|---|
| [cli-output](../openspec/specs/cli-output/spec.md) | commands, global flags, envelope, error and exit codes, `--output`, dry run, `version`, `doctor` |
| [secret-storage](../openspec/specs/secret-storage/spec.md) | backends, test gate, names, record IDs, `secret` commands |
| [profile-config](../openspec/specs/profile-config/spec.md) | YAML schema, config selection, safe writes, locking, `profile` commands |
| [exec](../openspec/specs/exec/spec.md) | resolution, child environment, signals, exit status, result reporting |
| [secret-transfer](../openspec/specs/secret-transfer/spec.md) | `export`, `import` and the `env-vault.bundle.v1` container |
| [release-process](../openspec/specs/release-process/spec.md) | pull request gates, planning, authorization, build, attestation, publication |
| [homebrew-distribution](../openspec/specs/homebrew-distribution/spec.md) | formula, tap checks and installation |

## Architecture

env-vault is a Go CLI with a small package boundary:

- `internal/cli`: Cobra command wiring and global flags.
- `internal/config`: YAML config schema, paths, validation, mapping parser, and
  cross-platform profile transaction lock.
- `internal/secretstore`: backend-neutral secret interface and record IDs.
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

## Secret store

The store interface has `Set`, `Get`, `Exists`, `Delete` and `List`. No
command exposes `Get`: it supplies values only for child injection, encrypted
export and explicit write verification. `Exists` answers from the key listing
that `List` uses, so metadata commands never decrypt a value and, on macOS,
never ask for Keychain access to an item. `pass` stays last in the backend
allowlist so that discovery prefers the native OS stores.

The record ID is derived from the service and name only. A value-derived
digest, a keyed MAC, and backend modification times were rejected for #77:
the first allows offline guessing of low-entropy values, and all of them
either read every value for metadata commands or are not available on every
production backend.

## Config writes

Profile mutations publish a complete temporary sibling and replace the target
in the same directory, so readers never see a truncated file and a tracked
`.env-vault.yaml` symlink cannot redirect a write. The adjacent lock file is
kept after unlock: removing it would let concurrent processes lock different
inodes for one config. The lock comes from `github.com/gofrs/flock`. Windows
does not promise rename atomicity, so env-vault retries only transient
sharing and access violations and never deletes the previous file first.

## Exec

`exec` runs the argv directly. A shell is involved only when the user names
one, which keeps quoting and injection under the user's control. Unix signal
forwarding skips SIGINT and SIGQUIT while env-vault and the child share the
terminal's foreground group, because the terminal already delivered them and
forwarding would deliver each one twice; env-vault cannot tell a keypress
from `kill`. A child killed by a terminating signal makes env-vault end with
the same signal so that a calling shell loop stops as it would without
env-vault.

## Transfer container

The format and its threat model are decided in
[ADR 0010](adr/0010-encrypted-secret-transfer-container.md). The container
carries values only: `.env-vault.yaml` holds no values and is already
portable, so copying mappings into the container would only widen what a
leaked container discloses. The flag that adds services to an export is
deliberately not called `--service`: on `secret set` that name replaces the
default service, whereas export always includes the default.

## Generic scope

env-vault is generic because local automation often mixes package registries, SaaS APIs, CI emulation, private services, and development tools. The core abstraction is `secret-name -> ENV_NAME`, not a cloud provider.

## Module path

The public module path is:

```text
github.com/ildarbinanas-design/env-vault
```

The module requires the exact stable Go 1.26.8 patch. That version was selected
from the official [Go release history](https://go.dev/doc/devel/release), and
the migration follows the [Go 1.26 release notes](https://go.dev/doc/go1.26).
CI reads the version from `go.mod`, so the compiler recorded in every artifact
is the compiler that actually ran its checks.

## Releases

Releases follow [ADR 0011](adr/0011-minimal-release-pipeline.md),
[ADR 0012](adr/0012-attestation-verification-pins-release-workflow.md) and
the [release-process](../openspec/specs/release-process/spec.md)
specification; [RELEASING.md](../RELEASING.md) is the operator runbook.
Darwin binaries are built with cgo on macOS runners because the Keychain
backend requires it; Linux and Windows binaries are built without cgo.

The release audit trail is the GitHub Releases page, the attestations, and git
and pull-request history. The append-only evidence ledger that earlier releases
published was retired on 2026-07-30. The owner removed its `release-evidence`
branch and protecting ruleset on 2026-10-03; neither is part of the current
pipeline. Historical ADRs describe the old design, and the code that replayed
the ledger is at tag `pre-trim-2026-07-30`. Existing evidence artifacts remain
frozen history.
