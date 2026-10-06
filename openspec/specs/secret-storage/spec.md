# secret-storage Specification

## Purpose

Define where env-vault keeps secret values, how secret and service names are
validated, how values enter the store, and the behavior of `secret set`,
`secret check`, `secret list` and `secret delete`. Values are read back only
for `exec`, `export` and `secret set --verify`; no command prints them.

## Requirements

### Requirement: Production backend allowlist

Production storage SHALL use only these backends, in this discovery order:
macOS Keychain, Linux Secret Service, KWallet, Windows Credential Manager and
`pass`. The keyring file backend, environment files and any other plaintext
store SHALL NOT be used, including as a fallback. With `ENV_VAULT_BACKEND`
unset or set to `keyring`, env-vault SHALL use the first available backend
of the allowlist; with `ENV_VAULT_BACKEND=pass` it SHALL use only `pass`.
Any other `ENV_VAULT_BACKEND` value outside the test gate SHALL fail with
`BACKEND_UNAVAILABLE` and the message `Unsupported secret backend requested`.

#### Scenario: Explicit pass without pass installed

- **WHEN** `ENV_VAULT_BACKEND=pass` is set and `pass` is not installed or initialized
- **THEN** a store operation fails with `BACKEND_UNAVAILABLE` (exit `4`) and the remediation `install pass or use another supported OS keychain backend.`

#### Scenario: Unsupported backend value

- **WHEN** `ENV_VAULT_BACKEND=file` is set and no other test-backend variable is set
- **THEN** a store operation fails with `BACKEND_UNAVAILABLE` and no file is written

### Requirement: Gated insecure test backend

env-vault SHALL use its insecure file-based test backend only when all three
gates are set: `ENV_VAULT_BACKEND=test`,
`ENV_VAULT_ALLOW_INSECURE_TEST_BACKEND=1`, and `ENV_VAULT_TEST_STORE` set to
an absolute path under the system temporary directory. If any of the three
variables is set but the gate is incomplete or invalid, every store operation
SHALL fail closed with `BACKEND_UNAVAILABLE` and SHALL NOT fall back to a
production backend. The test backend SHALL never be a production fallback.

#### Scenario: Partial gate

- **WHEN** `ENV_VAULT_BACKEND=test` and `ENV_VAULT_TEST_STORE=/tmp/store` are set but `ENV_VAULT_ALLOW_INSECURE_TEST_BACKEND` is not
- **THEN** `secret set` fails with `BACKEND_UNAVAILABLE`, message `Insecure test backend is not explicitly allowed`, and no store is written

#### Scenario: Store outside the temporary directory

- **WHEN** the full gate is set with `ENV_VAULT_TEST_STORE=/home/user/store`
- **THEN** the command fails with `BACKEND_UNAVAILABLE`, message `Test backend store path must be under /tmp`

### Requirement: Service namespace

Every secret SHALL be addressed by the pair (service, secret name). The
default service SHALL be `env-vault`. `secret set`, `secret check` and
`secret delete` SHALL accept `--service <name>` to replace the default.
`secret list`, `exec` and `profile add --check-secret` SHALL use only the
default service. With `pass`, entries SHALL be stored under the prefix
`env-vault/<service>/` so that env-vault never lists or removes other entries
of the password store.

#### Scenario: Custom service

- **WHEN** the user runs `env-vault secret set token --service team/ci`
- **THEN** the value is stored for the pair (`team/ci`, `token`) and `secret check token` without `--service` does not find it

### Requirement: Secret and service names

A secret name SHALL be non-empty, valid UTF-8, consist only of letters,
digits, `.`, `_`, `-`, `/` and `@`, and SHALL NOT contain `:`. A service name
SHALL be non-empty, valid UTF-8 and free of control characters. Both SHALL be
relative slash-separated paths: no leading `/` or `\`, no Windows drive
prefix, no `\` anywhere, and no empty, `.` or `..` component. With `pass`, a
service name SHALL NOT contain `/`, because `pass` keeps service and name in
one path; a service containing `/` SHALL never fall back to `pass` from the
default backend list. The production backend SHALL repeat these checks before
opening a store. Invalid names SHALL fail with `USAGE`.

#### Scenario: Traversal in a secret name

- **WHEN** the user runs `env-vault secret set ../token`
- **THEN** env-vault exits `2` with `USAGE` before any backend access

#### Scenario: Slashed service with pass

- **WHEN** `ENV_VAULT_BACKEND=pass` is set and the user runs `env-vault secret set token --service team/ci`
- **THEN** env-vault exits `2` with `USAGE` and the remediation `Use a service name without a slash with the pass backend`

### Requirement: Record identity

Each stored secret SHALL be identified in output by a record ID equal to the
first 16 hexadecimal characters of `sha256(service + "\x00" + name)`. The
record ID SHALL NOT depend on the value. Output SHALL report it as
`record_id` and also as the deprecated alias `fingerprint` with the same
value.

#### Scenario: Overwrite keeps the record ID

- **WHEN** `secret set token` runs twice with different values
- **THEN** both results report the same `record_id`

### Requirement: Secret input

`secret set` SHALL read the value only from a hidden interactive prompt or,
with `--stdin`, from standard input. The hidden prompt SHALL write `Secret: `
to stderr, SHALL require stdin to be a terminal, and SHALL fail with `USAGE`
otherwise. `--stdin` SHALL refuse a terminal with `USAGE`, read all of stdin,
and remove exactly one trailing `\n` or `\r\n`. An empty value SHALL fail with
`USAGE`. On macOS and Linux, a SIGINT, SIGTERM, SIGHUP or SIGQUIT received
while the hidden prompt is open SHALL discard the partial input, restore the
terminal state, and end env-vault with that signal (or exit `128+n` when the
signal cannot end it); a SIGINT or SIGHUP inherited as ignored SHALL remain
ignored.

#### Scenario: Piped value with CRLF

- **WHEN** `printf 'value\r\n' | env-vault secret set token --stdin` runs
- **THEN** the stored value is `value`

#### Scenario: Stdin is a terminal

- **WHEN** the user runs `env-vault secret set token --stdin` from an interactive terminal
- **THEN** env-vault exits `2` with `USAGE` without reading input

### Requirement: Storing a secret

`secret set <name>` SHALL check whether the record exists, then write the
value, and SHALL report `action` as `created` or `overwritten` from that
check. Its data SHALL contain `name`, `service`, `record_id`, `fingerprint`,
`action`, `verified` and `dry_run`; a dry run SHALL omit `action` and
`verified`. When the backend has a value limit and the
value exceeds it, the command SHALL fail with `SECRET_TOO_LARGE` (exit `2`)
before writing. On Windows Credential Manager the limit SHALL be 2560 bytes.
With `--dry-run`, it SHALL validate the name, the service and the backend
selection and SHALL NOT read input, open a production backend or write.

#### Scenario: New secret

- **WHEN** `secret set token --stdin` stores a name that did not exist
- **THEN** the result reports `action: created` and `verified: false`

#### Scenario: Dry run

- **WHEN** the user runs `env-vault --json --dry-run secret set token`
- **THEN** the data holds `name`, `service`, `record_id`, `fingerprint` and `dry_run: true`, without `action` or `verified`, and no input is read

#### Scenario: Oversized value on Windows

- **WHEN** a 3000-byte value is piped to `secret set token --stdin` on Windows Credential Manager
- **THEN** env-vault exits `2` with `SECRET_TOO_LARGE` and nothing is written

### Requirement: Write verification

With `--verify`, `secret set` SHALL read the value back after the backend
acknowledges the write and compare it with the input in constant time, without
printing or hashing it. A missing or different value SHALL fail with
`SECRET_UNVERIFIED` (exit `1`); a backend error during the read-back SHALL
fail with `BACKEND_UNAVAILABLE`. Both errors SHALL name the `created` or
`overwritten` action from the pre-write check. A failed verification SHALL NOT
roll back the write. On success the result SHALL report `verified: true`.

#### Scenario: Verified write

- **WHEN** `secret set token --stdin --verify` stores a value the backend returns unchanged
- **THEN** the human output ends with `, verified)` and the exit code is `0`

### Requirement: Existence check without reading values

`secret check <name>` and every other existence check (`profile add
--check-secret`, `exec --dry-run`, import conflict checks, and the pre-write
check of `secret set`) SHALL answer from the backend's key listing and SHALL
NOT read the value. On Windows Credential Manager the name comparison SHALL
ignore case. A missing secret SHALL fail `secret check` with `MISSING_SECRET`
(exit `3`) and a remediation naming `secret set` (with `--service` for a
non-default service).

#### Scenario: Existing secret on macOS

- **WHEN** `secret check token` runs for a stored Keychain item
- **THEN** it reports `secret exists: token (record: <id>)` without a Keychain access prompt for the value

### Requirement: Listing secrets

`secret list` SHALL list the names stored in the default service, sorted, each
with its `record_id` and `fingerprint`, without reading values. Its data SHALL
contain `service` and `secrets`.

#### Scenario: Empty store

- **WHEN** no secret is stored in the default service
- **THEN** human output is `no secrets` and JSON output has `secrets: []`

### Requirement: Deleting a secret

`secret delete <name>` SHALL require `--confirm` equal to the name; otherwise
it SHALL fail with `CONFIRMATION_REQUIRED` (exit `2`) and the remediation
`Re-run with --confirm <name>`. A missing record SHALL fail with
`MISSING_SECRET`. With `--dry-run` it SHALL check the name, service and
confirmation only, without opening the backend or checking existence.

#### Scenario: Confirmed deletion

- **WHEN** the user runs `env-vault secret delete token --confirm token`
- **THEN** the record is removed and stdout reports `secret deleted: token`

### Requirement: Bounded backend calls

Every backend call SHALL give up after two minutes and fail with
`BACKEND_UNAVAILABLE` and the remediation `Answer the system keychain prompt
or unlock the keychain, then retry`. A timed-out call MAY still complete in
the background, for example when `pass` or `gpg` keeps running. On macOS, a
read reported as "not found" for a record that the key listing still contains
SHALL fail with `BACKEND_UNAVAILABLE` (a refused or locked read) and SHALL NOT
be treated as a missing secret.

#### Scenario: Unanswered Keychain prompt

- **WHEN** `exec` needs a value and the macOS access prompt is not answered
- **THEN** after at most two minutes env-vault exits `4` with `BACKEND_UNAVAILABLE` and the child is not started

### Requirement: Native record identity

The Secret Service adapter SHALL keep the collection paths, the `profile`
attribute and the item encoding of existing records, and SHALL select a
collection by an env-vault alias where supported, otherwise by its exact
label. When several physical records or collections match one identity,
env-vault SHALL fail with `BACKEND_UNAVAILABLE` and SHALL NOT choose, migrate
or repair them. The Windows adapter SHALL use the target name
`keyring:<service>:<name>` for generic credentials and SHALL treat
`ERROR_NOT_FOUND` from enumeration as an empty listing.

#### Scenario: Duplicate records

- **WHEN** two Secret Service items carry the same identity
- **THEN** commands that address that secret fail with `BACKEND_UNAVAILABLE` and the remediation to inspect duplicate records in the OS keychain
