# profile-config Specification

## Purpose

Define the YAML profile config: its schema and validation, how env-vault
selects the config file, how it is written safely and serialized between
processes, and the behavior of `profile create`, `profile add`,
`profile remove` and `profile show`. A config holds secret names and
environment mappings only, never values.

## Requirements

### Requirement: Config schema

A config SHALL be a single YAML document with `version: 1` and an optional
`profiles` map from profile name to profile; a missing or null `profiles`
SHALL be read as an empty map. A profile SHALL have an optional `description`
and an optional `secrets` list; each mapping SHALL have `name` (secret name)
and `env` (target variable) and an optional boolean `required` that defaults
to `false` when omitted. Unknown fields SHALL be rejected. Validation SHALL require `version` to equal `1`, a
non-empty profile name, a valid secret name (see `secret-storage`), an `env`
matching `[A-Za-z_][A-Za-z0-9_]*`, and `env` values that are unique within a
profile when compared without regard to case. A config SHALL NOT contain
secret values.

#### Scenario: Unknown field

- **WHEN** a config contains `profiles.dev.secrets[0].value: abc`
- **THEN** every command that loads it fails with `CONFIG_INVALID` (exit `5`)

#### Scenario: Omitted profiles and required

- **WHEN** a config contains only `version: 1`, or a profile mapping omits `required`
- **THEN** the config loads as having no profiles, or the mapping is shown with `required: "false"` and treated as optional by `exec`

#### Scenario: Missing version

- **WHEN** a config omits `version`
- **THEN** loading fails with `CONFIG_INVALID`

#### Scenario: Targets differing only by case

- **WHEN** one profile maps `a:TOKEN` and `b:token`
- **THEN** loading the config fails with `CONFIG_INVALID`

#### Scenario: Trailing document

- **WHEN** a config file contains a second YAML document after the first
- **THEN** loading fails with `CONFIG_INVALID` and the message `Config must contain exactly one YAML document`

### Requirement: Config file selection

For reading, env-vault SHALL use the `--config` path when given; otherwise
`./.env-vault.yaml` when it exists; otherwise the user config:
`$XDG_CONFIG_HOME/env-vault/config.yaml` or
`~/.config/env-vault/config.yaml` on Linux,
`~/Library/Application Support/env-vault/config.yaml` on macOS, and
`%APPDATA%\env-vault\config.yaml` or
`~/AppData/Roaming/env-vault/config.yaml` on Windows. Fallback to the next
location SHALL happen only when the previous file is absent. A selected path
that is a symlink (including a dangling one), not a regular file, or
unreadable SHALL fail with `CONFIG_INVALID`. A selected config that does not
exist SHALL read as an empty config.

#### Scenario: Local config is a symlink

- **WHEN** `./.env-vault.yaml` is a symlink and no `--config` is given
- **THEN** commands that read the config fail with `CONFIG_INVALID` and do not fall back to the user config

### Requirement: Safe config writes

A config mutation SHALL create missing parent directories with mode `0700`,
refuse a symlink or non-regular target, write the complete new content to a
temporary sibling with mode `0600`, sync it, and replace the target in the
same directory. It SHALL never truncate the target or remove it before the
replacement. On Windows, transient sharing and access violations during reads
and replacement SHALL be retried within a fixed deadline. A mutation that
changes nothing SHALL NOT rewrite the file.

#### Scenario: Config target is a symlink

- **WHEN** `--config` points to a symlink and the user runs `profile create dev`
- **THEN** env-vault fails with `CONFIG_INVALID`, message `Unsafe config target`, and the symlink target is not modified

### Requirement: Serialized profile mutations

`profile create`, `profile add` and `profile remove` SHALL hold an exclusive
lock on `<config>.lock` for the whole load–mutate–validate–save sequence. The
lock file SHALL be created with mode `0600` where POSIX modes apply, SHALL be
refused when it is a symlink or not a regular file, and SHALL remain after
unlock. Acquisition SHALL retry every 25 milliseconds for at most five
seconds, then fail with `CONFIG_LOCKED` (exit `5`). A requested secret
existence check SHALL complete before the lock is taken. Reads SHALL NOT take
the lock.

#### Scenario: Concurrent mutation

- **WHEN** another process holds the lock for longer than five seconds
- **THEN** `profile add` fails with `CONFIG_LOCKED` and the remediation `Retry after the other profile command finishes`

### Requirement: Creating a profile

`profile create <profile>` SHALL add an empty profile. It SHALL write to the
`--config` path when given; otherwise to `./.env-vault.yaml` (the default, or
with `--local`) or to the user config with `--global`; `--config` takes
precedence over both flags. Without `--config`, `--local` together with
`--global` SHALL fail with `USAGE`. An existing profile SHALL fail with
`PROFILE_EXISTS` (exit `2`). Any non-empty profile name SHALL be accepted.
The data SHALL contain `profile`, `path` and `dry_run`.

#### Scenario: Default target

- **WHEN** the user runs `env-vault profile create dev` without `--config`
- **THEN** `./.env-vault.yaml` is created or updated and stdout reports `profile created: dev (.env-vault.yaml)`

### Requirement: Adding a mapping

`profile add <profile> <secret-name:ENV_NAME>` SHALL parse the mapping at the
first `:`, validate both parts, and append it with `required: true` to the
config selected for reading. A missing profile SHALL fail with
`PROFILE_NOT_FOUND`. A mapping identical to an existing one SHALL succeed
without changing the file. A different mapping whose target equals an
existing target without regard to case SHALL fail with `CONFIG_INVALID` and
the message `Target env var is already mapped: <ENV>`. With
`--check-secret`, the secret SHALL be checked in the default service first,
and a missing secret SHALL fail with `MISSING_SECRET`. The data SHALL contain
`profile`, `name`, `env`, `path` and `dry_run`.

#### Scenario: Target collision

- **WHEN** profile `dev` maps `tok:TOKEN` and the user runs `profile add dev other:token`
- **THEN** env-vault exits `5` with `CONFIG_INVALID` and the config is unchanged

#### Scenario: Repeated identical mapping

- **WHEN** the user runs `profile add dev tok:TOKEN` twice
- **THEN** both runs exit `0`, report `profile updated: dev added TOKEN`, and the profile holds one mapping

### Requirement: Removing a mapping

`profile remove <profile> <selector>` SHALL accept either `ENV_NAME`, matching
the `env` value exactly (case-sensitive), or `secret-name:ENV_NAME`, matching
both fields exactly, and SHALL remove every matching mapping. A missing
profile SHALL fail with `PROFILE_NOT_FOUND`; an invalid selector with
`USAGE`; no match with `CONFIG_INVALID` (exit `5`) and the message
`Mapping not found: <selector>`. The data SHALL contain `profile`, `env`,
`path` and `dry_run`.

#### Scenario: Selector in a different case

- **WHEN** profile `dev` maps `tok:TOKEN` and the user runs `profile remove dev token`
- **THEN** env-vault exits `5` with `CONFIG_INVALID` and message `Mapping not found: token`

### Requirement: Showing a profile

`profile show <profile>` SHALL report the profile's mappings without values,
as data `profile`, `path` and `secrets`, where each entry has `name`, `env`
and `required` (the string `true` or `false`). A missing profile SHALL fail
with `PROFILE_NOT_FOUND`.

#### Scenario: Show in JSON

- **WHEN** the user runs `env-vault --json profile show dev` for a profile mapping `tok:TOKEN`
- **THEN** `data.secrets` is `[{"env":"TOKEN","name":"tok","required":"true"}]`

### Requirement: Dry-run profile mutations

With `--dry-run`, profile mutations SHALL validate the target path, load the
config, apply the mutation in memory and validate the result, and SHALL NOT
create directories, the config or the lock file.

#### Scenario: Dry-run add

- **WHEN** the user runs `env-vault --dry-run profile add dev tok:TOKEN` for an existing profile
- **THEN** stdout reports `dry run: profile dev would add TOKEN` and the config file is unchanged
