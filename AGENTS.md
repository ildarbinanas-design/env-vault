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
- Every implementation run must add a versioned `evidence/*.evidence.json`
  record. The JSON is authoritative and must bind the exact before and
  implementation commit SHAs, stable scope/change codes, commands and results,
  preserved guarantees, residual risks, and claim statuses. Remote evidence
  must additionally bind exact run IDs, attempts, asset digests, attestations,
  tap commits/CI, publication state, timings, and retries when applicable.
- Normalize an implementation record and regenerate the concise Markdown index
  with `go run ./cmd/release-evidence implementation --record <record> --candidate-sha <sha>`.
  The command must fail closed unless `<sha>` is the clean current HEAD or the
  exact parent of an evidence-only child commit. Validate a release evidence
  record with `go run ./cmd/release-evidence validate --input evidence/<task>.evidence.json`;
  never hand-edit generated JSON or `evidence/README.md`.
- `evidence_bundle.md` is a read-only historical archive from before the
  machine-evidence transition. Never append to it.
- Do not commit, push, tag, release, create a remote, or publish without explicit approval.
  Merging a generated Release Please pull request is explicit approval only for
  the exact version and source SHA recorded by that reviewed PR; the automated
  release workflow may then create that exact tag and run the existing
  fail-closed publisher. It is not approval for any other version or ref.

## Project Scope

This repository contains the public env-vault MVP at `github.com/ildarbinanas-design/env-vault`. Commits, pushes, tags, releases, and other publishing actions still require explicit approval.

The MVP command surface is allowed to include:

- `env-vault version`
- `env-vault secret set/check/delete/list`
- `env-vault profile create/add/remove/show`
- `env-vault exec`
- `env-vault doctor`

The hard security rules above remain mandatory for every change.
