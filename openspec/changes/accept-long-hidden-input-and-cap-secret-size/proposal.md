# Proposal

## Why

The owner's 6050-character hidden input exposed a canonical-terminal limit:
long lines can hang on macOS or be silently truncated on Linux, while values
accepted by storage can later cause `exec` to fail with an unhelpful `E2BIG`
error. Fixing input integrity and bounding new values addresses this concrete
owner failure within [ADR 0013](../../../docs/adr/0013-owner-benefit-scope.md).

## What Changes

- On macOS and Linux, read hidden input in noncanonical mode with application
  line editing, preserving complete long input and separately reading an
  export passphrase and its confirmation even when pasted together.
- Limit new secret values to `secretstore.MaxValueBytes = 64 << 10` (65536
  bytes), or a smaller backend limit (2560 bytes for Windows Credential
  Manager). Apply this to `secret set`, including `--stdin`, and import
  preflight through the existing value-limit mechanism.
- Bound secret input memory, drain an oversized hidden line through Enter,
  wipe the input, and return `SECRET_TOO_LARGE` before opening a backend.
  Trim one stdin line ending before checking its value length.
- Keep existing oversized records usable by `secret check`, `exec` and
  `export`; when the OS refuses process creation with `E2BIG`, keep
  `RUNTIME_ERROR` and exit `1` and explain how to reduce the environment.
- Add regression coverage, native validation and user documentation without
  changing signal restoration, other-platform prompts or container encoding.

**BREAKING — import acceptance contract.** A valid container, including one
created by v0.4.2, with a value over 65536 bytes that import would write will
now fail with `SECRET_TOO_LARGE` before the first write. Such a value was
previously importable when its backend allowed it. Existing entries skipped
with `--on-conflict skip` remain exempt; authentication, format compatibility
and the smaller Windows limit are unchanged. Export can still produce a
container carrying an existing oversized value; importing that value for a
new write is intentionally refused.

## Capabilities

### Modified Capabilities

- `secret-storage`: complete hidden input and editing, bounded stdin, and the
  effective limit on newly stored values.
- `secret-transfer`: long hidden passphrases and confirmation, and the common
  value limit in import preflight.
- `exec`: actionable remediation for an OS environment-size launch failure.
- `cli-output`: the expanded meaning of `SECRET_TOO_LARGE`, with unchanged
  error codes, exit statuses and result fields.

## Impact

Implementation is limited to the existing input and write paths in
`internal/cli`, shared size definitions in `internal/secretstore`, the launch
error in `internal/runner`, their tests, `README.md` and `docs/security.md`.
The four delta specs will be synchronized and this change archived in the
implementation PR. No dependencies, backends, container formats, release
files, `CHANGELOG.md`, Windows/BSD prompt implementation or agent permissions
change.

**Observed in source at `f5012e1`.** `prompt_unix.go` explicitly enables
`ICANON`; both secret and transfer inputs call it. `readSecret` uses unbounded
`io.ReadAll`, and import's `refuseOversizedValues` only checks a backend's
optional `ValueLimiter`. Process start failures currently share generic
permissions/arguments remediation. The platform thresholds and the owner's
6050-character failure are supplied incident evidence; native before/after
tests will record their reproduction during implementation, not planning.

**Review boundary.** These English artifacts are the complete planning
deliverable. Commit and push them to `agent/hidden-prompt-long-input`, then
stop without opening a PR. Implementation starts only after the owner's
`apply` instruction; the eventual PR is not to be merged by an agent.
