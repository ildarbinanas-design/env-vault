# Architecture decision records

Each record states one significant decision: its context, the options
considered, the decision and its consequences. The format follows Michael
Nygard's ADRs, with the Considered options section from MADR.

## Index

| ADR | Decision | Status |
|---|---|---|
| [0000](0000-local-bootstrap.md) | Local Bootstrap | Superseded, without an ADR |
| [0001](0001-secret-backend.md) | Secret Backend | Accepted |
| [0002](0002-release-github-transport.md) | Strict GitHub transport for release authority | Superseded by 0011 |
| [0003](0003-compact-release-evidence-ledger.md) | Compact content-addressed release evidence and automatic ledger genesis | Superseded by the [trim plan](https://github.com/ildarbinanas-design/env-vault/blob/1fd6638295fb616189e66da7cc110cf4831a3d94/docs/trim-plan-2026-07-30.md) on 2026-07-30 |
| [0004](0004-empty-release-asset-bootstrap.md) | Source-bound bootstrap for an empty immutable Release | Superseded by 0011 |
| [0005](0005-informational-link-and-homebrew-bridge.md) | Informational Link metadata and a protected-main Homebrew bridge | Superseded by 0011 |
| [0006](0006-versioned-operational-release-contract.md) | Versioned operational release contract and closed historical routing | Superseded by 0011 |
| [0007](0007-actions-artifact-lifecycle.md) | Typed Actions artifact lifecycle and bounded cleanup | Superseded by 0011 |
| [0008](0008-freeze-release-ceremony-require-personalos-link.md) | Freeze release-engineering investment; require an explicit PersonalOS link | Accepted, amended by 0011 |
| [0009](0009-no-code-signing-homebrew-only-macos-distribution.md) | No code signing or notarization; Homebrew tap is the supported macOS install path | Accepted |
| [0010](0010-encrypted-secret-transfer-container.md) | Encrypted Secret Transfer Container | Accepted |
| [0011](0011-minimal-release-pipeline.md) | Minimal release pipeline for an equal-maintainer team | Accepted |

In force: 0001, 0008 as amended by 0011, 0009, 0010 and 0011. Until step 3
of ADR 0011 merges, releases still run on the pipeline that 0002 and
0004–0007 describe, and AGENTS.md still enforces its contract v2 and GitHub
transport rules. AGENTS.md also keeps the ADR 0007 artifact deletion
ceremony until step 6.

## Rules

- **One decision per record.** Name the file `NNNN-short-title.md` with the
  next free number. Numbers are never reused.
- **Status** is one of:
  - Proposed;
  - Accepted;
  - Deprecated, when the decision no longer applies and nothing replaces it;
  - Superseded by ADR N.

  A record that changes part of another says "Amends ADR N", and the other
  record's status says "Amended by ADR N".
- **An accepted record does not change.** Only its status block changes, to
  record a replacement or an amendment, with the date. A new or changed
  decision is a new record that links to the old one, and the old one links
  back.
- **Links to files in this repository point to records or to a fixed
  commit.** A document in the working tree can be deleted, so link to it as
  `https://github.com/ildarbinanas-design/env-vault/blob/<commit>/<path>`.
- **A record holds the decision, not the work.** Run IDs, incident timelines
  and step-by-step plans belong in issues, pull requests and runbooks. The
  record links to them.
- **A pull request proposes a record, and the owner, `ildarbinanas-design`,
  accepts it**, by merging it or by explicitly delegating the merge.

Records 0000 to 0011 predate these rules. On 2026-09-27, with the owner's
approval, their status blocks were updated and links to documents due for
deletion were pinned to a commit. ADR 0011 also gained its Considered options
and moved its step-by-step plan to
[issue #107](https://github.com/ildarbinanas-design/env-vault/issues/107).
ADRs 0000 and 0003 were superseded outside the ADR process.

## Template

```markdown
# ADR NNNN: Decision in a few words

## Status

Proposed

## Date

YYYY-MM-DD

## Context

The facts and forces that make a decision necessary.

## Decision

What we will do.

## Considered options

Each option with its advantages and drawbacks, and why it was chosen or
rejected.

## Consequences

What becomes easier or harder, and which risks are accepted.
```
