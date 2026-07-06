# ADR 0000: Local Bootstrap

## Status

Accepted; superseded for public module path by the selected repository owner.

## Date

2026-07-05

## Context

env-vault is being created as a local standalone project repository from AI-PDLC registration work.

GitHub owner and remote creation were unavailable during local bootstrap and were deferred.

## Decision

Create the repository locally with a provisional Go module path.

No remote was created. No remote was added. No commit was created during this bootstrap decision.

## Consequences

- Local development could start without a GitHub owner decision.
- Public import compatibility was intentionally not promised during bootstrap.
- Publication work must use the selected public module path recorded in current project metadata.
