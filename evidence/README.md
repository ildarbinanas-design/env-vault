# Checked-in operational proofs

This directory holds a few reviewed, machine-readable one-time proofs. It is
**not** the release audit trail: that is the GitHub Releases page plus git and
pull-request history. The per-release evidence ledger that once lived on the
`release-evidence` branch was retired on 2026-07-30 (Phase 3 of
[`docs/trim-plan-2026-07-30.md`](../docs/trim-plan-2026-07-30.md)); that branch
and the durable replay artifacts in Actions storage stay frozen and are never
extended or rewritten.

No file here may contain credentials, installation tokens, private keys, secret
values, or unredacted environment dumps.

[`e2e-baseline-migration/`](e2e-baseline-migration/) is the checked-in one-time
proof that replaced the expiring historical E2E comparator with
`docs/e2e-baseline.json`. It is verified offline with:

```sh
go run ./cmd/e2e-baseline verify-migration \
  --repository-root . \
  --contract release/contract.v2.json \
  --baseline docs/e2e-baseline.json \
  --migration evidence/e2e-baseline-migration/migration.json
```

[`release-pipeline-restart/github-state-baseline.v1.json`](release-pipeline-restart/github-state-baseline.v1.json)
is the machine-readable remote-state snapshot taken before the selective
release-pipeline restart. It preserves the exact main, generated release PR,
and failed immutable `v0.0.8` tuples without turning remote observation into a
networked checker responsibility.

Stage-5 Actions artifact cleanup uses a separate compact reviewed namespace:
`actions-artifact-cleanups/objects/sha256/<raw-sha256>.json.gz` contains the
exact canonical decision manifest in ADR-0003-compatible stored gzip, and
`actions-artifact-cleanups/manifests/<semantic-sha256>.summary.json` binds its
semantic/raw/gzip identities, sizes, and totals. A small reviewed PR may add
only those no-secret files after independent offline replay. Raw API pages,
snapshots, and live-fence workspaces remain outside Git, and the package alone
does not authorize deletion.

See [`RELEASING.md`](../RELEASING.md) for the promotion, metrics, repair, and
post-release verification contracts.
