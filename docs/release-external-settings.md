# External settings for automated releases

This document is the external configuration contract for release planning and
publication in `ildarbinanas-design/env-vault`. Release Please v5 prepares
version documentation and `CHANGELOG.md` through a protected pull request in
`env-vault`. After that exact release merge passes `ci` as a push to `main`,
the release-planning workflow downloads the five native artifacts and promotion
manifest from that one exact CI attempt, verifies them offline, and only then
creates or verifies the exact tag at the green SHA. That tag starts the
seven-job `build-binaries` publisher, which promotes those bytes without a
rebuild and alone creates the public GitHub Release and assets. It then
publishes the Homebrew formula through a protected pull request in
`ildarbinanas-design/homebrew-tap` and proves both the pull-request check and
the post-merge default-branch check for exact commit SHAs.

Repository settings must match this document before a release pull request is
merged. Planning credentials are stored only in `release-planning`; tap
credentials are stored only in `release`. The tap audit requests metadata read;
the planning audit additionally requests Administration read so it can verify
merge settings, ruleset structure, and that the planning App itself has no
bypass. The operational pre-tag check is authoritative for the complete global
bypass lists: the read-only App queries each canonical ruleset's GraphQL
`bypassActors.totalCount`, requires all three counts to be zero, and seals that
response together with the exact REST rule details for offline health/evidence
replay. Missing GraphQL data, partial errors, pagination, or a nonzero count
fails closed.

The planning audit reads repository merge settings and actor-independent
ruleset bypass counts through GitHub GraphQL `Repository` fields. GitHub's REST
responses omit parts of that policy for this deliberately permission-bounded
token, while GraphQL exposes the required read-only state without granting
Administration write. Missing GraphQL data or an API error fails the audit
closed.

## Trust boundary

Two dedicated GitHub Apps separate planning from cross-repository publication:

- `env-vault-release-planning` is installed only on `env-vault`. Its token may
  create and update the Release Please pull request and perform the classified
  exact-tag handoff after green CI. The Release Please action is configured
  PR-only and cannot create a tag or GitHub Release itself.
- `env-vault-tap-release` is installed only on `homebrew-tap`. Its token may
  create, verify, and merge the deterministic Homebrew formula pull request.

The repository-scoped `GITHUB_TOKEN` is not used to author the Release Please
pull request because events created by that token do not trigger the protected
pull-request workflows. It is not used for cross-repository writes. The
workflow uses it only for read-only Contents, Pull requests, Issues, and
Actions authorization checks. It mints each short-lived installation token
only inside the job that needs it, and the token action revokes it when that
job finishes.

The steady-state release path has no personal access token, SSH deploy key, or
other long-lived cross-repository writer. In particular, neither repository
contains an active `TAP_DEPLOY_KEY` after the App-based cutover has succeeded.

## 1. Release-planning App and environment

Create a dedicated App named `env-vault-release-planning` with this exact
scope:

- Webhooks: disabled.
- Installation: **Only select repositories**, with only `env-vault` selected.
- Repository permissions:
  - **Contents: Read and write** — create and update the release-planning
    branch and commit;
  - **Pull requests: Read and write** — create, update, inspect, and label the
    generated release pull request;
  - **Issues: Read and write** — maintain Release Please pull-request labels;
  - **Administration: Read** — verify repository merge settings, ruleset
    structure, and that this App itself cannot bypass any release ruleset;
  - **Metadata: Read** — GitHub grants this automatically.
- Organization/account permissions: none.
- Ruleset bypass: none. The generated release pull request must satisfy the
  same checks and merge policy as every other change.

Do not grant Actions, Administration write, Environments, Secrets, Workflows,
Packages, Checks, or Deployments permissions. The planning App's contents
permission is used by the workflow only for the exact tag authorized by a
deterministic release pull-request merge and its successful `ci` run. GitHub
does not offer separate tag-write and Release-write permissions: `Contents:
write` technically covers both. Therefore action SHA pins, exact-path tests,
the tag/ruleset gates, and code review enforce that this workflow never moves
or deletes a tag, calls the GitHub Release or asset APIs, approves or merges its
own pull request, or bypasses branch protection. The exact tag push, rather
than an App workflow dispatch, starts `build-binaries`.

Create an Actions environment named `release-planning` with exactly these
values:

| Kind | Name | Value |
| --- | --- | --- |
| Variable | `RELEASE_APP_CLIENT_ID` | Client ID of `env-vault-release-planning` |
| Secret | `RELEASE_APP_PRIVATE_KEY` | Complete PEM private key for that App |

Allow only the protected default branch `main`. Do not allow tags. A wait timer
or required environment reviewer is not needed because the exact owner/member
confirmation comment on the reviewed release pull request is the explicit
authorization; the unchanged PR must then be merged and the exact merge SHA
must pass `ci` before the environment-backed workflow can create the tag.

The `release-please` workflow uses Release Please v5, pinned to the verified
`v5.0.0` commit `45996ed1f6d02564a971a2fa1b5860e934307cf7`, in
manifest PR-only mode. Its checked-in extra-files contract updates the single
`<!-- x-release-please-version -->` version line in `README.md` together with
the manifest and generated `CHANGELOG.md` section. The checked-in title and
footer produce the exact branch, title, and body evidence used by the
authorization gate. The body header explicitly warns that merging authorizes
publication after green `main` CI; the lifecycle labels remain
`autorelease: pending` and `autorelease: tagged`. Before opening the first proposal, the planning workflow
idempotently creates or normalizes those repository labels and verifies their
exact names, colors, and descriptions. The planning job is the only operational
job that declares `environment: release-planning`; the separately dispatched
read-only scope/settings audit is the documented exception. Its repository
workflow token remains read-only and performs authorization reads. The App token
performs the configured pull-request writes, exact-tag handoff, and lifecycle
label reconciliation. The workflow contains no public Release or asset API
call, even though the coarse GitHub contents permission cannot exclude that
capability from the credential itself.

Verify the App installation with
`.github/workflows/audit-release-planning-app.yml`. That manual workflow mints
a metadata-plus-Administration-read token, succeeds only when the installation
contains exactly `ildarbinanas-design/env-vault` and the App cannot bypass the
main, immutable-tag, or append-only-evidence rulesets, all three global bypass
actor counts are zero, and relies on post-step revocation. It verifies
squash-only merge policy and bypass counts through GraphQL so the audit token
does not need ruleset-write capability. A
failed audit blocks release planning.

## 2. Homebrew tap GitHub App

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

The `homebrew` job uses the current v3 interface pinned to the verified
`v3.2.0` commit SHA:

```yaml
- id: tap-token
  uses: actions/create-github-app-token@bcd2ba49218906704ab6c1aa796996da409d3eb1 # v3.2.0
  with:
    client-id: ${{ vars.TAP_APP_CLIENT_ID }}
    private-key: ${{ secrets.TAP_APP_PRIVATE_KEY }}
    owner: ${{ github.repository_owner }}
    repositories: homebrew-tap
    permission-actions: read
    permission-contents: write
    permission-pull-requests: write
```

Keep the exact privileged-action SHA and its inputs covered by workflow
regression tests. Verify the upstream tag and commit signature, then review
release notes before changing the pin.

Verify the installation itself with `.github/workflows/audit-release-app.yml`.
That manual workflow intentionally omits `repositories` while requesting only
`permission-metadata: read`, lists the installation repositories without
logging them, and succeeds only when the list is exactly
`ildarbinanas-design/homebrew-tap`. The action revokes the audit token in its
post-step. A failed audit blocks release preparation.

## 3. `env-vault` release environment

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

- allow the current default branch, `main`, for the manual read-only App audit;
- allow tags matching `v*` for automatic publication and exact-tag repairs; and
- rely on the workflow's strict `vMAJOR.MINOR.PATCH` validation rather than
  treating the environment glob as version validation.

The automated tap-publication path requires no wait timer and no required
environment reviewer. Adding either protection intentionally adds a second
approval gate after the release pull request has already authorized
publication, and every release or `homebrew` repair will wait at the `homebrew`
job before the App key becomes available.

Within `build-binaries.yml`, only this job declares the environment:

```yaml
homebrew:
  environment: release
```

The `metadata`, `preflight`, `promotion`, `release`, `supply_chain`, and
`health` jobs must not declare it. The publisher has no build-only mode and no
product build job. `repair=health` skips `homebrew`; the read-only health job
uses `contents: read`, `attestations: read`, public tap state, and the repository
workflow token. The independent `audit-release-app.yml` workflow also declares
`release`, but it is manual-only and mints metadata-read rather than
write-capable permissions.

## 4. `env-vault` branch and merge policy

Keep `main` as the default branch. Enable squash merging, disable merge commits
and rebase merging, configure the squash commit title to use the pull request
title (`PR_TITLE`), and configure the squash commit message to use the pull
request body (`PR_BODY`). This makes the reviewed Conventional Commit title and
its `BREAKING CHANGE:` footer the exact commit consumed by Release Please.

Apply an active ruleset to `refs/heads/main` with these rules:

- changes must arrive through a pull request;
- required status checks include the exact observed contexts for
  `quality-gate`, `pr-title`, dependency review, CodeQL Go, and CodeQL Actions;
- required checks are strict so the head must include the current `main`;
- conversations must be resolved;
- only squash merge is allowed;
- force pushes and branch deletion are blocked; and
- neither release App nor any workflow identity has a bypass.

Apply a second active tag ruleset named `Protect env-vault release tags` to
`refs/tags/v*`. It must restrict both tag updates and tag deletion, have no
bypass actor, and leave creation of a new version tag allowed. This lets the
planning App create one new exact tag but prevents any actor from moving or
deleting a published version through the normal repository path.

Apply a third active branch ruleset named
`Protect env-vault release evidence` to the exact ref
`refs/heads/release-evidence`. It must block non-fast-forward updates and
deletion, have no bypass actor, and allow initial branch creation plus ordinary
fast-forward commits. Evidence links use exact commit SHAs; this ruleset also
prevents the durable evidence history from being rewritten or removed.

Before the first durable publication only, create that ref without force at
the exact source SHA of the release whose evidence will initialize the branch.
The operator must prove the exact absence, push, and resulting remote equality;
the evidence workflow requires the pre-created ref and thereafter writes only
fast-forward evidence commits. Do not replace `GITHUB_TOKEN` with a
workflows-capable App/PAT or broaden either release App: creating a new ref
directly at a tree that contains `.github/workflows` needs that broader
permission, while updating the pre-created ref with evidence-only changes needs
only `Contents: write`.

Observe each real check context from a pull request before adding it to the
ruleset. Do not guess from a display label. The dedicated lightweight
`pr-title` workflow accepts the forms documented in `CONTRIBUTING.md` and
reruns when pull-request metadata changes. The full cross-platform `ci`
workflow does not run for metadata-only edits; its independent `quality-gate`
remains bound to the last code-bearing pull-request head.

The Release Please pull request is not auto-merged. Opening, updating,
approving, or closing it does not authorize publication. The only human
checkpoint combines semantic review with the exact tuple authorization
documented in `RELEASING.md`; the version, PR number, and full head SHA must be
re-read immediately before the squash merge and recorded as that exact
owner/member PR comment. The comment must be created and last updated before
the merge. The checked-in `authorize-and-merge-release-pr.sh` wrapper binds and
offline-validates the exact base contract, verifies its required-check
identities, and performs the comment write, GitHub-second settling, state
rechecks, and head-guarded merge as one resumable deterministic operator
action; separate ad-hoc comment and merge commands are not the normal path. The
merge commit must then pass `ci` on `main`; only a successful
push run from this repository for that exact SHA, with a complete
single-attempt promotion manifest and ten matching artifacts, may create the
tag.

Release Please v5 must remain PR-only. The surrounding planning workflow may
create only the classified exact tag after green `ci` and generated-PR
provenance checks, then must replace `autorelease: pending` with
`autorelease: tagged`. That tag starts `build-binaries`, whose tag entry point
repeats the authorization and promotion checks before acting as the sole
public GitHub Release and asset publisher. It must promote the CI-verified
artifacts rather than rebuild them; its preflight and monotonicity gates must
pass before the Release is created. Configure no ruleset bypass for the
release-planning App. Its coarse PR/contents permissions could technically
merge a green PR, so the pinned workflow and its contract tests enforce that
the App never calls a merge endpoint; only the maintainer squash merge is an
accepted publication authorization.

The planning workflow and the manual planning-App audit use Administration-read
access to verify repository merge settings and the exact active
main/tag/evidence rulesets. GitHub deliberately omits REST `bypass_actors` from
a caller that cannot edit rulesets, so the same read-only token obtains the
complete actor-independent zero-bypass decision from GraphQL
`RepositoryRuleset.bypassActors`. REST still supplies the exact rule structure
and must report that the planning App itself can never bypass; an unexpectedly
present REST bypass list is accepted only when it is empty. The offline checker
preserves the exact raw GraphQL and REST responses and digests, then seals them
to the source/version/planning-run tuple. The publisher's read-only health job
downloads that attempt-qualified proof and replays it offline instead of
querying Administration APIs. The separately dispatched read-only App audit
cannot replace the sealed pre-tag proof. Repository merge settings and global
bypass counts are queried through GraphQL, preserving the manual audit's
read-only permission boundary. Correct the
repository if the automated check
reports rebase merging, `COMMIT_OR_PR_TITLE`, `COMMIT_MESSAGES`, a non-squash
ruleset merge method, a missing strict check, weakened branch protection, or a
missing immutable `v*` tag ruleset, or a mutable/deletable evidence branch. The workflow does not weaken the contract
to accommodate unsafe settings.

## 5. `homebrew-tap` branch and merge policy

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

## 6. Exact Homebrew PR, merge, and CI contract

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
5. Resolve the exact release merge commit SHA and independently snapshot the
   current default-branch SHA.
6. Wait up to 15 minutes for `test-formula.yml` with `event=push`, that exact
   release merge SHA, and `conclusion=success`.
7. Pass the PR URL, exact merge SHA, current tap SHA, and exact push-run URL to
   `health`.

A timeout, cancellation, API error, malformed response, changed PR head, closed
PR, merge failure, or non-success workflow conclusion stops the release. A
successful check for a different SHA or event is never accepted.

If the tap default branch already contains the same version and byte-identical
formula, publication is an idempotent no-op. If the matching release PR exists,
it must already be merged and its merge commit must be an ancestor of the tap
default branch. The workflow still waits for the successful push run on that
exact release merge SHA, while separately requiring the current tap formula to
remain byte-identical and the current tap SHA to descend from the merge. Later
unrelated tap commits therefore do not invalidate already completed release
CI. Same-version content or checksum differences are a hard failure.

The final job summary contains links to:

- the GitHub Release;
- the immutable env-vault source SHA;
- the tap pull request when one exists;
- the exact release merge and current tap commits;
- the successful `test-formula` push run for the release merge commit;
- repository attestations; and
- the env-vault release workflow run.

## 7. Repair behavior

The release modes use the App and environment as follows:

| Mode | App credential | Tap behavior | Health behavior |
| --- | --- | --- | --- |
| `release-assets` | `homebrew` only | create/reuse PR or exact no-op after asset reconciliation; wait exact PR and push CI | verify all release and tap evidence |
| `homebrew` | `homebrew` only | resume/reuse PR or exact no-op; wait exact CI | verify all release and tap evidence |
| `health` | none | no branch, PR, merge, or formula mutation | re-download assets, verify attestations/formula, and wait for exact tap push CI |

The automatic tag event uses the internal normal mode; it is not selectable by
manual dispatch. Every manual repair must run from the exact immutable tag ref.
None of the repair modes rebuilds product artifacts.

`repair=homebrew` is the recovery path after a branch, PR, merge, or tap-CI
failure. It accepts existing remote state only when every version, source SHA,
formula byte, marker, PR head, and merge relationship remains consistent.
`repair=health` is strictly read-only and is appropriate after publication is
already complete but the final evidence step failed.

## 8. Homebrew App cutover and legacy credential removal

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

## 9. Dependency review settings

For `env-vault`, enable the dependency graph and require the exact observed
**`Dependency review`** check on pull requests to the default branch. Observe
the real check context from `.github/workflows/dependency-review.yml` before
adding it to a ruleset. Ordinary and Dependabot pull requests must both be
blocked when the check fails or is missing.

Keep the workflow's default token at `contents: read`; grant a broader
permission only if a documented dependency-review feature requires it.

## 10. Credential rotation and rollback

Rotate the planning App private key without preparing or publishing a release:

1. Generate a second key for `env-vault-release-planning`.
2. Replace `RELEASE_APP_PRIVATE_KEY` in `release-planning` without printing
   either key.
3. Dispatch `audit-release-planning-app.yml` and require its read-only
   single-`env-vault` scope, repository-setting, and App-no-bypass audit to
   pass. The next tag handoff remains blocked until the operational pre-tag
   checker observes and seals empty global bypass lists for all three rulesets.
4. Delete the old planning App key immediately after the new key succeeds.

Rotate the tap App private key without running a release:

1. Generate a second key for `env-vault-tap-release`.
2. Replace `TAP_APP_PRIVATE_KEY` in the `release` environment without printing
   either key.
3. Dispatch `audit-release-app.yml` and require its metadata-only
   single-`homebrew-tap` scope audit to pass.
4. Delete the old tap App key immediately after the new key succeeds.

If the App key may be compromised, revoke it first, pause releases, install a
new key in the environment, and audit App installation and repository events
before resuming.

Rollback is operational, not destructive:

- Before a release pull request is merged, preserve it, fix the planning App,
  workflow, Conventional title, version documentation, or CI failure, and let
  Release Please update the same proposal. Do not publish it manually merely
  to bypass the planning failure.
- After the release pull request is merged but before the exact `main` CI run
  succeeds, fix forward through a normal pull request. A failed or cancelled
  run must not create the tag.
- Before merge, leave the deterministic PR and branch intact, fix the external
  Homebrew setting or tap check, and run `repair=homebrew`.
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
