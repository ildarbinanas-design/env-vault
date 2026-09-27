# env-vault Agent Rules

env-vault is the owner's personal Go CLI. It runs commands with secrets
injected as environment variables and keeps the secrets in the operating system
keychain, never in files or shell history. Judge every change against that
goal: secret values must not leak, `exec` must work on macOS, Linux, and
Windows, binaries must reach the owner intact through GitHub Releases and
Homebrew, and everything else stays proportionate to a single-user tool
(ADR 0008).

## Hard Security Rules

- Never print, log, store in config, store in tests, or store in evidence any secret value.
- Do not implement or document a `secret get` command.
- Do not add a `--value` flag or any equivalent secret-value command-line argument.
- `export` writes stored secret values only as authenticated ciphertext in a
  transfer container, under a passphrase read from a hidden prompt (ADR 0010).
  A container carries values only and never profile mappings. The
  passphrase itself obeys the same rule as a secret value: no flag, no
  environment variable, no file. The single exception is reading it from stdin
  when the complete insecure test-backend gate is already active. Plaintext
  export of a secret value remains forbidden.
- Secret input must use a hidden prompt or `--stdin` only.
- The production backend target is the operating system keychain.
- No production plaintext secret backend is allowed.
- A test or insecure backend is allowed only behind an explicit environment gate and must be impossible to enable accidentally.
- Structured errors are mandatory for implemented commands.
- Mandatory tests are required once behavior beyond the local version placeholder is implemented.
- Releases follow ADR 0011. On every push to `main`,
  `.github/workflows/release.yml` lets Release Please maintain the release pull
  request. The run for a merged release pull request tags the merge commit,
  builds the five targets from it, attests the archives and binaries, publishes
  the draft release, verifies the published release, and opens the Homebrew tap
  pull request with auto-merge. No workflow runs on a tag.
- Merging the generated Release Please pull request is the release
  authorization. Merge it head-guarded — `gh pr merge <n> --squash
  --match-head-commit <head-sha>` — so a head that moved during review can never
  be published silently. The merge authorizes only the resulting merge commit
  and its tag; it is not approval for any changed head, version, or ref.
- A release file is genuine only if `gh attestation verify` passes with the
  release workflow and `main` pinned (ADR 0012):
  `--signer-workflow ildarbinanas-design/env-vault/.github/workflows/release.yml
  --source-ref refs/heads/main`. `-R` alone accepts attestations from any
  branch.
- Release mutations are never blindly retried after an ambiguous result. A
  failed release is resumed by re-running the failed job of the same run.
- The release audit trail is the GitHub Releases page, the attestations, and
  ordinary git and pull request history. The published `release-evidence`
  branch and the durable evidence artifacts already in Actions storage are
  frozen history: never rewrite, extend, or retrofit them.
- Until migration step 6 (issue #107) removes them, `release/contract.v2.json`,
  the release Go packages, the `scripts/release/` helpers that `release.yml`
  does not call, and the bootstrap, bridge, and legacy workflows are dormant
  code of the old pipeline. Do not extend them.
- Deleting Actions artifacts is a separate, still-mandatory ceremony: it keeps
  its byte-exact `ПОДТВЕРЖДАЮ DELETE ACTIONS ARTIFACTS …` confirmation, because
  that operation is irreversible and has no release gate behind it (ADR 0007).

## Working Mode

The owner decides how much agents may do without asking. The Standing
Delegation below applies here and in `ildarbinanas-design/homebrew-tap`.

### Standing Delegation

Agents may, without asking:

- read any repository and GitHub state of the owner;
- create `claude/`-prefixed branches, commit and push to them, and delete the
  branches they created once finished;
- open, update, and comment on their own pull requests and reply to review
  threads on them;
- dispatch, re-run, and cancel workflow runs on their own `claude/` branches;
- push a temporary verification workflow to a `claude/verify-*` branch when a
  check needs another operating system or network access that the agent's own
  environment lacks. The workflow triggers only on that branch (`push`,
  `workflow_dispatch`), has `contents: read` (plus `actions: read` only for a
  job that downloads an artifact), receives no secrets, never uses
  `pull_request_target` or `workflow_run`, pins every action by full commit
  SHA, sets `timeout-minutes` on every job, and prints only non-secret results.
  The branch is deleted when the check is done;
- merge their own pull request into `main` head-guarded (`gh pr merge <n>
  --squash --match-head-commit <head-sha>` or the API equivalent) once every
  required check is green on that exact head, a fresh-context review of the
  final diff found nothing blocking, and the pull request changes no reserved
  path.

Reserved for the owner:

- merging the generated Release Please pull request (the release
  authorization, see `RELEASING.md`) and creating tags or releases by any other
  path;
- merging a pull request that changes a reserved path. In this repository:
  `AGENTS.md`, `.claude/`, `.github/workflows/`, `release/`,
  `scripts/release/`, `release-please-config.json`, and
  `.release-please-manifest.json`. In homebrew-tap: `AGENTS.md`, `.claude/`,
  `.github/workflows/`, and `Formula/` (the env-vault release workflow's
  formula pull request merges by auto-merge after the tap's `test` check). These
  paths decide what may be published and
  who may change it, so an agent prepares the pull request and the owner
  merges it;
- repository, ruleset, environment, secret, Actions, GitHub App, security, and
  account settings;
- deleting Actions artifacts (the ceremony above);
- force-pushes, history rewrites, and deleting branches, tags, or releases the
  agent did not create;
- anything that weakens a Hard Security Rule.

### Asking the Owner

- Settle whatever the code, the documentation, or a conventional default
  settles, and state the default you took.
- Ask only at a genuine fork: a decision that belongs to the owner and changes
  what you do next. Use the structured question tool (`AskUserQuestion` in
  Claude Code), put the recommended option first, and batch open forks into one
  round instead of asking one at a time.
- Collect reserved actions into one ordered owner checklist per task, each item
  with the exact place and action.
- When a permission rule, hook, or safety classifier blocks an action, do not
  work around it: add it to the owner checklist and continue with the rest.
- Report results with evidence: the command or run and what it returned.

### What Enforces This Section

GitHub enforces only its rulesets, for everyone including the owner: `main`
changes only through a squash-merged pull request with the required checks and
cannot be force-pushed or deleted, `release-evidence` cannot be force-pushed or
deleted, and `v*` tags cannot be updated or deleted. Nothing else reserved for
the owner above is enforced; it is an instruction, not a control. Agents act
under the owner's GitHub identity, so GitHub cannot tell an agent's merge from
the owner's.

The deny rules in `.claude/settings.json` load only when a Claude Code session
starts in this repository's root; a cloud session that clones several
repositories one level up does not load them. They match literal Bash command
text only and do not cover GitHub MCP tools. Treat them as a guard against
accidents, not as a boundary, and do not weaken them either.

## Project Scope

This repository contains the public env-vault MVP at `github.com/ildarbinanas-design/env-vault`. Commits, pushes, merges, tags, releases, and other publishing actions follow the Working Mode above. Pull request conventions and local checks are in `CONTRIBUTING.md`.

The MVP command surface is allowed to include:

- `env-vault version`
- `env-vault secret set/check/delete/list`
- `env-vault profile create/add/remove/show`
- `env-vault exec`
- `env-vault doctor`
- `env-vault export` / `env-vault import`

The hard security rules above remain mandatory for every change.
