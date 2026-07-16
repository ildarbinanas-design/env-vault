# Releasing env-vault

This is the operator runbook for release planning, build-only runs,
publication, repairs, and release incidents. Release Please v5 prepares the
version and `CHANGELOG.md` in a pull request but is not a publisher. Merging
that reviewed release pull request is the explicit authorization to publish
its exact version after the merge commit passes `ci` on `main`.

The release-planning workflow owns only the exact tag handoff after green CI.
`build-binaries` is the only public publisher: it owns the GitHub Release,
archives, checksums, attestations, and Homebrew handoff. Homebrew publication
uses a separate version-specific pull request, an exact-head squash merge, and
an exact post-merge CI gate. The automated health job repeats the immutable
release and tap checks before the workflow can finish successfully.

Do not publish from an unreviewed working tree. Do not move an existing tag,
replace an existing release asset, or lower the Homebrew version.

## Release invariants

- A publishing version is exactly `vMAJOR.MINOR.PATCH`. The leading `v` is part
  of the version and of the output from both `env-vault --version` and
  `env-vault version`.
- Release Please runs in manifest, PR-only mode. It may open or update the
  release pull request; it must not create a tag or GitHub Release.
- `.release-please-manifest.json`, the checked-in Release Please configuration,
  and `CHANGELOG.md` form the reviewed version-documentation boundary. The
  generated release pull request must keep them consistent.
- Pull request titles use Conventional Commits and become the squash commit
  subject. The generated release pull request is also squash-merged. Rebase
  merges are outside the release contract.
- The release-planning GitHub App is installed only on `env-vault`; its
  short-lived token is available only in the `release-planning` environment.
  It may prepare the release pull request and create only the classified exact
  release tag. GitHub's `Contents: write` permission also technically covers
  Releases, so the enforced workflow contract—not permission granularity—is
  what prevents this token from calling the Release or asset APIs.
- Merging a generated release pull request is an explicit publication
  authorization for its exact manifest version and merge SHA. Merely opening,
  updating, approving, or closing the pull request is not authorization.
- Automatic handoff starts only after the `ci` workflow succeeds for the exact
  release merge SHA on `main`. A failed, cancelled, foreign-repository,
  non-push, or non-default-branch run must not create the tag.
- The release-planning workflow creates or verifies the exact `vX.Y.Z` tag only
  when the green commit is a deterministic Release Please merge that changes
  exactly the manifest, `CHANGELOG.md`, and marked README version line. It also
  requires the single associated PR to have the expected App author, release
  branch, title, generated body marker, lifecycle label, merge SHA, and base.
- The proposed version must equal the manifest on current `main`; the release
  SHA must remain in `main` and have a successful `ci` push run. A stale
  replay, detached branch tag, or hand-written lookalike PR fails closed.
- After verifying the exact tag, the planning workflow replaces
  `autorelease: pending` with `autorelease: tagged`. This idempotent handoff is
  required before Release Please can plan a later version.
- The tag starts `build-binaries`, the sole workflow that calls the GitHub
  Release and asset write APIs.
  Its tag-triggered entry point repeats the commit and generated-PR
  authorization checks before running release quality and creating the public
  Release; the PR-only Release Please action never performs that mutation.
- A manual publishing run must be dispatched from the repository default
  branch and can only retry an existing exact tag. It cannot choose a new
  version or create its tag.
- A dispatch without a version is build-only. It must use `repair=none` and
  cannot create a tag, Release, or Homebrew change.
- The global `env-vault-release` concurrency group serializes both release
  planning and publication. A tag-triggered publisher waits until its planning
  job finishes label reconciliation, and a later proposal waits for an active
  publication. `cancel-in-progress: false` and `queue: max` retain every handoff.
  Do not intentionally dispatch competing repairs: inspect the active run first.
- A version lower than the version in the current Homebrew formula is refused.
  An equal version is a repair or idempotent retry only: existing remote state
  must not conflict. Missing archive/checksum pairs and supply-chain evidence
  may be completed, but existing bytes, tag SHA, or formula metadata may not be
  replaced.
- A GitHub Release is not healthy until its exact Homebrew formula has passed
  the tap CI for the commit on the tap default branch.
- The generated formula declares macOS 15 (Sequoia) as its minimum, selects all
  four macOS/Linux architecture archives through `on_arm`/`on_intel`, and
  installs `README.md`, `LICENSE`, and `THIRD_PARTY_NOTICES.md` as documentation.

## Version and changelog policy

Release Please v5 reads the Conventional Commit squash subjects on `main` and
the checked-in manifest. The pull request title policy is documented in
[`CONTRIBUTING.md`](CONTRIBUTING.md). The checked-in Release Please
configuration is authoritative for the exact bump and changelog sections;
operators must not imitate its calculation by guessing a version or creating a
tag.

Review the generated release pull request as a release artifact:

Its checked-in header states that merging authorizes publication of that exact
version after green `main` CI. If that marker is missing or changed, stop; both
the proposal and publication gates reject the PR.

1. Confirm that its base is the current `main` and that it changes only the
   expected version documentation, manifest, and `CHANGELOG.md` paths.
2. Confirm that the proposed version is strict SemVer without a prerelease or
   build suffix and is greater than the current published version.
3. Read every changelog entry against the actual merged changes. No secret,
   credential, temporary path, test sentinel, or unsupported claim may appear.
4. Confirm that required pull request checks are successful, conversations are
   resolved, and the head has not changed since review.
5. Squash-merge the release pull request. That merge action is the explicit
   approval to publish the exact proposed version; do not separately create the
   tag or Release.

The release-planning workflow waits for `ci` to succeed on the resulting exact
`main` SHA. It verifies the deterministic release subject, version increase,
three-path diff, regular-file modes, README marker, non-empty changelog section,
generated PR provenance, current manifest, `main` ancestry, and the exact CI
run. Only then may it create the exact tag at that SHA and reconcile the PR to
`autorelease: tagged`. That tag starts `build-binaries`, whose tag entry point
repeats the authorization checks before publication.

## Prepare external publication

Before merging a release pull request:

1. Confirm that the release-planning App/environment, Homebrew App/environment,
   `env-vault` ruleset, and tap ruleset match
   [`docs/release-external-settings.md`](docs/release-external-settings.md).
   Rebase merging must be disabled, and squash commits must use `PR_TITLE` plus
   `PR_BODY`; planning verifies this before any write token is used.
2. Inspect the current Homebrew version. The proposed version must be higher:

   ```sh
   git -C ../homebrew-tap fetch origin main
   git -C ../homebrew-tap show origin/main:Formula/env-vault.rb |
     sed -nE 's/^[[:space:]]*version "([^"]+)"$/\1/p'
   ```

3. Ensure that no publication is active or queued:

   ```sh
   gh run list --repo ildarbinanas-design/env-vault \
     --workflow build-binaries.yml --limit 10 \
     --json databaseId,status,conclusion,headSha,url
   ```

4. After any planning-App installation or key change, dispatch
   `audit-release-planning-app.yml` and require it to prove that the
   installation contains only `env-vault`. After any tap-App installation or
   key change, dispatch `audit-release-app.yml` and require it to prove that
   the installation contains only `homebrew-tap`.

Authentication, API, transport, and parsing failures are never evidence that a
tag, Release, pull request, or workflow run is absent.

## Machine-readable release status

`releasectl` is a repository operator tool; it is not part of the distributed
`env-vault` command surface. It observes the exact release chain through
read-only `gh api --method GET` requests and emits one versioned JSON document.
It never merges a pull request, reruns a workflow, creates or moves a tag,
uploads an asset, or changes Homebrew state.

Inspect one snapshot with the exact reviewed release identity when it is known:

```sh
go run ./cmd/releasectl release status \
  --repo ildarbinanas-design/env-vault \
  --version vX.Y.Z \
  --source-sha 0123456789abcdef0123456789abcdef01234567 \
  --json
```

The default single-snapshot deadline is two minutes. `watch` uses a three-hour
overall deadline by default so the declared quality and Homebrew job limits fit
inside one observation; `--timeout` overrides the applicable deadline.

Omitting `--source-sha` is supported after the exact tag exists; the tool then
resolves and freezes the source identity from that tag. Supply the SHA while
waiting before tag creation so `main` CI and release planning can be observed
without guessing identity. Watch until a terminal state with one final JSON
document and no intermediate stdout:

```sh
go run ./cmd/releasectl release watch \
  --repo ildarbinanas-design/env-vault \
  --version vX.Y.Z \
  --source-sha 0123456789abcdef0123456789abcdef01234567 \
  --interval 30s \
  --timeout 3h \
  --json
```

The schema identifier is `env-vault.release-status.v1`. The document binds the
tag, exact-SHA `main` CI and planning runs, tag-triggered publisher, GitHub
Release asset set, and the publisher's `supply_chain`, `homebrew`, and `health`
jobs. `failed_jobs[].failed_steps` identifies a deterministic failure without
requiring log interpretation. `next_action.code` is a stable machine value;
it is never free-form LLM guidance. Manual repair-run correlation and an
independent reimplementation of attestation or tap verification are outside
v1; the successful publisher jobs remain the evidence for those checks.

Exit statuses are part of the operator contract:

| Status | Meaning |
| ---: | --- |
| `0` | valid pending/running snapshot, or a successful terminal chain |
| `1` | observed terminal failure or inconsistent remote state |
| `2` | invalid command or identity input |
| `3` | dependency, authentication, transport, API, or response-schema failure |
| `4` | `watch` timed out and emitted its last valid snapshot |

An explicit HTTP 404 or an empty exact-filter result may represent an absent
stage. Authentication, rate-limit, network, server, and malformed-response
errors produce `ok=false` and never become `not_found`.

## Build-only validation

A build-only run exercises tests, vet, race, smoke, the license gate, all five
builds, native version smoke jobs, and the full binary-only E2E matrix. Build
artifacts are retained for 14 days and E2E reports for 30 days. A build-only run
does not publish anything. The E2E matrix and report contracts are documented
in [`docs/e2e.md`](docs/e2e.md).

From the Actions UI, run `build-binaries`, leave `version` empty, and select
`repair=none`. The equivalent CLI command on the current branch is:

```sh
gh workflow run build-binaries.yml --repo ildarbinanas-design/env-vault \
  --ref "$(git branch --show-current)" \
  -f version= \
  -f repair=none
```

Watch the run and inspect every matrix result:

```sh
gh run list --repo ildarbinanas-design/env-vault \
  --workflow build-binaries.yml --limit 1
gh run watch RUN_ID --repo ildarbinanas-design/env-vault --exit-status
```

The branch or ref name is used as the embedded version label for build-only
runs. Strict `vMAJOR.MINOR.PATCH` validation applies only when publishing.

## Publish a new release

The normal entry point is the merge of the generated Release Please pull
request. Do not dispatch `build-binaries` for a proposed version while that
pull request is open. After the merge:

1. `ci` checks the exact release merge SHA on `main`.
2. `release-please` accepts only a successful push run from this repository for
   the default branch, checks out that exact SHA, and skips stale planning-only
   runs whose SHA is no longer the current head.
3. For a release merge it validates the deterministic commit and generated PR,
   then creates or verifies the exact tag and marks the PR
   `autorelease: tagged`. The tag push starts `build-binaries`.
4. `build-binaries` repeats release quality, checks Homebrew monotonicity, and
   only then creates the GitHub Release. Its notes are the non-empty exact
   version section extracted from the reviewed `CHANGELOG.md`, not regenerated
   from mutable GitHub metadata.
5. The same run publishes attestations, updates Homebrew through its protected
   pull request, and finishes with the health verification.

Record the release-planning run and `build-binaries` run URLs. The source SHA
embedded in the dispatch, tag, artifacts, attestations, formula marker, and
health evidence must remain identical.

If planning fails before creating the tag, rerun that exact `release-please`
workflow after fixing the cause; `build-binaries` cannot replace the tag
handoff. After the authorized tag exists and the PR is `autorelease: tagged`, a
manual full retry may be dispatched from the repository default branch:

```sh
export VERSION=vX.Y.Z
export REPOSITORY=ildarbinanas-design/env-vault
gh workflow run build-binaries.yml --repo "$REPOSITORY" \
  --ref main \
  -f version="$VERSION" \
  -f repair=none
```

Use GitHub's actual default branch in place of `main` if it changes. The
dispatch resolves the existing tag, repeats deterministic release-PR
authorization for `v0.0.8` and later, and never creates or moves a tag. A tag
push is an event trigger, not an authorization mechanism.

Record the workflow URL and wait for it to finish. A failed run is a partial
release until remote state has been inspected; do not immediately create a new
tag or upload replacement files.

## Repair modes

Every repair is a manual dispatch from the default branch and requires the
exact existing version. The workflow resolves the source SHA from that tag.

Releases `v0.0.1` through `v0.0.7` predate the Release Please manifest. Their
manual repair compatibility path requires an existing stable GitHub Release,
an exact tag contained in current `main`, and never creates a tag. Starting at
`v0.0.8`, every retry additionally requires the deterministic generated-PR and
`autorelease: tagged` authorization. This legacy boundary exists only for
already published versions; it cannot authorize a new release.

| Mode | Rebuilds | Release assets | Homebrew | Health check | Use when |
| --- | --- | --- | --- | --- | --- |
| `none` | yes | reconcile | PR/update or exact no-op | yes | complete idempotent retry after the authorized tag exists |
| `release-assets` | yes | reconcile | PR/update or exact no-op | yes | the tag is correct but the Release, assets, or attestations are incomplete |
| `homebrew` | no | verify/download | resume PR or exact no-op | yes | Release assets and attestations are complete and the tap stage must be resumed |
| `health` | no | verify/download | read-only verification | yes | publication is complete and only health evidence must be repeated |

Dispatch a repair with:

```sh
export VERSION=vX.Y.Z
export REPOSITORY=ildarbinanas-design/env-vault
gh workflow run build-binaries.yml --repo "$REPOSITORY" \
  --ref main \
  -f version="$VERSION" \
  -f repair=release-assets
```

Replace `release-assets` only after matching the observed remote state to the
table. A tag-triggered run cannot select repair mode. Missing attestations can
be minted only when the workflow's own source SHA equals the release tag SHA.
If `main` has advanced, rerun the original failed workflow attempt at that SHA;
the workflow deliberately refuses to claim provenance from a later commit.

The `none`, `release-assets`, and `homebrew` modes schedule the `homebrew` job,
which alone declares `environment: release` and mints the short-lived tap App
token. `health` skips that job and cannot read release-environment values. It
clones the public tap default branch, verifies the exact generated formula, and
waits for the successful `push` run on that exact tap SHA using read-only
permissions.

### Safe retry rules

1. Inspect the failed job and remote tag, Release, and asset state first.
2. For an infrastructure-only failure before any publication, rerunning failed
   jobs is acceptable:

   ```sh
   gh run rerun RUN_ID --repo "$REPOSITORY" --failed
   ```

3. Once a tag or Release exists, prefer an explicit repair dispatch. The
   repair scripts accept an existing tag only when it resolves to the expected
   source SHA. They accept an existing Release only when it is public,
   non-draft, non-prerelease, and bound to the expected tag.
4. Existing archive/checksum pairs are downloaded and verified. Missing
   members may be added only when the available bytes prove the pair. A hash,
   filename, tag SHA, Release state, or formula mismatch is a hard stop.
5. Never use `gh release upload --clobber`. That flag deletes and re-uploads an
   asset with the same name, which destroys the immutable retry boundary.
6. A same-version formula that exactly matches is a no-op. A same-version
   formula with different metadata or checksums is an incident, not an update.
7. Missing or partial provenance/SBOM evidence requires
   `repair=release-assets` while the workflow still runs at the release source
   SHA, or a rerun of the original failed workflow after `main` advances.
   `repair=homebrew` and `repair=health` verify existing attestations and fail
   closed; they do not mint replacements.
8. A tap retry reuses only the deterministic `release/env-vault-$VERSION`
   branch and matching PR. The helper rejects unexpected files, formula bytes,
   release markers, source SHAs, closed PRs, or changed PR heads. After fixing
   an external check or merge-policy failure, use `repair=homebrew`; never
   overwrite or force-push the version branch.

### Current-release-only monotonic boundary

The Homebrew monotonic guard compares the requested version with the one
currently published in `Formula/env-vault.rb`. Therefore:

- a normal release must be higher than the tap version;
- an idempotent retry or repair may be equal;
- a repair for an older release is intentionally rejected after Homebrew has
  advanced; and
- the guard does not replace an audit of all GitHub tags, Releases, or other
  distribution channels.

If a historical release needs correction after the tap has advanced, preserve
it and publish a new patch release. Do not lower the tap to make the historical
repair pass.

## Healthy release definition

A release is healthy only when all of the following are true for the same
`$VERSION` and `$SOURCE_SHA`:

1. The tag exists and resolves, through any annotated tag objects, to exactly
   `$SOURCE_SHA`.
2. The GitHub Release is publicly visible, has `tagName == $VERSION`, and is
   neither a draft nor a prerelease.
3. The Release contains exactly these ten assets, with no duplicate or extra
   names:

   ```text
   env-vault-linux-amd64.tar.gz
   env-vault-linux-amd64.tar.gz.sha256
   env-vault-linux-arm64.tar.gz
   env-vault-linux-arm64.tar.gz.sha256
   env-vault-darwin-amd64.tar.gz
   env-vault-darwin-amd64.tar.gz.sha256
   env-vault-darwin-arm64.tar.gz
   env-vault-darwin-arm64.tar.gz.sha256
   env-vault-windows-amd64.zip
   env-vault-windows-amd64.zip.sha256
   ```

   Every checksum file contains one SHA-256 record naming its paired archive,
   and every archive matches that record. The native smoke jobs have verified
   that both version commands print exactly `$VERSION`.
4. `homebrew-tap/Formula/env-vault.rb` is byte-for-byte the formula generated
   from those published assets. Its version, tag URLs, and four platform
   checksums are exact. It declares macOS Sequoia as the minimum, uses
   `on_arm`/`on_intel` blocks, installs the three archived documentation files,
   and its test asserts the exact output `v#{version}`.
5. The `test-formula` workflow has succeeded for the formula commit on the tap
   default branch. A successful PR check alone is not enough if the merged
   commit differs.
6. The publishing workflow has generated build-provenance and SBOM
   attestations for all five archives. The combined SPDX document was available
   inside a 14-day workflow artifact named for the version and run attempt.
   Expiry of that short-lived workflow artifact does not invalidate a release
   if GitHub's persisted attestations for every archive still verify against
   `$SOURCE_SHA`.

The workflow verifies all six conditions. Before merge, `homebrew` waits for a
successful `test-formula.yml` run whose event is `pull_request` and whose
`head_sha` is the unchanged PR head. It then squash-merges with
`--match-head-commit`, resolves the actual tap commit, and waits for a
successful run whose event is `push` and whose `head_sha` is that exact commit.
The `health` job verifies the formula at that commit and consumes those exact
outputs. In `repair=health`, it independently resolves the current tap SHA and
repeats the exact push-run wait without an App credential.

The job summary links to the Release, source SHA, tap pull request when one
exists, exact tap commit, successful exact-SHA tap CI run, attestations, and
release workflow run. Treat those URLs as one evidence set; a checks page or a
successful run for another SHA is not equivalent.

## Verify a completed release

Authenticate `gh`, then run the repository helpers from a clean `env-vault`
checkout and a fresh temporary directory:

```sh
export VERSION=vX.Y.Z
export SOURCE_SHA=0123456789abcdef0123456789abcdef01234567
export REPOSITORY=ildarbinanas-design/env-vault
export TAP_REPOSITORY=ildarbinanas-design/homebrew-tap
export GITHUB_REPOSITORY=$REPOSITORY
scripts/release/resolve-tag-sha.sh "$VERSION"
scripts/release/get-release-state.sh "$VERSION"
export HEALTH_DIR="$(mktemp -d)"
scripts/release/download-release-assets.sh "$VERSION" "$HEALTH_DIR/assets"
git clone --depth 1 "https://github.com/${TAP_REPOSITORY}.git" "$HEALTH_DIR/tap"
scripts/release/verify-homebrew-formula.sh \
  "$VERSION" "$HEALTH_DIR/assets" "$HEALTH_DIR/tap/Formula/env-vault.rb"
```

Compare the first command with `$SOURCE_SHA`; the release state must be exactly
`$VERSION|false|false`. The download helper rejects missing, extra, duplicate,
malformed, or mismatched archive/checksum pairs.

Find the tap commit and its push CI run:

```sh
export TAP_SHA="$(git -C "$HEALTH_DIR/tap" rev-parse HEAD)"
gh run list --repo "$TAP_REPOSITORY" \
  --workflow test-formula.yml \
  --commit "$TAP_SHA" \
  --event push \
  --json databaseId,headSha,status,conclusion,url
```

Require one completed run with `headSha == $TAP_SHA` and
`conclusion == "success"`. Record the Release URL, source SHA, tap SHA, tap CI
URL, and release workflow URL in the release evidence.

For an installed Homebrew copy, also run:

```sh
brew update
brew style ildarbinanas-design/tap
brew test ildarbinanas-design/tap/env-vault
test "$(env-vault --version)" = "$VERSION"
test "$(env-vault version)" = "$VERSION"
```

## SBOM and artifact attestations

The Release contract remains exactly the ten archive/checksum assets listed
above. Supply-chain evidence is published separately so it cannot accidentally
change the immutable Release-asset set:

- Syft `v1.44.0` scans the extracted contents of all five platform packages and
  writes one combined SPDX JSON document named `env-vault-sbom.spdx.json`.
- Extraction uses the repository's bounded Go helper. It accepts only the five
  expected archive/root pairs and rejects traversal, links, special files,
  collisions, excessive entries, and excessive compressed or expanded sizes
  before the attestation action receives an OIDC token.
- The file is uploaded inside the retry-safe workflow artifact
  `env-vault-sbom-$VERSION-attempt-$GITHUB_RUN_ATTEMPT` with 14-day retention.
  It is not a GitHub Release asset.
- `actions/attest@v4` publishes build-provenance and SBOM attestations whose
  subjects are each of the five release archives.
- The attesting job lives in `.github/workflows/build-binaries.yml`, so that is
  the signer workflow identity to enforce during verification.

The attesting job has only the GitHub Actions permissions required for this
operation:

```yaml
permissions:
  contents: read
  id-token: write
  attestations: write
  artifact-metadata: write
```

Download and retain the SBOM during the 14-day review window:

```sh
gh api "repos/${REPOSITORY}/actions/runs/RUN_ID/artifacts" \
  --jq '.artifacts[] | select(.name | startswith("env-vault-sbom-")) | .name'
export SBOM_ARTIFACT="env-vault-sbom-${VERSION}-attempt-RUN_ATTEMPT"
gh run download RUN_ID \
  --repo "$REPOSITORY" \
  --name "$SBOM_ARTIFACT" \
  --dir "$HEALTH_DIR/sbom"
test -f "$HEALTH_DIR/sbom/env-vault-sbom.spdx.json"
```

Replace `RUN_ATTEMPT` with the suffix shown by the API query. For every archive,
verify its Release association, provenance signer, source digest, and SBOM
attestation. The tag check above establishes `$SOURCE_SHA`; artifact
verification enforces that same digest together with the exact archive digest
and signer identity. The default
`gh attestation verify` predicate is SLSA build provenance; SPDX SBOM
attestations require the SPDX predicate type.

```sh
export SIGNER_WORKFLOW=ildarbinanas-design/env-vault/.github/workflows/build-binaries.yml
for archive in \
  env-vault-linux-amd64.tar.gz \
  env-vault-linux-arm64.tar.gz \
  env-vault-darwin-amd64.tar.gz \
  env-vault-darwin-arm64.tar.gz \
  env-vault-windows-amd64.zip
do
  path="$HEALTH_DIR/assets/$archive"
  gh release verify-asset "$VERSION" "$path" --repo "$REPOSITORY"
  gh attestation verify "$path" \
    --repo "$REPOSITORY" \
    --signer-workflow "$SIGNER_WORKFLOW" \
    --source-digest "$SOURCE_SHA"
  gh attestation verify "$path" \
    --repo "$REPOSITORY" \
    --signer-workflow "$SIGNER_WORKFLOW" \
    --source-digest "$SOURCE_SHA" \
    --predicate-type https://spdx.dev/Document/v2.3
done
```

If signing ever moves to another or reusable workflow, change the enforced
signer identity at the same time and cover it with a workflow regression test.
Keep the tag, Release archives, checksums, SBOM, and provenance in the same
version-specific evidence record.

## Rollback and withdrawal

There is no destructive rollback for an immutable published release.

- Never move or delete-and-recreate the tag.
- Never overwrite an archive or checksum, including with `--clobber`.
- Never point the Homebrew formula at an older version to undo a bad release.
- Never reuse the version for different source or bytes.

For an incomplete but internally consistent release, use the narrowest repair
mode. For a wrong SHA, checksum mismatch, unsafe binary, or other content
incident:

1. Stop the release and preserve logs, SHAs, checksums, and URLs. Do not include
   credentials or secret values in the incident record.
2. Mark the GitHub Release clearly as withdrawn or deprecated in its title and
   notes without changing its tag or assets. GitHub Releases do not provide a
   package-manager-style yank that makes mutated bytes safe.
3. If installation must be blocked before a fixed build exists, submit a tap
   pull request that uses Homebrew's documented deprecation or disablement
   mechanism. Do not downgrade the formula URL or checksum.
4. If the version is consumed as a Go module, consider a `retract` directive in
   the next module version in addition to the release notice.
5. Fix forward and publish a higher patch version from the correct source
   commit. Update Homebrew to that patch and repeat the full health check.

The follow-up patch is the recovery boundary. A release is not healthy merely
because a replacement version exists; the replacement must independently meet
every condition in the healthy release definition.
