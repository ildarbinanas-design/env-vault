# env-vault v0.0.18 release evidence

- Result: `pass`
- Source SHA: `2346d2aab4bb1081eb6eb819bd8561a69732979e`
- Tag ref SHA: `2346d2aab4bb1081eb6eb819bd8561a69732979e`
- Tag target SHA: `2346d2aab4bb1081eb6eb819bd8561a69732979e`
- Release: [v0.0.18](https://github.com/ildarbinanas-design/env-vault/releases/tag/v0.0.18)
- Authorized release PR: `#55` at `4a799c1e675b06995da975b7a43e5c6acffe2842`
- Exact confirmation: [comment `5017317173`](https://github.com/ildarbinanas-design/env-vault/pull/55#issuecomment-5017317173) by `ildarbinanas-design` (`OWNER`) at `2026-07-19T20:46:27Z`; body SHA-256 `1605952b113201e28bdc00ce3287c4b566faf1f791118d1f1bff007411bafac6`
- Planning run / attempt: `29703620511` / `1`
- Release PR CI run / attempt: `29685480740` / `1`
- Evidence run / attempt: `29703960883` / `1`
- Publisher run / attempt / repair: `29703664391` / `1` / `none`
- Promotion manifest SHA-256: `fb8a87e45ec2e2082a5dee8ceec490bb9e3a8ab9832d074bcc1c13449ce75906`
- Evidence SHA-256: `9cecd06de836c1e57c2d5767e8c87ba780af35f5a410bb8d570ca5b4f2362110`
- Observed at: `2026-07-19T21:13:07Z`

## Workflow metrics

| Workflow | Run / attempt | Jobs | Queue (s) | Wall (s) | Runner (s) | Retries |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| CI | 29703155093 / 1 | 12 | 0 | 904 | 1538 | 0 |
| Publisher | 29703664391 / 1 | 7 | 0 | 591 | 571 | 0 |

## Published assets

| Asset | Bytes | SHA-256 |
| --- | ---: | --- |
| `env-vault-linux-amd64.tar.gz` | 2581414 | `4dc60e662b1427e5e2ac00d74f7a03d36d8aeae3292745e1e168b4446f717199` |
| `env-vault-linux-amd64.tar.gz.sha256` | 95 | `091d6dd5448c70ec3162507ef75743d1732205726f5b2f54ad6d3234a3dc2e2e` |
| `env-vault-linux-arm64.tar.gz` | 2323260 | `2c8b18de4a65f3a35d504973c17571919cdbe454da9d8b63feeb05de9c03c548` |
| `env-vault-linux-arm64.tar.gz.sha256` | 95 | `70bf4ec62a72204965212f60c99986cc42b1f2fb519a04ce64a212e800a4a481` |
| `env-vault-darwin-amd64.tar.gz` | 2275427 | `1c2d3f67b577352375dbc73a27f5f3bbf8d8275561c1524d873677d05431e4ac` |
| `env-vault-darwin-amd64.tar.gz.sha256` | 96 | `007f04182d12e4b7c3c7bc44d39c7d93e10c2fa9c195375e1a762373f28ce3e0` |
| `env-vault-darwin-arm64.tar.gz` | 2087299 | `a54d29f44132aa1498e456d563de55bf11178ed5c0dfb3b993d5ec4f0e9f9d3a` |
| `env-vault-darwin-arm64.tar.gz.sha256` | 96 | `781f5e7faeb1ac8b9e492134bc44ad9636101534a35566797ec06942d0cf3e6e` |
| `env-vault-windows-amd64.zip` | 2426130 | `9c5ec4a0f04161b961d2c12507fe6f624f6dd542cea0af13a3220a5263588c0e` |
| `env-vault-windows-amd64.zip.sha256` | 94 | `f47d913c55045f0cdb37ea5f80ca1fab56d5df0bc5f7829fe2a6852dc9330406` |

## Supply-chain attestations

| Archive | Provenance run / attempt | SPDX run / attempt |
| --- | ---: | ---: |
| `env-vault-linux-amd64.tar.gz` | 29703664391 / 1 | 29703664391 / 1 |
| `env-vault-linux-arm64.tar.gz` | 29703664391 / 1 | 29703664391 / 1 |
| `env-vault-darwin-amd64.tar.gz` | 29703664391 / 1 | 29703664391 / 1 |
| `env-vault-darwin-arm64.tar.gz` | 29703664391 / 1 | 29703664391 / 1 |
| `env-vault-windows-amd64.zip` | 29703664391 / 1 | 29703664391 / 1 |

## Homebrew

- Pull request: [#11](https://github.com/ildarbinanas-design/homebrew-tap/pull/11)
- PR head SHA: `429cbf68197e5c834f555bc5e38f0e9bb389c5d8`
- Exact release merge SHA: `8f7fc2691d5237bec3ae4cbfd6c05740fa550051`
- Current tap SHA: `8f7fc2691d5237bec3ae4cbfd6c05740fa550051` (contains release merge: `true`)
- PR-head CI: run `29703804051`, attempt `1`
- Post-merge CI: run `29703857251`, attempt `1`
- Formula SHA-256: `62f18fbc2f2ef3f85516af64c448b3403dfd6a8a5841103236c26f7f797b7dab`

## Preserved blocked-tag policy

- `v0.0.8`: tag `1d094f9e4a3e0343e713d4126f6118a8a9e98e2d` exists; GitHub Release absent
- `v0.0.9`: tag `b8b652dcff41d5f2ab4a9f14bed65ddf1f866c65` exists; GitHub Release absent
- `v0.0.10`: tag `591350ea0e9ebb2b9ef7a8f9d89c0e86c251c795` exists; GitHub Release absent
- `v0.0.11`: tag `95181260700afdb0bf257b69f490079d2fb6d5f0` exists; GitHub Release absent

## Preserved abandoned-release policy

- `v0.0.12`: merged PR `#31` at `a0eb82cb1fc4fa486ff2032d50ddedf6bccdbb8b` is labeled `autorelease: abandoned`; tag and GitHub Release absent
