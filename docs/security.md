# Security

## Secret Handling

env-vault does not print secret values. It does not store secret values in config, logs, docs, CI metadata, JSON output, JSONL output, or evidence bundles.

Secret input is limited to:

- a hidden interactive prompt;
- `--stdin`, which trims exactly one trailing newline byte.

There is no `secret get` command and no command-line flag for passing a secret value.

## Config

Config files store only profile mappings:

- secret name;
- target environment variable;
- required flag;
- optional profile description.

Config files are created with mode `0600` where applicable.

## Backend Assumptions

Production secret storage uses the operating system keychain through `github.com/99designs/keyring`. The production backend allowlist excludes plaintext file-style fallback.

The insecure test backend is available only when all three gates are set:

- `ENV_VAULT_BACKEND=test`
- `ENV_VAULT_ALLOW_INSECURE_TEST_BACKEND=1`
- `ENV_VAULT_TEST_STORE=/tmp/...`

The test backend is never a production fallback.

Tests and smoke checks generate ephemeral secret fixture values at runtime. Stable secret payload fixtures must not be committed to tests, scripts, docs, CI output, JSON/JSONL examples, or evidence bundles.

## Known Limitations

- A child process receives secret values through environment variables and can leak them if it prints or forwards its environment.
- On Linux, process environment variables may be visible to the same user through `/proc` in some environments.
- OS keychain availability depends on the platform session and keyring daemon.
- env-vault does not rotate credentials by itself.

## Bug Reports

Do not include secret values in bug reports, terminal transcripts, screenshots, logs, or reproduction data. Include command names, structured error codes, platform, keychain backend notes, and redacted config mappings only.

Security reports should be sent privately once a public maintainer contact exists. Until then, keep reports local and do not publish secret-bearing evidence.
