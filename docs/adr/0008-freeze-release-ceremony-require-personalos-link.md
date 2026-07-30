# ADR 0008: Freeze release-engineering investment; require an explicit PersonalOS link

## Status

Accepted

## Date

2026-07-30

## Context

`env-vault` is the account owner's personal secret-injection CLI. Over its
lifetime it accumulated release-engineering machinery disproportionate to a
single-user tool: a Homebrew tap distribution pipeline, provenance/SBOM
attestations, and a versioned release-evidence ledger with genesis/legacy
rules (see prior ADRs 0002–0007).

An account-wide audit against the owner's stated goal — **PersonalOS**, a
personal/family system for time, budget, space, and decision management (see
`personalos-core` and `claude-ops/docs/repo-audit-2026-07-30.md`) — found
that `env-vault`'s core function (keeping secrets out of shell history/
plaintext config) is a genuinely reusable piece of infrastructure other
PersonalOS components could depend on, but that no PersonalOS component
actually uses it yet. The release/evidence machinery was expanding on its
own trajectory, disconnected from that goal.

## Decision

- Stop further investment in release-engineering ceremony (Homebrew tap
  polish, provenance/SBOM, evidence-ledger extensions) beyond what already
  exists. Existing machinery is **not** removed — it is functional and
  already paid for — but it does not get new scope.
- `env-vault` remains `keep`: it is legitimate, working infrastructure.
  Further investment must be justified by an actual consumer.
- The first required proof of PersonalOS relevance: use `env-vault` to
  store a credential needed by `personalos-core` or `homelab-collector`
  (e.g. an API key for a future data source), not as a standalone exercise.
- Backlog P1/P2 items (see `backlog.md`) are gated behind an explicit
  "does this serve PersonalOS" check before being picked up — see backlog
  update in the same commit as this ADR.

### Exceptions (added 2026-07-30, after `docs/refactor-decision-log.md` review)

- **Structural necessity to keep an existing invariant alive is not "new
  scope"** and does not require a PersonalOS consumer or security
  justification. Concretely: `docs/release-refactor-backlog.md` item 11
  (evidence-ledger checkpoint/Merkle design) is explicitly exempt from this
  freeze — the bounded evidence-ledger validation window (64 commits, ~61
  slots remaining as of this ADR) will hard-fail future releases on append
  if exhausted before that design lands. This is maintenance of an already
  -accepted guarantee (ADR 0003), not release-engineering ceremony, and may
  proceed without further sign-off.
- **"Security requirement" (used above and in `backlog.md` P1/P2 gating)
  means:** a concrete, already-exploited or documented vulnerability or
  credential/secret exposure (e.g. tracked in `SECURITY.md` or an incident
  record) — not a general "this reduces attack surface" or "this is more
  secure by design" argument. General hardening arguments do not, by
  themselves, unfreeze a P1/P2 item or a `release-refactor-backlog.md` item.

## Consequences

- No new release/CI/evidence-ledger PRs land here without either a security
  fix (as narrowly defined above), a concrete PersonalOS consumer, or the
  structural-necessity exception above.
- If no PersonalOS component adopts `env-vault` within a reasonable window,
  the "keep" verdict should be revisited.
