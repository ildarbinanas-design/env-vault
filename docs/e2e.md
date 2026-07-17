# End-to-end CLI verification

The E2E suite is a black-box compatibility boundary for env-vault. It invokes
only a real, native `env-vault` executable with `os/exec`; it does not import
`internal/cli` or any other production package. The normal pass uses a
release-like `go build -trimpath` binary (or an unpacked release artifact). A
separate pass builds an instrumented binary with
`go build -trimpath -cover -coverpkg=./...` and collects subprocess coverage
through its own `GOCOVERDIR`.

The canonical scenario manifest is [`e2e/scenarios.json`](../e2e/scenarios.json).
It is the machine-readable source for
`feature/requirement -> scenario ID -> Go test -> platforms -> result`.
All listed scenarios are critical. A missing or unexpected skip fails the job.
The manifest retains the legacy `atomic` scenario ID and requirement wording.
On Windows those scenarios assert complete readable YAML, preservation of the
prior file before replacement, and removal of temporary siblings; they do not
claim an operating-system atomicity guarantee that Go does not provide.
The semantic suite hash covers the scenario/harness sources plus the runner,
normalization, validation, and rendering code that interprets their reports.
Generated reports are excluded. In the isolated reporting-tool file, only the
two version pin values are canonicalized; the checksum pin and every other byte
remain hashed.
The checked-in durable baseline therefore rejects a semantic test change even
when a stale report set is internally consistent.

## Isolation and secret safety

Every scenario uses a unique `t.TempDir` and separately sets `HOME`,
`XDG_CONFIG_HOME`, `APPDATA`, `USERPROFILE`, `TMPDIR`, `TMP`, and `TEMP`. The
only backend available to the subprocess is the insecure test backend, enabled
by all three required gates:

```text
ENV_VAULT_BACKEND=test
ENV_VAULT_ALLOW_INSECURE_TEST_BACKEND=1
ENV_VAULT_TEST_STORE=<scenario os.TempDir descendant>
```

The suite never selects Keychain, Credential Manager, Secret Service, KWallet,
or `pass`, and it performs no network access. Each scenario creates a random
sentinel value at runtime. The sentinel is never placed in argv and its value
is never persisted in the repository. Tests scan stdout, stderr, normalized
contracts, and saved fixture files; the runner also scans raw test JSONL,
JUnit, coverage reports, summaries, contracts, the sanitized failure bundle,
and `$GITHUB_STEP_SUMMARY`. The private test-store file is the only intentional
secret-bearing file and is deleted with the scenario temporary directory.
Reports retain only SHA-256 evidence that each scenario created a sentinel.

Every CLI subprocess has a hard deadline. Timeout cleanup terminates the
process tree. Concurrency and signal tests use readiness files and bounded
polling, not sleeps. Tests do not run in parallel, and burn-in uses shuffled
order without rerunning failed tests. The runner records and requires three
distinct full-suite scenario-order seeds and five distinct locking-suite seeds.
Before the binary suite, every native matrix job runs the config package once;
Windows additionally runs the focused concurrent save/read test ten times in
one process as a sequential burn-in. Any failed repetition fails the job.

The Windows package suite also proves the real bounded replacement path with a
non-delete-shared read handle. The test holds that handle, starts replacement,
observes the first whitelisted Win32 failure, proves replacement has not
completed, releases the handle, and requires successful completion with the
new complete file. Its test log records the underlying Win32 errno, attempt
count, and elapsed time. Normal config reads use `FILE_SHARE_DELETE`, so a
reader can continue reading the prior complete file while the pathname is
replaced; partial reads and delete-before-replace are not accepted.

On Windows, the coverage-instrumented binary also exercises the native config
replacement retry deterministically. Only when the insecure test backend's
full gate, the E2E child marker, and `GOCOVERDIR` are all present, the runtime
confirms that the executable was built with `go build -cover`, and the
store/config paths remain inside the isolated scenario root, replacement of an
existing regular test config returns one synthetic
`ERROR_SHARING_VIOLATION` before calling the root-relative atomic rename. Existing
profile/concurrency assertions therefore fail if the bounded retry stops
working. Release-like binaries and all real keyring backends cannot activate
this testability hook. A process that supplies every E2E request gate without
the coverage build identity fails closed instead of silently bypassing the
exercise, so the instrumented Windows job cannot pass if detection regresses.

## Supported platform matrix

| Platform ID | Native runner | Release-like artifact |
|---|---|---|
| `linux-amd64` | `ubuntu-latest` | `tar.gz`, CGO disabled |
| `linux-arm64` | `ubuntu-24.04-arm` | `tar.gz`, CGO disabled |
| `darwin-amd64` | `macos-15-intel` | `tar.gz`, CGO enabled |
| `darwin-arm64` | `macos-15` | `tar.gz`, CGO enabled |
| `windows-amd64` | `windows-latest` | `zip`, CGO disabled |

`PROFILE_SYMLINK_REJECTED` and `EXEC_SIGNAL_FORWARDING` have explicit expected
skips on `windows-amd64`: unprivileged symlink creation is not guaranteed on
hosted Windows runners, and Unix signal forwarding has no Windows equivalent.
No other critical scenario may skip.

## Functional coverage matrix

`P5` means all five platform IDs above. `Unix + expected Windows skip` means
the manifest still requires the Windows result, but records the intentional
skip instead of silently dropping the platform.

| Feature or requirement | Scenario ID | Go test | Platforms |
|---|---|---|---|
| Root `--help`; zero exit; stdout/stderr separation | `CLI_HELP_ROOT` | `TestE2E/CLI_HELP_ROOT` | P5 |
| Help for every public command and subcommand | `CLI_HELP_SUBCOMMANDS` | `TestE2E/CLI_HELP_SUBCOMMANDS` | P5 |
| `--version`, `version`, and JSON version agreement | `CLI_VERSION_FORMS` | `TestE2E/CLI_VERSION_FORMS` | P5 |
| Missing commands/arguments and unknown flags | `CLI_ARGUMENT_ERRORS` | `TestE2E/CLI_ARGUMENT_ERRORS` | P5 |
| Stable usage exit code and human/machine stream separation | `CLI_ARGUMENT_ERRORS` | `TestE2E/CLI_ARGUMENT_ERRORS` | P5 |
| Exact non-secret text contracts for secret and profile lifecycles | `TEXT_OUTPUT_CONTRACTS` | `TestE2E/TEXT_OUTPUT_CONTRACTS` | P5 |
| Secret set through stdin | `SECRET_LIFECYCLE` | `TestE2E/SECRET_LIFECYCLE` | P5 |
| Existing and missing secret checks | `SECRET_LIFECYCLE` | `TestE2E/SECRET_LIFECYCLE` | P5 |
| Secret list metadata without values | `SECRET_LIFECYCLE` | `TestE2E/SECRET_LIFECYCLE` | P5 |
| Confirmed delete and repeated delete | `SECRET_LIFECYCLE` | `TestE2E/SECRET_LIFECYCLE` | P5 |
| Invalid secret names and traversal forms | `SECRET_VALIDATION_SECURITY` | `TestE2E/SECRET_VALIDATION_SECURITY` | P5 |
| Invalid service names and traversal forms | `SECRET_VALIDATION_SECURITY` | `TestE2E/SECRET_VALIDATION_SECURITY` | P5 |
| Custom-service set/check/delete and isolation from the default-service list | `SECRET_LIFECYCLE` | `TestE2E/SECRET_LIFECYCLE` | P5 |
| Reject positional/flag/non-stdin secret-value channels before backend access | `SECRET_VALIDATION_SECURITY` | `TestE2E/SECRET_VALIDATION_SECURITY` | P5 |
| Profile create and duplicate create | `PROFILE_LIFECYCLE` | `TestE2E/PROFILE_LIFECYCLE` | P5 |
| Add, show, and remove mappings | `PROFILE_LIFECYCLE` | `TestE2E/PROFILE_LIFECYCLE` | P5 |
| Remove absent mapping | `PROFILE_LIFECYCLE` | `TestE2E/PROFILE_LIFECYCLE` | P5 |
| Multiple mappings and idempotent identical add | `PROFILE_LIFECYCLE` | `TestE2E/PROFILE_LIFECYCLE` | P5 |
| Default/local/global/explicit config targets and local precedence | `PROFILE_TARGETS_CHECK_SECRET` | `TestE2E/PROFILE_TARGETS_CHECK_SECRET` | P5 |
| Mutually exclusive local/global selectors | `PROFILE_TARGETS_CHECK_SECRET` | `TestE2E/PROFILE_TARGETS_CHECK_SECRET` | P5 |
| `--check-secret` success and fail-before-lock/no-mutation behavior | `PROFILE_TARGETS_CHECK_SECRET` | `TestE2E/PROFILE_TARGETS_CHECK_SECRET` | P5 |
| Exact `ENV_NAME` conflict | `PROFILE_COLLISIONS_PERSISTENCE` | `TestE2E/PROFILE_COLLISIONS_PERSISTENCE` | P5 |
| Case-insensitive `ENV_NAME` collision | `PROFILE_COLLISIONS_PERSISTENCE` | `TestE2E/PROFILE_COLLISIONS_PERSISTENCE` | P5 |
| Persistence across separate processes | `PROFILE_COLLISIONS_PERSISTENCE` | `TestE2E/PROFILE_COLLISIONS_PERSISTENCE` | P5 |
| Same-directory replacement and readable YAML | `PROFILE_ATOMIC_PERMISSIONS` | `TestE2E/PROFILE_ATOMIC_PERMISSIONS` | P5 |
| Private config/lock permissions and no stale temp files | `PROFILE_ATOMIC_PERMISSIONS` | `TestE2E/PROFILE_ATOMIC_PERMISSIONS` | P5 |
| Final config and lock target symlink defenses | `PROFILE_SYMLINK_REJECTED` | `TestE2E/PROFILE_SYMLINK_REJECTED` | Unix + expected Windows skip |
| Exec with profile | `EXEC_PROFILE_DIRECT_MULTI` | `TestE2E/EXEC_PROFILE_DIRECT_MULTI` | P5 |
| Exec with direct `--secret` | `EXEC_PROFILE_DIRECT_MULTI` | `TestE2E/EXEC_PROFILE_DIRECT_MULTI` | P5 |
| Exec with multiple and combined mappings | `EXEC_PROFILE_DIRECT_MULTI` | `TestE2E/EXEC_PROFILE_DIRECT_MULTI` | P5 |
| Environment inheritance | `EXEC_ENV_MODES` | `TestE2E/EXEC_ENV_MODES` | P5 |
| Inherited collision rejection | `EXEC_ENV_MODES` | `TestE2E/EXEC_ENV_MODES` | P5 |
| `--override-env` | `EXEC_ENV_MODES` | `TestE2E/EXEC_ENV_MODES` | P5 |
| `--clean-env` | `EXEC_ENV_MODES` | `TestE2E/EXEC_ENV_MODES` | P5 |
| Preserve argv spaces, quotes, symbols, Unicode, and slashes | `EXEC_ARG_STREAM_EXIT` | `TestE2E/EXEC_ARG_STREAM_EXIT` | P5 |
| Child stdin passthrough | `EXEC_ARG_STREAM_EXIT` | `TestE2E/EXEC_ARG_STREAM_EXIT` | P5 |
| Child stdout/stderr and CRLF byte passthrough | `EXEC_ARG_STREAM_EXIT` | `TestE2E/EXEC_ARG_STREAM_EXIT` | P5 |
| Exact child exit-code propagation | `EXEC_ARG_STREAM_EXIT` | `TestE2E/EXEC_ARG_STREAM_EXIT` | P5 |
| Resolution failure prevents child launch | `EXEC_MISSING_SECRET_NO_CHILD` | `TestE2E/EXEC_MISSING_SECRET_NO_CHILD` | P5 |
| Missing command exit 127; non-executable command exit 126 where applicable | `EXEC_MISSING_SECRET_NO_CHILD` | `TestE2E/EXEC_MISSING_SECRET_NO_CHILD` | P5 |
| Signal forwarding after explicit readiness | `EXEC_SIGNAL_FORWARDING` | `TestE2E/EXEC_SIGNAL_FORWARDING` | Unix + expected Windows skip |
| Windows direct-process behavior through portable helper | `EXEC_ARG_STREAM_EXIT` | `TestE2E/EXEC_ARG_STREAM_EXIT` | `windows-amd64` |
| Dry secret mutation creates no store | `DRY_RUN_NO_SIDE_EFFECTS` | `TestE2E/DRY_RUN_NO_SIDE_EFFECTS` | P5 |
| Dry profile mutation creates no config or lock | `DRY_RUN_NO_SIDE_EFFECTS` | `TestE2E/DRY_RUN_NO_SIDE_EFFECTS` | P5 |
| Dry exec does not launch the child | `DRY_RUN_NO_SIDE_EFFECTS` | `TestE2E/DRY_RUN_NO_SIDE_EFFECTS` | P5 |
| Dry JSON and JSONL metadata contain no values | `DRY_RUN_NO_SIDE_EFFECTS` | `TestE2E/DRY_RUN_NO_SIDE_EFFECTS` | P5 |
| Dry delete/add/remove preserve existing store/config digests and public state | `DRY_RUN_NO_SIDE_EFFECTS` | `TestE2E/DRY_RUN_NO_SIDE_EFFECTS` | P5 |
| Dry operations create no lock or replacement temporary file | `DRY_RUN_NO_SIDE_EFFECTS` | `TestE2E/DRY_RUN_NO_SIDE_EFFECTS` | P5 |
| JSON envelope and schema fields | `OUTPUT_JSON_JSONL_FILE` | `TestE2E/OUTPUT_JSON_JSONL_FILE` | P5 |
| Exactly one JSONL event and CRLF normalization | `OUTPUT_JSON_JSONL_FILE` | `TestE2E/OUTPUT_JSON_JSONL_FILE` | P5 |
| `--output`, `--quiet`, and private output permissions | `OUTPUT_JSON_JSONL_FILE` | `TestE2E/OUTPUT_JSON_JSONL_FILE` | P5 |
| Quiet exec metadata file with unmodified child stdout/stderr passthrough | `OUTPUT_JSON_JSONL_FILE` | `TestE2E/OUTPUT_JSON_JSONL_FILE` | P5 |
| Output-write `RUNTIME_ERROR` exit 1 and opt-in verbose diagnostic | `OUTPUT_JSON_JSONL_FILE` | `TestE2E/OUTPUT_JSON_JSONL_FILE` | P5 |
| Stable structured error codes; no diagnostic mixing | `OUTPUT_JSON_JSONL_FILE` | `TestE2E/OUTPUT_JSON_JSONL_FILE` | P5 |
| Healthy explicitly allowed test backend in text and JSON | `DOCTOR_BACKENDS` | `TestE2E/DOCTOR_BACKENDS` | P5 |
| Requested but incompletely gated test backend in text and JSON | `DOCTOR_BACKENDS` | `TestE2E/DOCTOR_BACKENDS` | P5 |
| Backend unavailable/unsupported warning in text and JSON | `DOCTOR_BACKENDS` | `TestE2E/DOCTOR_BACKENDS` | P5 |
| Incomplete explicit test backend returns `BACKEND_UNAVAILABLE` exit 4 with no native fallback | `DOCTOR_BACKENDS` | `TestE2E/DOCTOR_BACKENDS` | P5 |
| Concurrent profile mutations from separate processes | `CONCURRENCY_PROFILE_MUTATIONS` | `TestE2E/CONCURRENCY_PROFILE_MUTATIONS` | P5 |
| No lost updates, valid YAML, no stale replacement files | `CONCURRENCY_PROFILE_MUTATIONS` | `TestE2E/CONCURRENCY_PROFILE_MUTATIONS` | P5 |
| Bounded lock timeout and `CONFIG_LOCKED` | `LOCK_TIMEOUT_CRASH_INTEGRITY` | `TestE2E/LOCK_TIMEOUT_CRASH_INTEGRITY` | P5 |
| Killed active writer before replacement preserves the prior YAML | `LOCK_TIMEOUT_CRASH_INTEGRITY` | `TestE2E/LOCK_TIMEOUT_CRASH_INTEGRITY` | P5 |
| Lock release after process death permits recovery | `LOCK_TIMEOUT_CRASH_INTEGRITY` | `TestE2E/LOCK_TIMEOUT_CRASH_INTEGRITY` | P5 |
| Unique sentinel per scenario and no output/artifact leakage | all scenarios | `TestE2E/*` plus runner leak gate | P5 |
| Real user keyrings remain untouched | all scenarios | isolated triple-gated harness | P5 |

## Durable checked-in baseline

[`docs/e2e-baseline.json`](e2e-baseline.json) is the versioned compatibility
floor used by every candidate matrix. It stores the semantic suite identity,
exact accepted Go and gotestsum versions, provenance of the accepted matrix,
per-platform normalized contract hashes, coverage floors, every critical
scenario result, expected skips, and leak expectations. It contains no secret
values and does not depend on workflow artifact retention.

The one-time checked-in migration bundle under
[`evidence/e2e-baseline-migration/`](../evidence/e2e-baseline-migration/)
binds the successful legacy comparison bytes to the durable normalized facts
and the reviewed independent-sentinel suite transition. This proves the
replacement of the historical comparator. Normal CI reads only current reports
and the checked-in baseline; it never downloads or reinterprets historical
reports.

Verify the migration proof entirely offline:

```sh
GOTOOLCHAIN=go1.26.5 go run ./cmd/e2e-baseline verify-migration \
  --repository-root . \
  --contract release/contract.v1.json \
  --baseline evidence/e2e-baseline-migration/migrated-baseline.json \
  --migration evidence/e2e-baseline-migration/migration.json
```

The migrated snapshot remains beside its proof so the historical equivalence
can always be replayed. The active baseline now comes from the accepted current
five-platform matrix and therefore has no runtime dependency on that one-time
migration.

Updating the baseline is an explicit reviewed change, not an automatic
tolerance adjustment. First produce a passing current five-platform
`matrix-validation.json`; then run the deterministic update and inspect both
the baseline and machine diff:

```sh
GOTOOLCHAIN=go1.26.5 go run ./cmd/e2e-baseline update \
  --repository-root . \
  --contract release/contract.v1.json \
  --proof reports-download/matrix-validation.json \
  --baseline docs/e2e-baseline.json \
  --diff-output baseline.diff.json

git diff -- docs/e2e-baseline.json
jq . baseline.diff.json
```

Commit an update only when its exact run tuple, tool versions, suite-hash
change, per-platform contract changes, coverage-floor changes, scenario
changes, and leak changes are intentional. CI must verify the updated baseline
against another proof from the same exact matrix identity before it can become
a release gate.

The accepted independent-sentinel update and its exact run/attempt bindings
are preserved under
[`evidence/e2e-baseline-updates/`](../evidence/e2e-baseline-updates/).

## Running locally

CI does not fetch the reporting tool inside native matrix jobs. The isolated
[`tools/e2e-reporter`](../tools/e2e-reporter) module pins its complete checksum
graph. The resolve job downloads that graph with three bounded attempts, then
builds all five `CGO_ENABLED=0` reporters once and uploads source-SHA- and
attempt-qualified artifacts. Each native job verifies the downloaded binary's
SHA-256, Go build information, target, and exact `--version` output before
running with `GOPROXY=off`. A missing or incompatible reporter therefore fails
closed without a second network fallback or artifacts from another attempt.

Build the checksum-pinned reporting tool outside the product module; it is not
a production dependency. The durable baseline and candidates both require
stable `v1.13.0`, whose `x/tools` graph builds with Go 1.26.5 while preserving
JSONL, JUnit, and test exit-code behavior. The same builder used by CI emits all
five target binaries and their exact checksum sidecars:

```sh
GOTOOLCHAIN=go1.26.5 go run ./cmd/releasecheck contract matrix --json \
  > /tmp/env-vault-native-matrix.json
toolchain="$(GOTOOLCHAIN=go1.26.5 go env GOROOT)"
PATH="$toolchain/bin:$PATH" scripts/release/build-e2e-reporters.sh \
  /tmp/env-vault-native-matrix.json /tmp/env-vault-e2e-reporters
```

Run every functional, coverage, full burn-in, and locking burn-in pass. With no
binary option, the runner builds the release-like binary itself. Select the
native platform directory (`gotestsum.exe` on Windows) and keep module network
access disabled during execution:

```sh
reporter=/tmp/env-vault-e2e-reporters/darwin-arm64/gotestsum
PATH="$toolchain/bin:$PATH" GOTOOLCHAIN=local GOPROXY=off \
  go run ./e2e/cmd/e2e-runner run \
  --phase candidate \
  --reporter "$reporter" \
  --reporter-checksum "$reporter.sha256"
```

Use an already built native binary or release archive without changing the
suite:

```sh
PATH="$toolchain/bin:$PATH" GOTOOLCHAIN=local GOPROXY=off \
  go run ./e2e/cmd/e2e-runner run --phase candidate \
  --binary ./env-vault --reporter "$reporter" --reporter-checksum "$reporter.sha256"
PATH="$toolchain/bin:$PATH" GOTOOLCHAIN=local GOPROXY=off \
  go run ./e2e/cmd/e2e-runner run --phase candidate \
  --artifact ./dist/env-vault-darwin-arm64.tar.gz \
  --checksum ./dist/env-vault-darwin-arm64.tar.gz.sha256 \
  --reporter "$reporter" --reporter-checksum "$reporter.sha256"
```

The raw Go suite also deliberately accepts a prebuilt binary directly:

```sh
ENV_VAULT_E2E_BINARY="$PWD/env-vault" GOTOOLCHAIN=go1.26.5 \
  go test -json -run '^TestE2E$' ./e2e
```

The runner defaults to three shuffled full-suite iterations and five shuffled
locking/concurrency iterations. These are burn-in executions, not automatic
failure retries. Every failure remains a failure.

CI gives each platform job 90 minutes but passes five-minute suite and
three-minute build/report command deadlines. This leaves enough outer budget
for process-tree cleanup and fail-closed report finalization instead of letting
the hosted runner terminate a hung job before artifacts exist.

## Reports and gates

Each native job writes below:

```text
reports/e2e/<baseline|candidate>/<go-version>/<goos>-<goarch>/
```

The directory contains `junit.xml`, raw `go test` JSONL, `summary.json`,
`summary.md`, `feature-coverage.json`, `feature-coverage.md`, normalized public
CLI `contracts.json`, `metadata.json`, `coverage.out`, `coverage.txt`,
`coverage.html`, burn-in JSONL, `leak-scan.json`, and a sanitized failure
bundle. Metadata records the commit, exact Go version, GOOS/GOARCH, runner OS,
binary and suite SHA-256 values, the compiler version read from the actual
subject with `go version -m`, timestamps, duration, result counts, statement
coverage, expected skips, normalized commands, immutable evidence-file
digests, archive/checksum evidence, the pinned reporter version, and the exact
GitHub repository plus Actions run ID, URL, and attempt (or the explicit
`local` identity). Host repository, home, and temporary paths are replaced by
stable placeholders before persistence.

Reports are finalized even after a test failure, while the original non-zero
status remains authoritative. CI uploads them with `if: always()`, unique
platform/attempt names, and 30-day retention. Raw JSONL terminal events, JUnit
testcase IDs/counts, burn-in repetitions/seeds, and the statement percentage
recomputed from `coverage.out` are cross-checked against feature and metadata
evidence. Coverage source paths must resolve to checked-in production CLI
files, human-readable reports are regenerated exactly from their machine
evidence, `coverage.txt` and full `coverage.html` are regenerated from
`coverage.out` with the report's exact Go patch toolchain, package percentages
are independently recomputed, and immutable report digests are rechecked.
`e2e-gate` fails closed if a
platform or required file is missing, malformed, leaked, skipped unexpectedly,
does not have 100% critical scenario coverage, or falls below the conservative
60% cross-platform statement-coverage floor. The durable baseline is the
stronger non-regression gate: it permits no decrease from its reviewed
per-platform floors and requires exact normalized contract, critical-scenario,
expected-skip, leak, toolchain, and semantic-suite identities. Matrix
validation recomputes the semantic suite hash from the exact checkout and
rejects reports produced by a different runner/scenario implementation, even
if every report in that stale set agrees with every other one.

Validate a downloaded five-platform set with:

```sh
GOTOOLCHAIN=go1.26.5 go run ./e2e/cmd/e2e-runner validate-matrix \
  --contract release/contract.v1.json \
  --reports reports-download --phase candidate \
  --expected-commit "$GITHUB_SHA" --expected-run-id "$GITHUB_RUN_ID" \
  --expected-run-url "$GITHUB_SERVER_URL/$GITHUB_REPOSITORY/actions/runs/$GITHUB_RUN_ID" \
  --expected-run-attempt "$GITHUB_RUN_ATTEMPT" \
  --expected-repository "$GITHUB_REPOSITORY" \
  --expected-reporter "v1.13.0"
```

That command writes a sealed `matrix-validation.json`. Verify it against the
checked-in baseline with the same exact coordinates:

```sh
GOTOOLCHAIN=go1.26.5 go run ./cmd/e2e-baseline verify \
  --repository-root . \
  --contract release/contract.v1.json \
  --baseline docs/e2e-baseline.json \
  --proof reports-download/matrix-validation.json \
  --output baseline-verification \
  --phase candidate \
  --expected-commit "$GITHUB_SHA" --expected-run-id "$GITHUB_RUN_ID" \
  --expected-run-url "$GITHUB_SERVER_URL/$GITHUB_REPOSITORY/actions/runs/$GITHUB_RUN_ID" \
  --expected-run-attempt "$GITHUB_RUN_ATTEMPT" \
  --expected-repository "$GITHUB_REPOSITORY"
```

The verification writes versioned JSON and Markdown. For a strict
`vMAJOR.MINOR.PATCH` candidate, every native job also records the three exact
`CLI_VERSION_FORMS` outputs in its promotion proof. The promotion manifest is
assembled only after the sealed matrix and durable baseline both pass, so a
wrong binary version, stale suite, or lower coverage cannot be masked by
another target.

The current symlink contract rejects unsafe final config and lock targets. It
does not claim protection from a hostile same-user process or a pre-existing
symlink in an ancestor directory; that stronger handle-relative filesystem
boundary is a documented residual risk rather than a hidden test exclusion.
