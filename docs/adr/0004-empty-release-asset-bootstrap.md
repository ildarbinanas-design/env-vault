# ADR 0004: Source-bound bootstrap for an empty immutable Release

- Status: accepted
- Date: 2026-07-18
- Scope: valid empty GitHub Release reconciliation and immutable-tag recovery

## Context

The first `v0.0.16` publisher created the correct public GitHub Release and
then stopped before uploading an asset. The Release API returned a valid
`assets: []`, but `reconcile-release-assets.sh` combined response-shape
validation and name extraction in one `jq -e` pipeline. A valid empty array
emits no values, so jq exited `4` and the script misclassified the response as
malformed. The exact failure is recorded in the operator incident matrix; no
asset, attestation, Homebrew, or durable-evidence mutation followed it.

Changing `main` cannot repair the workflow and scripts frozen in the immutable
tag. Moving the tag, deleting/recreating the Release, manually uploading all
assets, or blindly rerunning the failed job would either violate immutability
or execute the same deterministic defect.

## Decision

Release-response shape validation and asset-name extraction are separate
operations. A response is valid only when `assets` is a bounded array of
objects with safe string names. The reconciliation path accepts an empty valid
array and treats all ten contract assets as missing. The complete-download
path still requires exactly ten unique expected names, so empty is never
reported as a healthy release.

Each no-clobber upload is attempted once. After every successful or failed
upload response, reconciliation refreshes the complete remote inventory. An
ambiguous response is accepted only when the fresh inventory differs by the
one intended name and a dedicated download compares byte-for-byte with the
verified promotion. No mutation is blindly retried.

For an immutable tag whose old script cannot parse the empty state,
`bootstrap-release-assets.yml` is a reviewed default-branch control plane. It
has only `actions: read` and `contents: write`, shares the global
`env-vault-release` concurrency group, and uses the protected `release`
environment. Every incident coordinate is a required explicit dispatch input;
there are no version/run/SHA operational defaults.

Before mutation it requires all of the following:

- current protected default-branch control SHA and a valid current contract;
- exactly one successful main CI run/attempt for that reviewed control SHA;
- an exact lightweight immutable tag resolving to the supplied source SHA;
- source and current contracts with identical platform, asset, naming, CI,
  publisher, and promotion-schema semantics;
- exactly one successful source CI run/attempt and its complete classified
  five-target matrix;
- byte-exact offline verification of its promotion manifest and ten assets;
- exactly one failed tag-triggered publisher, its seven-job terminal graph,
  the exact failed release job/step, and its retained bundle ID/digest;
- equality of the source-CI bytes and retained publisher-bundle bytes;
- the exact public, non-draft, non-prerelease Release ID with zero assets.

Immediately before the first pair mutation, the workflow re-reads the protected
default-branch ref and requires it still equals the dispatched control SHA.

The bootstrap uploads only the first contract archive and its checksum. Every
snapshot rejects unexpected or duplicate names, and both members are read back
and compared. A versioned run/source/release/pair result is retained as an
Actions artifact. The existing tag-scoped `repair=release-assets` workflow is
then the only mechanism allowed to publish the other eight assets and continue
attestations, Homebrew, health, and evidence.

## Alternatives considered

- Moving or recreating the tag/Release was rejected because published release
  identities are immutable.
- Uploading all ten files from a local operator was rejected because it would
  bypass promotion, workflow identity, environment, concurrency, attestations,
  Homebrew, health, and durable evidence.
- Rerunning the failed job was rejected because its tag-frozen parser is
  deterministic and still rejects `assets: []`.
- Uploading one arbitrary placeholder was rejected because every Release asset
  must be one of the exact promoted contract bytes.
- Granting broader workflow/App permissions was rejected; the bootstrap needs
  no Workflows, Attestations, administration, or tap write permission.

## Consequences

A brand-new empty Release is now a tested steady-state reconciliation input.
Concatenated JSON roots are malformed even when each root is independently
valid. Malformed, duplicate, unsafe, unexpected, divergent, and concurrent
states fail before further mutation. Immutable-tag recovery needs one extra
reviewed workflow run, but the exceptional mutation is the minimum exact pair
and remains inside the normal release environment and serialization boundary.

The bootstrap is not authorization for an arbitrary historical repair. Any
future invocation must supply a newly reviewed exact incident tuple and must
begin from the same zero-asset/no-conflict state. A nonempty Release is a hard
stop.
