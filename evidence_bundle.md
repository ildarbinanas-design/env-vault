# env-vault Evidence Bundle

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
