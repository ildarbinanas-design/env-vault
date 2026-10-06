# Open questions

The specifications in `openspec/specs/` record current behavior as is. This
list holds behavior that looks unintended, inconsistent or undocumented, found
while writing the baseline on 2026-10-06. Each item is resolved either by an
OpenSpec change that alters behavior or by confirming the current behavior and
removing it from this list.

## Product behavior

1. **Profile names are not validated.** Any non-empty string is accepted,
   including control characters and terminal escape sequences, and is printed
   unchanged by `profile create` and `profile show`
   (`profile-config` › Creating a profile). Should profile names follow a
   naming rule like secret names?
2. **A repeated identical `profile add` reports `added`.** The file is not
   changed, but output says `profile updated: <profile> added <ENV>` and the
   data has no field telling the two cases apart.
3. **`profile remove` matches case-sensitively.** Adding treats `TOKEN` and
   `token` as the same target, but `profile remove dev token` does not remove
   `TOKEN`. A missing mapping fails with `CONFIG_INVALID` (exit `5`), not a
   usage-type code.
4. **Mappings added through the CLI are always required.** `required: false`
   can only be set by editing the YAML.
5. **`--json` and `--jsonl` produce identical output.** Both print one
   envelope; the `--jsonl` help text promises "JSONL events".
6. **`export` and `import` print only `ok` in human mode**, while every other
   command prints a summary.
7. **Dry runs differ in depth.** `secret set --dry-run` validates the backend
   selection; `secret delete --dry-run` does not open the backend at all.
8. **Custom services are unreachable from `exec`.** `exec`, profiles,
   `profile add --check-secret` and `secret list` use only the default service,
   so a secret stored with `--service` can be exported but not injected.
9. **`doctor` reports `backend: keyring` for an unsupported
   `ENV_VAULT_BACKEND`.** The warning carries the error text with its code
   prefix (`BACKEND_UNAVAILABLE: Unsupported secret backend requested`).
10. **A strict `--output` failure is reported as a generic error.** For
    commands other than a completed `exec`, the result is `RUNTIME_ERROR`
    `Unexpected runtime error`; the reason appears only with `--verbose`.
11. **`exec` reads secret values before checking the command.** A misspelled
    command fails with `127` only after every value was read, which on macOS
    can mean Keychain prompts for a command that never runs.
12. **Interrupt forwarding on Windows is a no-op.** The runner calls
    `Process.Signal(os.Interrupt)`, which Go does not implement on Windows;
    the child gets Ctrl+C from the console instead. Should the forwarding code
    be removed or replaced?

## Release and distribution

13. **Linux arm64 is supported but not tested in the tap.** README and the
    formula offer `linux-arm64`; `test-formula.yml` installs only on
    `ubuntu-24.04` x86_64.
14. **Who may authorize a release.** `RELEASING.md` says "Either maintainer may
    authorize a release", while `AGENTS.md` describes a single owner.
15. **Release binaries contain test-only code paths.** The insecure test
    backend and the E2E config-save hook are compiled into release binaries
    and are reachable only through the environment-variable gate. A build tag
    would remove them from releases, but CI E2E would then test a binary built
    with different code than the one released.
16. **Required checks defined outside the repository.** The `main` ruleset
    requires `Analyze (go)` and `Analyze (actions)`, which come from CodeQL
    default setup in repository settings, not from a workflow in this
    repository.
