# Security

## Secret Handling

env-vault does not print secret values. It does not store secret values in config, logs, docs, CI metadata, JSON output, JSONL output, or evidence bundles.

Secret input is limited to:

- a hidden interactive prompt;
- `--stdin`, which trims exactly one trailing line ending (`\n` or `\r\n`).

There is no `secret get` command and no command-line flag for passing a secret value.

## Transfer Containers

`export` writes every stored secret value to a single encrypted file; `import`
restores them into the keychain on another machine. The format is AES-256-GCM
under a key derived with Argon2id (64 MiB, three passes, four lanes), with the
header authenticated and the key-derivation parameters bounds-checked before
any key is derived. Secret names live inside the ciphertext, so a container
discloses nothing about its contents without the passphrase. See
[ADR 0010](adr/0010-encrypted-secret-transfer-container.md).

A container carries values only. Profile mappings stay in `.env-vault.yaml`,
which holds no values and is meant to be committed, so it is already portable
and duplicating it into the container would only widen what a leaked container
discloses.

Export covers the default keychain service plus any service named explicitly
with `--with-services`. A keychain cannot enumerate the services an application
has used, so a secret stored under a custom service is silently absent from a
container unless that service is named again on export.

The passphrase is read from a hidden terminal prompt only. There is no
passphrase flag, environment variable, or file. Export asks twice and compares,
because a mistyped passphrase produces a container nobody can open. The single
exception is stdin input while the complete insecure test-backend gate is
active, which exists so the E2E suite can round-trip without a terminal.

Understand what a container costs you before writing one:

- **Its strength is the passphrase, not the operating system.** A keychain
  entry is protected by the login session, platform key storage, and OS rate
  limiting. A container file can be attacked offline with no rate limit. Use a
  long passphrase; the enforced 12-character minimum is a floor, not a target.
- **There is no revocation.** Deleting a secret from the keychain destroys it.
  A container that reached a backup, a cloud-sync folder, a git commit, or a
  chat message survives every later rotation, and env-vault cannot know it
  exists. After rotating a secret, destroy the containers that carried it.
- **Keep containers out of version control and synced folders.** Add the
  container path to `.gitignore` and prefer a location outside Dropbox,
  iCloud, OneDrive, and Time Machine coverage.
- **Memory wiping is best-effort.** Passphrases, derived keys, and decrypted
  values are overwritten as soon as they are no longer needed, but Go's garbage
  collector may have copied them, so this narrows the window rather than
  closing it.

Containers are written with mode `0600` through a synced temporary sibling and
a same-directory replacement, and a symlink or non-regular file at the target
is rejected rather than written through. `export` refuses to overwrite an
existing file without `--force`.

## Config

Config files store only profile mappings:

- secret name;
- target environment variable;
- required flag;
- optional profile description.

Config files are created with mode `0600` where applicable. Mutations reject a
config symlink and use a synced temporary sibling plus same-directory
replacement, so a checkout cannot redirect a profile mutation through
`.env-vault.yaml` and readers do not observe a truncated file. On Windows,
transient sharing/access violations on reads and replacement receive bounded
retries without deleting the prior config first.

The Windows coverage E2E pass injects one sharing violation before replacing an
existing regular test config. This path additionally requires the complete
insecure test-backend gate, the E2E child marker, and `GOCOVERDIR`; it cannot run
against a production keyring backend. Runtime coverage build-identity and
isolated store/config path checks prevent activation in a release-like binary,
and the hook does not alter public output. Supplying every request gate to an
uninstrumented binary fails closed with a config error instead of injecting or
silently ignoring the build-identity mismatch.

Profile create/add/remove also serialize their complete
load-mutate-validate-save sequence through a persistent adjacent lock file. The
lock is created with mode `0600` where POSIX modes apply, rejected when it is a
symlink or non-regular target, and intentionally retained after unlock to avoid
an inode-replacement race between cooperating processes. Waiting is bounded to
five seconds or the caller's earlier deadline; contention returns the structured
code `CONFIG_LOCKED`. Secret backend checks are performed before entering this
transaction, and dry runs do not create a config or lock file.

## Backend Assumptions

Production secret storage uses OS keychain-style backends through `github.com/99designs/keyring`: macOS Keychain, Linux Secret Service, Linux `pass`, KWallet, and Windows Credential Manager. `pass` requires the `pass` command and an initialized password store. Secret and service identifiers are validated as relative slash-separated names before backend access; absolute, empty, `.` and `..` path components are rejected so `pass` operations remain below the `env-vault` prefix.

If `pass` is explicitly selected and unavailable, commands return structured error code `BACKEND_UNAVAILABLE` with remediation to install `pass` or use another supported OS keychain backend.

The production backend allowlist excludes plaintext file-style fallback. `keyring.FileBackend`, env files, and other plaintext storage must not be production-enabled without a separate ADR and explicit approval.

Passwork is not implemented in this MVP and is deferred.

The insecure test backend is available only when all three gates are set:

- `ENV_VAULT_BACKEND=test`
- `ENV_VAULT_ALLOW_INSECURE_TEST_BACKEND=1`
- `ENV_VAULT_TEST_STORE=/tmp/...`

The test backend is never a production fallback.

Tests and smoke checks generate ephemeral secret fixture values at runtime. Stable secret payload fixtures must not be committed to tests, scripts, docs, CI output, JSON/JSONL examples, or evidence bundles.

## Known Limitations

- A child process receives secret values through environment variables and can leak them if it prints or forwards its environment.
- On Linux, process environment variables may be visible to the same user through `/proc` in some environments.
- OS keychain availability depends on the platform session and keyring daemon.
- Any process or principal with write access through ownership, group mode, or
  ACLs can replace a parent directory, lock path, or temporary filename during
  a checked filesystem operation. This remains outside the cooperative
  transaction guarantee. Keep config directories non-writable by untrusted
  principals and processes.
- env-vault does not rotate credentials by itself.

## Bug Reports

Do not include secret values in bug reports, terminal transcripts, screenshots, logs, or reproduction data. Include command names, structured error codes, platform, keychain backend notes, and redacted config mappings only.

Security reports should be sent privately once a public maintainer contact exists. Until then, keep reports local and do not publish secret-bearing evidence.
