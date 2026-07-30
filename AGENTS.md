# env-vault Agent Rules

env-vault is a standalone Go CLI project for safe local automation with OS-keychain-backed environment profiles.

## Hard Security Rules

- Never print, log, store in config, store in tests, or store in evidence any secret value.
- Do not implement or document a `secret get` command.
- Do not add a `--value` flag or any equivalent secret-value command-line argument.
- Secret input must use a hidden prompt or `--stdin` only.
- The production backend target is the operating system keychain.
- No production plaintext secret backend is allowed.
- A test or insecure backend is allowed only behind an explicit environment gate and must be impossible to enable accidentally.
- Structured errors are mandatory for implemented commands.
- Mandatory tests are required once behavior beyond the local version placeholder is implemented.
- The release audit trail is the GitHub Releases page plus ordinary git and pull
  request history. There is no append-only evidence ledger: the publisher's
  `health` job verifies live release, attestation, Homebrew, blocked-tag, and
  abandoned-release state and fails the release, it does not assemble a durable
  record. The published `release-evidence` branch and the durable evidence
  artifacts already in Actions storage are frozen history: never rewrite,
  extend, or retrofit them.
- `release/contract.v2.json` is the only current operational release contract.
  Runtime mutation code must consume a digest-bound releasecheck version plus
  operational-projection pair and call
  `release_require_typed_contract_projection` before GitHub access. Static
  Actions fields that cannot consume the projection must have exact contract
  parity tests.
- Historical v1 authority is closed: only the exact bytes in
  `release/history/contract.v1.json` and exact tuples in
  `release/contract-history.v2.json` may route a v1 source. The live
  `release/contract.v1.json` is not the archive or a source of new operational
  defaults. The registry's `evidence_format` entries are frozen historical
  identity for those v1 releases; they authorize nothing new.
- GitHub transport and mutations use `gh` or the GitHub API. Repository
  checkers consume saved files offline, hold no credentials, and fail closed on
  unknown, incomplete, invalid, or unsupported input.
- Release REST reads must go through `scripts/release/releasetransport.sh` (or
  its `gh-api-read.sh` GET adapter). Actions authority uses attempt-qualified
  typed identity; run `.name`, job `workflow_name`, and `.pull_requests` are
  diagnostic only. Direct/high-level `gh` exceptions must remain enumerated in
  `release/github-transport-boundary.v1.json`; mutations are never blindly
  retried after an ambiguous transport result. Non-paginated reads do not
  interpret informational RFC `Link` metadata; paginated reads follow only one
  unanchored, trusted, invariant-preserving `rel="next"` and ignore other
  well-formed relation contexts.
- Do not commit, push, tag, release, create a remote, or publish without explicit approval.
  A generated Release Please pull request may be merged only after the operator
  records the exact authorization tuple as a comment on that pull request,
  containing its version, pull-request number, and full unchanged head SHA.
  The comment must be authored by a repository owner or member and its creation
  and last edit must be strictly earlier than the recorded merge timestamp;
  same-second, post-merge, and post-merge-edited comments are invalid. That one
  tuple authorizes only the resulting exact merge source, immutable tag, and
  fail-closed publisher; it is not approval for any changed PR head, version,
  or ref. Use `scripts/release/authorize-and-merge-release-pr.sh` to record the
  comment and merge; do not perform those two mutations as independent manual
  commands.

## Project Scope

This repository contains the public env-vault MVP at `github.com/ildarbinanas-design/env-vault`. Commits, pushes, tags, releases, and other publishing actions still require explicit approval.

The MVP command surface is allowed to include:

- `env-vault version`
- `env-vault secret set/check/delete/list`
- `env-vault profile create/add/remove/show`
- `env-vault exec`
- `env-vault doctor`

The hard security rules above remain mandatory for every change.
