# ADR 0002: Strict GitHub transport for release authority

- Status: accepted
- Date: 2026-07-17
- Scope: release-only GitHub reads, workflow/run/job/attempt identity, and the
  typed v2 evidence Git-data mutation boundary

## Context

The `v0.0.15` baseline contained 44 direct `gh api` source call sites: 36 reads
(35 REST and one GraphQL) and eight explicit mutations. Thirty-five of the 36
reads were outside the existing helper; the helper itself contained the other
direct REST site.
Callers independently implemented pagination, retries, response parsing, and
Actions identity. Historical incidents also proved that REST `.name` changes
when `run-name` is configured, `.pull_requests` may become empty after merge,
and Git blob content is canonical base64 transported with line wrapping.

Release reads may be retried after a transport failure. Mutations may have
committed before the client saw a timeout and therefore must not share that
retry policy. The five published targets, immutable tags/assets, global release
serialization, and exact confirmation boundary are unchanged by this decision.

## Decision

`cmd/releasetransport` and `internal/githubtransport` are the release GitHub
read boundary. They:

- pin `github.com`, REST API version `2022-11-28`, accepted media types, and a
  sanitized non-interactive `gh` environment;
- publish only no-clobber regular output files after a complete valid response;
- reject duplicate or case-variant JSON members recursively;
- apply at most five attempts per page, 100 pages, 500 REST requests, and 120
  seconds of cumulative retry wait per read; each `gh` process is capped at 64
  MiB stdout and 256 KiB stderr;
- require every pagination `Link` to keep the original host, path, and complete
  query scope (endpoint query plus fields), including invariant `per_page` when
  supplied; the reviewed release endpoints need only canonical `page`, which
  must advance by exactly one, so added filters and cursor controls fail closed;
- expose stable versioned capability, error, REST observation, Contents, Git
  blob, and Actions identity documents;
- bind Actions authority to repository/head-repository, run ID and attempt,
  workflow path, event, direct head SHA/ref, status/conclusion, canonical URL,
  and—when required—the attempt-qualified job ID/name/URL;
- retain run display name and job workflow name as diagnostic fields only.

The v2 evidence publisher additionally uses `rest mutate-once`, a closed
Git-data adapter for evidence blobs, trees, commits, and the
`release-evidence` reference. It validates an endpoint-specific strict
closed-schema JSON payload snapshot (with canonical base64 content), streams
it on standard input, performs exactly one request,
and returns a typed `success`, terminal `http_error`, or `ambiguous` outcome.
It never retries a mutation. A missing `gh api --input` capability does not
break the established read boundary: preflight simply omits
`one_shot_git_data_mutation`, while an attempted mutation fails before network
I/O. Success requires a strict operation-specific response body. Any observed
4xx is terminal, and only an exact reviewed 422 shape may trigger reference-race
reconciliation. Indeterminate blob or reference outcomes can proceed only
through a fresh exact read reconciliation; ambiguous tree and commit creation
fail closed.

`scripts/release/releasetransport.sh` is the general launcher and
`gh-api-read.sh` is its GET compatibility adapter. CI jobs build the binary once
and export `RELEASE_TRANSPORT_BIN`; the integration-test process does the same.
Help is human-readable with exit zero. Every controlled validation, bootstrap,
and transport failure path in the CLI, launcher, and adapter emits one
`env-vault.github-transport-error.v1` document with a constant secret-safe
input/bootstrap message when no remote error is available. This guarantee does
not claim to structure an external OS signal or an executable-disappearance
race after the launcher has validated the file.

All eight baseline direct REST mutations remain visible, single-attempt
operations with their existing exact postcondition or ambiguity
reconciliation. The ninth registered mutation authority is the typed v2
adapter, including automatic creation of a previously absent evidence
reference. In particular, an ambiguous Git-blob POST is never replayed: the
publisher calculates the deterministic Git object SHA and performs one typed
byte-for-byte read-back. Arbitrary binary blobs are encoded outside jq and
then passed as canonical base64, so gzip bytes are not coerced through a UTF-8
string.
The only remaining direct non-mutation API exception is the pre-existing
read-only GraphQL ruleset
query. Every direct or high-level `gh` exception is enumerated with owner and
rationale in `release/github-transport-boundary.v1.json`; tests fail on an
unregistered command or count change.

## Alternatives considered

- Shell-only hardening was rejected because it would continue duplicating
  response schemas, attempt identity, pagination, and retry classification.
- Retrying all `gh api` calls was rejected because transport ambiguity after a
  write is not proof that the write did not commit.
- Treating workflow display names or PR associations as identity was rejected
  because both have already changed independently of the authoritative run.
- Replacing all dedicated `gh` commands was deferred. Cross-workflow check
  aggregation, cryptographic attestation verification, and reviewed mutations
  remain explicit registered boundaries; authoritative Actions tuples are
  resolved immediately through the typed transport.

## Consequences

The transport is security-critical and has a larger Go/test surface, but release
read behavior and error semantics now have one implementation. Operational
source has zero direct REST reads, eight registered direct mutations, one
registered typed mutation adapter, one registered GraphQL observation, 62
bounded helper call sites, and 17 typed Actions-identity call sites.

Two single observed local runs of
`go test ./tests -run '^TestPublishReleaseEvidenceIsNoClobberAndRaceSafe$' -count=1`
changed from 91.251 seconds with per-call cold builds to 14.978 seconds with one
process build (76.273 seconds, 83.6%). Host/tool/cache metadata and repetitions
were not recorded, so this is directional test evidence, not a benchmark or an
Actions-runner claim.

The implemented retry, page-count, retry-wait, and per-process output caps do
not bound the whole command's time or aggregate memory. The CLI currently uses
only process-signal cancellation and relies on its enclosing workflow timeout;
it has no own per-request or end-to-end deadline. Paginated aggregation also
has no separate total-byte cap, so 100 individually accepted 64 MiB pages can
theoretically contribute 6,400 MiB before decoding/aggregation overhead.
Release engineering owns a Stage 4 transport-cleanup target of at most 60
seconds per request, 300 seconds end to end, and 256 MiB aggregate response
bytes, with timeout/cancellation and boundary tests before those become
guarantees.
