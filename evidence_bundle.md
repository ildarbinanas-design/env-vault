# env-vault Evidence Bundle

## Task ID: `ENV-VAULT-GO1265-MIGRATION-CANDIDATE`

Timestamp UTC: `2026-07-15T20:14:01Z`

### Scope

Migrate the Phase 1 E2E-protected `main` at
`7a044bdbf73aa592016bbb3a02d81f314f08fe63` from Go 1.22 to the latest stable
Go release without changing the public CLI scenarios or golden contracts. The
official [Go release history](https://go.dev/doc/devel/release) identifies
Go 1.26.5, released 2026-07-07, as the current stable patch; beta, RC, and
development versions were excluded. The migration also follows the official
[Go 1.26 release notes](https://go.dev/doc/go1.26).

### Dependency Compatibility

Published module files establish these minimum Go requirements:

| Direct module | Candidate version | Minimum Go | Migration action |
|---|---:|---:|---|
| `github.com/99designs/keyring` | `v1.2.2` | 1.19 | already current; unchanged |
| `github.com/gofrs/flock` | `v0.13.0` | 1.24.0 | controlled update from `v0.12.1` |
| `github.com/spf13/cobra` | `v1.10.2` | 1.15 | already current; unchanged |
| `golang.org/x/term` | `v0.45.0` | 1.25.0 | controlled update from `v0.29.0` |
| `gopkg.in/yaml.v3` | `v3.0.1` | no directive | already current; unchanged |

`golang.org/x/term v0.45.0` requires indirect `golang.org/x/sys v0.47.0`, whose
module requires Go 1.25.0. Go 1.26.5 therefore satisfies every selected module.
The exact updates from superseded Dependabot PR #14 were recreated on the
post-baseline branch rather than merging its pre-baseline history.

The E2E reporting tool is not a production dependency. Baseline reports retain
gotestsum `v1.12.2`; the Go 1.26.5 candidate uses latest stable `v1.13.0`
(minimum Go 1.24.0) because `v1.12.2` carries an `x/tools` version that does not
compile with Go 1.26.5. Context7 and source inspection confirm that JSONL,
JUnit, and test exit-code behavior remain available. `go-licenses v2.0.1`
remains the latest stable license tool and builds with Go 1.26.5.

### Local Candidate Evidence

Initial local candidate artifact commit:
`22fe5438bae2719885283a6e720f3c8070146f57`. Green candidate code head:
`7496fd77e4d3a566b614344e18540657363cdf88`.

| Item | Result | Claim status |
|---|---|---|
| Exact toolchain | `go1.26.5 darwin/arm64`; release-like binary embeds `go1.26.5` | cli_observed |
| Functional matrix | 22/22 critical scenarios passed; 0 failed, skipped, or missing | cli_observed |
| Feature coverage | 100% critical scenario coverage | cli_observed |
| Statement coverage | 71.1% from the separately instrumented subprocess binary; equal to Darwin arm64 baseline | cli_observed |
| Suite identity | `ace01466c8b504af9a1a2af2ec2ba3bcd9446e637044d94b4ce7d5dffa842fcf`, exactly equal to canonical baseline | cli_observed |
| Reporter | gotestsum `v1.13.0`, binary built by `go1.26.5` | cli_observed |
| Stability | two initial complete candidate executions passed; after the signal-forwarding fix, 200 focused signal scenarios, three additional shuffled full suites, the coverage suite, and all locking/concurrency burn-ins passed | cli_observed |
| Public contracts | normalized Darwin arm64 `contracts.json`, critical scenario IDs/results, and exit-code/stream contracts byte-equal to baseline | cli_observed |
| Secret safety | 125 hash-only sentinel records; zero leak findings across reports and artifacts | cli_observed |
| Release-like artifact | checksum verified; SHA-256 `5f036b8e135b92544a7e4b37bec832e26a73c0bd1a7e5b399d89cd16ace107e5` | cli_observed |
| Subject binary | SHA-256 `1c72bb7e20126c130af8bcc9c55a9bda8896bd5921a180733a8a0dfa656b2e9a` | cli_observed |

### Local Verification

| Command or check | Result | Claim status |
|---|---|---|
| `GOTOOLCHAIN=go1.26.5 go mod tidy` twice with before/after SHA-256 | passed; second run produced identical `go.mod` and `go.sum` | cli_observed |
| `GOTOOLCHAIN=go1.26.5 go mod tidy -diff` and `go mod verify` | passed | cli_observed |
| `GOTOOLCHAIN=go1.26.5 go test ./...` on a clean build cache | passed | cli_observed |
| `GOTOOLCHAIN=go1.26.5 go vet ./...` | passed | cli_observed |
| `GOTOOLCHAIN=go1.26.5 go test -race ./...` | passed | cli_observed |
| `GOTOOLCHAIN=go1.26.5 scripts/smoke.sh` | passed against a real binary | cli_observed |
| `GOTOOLCHAIN=go1.26.5 scripts/license-check.sh` | passed with pinned `go-licenses v2.0.1` | cli_observed |
| Native/cross production builds | Darwin arm64/amd64 with CGO, Linux arm64/amd64, and Windows amd64 built with embedded `go1.26.5` | cli_observed |
| Workflow contracts and changed-workflow `actionlint` | passed | cli_observed |
| Independent reviews | no migration correctness, security, report integrity, or CI blocker found | repo_verified |
| Cross-source comparator hardening | exact-source validation outcomes, fresh matrix attestations, bounded non-symlink reads, finite zero tolerance, and fail-closed unit cases passed; independently audited twice | repo_verified |
| `git diff --check` and clean tracked worktree after report generation | passed | cli_observed |

The first local artifact attempt was rejected before functional execution
because macOS `tar` injected an AppleDouble `._` entry. Repacking with
`COPYFILE_DISABLE=1` produced the release-compatible archive listed above; the
complete E2E suite was then run twice and passed. No assertion, scenario,
golden contract, retry policy, or coverage tolerance was weakened.

The first macOS amd64 CI execution exposed a real startup race in Unix signal
forwarding: the helper could become ready just before the parent installed its
signal subscription. Commit `8b5c729762bae7f27f25ef8200711b3e9e6b53b7`
subscribes before child start and safely queues an early signal. The scenario,
golden contract, and timeout were unchanged. The focused scenario then passed
200 consecutive local executions, the full candidate runner passed with
coverage and burn-ins, and all four Unix native jobs passed remotely.

### Remote Candidate Gate

The authoritative code-bearing pull-request candidate is
[Actions run 29446986126](https://github.com/ildarbinanas-design/env-vault/actions/runs/29446986126),
attempt `1`, at reviewed branch head
`7496fd77e4d3a566b614344e18540657363cdf88` and synthetic pull-request merge commit
`0c17678dc3e8bf3a9fa60b32581d9e4ca164a90a`. Every required build, smoke,
test, vet, race, module, license, native artifact E2E, matrix, comparison, and
`quality-gate` job passed. [CodeQL run 29446982881](https://github.com/ildarbinanas-design/env-vault/actions/runs/29446982881)
and [dependency review run 29446985920](https://github.com/ildarbinanas-design/env-vault/actions/runs/29446985920)
also passed.

| Platform | Passed / failed / skipped | Statement coverage | Expected skips | E2E artifact ID and SHA-256 digest |
|---|---:|---:|---|---|
| Darwin amd64 | 22 / 0 / 0 | 71.1% | none | `8355950775`, `7d135a8dc283239973c8a98aafe6d62687ee9c2e844104ea0f8ad30070720478` |
| Darwin arm64 | 22 / 0 / 0 | 71.1% | none | `8355929146`, `b7f379b3ca16eb9b62d4daa7554dca79533b9dc207a42c96031bce21b6fed41f` |
| Linux amd64 | 22 / 0 / 0 | 71.2% | none | `8355921541`, `ab767cd3832ba73b4d0df98ef10fea69f0ffe31b318bb7a5798af5c9d9957f7d` |
| Linux arm64 | 22 / 0 / 0 | 71.2% | none | `8355917517`, `bc3243156b199917da23ac8ada3775b4c4a513b722066f652f6fd0663f9da703` |
| Windows amd64 | 20 / 0 / 2 | 70.7% | `EXEC_SIGNAL_FORWARDING`, `PROFILE_SYMLINK_REJECTED` | `8355969824`, `1943f2e6a87eaa15caa4d46468ec85257e40a5fead8ff05c894e555e408da24a` |

All five reports use `go1.26.5`, gotestsum `v1.13.0`, subject kind
`artifact`, and unchanged semantic suite hash
`ace01466c8b504af9a1a2af2ec2ba3bcd9446e637044d94b4ce7d5dffa842fcf`.
Each platform records 100% critical feature coverage, no failed or missing
scenario, 125 hash-only sentinel registry entries, and zero leak findings.
The candidate matrix artifact is ID `8355983138`, digest
`c73e205a682878f49e64a9a0789cb29924094c6b17aca2236c4f7826fad308fc`.
The comparison artifact is ID `8355998341`, digest
`ff3c4722edc564ef65ce2337e98197086402956f623d1e99266920687d5b1898`.
All seven E2E evidence artifacts are retained through at least
`2026-08-14T20:08:16Z`.

The comparison independently revalidated the canonical Go 1.22.12 baseline
against baseline commit `7a044bdbf73aa592016bbb3a02d81f314f08fe63`
and the Go 1.26.5 candidate against its own source. Both source-validation
outcomes were `success`. All ten comparison checks passed: both matrices and
report sets, exact five-platform set and suite/run identities, critical
scenario results, normalized public CLI contracts, zero-tolerance statement
coverage non-regression, and secret leak gates. The downloaded artifacts were
then revalidated locally with the pinned candidate toolchain; all derived
evidence digests and coverage reports reproduced, the comparison passed again,
and a recursive sentinel-prefix scan returned no matches.

Earlier CI runs were retained as diagnostic evidence rather than rerun away.
Run `29443527036` revealed first-use toolchain download output contaminating a
coverage comparison and led to explicit exact-toolchain preload. Run
`29444212459` exposed the Unix signal startup race fixed above. Run
`29445326644` proved all native E2E jobs green, then correctly rejected
regenerating a baseline coverage profile against candidate production source;
that finding led to separate exact-source validation plus the source-neutral,
fail-closed comparator. No E2E assertion, scenario, golden contract, expected
skip, retry policy, or coverage tolerance changed across these fixes.

## Task ID: `ENV-VAULT-E2E-GO122-BASELINE-LOCAL`

Timestamp UTC: `2026-07-15T18:00:07Z`

### Scope

Establish the Phase 1 black-box compatibility baseline before any Go toolchain
or production-dependency migration. The branch starts at `origin/main`
`4fbae380747e75a1f59498adbd76ccf5791e0480`; `go.mod` remains `go 1.22`, and
`go.mod` plus `go.sum` remain byte-for-byte equal to that source commit. The
suite invokes only a real native `env-vault` executable through `os/exec`, uses
the explicitly gated disposable test backend, performs no network access, and
does not touch a platform keyring.

### Canonical Remote Baseline

The Phase 1 pull request was squash-merged before the Go migration. The
canonical post-merge `main` run is
[29441160687](https://github.com/ildarbinanas-design/env-vault/actions/runs/29441160687)
at commit `7a044bdbf73aa592016bbb3a02d81f314f08fe63`, attempt `1`, using
`go1.22.12` and gotestsum `v1.12.2`. All five native jobs, the matrix gate, and
the stable `quality-gate` passed with suite hash
`ace01466c8b504af9a1a2af2ec2ba3bcd9446e637044d94b4ce7d5dffa842fcf`.
Linux/Darwin each passed 22 scenarios at 71.1% statement coverage; Windows
passed 20 plus the two declared platform skips at 70.7%. Exact artifact and
binary digests are preserved in `docs/e2e-baseline.json`.

### Local Baseline Evidence

| Item | Result | Claim status |
|---|---|---|
| Native subject | release-like Darwin arm64 archive built with `go1.22.12` and `go build -trimpath`; embedded compiler and target verified with `go version -m` | cli_observed |
| Functional matrix | 22/22 critical scenario IDs passed; 0 failed, 0 skipped, 0 missing; critical feature coverage 100% | cli_observed |
| Subprocess statement coverage | 71.1% from a separately built `-cover -coverpkg=./...` CLI binary and `GOCOVERDIR`; not test-harness coverage | cli_observed |
| Stability | three shuffled full-suite passes plus five shuffled concurrency/locking passes succeeded with distinct recorded seeds | cli_observed |
| Suite identity | `ace01466c8b504af9a1a2af2ec2ba3bcd9446e637044d94b4ce7d5dffa842fcf` | cli_observed |
| Subject binary SHA-256 | `4585e5ceedef1b52bb61bc04c0fa969240534cb5a5ec5e205ea7d8ed681a6412` | cli_observed |
| Native archive SHA-256 | `e913e5d5b093727bce5300950d07fe3726060e90d03404dd25c089b55f1dfe5c`; sidecar checksum verified before extraction | cli_observed |
| Reporting | JUnit, raw JSONL, summaries, feature trace, normalized contracts, coverage profile/text/HTML, burn-in logs, metadata, leak result, and sanitized failure bundle generated and cross-validated | cli_observed |
| Secret safety | 125 hash-only sentinel registry records; recursive report/artifact leak scan passed with zero findings | cli_observed |
| Report integrity | immutable evidence digests, exact human-report regeneration, exact Go-version coverage regeneration, schema/count/seed checks, and current-checkout suite-hash anchoring passed | cli_observed |
| Archive-path static boundary | raw tar and ZIP entry names are rejected on any double-dot sequence directly at the archive source before a path or filesystem helper; this mirrors the existing CodeQL-approved release extractor pattern | repo_verified |

### Verification

| Command or check | Result | Claim status |
|---|---|---|
| `GOTOOLCHAIN=go1.22.12 go test ./...` | passed | cli_observed |
| `GOTOOLCHAIN=go1.22.12 go vet ./...` | passed | cli_observed |
| `GOTOOLCHAIN=go1.22.12 go test -race ./...` | passed | cli_observed |
| `GOTOOLCHAIN=go1.22.12 go mod tidy` twice plus clean diff and `go mod verify` | passed; module files unchanged | cli_observed |
| `GOTOOLCHAIN=go1.22.12 scripts/smoke.sh` | passed against a real binary | cli_observed |
| Pinned `go-licenses v2.0.1` license check | passed with its isolated Go 1.23 tool runtime; production baseline remained Go 1.22 | cli_observed |
| Native/cross build contract | production binary, E2E runner, subprocess helper, and test binary built for all five declared OS/architecture targets | cli_observed |
| Independent security/report audit | no remaining concrete leak, report-forgery, archive-extraction, or cross-platform implementation finding | repo_verified |
| `git diff --check` | passed | cli_observed |

### Claims

| Claim | Status | Evidence |
|---|---|---|
| The Phase 1 changes do not migrate Go or production dependencies | repo_verified | `go.mod` and `go.sum` are byte-identical to `origin/main`; the directive remains `go 1.22` |
| The compatibility boundary is the shipped CLI, not `internal/cli.Run` | repo_verified | the harness accepts `ENV_VAULT_E2E_BINARY` or a verified native archive and launches every CLI action with `os/exec`; no production package is imported by `e2e` |
| Every required public feature has an explicit stable scenario trace | repo_verified | `e2e/scenarios.json` maps each critical requirement to scenario ID, Go test, platforms, expected result, and the two explicit Windows skips |
| Passing reports cannot silently omit failures, platforms, evidence, or leaks | repo_verified | matrix validation fails closed on malformed/missing reports, unexpected skips, sentinel/redaction markers, mismatched identities, inconsistent derived coverage, stale suite hashes, or coverage below the gate |

### Residual Risks And Next Gate

| Risk or pending evidence | Status | Mitigation or next action | Claim status |
|---|---|---|---|
| This local evidence is Darwin arm64 only and is not the canonical remote baseline | resolved | Canonical `main` run `29441160687` passed on Linux amd64/arm64, macOS amd64/arm64, and Windows amd64; its exact SHA, identity, reports, and digests are preserved above and in `docs/e2e-baseline.json` | remote_observed |
| The final-config and lock symlink checks do not eliminate a hostile parent-directory swap | accepted | Keep the existing documented trust boundary; a future portable hardening needs handle-relative no-follow filesystem APIs | repo_verified |
| Exact Go coverage HTML validation needs the report's patch toolchain | accepted | Metadata pins the exact patch version and validation uses that recorded `GOTOOLCHAIN`; CI must fail rather than silently validate with a different template | repo_verified |

## Task ID: `ENV-VAULT-CONFIG-TRANSACTION-WAVE-2`

Timestamp UTC: `2026-07-14T22:33:44Z`

### Scope

Close the cooperative lost-update risk for profile mutations on local PR branch
`codex/security-baseline-env-vault`, starting from
`76290585b93e16f62ab1aedee1b04bae2a80cd0d`. This wave changed only the local
`env-vault` worktree. It did not commit, push, publish a release, change a remote
setting, edit `homebrew-tap` or `observability-stack`, or access a production
secret backend.

`github.com/gofrs/flock v0.13.0` was evaluated but not selected because its
official module file requires Go 1.24. The verified compatible
`github.com/gofrs/flock v0.12.1` module requires Go 1.21 and provides the needed
`New`, `SetPermissions`, `TryLockContext`, and `Unlock`/`Close` API with Darwin,
Linux, and Windows implementations. The project therefore retains its Go 1.22
directive and existing `golang.org/x/sys v0.30.0` selection.

### Changes

| Area | Purpose |
|---|---|
| `internal/config/transaction.go` | Serialize Load→mutate→Validate→same-directory Save through a bounded exclusive lock on persistent adjacent `<config>.lock` |
| Lock target boundary | Create with `flock.SetPermissions(0600)`, correct existing POSIX permissions, reject symlink/non-regular targets before acquisition and recheck after acquisition/chmod, and never remove the stable lock file |
| `internal/cli/cli.go` | Route real profile create/add/remove operations through `config.Transaction`; keep dry runs non-mutating and complete `--check-secret` backend access before the config lock |
| Structured errors | Add `CONFIG_LOCKED` with config-invalid exit status, preserved context deadline/cancellation cause, bounded five-second maximum wait, and retry remediation |
| Regression tests | Cover interprocess serialization and retained updates, concurrent CLI adds, timeout semantics, stable inode/mode, permission repair, unsafe lock targets, all three profile mutations, dry-run behavior, and backend-before-lock ordering |
| `.gitignore`, process regression | Ignore exact default local `.env-vault.yaml.lock` alongside `.env-vault.yaml` without hiding unrelated `*.lock` files |
| Dependency and notices | Pin `github.com/gofrs/flock v0.12.1`, retain Go 1.22/x/sys 0.30.0, record BSD-3-Clause in `THIRD_PARTY_NOTICES.md`, and keep a tidy module graph |
| `README.md`, `docs/design.md`, `docs/security.md` | Document the implemented transaction, persistent lock rationale, timeout contract, non-locking dry-run/backend behavior, and remaining untrusted-writer filesystem race |

### Commands And Results

| Command | Result | Claim status |
|---|---|---|
| Official module-file/source inspection for `gofrs/flock` v0.13.0 and v0.12.1 | v0.13.0 requires Go 1.24; v0.12.1 requires Go 1.21 and contains the required cross-platform API | cli_observed |
| `GOCACHE=/tmp/env-vault-transaction-target-cache-5 go test -count=3 ./internal/config ./internal/cli -run 'TestTransaction\|TestProfile\|TestConcurrentProfile'` | passed | cli_observed |
| `GOCACHE=/tmp/env-vault-transaction-ignore-cache go test ./tests -run '^TestLocalConfigAndTransactionLockAreIgnored$' -v` | passed | cli_observed |
| `GOCACHE=/tmp/env-vault-transaction-full-cache go test ./...` | passed | cli_observed |
| `GOCACHE=/tmp/env-vault-transaction-race-cache go test -race ./...` | passed, including subprocess and concurrent CLI transaction regressions | cli_observed |
| `GOCACHE=/tmp/env-vault-transaction-vet-cache go vet ./...` | passed | cli_observed |
| `go mod verify` | passed; all modules verified | cli_observed |
| `go mod tidy -diff -go=1.22` with sandbox-local `GOCACHE` | passed with no diff | cli_observed |
| Windows amd64 `go test -c` for config and CLI plus `go build ./cmd/env-vault` | passed with `CGO_ENABLED=0` | cli_observed |
| `GOCACHE=/tmp/env-vault-transaction-license-cache scripts/license-check.sh` | passed with pinned `go-licenses v2.0.1`; flock resolved as an allowed BSD dependency | cli_observed |
| `git diff --check` | passed | cli_observed |

### Claims

| Claim | Status | Evidence |
|---|---|---|
| Cooperating profile commands no longer lose a successful concurrent update to the same config | cli_observed | two subprocesses are coordinated so the second cannot enter while the first holds the transaction; final config retains both updates, and 12 concurrent CLI adds retain all mappings |
| Profile create/add/remove use the same complete transaction boundary | repo_verified | all three commands call `applyConfigMutation`, which delegates real mutations to `config.Transaction`; command regression creates, adds, and removes through one persistent lock |
| A contended transaction fails predictably instead of waiting forever | cli_observed | held-lock test observes `context.DeadlineExceeded`, `CONFIG_LOCKED`, exit 5, retry remediation, no callback, and sub-second completion under an 80 ms caller deadline |
| The cooperative lock identity remains stable between commands | cli_observed | the lock is not removed and sequential transactions observe the same file identity; interprocess and race tests pass |
| Unsafe lock targets are rejected before mutation | cli_observed | symlink and directory targets prevent callback execution; the symlink target sentinel remains unchanged |
| Secret backend existence checks do not wait inside the config transaction | cli_observed | with the config lock held elsewhere, `profile add --check-secret` returns `MISSING_SECRET` before the one-second bound rather than `CONFIG_LOCKED` |
| Dry-run remains non-mutating and the default local lock cannot be accidentally tracked | repo_verified | dry-run test observes neither config nor lock; process regression requires exact `.gitignore` entries for both local paths |
| Go 1.22 compatibility was preserved | repo_verified | `go.mod` remains `go 1.22`, flock v0.12.1 declares Go 1.21, module tidy/verify pass, and Windows cross-compilation succeeds |

### Residual Risks

| Risk | Status | Mitigation or next action | Claim status |
|---|---|---|---|
| Any untrusted process or principal with parent-directory write access through ownership, group mode, or ACLs can still swap a directory, lock pathname, or temporary pathname between path-based checks | accepted | Keep config directories non-writable by untrusted principals/processes; a stronger future implementation needs handle-relative no-follow operations such as dirfd/openat or `os.Root` once a portable design is available | repo_verified |
| The file lock coordinates cooperating env-vault processes, not direct edits or non-cooperating programs, and remote/network filesystem lock semantics may vary | accepted | Avoid editing the config concurrently outside env-vault and keep user config on a local filesystem | repo_verified |
| Windows code was cross-compiled but not executed on a native Windows host during this local wave | planned | Require the existing native Windows quality job before merge/release | cli_observed |
| Remote PR verification is outside this local evidence capture | planned | Publish the reviewed branch and require the existing GitHub checks before merge or release | cli_observed |

## Task ID: `ENV-VAULT-SECURITY-AND-DISTRIBUTION-BASELINE`

Timestamp UTC: `2026-07-14T21:51:07Z`

### Scope

Harden local runtime identifier, config-write, environment-collision, and
Homebrew-generation boundaries on local branch
`codex/security-baseline-env-vault` from clean `main` at
`859cbfebf6b6b3ed84408100741ea5bcf5df0ee1`. No real secret value, keychain
item, password-store entry, remote ref, commit, tag, release, or remote setting
was changed. The coordinated local tap branch
`codex/distribution-hardening-tap` at base
`f8f9897595914a21e657c7f6a1ce106e47867dfb` was read to align the upstream
generator; this env-vault wave did not edit any `homebrew-tap` file.

### Changes

| Area | Purpose |
|---|---|
| `internal/secretstore`, `internal/config`, `internal/cli` | Centralize secret/service validation; preserve safe slash hierarchy while rejecting absolute, backslash, empty, `.` and `..` path forms |
| `internal/secretstore/keyring` | Repeat identifier validation immediately before backend access; fake-pass regression proves the exact safe prefix and proves unsafe input cannot invoke the backend |
| `internal/config` | Reject existing and dangling config symlinks; publish a synced mode-`0600` temporary sibling with same-directory replacement instead of truncating the target |
| `internal/platform`, `internal/runner` | Use one case-insensitive portable environment key for mapping duplicates, inherited collisions, override replacement, and `--clean-env`, including Windows `Path`/`PATH` |
| Go regression tests | Cover traversal/service variants, safe slash names, fake-pass argv, symlink targets, concurrent complete-file visibility, case-only mapping duplicates, override deduplication, and Windows-style minimal env |
| `scripts/release/generate-homebrew-formula.sh` | Emit Homebrew-native `on_macos`/`on_linux` plus `on_arm`/`on_intel` blocks, declare macOS Sequoia as the minimum, and install the three archived documentation files without changing version, URL, or checksum inputs |
| `tests/workflows_test.go` | Require the release build to archive all three documentation files; generate four fake archive/checksum pairs; verify exact platform/architecture URL/checksum placement; reject `Hardware::CPU` branching; require minimum/docs/exact-version behavior; and pass the generated result through `verify-homebrew-formula.sh` |
| `README.md`, `RELEASING.md`, `docs/design.md`, `docs/security.md` | Document the implemented runtime boundaries, macOS 15+ support floor, generated-formula contract, and remaining transaction-lock limitation |

### Commands And Results

| Command | Result | Claim status |
|---|---|---|
| `git switch -c codex/security-baseline-env-vault` | passed from clean `main` at the recorded source SHA | cli_observed |
| `gofmt -w` on changed Go files | passed | cli_observed |
| targeted modified-package `go test` with sandbox-local `GOCACHE` | passed | cli_observed |
| `GOCACHE=/tmp/env-vault-security-full-cache go test ./...` | passed | cli_observed |
| `GOCACHE=/tmp/env-vault-security-race-cache go test -race ./...` | passed | cli_observed |
| `GOCACHE=/tmp/env-vault-security-vet-cache go vet ./...` | passed | cli_observed |
| `go test -count=10 ./internal/config ./internal/runner ./internal/secretstore/keyring` with sandbox-local `GOCACHE` | passed | cli_observed |
| Windows amd64 `go test -c` for platform, secretstore, keyring, config, runner, and CLI packages | passed | cli_observed |
| `GOCACHE=/tmp/env-vault-homebrew-target-cache-2 go test ./tests -run '^TestGeneratedHomebrewFormulaPreservesDistributionContract$' -v` | passed; verified the workflow docs-copy contract, generated four temporary archive/checksum fixtures and the formula, then passed `verify-homebrew-formula.sh` | cli_observed |
| `gh release download v0.0.6`, regenerate from all ten published assets, then `cmp` with the coordinated tap formula | passed; both formula files have SHA-256 `fb3d7c888f758379a2ebc6b119ccdc49b079066fcf30b75ea2044bc764f3ca52` | remote_observed |
| List the published Darwin arm64 and Linux amd64 archive members | passed; both contain `README.md`, `LICENSE`, `THIRD_PARTY_NOTICES.md`, and the binary | remote_observed |
| `shellcheck -x scripts/release/generate-homebrew-formula.sh scripts/release/verify-homebrew-formula.sh` | passed | cli_observed |
| `GOCACHE=/tmp/env-vault-homebrew-full-cache-2 go test ./...` | passed after the final generator/test synchronization | cli_observed |
| `GOCACHE=/tmp/env-vault-homebrew-vet-cache-2 go vet ./...` | passed after the final generator/test synchronization | cli_observed |
| `git diff --check` | passed | cli_observed |

### Claims

| Claim | Status | Evidence |
|---|---|---|
| `pass` operations cannot use a secret or service traversal to leave the `env-vault` prefix | repo_verified | shared component validation plus adapter-level validation; fake-pass tests verify safe argv and zero calls for unsafe identifiers |
| Existing safe slash-separated secret and service names remain supported | repo_verified | validator and fake-pass positive regression cases |
| A config target symlink is not followed, created through, or replaced | cli_observed | existing-target and dangling-target tests preserve the symlink and outside target state |
| Readers do not see a truncated config during concurrent saves | cli_observed | concurrent writer/reader regression plus same-directory temporary-file replacement and race test |
| Environment mapping identity is portable to Windows | repo_verified | canonical key is used for config duplicate, runtime collision, replacement, and minimal-env selection; Windows-style tests and Windows cross-compilation pass |
| The next generated formula cannot silently return to `Hardware::CPU.arm?` or omit the macOS minimum/documentation contract without failing the workflow regression suite | repo_verified | generated-fixture test requires exact DSL counts, Sequoia dependency, documentation installation, archived documentation inputs, and absence of `Hardware::CPU` |
| Each generated architecture URL retains the SHA-256 of its matching archive | cli_observed | the regression creates four distinct archive bytes/checksums, requires each exact platform/selector URL/checksum block, and runs the project's exact formula verifier |
| The published `v0.0.6` assets regenerate the coordinated local tap formula byte-for-byte | remote_observed | all ten release assets were downloaded read-only into a temporary directory; regenerated and tap formula SHA-256 values both equal `fb3d7c888f758379a2ebc6b119ccdc49b079066fcf30b75ea2044bc764f3ca52` |
| The formula's documentation install inputs exist in the published archives | remote_observed | member listings for Darwin arm64 and Linux amd64 contain all three documentation files; the release workflow uses the same packaging step for every target |
| No remote or production secret-store mutation occurred | cli_observed | only local Git/file commands, fake pass, generated non-secret fixtures, and local test processes were used |

### Residual Risks

| Risk | Status | Mitigation or next action | Claim status |
|---|---|---|---|
| Profile commands still perform load-modify-save outside one inter-process lock, so concurrent successful commands can logically lose an update | open | Introduce a cross-platform locked config transaction API and move profile create/add/remove into it; current replacement save prevents truncation/corruption only | repo_verified |
| A hostile same-user process that can swap a parent directory or replace the temporary filename after close remains outside the target-symlink fix | accepted | Keep config directories user-owned; a stronger future implementation should use dirfd/openat or `os.Root` no-follow operations | repo_verified |
| Windows behavior was cross-compiled but not executed on a native Windows host in this local run | planned | Require the existing Windows CI runner to execute focused runtime tests before release | cli_observed |
| Real Keychain, Secret Service, WinCred, KWallet, and `pass` stores were not exercised | accepted | Run separately with disposable identifiers only after review; fake-pass tests cover the namespace boundary without touching user data | cli_observed |
| Generator synchronization and the coordinated tap hardening exist only on local, uncommitted branches; no formula was published and no release was created | planned | Review both local diffs together, then use the normal PR and release gates only after explicit authorization | cli_observed |
| This env-vault run did not execute `brew style`, install, or test against a live tap checkout | planned | The coordinated `homebrew-tap` wave owns those platform checks; upstream protects the generator contract with fake archives, ShellCheck, Go regression tests, and the exact formula verifier | cli_observed |

## Task ID: `ENV-VAULT-RELEASE-APP-CUTOVER-COMPLETE`

Timestamp UTC: `2026-07-10T22:46:27Z`

### Scope

Complete the least-privilege GitHub App cutover, publish `v0.0.6`, require the
exact Homebrew pull-request and post-merge CI chain to succeed, verify the
installed formula, and retire the legacy deploy credentials only after the
release health gate passed. No existing tag was moved and no existing Release
asset was replaced.

### Publication Evidence

| Item | Result | Claim status |
|---|---|---|
| GitHub App scope audit [29128162315](https://github.com/ildarbinanas-design/env-vault/actions/runs/29128162315) | passed at source `76c9ac760b9d98752d737a1875339ac3ca2de0e5`; the metadata-only audit observed exactly `ildarbinanas-design/homebrew-tap` | remote_observed |
| Release workflow [29128230296](https://github.com/ildarbinanas-design/env-vault/actions/runs/29128230296) | passed metadata, monotonic preflight, test/vet/race/smoke, five builds, five native exact-version smokes, three native license scans, Release, supply-chain, Homebrew, and health jobs | remote_observed |
| Release [`v0.0.6`](https://github.com/ildarbinanas-design/env-vault/releases/tag/v0.0.6) | published, non-draft, non-prerelease; the lightweight tag resolves to `76c9ac760b9d98752d737a1875339ac3ca2de0e5`, and release run 29128230296 was dispatched from `main` at that same source SHA | remote_observed |
| Release assets | exactly five archives plus their five `.sha256` companions; the workflow verified every pair and rejected replacement semantics | remote_observed |
| [Supply-chain evidence](https://github.com/ildarbinanas-design/env-vault/attestations) | SPDX SBOM workflow artifact generated; the project verifier cryptographically verified one SLSA provenance and one SPDX attestation for each archive against the exact signer workflow and release source | remote_observed |
| Homebrew PR [#3](https://github.com/ildarbinanas-design/homebrew-tap/pull/3) | exact head `b70542691637345922214d5a495d55fdfe9c83ea` passed [PR CI 29128368672](https://github.com/ildarbinanas-design/homebrew-tap/actions/runs/29128368672) and squash-merged without bypass | remote_observed |
| Homebrew publication | the formula publication squash commit is `f8f9897595914a21e657c7f6a1ce106e47867dfb`; exact [post-merge push CI 29128402784](https://github.com/ildarbinanas-design/homebrew-tap/actions/runs/29128402784) passed at that commit | remote_observed |
| Published formula | `version "0.0.6"`; exact `assert_equal "v#{version}", shell_output("#{bin}/env-vault --version").strip` | remote_observed |
| Local Homebrew verification | upgraded only `env-vault` to `0.0.6`; both version commands returned exactly `v0.0.6`; `brew style` and `brew test` passed | cli_observed |
| Final repository verification | gofmt, module verify/tidy diff, `go test ./...`, vet, race, smoke, pinned license scan, workflow regression tests, targeted ShellCheck, actionlint with exact stale-schema/intentional-literal diagnostics excluded, and diff checks passed | cli_observed |
| Legacy credential retirement | repository secret `TAP_DEPLOY_KEY` and tap write deploy key `156891216` were deleted after health succeeded; follow-up lists contain neither | remote_observed |
| Preserved manual-binary backup | still present at the requested path; observed size `7,531,074` bytes and mtime `2026-07-09T09:34:44+0300`; it was not modified or deleted | cli_observed |

### Final Trust Boundary

| Control | Verified state | Claim status |
|---|---|---|
| GitHub App | private App with Actions read, Contents write, Pull requests write; installation scope is exactly `homebrew-tap` | remote_observed |
| `release` environment | contains only App client-id/private-key names for this flow; deployment policies are `main` and tag `v*` | remote_observed |
| `env-vault` main ruleset | no bypass actor; PR, thread resolution, strict `quality-gate`, `Dependency review`, `Analyze (go)`, and `Analyze (actions)` checks | remote_observed |
| `homebrew-tap` main ruleset | no bypass actor; PR, thread resolution, squash-only merge, required `test`, force-push/deletion blocked | remote_observed |
| Tap writer | the scoped GitHub App is the only configured release writer; legacy repository secret and deploy key are absent | remote_observed |

### Residual Risk

Archive extraction still has a theoretical parent-directory symlink-swap TOCTOU
between path inspection and file creation. Release extraction runs in a private
ephemeral runner output directory where a hostile concurrent local process is
outside the threat model. A generalized extractor should move to `os.Root` or
dirfd/openat with no-follow semantics.

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

---

# Automated Release Planning Evidence — 2026-07-15T21:23:05Z

## Scope

This implementation run adds reviewed SemVer planning and exact-tag handoff
without creating a tag or public release locally or remotely. The source base
is `a4e9a5169959666a50f5022194d0b802cf3edac8` on local branch
`agent/automated-release`.

The changed scope is limited to:

- Release Please manifest/configuration and the bootstrapped `CHANGELOG.md`;
- protected release-planning, App-scope-audit, CI title, and existing publisher
  workflows;
- fail-closed release commit, generated-PR authorization, changelog extraction,
  and lifecycle-label helpers;
- workflow/release-script regression tests; and
- contributor, architecture, external-settings, release, and user
  documentation.

No CLI secret behavior, production secret backend, product dependency, Go
toolchain directive, release tag, GitHub Release, or Homebrew formula was
changed by this run.

## Implemented Controls

| Control | Result | Claim status |
|---|---|---|
| Version ownership | `.release-please-manifest.json` bootstraps the published `0.0.7`; no manual bump is included in this infrastructure branch | repo_verified |
| Documentation/version boundary | A generated PR updates the manifest, exact `CHANGELOG.md` section, and the single README version marker together | repo_verified |
| Release Please strategy | Go strategy, manifest mode, PR-only, separate component PRs, literal project name in the title, and pinned v17.6.0 schema preserve the exact reviewed branch/title contract while keeping tags component-free | repo_verified |
| Publication authorization | Merge of the generated release PR plus successful exact-SHA `ci` push run is required | repo_verified |
| Generated PR provenance | Expected App bot, branch, title, stable footer, lifecycle label, base repo/branch, and merge SHA are checked | repo_verified |
| Stale/detached protection | Current manifest equality, current-main ancestry, and exact successful CI are checked before tag/publication; every open proposal is one exact three-file commit on a green main base | repo_verified |
| Tag-trigger protection | `build-binaries` repeats deterministic commit and generated-PR authorization before any public mutation | repo_verified |
| Manual recovery boundary | Publisher dispatches can only resolve an existing tag; `v0.0.8+` repeats generated-PR authorization, while published `v0.0.1`–`v0.0.7` require an existing stable Release and tag ancestry | repo_verified |
| Release Please lifecycle | Exact tag verification is followed by idempotent `pending` to `tagged` reconciliation; the serialized publisher requires tagged-only state with a bounded deadline | repo_verified |
| Lifecycle label bootstrap | Planning idempotently creates/normalizes both Release Please repository labels before opening a proposal | repo_verified |
| Merge-message integrity | Planning and the manual audit require squash-only `PR_TITLE` plus `PR_BODY`, strict checks, immutable `v*` tags, and no App bypass; rebase/merge commits fail closed | repo_verified |
| Publisher ownership | `build-binaries` remains the sole GitHub Release/assets/attestation/Homebrew publisher | repo_verified |
| Release notes | The public Release body is the reviewed, non-empty version section fetched from `CHANGELOG.md` at the exact tag source SHA | repo_verified |
| External Actions | Checkout, setup, artifact, attestation, SBOM, dependency review, Release Please v5.0.0, and App-token v3.2.0 are pinned to verified full commit SHAs | source_verified |
| PR title integrity | a separately required lightweight `pr-title` workflow handles `pull_request.edited`; metadata edits cannot rerun or replace the code-bearing `quality-gate` | repo_verified |
| Human publication authorization | The generated PR has a stable body header stating that merge authorizes its exact release; proposal and merged-PR gates require the marker | repo_verified |
| Release serialization | Planning and publication share non-cancelling `env-vault-release` concurrency, preventing label/tag handoff and successive-version races | repo_verified |
| Future generated-PR CI | Workflow tests derive strict SemVer from the manifest rather than pinning `0.0.7`, so the generated version-only PR remains testable | repo_verified |
| Secrets | No credential value was read, printed, persisted, or added to evidence | cli_observed |

## Commands And Results

| Command or action | Result | Claim status |
|---|---|---|
| Context7 plus official Release Please/GitHub documentation review | confirmed manifest mode, generic marker, title tokens, workflow recursion, App token, action inputs, and current stable action releases | source_verified |
| GitHub API release/tag and signature reads for privileged actions | `release-please-action` v5.0.0 commit and `create-github-app-token` v3.2.0 commit are verified | source_verified |
| `go test ./tests` | passed after release workflow, script, authorization, and Windows portability tests | cli_observed |
| `go test ./... -count=1` | passed on the release-automation tree, including E2E runner and workflow contracts | cli_observed |
| `go vet ./...` | passed on the release-automation tree | cli_observed |
| `go test -race ./... -count=1` | passed; current release/workflow tests were repeated under race after the final ruleset changes | cli_observed |
| `scripts/smoke.sh` | clean unrestricted rerun passed: `smoke ok` | cli_observed |
| `scripts/license-check.sh` | unrestricted rerun passed with pinned go-licenses v2.0.1; expected non-Go assembly warning only | cli_observed |
| `go mod tidy -diff`; `go mod verify` | no diff; all modules verified | cli_observed |
| `shellcheck -x` on all new release helpers | passed | cli_observed |
| `bash -n` on all new release helpers | passed | cli_observed |
| Official v17.6.0 JSON schema + temporary pinned `ajv-cli@5.0.0` | `release-please-config.json valid`; only the schema's unsupported `uri-reference` format was ignored | cli_observed |
| `git diff --check`; `gofmt`; `bash -n`; `shellcheck -x` | passed after final documentation, workflow, test, and helper edits | cli_observed |
| Independent security, API/source, action-pin, and test audits | identified and drove fixes for Release Please merged-PR defaults, component/title rendering, version rendering, proposal-base TOCTOU, future generated-PR CI, explicit human authorization, lifecycle races, API pagination, App identity, rulesets, action pinning, and capability overclaims | cli_observed |
| Read-only live repository settings gate | passed after the authorized squash-only merge settings and exact active main/tag rulesets were applied | cli_observed |

## Authorized Remote Setup Update — 2026-07-15T22:39:50Z

The user explicitly authorized the infrastructure commit, push, pull request,
repository settings, GitHub App, and environment configuration. Infrastructure
PR [#17](https://github.com/ildarbinanas-design/env-vault/pull/17) was opened
from `agent/automated-release` at
`62dea01afaf1de2e66e347abba0996e8e8523907`. Its first complete CI run
[29454761680](https://github.com/ildarbinanas-design/env-vault/actions/runs/29454761680),
CodeQL run
[29454759495](https://github.com/ildarbinanas-design/env-vault/actions/runs/29454759495),
and dependency-review run
[29454761485](https://github.com/ildarbinanas-design/env-vault/actions/runs/29454761485)
all passed, including the five-platform native E2E matrix and stable
`quality-gate`.

Authorized external state now matches the documented contract:

- repository merge settings allow squash only, with pull-request title and
  body as the squash title/body;
- active main ruleset `18792628` requires the exact strict checks, resolved
  conversations, and squash-only pull requests, with an empty bypass list;
- active tag ruleset `19015306` protects `refs/tags/v*` from updates and
  deletion, with an empty bypass list while allowing initial creation;
- `release-planning` permits only branch `main`, has no reviewers or wait
  timer, contains public variable `RELEASE_APP_CLIENT_ID`, and contains secret
  `RELEASE_APP_PRIVATE_KEY`;
- App `env-vault-release-planning` (App ID `4309657`, installation
  `146851190`) is installed only on `env-vault`, with Administration and
  Metadata read plus Contents, Issues, and Pull requests read/write; and
- `scripts/release/verify-repository-release-settings.sh` passes against the
  live repository.

The private key was validated before upload without printing its contents,
stored through `gh secret set` on standard input, and removed from the local
filesystem. The inaccessible bootstrap key was revoked; exactly the newly
uploaded key remains active. Secret values were not read back or recorded.

## Risks And Pending Evidence

| Risk or pending evidence | Status | Mitigation or required action | Claim status |
|---|---|---|---|
| Dedicated `env-vault-release-planning` GitHub App and `release-planning` environment are external state | configured | Exact least-privilege installation, environment branch policy, variable, and secret were configured; dispatch the committed read-only App audit after infrastructure merge | remote_observed |
| Repository merge settings | configured | Squash-only `PR_TITLE` plus `PR_BODY` is active and the live settings verifier passes | remote_observed |
| Main and immutable release-tag rulesets | configured | Exact active main and `refs/tags/v*` rulesets are present and the live settings verifier passes | remote_observed |
| Global ruleset bypass actors are visible only to a ruleset writer | resolved_for_setup | Administrator inspection recorded empty main/tag bypass lists; repeat during App/key rotation | remote_observed |
| Release Please lifecycle labels are currently absent in the repository | mitigated_in_code | The scoped planning App idempotently creates and verifies both definitions before the first proposal | cli_observed |
| Exact App bot login/branch/body/label contract has not yet been observed in this repository | open | The implementation pins the upstream version and tests fixtures; confirm the first generated PR before merge | source_verified |
| No real tag-triggered automated release has run | open | Merge the infrastructure PR only after green CI, inspect the generated release PR, then record the first planning and publisher run URLs | unknown |
| Release Please reads the remote branch and cannot lock `main` during its API call | mitigated_in_code | Post-action validation requires the proposal to be one exact commit over a main SHA with successful push CI; shared concurrency and repeated exact-SHA publication checks fail closed | repo_verified |
| Planning App `Contents: write` also technically permits Release API calls, and PR/contents write could merge a green PR | accepted | GitHub permission granularity cannot split these operations; exact action pins and workflow contract tests prove no Release/asset/merge endpoint exists in the planning path | source_verified |
| Published `v0.0.1`–`v0.0.7` predate Release Please metadata | accepted | A bounded manual-only compatibility path requires their existing stable GitHub Release, immutable exact tag, and ancestry; it cannot create a tag or authorize `v0.0.8+` | repo_verified |
| Remote publication requires exact approval under `AGENTS.md` | pending_exact_release | Infrastructure mutation was explicitly authorized. Do not merge the generated release PR or create its exact tag until the user reviews and approves that version and SHA | cli_observed |
| Expected next version is `v0.0.8`, but Release Please is authoritative | pending | Do not edit the manifest manually; verify the generated release PR's computed version | source_verified |

## Claim Status Summary

| Claim | Status | Evidence |
|---|---|---|
| No manual version bump in implementation branch | repo_verified | manifest remains `0.0.7`; README marker remains `v0.0.7` |
| Automatic PR-only version/changelog preparation implemented | repo_verified | release config, planning workflow, workflow tests |
| Exact generated-PR and CI authorization implemented | repo_verified | authorization helper, tag gate, publisher entry gate, fail-closed tests |
| Public release remains single-owner | repo_verified | Release Please skip setting and `build-binaries` publication step |
| Remote release automation operational | partially_verified | external settings and PR #17 CI are green; the committed App audit and first Release Please proposal still require post-merge execution |
| Authorized infrastructure mutation recorded | remote_observed | PR #17, exact commit/run URLs, ruleset IDs, environment, App installation, and key-handling evidence are recorded above |

## PR #17 Windows Replacement Resilience — 2026-07-15T23:17:48Z

### Scope

Full CI run
[29456774217](https://github.com/ildarbinanas-design/env-vault/actions/runs/29456774217)
failed only the Windows E2E burn-in job
[87491786025](https://github.com/ildarbinanas-design/env-vault/actions/runs/29456774217/job/87491786025):
one of three shuffled full-suite repetitions reported
`CONCURRENCY_PROFILE_MUTATIONS` exit `5` / `CONFIG_INVALID` while replacing the
existing config. The release-like and coverage passes, the other two shuffled
full-suite repetitions, and all five locking-only repetitions passed. The
sanitized failure bundle contained no secret sentinel.

The fix keeps the Unix path at one target validation and one `os.Rename`. On
Windows only, config-target inspection and same-directory replacement retry
`ERROR_ACCESS_DENIED`, `ERROR_SHARING_VIOLATION`, and
`ERROR_LOCK_VIOLATION` for at most one second with 25 millisecond polling.
Every replacement attempt revalidates the target; typed symlink/non-regular
errors fail immediately, permanent filesystem errors remain failures, the
prior config is never removed first, and the existing deferred cleanup removes
an uncommitted temporary sibling. No E2E scenario, golden contract, or
baseline identity changed.

The instrumented Windows E2E binary deterministically returns one native
sharing errno before replacing an existing regular test config, but only when
the full insecure test-backend gate, E2E child marker, and `GOCOVERDIR` are
present, `runtime/coverage` confirms the binary was built with `-cover`, and
store/config paths remain inside the isolated scenario root. Existing mutation
and concurrency assertions therefore exercise the retry path without changing
public contracts or the suite hash. The native E2E
matrix also runs the Windows-only deterministic config tests before the binary
suite. A fully requested hook with a missing coverage build identity fails
closed, so a regression in runtime detection cannot silently skip this
exercise in the Windows coverage pass.

The canonical manifest's legacy `atomic` labels remain byte-for-byte unchanged
to preserve the reviewed Go 1.22 suite hash. Current documentation explicitly
limits the Windows assertion to complete readable YAML, preservation before
replacement, and cleanup of temporary siblings; it does not claim an OS-level
atomicity guarantee.

### Commands And Checks

| Command or evidence | Result | Claim status |
|---|---|---|
| Context7 `/golang/go/go1.26.0` official `os.Rename` documentation and Go Windows source | confirmed non-Unix atomicity limitation, `MoveFileEx(..., MOVEFILE_REPLACE_EXISTING)`, and known antivirus-induced `ERROR_ACCESS_DENIED` behavior | source_verified |
| Official `runtime/coverage.WriteMeta(io.Discard)` probe in plain and `go build -cover` executables | returned `false` for the release-like build and `true` for the instrumented build, establishing a non-env build-identity gate | cli_observed |
| Sanitized Windows run artifact inspection | isolated the failure to config replacement during one burn-in repetition; 19 files and 125 registry records scanned with zero sentinel findings | cli_observed |
| Deterministic Windows retry tests | cover retryable vs permanent errors, positive deadline `25+25+20 ms`, last-error return, target revalidation, typed unsafe target rejection, transient inspection retry, wrapped Windows errno classification, coverage build identity, isolated-path enforcement, and complete E2E injection gating without wall-clock sleeps | repo_verified |
| `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test -exec=/usr/bin/true ./...` | all Windows packages and tests compile | cli_observed |
| `go test ./... -count=1`; `go vet ./...`; `go test -race ./... -count=1` | passed after the final platform split, fail-fast unsafe-target fix, native Windows test gate, and coverage-only injection gate | cli_observed |
| `go mod tidy`; repeated `go mod tidy -diff`; `git diff --check` | clean; existing `golang.org/x/sys v0.47.0` is now a direct dependency of the Windows implementation | cli_observed |
| Pinned actionlint on `reusable-quality.yml`; workflow contract tests | passed with the native platform config-test step required before E2E | cli_observed |
| `GOTOOLCHAIN=go1.26.5 go run ./e2e/cmd/e2e-runner run --phase candidate` | final Darwin arm64 pass: 22 passed, 0 failed/skipped/missing, 100% critical feature coverage, 71.3% statement coverage, suite hash `ace01466c8b504af9a1a2af2ec2ba3bcd9446e637044d94b4ce7d5dffa842fcf` | cli_observed |

### Risks And Claim Status

| Risk or claim | Status | Mitigation or required evidence | Claim status |
|---|---|---|---|
| Exact native Windows errno from the failed process | unavailable by design | sanitized public error contracts omit internal causes; failure point and subsequent success establish a transient replacement-error class, not a claim about a specific scanner process | cli_observed |
| Native Windows retry execution | pending updated PR CI | The native E2E matrix now runs `go test ./internal/config -count=1`; Windows-only deterministic tests and the unchanged E2E burn-in must pass on `windows-latest` before merge | repo_verified |
| Permanent ACL or read-only failure | preserved | narrow retries expire after one second and return the last error without changing permissions or deleting the destination | repo_verified |
| Unix behavior and E2E coverage | preserved | platform build tags retain the single-attempt path; final subprocess coverage is 71.3%, above the 71.1% Darwin baseline | cli_observed |
| Secret handling | preserved | no real secret or sentinel appears in code, reports, downloaded artifacts, or this evidence | cli_observed |

## PR #17 Windows Concurrent Read Resilience — 2026-07-16T04:50:19+05:00

### Scope

Updated full CI run
[29459242684](https://github.com/ildarbinanas-design/env-vault/actions/runs/29459242684)
failed its native Windows config-test step in job
[87499276143](https://github.com/ildarbinanas-design/env-vault/actions/runs/29459242684/job/87499276143).
`TestConcurrentSavePublishesOnlyCompleteConfigs` observed
`CONFIG_INVALID: Unable to read config` while twelve writers replaced the same
config. This was an open/read failure, not an invalid-YAML failure; the exact
wrapped Win32 errno is intentionally absent from the public error string and is
not claimed here. The E2E steps did not start, and the missing-artifact error
was secondary to that prerequisite failure.

Windows `Load` now uses the same one-second, 25-millisecond bounded retry helper
as replacement, and retries only `ERROR_ACCESS_DENIED`,
`ERROR_SHARING_VIOLATION`, and `ERROR_LOCK_VIOLATION`. Unix still calls
`os.ReadFile` exactly once. Missing files retain the existing empty-config
behavior, YAML/schema failures are not retried, non-whitelisted errors return
immediately, and a persistent whitelisted error is returned after the deadline.
The concurrency assertion remains unchanged.

The next exact-SHA run
[29460098566](https://github.com/ildarbinanas-design/env-vault/actions/runs/29460098566),
job
[87501740067](https://github.com/ildarbinanas-design/env-vault/actions/runs/29460098566/job/87501740067),
then completed the concurrent read/write portion and failed only the final
test assertion `config mode=0666, want 0600`. Go's Windows `FileMode` does not
model POSIX `0600`; the E2E and lock tests already enforce that permission bit
only outside Windows, matching the documented "where applicable" contract.
The unit test now always requires a regular file and requires exact `0600` on
non-Windows platforms. No production assertion or public E2E contract changed.
An exhaustive mode-assertion scan found the same non-portable expectation in
the E2E comparison report test; it now also requires a regular file everywhere
and exact `0600` only where POSIX mode bits apply.

### Commands And Checks

| Command or evidence | Result | Claim status |
|---|---|---|
| Native Windows job log inspection | isolated the primary failure to `go test ./internal/config -count=1`; the failure was a concurrent open/read error before E2E startup | remote_observed |
| Follow-up native Windows job log inspection | read/write integrity completed; isolated the failure to a non-portable POSIX mode-bit assertion after `os.Stat` returned the normal Windows regular-file mode `0666` | remote_observed |
| Cross-platform permission-assertion audit | found and corrected the equivalent E2E comparison-report test; all other critical mode assertions were already Windows-aware or inside an explicit Windows skip | repo_verified |
| Deterministic Windows read tests | cover transient success, permanent failure, and the wrapped `os.PathError` shape returned by `os.ReadFile` | repo_verified |
| `go test ./internal/config -count=1`; `go test ./... -count=1`; `go vet ./...`; `go test -race ./... -count=1` | passed locally after the read retry | cli_observed |
| Windows config test cross-compile and `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go vet ./internal/config` | passed | cli_observed |
| `go test ./tests -count=1`; pinned actionlint v1.7.12 on `reusable-quality.yml` | passed with the Windows-only ten-repetition concurrency burn-in contract | cli_observed |
| Release-like cross-builds for linux amd64/arm64, darwin amd64/arm64, and windows amd64; Windows coverage cross-build | passed with Go 1.26.5 and `CGO_ENABLED=0` | cli_observed |
| `go mod tidy -diff`; `scripts/smoke.sh`; `scripts/license-check.sh`; `git diff --check` | passed; license check used pinned go-licenses v2.0.1 with only the expected x/sys assembly warning | cli_observed |
| `GOTOOLCHAIN=go1.26.5 go run ./e2e/cmd/e2e-runner run --phase candidate` | Darwin arm64 pass: 22 passed, 0 failed/skipped/missing, 100% critical feature coverage, 71.4% statement coverage, unchanged suite hash `ace01466c8b504af9a1a2af2ec2ba3bcd9446e637044d94b4ce7d5dffa842fcf` | cli_observed |
| Final leak scan | 18 report files and 125 sentinel-registry records scanned; zero findings | cli_observed |
| Two independent read-only audits | both returned green with no P0-P2 findings; confirmed bounded/whitelisted retry, unchanged Unix and missing-file semantics, and no assertion weakening | cli_observed |

### Risks And Claim Status

| Risk or claim | Status | Mitigation or required evidence | Claim status |
|---|---|---|---|
| Exact underlying Win32 errno | unavailable by design | public structured errors omit causes; implementation covers only the three documented transient contention classes and preserves every other error | cli_observed |
| Native Windows concurrency test after the read and test-portability fixes | pending updated PR CI | the Windows E2E job requires the package once plus ten sequential focused concurrency runs before the unchanged full E2E/burn-in/coverage job | repo_verified |
| Permanent access failure latency | bounded | a whitelisted but persistent Windows access error adds at most one second and then returns the last error | repo_verified |
| Unix behavior | unchanged | build-tagged implementation performs one `os.ReadFile` call and the existing single replacement attempt | repo_verified |

## Post-merge read-only release App audit — 2026-07-16

### Scope

The merged `main` CI run
[29460867618](https://github.com/ildarbinanas-design/env-vault/actions/runs/29460867618)
completed successfully, including all five native E2E jobs, report validation,
baseline comparison, and `quality-gate`. The first manual planning-App audit
[29460920983](https://github.com/ildarbinanas-design/env-vault/actions/runs/29460920983)
minted the expected `env-vault-release-planning` installation token and then
failed before ruleset inspection. The token intentionally has only Metadata
read and Administration read. GitHub's REST repository response omitted the
merge-policy fields for that non-pushing caller, so the fail-closed verifier
correctly rejected an incomplete response even though an administrator
independently observed the configured squash-only `PR_TITLE`/`PR_BODY` policy.

The verifier now requests the same policy through the typed GitHub GraphQL
`Repository` fields. Those fields are non-null in the current schema. The
manual audit retains its read-only App permissions; missing repository data,
unsafe values, and GraphQL transport failures all remain hard failures. The
full release-planning token uses the same verifier, so proposal planning and
manual auditing continue to enforce one repository-settings contract.

### Evidence and status

| Evidence | Result | Claim status |
|---|---|---|
| Main push CI run `29460867618` | green through `quality-gate` | remote_observed |
| Manual App audit run `29460920983`, job `87503977775` | exact App identity passed; REST merge-settings read failed before ruleset checks | remote_observed |
| GitHub GraphQL schema introspection | required merge-policy fields are present and non-null; live administrator query returned the configured squash-only values | cli_observed |
| Release automation tests | cover safe settings, unsafe rebase, missing GraphQL repository, transport failure, partial data with GraphQL errors, and unsafe main/tag rulesets | repo_verified |
| App permission contract | remains Metadata read plus Administration read, with no Contents, Issues, or Pull requests permission in the audit workflow | repo_verified |
| `GITHUB_REPOSITORY=ildarbinanas-design/env-vault scripts/release/verify-repository-release-settings.sh` | corrected GraphQL plus unchanged REST ruleset verifier passed against live settings | cli_observed |
| `go test ./... -count=1`; `go vet ./...`; `go test -race ./... -count=1` | passed after restoring the unrelated REST fixture used by release-authorization tests | cli_observed |
| Focused release/workflow tests; `shellcheck -x` on the changed verifier; `scripts/smoke.sh`; `scripts/license-check.sh` | passed; license check used pinned go-licenses v2.0.1 with only the expected x/sys assembly warning | cli_observed |
| `go mod tidy`; repeated `go mod tidy -diff`; `git diff --check` | clean with no module-file change | cli_observed |
| `GOTOOLCHAIN=go1.26.5 go run ./e2e/cmd/e2e-runner run --phase candidate` | 22 passed, 0 failed/skipped/missing, 100% critical feature coverage, 71.4% statement coverage, unchanged suite hash `ace01466c8b504af9a1a2af2ec2ba3bcd9446e637044d94b4ce7d5dffa842fcf`; 18 report files and 125 registry records scanned with zero leak findings | cli_observed |

The corrected workflow must pass its pull-request CI, merge through the normal
protected path, and then pass a fresh manual App audit on `main` before the
release proposal is eligible for reviewed publication.
