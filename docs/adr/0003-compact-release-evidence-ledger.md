# ADR 0003: Compact content-addressed release evidence and automatic ledger genesis

- Status: accepted
- Date: 2026-07-17
- Scope: release-evidence format selection, offline replay, durable Git layout,
  automatic genesis, and compatibility with the published legacy ledger

## Context

The immutable `v0.0.15` evidence record is valid and fully replayable, but its
canonical v1 JSON is 1,475,191 bytes and is stored both at the version root and
at the publisher-attempt path. Most of those bytes are repeated raw provenance
and SBOM verification documents. The protected production ledger already has a
two-commit legacy first-parent history rooted at the independently established
source bootstrap. Rewriting that history, its published evidence, or releases
`v0.0.12` through `v0.0.15` is prohibited.

The legacy publisher also required an operator to pre-create
`refs/heads/release-evidence` at a release source. Creating the first commit by
inheriting that source tree made workflow files reachable and therefore
required a broader Workflows permission. A new repository instead needs a
parentless, evidence-only initial commit that can be created with Contents
write and whose release/source/workflow tuple is machine-verifiable.

## Decision

### Versioned format and capability routing

The canonical v1 evidence remains the semantic source. A v2 bundle moves raw
attestation documents and the canonical evidence core into a deduplicated
content-addressed object store while preserving byte-exact v1 reconstruction.
The six durable per-version and per-attempt metadata files are:

- `release-evidence-bundle.json`;
- `index.md`;
- `metrics-comparison.json`;
- `metrics-comparison.md`;
- `storage-metrics.json`;
- `parity.json`.

Objects live at `evidence/objects/sha256/<raw-sha256>.gz`. The path digest is
the SHA-256 of the uncompressed canonical object. Each root descriptor also
binds its compressed byte count and compressed SHA-256; these two identities
must not be conflated.

The source-built checker selects the route through its version document:

- both capability keys genuinely absent selects v1;
- exactly `release_evidence_bundle:[2]` and
  `release_evidence_genesis:[1]` selects v2;
- null, empty, partial, differently typed, or unknown values fail closed.

The write-scoped job independently derives the source checker route and binds
it to the read-scoped output. The protected-listener checker must support the
exact v2/genesis pair. Both checkers replay the candidate in an isolated
environment with an empty command path and non-secret sentinels that prove
credential independence before mutation. During the migration, the seven-day
handoff artifact contains v1 and v2. The per-release/per-attempt portion of a
v2 90-day replay artifact and Git ledger contains only the six compact metadata
files plus objects; a fresh ledger also contains genesis, and the production
ledger retains its legacy v1 history. The v1 route is unchanged for an older
source checkout.

`parity.json` is a successful-publication certificate and is emitted only after
both v1 and v2 pass, reconstruct to identical canonical v1 bytes, and return
the same successful decision. It is not a container for failed evidence.
Executable tests separately require semantically equivalent v1/v2 failures to
return the same stable error code in the Go API and offline CLI.

### Deterministic object encoding and bounds

Canonical gzip is independent of Go compressor-version choices: a fixed
10-byte gzip header, stored DEFLATE blocks of at most 65,535 bytes, a final
stored block, and canonical CRC32/ISIZE trailer. Readers reject trailing
members, non-canonical headers/blocks, and descriptor mismatches. This is valid
gzip and deterministic transport encoding; stored DEFLATE intentionally makes
no compression-ratio claim.

The root is bounded to 153,599 bytes. A bundle has 1–64 objects, at most 16 MiB
uncompressed per object, at most 16 MiB plus 2 KiB encoded per object, and
bounded 64 MiB raw / 64 MiB plus 64 KiB encoded aggregates. A bundle directory
is exclusively reserved, carries an `.incomplete` marker while objects are
written, publishes the root last, and is never allowed to replace either an
empty or non-empty concurrent target directory.

Storage metrics name their byte domains explicitly:

- `git_blob_payload_bytes_per_ledger_path`;
- `git_blob_payload_bytes_after_object_id_deduplication`;
- `durable_metadata_plus_gunzip_object_bytes`;
- deterministic USTAR plus canonical stored-gzip export, excluding the
  storage report itself.

Replaying immutable `af521d52b898088cb49f6256964e377e33e95a5d` with the
current checker and unchanged contract produced: root 1,887 bytes; auxiliary
metadata 6,944; parity 593; storage self-report 1,468; three objects totaling
357,677 raw and 357,766 encoded bytes. Root-plus-attempt logical payload fell
from 2,964,270 to 379,550 bytes (871 permille); unique Git blob payload is
368,658; offline reconstructed payload is 368,569. Deterministic export fell
from 1,486,981 to 374,320 bytes (748 permille). These are scoped measurements,
not network-transfer or compression-ratio claims. The semantic bundle digest
is `a5a65168aab8d31762c84cb4db98dc79a40b2460c4a5873fb3a368b09d6f9c80`.

### Ledger creation and append

Only an exact HTTP 404 for the evidence ref enables genesis. Before every
genesis Git mutation, the source commit is freshly re-observed. The publisher
creates blobs and then an evidence-only tree with no `base_tree`, a parentless
commit, and `refs/heads/release-evidence`. The versioned
`evidence/genesis.v1.json` binds repository, first version/source, first bundle,
publisher run/attempt/repair, and evidence workflow run/attempt. The initial
tree is an exact closed set; source files and workflow files are not inherited.

The anchor self-digest proves internal integrity, not authenticity. Authenticity
depends on the externally enforced exact protected ref, reviewed workflow and
checker, and independently observed release tuple. On 2026-07-17 the active
`Protect env-vault release evidence` ruleset was observed on exact
`refs/heads/release-evidence` with deletion and non-fast-forward protection,
no bypass actors, and no current-user bypass. The ruleset ID and current ref tip
are observations recorded in the external-settings document, never operational
constants.

The existing production branch remains `legacy-compatible`: it is neither
rewritten nor retrofitted with an anchor. Its first-parent history is checked
for a single parent, exact controlled-blob preservation, unchanged inherited
non-evidence paths, closed v1/v2 namespace shape, and a maximum depth of 64.
An exact existing tuple may be read at depth 64; any append is rejected before
the first mutation. The observed two-commit baseline therefore had 62 append
slots before this stage and will have 61 after one successful next-patch
append. A reviewed checkpoint/Merkle migration must land before exhaustion.

For anchored history, every root/tuple metadata blob is bound to exact Git-tree
SHA and size before read, root and first tuple identities must match, and each
unique metadata blob is read once through the 17 MiB typed blob transport.
Descriptor count/per-object/aggregate limits and descriptor-versus-tree object
size are checked before object reads. Before either publisher creates a commit,
the returned tree must preserve all earlier controlled blobs and the exact
non-evidence projection.

## Consequences

Fresh repositories require no manual branch bootstrap and no Workflows write.
The production legacy ledger keeps its historical trust model and external
anchor, while new ledgers gain an in-band machine-verifiable genesis tuple.
Offline replay no longer depends on GitHub, credentials, expired artifacts, or
the compressor implementation used by a future Go toolchain.

The 64-commit walk is deliberately bounded. Before the remaining capacity is
consumed, release engineering must design a reviewed checkpoint or Merkle
summary that preserves old-root/source binding and offline verification. A
separate hardening item should freeze a compact historical v1 golden fixture in
CI and dynamically bind the oldest accepted legacy parent/source; neither may
be claimed as implemented by this ADR.
