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
- Release evidence must be versioned machine JSON generated from exact workflow
  and artifact identities. A generated Markdown index may summarize that JSON;
  append-only narrative evidence is not an authorization or release gate.
- GitHub transport and mutations use `gh` or the GitHub API. Repository
  checkers consume saved files offline, hold no credentials, and fail closed on
  unknown, incomplete, invalid, or unsupported input.
- Do not commit, push, tag, release, create a remote, or publish without explicit approval.
  A generated Release Please pull request may be merged only after the operator
  records the exact authorization tuple as a comment on that pull request,
  containing its version, pull-request number, and full unchanged head SHA.
  The comment must be authored by a repository owner or member and its creation
  and last edit must be strictly earlier than the recorded merge timestamp;
  same-second, post-merge, and post-merge-edited comments are invalid. That one
  tuple authorizes only the resulting exact merge source, immutable tag, and
  fail-closed publisher; it is not approval for any changed PR head, version,
  or ref.

## Project Scope

This repository contains the public env-vault MVP at `github.com/ildarbinanas-design/env-vault`. Commits, pushes, tags, releases, and other publishing actions still require explicit approval.

The MVP command surface is allowed to include:

- `env-vault version`
- `env-vault secret set/check/delete/list`
- `env-vault profile create/add/remove/show`
- `env-vault exec`
- `env-vault doctor`

The hard security rules above remain mandatory for every change.
