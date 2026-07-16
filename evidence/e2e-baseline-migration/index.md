# Durable E2E baseline migration

Status: **machine-verifiable**

This compact bundle replaces the expiring historical-artifact dependency. It
contains the exact successful comparator output from workflow run
`29479484474`, attempt `1`; its byte SHA-256 is
`84a9f5f9d2e6b129f7dc1338db3c5d0b7fabd6577b62d6d2048a02a01dbcf293`.
The comparator records a passing equivalence check against historical run
`29441160687` for all five native platforms.

`migration.json` binds those exact bytes to the normalized facts in
`migrated-baseline.json` (file SHA-256
`4e2711bc621e8b5f43ba663f5a53bb4d023cbf2a7d700dfc192e7f9e3c807f0d`)
and to the reviewed one-line independent-sentinel transition. The transition
also moves the suite identity to the
domain-separated `env-vault.e2e-semantic-suite.v1` algorithm; runner,
normalization, validation, and renderer source remain hashed, while generated
reports are excluded and only the two reporter version strings are
canonicalized.

Offline verification:

```console
go run ./cmd/e2e-baseline verify-migration \
  --repository-root . \
  --contract release/contract.v1.json \
  --baseline evidence/e2e-baseline-migration/migrated-baseline.json \
  --migration evidence/e2e-baseline-migration/migration.json
```

Deterministic future update after a new five-platform matrix proof:

```console
go run ./cmd/e2e-baseline update \
  --repository-root . \
  --contract release/contract.v1.json \
  --proof reports/e2e/candidate/matrix-validation.json \
  --baseline docs/e2e-baseline.json \
  --diff-output reports/e2e/candidate/baseline.diff.json
```

Claims and risks:

- **pass** — comparator bytes, run tuple, platform set, validation outcomes,
  stable check list, tool versions, archived suite transition, and baseline
  facts are checked offline and fail closed. The verifier is purely archival:
  later reviewed suites are bound independently by their current baseline and
  matrix proof, and never rewrite this historical target hash.
- **pass** — no secret value is stored; the evidence contains only public run
  identities, digests, scenario results, coverage floors, and leak counts.
- **bounded risk** — GitHub's historical raw reports still expire. They are not
  required after this migration because their successful legacy validator and
  comparator result is preserved byte-for-byte; the proof intentionally does
  not reinterpret them under a newer validator.
