# Releasing env-vault

This runbook covers the deterministic release path, narrow repair paths, and
release incidents. GitHub transport and mutations use `gh`. Repository tools
consume saved JSON and artifacts only; they do not access the network or hold
credentials.

Release Please v5 prepares the version, `CHANGELOG.md`, manifest, and marked
README line in a generated pull request. The exact release merge is tested and
packaged by normal `ci`. Release planning creates the immutable tag only after
that exact CI attempt and its promotion manifest pass the pre-tag gate.
`build-binaries` then promotes those verified bytes; it does not rebuild the
product or repeat version-independent source quality.

Never move or delete an existing release tag, overwrite a Release asset, mix
artifacts from workflow attempts, or lower the Homebrew version.

## Sources of truth

- [`release/contract.v1.json`](release/contract.v1.json) is the single
  declarative release contract. It defines the five native platforms, ten
  archive/checksum assets, workflow identities, App identities, release
  stages, repair actions, schemas, and stable action/reason/error codes.
- [`docs/e2e-baseline.json`](docs/e2e-baseline.json) is the durable E2E
  compatibility baseline. CI verifies it from the current checkout; it does
  not download an expiring historical comparator artifact.
- `.release-please-manifest.json`, `release-please-config.json`,
  `CHANGELOG.md`, and the marked README version line are the reviewed version
  boundary.
- A promotion manifest is valid only for one repository, exact version,
  source SHA, CI run ID, and run attempt. It contains the five platform proofs,
  ten artifact digests, semantic suite and contract identities, and the
  source-quality, contract, literal-version, coverage, and leak results.

Unknown, incomplete, malformed, authentication-failed, rate-limited, or
transport-failed state is not absence. Every release gate fails closed.

## The only human checkpoint

Review the generated Release Please pull request semantically: confirm the
version and changelog describe the changes, its required checks are green, and
its exact head has not changed. Before that generated pull request is merged,
record all three coordinates:

- exact `vX.Y.Z` version;
- generated release PR number;
- full 40-character head SHA.

The one release authorization must be exactly:

```text
ПОДТВЕРЖДАЮ RELEASE <version> PR #<number> SHA <full-sha>
```

After receiving that one human confirmation, automation records the same
byte-exact line as a comment on the generated PR before merging it. The release
gate accepts exactly one matching comment from a GitHub `User` whose author
association is `OWNER` or `MEMBER`; both its creation and last-update times
must be strictly earlier than the PR merge time. GitHub timestamps have
one-second precision, so a comment created or edited in the same recorded
second as the merge is ambiguous and fails closed; automation waits for the
next observable second, rechecks the unchanged tuple, and only then merges.
The user is not asked to post a second manual confirmation. Durable evidence
binds the comment ID, URL, actor, association, timestamps, canonical-body
SHA-256, PR number and head SHA.

Immediately before merge, re-read the remote PR and require the same tuple.
Any version, PR number, or head-SHA change invalidates the authorization. There
is no additional routine approval for tag creation, publication, Homebrew, or
post-release verification.

Opening, updating, approving, or closing the generated PR is not publication
authorization. Do not create a tag or Release manually.

## Normal release sequence

1. Release Please opens or updates the generated release PR in PR-only mode.
2. The PR's normal `ci` run verifies the exact proposed version on all five
   native targets.
3. After the exact tuple authorization has been recorded as the exact pre-merge
   PR comment, squash-merge that unchanged generated PR. The merge commit
   becomes the release source SHA.
4. The `ci` push run for that exact `main` SHA performs source quality once,
   builds the five native artifacts, runs E2E and leak gates, and verifies all
   three literal version forms on every target. A bounded native
   `release-version-probe` executes them with a scrubbed environment and saves
   versioned JSON; `releasecheck` only reads and binds those bytes:

   ```text
   env-vault --version
   env-vault version
   env-vault version --json
   ```

5. The same CI attempt seals a versioned promotion manifest. Its five native
   proofs and ten assets all carry the same run ID and attempt.
6. `release-please` downloads that exact attempt, classifies completeness,
   verifies the manifest and all ten bytes offline, rechecks generated-PR
   provenance, and only then creates or verifies the immutable tag at the
   release source SHA.
7. The tag starts `build-binaries`. Its seven jobs are `metadata`, `preflight`,
   `promotion`, `release`, `supply_chain`, `homebrew`, and `health`.
   `promotion` downloads and verifies the same CI attempt again; `release`
   publishes those bytes without rebuilding.
8. `supply_chain` creates exact-source provenance and SPDX SBOM attestations.
   `homebrew` creates or reuses the deterministic tap PR, requires CI on its
   exact head, squash-merges with a head guard, and requires post-merge tap CI
   on the exact release merge SHA. The current tap SHA is observed separately
   and may advance only as a descendant while the formula remains exact.
9. `health` verifies the tag, Release, ten assets, digests, attestations,
   Homebrew formula, PR head, both tap CI gates, and the protected failed-tag
   exception. It also downloads the unique attempt-qualified settings proof
   from the exact successful planning run and replays it offline; `health`
   never receives the planning App credential or queries Administration APIs.
10. `release-evidence` preserves versioned machine JSON, a generated Markdown
    index, and automatic timing/retry metrics.

The shared `env-vault-release` concurrency group covers planning,
publication, and durable evidence with cancellation disabled and
`queue: max`; GitHub queues up to 100 pending runs in arrival order instead of
discarding an older pending release stage. Manual CI dispatch has its own
identity and cannot cancel an automatic green-`main` run. A full CI rerun also
uses an attempt-qualified concurrency identity, so it cannot cancel a newer
automatic `main` run.

## CI topology

The normal `ci` path has one reusable quality graph plus the caller's required
`quality-gate`:

- one contract/version resolver;
- one combined source-quality job (`tidy`, module verification, tests, vet,
  smoke, and full race suite);
- three native license jobs;
- five native build/package/E2E jobs;
- one `e2e-gate` that validates the matrix once, checks the durable baseline,
  and seals release promotion evidence when the push is a release merge;
- one top-level `quality-gate`, which remains `always()` so cancellation cannot
  become a merge bypass.

Downstream gates use `always() && !cancelled()`: upstream failures are reported
deterministically, while an intentional cancellation does not start more work.
The publisher does not rerun this graph for the same source SHA.

## Offline `releasecheck`

Build the checker from the source revision being inspected:

```sh
go build -trimpath -o ./releasecheck ./cmd/releasecheck
./releasecheck --version --json
./releasecheck validate-contract --json
./releasecheck contract matrix --json
```

`--version --json` reports the checker version, build/source revision when
available, supported schema versions, release contract schema, and semantic
contract hash. `releasecheck` has no network client, never reads credentials,
and never executes a candidate binary. Use `gh` to save remote observations,
then pass filenames:

```sh
REPOSITORY=ildarbinanas-design/env-vault
RUN_ID=123456789

gh api "repos/$REPOSITORY/actions/runs/$RUN_ID" > run.json
gh api --paginate --slurp \
  "repos/$REPOSITORY/actions/runs/$RUN_ID/artifacts?per_page=100" \
  > artifacts.json

./releasecheck classify-attempt \
  --run run.json --artifacts artifacts.json --json \
  > attempt-classification.json
```

The checker accepts complete saved responses and rejects duplicate or
case-variant JSON keys, unknown fields, unsupported schemas, incompatible
contract identities, and incomplete evidence.

Exit statuses are stable:

| Status | Meaning |
| ---: | --- |
| `0` | requested offline validation or evidence generation succeeded |
| `2` | command-line usage error |
| `3` | release contract invalid or schema unsupported |
| `4` | valid attempt classification requires waiting, inspection, or `rerun_all_jobs` |
| `5` | saved input or promotion evidence is invalid, incomplete, or inconsistent |
| `6` | internal or no-clobber output failure |

Promotion verification is explicit about every coordinate:

```sh
./releasecheck promotion verify \
  --manifest promotion-manifest.json \
  --source-sha "$SOURCE_SHA" \
  --release-version "$VERSION" \
  --repository "$REPOSITORY" \
  --run-id "$RUN_ID" \
  --run-attempt "$RUN_ATTEMPT" \
  --artifacts-root release-assets \
  --json
```

## Incomplete workflow attempts

An incomplete current attempt cannot be repaired with “rerun failed jobs”:
that operation can leave artifacts from different executions under one run.
The classifier instead emits all of the following:

- `ok=false`;
- `action_code="rerun_all_jobs"`;
- exact run ID and attempt;
- sorted missing targets/artifacts;
- `reason_code="ATTEMPT_MATRIX_INCOMPLETE"`;
- `rerun_failed_jobs_allowed=false` and the prohibited action
  `rerun_failed_jobs`.

The read-only planning job preserves the run, artifact inventory, and
classification JSON, then an isolated `actions:write` job re-snapshots the
same tuple and automatically performs at most one full rerun. The guarded
transport shim validates the entire document and deliberately invokes
`gh run rerun` without `--failed`; the same command remains useful for a
diagnostic reproduction:

```sh
scripts/release/rerun-classified-attempt.sh \
  attempt-classification.json ildarbinanas-design/env-vault
```

The new completed attempt triggers classification again. A second incomplete
attempt stops with the same machine action instead of entering an infinite
retry loop. Never copy artifacts between attempts or manually edit the
manifest.

## Manual repairs

Manual publisher dispatch is only for an existing exact immutable tag. Run it
at that tag ref, not at `main`:

```sh
VERSION=vX.Y.Z
REPOSITORY=ildarbinanas-design/env-vault
gh workflow run build-binaries.yml \
  --repo "$REPOSITORY" \
  --ref "$VERSION" \
  -f version="$VERSION" \
  -f repair=release-assets
```

| Repair | Rebuilds product | Resume point | Required existing state |
| --- | --- | --- | --- |
| `release-assets` | no | promotion/publication | exact tag and publication-eligible CI promotion attempt |
| `homebrew` | no | Homebrew | exact public Release, ten assets, and attestations |
| `health` | no | read-only health | publication complete; regenerate verification/evidence only |

The publisher resolves the source SHA from the tag and fails if the tag,
generated release provenance, CI attempt, promotion manifest, existing Release
bytes, or Homebrew state conflicts. Existing assets are verified before any
missing asset is uploaded. `gh release upload --clobber` is forbidden.

Use a repair only after collecting the exact failed job, step, log, run ID,
attempt, artifacts, and remote state. Fix workflow or code defects through a
normal reviewed PR; do not mask a reproducible failure with repeated reruns.

## Legacy and blocked versions

`v0.0.1` through `v0.0.7` may be rebuilt only for diagnostics through
`legacy-rebuild.yml`. The contract binds each immutable tag to its peeled
source SHA and Go `1.22.12`; every output declares
`publication_eligible=false`. Legacy diagnostic bytes must never enter a
promotion manifest, GitHub Release, or Homebrew update.

```sh
gh workflow run legacy-rebuild.yml \
  --repo ildarbinanas-design/env-vault \
  --ref main \
  -f version=v0.0.7
```

`v0.0.8` is a permanently failed immutable tag at
`1d094f9e4a3e0343e713d4126f6118a8a9e98e2d`. It must remain present and must
not acquire a GitHub Release. It is blocked from steady-state publication and
from the legacy diagnostic selector.

Historical published releases are immutable. If one needs correction, publish
a higher patch version; never rebuild historical bytes for publication or
lower the tap.

## Healthy release definition

A release is healthy only when one evidence tuple proves all of the following:

1. The immutable tag peels to the exact release source SHA.
2. The GitHub Release is public, non-draft, non-prerelease, and bound to that
   tag.
3. It contains exactly five archives and their five matching SHA-256 sidecars,
   with no duplicate or extra assets and no changed bytes.
4. All five archives were promoted from one CI run attempt whose manifest
   passed source quality, contracts, coverage, leak scanning, semantic-suite
   identity, and the three literal version checks.
5. Build-provenance and SPDX SBOM attestations for all five archives verify
   against the exact source SHA and publisher workflow identity. Evidence
   embeds the exact raw `gh attestation verify` JSON and its digest for each
   archive/predicate pair, then replays all ten documents offline.
6. The generated Homebrew formula is byte-exact for the version and four
   supported Homebrew archives; the version is monotonic.
7. The deterministic tap PR's recorded head passed pull-request CI, the exact
   head was merged, post-merge tap CI passed on that immutable merge SHA, and
   the current tap SHA contains the merge with the byte-exact formula intact.
8. A pre-tag settings proof binds the exact repository merge policy, three
   rulesets, present empty bypass lists, source/version, and planning run
   attempt; health and durable evidence replay its self-digest offline.
9. Release health passed and `v0.0.8` still has no GitHub Release.

The Release asset set is always exactly:

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

Supply-chain documents remain attestations and workflow artifacts; they are
not added to the ten-asset Release contract.

## Evidence and metrics

The release workflows automatically generate versioned machine JSON and a
short Markdown index. Evidence binds source and tag SHAs, CI and publisher run
IDs/attempts, promotion and artifact digests, attestation verification,
Homebrew PR/head/merge coordinates, both tap CI gates, publication state, and
health state. It also records the exact publisher repair mode. The first
successful evidence remains immutable at `evidence/releases/vX.Y.Z/`; every
successful publisher run and attempt, including that first publication, is
additionally stored at
`evidence/releases/vX.Y.Z/publisher-runs/run-RUN_ID/attempt-ATTEMPT/`.
Replaying the same run/attempt is a no-op only when all four files are
byte-identical. A partial tuple directory, unexpected path, or conflicting
bytes fails closed; a later repair appends its own tuple directory without
rewriting the initial snapshot. Evidence must not contain credentials or
secret values.

Metrics are derived from a saved complete `gh run view` document:

```sh
gh run view "$RUN_ID" --attempt "$RUN_ATTEMPT" --repo "$REPOSITORY" \
  --json attempt,conclusion,createdAt,databaseId,event,headSha,jobs,startedAt,status,updatedAt,url,workflowName \
  > run-metrics-input.json

./releasecheck metrics \
  --run-json run-metrics-input.json \
  --output release-metrics.json
```

The versioned metrics record includes queue time, wall time, job count,
aggregate runner-seconds, retries, observed critical path, and available
artifact/cache transfer time. Outputs are no-clobber regular files.

## External configuration and incidents

Before a release, the release-planning App, tap App, environments, branch/tag
rulesets, and required checks must match
[`docs/release-external-settings.md`](docs/release-external-settings.md).
Neither App may bypass a ruleset. Only the `homebrew` job receives the tap App
credential; supply-chain OIDC and Release writes remain separate permission
boundaries.

For a wrong SHA, checksum mismatch, unsafe binary, or inconsistent published
state:

1. Stop and preserve machine evidence, logs, SHAs, digests, and URLs without
   credentials or secret values.
2. Do not move the tag, replace assets, rewrite attestations, force-push the
   tap branch, or weaken a required check/environment/ruleset.
3. Fix the defect through a normal PR and publish a higher patch version.
4. If necessary, mark the existing Release as withdrawn in its notes and use
   Homebrew's reviewed deprecation/disable mechanism; do not mutate its bytes.

Global release serialization, five native targets, E2E/burn-in frequency,
single-attempt identity, both Homebrew CI gates, and Windows concurrency
coverage are release guarantees, not emergency retry knobs.
