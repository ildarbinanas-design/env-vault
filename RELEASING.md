# Releasing env-vault

This is the operator runbook for build-only runs, new releases, repairs, and
release incidents. It describes the repository state as well as the additional
manual checks that are still required while Homebrew updates use a deploy key
and a direct push.

Do not publish from an unreviewed working tree. Do not move an existing tag,
replace an existing release asset, or lower the Homebrew version.

## Release invariants

- A publishing version is exactly `vMAJOR.MINOR.PATCH`. The leading `v` is part
  of the version and of the output from both `env-vault --version` and
  `env-vault version`.
- A manual publishing run must be dispatched from the repository default
  branch. The current default branch is `main`; the workflow checks GitHub's
  configured default branch rather than trusting a hard-coded name.
- A dispatch without a version is build-only. It must use `repair=none` and
  cannot create a tag, Release, or Homebrew change.
- The global `env-vault-release` concurrency group serializes release runs.
  `cancel-in-progress: false` prevents a running release from being cancelled,
  and `queue: max` retains pending release runs. Do not intentionally dispatch
  competing releases: inspect the active run before starting another one.
- A version lower than the version in the current Homebrew formula is refused.
  An equal version is a repair or idempotent retry only: existing remote state
  must not conflict. Missing archive/checksum pairs and supply-chain evidence
  may be completed, but existing bytes, tag SHA, or formula metadata may not be
  replaced.
- A GitHub Release is not healthy until its exact Homebrew formula has passed
  the tap CI for the commit on the tap default branch.

## Prepare a release

Set the intended version and repositories once. Keep the values in the current
shell; do not put credentials in these variables.

```sh
export VERSION=vX.Y.Z
export REPOSITORY=ildarbinanas-design/env-vault
export TAP_REPOSITORY=ildarbinanas-design/homebrew-tap
```

Before a new release:

1. Confirm that `$VERSION` matches `^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`.
2. Read the changes since the previous release and confirm that the selected
   version follows the project's compatibility policy.
3. Confirm that both repositories are clean and that `env-vault` is on the
   current default branch:

   ```sh
   git status --short --branch
   git -C ../homebrew-tap status --short --branch
   git fetch --tags origin
   test "$(git branch --show-current)" = "$(gh repo view "$REPOSITORY" --json defaultBranchRef --jq .defaultBranchRef.name)"
   ```

4. Record the source commit. This is the immutable expected SHA for every later
   tag and repair check:

   ```sh
   export SOURCE_SHA="$(git rev-parse HEAD)"
   printf '%s  %s\n' "$VERSION" "$SOURCE_SHA"
   ```

5. If this is the first attempt for the version, probe both the tag and Release:

   ```sh
   GITHUB_REPOSITORY="$REPOSITORY" scripts/release/resolve-tag-sha.sh "$VERSION"
   GITHUB_REPOSITORY="$REPOSITORY" scripts/release/get-release-state.sh "$VERSION"
   ```

   An explicit not-found result (exit status 4) is expected for each on a new
   version. If either exists, stop first-release preparation and follow the
   retry rules below. Authentication, API, transport, and parsing failures are
   never evidence that the remote object is absent.

6. Run the local gates that are available on the current platform:

   ```sh
   gofmt -w $(git ls-files '*.go')
   git diff --check
   go test ./...
   go vet ./...
   go test -race ./...
   scripts/smoke.sh
   scripts/license-check.sh
   ```

   Review any `gofmt` diff before proceeding. Platform-specific smoke and
   license jobs in GitHub Actions remain authoritative for platforms that are
   not represented locally.

7. Inspect the published Homebrew boundary:

   ```sh
   git -C ../homebrew-tap fetch origin main
   git -C ../homebrew-tap show origin/main:Formula/env-vault.rb |
     sed -nE 's/^[[:space:]]*version "([^"]+)"$/\1/p'
   ```

   A new version must be higher. The guard compares only with the version in
   the current tap formula; it is not a complete audit of every historical tag
   or Release. Inspect GitHub separately if the release history is uncertain.

8. Check Actions for an active or queued release. Do not race it with another
   dispatch:

   ```sh
   gh run list --repo "$REPOSITORY" --workflow build-binaries.yml \
     --limit 10 --json databaseId,status,conclusion,headSha,url
   ```

9. Confirm that the required external release settings are present before
   migrating the tap update to a pull request. The exact proposed settings and
   the current migration blocker are in
   [`docs/release-external-settings.md`](docs/release-external-settings.md).

## Build-only validation

A build-only run exercises tests, vet, race, smoke, the license gate, all five
builds, and native version smoke jobs. It uploads Actions artifacts for 14 days
but does not publish anything.

From the Actions UI, run `build-binaries`, leave `version` empty, and select
`repair=none`. The equivalent CLI command on the current branch is:

```sh
gh workflow run build-binaries.yml --repo "$REPOSITORY" \
  --ref "$(git branch --show-current)" \
  -f version= \
  -f repair=none
```

Watch the run and inspect every matrix result:

```sh
gh run list --repo "$REPOSITORY" --workflow build-binaries.yml --limit 1
gh run watch RUN_ID --repo "$REPOSITORY" --exit-status
```

The branch or ref name is used as the embedded version label for build-only
runs. Strict `vMAJOR.MINOR.PATCH` validation applies only when publishing.

## Publish a new release

The preferred entry point is a manual dispatch from the default branch. It
lets the workflow create the tag with its scoped `GITHUB_TOKEN`; that tag does
not recursively start a second release workflow.

```sh
gh workflow run build-binaries.yml --repo "$REPOSITORY" \
  --ref main \
  -f version="$VERSION" \
  -f repair=none
```

Use GitHub's actual default branch in place of `main` if it changes. A strict
`vX.Y.Z` tag push remains supported, but it is not the preferred operator path
because the tag must already have been created correctly.

Record the workflow URL and wait for it to finish. A failed run is a partial
release until remote state has been inspected; do not immediately create a new
tag or upload replacement files.

## Repair modes

Every repair is a manual dispatch from the default branch and requires the
exact existing version. The workflow resolves the source SHA from that tag.

| Mode | Rebuilds | Release assets | Homebrew | Health check | Use when |
| --- | --- | --- | --- | --- | --- |
| `none` | yes | reconcile | update/no-op | yes | first attempt, or a complete idempotent retry |
| `release-assets` | yes | reconcile | update/no-op | yes | the tag is correct but the Release, assets, or attestations are incomplete |
| `homebrew` | no | verify/download | update/no-op | yes | Release assets and attestations are complete and only the tap stage failed |
| `health` | no | verify/download | verify only | yes | publication is complete and only health evidence must be repeated |

Dispatch a repair with:

```sh
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
   checksums are exact, and its test asserts the exact output `v#{version}`.
5. The `test-formula` workflow has succeeded for the formula commit on the tap
   default branch. A successful PR check alone is not enough if the merged
   commit differs.
6. The publishing workflow has generated build-provenance and SBOM
   attestations for all five archives. The combined SPDX document was available
   inside a 14-day workflow artifact named for the version and run attempt.
   Expiry of that short-lived workflow artifact does not invalidate a release
   if GitHub's persisted attestations for every archive still verify against
   `$SOURCE_SHA`.

The current workflow's `health` job verifies items 1 through 4 and item 6, but
the direct-push implementation does not yet wait for the exact tap CI run in
item 5. Until the GitHub App/PR migration is approved and implemented, an
operator must verify item 5 manually before declaring the release healthy. The
job summary links to the Release, tap commit, tap checks page, attestations, and
release run for navigation; those links are not evidence that tap CI was
awaited or succeeded.

## Verify a completed release

Authenticate `gh`, then run the repository helpers from a clean `env-vault`
checkout and a fresh temporary directory:

```sh
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
