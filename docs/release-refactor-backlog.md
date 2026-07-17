# Release automation refactor backlog

This backlog records deliberately deferred release-automation work. None of the
items below is authorization to change product behavior, rebuild an immutable
release, relax a release invariant, or add an LLM-controlled decision plane.
Every proposal must land in a separate reviewed pull request and must preserve
the offline, fail-closed `releasecheck` boundary.

The measurements used for planning are the established baselines in
`release/metrics-baseline.v1.json`: main CI is 25 jobs / 387 seconds wall /
1,253 runner-seconds, pull-request CI is 25 jobs / 359 seconds wall / 1,205
runner-seconds, and a publication-eligible publisher is 30 jobs / 417 seconds
wall / 1,280 runner-seconds. Reduction estimates are targets, not claims about
work completed by the documentation release.

## 1. Dual-source read-only verification for immutable historical tags

- **Problem and evidence:** the current checker validates the current contract
  and can rebuild legacy diagnostics, but a historical tag may predate the
  current release contract. Failed immutable tags `v0.0.8` through `v0.0.11`
  must not move, while their historical tree and the current policy remain two
  distinct sources of evidence. A single-checkout operator is therefore easy
  to misuse when investigating an old tag.
- **Affected files/workflows:** a new release-only package under
  `internal/releasecontract` or `internal/releasehistory`, `cmd/releasecheck`,
  `release/contract.v1.json`, `legacy-rebuild.yml`, and releasecheck tests.
- **Guarantee preserved:** tags are read only, blocked Release absences remain
  permanent, historical code is never promoted, and current policy is the only
  authorization source.
- **Proposed architecture:** accept two explicit local inputs: an exported tree
  for the immutable tag and a current, separately checksummed contract/checker
  bundle. Emit one schema-versioned comparison document containing both source
  SHAs and an `observation_only: true` capability. The command must contain no
  GitHub transport and no mutation action codes.
- **Expected reduction:** no CI job reduction initially; target 80-150 fewer
  lines of ad-hoc operator shell and 30-60 seconds less manual investigation per
  legacy incident. A later reusable job may replace one diagnostic job.
- **Risk:** confusing historical behavior with current policy, or accidentally
  treating a reproducible rebuild as publication eligible.
- **Required tests:** swapped inputs, mutable ref rejection, missing tree,
  current-contract downgrade, blocked-tag SHA mismatch, absent-Release proof,
  schema downgrade, and an assertion that every output action is read only.
- **Dependencies and order:** first define the two-input schema; then add the
  offline checker; last wire an observation-only workflow. Do not add an
  operator plane in the first implementation.
- **Acceptance criteria:** the same invocation can verify `v0.0.8` from a clean
  export while proving its GitHub Release remains absent; no token is available
  to the checker; mutation and promotion are structurally impossible.

## 2. Generated recovery state machine and transition proofs

- **Problem and evidence:** the one-time `v0.0.12` recovery required a pinned
  incident identity, a pinned successful `v0.0.13` source, temporary planning
  steps, and adversarial tests spread across contract, Go, shell, and workflow
  files. Hand-maintained pins are appropriate for the incident but do not scale
  to another recovery without duplicated logic.
- **Affected files/workflows:** `release/contract.v1.json`,
  `internal/releasecontract/recovery.go`, `cmd/releasecheck`,
  `release-please.yml`, and recovery/operator tests.
- **Guarantee preserved:** transitions are declarative and offline; incident
  identity is immutable; `complete` cannot roll back; blocked tag and Release
  absences remain mandatory.
- **Proposed architecture:** define an append-only transition record with
  `from_state`, `to_state`, exact source identity, reason code, and a canonical
  semantic digest. Generate validators and workflow predicates from one typed
  transition table. Keep permanent incident fields in the canonical contract.
- **Expected reduction:** 150-250 release-only LOC and 2-4 duplicated workflow
  steps in a future incident; no steady-state job reduction.
- **Risk:** a generic transition engine could accidentally make an exceptional
  path look routine or accept an unreviewed state edge.
- **Required tests:** all disallowed state pairs, missing/null/unknown fields,
  duplicate transitions, digest mismatch, historical incident mutation, and
  network/credential absence.
- **Dependencies and order:** land a schema ADR, then generator golden tests,
  then migrate the completed `v0.0.12` record without changing its semantic
  digest or guarantees.
- **Acceptance criteria:** adding a hypothetical incident requires data plus
  golden fixtures, not workflow conditionals; an undeclared edge returns stable
  `INPUT_INVALID`/exit 5; the `v0.0.12` canonical output is unchanged.

## 3. Reusable GitHub App identity and installation-scope audit

- **Problem and evidence:** Homebrew App identity/scope checks are repeated in
  `build-binaries.yml` and `audit-release-app.yml`; the standalone tap audit
  currently proves the single-repository scope, while the publisher separately
  proves the `env-vault-tap-release` slug. The release-planning audit has another
  similar implementation.
- **Affected files/workflows:** `audit-release-app.yml`,
  `audit-release-planning-app.yml`, `build-binaries.yml`,
  `release-please.yml`, `release/contract.v1.json`, and workflow tests.
- **Guarantee preserved:** short-lived tokens, exact App slug, exactly one
  allowed repository, least permissions, no token or private-key output, and
  automatic token revocation.
- **Proposed architecture:** a pinned reusable workflow accepts only a contract
  App ID and a permission profile, mints the minimum token, and emits a small
  non-secret JSON proof. Callers validate the proof's contract digest and exact
  slug/repository tuple before any mutation.
- **Expected reduction:** 100-180 workflow LOC, 2 duplicated setup/audit jobs,
  and 10-25 runner-seconds per publisher if the preflight proof is safely reused
  within the same attempt.
- **Risk:** broad reusable-workflow inputs or proof reuse across attempts could
  expand token authority or stale the scope observation.
- **Required tests:** wrong slug, zero/two repositories, permission addition,
  cross-attempt proof, malformed JSON, secret redaction, post-step revocation,
  and contract digest mismatch.
- **Dependencies and order:** define the proof schema first; migrate read-only
  audit workflows; only then consume the same implementation in publisher and
  planning mutations.
- **Acceptance criteria:** both standalone audits and both mutating workflows
  validate the same typed proof; changing slug, scope, or permissions fails
  before repository mutation; logs contain no credential material.

## 4. Typed GitHub transport and CLI compatibility boundary

- **Problem and evidence:** release scripts individually combine `gh api`,
  pagination, retry, jq, and shell parsing. Operator history includes a CLI
  flag incompatibility (`gh config set --hostname` versus `-h`), sandbox DNS
  denial, and a current `gh` behavior where `--slurp` cannot be combined with
  `--jq`. Blindly replaying mutations after transport ambiguity is prohibited.
- **Affected files/workflows:** `scripts/release/gh-api-read.sh`, release shell
  scripts, operator documentation, and transport/workflow tests.
- **Guarantee preserved:** reads may use bounded retries; mutations are never
  blindly retried; JSON is atomic and schema checked; credentials never enter
  evidence.
- **Proposed architecture:** a small release-only transport executable reports
  a versioned capability/preflight JSON, performs atomic read pagination, and
  classifies authentication, permission, rate-limit, sandbox/DNS, and malformed
  response failures with stable codes. Mutations remain explicit commands with
  postcondition probes and idempotency identities.
- **Expected reduction:** 200-350 shell LOC, 20-40 seconds of repeated setup per
  full release, and fewer non-deterministic operator retries; no required job
  reduction.
- **Risk:** central transport code becomes security critical and could hide a
  partial mutation if read and write policies are conflated.
- **Required tests:** recorded HTTP fixtures, truncated pagination, retry-after,
  DNS denial, invalid keyring token without token disclosure, HTTP 401/403/429,
  post-write timeout, CLI capability drift, and atomic-output interruption.
- **Dependencies and order:** specify codes and exit statuses; implement reads;
  migrate one verifier at a time; design mutation postconditions only after read
  parity is proven.
- **Acceptance criteria:** every release script consumes the same preflight and
  error schema; unsupported CLI syntax fails before action; a transport-unknown
  mutation is classified for inspection rather than retried.

## 5. Consolidated promotion-manifest and artifact inventory engine

- **Problem and evidence:** promotion, asset reconciliation, exact-version
  inventory, provenance, SBOM, and attestations cross several Go commands,
  shell scripts, jq filters, and publisher jobs. The publisher baseline is 30
  jobs and 1,280 runner-seconds, with repeated downloads and identity parsing.
- **Affected files/workflows:** `build-binaries.yml`,
  `internal/releasepromotion`, `internal/releasectl`,
  `scripts/release/reconcile-release-assets.sh`, attestation scripts, and their
  tests.
- **Guarantee preserved:** one workflow attempt, five exact native targets, ten
  no-clobber assets, source-bound digests, exact versions, SBOM and provenance
  attestations, and immutable Release assets.
- **Proposed architecture:** one typed inventory library consumes the promotion
  manifest and local asset directory once, then emits separate signed/checksummed
  stage proofs. Jobs pass proofs by artifact identity rather than re-downloading
  and reparsing assets.
- **Expected reduction:** 3-5 publisher jobs, 80-140 wall seconds, 250-400
  runner-seconds, 150-300 shell LOC, and one full asset download/upload cycle.
- **Risk:** over-consolidation can reduce platform independence or turn one
  corrupt proof into a single point of failure.
- **Required tests:** mixed attempts, duplicate/missing assets, checksum line
  endings, archive traversal, wrong embedded version, wrong source SHA,
  attestation subject mismatch, proof tampering, and no-clobber replay.
- **Dependencies and order:** freeze existing proof schemas; implement parity
  readers; dual-run old/new read-only verification; then remove duplicated jobs.
- **Acceptance criteria:** all current adversarial fixtures produce the same
  stable codes; five builders stay independent; final metrics meet the target
  without weakening any inventory or attestation check.

## 6. CI graph reduction with native-gate preservation

- **Problem and evidence:** main and PR CI each use 25 jobs and more than 1,200
  aggregate runner-seconds. Some release-only validation/setup is repeated even
  when inputs are identical, while five native jobs, `e2e-gate`, and the top
  `quality-gate` must remain independently visible.
- **Affected files/workflows:** `ci.yml`, `reusable-quality.yml`, test bootstrap
  scripts, workflow tests, and metrics baselines.
- **Guarantee preserved:** all five native jobs, exact platform semantics,
  product E2E coverage, fail-closed aggregation, and required-check names.
- **Proposed architecture:** materialize one source/test-plan artifact, share
  hermetic test-only tooling, collapse only pure Linux release-contract shards,
  and keep the five native jobs plus named gates intact.
- **Expected reduction:** 3-6 jobs, 60-110 wall seconds, 250-450 runner-seconds,
  and 80-150 workflow LOC for both PR and main CI.
- **Risk:** hidden fan-in dependencies or runner-specific behavior can make a
  green aggregate mask a skipped native check.
- **Required tests:** workflow graph golden tests, every native job forced to
  fail once, cancelled/skipped propagation, cache poisoning, artifact identity,
  and unchanged required-check tuples.
- **Dependencies and order:** add graph/metrics assertions; remove one duplicate
  shard at a time; update the baseline only from successful main and PR runs.
- **Acceptance criteria:** five native jobs, `e2e-gate`, and `quality-gate` are
  present and green; no test count drops; measured successful runs improve both
  wall and aggregate time by the stated target.

## 7. Hermetic test-tool bootstrap for offline jobs

- **Problem and evidence:** the initial `v0.0.13` path exposed test-only
  `gotestsum` dependencies while `GOPROXY=off`. Production dependencies were not
  the problem, but the test reporter bootstrap was not represented as a
  hermetic input.
- **Affected files/workflows:** reporter bootstrap scripts, `ci.yml`,
  `reusable-quality.yml`, `build-binaries.yml`, tool manifests, and reporter
  tooling tests.
- **Guarantee preserved:** production module graph is unchanged, offline jobs
  make no network access, tool versions and digests are pinned, and reports stay
  deterministic.
- **Proposed architecture:** build pinned test reporters once in a networked,
  checksummed bootstrap boundary or check in a standard Go `tools` manifest;
  publish a source-bound tool bundle consumed with `GOPROXY=off` everywhere
  else.
- **Expected reduction:** 1-2 jobs or 20-45 runner-seconds per full CI/publisher
  cycle and elimination of repeated tool compilation; 50-100 shell LOC.
- **Risk:** cross-platform tool binaries or stale tool bundles could produce
  inconsistent reports.
- **Required tests:** empty module cache, `GOPROXY=off`, wrong tool digest,
  source mismatch, all runner architectures, report byte stability, and no
  production `go.mod` dependency change.
- **Dependencies and order:** choose source-built or per-platform bundles;
  establish signed digests; dual-run report comparison; then remove old setup.
- **Acceptance criteria:** every offline job starts from an empty cache and
  succeeds without network; reporter output matches the current schema; product
  module dependencies are byte-for-byte unchanged.

## 8. Durable evidence collector independent of publisher conclusion

- **Problem and evidence:** release-evidence run `29557533919` was skipped when
  the `v0.0.13` repair publisher concluded failure in health, even though the
  Release, ten assets, attestations, and Homebrew state were already correct.
  This history must never be rewritten as successful evidence.
- **Affected files/workflows:** `release-evidence.yml`, `build-binaries.yml`,
  `internal/releaseevidence`, evidence schemas, and publisher/evidence tests.
- **Guarantee preserved:** only a completely successful health-verified attempt
  is publication-eligible evidence; partial attempts remain explicit failures;
  evidence is source/attempt bound and contains no secrets.
- **Proposed architecture:** always run a read-only collector after publisher
  completion, emit either a complete proof or a typed incomplete-attempt record,
  and allow the durable publication step only when the collector proves every
  required stage from one eligible attempt. This improves diagnosis without
  upgrading a failed release.
- **Expected reduction:** no job reduction; 10-30 minutes less incident
  reconstruction and 50-100 fewer lines of manual evidence gathering. A shared
  collector may remove one later repair-only job.
- **Risk:** operators may mistake diagnostic evidence for successful durable
  release evidence unless capability and eligibility fields are unmistakable.
- **Required tests:** failed publisher with valid assets, skipped health,
  successful health, cross-attempt mixing, replay, missing Homebrew CI, secret
  pattern scan, and durable-write prohibition for incomplete inputs.
- **Dependencies and order:** define the incomplete-attempt schema and UI
  wording; add collector-only runs; only then consider changing trigger logic.
- **Acceptance criteria:** every publisher attempt has machine JSON; failed
  attempts say `publication_eligible: false`; only one fully green attempt can
  create the durable release-evidence record.

## Suggested implementation order

1. Add metrics/graph assertions and the typed GitHub transport read boundary.
2. Make test-tool bootstrap hermetic.
3. Consolidate App audits and promotion inventory with parity dual-runs.
4. Reduce the CI/publisher graph using measurements from successful runs.
5. Add the diagnostic evidence collector.
6. Generalize recovery transitions only after the completed `v0.0.12` incident
   has remained stable through at least one fully green later release.
7. Implement dual-source historical verification last, as a read-only tool with
   no operator plane.

Each step requires its own before/after successful-run metrics and a product-path
diff proving that `cmd/env-vault`, `internal/config`, `internal/secretstore`,
`internal/runner`, `internal/output`, product E2E scenarios, and release binary
behavior are unchanged.
