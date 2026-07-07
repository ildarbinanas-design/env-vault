# env-vault Backlog

## P0

- Verify real macOS Keychain manually.
- Verify real Debian Secret Service manually.
- Verify Linux `pass` manually with installed `pass` and an initialized password store.
- Confirm no secret values in logs with regression test.
- Verify public GitHub repository settings after first push.
- Revoke the one-time GitHub token used for initial publication.

## P1

- Evaluate whether GoReleaser is worth adding after the first manual GitHub Actions release workflow.
- Nexus binary publishing.
- Shell completions.
- Homebrew tap.
- Debian package.
- Profile import/export without values.

## P2

- Optional Vault/1Password/KeePassXC connectors.
- Passwork connector deferred; requires separate design and explicit approval.
- Production file/plaintext backend deferred; requires separate ADR and explicit approval.
- MCP server wrapper for agent runtime.
- Policy hooks for enterprise use.
- Secret rotation workflow helpers.
