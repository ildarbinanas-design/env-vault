# Actions activity report — 2026-07-30

Measured directly via `gh api` / `gh run list` / `gh pr list` against
`ildarbinanas-design/env-vault`. All timestamps UTC. This is raw
observation, not a restatement of `docs/release-refactor-backlog.md` —
runner-seconds/cost (backlog item 6) is a separate axis and is not repeated
here; this report is about wall-clock time a human actually waits.

## 1. PR open → checks green → merge (last 15 merged PRs, #49–#67)

| PR | Kind | Files | Open → gate green | Gate green → merge |
|----|------|-------|-------------------:|---------------------:|
| #67 | docs | .md only | 15m27s | 30s |
| #66 | docs(adr) | .md only | 15m48s | 39s |
| #63 | docs | .md only | 15m21s | 1m06s |
| #62 | docs | .md only | **30m30s** | 37s |
| #61 | docs | .md only | 15m27s | 2m14s |
| #60 | feat | code | 15m28s | 49s |
| #56 | docs(release) | .md only | 15m09s | 38s |
| #54 | docs | .md only | **1h20m25s** | 1m30s |
| #53 | refactor(release) | workflow yml | 15m09s | 47s |
| #52 | fix(release) | workflow yml | 12m59s | 2m09s |
| #51 | fix(release) | workflow yml | 11m21s | 56s |
| #49 | fix(release) | workflow yml | 13m38s | 1m16s |

(PRs #57/#55/#50 omitted from this table — they're the automated
`chore(main): release ...` PRs; see §3, different dynamic.)

**Finding:** for 10 of 12 non-release PRs, open→green is a flat **~11–16
minutes regardless of diff size or kind** — a one-line ADR edit (#66) waits
exactly as long as a multi-file `feat` (#60). The single job that
determines this is `quality / source-quality`: across every PR checked, it
starts within seconds of PR creation and runs **13–15 minutes** every
time, while every other required check (artifact-quality ×5 platforms,
license ×3, CodeQL, Dependency review, pr-title) completes in under 3
minutes. Source-quality, not the multi-platform build matrix, is the
pipeline's actual latency floor.

**#62 (30m30s)** is the same 15-minute job, but delayed a further ~15
minutes before it even started (queued from PR-open at 18:55:42 to
job-start at 19:10:54) — the fixed cost compounded with a scheduling gap.

**#54 (1h20m25s)** is not pipeline latency at all: the head commit was
pushed to 13 times in that window (visible as a chain of `cancelled` `ci`
runs at 09:59, 10:00, 10:08, 10:12, 10:15, 10:30, 10:32, 10:40, 10:42,
10:48, 10:50, 10:52 before the final green run at 11:05–11:20) — each push
cancelled the in-flight run and restarted the 15-minute clock. That's
iterative human editing, not the pipeline's doing.

Merge itself is fast everywhere: **30s–2m14s** from green to merged, i.e.
auto-merge/manual-merge overhead is negligible once checks pass.

## 2. Merge → release published (last 3 actual releases)

| Release | PR | Merged | release-please picks up | Binaries published | Evidence recorded |
|---------|-----|--------|--------------------------|----------------------|---------------------|
| v0.1.0  | #57 | 21:16:24 | +15m33s | +27m06s | +33m45s |
| v0.0.18 | #55 | 20:46:56 | +15m08s | +26m16s | +33m29s |
| v0.0.17 | #50 | 21:16:24* | +15m06s | +25m16s | +31m44s |

*v0.0.17's actual merge-to-main push was 2026-07-19T10:14:26; offsets are
relative to that.

**Finding:** merge → binaries-published is a consistent **~25–27
minutes**, fully unattended (no human waits on it, no approval gate). It
decomposes as: the same ~15-minute `source-quality` job re-running on the
push-to-main (before `release-please` is even allowed to fire, since it's
triggered off `ci` completing), then ~10 minutes for
`env-vault-publication` to build and ship 5 platform binaries, then a
separate ~6-minute `env-vault-release-evidence` audit run after. So the
15-minute tax from §1 isn't paid once per PR — it's paid **again** on
every push to `main`, whether or not a release follows.

## 3. Release-please PRs: the real wait is a business decision, not CI

| Release PR | Opened | Merged | Elapsed |
|---|---|---|---|
| #57 (v0.1.0) | 2026-07-19T22:10:20 | 2026-07-20T21:16:24 | ~23h06m |
| #55 (v0.0.18) | 2026-07-19T11:37:50 | 2026-07-19T20:46:56 | ~9h09m |
| #50 (v0.0.17) | 2026-07-17T21:45:45 | 2026-07-19T10:14:26 | ~1d12h29m |

Checks on these PRs go green within the same ~15 minutes as any other PR
(re-verified each time release-please force-pushes the PR as new commits
land on `main`). The multi-hour-to-multi-day gap before merge is the
maintainer choosing *when to cut a release*, not the pipeline blocking
anyone. This is not developer-UX friction — flagging it only so it isn't
mistaken for pipeline latency in the totals above.

## 4. Workflow run duration, last ~4 weeks (2026-07-02 → 2026-07-30)

- `quality / source-quality`: flat 13–15 min on every single run, PR or
  push, docs-only or not. No outliers — it's not degrading, it's just
  uniformly this slow.
- Artifact builds (darwin/linux/windows × amd64/arm64, 5 jobs): 1m–4m40s
  each, run in parallel, never the critical path.
- License / Dependency review / pr-title / CodeQL / Analyze: all
  well under 90 seconds.
- `env-vault-publication` (the actual release build+publish): 8–11 min on
  clean runs.
- `env-vault-release-evidence` (post-publish audit): ~6–7 min, runs after
  publication, non-blocking.
- One clear historical degradation window: **2026-07-17, ~05:00–2026-07-18
  00:14** — three consecutive release-publication failures and repairs
  (v0.0.13 push failed, its `repair=homebrew` retry also failed; v0.0.14
  push "succeeded" but its evidence-check failed twice, needing two
  `repair=health` manual dispatches; v0.0.16 push failed, its
  `repair=release-assets` retry failed too, before a manual
  `bootstrap-release-assets` + `repair=health` finally landed it ~4 hours
  later). This is exactly the incident window that produced PRs **#49
  ("recover empty release assets"), #51 ("require GET for workflow
  queries"), #52 ("recover Homebrew publication safely")** — i.e. 3 of the
  last 15 merged PRs exist purely to fix bugs the release pipeline itself
  introduced.

## 5. Last ~30 runs / current state

- 2 failures in the last 30 runs: 2026-07-27 (`ci` on a Dependabot PR —
  fails because Dependabot PRs run with restricted secrets, not a real
  defect) and 2026-07-21 (`ci` on the auto-updating release-please PR
  #64, self-resolved on the next push). No unexplained failures.
- No runs currently stuck: at time of writing 2 runs are legitimately
  `in_progress` (this session's own PR #68, opened during this
  measurement). No hung/orphaned runs found in the window.
- `actions/artifacts`: **373 artifacts currently retained**, mix of
  14-day retention (release binaries, `gotestsum` reports) and 30-day
  retention (`e2e-candidate`, `e2e-baseline-verification`). Recording the
  fact per the task; not evaluating whether this is a problem here.

## Bottom line (of the 15 recent PRs: 3 were release-please cut PRs, 3
were fixes for pipeline failures, 1 shipped a feature, 8 were docs)

The pipeline does not create meaningful developer-facing delay for a
solo maintainer today: PR wait time is a flat ~15 minutes regardless of
what's in the diff, merges apply within seconds of green, and release
publication is a fully unattended ~25 minutes. The actual cost isn't
latency — it's that a single always-on 13–15 minute job runs unconditionally
on every doc-only edit and a second time on every push to `main`, and
that in the last two weeks the release-publish path itself broke three
separate times, consuming three PRs' worth of the owner's time to repair.
For a one-person repo whose recent throughput is mostly documentation and
release-pipeline maintenance rather than features, that ratio — engineering
effort spent operating and fixing the pipeline vs. effort spent on
PersonalOS-relevant work — is the one number worth revisiting, more than
either the 15-minute tax or the artifact count.
