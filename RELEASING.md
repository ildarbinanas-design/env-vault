# Releasing env-vault

Releases follow [ADR 0011](docs/adr/0011-minimal-release-pipeline.md) and
[ADR 0012](docs/adr/0012-attestation-verification-pins-release-workflow.md).
One workflow, [`.github/workflows/release.yml`](.github/workflows/release.yml),
does all of it. No workflow runs on a tag.

## How a release happens

1. Changes reach `main` through squash-merged pull requests with Conventional
   Commits titles. Only `feat`, `fix`, `perf`, and `revert` appear in the
   changelog and create a release; `docs`, `ci`, `build`, `test`, `refactor`,
   and `chore` do not. Dependabot titles Go module updates `fix(deps)` and
   GitHub Actions updates `ci(deps)`.
2. On every push to `main`, Release Please opens or updates the release pull
   request: the version, `CHANGELOG.md`, `.release-please-manifest.json`, and
   the marked README line. It uses `RELEASE_PLANNING_TOKEN`, because a pull
   request opened with `GITHUB_TOKEN` would not run the required checks.
3. **Merging the release pull request is the release authorization.** Review
   the version and changelog, check that the required checks are green, and
   merge head-guarded, so a head that moved during review is never published:

   ```sh
   gh pr merge <number> --repo ildarbinanas-design/env-vault \
     --squash --match-head-commit <full-head-sha>
   ```

   Either maintainer may merge it. An agent merges it only on the owner's
   explicit instruction (see `AGENTS.md`).
4. The `release.yml` run for the merge commit then works through these jobs:
   - **release-please** tags the merge commit and opens a draft release. Only
     the run for the tagged commit builds, so the attestations name exactly
     that commit. The run for the merge commit fails if the tag is missing or
     points to another commit, so a release never stops silently.
   - **build** stops unless the tag points to the checked-out commit. It builds
     the five targets, checks that the build information is unmodified and
     that `--version` reports the tag, uploads each binary, and smoke-tests the
     real OS secret store.
   - **publish** packages the five archives deterministically, attests the
     archives and the binaries, and verifies those attestations. It then
     uploads the ten files to the draft and publishes it. Releases are
     immutable, so the published release can no longer change. Never publish
     the draft by hand: a release published before its run built it stays
     without files, the run for its commit fails at **release-please** or
     **publish**, and the version has to be abandoned (see
     [When a release fails](#when-a-release-fails)).
   - **verify** checks that the release is published and immutable and that
     the tag points to the release commit. It downloads the ten assets and
     checks their checksums and attestations.
   - **tap** generates the formula from the published archives and opens a
     pull request in `ildarbinanas-design/homebrew-tap` with auto-merge. The
     tap's `test` check must pass first; it also compares every url and sha256
     with the published checksums.

## Verifying a release

```sh
TARGET=darwin-arm64
gh release download vX.Y.Z --repo ildarbinanas-design/env-vault \
  --pattern "env-vault-$TARGET.tar.gz*"
shasum -a 256 -c "env-vault-$TARGET.tar.gz.sha256"
gh attestation verify "env-vault-$TARGET.tar.gz" \
  --repo ildarbinanas-design/env-vault \
  --signer-workflow ildarbinanas-design/env-vault/.github/workflows/release.yml \
  --source-ref refs/heads/main \
  --deny-self-hosted-runners
```

An installed binary verifies the same way: pass `"$(command -v env-vault)"`
instead of the archive. `-R` alone is not enough, because it accepts an
attestation from any branch or workflow in the repository (ADR 0012).

The archives are deterministic. Rebuilding from the tag with the Go version in
`go.mod` reproduces the Linux and Windows binaries and, from the published
binaries, all five archives (`scripts/release/package-archives.sh` with
`SOURCE_DATE_EPOCH` set to the commit time). Darwin binaries use cgo and can be
rebuilt only on macOS.

## When a release fails

- Use **Re-run failed jobs** on the same run. A re-run uses the same workflow
  file and commit, so it cannot fix a defect in them. Do not use **Re-run all
  jobs** once the release is published: that run finds the published release,
  skips `verify` and `tap`, and ends green.
- If the merge commit's run fails because its tag is missing, Release Please
  did not create it: re-run the failed job. If the tag points to another
  commit, for example because it was created by hand before the merge, the
  version cannot be released.
- `publish` never replaces an uploaded asset. On a re-run it keeps assets whose
  bytes match and stops on any difference. If an asset in the draft is broken,
  the owner deletes that asset from the draft, which can still change, and
  re-runs the failed job. After the release is published, a re-run of
  `publish` only confirms that the published files are these files.
- `tap` re-runs reuse the branch and the pull request of an earlier attempt,
  but only a branch that changes nothing except the formula. A re-run never
  moves the tap back to an older version. If the tap's `test` check fails, the
  pull request stays open. Fix the cause and let auto-merge finish.
- A tag cannot be moved or deleted. If a tagged version cannot be finished,
  abandon it: label its release pull request `autorelease: abandoned`, fix the
  defect, and release the next version.

## Configuration

- `release-please-config.json` makes Release Please open draft releases,
  create the tag itself (`force-tag-creation`), and hide the non-product
  changelog sections. `.release-please-manifest.json` holds the version.
- The `release-planning` environment holds `RELEASE_PLANNING_TOKEN`, a
  fine-grained token for this repository that expires every 90 days. The next
  renewal is due before 2026-12-26.
- The `release` environment holds `HOMEBREW_TAP_TOKEN`, a fine-grained token
  for `ildarbinanas-design/homebrew-tap`. Both environments admit only `main`;
  the `v*` rule the old pipeline needed in `release` is removed in migration
  step 5 ([#107](https://github.com/ildarbinanas-design/env-vault/issues/107)).
- [`docs/release-external-settings.md`](docs/release-external-settings.md)
  lists every external setting, the token permissions, and how to check and
  rotate them.
- Immutable releases are enabled for the repository. The `main` ruleset
  requires the `quality-gate`, `pr-title`, `Dependency review`,
  `Analyze (go)`, and `Analyze (actions)` checks.

## Versions that must stay unpublished

- `v0.0.8` through `v0.0.11` are failed immutable tags. They stay, and they
  never get a GitHub Release.
- `v0.0.12` (pull request #31) and `v0.3.3` (pull request #102) are abandoned.
  No tag or release may exist for them.
- Published releases are immutable. To correct one, publish a higher version.

## Before ADR 0011

Releases up to v0.3.4 went through the previous pipeline: release planning,
the tag-triggered publisher, and the repair workflows. Its procedures are in
[`RELEASING.md` at v0.3.4](https://github.com/ildarbinanas-design/env-vault/blob/1fd6638295fb616189e66da7cc110cf4831a3d94/RELEASING.md).
Migration step 6 ([#107](https://github.com/ildarbinanas-design/env-vault/issues/107))
removed its workflows and code; git history keeps them.
