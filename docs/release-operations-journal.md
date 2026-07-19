# Release operations journal

This append-only journal records safe external operations from the release
refactor that are not fully represented by repository source. It is a handoff
and audit index, not an authorization mechanism, release contract, or durable
release-evidence format. Machine evidence and the exact release authorization
defined in [AGENTS.md](../AGENTS.md) remain authoritative.

Historical coordinates below are evidence only. They are never defaults for a
new dispatch, repair, tag, Release, asset, tap transition, or evidence append.
Incident mechanics are intentionally linked to the
[operator runbook](release-operator-runbook.md) and accepted ADRs instead of
copying raw logs.

## Append protocol

- Records are chronological and use RFC 3339 UTC timestamps from the external
  operation or its final verification.
- A record states failure, partial completion, no-op, or success exactly; an
  observed check is not promoted to a stronger claim.
- Published records are not edited. A later factual correction is a new record
  that identifies the superseded record and preserves both histories.
- Safe identities are limited to public PR/run/attempt/job/artifact, commit,
  tag, Release, and digest coordinates. Never add credentials, OTPs, cookies,
  private mail content, secret values, or raw logs.
- The interaction field states whether browser, email, or an interactive login
  was used. If one is later required, record only its safe purpose and result.

## OP-0001 — Stage 1 baseline integration

- **UTC:** `2026-07-17T12:21:17Z`
- **Repository/scope:** `ildarbinanas-design/env-vault`; immutable baseline and
  release-architecture map.
- **Action and reason:** Pushed, reviewed, and squash-merged
  [PR #45](https://github.com/ildarbinanas-design/env-vault/pull/45) so the
  refactor began from a measured, product-path-neutral baseline.
- **Authorization/gate:** The task authorized staged implementation PRs and
  required exact-head green CI before each merge. This was not a release merge
  and consumed no release confirmation.
- **Safe identity:** PR head
  `34b26eeafc98f213bf6b4b7180698c98248df714`; exact-head
  [CI `29579264273/1`](https://github.com/ildarbinanas-design/env-vault/actions/runs/29579264273)
  and [CodeQL `29579261934/1`](https://github.com/ildarbinanas-design/env-vault/actions/runs/29579261934);
  merge `114ab3e35b6948c0d40e5b7f9c97b867f32cf5eb`; protected-main
  [CI `29579655734/1`](https://github.com/ildarbinanas-design/env-vault/actions/runs/29579655734),
  [CodeQL `29579655513/1`](https://github.com/ildarbinanas-design/env-vault/actions/runs/29579655513),
  and [planning `29579878494/1`](https://github.com/ildarbinanas-design/env-vault/actions/runs/29579878494).
- **Result and verification:** Exact PR head and resulting protected-main SHA
  passed their gates. The durable before-state and graph are in
  [Release architecture and refactor baseline](release-architecture.md).
- **Minimum permission surface:** Normal branch/PR writes and protected squash
  merge; Actions/commit observations were read-only. No settings change,
  ruleset bypass, tag, Release, asset, tap, or evidence mutation.
- **Interaction:** GitHub CLI/API and Actions; browser, email, and interactive
  login were not used.

## OP-0002 — Stage 2 typed GitHub transport integration

- **UTC:** `2026-07-17T15:00:34Z`
- **Repository/scope:** `ildarbinanas-design/env-vault`; release-only typed
  GitHub transport and attempt-qualified identities.
- **Action and reason:** Pushed, reviewed, and squash-merged
  [PR #47](https://github.com/ildarbinanas-design/env-vault/pull/47) to replace
  fragmented release reads with one bounded fail-closed transport boundary.
- **Authorization/gate:** Staged implementation PR authorization; no release
  confirmation was consumed.
- **Safe identity:** PR head
  `664bbf727b1b85cd1f9d49b30cd583fe2bfb0b17`; exact-head
  [CI `29589370666/1`](https://github.com/ildarbinanas-design/env-vault/actions/runs/29589370666)
  and [CodeQL `29589365988/1`](https://github.com/ildarbinanas-design/env-vault/actions/runs/29589365988);
  merge `2c6932fe10a70b3a7cf22e751965af8aca52cb6b`; protected-main
  [CI `29589943145/1`](https://github.com/ildarbinanas-design/env-vault/actions/runs/29589943145),
  [CodeQL `29589938710/1`](https://github.com/ildarbinanas-design/env-vault/actions/runs/29589938710),
  and [planning `29590273689/1`](https://github.com/ildarbinanas-design/env-vault/actions/runs/29590273689).
- **Result and verification:** Exact-head and protected-main gates succeeded.
  Direct REST reads fell from 35 to zero while eight mutation boundaries stayed
  explicit and non-retrying; see
  [ADR 0002](adr/0002-release-github-transport.md).
- **Minimum permission surface:** Normal branch/PR writes and protected squash
  merge; read-only GitHub/Actions verification. No release or settings
  mutation and no permission expansion.
- **Interaction:** GitHub CLI/API and Actions; browser, email, and interactive
  login were not used.

## OP-0003 — Stage 3 compact durable evidence integration

- **UTC:** `2026-07-17T18:29:53Z`
- **Repository/scope:** `ildarbinanas-design/env-vault`; evidence-only genesis,
  compact content-addressed bundle, v1/v2 parity, and offline replay.
- **Action and reason:** Pushed, reviewed, and squash-merged
  [PR #48](https://github.com/ildarbinanas-design/env-vault/pull/48) to remove
  manual fresh-ledger bootstrap and compact evidence without rewriting the
  published production ledger.
- **Authorization/gate:** Staged implementation PR authorization; no release
  confirmation was consumed.
- **Safe identity:** PR head
  `b16d720ebe63994b7987668e14c1ce437e0e4771`; exact-head
  [CI `29602521027/1`](https://github.com/ildarbinanas-design/env-vault/actions/runs/29602521027)
  and [CodeQL `29602518666/1`](https://github.com/ildarbinanas-design/env-vault/actions/runs/29602518666);
  merge `fa5e3fdfe75c956dbd9e4f70484de1f0ec81de3a`; protected-main
  [CI `29603327751/1`](https://github.com/ildarbinanas-design/env-vault/actions/runs/29603327751),
  [CodeQL `29603327129/1`](https://github.com/ildarbinanas-design/env-vault/actions/runs/29603327129),
  and [planning `29603976965/1`](https://github.com/ildarbinanas-design/env-vault/actions/runs/29603976965).
- **Result and verification:** Exact-head and protected-main gates succeeded;
  v1 history remained immutable. The measured logical payload reduction was
  87.1% and deterministic-export reduction was 74.8%, with full offline
  reconstruction and parity. Design and bounded-history constraints are in
  [ADR 0003](adr/0003-compact-release-evidence-ledger.md).
- **Minimum permission surface:** Normal branch/PR writes and protected squash
  merge. Fresh genesis needs only `Contents: write`; no Workflows permission,
  bypass, or settings mutation was added.
- **Interaction:** GitHub CLI/API and Actions; browser, email, and interactive
  login were not used.

## OP-0004 — Exact v0.0.16 authorization, release merge, and tag handoff

- **UTC:** `2026-07-17T20:21:46Z`
- **Repository/scope:** `ildarbinanas-design/env-vault`; generated Release
  Please patch PR and immutable release source.
- **Action and reason:** After rechecking the exact tuple, merged
  [Release Please PR #46](https://github.com/ildarbinanas-design/env-vault/pull/46).
  Green source CI then allowed the planning workflow to create the immutable
  `v0.0.16` tag and start publication.
- **Authorization/gate:** Exact user confirmation:
  `ПОДТВЕРЖДАЮ RELEASE v0.0.16 PR #46 SHA d31480b1ff935a96ef7fc4c927bc13a5c7b5f277`.
  It authorized only that unchanged PR head and resulting release source.
- **Safe identity:** PR head
  `d31480b1ff935a96ef7fc4c927bc13a5c7b5f277`; merge/source/tag
  `ddfd38c3144ed3d0968d2c5e7e4b2acfef841478`; source
  [CI `29610157051/1`](https://github.com/ildarbinanas-design/env-vault/actions/runs/29610157051);
  [planning/tag handoff `29610842415/1`](https://github.com/ildarbinanas-design/env-vault/actions/runs/29610842415).
- **Result and verification:** PR, source, and lightweight tag resolved to the
  same immutable SHA. Published versions `v0.0.12` through `v0.0.15` and their
  evidence histories were not changed.
- **Minimum permission surface:** Protected PR merge; the planning App retained
  its documented single-repository Contents/PR/Issues write and Administration
  read scope with no bypass. It did not create a public Release or asset; see
  [external settings](release-external-settings.md).
- **Interaction:** Exact authorization arrived in the user task; GitHub
  operations used CLI/API and Actions. Browser, email, and interactive login
  were not used.

## OP-0005 — Initial v0.0.16 publication stopped at valid empty assets

- **UTC:** `2026-07-17T20:23:09Z`
- **Repository/scope:** `ildarbinanas-design/env-vault`; public
  [v0.0.16 Release](https://github.com/ildarbinanas-design/env-vault/releases/tag/v0.0.16).
- **Action and reason:** The tag-triggered publisher created the correct public
  Release, then failed closed when a valid `assets: []` response was
  misclassified. The state was inspected before choosing a repair.
- **Authorization/gate:** The OP-0004 exact release authorization permitted the
  fail-closed publisher and an admissible no-clobber repair of that same source;
  it did not authorize moving the tag or replacing published bytes.
- **Safe identity:** Release ID `355905998`; publisher
  [run `29610907056/1`](https://github.com/ildarbinanas-design/env-vault/actions/runs/29610907056),
  release job `87985286552`; source
  `ddfd38c3144ed3d0968d2c5e7e4b2acfef841478`.
- **Result and verification:** The run failed with exactly zero assets; no
  attestation, Homebrew, health, or evidence mutation followed. Root cause and
  recovery boundary are documented in
  [ADR 0004](adr/0004-empty-release-asset-bootstrap.md).
- **Minimum permission surface:** The release job had its normal
  `Contents: write` boundary; later jobs were skipped. No settings, tap,
  evidence, or historical release mutation.
- **Interaction:** Read-only GitHub CLI/API and Actions diagnosis; browser,
  email, and interactive login were not used. The failed job was not blindly
  rerun.

## OP-0006 — Empty-Release repair PR and safe pre-mutation bootstrap stop

- **UTC:** `2026-07-17T21:46:59Z`
- **Repository/scope:** `ildarbinanas-design/env-vault`; protected-main
  source-bound recovery control plane.
- **Action and reason:** Pushed, reviewed, and merged
  [PR #49](https://github.com/ildarbinanas-design/env-vault/pull/49), then
  dispatched the exact-input bootstrap once. Typed transport rejected an
  ambiguous query method before any mutation, so the old run was preserved and
  a new reviewed fix was prepared instead of rerunning it.
- **Authorization/gate:** Admissible repair under OP-0004 plus task authority
  for reviewed repair PRs and scoped Actions dispatch.
- **Safe identity:** PR head
  `4f27930ad78d204bd61b29953bbba7ca8550df80`; exact-head
  [CI `29614197192/1`](https://github.com/ildarbinanas-design/env-vault/actions/runs/29614197192)
  and [CodeQL `29614195450/1`](https://github.com/ildarbinanas-design/env-vault/actions/runs/29614195450);
  merge `6989b737c0e0a7407b5b7949840b0e139f406f16`; protected-main
  [CI `29615045539/1`](https://github.com/ildarbinanas-design/env-vault/actions/runs/29615045539)
  and [CodeQL `29615042863/1`](https://github.com/ildarbinanas-design/env-vault/actions/runs/29615042863);
  failed bootstrap
  [run `29615817787/1`](https://github.com/ildarbinanas-design/env-vault/actions/runs/29615817787),
  job `88000569653`.
- **Result and verification:** PR and main gates succeeded; bootstrap returned
  `INPUT_INVALID` before the mutation step. The Release remained at zero
  assets. No rerun, tag move, asset upload, attestation, tap, or evidence write
  occurred.
- **Minimum permission surface:** Bootstrap declared `Actions: read` and
  `Contents: write` inside the protected `release` environment, but failed
  during read validation before using write authority. No Workflows permission
  or bypass.
- **Interaction:** GitHub CLI/API and Actions; browser, email, and interactive
  login were not used.

## OP-0007 — Corrected bootstrap and partial tag-scoped asset repair

- **UTC:** `2026-07-17T22:31:54Z`
- **Repository/scope:** `ildarbinanas-design/env-vault`; immutable v0.0.16
  asset, checksum, provenance, and SBOM recovery.
- **Action and reason:** Pushed, reviewed, and merged
  [PR #51](https://github.com/ildarbinanas-design/env-vault/pull/51); dispatched
  a fresh bootstrap from the new protected-main SHA; then dispatched one
  standard `repair=release-assets` run for the immutable tag.
- **Authorization/gate:** Admissible repair under OP-0004 and explicit task
  authority for reviewed fixes and scoped fresh dispatches. Failed run
  `29615817787/1` was not rerun.
- **Safe identity:** PR head
  `a1d821846665fb435474eecd7676c1c755d3f139`; exact-head
  [CI `29616480187/1`](https://github.com/ildarbinanas-design/env-vault/actions/runs/29616480187)
  and [CodeQL `29616478387/1`](https://github.com/ildarbinanas-design/env-vault/actions/runs/29616478387);
  merge `2f92b66ee02eb77bb7bc6e628c4c2766c889ff20`; protected-main
  [CI `29617136508/1`](https://github.com/ildarbinanas-design/env-vault/actions/runs/29617136508)
  and [CodeQL `29617135441/1`](https://github.com/ildarbinanas-design/env-vault/actions/runs/29617135441);
  successful bootstrap
  [run `29617861201/1`](https://github.com/ildarbinanas-design/env-vault/actions/runs/29617861201),
  job `88006715813`, result artifact `8421133392`; asset repair
  [run `29617982467/1`](https://github.com/ildarbinanas-design/env-vault/actions/runs/29617982467),
  supply-chain job `88007373760`.
- **Result and verification:** Bootstrap uploaded the one source-bound pair;
  the tag-scoped run reconciled all ten exact assets and completed provenance
  plus SPDX attestations. Its overall conclusion remained **failure** because
  the later Homebrew job rejected informational `Link` metadata before formula,
  App-token, PR, or tap mutation. Health and evidence were skipped. See
  [ADR 0005](adr/0005-informational-link-and-homebrew-bridge.md).
- **Minimum permission surface:** Bootstrap used only `Actions: read` and
  `Contents: write`; publisher release and supply-chain jobs used their normal
  isolated Contents/Attestations write scopes. No Workflows permission,
  settings change, bypass, tag move, or asset replacement.
- **Interaction:** GitHub CLI/API and Actions; browser, email, and interactive
  login were not used. The partially successful publisher was not blindly
  rerun.

## OP-0008 — Homebrew transport repair integration

- **UTC:** `2026-07-18T00:04:12Z`
- **Repository/scope:** `ildarbinanas-design/env-vault`; informational `Link`
  parsing and protected-main Homebrew-only bridge.
- **Action and reason:** Pushed, reviewed, and merged
  [PR #52](https://github.com/ildarbinanas-design/env-vault/pull/52) so the
  immutable source could be bridged without changing its tag, Release assets,
  or attestations.
- **Authorization/gate:** Admissible repair under OP-0004 and staged repair PR
  authority; no second release confirmation was introduced.
- **Safe identity:** PR head
  `7eff069a1992fe2a2bccf313f15ceb41d362e695`; exact-head
  [CI `29621067242/1`](https://github.com/ildarbinanas-design/env-vault/actions/runs/29621067242)
  and [CodeQL `29621066055/1`](https://github.com/ildarbinanas-design/env-vault/actions/runs/29621066055);
  merge `ce1ba7186a4d3133fb04075f275f06e6042c0ccb`; protected-main
  [CI `29621690590/1`](https://github.com/ildarbinanas-design/env-vault/actions/runs/29621690590),
  [CodeQL `29621690204/1`](https://github.com/ildarbinanas-design/env-vault/actions/runs/29621690204),
  and [planning `29622226449/1`](https://github.com/ildarbinanas-design/env-vault/actions/runs/29622226449).
- **Result and verification:** Exact-head and protected-main gates succeeded.
  Correct non-pagination handling and the exact-input bridge landed without
  changing product paths or external settings.
- **Minimum permission surface:** Normal branch/PR writes and protected squash
  merge; read-only GitHub/Actions verification. No release, tap, evidence, or
  settings mutation in this record.
- **Interaction:** GitHub CLI/API and Actions; browser, email, and interactive
  login were not used.

## OP-0009 — v0.0.16 Homebrew bridge, PR, merge, and both tap CI gates

- **UTC:** `2026-07-18T00:11:00Z`
- **Repository/scope:** `ildarbinanas-design/env-vault` and
  `ildarbinanas-design/homebrew-tap`; formula publication.
- **Action and reason:** Dispatched the exact-input protected-main bridge. It
  revalidated release bytes and attestations, generated the formula, created
  [tap PR #9](https://github.com/ildarbinanas-design/homebrew-tap/pull/9),
  waited for exact-head CI, merged that head, and waited for post-merge CI.
- **Authorization/gate:** The OP-0004 exact release authorization plus the
  reviewed OP-0008 bridge and task authority for the required Homebrew
  PR/merge/install/test path.
- **Safe identity:** Bridge
  [run `29622303701/1`](https://github.com/ildarbinanas-design/env-vault/actions/runs/29622303701),
  job `88019597858`, result artifact `8422669170`, digest
  `sha256:773757223242a4dfc3ee189952a3527d3ae3d84492de868e03285d751c6caefd`;
  tap PR head `365363826aa722ac5c2df1cc1e5278dc2c69cfcb`;
  [PR CI `29622381037/1`](https://github.com/ildarbinanas-design/homebrew-tap/actions/runs/29622381037);
  merge/tap SHA `8a20bec7e62c854af9bb9a3f94375ccab580cf4c`;
  [post-merge CI `29622449331/1`](https://github.com/ildarbinanas-design/homebrew-tap/actions/runs/29622449331).
- **Result and verification:** Both arm64 and x86_64 tap jobs completed formula
  style/audit/install/test before and after merge. The typed bridge result
  reported only the next action `dispatch_tag_scoped_health`.
- **Minimum permission surface:** Source token was read-only for Actions,
  Attestations, and Contents. The single-repository tap App had only
  `Actions: read`, `Contents: write`, and `Pull requests: write`, with no
  bypass or Administration/Workflows/Actions-write permission.
- **Interaction:** GitHub Actions and App-backed API operations; browser,
  email, and interactive login were not used.

## OP-0010 — v0.0.16 health, durable evidence, and offline replay

- **UTC:** `2026-07-18T00:16:02Z`
- **Repository/scope:** `ildarbinanas-design/env-vault`; read-only health and
  append-only compact release evidence.
- **Action and reason:** Dispatched one tag-scoped `repair=health` after the
  bridge result; the successful publisher triggered the evidence listener,
  which assembled, replayed, and fast-forwarded the durable ledger.
- **Authorization/gate:** The OP-0004 release authorization and the exact
  next-action result from OP-0009. No further v0.0.16 repair was authorized or
  performed after closure.
- **Safe identity:** Health publisher
  [run `29622574820/1`](https://github.com/ildarbinanas-design/env-vault/actions/runs/29622574820);
  evidence listener
  [run `29622650408/1`](https://github.com/ildarbinanas-design/env-vault/actions/runs/29622650408);
  evidence commit
  [`e697239298c4b5b1240fc53abe611131d45ac7c0`](https://github.com/ildarbinanas-design/env-vault/commit/e697239298c4b5b1240fc53abe611131d45ac7c0)
  with sole parent `af521d52b898088cb49f6256964e377e33e95a5d`;
  compact artifact `8422728320`, digest
  `sha256:8732f0365a4564c3d063b5a2ae1909c14996dca007a1321b0c66304190030eea`.
- **Result and verification:** Health and evidence succeeded. Exact-source
  credential-isolated offline replay reconstructed 1,475,935 canonical bytes,
  matched legacy evidence digest
  `f0e8ab2a0e706192f7ddcffb3d5124bda51d85737f43535763e094b00b96a29f`,
  canonical JSON digest
  `344568ad80a41c6ef97612d604225ce044fe5a48859e0cfd8ab3e89e019d9d70`,
  and bundle semantic digest
  `1cc44109f18d9f6cba0da60e3368afaa186cd5a47d03dcf6b06b7f94f311d003`.
  The closed tuple is also pinned in
  [`release/contract-history.v2.json`](../release/contract-history.v2.json).
- **Minimum permission surface:** Health used read-only observations. Evidence
  assembly was read-only; only its isolated publisher job used
  `Contents: write` for one non-forced evidence-ref fast-forward. No Workflows
  permission, ruleset bypass, tag/Release/asset/tap mutation, or history rewrite.
- **Interaction:** GitHub CLI/API and Actions plus local fully offline replay;
  browser, email, and interactive login were not used.

## OP-0011 — Stage 4 operational contract integration

- **UTC:** `2026-07-19T09:52:56Z`
- **Repository/scope:** `ildarbinanas-design/env-vault`; canonical operational
  contract v2, closed historical routing, and release graph parity.
- **Action and reason:** Pushed, independently reviewed, and squash-merged
  [PR #53](https://github.com/ildarbinanas-design/env-vault/pull/53) to finish
  centralizing current release parameters while preserving historical v1
  authority and every fail-closed/concurrency boundary.
- **Authorization/gate:** Staged implementation PR authorization; no release
  confirmation was consumed.
- **Safe identity:** PR head
  `8fffe52afcb1d2088b326d2f94631970ad8db03f`; exact-head
  [CI `29681408984/1`](https://github.com/ildarbinanas-design/env-vault/actions/runs/29681408984)
  and [CodeQL `29681408317/1`](https://github.com/ildarbinanas-design/env-vault/actions/runs/29681408317);
  merge `0d874277aad3bbfa21b12296d61df8a7f770d622`; protected-main
  [CI `29681884441/1`](https://github.com/ildarbinanas-design/env-vault/actions/runs/29681884441),
  [CodeQL `29681884671/1`](https://github.com/ildarbinanas-design/env-vault/actions/runs/29681884671),
  and [planning `29682330721/1`](https://github.com/ildarbinanas-design/env-vault/actions/runs/29682330721).
- **Result and verification:** Exact-head and protected-main checks succeeded.
  Planning also succeeded on the exact merge SHA. Product implementation paths
  remained unchanged; the current design is in
  [ADR 0006](adr/0006-versioned-operational-release-contract.md).
- **Minimum permission surface:** Normal branch/PR writes and protected squash
  merge; read-only Actions/commit verification. No settings mutation, bypass,
  tag, Release, asset, tap, or evidence mutation.
- **Interaction:** GitHub CLI/API and Actions; browser, email, and interactive
  login were not used.

## OP-0012 — Safe GitHub authentication health recheck

- **UTC:** `2026-07-19T09:54:21Z`
- **Repository/scope:** Existing GitHub CLI session for
  `ildarbinanas-design/env-vault` and
  `ildarbinanas-design/homebrew-tap`.
- **Action and reason:** Rechecked the existing session before publishing this
  handoff journal; confirmed the active account through a read-only user
  identity request.
- **Authorization/gate:** The task explicitly authorized GitHub authentication
  recovery if required and designated a single authentication keeper. No
  recovery was required.
- **Safe identity:** Active account `ildarbinanas-design`; GitHub CLI session
  health succeeded. No credential, token scope list, or raw auth diagnostic is
  retained here.
- **Result and verification:** Both repositories remained accessible with the
  existing session. No login flow, OTP, permission change, or account switch
  occurred.
- **Minimum permission surface:** Read-only authentication status and user
  metadata observation only.
- **Interaction:** GitHub CLI/API; browser, email, interactive login, and OTP
  access were not used.

## OP-0013 — Committed journal branch and draft handoff PR

- **UTC:** `2026-07-19T09:59:55Z`
- **Repository/scope:** `ildarbinanas-design/env-vault`; this append-only
  release-operations journal.
- **Action and reason:** Created a clean sibling worktree from protected
  `main`, committed and pushed the safe backfill, and opened
  [draft PR #54](https://github.com/ildarbinanas-design/env-vault/pull/54) so
  later release operations can be checkpointed for handoff without merging an
  incomplete journal.
- **Authorization/gate:** The user authorized all required writes through task
  completion and specifically required a committed journal for external
  operations. The coordinator opened this stage only after exact protected-main
  CI and CodeQL were green.
- **Safe identity:** Branch `agent/release-operations-journal`; base
  `0d874277aad3bbfa21b12296d61df8a7f770d622`; initial journal commit and PR
  head `e1b2916c5e9204bff2ee736ebe41d768b82383d8`; PR #54 is open and draft.
- **Result and verification:** The remote branch and draft PR exactly matched
  the local initial checkpoint. The PR remains unmerged and available for
  append-only checkpoints through final release verification.
- **Minimum permission surface:** Normal single-repository Contents and pull
  request writes for one branch and draft PR. No settings, Actions dispatch,
  tag, Release, asset, tap, evidence, permission, or ruleset mutation.
- **Interaction:** GitHub CLI/API; browser, email, and interactive login were
  not used.

## OP-0014 — v0.0.17 Release Please exact-head pre-authorization verification

- **UTC:** `2026-07-19T10:07:59Z`
- **Repository/scope:** `ildarbinanas-design/env-vault`; generated
  [Release Please PR #50](https://github.com/ildarbinanas-design/env-vault/pull/50)
  proposing `v0.0.17`.
- **Action and reason:** Re-resolved the proposal after Stage 4 planning,
  verified its exact base/head, generated-only patch, mergeability, proposal
  contract, and all required exact-head checks before presenting a release
  authorization tuple to the user.
- **Authorization/gate:** This was a read-only pre-merge verification under the
  task's release-planning authorization. **Release authorization is absent.**
  The user's earlier general statement of willingness to confirm a release is
  not the exact tuple and is not recorded as release authorization. No exact
  owner/member authorization comment existed on the PR at verification time.
- **Safe identity:** Version `v0.0.17`; PR #50 open, non-draft,
  `MERGEABLE/CLEAN`; base
  `0d874277aad3bbfa21b12296d61df8a7f770d622`; head
  `f60b39333f1b18e53cdc499a095ec29fcad6c54b`; exact-head
  [CI `29682351617/1`](https://github.com/ildarbinanas-design/env-vault/actions/runs/29682351617),
  quality-gate job `88181708040`;
  [CodeQL `29682350994/1`](https://github.com/ildarbinanas-design/env-vault/actions/runs/29682350994),
  actions job `88180634393`, Go job `88180634398`;
  [dependency review `29682351551/1`](https://github.com/ildarbinanas-design/env-vault/actions/runs/29682351551),
  job `88180634535`; and
  [PR-title `29682351575/1`](https://github.com/ildarbinanas-design/env-vault/actions/runs/29682351575),
  job `88180635754`. Required base dependencies were protected-main
  [CI `29681884441/1`](https://github.com/ildarbinanas-design/env-vault/actions/runs/29681884441)
  and [planning `29682330721/1`](https://github.com/ildarbinanas-design/env-vault/actions/runs/29682330721).
- **Result and verification:** All required exact-head checks and the complete
  CI graph succeeded. The proposal verifier passed. The head was one
  bot-authored commit with the exact base as sole parent and changed only
  `.release-please-manifest.json`, `CHANGELOG.md`, and `README.md`. The tuple is
  eligible to be shown to the user, but no merge, tag, Release, asset,
  attestation, tap, health, or evidence action is authorized by this record.
- **Minimum permission surface:** Read-only PR, commit, check-run, and comment
  observations. No write permission was exercised and no setting or ruleset
  was changed.
- **Interaction:** GitHub CLI/API and local proposal verification; browser,
  email, and interactive login were not used.

## OP-0015 — Exact v0.0.17 release authorization received

- **UTC:** `2026-07-19T10:11:54Z`
- **Repository/scope:** `ildarbinanas-design/env-vault`; exact generated
  Release Please proposal verified in OP-0014.
- **Action and reason:** Received the user's exact release authorization after
  the unchanged tuple had passed the OP-0014 exact-head precheck.
- **Authorization/gate:** Exact user message:
  `ПОДТВЕРЖДАЮ RELEASE v0.0.17 PR #50 SHA f60b39333f1b18e53cdc499a095ec29fcad6c54b`.
  This permits the contract-defined authorization-comment and merge operation
  only for `v0.0.17`, PR #50, and that full unchanged head SHA. It does not
  authorize a changed head, PR, version, ref, or unrelated mutation.
- **Safe identity:** Version `v0.0.17`; PR #50; exact head
  `f60b39333f1b18e53cdc499a095ec29fcad6c54b`; green pre-authorization evidence
  is OP-0014.
- **Result and verification:** The exact authorization fact is recorded. At
  this boundary no PR comment, merge, tag, Release, asset, attestation,
  Homebrew, health, or evidence operation is claimed as completed.
- **Minimum permission surface:** No GitHub write permission was exercised by
  this journal record. The later contract helper may act only on the unchanged
  authorized tuple and must revalidate it before each mutation.
- **Interaction:** Authorization arrived directly in the user task; browser,
  email, GitHub login, and OTP access were not used.

## OP-0016 — Current-task head-guarded automerge preference

- **UTC:** `2026-07-19T10:14:21Z`
- **Repository/scope:** Operator preference for the remaining head-guarded
  merge actions in this already confirmed env-vault release task.
- **Action and reason:** Recorded the user's instruction:
  `Ты в следующий раз можешь не ждать такую строку? Даю разрешение на автомердж тобой.`
  This avoids asking for a duplicate gate while autonomously completing the
  exact task already authorized in OP-0015.
- **Authorization/gate:** For later head-guarded merge actions within this
  current confirmed task, no repeated confirmation gate is required. The
  exact `v0.0.17` authorization remains OP-0015 and is not broadened to a
  different release version, PR, or head.
- **Safe identity:** Current release authorization remains version `v0.0.17`,
  PR #50, head `f60b39333f1b18e53cdc499a095ec29fcad6c54b` from OP-0015.
- **Result and verification:** The operator preference is recorded only. It
  does not change repository protections, release contract, rulesets, required
  checks, App permissions, or any GitHub object. Future separate releases still
  follow the current repository contract unless a distinct reviewed policy
  change is merged.
- **Minimum permission surface:** No GitHub or repository write permission was
  exercised by this decision record.
- **Interaction:** Instruction arrived directly in the user task; browser,
  email, GitHub login, and OTP access were not used.

## OP-0017 — Exact authorization comment and head-guarded v0.0.17 merge

- **UTC:** `2026-07-19T10:14:26Z`
- **Repository/scope:** `ildarbinanas-design/env-vault`;
  [Release Please PR #50](https://github.com/ildarbinanas-design/env-vault/pull/50).
- **Action and reason:** Ran the checked-in typed-contract wrapper and
  fail-closed release authorization/merge helper. The operation first created
  exactly one owner-authored exact-tuple comment, revalidated the unchanged PR
  state, and then head-guarded the squash merge to that same authorized head.
- **Authorization/gate:** OP-0015 exact authorization for `v0.0.17`, PR #50,
  head `f60b39333f1b18e53cdc499a095ec29fcad6c54b`. OP-0016 required no duplicate
  prompt but did not broaden that tuple.
- **Safe identity:** At `2026-07-19T10:13:59Z`, the helper created exact
  authorization comment ID `5015329932`,
  [permanent URL](https://github.com/ildarbinanas-design/env-vault/pull/50#issuecomment-5015329932),
  authored by the repository `OWNER` user with the exact OP-0015 tuple body.
  At `2026-07-19T10:14:26Z`, it squash-merged PR #50 from base
  `0d874277aad3bbfa21b12296d61df8a7f770d622` and authorized head
  `f60b39333f1b18e53cdc499a095ec29fcad6c54b` to merge/source/main
  `53d256eaa07a2c25f49ae373f26aa3f2946ae82c`. The merge commit has the exact
  base as its sole parent; the comment precedes the merge by 27 seconds.
- **Result and verification:** Exactly one authorization comment and one
  head-guarded squash merge were observed. The PR merge is complete. This
  record does not yet claim source CI, tag, Release, publisher, asset,
  attestation, Homebrew, health, or evidence completion.
- **Minimum permission surface:** Issues/pull-request write for the single
  authorization comment; pull-request/Contents write for the exact squash
  merge. No bypass, Administration, settings, ruleset, Actions-write, or
  permission change.
- **Interaction:** Checked-in local helper plus GitHub CLI/API. No rerun,
  workflow dispatch, authentication flow, browser, email, or OTP access was
  used.

## OP-0018 — v0.0.17 exact-source protected-main gate

- **UTC:** `2026-07-19T10:29:30Z`
- **Repository/scope:** `ildarbinanas-design/env-vault`; exact release source
  `53d256eaa07a2c25f49ae373f26aa3f2946ae82c` after OP-0017.
- **Action and reason:** Waited for and read-only verified the repository-owned
  protected-main CI and CodeQL attempts before allowing the automatic planning
  workflow to classify or create the release tag.
- **Authorization/gate:** OP-0015 exact release authorization and the exact
  OP-0017 merge/source tuple. This observation introduced no new authorization.
- **Safe identity:** Push/main
  [CI `29682997343/1`](https://github.com/ildarbinanas-design/env-vault/actions/runs/29682997343),
  created `2026-07-19T10:14:28Z`, completed
  `2026-07-19T10:29:30Z`; quality-gate job `88183518472`, E2E gate
  `88183478967`, source job `88182361433`, and five native platform jobs
  `88182391784`, `88182391790`, `88182391792`, `88182391796`, and
  `88182392237`. Exact-source
  [CodeQL `29682997053/1`](https://github.com/ildarbinanas-design/env-vault/actions/runs/29682997053)
  also succeeded.
- **Result and verification:** All 12 CI jobs succeeded. Immediately before
  planning, the typed `v0.0.17` tag/Release observation returned documented
  absence status `4`; no tag or Release pre-existed. No rerun, repair, or
  workflow dispatch occurred in this checkpoint.
- **Minimum permission surface:** Read-only Actions, commit/ref, and Release
  observations. No GitHub write permission, settings change, or ruleset bypass.
- **Interaction:** GitHub CLI/API and Actions; browser, email, authentication
  flow, and OTP access were not used.

## OP-0019 — Automated v0.0.17 planning and exact tag creation

- **UTC:** `2026-07-19T10:30:46Z`
- **Repository/scope:** `ildarbinanas-design/env-vault`; release-planning
  transition from exact green source to immutable `refs/tags/v0.0.17`.
- **Action and reason:** The `release-please` workflow_run listener rechecked
  repository settings, the exact authorization/merge tuple, the successful
  source CI attempt, and the offline promotion before the planning App created
  the lightweight tag and closed the proposal lifecycle.
- **Authorization/gate:** OP-0015 exact release authorization, OP-0017 exact
  comment/merge, and OP-0018 green source gate. The automatic transition was
  the contract-defined next action and required no additional manual tag step.
- **Safe identity:** Planning
  [run `29683428751/1`](https://github.com/ildarbinanas-design/env-vault/actions/runs/29683428751),
  event `workflow_run`, exact source
  `53d256eaa07a2c25f49ae373f26aa3f2946ae82c`, conclusion `success`; inspect
  job `88183528476`, plan job `88183560284`, and correctly skipped whole-attempt
  rerun job `88183560516`. Settings proof ran from
  `2026-07-19T10:30:31Z` to `10:30:32Z`; exact release authorization was
  reverified from `10:30:35Z` to `10:30:38Z`; source CI attempt was rechecked
  from `10:30:38Z` to `10:30:39Z`. The App created
  `refs/tags/v0.0.17` at the exact source from `10:30:39Z` to `10:30:46Z`.
- **Result and verification:** The run succeeded. Offline attempt
  classification passed for five native targets and ten promoted envelopes;
  promotion-manifest SHA-256 was
  `1d18021b7a8310790fa0f59150950e447f75c99df5b76b56d1d4bb42b81bdfca`.
  The pre-state had neither tag nor Release. The resulting tag resolves exactly
  to the authorized source, and PR #50 transitioned to the sole
  `autorelease: tagged` lifecycle label. No public Release or publisher result
  is claimed by this record.
- **Minimum permission surface:** The planning App remained repository-scoped
  with Contents, Issues, and pull-request write plus settings/Administration
  read; no ruleset bypass or permission change. No manual tag, rerun, repair,
  workflow dispatch, or settings mutation occurred.
- **Interaction:** Automated GitHub Actions and repository-scoped App API;
  browser, email, authentication flow, and OTP access were not used.

## OP-0020 — v0.0.17 Release, supply chain, and tap merge checkpoint

- **UTC:** `2026-07-19T10:36:39Z`
- **Repository/scope:** `ildarbinanas-design/env-vault` publisher and
  `ildarbinanas-design/homebrew-tap` formula transition.
- **Action and reason:** The tag-triggered publisher promoted the exact CI
  bytes without a rebuild, published the stable Release and supply-chain
  attestations, then created, tested, and head-guarded the deterministic
  Homebrew formula PR merge. At this timestamp tap post-merge CI was still
  pending, so the publisher was not yet claimed terminal.
- **Authorization/gate:** OP-0015 exact release authorization, OP-0018 source
  gate, and OP-0019 exact tag. This was the contract-defined publisher path
  with `repair=none`.
- **Safe identity — publisher:**
  [run `29683468172/1`](https://github.com/ildarbinanas-design/env-vault/actions/runs/29683468172),
  tag `v0.0.17`, source
  `53d256eaa07a2c25f49ae373f26aa3f2946ae82c`; metadata job `88183645193`,
  preflight `88183675072`, promotion `88183675076`, release `88183725422`,
  and supply-chain `88183784757` all succeeded.
- **Safe identity — Release and assets:** Stable
  [v0.0.17 Release](https://github.com/ildarbinanas-design/env-vault/releases/tag/v0.0.17),
  ID `356314103`, published `2026-07-19T10:32:19Z`. Its exact no-clobber
  archive/checksum pairs were:
  - `linux-amd64`: asset `482305350`,
    `sha256:414624bec9c9204c6f41002eb5725ce1a4ee2e5dd46e2c3ef992dd60e3d4f800`;
    checksum `482305358`,
    `sha256:5a0d05265e419bd2fbcedf8babbc14612ffa9c64862f1f1a544bf57170021404`.
  - `linux-arm64`: asset `482305362`,
    `sha256:b51ed6dbbbb7bd6e91951fe03bd12aa7bef080858da9ea7d39c82492120880c0`;
    checksum `482305371`,
    `sha256:8d7b750f514797ef9a00aebc7a6a796a909fdb339986e80d9635001389de635c`.
  - `darwin-amd64`: asset `482305375`,
    `sha256:fa39b2621953a80fc75edff3f73c309a650c8d0394b66a1f918b2fd027693969`;
    checksum `482305384`,
    `sha256:7d821dab7a48335009297ccfe884f92127c20b5f483ebc56c37d52d1333f38ce`.
  - `darwin-arm64`: asset `482305389`,
    `sha256:52f9a07b07a8a69622369eee1732a5d938c7bb58d49d7881ad4b01606e0137e8`;
    checksum `482305398`,
    `sha256:e62b3fbe160d029146a136a1b7aa7121ca49b950e01ff37606f73dce72c1ceee`.
  - `windows-amd64`: asset `482305411`,
    `sha256:14cc1a6d16fcac6450ca463ac0c8faff87266c894655ab07af0fb93dd5fc8fe2`;
    checksum `482305423`,
    `sha256:7a981d6420b1168837c0b8e8f0e1877d11d506f8d14104380dbe037a3a0f9e45`.
- **Safe identity — supply chain:** All ten Release bytes compared equal to
  the exact CI promotion. One SLSA provenance statement and one SPDX 2.3
  statement each bound all five archive subjects; all ten subject/predicate
  endpoint counts were exactly one. `gh attestation verify` passed all five
  archives against both predicates with signer
  `build-binaries.yml@refs/tags/v0.0.17`, exact source, and invocation
  `29683468172/1`.
- **Safe identity — Homebrew checkpoint:** Bot-created
  [tap PR #10](https://github.com/ildarbinanas-design/homebrew-tap/pull/10),
  created `2026-07-19T10:34:51Z`; base
  `8a20bec7e62c854af9bb9a3f94375ccab580cf4c`; head and sole formula commit
  `b784483a9d2d31aef3dbd83f7519cdc3146c8e37` with the exact base as sole
  parent. Only `Formula/env-vault.rb` changed (`+9/-9`), byte-equal to the
  generator, formula SHA-256
  `8b512f0b28e0b84f8ea1846485d96fb8fd11ece8336703b131401bfb7953eb21`.
  Exact-head
  [PR CI `29683590059/1`](https://github.com/ildarbinanas-design/homebrew-tap/actions/runs/29683590059)
  succeeded, then the App head-guarded the squash merge at
  `2026-07-19T10:36:39Z` to tap main
  `fd42c1af83fac106ee29709047b57641efb8b499`.
- **Result and verification:** Release, ten assets, provenance, SPDX SBOM, tap
  PR-head CI, and exact tap merge were complete. Tap post-merge
  [CI `29683642229/1`](https://github.com/ildarbinanas-design/homebrew-tap/actions/runs/29683642229)
  was pending at this boundary; no Homebrew-job, health, publisher-terminal, or
  evidence success is claimed here.
- **Minimum permission surface:** Release job `Contents: write`; supply-chain
  job `Contents: read`, `id-token: write`, and `Attestations: write`. The tap
  App retained only `Actions: read`, `Contents: write`, and
  `Pull requests: write`, with no bypass or manual mutation.
- **Interaction:** Automated GitHub Actions and repository-scoped App API;
  browser, email, authentication flow, and OTP access were not used.

## OP-0021 — Homebrew post-merge CI completion

- **UTC:** `2026-07-19T10:38:20Z`
- **Repository/scope:** `ildarbinanas-design/homebrew-tap` exact PR-head and
  protected-main formula gates; env-vault publisher Homebrew job.
- **Action and reason:** Waited for the exact tap main CI attempt after the
  OP-0020 head-guarded merge and bound both platform results back to the
  publisher before allowing its health job to start.
- **Authorization/gate:** Continuation of the OP-0020 contract-defined
  Homebrew transition; no new release or repair authorization.
- **Safe identity:** PR-head
  [CI `29683590059/1`](https://github.com/ildarbinanas-design/homebrew-tap/actions/runs/29683590059)
  succeeded at `2026-07-19T10:36:30Z` on exact head
  `b784483a9d2d31aef3dbd83f7519cdc3146c8e37`: arm64 `macos-15` job
  `88183969485`, x86_64 `macos-15-intel` job `88183969473`, gate
  `88184084997`. After PR #10 merged to
  `fd42c1af83fac106ee29709047b57641efb8b499`, post-merge
  [CI `29683642229/1`](https://github.com/ildarbinanas-design/homebrew-tap/actions/runs/29683642229)
  succeeded at `2026-07-19T10:38:20Z`: x86_64 job `88184107486`, arm64 job
  `88184107493`, gate `88184224018`.
- **Result and verification:** Both architectures passed `brew style`,
  `brew audit`, `brew install`, and `brew test` in PR-head and post-merge runs.
  Publisher Homebrew job `88183884470` succeeded. Health job `88184241254` was
  active at this boundary; its result, publisher terminal status, and durable
  evidence are not claimed here.
- **Minimum permission surface:** Tap App `Actions: read`, `Contents: write`,
  and `Pull requests: write`; no manual merge, rerun, settings change, or
  ruleset bypass.
- **Interaction:** Automated GitHub Actions and repository-scoped App API;
  browser, email, authentication flow, and OTP access were not used.

## OP-0022 — First v0.0.17 publisher completed without repair

- **UTC:** `2026-07-19T10:39:42Z`
- **Repository/scope:** `ildarbinanas-design/env-vault`; terminal status of
  the first tag-triggered v0.0.17 publisher.
- **Action and reason:** Waited for the sealed health job and re-observed the
  whole publisher attempt after OP-0021 Homebrew completion.
- **Authorization/gate:** OP-0015 exact release authorization and the normal
  `repair=none` publisher path; no repair authorization was consumed.
- **Safe identity:** Publisher
  [run `29683468172/1`](https://github.com/ildarbinanas-design/env-vault/actions/runs/29683468172),
  event `push`, tag `v0.0.17`, exact source
  `53d256eaa07a2c25f49ae373f26aa3f2946ae82c`, completed `success` at
  `2026-07-19T10:39:42Z`. All seven jobs succeeded: metadata `88183645193`,
  preflight `88183675072`, promotion `88183675076`, release `88183725422`,
  supply-chain `88183784757`, Homebrew `88183884470`, and sealed health
  `88184241254`.
- **Result and verification:** The first publisher completed successfully with
  no repair, workflow dispatch, or rerun. Release, assets, attestations,
  Homebrew, and health are terminally green for this attempt. The automatic
  evidence listener is expected next, but no evidence run or ledger result is
  claimed by this record.
- **Minimum permission surface:** No additional mutation surface beyond the
  isolated OP-0020 publisher jobs; sealed health used read-only observations.
  No settings change, bypass, or permission expansion.
- **Interaction:** Automated GitHub Actions; browser, email, authentication
  flow, and OTP access were not used.

## OP-0023 — Typed v0.0.17 health proof verification

- **UTC:** `2026-07-19T10:41:48Z`
- **Repository/scope:** `ildarbinanas-design/env-vault`; retained publisher
  observation and health proof for the OP-0022 successful attempt.
- **Action and reason:** Downloaded the retained observation artifact through
  read-only Actions access and verified its typed health document and digest
  before relying on health as an input to durable evidence.
- **Authorization/gate:** Read-only verification of the OP-0022 normal
  publisher result; no new release, repair, or evidence authorization.
- **Safe identity:** Artifact ID `8441392159`, name
  `env-vault-release-observation-v0.0.17-attempt-1`, digest
  `sha256:0af38c95d7fb81c6a4a2018ae95ea1aa631df4bcf7f124105c52851c69c660b6`.
  Health schema `env-vault.release-health-proof.v1` was checked at
  `2026-07-19T10:39:39Z` for publisher `29683468172/1`, tag `v0.0.17`, and
  source `53d256eaa07a2c25f49ae373f26aa3f2946ae82c`; proof SHA-256
  `8d76b73fc71ec7d7cc8b8c92ab135382e748651ed428365f09b17c14b10ae297`.
- **Result and verification:** Typed result `pass`. Exact tag/source, published
  Release, ten assets, attestations, Homebrew state, both tap CI gates,
  blocked-tag policy, and abandoned `v0.0.12` policy were all true. The settings
  proof bound three `ACTIVE` rulesets with zero bypass actors. Durable evidence
  was still pending and is not claimed by this record.
- **Minimum permission surface:** Read-only Actions artifact download and local
  proof verification. No GitHub mutation, settings change, bypass, or permission
  expansion.
- **Interaction:** GitHub CLI/API plus local proof verification; browser,
  email, authentication flow, and OTP access were not used.

## OP-0024 — v0.0.17 durable evidence fast-forward and offline verification

- **UTC:** `2026-07-19T10:46:10Z`
- **Repository/scope:** `ildarbinanas-design/env-vault`; automatic compact
  evidence listener, append-only production ledger, and independent offline
  replay for the successful OP-0022 publisher.
- **Action and reason:** The workflow_run listener assembled the exact
  publisher candidate, verified it before mutation, fast-forwarded only the
  protected evidence ref, retained the compact bundle, and then independently
  downloaded and replayed that bundle with an empty environment and command
  path.
- **Authorization/gate:** Automatic evidence transition from successful
  publisher `29683468172/1`; no manual evidence, repair, bootstrap, or rerun
  authorization was used.
- **Safe identity — workflow and candidate:** Automatic
  [run `29683728742/1`](https://github.com/ildarbinanas-design/env-vault/actions/runs/29683728742),
  title `env-vault-release-evidence publisher-run=29683468172 attempt=1`, event
  `workflow_run`, exact source
  `53d256eaa07a2c25f49ae373f26aa3f2946ae82c`, completed `success`; assemble
  job `88184341930` and publish job `88184614337` both succeeded. Candidate
  artifact `8441424668` was 232,525 bytes with digest
  `sha256:c0d98e85ab11433956c043780439ceff8c0c285482df599e73c090b22a42582a`.
- **Safe identity — ledger:** Between `2026-07-19T10:45:40Z` and
  `10:46:04Z`, `refs/heads/release-evidence` fast-forwarded from
  `e697239298c4b5b1240fc53abe611131d45ac7c0` to
  [`b0592ee7e9013d750704733d8e030a69056ef319`](https://github.com/ildarbinanas-design/env-vault/commit/b0592ee7e9013d750704733d8e030a69056ef319).
  The new commit has the old tip as its sole parent and adds only v0.0.17 paths
  plus three content-addressed objects. All v0.0.14, v0.0.15, and v0.0.16 tree
  entries remained byte- and object-identical.
- **Safe identity — compact replay:** Artifact `8441452672`, name
  `env-vault-release-evidence-v2-v0.0.17-b0592ee7e9013d750704733d8e030a69056ef319-publisher-29683468172-attempt-1-evidence-29683728742-1`,
  size 56,073 bytes, digest
  `sha256:8c882e2c37a651d0461af79acb6c1b3089e02f48c49ebef1b65d60e0ea06e81a`.
  Independent `env -i` empty-`PATH` bundle verification passed with evidence
  SHA-256 `949636f066591e44c3dadd39352b548bc1513a4fe17e5d111105b09964b01830`,
  bundle SHA-256
  `7ef2713bae2efb6fed4dc80aad3e199997c7a7dcd207f7b10b41fb50b1fca5fd`,
  1,476,724 reconstructed v1 bytes, `decision=pass`, empty error, and
  `reconstructed_byte_exact=true`.
- **Result and verification:** Evidence workflow and post-write observation
  succeeded. The bundle used three objects; compact metadata was 10,926 bytes
  versus 1,483,702 legacy bytes. Logical reduction was 871 permille and
  deterministic-export reduction was 747 permille; `targets_met=true`. Durable
  evidence for v0.0.17 is complete and fully replayable offline.
- **Minimum permission surface:** Only the isolated publisher job used
  `Contents: write` for one non-forced evidence-ref fast-forward. No Workflows
  write, App/PAT replacement, ruleset bypass, manual bootstrap, or historical
  rewrite. Artifact download and post-write verification were read-only.
- **Interaction:** Automated GitHub Actions plus read-only download and local
  credential-isolated offline verification; browser, email, authentication
  flow, and OTP access were not used.

## OP-0025 — Correction of the OP-0004 historical publication range

- **UTC:** `2026-07-19T10:49:52Z`
- **Repository/scope:** This journal's immutable historical-release wording;
  final invariant audit against `RELEASING.md`, `release/contract.v2.json`, and
  the operator runbook.
- **Action and reason:** The audit found that one OP-0004 result sentence called
  `v0.0.12` published. This correction supersedes only the OP-0004 sentence
  beginning `Published versions v0.0.12 through v0.0.15`; the original record
  remains present under the append-only protocol.
- **Authorization/gate:** Factual handoff correction required by the final
  invariant audit. It is not release authorization or a recovery action.
- **Safe identity:** The correct published immutable range in that sentence is
  `v0.0.13` through `v0.0.15`. Version `v0.0.12` is permanently abandoned: its
  exact tag and GitHub Release must remain HTTP 404, and its absence/recovery
  history remains preserved. All other OP-0004 facts remain unchanged.
- **Result and verification:** Journal wording is corrected without editing
  the earlier record. No tag, Release, asset, evidence, repository setting, or
  other external state changed.
- **Minimum permission surface:** Read-only local durable-source invariant
  audit; only the ordinary journal branch documentation checkpoint is written.
- **Interaction:** Local repository inspection and Git journal update; browser,
  email, GitHub authentication flow, and OTP access were not used.
