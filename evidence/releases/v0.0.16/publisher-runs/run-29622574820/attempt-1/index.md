# env-vault v0.0.16 release evidence

- Result: `pass`
- Source SHA: `ddfd38c3144ed3d0968d2c5e7e4b2acfef841478`
- Tag ref SHA: `ddfd38c3144ed3d0968d2c5e7e4b2acfef841478`
- Tag target SHA: `ddfd38c3144ed3d0968d2c5e7e4b2acfef841478`
- Release: [v0.0.16](https://github.com/ildarbinanas-design/env-vault/releases/tag/v0.0.16)
- Authorized release PR: `#46` at `d31480b1ff935a96ef7fc4c927bc13a5c7b5f277`
- Exact confirmation: [comment `5007074422`](https://github.com/ildarbinanas-design/env-vault/pull/46#issuecomment-5007074422) by `ildarbinanas-design` (`OWNER`) at `2026-07-17T20:08:42Z`; body SHA-256 `16d97833f558d3732fceb9eba80fe045fa7cee46876228dce0a061a28c51d3aa`
- Planning run / attempt: `29610842415` / `1`
- Release PR CI run / attempt: `29604019287` / `1`
- Evidence run / attempt: `29622650408` / `1`
- Publisher run / attempt / repair: `29622574820` / `1` / `health`
- Promotion manifest SHA-256: `0ad8b3da779d9e8bea0c36377ed7f9b3132cb7e72cec0fbb91e3fbe012e4ed5b`
- Evidence SHA-256: `f0e8ab2a0e706192f7ddcffb3d5124bda51d85737f43535763e094b00b96a29f`
- Observed at: `2026-07-18T00:14:28Z`

## Workflow metrics

| Workflow | Run / attempt | Jobs | Queue (s) | Wall (s) | Runner (s) | Retries |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| CI | 29610157051 / 1 | 12 | 0 | 680 | 1341 | 0 |
| Publisher | 29622574820 / 1 | 7 | 0 | 119 | 111 | 0 |

## Published assets

| Asset | Bytes | SHA-256 |
| --- | ---: | --- |
| `env-vault-linux-amd64.tar.gz` | 2581413 | `cf1621cc19a26755ee6c8296ce26fbb10d6ae1a5459b3015e01f89ba6958f98f` |
| `env-vault-linux-amd64.tar.gz.sha256` | 95 | `ef094a511702df4b3389abaf20353aae10048fbd9dc35f9c92caf2ad2ef60d72` |
| `env-vault-linux-arm64.tar.gz` | 2323274 | `bd6230d5ce64ac4b40fcdad29bd39920d0f37fa7a25228358c2906afa2c27b3f` |
| `env-vault-linux-arm64.tar.gz.sha256` | 95 | `1916aee2052fa91b0503c18f165b1c2647d67d9b903d392db690cc6b87e7171c` |
| `env-vault-darwin-amd64.tar.gz` | 2275421 | `afc3ed48d33cdaa1016a943b6f44809bc2dfbd015fa245f175e9c2b35aa2fc2f` |
| `env-vault-darwin-amd64.tar.gz.sha256` | 96 | `7e8955029f43e541012516eb7d2e85f7ab49e9e527b38630ab06c4b2534fc29e` |
| `env-vault-darwin-arm64.tar.gz` | 2087284 | `7ddbc60007508eff30c56b8770caf52d46d7e0a3ddfc7d894fda7e96b2448989` |
| `env-vault-darwin-arm64.tar.gz.sha256` | 96 | `e0dc41ecdf0990c8c0c993991d8a1e4431c15b9060a8836571570ca84e6500fd` |
| `env-vault-windows-amd64.zip` | 2426127 | `125b853ac523d4598d7e8cb6190b4f9d8e2c97460d125c13f6a6263905f3af18` |
| `env-vault-windows-amd64.zip.sha256` | 94 | `6cbf9f1bf814e121ec493a5a27603eddb006b4ee3f105b570c743ccd409e6bb7` |

## Supply-chain attestations

| Archive | Provenance run / attempt | SPDX run / attempt |
| --- | ---: | ---: |
| `env-vault-linux-amd64.tar.gz` | 29617982467 / 1 | 29617982467 / 1 |
| `env-vault-linux-arm64.tar.gz` | 29617982467 / 1 | 29617982467 / 1 |
| `env-vault-darwin-amd64.tar.gz` | 29617982467 / 1 | 29617982467 / 1 |
| `env-vault-darwin-arm64.tar.gz` | 29617982467 / 1 | 29617982467 / 1 |
| `env-vault-windows-amd64.zip` | 29617982467 / 1 | 29617982467 / 1 |

## Homebrew

- Pull request: [#9](https://github.com/ildarbinanas-design/homebrew-tap/pull/9)
- PR head SHA: `365363826aa722ac5c2df1cc1e5278dc2c69cfcb`
- Exact release merge SHA: `8a20bec7e62c854af9bb9a3f94375ccab580cf4c`
- Current tap SHA: `8a20bec7e62c854af9bb9a3f94375ccab580cf4c` (contains release merge: `true`)
- PR-head CI: run `29622381037`, attempt `1`
- Post-merge CI: run `29622449331`, attempt `1`
- Formula SHA-256: `84744fab6a16c70b89d54b49f9390771f880696d2b4d63846856c772fb14510f`

## Preserved blocked-tag policy

- `v0.0.8`: tag `1d094f9e4a3e0343e713d4126f6118a8a9e98e2d` exists; GitHub Release absent
- `v0.0.9`: tag `b8b652dcff41d5f2ab4a9f14bed65ddf1f866c65` exists; GitHub Release absent
- `v0.0.10`: tag `591350ea0e9ebb2b9ef7a8f9d89c0e86c251c795` exists; GitHub Release absent
- `v0.0.11`: tag `95181260700afdb0bf257b69f490079d2fb6d5f0` exists; GitHub Release absent

## Preserved abandoned-release policy

- `v0.0.12`: merged PR `#31` at `a0eb82cb1fc4fa486ff2032d50ddedf6bccdbb8b` is labeled `autorelease: abandoned`; tag and GitHub Release absent
