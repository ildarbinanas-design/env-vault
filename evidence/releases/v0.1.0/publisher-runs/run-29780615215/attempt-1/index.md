# env-vault v0.1.0 release evidence

- Result: `pass`
- Source SHA: `3db426262d230bee0aa135ea58e9ec0dbe3cb51c`
- Tag ref SHA: `3db426262d230bee0aa135ea58e9ec0dbe3cb51c`
- Tag target SHA: `3db426262d230bee0aa135ea58e9ec0dbe3cb51c`
- Release: [v0.1.0](https://github.com/ildarbinanas-design/env-vault/releases/tag/v0.1.0)
- Authorized release PR: `#57` at `3571e9472a7a2f7c529852365c93b46b30c8d158`
- Exact confirmation: [comment `5027362337`](https://github.com/ildarbinanas-design/env-vault/pull/57#issuecomment-5027362337) by `ildarbinanas-design` (`OWNER`) at `2026-07-20T21:16:03Z`; body SHA-256 `c0ed1b4f1e1b79d2d45bfc03eaa0084a67671501a6395c5ee9f8fdaea8c64d46`
- Planning run / attempt: `29780539919` / `1`
- Release PR CI run / attempt: `29773169852` / `1`
- Evidence run / attempt: `29781285373` / `1`
- Publisher run / attempt / repair: `29780615215` / `1` / `none`
- Promotion manifest SHA-256: `a678f1dab7f6ea3bb5bbfdd56918e8b0ce2e5dfc9a97ed492dfed2894394757c`
- Evidence SHA-256: `cd3f67edc7a6eab90d3b3ad7179dc9f54c81ca1a2af57d48f20e94fd9ffe10bd`
- Observed at: `2026-07-20T21:43:27Z`

## Workflow metrics

| Workflow | Run / attempt | Jobs | Queue (s) | Wall (s) | Runner (s) | Retries |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| CI | 29779532885 / 1 | 12 | 0 | 928 | 1620 | 0 |
| Publisher | 29780615215 / 1 | 7 | 0 | 624 | 605 | 0 |

## Published assets

| Asset | Bytes | SHA-256 |
| --- | ---: | --- |
| `env-vault-linux-amd64.tar.gz` | 2581403 | `93a1cbf23239391c3fa549f5fb37d4aa4649518c2d3e8ba1f1d1374f4e1460c5` |
| `env-vault-linux-amd64.tar.gz.sha256` | 95 | `f53894e7be950baca6b28525ef4d0ef5d59186b44a81b07f924b24dbda1ee811` |
| `env-vault-linux-arm64.tar.gz` | 2323264 | `cfe0a0ea995c686e2dea02b6cbfe718a392e9c9c6a728972e9dd52c1380747b9` |
| `env-vault-linux-arm64.tar.gz.sha256` | 95 | `7a2fac3ae78876d59253d0ea090960c4130e7dc07050103ccc03de7c98637805` |
| `env-vault-darwin-amd64.tar.gz` | 2275399 | `2dec8d2c76ea0a7177189751dc841e5fb6c0290b890752670793a58a0576f617` |
| `env-vault-darwin-amd64.tar.gz.sha256` | 96 | `3377ceb64f07d2ae4bdfc222b306a0893fd52c04f3175c7c72cec5e7e530f8bf` |
| `env-vault-darwin-arm64.tar.gz` | 2087299 | `1414cf087a66e663a65e6fc43b06d56dc0121c5eee7864031a15231dd0534409` |
| `env-vault-darwin-arm64.tar.gz.sha256` | 96 | `ecf98cce36117d5dc1e7ab413a9387c9c2f0df08e0cfa650623e40dd8c1a0fde` |
| `env-vault-windows-amd64.zip` | 2426090 | `56c0a49f93e10c3fa2813cf6ec2eee7c754afd38662d09452da3a3591a80fc7e` |
| `env-vault-windows-amd64.zip.sha256` | 94 | `f82ee1f759afd0e354e4a9a99d491848fa8a1a6692eedbfd0346a4f6a24b4fdf` |

## Supply-chain attestations

| Archive | Provenance run / attempt | SPDX run / attempt |
| --- | ---: | ---: |
| `env-vault-linux-amd64.tar.gz` | 29780615215 / 1 | 29780615215 / 1 |
| `env-vault-linux-arm64.tar.gz` | 29780615215 / 1 | 29780615215 / 1 |
| `env-vault-darwin-amd64.tar.gz` | 29780615215 / 1 | 29780615215 / 1 |
| `env-vault-darwin-arm64.tar.gz` | 29780615215 / 1 | 29780615215 / 1 |
| `env-vault-windows-amd64.zip` | 29780615215 / 1 | 29780615215 / 1 |

## Homebrew

- Pull request: [#12](https://github.com/ildarbinanas-design/homebrew-tap/pull/12)
- PR head SHA: `e6c23fcb19ffefd4db09a40df32005d60943ba67`
- Exact release merge SHA: `50c30f64ad03e817077b996540b77e56c49cac14`
- Current tap SHA: `50c30f64ad03e817077b996540b77e56c49cac14` (contains release merge: `true`)
- PR-head CI: run `29780938186`, attempt `1`
- Post-merge CI: run `29781040954`, attempt `1`
- Formula SHA-256: `87aaf2e66a97a978b5fc3e35d1d2b83d17527d5636e3c65e6266f7ba1b5558b8`

## Preserved blocked-tag policy

- `v0.0.8`: tag `1d094f9e4a3e0343e713d4126f6118a8a9e98e2d` exists; GitHub Release absent
- `v0.0.9`: tag `b8b652dcff41d5f2ab4a9f14bed65ddf1f866c65` exists; GitHub Release absent
- `v0.0.10`: tag `591350ea0e9ebb2b9ef7a8f9d89c0e86c251c795` exists; GitHub Release absent
- `v0.0.11`: tag `95181260700afdb0bf257b69f490079d2fb6d5f0` exists; GitHub Release absent

## Preserved abandoned-release policy

- `v0.0.12`: merged PR `#31` at `a0eb82cb1fc4fa486ff2032d50ddedf6bccdbb8b` is labeled `autorelease: abandoned`; tag and GitHub Release absent
