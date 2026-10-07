# Tasks

Implementation starts only after the owner says `apply`. Work in the order
below; mark a task complete only after its acceptance criterion is met.
Requirements are in the four [delta specs](specs); implementation choices and
evidence rules are in [design.md](design.md). If the agreed contract must
change, use `openspec-update-change` before editing implementation code.
All test values are generated at runtime with `internal/testutil`; neither
payloads nor passphrases may appear in arguments, fixtures or diagnostics.

## 1. Record failing prompt regressions before changing production code

- [x] 1.1 Extend `internal/cli/prompt_unix_test.go` using `openPseudoTerminal` and the existing subprocess helper. Generate values from `testutil.EphemeralValue`; helper stdout must contain only byte length and hex SHA-256, and mismatches only `length/digest mismatch`. Add lengths 1, 1023, 1024, 1025, 4095, 4096, 6050 and 65536, each as one large goroutine write and as chunks; wait for hidden mode before writing and bound writer/process waits. Acceptance: the tests compile against unchanged production code, short-input controls pass, and inspection confirms no raw-value diagnostics or fixed secret payloads.
- [x] 1.2 Run the new tests on the original prompt on macOS with `GOTOOLCHAIN=go1.26.8`, without implementing the fix. Acceptance: record the exact command, test-only commit/source SHA and failing long-input timeout result in the evidence log below; no payload is printed and PTY/subprocess cleanup completes.
- [x] 1.3 Run the same test-only revision on native Linux `ubuntu-latest` through authorized branch verification before changing production code. Acceptance: retain the run URL, exact head, command, test names and failing long-input length/digest mismatch, distinguish it from macOS timeout, and prove the Linux PTY cases were executed rather than skipped.

## 2. Implement and verify hidden input

- [x] 2.1 Add `secretstore.MaxValueBytes = 64 << 10` and the bounded Unix secret-input entry point described in the design. Apply the exact noncanonical flags, one-byte reads, UTF-8 Backspace, Ctrl+U, Unicode-whitespace Ctrl+W, empty/nonempty Ctrl+D and CR/LF termination; retain unbounded transfer passphrase input and leave `prompt_other.go`, signals and `restoreInterruptedPrompt` unchanged. Acceptance: all eight length cases in both delivery modes pass on macOS, the 6050-byte passphrase is read intact, and the targeted prompt suite retains existing restoration/flush behavior.
- [x] 2.2 Add regression cases for 65537-byte overflow, overflow followed by editing or actual EOF, no rejected-line tail for the next reader, UTF-8 and invalid-tail Backspace with both erase bytes, Ctrl+U, Ctrl+W, empty/nonempty Ctrl+D and CR. Implement latched overflow, drain through Enter, wipe with `bundle.Wipe`, and map `ErrValueTooLarge` to `SECRET_TOO_LARGE` before store construction. Acceptance: the targeted suite passes, overflow returns no value/backend call even if input ends during draining, and focused memory-ownership checks verify old allocations and removed suffixes are wiped.
- [x] 2.3 Add two identical 6050-byte export passphrase lines in one write, a 65537-byte passphrase accepted without a secret-value cap, and Ctrl+C after more than 4096 partial input bytes. Keep both export prompts within one hidden terminal session on macOS/Linux so restoring canonical mode cannot corrupt the queued confirmation. Acceptance: the real terminal-input route consumes exactly two complete lines, restores state after success or validation failure, preserves the existing signal/flush lifecycle, and all existing signal tests remain green.
- [x] 2.4 Update `README.md` and `docs/security.md` for long macOS/Linux hidden input, editing keys, the Enter/drain behavior after overflow and unchanged interruption recovery. Acceptance: review the documented behavior against every Secret input and Transfer passphrase scenario without introducing a passphrase maximum or changing Windows/BSD prompt promises.

## 3. Enforce new-write limits and preserve legacy reads

- [x] 3.1 Bound `--stdin` with `io.LimitReader(stdin, limit+3)`, trim exactly one LF/CRLF before checking length, and wipe owned buffers. Enforce the smaller positive backend `ValueLimiter` limit before `Exists` or `Set`, retaining `ErrValueTooLarge`, `CodeSecretTooLarge`, dry-run behavior and the backend's defensive check. Acceptance: tests accept `limit`, `limit` plus LF and `limit` plus CRLF, reject `limit+1` with `SECRET_TOO_LARGE` and zero store-factory calls, and reject a smaller backend violation with zero backend operations; terminal stdin remains refused.
- [x] 3.2 Extend `refuseOversizedValues` and `internal/cli/value_limit_test.go` for the effective common/backend limit, including absent, zero and larger advertised backend limits. Acceptance: 65536-byte writes pass where permitted; a batch with a small entry before a 65537-byte entry writes nothing under normal or dry-run import, oversized overwrite writes nothing, `skip` exempts existing entries, and Windows 2560/2561-byte coverage remains green. Generate oversized containers directly or seed a disposable store below the CLI boundary, never through the now-restricted `secret set`.
- [x] 3.3 Verify legacy compatibility boundaries with generated values seeded directly into a gated temporary test store and an otherwise valid v0.4.2-format container. Acceptance: `secret check` does not read/reject the old value, `exec` can use a value over 65536 bytes when the OS allows it, `export` preserves it as authenticated ciphertext, and importing an oversized selected value fails with `SECRET_TOO_LARGE` before any write; ordinary historical-format import still passes. Record the actual historical validation method and any unavailable historical binary explicitly.
- [x] 3.4 Update `README.md` and `docs/security.md` for 65536 bytes, the effective 2560-byte Windows Credential Manager limit, write-only application, skipped-import exemption and the explicit old-container import acceptance break. Acceptance: source/spec/document review finds no claim that legacy reads or export are capped, no implication that the container encoding changed, and size remediation accurately names the applicable limit in human and machine output.

## 4. Explain OS environment-size launch failures

- [x] 4.1 Match wrapped `E2BIG` in the runner start-error path and use the remediation in the exec delta without changing `RUNTIME_ERROR`, exit `1`, `Unable to start command` or other launch handling. Add a native Linux CLI regression that seeds a generated 131072-byte value directly into the gated test store, plus coverage of aggregate-limit error mapping. Acceptance: native Linux execution reports the new remediation, no child starts and no environment/value appears in output; ordinary start, stream, exit and signal tests remain green.
- [x] 4.2 Explain the practical 64 KiB bound and remaining per-string/aggregate OS limits in `README.md` and `docs/security.md`. Acceptance: documentation explains reducing the size or number of secrets after `E2BIG` without promising that arbitrary environment names, argv or many valid secrets always fit.

## 5. Manual macOS checks

- [ ] 5.1 Build the updated CLI with Go 1.26.8 and perform real terminal paste checks using all three test-backend gates: `ENV_VAULT_BACKEND=test`, `ENV_VAULT_ALLOW_INSECURE_TEST_BACKEND=1`, and an absolute `ENV_VAULT_TEST_STORE` under the system temporary directory. Generate the 6050-byte clipboard value with `openssl rand -base64 4600 | tr -d '\n' | head -c 6050 | pbcopy`; generate fresh sufficiently large inputs for 65536 and 65537 bytes in the same way. Acceptance: `secret set EXAMPLE` accepts 6050 and 65536 bytes; `pbpaste | shasum -a 256` matches the digest from `exec --secret EXAMPLE:EXAMPLE -- sh -c 'printf %s "$EXAMPLE" | shasum -a 256'`; 65537 bytes returns `SECRET_TOO_LARGE`, leaves the previous value intact and restores the terminal. Record only lengths/digests/statuses, then remove the disposable store and clipboard contents.
- [ ] 5.2 Interrupt a long partial paste with Ctrl+C in the same isolated macOS terminal setup. Acceptance: echo returns, no partial secret becomes shell input, the established interruption status is preserved, and the disposable store is removed after checking.
- [x] 5.3 With test-backend variables unset, generate a fresh 65536-byte value at runtime and pipe it to `secret set size-probe --service env-vault-size-probe --stdin --verify`; remove it with `secret delete size-probe --service env-vault-size-probe --confirm size-probe`. Acceptance: verified Keychain write and deletion both succeed, no other entry is touched, and `sysctl kern.argmax` is recorded. Report unavailable interactive access rather than claiming this check passed.

## 6. Full validation and independent implementation review

- [x] 6.1 Run the complete applicable CONTRIBUTING set listed below, plus strict change and main-spec validation. Acceptance: record each exact command/result, fix failures, list unavailable checks and skips explicitly, and do not count a skipped native/E2E/release-script test as passed. Keep `CHANGELOG.md`, backend implementations, dependencies and reserved paths unchanged in the product branch.
- [ ] 6.2 Verify the final implementation on native CI, including `ubuntu-latest` prompt and Linux E2BIG tests. Ordinary CI alone does not establish native Windows behavior: run `GOTOOLCHAIN=go1.26.8 go test -count=1 -json ./internal/cli/... ./internal/secretstore/keyring/...` on `windows-latest` from a temporary `agent/verify-windows-*` branch following AGENTS.md. Acceptance: record run URLs and exact tested head SHAs; inspect JSON test events to prove the Windows 2560/2561-byte limit and pre-backend refusal tests actually ran, and that every new applicable Linux test ran without a PTY skip; retain native macOS results. Delete the temporary verification branches after checking. If the Windows run is impossible, record `Windows: not natively verified` in the evidence log and leave this task incomplete.
- [ ] 6.3 Give an independent reviewer without the implementer's conversation the final implementation diff, the agreed delta specs and before/after validation results. Acceptance: all blocking findings are resolved, material fixes are re-reviewed and relevant checks rerun, and the reviewer result/limitations are recorded below. This does not authorize merging the eventual PR.

## 7. Synchronize and archive in the same PR

- [ ] 7.1 Use `openspec-sync-specs` as part of the `openspec-archive-change` workflow to merge the four complete MODIFIED deltas into `openspec/specs/`. Acceptance: `openspec validate --specs --strict` passes and `rg -n '2560|SECRET_TOO_LARGE|hidden prompt' openspec/specs` shows no statements contradicting the implemented contract; retain unrelated requirements and scenarios.
- [ ] 7.2 Archive with `openspec-archive-change` to `openspec/changes/archive/<date>-accept-long-hidden-input-and-cap-secret-size/`, preserving validation commands and results in this file and fixing relative links affected by the extra directory. Acceptance: the archive exists, the active change is absent, all earlier acceptance criteria have recorded evidence, and this final checkbox is marked complete in the archived file only after the move succeeds.
- [ ] 7.3 Review the archived change and synchronized main specs against the final diff and rerun `git diff --check` and `openspec validate --specs --strict`. Acceptance: both pass, all task checkboxes in the archive are complete, no behavioral conflict remains, and the independent reviewer has inspected any material final changes.

## Validation commands and evidence log

Use Go 1.26.8 for all Go-based checks. Run from the repository root:

```sh
gofmt -w $(git ls-files '*.go')
git diff --check
GOTOOLCHAIN=go1.26.8 go mod tidy -diff
GOTOOLCHAIN=go1.26.8 go mod verify
GOTOOLCHAIN=go1.26.8 go vet ./...
GOTOOLCHAIN=go1.26.8 go test ./...
GOTOOLCHAIN=go1.26.8 go test -race ./...
GOTOOLCHAIN=go1.26.8 scripts/smoke.sh
GOTOOLCHAIN=go1.26.8 scripts/vuln-check.sh
GOTOOLCHAIN=go1.26.8 scripts/license-check.sh
openspec validate accept-long-hidden-input-and-cap-secret-size --strict
openspec validate --specs --strict
```

Run the targeted prompt/limit/runner cases separately when each task calls for
them; record their actual test names and commands. Do not use the change-name
validation after the change has been archived. Existing full E2E/native CI
jobs remain authoritative; record their outcomes as well as local results.
For release-script tests, check Bash 4+ and required GNU tools as described in
CONTRIBUTING before interpreting a skip. Do not expand checks into a release.

Ordinary CI can be manually dispatched on the agent-owned branch before the
PR exists. When extra Linux execution evidence is necessary, use a temporary
`agent/verify-*` branch according to AGENTS.md: triggers restricted to that
branch, `contents: read` (and `actions: read` only for artifact downloads),
no secrets, no `pull_request_target` or `workflow_run`, actions pinned by full
commit SHA, timeout on every job, and only non-secret results. Run the
targeted tests with `-json` or `-v` and require their pass events without skips.
Record which product/test head it verifies; delete only the verification
branch created for this task, leaving Actions artifacts untouched. Do not
merge the temporary workflow into the product branch.

No implementation checks have run in Phase 1. Fill this table with actual
commands, results and evidence as implementation proceeds; do not infer a
pass from the plan or from a test that did not execute.

| Evidence | Command / revision / run | Actual result |
| --- | --- | --- |
| Original-code macOS PTY regression | Test commit `9c1214d48653e41e67c1d218e51ddda21ed273dd`; command below | Expected exit 1: 4 short cases pass; 12 long cases time out; no skips or cleanup failures |
| Original-code Linux PTY regression | [Run 37577529799](https://github.com/ildarbinanas-design/env-vault/actions/runs/37577529799), verification head `badb451521e6a2e00516f01f73f0ebb07e11bcbe`, test source `9c1214d` | Expected Go exit 1; 10 short cases pass, 6 long cases fail with length/digest mismatch; all 16 executed without skips |
| Fixed prompt, size and compatibility tests | Native macOS commands below | Pass; no targeted prompt/size skips; actual pinned v0.4.2 source tested |
| Linux E2BIG regression | [Run 37579807389](https://github.com/ildarbinanas-design/env-vault/actions/runs/37579807389), product `b2bb23d` | Per-string CLI and aggregate runner tests executed and passed |
| Full local CONTRIBUTING checks and explicit skips | Go 1.26.8, implementation `b2bb23dc593c785acada22960bbab2c2ee955917`; commands below | All commands passed; full and race suites each 861 pass events and 9 explicit skips |
| Native CI execution and exact heads | Dedicated Linux/Windows runs below | Required tests passed; initial full matrix needs E2E coverage follow-up |
| Manual macOS paste, interrupt and Keychain probe | Native probe and isolated Terminal script described below | Keychain write/verify/delete pass; 6050-byte paste pass; 65536 result check failed and repeat pending; overflow/interrupt not reached |
| Independent implementation review | Separate reviewer without implementation conversation, product `b2bb23d` and E2E follow-up | No actionable code/test blockers; final native results and archive review pending |
| Final synchronized-spec and archive checks | Pending apply | Not run |

## Delivery after archival

Open a PR from `agent/hidden-prompt-long-input` with the exact title
`fix(cli): accept long hidden prompt input and cap secret size at 64 KiB`.
This is a patch release: keep the title without `!`. Do not put the tokens
`BREAKING CHANGE:` or `BREAKING-CHANGE:` in any commit message or the PR body,
because the body becomes the squash commit body and those tokens would make
Release Please select v1.0.0. The word BREAKING may remain in `proposal.md`.
Its body must link the archived change, explain the intentional old-container
import acceptance change in a `Compatibility` section using ordinary prose,
include the failing/passing regression results,
every check and explicit skip, native CI evidence and manual macOS results.
Return the PR URL, head SHA and check list. Leave the PR unmerged for the
separate independent reviewer and owner; task completion is not permission
to merge it.

## Recorded baseline evidence

On macOS Darwin 25.6.0 arm64 with Go 1.26.8, before production edits:

```sh
GOTOOLCHAIN=go1.26.8 GOCACHE=/tmp/env-vault-gocache go test -count=1 -json -timeout=90s ./internal/cli -run '^TestHiddenPromptPreservesLongInput$'
```

Exit `1`: all 16 cases executed. Lengths 1 and 1023 passed in single-write
and chunked modes. Lengths 1024, 1025, 4095, 4096, 6050 and 65536 failed in
both modes with `hidden prompt read timed out`. No PTY skips, writer timeouts
or cleanup timeouts occurred. The test-only revision is
`9c1214d48653e41e67c1d218e51ddda21ed273dd`.

```sh
GOTOOLCHAIN=go1.26.8 GOCACHE=/tmp/env-vault-gocache go test -count=1 -json -timeout=90s ./internal/cli -run '^TestHiddenPromptPreservesLongInput$/^length-(1|1023)$/|^TestHiddenPromptsRestoreTerminalOnSignals$|^TestHiddenPromptDiscardsInterruptedInput$'
```

Exit `0`: all four short boundaries, twelve existing signal/restoration
cases and both interrupted-input flush cases passed without skips.

Native Linux baseline ran `GOTOOLCHAIN=go1.26.8 go test -count=1 -json
./internal/cli/... -run '^TestHiddenPromptPreservesLongInput$'` on
`ubuntu-latest`. The verifier required pass events through 4095 bytes and
failure events containing `length/digest mismatch` at 4096, 6050 and 65536,
for both delivery modes; every expectation was met. Run
[37577529799](https://github.com/ildarbinanas-design/env-vault/actions/runs/37577529799)
passed because it confirmed the expected regression. The earlier verification
run stopped on the expected nonzero Go status before event inspection; only
the temporary workflow shell handling changed for this successful run.

## Fixed native macOS prompt evidence

```sh
GOTOOLCHAIN=go1.26.8 GOCACHE=/tmp/env-vault-gocache go test -count=1 -json -timeout=120s ./internal/cli -run '^TestHidden'
GOTOOLCHAIN=go1.26.8 GOCACHE=/tmp/env-vault-gocache go test -count=20 -json -timeout=120s ./internal/cli -run '^TestHiddenPassphraseLongInputAndConfirmation$'
```

Both returned exit `0` without skips. The first includes all 16 length cases,
editing and memory wiping, overflow/EOF and pre-store rejection, confirmation,
validation-failure restoration, and existing signal/flush cases extended with
a 6050-byte partial SIGINT. The second reran the previously unstable pasted
confirmation route: 80 leaf passes, zero failures or skips. Both export prompts
share a hidden terminal session to avoid macOS corruption of queued input when
canonical mode is restored between them. The signal handler and interruption
restoration bodies remain unchanged. Windows/BSD's original prompt file is
unchanged. README/security documentation was checked against these scenarios.

## Write limits and legacy compatibility evidence

```sh
GOTOOLCHAIN=go1.26.8 GOCACHE=/tmp/env-vault-gocache go test -count=1 -json -timeout=180s ./internal/cli -run '^Test(RefuseOversizedValues|SecretSet.*(ValueLimit|Oversized|DryRun|Stdin)|ReadSecretStdin|Import.*(ValueLimit|Oversized))'
GOCACHE=/tmp/env-vault-hidden-input-evidence/go-cache GOTOOLCHAIN=go1.26.8 go test ./internal/cli -run '^TestLegacy' -count=1 -v
GOCACHE=/tmp/env-vault-hidden-input-evidence/go-cache GOTOOLCHAIN=go1.26.8 scripts/historical-transfer.sh
GOTOOLCHAIN=go1.26.8 GOCACHE=/tmp/env-vault-gocache go test -count=1 -json ./internal/runner/...
```

All returned exit `0` on macOS. The size suite recorded 82 pass events, no
failures or skips, including common 65536/65537 and simulated backend
2560/2561 boundaries, zero backend operations on refusal, import normal,
dry-run, overwrite and skip policies, bounded stdin and wiping, and existing
terminal-stdin/dry-run controls. This macOS run is not native Windows evidence.
Legacy tests proved metadata-only check, 65537-byte exec preservation and
authenticated export, with import rejection before writes. Historical
verification fetched and verified v0.4.2 source
`a79a02b6c079ccaa136b634ea6329a626f316b85`, built its actual binary and the current
binary, and passed ordinary default/named-service imports plus oversized
normal/dry-run refusal of a container produced by the old binary. No historical
binary was unavailable. Runner controls recorded 30 passes without skips;
the new Linux-only E2BIG regressions still require native Linux.

The initial legacy-test attempt could not access the sandbox's default Go
cache; using the writable temporary cache above resolved it before tests ran.
README/security documentation now describes the applicable write limit,
legacy reads, old-container import acceptance change, skipped-import exemption
and remaining OS environment/argument limits.

## Native macOS Keychain probe

Built with `GOTOOLCHAIN=go1.26.8 GOCACHE=/tmp/env-vault-gocache go build -o
/tmp/env-vault-hidden-input-evidence/env-vault ./cmd/env-vault`. With all three
test-backend variables unset, generated a fresh 65536-byte value in memory
and supplied it only on stdin to:

```sh
env-vault --json secret set size-probe --service env-vault-size-probe --stdin --verify
env-vault --json secret delete size-probe --service env-vault-size-probe --confirm size-probe
sysctl kern.argmax
```

Verified write and deletion each returned exit `0`, `ok: true`; only the
disposable named entry was used. `kern.argmax: 1048576`. Binary SHA-256:
`39650bdaee8d9732412be0266a53bac1ae0c9fcf856e130c670fcf5868ca7dc7`.

The real Terminal clipboard check is pending owner interaction: Computer Use
refused access to `com.apple.Terminal` for safety reasons. The prepared isolated
script recorded a complete 6050-byte length/digest match, then accepted the
65536-byte prompt but failed its subsequent result check. Clipboard and
temporary store cleanup succeeded. That failed check is being diagnosed;
automated PTY passes do not substitute for completing this manual check.

## Full local validation

Implementation revision: `b2bb23dc593c785acada22960bbab2c2ee955917`.
All commands below returned exit `0` on native macOS arm64:

```sh
gofmt -w $(git ls-files '*.go')
git diff --check
GOTOOLCHAIN=go1.26.8 GOCACHE=/tmp/env-vault-gocache go mod tidy -diff
GOTOOLCHAIN=go1.26.8 go mod verify
GOTOOLCHAIN=go1.26.8 GOCACHE=/tmp/env-vault-gocache go vet ./...
GOTOOLCHAIN=go1.26.8 GOCACHE=/tmp/env-vault-gocache go test -json ./...
GOTOOLCHAIN=go1.26.8 GOCACHE=/tmp/env-vault-gocache go test -race -json ./...
GOTOOLCHAIN=go1.26.8 GOCACHE=/tmp/env-vault-gocache scripts/smoke.sh
GOTOOLCHAIN=go1.26.8 GOCACHE=/tmp/env-vault-gocache scripts/vuln-check.sh
GOTOOLCHAIN=go1.26.8 GOCACHE=/tmp/env-vault-gocache scripts/license-check.sh
openspec validate accept-long-hidden-input-and-cap-secret-size --strict
openspec validate --specs --strict
```

New untracked Go files were also formatted before committing. Module tidy had
no diff; module verification reported all modules verified; smoke reported
`smoke ok`. Govulncheck v1.8.0 reported zero reachable vulnerabilities and one
advisory in a required module whose affected code is not called. The license
check passed with go-licenses v2.0.1 (assembly inspection warnings only).
Strict change validation passed, and strict main-spec validation reported
7 passed, 0 failed, with informational long-requirement notices only.

Both full and race JSON runs recorded 861 passing test events and these nine
explicit skips; none is counted as a successful execution:

- `TestE2E`: requires the binary-only E2E runner; native CI runs that separately.
- `TestHistoricalTransfer`: requires the pinned historical binary gate; the
  separate historical command above actually ran and passed.
- `TestReleaseFindStepBuildsOnlyTheTaggedReleaseCommit`,
  `TestReleasePublishStepNeverReplacesAssetsAndResumes`,
  `TestReleaseTapStepOpensOnePullRequestAndResumes`,
  `TestReleasePublishVerifiesEveryArchiveAndBinary`,
  `TestReleaseVerifyStepChecksThePublishedRelease`: GNU base64 unavailable.
- `TestPackageArchivesIsDeterministicAndKeepsTheLayout`,
  `TestPackageReleaseArrangesDownloadsAndAttestationSubjects`: GNU tar
  unavailable. These release-script tests run on the normal Linux CI runner.

No backend implementation, dependency, changelog or reserved product path
changed. Linux-specific E2BIG tests are absent from a macOS build rather than
counted as local passes.

## Native verification and full-matrix follow-up

Dedicated verification of product
`b2bb23dc593c785acada22960bbab2c2ee955917` passed:

| Native runner | Verification head / run | Command | Required event proof |
| --- | --- | --- | --- |
| `ubuntu-latest` | `d4df39a98c79589408b77344114fff4dae754285`; [37579807389](https://github.com/ildarbinanas-design/env-vault/actions/runs/37579807389) | `GOTOOLCHAIN=go1.26.8 go test -count=1 -json ./internal/cli/... ./internal/runner/... ./internal/secretstore/keyring/...` | Exit 0; 116 required cases each ran and passed, including every prompt leaf without PTY skips, both E2BIG tests, legacy exec and actual pinned v0.4.2 oversized normal/dry-run imports |
| `windows-latest` | `704beed61ca7d72b97aa190b002fef15c87a09c9`; [37579814611](https://github.com/ildarbinanas-design/env-vault/actions/runs/37579814611) | `GOTOOLCHAIN=go1.26.8 go test -count=1 -json ./internal/cli/... ./internal/secretstore/keyring/...` | Exit 0; 48 required cases each ran and passed, including 2560/2561 secret-set/import boundaries, LF/CRLF and refusal before backend operations in human/JSON/JSONL output |

Both verification commits add only their temporary workflow and event-verifier
metadata to the product revision. Workflows restricted their trigger/ref to
their own branch, used `contents: read`, no secrets, pinned action SHAs and job
timeouts. The temporary `agent/verify-linux-hidden-input-20261007` and
`agent/verify-windows-hidden-input-20261007` remote/local branches and worktrees
were deleted after verification. Actions history/artifacts were not changed.

Explicit unrelated skips in these targeted package runs:

- Linux: `TestSecretServiceDisposableIntegration` (isolated service gate),
  `TestMetadataDetectsMissingCaseAlias` and
  `TestMetadataRechecksNewFilesystemAliases` (filesystem/platform cases).
- Windows: `TestInvalidLocalConfigNeverFallsBackToGlobal/unreadable_file` and
  `/unsearchable_directory` (Unix permissions);
  `TestPassBackendScopesSafeSlashNames`,
  `TestKeyringAdapterRejectsTraversalBeforeOpeningBackend`,
  `TestExistsReadsKeyListingWithoutDecryptingValue` (`pass` unavailable);
  `TestWindowsNativeCredentialIdentity` (native session gate);
  `TestHistoricalTransfer` (historical binary gate); and
  `TestLegacyLargeSecretExecPreservesValue` (macOS/Linux environment allowance).
  No required Windows size or pre-backend test was skipped.

The first ordinary full matrix,
[37579748237](https://github.com/ildarbinanas-design/env-vault/actions/runs/37579748237),
tested the same product head. Source quality (including the full race suite,
actual historical transfer and isolated Debian Secret Service), all three
license jobs, Linux arm64, macOS amd64 and Windows amd64 native jobs passed.
Linux amd64 and macOS arm64 E2E each passed all 24 functional scenarios, but
failed statement coverage: 58.80% and 59.90%, respectively, below the existing
60% floor. Their downstream real-store smoke steps did not run. The new Unix
prompt needs a real-binary E2E regression; the threshold remains unchanged.
This run is recorded as failed, not as a complete matrix pass.

## Real-binary E2E regression

Extended the existing `SECRET_LIFECYCLE` scenario without changing manifest
IDs, production code, dependencies, CI or the 60% coverage floor. It accepts
65536 bytes plus CRLF through stdin with verification, rejects 65537 without
changing the store, and on macOS/Linux drives the actual CLI through a PTY
for 6050/65536-byte input, editing, overflow, no echo and terminal restoration.
The child checks only byte length and SHA-256; generated values remain in
the existing isolated stores and sentinel tracking.

```sh
GOTOOLCHAIN=go1.26.8 GOCACHE=/tmp/env-vault-gocache GOPROXY=off go run ./e2e/cmd/e2e-runner run --phase candidate --binary /tmp/env-vault-hidden-input-evidence/e2e-prompt-cli --reporter /tmp/env-vault-audit-20261003.0HKrh2/reporters/darwin-arm64/gotestsum --reporter-checksum /tmp/env-vault-audit-20261003.0HKrh2/reporters/darwin-arm64/gotestsum.sha256 --reports reports/e2e-prompt-check --coverage-floor 60 --test-timeout 3m --command-timeout 3m
```

Exit `0` on native macOS: 24 scenarios passed, zero failures/skips/missing;
critical feature coverage 100%, statement coverage 63.7%. Three full-suite
and five locking burn-ins completed; the registry validated 245 records and
the leak scan found zero occurrences across 18 report files. The existing
reporter was checksum-verified. Generated reports were moved out of the
checkout into the local evidence directory. Linux amd64 and Windows amd64
E2E package cross-builds passed; these are not substitutes for native CI.

## Independent review and follow-up validation

An independent agent without the implementation conversation reviewed the
product diff at `b2bb23d` against `f5012e1`, the agreed deltas, baseline failures
and passing local evidence. It found no actionable correctness/security
blockers in the terminal editor, memory ownership, signal lifecycle, write
limits, import preflight/skip behavior, legacy compatibility or E2BIG mapping.
It separately reviewed the final E2E follow-up before commit `08a7452`,
including bounded process/writer cleanup, no secret-bearing arguments or
diagnostics, and the actual canonical report/registry/leak-scan results; no
actionable blockers were found. Final native results and spec/archive review
remain separate completion checks.

After the E2E-only follow-up:

```sh
GOTOOLCHAIN=go1.26.8 GOCACHE=/tmp/env-vault-gocache go vet ./e2e/...
GOTOOLCHAIN=go1.26.8 GOCACHE=/tmp/env-vault-gocache go test -race ./e2e/...
```

Both returned exit `0`. The ordinary binary-gated `TestE2E` is skipped in this
package invocation; the canonical runner above actually exercised it.
`git diff b2bb23d 08a7452 -- internal` is empty: the follow-up changes only
E2E tests/helper and documentation/evidence.

## Final-head native checks and suite fingerprint correction

Dedicated native verification was repeated from product/test revision
`08a7452e054fb14145cd97cf1f7832fe83b595b1`, using the same commands and event
expectations as above:

- Linux [37581451390](https://github.com/ildarbinanas-design/env-vault/actions/runs/37581451390),
  verification head `29ff76cd4afd4856576809d055d0e28adbd08a79`: Go exit `0`,
  all 116 required run/pass events, no required skips.
- Windows [37581459331](https://github.com/ildarbinanas-design/env-vault/actions/runs/37581459331),
  verification head `9df021b7eb8b2b791c3c0cd685a9540d0562e1a4`: Go exit `0`,
  all 48 required run/pass events, including 2560/2561 and pre-backend refusal.

Unrelated/platform skips matched the previous dedicated runs. Both temporary
`agent/verify-linux-hidden-input-final-20261007` and
`agent/verify-windows-hidden-input-final-20261007` branches and worktrees were
deleted, with remote absence verified. Previous logs and Actions artifacts
were preserved.

The second full matrix,
[37581338054](https://github.com/ildarbinanas-design/env-vault/actions/runs/37581338054),
passed Linux amd64 E2E (24/24, no skips, statement coverage 62.60%, critical
coverage 100%) and its real-store smoke. Source tests and macOS/Windows native
internal tests found the stale expected constant in
`TestCanonicalRepositoryHashIsPinned`; their only test failure was this suite
fingerprint mismatch. The later E2E/report steps on those two native jobs did
not run. This matrix is recorded as failed.

Updated only the expected test fingerprint to the hash of the intentionally
changed E2E sources:
`ad508a7c62217b8dfdae87b82ee38619f795b71faea5397595947add31501ad5`. The independent
reviewer recomputed it from the 30 canonical E2E source files and approved the
update; `GOTOOLCHAIN=go1.26.8 GOCACHE=/tmp/env-vault-review-gocache go test
-count=1 ./internal/e2esuite` passed. The hash algorithm, schema, production
code and historical reports were unchanged.

Repeated local full and race commands after that correction:

```sh
GOTOOLCHAIN=go1.26.8 GOCACHE=/tmp/env-vault-gocache go test -json ./...
GOTOOLCHAIN=go1.26.8 GOCACHE=/tmp/env-vault-gocache go test -race -json ./...
```

Both returned exit `0`, each recording 861 pass events and the same nine
explicit local skips documented above.
