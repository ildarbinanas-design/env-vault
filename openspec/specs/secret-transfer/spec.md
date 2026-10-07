# secret-transfer Specification

## Purpose

Define `export` and `import`, which move stored secret values between
machines through a passphrase-encrypted transfer container, and the container
format `env-vault.bundle.v1`. A container carries values only; profile
mappings travel separately. Neither command reads or writes a config file.

## Requirements

### Requirement: Export selection

`export --out <path>` SHALL export every secret of the default service
`env-vault` plus every service named with `--with-services`, which accepts a
comma-separated list and may be repeated. Entries SHALL be trimmed of
surrounding whitespace and de-duplicated; an empty entry or an invalid
service name SHALL fail with `USAGE`. Services that are not named SHALL NOT
be exported, because a keychain cannot enumerate them. A missing `--out`
SHALL fail with `USAGE`. When the selection holds no secret, export SHALL
fail with `USAGE` and the message `No stored secrets to export`. A secret
deleted between listing and reading SHALL be skipped. The data SHALL contain
`path`, `services`, `secret_count`, `secrets` (each with `service`, `name`,
`record_id`, `fingerprint`), `kdf`, `cipher` and `dry_run`.

#### Scenario: Custom service

- **WHEN** secrets exist in `env-vault` and `team/ci` and the user runs `env-vault export --out vault.evb --with-services team/ci`
- **THEN** the container holds the secrets of both services and the data lists `services: ["env-vault","team/ci"]`

### Requirement: Export destination

Before asking for the passphrase, export SHALL refuse a destination that is a
symlink or not a regular file (`USAGE`, `Unsafe container target`), and,
without `--force`, an existing destination (`USAGE`, `Container already
exists: <path>`). The container SHALL be written with mode `0600` through a
synced temporary sibling. Without `--force`, publication SHALL atomically
refuse an occupied destination, including one created while the prompt was
open, and SHALL never fall back to overwriting. With `--force`, the complete
temporary file SHALL replace the destination.

#### Scenario: Existing destination

- **WHEN** `vault.evb` exists and the user runs `env-vault export --out vault.evb`
- **THEN** env-vault exits `2` with `USAGE` before any passphrase prompt

### Requirement: Transfer passphrase

The passphrase SHALL be read only from a hidden terminal prompt
(`Passphrase: ` and, for export, `Confirm passphrase: ` on stderr); without a
terminal the command SHALL fail with `USAGE`. There SHALL be no passphrase
flag, environment variable or file. An empty passphrase SHALL fail with
`PASSPHRASE_INVALID` (exit `2`). Export SHALL require at least 12 characters,
counted as Unicode code points, and two identical entries; otherwise it SHALL
fail with `PASSPHRASE_INVALID`. Import SHALL accept any non-empty passphrase.
The secret-value write limit SHALL NOT limit transfer passphrase length.
Only while the complete insecure test-backend gate is active (see
`secret-storage`) SHALL the passphrase be read from stdin instead, with one
trailing line ending removed; an incomplete gate SHALL fail closed before
stdin is read.

On macOS and Linux, all transfer passphrase prompts SHALL use the same hidden
editing, line-termination and signal-restoration behavior as "Secret input"
in `secret-storage`, without its secret-value size limit. Long passphrases
SHALL be accepted completely without canonical-line truncation or hanging.
Each prompt SHALL consume only its own line: an export passphrase and its
confirmation supplied in one paste SHALL be read as separate complete
entries. Hidden prompt behavior on Windows and BSD SHALL remain unchanged.

#### Scenario: Short passphrase

- **WHEN** the user enters an 11-character passphrase at the export prompt
- **THEN** env-vault exits `2` with `PASSPHRASE_INVALID` and message `Passphrase must be at least 12 characters`, and no file is written

#### Scenario: Mismatched confirmation

- **WHEN** the two export entries differ
- **THEN** env-vault exits `2` with `PASSPHRASE_INVALID` and message `Passphrases do not match`

#### Scenario: Long import passphrase

- **WHEN** a generated 6050-byte passphrase matching a valid container is entered at the import prompt on macOS or Linux
- **THEN** the complete passphrase is read without hanging or truncation and the container authenticates successfully

#### Scenario: Long export confirmation in one paste

- **WHEN** two identical generated 6050-byte passphrase lines arrive in one write at the export prompt on macOS or Linux
- **THEN** the first prompt reads only the first complete line, confirmation reads the second complete line, and the confirmation succeeds

#### Scenario: Passphrase exceeds the secret-value limit

- **WHEN** a generated 65537-byte passphrase is entered twice identically for export on macOS or Linux, or entered for import of a container encrypted with that passphrase
- **THEN** its full length is accepted and no `SECRET_TOO_LARGE` error is caused by the passphrase

### Requirement: Container format

A container SHALL be one JSON document with the fields `schema`
(`env-vault.bundle.v1`), `version` (`1`), `created_at` (RFC 3339 UTC),
`tool_version`, `kdf` (`algorithm: argon2id`, 16-byte base64 `salt`, `time`,
`memory_kib`, `parallelism`, `key_length: 32`), `cipher`
(`algorithm: aes-256-gcm`, 12-byte base64 `nonce`) and `payload` (base64
AES-256-GCM ciphertext with its 16-byte tag). The header (every field except
`payload`, re-encoded in this field order) SHALL be the additional
authenticated data. The plaintext SHALL be `{"secrets":[{"service":…,
"name":…,"value":<base64>}]}` with at least one entry and unique
(`service`, `name`) pairs. Secret and service names SHALL appear only inside
the ciphertext; the header exposes creation time, tool version and
parameters, and the file length is visible.

#### Scenario: Header tampering

- **WHEN** an attacker lowers `kdf.time` in a container within the accepted bounds
- **THEN** import fails with `BUNDLE_AUTH_FAILED` because the header is authenticated

### Requirement: Key derivation and encryption

Export SHALL derive a 32-byte key with Argon2id using `time` 3, `memory_kib`
65536 and `parallelism` 4, and SHALL draw a fresh random salt and nonce for
every container. Both commands SHALL accept only `time` in [1, 10],
`memory_kib` in [8192, 1048576] and `parallelism` in [1, 8], and SHALL check
these bounds and every other structural rule before deriving a key. Decrypted
values, passphrases and keys SHALL be overwritten after use on a best-effort
basis.

#### Scenario: Excessive declared memory

- **WHEN** a container declares `memory_kib: 4194304`
- **THEN** import fails with `BUNDLE_INVALID` without deriving a key

### Requirement: Container limits and strict parsing

A container file SHALL be at most 24 MiB and its ciphertext, tag included, at
most 16 MiB. Export SHALL check both limits before deriving a key and SHALL
fail with `BUNDLE_INVALID` without creating or replacing the file, also with
`--force`. Import SHALL refuse a larger, non-regular or unreadable file with
`BUNDLE_INVALID` before reading it fully. Both the container and the decrypted
plaintext SHALL contain exactly one JSON document followed only by
whitespace, and unknown or repeated fields (including escaped spellings and
case variants) SHALL be rejected at every level with `BUNDLE_INVALID`.

#### Scenario: Unknown header field

- **WHEN** a container carries an extra top-level field
- **THEN** import fails with `BUNDLE_INVALID` (exit `5`)

### Requirement: Import authentication and validation

Import SHALL authenticate and decrypt the whole container before any write. A
wrong passphrase and a modified container SHALL fail identically with
`BUNDLE_AUTH_FAILED` (exit `5`), message `Unable to decrypt the container`.
Decrypted service and secret names SHALL be validated with the ordinary rules
and fail with `BUNDLE_INVALID`; a service name that `pass` cannot store SHALL
fail with `USAGE` when `pass` is selected. When the backend reports that two
entries address the same record (on Windows, case variants of service or
name), import SHALL fail with `BUNDLE_INVALID` for every conflict policy.

#### Scenario: Wrong passphrase

- **WHEN** the user enters a wrong passphrase at the import prompt
- **THEN** env-vault exits `5` with `BUNDLE_AUTH_FAILED` and no secret is written

### Requirement: Import conflict policy

`import <path>` SHALL accept `--on-conflict fail|skip|overwrite`, defaulting
to `fail`; another value SHALL fail with `USAGE`. Before the first write,
import SHALL check every entry's existence through the key listing and, for
`fail`, SHALL stop at the first existing secret with `SECRET_EXISTS` (exit
`2`) without writing; `skip` SHALL leave existing secrets untouched and
`overwrite` SHALL replace them. Before the first write it SHALL also refuse
with `SECRET_TOO_LARGE` (exit `2`) any value that would be written (a new
secret, or an existing one under `overwrite`) and exceeds 65536 bytes or a
smaller limit reported by the selected backend. The effective limit SHALL be
65536 bytes without a smaller known backend limit and 2560 bytes on Windows
Credential Manager. A value skipped under `skip` SHALL NOT be checked
against that limit. All selected writes SHALL pass the size check before any
entry is written, including during `--dry-run` preflight. The rule SHALL
apply to every otherwise valid container, including containers created by
v0.4.2; format compatibility SHALL NOT exempt an oversized value from the
write limit. The data SHALL contain `path`, `services`, `secret_count`,
`on_conflict`, `dry_run` and `secrets`, each with `service`, `name`,
`record_id`, `fingerprint` and `action` (`created`, `overwritten` or
`skipped`).

#### Scenario: Default policy with an existing secret

- **WHEN** the container holds `tok` and `tok` is already stored
- **THEN** import exits `2` with `SECRET_EXISTS` and remediation `Re-run with --on-conflict overwrite or --on-conflict skip`, and nothing is written

#### Scenario: Oversized value that is skipped

- **WHEN** on Windows Credential Manager the container holds a 3000-byte value for `tok`, `tok` already exists, and the policy is `skip`
- **THEN** import does not fail on the size, leaves `tok` unchanged and reports `action: skipped`

#### Scenario: Skip policy

- **WHEN** the same import runs with `--on-conflict skip`
- **THEN** it exits `0` and reports `action: skipped` for `tok`

#### Scenario: Common limit accepted

- **WHEN** a valid container holds a generated 65536-byte value selected for writing and the backend has no smaller limit
- **THEN** import writes the complete value and does not report `SECRET_TOO_LARGE`

#### Scenario: Oversized value prevents every write

- **WHEN** a valid container has one small value followed by a generated 65537-byte value, both selected for writing
- **THEN** import exits `2` with `SECRET_TOO_LARGE` before writing either value

#### Scenario: Oversized overwrite is refused

- **WHEN** a valid container supplies a generated 65537-byte value for an existing secret and the policy is `overwrite`
- **THEN** import exits `2` with `SECRET_TOO_LARGE` before any write and leaves the existing record unchanged

#### Scenario: Oversized dry-run import

- **WHEN** a valid container has a generated 65537-byte value selected for writing and import runs with `--dry-run`
- **THEN** preflight exits `2` with `SECRET_TOO_LARGE` instead of reporting success, and nothing is written

#### Scenario: Legacy container with an oversized value

- **WHEN** an otherwise valid v0.4.2 container contains a generated value larger than 65536 bytes that import would write
- **THEN** import authenticates and parses the unchanged container format but exits `2` with `SECRET_TOO_LARGE` before its first write

#### Scenario: Oversized common-limit value is skipped

- **WHEN** a valid container has a value larger than 65536 bytes for an existing secret and a permitted value for a new secret, and the policy is `skip`
- **THEN** import leaves the existing record unchanged with `action: skipped`, writes the permitted new value, and does not reject the skipped value for its size

#### Scenario: Windows value boundary

- **WHEN** separate valid containers select generated values of 2560 and 2561 bytes for writing to Windows Credential Manager
- **THEN** the 2560-byte value passes the size check and the 2561-byte value fails with `SECRET_TOO_LARGE` before the first write

### Requirement: Import is not transactional

The checks before the first write SHALL NOT make import atomic. A backend
failure during the write phase SHALL fail with `BACKEND_UNAVAILABLE` and MAY
leave the secrets written before it in place, and another process MAY change
the store between the checks and the writes.

#### Scenario: Backend fails mid-import

- **WHEN** the backend rejects the third of five writes
- **THEN** import exits `4` and the first two secrets remain stored

### Requirement: Dry-run transfer

`export --dry-run` SHALL list the selected secrets and report the data
without checking the destination, reading values, asking for a passphrase or
writing a file. `import --dry-run` SHALL read the container, ask for the
passphrase, decrypt and run every pre-write check, and SHALL NOT write.

#### Scenario: Dry-run import

- **WHEN** the user runs `env-vault --dry-run import vault.evb` with the right passphrase
- **THEN** the result reports the action each entry would get and the store is unchanged
