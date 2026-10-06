# release-process Specification

## Purpose

Define how a change reaches `main`, how a release is planned and
authorized, and how `.github/workflows/release.yml` tags, builds, packages,
attests, publishes and verifies a release and hands it to Homebrew. The
operator procedures and recovery steps are in `RELEASING.md`; repository
settings and tokens are listed in `docs/release-external-settings.md`.

## Requirements

### Requirement: Changes reach main through checked pull requests

Every change to `main` SHALL arrive through a squash-merged pull request whose
title and body become the commit subject and message. The pull request title
SHALL match `^(feat|fix|perf|refactor|build|ci|docs|test|chore|revert)(\([a-z0-9][a-z0-9._/-]*\))?!?: [^ ].+$`,
enforced by the `pr-title` check. The `main` ruleset SHALL require the checks
`quality-gate`, `pr-title`, `Dependency review`, `Analyze (go)` and
`Analyze (actions)`, and SHALL forbid force pushes and deletion. The
`quality-gate` check SHALL pass only when every job of the reusable quality
workflow passed: source checks (`go mod tidy -diff`, `go mod verify`,
`go test ./...`, `go vet ./...`, `govulncheck`, smoke tests, the race
detector, the pinned historical-transfer check and the Debian Secret Service
check), license checks on Linux, macOS and Windows, and the native matrix:
build and real-backend smoke test for all five release targets
(`linux-amd64`, `linux-arm64`, `darwin-amd64`, `darwin-arm64`,
`windows-amd64`), and the E2E suite once per operating system on
`linux-amd64`, `darwin-arm64` and `windows-amd64`.

#### Scenario: Non-conventional title

- **WHEN** a pull request is titled `Update readme`
- **THEN** the `pr-title` check fails and the pull request cannot merge

### Requirement: Version planning

On every push to `main`, the `release-please` job SHALL run Release Please
with `RELEASE_PLANNING_TOKEN` from the `release-planning` environment and
keep a release pull request titled `chore(main): release env-vault vX.Y.Z`
current. That pull request SHALL update `CHANGELOG.md`,
`.release-please-manifest.json` and the version line marked
`x-release-please-version` in `README.md`. `feat` SHALL request a minor
version, an explicit breaking change (`!` or `BREAKING CHANGE:`) a major
version also below 1.0.0, and `fix`, `perf` and `revert` a patch version.
`build`, `ci`, `docs`, `test`, `refactor` and `chore` SHALL NOT request a
release and SHALL be hidden from the changelog. Dependabot SHALL title Go
module updates `fix(deps)` and Actions updates `ci(deps)`.

#### Scenario: Documentation-only change

- **WHEN** only `docs:` pull requests merged since the last release
- **THEN** no release pull request is opened or updated for them

### Requirement: Release authorization

Merging the generated release pull request SHALL be the only release
authorization. A maintainer SHALL merge it as a head-guarded squash merge
(`gh pr merge <n> --squash --match-head-commit <head-sha>`); an agent SHALL do
so only on the owner's explicit instruction. It SHALL
authorize only the version, tag and merge commit produced by that head. No
workflow SHALL run on a tag, and tags or releases SHALL NOT be created by any
other path. A draft release SHALL NOT be published by hand.

#### Scenario: Head changed after review

- **WHEN** the release pull request receives a new commit after review
- **THEN** the head-guarded merge of the reviewed SHA fails and the new head needs a new review

### Requirement: Tag and draft creation

After the release pull request merges, Release Please SHALL create the tag
`vX.Y.Z` on the merge commit and a draft GitHub Release. Only the workflow
run whose commit the tag points to SHALL build the release. A run for a
commit that changed the manifest version but is not the tagged commit SHALL
fail. A run that finds its tagged release already published with exactly 10
assets SHALL finish without building; one published with any other number of
assets SHALL fail.

#### Scenario: Out-of-order runs

- **WHEN** runs for two consecutive pushes to `main` execute in reverse order
- **THEN** only the run for the tagged merge commit builds and publishes

### Requirement: Release build

The `build` job SHALL build five targets on GitHub-hosted runners with the
exact Go version from `go.mod` and `GOTOOLCHAIN=local`: `linux-amd64` and
`linux-arm64` (`CGO_ENABLED=0`), `darwin-amd64` and `darwin-arm64`
(`CGO_ENABLED=1`, macOS 15 runners), and `windows-amd64` (`CGO_ENABLED=0`),
using `go build -trimpath -ldflags="-s -w"` with no `-X` stamping. For a
release it SHALL first check that the checkout is the release commit and the
tag points to it. It SHALL require the build information to carry a VCS stamp
with `vcs.modified=false`, the module version to equal the release tag, and
`--version` to print that version. It SHALL upload each binary before running
anything else, then smoke-test the platform's real secret store (Windows
Credential Manager, a throwaway macOS keychain, or `pass` with a throwaway
GPG key on Linux) with random values that are never printed. The full E2E
suite runs in pull request CI on its own builds; release binaries receive only
these build checks and the backend smoke test.

#### Scenario: Modified build tree

- **WHEN** the build information reports `vcs.modified=true`
- **THEN** the build job fails and nothing is published

### Requirement: Deterministic packaging

Packaging SHALL produce, for each target, `env-vault-<target>.tar.gz`
(`env-vault-windows-amd64.zip` for Windows) containing the directory
`env-vault-<target>/` with the binary (mode `0755`) and `README.md`, `LICENSE`
and `THIRD_PARTY_NOTICES.md` (mode `0644`), plus a sidecar
`<archive>.sha256` holding the single line `<sha256>  <archive>`. Archives
SHALL be byte-reproducible: sorted entries, owner and group 0, timestamps set
to the release commit time, `gzip -n -9` for tar archives. Packaging SHALL run
twice and fail unless both runs produce identical bytes.

#### Scenario: Repackaging a release

- **WHEN** the published binaries are repackaged with `scripts/release/package-archives.sh` and `SOURCE_DATE_EPOCH` set to the commit time
- **THEN** all five archives are byte-identical to the published ones

### Requirement: Attestation

The `publish` job, in the `release` environment, SHALL attest the five
archives and the five binaries (as `env-vault-<target>` or
`env-vault-<target>.exe`) with GitHub artifact attestations, then verify every
attestation with `gh attestation verify` pinned to
`--signer-workflow ildarbinanas-design/env-vault/.github/workflows/release.yml`,
`--source-ref refs/heads/main`, `--source-digest <release commit>` and
`--deny-self-hosted-runners`. It SHALL be the only job with write permissions
(`contents`, `id-token` and `attestations`).

#### Scenario: Attestation from another branch

- **WHEN** an attestation was produced by a workflow run on a branch other than `main`
- **THEN** the pinned verification rejects it and the release is not published

### Requirement: Publication

`publish` SHALL upload exactly the five archives and five sidecars to the
draft release and then publish it; immutable releases SHALL lock the assets
and tag. It SHALL never replace an uploaded asset: an existing asset with
identical bytes SHALL be reused, and one with different bytes or an
unexpected name SHALL fail the job. When the release is already published,
the job SHALL succeed only if it already holds exactly the built files.

#### Scenario: Retry after a partial upload

- **WHEN** a failed `publish` job is re-run and the draft already holds some identical assets
- **THEN** the job uploads only the missing files and publishes the release

### Requirement: Published release verification

The `verify` job SHALL require the release to be published and immutable, the
tag to point to the release commit, the asset list to equal the expected ten
files, every sidecar checksum to match, and every archive attestation to pass
the pinned verification. The tap hand-off SHALL run only after it passes.

#### Scenario: Missing asset

- **WHEN** the published release lacks `env-vault-linux-arm64.tar.gz.sha256`
- **THEN** `verify` fails and no Homebrew pull request is opened

### Requirement: Homebrew hand-off

The `tap` job, in the `release` environment, SHALL generate the formula from
the four published macOS and Linux archives and their sidecars with
`scripts/release/homebrew-formula.sh`, failing if a sidecar does not match its
archive. Using `HOMEBREW_TAP_TOKEN`, it SHALL put the formula on branch
`env-vault-vX.Y.Z` of `ildarbinanas-design/homebrew-tap`, open a pull request
titled `env-vault vX.Y.Z`, and enable squash auto-merge. It SHALL do nothing
when the tap already has this formula or a newer version, SHALL refuse an
existing branch that changes anything but `Formula/env-vault.rb` or holds a
different formula, and SHALL reuse a matching branch or pull request. The tap
side is specified in `homebrew-distribution`.

#### Scenario: Late re-run of an older release

- **WHEN** the tap already has v0.4.6 and the `tap` job of v0.4.5 is re-run
- **THEN** the job exits successfully without changing the tap

### Requirement: Verification runs without publication

Pull requests that change `release.yml`, the packaging or formula scripts, or
`scripts/backend-smoke.sh`, and manual runs, SHALL build and package all
targets and check that the runner's `gh attestation verify` supports every
pinned flag, without tagging, attesting, publishing or using either release
token.

#### Scenario: Pull request touching the release workflow

- **WHEN** a pull request changes `.github/workflows/release.yml`
- **THEN** the release workflow builds and packages the five targets and stops before `publish`

### Requirement: Release credentials

`RELEASE_PLANNING_TOKEN` SHALL be a fine-grained token scoped to `env-vault`,
stored only in the `release-planning` environment and referenced only by the
`release-please` job. `HOMEBREW_TAP_TOKEN` SHALL be a fine-grained token
scoped to `homebrew-tap`, stored only in the `release` environment and
referenced only by the `tap` job. Both environments SHALL admit only `main`.
Workflow-level permissions SHALL be `contents: read`. The release workflow
SHALL NOT verify these external settings itself; they are audited by hand.

#### Scenario: Pull request run

- **WHEN** the release workflow runs for a pull request
- **THEN** neither release token is available to any job

### Requirement: Failure recovery

A failed release run SHALL be resumed with "Re-run failed jobs" on the same
run, never "Re-run all jobs", and ambiguous release mutations SHALL NOT be
blindly retried. A version that cannot be completed (its tag points to
another commit, or its draft was published without all files) SHALL be
abandoned by labelling its release pull request `autorelease: abandoned` and
releasing a higher version. Tags `v0.0.8` to `v0.0.11` SHALL stay without a
release, and `v0.0.12` and `v0.3.3` SHALL never be tagged or released.
Published releases SHALL NOT be changed; corrections ship in a higher version.

#### Scenario: Draft published by hand too early

- **WHEN** a draft release becomes immutable before the run uploaded its files
- **THEN** that version is abandoned and the fix ships as the next version

### Requirement: Consumer verification

Release documentation SHALL instruct users to verify a downloaded archive or
installed binary with `gh attestation verify <file> --repo
ildarbinanas-design/env-vault --signer-workflow
ildarbinanas-design/env-vault/.github/workflows/release.yml --source-ref
refs/heads/main --deny-self-hosted-runners`, and SHALL state that `--repo`
alone accepts attestations from any branch or workflow. Releases up to v0.3.4
used an earlier pipeline and do not verify with these flags.

#### Scenario: Verifying an installed binary

- **WHEN** the user passes `"$(command -v env-vault)"` from a Homebrew installation of v0.4.6 to the pinned command
- **THEN** the verification succeeds for the binary attested by the v0.4.6 release run
