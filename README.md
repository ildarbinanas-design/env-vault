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
- `--stdin` trims exactly one trailing line ending (`\n` or `\r\n`). It reads a
  pipe or a file and refuses a terminal, which would show the value as it is
  typed; there, omit `--stdin` and use the hidden prompt.
- Production storage uses `github.com/99designs/keyring` with OS keychain-style backends only: macOS Keychain, Linux Secret Service, Linux `pass`, KWallet, and Windows Credential Manager.
- Windows Credential Manager stores at most 2560 bytes per secret, and secret
  names that differ only in case are one secret there: `secret set TOKEN` over
  `token` reports `overwritten`, and the record follows the name as typed.
  Service names are still compared exactly, so keep one spelling per service.
  env-vault refuses a larger value with `SECRET_TOO_LARGE`; `import` checks
  every value it will write before it writes the first one.
- Secret and service identifiers may use safe slash-separated hierarchy, but absolute paths and empty, `.` or `..` components are rejected before backend access.
- The Linux `pass` backend is the exception for service names: it keeps the
  service and the secret name in one path, so the service `team/ci` with the
  secret `tok` and the service `team` with the secret `ci/tok` would be one
  entry. With `pass`, a service name cannot contain a slash, and a service with
  a slash never falls back to `pass`. The secret `tok` stored earlier under the
  service `team/ci` stays in the store and is reachable as the service `team`
  with the secret `ci/tok`.
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
The workflow builds the formula from the published release and opens the tap
pull request with auto-merge. The tap's `test` check runs style, audit,
installation, and version checks, and compares every url and sha256 with the
published checksums; the pull request merges only after it passes. See
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

Current version: `v0.4.0`. <!-- x-release-please-version -->

The line above is managed by Release Please. `v0.0.8` through `v0.0.11` are
preserved failed immutable tags and intentionally have no GitHub Release, and
`v0.3.3` was never tagged or published (its changes ship in `v0.3.4`); use the
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

On Linux, use `sha256sum -c` if `shasum` is not available.

The checksum only shows that the download is complete. To check that a file
was built by this repository's release workflow from `main`
([ADR 0012](docs/adr/0012-attestation-verification-pins-release-workflow.md)),
verify its attestation. This works for an archive and for an installed binary,
including one installed by Homebrew:

```sh
gh attestation verify "$(command -v env-vault)" \
  --repo ildarbinanas-design/env-vault \
  --signer-workflow ildarbinanas-design/env-vault/.github/workflows/release.yml \
  --source-ref refs/heads/main \
  --deny-self-hosted-runners
```

Releases up to v0.3.4 were built by the previous pipeline and do not verify
with this command.

**On macOS, manual download is not a supported install path.** Release
binaries are not Developer ID signed and not notarized
(see [ADR 0009](docs/adr/0009-no-code-signing-homebrew-only-macos-distribution.md)),
so `spctl --assess` rejects them. Any download path that attaches the
Gatekeeper quarantine attribute — a browser, or extraction from an archive
that itself carries the attribute — produces a binary macOS terminates on
launch. `xattr -d com.apple.quarantine env-vault` clears the attribute, but
it is a manual local override after you have verified the release source and
checksum; it is not a substitute for signing or notarization, and env-vault
never removes quarantine automatically.

Use the Homebrew path above on macOS. Homebrew does not attach the
quarantine attribute, which is why that path works.

## Install From Source

Source builds require Go 1.26.8 or newer. CI and release artifacts use the
exact stable patch declared in `go.mod`.

```sh
GOTOOLCHAIN=go1.26.8 go version
GOTOOLCHAIN=go1.26.8 go build -o env-vault ./cmd/env-vault
./env-vault version
```

## Version

`env-vault --version` prints the version, the short commit and the commit date,
for example `v0.4.0 (1bc4567, 2026-09-29)`. `env-vault --json version` adds the
full commit, the commit time, whether the source tree was modified, the Go
version and the platform:

```json
{"ok":true,"command":"version","timestamp":"2026-09-29T12:00:00Z","data":{"commit":"1bc45679516875794146ac11a5ba5dfcaa598c7a","commit_time":"2026-09-29T07:36:23Z","go":"go1.26.5","modified":false,"platform":"darwin/arm64","version":"v0.4.0"},"warnings":[],"error":null}
```

Every value comes from the build information Go embeds, which `go version -m`
also shows. A build from a checkout between releases reports a pseudo-version,
ending in `+dirty` if the tree had changes. A build without Git information has
no commit fields, and one without a module version prints `dev`. The output
helps diagnosis but proves nothing, because a modified binary can print
anything; the attestation check above proves where a binary came from.

## GitHub Builds

Pull-request and `main` CI call `reusable-quality.yml`: source tests, vet, a
govulncheck scan for reachable known vulnerabilities, the race suite and smoke
tests, three native license checks, and a native job for each of the five
targets that builds the binary and smoke-tests it against the platform's real
secret store. One target per operating system (linux-amd64,
darwin-arm64, windows-amd64) also runs the full E2E suite against the packaged
archive. The E2E reporter is built once from an isolated checksum-pinned tool
module, and each E2E job consumes only its source-SHA- and attempt-qualified
reporter artifact with network fallback disabled.

Releases follow [ADR 0011](docs/adr/0011-minimal-release-pipeline.md).
`release.yml` runs on every push to `main`, where Release Please maintains the
generated release pull request. Pull request titles follow Conventional
Commits because the squash title drives the version and changelog; only
`feat`, `fix`, `perf`, and `revert` changes create a release. See
[CONTRIBUTING.md](CONTRIBUTING.md) for the accepted types.

The only routine human release checkpoint is reviewing the generated release
pull request and squash-merging it with a server-side head guard
(`--match-head-commit`); that merge is the release authorization. The run for
the merge commit then:

- tags the merge commit and opens a draft release;
- builds the five targets from the tagged commit, checks that `--version`
  reports the tag, and smoke-tests the real secret store;
- packages five deterministic archives with SHA-256 files, attests the archives
  and the binaries, and publishes the release, which is immutable;
- verifies the published checksums and attestations;
- opens the Homebrew tap pull request with auto-merge.

A failed release is resumed by re-running the failed job of the same run. See
[RELEASING.md](RELEASING.md).

Supported targets are Linux amd64/arm64, macOS 15+ amd64/arm64, and Windows amd64.
Each release contains exactly five archives and five matching SHA-256 files.
There is no separate SBOM: `go version -m` lists every module with its hash.

macOS 15+ release artifacts are built on macOS runners with `CGO_ENABLED=1`;
the macOS Keychain backend requires darwin artifacts with CGO enabled. Linux
and Windows release targets keep `CGO_ENABLED=0`.

Every E2E runner executes the same public CLI scenarios against the
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

`exec` returns the child's exit code. On Unix, if the child is killed by
SIGHUP, SIGINT, SIGTERM, or SIGKILL, env-vault ends with the same signal, so a
calling shell loop stops as it would without env-vault. It exits with 128 plus
the signal number instead for other signals, for a SIGHUP or SIGINT it
inherited as ignored (as under `nohup`), and when it runs as PID 1.

On Unix, env-vault forwards SIGTERM, SIGHUP, SIGINT, and SIGQUIT to the child,
except SIGINT and SIGQUIT while env-vault and the child are both in the
terminal's foreground process group. There Ctrl+C and Ctrl+\ reach the child
directly, and forwarding would deliver them twice. env-vault cannot tell who
sent a signal, so in that situation a SIGINT or SIGQUIT sent to env-vault with
`kill` is not forwarded either; send SIGTERM, or signal the process group.

A SIGHUP or SIGINT that env-vault inherited as ignored, as under `nohup` or in
a script's background job, stays ignored: env-vault neither catches nor
forwards it, and the child inherits the same ignore. The job then survives a
closed terminal or an interrupted script, as it would without env-vault.

## Overwriting A Secret

Running `secret set` for an existing name replaces its value and reports
`secret overwritten` instead of `secret created`. The `record` shown by `set`,
`check`, and `list` identifies the stored record — the service and name — and
never the value, so it stays the same after an overwrite and cannot be used to
guess a value. To confirm that the new value is what the keychain now holds,
without printing it, add `--verify`:

```sh
env-vault secret set --verify nexus-token
# secret overwritten: nexus-token (record: e4ce7c4d5e6625c5, verified)
```

`--verify` reads the value back and compares it in constant time; a mismatch
fails with `SECRET_UNVERIFIED`. The write has already happened by then, so the
error says whether the secret was created or overwritten; an overwrite has
already replaced the previous value. On macOS the read-back is a Keychain access to
the item, so it can show the same access prompt as `exec`.

## Moving To Another Machine

`export` seals every stored secret value into one encrypted file; `import`
restores them into the keychain on the other side.

```sh
env-vault export --out vault.evb
env-vault import vault.evb
```

Profiles are not in the container, and do not need to be: `.env-vault.yaml`
holds only `secret-name -> ENV_NAME` mappings and no values, so it belongs in
your repository and travels with it. The keychain values are the only part that
cannot follow you, and that is exactly what the container carries.

Both commands ask for a passphrase at a hidden prompt — export asks twice,
because a typo would produce a file nobody can open. There is deliberately no
passphrase flag, environment variable, or file, for the same reason there is no
`--value` flag.

Useful options:

```sh
env-vault export --out vault.evb --force            # replace an existing container
env-vault import vault.evb --on-conflict skip       # keep values already stored here
env-vault import vault.evb --on-conflict overwrite  # replace them
env-vault --dry-run export --out vault.evb          # list what would be written
```

`--on-conflict` defaults to `fail`, which refuses the import and writes nothing
if any secret already exists here.

Export covers the default keychain service. If you stored something with
`secret set --service <name>`, name that service again on export — a keychain
offers no way to enumerate the services an application has used, so env-vault
cannot discover them for you:

```sh
env-vault export --out vault.evb --with-services team/ci
env-vault export --out vault.evb --with-services team/ci,team/ops
env-vault export --out vault.evb --with-services team/ci --with-services team/ops
```

The flag is `--with-services`, not `--service`, because it means something
different from the `--service` on `secret set`: there the flag *replaces* the
default service, here the default is always included and these are *added* to
it.

The container is AES-256-GCM encrypted under an Argon2id-derived key, and no
secret name appears outside the ciphertext. **Read the
[container limitations](docs/security.md#transfer-containers) before writing
one:** its strength is your passphrase rather than the operating system, and
there is no way to revoke a copy that has already been backed up, synced, or
committed. Keep `*.evb` out of git and out of cloud-sync folders.

## JSON And Dry Run

```sh
env-vault --json secret check nexus-token
env-vault --quiet --output env-vault-meta.json exec dev -- make test
env-vault --dry-run exec dev -- make test
env-vault --dry-run secret set nexus-token
```

Successful JSON output follows this shape:

```json
{"ok":true,"command":"secret_check","timestamp":"2026-07-06T00:00:00Z","data":{"fingerprint":"example","name":"nexus-token","record_id":"example","service":"env-vault"},"warnings":[],"error":null}
```

`record_id` identifies the record by service and name. `fingerprint` carries
the same value as a deprecated alias and will be removed in a later minor
release; read `record_id` instead.

Errors are structured with `code`, `message`, and `remediation`.

For `exec`, child stdout and stderr are inherited by default and may break machine-readable stdout. Prefer `--quiet --output file` for exec metadata without an additional envelope on stdout.

If the command exits with a non-zero status, env-vault exits with the same
status. If a signal kills it, env-vault ends by the same signal where it can,
and otherwise exits with 128 plus the signal number. The `--output` file then
records `"ok":false` with the error code `COMMAND_FAILED`, the status in
`data.exit_code` (128 plus the signal number for a signal) and, for a signal,
its name in `data.signal`, such as `SIGTERM`. env-vault prints nothing of its
own to stdout or stderr, except that `--verbose` reports `OUTPUT_WRITE_FAILED`
on stderr when the file cannot be written; the previous record then stays.

`--output` names a regular file or a new one; missing directories are created
with mode `0700`. env-vault writes a new file with mode `0600` next to it and
renames it into place, so a reader never sees a partial record. The directory
must therefore be writable, and the file belongs to whoever ran env-vault. A
symlink, a device such as `/dev/stdout`, or a pipe at that path is refused
instead of written through. On Windows, a replacement blocked by a program
that holds the file open, such as a virus scanner, is retried for up to a
second.

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

macOS asks for Keychain access the first time a given env-vault binary reads a
stored value: `exec`, `secret set --verify`, and `export`. Every Homebrew
upgrade installs a new binary, so the prompt returns once per secret after each
`brew upgrade env-vault`. A non-interactive `exec`, for example from a
LaunchAgent in a logged-in session, waits on that prompt for up to two minutes
and then fails with `BACKEND_UNAVAILABLE`; a denied prompt fails the same way.
After upgrading, run each profile once in a terminal and choose
**Always Allow**:

```sh
env-vault exec dev -- true
```

`secret check`, `secret list`, `profile add --check-secret`, `import` conflict
checks, and `doctor` only list stored names and never read a value.

Debian/Linux systems may require a Secret Service-compatible keyring daemon depending on desktop or headless setup. Linux also supports `pass` when the `pass` command is installed and the password store is initialized. Headless environments should use a CI secret manager or an explicit supported backend, not plaintext config.

To force `pass`, set `ENV_VAULT_BACKEND=pass`. If `pass` is unavailable, commands return `BACKEND_UNAVAILABLE` with remediation to install `pass` or use another supported OS keychain backend.

## Shell Init Warning

Do not put tokens into `bashrc`, `zshrc`, shell history, or shell init snippets. Aliases and completions are acceptable; secret values are not.

## Limitations

- Child processes can leak env if they print or forward it.
- OS process environment caveats still apply.
- env-vault does not rotate tokens by itself.
- An exported container is protected by its passphrase alone and cannot be
  revoked once it has been copied elsewhere.

## Rotation

Revoke and rotate credentials externally, then update the stored value:

```sh
env-vault secret set nexus-token
```

Remove stale mappings with:

```sh
env-vault profile remove dev NPM_TOKEN
```

Rotation does not reach exported containers. A container written before the
rotation still decrypts to the old value, so destroy any container that carried
a credential you have just rotated.

## Contributing

Keep changes small, tested, and security-focused. Do not add commands that print secret values. Do not add plaintext production storage. Include tests and update docs for behavior changes.

## Security Reports

Do not include secret values in issues, pull requests, logs, screenshots, terminal transcripts, or reproduction data. Use GitHub private vulnerability reporting when available, or contact the maintainer privately before sharing sensitive details.
