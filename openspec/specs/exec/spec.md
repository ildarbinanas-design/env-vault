# exec Specification

## Purpose

Define how `env-vault exec` resolves secret mappings, builds the child
environment, launches the child, forwards signals, propagates the child's
result, and reports it on the terminal and in the `--output` metadata file.
The child receives values in its environment and controls what it does with
them; env-vault cannot prevent a child from printing them.

## Requirements

### Requirement: Invocation

`exec` SHALL be invoked as `exec [<profile>] [--secret <secret-name:ENV_NAME>
...] [--override-env] [--clean-env] -- <command> [args...]`. A missing `--`
SHALL fail with `USAGE` and the message `Missing -- delimiter before child
command`. More than one argument before `--` SHALL fail with `USAGE`. An empty
command after `--` SHALL fail with `USAGE`. The child argv SHALL be executed
directly, without a shell; a shell runs only when the user names one, for
example `-- bash -lc '...'`.

#### Scenario: Missing delimiter

- **WHEN** the user runs `env-vault exec dev make test`
- **THEN** env-vault exits `2` with `USAGE` and does not read any secret

### Requirement: Mapping sources

`exec` SHALL combine the mappings of the named profile (from the config
selected for reading, see `profile-config`) followed by every `--secret`
mapping, which is always required. All secrets SHALL be read from the default
service `env-vault`. A missing profile SHALL fail with `PROFILE_NOT_FOUND`. An
invalid `--secret` value SHALL fail with `USAGE`. Two mappings with the same
target, compared without regard to case, SHALL fail with `CONFIG_INVALID`
(exit `5`) before any secret is read.

#### Scenario: Direct mapping duplicates a profile target

- **WHEN** profile `dev` maps `tok:TOKEN` and the user adds `--secret other:token`
- **THEN** env-vault exits `5` with `CONFIG_INVALID` and message `Duplicate target env var: token`

### Requirement: Resolution before launch

env-vault SHALL resolve every mapping before starting the child. A missing
required secret SHALL fail with `MISSING_SECRET` (exit `3`); a missing
optional secret (`required: false`) SHALL be skipped and its target left
unset; a backend failure SHALL fail with `BACKEND_UNAVAILABLE` (exit `4`). In
each of these cases the child SHALL NOT start.

#### Scenario: Missing required secret

- **WHEN** profile `dev` maps a required secret that is not stored
- **THEN** env-vault exits `3` with `MISSING_SECRET`, names the secret, and the child never runs

#### Scenario: Missing optional secret

- **WHEN** a mapping has `required: false` and its secret is not stored
- **THEN** the child runs without that variable and the result omits the mapping

### Requirement: Child environment

The child environment SHALL start from env-vault's own environment, or with
`--clean-env` from only `PATH`, `HOME`, `USER`, `LOGNAME`, `TMPDIR`, `TMP`,
`TEMP`, `SYSTEMROOT`, `WINDIR` and `COMSPEC` (names compared without regard to
case; the last occurrence of a repeated name wins). A mapping whose target
already exists in that base environment, compared without regard to case,
SHALL fail with `ENV_COLLISION` (exit `2`) unless `--override-env` is set.
With `--override-env`, the existing entry SHALL be replaced by one entry using
the mapping's spelling of the name.

#### Scenario: Collision without override

- **WHEN** `TOKEN` is already set and profile `dev` maps `tok:TOKEN`
- **THEN** `env-vault exec dev -- true` exits `2` with `ENV_COLLISION` and the remediation `Use --override-env or choose a different target env var`

#### Scenario: Clean environment

- **WHEN** the user runs `env-vault exec --clean-env dev -- env`
- **THEN** the child sees only the kept variables that were set plus the mapped secrets

### Requirement: Command validation

After resolving the mappings and before launch, env-vault SHALL check the
command. A command containing a path separator SHALL be checked at that path;
otherwise it SHALL be looked up in `PATH`. A missing command SHALL fail with
`COMMAND_NOT_FOUND` (exit `127`); a directory or a file without execute
permission (on Unix) SHALL fail with `COMMAND_NOT_EXECUTABLE` (exit `126`).
A start failure for any other reason SHALL fail with `RUNTIME_ERROR`.

#### Scenario: Unknown command

- **WHEN** the user runs `env-vault exec dev -- nope-cmd`
- **THEN** env-vault exits `127` with `COMMAND_NOT_FOUND` and message `Command not found: nope-cmd`

### Requirement: Standard streams

The child SHALL inherit env-vault's stdin, stdout and stderr. env-vault SHALL
NOT add output to stdout or stderr while the child runs. Child output can
interleave with, and break, machine-readable stdout.

#### Scenario: Child output passes through

- **WHEN** the child writes to stdout and stderr
- **THEN** the same bytes appear on env-vault's stdout and stderr

### Requirement: Signal forwarding on Unix

On Unix, env-vault SHALL forward SIGTERM, SIGHUP, SIGINT and SIGQUIT to the
child. While env-vault and the child are both in the terminal's foreground
process group, SIGINT and SIGQUIT SHALL NOT be forwarded, because the terminal
already delivered them to the child; this also applies to such a signal sent
only to env-vault. A SIGHUP or SIGINT inherited as ignored, as under `nohup`,
SHALL stay ignored for env-vault and the child. On Windows, env-vault SHALL
catch an interrupt instead of exiting and keep waiting for the child, which
receives the console's Ctrl+C itself.

#### Scenario: Ctrl+C in a terminal

- **WHEN** the user presses Ctrl+C while the child runs in the foreground
- **THEN** the child receives exactly one SIGINT

#### Scenario: Running under nohup

- **WHEN** env-vault is started under `nohup` and the terminal hangs up
- **THEN** neither env-vault nor the child is terminated by SIGHUP

### Requirement: Exit status propagation

env-vault SHALL exit with the child's exit code. On Unix, when a signal kills
the child, env-vault SHALL end itself with the same signal if it is SIGHUP,
SIGINT, SIGTERM or SIGKILL, it was not inherited as ignored, and env-vault is
not PID 1; otherwise it SHALL exit with `128 + <signal number>`.

#### Scenario: Non-zero exit

- **WHEN** the child exits with status `7`
- **THEN** env-vault exits `7` and writes nothing of its own to stdout or stderr

#### Scenario: Child killed by SIGTERM

- **WHEN** the child is killed by SIGTERM under ordinary Unix conditions
- **THEN** env-vault ends by SIGTERM, so its shell sees status `143`

### Requirement: Exec result data

The data of an `exec` result SHALL contain `argv`, `secret_count`, `secrets`
(each with `name`, `env`, `record_id` and `fingerprint`), `override_env`,
`clean_env` and `dry_run`, and SHALL NOT contain secret values or the child
environment. On success, human mode SHALL print nothing of env-vault's own;
`--json` and `--jsonl` SHALL write the success envelope to stdout after the
child's output.

#### Scenario: Successful child in JSON mode

- **WHEN** `env-vault --json exec dev -- true` succeeds
- **THEN** stdout holds one success envelope with `command: "exec"` and no `exit_code` field

### Requirement: Failed child reporting

When the child exits non-zero or is killed by a signal, env-vault SHALL write
nothing of its own to stdout or stderr and SHALL record the failure only in
the `--output` file: an envelope with `ok: false`, the exec data plus
`exit_code` and, for a signal, `signal` (for example `SIGTERM`), and the error
`COMMAND_FAILED` with the message `Command exited with status <n>` or
`Command was killed by <SIGNAL> (status <n>)` and the remediation
`Inspect the command's output`. A failure to write that file SHALL be reported
only with `--verbose`, as one `OUTPUT_WRITE_FAILED: <reason>` line on stderr,
and SHALL NOT change the exit code.

#### Scenario: Failure recorded in metadata

- **WHEN** `env-vault --quiet --output meta.json exec dev -- sh -c 'exit 3'` runs
- **THEN** env-vault exits `3`, prints nothing of its own, and `meta.json` holds `COMMAND_FAILED` with `data.exit_code: 3`

#### Scenario: Signal recorded in metadata

- **WHEN** the child is killed by SIGTERM under ordinary Unix conditions with a writable `--output`
- **THEN** the file holds `COMMAND_FAILED`, `data.exit_code: 143` and `data.signal: "SIGTERM"`

### Requirement: Child success survives a metadata write failure

After a child that actually ran exits `0` without start, wait or stream
errors, env-vault SHALL exit `0` even when publishing the `--output` file
fails. It SHALL attempt that publication once (its bounded internal retries
included), SHALL NOT rerun the child, and SHALL NOT replace an older metadata
record or write an error envelope because of the failure. Human output SHALL
add nothing; `--json` and `--jsonl` SHALL still write the unchanged success
envelope (no added `exit_code`, warning or error) to stdout, also with
`--quiet`. Only `--verbose` SHALL add one `OUTPUT_WRITE_FAILED: <reason>` line
to stderr after the child's output; `--quiet` SHALL NOT suppress it.

#### Scenario: Output path is a directory

- **WHEN** the child exits `0` and `--output` names an existing directory
- **THEN** env-vault exits `0`, the directory is unchanged, and the child ran exactly once

#### Scenario: Verbose diagnostic

- **WHEN** the same command runs with `--verbose`, with or without `--quiet`
- **THEN** stderr ends with one `OUTPUT_WRITE_FAILED: path is not a regular file` line and the exit code stays `0`

#### Scenario: Machine output after a file failure

- **WHEN** the child exits `0`, the file cannot be written and `--json` is set
- **THEN** stdout holds the child's output followed by one success envelope with `warnings: []` and `error: null`

### Requirement: Output path protection around the child

The `--output` conflict check (see `cli-output`) SHALL run before the child
starts; a conflict SHALL fail with `USAGE` and the child SHALL NOT start. The
check SHALL run again before publication; a conflict that appears while the
child runs SHALL prevent the write and SHALL be handled as a metadata write
failure, keeping the child's result.

#### Scenario: Alias created by the child

- **WHEN** the initial check passes, the child creates a hard link to the config at the `--output` path and exits `0`
- **THEN** env-vault does not write that path, exits `0`, and reports `OUTPUT_WRITE_FAILED` only with `--verbose`

### Requirement: Strict errors outside successful execution

Errors that occur before the child runs, while starting or waiting for it,
while copying its streams, or while writing env-vault's own stdout SHALL keep
their error codes and SHALL NOT be turned into success by the metadata
failure rule. `exec --dry-run` SHALL follow the strict metadata rule of
`cli-output`.

#### Scenario: Dry run with an unwritable output file

- **WHEN** a valid `exec --dry-run` uses `--output` on an existing directory
- **THEN** env-vault exits `1` with `RUNTIME_ERROR` and no child runs

#### Scenario: Missing executable with an unwritable output file

- **WHEN** the command does not exist and `--output` cannot be written
- **THEN** env-vault exits `127` with `COMMAND_NOT_FOUND`

### Requirement: Dry-run exec

With `--dry-run`, `exec` SHALL perform every check above — mappings,
existence of each secret through the key listing, environment collisions and
the command — without reading values, injecting them or starting the child.
On success, human mode SHALL print `exec dry-run passed`.

#### Scenario: Dry run with all secrets present

- **WHEN** the user runs `env-vault --dry-run exec dev -- make test` and every required secret exists
- **THEN** env-vault prints `exec dry-run passed`, exits `0`, and `make` does not run
