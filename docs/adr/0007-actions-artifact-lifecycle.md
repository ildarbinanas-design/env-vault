# ADR 0007: Typed Actions artifact lifecycle and bounded cleanup

- Status: accepted
- Date: 2026-07-20
- Scope: Actions artifact retention, inventory, keep/delete authority, bounded
  deletion, and verification

## Context

Actions artifact storage had become a release-availability risk. The repository
already had 23 explicit `upload-artifact` sites in seven workflows, using
7-, 14-, 30-, and 90-day retention. A broad age or name filter cannot preserve
attempt-qualified promotion, repair, protected-main, open-PR, stable-release,
and durable-evidence dependencies. Mutation ambiguity also means a timed-out
DELETE cannot safely be retried.

Stage 1 and a later typed development replay supplied dated capacity evidence.
They were not collected after this implementation merged, were never
human-authorized deletion manifests, and cannot be operational defaults.

## Decision

Keep the existing retention tiers unchanged. Exact source inventory tests bind
the policy to 23 upload sites in seven workflows and to distribution `7:3`,
`14:10`, `30:5`, `90:5`. No queued-workflow or repair-window proof supports
shortening a tier.

Use five separate trust boundaries:

1. The typed collector fully paginates artifacts and runs, reads every required
   producer attempt, globally rereads all pages, and publishes only a complete
   no-clobber raw collection.
2. The live collector independently binds protected main, stable release,
   every open PR head, repair state, complete run/attempt state, immutable keep
   IDs, and positive supersession identities. Offline derivation distrusts and
   replays its raw files.
3. The classifier emits one sorted record per artifact, exact keep/delete
   totals, and a whitespace-independent semantic SHA-256. Unknown, active,
   incomplete, overlapping, stale, or uncorroborated state fails closed.
4. After independent offline reproduction, a deterministic packager stores
   only the canonical manifest as one content-addressed object plus one small
   summary. Temporary raw collections remain outside Git.
5. Only a new post-merge canonical manifest plus its exact delete count/bytes,
   semantic digest, and byte-exact human confirmation authorizes deletion.

The compact package uses fixed repository paths:

- `evidence/actions-artifact-cleanups/objects/sha256/<raw-sha256>.json.gz`;
- `evidence/actions-artifact-cleanups/manifests/<semantic-sha256>.summary.json`.

The object path hashes the exact uncompressed canonical manifest bytes,
including the final newline. Its encoding reuses ADR 0003's canonical gzip:
zero mtime, no name/comment, OS 255, a fixed ten-byte header, maximal
65,535-byte stored-DEFLATE blocks, and CRC32/ISIZE trailer. The versioned
canonical summary binds semantic/raw/gzip digests and byte counts, compression,
object path, and all four totals. Create and verify it entirely offline with
`releasecheck artifacts package-manifest` and
`verify-manifest-package`. Existing paths are never overwritten.

Only a small reviewed Stage-5 PR persists the object and summary. Raw API
pages, the snapshot, live fence, scope, observation, and repair proof stay in
the private reviewer workspace and do not bloat Git history. Any Actions
artifacts produced by that PR or its CI are new state and cannot be enrolled by
the packaged manifest. Package existence is not human authorization.

The confirmation is:

```text
ПОДТВЕРЖДАЮ DELETE ACTIONS ARTIFACTS COUNT <count> BYTES <bytes> MANIFEST SHA256 <sha256>
```

Deletion uses explicit batches of at most 500 unique artifact IDs. Every
authorized keep stays present and immutable; a changed decision/reason does not
weaken that keep. Every authorized delete that remains present must still be
the same `DELETE_SUPERSEDED` repository/run/attempt/workflow/head/size and
artifact-content tuple. New artifacts are preserved and never auto-enrolled.

Batch results form one cumulative canonical JSONL chain. A synced intent is
written before each request and a synced typed outcome after it. Missing or
unmatched records fail closed. A later batch may treat an absent authorized ID
as terminal only when the chain contains either exact empty-204 success or an
ambiguous outcome followed by one checked 404 absence. Unresolved present,
unknown, error, forked, or omitted history blocks the next batch.

The shared transport adds one closed mutation shape: exact bodyless
`DELETE repos/OWNER/REPO/actions/artifacts/ID`, expected status 204. It retains
the pinned host/API, sanitized environment, one request, no retry, and typed
`success`, `http_error`, or `ambiguous` outcomes. No workflow-run delete exists.

The executor is a dormant standalone command. No workflow, normal checker, or
validation script calls it. Production freshness is bound to the actual wall
clock, the process deadline cannot exceed proof expiry, and freshness is
rechecked before each intent and DELETE. Every batch uses a fresh current
snapshot/raw-live replay and re-proves every authorized KEEP physical and
lineage tuple present; durable packaging does not freeze current state.

## Consequences

Cleanup requires a separate post-merge collection and human gate; development
aggregates cannot accelerate it. Ambiguity and crashes may require a new
reconciliation or authority rather than an automatic retry. This is deliberate.

The compact manifest package makes the reviewed decision durable without
checking temporary raw responses into Git. Its semantic digest is the human
confirmation identity; raw and gzip digests independently protect exact
reconstruction and transport bytes.

Post-delete verification fully paginates again, proves terminal IDs absent and
keep IDs present, and reconciles counts/bytes. GitHub Billing/Usage can lag the
artifact API by 6–12 hours, so billing is observed separately after that
window. No cleanup changes tags, Releases, assets, attestations, SBOMs, evidence
history, logs, run conclusions, settings, budgets, rulesets, or permissions.

## Alternatives considered

- Broad age/name/glob deletion was rejected because it is not exact authority.
- Retention shortening was rejected because dependency windows are unproved.
- Treating a development manifest as authority was rejected because it
  predates the merged implementation and exact confirmation.
- Retrying DELETE after timeout was rejected because the first request may
  have committed.
- Treating 404 as idempotent success was rejected; only the explicit
  ambiguity/read-after path may record terminal absence.
