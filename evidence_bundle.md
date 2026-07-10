# env-vault Evidence Bundle

## Task ID: `ENV-VAULT-RELEASE-APP-CUTOVER-CHECKPOINT`

Timestamp UTC: `2026-07-10T22:30:47Z`

### Scope

Replace the Homebrew deploy-key direct push with a least-privilege GitHub App
pull-request flow, apply the approved external release settings, publish the
reviewed repository changes through pull requests, and establish exact tap CI
evidence before the `v0.0.6` release. This checkpoint precedes the App scope
audit and version publication. No existing tag was moved, no Release asset was
replaced, and the preserved manual-binary backup was not accessed or changed.

### Changes

| Repository/file or setting | Purpose |
|---|---|
| `build-binaries.yml`, `publish-homebrew-pr.sh`, `merge-homebrew-pr.sh`, `wait-tap-ci.sh` | Create/reuse one deterministic formula PR, bind it to version/source/formula digest, wait exact PR CI, merge the unchanged head without bypass, and wait exact post-merge CI |
| `audit-release-app.yml` | Mint a metadata-only token and fail unless the App installation contains exactly `homebrew-tap` |
| release-script and workflow regression tests | Cover monotonic/no-op/partial PR state, markers and formula bytes, merge head drift, exact workflow/SHA/event, timeout, malformed API data, and removal of the direct-push path |
| `reusable-quality.yml`, `ci.yml` | Run the pinned license scanner with Go 1.23 on Linux/macOS/Windows, avoid duplicate PR branch runs, and expose one stable `quality-gate` required-check context |
| `internal/releasearchive` | Add a source-local conservative double-dot barrier recognized by CodeQL before tar/zip names reach filesystem helpers |
| `README.md`, `RELEASING.md`, `docs/design.md`, `docs/release-external-settings.md` | Document the implemented App/PR flow, exact CI evidence, repair behavior, scope audit, credential rotation, and rollback |
| GitHub App `env-vault-homebrew-release` | Dedicated private App with Actions read, Contents write, Pull requests write, no webhook, and no unrelated account permissions |
| `env-vault` environment `release` | Store only `TAP_APP_CLIENT_ID` and `TAP_APP_PRIVATE_KEY`; allow deployment only from `main` and tags matching `v*` |
| `homebrew-tap` ruleset `Protect Homebrew tap main` | Require a PR, squash, the GitHub Actions `test` context, conversation resolution, and block force-push/deletion with no bypass actor |

### Commands And Results

| Command or action | Result | Claim status |
|---|---|---|
| Context7 and official GitHub Actions, GitHub CLI, GitHub App token, environment, ruleset, and CodeQL documentation | passed; current v3 `client-id`, explicit permissions, exact run filters, merge head guard, and path barrier were used | doc_verified |
| Expired local `gh` authorization removal and browser OAuth login | passed; API identity is `ildarbinanas-design`; credential value was not printed or persisted in repository files | cli_observed |
| `release` environment and deployment-policy API verification | passed; custom policies are exactly branch `main` and tag `v*`; environment contains the expected variable/secret names | cli_observed |
| Homebrew tap PR [#1](https://github.com/ildarbinanas-design/homebrew-tap/pull/1) | passed required `test`; squash-merged without bypass at `bc8477a95437da3d171c14db306b51ef220ae963` | remote_observed |
| Exact post-merge tap run [29127628637](https://github.com/ildarbinanas-design/homebrew-tap/actions/runs/29127628637) | passed for `workflow=test-formula.yml`, `event=push`, and the exact merge SHA | remote_observed |
| env-vault PR [#9](https://github.com/ildarbinanas-design/env-vault/pull/9) | 21 checks passed: test, vet, race, smoke, five builds, five native version smokes, three native license scans, stable quality gate, dependency review, and CodeQL | remote_observed |
| Initial native license jobs | failed closed on all OS because `go-licenses v2.0.1` requires Go 1.23 while the project compatibility toolchain is Go 1.22 | remote_observed |
| Explicit license runtime fix | passed locally and in all three rerun jobs; project test/build compatibility remains on `go.mod` | remote_observed |
| CodeQL Zip Slip review threads | initial custom sanitizer was not modeled; source-local double-dot guards and tests added; rerun passed and both bot threads auto-resolved | remote_observed |
| `go test ./...`, targeted vet, ShellCheck, pinned license scan, workflow regression tests, and `git diff --check` | passed at this checkpoint | cli_observed |

### Claims

| Claim | Status | Evidence |
|---|---|---|
| Release automation no longer contains an SSH direct push to tap `main` | repo_verified | no `TAP_DEPLOY_KEY`, `ssh-keyscan`, deploy-key file, or `HEAD:main` path remains in the workflow; regression tests reject reintroduction |
| The App write token is explicitly scoped to `homebrew-tap` and the minimum three permissions | repo_verified | token action v3 inputs name one repository and Actions read, Contents write, Pull requests write |
| A release cannot be healthy from a PR check or checks-page link alone | repo_verified | Homebrew waits the exact merged SHA's successful push run and passes its URL/SHA to health |
| `repair=health` cannot read the App private key or mutate the tap | repo_verified | health skips the environment-backed Homebrew job and uses public/read-only state |
| Native license scanning is executable on every configured runner | remote_observed | Linux, macOS, and Windows jobs passed with the explicit Go 1.23 tool runtime |
| Archive traversal names are rejected before archive data reaches filesystem helpers | remote_observed | source-local tar/zip barrier, traversal/over-rejection tests, and successful CodeQL rerun |

### Remaining Cutover Steps And Risks

| Item | Status | Required action or mitigation | Claim status |
|---|---|---|---|
| Prove App installation selection after it was corrected from all repositories to selected repositories | pending | Merge `audit-release-app.yml`, dispatch it, and require its metadata-only exact-one-repository check to pass before release | planned |
| Publish and health-check `v0.0.6` | pending | Merge env-vault PR #9, run the App scope audit, dispatch `build-binaries` from `main`, and verify exact tag/Release/assets/attestations/tap PR/tap SHA/tap CI | planned |
| Remove legacy `TAP_DEPLOY_KEY` and matching tap deploy key | pending | Delete both only after the first App-based release is healthy, then verify no remaining reference or writer | planned |
| Parent-directory symlink replacement between `Lstat` and file creation is not prevented by lexical archive-name checks | accepted | release extraction runs in a private ephemeral runner output directory; a hostile concurrent local process is outside this workflow threat model; a future generalized extractor should use dirfd/openat `O_NOFOLLOW` or `os.Root` | repo_verified |

## Task ID: `ENV-VAULT-RELEASE-PROCESS-STAGE-5`

Timestamp UTC: `2026-07-10T21:44:47Z`

### Scope

Implement the remaining release-process improvements that do not require new
credentials or GitHub repository settings, and document the exact external
configuration needed for a future Homebrew pull-request flow. No credential,
GitHub App, environment, ruleset, branch-protection setting, dependency-graph
setting, tag, Release, remote workflow run, or push was created or changed. The
preserved manual-binary backup was not accessed or changed.

### Changes

| Repository/file or area | Purpose |
|---|---|
| `env-vault/.github/workflows/reusable-quality.yml`, `ci.yml`, `build-binaries.yml` | Reuse one test/vet/race/smoke/license/build/native-version workflow from CI and releases |
| `env-vault/scripts/license-check.sh` | Run pinned `go-licenses v2.0.1` natively on Linux, macOS, and Windows |
| `env-vault/.github/dependabot.yml`, `dependency-review.yml`, `tests/process_config_test.go` | Add weekly Go/Actions updates and pull-request dependency review with current action majors |
| `homebrew-tap/.github/dependabot.yml` | Add weekly Actions updates for the tap |
| `env-vault/internal/releasearchive`, `cmd/release-extract` | Safely and boundedly extract the exact five packages before SBOM inspection; reject traversal, links, special files, collisions, and resource-limit violations |
| `env-vault/scripts/release/artifact-attestation-state.sh`, `verify-artifact-attestations.sh` | Classify missing predicates without treating API failures as absence and verify archive digest, signer workflow, predicate, and release source SHA |
| `env-vault/.github/workflows/build-binaries.yml` supply-chain and health jobs | Generate a Syft v1.44.0 SPDX workflow artifact, publish provenance/SBOM attestations separately from the exact ten Release assets, avoid duplicate complete predicates, gate Homebrew, and add Release/tap/tap-CI/attestation/run links to the job summary |
| `env-vault/RELEASING.md` | Define preparation, retry, repair, rollback/withdrawal, immutable boundaries, supply-chain verification, and a healthy release |
| `env-vault/docs/release-external-settings.md` | Specify the proposed least-privilege GitHub App, release environment, tap ruleset/CI, dependency-review setting, approval decisions, and deploy-key cutover without applying them |
| `README.md`, `docs/design.md`, `THIRD_PARTY_NOTICES.md` | Describe reusable quality gates, native license scans, the ten-asset/SBOM boundary, and the current manual tap-CI limitation accurately |

### Commands And Results

| Command or action | Result | Claim status |
|---|---|---|
| Context7 current GitHub Actions, GitHub CLI, actionlint, Syft, and Homebrew documentation | passed; current runner labels, action inputs/permissions, attestation verification, SBOM, and lint behavior were used | doc_verified |
| Official `actions/attest@v4` and `anchore/sbom-action@v0` action definitions | passed; confirmed multi-subject paths, `sbom-path`, Node 24, `artifact-metadata: write`, and explicit release-asset opt-out | doc_verified |
| `gofmt` tracked Go sources | passed; no unformatted files | cli_observed |
| `go test ./...` | passed | cli_observed |
| `go vet ./...` | passed | cli_observed |
| `go test -race ./...` | passed | cli_observed |
| `scripts/smoke.sh` | passed; sandbox emitted only a non-fatal Go module stat-cache warning | cli_observed |
| `scripts/license-check.sh` | passed with pinned v2.0.1; expected assembly-inspection warning only | cli_observed |
| `shellcheck scripts/release/*.sh scripts/license-check.sh` | passed | cli_observed |
| actionlint v1.7.12 | passed after ignoring only its known false-positive for current `concurrency.queue` syntax | cli_observed |
| Workflow/config and fake-GitHub regression tests | passed; includes repair gates, native runners, exact assets/formula, API 404/503/network classification, partial predicate state, source SHA, and summary structure | cli_observed |
| Release archive extractor tests | passed; valid packages plus traversal, absolute paths, symlink, hardlink, special/collision, duplicate, entry-count, and size-limit failures | cli_observed |
| `brew style Formula/env-vault.rb` and `ruby -c Formula/env-vault.rb` | passed; no offenses and syntax valid | cli_observed |
| `brew test --force ildarbinanas-design/tap/env-vault` | passed against installed v0.0.5 | cli_observed |
| `git diff --check` | passed | cli_observed |

### Claims

| Claim | Status | Evidence |
|---|---|---|
| CI and releases cannot drift across duplicated quality/build matrices | repo_verified | both callers use `reusable-quality.yml`; regression tests reject duplicated release jobs |
| License policy is evaluated on every supported host OS | repo_verified | native Linux/macOS/Windows matrix plus pinned cross-platform script |
| Supply-chain evidence cannot change the immutable Release asset set | repo_verified | SBOM upload is a workflow artifact; `upload-release-assets: false`; Release download requires exactly ten assets |
| A complete same-version retry does not create duplicate attestations | repo_verified | predicate-specific API state and verification produce separate `create_provenance`/`create_sbom` outputs; complete evidence is a no-op |
| Attestations must match the archive, expected signer, predicate, and release source commit | repo_verified | `gh attestation verify` uses repository, signer workflow, exact predicate, and `--source-digest`; workflow refuses creation when run SHA differs |
| Dependency update and review configuration is version-controlled | repo_verified | weekly Dependabot configs, PR-only dependency review, and regression tests |
| Tap PR publication and exact tap-CI waiting were not enabled without an approved trust boundary | repo_verified | current direct-push workflow remains explicit; proposed external settings and approval checklist are documented separately |

### Risks And External Blockers

| Risk or blocker | Status | Mitigation or required action | Claim status |
|---|---|---|---|
| Homebrew still updates by deploy-key direct push and release health does not await tap CI | blocked | Approve and configure the dedicated GitHub App, `release` environment, tap ruleset/required check, and auto-merge policy in `docs/release-external-settings.md`; then implement the PR/wait path | user_action_required |
| Dependency review is not yet a required server-side check | blocked | Enable/confirm dependency graph, observe the real `Dependency review` context, and add it to the default-branch ruleset after approval | user_action_required |
| A late `release-assets` repair cannot honestly mint provenance after `main` moves past the release SHA | accepted | Rerun the original workflow at the release SHA; the new workflow fails closed instead of signing from a later commit; fix forward with a new patch if the original run is unavailable | repo_verified |
| Attestation and multi-platform jobs were not exercised in live GitHub Actions | accepted | Local/fake-API tests, actionlint, exact action docs, and native local checks passed; a pushed PR CI run is the remaining authoritative execution check | planned |
| actionlint v1.7.12 predates current `concurrency.queue` syntax | accepted | Ignore only that exact diagnostic; official current documentation and a regression test protect the queue setting | doc_verified |

## Task ID: `ENV-VAULT-RELEASE-PROCESS-STAGE-4`

Timestamp UTC: `2026-07-10T20:55:34Z`

### Scope

Make the release workflow serialized, monotonic, idempotent, resumable after
partial failure, and explicitly repairable at the release-assets, Homebrew, or
health stage. No live tag, GitHub Release, asset, tap branch, credential, or
remote workflow run was created, moved, overwritten, or deleted.

### Changes

| File or area | Purpose |
|---|---|
| `.github/workflows/build-binaries.yml` | Adds a global non-cancelling FIFO release queue, repair inputs/start-at gates, canonical source SHA, read-only monotonic preflight, existing tag/Release acceptance, asset reconciliation, commit-time monotonic/no-op guards, and final health verification |
| `scripts/release/lib.sh` | Defines the exact five archives/ten assets and strict archive/checksum validation |
| `scripts/release/resolve-tag-sha.sh` | Resolves lightweight or annotated tags to a commit and distinguishes explicit 404 from operational failures |
| `scripts/release/get-release-state.sh` | Reads stable Release state and distinguishes explicit absence from auth/network/API failure |
| `scripts/release/reconcile-release-assets.sh` | Verifies existing pairs, uploads only missing members, rejects mismatches, and never overwrites an asset |
| `scripts/release/download-release-assets.sh` | Downloads exactly the required assets into staging and publishes the directory only after every pair verifies |
| `scripts/release/generate-homebrew-formula.sh`, `verify-homebrew-formula.sh` | Generate and byte-compare the formula from verified Release assets |
| `scripts/release/semver-compare.sh` | Compares unbounded numeric SemVer components without integer overflow |
| `tests/release_scripts_test.go` | Uses fake GitHub APIs/assets to cover annotated tags, 404/503/network failures, all four partial-pair states, checksum mismatch, zero-upload no-op, and overwrite prohibition |
| `tests/workflows_test.go` | Protects concurrency, repair wiring, result gates, source checkouts, preflight order, Release reuse, exact formula generation, monotonic/no-op behavior, and health checks |

### Commands And Results

| Command or action | Result | Claim status |
|---|---|---|
| Context7 and official GitHub Actions concurrency documentation | passed; confirmed static groups, `cancel-in-progress: false`, and current `queue: max` semantics | doc_verified |
| Context7 actionlint documentation | passed; selected pinned actionlint v1.7.12 | doc_verified |
| `bash -n scripts/release/*.sh` | passed | cli_observed |
| `shellcheck -x scripts/release/*.sh` | passed | cli_observed |
| Fake-GitHub resolver/state/reconciliation tests | passed | cli_observed |
| Workflow regression tests | passed | cli_observed |
| `go test ./...` | passed | cli_observed |
| `go vet ./...` | passed | cli_observed |
| actionlint v1.7.12 with only its known `queue` false-positive ignored | passed with no remaining diagnostics | cli_observed |
| `git diff --check` | passed | cli_observed |

### Claims

| Claim | Status | Evidence |
|---|---|---|
| Concurrent release requests cannot cancel a running or already queued release | repo_verified | one global group, `cancel-in-progress: false`, `queue: max`; regression test |
| A target below the published Homebrew version cannot mutate tag, Release, or tap | repo_verified | read-only preflight is a direct release dependency; commit-time guard repeats the check for TOCTOU |
| Same version, same expected tag commit, verified assets, and identical formula is an external no-op | repo_verified | tag/Release reuse, zero-upload reconciliation, byte-identical formula exit, then health verification |
| Existing tags are accepted only at the expected peeled commit | repo_verified | resolver follows annotated tag objects; mismatch is fatal; no PATCH/DELETE/move operation exists |
| Existing Release assets are never silently replaced | repo_verified | remote complete pairs are verified; only missing members are uploaded; `--clobber` is forbidden by code and tests |
| A checksum or SHA mismatch is fatal | repo_verified | strict pair/SHA comparisons and fake-API regression cases |
| Repair modes have explicit start-at behavior | repo_verified | `release-assets`, `homebrew`, and `health` choices plus exact skipped/success result gates |

### Risks

| Risk | Status | Mitigation | Claim status |
|---|---|---|---|
| actionlint v1.7.12 predates GitHub's May 2026 `concurrency.queue` syntax | accepted | ignore only that exact linter diagnostic; official GitHub docs and a dedicated regression test protect `queue: max` | doc_verified |
| GitHub API mutation paths were not exercised against the live repository | accepted | fake `gh` tests cover success, partial state, explicit 404, 503/network error, and mismatch behavior; live publication remains separately authorized | cli_observed |
| `release-assets` repair rejects a version older than the current tap | accepted | prevents an old repair from downgrading Homebrew; document the current-release-only boundary in the process-improvement stage | planned |
| Tap still updates by direct push and this stage does not await tap CI | open | cross-repository PR/authentication and downstream CI settings are handled in the process-improvement stage | planned |

## Task ID: `ENV-VAULT-RELEASE-PROCESS-STAGE-3`

Timestamp UTC: `2026-07-10T20:28:26Z`

### Scope

Make Homebrew version validation exact in both the published formula and its
release-time generator, and document a non-destructive migration from manual or
`go install` binaries. The preserved manual-binary backup was not accessed or
changed. No remote tap update, release, tag, credential change, or push occurred.

### Changes

| Repository/file | Purpose |
|---|---|
| `env-vault/.github/workflows/build-binaries.yml` | Generates an exact `assert_equal "v#{version}"` formula test |
| `env-vault/tests/workflows_test.go` | Requires the full exact assertion, rejects substring matching, and rejects `link_overwrite` |
| `env-vault/README.md` | Adds inspect, dry-run, timestamped backup, plain-link, and PATH verification steps for migration |
| `homebrew-tap/Formula/env-vault.rb` | Uses the same exact version assertion as the generator |
| `env-vault/evidence_bundle.md` | Records checks and the local Homebrew trust limitation |

### Commands And Results

| Command or action | Result | Claim status |
|---|---|---|
| Context7 Homebrew documentation query | passed; confirmed formula test assertions and safe linking behavior | doc_verified |
| Targeted generated-formula regression tests | passed | cli_observed |
| `go test ./...` | passed | cli_observed |
| `brew style Formula/env-vault.rb` | passed; one file, no offenses | cli_observed |
| `ruby -c Formula/env-vault.rb` | passed | cli_observed |
| `brew test --force ildarbinanas-design/tap/env-vault` | passed against the installed v0.0.5 keg | cli_observed |
| Exact installed-binary comparison with `v0.0.5` | passed | cli_observed |
| Temporary local test tap cleanup | passed; tap removed and no trust exception was persisted | cli_observed |

### Claims

| Claim | Status | Evidence |
|---|---|---|
| The checked-in tap formula and future generated formula use identical exact version semantics | repo_verified | both contain the same `assert_equal ... .strip` line; regression test protects the generator |
| The formula cannot silently opt into overwriting unmanaged files | repo_verified | no `link_overwrite`; regression test rejects adding it to generated content |
| Migration inspects before mutation and uses only `--overwrite --dry-run` | repo_verified | README sequence backs up the exact conflict, then uses plain `brew link` |
| The existing manual-binary backup remains untouched | cli_observed | no command referenced, moved, or removed the preserved backup path |

### Risks

| Risk | Status | Mitigation | Claim status |
|---|---|---|---|
| Current Homebrew rejects loading an untrusted temporary local tap for `brew test` | accepted | did not persist a trust-policy exception; working formula passed style/Ruby parsing, generator regression, and exact installed-binary checks; remote tap CI remains authoritative | cli_observed |
| Homebrew auto-update ran while creating the temporary tap | accepted | no env-vault repository, formula, installed keg, or preserved backup was changed; temporary tap was removed | cli_observed |
| Tap update is still a direct push and release does not yet await tap CI | open | addressed in the reliability/process stages | planned |

## Task ID: `ENV-VAULT-RELEASE-PROCESS-STAGE-2`

Timestamp UTC: `2026-07-10T20:21:36Z`

### Scope

Require every packaged release binary to report the exact resolved build version
through both supported version commands on a compatible native runner. No tag,
release, remote workflow run, credential, or published artifact was created.

### Changes

| File | Purpose |
|---|---|
| `.github/workflows/build-binaries.yml` | Adds post-build native smoke jobs for all five targets, exact checks for `--version` and `version`, archive checksum verification, and a release dependency on those checks |
| `tests/workflows_test.go` | Protects the native runner matrix, exact two-command comparisons, resolved-version propagation, and the release gate |
| `evidence_bundle.md` | Records the stage scope, documentation basis, checks, and residual risks |

### Commands And Results

| Command or action | Result | Claim status |
|---|---|---|
| Context7 GitHub Actions documentation query | passed; confirmed matrix-driven runners and workflow concurrency syntax | doc_verified |
| Official GitHub-hosted runner reference | passed; `ubuntu-24.04-arm` and `windows-latest` are current standard native labels | doc_verified |
| Targeted workflow regression tests | passed | cli_observed |
| `go test ./...` | passed | cli_observed |
| `gofmt -w tests/workflows_test.go` | passed | cli_observed |

### Claims

| Claim | Status | Evidence |
|---|---|---|
| Cross-compiled binaries are never executed on the incompatible Ubuntu build runner | repo_verified | all execution moved to a post-build native smoke matrix |
| Linux arm64 executes on arm64 hardware | repo_verified | smoke target uses `ubuntu-24.04-arm` |
| Windows amd64 executes on Windows | repo_verified | smoke target uses `windows-latest` and PowerShell-native extraction/checking |
| Both version commands must exactly match the resolved `${VERSION}` | repo_verified | Unix byte-for-byte `diff`; Windows one-line case-sensitive comparisons; regression tests inspect both paths |
| GitHub Release creation cannot start before every native smoke check passes | repo_verified | release job directly needs `smoke` |

### Risks

| Risk | Status | Mitigation | Claim status |
|---|---|---|---|
| Native runner labels and installed images can change over time | accepted | use documented standard labels and keep the matrix protected by tests; remote CI remains the authoritative execution check | doc_verified |
| Windows PowerShell smoke code cannot execute on the local macOS host | accepted | YAML structure is parsed by Go regression tests; the Windows job runs only on `windows-latest` | repo_verified |
| No remote Actions workflow was run in this local-only stage | accepted | publication and push require separate approval | planned |

## Task ID: `ENV-VAULT-RELEASE-PROCESS-STAGE-1`

Timestamp UTC: `2026-07-10T20:17:59Z`

### Scope

Correct unknown-flag remediation paths and add exact CLI regression coverage.
No secret value, credential, tag, release, remote branch, or published artifact
was read, changed, or created.

### Changes

| File | Purpose |
|---|---|
| `internal/cli/cli.go` | Uses Cobra's complete `CommandPath()` directly so the program name is not duplicated |
| `internal/cli/flags_test.go` | Covers root and nested unknown flags, exact remediation, and the non-duplication invariant |
| `internal/cli/version_test.go` | Requires `version` and `--version` to emit the same exact version line with no stderr |
| `evidence_bundle.md` | Records stage scope, checks, and residual risk without secret material |

### Commands And Results

| Command or action | Result | Claim status |
|---|---|---|
| Applicable `AGENTS.md` and both repository statuses | passed; both repositories started clean on `main` | cli_observed |
| Context7 Cobra documentation query | passed; `CommandPath()` is the full path and the root flag handler is inherited by child commands | doc_verified |
| `gofmt -w internal/cli/cli.go internal/cli/flags_test.go internal/cli/version_test.go` | passed | cli_observed |
| Targeted CLI regression tests | passed | cli_observed |
| `go test ./...` | passed | cli_observed |

### Risks

| Risk | Status | Mitigation | Claim status |
|---|---|---|---|
| Other hand-written remediation strings could drift independently | accepted | this stage targets Cobra flag parsing; existing command-specific remediations remain covered by their command tests | repo_verified |
| Release binaries have not been rebuilt or published | accepted | workflow hardening and release verification are handled in later stages; no publication is authorized | planned |

## Task ID: `ENV-VAULT-V0.0.5-RELEASE-WORKFLOW-HARDENING`

Timestamp UTC: `2026-07-10T19:12:05Z`

### Scope

Harden the existing GitHub Release and Homebrew publication path, synchronize
stale project documentation, and prepare the approved `v0.0.5` patch release.

No secret value, keychain item, `TAP_DEPLOY_KEY`, GitHub token value, or raw
credential material was read or persisted. External publication results are
verified separately after this evidence-bearing commit passes review and CI.

### Baseline

- Local branch started clean from `main` at
  `765627566f1d5ba175de017fe8ef3614a0408453`.
- `v0.0.4` already existed at that commit with ten verified release assets.
- `homebrew-tap` already contained the successful bot update for `v0.0.4`.
- The prior release path required a manually pushed tag; a blank
  `workflow_dispatch` produced temporary artifacts only.

### Changes

| File | Purpose |
|---|---|
| `.github/workflows/build-binaries.yml` | Resolves one validated version, preserves build-only dispatches and tag releases, adds default-branch manual releases, requires tests and license checks, updates actions to Node.js 24 majors, creates tags with `GITHUB_TOKEN`, verifies installed Homebrew version, and stops masking commit failures |
| `.github/workflows/ci.yml` | Updates action majors and adds the pinned license job |
| `scripts/license-check.sh` | Runs `go-licenses v2.0.1` from a temporary tool directory with an explicit permissive allowlist |
| `tests/workflows_test.go` | Adds regression coverage for action majors, release metadata, semantic-version gates, resolved-version propagation, license gates, Homebrew version testing, and commit/push ordering |
| `README.md`, `docs/design.md` | Documents build-only dispatches, manual and tag releases, verification gates, artifacts, and Homebrew publication boundaries |
| `AGENTS.md`, `docs/project-charter.md`, `backlog.md`, `THIRD_PARTY_NOTICES.md` | Replaces stale bootstrap state, closes completed distribution work, and records the automated license policy |
| `evidence_bundle.md` | Records scope, checks, claims, and residual risks without secret values |

### Commands And Results

| Command or action | Result | Claim status |
|---|---|---|
| Official action release and `action.yml` checks | passed; `checkout@v7`, `setup-go@v6`, `upload-artifact@v7`, and `download-artifact@v8` exist and declare `node24` | doc_verified |
| `go test ./...` | passed | cli_observed |
| `go vet ./...` | passed | cli_observed |
| `go test -race ./...` | passed | cli_observed |
| `scripts/smoke.sh` | passed | cli_observed |
| `scripts/license-check.sh` | passed with pinned `go-licenses v2.0.1`; expected non-Go assembly warning only | cli_observed |
| `sh -n scripts/license-check.sh` and `shellcheck scripts/license-check.sh` | passed | cli_observed |
| `go mod verify` and `go mod tidy -diff` | passed; module files unchanged | cli_observed |
| `gitleaks detect --source . --redact --no-banner` | passed; no leaks found | cli_observed |
| `git diff --check` | passed | cli_observed |
| Native release build with `Version=v0.0.5` | passed; both `--version` and JSON `version` reported `v0.0.5` | cli_observed |
| Linux amd64/arm64 and Windows amd64 cross-builds | passed with `CGO_ENABLED=0` | cli_observed |

### Claims

| Claim | Status | Evidence |
|---|---|---|
| Manual publication requires a strict `vMAJOR.MINOR.PATCH` input from the default branch | repo_verified | metadata job and workflow regression tests |
| Dispatch without a version remains build-only | repo_verified | metadata publish output defaults to `false` |
| Tag-driven releases remain supported | repo_verified | `push.tags: v*` plus strict metadata validation |
| Release publication waits for tests, license validation, and all five platform builds | repo_verified | direct `needs` contract on the release job |
| Manual release tags use the workflow-scoped token and do not require a PAT | repo_verified | refs API step uses `${{ github.token }}` |
| Homebrew commit errors are no longer treated as successful no-ops | repo_verified | staged-diff guard followed by unmasked commit and push |
| Generated Homebrew formula verifies the installed binary version | repo_verified | formula test uses `--version` and `version.to_s` |
| Secret values were not read, printed, or recorded | cli_observed | no keychain mutation, token-read command, env dump, or secret output was used |

### Risks

| Risk | Status | Mitigation | Claim status |
|---|---|---|---|
| GitHub Release and downstream tap CI are not atomic | accepted | report the release healthy only after the tap workflow succeeds | repo_verified |
| Action major tags are mutable | accepted | use official GitHub-owned actions and regression-test the expected majors; consider immutable SHA pins separately | planned |
| License scanning requires network access and cannot inspect assembly for further dependencies | accepted | pin the scanner version, fail closed on disallowed licenses, and record the non-Go warning | cli_observed |
| Tap CI currently runs only on `macos-latest` | open | add Linux and explicit architecture coverage as a separate distribution task | planned |
| Real OS keychain mutation was not part of this workflow-only change | accepted | existing backend tests and smoke checks remain the non-destructive release gate | cli_observed |
| Local Go builds emitted non-fatal sandbox stat-cache warnings | accepted | all build commands exited successfully and produced versioned binaries | cli_observed |

## Task ID: `ENV-VAULT-V0.0.2-DOCS-GATE`

Timestamp UTC: `2026-07-07T19:49:33Z`

### Scope

Synchronize release and security documentation before the `v0.0.2` tag.

No source code, workflow behavior, real keychain secret mutation, tag, release, or publish action was performed in this docs gate.

### Objective

Confirm current `main` reflects the darwin CGO and pass-backend release state, then update stale release/security docs before tagging.

### Changes

| File | Purpose |
|---|---|
| `README.md` | Keeps the tag-driven release example aligned with the planned `v0.0.2` release |
| `SECURITY.md` | Updates supported-version wording now that `v0.0.1` exists |
| `docs/design.md` | Documents that release publication is tag-driven through `build-binaries` |
| `evidence_bundle.md` | Records this docs gate without secret values |

### Commands And Results

| Command or action | Result | Claim status |
|---|---|---|
| Baseline repo, branch, status, remote, tags, and recent log checks | passed; repo was clean before sync | cli_observed |
| `git fetch --prune origin` and `git fetch --prune --tags origin` | passed | cli_observed |
| `git switch main` and `git pull --ff-only origin main` | passed; local `main` matched `origin/main` at `e5a7ca83e775c660a8e65cbf2794306d6dcb4463` | cli_observed |
| `git merge-base --is-ancestor 8a5c314f8290d459d5bab7f6dd455a58a8bcf5fb HEAD` | failed; handled as squash/content-equivalent merge | cli_observed |
| Content signatures for Pass backend, FileBackend exclusion, Passwork deferral, darwin macOS runners, and darwin `CGO_ENABLED=1` | passed | repo_verified |
| Documentation check for Pass prerequisites, darwin CGO Keychain behavior, plaintext exclusion, and gated test backend | passed with one stale supported-version sentence found | repo_verified |
| `git diff --name-status` | passed; docs/evidence files only | cli_observed |
| `git diff --check` | passed | cli_observed |
| `go test ./...` | passed | cli_observed |
| `go vet ./...` | passed | cli_observed |
| `CGO_ENABLED=1 go test ./...` | passed | cli_observed |
| `ENV_VAULT_BACKEND=test ENV_VAULT_ALLOW_INSECURE_TEST_BACKEND=1 ENV_VAULT_TEST_STORE="$(mktemp -d)" ./scripts/smoke.sh` | passed; non-fatal Go stat-cache warning only | cli_observed |
| `gitleaks detect --source . --redact --no-banner` | passed; no leaks found | cli_observed |

### Security And Backend Claims

| Claim | Status | Evidence |
|---|---|---|
| Secret values were not read or printed | repo_verified | no secret-value command, env dump command, or real keychain mutation was run |
| Release flow remains workflow-owned | repo_verified | docs state tag-driven `build-binaries` publication |
| Production plaintext/file backend remains disabled | repo_verified | docs and allowlist tests exclude `keyring.FileBackend` |
| Passwork remains deferred | repo_verified | docs/backlog and allowlist tests exclude Passwork |

### Risks

| Risk | Status | Mitigation | Claim status |
|---|---|---|---|
| Real macOS Keychain dummy mutation remains manual/non-CI | accepted | Release gate uses `doctor` only and avoids real secret mutation | planned |
| Real Linux `pass` store verification remains manual/non-CI | open | Backlog keeps manual pass-store verification | planned |

## Task ID: `ENV-VAULT-DARWIN-CGO-PASS-BACKEND`

Timestamp UTC: `2026-07-07T18:06:48Z`

### Scope

Targeted release/backend hardening for `/Users/ildarzaripov/env-vault` on branch `fix/darwin-cgo-pass-backend`.

No real keychain secret set/get, tag, release, or publish action was performed.

### Objective

Build darwin release artifacts with CGO enabled on macOS runners, add `keyring.PassBackend` to supported production backends, and keep plaintext/file/Passwork out of production scope.

### Changes

| File | Purpose |
|---|---|
| `.github/workflows/build-binaries.yml` | Runs darwin artifact builds on macOS runners with `CGO_ENABLED=1`; keeps Linux/Windows on Ubuntu with `CGO_ENABLED=0` |
| `.github/workflows/ci.yml` | Gives CI darwin build jobs the same macOS runner and CGO contract |
| `internal/secretstore/keyring/keyring.go` | Adds `keyring.PassBackend` at the end of the production allowlist and supports explicit pass selection |
| `internal/secretstore/secretstore.go` | Adds pass-specific unavailable sentinel and remediation text |
| `internal/cli/cli.go` and `internal/runner/resolver.go` | Return structured `BACKEND_UNAVAILABLE` remediation for unavailable explicit pass backend |
| `internal/secretstore/keyring/keyring_test.go` | Verifies production allowlist includes Pass and excludes test, file, and passwork |
| `internal/cli/dry_run_test.go` | Verifies explicit unavailable pass backend returns structured remediation |
| `tests/workflows_test.go` | Statically verifies darwin workflow builds use macOS runners with `CGO_ENABLED=1` |
| `scripts/smoke.sh` | Keeps smoke checks on the gated test backend without shell `set` or `export` statements |
| `README.md`, `SECURITY.md`, `docs/security.md`, `docs/design.md`, `docs/adr/0001-secret-backend.md`, `backlog.md` | Documents darwin CGO artifacts, Linux Secret Service/pass support, pass prerequisites, and file/plaintext/Passwork exclusions |
| `evidence_bundle.md` | Records this change and verification without secret values |

### Commands And Results

| Command or action | Result | Claim status |
|---|---|---|
| Full change prompt read from `/tmp/ai_pdlc_env_vault_change_context_20260707T173414Z/prompts/codex_env_vault_change_prompt.txt` | passed; 152 lines read | cli_observed |
| `AGENTS.md` read | passed | cli_observed |
| `git status --porcelain=v2 --branch` | passed; clean `main` before branch creation | cli_observed |
| `git switch -c fix/darwin-cgo-pass-backend` | passed after approved git ref write | cli_observed |
| Context7 GitHub Actions docs query | passed; confirmed matrix include values can drive `runs-on` | doc_verified |
| GitHub-hosted runner documentation check | passed; macOS Intel and arm64 runner labels verified from official docs | doc_verified |
| `go test ./...` | passed | cli_observed |
| `go vet ./...` | passed | cli_observed |
| `CGO_ENABLED=1 go test ./...` | passed | cli_observed |
| `go test -race ./...` | passed | cli_observed |
| `ENV_VAULT_BACKEND=test ENV_VAULT_ALLOW_INSECURE_TEST_BACKEND=1 ENV_VAULT_TEST_STORE="$(mktemp -d)" ./scripts/smoke.sh` | passed after removing shell `set`/`export` from the script; non-fatal Go stat-cache warning only | cli_observed |
| `CGO_ENABLED=1 go build -o /tmp/env-vault-cgo-darwin ./cmd/env-vault` | passed; non-fatal Go stat-cache warning only | cli_observed |
| `/tmp/env-vault-cgo-darwin doctor` | passed; reported backend availability warning only, no secret values | cli_observed |
| `gitleaks detect --source . --redact --no-banner` | passed; no leaks found | cli_observed |
| `git diff --check` | passed | cli_observed |
| Static scan for `secret get` / `--value` command implementation | passed; no implementation matches | cli_observed |
| Static scan for shell `set`/`export` in scripts | passed; no command matches | cli_observed |

### Security And Backend Claims

| Claim | Status | Evidence |
|---|---|---|
| Darwin artifacts build with CGO enabled | repo_verified | release and CI darwin matrix entries use macOS runners and `cgo: "1"` |
| Darwin builds do not rely on Linux CGO cross-compile | repo_verified | darwin matrix entries use `macos-15-intel` and `macos-15` |
| `keyring.PassBackend` is production-allowed | repo_verified | `internal/secretstore/keyring` allowlist and tests |
| `keyring.FileBackend` is not production-allowed | repo_verified | allowlist test excludes it |
| Gated test backend is not production-allowed | repo_verified | allowlist test excludes `test`; runtime gate remains explicit |
| Passwork is not implemented | repo_verified | allowlist test excludes `passwork`; docs/backlog defer connector |
| Secret values were not read, printed, or stored in evidence | cli_observed | tests/smoke used generated ephemeral values and redacted scan passed |
| No env values printed | cli_observed | no `printenv`, standalone `env`, or environment-dumping `set` command was used; smoke uses scoped command env only |

### Risks

| Risk | Status | Mitigation | Claim status |
|---|---|---|---|
| Real macOS Keychain availability was not validated with secret mutation | accepted | Only `doctor` was run; manual Keychain set/check remains optional and non-CI | cli_observed |
| Real Linux `pass` store was not exercised | open | Backlog includes manual verification with installed `pass` and initialized password store | planned |
| Go sandbox emitted stat-cache warnings during smoke/build | accepted | Commands exited successfully; warnings did not affect produced binary or smoke result | cli_observed |

## Task ID: `ENV-VAULT-V0.0.1-FIRST-RELEASE`

Timestamp UTC: `2026-07-06T22:40:17Z`

### Scope

Prepare and publish the first public release tag `v0.0.1` for `github.com/ildarbinanas-design/env-vault`.

### Objective

Ship the local MVP as a GitHub Release with downloadable binaries, after applying the open Dependabot security update and verifying the release build path.

### Changes

| File | Purpose |
|---|---|
| `go.mod` and `go.sum` | Merged Dependabot PR #1, updating indirect `github.com/dvsekhvalnov/jose2go` from `v1.5.0` to `v1.7.0` |
| `internal/cli/cli.go` | Changed the CLI version to a build-time variable |
| `internal/cli/dry_run_test.go` | Added coverage proving `env-vault version` uses the build-time version |
| `.github/workflows/build-binaries.yml` | Injects `github.ref_name` into release binaries through `-ldflags -X` |
| `README.md` | Documents `v0.0.1` as the first release tag example |
| `evidence_bundle.md` | Records this release preparation and verification run |

### Commands And Results

| Command or action | Result | Claim status |
|---|---|---|
| GitHub connector read PR #1 metadata and changed files | passed; PR changed only `go.mod` and `go.sum` | connector_observed |
| GitHub connector read PR #1 patch | passed; only indirect `jose2go` version and sums changed | connector_observed |
| GitHub connector read PR #1 workflow run | passed; `ci` completed successfully for PR head | connector_observed |
| GitHub connector squash-merge PR #1 | passed; merged as `00d11c2726949a65f00416c449ce6e1851a07456` | connector_observed |
| `git pull --ff-only origin main` | passed; local `main` fast-forwarded to `00d11c2726949a65f00416c449ce6e1851a07456` | cli_observed |
| `go test ./...` | passed | cli_observed |
| `go vet ./...` | passed | cli_observed |
| `go test -race ./...` | passed | cli_observed |
| `scripts/smoke.sh` | passed; no generated secret leaked to captured outputs or evidence | cli_observed |
| `go mod tidy -diff` | passed; no diff required | cli_observed |
| `go mod verify` | passed after the new dependency was present in the module cache | cli_observed |
| `git diff --check` | passed | cli_observed |
| Provider-token scan for old module path and provider wording | passed; no matches | cli_observed |
| Policy scan for `secret get` and `--value` | passed; only policy/documentation prohibitions remain, no command implementation | cli_observed |
| Local build with `-X .../internal/cli.Version=v0.0.1` plus `env-vault --json version` | passed; output reported `v0.0.1` | cli_observed |
| Local release matrix build for linux amd64/arm64, darwin amd64/arm64, and windows amd64 | passed; Go emitted sandbox stat-cache warnings only | cli_observed |
| GitHub Actions first tag run for `v0.0.1` | `ci` passed; `build-binaries` binary jobs passed; `release` job failed before creating a Release | api_observed |
| GitHub connector release-job log read | passed; root cause was `gh release create` running outside a git checkout without `--repo` | connector_observed |
| Context7 query for GitHub CLI manual | passed; `gh release create --repo` is the documented way to select a repository explicitly | doc_verified |
| Workflow release command update | passed; uses `--repo "github.com/${GITHUB_REPOSITORY}"` and `--verify-tag` | repo_verified |
| Updated `v0.0.1` tag run on `b9dd8826b3dca3a0f638df39797cb13d1eb10aa5` | passed; `ci` and `build-binaries` completed successfully | api_observed |
| GitHub Release API read for `v0.0.1` | passed; release exists at `https://github.com/ildarbinanas-design/env-vault/releases/tag/v0.0.1` with 10 assets | api_observed |
| Public release asset download check | passed; `env-vault-darwin-arm64.tar.gz` checksum verified and binary reported `v0.0.1` | cli_observed |

### Risks

| Risk | Status | Mitigation | Claim status |
|---|---|---|---|
| GitHub Release workflow still needs to run on the actual tag | closed | Updated `v0.0.1` tag run completed successfully and release assets were verified | api_observed |
| macOS sandbox prevented Go stat-cache writes during local cross-builds | accepted | Builds exited successfully; warning affects cache metadata only, not produced binaries | cli_observed |
| First `build-binaries` run for `v0.0.1` is failed in Actions history | accepted | Binary jobs succeeded; fix the release job, move the not-yet-released tag to the fixed commit, and verify the new run | api_observed |

## Task ID: `ENV-VAULT-GITHUB-BINARY-BUILD-WORKFLOW`

Timestamp UTC: `2026-07-06T22:12:56Z`

### Scope

Add a GitHub Actions workflow for building and packaging public binary artifacts for `github.com/ildarbinanas-design/env-vault`.

No tag, release, or package publishing was performed during this step.

### Objective

Allow maintainers to build downloadable binaries from GitHub with either a manual workflow run or a version tag.

### Changes

| File | Purpose |
|---|---|
| `.github/workflows/build-binaries.yml` | Builds Linux, macOS, and Windows archives, uploads workflow artifacts, and creates a GitHub Release on `v*` tags |
| `README.md` | Documents manual and tag-based binary builds |
| `backlog.md` | Moves GoReleaser to an optional follow-up after the simple workflow |
| `evidence_bundle.md` | Records this build-workflow change |

### References

| Source | Use |
|---|---|
| GitHub Actions artifacts documentation | Confirmed artifact upload/download workflow pattern |
| GitHub Actions `GITHUB_TOKEN` documentation | Confirmed workflow-scoped token usage for release creation |
| GitHub Releases documentation | Confirmed release assets are the right public distribution surface |

### Risks

| Risk | Status | Mitigation | Claim status |
|---|---|---|---|
| Release workflow not yet exercised on a real tag | open | Create a `v*` tag after review and verify release assets before announcing | planned |
| Manual artifacts expire | accepted | Artifacts use 14-day retention; tagged releases attach persistent release assets | repo_verified |

## Task ID: `ENV-VAULT-GITHUB-FIRST-PUBLICATION`

Timestamp UTC: `2026-07-06T22:06:17Z`

### Scope

First public GitHub publication for `/Users/ildarzaripov/env-vault`.

Target repository: `https://github.com/ildarbinanas-design/env-vault`

### Objective

Create the public GitHub repository, publish the prepared `main` branch over SSH, and enable baseline repository security settings without storing the one-time GitHub token.

### Commands And Results

| Command or action | Result | Claim status |
|---|---|---|
| Read one-time GitHub token from macOS clipboard | used only in memory; token not printed or stored | cli_observed |
| GitHub API authenticated user check | passed; authenticated as repository owner | cli_observed |
| GitHub API create repository | passed; public repository created | cli_observed |
| GitHub API update repository metadata | passed | cli_observed |
| GitHub API enable vulnerability alerts | passed | cli_observed |
| GitHub API enable automated security fixes | passed | cli_observed |
| GitHub API set repository topics | passed | cli_observed |
| GitHub API enable private vulnerability reporting | passed | cli_observed |
| `git remote add origin git@github.com:ildarbinanas-design/env-vault.git` | passed | cli_observed |
| `git push -u origin main` | passed; `main` published | cli_observed |
| GitHub API protect main branch | passed | cli_observed |
| Clipboard cleanup | passed | cli_observed |
| `git status --short --branch` | passed; local `main` tracks `origin/main` | cli_observed |
| `git remote -v` | passed; SSH remote configured | cli_observed |
| `git ls-remote --heads origin` | passed; remote `main` at `9dbfe1319fcd112a858fc8c6e77aa7361c958a3e` | cli_observed |
| GitHub connector repo read | passed; repo is public, default branch `main`, connector has admin permission | connector_observed |

### Security Notes

| Check | Result | Claim status |
|---|---|---|
| Token in command line | not used | cli_observed |
| Token in git remote URL | not used; SSH remote configured | cli_observed |
| Token persisted to repository files | not used | repo_verified |
| Token persisted to evidence | not used | repo_verified |

### Risks

| Risk | Status | Mitigation | Claim status |
|---|---|---|---|
| One-time GitHub token may remain valid in GitHub account settings | open | Revoke/delete the token immediately in GitHub Developer Settings | user_action_required |
| GitHub Actions result was not yet checked after first push | open | Verify the `ci` workflow run in GitHub after publication | planned |
| Public repository settings can differ by account plan and GitHub availability | accepted | API calls recorded success for available baseline settings; manually inspect repository settings if needed | cli_observed |

## Task ID: `ENV-VAULT-GITHUB-PUBLICATION-PREP`

Timestamp UTC: `2026-07-06T21:48:12Z`

### Scope

Prepare `/Users/ildarzaripov/env-vault` for first public GitHub publication at `github.com/ildarbinanas-design/env-vault`.

Repository creation, remote addition, push, tag, release, and package publishing were not performed during this preparation step.

### Objective

Move the Go module and documentation from the local bootstrap path to the selected public module path, add minimal public security guidance, and verify the repository before first push.

### Changes

| File | Purpose |
|---|---|
| `go.mod` and Go imports | Migrated module path to `github.com/ildarbinanas-design/env-vault` |
| `README.md` | Removed local-only bootstrap wording, added independent-project note and security report guidance |
| `.gitignore` | Added `.env-vault.yaml` as local secret-adjacent config |
| `SECURITY.md` | Added vulnerability reporting and secret-handling policy |
| `THIRD_PARTY_NOTICES.md` | Added initial third-party dependency notice |
| `docs/design.md` and `docs/adr/0000-local-bootstrap.md` | Updated module-path documentation for publication |
| `backlog.md` | Replaced owner/path migration items with post-publication checks |
| `evidence_bundle.md` | Recorded publication preparation evidence |

### Commands And Results

| Command | Result | Claim status |
|---|---|---|
| `git status --short --branch` | passed; clean before prep | cli_observed |
| `git remote -v` | passed; no remote configured before prep | cli_observed |
| `git log --oneline --decorate --all --max-count=10` | passed; single local commit before prep | cli_observed |
| `git ls-files` | passed; scoped tracked file inventory | cli_observed |
| `git ls-files --others --exclude-standard` | passed; no untracked files before prep | cli_observed |
| module-path scan | passed; old local module path found before migration | cli_observed |
| bulk module-path rewrite | passed | cli_observed |
| `gofmt -w cmd internal` | passed | cli_observed |
| `go mod tidy -diff` | passed; no diff required | cli_observed |
| `go test ./...` | passed | cli_observed |
| `go vet ./...` | passed | cli_observed |
| `go test -race ./...` | passed | cli_observed |
| `scripts/smoke.sh` | passed | cli_observed |
| `go build -o bin/env-vault ./cmd/env-vault` | passed with sandbox stat-cache warning only | cli_observed |
| old module path and provider-token scan | passed; no matches after prep | cli_observed |
| common credential-pattern scan | passed; no matches | cli_observed |
| `secret get` / `--value` implementation scan | passed; policy-only mentions remain | cli_observed |
| `git diff --check` | passed | cli_observed |

### Risks

| Risk | Status | Mitigation | Claim status |
|---|---|---|---|
| One-time GitHub token can grant broad access until revoked | open | Use only through a hidden prompt, do not store it, revoke immediately after publication | user_asserted |
| Public repository settings still need GitHub-side verification after push | open | Verify repository visibility, security features, and CI after remote creation | planned |
| Real OS keychain manual verification remains outside sandbox checks | open | Keep as post-publication manual validation | unknown |

## Task ID: `ENV-VAULT-DOC-PROVIDER-WORDING-CLEANUP`

Timestamp UTC: `2026-07-06T21:06:05Z`

### Scope

Targeted documentation wording cleanup for `/Users/ildarzaripov/env-vault`, followed by an explicitly requested amend of the only local commit.

No push, tag, release, remote creation, or publishing action was performed.

### Objective

Remove a cloud-provider-specific token from current tracked repository content while preserving the generic design intent.

### Commands And Results

| Command | Result | Claim status |
|---|---|---|
| `git status --short --branch` | passed; clean before edit | cli_observed |
| `git grep -n -i <provider-token>` | passed; one current tracked match in `docs/design.md` before edit | cli_observed |
| `date -u +%Y-%m-%dT%H:%M:%SZ` | passed | cli_observed |
| `git grep -n -i <provider-token>` | passed; no current tracked matches after edit | cli_observed |
| `rg -n -i "<provider-token>" . ...` | passed; no working-tree matches outside ignored build/coverage/git internals after edit | cli_observed |
| `git diff --check` | passed | cli_observed |
| `git diff -- docs/design.md evidence_bundle.md` | passed; reviewed scoped diff | cli_observed |
| User approval to amend | received at `2026-07-06T21:09:28Z` | user_asserted |

### Changes

| File | Purpose |
|---|---|
| `docs/design.md` | Reworded Generic Scope to avoid provider-specific phrasing while retaining generic/local automation meaning |
| `evidence_bundle.md` | Recorded this targeted documentation cleanup without reintroducing the removed token |

### Risks

| Risk | Status | Mitigation | Claim status |
|---|---|---|---|
| Historical commits may still contain prior wording before amend | mitigated | User explicitly approved amending the only local commit before publication | repo_verified |

Task ID: `ENV-VAULT-P0-DUMMY-FIXTURE-REMEDIATION`

Timestamp UTC: `2026-07-06T01:19:42Z`

## Scope

Targeted local-only security remediation for `/Users/ildarzaripov/env-vault`.

No GitHub remote, commit, push, tag, release, publishing action, unrelated repository scan, or D1 inventory was performed.

## Objective

Remove stable hardcoded secret payload fixtures from tests, smoke checks, docs, and evidence while preserving safe secret names and environment variable identifiers.

## Discovery

| Check | Result | Claim status |
|---|---:|---|
| UTC timestamp | passed | cli_observed |
| Working directory | `/Users/ildarzaripov/env-vault` | cli_observed |
| Inside git worktree | yes | cli_observed |
| Git remote | none configured | cli_observed |
| Module path | `github.com/ildarbinanas-design/env-vault` | repo_verified |
| Initial worktree | no commits yet; files untracked | cli_observed |
| File list | scoped to current repo, max depth 4 | cli_observed |

## Remediation Summary

| Area | Action | Claim status |
|---|---|---|
| Go tests | Replaced stable secret payload fixtures with runtime-generated ephemeral values | repo_verified |
| CLI output regression tests | Added/updated assertions for human, JSON, JSONL, output-file, dry-run, and structured-error paths | repo_verified |
| Runner tests | Stored generated expected value only in memory or a temp file needed by the child-process assertion | repo_verified |
| Redaction tests | Replaced stable dummy payloads with generated values | repo_verified |
| Smoke script | Generates an ephemeral test value at runtime, sends it through stdin, checks captured output, metadata, and this evidence bundle | repo_verified |
| Docs | Documented generated ephemeral fixtures and no stable secret payload fixtures | repo_verified |
| Evidence | Records commands and results without secret values | repo_verified |

## Files Changed

| File | Purpose |
|---|---|
| `internal/testutil/secret.go` | Runtime generated test values and non-leaking assertions |
| `internal/cli/dry_run_test.go` | CLI output, JSON, JSONL, metadata, dry-run, and structured-error leakage regressions |
| `internal/config/config_test.go` | Config roundtrip no-secret-value regression without stable payload fixture |
| `internal/redact/redact_test.go` | Redaction regressions using generated values |
| `internal/runner/resolver_test.go` | Resolver tests using generated secret payloads |
| `internal/runner/runner_test.go` | Child env assertion using generated value and temp file |
| `scripts/smoke.sh` | Runtime generated smoke fixture and leakage checks |
| `docs/security.md` | Security policy note for generated fixtures |
| `README.md` | Security model note for generated fixtures |
| `evidence_bundle.md` | Current remediation evidence |

## Fixture Findings

| Phase | Secret-value fixture findings | Identifier/policy findings | Notes | Claim status |
|---|---:|---:|---|---|
| Before remediation | present | present | Initial detector found test, smoke, and README candidates; targeted review also found stable payload contexts not covered by the detector regexes | cli_observed |
| After remediation | 0 | 11 | Remaining findings are safe identifiers, test function names, policy text, or parser fixtures; no matched values printed | cli_observed |

## Commands And Results

| Command | Result | Claim status |
|---|---|---|
| `date -u +"%Y-%m-%dT%H:%M:%SZ"` | passed | cli_observed |
| `pwd` | passed | cli_observed |
| `git rev-parse --is-inside-work-tree` | passed | cli_observed |
| `git status --short --branch` | passed; no commits yet, files untracked | cli_observed |
| `git remote -v` | passed; no remote configured | cli_observed |
| `test -f go.mod && cat go.mod` | passed | cli_observed |
| `find . -maxdepth 4 -type f ...` | passed | cli_observed |
| Targeted fixture scan | before findings recorded without values | cli_observed |
| `gofmt -w .` | passed after fixing generated quoting | cli_observed |
| `go test ./...` | passed | cli_observed |
| `go vet ./...` | passed | cli_observed |
| `go test -race ./... || true` | passed | cli_observed |
| `scripts/smoke.sh` | passed | cli_observed |
| `git status --short --branch` | passed | cli_observed |
| `git diff --stat` | passed; empty because repository has no initial commit/tracked diff | cli_observed |
| Post-remediation detector scan | 0 secret-value fixture findings | cli_observed |

## Security Checks

| Check | Result | Claim status |
|---|---|---|
| No generated value in CLI output regression tests | passed | cli_observed |
| No generated value in JSON output regression tests | passed | cli_observed |
| No generated value in JSONL output regression tests | passed | cli_observed |
| No generated value in structured error regression tests | passed | cli_observed |
| No generated value in smoke captured output, metadata, or evidence | passed | cli_observed |
| No `secret get` implementation | passed; only policy/docs mentions remain | repo_verified |
| No `--value` flag implementation | passed; only policy/docs mentions remain | repo_verified |
| No production plaintext backend | passed; production path returns keyring backend, test backend remains explicitly gated | repo_verified |
| Test backend gates retained | `ENV_VAULT_BACKEND=test`, `ENV_VAULT_ALLOW_INSECURE_TEST_BACKEND=1`, `ENV_VAULT_TEST_STORE=/tmp/...` | repo_verified |
| D1 inventory | not started | user_asserted |
| GitHub/remote actions | deferred; none performed | cli_observed |

## Errors And Fixes

| Issue | Action | Claim status |
|---|---|---|
| Initial smoke rewrite command hit shell quoting around embedded `sh -c` | Rewrote via literal heredoc without exposing values | cli_observed |
| First `gofmt` found generated Go newline quoting errors | Replaced real newlines with escaped newline literals and reran `gofmt` successfully | cli_observed |

## Risks

| Risk | Status | Mitigation | Claim status |
|---|---|---|---|
| Repository has no initial commit, so `git diff --stat` is empty for local changes | open | Use file list in this evidence bundle until an initial baseline exists | cli_observed |
| Detector scan is regex-based and may produce identifier false positives | accepted | Findings are classified by type and hash without printing values | cli_observed |
| AI-PDLC registry was not updated in this prompt | deferred | Repeat D0.20 acceptance with registry update gate | user_asserted |
| Real OS keychain manual verification remains outside this task | open | Keep as separate acceptance activity | unknown |

## Claim Status Summary

| Claim | Status | Evidence |
|---|---|---|
| Current repo only | repo_verified | `pwd`, scoped file list |
| No D1 inventory started | user_asserted | User scope and no D1 command run |
| No GitHub/remote action | cli_observed | `git remote -v`, no git commit/push/tag/release commands |
| Stable secret payload fixtures removed | cli_observed | Post-remediation scan: 0 secret-value fixture findings |
| Generated values do not appear in CLI/JSON/JSONL/errors/smoke/evidence | cli_observed | Go tests and smoke passed |
| `go test ./...` passes | cli_observed | Validation command |
| `go vet ./...` passes | cli_observed | Validation command |
| `scripts/smoke.sh` passes | cli_observed | Validation command |
| No production plaintext backend | repo_verified | `internal/cli/cli.go`, `internal/secretstore/keyring`, gated `teststore` |
| No `secret get` and no `--value` | repo_verified | Command scan shows policy/docs mentions only |
| Source/web verification | unknown | No web or external source citation used |

## Next Step

Repeat AI-PDLC D0.20 acceptance with the registry update gate.
