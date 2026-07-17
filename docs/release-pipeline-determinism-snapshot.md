# Release pipeline determinism implementation snapshot

This document records the local `agent/release-pipeline-determinism`
implementation snapshot prepared on 2026-07-16 and reviewed on 2026-07-17.
It is a checkpoint description, not evidence that the branch is ready to merge
or release.

## Freshness and scope

The files were changed in two sessions on 2026-07-16: 12:59-14:45 and
17:46-18:29 (UTC+05:00). Before this checkpoint commit, the worktree contained:

- 45 modified tracked files;
- 3 intentionally deleted tracked files;
- 49 new files;
- 4,216 tracked additions and 3,749 tracked deletions;
- approximately 13,771 lines in the new files.

The implementation started from `0eef84548b802a89f98c08744b5b51aac3d543ad`.
At review time, `origin/main` was 23 commits ahead. Of the 97 affected paths,
11 matched `origin/main` exactly, 3 deletions were already reflected there, 46
had different content, and 37 remained local-only. The branch therefore needs
an explicit semantic reconciliation with current `main`; it must not be treated
as disposable or mechanically rebased without review.

## Character of the changes

The implementation objective is `llm-free-release`: make the release process
deterministic and machine-verifiable without free-text or log interpretation.
The change set introduces:

- a declarative release contract for platforms, assets, workflows, release
  stages, applications, and repair modes;
- promotion of exact, attempt-qualified CI artifacts instead of rebuilding
  release payloads during publication;
- durable machine evidence binding source SHAs, workflow attempts, asset
  digests, attestations, Homebrew state, timings, and retries;
- digest-bound, precondition-checked, dry-run-first repair and operator plans;
- a durable E2E baseline and sealed matrix proof in place of the old
  cross-source comparator;
- an isolated diagnostic workflow for legacy rebuilds whose outputs cannot be
  published;
- stricter release, archive, Homebrew, Windows atomic-replace, and repository
  observation behavior;
- extensive workflow, Go, shell, documentation, and policy tests.

The removal of `cmd/e2e-compare` and the old runner comparator is intentional:
the new baseline and matrix-proof architecture replaces them.

## Readiness at the checkpoint

The work has strong implementation-quality signals: the diff passes whitespace
checks, Go formatting is clean, shell and JSON syntax are valid, no explicit
TODO/FIXME/WIP markers remain, and targeted Go tests and vet checks are recorded
as passing in the implementation record.

It is not yet an integration or release candidate. The implementation record
still marks exact-head required CI as `not_run`, keeps
`required-ci-pending` open, and accepts that no release was executed. Existing
generated reports predate the final source edits and cannot validate this exact
snapshot. Reconciliation with current `main`, a fresh complete validation run,
and exact-head CI are required before merge or release consideration.
