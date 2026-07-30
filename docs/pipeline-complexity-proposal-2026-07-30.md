# Release-pipeline complexity vs. actual need — proposal

Status: proposal, not adopted. Written 2026-07-30 as a follow-up to
[ADR 0008](adr/0008-freeze-release-ceremony-require-personalos-link.md), which
froze *new* release-engineering investment but explicitly left existing
machinery in place ("Existing machinery is **not** removed"). This document
answers the question ADR 0008 did not: given the actual base need, should the
existing machinery be kept, trimmed, or extracted — and if trimmed, what
specifically.

## 1. The actual base need

`env-vault` is a CLI used by one person (the account owner), distributed via
`brew install ildarbinanas-design/tap/env-vault`. The base need is exactly:

1. Build five platform binaries (darwin/linux amd64+arm64, windows amd64).
2. Publish them as a GitHub Release with checksums.
3. Update `homebrew-tap/Formula/env-vault.rb` with the new URL/SHA256 so `brew
   upgrade` picks it up.

Nothing about "many untrusted contributors," "many external consumers
verifying supply-chain provenance," or "multi-year drift across a rotating
team" applies. The owner is the sole author, sole reviewer, and sole approver
on every PR in this repo.

**A working, minimal version of exactly this already exists in a sibling
repo the owner maintains.** `macos-user-settings/.github/workflows/release.yml`
is 187 lines, 2 jobs, no GitHub App, no Release Please, no evidence ledger, no
provenance/SBOM, and no typed contract: tag push → build both darwin
binaries → verify the version string → checksum → `gh release create`. It
uses only the default `GITHUB_TOKEN`. That repo's Homebrew formula
(`homebrew-tap/Formula/macos-user-settings.rb`) is updated by hand, based on
its commit history.

## 2. Scale comparison

| Surface | env-vault release engineering | env-vault actual product | macos-user-settings release engineering |
| --- | ---: | ---: | ---: |
| Go (release-only: `cmd/releasecheck`, `release-extract`, `release-version-probe`, `releasetransport`, `actionsartifact*`, `e2e-baseline` + matching `internal/` packages) | ~18,000+ physical lines (13,433 release core + 4,729 transport Go, per `docs/release-architecture.md`, plus additional `actionsartifact*`/`canonicalgzip`/`strictjson` packages not folded into that count) | 5,533 lines (`internal/cli`, `config`, `secretstore`, `runner`, `output`, `redact`, `errors`, `platform`) | 0 (no dedicated release Go) |
| Workflow YAML | 12 files / 27 static jobs / 5,926 physical lines | — | 2 files / 342 lines |
| `scripts/release` shell+jq | 35 files / 6,457 physical lines | — | 0 |
| GitHub Apps | 2 (`env-vault-release-planning`, `env-vault-tap-release`), each with a dedicated audit workflow and rotation runbook | — | 0 (default token only) |
| Release confirmation ceremony | Byte-exact `ПОДТВЕРЖДАЮ RELEASE …` tuple, checked-in resumable authorize-and-merge wrapper | — | none (tag push is the trigger) |

The release-engineering surface for a one-maintainer CLI is roughly **3–4x
larger than the product it ships**, and two orders of magnitude larger than
the working minimal pattern the same owner already runs successfully next
door.

## 3. Component table

| Component | Why it exists | Excessive for one maintainer? | Reasoning |
| --- | --- | --- | --- |
| Build 5 native targets, checksum, GitHub Release | Core distribution need | **No** | This is the actual base requirement. |
| Homebrew formula URL/SHA bump | Core distribution need | **No** | Required for `brew upgrade` to work; can be a PR or a direct commit. |
| Release Please (auto changelog/version PR) | Convenience: generates version bump + changelog | **Partially** | The changelog generation itself is fine; the App-based token, environment-scoped credential, and label-lifecycle machinery around it (vs. just running `release-please` with `GITHUB_TOKEN` or doing version bumps by hand) is not justified when the only reviewer is the release-please diff's author. |
| Two dedicated GitHub Apps + rotation runbook + 2 audit workflows | Least-privilege separation between "planning" and "publishing" trust domains | **Yes** | Least-privilege isolation defends against a compromised or malicious *second* party. There is no second party — the owner already holds full admin/push rights on both repos. The Apps add zero security benefit here and cost 2 installs, 2 audit workflows, and a documented multi-step key-rotation procedure to maintain. |
| Typed GitHub transport (`cmd/releasetransport`, `internal/githubtransport`, ~4,700 Go lines) | Eliminate ad-hoc `gh api` pagination/retry bugs (real incidents: `run-name` shadowing `.name`, empty `.pull_requests` post-merge, line-wrapped base64 blobs) | **Partially** | The underlying bugs were real, but they were hit by *building* this much custom transport code, not by the base need. `macos-user-settings` gets the equivalent behavior from three or four inline `gh api`/`gh release create` calls. |
| Versioned operational release contract (v1/v2, historical-authority registry, contract-history pinning) | Prevent policy drift / downgrade across contract revisions | **Yes** | Solves "drift across many contributors over years," which doesn't exist with one committer who can read the one workflow file that exists at any time. |
| Versioned evidence ledger (`release-evidence` branch, genesis anchor, content-addressed bundle, 64-commit bounded window, planned checkpoint/Merkle follow-up) | Durable, offline-replayable audit trail of what was published | **Yes** | Git history plus the GitHub Releases page already *is* the audit trail for a single-operator repo. This re-implements a tamper-evident ledger for a threat model (disputing what happened, multi-party audit) that doesn't apply. |
| Dual-source verification for immutable historical tags (backlog item 1) | Verify an old tag against a since-changed contract | **Yes** | Not even built yet (`frozen` in the backlog); pure speculative hardening. |
| Provenance/SBOM attestations | Supply-chain verification for consumers who don't trust the publisher | **Yes** | The GitHub Actions attestation actions themselves are cheap, but the owner is the only consumer of `env-vault`. Nobody is verifying SLSA provenance on a personal secrets CLI. |
| Cross-platform E2E matrix + immutable Go-1.22.12 baseline comparison | Regression detection across releases | **Partially** | Running the E2E suite on real target platforms is legitimate and worth keeping in some form; the "immutable baseline identity" byte-for-byte comparison ceremony on top of it is not. |
| GitHub App-based Homebrew bridge (formula PR, wait-for-tap-CI, guarded squash-merge, post-merge CI wait, `health` re-verification) | Fully automate the tap update including CI-gating | **Partially** | The end result (`brew install`/`upgrade` works) is the real need; `macos-user-settings` reaches the same end state with a manual formula edit. Full automation is convenient but not required, and it is currently the single largest source of App/environment/audit overhead. |
| Actions artifact lifecycle policy (23 upload sites, retention tiers, cleanup manifest + confirmation ritual) | Avoid Actions storage billing block | **No, but derivative** | This is a real, already-triggered problem (177.8 GB-hours against a 0.5 GB-month allowance, per backlog item 13) — but it is a *symptom* of how many artifacts the rest of this machinery uploads. Trimming the machinery shrinks this problem instead of requiring separate cleanup tooling. |
| Byte-exact release-confirmation ceremony (`ПОДТВЕРЖДАЮ RELEASE …`) + resumable authorize-and-merge wrapper | Prevent an unintended/ambiguous publish | **Partially** | A single, deliberate "yes, publish this" confirmation is reasonable to keep in some form; the exact-tuple/no-paraphrase/no-LLM-substitution machinery around it is calibrated for a process where the confirmer might not be the same person as the author — which is never true here. |

## 4. Reuse across the owner's other repositories

Checked via `ls`/`README.md` only, per instructions:

- **`homebrew-tap`** already hosts formulae for both `env-vault` and
  `macos-user-settings`. It has exactly one workflow, `test-formula.yml`
  (2 jobs). It does **not** run any App-token or auto-PR automation of its
  own — that logic lives entirely inside each producing repo's release
  pipeline (`build-binaries.yml` for env-vault; nothing, i.e. manual edits,
  for macos-user-settings). So the tap itself is already a reused,
  lightweight piece — the *heavy* App/audit/contract machinery is not shared,
  it is bespoke to `env-vault` alone.
- **`homebrew-personalos`** is a second, separate tap explicitly for
  PersonalOS-managed CLIs, currently serving one checksum-pinned
  `homelab-collector 0.1.0-poc.1` formula for macOS arm64 only — no release
  automation visible from its README.
- **`macos-user-settings`** independently reinvented (or rather, never
  needed) any of env-vault's heavy machinery, and ships successfully with a
  187-line workflow and a manually-edited tap formula. This is direct
  evidence that the owner does not need env-vault's release-engineering scale
  to keep a personal Homebrew-distributed CLI working.
- **`homelab-collector`** has no `.github/workflows` at all yet; its own
  "release" is a local, deterministic `go run ./cmd/release-package` producing
  a checksummed archive — an even lighter pattern than macos-user-settings.

**Conclusion on reuse:** none of env-vault's excess machinery (Apps, typed
v1/v2 contract, evidence ledger, dual-source verification, provenance/SBOM,
byte-exact confirmation wrapper) is used by, or a natural fit for, any other
repo in the account. Every other repo either has no release automation yet or
uses a pattern one to two orders of magnitude smaller. The only genuinely
shared piece — the tap repository itself, and the general shape "build →
checksum → GitHub Release → bump formula" — is already factored out as far as
it needs to be: each producer repo owns its own build, and the tap stays a
thin formula host. There is no second consumer waiting for a
"github-release-toolkit" library; extracting one would mean maintaining a new
public interface for an audience of one.

## 5. Recommendation: **trim**

**Not keep as-is.** The current surface (~18k+ Go lines, ~5,900 workflow
lines, ~6,500 shell lines, 2 GitHub Apps, a versioned contract system, and a
content-addressed evidence ledger) defends against threats — multi-party
trust separation, supply-chain-provenance-skeptical consumers, multi-year
contributor drift, disputed audit history — that do not exist for this
repository. It has already caused one real operational problem of its own
making (the Actions-artifact billing exposure in backlog item 13) precisely
because of how much machinery uploads how many artifacts. Keeping it as-is
means continuing to carry and occasionally repair (see ADRs 0004, 0005: two
of the eight ADRs are incident write-ups from this machinery's own failures)
a system sized for a problem the owner doesn't have.

**Not extract.** Extraction only pays off when a second consumer exists or is
concretely planned. Section 4 shows the opposite: every sibling repo either
has no release automation or independently uses something far simpler, and
none of them touch the parts that would be worth extracting (Apps, contract
versioning, evidence ledger). Extracting this machinery into a shared repo
would not reduce total complexity — it would relocate the same ~18k+ lines
into a new package, add a version-compatibility surface between that package
and every consumer (starting with a consumer base of exactly one), and turn
"repair one repo's release workflow" into "repair a shared dependency other
things might one day depend on." That is more engineering, not less, for a
need that is fully satisfied by the pattern `macos-user-settings` already
runs inline.

**Trim, modeled on the already-proven `macos-user-settings` pattern.**
Concretely, that means:

- Keep: native cross-platform build matrix, checksums, `gh release create`
  (or equivalent), and a Homebrew formula bump (can stay automated as a PR,
  but through the default repository token rather than a dedicated App —
  the owner already has merge rights either way).
- Keep, in reduced form: one deliberate "yes, publish" human confirmation
  step before tagging; a real E2E run on target platforms before release.
  Both can lose the byte-exact-tuple/no-paraphrase ceremony and the
  immutable-baseline-identity comparison and just be "tests pass, I reviewed
  the diff, tag it."
- Drop: the two GitHub Apps and their audit workflows and rotation runbook
  (revert to the default `GITHUB_TOKEN`, which is sufficient for a
  single-admin repo pushing to its own tap — or, if cross-repo write is
  needed, one PAT scoped to `homebrew-tap` is far simpler than an App
  install-plus-rotation lifecycle).
- Drop: `release/contract.v1.json`/`v2.json` and the historical-authority
  registry — replace with the workflow YAML itself as the single source of
  truth, the same way `macos-user-settings/release.yml` is.
- Drop: the versioned evidence ledger, genesis/legacy routing, and the
  planned checkpoint/Merkle follow-up — the GitHub Releases page and Git
  history already serve as the record.
- Drop: dual-source historical-tag verification (not built; simply don't
  build it) and provenance/SBOM attestation generation (no consumer verifies
  it).
- Let the Actions-artifact lifecycle problem shrink on its own once the
  above stops generating dozens of upload sites; no separate lifecycle-policy
  engineering is needed once the artifact count returns to "five build
  outputs and maybe a test report."

This is a larger, more literal cut than ADR 0008's freeze (which stopped
*new* scope but explicitly kept existing machinery running). Whether to act
on it — and how aggressively — is the owner's call; this document does not
implement any part of it.
