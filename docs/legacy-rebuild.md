# Legacy rebuild break-glass path

The steady-state publisher never rebuilds `v0.0.1` through `v0.0.7`. A rare
diagnostic rebuild uses the separate `legacy-rebuild.yml` workflow and the
versioned `releasectl release legacy-rebuild plan/apply` protocol. The workflow
cannot create or update a GitHub Release, upload release assets, attest an
artifact, update Homebrew, create a tag, or move a tag.

`v0.0.8` is deliberately outside this path. Its tag remains pinned to
`1d094f9e4a3e0343e713d4126f6118a8a9e98e2d` without a GitHub Release.

## Deterministic operation

Create a plan with the exact existing tag commit:

```sh
go run ./cmd/releasectl release legacy-rebuild plan \
  --repo ildarbinanas-design/env-vault \
  --version v0.0.7 \
  --source-sha 4fbae380747e75a1f59498adbd76ccf5791e0480 \
  --json > legacy-rebuild-plan.json
```

The plan binds the tag commit, the exact `main` control SHA, workflow identity,
and every existing release asset ID, name, size, and server-reported SHA-256.
Authentication, transport, rate-limit, schema, missing-digest, incomplete asset
matrix, and inaccessible-resource states fail closed.

`apply` is a dry run unless `--apply` is present. Both forms require the exact
`plan_digest` emitted by the plan:

```sh
go run ./cmd/releasectl release legacy-rebuild apply \
  --plan legacy-rebuild-plan.json \
  --plan-digest SHA256_FROM_PLAN \
  --json
```

The mutating form adds `--apply`. It re-reads every remote precondition before
dispatch, sends only `workflow_dispatch` for `legacy-rebuild.yml`, and does not
report success until the exact plan-digest run is visible through the Actions
API. Reapplying the same plan is a no-op after that exact run appears.

GitHub's workflow dispatch API has no caller-supplied idempotency key, so two
distributed callers can both receive an accepted dispatch before either run is
visible. Those runs share an exact plan-digest concurrency group. Only the first
run whose cheap preflight succeeds can allocate the five native runners: each
later run queries the complete (bounded) workflow-run inventory with
`actions: read`, verifies the exact run name/control SHA/workflow path, verifies
that the earlier run's `preflight` job succeeded, and exits as a deterministic
`legacy_exact_plan_already_started` no-op. API, schema, identity, pagination, or
job-inventory ambiguity fails closed. Thus duplicate dispatch records remain a
possible API-level race, but duplicate expensive work for one exact plan does
not. Avoiding even the duplicate dispatch record would require a separate
mutable distributed lock and additional write permission, which this
diagnostic-only path intentionally does not acquire.

To recover a failed or incomplete attempt, generate a new plan and dispatch a
new full workflow run; do not use “rerun failed jobs” and do not combine
artifacts across attempts.

## Proof boundary

The resulting artifacts are diagnostic and explicitly carry
`publication_eligible: false` and the stable reason code
`LEGACY_PROMOTION_PROOF_UNAVAILABLE`. All five native targets build in one workflow
run and one attempt with Go `1.22.12`, an exact tag/source check, checksum
verification, and the version surfaces supported by that tag. Artifact names
contain the workflow attempt, and the aggregate gate requires all five current-
attempt artifacts.

This is not a modern promotion proof:

- the historical commits do not contain the durable five-platform E2E contract;
- the exact Go patch version used for the original published bytes was not
  recorded, so a byte-for-byte provenance claim cannot be reconstructed;
- `v0.0.1` through `v0.0.3` predate the literal `--version` flag. The workflow
  verifies `version` and JSON version and records that the literal flag is
  unavailable; `v0.0.4` through `v0.0.7` verify all three surfaces.

Consequently no publication workflow accepts these diagnostic artifacts. A
future need to replace a legacy public asset would require a separate reviewed
publication plan with no-clobber, exact-source provenance/SBOM, attestations,
and Homebrew preconditions; absence of that plan is intentional fail-closed
behavior.

## Known tag commits

| Version | Exact commit |
| --- | --- |
| `v0.0.1` | `b9dd8826b3dca3a0f638df39797cb13d1eb10aa5` |
| `v0.0.2` | `595bf4fa7ca6a7346400e2243bc3b678f6767c5b` |
| `v0.0.3` | `4a8b11697d93829c364e0807d83fc87df2a2fd5a` |
| `v0.0.4` | `765627566f1d5ba175de017fe8ef3614a0408453` |
| `v0.0.5` | `1d927ce2828153e87399749b48656d8dbc9ce1f4` |
| `v0.0.6` | `76c9ac760b9d98752d737a1875339ac3ca2de0e5` |
| `v0.0.7` | `4fbae380747e75a1f59498adbd76ccf5791e0480` |
