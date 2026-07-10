# External settings for tap pull-request releases

Status: **proposal only; blocked pending explicit approval**.

No GitHub settings, GitHub App, environment, repository ruleset, deploy key,
secret, or credential was created, changed, or removed while preparing this
document. The current release workflow still uses `TAP_DEPLOY_KEY` to push the
formula directly to `homebrew-tap/main`, and it does not wait for the exact tap
CI run. Moving to a tap pull request and waiting for tap CI must not be enabled
until the settings below are approved and applied.

## Intended trust boundary

The release workflow in `ildarbinanas-design/env-vault` should obtain a
short-lived GitHub App installation token that can act only in
`ildarbinanas-design/homebrew-tap`. It should create or update a version-specific
branch, open one formula pull request, wait for required checks, merge under the
tap ruleset, and then wait for `test-formula` on the exact merged commit.

The repository-scoped `GITHUB_TOKEN` is not used for cross-repository writes.
Do not replace the deploy key with a long-lived personal access token.

## 1. GitHub App

Create a dedicated GitHub App, for example `env-vault-tap-release`, owned by the
same account or organization that owns the two repositories.

Configure it as follows:

- Webhook: disabled, because this flow does not consume webhook events.
- Installation scope: **Only select repositories**, with only
  `homebrew-tap` selected. Do not install it account-wide.
- Repository permissions:
  - **Contents: Read and write** — create the release branch and commit the
    generated formula.
  - **Pull requests: Read and write** — create, inspect, update, and merge the
    formula pull request.
  - **Actions: Read** — find and inspect `test-formula` runs for the exact tap
    commit.
  - **Metadata: Read** — granted automatically by GitHub.
- No organization permissions are required.
- Do not grant Administration, Environments, Secrets, Workflows, Issues,
  Packages, or other unrelated permissions.

Optional permissions must be justified by the implementation:

- Add **Checks: Read** only if the release code reads check runs directly or
  uses `gh pr checks`. Polling the named Actions workflow through the Actions
  API does not require write access.
- Add **Actions: Read and write** only if the release workflow is explicitly
  approved to rerun tap CI. The recommended initial implementation uses
  **Actions: Read** and leaves reruns to a maintainer, so a failed check remains
  a visible hard failure.

The App must not be on the tap ruleset bypass list. Its pull requests should be
subject to exactly the same required CI as human pull requests.

Record the App ID and generate one private key only after approval. Never print
the private key or installation token in logs. Installation tokens are
short-lived and must be minted during the job rather than stored as secrets.

## 2. `env-vault` release environment

Create a GitHub Actions environment named `release` in the `env-vault`
repository. The future workflow should attach this environment only to the job
that mints the App token and changes the tap; build, test, and build-only jobs
must not receive release credentials.

Create these environment-scoped values:

| Kind | Name | Value |
| --- | --- | --- |
| Variable | `TAP_APP_ID` | Numeric ID of the dedicated GitHub App |
| Secret | `TAP_APP_PRIVATE_KEY` | Complete PEM private key generated for that App |

`TAP_APP_ID` is an identifier, not a credential, so it is an environment
variable. The PEM is a credential and must be an environment secret. Do not
store either value in the repository, an Actions artifact, a job summary, or a
tap branch.

Recommended environment protection:

- Deployment branches and tags: **Selected branches and tags**.
- Allow the default branch `main` for manual releases and repairs.
- If tag-triggered releases remain enabled, allow tag pattern `v*`; the
  workflow's strict semantic-version validation remains mandatory because an
  environment glob is not a semantic-version validator.
- Required reviewers: at least one release maintainer.
- Enable prevention of self-review where the repository plan and environment
  settings expose that option.
- No wait timer is required unless the maintainer deliberately wants a cooling
  off period.

This creates one human authorization boundary before the cross-repository
credential is released. If a different default branch is configured later,
update both the selected-ref policy and this document before releasing.

The environment alone does not protect anything until the Homebrew job declares
`environment: release`. That workflow change is part of the blocked migration,
not an external setting performed by this document.

## 3. `homebrew-tap` ruleset and required CI

Create an active repository ruleset targeting `refs/heads/main` in
`homebrew-tap`:

- Require changes through a pull request.
- Require the branch to be up to date before merging.
- Require status check **`test`**, emitted by the `test` job in
  `.github/workflows/test-formula.yml`.
- Require conversation resolution.
- Block force pushes and branch deletion.
- Do not add the release App to bypass actors.

Before making `test` required, run the existing workflow on a pull request and
select the exact observed check context in the ruleset UI. The current workflow
job is `test`; selecting the emitted context avoids creating an unfulfillable
rule by typing a similar display name manually.

Do not add a signed-commit rule as part of this migration unless App commit
signing is designed and tested separately. It is unrelated to the PR/CI goal
and could make the release bot unable to update the formula.

### Approval and auto-merge decision

Recommended baseline:

- Use the `env-vault` `release` environment reviewer as the human approval
  boundary.
- Configure the tap PR rule with zero additional required approvals, while
  still requiring the `test` check and conversation resolution.
- Enable **Allow auto-merge** in `homebrew-tap` and let the App request a squash
  auto-merge. The ruleset remains authoritative: auto-merge cannot complete
  before its required checks pass.

This avoids requiring the same maintainer to approve both the release
environment and the generated formula PR. If the owner instead requires a
second-person formula review, set one required PR approval. In that policy the
release workflow needs a documented bounded wait and must exit non-healthy when
approval does not arrive; a later `repair=homebrew` run can resume after the
approval. Do not weaken the ruleset automatically to make a release finish.

After merge, the release workflow must capture the actual tap default-branch
commit and wait for the `push` run of `test-formula.yml` with that exact
`headSha`. Success on the PR head is useful pre-merge evidence but is not a
substitute for success on the merged commit.

The target sequence is:

1. Generate the formula from verified Release assets.
2. Create or reuse a deterministic version branch and PR.
3. Treat a same-version, same-content existing PR or merged formula as a no-op.
4. Wait for required PR CI; fail on cancellation, timeout, or non-success.
5. Auto-merge under the ruleset, or wait for the configured approval policy.
6. Resolve the merged/default-branch tap SHA.
7. Wait for `test-formula.yml` with `event=push`, that exact SHA, and
   `conclusion=success`.
8. Put the PR, tap commit, and tap CI URLs in the env-vault job summary.

This sequence is not yet implemented. It is blocked on approval of the App,
environment values, ref/reviewer policy, tap ruleset, and auto-merge policy.

## 4. Dependency graph and dependency review

The repository contains `.github/workflows/dependency-review.yml`, but the
server-side dependency graph and required-check policy must be configured
externally.

For `env-vault`:

1. In **Settings -> Security -> Advanced Security**, enable the dependency
   graph. Confirm availability for the repository's visibility and GitHub plan.
2. Open or update a pull request that changes a supported dependency manifest
   so the workflow emits its check context.
3. In the active ruleset for the default branch, require the exact observed
   **`Dependency review`** job from the `Dependency review` workflow.
4. Keep `contents: read` as the workflow's default permission. Add only a
   documented permission that the action demonstrably requires.
5. Confirm that ordinary and Dependabot pull requests cannot merge when the
   dependency-review check fails or is missing.

Enable the graph before making the check required, and observe the real check
context first, to avoid deadlocking all pull requests. No credential is needed
for this configuration, but changing repository policy still requires explicit
approval.

## 5. Migrate from `TAP_DEPLOY_KEY`

Perform this migration only after the GitHub App, installation, environment,
and tap ruleset are approved.

1. Inventory the existing `TAP_DEPLOY_KEY` repository or environment secret in
   `env-vault` and the matching SSH deploy key in `homebrew-tap`. Do not display
   the private value.
2. Create the App and install it only on `homebrew-tap` with the permissions
   above.
3. Add `TAP_APP_ID` and `TAP_APP_PRIVATE_KEY` to the `release` environment.
4. Change `build-binaries.yml` to mint a short-lived installation token, push a
   version branch, open/reuse a PR, wait for required CI and merge, then wait
   for the exact post-merge tap CI run. Pin the token action to the project's
   accepted action-version policy and add workflow regression tests.
5. Attach only the tap-update job to `environment: release`; make build-only
   runs prove that no release credential is requested.
6. Validate the new path without publishing a new env-vault version. Use a
   reviewed test plan that cannot overwrite the current formula or move a tag.
7. After the App path has succeeded and its least-privilege audit is complete,
   obtain explicit approval to remove the old credential.
8. Remove the old SSH deploy key from `homebrew-tap`, delete the corresponding
   `TAP_DEPLOY_KEY` secret from `env-vault`, and confirm that no workflow still
   references it.

Do not remove the deploy key first: doing so would turn the approved migration
into an uncontrolled release outage. Do not retain both write paths after the
cutover window, because either credential would then bypass the intended single
trust boundary.

## Approval checklist

All boxes are intentionally open:

- [ ] Approve creation of the dedicated GitHub App.
- [ ] Approve installing it only on `homebrew-tap` with Contents RW, Pull
      requests RW, and Actions R.
- [ ] Decide whether Checks R is needed by the final waiting implementation.
- [ ] Decide whether Actions W is allowed for automated CI reruns; default is
      no.
- [ ] Approve the `env-vault` `release` environment, selected refs, reviewer,
      `TAP_APP_ID`, and `TAP_APP_PRIVATE_KEY`.
- [ ] Approve the `homebrew-tap` main-branch ruleset and required `test` check.
- [ ] Approve the recommended auto-merge policy or choose the two-review policy.
- [ ] Enable and require dependency review for `env-vault`.
- [ ] Approve workflow code changes for tap PR creation and exact tap CI wait.
- [ ] Validate the App path, then separately approve removal of
      `TAP_DEPLOY_KEY` and its matching deploy key.

Until those approvals and settings exist, tap PR publication and the exact tap
CI wait remain blocked. The current deploy-key/direct-push behavior remains the
repository's actual behavior and must be described as such in release evidence.
