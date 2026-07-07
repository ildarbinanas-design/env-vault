# ADR 0001: Secret Backend

## Status

Accepted for local MVP

## Date

2026-07-06

## Context

env-vault is intended to support safe local automation. The primary risk is accidental disclosure of secret values through files, logs, shell history, tests, evidence, or command output.

The MVP needs a Go backend that can target platform credential stores without custom crypto and without a plaintext production fallback.

## Decision

Use `github.com/99designs/keyring` as the primary backend.

The production allowlist includes OS keychain-style backends only:

- macOS Keychain;
- Secret Service;
- KWallet;
- Windows Credential Manager;
- `pass`.

`pass` is allowed after the native platform stores. It requires the `pass` command and an initialized password store. When explicitly selected and unavailable, the CLI returns structured error code `BACKEND_UNAVAILABLE` with remediation to install `pass` or use another supported OS keychain backend.

The `file` and kernel keyctl backends are not used as production fallback in this MVP. `keyring.FileBackend`, plaintext config, and env-file storage require a separate ADR and explicit approval before any production use.

Passwork is not implemented and remains deferred.

The CLI must not expose a `secret get` command or a command-line secret value flag.

## Options Considered

### 99designs/keyring

Pros:

- Go library with uniform `Set`, `Get`, `Remove`, and `Keys`.
- Allows explicit backend allowlisting.
- Supports the desired OS keychain-style backends.

Cons:

- Runtime availability depends on platform keychain services.
- Some backends may need desktop session services.

Selected for MVP.

### zalando/go-keyring

Pros:

- Simple API.
- Strong fit for set/get/delete across common OS stores.
- No C bindings.

Cons:

- Less useful for this MVP because listing support is not part of the same minimal API shape.
- Backend allowlisting is less central to the public API than in 99designs/keyring.

Kept as a fallback candidate only if build or runtime blockers appear.

### node-keytar

Useful inspiration for cross-platform keychain UX, but it is a Node implementation and not suitable as the Go MVP backend.

### KeePassXC CLI

Pros:

- Mature user-managed vault.
- Good fit for users already operating KeePassXC.

Cons:

- External process dependency.
- UX and unlock lifecycle are outside env-vault.
- Not a default OS keychain backend.

Deferred as an optional connector.

### 1Password CLI

Pros:

- Strong secret management UX for teams.
- Good audit and sharing model.

Cons:

- External account and CLI dependency.
- Not an OS keychain default.

Deferred as an optional connector.

### Plaintext/env Files

Rejected for production. Plaintext config or env files would undermine the core safety goal and risk shell history, backups, terminal transcripts, and accidental commits.

### pass

Pros:

- Useful Linux backend for headless or minimal systems that already use password-store.
- Avoids production plaintext fallback while keeping local user-controlled storage.

Cons:

- External `pass` command dependency.
- Requires an initialized password store and user-managed GPG setup.

Accepted as a production backend after native OS keychain-style backends.

### Passwork

Deferred. A Passwork product connector is out of MVP scope and would require separate design, authentication, and approval.

## Consequences

- Production behavior does not fall back to plaintext storage.
- Linux users may need a Secret Service-compatible daemon, desktop keyring, or an initialized `pass` password store.
- Headless CI should use a CI secret manager or the explicitly gated test backend for tests only.
- Future connectors can be added behind explicit backend selection without changing the profile schema.
