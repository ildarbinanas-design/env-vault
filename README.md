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
- Secret and service identifiers may use safe slash-separated hierarchy, but absolute paths and empty, `.` or `..` components are rejected before backend access.
- Config mutations reject symlink targets and use a synced mode-`0600` temporary sibling for same-directory replacement.
- Environment target names are compared case-insensitively so a profile remains unambiguous when moved to Windows.
- The `file`/plaintext keyring backend is not production-enabled.
- The test backend is insecure and enabled only when all three env vars are set: `ENV_VAULT_BACKEND=test`, `ENV_VAULT_ALLOW_INSECURE_TEST_BACKEND=1`, and `ENV_VAULT_TEST_STORE=/tmp/...`.
- Tests and smoke checks use generated ephemeral fixtures; stable secret payload fixtures are not stored in the repo.

## Install

### Homebrew (macOS and Linux)

```sh
brew install ildarbinanas-design/tap/env-vault
```

Supported platforms: macOS 15+ arm64/amd64 and Linux arm64/amd64. Homebrew
downloads do not receive the Gatekeeper quarantine attribute, so no
`xattr -d com.apple.quarantine` step is needed on macOS. The formula lives in
[ildarbinanas-design/homebrew-tap](https://github.com/ildarbinanas-design/homebrew-tap)
and is generated and proposed through a pull request by the release workflow.
The tap runs style, installation, and exact-version checks separately. The
release workflow opens or reuses a version-specific tap pull request, waits for
`test-formula.yml` on
the exact pull-request head, squash-merges that exact head, and then waits for
the workflow's successful `push` run on the resulting release merge commit.
Health also records the current tap head separately, proving that it still
contains that merge and the exact formula even if unrelated tap commits arrive
later. The release is healthy only after that post-merge check succeeds. See
[RELEASING.md](RELEASING.md). Upgrade with `brew upgrade env-vault`.

### Migrating a manual or `go install` installation to Homebrew

First inspect every executable that your shell can resolve and the current
Homebrew-prefix entry. These commands do not change anything:

```sh
type -a env-vault
ls -l /opt/homebrew/bin/env-vault
go version -m /opt/homebrew/bin/env-vault
brew link --overwrite env-vault --dry-run
```

The `/opt/homebrew` path is the default on Apple Silicon. If Homebrew uses a
different prefix, obtain it with `brew --prefix` and inspect that prefix's
`bin/env-vault` instead. The dry run may mention files that Homebrew would
replace, but it does not replace them.

If the dry run reports an unmanaged manual binary or symlink, move that exact
conflicting file to a new backup path before linking. Never overwrite an
existing backup:

```sh
backup_dir="$HOME/.local/share/env-vault/backups"
backup="$backup_dir/env-vault-pre-homebrew-$(date -u +%Y%m%dT%H%M%SZ)"
mkdir -p "$backup_dir"
test ! -e "$backup" && mv /opt/homebrew/bin/env-vault "$backup"
brew link env-vault
hash -r
```

If `type -a env-vault` shows a `go install` location such as
`$(go env GOPATH)/bin/env-vault` before the Homebrew path, back up that file in
the same way or remove its directory from the earlier part of `PATH`. Run
`type -a env-vault` and `env-vault --version` again after linking. The formula
does not opt into automatic overwriting of files it does not own.

### Manual download

Download the archive for your platform from the
[latest release](https://github.com/ildarbinanas-design/env-vault/releases/latest),
verify its checksum, and unpack (substitute the version, OS, and architecture):

Current version: `v0.0.11`. <!-- x-release-please-version -->

The line above is managed by Release Please. `v0.0.8` through `v0.0.11` are
preserved failed immutable tags and intentionally have no GitHub Release; use the
[`latest` Release](https://github.com/ildarbinanas-design/env-vault/releases/latest)
until the next version has completed automated publication and health checks.

```sh
VERSION=vX.Y.Z TARGET=darwin-arm64
curl -fsSLO "https://github.com/ildarbinanas-design/env-vault/releases/download/${VERSION}/env-vault-${TARGET}.tar.gz"
curl -fsSLO "https://github.com/ildarbinanas-design/env-vault/releases/download/${VERSION}/env-vault-${TARGET}.tar.gz.sha256"
shasum -a 256 -c "env-vault-${TARGET}.tar.gz.sha256"
tar xzf "env-vault-${TARGET}.tar.gz"
./env-vault-${TARGET}/env-vault --version
```

On Linux, use `sha256sum -c` if `shasum` is not available. With a manual
download on macOS, the browser or curl-less tooling may quarantine the binary;
`xattr -d com.apple.quarantine env-vault` removes the attribute. The Homebrew
path above avoids this entirely.

## Install From Source

Source builds require Go 1.26.5 or newer. CI and release artifacts use the
exact stable patch declared in `go.mod`.

```sh
GOTOOLCHAIN=go1.26.5 go version
GOTOOLCHAIN=go1.26.5 go build -o env-vault ./cmd/env-vault
./env-vault version
```

## GitHub Builds

Pull-request and `main` CI call `reusable-quality.yml`. One graph performs
source tests/vet/race/smoke, three native license checks, five native
build/package/E2E jobs, and one complete-matrix gate. The matrix gate verifies
the checked-in durable baseline; it does not depend on an expiring historical
workflow artifact.

For an exact release merge, a bounded native probe verifies `--version`,
`version`, and JSON version output on every native target with a scrubbed
environment. The file-only checker binds those saved results and seals the five
archives, five checksum sidecars, contract/coverage/leak results, and semantic
suite identity in one promotion manifest. Release planning verifies that exact
attempt before creating the immutable tag. The seven-job `build-binaries`
publisher then promotes those checked bytes, creates provenance and SPDX SBOM
attestations, publishes Homebrew through exact PR-head and post-merge CI gates,
and performs release health checks. It does not rebuild the product or repeat
source quality.

GitHub API transport, observation, and mutations use `gh`; the repository's
`releasecheck` tool is offline and validates saved JSON, manifests, and
artifacts. An incomplete attempt deterministically requests a full
`rerun_all_jobs`, never a failed-jobs-only artifact mixture. Manual publisher
dispatch can only resume `release-assets`, `homebrew`, or `health` for an exact
existing tag. `v0.0.1`–`v0.0.7` rebuilds are diagnostic-only and can never be
published; `v0.0.8` through `v0.0.11` remain failed tags without Releases.

The only routine human release checkpoint is semantic review plus an exact
version/PR/head-SHA authorization. Automation records that exact line as a
pre-merge generated-PR comment from the authorizing owner/member and binds its
identity and body digest into machine evidence. Planning, publisher,
supply-chain, Homebrew, health, machine evidence, and metrics then run
automatically. Planning and publication share one non-cancelling global
concurrency group.

Pull request titles follow Conventional Commits because the squash title is
the input to version and changelog generation. See [CONTRIBUTING.md](CONTRIBUTING.md)
for the accepted types and [RELEASING.md](RELEASING.md) for the complete
planning, publication, and repair contracts.

Supported targets are Linux amd64/arm64, macOS 15+ amd64/arm64, and Windows amd64.
Each release contains exactly five archives and five matching SHA-256 files.
The combined SPDX SBOM is a 14-day workflow artifact, and GitHub build
provenance and SBOM attestations are stored separately for all five archives;
they are deliberately not added to the immutable ten-asset Release contract.

macOS 15+ release artifacts are built on macOS runners with `CGO_ENABLED=1`;
the macOS Keychain backend requires darwin artifacts with CGO enabled. Linux
and Windows release targets keep `CGO_ENABLED=0`.

Every native CI runner executes the same public CLI scenarios against the
unpacked release-like artifact. The suite also builds a separate
coverage-instrumented subprocess binary, performs shuffled full and locking
burn-ins, scans all retained evidence for runtime-generated sentinel values,
and uploads JUnit, raw JSONL, feature coverage, normalized CLI contracts, and
HTML/text statement coverage for 30 days. See [docs/e2e.md](docs/e2e.md) for the
complete requirement matrix and local commands.

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
env-vault --quiet --output env-vault-meta.json exec dev -- make test
env-vault --dry-run exec dev -- make test
env-vault --dry-run secret set nexus-token
```

Successful JSON output follows this shape:

```json
{"ok":true,"command":"secret_check","timestamp":"2026-07-06T00:00:00Z","data":{"name":"nexus-token","fingerprint":"example"},"warnings":[],"error":null}
```

Errors are structured with `code`, `message`, and `remediation`.

For `exec`, child stdout and stderr are inherited by default and may break machine-readable stdout. Prefer `--quiet --output file` for exec metadata without an additional envelope on stdout.

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

`profile create`, `profile add`, and `profile remove` serialize their complete
read-modify-validate-save operation through a persistent adjacent
`<config>.lock` file. The lock is private (`0600` where POSIX modes apply) and
is intentionally not removed, because replacing it would allow two processes
to lock different inodes. Lock waits are bounded; a timeout returns
`CONFIG_LOCKED`. Dry runs do not create either the config or lock file, and
`profile add --check-secret` completes its backend existence check before
entering the config transaction. The default local `.env-vault.yaml.lock` is
gitignored alongside `.env-vault.yaml`.

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
