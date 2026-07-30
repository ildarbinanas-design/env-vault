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
credentials are stored only in `release`. The planning token holds
Administration read so the pre-tag check can verify merge settings, ruleset
structure, and that the release path itself has no bypass.

The operational pre-tag check is authoritative for the complete global bypass
lists: it reads each canonical ruleset's GraphQL `bypassActors.totalCount`,
requires all three counts to be zero, and seals that response together with the
exact REST rule details for offline health replay. Missing GraphQL
data, partial errors, pagination, or a nonzero count fails closed.

That check reads repository merge settings and actor-independent ruleset bypass
counts through GitHub GraphQL `Repository` fields. GitHub's REST responses omit
parts of that policy for this deliberately permission-bounded token, while
GraphQL exposes the required read-only state without granting Administration
write. Missing GraphQL data or an API error fails the check closed.

## Trust boundary

Two dedicated fine-grained personal access tokens separate planning from
cross-repository publication. Each lives only in the Actions environment of the
job that needs it:

- `RELEASE_PLANNING_TOKEN` is scoped to `env-vault` only. It may create and
  update the Release Please pull request and perform the classified exact-tag
  handoff after green CI. The Release Please action is configured PR-only and
  cannot create a tag or GitHub Release itself.
- `HOMEBREW_TAP_TOKEN` is scoped to `homebrew-tap` only. It may create, verify,
  and merge the deterministic Homebrew formula pull request.

The repository-scoped `GITHUB_TOKEN` is not used to author the Release Please
pull request because events created by that token do not trigger the protected
pull-request workflows — the generated release PR would never collect its
required checks and could never be merged. It is not used for cross-repository
writes either. The workflow uses it only for read-only Contents, Pull requests,
Issues, and Actions authorization checks.

Release automation deliberately no longer uses GitHub Apps. The two retired
Apps (`env-vault-release-planning`, `env-vault-tap-release`) added an
installation and key-rotation lifecycle, plus two audit workflows, without
providing a capability a repository-scoped token cannot. The trade-off is
explicit: a fine-grained PAT is longer-lived than an installation token, so it
is scoped to a single repository, kept in an environment secret, and given an
expiry that the operator rotates on schedule (section 10).

There is no SSH deploy key or other long-lived cross-repository writer. In
particular, neither repository contains an active `TAP_DEPLOY_KEY`.

## 1. Release-planning token and environment

Create a fine-grained personal access token with this exact scope:

- Resource owner: the account that owns both repositories.
- Repository access: **Only select repositories**, with only `env-vault`
  selected.
- Repository permissions:
  - **Contents: Read and write** — create and update the release-planning
    branch and commit, and create the exact authorized tag;
  - **Pull requests: Read and write** — create, update, inspect, and label the
    generated release pull request;
  - **Issues: Read and write** — maintain Release Please pull-request labels;
  - **Administration: Read** — verify repository merge settings, ruleset
    structure, and that the release path cannot bypass any release ruleset;
  - **Metadata: Read** — GitHub grants this automatically.
- Account permissions: none.
- Expiry: set an explicit expiry and rotate per section 10.

Do not grant Actions, Administration write, Environments, Secrets, Workflows,
Packages, Checks, or Deployments permissions. The token's contents permission is
used by the workflow only for the exact tag authorized by a deterministic
release pull-request merge and its successful `ci` run. GitHub does not offer
separate tag-write and Release-write permissions: `Contents: write` technically
covers both. Therefore action SHA pins, exact-path tests, the tag/ruleset gates,
and code review enforce that this workflow never moves or deletes a tag, calls
the GitHub Release or asset APIs, approves or merges its own pull request, or
bypasses branch protection. The exact tag push starts `build-binaries`.

The token must not be granted any ruleset bypass. The generated release pull
request must satisfy the same checks and merge policy as every other change.

Create an Actions environment named `release-planning` with exactly this value:

| Kind | Name | Value |
| --- | --- | --- |
| Secret | `RELEASE_PLANNING_TOKEN` | Fine-grained PAT scoped to `env-vault` |

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
`autorelease: pending` and `autorelease: tagged`. Before opening the first
proposal, the planning workflow idempotently creates or normalizes those
repository labels and verifies their exact names, colors, and descriptions. The
planning job is the only operational job that declares
`environment: release-planning`. Its repository workflow token remains
read-only and performs authorization reads. The environment token performs the
configured pull-request writes, exact-tag handoff, and lifecycle label
reconciliation. The workflow contains no public Release or asset API call, even
though the coarse GitHub contents permission cannot exclude that capability
from the credential itself.

## 2. Homebrew tap token

Create a second fine-grained personal access token, owned by the account or
organization that owns the two repositories.

Required configuration:

- Repository access: **Only select repositories**, with only `homebrew-tap`
  selected.
- Repository permissions:
  - **Actions: Read** — query `test-formula.yml` runs by event and head SHA.
  - **Contents: Read and write** — create the version branch and formula
    commit.
  - **Pull requests: Read and write** — create, inspect, and squash-merge the
    formula pull request.
  - **Metadata: Read** — GitHub grants this automatically.
- Account permissions: none.
- Expiry: set an explicit expiry and rotate per section 10.

Do not grant Administration, Environments, Secrets, Workflows, Issues,
Packages, Checks, or Actions write access. The implementation reads workflow
runs through the Actions API and does not rerun them, so neither Checks access
nor Actions write access is needed.

The token must not be granted any ruleset bypass: it must satisfy the same tap
checks as any other pull-request author.

Never print the token or a derived credential. The `homebrew` job consumes it
straight from the `release` environment:

```yaml
- name: Create or reuse deterministic Homebrew pull request
  env:
    GH_TOKEN: ${{ secrets.HOMEBREW_TAP_TOKEN }}
```

Because the token is a repository-scoped secret rather than a minted
installation token, there is no token-minting action to pin and no audit
workflow to dispatch. Workflow regression tests assert that no workflow
reintroduces `create-github-app-token`, an App client ID, or an App private key.

## 3. `env-vault` release environment

Create an Actions environment named `release` in `env-vault` with exactly these
release values:

| Kind | Name | Value |
| --- | --- | --- |
| Secret | `HOMEBREW_TAP_TOKEN` | Fine-grained PAT scoped to `homebrew-tap` |

The token is a credential and belongs in an environment secret. It does not
belong in the repository, a branch, a workflow artifact, or a
job summary.

Environment branch and tag policy:

- allow the current default branch, `main`, for the protected-main Homebrew
  publication bridge;
- allow tags matching `v*` for automatic publication and exact-tag repairs; and
- rely on the workflow's strict `vMAJOR.MINOR.PATCH` validation rather than
  treating the environment glob as version validation.

The automated tap-publication path requires no wait timer and no required
environment reviewer. Adding either protection intentionally adds a second
approval gate after the release pull request has already authorized
publication, and every release or `homebrew` repair will wait at the `homebrew`
job before the environment token becomes available.

Within `build-binaries.yml`, only this job declares the environment:

```yaml
homebrew:
  environment: release
```

The `metadata`, `preflight`, `promotion`, `release`, `supply_chain`, and
`health` jobs must not declare it. The publisher has no build-only mode and no
product build job. `repair=health` skips `homebrew`; the read-only health job
uses `contents: read`, `attestations: read`, public tap state, and the repository
workflow token. The environment-backed publication job also declares
`release`, but it is manual-only and mints metadata-read rather than
write-capable permissions.

## 3b. Verify both tokens before the first release

The retired App audit workflows proved single-repository installation scope on
demand. Fine-grained tokens carry their scope in the token itself, so the
equivalent proof is a one-time local check made when the token is created or
rotated. Every probe below is a read; none mutates anything.

Never pass a token as a command-line argument — it would land in shell history
and in the process table. Read it into a variable with a hidden prompt and pass
it to `gh` through the environment, with shell tracing off:

```bash
set +x
IFS= read -rs TOKEN            # paste the token; input stays hidden
export GH_TOKEN="$TOKEN"
```

Release-planning token — all four must hold:

```bash
gh api repos/ildarbinanas-design/env-vault --jq .full_name          # the repository is reachable
gh api repos/ildarbinanas-design/env-vault/rulesets --jq length     # 3 — proves Administration: Read
gh api repos/ildarbinanas-design/env-vault --jq .permissions.push   # true — proves Contents write
gh api 'repos/ildarbinanas-design/env-vault/pulls?per_page=1' >/dev/null   # pull requests readable
```

The ruleset probe is the important one: it is the capability `GITHUB_TOKEN`
cannot be granted at all, and `verify-repository-release-settings.sh` depends
on it.

Single-repository scope is **not** checkable this way. Both repositories are
public, so a fine-grained token can always read the other one; and
`.permissions.push` reports the caller's owner role rather than the token's
grant, so it shows write access even for a correctly scoped token. Verify the
selected repositories on the token's own settings page
(<https://github.com/settings/personal-access-tokens>), which states them
authoritatively — that page is the replacement for the retired
installation-scope audit.

Homebrew tap token — all four must hold:

```bash
gh api repos/ildarbinanas-design/homebrew-tap --jq .full_name
gh api repos/ildarbinanas-design/homebrew-tap/actions/workflows --jq '.workflows[].path'
                                                # includes .github/workflows/test-formula.yml
gh api repos/ildarbinanas-design/homebrew-tap --jq .permissions.push       # true
gh api 'repos/ildarbinanas-design/homebrew-tap/pulls?per_page=1' >/dev/null
```

The workflow probe doubles as a contract cross-check: the observed path must
match `homebrew.tap_ci_workflow_file` in `release/contract.v2.json`.

Then clear the variable: `unset GH_TOKEN TOKEN`.

Pull-requests and Issues *write* cannot be proven without mutating something.
They are first exercised by the next real planning or tap publication run, so
keep the previous credential in place until that run is green.

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
- no release token nor any workflow identity has a bypass.

Apply a second active tag ruleset named `Protect env-vault release tags` to
`refs/tags/v*`. It must restrict both tag updates and tag deletion, have no
bypass actor, and leave creation of a new version tag allowed. This lets the
planning token create one new exact tag but prevents any actor from moving or
deleting a published version through the normal repository path.

Apply a third active branch ruleset named
`Protect env-vault release evidence` to the exact ref
`refs/heads/release-evidence`. It must block non-fast-forward updates and
deletion and have no bypass actor. Nothing writes to this branch any more: the
evidence ledger was retired on 2026-07-30 (Phase 3 of
[`trim-plan-2026-07-30.md`](trim-plan-2026-07-30.md)), and the ruleset now
exists solely to keep that frozen history from being rewritten or removed.
Evidence links in historical documents use exact commit SHAs and must stay
resolvable.

Never rewrite the ref, move its root, or resurrect the ledger. On 2026-07-17,
read-only inspection observed ruleset ID `19058721` active on the exact ref,
with deletion and non-fast-forward protection, `bypass_actors=[]`, and
`current_user_can_bypass=never`; the ref tip was
`af521d52b898088cb49f6256964e377e33e95a5d`. Those values are dated audit
evidence, not workflow inputs or operational constants. Re-read and validate
current settings and ref identity for every release.

Observe each real check context from a pull request before adding it to the
ruleset. Do not guess from a display label. The dedicated lightweight
`pr-title` workflow accepts the forms documented in `CONTRIBUTING.md` and
reruns when pull-request metadata changes. The full cross-platform `ci`
workflow does not run for metadata-only edits; its independent `quality-gate`
remains bound to the last code-bearing pull-request head.

The Release Please pull request is not auto-merged. Opening, updating,
approving, or closing it does not authorize publication — **merging** it does.
Re-read the version, PR number, and full head SHA immediately before the squash
merge and pass that head SHA to `--match-head-commit`, so a head that moved
during review can never be published silently. The byte-exact confirmation
comment and its wrapper were removed on 2026-07-30; see `RELEASING.md`. The
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
release-planning token. Its coarse PR/contents permissions could technically
merge a green PR, so the pinned workflow and its contract tests enforce that
the workflow never calls a merge endpoint; only the maintainer squash merge is an
accepted publication authorization.

The planning workflow uses Administration-read
access to verify repository merge settings and the exact active
main/tag/evidence rulesets. GitHub deliberately omits REST `bypass_actors` from
a caller that cannot edit rulesets, so the same read-only token obtains the
complete actor-independent zero-bypass decision from GraphQL
`RepositoryRuleset.bypassActors`. REST still supplies the exact rule structure
and must report that the planning token itself can never bypass; an unexpectedly
present REST bypass list is accepted only when it is empty. The offline checker
preserves the exact raw GraphQL and REST responses and digests, then seals them
to the source/version/planning-run tuple. The publisher's read-only health job
downloads that attempt-qualified proof and replays it offline instead of
querying Administration APIs. No out-of-band read can replace the sealed
pre-tag proof. Repository merge settings and global bypass counts are queried
through GraphQL, preserving the read-only permission boundary. Correct the
repository if the automated check reports rebase merging,
`COMMIT_OR_PR_TITLE`, `COMMIT_MESSAGES`, a non-squash ruleset merge method, a
missing strict check, weakened branch protection, a missing immutable `v*` tag
ruleset, or a mutable/deletable frozen evidence branch. The workflow does not weaken
the contract to accommodate unsafe settings.

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
- no bypass entry for the release token.

Select the observed `test` check context from a real tap pull request rather
than typing a similar display name. Do not add a signed-commit requirement
without separately designing and testing commit signing for the release path.

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

The release modes use the tap token and environment as follows:

| Mode | Tap credential | Tap behavior | Health behavior |
| --- | --- | --- | --- |
| `release-assets` | `homebrew` only | create/reuse PR or exact no-op after asset reconciliation; wait exact PR and push CI | verify all live release and tap state |
| `homebrew` | `homebrew` only | resume/reuse PR or exact no-op; wait exact CI | verify all live release and tap state |
| `health` | none | no branch, PR, merge, or formula mutation | re-download assets, verify attestations/formula, and wait for exact tap push CI |

The automatic tag event uses the internal normal mode; it is not selectable by
manual dispatch. Every manual repair must run from the exact immutable tag ref.
None of the repair modes rebuilds product artifacts.

`repair=homebrew` is the recovery path after a branch, PR, merge, or tap-CI
failure. It accepts existing remote state only when every version, source SHA,
formula byte, marker, PR head, and merge relationship remains consistent.
`repair=health` is strictly read-only and is appropriate after publication is
already complete but the final verification step failed.

## 8. Tap credential cutover and legacy credential removal

Use this order when establishing or rotating the tap write path:

1. Create the fine-grained tap PAT scoped only to `homebrew-tap` with the
   permissions above.
2. Add `HOMEBREW_TAP_TOKEN` to the `release` environment without exposing its
   value.
3. Apply the tap ruleset and merge settings.
4. Run the reviewed path and prove deterministic PR creation/reuse, exact PR CI,
   guarded merge, exact push CI, and the final health summary.
5. After that cutover succeeds, remove any older write path from `homebrew-tap`
   — the retired `env-vault-tap-release` App installation and, if still present,
   the SSH deploy key and its `TAP_DEPLOY_KEY` secret in `env-vault`.
6. Confirm that no workflow, repository variable, environment, or runbook still
   references `TAP_DEPLOY_KEY`, `TAP_APP_CLIENT_ID`, or `TAP_APP_PRIVATE_KEY`,
   and that only `HOMEBREW_TAP_TOKEN` can perform automated tap writes.

Do not retain two write paths after a successful cutover. Do not remove the
previous credential before the first proof on the new one, because that converts
a controlled migration into a release outage.

## 9. Dependency review settings

For `env-vault`, enable the dependency graph and require the exact observed
**`Dependency review`** check on pull requests to the default branch. Observe
the real check context from `.github/workflows/dependency-review.yml` before
adding it to a ruleset. Ordinary and Dependabot pull requests must both be
blocked when the check fails or is missing.

Keep the workflow's default token at `contents: read`; grant a broader
permission only if a documented dependency-review feature requires it.

## 10. Credential rotation and rollback

Both release tokens have an explicit expiry. Rotate each on schedule, and
immediately if it may be compromised. Rotation does not require preparing or
publishing a release.

Rotate the release-planning token:

1. Create a replacement fine-grained PAT with the identical `env-vault`-only
   scope from section 1.
2. Replace `RELEASE_PLANNING_TOKEN` in the `release-planning` environment
   without printing either value.
3. Verify the next planning run reaches its authorization reads and repository
   release-settings verification. The next tag handoff remains blocked until the
   operational pre-tag checker observes and seals empty global bypass lists for
   all three rulesets.
4. Delete the old token immediately after the new one succeeds.

Rotate the tap token:

1. Create a replacement fine-grained PAT with the identical `homebrew-tap`-only
   scope from section 2.
2. Replace `HOMEBREW_TAP_TOKEN` in the `release` environment without printing
   either value.
3. Delete the old token immediately after the new one succeeds.

If a token may be compromised, revoke it first, pause releases, install the
replacement in the environment, and audit repository and Actions events before
resuming.

Rollback is operational, not destructive:

- Before a release pull request is merged, preserve it, fix the planning token,
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
- If token authentication or permissions fail, pause publication and correct the
  token scope or environment value. A maintainer-authored
  formula PR that satisfies the same ruleset can restore distribution while
  the token is repaired; finish with `repair=health`.
- Revert a faulty workflow change through the normal `env-vault` pull-request
  process. Do not move a tag, overwrite Release assets, lower the Homebrew
  version, bypass the tap ruleset, or weaken checks to make a run green.

Reintroducing a deploy key is not the normal rollback. If an exceptional
incident requires a temporary legacy credential, record its owner and expiry,
scope it to `homebrew-tap`, route changes through a reviewed pull request, and
remove it as soon as the token path is restored. Never keep both automated write
credentials active as a steady state.
