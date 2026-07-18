# ADR 0005: Informational Link metadata and a protected-main Homebrew bridge

- Status: accepted
- Date: 2026-07-18
- Scope: typed REST pagination and immutable-tag Homebrew-only recovery

## Context

The reviewed empty-Release bootstrap for `v0.0.16` succeeded in run
`29617861201` attempt 1, job `88006715813`, and emitted result artifact
`8421133392`. The subsequent tag-scoped `repair=release-assets` publisher,
run `29617982467` attempt 1, verified and retained all ten Release assets and
completed the supply-chain job. Its Homebrew job `88007538165` then failed in
“Require exact-source attestations before tap mutation”; health was skipped.

The public Attestations REST read returned HTTP `200` and a legitimate
informational header with `rel="deprecation"`, accompanied by `Deprecation`
and `Sunset` dates. The transport called pagination parsing for every read and
accepted only `next`, `prev`, `first`, or `last`, so it reported
`PAGINATION_INVALID`. This was observed before formula generation, App-token
minting, branch push, pull-request creation, or tap mutation. The formula/App
steps were therefore skipped; their susceptibility to the same transport path
was not observed.

The immutable tag still contains that deterministic transport behavior.
Rerunning the failed job or dispatching the tag-scoped `homebrew` repair would
execute the same source-frozen code. Moving the tag, changing any of the ten
assets or attestations, or editing the tap manually would violate the release
contract.

## Decision

Non-paginated transport reads never interpret `Link`. Paginated reads parse
complete RFC link-values, including quoted commas and relative informational
URI-references. They accept repeatable informational target attributes, ignore
well-formed non-pagination relations and any link with an `anchor` context the
client does not implement. Only one canonical,
unanchored `rel="next"` may be followed, and that target must remain absolute
HTTPS on `api.github.com`, keep the original endpoint path and query/field
scope, and advance canonical `page` by exactly one. Duplicate, ambiguous,
unsafe, malformed, looping, over-limit, or incomplete pagination remains a
hard failure.

`publish-homebrew-bridge.yml` is a protected-default-branch incident bridge.
It is reusable only through explicit exact inputs; no release version, SHA,
run, job, Release, or artifact coordinate is a source default. Its source
token has only Actions read, Attestations read, and Contents read. It shares
the `env-vault-release` concurrency group and uses the protected `release`
environment.

Before the tap App secret is accessed, the bridge requires:

- its dispatched protected-main SHA and exact successful main CI identity;
- source/current contract parity for naming, platforms, ten assets, formula
  output (generated in a credential-empty environment), tap App and tap-CI
  identity, and CI/publisher workflow identities;
- the immutable tag/source, exact successful source CI, stable Release ID,
  ten named assets and checksum relations;
- complete exact-source provenance and SPDX attestations signed by the frozen
  publisher workflow;
- the successful bootstrap run/job, exact result artifact ID/digest, offline
  result tuple, and pair digests equal to the downloaded Release bytes;
- the exact failed seven-job publisher graph and diagnostic
  `repair=release-assets` coordinate, including the one failed attestation
  gate and every later formula/App/PR/merge step skipped;
- no deterministic tap branch or PR in any state/base and a lower tap formula
  version. Protected main and this unpublished tap state are re-read
  immediately before token minting.

The tap App token remains scoped to exactly one repository with Actions read,
Contents write, and Pull requests write. The unpublished state and exact tap
base are checked again with that token. `publish-homebrew-pr.sh` also enforces
the expected base inside its own mutation boundary before its first branch
push and again before PR creation. When that expected base is present, any
reused or raced release head must be exactly one formula-only commit whose
sole parent is that base; a stale or multi-parent head fails before PR
mutation. PR discovery is by deterministic head across every base and state,
so an older wrong-base PR cannot be hidden.

The bridge reuses the normal formula generator, deterministic PR publisher,
typed PR-head CI wait, exact-head merge, post-merge CI wait, and read-only
published-state verifier. `wait-tap-ci.sh` can retain its already verified
attempt-qualified identity through a no-clobber output. A compact
`env-vault.homebrew-publication-bridge.v1` artifact binds control/source,
bootstrap, failed publisher, formula, PR/head/merge, both tap CI identities,
and the final verified tap snapshot. Its only next action is
`dispatch_tag_scoped_health`.

## Alternatives considered

- A blind tag-scoped Homebrew rerun was rejected because the same frozen
  transport deterministically fails.
- Manual tap editing or a local PR was rejected because it would bypass the
  release environment, App scope, exact formula, both CI gates, and typed
  result.
- Broadening the source workflow token was rejected; only the already-scoped
  tap App performs tap writes.
- Replacing or recreating assets, attestations, the Release, or tag was rejected
  because those identities are already correct and immutable.
- Treating every `Link` as pagination was rejected because RFC relations also
  communicate policy and lifecycle metadata for the current representation.

## Consequences

The transport accepts the observed deprecation metadata without weakening
origin, path, query, page, completeness, request-count, or retry bounds for
real pagination. The exceptional bridge is larger than a normal repair
because it reconstructs all preconditions from current reviewed code, but its
only remote product mutation is the already-designed Homebrew PR/merge path.

The bridge intentionally does not itself run health or durable evidence. Its
typed result authorizes at most one ordinary tag-scoped `repair=health`; that
frozen read-only stage consumes the completed Release/Homebrew state and
triggers durable evidence.

That recovery sequence is closed. Bridge run `29622303701` attempt 1 at
control SHA `ce1ba7186a4d3133fb04075f275f06e6042c0ccb`, job `88019597858`,
produced result artifact `8422669170` with digest
`sha256:773757223242a4dfc3ee189952a3527d3ae3d84492de868e03285d751c6caefd`.
It bound Homebrew PR #9 at head
`365363826aa722ac5c2df1cc1e5278dc2c69cfcb`, PR CI `29622381037/1`, merge
and tap SHA `8a20bec7e62c854af9bb9a3f94375ccab580cf4c`, and post-merge CI
`29622449331/1`, both successful. Final health publisher `29622574820/1`
then succeeded; evidence listener `29622650408/1` published commit
`e697239298c4b5b1240fc53abe611131d45ac7c0` and compact artifact
`8422728320`. These coordinates are immutable completion evidence, never
operational defaults. Do not dispatch the bridge or health repair again for
`v0.0.16`.
