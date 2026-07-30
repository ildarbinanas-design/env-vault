# env-vault Backlog

## P0

- Verify real macOS Keychain manually.
- Verify real Debian Secret Service manually.
- Verify Linux `pass` manually with installed `pass` and an initialized password store.
- Revoke the one-time GitHub token used for initial publication. **Status
  unresolved (2026-07-30):** cannot be confirmed from repo state — this is
  an account-level fact (github.com/settings/tokens), not something visible
  in code or docs. Requires the owner to manually check for and revoke any
  classic/fine-grained PAT created during the 2026-07-05/06 bootstrap window
  (see ADR 0000), independent of the App-based tokens now used for releases.

## P1

Gated by ADR 0008 (2026-07-30): none of these are picked up without an
explicit PersonalOS consumer or a security requirement driving them.

- Evaluate whether GoReleaser would materially improve the working custom release and Homebrew pipeline.
- Nexus binary publishing.
- Shell completions.
- Debian package.
- Profile import/export without values.

## P2

Gated by ADR 0008 (2026-07-30) — same rule as P1.

- Optional Vault/1Password/KeePassXC connectors.
- Passwork connector deferred; requires separate design and explicit approval.
- Production file/plaintext backend deferred; requires separate ADR and explicit approval.
- MCP server wrapper for agent runtime.
- Policy hooks for enterprise use.
- Secret rotation workflow helpers. **Boundary (2026-07-30, see
  `project-charter.md` Non-Goals):** in scope only if it requires a manual
  invocation each time (e.g. a combined "revoke old + prompt for new +
  remove stale mapping" command around the existing `secret set` flow); any
  scheduled/triggered rotation without a human invoking it crosses into the
  charter's "no automatic secret rotation" non-goal and is out of scope.

## Completed

- Public GitHub binary releases for Linux, macOS, and Windows.
- Homebrew formula distribution with automatic tap updates and tap CI.
- Default-branch manual releases with explicit semantic versions and retained tag-driven releases.
- Pinned automated license gate before release publication.
- Verify public GitHub repository settings after first push. **Verified
  2026-07-30:** `.github/workflows/audit-release-planning-app.yml` mints a
  read-only (`permission-administration: read`) App token and proves
  repository/ruleset settings on demand (`workflow_dispatch`).
- Confirm no secret values in logs with regression test. **Verified
  2026-07-30:** `docs/e2e.md` — every E2E scenario creates a random sentinel
  value and a fail-closed "runner leak gate" (P5, mandatory for all
  scenarios) scans stdout/stderr/artifacts/summaries for it.
