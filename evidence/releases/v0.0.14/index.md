# env-vault v0.0.14 release evidence

- Result: `pass`
- Source SHA: `c42a92144a82c19edea41c76328ec7fd1e408ceb`
- Tag ref SHA: `c42a92144a82c19edea41c76328ec7fd1e408ceb`
- Tag target SHA: `c42a92144a82c19edea41c76328ec7fd1e408ceb`
- Release: [v0.0.14](https://github.com/ildarbinanas-design/env-vault/releases/tag/v0.0.14)
- Authorized release PR: `#39` at `40d12c48fe87a7a4ef7fbb735d7b2759d88c53a9`
- Exact confirmation: [comment `5000118962`](https://github.com/ildarbinanas-design/env-vault/pull/39#issuecomment-5000118962) by `ildarbinanas-design` (`OWNER`) at `2026-07-17T07:23:48Z`; body SHA-256 `85feaa6f735ab34f9c5ddc72f1fe9e249cb51abd42de2fbe17a2173fb115bcbf`
- Planning run / attempt: `29563198918` / `1`
- Release PR CI run / attempt: `29562392602` / `1`
- Evidence run / attempt: `29569819553` / `2`
- Publisher run / attempt / repair: `29569706872` / `1` / `health`
- Promotion manifest SHA-256: `eae28e5063d0a1a2b64e8c05e81bd83049bb1b7242e0986140eacd1fa2942569`
- Evidence SHA-256: `6a4d45205a5a662cfb21beee5726a67473a42dd273763c0662299343c3e85076`
- Observed at: `2026-07-17T09:23:49Z`

## Workflow metrics

| Workflow | Run / attempt | Jobs | Queue (s) | Wall (s) | Runner (s) | Retries |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| CI | 29562964603 / 1 | 12 | 0 | 270 | 911 | 0 |
| Publisher | 29569706872 / 1 | 7 | 0 | 107 | 100 | 0 |

## Published assets

| Asset | Bytes | SHA-256 |
| --- | ---: | --- |
| `env-vault-linux-amd64.tar.gz` | 2581428 | `63c08758c643b8f9b6be9874e1e433a4c4ab49b31d035af62491196379f4422b` |
| `env-vault-linux-amd64.tar.gz.sha256` | 95 | `bd0f1944567c6a390c39c3987cdbb7a4c829f024dc1d15f12fbf3732326bd511` |
| `env-vault-linux-arm64.tar.gz` | 2323276 | `a9865bcb44caa40947a32f303db9f80dcfa289e5db089ccb179d37b17acca78f` |
| `env-vault-linux-arm64.tar.gz.sha256` | 95 | `b06599759fbbbc073520f92c96866b450dc221d2810c201f273a28270f328491` |
| `env-vault-darwin-amd64.tar.gz` | 2275427 | `bef836e0c848c86fb2c438379c0834032de7abf3461a63c491a96d58a860edcd` |
| `env-vault-darwin-amd64.tar.gz.sha256` | 96 | `480e32c83725378072e5899509bf309a0d8313f6eb1981328c1ecb89f35ce167` |
| `env-vault-darwin-arm64.tar.gz` | 2087303 | `a7886ca55e1869ac6bcd8033f724a5e5332b2fd0a8a8e099dcda44cc197925b5` |
| `env-vault-darwin-arm64.tar.gz.sha256` | 96 | `cdaadbc6bf5bb3a39b4d36b06230f0ab982457b30b0716148f34b54a8da6a240` |
| `env-vault-windows-amd64.zip` | 2426130 | `31045367216e6c4363a29983ac3bf64284bb2e8810fc4c65e780789c20137507` |
| `env-vault-windows-amd64.zip.sha256` | 94 | `832920cd8391a75c2ff65252570863e48d0435a76170a50da2e22bad8cc23275` |

## Supply-chain attestations

| Archive | Provenance run / attempt | SPDX run / attempt |
| --- | ---: | ---: |
| `env-vault-linux-amd64.tar.gz` | 29563246593 / 1 | 29563246593 / 1 |
| `env-vault-linux-arm64.tar.gz` | 29563246593 / 1 | 29563246593 / 1 |
| `env-vault-darwin-amd64.tar.gz` | 29563246593 / 1 | 29563246593 / 1 |
| `env-vault-darwin-arm64.tar.gz` | 29563246593 / 1 | 29563246593 / 1 |
| `env-vault-windows-amd64.zip` | 29563246593 / 1 | 29563246593 / 1 |

## Homebrew

- Pull request: [#7](https://github.com/ildarbinanas-design/homebrew-tap/pull/7)
- PR head SHA: `d7ed71e72e82ae0c66cee57d07aca313deec2f87`
- Exact release merge SHA: `10b414ded49c9730c654139ee3b42b1fbfb9abd0`
- Current tap SHA: `10b414ded49c9730c654139ee3b42b1fbfb9abd0` (contains release merge: `true`)
- PR-head CI: run `29563446331`, attempt `1`
- Post-merge CI: run `29563590353`, attempt `1`
- Formula SHA-256: `fc6e25bcf6c2be8f6cd9ad7591587b6e75477fb21406db270c4a2b6bf8a8709e`

## Preserved blocked-tag policy

- `v0.0.8`: tag `1d094f9e4a3e0343e713d4126f6118a8a9e98e2d` exists; GitHub Release absent
- `v0.0.9`: tag `b8b652dcff41d5f2ab4a9f14bed65ddf1f866c65` exists; GitHub Release absent
- `v0.0.10`: tag `591350ea0e9ebb2b9ef7a8f9d89c0e86c251c795` exists; GitHub Release absent
- `v0.0.11`: tag `95181260700afdb0bf257b69f490079d2fb6d5f0` exists; GitHub Release absent

## Preserved abandoned-release policy

- `v0.0.12`: merged PR `#31` at `a0eb82cb1fc4fa486ff2032d50ddedf6bccdbb8b` is labeled `autorelease: abandoned`; tag and GitHub Release absent
