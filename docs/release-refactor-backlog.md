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
- **Affected files/workflows:** the existing closed router under
  `internal/releasecontract`, `cmd/releasecheck`,
  `release/history/contract.v1.json`, `release/contract-history.v2.json`,
  `legacy-rebuild.yml`, and releasecheck tests. Stage 4 completed exact routing
  for published `v0.0.14`-`v0.0.16`; this item remains only for the older
  observation-only failed-tag export workflow.
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
- **Affected files/workflows:** `release/contract.v2.json`,
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
  `release-please.yml`, `release/contract.v2.json`, and workflow tests.
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

**Implemented in refactor Stage 2 (2026-07-17).** The accepted design is
recorded in [ADR 0002](adr/0002-release-github-transport.md). Operational
source moved from 44 direct `gh api` sites (35 REST reads, one GraphQL read,
and eight mutations) to nine registered sites: zero direct REST reads, the
same eight explicit mutations, and the same one read-only GraphQL ruleset
observation. Bounded helper usage grew from
37 sites in six callers to 62 sites in 19 callers, and 17 operational call
sites now resolve typed Actions run/job/attempt identity. The checked registry
is `release/github-transport-boundary.v1.json`; additions or count drift fail
tests.

The predicted 20-40 second workflow setup reduction cannot be claimed before
an exact successful Actions comparison. Two single observed local runs of
`go test ./tests -run '^TestPublishReleaseEvidenceIsNoClobberAndRaceSafe$' -count=1`
changed from 91.251 seconds to 14.978 seconds (-76.273 seconds, -83.6%) after
removing per-call Go cold builds. Host/tool/cache metadata and repetitions were
not recorded; this is directional evidence, not a benchmark, hosted-runner
metric, or release-pipeline metric.

The same baseline line definitions show that env-vault workflows changed from
10 files / 25 jobs / 3,646 physical / 3,395 nonblank lines to 10 / 25 / 3,847 /
3,581 (+201/+186 lines). Release shell/jq changed from 28 files / 4,450 physical
/ 4,008 nonblank to 29 / 4,521 / 4,083 (+1 file, +71/+75 lines). The typed
transport adds nine Go files / 2,833 physical / 2,669 nonblank lines (1,852
physical non-test and 981 physical test); `internal/strictjson/strictjson.go`
adds 15 physical / 14 nonblank lines. Therefore the original 200-350 shell-LOC
reduction was not achieved in Stage 2. Release engineering owns cleanup and
graph consolidation in Stage 4; these current measurements are not folded into
or substituted for the immutable baseline JSON.

- **Problem and evidence:** release scripts individually combine `gh api`,
  pagination, retry, jq, and shell parsing. Operator history includes a CLI
  flag incompatibility (`gh config set --hostname` versus `-h`), sandbox DNS
  denial, and a current `gh` behavior where `--slurp` cannot be combined with
  `--jq`. Investigation of evidence run `29563754061` additionally showed that
  Actions REST `.name` can expose a custom `run-name`, while the exact
  successful PR run `29562392602` has `.pull_requests: []` after PR #39 was
  merged. Evidence publish run `29566800374` then showed that Git Blobs API
  `.content` is line-wrapped base64, unlike the original unwrapped fake. Blindly
  replaying mutations after transport or identity ambiguity is prohibited.
- **Affected files/workflows:** `scripts/release/gh-api-read.sh`, release shell
  scripts, `release-evidence.yml`, release/planning/publisher observers,
  operator documentation, and transport/workflow tests.
- **Guarantee preserved:** reads may use bounded retries; mutations are never
  blindly retried; JSON is atomic and schema checked; credentials never enter
  evidence.
- **Implemented bounds and residual:** each page has at most five attempts; one
  read has at most 100 pages, 500 REST requests, and 120 seconds cumulative
  retry wait; each `gh` process permits at most 64 MiB stdout and 256 KiB
  stderr. Stage 4 added a 60-second request-process deadline, 300-second public
  operation deadline, and 256 MiB aggregate response budget. Every attempt is
  charged before classification, including malformed, incomplete,
  rate-limited, and retryable responses. Capability singleflight waiters honor
  cancellation. Pagination still preserves the complete initial query scope
  and exactly consecutive canonical `page` controls.
- **Proposed architecture:** a small release-only transport executable reports
  a versioned capability/preflight JSON, performs atomic read pagination, and
  classifies authentication, permission, rate-limit, sandbox/DNS, and malformed
  response failures with stable codes. Add a typed Actions run identity record
  based on exact run ID/attempt, workflow path, event, head SHA/ref, and the
  originating required-check URL/job ID where applicable. Cross-check the job
  endpoint's run ID/attempt, head SHA, name, workflow, result, and canonical URL;
  REST `.name` and `.pull_requests` remain diagnostic-only. Model Git blob
  payloads as typed `{sha,encoding,size,content}` records: remove only CR/LF
  transport wrapping, decode fail-closed, and compare declared size and exact
  bytes. Mutations remain explicit commands with postcondition probes and
  idempotency identities.
- **Original target:** 200-350 shell LOC, 20-40 seconds of repeated setup per
  full release, and fewer non-deterministic operator retries; no required job
  reduction. Stage 2 did not meet the shell-LOC target and has no Actions timing
  claim; the verified source counts and single-run local test observation are
  recorded above.
- **Risk:** central transport code becomes security critical and could hide a
  partial mutation if read and write policies are conflated.
- **Required tests:** recorded HTTP fixtures, truncated pagination, retry-after,
  DNS denial, invalid keyring token without token disclosure, HTTP 401/403/429,
  post-write timeout, CLI capability drift, atomic-output interruption, custom
  workflow `run-name`, merged-PR empty associations, stale/wrong check URL/job,
  wrong run attempt, direct-head mismatch, realistic line-wrapped Git blobs,
  malformed/trailing base64, missing/extra padding, noncanonical pad bits,
  declared-size mismatch, and byte mismatch.
- **Dependencies and order:** specify codes and exit statuses; implement reads;
  migrate one verifier at a time; design mutation postconditions only after read
  parity is proven.
- **Acceptance criteria (met):** every release script consumes the same
  preflight and error/identity schema; unsupported CLI syntax fails before action; custom
  names and missing post-merge associations cannot break a valid exact tuple;
  a transport-unknown mutation is classified for inspection rather than
  retried.

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
- **Implemented incident hardening (2026-07-18):** valid `assets: []` shape is
  now distinct from an empty extracted name stream. Reconciliation accepts it
  as ten missing assets, while the complete downloader still requires exactly
  ten. Every upload refreshes inventory; ambiguous outcomes require the exact
  one-name delta and byte read-back without retry. Realistic tests cover an
  empty new Release, null/wrong-type/unsafe/duplicate/unexpected inventories,
  no-clobber, divergence, and concurrent/ambiguous mutation. ADR 0004 adds a
  source-bound two-asset bootstrap only because an immutable tag cannot consume
  the fixed parser.
- **Residual:** the exceptional bootstrap and steady-state reconciler still
  express related inventory/postcondition rules in shell. The proposed typed
  inventory engine should absorb both only after parity tests preserve the
  exact zero/two/ten-state transitions; this incident is not justification to
  collapse the five native builders or broaden mutation permissions.
- **Implemented follow-up hardening (2026-07-18):** a later Homebrew failure
  showed that the public Attestations endpoint can return an informational
  RFC `Link` with only `rel="deprecation"`. Non-paginated reads now ignore Link
  metadata, while paginated reads parse complete link-values and follow only a
  unique unanchored trusted invariant-preserving `next`. The protected-main
  Homebrew bridge verifies the already-correct ten assets, bootstrap result,
  exact attestations, failed publisher graph, formula parity, and unpublished
  tap state before the existing scoped App PR/CI/merge path. Its incident tuple
  remains dispatch data and its typed result points only to a tag-scoped health
  repair. ADR 0005 records the recovery boundary.
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

## 9. Event-aware required-check observability without trigger weakening

- **Problem and evidence:** Release Please updates can emit `synchronize` and
  `edited` within one second for the same PR head. For PR #39 this produced
  `pr-title` run `29562392487`, cancelled before steps, followed by successful
  run `29562393511`. In the Actions list, CI, Dependency Review, and title
  checks share the release PR title, which looks like duplication despite
  representing different workflows; CodeQL uses the distinct display title
  `PR #39` but still appears as another row for the same PR/head. In the 55-run
  cohort observed through 2026-07-17T07:13Z, 43 head SHAs were unique and 12
  had a second run; six immediate cancelled/success pairs were Release Please
  proposals.
- **Affected files/workflows:** `pr-title.yml`, optional shared run-name
  conventions, workflow graph tests, and operator documentation.
- **Guarantee preserved:** `synchronize` must validate every new head;
  `edited` must invalidate a title changed without a new commit; `pr-title`
  remains a strict required check; cancelled stale runs never count as green.
- **Proposed architecture:** add event/action/head context to `run-name` and
  machine audit output while keeping the current concurrency cancellation.
  An in-workflow or reusable coalescer cannot prevent GitHub from creating a
  run record for each producer event; eliminating the record would require
  removing/filtering a trigger or an external status producer, neither of
  which is justified by the current evidence.
- **Expected reduction:** safe phase: 0 jobs, 0 wall/runner seconds, and 0 LOC
  removed; expect 1-5 workflow LOC added for `run-name`, with materially faster
  UI classification. No run-count reduction is claimed.
- **Risk:** removing either trigger or cancelling the wrong run can leave the
  new SHA without its required context, or allow a valid title to be edited to
  an invalid one after the last check.
- **Required tests:** synchronize-only, title-only edit, body-only edit,
  simultaneous synchronize/edit in both orders, cancelled predecessor,
  required-context presence on the current SHA, and a post-success invalid
  title edit.
- **Dependencies and order:** land run-name/telemetry only; collect several
  Release Please cycles; model GitHub check-suite/ruleset behavior; retain both
  triggers unless a separate design proves equivalent required-status
  semantics before implementation.
- **Acceptance criteria:** Actions UI identifies workflow/event/action without
  opening a run; every current head has one successful canonical `pr-title`
  context; title edits are always revalidated; no required-check or ruleset
  weakening occurs.

## 10. Compact content-addressed offline evidence bundle

**Implemented in refactor Stage 3 (2026-07-17).** The accepted format and
migration decision are recorded in [ADR 0003](adr/0003-compact-release-evidence-ledger.md).
The source capability document selects v1 only when both new keys are absent
and v2 only for exact bundle/genesis versions. The seven-day handoff dual-writes
v1 and v2; the durable v2 tuple and 90-day artifact omit the monolith after
byte-exact reconstruction, semantic replay, and success parity. Failure parity
is executable API/CLI coverage rather than a misleading `parity.json` file.

Objects are raw-SHA-addressed and use a canonical stored-DEFLATE gzip encoding
with independent compressed digest/size binding. Strict directory shape,
no-clobber publication, decompression and aggregate limits, missing/extra
object rejection, and network/credential-independent offline replay are tested.
The six metadata files and byte-domain definitions are stable and explicit.

Replay of immutable evidence commit
`af521d52b898088cb49f6256964e377e33e95a5d` produced a 1,887-byte root, 6,944
auxiliary metadata bytes, a 593-byte parity record, a 1,468-byte self-report,
and three objects totaling 357,677 raw / 357,766 encoded bytes. Root-plus-
attempt logical payload changed from 2,964,270 to 379,550 bytes (-87.1%);
deterministic export changed from 1,486,981 to 374,320 bytes (-74.8%). Unique
Git blob payload is 368,658 bytes and complete offline reconstructed payload is
368,569 bytes. These scopes are not interchangeable with transfer, billing, or
compression-ratio metrics.

**Residual hardening backlog:** commit a small frozen historical-v1 golden
fixture in addition to generated compatibility fixtures. It must contain no
secret or credential material and must prove stable reconstruction/error codes
without treating the dated production evidence SHA as an operational constant.

## 11. Automatic evidence-only ledger genesis

**Implemented in refactor Stage 3 (2026-07-17).** Only an exact typed HTTP 404
enables genesis. The publisher re-observes the exact source immediately before
each mutation, creates blobs, creates a tree without `base_tree`, creates a
parentless commit, and creates the ref without force. The closed initial tree
contains only evidence paths and `evidence/genesis.v1.json`; it never inherits
source workflow files. Ambiguous ref creation and concurrent creation are
accepted only after exact read-back reconciliation. Contents write remains the
only write permission; Workflows write, PAT/App expansion, and bypasses remain
forbidden.

The versioned genesis anchor binds repository, first version/source and bundle,
publisher run/attempt/repair, and evidence workflow run/attempt. Its self-digest
proves internal integrity, while authenticity remains external: protected
exact ref, reviewed workflow/checker, and independently observed release tuple.
The existing production history stays `legacy-compatible`; Stage 3 neither
rewrites it nor retrofits an anchor. Its immutable baseline and the dated live
ruleset/ref observations are documented separately from operational inputs.

Both anchored and legacy lineages have a 64-commit validation window. Exact
no-op replay is allowed at depth 64; append is rejected before any mutation.
The two-commit production baseline had 62 append slots before this stage and is
expected to have 61 after the next successful patch.

**Residual architecture backlog:** design and review a checkpoint or Merkle
summary before the validation window is exhausted. It must preserve historical
root/source binding and complete offline verification. Also replace the oldest
accepted legacy parent/source observation with a dynamic, independently bound
proof before removing any compatibility fixture. Neither follow-up authorizes
rewriting the current branch.

## 12. Versioned operational release contract and workflow parity

**Implemented in refactor Stage 4 (2026-07-18).** The accepted trust-domain
decision is recorded in
[ADR 0006](adr/0006-versioned-operational-release-contract.md).
`release/contract.v2.json` now owns current repository/default-branch, version,
tag, naming, target, asset, Homebrew, workflow, concurrency, App/environment,
and required-check identities. Runtime mutation consumers require a dedicated
same-checkout checker to strictly validate the exact local contract and
byte-for-byte corroborate its freshly generated checker-version and operational
projection documents. Self-declared digests cannot authorize different bytes.
The operator wrapper builds one private checker, overrides caller projection
variables, and cleans up on success, failure, and signals; jq fallback remains
read-only and non-authoritative.

Historical v1 authority is closed to exact archived bytes and exact
repository/version/source tuples in `release/contract-history.v2.json`.
Contract generation and evidence format are separate: `v0.0.14` and `v0.0.15`
route v1/v1, while `v0.0.16` routes v1/v2. Arbitrary in-memory v1 documents and
the live transition file have no compatibility authority. Frozen capability
fixtures and the compact `v0.0.16` bundle verify that route fully offline.

Static parity tests bind Actions fields that cannot consume runtime shell:
workflow trigger names/branches/tags, exact events/jobs, five shared release
concurrency participants, App-token environments/repositories, required-check
identity, and the five provenance/SBOM subject archives. Five repeated
publisher activation bodies were reduced to one checked helper without
removing the cross-job artifact boundary.

Measured from Stage 3 base `ce1ba7186a4d3133fb04075f275f06e6042c0ccb`,
workflow files/jobs remain 12/27 while workflow source grows 5,245/4,924 to
5,926/5,585 physical/nonblank lines. Release shell/jq grows from 33 files and
6,089/5,554 lines to 35 and 6,457/5,905. Contract Go grows from 6 files and
2,521/2,377 lines to 12 and 4,024/3,812; transport Go remains 11 files and grows
4,060/3,838 to 4,729/4,464. Raw live-v1 literal occurrences fall 27 to 6 (all
historical source routing), and exact repository literals fall 12 to 2 (both
Go module linker paths). Product implementation paths are unchanged. These are source
measurements, not a runtime speedup claim.

**Genuine follow-ups only:**

- record exact hosted main/PR/publisher wall and runner metrics from the first
  successful v2-contract release and compare them with the immutable baseline;
- keep the residual checkpoint/Merkle work in item 11 before the 64-commit
  evidence validation window is exhausted;
- keep the failed legacy-tag export in item 1, generic recovery transitions in
  item 2, reusable App proof in item 3, inventory engine in item 5, and any CI
  job reduction in item 6 as separately reviewed changes;
- do not remove the live v1 transition file until no supported immutable source
  needs its source-tree path; never remove or rewrite the archived v1 bytes or
  tuple registry.

## Suggested implementation order

1. Record exact hosted metrics from the first successful v2-contract release;
   retain the immutable v1 and pre-refactor comparators.
2. Design the evidence checkpoint/Merkle transition before the bounded history
   window becomes operationally tight; keep legacy history immutable.
3. Consolidate App audits and promotion inventory only through typed-proof
   parity dual-runs.
4. Make the remaining test-tool bootstrap hermetic.
5. Reduce the CI/publisher graph only from successful-run measurements while
   retaining five native jobs and both named gates.
6. Add event-aware run names and measure title-check event fan-out without
   changing triggers.
7. Add the diagnostic evidence collector.
8. Generalize recovery transitions only after the completed `v0.0.12` incident
   has remained stable through at least one fully green later release.
9. Implement dual-source historical verification last, as a read-only tool with
   no operator plane.

Each step requires its own before/after successful-run metrics and a product-path
diff proving that `cmd/env-vault`, `internal/config`, `internal/secretstore`,
`internal/runner`, `internal/output`, product E2E scenarios, and release binary
behavior are unchanged.
