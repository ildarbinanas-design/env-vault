# ADR 0013: Justify work by owner benefit or demonstrated risk

## Status

Accepted in the resulting tree when the owner merges this proposal.
Supersedes [ADR 0008](0008-freeze-release-ceremony-require-personalos-link.md).
Amends [ADR 0011](0011-minimal-release-pipeline.md) only where it preserves
the PersonalOS product-scope requirement.

## Date

2026-10-03

## Context

ADR 0008 constrained growing release machinery by requiring a PersonalOS
consumer or a narrowly defined exception. ADR 0011 simplified that machinery
but kept the product-scope gate. During the agent-rule audit, the owner chose
to evaluate work by its value to env-vault's owner, without requiring adoption
by a separate project. The cost of development and maintenance still needs
to fit a personal, single-user CLI.

## Decision

- Before starting new work, including backlog P1/P2 items and release
  engineering, identify a concrete benefit to the env-vault owner or a
  demonstrated risk the work addresses. State the use case or evidence in
  the task or pull request and keep the implementation and maintenance cost
  proportionate to that benefit or risk.
- A PersonalOS consumer is optional. Maintaining an existing guarantee is a
  valid benefit; a documented, plausible failure or exposure is a valid risk.
  An incident need not already have happened. General claims of being
  "more secure" or "more robust" without a concrete failure or benefit are
  insufficient.
- Apply the same test to product features, dependencies, CI and release
  tooling. There is no separate release-engineering freeze.
- Preserve the existing security rules, project non-goals and owner approval
  boundaries. Meeting the scope test does not authorize a release, a reserved
  action or a change to those boundaries.

## Considered options

- Keep the PersonalOS requirement. It ties work to an external consumer but
  excludes useful work for the CLI's owner and requires exceptions for
  ordinary maintenance.
- Require owner benefit or demonstrated risk. Chosen: it gives each change
  a concrete justification while allowing proportionate preventive work.
- Remove the scope gate. Rejected: an unbounded backlog could recreate the
  maintenance burden that motivated ADR 0008.

## Consequences

Useful work can proceed without PersonalOS integration or a prior security
incident. Authors must explain the need and cost; a backlog entry alone is
not a justification. Historical PersonalOS references in accepted records
remain part of the decision history, with their current force identified by
the status blocks. This decision changes no CLI behavior, release workflow
or release authorization.
