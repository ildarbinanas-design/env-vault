# Design

## Architecture

env-vault is a Go CLI with a small package boundary:

- `internal/cli`: Cobra command wiring and global flags.
- `internal/config`: YAML config schema, paths, validation, mapping parser, and
  cross-platform profile transaction lock.
- `internal/secretstore`: backend-neutral secret interface and non-secret fingerprinting.
- `internal/secretstore/keyring`: production OS keychain backend using `github.com/99designs/keyring`.
- `internal/secretstore/teststore`: explicitly gated insecure backend for tests only.
- `internal/runner`: exec resolver, env collision checks, process launch, exit-code propagation, and signal forwarding.
- `internal/output`: human, JSON, JSONL, and `--output` envelope rendering.
- `internal/redact`: last-resort string redaction for diagnostics.
- `internal/errors`: structured error contract.
- `internal/platform`: config path and platform helpers.

## SecretStore Interface

The interface supports `Set`, `Get`, `Exists`, `Delete`, and `List`. Commands never expose `Get` to the user. `Get` exists only so `exec` can inject values into the child environment.

Production storage uses `github.com/99designs/keyring` with an explicit allowlist: macOS Keychain, Secret Service, KWallet, Windows Credential Manager, and `pass`. `pass` is kept after the platform keychain backends so discovery still prefers the native OS stores first. `keyring.FileBackend`, plaintext/env-file storage, and Passwork are not production backends.

The public metadata fingerprint is not derived from secret values:

```text
sha256(service + "\x00" + secretName), truncated to 16 hex chars
```

## Config Schema

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

Secret names allow letters, digits, dot, underscore, dash, slash, and at-sign.
Slash-separated hierarchy is preserved, but absolute paths, empty components,
`.`/`..` components, backslashes, colon, newline, and control characters are
rejected. Service names use the same path-safety rules, and the production
keyring adapter repeats both validations before opening a backend. Environment
variables must match `[A-Za-z_][A-Za-z0-9_]*`; names that differ only by case
are treated as the same portable target because Windows environment names are
case-insensitive.

Config saves reject a symlink target and publish a mode `0600` temporary sibling
with `fsync` followed by atomic rename. This prevents truncation, partial reads,
and writes through a tracked config symlink. Profile create/add/remove wrap the
complete load, mutation, validation, and atomic save in an exclusive lock from
`github.com/gofrs/flock v0.13.0`, verified by the unchanged cross-platform E2E
contract on Go 1.26.5. The adjacent `<config>.lock` file is created with mode `0600`,
rechecked as a non-symlink regular file, and intentionally kept after unlock so
all processes continue to coordinate on one inode. Acquisition retries every
25 milliseconds for at most five seconds (or the caller's earlier deadline),
then returns `CONFIG_LOCKED`. Dry runs do not create a lock. A requested secret
existence check completes before the config transaction begins.

## Exec Flow

1. Validate the `--` delimiter and child argv.
2. Load profile mappings if a profile is supplied.
3. Parse direct `--secret <secret-name:ENV_NAME>` mappings.
4. Validate duplicate mappings and environment collisions.
5. Resolve all required secrets before spawning the child.
6. Build the child environment from inherited env or `--clean-env`.
7. Spawn the command directly without a shell.
8. Inherit child stdin/stdout/stderr by default.
9. Best-effort forward process signals.
10. Propagate the child exit code where possible.

`env-vault exec ... -- bash -lc ...` is allowed because the user explicitly supplied the shell.

## Output Schema

Success:

```json
{"ok":true,"command":"secret_set","timestamp":"RFC3339","data":{},"warnings":[],"error":null}
```

Error:

```json
{"ok":false,"command":"exec","timestamp":"RFC3339","data":null,"warnings":[],"error":{"code":"MISSING_SECRET","message":"Missing secret: nexus-token","remediation":"Run: env-vault secret set nexus-token"}}
```

Human errors use the same fields: `code`, `message`, and `remediation`.

## Dry Run

`--dry-run` validates without mutation or child execution.

- `secret set` validates name and backend selection but does not read or store input.
- profile mutations validate and report planned metadata but do not write config.
- `exec` validates mappings, missing secrets, env collisions, and child argv but does not inject values or run the child.

## Generic Scope

env-vault is generic because local automation often mixes package registries, SaaS APIs, CI emulation, private services, and development tools. The core abstraction is `secret-name -> ENV_NAME`, not a cloud provider.

## Module Path

The public module path is:

```text
github.com/ildarbinanas-design/env-vault
```

The module requires the exact stable Go 1.26.5 patch. That version was selected
from the official [Go release history](https://go.dev/doc/devel/release), and
the migration follows the [Go 1.26 release notes](https://go.dev/doc/go1.26).
CI reads the version from `go.mod`, so the compiler recorded in every artifact
is the compiler that actually ran its checks.

## Release Artifact Builds

Release planning and publication are separate trust boundaries. After `ci`
succeeds for a `main` push that is still current at the planning preflight,
Release Please v5 uses a short-lived token from the `release-planning`
environment to open or update a release pull request; stale planning-only runs
are skipped. The App is installed only on `env-vault`. Release Please runs in
manifest, PR-only mode: it updates the reviewed version documentation and
`CHANGELOG.md`, but it cannot create a tag or GitHub Release.
The resulting proposal must be one commit that changes exactly the manifest,
README marker, and changelog on top of a `main` commit with a successful push
CI run. Planning and publication share the `env-vault-release` concurrency
group, which completes the tag/label handoff before the publisher runs and
prevents a later proposal from overtaking an active release.

Pull request titles use Conventional Commits and become deterministic squash
subjects; squash bodies use the reviewed pull request body so an explicit
`BREAKING CHANGE:` footer survives into `main`. Merging the generated release
pull request is the explicit approval
to publish its exact manifest version. The release merge SHA must then pass
`ci` as a push to `main`; failed, foreign-repository, non-push, or unrelated
successful runs do not authorize the tag handoff. The planning workflow
classifies the exact green commit and creates or verifies the tag only when the
manifest, changelog, README marker, commit subject, file modes, and three-path
diff satisfy the deterministic release-commit contract. It also proves the
single associated merged PR was generated by the expected App on the expected
branch, the version still matches current `main`, the SHA remains in `main`,
and that SHA owns a successful `ci` push run. After tag verification it
reconciles the PR lifecycle label to `autorelease: tagged`.

The exact tag push hands the reviewed version and source SHA to
`build-binaries`. Release publication is owned exclusively by that workflow:
its tag entry point repeats the release-commit, generated-PR, ancestry,
manifest, and successful-CI authorization checks before release quality, and
creates the public GitHub Release only after those gates pass. The Release body is extracted from the exact non-empty
version section in the reviewed `CHANGELOG.md`; it is not regenerated from
mutable GitHub metadata. A manual dispatch remains a recovery interface and
can only retry an existing exact tag; new tags remain exclusive to planning.
Published `v0.0.1`–`v0.0.7` retain a bounded legacy repair path that requires an
existing stable Release and tag ancestry, while `v0.0.8+` also requires the
generated-PR authorization. A dispatch without a version is build-only. No
product version constant is maintained in Go source;
the reviewed release version is injected into each binary through Go linker
flags.

Both pull-request CI and releases call `reusable-quality.yml`. Every release
waits for unit tests, vet, race tests, smoke tests, a pinned native
`go-licenses` matrix on Linux, macOS, and Windows, all platform builds, and a
binary-only native E2E matrix. It publishes exactly five archives and five
matching SHA-256 files.

The E2E matrix runs the unpacked release-like artifacts on Linux amd64/arm64,
Darwin amd64/arm64, and Windows amd64. Tests invoke only the public executable
through `os/exec` with an isolated, explicitly gated test backend. A separate
coverage-instrumented binary produces subprocess coverage. A fail-closed
aggregate gate requires all native reports, 100% critical scenario coverage,
only declared platform skips, valid report formats, and a clean sentinel leak
scan. The full architecture and feature trace are documented in
[`docs/e2e.md`](e2e.md).

Every candidate matrix is also compared with the immutable Go 1.22.12 baseline
identity in [`docs/e2e-baseline.json`](e2e-baseline.json). The gate requires the
same semantic suite hash, critical scenarios, normalized public contracts and
exit codes, platform set, and non-decreasing statement coverage. Before the
cross-source comparison, baseline reports are revalidated (including derived
coverage regeneration) against the canonical baseline checkout/toolchain while
candidate reports are revalidated against the candidate checkout/toolchain; a
production fix cannot make either coverage profile appear invalid merely
because it belongs to a different source revision.

Darwin release artifacts support macOS 15+ and are built on macOS GitHub-hosted
runners with `CGO_ENABLED=1` because the macOS Keychain backend requires
CGO-enabled darwin binaries. Linux and Windows artifact builds remain
`CGO_ENABLED=0`.

After the GitHub Release succeeds, the workflow generates a combined SPDX SBOM
and GitHub provenance/SBOM attestations for all five archives without changing
the exact ten-asset Release contract. It then generates declarative
`on_macos`/`on_linux` and `on_arm`/`on_intel` URL/checksum blocks. The formula
declares macOS Sequoia as its minimum and installs the archived README, license,
and third-party notices as documentation. A short-lived, repository-scoped
GitHub App token creates or reuses `release/env-vault-vX.Y.Z` in `homebrew-tap`.
The generated pull request changes only `Formula/env-vault.rb` and carries a
marker binding the version, source SHA, and formula digest. The workflow waits for
`test-formula.yml` with `event=pull_request` and the exact PR head SHA before a
squash merge that is guarded by the same head SHA. It then waits for the
workflow with `event=push`, the exact merged/default-branch SHA, and a successful
conclusion. Style, installation, and the installed exact version therefore form
an automated release gate rather than a follow-up operator check.

The two release Apps are deliberately independent. The planning App is
installed only on `env-vault`; the Release Please planning job is the only
operational job that declares `environment: release-planning` and can read
`RELEASE_APP_CLIENT_ID` and `RELEASE_APP_PRIVATE_KEY`; the separately
dispatched read-only App scope/ruleset audit is the documented exception. The planning
workflow prepares the release pull request and performs the classified
exact-tag handoff. GitHub does not split tag/branch writes from Release writes
inside `Contents: write`, so this separation is an audited workflow invariant,
not a claim that the credential lacks Release API capability. The tap App is
installed only on `homebrew-tap`; only
the `homebrew` job declares
`environment: release` and can read `TAP_APP_CLIENT_ID` and
`TAP_APP_PRIVATE_KEY`. Build-only, build, Release, supply-chain, and `health`
jobs cannot read either App credential. The `health` repair is read-only: it
verifies the tag, Release, checksums, attestations, generated formula, and the
exact tap default-branch push run using public repository state and its
read-only workflow token. Required external settings and credential rotation
procedures are documented in `docs/release-external-settings.md`.

Separate manually dispatched `audit-release-planning-app.yml` and
`audit-release-app.yml` workflows request read-only tokens and fail unless their
installations contain exactly `env-vault` and `homebrew-tap`, respectively. The
planning audit adds Administration read to prove repository settings and
branch/tag ruleset structure and that the App itself cannot bypass them; the tap
audit remains metadata-only. GitHub exposes global bypass actors only to a
ruleset writer, so an administrator separately records the required empty lists
without granting that write capability to the App. Both audits rely on the
token action's post-step revocation. Run the matching audit after installation
or key changes and before the next planning or publication operation.
