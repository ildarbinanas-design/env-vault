# Spec Delta

## MODIFIED Requirements

### Requirement: Strict errors outside successful execution

Errors that occur before the child runs, while starting or waiting for it,
while copying its streams, or while writing env-vault's own stdout SHALL keep
their error codes and SHALL NOT be turned into success by the metadata
failure rule. `exec --dry-run` SHALL follow the strict metadata rule of
`cli-output`. When the operating system refuses to start the child with
`E2BIG`, env-vault SHALL retain `RUNTIME_ERROR`, exit `1` and the message
`Unable to start command`, and SHALL use the remediation `The environment
exceeds the operating system limit; reduce the size or number of secrets`.
The error SHALL NOT contain secret values or the child environment.

#### Scenario: Dry run with an unwritable output file

- **WHEN** a valid `exec --dry-run` uses `--output` on an existing directory
- **THEN** env-vault exits `1` with `RUNTIME_ERROR` and no child runs

#### Scenario: Missing executable with an unwritable output file

- **WHEN** the command does not exist and `--output` cannot be written
- **THEN** env-vault exits `127` with `COMMAND_NOT_FOUND`

#### Scenario: Existing value exceeds the Linux environment string limit

- **WHEN** on Linux an existing generated 131072-byte value is mapped into a child's environment and process creation fails with `E2BIG`
- **THEN** the child does not run, env-vault exits `1` with `RUNTIME_ERROR` and message `Unable to start command`, and the remediation is `The environment exceeds the operating system limit; reduce the size or number of secrets`

#### Scenario: Aggregate environment exceeds the operating system limit

- **WHEN** mapped values individually satisfy the write limit but the complete command and environment cause process creation to fail with `E2BIG`
- **THEN** env-vault reports the same `RUNTIME_ERROR` and environment-size remediation without exposing values or changing the exit code
