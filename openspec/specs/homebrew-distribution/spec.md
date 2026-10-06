# homebrew-distribution Specification

## Purpose

Define how env-vault is distributed through the Homebrew tap
`ildarbinanas-design/homebrew-tap`: the formula the release workflow
generates, the checks the tap applies before a formula update can merge, and
what users install. The release-side hand-off is specified in
`release-process`; this specification records what the release relies on
from the tap.

## Requirements

### Requirement: Supported installation

Users SHALL install env-vault with `brew install ildarbinanas-design/tap/env-vault`
on macOS 15 or newer (arm64 and amd64) and on Linux (arm64 and amd64). Homebrew
SHALL be the supported installation path on macOS, because release binaries
are not Developer ID signed or notarized and a quarantined manual download
cannot launch; Homebrew does not attach the quarantine attribute. Windows
SHALL be served only by the release ZIP archive.

#### Scenario: macOS install

- **WHEN** a user on macOS 15 arm64 runs `brew install ildarbinanas-design/tap/env-vault`
- **THEN** Homebrew installs the `darwin-arm64` release binary and `env-vault --version` prints the formula's version

### Requirement: Generated formula

`Formula/env-vault.rb` SHALL be written only by the env-vault release workflow
from the published release, never by hand. It SHALL define `class EnvVault`
with `desc`, `homepage`, a single top-level line `version "X.Y.Z"` and
`license "MIT"`; under `on_macos`, `depends_on macos: :sequoia` and one `url`
and `sha256` pair for arm and for intel; under `on_linux`, one pair for arm
and for intel. Every `url` SHALL point to
`https://github.com/ildarbinanas-design/env-vault/releases/download/vX.Y.Z/env-vault-<target>.tar.gz`
and every `sha256` SHALL equal the published sidecar checksum. `install` SHALL
install the `env-vault` binary into `bin` and `README.md`, `LICENSE` and
`THIRD_PARTY_NOTICES.md` into `doc`.

#### Scenario: Hand-edited formula

- **WHEN** a pull request changes a checksum in `Formula/env-vault.rb` by hand
- **THEN** the tap's `pins` check fails because the formula no longer matches the published sidecars

### Requirement: Formula test

The formula `test` block SHALL check that `env-vault --version` contains
`v<version>`, then create a profile, add a mapping, show it as JSON, remove
the mapping and confirm the profile is empty, all in a config under
Homebrew's `testpath`, without opening a secret store.

#### Scenario: brew test

- **WHEN** `brew test ildarbinanas-design/tap/env-vault` runs on a CI runner without a keychain session
- **THEN** the test passes without any secret backend access

### Requirement: Tap acceptance check

A formula change SHALL reach the tap's `main` only through a pull request
whose head passed the required check `test`. `test` SHALL pass only when both
of these job groups pass:

- `formula`, for env-vault on `ubuntu-24.04` (x86_64), `macos-15` (arm64) and
  `macos-15-intel` (x86_64): `brew style`, `brew audit --strict
  --except=version`, `brew install` and `brew test` of the checked-out tap,
  with Homebrew auto-update disabled;
- `pins`: the checker's unit tests, then a byte-for-byte comparison of the
  complete formula with its reviewed template after substituting only the
  version and the four checksums downloaded from the release's published
  sidecars. Versions up to 0.4.3 SHALL use the frozen version-only template;
  later versions SHALL use the current template. The checker SHALL NOT
  evaluate the formula's Ruby.

#### Scenario: Generated formula from a new release

- **WHEN** the release workflow opens the `env-vault vX.Y.Z` pull request
- **THEN** auto-merge waits until `test` passes on that head, then squash-merges it

#### Scenario: Template drift

- **WHEN** the release generator changes the formula structure without a matching template change in the tap
- **THEN** `pins` fails and the formula update cannot merge

### Requirement: Tap repository settings

The tap SHALL allow auto-merge, SHALL protect `main` with a ruleset that
requires pull requests, the `test` check and resolved conversations, allows
only squash merges, requires no approvals, and forbids force pushes and
deletion without bypass. Its workflows SHALL run with `contents: read`, pin
every action by full commit SHA, and use no secrets. `test-formula.yml` SHALL
also run on pushes to `main`, weekly on Mondays at 06:17 UTC, and on demand,
so that a Homebrew change that breaks the formula is seen before a release
needs it.

#### Scenario: Weekly run

- **WHEN** the weekly scheduled run of `test-formula.yml` fails on `main`
- **THEN** the failure is visible before the next release's formula pull request depends on it

### Requirement: Upgrade behavior on macOS

After `brew upgrade env-vault`, macOS SHALL treat the new binary as a new
Keychain client, so the first read of each stored value (`exec`,
`secret set --verify`, `export`) MAY show an access prompt. Documentation
SHALL tell users to run each profile once interactively and choose
**Always Allow**. Metadata-only operations SHALL NOT trigger these prompts.

#### Scenario: First exec after upgrade

- **WHEN** a LaunchAgent runs `env-vault exec` right after an upgrade and nobody answers the prompt
- **THEN** the command fails with `BACKEND_UNAVAILABLE` after at most two minutes
