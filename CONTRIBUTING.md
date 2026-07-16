# Contributing to env-vault

env-vault accepts changes through pull requests to `main`. Keep changes small,
preserve the security rules in `AGENTS.md`, and run the relevant local checks
before requesting review.

## Pull request titles

The pull request title becomes the squash commit subject and is consumed by
Release Please v5. Use Conventional Commits syntax:

```text
type(optional-scope): concise description
```

Accepted types are:

- `feat` for user-visible capability;
- `fix` for a user-visible or release-path correction;
- `perf` for a measurable performance improvement;
- `refactor` for behavior-preserving production restructuring;
- `docs`, `test`, `build`, `ci`, and `chore` for their corresponding
  maintenance changes;
- `revert` for a reverted change.

Use `!` after the type or scope and explain `BREAKING CHANGE:` in the pull
request body when compatibility is intentionally broken. The repository uses
`PR_TITLE` and `PR_BODY` for squash commits so both reviewed fields reach
Release Please. Examples:

```text
feat(profile): support profile descriptions
fix(release): preserve the exact release source SHA
docs: explain test-backend isolation
feat(exec)!: change child environment precedence
```

The checked-in Release Please configuration is the source of truth for the
exact version calculation and changelog sections. Features request a minor
version, an explicit breaking change requests a major version, and every other
visible configured type requests a patch. This deliberately lets a build,
toolchain, CI, documentation, or test-contract change produce a reviewed
release even when the Go source behavior did not change. `chore` is accepted as
a title type but is intentionally absent from the visible changelog sections,
so a chore-only change does not request a release. Never select a version by
creating or moving a tag; the generated release pull request is the review
boundary for any exceptional version choice.

Do not rebase-merge a pull request. The protected branch uses the reviewed pull
request title as the deterministic squash subject so release planning sees the
same Conventional Commit that reviewers approved.

## Documentation and release notes

Update durable documentation in the same pull request as the behavior or
operator contract it describes. In particular:

- update `README.md` for user-facing installation or CLI behavior;
- update `docs/design.md` for architecture and trust-boundary changes;
- update `RELEASING.md` and `docs/release-external-settings.md` for release or
  repository-setting changes;
- do not hard-code the current product release in examples when `vX.Y.Z` or a
  link to the latest release expresses the contract.

Release Please owns the version entry and generated release section in
`CHANGELOG.md`. Review that generated diff like code. Do not manually create a
release tag or GitHub Release to compensate for a missing changelog entry.

Merging the generated release pull request is an explicit authorization to
publish its exact manifest version after the merge commit passes `ci` on
`main`. The release-planning workflow may then create the exact tag at that
green SHA; `build-binaries` remains the only public GitHub Release and asset
publisher.

## Local checks

Run the checks appropriate to the change. The full release-quality set is:

```sh
gofmt -w $(git ls-files '*.go')
git diff --check
GOTOOLCHAIN=go1.26.5 go mod tidy -diff
GOTOOLCHAIN=go1.26.5 go mod verify
GOTOOLCHAIN=go1.26.5 go test ./...
GOTOOLCHAIN=go1.26.5 go vet ./...
GOTOOLCHAIN=go1.26.5 go test -race ./...
GOTOOLCHAIN=go1.26.5 scripts/smoke.sh
GOTOOLCHAIN=go1.26.5 scripts/license-check.sh
```

The protected pull request checks remain authoritative for the native Linux,
macOS, and Windows build, license, smoke, and E2E matrices.
