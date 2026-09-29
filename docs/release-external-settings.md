# External settings for releases

`.github/workflows/release.yml` publishes releases ([ADR 0011](adr/0011-minimal-release-pipeline.md),
[`RELEASING.md`](../RELEASING.md)). It depends on settings that live outside
the repository: two tokens, two Actions environments, repository settings and
rulesets in `env-vault`, and the branch policy of `homebrew-tap`. This document
lists them. No workflow checks them at release time, so review them after any
settings change and at each audit.

## Where each credential is used

| Job | Environment | Credential | What it does with it |
| --- | --- | --- | --- |
| `release-please` | `release-planning` | `RELEASE_PLANNING_TOKEN` | Maintains the release pull request. After it merges, tags the merge commit and opens a draft release, then reads that draft. |
| `build` | none | `GITHUB_TOKEN`, `contents: read` | Builds, checks and smoke-tests the five targets. |
| `publish` | `release` | `GITHUB_TOKEN`, `contents`, `id-token` and `attestations: write` | Attests the archives and binaries, uploads them to the draft and publishes it. |
| `verify` | none | `GITHUB_TOKEN`, `contents` and `attestations: read` | Checks the published release, its tag, checksums and attestations. |
| `tap` | `release` | `HOMEBREW_TAP_TOKEN` | Opens the formula pull request in `homebrew-tap` and enables auto-merge. |

Workflow-level permissions are `contents: read`; only `publish` raises them.
Pull request and manual runs stop after packaging and use no secret.

A pull request opened with `GITHUB_TOKEN` would not trigger the required checks,
so Release Please uses a personal access token. Both tokens are fine-grained,
scoped to one repository, stored only in their environment, and never granted a
ruleset bypass.

## 1. `RELEASE_PLANNING_TOKEN`

A fine-grained personal access token:

- repository access: only `env-vault`;
- repository permissions:
  - **Contents: Read and write**: the release branch, the release tag and the
    draft release;
  - **Pull requests: Read and write**: the release pull request;
  - **Issues: Read and write**: the `autorelease:` labels;
  - **Metadata: Read** (automatic);
- no account permissions, and an explicit expiry (section 5).

It needs no Administration access: `release.yml` does not check repository
settings. `Contents: write` cannot be narrowed further, so the workflow tests
pin what the job may do with it.

## 2. `HOMEBREW_TAP_TOKEN`

A fine-grained personal access token:

- repository access: only `homebrew-tap`;
- repository permissions:
  - **Contents: Read and write**: the formula branch and commit;
  - **Pull requests: Read and write**: the formula pull request and its
    auto-merge;
  - **Metadata: Read** (automatic);
- no account permissions, and an explicit expiry (section 5).

It needs no Workflows permission: the job changes only `Formula/env-vault.rb`.
It also no longer needs Actions read, because the job does not read tap runs;
drop that grant at the next rotation if the token still has it.

## 3. Actions environments in `env-vault`

| Environment | Secret | Deployment branches and tags |
| --- | --- | --- |
| `release-planning` | `RELEASE_PLANNING_TOKEN` | `main` only |
| `release` | `HOMEBREW_TAP_TOKEN` | `main` only |

No workflow runs on a tag, so neither environment allows tags. Neither has a
required reviewer or a wait timer: merging the release pull request is the
release authorization, and a second approval would stop every release halfway.

## 4. Repository settings

### `env-vault`

- Squash merging only, with the pull request title and body as the commit
  title and message, so the Conventional Commit title reaches Release Please.
- **Immutable releases** on. `release.yml` publishes from a draft, and a
  published release cannot change afterwards.
- Actions: default workflow permissions read-only, and Actions may not create
  or approve pull requests.
- Ruleset on `main`: changes only through a pull request; required checks
  `quality-gate`, `pr-title`, `Dependency review`, `Analyze (go)` and
  `Analyze (actions)`, strict; resolved conversations; squash only; no force
  push or deletion; no bypass.
- Ruleset on `refs/tags/v*`: tags cannot be updated or deleted; creation stays
  allowed so Release Please can tag the release commit; no bypass.
- Ruleset on `refs/heads/release-evidence`: no force push or deletion. The
  branch is frozen history of the old pipeline.

### `homebrew-tap`

- **Allow auto-merge** on (enabled 2026-09-27). The `tap` job merges the
  formula pull request only through auto-merge.
- Ruleset on `main`: changes only through a pull request; required check
  `test` from `.github/workflows/test-formula.yml`; resolved conversations;
  squash only; no force push or deletion; no required approvals; no bypass.
- Actions enabled for the pull request, push and weekly runs of
  `test-formula.yml`.

## 5. Checking and rotating

Read-only checks for the owner (`gh` signed in as the owner):

```sh
repo=ildarbinanas-design/env-vault
for env in release-planning release; do
  gh api "repos/$repo/environments/$env/deployment-branch-policies" \
    --jq '.branch_policies[] | "\(.type) \(.name)"'
  gh api "repos/$repo/environments/$env/secrets" --jq '.secrets[].name'
done
gh api "repos/$repo/rulesets" --jq '.[] | "\(.id) \(.name) \(.enforcement)"'
gh api repos/ildarbinanas-design/homebrew-tap --jq .allow_auto_merge
```

Each environment should list only `branch main` and its one secret, and
`allow_auto_merge` should be `true`.

Both tokens expire on 2026-12-26; regenerate them about a week earlier. For
each token: open <https://github.com/settings/personal-access-tokens>, choose
the token, select **Regenerate token**, set the expiry, then store the new value
through the hidden prompt:

```sh
gh secret set RELEASE_PLANNING_TOKEN --env release-planning -R ildarbinanas-design/env-vault
gh secret set HOMEBREW_TAP_TOKEN --env release -R ildarbinanas-design/env-vault
```

A regenerated token keeps its permissions. The next push to `main` exercises
the planning token; the next release exercises the tap token.
