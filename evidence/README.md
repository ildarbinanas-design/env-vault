# Release evidence

Release evidence is machine-first. Workflows collect remote observations with
`gh`, save them as files, and pass those files plus verified artifacts to the
offline checker. The checker emits versioned JSON; the Markdown index is
generated from that JSON and is never a second source of truth.

Per-release evidence binds the exact version, source and tag SHAs, CI and
publisher run IDs/attempts, promotion manifest and artifact digests,
attestation verification, publication state, Homebrew PR/head/exact-merge and
current-tap ancestry state, both tap CI gates, health, and automatic
timing/retry metrics. Evidence must
not contain credentials, installation tokens, private keys, secret values, or
unredacted environment dumps.

The release workflows publish their evidence automatically. Do not maintain an
append-only narrative log and do not edit a successful machine document to
describe a later retry. A new workflow attempt produces a new exact-attempt
document; only a fully verified terminal tuple is indexed as successful.

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

See [`RELEASING.md`](../RELEASING.md) for the promotion, metrics, repair, and
post-release verification contracts.
