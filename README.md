# env-vault

env-vault is a local Go CLI for running commands with environment variables resolved from OS-keychain-backed secret profiles. It stores profile mappings in YAML and secret values in the operating system keychain.

```text
github.com/ildarbinanas-design/env-vault
```

env-vault is an independent project and is not affiliated with any employer, vendor, or similarly named vault product.

## Threat Model

env-vault reduces accidental exposure from shell history, plaintext config, and repeated manual exports. It does not make a child process safe: a child command can leak a secret if it prints its environment or forwards it elsewhere.

On Linux, process environment variables may be visible to the same user through `/proc` in some environments. Use short-lived commands, trusted child processes, and a CI secret manager for headless automation.

## Security Model

- Secret values are never printed by env-vault.
- Config files store only profile mappings, never secret values.
- There is no `secret get` command.
- There is no `--value` flag.
- Secret input is accepted only through a hidden prompt or `--stdin`.
- `--stdin` trims exactly one trailing newline byte.
- Production storage uses `github.com/99designs/keyring` with OS keychain-style backends only: macOS Keychain, Linux Secret Service, Linux `pass`, KWallet, and Windows Credential Manager.
- The `file`/plaintext keyring backend is not production-enabled.
- The test backend is insecure and enabled only when all three env vars are set: `ENV_VAULT_BACKEND=test`, `ENV_VAULT_ALLOW_INSECURE_TEST_BACKEND=1`, and `ENV_VAULT_TEST_STORE=/tmp/...`.
- Tests and smoke checks use generated ephemeral fixtures; stable secret payload fixtures are not stored in the repo.

## Install

### Homebrew (macOS and Linux)

```sh
brew install ildarbinanas-design/tap/env-vault
```

Supported platforms: macOS arm64/amd64 and Linux arm64/amd64. Homebrew
downloads do not receive the Gatekeeper quarantine attribute, so no
`xattr -d com.apple.quarantine` step is needed on macOS. The formula lives in
[ildarbinanas-design/homebrew-tap](https://github.com/ildarbinanas-design/homebrew-tap)
and is updated automatically on every release. Upgrade with
`brew upgrade env-vault`.

### Manual download

Download the archive for your platform from the
[latest release](https://github.com/ildarbinanas-design/env-vault/releases/latest),
verify its checksum, and unpack (substitute the version, OS, and architecture):

```sh
VERSION=v0.0.3 TARGET=darwin-arm64
curl -fsSLO "https://github.com/ildarbinanas-design/env-vault/releases/download/${VERSION}/env-vault-${TARGET}.tar.gz"
curl -fsSLO "https://github.com/ildarbinanas-design/env-vault/releases/download/${VERSION}/env-vault-${TARGET}.tar.gz.sha256"
shasum -a 256 -c "env-vault-${TARGET}.tar.gz.sha256"
tar xzf "env-vault-${TARGET}.tar.gz"
./env-vault-${TARGET}/env-vault version
```

On Linux, use `sha256sum -c` if `shasum` is not available. With a manual
download on macOS, the browser or curl-less tooling may quarantine the binary;
`xattr -d com.apple.quarantine env-vault` removes the attribute. The Homebrew
path above avoids this entirely.

## Install From Source

```sh
go build -o env-vault ./cmd/env-vault
./env-vault version
```

## GitHub Builds

Binary archives are built by the `build-binaries` GitHub Actions workflow.

- Manual build: open **Actions** -> **build-binaries** -> **Run workflow**.
- Release build: push a version tag such as `v0.0.2`; the workflow builds archives and attaches them to a GitHub Release.

Supported targets are Linux amd64/arm64, macOS amd64/arm64, and Windows amd64.

macOS release artifacts are built on macOS runners with `CGO_ENABLED=1`; the macOS Keychain backend requires darwin artifacts with CGO enabled. Linux and Windows release targets keep `CGO_ENABLED=0`.

## Basic Usage

```sh
env-vault secret set nexus-token
env-vault profile create dev
env-vault profile add dev nexus-token:NPM_TOKEN
env-vault exec dev -- make test
env-vault exec --secret nexus-token:NPM_TOKEN -- make test
```

`exec` does not launch a shell by default. This is direct argv execution:

```sh
env-vault exec dev -- make test
```

A shell is used only when you explicitly provide one:

```sh
env-vault exec dev -- bash -lc 'make test'
```

## JSON And Dry Run

```sh
env-vault --json secret check nexus-token
env-vault --json --output env-vault-meta.json exec dev -- make test
env-vault --dry-run exec dev -- make test
env-vault --dry-run secret set nexus-token
```

Successful JSON output follows this shape:

```json
{"ok":true,"command":"secret_check","timestamp":"2026-07-06T00:00:00Z","data":{"name":"nexus-token","fingerprint":"example"},"warnings":[],"error":null}
```

Errors are structured with `code`, `message`, and `remediation`.

For `exec`, child stdout and stderr are inherited by default and may break machine-readable stdout. Prefer `--json --output file` for exec metadata.

## Doctor

```sh
env-vault doctor
env-vault --json doctor
```

`doctor` reports config path and backend status without printing secret values.

## Config

The local config file is `.env-vault.yaml`. If present, it has priority over the user config for profile definitions.

User config defaults:

- Linux: `$XDG_CONFIG_HOME/env-vault/config.yaml` or `~/.config/env-vault/config.yaml`
- macOS: `~/Library/Application Support/env-vault/config.yaml`

Example:

```yaml
version: 1
profiles:
  dev:
    description: local development
    secrets:
      - name: nexus-token
        env: NPM_TOKEN
        required: true
```

## Platform Notes

macOS uses the system Keychain through the selected Go backend.

Debian/Linux systems may require a Secret Service-compatible keyring daemon depending on desktop or headless setup. Linux also supports `pass` when the `pass` command is installed and the password store is initialized. Headless environments should use a CI secret manager or an explicit supported backend, not plaintext config.

To force `pass`, set `ENV_VAULT_BACKEND=pass`. If `pass` is unavailable, commands return `BACKEND_UNAVAILABLE` with remediation to install `pass` or use another supported OS keychain backend.

## Shell Init Warning

Do not put tokens into `bashrc`, `zshrc`, shell history, or shell init snippets. Aliases and completions are acceptable; secret values are not.

## Limitations

- Child processes can leak env if they print or forward it.
- OS process environment caveats still apply.
- env-vault does not rotate tokens by itself.

## Rotation

Revoke and rotate credentials externally, then update the stored value:

```sh
env-vault secret set nexus-token
```

Remove stale mappings with:

```sh
env-vault profile remove dev NPM_TOKEN
```

## Contributing

Keep changes small, tested, and security-focused. Do not add commands that print secret values. Do not add plaintext production storage. Include tests and update docs for behavior changes.

## Security Reports

Do not include secret values in issues, pull requests, logs, screenshots, terminal transcripts, or reproduction data. Use GitHub private vulnerability reporting when available, or contact the maintainer privately before sharing sensitive details.
