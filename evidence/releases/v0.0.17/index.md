# env-vault v0.0.17 release evidence

- Result: `pass`
- Source SHA: `53d256eaa07a2c25f49ae373f26aa3f2946ae82c`
- Tag ref SHA: `53d256eaa07a2c25f49ae373f26aa3f2946ae82c`
- Tag target SHA: `53d256eaa07a2c25f49ae373f26aa3f2946ae82c`
- Release: [v0.0.17](https://github.com/ildarbinanas-design/env-vault/releases/tag/v0.0.17)
- Authorized release PR: `#50` at `f60b39333f1b18e53cdc499a095ec29fcad6c54b`
- Exact confirmation: [comment `5015329932`](https://github.com/ildarbinanas-design/env-vault/pull/50#issuecomment-5015329932) by `ildarbinanas-design` (`OWNER`) at `2026-07-19T10:13:59Z`; body SHA-256 `be14db3da020c79514bf43dfa1f4ae1326c2eb5814e20a78c1622295ccab3156`
- Planning run / attempt: `29683428751` / `1`
- Release PR CI run / attempt: `29682351617` / `1`
- Evidence run / attempt: `29683728742` / `1`
- Publisher run / attempt / repair: `29683468172` / `1` / `none`
- Promotion manifest SHA-256: `1d18021b7a8310790fa0f59150950e447f75c99df5b76b56d1d4bb42b81bdfca`
- Evidence SHA-256: `949636f066591e44c3dadd39352b548bc1513a4fe17e5d111105b09964b01830`
- Observed at: `2026-07-19T10:39:39Z`

## Workflow metrics

| Workflow | Run / attempt | Jobs | Queue (s) | Wall (s) | Runner (s) | Retries |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| CI | 29682997343 / 1 | 12 | 0 | 902 | 1619 | 0 |
| Publisher | 29683468172 / 1 | 7 | 0 | 537 | 520 | 0 |

## Published assets

| Asset | Bytes | SHA-256 |
| --- | ---: | --- |
| `env-vault-linux-amd64.tar.gz` | 2581422 | `414624bec9c9204c6f41002eb5725ce1a4ee2e5dd46e2c3ef992dd60e3d4f800` |
| `env-vault-linux-amd64.tar.gz.sha256` | 95 | `5a0d05265e419bd2fbcedf8babbc14612ffa9c64862f1f1a544bf57170021404` |
| `env-vault-linux-arm64.tar.gz` | 2323270 | `b51ed6dbbbb7bd6e91951fe03bd12aa7bef080858da9ea7d39c82492120880c0` |
| `env-vault-linux-arm64.tar.gz.sha256` | 95 | `8d7b750f514797ef9a00aebc7a6a796a909fdb339986e80d9635001389de635c` |
| `env-vault-darwin-amd64.tar.gz` | 2275418 | `fa39b2621953a80fc75edff3f73c309a650c8d0394b66a1f918b2fd027693969` |
| `env-vault-darwin-amd64.tar.gz.sha256` | 96 | `7d821dab7a48335009297ccfe884f92127c20b5f483ebc56c37d52d1333f38ce` |
| `env-vault-darwin-arm64.tar.gz` | 2087288 | `52f9a07b07a8a69622369eee1732a5d938c7bb58d49d7881ad4b01606e0137e8` |
| `env-vault-darwin-arm64.tar.gz.sha256` | 96 | `e62b3fbe160d029146a136a1b7aa7121ca49b950e01ff37606f73dce72c1ceee` |
| `env-vault-windows-amd64.zip` | 2426129 | `14cc1a6d16fcac6450ca463ac0c8faff87266c894655ab07af0fb93dd5fc8fe2` |
| `env-vault-windows-amd64.zip.sha256` | 94 | `7a981d6420b1168837c0b8e8f0e1877d11d506f8d14104380dbe037a3a0f9e45` |

## Supply-chain attestations

| Archive | Provenance run / attempt | SPDX run / attempt |
| --- | ---: | ---: |
| `env-vault-linux-amd64.tar.gz` | 29683468172 / 1 | 29683468172 / 1 |
| `env-vault-linux-arm64.tar.gz` | 29683468172 / 1 | 29683468172 / 1 |
| `env-vault-darwin-amd64.tar.gz` | 29683468172 / 1 | 29683468172 / 1 |
| `env-vault-darwin-arm64.tar.gz` | 29683468172 / 1 | 29683468172 / 1 |
| `env-vault-windows-amd64.zip` | 29683468172 / 1 | 29683468172 / 1 |

## Homebrew

- Pull request: [#10](https://github.com/ildarbinanas-design/homebrew-tap/pull/10)
- PR head SHA: `b784483a9d2d31aef3dbd83f7519cdc3146c8e37`
- Exact release merge SHA: `fd42c1af83fac106ee29709047b57641efb8b499`
- Current tap SHA: `fd42c1af83fac106ee29709047b57641efb8b499` (contains release merge: `true`)
- PR-head CI: run `29683590059`, attempt `1`
- Post-merge CI: run `29683642229`, attempt `1`
- Formula SHA-256: `8b512f0b28e0b84f8ea1846485d96fb8fd11ece8336703b131401bfb7953eb21`

## Preserved blocked-tag policy

- `v0.0.8`: tag `1d094f9e4a3e0343e713d4126f6118a8a9e98e2d` exists; GitHub Release absent
- `v0.0.9`: tag `b8b652dcff41d5f2ab4a9f14bed65ddf1f866c65` exists; GitHub Release absent
- `v0.0.10`: tag `591350ea0e9ebb2b9ef7a8f9d89c0e86c251c795` exists; GitHub Release absent
- `v0.0.11`: tag `95181260700afdb0bf257b69f490079d2fb6d5f0` exists; GitHub Release absent

## Preserved abandoned-release policy

- `v0.0.12`: merged PR `#31` at `a0eb82cb1fc4fa486ff2032d50ddedf6bccdbb8b` is labeled `autorelease: abandoned`; tag and GitHub Release absent
