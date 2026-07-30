# Refactor plan v2 (2026-07-30, after `docs/refactor-decision-log.md`)

Supersedes the draft two-track plan discussed before the governance-document
audit. Docs only — no code was touched to produce this plan; the doc fixes
themselves (backlog.md, ADR 0008, project-charter.md, design.md,
release-refactor-backlog.md) already landed alongside this file, per the
9 answered questions.

## What didn't change

**Track A (product code)** is untouched by this round: `internal/errors`,
`internal/redact`, `internal/output`, `internal/secretstore`,
`internal/config`, `internal/cli`. None of the 9 questions concerned these
packages. Proceed exactly as originally planned — one package per session,
plan → patch → tests, freely (ADR 0008 does not gate product code, only
release-engineering ceremony).

## What changed

**Track B (release automation)** is more precisely scoped now that
`docs/release-refactor-backlog.md`'s 13 items carry explicit status tags:

- **3 items already implemented** (4, 10, 12) — drop from active planning
  entirely, no remaining work.
- **1 item explicitly exempt from the ADR 0008 freeze** (11 — evidence-ledger
  checkpoint/Merkle design): this is the only Track B item authorized to
  start now, as structural maintenance of an existing invariant (the
  64-commit bounded validation window), not discretionary release
  engineering.
- **8 items remain frozen** (1, 2, 3, 5, 6, 7, 8, 9): do not start any of
  these without an explicit PersonalOS consumer or a narrowly-defined
  security requirement (see ADR 0008's definition — a concrete, documented
  vulnerability/exposure, not general hardening reasoning).
- **1 item needs your call before either of the above rules applies** (13 —
  Actions artifact/billing cleanup): the audit found this describes an
  *already-accrued* billing exposure (177.8 GB-hours against a zero-dollar-
  blocking 0.5 GB-month allowance), not a speculative future risk. That may
  make it a structural-necessity exception like item 11 — but this plan
  does not decide that for you. **Action: confirm whether Actions runs are
  currently failing or budget-blocked.** If yes, item 13 jumps the queue
  ahead of everything else, including Track A. If no, it stays frozen with
  the rest.

## Revised step order

0. Baseline tests (unchanged from the original plan).
1. **If item 13 is live/urgent** (see above) — handle it first, as its own
   reviewed PR, independent of Track A/B sequencing below.
2. Track A, one package per session: `internal/errors` → `internal/redact`
   → `internal/output` → `internal/secretstore` → `internal/config` →
   `internal/cli`.
3. Track B item 11 (evidence-ledger checkpoint/Merkle) — its own reviewed
   PR, under the ADR 0008 exception. Not urgent by a fixed date, but should
   land before the bounded window (~61 slots as of 2026-07-30) gets tight.
4. `cmd/*` cleanup — deferred; no item currently authorizes it, revisit
   after Track A completes.

Track B items 1, 2, 3, 5, 6, 7, 8, 9 stay off this plan entirely until you
explicitly decide one of them has a PersonalOS consumer or qualifying
security requirement — re-litigating them was out of scope for this round.
