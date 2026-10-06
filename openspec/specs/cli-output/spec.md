# cli-output Specification

## Purpose

Define the env-vault command surface and the contract every command shares:
global flags, human and machine-readable output, the error envelope, exit
codes, the `--output` metadata file, `--dry-run`, `version` and `doctor`.
Command-specific behavior lives in `secret-storage`, `profile-config`, `exec`
and `secret-transfer`.

## Requirements

### Requirement: Command surface

env-vault SHALL provide exactly these commands: `version`, `secret set`,
`secret check`, `secret list`, `secret delete`, `profile create`,
`profile add`, `profile remove`, `profile show`, `exec`, `doctor`, `export`
and `import`, plus the root `--version` flag. env-vault SHALL NOT provide a
command that prints a stored secret value, and SHALL NOT accept a secret value
or a transfer passphrase as a command-line argument or flag. An unknown
command, a missing subcommand, or invocation without a command SHALL fail with
`USAGE`.

#### Scenario: Unknown subcommand

- **WHEN** the user runs `env-vault secret get token`
- **THEN** env-vault exits `2` with code `USAGE` and the message `unknown command "get" for "env-vault secret"`

#### Scenario: No command

- **WHEN** the user runs `env-vault` without arguments
- **THEN** env-vault exits `2` with code `USAGE`, message `Command is required` and remediation `Run: env-vault --help`

### Requirement: Global flags

Every command SHALL accept the global flags `--json`, `--jsonl`, `--quiet`,
`--verbose`, `--output <path>`, `--config <path>` and `--dry-run`. `--json`
and `--jsonl` SHALL be mutually exclusive.

#### Scenario: Both machine-readable modes

- **WHEN** the user runs `env-vault --json --jsonl version`
- **THEN** env-vault exits `2` and writes an error envelope with code `USAGE` and message `Use only one of --json or --jsonl` to stdout

### Requirement: Result envelope

Every machine-readable result SHALL be one JSON object with exactly the fields
`ok`, `command`, `timestamp`, `data`, `warnings` and `error`. `timestamp`
SHALL be the UTC time in RFC 3339 format. `warnings` SHALL be an array, empty
when there are none. On success `ok` SHALL be `true` and `error` SHALL be
`null`. On failure `ok` SHALL be `false`, `data` SHALL be `null` (except for
`COMMAND_FAILED`, see `exec`), and `error` SHALL hold `code`, `message` and
`remediation`. `command` SHALL be the command identifier: `version`,
`secret_set`, `secret_check`, `secret_list`, `secret_delete`,
`profile_create`, `profile_add`, `profile_remove`, `profile_show`, `exec`,
`doctor`, `export` or `import`; errors raised before a subcommand is selected
use `root`, `secret` or `profile`. No envelope field SHALL contain a secret
value or passphrase.

#### Scenario: Successful JSON result

- **WHEN** the user runs `env-vault --json secret set token --stdin` with a value piped on stdin
- **THEN** stdout holds one line `{"ok":true,"command":"secret_set","timestamp":"<RFC 3339 UTC>","data":{...},"warnings":[],"error":null}` and the data contains no value

### Requirement: Machine-readable modes

With `--json` or `--jsonl`, env-vault SHALL write exactly one envelope,
encoded as a single line terminated by a newline, to stdout for both success
and failure, without HTML escaping. The exception is an `exec` child that
exits non-zero or is killed: env-vault then writes no envelope to stdout (see
`exec`). The two modes SHALL currently produce
identical output. `--quiet` SHALL NOT suppress a machine-readable envelope.

#### Scenario: Error in JSON mode

- **WHEN** a command fails while `--json` is set
- **THEN** the error envelope is written to stdout, env-vault writes nothing to stderr except an `OUTPUT_WRITE_FAILED` line under `--verbose`, and the exit code is the error's exit code

### Requirement: Human output

Without `--json` or `--jsonl`, a successful command SHALL write a short
human-readable summary to stdout, and `--quiet` SHALL suppress it. A failing
command SHALL write three lines to stderr: `code=<CODE>`,
`message=<message>` and `remediation=<remediation>`; `--quiet` SHALL NOT
suppress them. The summaries SHALL be:

| Command | Human success output |
|---|---|
| `version` | `<version> (<short commit>, <YYYY-MM-DD>)`, or less when build information is missing |
| `secret set` | `secret <created\|overwritten>: <name> (record: <record id>[, verified])` |
| `secret check` | `secret exists: <name> (record: <record id>)` |
| `secret list` | one `<name> <record id>` line per secret, or `no secrets` |
| `secret delete` | `secret deleted: <name>` |
| `profile create` | `profile created: <profile> (<path>)` |
| `profile add` | `profile updated: <profile> added <ENV>` |
| `profile remove` | `profile updated: <profile> removed <ENV>` |
| `profile show` | `profile: <profile>`, then one `<name>:<ENV> required=<true\|false>` line per mapping |
| `exec` | nothing; only the child's own output |
| `doctor` | `doctor: ok`, then one `warning: <text>` line per warning |
| `export`, `import` | `ok` |

Dry runs of `secret set`, `secret delete`, `profile create`, `profile add`,
`profile remove` and `exec` SHALL print a `dry run: …` summary instead
(`exec dry-run passed` for `exec`).

#### Scenario: Human error

- **WHEN** `env-vault secret check missing` finds no such secret
- **THEN** stderr receives `code=MISSING_SECRET`, `message=Missing secret: missing`, `remediation=Run: env-vault secret set missing` and env-vault exits `3`

#### Scenario: Quiet success

- **WHEN** the user runs `env-vault --quiet profile show dev` successfully
- **THEN** nothing is written to stdout or stderr and the exit code is `0`

### Requirement: Error codes and exit codes

Every env-vault error SHALL carry one of the following codes and exit with the
listed status:

| Code | Exit | Meaning |
|---|---|---|
| `RUNTIME_ERROR` | 1 | unexpected failure, including a failed `--output` write on the strict path |
| `SECRET_UNVERIFIED` | 1 | `secret set --verify` read back a different or missing value |
| `USAGE` | 2 | invalid invocation, arguments or flags |
| `CONFIRMATION_REQUIRED` | 2 | `secret delete --confirm` does not match |
| `PROFILE_EXISTS` | 2 | `profile create` for an existing profile |
| `PROFILE_NOT_FOUND` | 2 | the named profile does not exist |
| `ENV_COLLISION` | 2 | an `exec` target already exists in the environment |
| `SECRET_EXISTS` | 2 | `import --on-conflict fail` met an existing secret |
| `SECRET_TOO_LARGE` | 2 | a value exceeds the backend limit |
| `PASSPHRASE_INVALID` | 2 | an empty, short or mismatched transfer passphrase |
| `MISSING_SECRET` | 3 | a required secret is not stored |
| `BACKEND_UNAVAILABLE` | 4 | the secret backend failed, timed out, refused or was not selectable |
| `CONFIG_INVALID` | 5 | config unreadable, unsafe, malformed or rejected by validation |
| `CONFIG_LOCKED` | 5 | the config lock was not acquired in time |
| `BUNDLE_INVALID` | 5 | a transfer container is malformed or out of bounds |
| `BUNDLE_AUTH_FAILED` | 5 | a transfer container failed authentication |
| `COMMAND_NOT_EXECUTABLE` | 126 | the `exec` command is not executable |
| `COMMAND_NOT_FOUND` | 127 | the `exec` command does not exist |

`COMMAND_FAILED` SHALL appear only in the `--output` file of `exec`, whose
exit code is the child's (see `exec`).

#### Scenario: Backend failure

- **WHEN** any command needs the secret backend and the backend is unavailable
- **THEN** env-vault exits `4` with code `BACKEND_UNAVAILABLE` and a remediation naming the next step

### Requirement: Metadata output file

When `--output <path>` is set, env-vault SHALL write the same envelope it
renders, followed by a newline, to that path for both successful and failed
commands. The file SHALL be published atomically through a synced temporary
sibling with mode `0600`; missing parent directories SHALL be created with
mode `0700`. The target SHALL be a regular file or a new path; symlinks,
directories, devices and pipes SHALL be refused. On Windows a replacement
blocked by another program's open handle SHALL be retried for up to one
second within the same publication attempt.

#### Scenario: Error recorded in the file

- **WHEN** `env-vault --output meta.json secret check missing` fails
- **THEN** `meta.json` holds the `MISSING_SECRET` error envelope and the human error is still written to stderr

### Requirement: Metadata output path protection

Before a command runs, env-vault SHALL refuse an `--output` path that is the
same file as the local config `.env-vault.yaml`, the `--config` path, the
user config path, the `.lock` file of any of them, the `export --out`
container or the `import` container, comparing file identity including
existing aliases. Such a conflict SHALL fail with `USAGE` before any other
action and without writing to that path. The check SHALL be repeated
immediately before publication. A flag-parsing failure SHALL leave metadata
files untouched.

#### Scenario: Output path is the config

- **WHEN** the user runs `env-vault --config c.yaml --output c.yaml profile show dev`
- **THEN** env-vault exits `2` with `USAGE` and message `Metadata output conflicts with a config or container path`, and `c.yaml` is unchanged

### Requirement: Metadata write failure handling

For every command except a completed, non-dry-run `exec`, a failure to write
the `--output` file of a successful result SHALL fail the command with
`RUNTIME_ERROR` (exit `1`) and SHALL suppress the success output. A failure to
write the file for an error result SHALL keep the original error and exit
code. In both cases, and only with `--verbose`, env-vault SHALL write one
`OUTPUT_WRITE_FAILED: <reason>` line to stderr; `--quiet` SHALL NOT suppress
it. The `exec` rules are in the `exec` specification.

#### Scenario: Output path is a directory

- **WHEN** the user runs `env-vault --verbose --output <existing directory> profile show dev`
- **THEN** stderr receives `OUTPUT_WRITE_FAILED: path is not a regular file`, then the `RUNTIME_ERROR` error lines, and env-vault exits `1`

### Requirement: Dry run

`--dry-run` SHALL validate a command without storing, overwriting or deleting
secrets, without writing a config or lock file, without writing a transfer
container, and without starting a child process. An explicitly requested
`--output` file SHALL still be written. Commands that only read SHALL behave
the same with or without `--dry-run`. The data of every mutating command SHALL
include `dry_run`.

#### Scenario: Dry-run mutation

- **WHEN** the user runs `env-vault --dry-run profile create dev`
- **THEN** env-vault prints `dry run: profile dev would be created (<path>)`, exits `0`, and creates neither the config nor its lock file

### Requirement: Version reporting

`version` and `--version` SHALL report the build from the Go build
information embedded in the binary. The data SHALL contain `version`, `go`
(the Go version) and `platform` (`GOOS/GOARCH`) and, when VCS information is
present, `commit` (full hash), `commit_time` and `modified`. `version` SHALL
be the main module version, which is the release tag for a release build, or
`dev` when none is recorded. The human line SHALL show the version, the first
seven characters of the commit and the UTC commit date. This output SHALL NOT
be treated as proof of authenticity; release attestations provide that (see
`release-process`).

#### Scenario: Release binary

- **WHEN** a v0.4.6 release binary runs `env-vault --version`
- **THEN** the first word printed is `v0.4.6`, followed by the short commit and commit date in parentheses

### Requirement: Doctor diagnostics

`doctor` SHALL be read-only: it SHALL NOT write a config, take the config
lock, or read a secret value. Its data SHALL contain `config_path`,
`config_exists`, `backend` and `test_backend`. `backend` SHALL be `test` when
the complete test-backend gate is set, `pass` when `ENV_VAULT_BACKEND=pass`,
and `keyring` otherwise. A config load failure SHALL fail the command with its
config error. A backend selection failure SHALL be added to `warnings` as its
error text, and a failed metadata listing of the default service SHALL add
the warning `secret backend unavailable`; neither SHALL fail the command.

#### Scenario: Backend unavailable

- **WHEN** `env-vault --json doctor` runs where the OS keychain cannot be opened
- **THEN** the envelope has `ok: true`, the data reports the config path, and `warnings` contains `secret backend unavailable`

#### Scenario: Invalid config

- **WHEN** the selected config file contains invalid YAML
- **THEN** `doctor` exits `5` with `CONFIG_INVALID`
