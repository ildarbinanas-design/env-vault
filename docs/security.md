# Security

## Secret Handling

env-vault does not print secret values. It does not store secret values in config, logs, docs, CI metadata, JSON output, JSONL output, or evidence bundles.

Secret input is limited to:

- a hidden interactive prompt;
- `--stdin`, which trims exactly one trailing line ending (`\n` or `\r\n`). It
  refuses a terminal, which would echo the value; there the hidden prompt is
  the way in. env-vault sees only its own stdin: where a terminal echoes into
  a pipe, as in Git Bash without ConPTY or `ssh host 'env-vault … --stdin'`
  without `-t`, it cannot tell, so pipe the value from a file or a command
  there instead of typing it.

There is no `secret get` command and no command-line flag for passing a secret value.

On macOS and Linux, interrupted hidden prompts restore the terminal on SIGINT,
SIGTERM, SIGHUP and SIGQUIT. Inherited ignored SIGINT/SIGHUP remain ignored.
SIGKILL cannot be handled or restore terminal state.

## Transfer Containers

`export` writes values from the selected keychain services to one encrypted
file; `import` restores them into the keychain on another machine. The format is AES-256-GCM
under a key derived with Argon2id (64 MiB, three passes, four lanes), with the
header authenticated and the key-derivation parameters bounds-checked before
any key is derived. Secret names and values live inside the ciphertext. The
cleartext header exposes the creation time, tool version and encryption
parameters; the file length is visible too. See
[ADR 0010](adr/0010-encrypted-secret-transfer-container.md).

A container carries values only. Profile mappings stay in YAML, contain no
values, and can be versioned with the consuming project. They are already
portable; duplicating them into the container would widen what a leaked
container discloses. This repository ignores its own local `.env-vault.yaml`
and lock file, as explained in [README.md](../README.md#profiles-and-config).

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
  long passphrase; the enforced 12-character minimum counts characters
  (Unicode code points), not bytes, and is a floor, not a target. It applies
  when `export` seals a container; `import` opens any container whose
  passphrase it is given.
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

Containers allow at most 16 MiB of ciphertext, including encoded values,
metadata and the authentication tag, and 24 MiB for the complete file. Export
refuses larger output with `BUNDLE_INVALID` before creating or replacing the
file, including with `--force`. Base64 encoding means the selected raw values
together must fit below 12 MiB. See [format and KDF bounds](design.md#transfer-container).

Containers are written with mode `0600` through a synced temporary sibling.
Without `--force`, publication atomically refuses an occupied destination,
including a file created while the passphrase prompt is open. With `--force`,
the complete temporary file replaces the destination. Symlinks and non-regular
targets are rejected. A filesystem that cannot safely publish without replacing
an existing file returns an error; export never falls back to an overwrite.

Import authenticates and decrypts the container before any keychain write.
It then checks backend identity. On Windows, entries that name
one credential through case variants of service or name are rejected with
`BUNDLE_INVALID`, regardless of conflict policy. Preflight failures write
nothing; failures during the later write phase can leave a partial import.
External concurrent changes are not excluded by the preflight.

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

Production secret storage uses OS keychain-style backends through `github.com/99designs/keyring`: macOS Keychain, Linux Secret Service, Linux `pass`, KWallet, and Windows Credential Manager. `pass` requires the `pass` command and an initialized password store. Secret and service identifiers are validated as relative names before backend access; absolute, empty, `.` and `..` path components are rejected so `pass` operations remain below the `env-vault` prefix. Secret names may contain slashes. Service names may contain slashes on other backends, but are refused with `pass` because its single path cannot distinguish a service hierarchy from a secret-name hierarchy.

If `pass` is explicitly selected and unavailable, commands return structured error code `BACKEND_UNAVAILABLE` with remediation to install `pass` or use another supported OS keychain backend.

The production backend allowlist excludes plaintext file-style fallback. Production plaintext storage is forbidden; `keyring.FileBackend` and env files are not production fallback options.

Passwork is not implemented in this MVP and is deferred.

The insecure test backend is available only when all three gates are set:

- `ENV_VAULT_BACKEND=test`
- `ENV_VAULT_ALLOW_INSECURE_TEST_BACKEND=1`
- `ENV_VAULT_TEST_STORE=<absolute path under the system temporary directory>`

The test backend is never a production fallback.

Tests and smoke checks generate ephemeral secret fixture values at runtime. Stable secret payload fixtures must not be committed to tests, scripts, docs, CI output, JSON/JSONL examples, or evidence bundles.

### macOS installation and Keychain access

Use Homebrew for release binaries on macOS 15+. They are not Developer ID
signed or notarized ([ADR 0009](adr/0009-no-code-signing-homebrew-only-macos-distribution.md)),
so `spctl --assess` rejects them. A browser download, or extraction from an
archive carrying Gatekeeper quarantine, can produce a binary macOS terminates
on launch. Homebrew does not attach quarantine.

`xattr -d com.apple.quarantine env-vault` is a manual local override after
verifying the release source and checksum; it does not replace signing or
notarization. env-vault never removes quarantine automatically, and manual
release downloads remain an unsupported installation path on macOS.

macOS asks for Keychain access the first time a given binary reads a stored
value: `exec`, `secret set --verify`, or `export`. Each Homebrew upgrade installs
a new binary, so access prompts return once per secret. After upgrading, run
each profile in a terminal and choose **Always Allow**:

```sh
env-vault exec dev -- true
```

A non-interactive process such as a LaunchAgent may otherwise wait for an
unanswered prompt for up to two minutes and fail with `BACKEND_UNAVAILABLE`.
Denied access fails the same way. `secret check`, `secret list`,
`profile add --check-secret`, import conflict checks, and `doctor` only list
stored names and never read a value.

### Linux backend selection

Secret Service requires a compatible daemon and session. For `pass`, install
the command and initialize its password store; `ENV_VAULT_BACKEND=pass` selects
it explicitly. A slashed service never falls back to `pass`, because that
backend cannot distinguish `team/ci` + `tok` from `team` + `ci/tok`. An older
entry created with the first combination remains reachable with the second.

Secret Service collections are selected by an env-vault alias where supported,
otherwise by their exact collection label. KDE saves the alias on the next
write; GNOME does not support custom aliases. Existing collections keep their
records and encoding. Duplicate labels or a competing legacy path fail with
`BACKEND_UNAVAILABLE`, even if another collection has the requested label.
This includes some old KDE `_HH` name collisions, which are indistinguishable
from renamed GNOME collections through this metadata. Identify the intended
collection before resolving the conflict: on KDE, its explicit env-vault alias
can select it; for a renamed old GNOME collection, restore its original unique
service label in the keyring manager. No automatic migration is attempted.

For headless automation, use a CI secret manager or an explicitly supported
backend with its required session; plaintext config is never a fallback.

### Windows Credential Manager

Values are limited to 2560 bytes. Secret names are case-insensitive:
`secret set TOKEN` over `token` reports `overwritten`, with a record ID based on
the spelling supplied. Service names are compared exactly in listings, so keep
one spelling per service. Oversized writes return `SECRET_TOO_LARGE`; import
checks every value it will write before its first write.

Import also rejects entries that address the same complete Windows credential
through case variants of service or name. This is independent of the selected
conflict policy; see [transfer containers](#transfer-containers).

## Known Limitations

- A child process receives secret values through environment variables and can leak them if it prints or forwards its environment.
- On Linux, process environment variables may be visible to the same user through `/proc` in some environments.
- OS keychain availability depends on the platform session and keyring daemon.
  Every backend call gives up after two minutes, so a system prompt that nobody
  answers, such as a macOS Keychain prompt shown to a LaunchAgent after an
  upgrade or a locked Secret Service collection, fails with
  `BACKEND_UNAVAILABLE` instead of blocking forever. A record the backend lists
  but refuses to return, for example after a denied Keychain prompt, also fails
  with `BACKEND_UNAVAILABLE` and is never treated as a missing secret. A
  timed-out write may still complete afterwards when the backend helper, such
  as `pass` or `gpg`, keeps running; check with `secret check` before retrying.
- Any process or principal with write access through ownership, group mode, or
  ACLs can replace a parent directory, lock path, or temporary filename during
  a checked filesystem operation. This remains outside the cooperative
  transaction guarantee. Keep config directories non-writable by untrusted
  principals and processes.
- env-vault does not rotate credentials by itself.

## Bug Reports

Do not include secret values in bug reports, terminal transcripts, screenshots, logs, or reproduction data. Include command names, structured error codes, platform, keychain backend notes, and redacted config mappings only.

Follow [SECURITY.md](../SECURITY.md#reporting-a-vulnerability): use GitHub private
vulnerability reporting when available, or contact the maintainer privately
before sharing reproduction details. Never publish secret-bearing evidence.
