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
otherwise. `--stdin` SHALL refuse a terminal with `USAGE`, read at most 65539
bytes, and remove exactly one trailing `\n` or `\r\n` before checking the
value length. An empty value SHALL fail with `USAGE`. A value larger than
65536 bytes SHALL fail with `SECRET_TOO_LARGE` (exit `2`) without opening or
calling a backend. A smaller backend limit SHALL also be enforced as defined
by "Storing a secret".

On macOS and Linux, the hidden prompt SHALL accept complete input through
65536 bytes without canonical-line truncation or hanging, whether the input
arrives in one paste or in pieces. The prompt SHALL apply these editing rules
without echoing any input:

| Input | Behavior |
|---|---|
| `\n` or `\r` | Finish the current line without including the terminator |
| Backspace (`0x7f` or `0x08`) | Remove the final complete UTF-8 character, or one byte when the suffix is invalid UTF-8; do nothing on an empty buffer |
| Ctrl+U | Clear the current buffer |
| Ctrl+W | Remove trailing Unicode whitespace, then the preceding word back to whitespace or the start; an invalid UTF-8 suffix is treated as one non-whitespace byte per editing step |
| Ctrl+D | Report end-of-file on an empty buffer; otherwise ignore it |
| Other bytes delivered by the terminal | Append unchanged |

On macOS and Linux, once a hidden line exceeds 65536 bytes, the prompt SHALL stop retaining
additional bytes and SHALL consume the rest of that line through its next
`\n` or `\r`. Editing after overflow SHALL NOT make the line acceptable. It
SHALL overwrite the retained input on a best-effort basis, restore the
terminal, and return `SECRET_TOO_LARGE` without opening or calling a backend.
It SHALL NOT consume input after that terminator. Normal completion SHALL
also restore the terminal. These editing changes SHALL apply only on macOS
and Linux; hidden prompt behavior on Windows and BSD SHALL remain unchanged.

On macOS and Linux, a SIGINT, SIGTERM, SIGHUP or SIGQUIT received while the
hidden prompt is open SHALL discard the partial input, restore the terminal
state, and end env-vault with that signal (or exit `128+n` when the signal
cannot end it); a SIGINT or SIGHUP inherited as ignored SHALL remain ignored.

#### Scenario: Piped value with CRLF

- **WHEN** a generated disposable value followed by `\r\n` is piped to `env-vault secret set token --stdin`
- **THEN** the stored bytes exactly match the generated value with that one trailing line ending removed

#### Scenario: Stdin is a terminal

- **WHEN** the user runs `env-vault secret set token --stdin` from an interactive terminal
- **THEN** env-vault exits `2` with `USAGE` without reading input

#### Scenario: Long hidden input

- **WHEN** a generated 6050-byte value is entered at the hidden prompt on macOS or Linux and followed by Enter
- **THEN** the complete value is accepted without truncation or hanging, and its length and digest match the input

#### Scenario: Hidden input boundary lengths and chunking

- **WHEN** a generated value of 1, 1023, 1024, 1025, 4095, 4096 or 65536 bytes is entered at the hidden prompt on macOS or Linux, either in one write or in pieces, and followed by Enter
- **THEN** the complete value is accepted with matching length and digest, and the original terminal state is restored

#### Scenario: Stdin at the common limit

- **WHEN** a generated 65536-byte value is piped to `secret set token --stdin`, either alone, followed by `\n`, or followed by `\r\n`, and the backend has no smaller limit
- **THEN** all 65536 value bytes are accepted and any one trailing line ending is removed before the size check

#### Scenario: Oversized stdin

- **WHEN** a generated 65537-byte value is piped to `secret set token --stdin`, either alone or followed by one line ending
- **THEN** env-vault exits `2` with `SECRET_TOO_LARGE` and no backend is opened or called

#### Scenario: Oversized hidden input is drained

- **WHEN** 65537 generated value bytes and Enter arrive at the hidden prompt on macOS or Linux
- **THEN** env-vault exits `2` with `SECRET_TOO_LARGE`, the retained input is overwritten, the terminal is restored, no tail of that line remains for the next reader, and no backend is opened or called

#### Scenario: Editing cannot recover an overflowed line

- **WHEN** the hidden input exceeds 65536 bytes on macOS or Linux, followed by Ctrl+U, a shorter replacement and Enter
- **THEN** the whole line is rejected with `SECRET_TOO_LARGE` after the terminator is consumed and no backend is opened or called

#### Scenario: Backspace removes a multibyte character

- **WHEN** a generated prefix and a multibyte UTF-8 character are entered at the hidden prompt on macOS or Linux, followed by either Backspace byte and Enter
- **THEN** the accepted value is exactly the generated prefix, with no bytes of the removed character retained

#### Scenario: Backspace removes an invalid UTF-8 suffix

- **WHEN** a generated prefix and one invalid trailing UTF-8 byte are entered at the hidden prompt on macOS or Linux, followed by Backspace and Enter
- **THEN** only the invalid trailing byte is removed and the generated prefix is accepted unchanged

#### Scenario: Clear and word erase

- **WHEN** disposable input is cleared with Ctrl+U, then two generated words separated by Unicode whitespace and followed by Unicode whitespace are entered before Ctrl+W and Enter on macOS or Linux
- **THEN** the cleared input, trailing whitespace and final word are absent, while the first word and its following separator remain unchanged

#### Scenario: End-of-file key

- **WHEN** Ctrl+D arrives at an empty hidden prompt on macOS or Linux
- **THEN** the prompt reports end-of-file and restores the terminal without opening or calling a backend

#### Scenario: End-of-file key with retained input

- **WHEN** a generated non-empty value is entered at the hidden prompt on macOS or Linux, followed by Ctrl+D and Enter
- **THEN** Ctrl+D is ignored and the complete generated value is accepted

#### Scenario: Carriage return finishes hidden input

- **WHEN** a generated value followed by `\r` is entered at the hidden prompt on macOS or Linux
- **THEN** the value is accepted without the terminator and the terminal is restored

#### Scenario: Interrupt during long input

- **WHEN** Ctrl+C interrupts more than 4096 bytes of partial hidden input on macOS or Linux and SIGINT was not inherited as ignored
- **THEN** the input is discarded, the original terminal state is restored, env-vault ends with the existing SIGINT behavior, and no backend is opened or called

### Requirement: Storing a secret

`secret set <name>` SHALL check whether the record exists, then write the
value, and SHALL report `action` as `created` or `overwritten` from that
check. Its data SHALL contain `name`, `service`, `record_id`, `fingerprint`,
`action`, `verified` and `dry_run`; a dry run SHALL omit `action` and
`verified`. A new or replacement value SHALL be at most 65536 bytes, or a
smaller limit reported by the selected backend. The effective limit SHALL be
the smaller of those limits; a backend without a known limit SHALL use
65536 bytes. On Windows Credential Manager the limit SHALL be 2560 bytes.
An oversized value SHALL fail with `SECRET_TOO_LARGE` (exit `2`) before the
backend is opened, existence is checked or a value is written. With
`--dry-run`, the command SHALL validate the name, the service and the backend
selection and SHALL NOT read input, open a production backend or write.

This value limit SHALL apply only to `secret set` and entries selected for
writing by `import`. `secret check`, `exec` and `export` SHALL NOT reject
previously stored values because they exceed this limit. Their other checks,
including operating system limits at process launch and transfer-container
limits, SHALL remain in effect.

#### Scenario: New secret

- **WHEN** `secret set token --stdin` stores a name that did not exist
- **THEN** the result reports `action: created` and `verified: false`

#### Scenario: Dry run

- **WHEN** the user runs `env-vault --json --dry-run secret set token`
- **THEN** the data holds `name`, `service`, `record_id`, `fingerprint` and `dry_run: true`, without `action` or `verified`, and no input is read

#### Scenario: Oversized value on Windows

- **WHEN** a 3000-byte value is piped to `secret set token --stdin` on Windows Credential Manager
- **THEN** env-vault exits `2` with `SECRET_TOO_LARGE`, no backend is opened or called, and nothing is written

#### Scenario: Windows value boundary

- **WHEN** generated values of 2560 and 2561 bytes are separately supplied to `secret set token --stdin` on Windows Credential Manager
- **THEN** the 2560-byte value is accepted and the 2561-byte value fails with `SECRET_TOO_LARGE` before any backend access

#### Scenario: Overwrite obeys the common limit

- **WHEN** an existing secret is supplied a generated 65537-byte replacement through `secret set --stdin`
- **THEN** env-vault exits `2` with `SECRET_TOO_LARGE` before checking existence, and the existing record is unchanged

#### Scenario: Existing oversized secret remains checkable

- **WHEN** `secret check token` addresses an existing record whose value exceeds 65536 bytes
- **THEN** the command checks existence without reading the value and does not reject the record for its size

#### Scenario: Existing oversized secret remains executable

- **WHEN** `exec` maps an existing value larger than 65536 bytes and the resulting command and environment fit the operating system limits
- **THEN** the child starts with the complete value and env-vault does not report `SECRET_TOO_LARGE`

#### Scenario: Existing oversized secret remains exportable

- **WHEN** export selects an existing value larger than 65536 bytes and the transfer container fits its existing limits
- **THEN** export includes the complete value as authenticated ciphertext without rejecting its size, even though a later import would refuse to write that value

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
