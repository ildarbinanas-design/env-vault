# Actions artifact lifecycle and bounded cleanup

This document covers collection, offline classification, separate deletion
authorization, bounded execution, and verification. Collection and
classification do not authorize deletion. Only the later exact confirmation
for a canonical post-merge manifest does so.

The measured policy remains exactly 23 upload sites in seven workflows with
retention tiers `7:3`, `14:10`, `30:5`, and `90:5`. The audit found no proof
that queued-workflow or repair windows permit shortening any tier, so this work
does not change a `retention-days` value.

## Collect through the checked transport

Build the transport once for a collection session, then let the checked
wrappers use it:

```sh
go run ./cmd/releasecheck artifacts validate-policy --json
go build -trimpath -o "$SNAPSHOT_DIR/releasetransport" ./cmd/releasetransport
export RELEASE_TRANSPORT_BIN="$SNAPSHOT_DIR/releasetransport"
go run ./cmd/actionsartifactcollect \
  --output "$SNAPSHOT_DIR/actions-artifacts-raw" \
  --repository "$REPOSITORY" --repository "$TAP_REPOSITORY"
```

`actionsartifactcollect` always executes
`scripts/release/gh-api-read.sh`, which executes
`scripts/release/releasetransport.sh read`. The environment variable only lets
that wrapper reuse the prebuilt binary. Do not invoke the binary directly for
collector reads and do not replace either checked wrapper. This preserves the
same host, API, authentication, no-clobber-output, and read-only trust boundary
while avoiding a Go rebuild for every page.

The collector fetches every artifact page, run page, and required producer
attempt. Only after all repositories and attempts are present does it globally
reread every artifact and run page into a distinct final file and compare the
bytes. It publishes `collection.json` only if every reread is identical.

Assemble and validate the offline snapshot with an explicit UTC time and an age
bound no greater than one hour:

```sh
go run ./cmd/releasecheck artifacts assemble-snapshot \
  --collection "$SNAPSHOT_DIR/actions-artifacts-raw" \
  --output "$SNAPSHOT_DIR/actions-artifacts-snapshot.json"
go run ./cmd/releasecheck artifacts validate-snapshot \
  --snapshot "$SNAPSHOT_DIR/actions-artifacts-snapshot.json" \
  --now "$VALIDATION_TIME" --max-age 1h --json
```

## Stage 3 live decision scope

Collect the live fence only after the snapshot has completed. Reuse the same
prebuilt checked transport binary, but write to a new no-clobber directory:

```sh
go run ./cmd/actionsartifactscopecollect \
  --output "$SNAPSHOT_DIR/actions-artifacts-live" \
  --snapshot "$SNAPSHOT_DIR/actions-artifacts-snapshot.json" \
  --release-repository "$REPOSITORY" \
  --repository "$REPOSITORY" --repository "$TAP_REPOSITORY"

go run ./cmd/releasecheck artifacts derive-scope \
  --snapshot "$SNAPSHOT_DIR/actions-artifacts-snapshot.json" \
  --live-collection "$SNAPSHOT_DIR/actions-artifacts-live" \
  --now "$VALIDATION_TIME" --max-age 1h \
  --observation-output "$SNAPSHOT_DIR/actions-artifacts-live-observation.json" \
  --repair-proof-output "$SNAPSHOT_DIR/actions-artifacts-repair-proof.json" \
  --output "$SNAPSHOT_DIR/actions-artifacts-scope.json"
```

`actionsartifactscopecollect` also executes only
`scripts/release/gh-api-read.sh`. It explicitly pages open pull requests and
workflow runs, reads each exact open pull request and every snapshot producer
attempt, captures the protected-main operational contract, and fully peels the
latest stable tag. After all inputs exist it globally rereads every saved
projection into a distinct final file. The index records the exact byte count
and SHA-256 of every raw file. A new, missing, active, or changed run compared
with the complete artifact snapshot invalidates the fence and requires a fresh
snapshot; a completed run appearing between the two collectors is not silently
accepted.

Stage 3 must collect the decision scope independently after the snapshot's
global final reread. It must bind all of the following from current,
authoritative, fully paginated GitHub state rather than infer them from the
artifact snapshot or use defaults:

- the exact protected-main SHA;
- whether a latest stable release exists and, when it does, its exact version
  and source SHA;
- the complete set of open pull request numbers and exact head SHAs;
- the repair boundary and every exact artifact-producing repair identity;
- any additional exact keep identities; and
- every exact identity for which Stage 3 has positive supersession evidence.

The decision-scope `observed_at` must be canonical UTC, no earlier than the
snapshot's `observed_finished_at`, no later than `--now`, and at most the
supplied freshness age old. The freshness age itself cannot exceed one hour.
Stage 3 must bind the protected main, latest stable release, and complete open
PR set in one independently validated live-state fence before emitting the
scope.

The arrays `open_pull_requests`, `repair_boundary.identities`,
`additional_keep_identities`, and `delete_eligible_identities` are mandatory
even when empty. A closed repair boundary requires zero identities; an open
boundary requires at least one. Every repair, additional-keep, and
delete-eligible identity is the exact tuple of producer run ID, run attempt,
workflow path, and head SHA, and must bind to an artifact-producing attempt in
the snapshot.

`delete_eligible_identities` is positive delete authority, not a convenience
filter. An empty array keeps otherwise superseded-eligible artifacts with
`KEEP_NOT_PROVEN_SUPERSEDED`. Only an exact member can receive
`DELETE_SUPERSEDED`. The scope is invalid if a delete identity overlaps an
exact repair/additional keep identity or its head is the protected main,
enabled stable source, or an open PR head.

Classify only while both the snapshot and live scope remain inside the same
one-hour-or-shorter validation window:

```sh
go run ./cmd/releasecheck artifacts classify \
  --snapshot "$SNAPSHOT_DIR/actions-artifacts-snapshot.json" \
  --scope "$SNAPSHOT_DIR/actions-artifacts-scope.json" \
  --live-collection "$SNAPSHOT_DIR/actions-artifacts-live" \
  --now "$VALIDATION_TIME" --max-age 1h \
  --output "$SNAPSHOT_DIR/actions-artifacts-manifest.json"
```

The output manifest is an offline, deterministic decision proof. It is not a
deletion command and does not widen Stage 3 authority.

## Development evidence is not deletion authority

The dated Stage 1 inventory and later typed development replay are capacity
and implementation evidence only. Aggregate counts, bytes, and semantic
digests may be recorded in the append-only operations journal, but temporary
files, live IDs, and observed heads are never defaults and must not be copied
into a deletion command.

After this implementation merges, Stage 5 must collect a new snapshot and raw
live fence, derive the scope, classify it, and review every immutable keep and
candidate-delete record. Keep the temporary raw collection, snapshot, live
collection, scope, observation, and repair proof outside Git long enough for an
independent reviewer to reproduce the classifier entirely offline. They are
not durable repository payloads.

After that review, create the compact deterministic package from the exact
canonical manifest:

```sh
go run ./cmd/releasecheck artifacts package-manifest \
  --manifest "$MANIFEST" --repository-root .
go run ./cmd/releasecheck artifacts verify-manifest-package \
  --repository-root . --manifest-sha256 "$MANIFEST_SHA256" \
  --compare-manifest "$MANIFEST" --json
```

The packager writes exactly one raw-SHA-addressed canonical stored-gzip object
at
`evidence/actions-artifact-cleanups/objects/sha256/<raw-sha256>.json.gz` and
one canonical summary at
`evidence/actions-artifact-cleanups/manifests/<semantic-sha256>.summary.json`.
The summary binds the manifest semantic digest, exact canonical raw and gzip
digests/byte counts, object path, compression format, and
before/keep/delete/expected-after totals. Both files are no-clobber and can be
reconstructed and verified with an empty command path and no credentials.

Persist only those two no-secret files through a small reviewed Stage-5 PR;
do not commit the raw collections or raw API responses. The PR and its CI can
create newer Actions artifacts, but they are never auto-enrolled into the old
manifest's delete set. The packaged post-merge manifest—not the development
manifest—is eligible for the separate human authorization below. Packaging is
evidence, not authorization: every authorized KEEP physical/lineage tuple must
still remain present, and every deletion batch must use a newly collected
current snapshot and raw-live replay.

## Exact human authorization

The human reviews the canonical manifest's semantic SHA-256 and exact
`totals.delete.count` and `totals.delete.bytes`, then supplies this byte-exact
line:

```text
ПОДТВЕРЖДАЮ DELETE ACTIONS ARTIFACTS COUNT <count> BYTES <bytes> MANIFEST SHA256 <sha256>
```

`<count>` and `<bytes>` are the manifest's delete totals and `<sha256>` is its
semantic digest. Any whitespace, count, byte, digest, or authorized-manifest
change invalidates authorization. A new batch or current-state proof under the
same unchanged authority does not require another human confirmation; drift
stops that execution and requires fresh proof, and a replacement authorized
manifest requires a new confirmation. This is separate from the release tuple
confirmation. The raw-object and gzip digests in the durable summary protect
storage and reconstruction bytes; neither substitutes for the manifest
semantic digest in this confirmation.

## Dormant bounded executor

`cmd/actionsartifactdelete` is absent from every workflow and normal validation
path. Invoke it only in the separately authorized deletion stage. Inputs are
the canonical authorized manifest, exact digest/totals and confirmation, a
canonical batch of 1–500 explicit artifact IDs, a newly collected current
snapshot/raw live fence/derived scope, and prior result JSONL files in
chronological order. There is no name, glob, age, or workflow-run deletion
mode.

Before mutation the executor uses the actual wall clock, replays the raw live
fence and classifier offline, and stops if the one-hour-or-shorter proof is
stale. Every authorized keep must remain physically and lineage-exact and
present. Every still-present authorized delete must remain the exact
`DELETE_SUPERSEDED` repository/run/attempt/workflow/head/size and artifact
content tuple. A new current artifact is preserved and cannot be enrolled by
an old manifest.

Later batches form a cumulative chain. An absent authorized delete must be
covered by a unique prior result under the same manifest authority. Accepted
terminal evidence is either exact successful empty-`204` deletion, or an
ambiguous DELETE followed by the one checked exact-ID read proving `404`
absence. A prior terminal ID must now be absent. Missing, extra, forked,
reordered, drifted, or unresolved result history stops the next batch.

The transport accepts only exact
`DELETE repos/OWNER/REPO/actions/artifacts/ID`, expected `204`, with no request
body. It pins `github.com` and REST API version `2022-11-28`, sends one request,
and never retries. Before each request the executor appends and `fsync`s an
intent; after it, a typed outcome. The no-clobber canonical JSONL contains only
manifest binding, exact tuple, UTC time, typed mutation/read-after outcomes,
and a footer—never raw API bodies, logs, tokens, or credentials.

Any non-success or ambiguous mutation gets at most one checked read-after of
that exact artifact ID. The executor records `present`, `absent`, or `unknown`,
stops the batch, and leaves all remaining IDs untouched. An unmatched synced
intent after a crash is unresolved evidence, not implicit success.

## Post-delete verification

After all authorized batches, repeat the complete artifact and live-scope
collection from scratch. Replay offline and prove every terminal delete ID
absent, every authorized keep ID present and lineage-exact, all new artifacts
preserved, and counts/bytes reconciled to the result chain. Do not infer
success from a deletion response alone and do not delete workflow runs, logs,
or conclusions.

GitHub Billing/Usage storage reporting can lag the artifact API by **6–12
hours**. Save the immediate complete API verification, wait that documented
window, then record a separate Billing/Usage observation. A delayed billing
number is not permission to change a budget, retention setting, ruleset, App
permission, tag, Release, asset, attestation, SBOM, or evidence history.
