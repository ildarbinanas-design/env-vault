# env-vault v0.0.15 release evidence

- Result: `pass`
- Source SHA: `c7dd1fd6176ac2abbea22f226795a0787e774c1b`
- Tag ref SHA: `c7dd1fd6176ac2abbea22f226795a0787e774c1b`
- Tag target SHA: `c7dd1fd6176ac2abbea22f226795a0787e774c1b`
- Release: [v0.0.15](https://github.com/ildarbinanas-design/env-vault/releases/tag/v0.0.15)
- Authorized release PR: `#42` at `04d91dcfae7dcf26cda7f66e722ff2936502081d`
- Exact confirmation: [comment `5002627095`](https://github.com/ildarbinanas-design/env-vault/pull/42#issuecomment-5002627095) by `ildarbinanas-design` (`OWNER`) at `2026-07-17T11:14:08Z`; body SHA-256 `580d86d464c0a05e35c9d623880352c4e482ebb4a66e1568538f942d3b8a6706`
- Planning run / attempt: `29576406873` / `1`
- Release PR CI run / attempt: `29572388235` / `1`
- Evidence run / attempt: `29576963736` / `1`
- Publisher run / attempt / repair: `29576465336` / `1` / `none`
- Promotion manifest SHA-256: `bde833e1e4a46a0de15fa83a7c78f2ef401662d0ed6560d6f6b2f37accf16302`
- Evidence SHA-256: `2c339829ad1ea77c4f8e91dc8cfb896d43978e281ee76b2e82022fe0c65fc63e`
- Observed at: `2026-07-17T11:28:37Z`

## Workflow metrics

| Workflow | Run / attempt | Jobs | Queue (s) | Wall (s) | Runner (s) | Retries |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| CI | 29576181486 / 1 | 12 | 0 | 240 | 910 | 0 |
| Publisher | 29576465336 / 1 | 7 | 0 | 546 | 527 | 0 |

## Published assets

| Asset | Bytes | SHA-256 |
| --- | ---: | --- |
| `env-vault-linux-amd64.tar.gz` | 2581416 | `0912cb05d1b05d726a1ae02e7873407be192c74201e5d6c326174f35fa1ccdea` |
| `env-vault-linux-amd64.tar.gz.sha256` | 95 | `8863fa0163839c0f097aef048ef6aab1b6c86670bfc774be9b0737734ea52e58` |
| `env-vault-linux-arm64.tar.gz` | 2323275 | `a4ba3dc289f148f7b46b4728954bb15d548c98dc1f64ae747237bd9f944efcc8` |
| `env-vault-linux-arm64.tar.gz.sha256` | 95 | `a0c720ae84a7bee7acf2d6040148d80b3fbcba2d3e9989f84552daf42f1432bf` |
| `env-vault-darwin-amd64.tar.gz` | 2275424 | `d9142b24ed0fd4c08317cb925f56c15a8e63a1cdad89b15b2ec5f8ad82b0a585` |
| `env-vault-darwin-amd64.tar.gz.sha256` | 96 | `36628196fba249f94c1e85ba3aa6b834cae8fd19871847c322450d78348c3e42` |
| `env-vault-darwin-arm64.tar.gz` | 2087288 | `81bb81ecf81d481176edceb65791bac6ad101d7e7ebd9399d1506d71a6fc21b2` |
| `env-vault-darwin-arm64.tar.gz.sha256` | 96 | `47fee4ae29312fa2c0da3ebc10589a10cac376aa546aeabcb31f1e307a7a3578` |
| `env-vault-windows-amd64.zip` | 2426127 | `e9eea182236da106ca3f40373642faa4b10703c30402740b57f5347b486ed41a` |
| `env-vault-windows-amd64.zip.sha256` | 94 | `37b5fe0481dbc27096427ed092aa6482168329f00cca3a6098403e7f6e84ee25` |

## Supply-chain attestations

| Archive | Provenance run / attempt | SPDX run / attempt |
| --- | ---: | ---: |
| `env-vault-linux-amd64.tar.gz` | 29576465336 / 1 | 29576465336 / 1 |
| `env-vault-linux-arm64.tar.gz` | 29576465336 / 1 | 29576465336 / 1 |
| `env-vault-darwin-amd64.tar.gz` | 29576465336 / 1 | 29576465336 / 1 |
| `env-vault-darwin-arm64.tar.gz` | 29576465336 / 1 | 29576465336 / 1 |
| `env-vault-windows-amd64.zip` | 29576465336 / 1 | 29576465336 / 1 |

## Homebrew

- Pull request: [#8](https://github.com/ildarbinanas-design/homebrew-tap/pull/8)
- PR head SHA: `019c8c721259dc2af8cb9ef19228bb4895378d1e`
- Exact release merge SHA: `71217af8d0c692e27d8c268c9cce5a2a533f4ea9`
- Current tap SHA: `71217af8d0c692e27d8c268c9cce5a2a533f4ea9` (contains release merge: `true`)
- PR-head CI: run `29576677229`, attempt `1`
- Post-merge CI: run `29576768487`, attempt `1`
- Formula SHA-256: `6b1b7710b9b406ac5309aed21a6cd14269129a2142e33690ed15b3a49d882ced`

## Preserved blocked-tag policy

- `v0.0.8`: tag `1d094f9e4a3e0343e713d4126f6118a8a9e98e2d` exists; GitHub Release absent
- `v0.0.9`: tag `b8b652dcff41d5f2ab4a9f14bed65ddf1f866c65` exists; GitHub Release absent
- `v0.0.10`: tag `591350ea0e9ebb2b9ef7a8f9d89c0e86c251c795` exists; GitHub Release absent
- `v0.0.11`: tag `95181260700afdb0bf257b69f490079d2fb6d5f0` exists; GitHub Release absent

## Preserved abandoned-release policy

- `v0.0.12`: merged PR `#31` at `a0eb82cb1fc4fa486ff2032d50ddedf6bccdbb8b` is labeled `autorelease: abandoned`; tag and GitHub Release absent
