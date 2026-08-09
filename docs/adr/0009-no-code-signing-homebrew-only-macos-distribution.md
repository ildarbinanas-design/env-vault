# ADR 0009: No code signing or notarization; Homebrew tap is the supported macOS install path

## Status

Accepted

## Date

2026-08-09

## Context

macOS distribution trust has been an open question since v0.0.2. Discovery
against the public release in July 2026 established that the checksum
sidecars verify, the darwin/amd64 binary is unsigned, the darwin/arm64
binary carries an ad-hoc linker signature rather than a Developer ID
identity, and no Apple notarization evidence exists. The decision at the
time was to **defer** production signing until a credential policy existed
for Apple certificate material, notary credentials, protected environments,
and release approval.

That deferral was never recorded in this repository. It has now been
re-verified against v0.1.0, sixteen releases later:

| Check | Result on v0.1.0 |
|---|---|
| `shasum -a 256 -c` on release sidecars | passes, both Darwin targets |
| `codesign` on darwin/amd64 | `code object is not signed at all` |
| `codesign` on darwin/arm64 | `adhoc, linker-signed`, `TeamIdentifier=not set` |
| `spctl --assess --type execute` | `rejected`, both targets |
| signing or notarization steps in release workflows | none |
| `com.apple.quarantine` after `brew install` | absent throughout the install chain |

Two findings changed the picture since July.

First, Gatekeeper quarantine now **propagates through `tar` extraction**. A
synthetic quarantine attribute placed on a release archive reappears on the
extracted binary — macOS assigns a fresh quarantine event rather than
copying the original attribute — and executing that binary terminates with
SIGKILL. Extraction from a non-quarantined archive does not produce the
attribute, so the propagation is specific rather than an artifact of
extraction. The July discovery observed the opposite behaviour; the macOS
version of that observation was not recorded, which makes the earlier result
unreproducible.

Second, because the binary has no stable code-signing identity, macOS
Keychain ACLs cannot recognise it across reinstalls. A full uninstall and
reinstall through Homebrew on 2026-08-09 caused `env-vault exec` to block on
a system authorization dialog, which in turn blocked a dependent LaunchAgent.
Keychain entries survive; the authorization does not.

This ADR also follows [ADR 0008](0008-freeze-release-ceremony-require-personalos-link.md),
which froze release-engineering investment as disproportionate to a
single-user tool. Developer ID signing and notarization would reopen exactly
the class of machinery ADR 0008 closed: certificate custody, notary
credentials, protected environments, and release approval gates.

## Decision

**Developer ID signing and Apple notarization will not be implemented.**

The Homebrew tap `ildarbinanas-design/tap` is the only supported macOS
installation path. Release artifacts remain unsigned and continue to ship
SHA-256 sidecars.

Manual download is documented as a fallback for inspection and for Linux,
and is explicitly unsupported on macOS.

## Consequences

Accepted knowingly:

1. **Reinstalling re-triggers Keychain authorization.** Every version
   upgrade presents a system dialog before secrets can be read, and any
   automation invoking `env-vault` non-interactively will block until it is
   answered. Measured 2026-08-09.
2. **Manual download on macOS yields a binary that will not run** when the
   download path attaches a quarantine attribute: `spctl` rejects it and
   execution terminates with SIGKILL.
3. **`spctl` rejects the binary in all cases**, including the Homebrew
   install. It runs because Homebrew does not attach a quarantine attribute,
   not because the binary is trusted.

Consequently the following are closed and will not be pursued: a Developer
ID and notarization credential policy, a browser-download Gatekeeper test
matrix for Darwin artifacts, and evaluation of notarizable packaging formats
(ZIP, DMG, PKG). Gatekeeper diagnostics are documented only to the extent
needed for the warning in `README.md`.

## Revisiting

This decision should be reopened if users install by any path other than
Homebrew, if distribution extends beyond the owner's own systems, or if
Keychain re-authorization on upgrade costs more than configuring signing
would.
