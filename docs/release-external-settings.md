# External settings for tap pull-request releases

This document is the external configuration contract for the release workflow
in `ildarbinanas-design/env-vault`. The workflow publishes Homebrew formula
changes through pull requests in `ildarbinanas-design/homebrew-tap` and proves
both the pull-request check and the post-merge default-branch check for exact
commit SHAs.

Repository settings must match this document before a release is dispatched.
Credentials are stored only in the `release` environment. Publishing exposes
them only to the `homebrew` job; the separate manual scope audit can request a
metadata-only token after an installation or key change.

## Trust boundary

The cross-repository writer is a dedicated GitHub App installed only on
`homebrew-tap`. `env-vault`'s repository-scoped `GITHUB_TOKEN` is not used for
cross-repository writes. The workflow mints a short-lived installation token
inside the `homebrew` job and the action revokes it when that job finishes.

The steady-state release path has no personal access token, SSH deploy key, or
other long-lived cross-repository writer. In particular, neither repository
contains an active `TAP_DEPLOY_KEY` after the App-based cutover has succeeded.

## 1. GitHub App

Use a dedicated App such as `env-vault-tap-release`, owned by the account or
organization that owns the two repositories.

Required configuration:

- Webhooks: disabled; the workflow polls GitHub Actions and pull-request state.
- Installation: **Only select repositories**, with only `homebrew-tap`
  selected.
- Repository permissions:
  - **Actions: Read** — query `test-formula.yml` runs by event and head SHA.
  - **Contents: Read and write** — create the version branch and formula
    commit.
  - **Pull requests: Read and write** — create, inspect, and squash-merge the
    formula pull request.
  - **Metadata: Read** — GitHub grants this automatically.
- Organization/account permissions: none.
- Ruleset bypass: none. The App must satisfy the same tap checks as any other
  pull-request author.

Do not grant Administration, Environments, Secrets, Workflows, Issues,
Packages, Checks, or Actions write access. The implementation reads workflow
runs through the Actions API and does not rerun them, so neither Checks access
nor Actions write access is needed.

Record the App **client ID**, not its numeric App ID, and generate one private
key. Never print the PEM, an installation token, or a derived credential. Key
rotation is described below.

The `homebrew` job uses the current v3 interface explicitly:

```yaml
- id: tap-token
  uses: actions/create-github-app-token@v3
  with:
    client-id: ${{ vars.TAP_APP_CLIENT_ID }}
    private-key: ${{ secrets.TAP_APP_PRIVATE_KEY }}
    owner: ${{ github.repository_owner }}
    repositories: homebrew-tap
    permission-actions: read
    permission-contents: write
    permission-pull-requests: write
```

Keep the major action version and its inputs covered by workflow regression
tests. Review action release notes before changing the major version.

Verify the installation itself with `.github/workflows/audit-release-app.yml`.
That manual workflow intentionally omits `repositories` while requesting only
`permission-metadata: read`, lists the installation repositories without
logging them, and succeeds only when the list is exactly
`ildarbinanas-design/homebrew-tap`. The action revokes the audit token in its
post-step. A failed audit blocks release preparation.

## 2. `env-vault` release environment

Create an Actions environment named `release` in `env-vault` with exactly these
release values:

| Kind | Name | Value |
| --- | --- | --- |
| Variable | `TAP_APP_CLIENT_ID` | Client ID of the dedicated App |
| Secret | `TAP_APP_PRIVATE_KEY` | Complete PEM private key for that App |

The client ID is an identifier and belongs in an environment variable. The PEM
is a credential and belongs in an environment secret. Neither value belongs in
the repository, a branch, a workflow artifact, release evidence, or a job
summary.

Environment branch and tag policy:

- allow the current default branch, `main`, for dispatch releases and repairs;
- allow tags matching `v*` while tag-triggered releases remain supported; and
- rely on the workflow's strict `vMAJOR.MINOR.PATCH` validation rather than
  treating the environment glob as version validation.

The automated release path requires no wait timer and no required environment
reviewer. Adding either protection intentionally turns publication into an
approval-gated flow and every release or `homebrew` repair will wait at the
`homebrew` job before the App key becomes available.

Within `build-binaries.yml`, only this job declares the environment:

```yaml
homebrew:
  environment: release
```

The metadata, quality, build, Release, supply-chain, and `health` jobs must not
declare it. A build-only run never schedules `homebrew`, so it cannot access a
release credential. `repair=health` also skips `homebrew`; the read-only health
job uses `contents: read`, `attestations: read`, public tap state, and the
repository workflow token. The independent `audit-release-app.yml` workflow
also declares `release`, but it is manual-only and mints metadata-read rather
than write-capable permissions.

## 3. `homebrew-tap` branch and merge policy

Keep `main` as the default branch and enable squash merging. The release helper
waits for the exact pull-request workflow run first and then invokes a squash
merge guarded by `--match-head-commit`; repository auto-merge is not required.

Apply an active ruleset to `refs/heads/main` with these rules:

- changes must arrive through a pull request;
- required status check **`test`**, emitted by the `test` job in
  `.github/workflows/test-formula.yml`;
- conversations must be resolved;
- force pushes and branch deletion are blocked;
- zero required approvals, because the release-environment boundary and exact
  generated-content validation are the authorization controls; and
- no bypass entry for the release App.

Select the observed `test` check context from a real tap pull request rather
than typing a similar display name. Do not add an App signed-commit requirement
without separately designing and testing App commit signing.

Keep the required-status-check policy loose rather than requiring the PR branch
to contain the latest `main`. The release workflow verifies the exact PR head,
guards the squash merge with that SHA, and then tests the actual merged commit
through a separate exact-SHA push run. This preserves the stronger final-state
check without making an unrelated tap commit invalidate an already tested PR.

GitHub Actions must remain enabled for `homebrew-tap`, including runs for
pull-request and push events in `test-formula.yml`. The workflow token needs to
read those runs but cannot modify or rerun them.

## 4. Exact PR, merge, and CI contract

For version `vX.Y.Z`, the workflow uses the deterministic branch
`release/env-vault-vX.Y.Z` and a pull request titled `env-vault vX.Y.Z`. The
pull-request body includes a machine marker binding:

- the exact version;
- the env-vault release source SHA; and
- the SHA-256 digest of the generated formula.

The publication helper fails closed if the branch or pull request changes any
path other than `Formula/env-vault.rb`, if its formula differs from the
generated bytes, or if its metadata/marker does not match. It never force-pushes
or overwrites an existing version branch.

The release sequence is:

1. Generate the formula only from verified Release assets.
2. Create or reuse the deterministic version branch and pull request.
3. Wait up to 15 minutes for `test-formula.yml` with
   `event=pull_request`, `head_sha` equal to the exact PR head, and
   `conclusion=success`.
4. Re-read the PR, require the unchanged head SHA, and squash-merge it with
   `--match-head-commit`.
5. Resolve the actual merge/default-branch commit SHA.
6. Wait up to 15 minutes for `test-formula.yml` with `event=push`, that exact
   commit SHA, and `conclusion=success`.
7. Pass the PR URL, tap SHA, and exact push-run URL to `health`.

A timeout, cancellation, API error, malformed response, changed PR head, closed
PR, merge failure, or non-success workflow conclusion stops the release. A
successful check for a different SHA or event is never accepted.

If the tap default branch already contains the same version and byte-identical
formula, publication is an idempotent no-op. If the matching release PR exists,
it must already be merged and its merge commit must be an ancestor of the tap
default branch. The workflow still waits for a successful push run on the exact
current tap SHA. Same-version content or checksum differences are a hard
failure.

The final job summary contains links to:

- the GitHub Release;
- the immutable env-vault source SHA;
- the tap pull request when one exists;
- the exact tap commit;
- the successful `test-formula` push run for that commit;
- repository attestations; and
- the env-vault release workflow run.

## 5. Repair behavior

The release modes use the App and environment as follows:

| Mode | App credential | Tap behavior | Health behavior |
| --- | --- | --- | --- |
| `none` | `homebrew` only | create/reuse PR or exact no-op; wait PR and push CI | verify all release and tap evidence |
| `release-assets` | `homebrew` only | same as `none` after asset reconciliation | verify all release and tap evidence |
| `homebrew` | `homebrew` only | resume/reuse PR or exact no-op; wait exact CI | verify all release and tap evidence |
| `health` | none | no branch, PR, merge, or formula mutation | re-download assets, verify attestations/formula, and wait for exact tap push CI |

`repair=homebrew` is the recovery path after a branch, PR, merge, or tap-CI
failure. It accepts existing remote state only when every version, source SHA,
formula byte, marker, PR head, and merge relationship remains consistent.
`repair=health` is strictly read-only and is appropriate after publication is
already complete but the final evidence step failed.

## 6. App cutover and legacy credential removal

Use this order when establishing or rotating the release path:

1. Install the dedicated App only on `homebrew-tap` with the permissions above.
2. Add `TAP_APP_CLIENT_ID` and `TAP_APP_PRIVATE_KEY` to the `release`
   environment without exposing their values.
3. Run `audit-release-app.yml` and require its single-repository scope check to
   pass.
4. Apply the tap ruleset and merge settings.
5. Run the reviewed App-based path and prove token minting, deterministic PR
   creation/reuse, exact PR CI, guarded merge, exact push CI, and the final
   health summary.
6. After that cutover succeeds, remove the old SSH deploy key from
   `homebrew-tap` and delete the corresponding `TAP_DEPLOY_KEY` secret from
   `env-vault`.
7. Confirm that no workflow, repository variable, environment, or runbook still
   references `TAP_DEPLOY_KEY` and that only the App installation can perform
   automated tap writes.

Do not retain both write paths after the successful cutover. Do not remove the
legacy key before the first App-based proof, because that converts a controlled
migration into a release outage.

## 7. Dependency review settings

For `env-vault`, enable the dependency graph and require the exact observed
**`Dependency review`** check on pull requests to the default branch. Observe
the real check context from `.github/workflows/dependency-review.yml` before
adding it to a ruleset. Ordinary and Dependabot pull requests must both be
blocked when the check fails or is missing.

Keep the workflow's default token at `contents: read`; grant a broader
permission only if a documented dependency-review feature requires it.

## 8. Credential rotation and rollback

Rotate an App private key without running a release:

1. Generate a second key for the same App.
2. Replace `TAP_APP_PRIVATE_KEY` in the `release` environment without printing
   either key.
3. Dispatch `audit-release-app.yml` and require its metadata-only scope audit
   to pass.
4. Delete the old App key immediately after the new key succeeds.

If the App key may be compromised, revoke it first, pause releases, install a
new key in the environment, and audit App installation and repository events
before resuming.

Rollback is operational, not destructive:

- Before merge, leave the deterministic PR and branch intact, fix the external
  setting or tap check, and run `repair=homebrew`.
- After merge but before a successful push check, fix or rerun tap CI for that
  exact merged SHA, then use `repair=homebrew` or the read-only
  `repair=health`.
- If App authentication or permissions fail, pause publication and correct the
  App installation, environment values, or permissions. A maintainer-authored
  formula PR that satisfies the same ruleset can restore distribution while
  the App is repaired; finish with `repair=health`.
- Revert a faulty workflow change through the normal `env-vault` pull-request
  process. Do not move a tag, overwrite Release assets, lower the Homebrew
  version, bypass the tap ruleset, or weaken checks to make a run green.

Reintroducing a deploy key is not the normal rollback. If an exceptional
incident requires a temporary legacy credential, record its owner and expiry,
scope it to `homebrew-tap`, route changes through a reviewed pull request, and
remove it as soon as the App path is restored. Never keep both automated write
credentials active as a steady state.
