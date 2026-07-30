# env-vault Project Charter

## Product Summary

OS-keychain-backed env profile executor for safe local automation.

## Scope

env-vault will help local automation load named environment profiles without exposing secret values in files, logs, shell history, or evidence.

## Historical Bootstrap Boundary

This charter originally described the repository bootstrap. That phase is
complete: the MVP CLI, production keychain backends, public GitHub repository,
binary releases, and Homebrew distribution now exist.

## Current Non-Goals

- No plaintext production secret backend.
- No command that returns secret values.
- No automatic (scheduled/triggered, unattended) secret rotation. A helper
  that a human must invoke each time — wrapping the existing `secret set`/
  `secret remove` flow, e.g. a combined "revoke old + prompt for new +
  remove stale mapping" command — is in scope and is not automatic rotation
  (2026-07-30; see `backlog.md` P2).
