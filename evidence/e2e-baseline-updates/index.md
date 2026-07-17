# Durable E2E baseline update

## Deterministic E2E reporter transition

The baseline was updated from the sealed five-platform candidate matrix
produced by workflow run
[`29549118082`](https://github.com/ildarbinanas-design/env-vault/actions/runs/29549118082),
attempt `1`, source `80fb5fb6e802d3d603bc22c6e2f97e29931987f7`, for pull
request head `51862c4b4e502269f041ea47eeb157e6cae0e000`.

The pre-update verifier failed exactly one check, `semantic_suite`. Matrix
schema and digest, platform set, exact run identity, archived migration,
normalized evidence, toolchain, all five public contracts, coverage floors,
critical scenarios, and leak expectations passed. The deterministic diff
changes only the semantic suite hash and exact run provenance; no accepted
contract, coverage, scenario, expected-skip, tool-version, or leak value
changed.

Machine bindings:

- baseline-verifier matrix proof digest:
  `a6f4c8023004d41a311782f6313c45851a0aecd0143e687660a9f9bb768ddea7`;
- matrix proof file SHA-256:
  `4597fc77433f9f0ba76c48fcfc10840ee649e9f567add6a09e7906a453c1fbce`;
- baseline proof artifact ID and archive digest:
  `8395144007`,
  `sha256:d33e271e7f39aa410dab3686eb63c3b1a8359a38e46c1c44224320a01e998e4b`;
- updated baseline semantic digest:
  `ce2b2b7e319817b848e95e6c3faa8722064eabb6e444dd503c5a3df05e05d7ff`;
- updated baseline file SHA-256:
  `d2b33b29802950835ac127dd854728f10f5617c00a0b4f7cf4b64a415e594fcf`;
- reviewable diff file SHA-256:
  `52f49db3a9255e9c3bb513e9215e03001ca477872de4b42c837f0e2ebe5f88b9`.

The deterministic update is recorded in
`run-29549118082-attempt-1.diff.json`.

## Release-version contract normalization

The baseline was updated from the sealed five-platform candidate matrix
produced by workflow run
[`29526068945`](https://github.com/ildarbinanas-design/env-vault/actions/runs/29526068945),
attempt `1`, source `054d7b1c3f1c3a63e8a2ed162f72f3ad2f28a9b9`.

The matrix proof passed schema, platform-set, exact-run, semantic-suite,
toolchain, normalized-evidence, public-contract, coverage, critical-scenario,
and leak validation. The reviewable diff changes only the suite hash and its
exact run provenance. All five public contract hashes, coverage floors,
scenario results, expected skips, tool versions, and leak expectations remain
unchanged.

Machine bindings:

- matrix proof semantic digest:
  `2cd97028543ea0b3301a1cdd77f205e0629f0b7fb81358b750620ead93526a97`;
- matrix proof file SHA-256:
  `f3bb1f23fb7c15fa17a0c256c43731f91be573bdcda561b2c6601fdc4e23b8b0`;
- updated baseline semantic digest:
  `f4b22f4df0f134f192b2c987a3f1dd777f5bfab1154b00715e8d839d27f7cdcc`;
- updated baseline file SHA-256:
  `338c6283db974b796ef1b7733ebd70f889ee5f2d5a0a5c25c292d263bdb018c0`;
- reviewable diff file SHA-256:
  `61ae11595e4482e255b4264603c9d8c80dec43fac6b82c3180c457b52dc3563f`.

The deterministic update is recorded in
`run-29526068945-attempt-1.diff.json`.

## Independent-sentinel transition

The checked-in baseline was updated from the sealed five-platform candidate
matrix produced by workflow run
[`29519762171`](https://github.com/ildarbinanas-design/env-vault/actions/runs/29519762171),
attempt `1`, source `62b3d25fcbc0a960c2eba03f98d7026cd2be8421`.

The matrix proof passed schema, platform-set, exact-run, semantic-suite,
toolchain, normalized-evidence, coverage, and critical-scenario validation.
The pre-update baseline rejected exactly the intended transition:

- all five contract hashes changed because `TOKEN_TWO` now has its own
  independently generated sentinel and normalizes to `<SENTINEL_SHA256_2>`;
- all five leak registry counts changed from `125` to `130`, one additional
  private hash for functional, coverage, and three full burn-in passes;
- no coverage floor, scenario result, expected skip, tool version, or leak
  outcome changed.

Machine bindings:

- matrix proof semantic digest:
  `63a4e01a235d000d10314472f285003a783aa7972bfdeb1c2a1d5827af2734f3`;
- matrix proof file SHA-256:
  `0afc20e0a8a136300869973d70d270612e6375aba2fe903dabb3e2eab11a4432`;
- updated baseline semantic digest:
  `33b1626cabdf2173c3ea1c76ac73461c0cb1a27d72fa2f852edc66cf6836216f`;
- updated baseline file SHA-256:
  `de242cb6cda4d13b92c3cfb9c9001354da32e9e7331f8d25f224484d3a692ad6`;
- reviewable diff file SHA-256:
  `e7f84b6548ef70b6df12ca4cf2e9ce13a76fea44e813c06f6cc43f577ba0538d`.

The deterministic update is recorded in
`run-29519762171-attempt-1.diff.json`. The accepted current matrix replaces the
one-time historical migration reference; the historical comparator and
migration bytes remain checked in as immutable audit evidence but are no
longer a runtime CI dependency.
