# ADR 0011: Minimal release pipeline for an equal-maintainer team

## Status

Accepted. The migration below is in progress. Until its switch step lands,
releases still run on the existing pipeline.

## Date

2026-09-27

## Context

The release machinery has outgrown the product it ships. On `main` at v0.3.4
it consists of:

- about 16,000 lines of non-test release Go, in `cmd/release*`,
  `cmd/actionsartifact*` and the `internal/release*`, `githubtransport`,
  `actionsartifact`, `canonicalgzip`, `e2ebaseline` and `e2esuite` packages;
- 4,210 lines of workflow YAML in nine workflows;
- 3,658 lines of shell in `scripts/release/`.

The product itself is about 4,300 lines of non-test Go.

The machinery kept failing on its own:

- Release planning stopped for 11 days in September 2026 because a strict
  decoder rejected a new, harmless ruleset field (#85).
- v0.3.3 was abandoned on 2026-09-27.
  `verify-abandoned-release-policy.sh` pinned the login of a GitHub App that
  was then deleted, so planning stopped before tagging (#102, #103).

A third incident came from outside the machinery: v0.2.1 was published without
its Homebrew formula for about 8.5 hours, after a Homebrew audit rule broke the
tap's CI.

The 2026-09-26 audit found that no release check ever caught an external
integrity problem. The real trust gaps sit elsewhere:

- The publisher runs on any pushed `v*` tag and executes code from that tag
  (W3-01).
- Releases since v0.2.0 carry no build attestations, and releases before
  v0.3.4 are mutable (W3-02, W7-06).
- Release binaries record a pseudo-version such as
  `v0.0.0-20260927101651-1fd6638295fb`, because CI checks out without tags,
  so a rebuild from an ordinary clone does not match (W8-03).

On 2026-09-26 the owner decided to rewrite the pipeline as a separate project
for an equal-maintainer team, using the least code and the fewest release steps
that current practice allows. The open forks were settled on 2026-09-27. ADR
0008 froze further release-engineering investment; this decision lifts that
freeze for this rewrite only.

## Decision

### One workflow on `main`

`.github/workflows/release.yml` runs on every push to `main`. Its jobs:

1. **release-please** maintains the release pull request, using
   `RELEASE_PLANNING_TOKEN`. After that pull request merges, it creates the
   tag and a draft GitHub Release. This needs `draft: true` and
   `force-tag-creation: true`, without `skip-github-release`. Release Please
   17.6.0 provides both options, and the first release on the new pipeline
   (step 5) exercises them.
2. **build** runs only when a release was created. It:
   - checks out the release commit that Release Please reports, with tags
     fetched;
   - fails unless the tag points to that commit;
   - builds the five native targets;
   - checks that `--version` reports the tag;
   - runs the backend smoke test;
   - uploads the raw binaries.

   A tag pushed by hand before the merge therefore stops the release instead
   of being built. Tags cannot be moved or deleted, so that version is then
   abandoned.
3. **publish** runs in the `release` environment. It:
   1. packages the five archives deterministically on Linux and writes their
      `.sha256` sidecars;
   2. attests the five archives and the five binaries with
      `actions/attest-build-provenance`;
   3. checks the local files with `gh attestation verify`;
   4. uploads everything to the draft;
   5. publishes the draft. Immutable releases are enabled, so the published
      release cannot change.
4. **verify** downloads the published assets, checks their sidecars, and runs
   `gh attestation verify` on each archive.
5. **tap** runs in the `release` environment after **verify**, so Homebrew
   never points at an unverified release. It generates the formula, keeping
   its `version "X.Y.Z"` line, which the tap's `pins` check and Homebrew
   before 6.0.14 need. It then opens a pull request in homebrew-tap with
   `HOMEBREW_TAP_TOKEN` and enables auto-merge; the tap ruleset still requires
   its `test` check.

No workflow runs on a tag push, so a tag created by hand publishes nothing.
This closes W3-01 without restricting who may create tags.

The release pull request must pass the required checks and be up to date with
`main` before it can merge, so the release commit's tree is the tree CI
tested.

### Only product changes create releases

Only the `feat`, `fix`, `perf` and `revert` sections stay visible in the
changelog. `docs`, `ci`, `build`, `test`, `refactor` and `chore` are hidden. A
change to the pipeline or the documentation then creates no release and no
macOS Keychain prompt after `brew upgrade` (W7-01).

- Dependabot uses the prefix `fix(deps)` for Go modules, so a dependency update
  still creates a release. For GitHub Actions it uses `ci(deps)`, which does
  not.
- Pipeline fixes use `ci:` or `build:`, never `fix(release)`.

### Linking a binary to its source

- `env-vault --version` prints one line: the version, the short commit and the
  commit date, for example `v0.4.0 (1fd6638, 2026-09-27)`.
- `env-vault --json version` adds the full commit, the commit time, whether the
  tree was modified, the Go version and the platform.
- Every value comes from the build information Go embeds
  (`runtime/debug.ReadBuildInfo`):
  - the main module version, which Go stamps from the tag when the checkout
    has it;
  - `vcs.revision`, `vcs.time` and `vcs.modified`.
- The build no longer injects the version with `-ldflags -X`, and it records
  no build timestamp.
- The printed commit helps diagnosis but proves nothing, because a tampered
  binary can print anything. Proof is the attestation, which binds the file's
  digest to this repository, the workflow and the commit:

  ```sh
  gh attestation verify "$(command -v env-vault)" -R ildarbinanas-design/env-vault
  ```

  Homebrew installs the binary from the archive unchanged, so the command also
  works for a Homebrew installation.
- Packaging is deterministic, so all five archives can be rebuilt from the tag,
  as can the Linux and Windows binaries. Darwin binaries use cgo and can be
  rebuilt only on macOS.
- There is no separate SBOM. `go version -m` lists every module with its hash,
  and `govulncheck -mode=binary` works from that.

### Credentials and authority

- `RELEASE_PLANNING_TOKEN` stays a fine-grained PAT scoped to this repository,
  in the `release-planning` environment. A release pull request opened with
  `GITHUB_TOKEN` would not trigger the required checks.
- `HOMEBREW_TAP_TOKEN` stays a fine-grained PAT scoped to homebrew-tap, in the
  `release` environment.
- Once the first release on the new pipeline is verified (step 5), the owner
  removes the `v*` tag rule from the `release` environment, and the
  environment then admits only `main`. Until then the rule stays, because the
  old publisher's `homebrew` job needs it if step 3 is reverted.
- Either maintainer may release by merging the release pull request.
- Agent authority does not change. An agent merges a release pull request only
  on an explicit instruction from the owner, `ildarbinanas-design`, and
  head-guarded as today. The reserved paths stay reserved.

## Consequences

The guarantees that stay, in a different form:

| Guarantee | Before | After |
|---|---|---|
| Only reviewed `main` is released | Release PR plus checks run from the tag's code | Build in the `main` workflow after the release PR merges; the tag must point to the release commit; no tag-triggered code |
| The published bytes are the built and checked bytes | Promotion manifest re-verified in three jobs; E2E on the exact published bytes | Build, `--version`, smoke and packaging in one run; digests signed by the attestation. Tests and E2E run on the pull request build |
| Integrity is verifiable | `.sha256` beside each archive | `.sha256`, attestations for archives and binaries, immutable releases |
| Assets cannot be replaced | Uploads without `--clobber` | Draft, upload, publish; the published release is immutable |
| The tap changes only through a pull request with CI | Head-guarded merge by script after waiting for CI | Auto-merge after the tap's `test` check; tap CI pins url and sha256 |
| A failed release can be resumed | Three repair modes, bootstrap and bridge workflows | Re-run the failed job of the same run |

A re-run uses the original workflow file and code, so re-running cannot fix a
deterministic defect. If the tag already exists, that version is abandoned,
because tags cannot be deleted. v0.3.3 was abandoned for the same reason
before its tag was created: re-running would have replayed the defective
check.

What is intentionally lost:

- the per-release verification of repository rulesets;
- the attempt-qualified promotion manifest;
- E2E on the exact published bytes;
- Actions artifact accounting and its deletion ceremony (ADR 0007). Deleting
  artifacts stays reserved for the owner;
- strict parsing and exact-value checks of GitHub API data.

The ruleset verification and the strict API checks caused #85 and the v0.3.3
abandonment. None of the lost parts ever caught an external problem. The v0.2.1
lag came from the tap's CI, which stays and now runs weekly.

Expected size: about 600 lines of workflow YAML, no release Go, and about 200
lines of shell. A release is one merge.

Risks:

- If the tap's `test` check fails, the tap pull request stays open. The owner
  receives the failure email, and the weekly tap CI keeps checking the
  formula.
- `RELEASE_PLANNING_TOKEN` expires every 90 days. The next renewal is due
  before 2026-12-26.
- The first release on the new pipeline must carry a product change, so that it
  exercises the whole chain: v0.4.0, with the new `--version`.

## Migration

Each step is one pull request.

1. **This ADR.** Under the current configuration a `docs` commit is visible, so
   Release Please opens a v0.3.5 release pull request after this merge. Leave
   it unmerged until it reads v0.4.0. Once step 3 hides the non-product
   sections, Release Please leaves it untouched, and step 4 retitles it.
2. **Add `release.yml` beside the existing pipeline**, with a manual trigger
   only and no publication or attestation. It builds from a commit, checks
   `--version`, runs the smoke test and packages deterministically.
   `tests/workflows_test.go` stops forbidding attestations and admits
   `release.yml` next to the contract's workflow inventory.
3. **Switch**, in one pull request:
   - `release.yml` runs on push to `main`.
   - `release-please.yml` and `build-binaries.yml` are removed.
   - CI drops the parts that only fed the old publisher: the promotion manifest,
     the release version probe and the sealed E2E proof. E2E runs once per
     operating system, and build and smoke still cover all five targets.
   - The Release Please configuration hides the non-product sections and
     replaces `skip-github-release`. The recovery validator and the workflow
     tests that pin the old configuration change with it.
   - Dependabot gets its prefixes.
   - The formula generator uses `assert_match "v#{version}"`.
   - AGENTS.md drops the contract v2 and GitHub transport rules, which the new
     workflow does not follow. AGENTS.md and RELEASING.md describe the new
     flow.
   - Required check names do not change, so the `main` ruleset needs no edit.

   A single revert undoes this step, because the `release` environment keeps
   its `v*` rule until step 5. After step 4 merges, revert step 4 first,
   because the restored old pipeline rejects the new `--version` format. It
   fails at the CI check on the release commit, before any tag is created.
4. **`--version` with the commit**, as a `feat:` commit so that the release
   becomes v0.4.0. A `feat!:` commit would give v1.0.0, because
   `bump-minor-pre-major` is not set.

   This step comes only after the switch. The old pipeline requires the release
   commit's binary to print exactly `vX.Y.Z` and strictly decodes `version
   --json` (`internal/releasepromotion/version_evidence.go`), so the new format
   would break the next release on the old pipeline. The step also updates the
   E2E version check, which compares `--version` with the injected version on
   every CI run.
5. **Release v0.4.0.** The owner merges the release pull request. Then:
   - check that the release is immutable;
   - check that `gh attestation verify` passes for an archive and for the
     installed binary;
   - check that the tap auto-merge completed;
   - check that `env-vault --version` reports the commit;
   - the owner removes the `v*` rule from the `release` environment.
6. **Remove the old machinery.**
   - Delete the release Go packages and commands, `scripts/release/` except what
     the new workflow needs, `release/*.json`, and the bootstrap, bridge and
     legacy workflows together with their tests.
   - Shorten RELEASING.md to one page.
   - Remove the release runbook, the architecture and refactor documents, and
     `evidence/`. Git history keeps them.
   - Drop the AGENTS.md rules about the evidence ledger and the artifact
     deletion ceremony.
   - Mark ADR 0002 and 0004–0007 superseded by this ADR.

ADR 0003 is already superseded. ADR 0008 keeps its product-scope rule: new
product features still need a PersonalOS consumer. ADR 0009 is unchanged: the
new pipeline adds provenance attestations, not code signing.
