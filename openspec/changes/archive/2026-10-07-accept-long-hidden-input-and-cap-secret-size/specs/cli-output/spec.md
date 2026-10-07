# Spec Delta

## MODIFIED Requirements

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
| `SECRET_TOO_LARGE` | 2 | a value exceeds the size limit (65536 bytes; 2560 on Windows Credential Manager) |
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

#### Scenario: Common size limit in JSON output

- **WHEN** `env-vault --json secret set token --stdin` receives a generated 65537-byte value
- **THEN** env-vault exits `2` with `command: "secret_set"`, `data: null`, code `SECRET_TOO_LARGE` and a remediation to use a shorter value, without including the value in any output field

#### Scenario: Windows size limit retains its error code

- **WHEN** Windows Credential Manager is selected and `secret set token --stdin` receives a generated 2561-byte value
- **THEN** env-vault exits `2` with `SECRET_TOO_LARGE`, without treating the size refusal as `BACKEND_UNAVAILABLE`
