# Spec Delta

## MODIFIED Requirements

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
