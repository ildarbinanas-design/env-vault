# env-vault Backlog

## P0

- Verify macOS user-session prompts, refusal, and locked/login keychain behavior
  manually. Disposable CI keychains do not establish these user-session facts.

## P1

Gated by ADR 0008 (2026-07-30): none of these are picked up without an
explicit PersonalOS consumer or a security requirement driving them.

- Evaluate whether GoReleaser would materially improve the working custom release and Homebrew pipeline.
- Nexus binary publishing.
- Shell completions.
- Debian package.

## P2

Gated by ADR 0008 (2026-07-30) — same rule as P1.

- Optional Vault/1Password/KeePassXC connectors.
- Passwork connector deferred; requires separate design and explicit approval.
- MCP server wrapper for agent runtime.
- Policy hooks for enterprise use.
- Secret rotation workflow helpers. **Boundary (2026-07-30, see
  `project-charter.md` Non-Goals):** in scope only if it requires a manual
  invocation each time (e.g. a combined "revoke old + prompt for new +
  remove stale mapping" command around the existing `secret set` flow); any
  scheduled/triggered rotation without a human invoking it crosses into the
  charter's "no automatic secret rotation" non-goal and is out of scope.

## Completed

- Secret import/export. **Delivered 2026-08-14,
  [ADR 0010](docs/adr/0010-encrypted-secret-transfer-container.md):** the
  original P1 scoping ("profile import/export without values") was dropped as
  solving the wrong half — profile mappings hold no values and already travel
  with the repository, while keychain values do not travel at all. `export`
  and `import` therefore move secret values as AES-256-GCM ciphertext under an
  Argon2id-derived passphrase, and carry no profile data. The owner's
  authorization on that date is also the explicit ADR 0008 "does this serve
  PersonalOS" decision for this item.
- Public GitHub binary releases for Linux, macOS, and Windows.
- Homebrew formula distribution with automatic tap updates and tap CI.
- Release migration #107 completed on 2026-09-29 (ADR 0011): pushes to `main`
  maintain the Release Please PR; its owner-authorized merge publishes the
  release. No tag-triggered workflow remains.
- Native CI covers disposable macOS keychains, Windows Credential Manager,
  Linux `pass`, and an isolated Debian Secret Service session. Unit/race tests,
  all five build targets, Windows config burn-in, and pinned v0.4.2 container
  import compatibility remain required. These checks do not replace the
  outstanding macOS user-session exercise above.
- Pinned automated license gate before release publication.
- Verify public GitHub repository settings after first push. **Verified
  2026-07-30** by the release planning check. ADR 0011 (2026-09-29) removed
  that per-release check; repository and ruleset settings are now checked
  during audits, last on 2026-09-27.
- Confirm no secret values in logs with regression test. **Verified
  2026-07-30:** `docs/e2e.md` — every E2E scenario creates a random sentinel
  value and a fail-closed "runner leak gate" (P5, mandatory for all
  scenarios) scans stdout/stderr/artifacts/summaries for it.
- Revoke the one-time GitHub token used for initial publication. **Confirmed
  by the owner 2026-08-09.** This is an account-level fact
  (github.com/settings/tokens) and is not observable from repository state,
  so the confirmation is the owner's, not a repository check. It covers any
  classic/fine-grained PAT created during the 2026-07-05/06 bootstrap window
  (see ADR 0000), independent of the scoped release tokens documented in
  `docs/release-external-settings.md`.
- macOS distribution trust. **Decided 2026-08-09,
  [ADR 0009](docs/adr/0009-no-code-signing-homebrew-only-macos-distribution.md):**
  Developer ID signing and notarization will not be implemented; the Homebrew
  tap is the only supported macOS install path. Closes the credential-policy,
  browser-download test matrix, and notarizable-packaging questions.
