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
- **Item 13 (Actions artifact/billing cleanup) — resolved 2026-07-30 via
  live GitHub API check, not deferred to the owner:** current state is 367
  artifacts / ~47.6 MB total (`gh api repos/.../actions/artifacts`), and the
  last 30 Actions runs are completing normally (one `in_progress`, the rest
  `success`, 4 unrelated `failure`s from 2026-07-21/27, no billing-block
  signature). The 177.8 GB-hours / 2,027-artifact snapshot documented in the
  item was a past, already-resolved state — retention tiers are evidently
  doing their job. **Stays frozen with the rest of Track B**; re-check if
  Actions usage climbs again.

## Revised step order

0. Baseline tests (unchanged from the original plan).
1. Track A, one package per session: `internal/errors` → `internal/redact`
   → `internal/output` → `internal/secretstore` → `internal/config` →
   `internal/cli`.
2. Track B item 11 (evidence-ledger checkpoint/Merkle) — its own reviewed
   PR, under the ADR 0008 exception. Not urgent by a fixed date, but should
   land before the bounded window (~61 slots as of 2026-07-30) gets tight.
3. `cmd/*` cleanup — deferred; no item currently authorizes it, revisit
   after Track A completes.

Track B items 1, 2, 3, 5, 6, 7, 8, 9, 13 stay off this plan entirely until
either a PersonalOS consumer/qualifying security requirement appears, or
the new Actions-activity workstream below finds a live problem —
re-litigating them was out of scope for this round.

## New authorized workstream (2026-07-30): GitHub Actions/administration activity analysis

The owner granted permission for broader GitHub read access (`gh auth
refresh -s user,admin:public_key,read:project`, requested 2026-07-30) and
asked for a standing analysis of what the current release pipeline actually
costs in developer UX and time-to-market — not just whether it's internally
consistent (which the governance-document audit above already covered).

This is explicitly meant to run as **separate, dedicated sessions** (per the
owner's own instruction), not inline in an orchestrator chat — read-only
`gh api`/`gh run` analysis over Actions run history, timing, and cost, plus
a comparison against the actual baseline need. Two concrete workstreams:

1. **Time-to-market / developer UX measurement**: for recent PRs, measure
   PR-open → CI-green → merge → publish latency; Actions run duration
   trends (the repo's own item 6 already tracks *aggregate* CI time — this
   is about the human-facing wait, not just runner-seconds); flag anything
   that visibly slows down a solo maintainer's iteration loop.
2. **Complexity-vs-baseline comparison + extract-to-separate-repo
   proposal**: compare what the release pipeline actually does today (App-
   based token minting, provenance/SBOM attestations, versioned
   contract v1/v2, evidence ledger, artifact lifecycle policy) against the
   baseline developer need ("`brew install env-vault` works and updates
   cleanly"). Produce a genuine proposal — keep as-is / trim / extract the
   release-engineering machinery into a separate, reusable repo (candidate
   home: alongside `homebrew-personalos`/`homebrew-tap`, since the pattern
   would directly reuse across future PersonalOS CLI tools) — with an
   explicit recommendation, not just a list of options.

Neither workstream is started by this plan — they should be launched as
their own `claude -p` sessions (or equivalent), producing a written report
each, per the owner's request. See the parent claude-ops session's next
steps for launch details.
