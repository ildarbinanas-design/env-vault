# Security Policy

## Supported Versions

This project is a local MVP. Until the first tagged release, security fixes are handled on the `main` branch.

## Reporting a Vulnerability

Do not include secret values in public issues, pull requests, screenshots, logs, terminal transcripts, or reproduction data.

Use GitHub private vulnerability reporting when it is available for this repository. If private reporting is not available, contact the maintainer privately first and share only non-secret metadata such as command names, structured error codes, platform details, keychain backend notes, and redacted config mappings.

## Secret Handling In Reports

env-vault must not print, log, or store secret values outside the operating system keychain. Reports should never include real credentials. If a reproduction needs a secret-shaped value, generate an ephemeral dummy value and revoke or delete it immediately after testing.
