# ADR 0012: Attestation verification pins the release workflow and main

## Status

Accepted. Amends [ADR 0011](0011-minimal-release-pipeline.md).

## Date

2026-09-27

## Context

[ADR 0011](0011-minimal-release-pipeline.md) attests every release archive and
binary with GitHub artifact attestations. As the proof, it shows
`gh attestation verify <file> -R ildarbinanas-design/env-vault`.

With `-R` alone, `gh` accepts an attestation that any workflow run in the
repository signed, from any branch and any event. A collaborator with write
access can push a branch whose workflow requests `id-token: write` and
`attestations: write` and attests any file. `-R` alone would accept that
attestation. The repository has such a collaborator: the owner kept that access
after the 2026-09-26 audit (finding W4-03).

## Decision

A release file is genuine only if its attestation meets three conditions:

- `.github/workflows/release.yml` in this repository signed it
  (`--signer-workflow`);
- it comes from a run on `refs/heads/main` (`--source-ref`);
- it was made on a GitHub-hosted runner (`--deny-self-hosted-runners`).

The release workflow checks all three for every archive and binary before it
publishes a release, and again for the published archives. The workflow also
pins the release commit (`--source-digest`), because only the run for the
tagged commit builds a release.

User documentation gives this command:

```sh
gh attestation verify "$(command -v env-vault)" \
  --repo ildarbinanas-design/env-vault \
  --signer-workflow ildarbinanas-design/env-vault/.github/workflows/release.yml \
  --source-ref refs/heads/main \
  --deny-self-hosted-runners
```

## Considered options

- `-R` only, as ADR 0011 showed. It is the shortest command, but it accepts
  attestations from any workflow and any branch.
- The stricter command only in the documentation, leaving ADR 0011 as it is.
  On 2026-09-27 the owner chose a separate ADR instead, because this is the
  trust boundary of a release.
- Users also pin the release commit with `--source-digest`. That is exact, but
  users would first have to look up the commit of the tag they installed.

## Consequences

- A workflow on another branch can still create attestations in this
  repository, but they fail this verification.
- Verification proves that a file came from `release.yml` on `main`. It cannot
  prove more than `main` itself: whoever can merge into `main` controls what
  gets attested. The `main` ruleset and its required checks protect that step.
- Renaming `release.yml` changes the command. Releases attested under the old
  name verify only with the old name.
