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

- [x] 5.1 Build the updated CLI with Go 1.26.8 and perform real terminal paste checks using all three test-backend gates: `ENV_VAULT_BACKEND=test`, `ENV_VAULT_ALLOW_INSECURE_TEST_BACKEND=1`, and an absolute `ENV_VAULT_TEST_STORE` under the system temporary directory. Generate the 6050-byte clipboard value with `openssl rand -base64 4600 | tr -d '\n' | head -c 6050 | pbcopy`; generate fresh sufficiently large inputs for 65536 and 65537 bytes in the same way. Acceptance: `secret set EXAMPLE` accepts 6050 and 65536 bytes; `pbpaste | shasum -a 256` matches the digest from `exec --secret EXAMPLE:EXAMPLE -- sh -c 'printf %s "$EXAMPLE" | shasum -a 256'`; 65537 bytes returns `SECRET_TOO_LARGE`, leaves the previous value intact and restores the terminal. Record only lengths/digests/statuses, then remove the disposable store and clipboard contents.
- [x] 5.2 Interrupt a long partial paste with Ctrl+C in the same isolated macOS terminal setup. Acceptance: echo returns, no partial secret becomes shell input, the established interruption status is preserved, and the disposable store is removed after checking.
- [x] 5.3 With test-backend variables unset, generate a fresh 65536-byte value at runtime and pipe it to `secret set size-probe --service env-vault-size-probe --stdin --verify`; remove it with `secret delete size-probe --service env-vault-size-probe --confirm size-probe`. Acceptance: verified Keychain write and deletion both succeed, no other entry is touched, and `sysctl kern.argmax` is recorded. Report unavailable interactive access rather than claiming this check passed.

## 6. Full validation and independent implementation review

- [x] 6.1 Run the complete applicable CONTRIBUTING set listed below, plus strict change and main-spec validation. Acceptance: record each exact command/result, fix failures, list unavailable checks and skips explicitly, and do not count a skipped native/E2E/release-script test as passed. Keep `CHANGELOG.md`, backend implementations, dependencies and reserved paths unchanged in the product branch.
- [x] 6.2 Verify the final implementation on native CI, including `ubuntu-latest` prompt and Linux E2BIG tests. Ordinary CI alone does not establish native Windows behavior: run `GOTOOLCHAIN=go1.26.8 go test -count=1 -json ./internal/cli/... ./internal/secretstore/keyring/...` on `windows-latest` from a temporary `agent/verify-windows-*` branch following AGENTS.md. Acceptance: record run URLs and exact tested head SHAs; inspect JSON test events to prove the Windows 2560/2561-byte limit and pre-backend refusal tests actually ran, and that every new applicable Linux test ran without a PTY skip; retain native macOS results. Delete the temporary verification branches after checking. If the Windows run is impossible, record `Windows: not natively verified` in the evidence log and leave this task incomplete.
- [x] 6.3 Give an independent reviewer without the implementer's conversation the final implementation diff, the agreed delta specs and before/after validation results. Acceptance: all blocking findings are resolved, material fixes are re-reviewed and relevant checks rerun, and the reviewer result/limitations are recorded below. This does not authorize merging the eventual PR.

## 7. Synchronize and archive in the same PR

- [x] 7.1 Use `openspec-sync-specs` as part of the `openspec-archive-change` workflow to merge the four complete MODIFIED deltas into `openspec/specs/`. Acceptance: `openspec validate --specs --strict` passes and `rg -n '2560|SECRET_TOO_LARGE|hidden prompt' openspec/specs` shows no statements contradicting the implemented contract; retain unrelated requirements and scenarios.
- [x] 7.2 Archive with `openspec-archive-change` to `openspec/changes/archive/<date>-accept-long-hidden-input-and-cap-secret-size/`, preserving validation commands and results in this file and fixing relative links affected by the extra directory. Acceptance: the archive exists, the active change is absent, all earlier acceptance criteria have recorded evidence, and this final checkbox is marked complete in the archived file only after the move succeeds.
- [x] 7.3 Review the archived change and synchronized main specs against the final diff and rerun `git diff --check` and `openspec validate --specs --strict`. Acceptance: both pass, all task checkboxes in the archive are complete, no behavioral conflict remains, and the independent reviewer has inspected any material final changes.

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

The table summarizes completed acceptance. The detailed sections preserve
chronological attempts, including failures and intermediate pending states;
later successful checks do not turn earlier failed or skipped runs into passes.

| Evidence | Command / revision / run | Actual result |
| --- | --- | --- |
| Original-code macOS PTY regression | Test commit `9c1214d48653e41e67c1d218e51ddda21ed273dd`; command below | Expected exit 1: 4 short cases pass; 12 long cases time out; no skips or cleanup failures |
| Original-code Linux PTY regression | [Run 37577529799](https://github.com/ildarbinanas-design/env-vault/actions/runs/37577529799), verification head `badb451521e6a2e00516f01f73f0ebb07e11bcbe`, test source `9c1214d` | Expected Go exit 1; 10 short cases pass, 6 long cases fail with length/digest mismatch; all 16 executed without skips |
| Fixed prompt, size and compatibility tests | Native macOS commands below | Pass; no targeted prompt/size skips; actual pinned v0.4.2 source tested |
| Linux E2BIG regression | [Run 37579807389](https://github.com/ildarbinanas-design/env-vault/actions/runs/37579807389), product `b2bb23d` | Per-string CLI and aggregate runner tests executed and passed |
| Full local CONTRIBUTING checks and explicit skips | Go 1.26.8, implementation `b2bb23dc593c785acada22960bbab2c2ee955917`; commands below | All commands passed; full and race suites each 861 pass events and 9 explicit skips |
| Native CI execution and exact heads | Dedicated runs below; full [37582064720](https://github.com/ildarbinanas-design/env-vault/actions/runs/37582064720) at `9adfd4df2d87d309063bd0360d30446f719ffbee` | All 12 full-matrix jobs passed; native event proofs and explicit skips recorded below |
| Manual macOS paste, interrupt and Keychain probe | Native probe and isolated Terminal script described below | Keychain write/verify/delete pass; 08:13 UTC repeat passes 6050/65536 paste and 65537 rejection; 08:25 UTC Ctrl+C repeat passes SIGINT, terminal restoration, zero pending input and unchanged stored value |
| Independent implementation review | Separate reviewer without implementation conversation; final code/test head `9adfd4d` and final native results | No blocking findings after independent implementation and final spec/archive review; manual acceptance completed |
| Final synchronized-spec and archive checks | Inline sync/archive on 2026-10-07; final commands and review below | Six requirements synchronized across four specs; archived change, valid links, all 22 tasks complete; strict validation and diff check pass |

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

At this earlier checkpoint the real Terminal clipboard check was pending owner
interaction: Computer Use
refused access to `com.apple.Terminal` for safety reasons. The prepared isolated
script recorded a complete 6050-byte length/digest match, then accepted the
65536-byte prompt but failed its subsequent result check. Clipboard and
temporary store cleanup succeeded. That failed check required a diagnostic repeat;
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
actionable blockers were found. At that checkpoint, final native results and spec/archive review
remained separate completion checks.

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

## Final full CI and independent review result

[Run 37582064720](https://github.com/ildarbinanas-design/env-vault/actions/runs/37582064720)
completed successfully on exact code/test head
`9adfd4df2d87d309063bd0360d30446f719ffbee`. All 12 jobs passed: source quality,
three platform license checks, source resolution, all five native artifact
jobs (Linux amd64/arm64, macOS amd64/arm64, Windows amd64), E2E gate and
quality gate. The native jobs completed their applicable real-store smoke
checks. Source quality covered the full tests/race/vet/module/vulnerability/
smoke set, pinned historical transfer and isolated Debian Secret Service.

| Native binary E2E | Passed | Explicit skips | Statement coverage |
| --- | --- | --- | --- |
| Linux amd64 | 24 | 0 | 62.6% |
| macOS arm64 | 24 | 0 | 63.7% |
| Windows amd64 | 22 | 2: `EXEC_SIGNAL_FORWARDING`, `PROFILE_SYMLINK_REJECTED` | 61.8% |

All three reports have 100% critical-feature coverage, no unexpected skips
and zero detected leaks. They name the exact head above and reviewed semantic
suite hash `ad508a7c62217b8dfdae87b82ee38619f795b71faea5397595947add31501ad5`.
The runtime implementation and targeted CLI/keyring/runner tests are unchanged
from the dedicated final-head Linux/Windows verification at `08a7452`; only
the E2E fingerprint assertion and evidence changed afterward.

The independent reviewer inspected the final results, report metadata and
leak scans and completed code review at `9adfd4d` with no actionable code,
test or security findings. It explicitly distinguished code-review completion
from manual acceptance: tasks 5.1/5.2 were then incomplete, and a material fix
after a manual repeat would require renewed review. Synced specs and archive
had not yet been produced or reviewed at that checkpoint.

## Manual acceptance status before the owner-operated repeat

The isolated real Terminal run passed the 6050-byte length/digest comparison.
For 65536 bytes the CLI returned exit `0` and restored terminal attributes,
but the script's subsequent verification failed. Its original diagnostic did
not record enough metadata to identify the cause. No operator error or product
root cause is inferred. The 65537-byte and Ctrl+C phases were not reached.
The script removed its disposable store and cleared its clipboard.

A separate non-UI stdin-to-store-to-exec shell check passed at both 6050 and
65536 bytes with exact length/digest matches using the same binary. It does
not replace the real paste requirement. The manual script now records only
safe status, length, digest and fixed diagnostic labels for the repeat.

Computer Use explicitly refused to operate `com.apple.Terminal` for safety
reasons; no alternative UI-control path was used after that refusal. The
owner was asked to run the following in Terminal and follow its four `READY`
steps: Cmd+V then Enter for 6050, 65536 and 65537; Cmd+V then Ctrl+C without
Enter for the final interruption check.

```sh
/bin/bash /tmp/env-vault-hidden-input-evidence/manual-paste.sh /tmp/env-vault-hidden-input-evidence/env-vault
```

At that checkpoint the repeat result was pending. Tasks 5.1/5.2 remained
unchecked, so the change had not yet been synchronized, archived or submitted
as a PR. The owner required
all acceptance criteria to be completed before that delivery.

## Manual failure diagnostic follow-up

The owner reported that an independent Linux PTY check at
`8f7ed6a18684d40edc4ad8a9be98b0ad6a4a6623` passed with 1024-byte paste chunks
and CR termination: 6050 and 65536 bytes had matching exec hashes, 65537
returned `SECRET_TOO_LARGE` without changing the stored value, and Ctrl+C
terminated with SIGINT. This is owner-supplied Linux evidence, not a repeat
of the required real macOS clipboard paste.

The two suggested script causes were checked against the original script's
authoring history as well as the current script and original log. From its
first version, the generator used 4600 random bytes for 6050 and 52000 for
both larger inputs, and checked the actual clipboard length before `READY`.
The failing run records an actual clipboard length of 65536. The script
checked only exit status and error code; it never required `action=created`
for the second write. Neither insufficient generated input nor a mistaken
`created` assertion explains the recorded failure. An independent script
audit reached the same conclusion and found insufficient historical
diagnostics to identify the actual cause.

A fresh macOS diagnostic built the exact source head above and used a new
temporary store with all three gates. It generated values at runtime and
used `--stdin` for sequential writes to the same `EXAMPLE`, followed by the
same shell-based exec length/hash comparison as the manual script:

```sh
GOTOOLCHAIN=go1.26.8 GOCACHE=/tmp/env-vault-gocache go build -o /tmp/env-vault-hidden-input-evidence/env-vault-head-8f7ed6a ./cmd/env-vault
python3 /tmp/env-vault-hidden-input-evidence/check-manual-hypotheses.py
```

Both commands exited `0`. Generated base64 lengths were 6136 from 4600
random bytes and 69336 from 52000, with exact requested lengths after
truncation. The disposable store was removed; no clipboard or terminal UI
was used for this diagnostic.

| Input bytes | Stored bytes | Hash matches expected retained value | Set exit | Exec exit | Action | Error code |
| --- | --- | --- | --- | --- | --- | --- |
| 6050 | 6050 | true | 0 | 0 | created | none |
| 65536 | 65536 | true | 0 | 0 | overwritten | none |
| 65537 | 65536 | true | 2 | 0 | none | SECRET_TOO_LARGE |

The manual script now also logs allowlisted action metadata and explicitly
expects `created` for the first write and `overwritten` for the second. Its
shell and embedded Python syntax checks passed. This improves diagnostics;
it is not evidence of a fix for the original failure. The original log is
preserved, no product code was changed, and the historical cause remains
undetermined until a macOS repeat supplies the missing verification result.
Tasks 5.1/5.2 were still incomplete at that checkpoint. The owner was asked to repeat the script
using the freshly built `env-vault-head-8f7ed6a` binary, or identify an
already-created repeat log.

## Owner-operated macOS repeat at 08:13 UTC

On 2026-10-07 the owner ran the updated manual script in Terminal with
`env-vault-head-8f7ed6a`, built using Go 1.26.8 from
`8f7ed6a18684d40edc4ad8a9be98b0ad6a4a6623`. The supplied output matches
the appended local manual log. All three test-backend gates were active.

| Input bytes | Stored bytes | Hash comparison | Set exit | Exec exit | Action |
| --- | --- | --- | --- | --- | --- |
| 6050 | 6050 | matches clipboard | 0 | 0 | created |
| 65536 | 65536 | matches clipboard | 0 | 0 | overwritten |
| 65537 | 65536 | previous value unchanged | 2 (`SECRET_TOO_LARGE`) | 0 | none |

All three size cases restored the original terminal attributes and echo.
The script subsequently cleared its clipboard and removed its disposable
store. These results complete task 5.1. They do not establish why the
initial 65536 verification failed. The preserved log also includes an
earlier 07:56 UTC attempt with clipboard length 6050 but stored length 479
and a hash mismatch; its cause is likewise not established.

The 08:13 UTC Ctrl+C phase failed the script's whole-terminal-state equality
check. The script checked that equality before logging child exit status,
and did not log the differing attributes. Thus this run alone establishes
neither failed echo restoration nor a preserved SIGINT exit. The script's
final cleanup restored attributes, cleared the clipboard and removed the
store; the pending-input check was not reached. Task 5.2 remained incomplete
after that attempt.

No product code was changed in response. Diagnostic logging now records
child exit and terminal flag differences before asserting restoration.
The existing native macOS signal/flush regressions were repeated:

```sh
GOTOOLCHAIN=go1.26.8 GOCACHE=/tmp/env-vault-gocache go test -count=1 -json ./internal/cli -run '^(TestHiddenPromptsRestoreTerminalOnSignals|TestHiddenPromptDiscardsInterruptedInput)$'
```

Exit `0`: 21 test pass events, no failures or skips (19 leaf cases plus
the two parent tests). This does not replace the incomplete manual Ctrl+C
acceptance check. The isolated PTY comparison below identifies a reproducible
false-failure condition in the script, while the cause of this particular
manual failure remains unconfirmed without its terminal-state differences.

### Isolated macOS Ctrl+C diagnosis

An independent agent ran the exact binary in isolated native macOS PTYs
with Python's caught SIGINT handler, both with and without the Bash no-op
SIGINT trap used by the manual script. Each case asserted a complete write
of 6050 runtime-generated bytes; no Terminal UI or clipboard was accessed.
The diagnostic commands were:

```sh
python3 /private/tmp/env-vault-independent-interrupt-probe.py plain python interrupt
python3 /private/tmp/env-vault-independent-interrupt-probe.py plain bash interrupt
python3 /private/tmp/env-vault-independent-interrupt-probe.py pendin python interrupt
python3 /private/tmp/env-vault-independent-interrupt-probe.py pendin bash interrupt
python3 /private/tmp/env-vault-independent-interrupt-probe.py plain bash complete
python3 /private/tmp/env-vault-independent-interrupt-probe.py pendin bash complete
```

All six diagnostic supervisors completed with status `0`. All four
interrupted CLI processes exited by SIGINT (`-2`, shell status `130`),
restored echo and canonical mode, left zero pending input bytes and created
no store. With `PENDIN` initially set, the sole difference was that local
flag becoming clear (`0x20000000`); all other flags, speeds and control
characters matched. The Bash wrapper did not affect the result. Normal
completion also demonstrated that `PENDIN` can change independently of
terminal configuration.

The local Apple SDK's `usr/include/sys/termios.h` defines `PENDIN` as pending
input state. Thus whole-state equality is too strict for this manual
restoration check. This establishes a script defect that can reproduce the
reported symptom; it does not retrospectively prove that the original
08:13 failure differed only in this bit. The earlier length/hash failures
are separate and still have no established cause.

The temporary manual script now logs the child exit and all terminal
differences before checking restoration. It compares all configuration
fields exactly except `PENDIN`, still requires echo, exact SIGINT termination,
zero queued bytes and an unchanged stored value, and retains cleanup. A new
`--interrupt-only` mode seeds a fresh gated disposable store through stdin
and verifies its length/hash, then requests only the remaining 6050-byte
paste and Ctrl+C. Shell and embedded Python syntax passed; the independent
agent reviewed the script update without blocking findings. No product code
was modified. At that point task 5.2 awaited the real terminal repeat below.

### Completed owner-operated Ctrl+C repeat at 08:25 UTC

The owner ran the following command on 2026-10-07 at 08:25:36 UTC using the
same Go 1.26.8 binary from `8f7ed6a`. The supplied output matches the local
log, and all three test-backend gates were active:

```sh
/bin/bash /tmp/env-vault-hidden-input-evidence/manual-paste.sh /tmp/env-vault-hidden-input-evidence/env-vault-head-8f7ed6a --interrupt-only
```

The generated 65536-byte seed was created successfully (set exit `0`,
`action=created`), and its exec length/hash matched. After a generated
6050-byte partial paste and Ctrl+C, the CLI exited by SIGINT (`-2`, shell
status `130`). Echo and canonical mode were restored; all configuration
fields matched. The sole raw difference was local flags `536872395` to
`1483`, exactly the `PENDIN` bit identified by the isolated diagnostic.
The pending-input probe read zero bytes. Exec exited `0` and confirmed the
previous 65536-byte value's unchanged length/hash. The script cleared its
clipboard, removed the disposable store, and reported
`manual_macOS_interrupt=PASS`. This completes task 5.2.

This repeat confirms the script's full-state equality can report failure
solely because Darwin changes `PENDIN`, while the required restoration and
interruption behavior succeeds. The script correction excludes only that
runtime bit and preserves all functional checks; no CLI fix was needed.
The earlier 08:13 attempt did not record its flag difference, so that exact
historical state cannot be reconstructed. The original length/hash failures
remain explicitly unexplained rather than attributed to this separate issue.
The retained log also has an 08:24 attempt that returned exit `2` instead of
SIGINT; it is not counted as a successful interruption check.

Tasks 5.1, 5.2 and 5.3 now have separate successful native macOS evidence.
All runtime code and tests are unchanged since the green full CI run at
`9adfd4d`; subsequent commits have recorded evidence only.

## Final specification synchronization and archive

On 2026-10-07, the inline `openspec-sync-specs` workflow applied six complete
MODIFIED requirements across `cli-output`, `exec`, `secret-storage` and
`secret-transfer`. The main specs preserve their titles, purposes, unrelated
requirements and every pre-existing scenario. Comparing all six updated
blocks with their deltas leaves nothing pending to synchronize; no delta
operation headings appear in the main specs.

`openspec validate --specs --strict` exited `0` (7 passed, 0 failed);
`git diff --check` passed. The requested
`rg -n '2560|SECRET_TOO_LARGE|hidden prompt' openspec/specs` review found no
contradiction with the agreed common/backend limits, Unix prompt behavior,
legacy read/export boundary or import preflight. Task 7.1 is complete.

The `openspec-archive-change` workflow then moved the complete change,
including `.openspec.yaml`, to
`openspec/changes/archive/2026-10-07-accept-long-hidden-input-and-cap-secret-size/`.
The destination was confirmed absent before the move; afterward the archive
exists and the active change does not. The proposal's ADR link was corrected
for the additional directory depth. Task 7.2 was marked complete only in the
archived file after the move succeeded. Only the final independent review
and validation remained at that checkpoint.

A fresh independent agent, without the implementer's conversation, reviewed
the final product diff, synchronized specs, archive and validation evidence.
It verified exact agreement of all six MODIFIED blocks, retention of all 49
existing requirements and their scenario names, and byte-for-byte preservation
of 43 unrelated requirement blocks. All eight change files were archived;
all five relative Markdown links resolve. No product/test/dependency change
exists after the green `9adfd4d` code/test head, and no reserved path changed.
The reviewer independently confirmed the full CI run's exact head and all
12 successful jobs and checked the successful owner-operated manual logs.

The only final finding was stale present-tense pending status in this
historical evidence. The summary now reports completed acceptance and prior
pending passages are explicitly historical. No product, contract or security
finding remained. The reviewer did not repeat product tests or owner-operated
manual steps; it inspected their evidence and live CI metadata.

Final `git diff --check` passed and `openspec validate --specs --strict`
exited `0` with 7 passed, 0 failed (informational length notices only).
All 22 task checkboxes are complete; `openspec list --json` reports no active
changes. The final requirement/link comparison passed. Task 7.3 is complete.
The PR must remain unmerged for the owner's requested review.
