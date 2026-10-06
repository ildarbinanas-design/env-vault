# Releasing env-vault

This is the operator runbook. The
[release-process](openspec/specs/release-process/spec.md) and
[homebrew-distribution](openspec/specs/homebrew-distribution/spec.md)
specifications define what the pipeline does; ADRs
[0011](docs/adr/0011-minimal-release-pipeline.md) and
[0012](docs/adr/0012-attestation-verification-pins-release-workflow.md) record
why. [`.github/workflows/release.yml`](.github/workflows/release.yml) runs on
pushes to `main`; no workflow runs on a tag.

## Authorize a release

Release Please maintains the generated release pull request when product
changes require a release. `feat`, `fix`, `perf`, and `revert` create releases;
`docs`, `ci`, `build`, `test`, `refactor`, and `chore` do not. Dependabot uses
`fix(deps)` for Go modules and `ci(deps)` for Actions updates. See
[CONTRIBUTING.md](CONTRIBUTING.md) for title and version rules.

**Merging the generated release pull request authorizes its exact version.**
Review the version, changelog, manifest and marked README line, confirm the
required checks are green, then merge the reviewed head:

```sh
gh pr merge <number> --repo ildarbinanas-design/env-vault \
  --squash --match-head-commit <full-head-sha>
```

Either maintainer may authorize a release. An agent needs the owner's explicit
instruction ([AGENTS.md](AGENTS.md)). A changed head requires a new review.

## From merge to Homebrew

An ordinary push maintains release planning. Publication follows the merge of
the generated release pull request; only the run for its tagged commit builds.

```mermaid
flowchart TD
    Main["Push to main"] --> Plan["Release Please: maintain a release PR<br/>when product changes require one"]
    Plan -->|"Owner authorizes the reviewed release PR"| Merge["Head-guarded squash merge"]
    Merge --> Tag["Release Please: tag the merge SHA<br/>and create a draft"]
    Tag --> Build["Only the run for the tagged merge SHA builds<br/>Build five targets; check version and clean build info<br/>Smoke-test OS secret stores"]
    Build --> Package["Package five archives deterministically<br/>Write SHA-256 sidecars"]
    Package --> Attest["Attest archives and binaries<br/>Verify workflow, main, merge SHA and hosted runner"]
    Attest --> Publish["Upload ten files to draft<br/>Publish immutable release"]
    Publish --> Verify["Verify published release, tag,<br/>ten assets, checksums and archive attestations"]
    Verify --> Tap["Open Homebrew formula PR<br/>Auto-merge after tap test passes"]
```

Each step's checks are specified in
[release-process](openspec/specs/release-process/spec.md).

**Never publish the draft by hand.** If it becomes immutable before the run
uploads its files, that version cannot be completed. See recovery below.

## Verify a release

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

For an installed binary, pass `"$(command -v env-vault)"` instead of the
archive. `-R` alone accepts attestations from any branch or workflow in the
repository. The release workflow also pins `--source-digest` to the release
commit (ADR 0012).

Rebuilding from the tag with the Go version in `go.mod` reproduces the Linux
and Windows binaries. Repackaging the published binaries with
`scripts/release/package-archives.sh` and `SOURCE_DATE_EPOCH` set to the commit
time reproduces all five archives. Darwin binaries use cgo and require macOS
to rebuild.

## When a release fails

Inspect the current run, release and tap PR before retrying an ambiguous
mutation. Use **Re-run failed jobs** on the same run. A re-run keeps the
original workflow and commit, so it cannot fix a defect in either. **Re-run all
jobs** after publication skips `verify` and `tap` and can end green without
finishing them.

| Failure | Recovery |
| --- | --- |
| Release commit has no tag | Re-run the failed job so Release Please can create it. |
| Release commit's tag points to another commit | Abandon that version; tags cannot be moved or deleted. |
| A draft asset differs from the build | `publish` stops without replacing it. The owner deletes the broken draft asset, then re-runs the failed job. Matching uploaded bytes are reused. |
| `publish` is retried after publication | It succeeds only if the release already has exactly the built files. |
| Tap PR or branch already exists | The job reuses it only if the branch changes just the formula and its content matches the expected state. It never downgrades the tap. |
| Tap `test` fails | Fix the cause in the open PR and let auto-merge finish. |
| Draft was published before files were uploaded | Abandon the version; an immutable release cannot receive the missing files. |

To abandon a version that cannot be completed, label its release pull request
`autorelease: abandoned`, fix the cause, and release a higher version.

## Configuration and external settings

`release-please-config.json` and `.release-please-manifest.json` drive
version planning. Tokens, environments, rulesets and required checks are
specified in [release-process](openspec/specs/release-process/spec.md).

[External settings](docs/release-external-settings.md) lists permissions,
rulesets, token expiry and rotation. Check it at each audit and after settings
changes; the release workflow does not verify those settings itself.

## Versions that must stay unpublished

- `v0.0.8` through `v0.0.11` are failed immutable tags. They stay without a
  GitHub Release.
- `v0.0.12` (PR #31) and `v0.3.3` (PR #102) are abandoned. Neither may get a tag
  or release.
- Published releases are immutable; corrections ship in a higher version.

## Historical releases

The generated v0.3.4 changelog links to abandoned v0.3.3. Use the
[v0.3.2 to v0.3.4 comparison](https://github.com/ildarbinanas-design/env-vault/compare/v0.3.2...v0.3.4)
for the changes between published releases. The runtime fix first shipped in
v0.3.4 preserves ignored SIGHUP and SIGINT under `nohup`
([#101](https://github.com/ildarbinanas-design/env-vault/pull/101));
[#103](https://github.com/ildarbinanas-design/env-vault/pull/103) repaired the
previous pipeline's handling of a deleted GitHub App author. Repeated older
entries, including encrypted transfer (#78), artifact lifecycle tooling (#60)
and the initial MVP, do not date those features to v0.3.4.

Releases through v0.3.4 used the previous pipeline. Its procedures remain in
[`RELEASING.md` at v0.3.4](https://github.com/ildarbinanas-design/env-vault/blob/1fd6638295fb616189e66da7cc110cf4831a3d94/RELEASING.md).
Migration [#107](https://github.com/ildarbinanas-design/env-vault/issues/107)
removed that code; git history, generated changelog and immutable releases
retain the record.
