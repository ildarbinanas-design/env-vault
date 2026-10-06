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
Only while the complete insecure test-backend gate is active (see
`secret-storage`) SHALL the passphrase be read from stdin instead, with one
trailing line ending removed; an incomplete gate SHALL fail closed before
stdin is read.

#### Scenario: Short passphrase

- **WHEN** the user enters an 11-character passphrase at the export prompt
- **THEN** env-vault exits `2` with `PASSPHRASE_INVALID` and message `Passphrase must be at least 12 characters`, and no file is written

#### Scenario: Mismatched confirmation

- **WHEN** the two export entries differ
- **THEN** env-vault exits `2` with `PASSPHRASE_INVALID` and message `Passphrases do not match`

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
`overwrite` SHALL replace them. It SHALL also refuse, before the first write,
any value larger than the backend stores with `SECRET_TOO_LARGE`. The data
SHALL contain `path`, `services`, `secret_count`, `on_conflict`, `dry_run` and
`secrets`, each with `service`, `name`, `record_id`, `fingerprint` and
`action` (`created`, `overwritten` or `skipped`).

#### Scenario: Default policy with an existing secret

- **WHEN** the container holds `tok` and `tok` is already stored
- **THEN** import exits `2` with `SECRET_EXISTS` and remediation `Re-run with --on-conflict overwrite or --on-conflict skip`, and nothing is written

#### Scenario: Skip policy

- **WHEN** the same import runs with `--on-conflict skip`
- **THEN** it exits `0` and reports `action: skipped` for `tok`

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
