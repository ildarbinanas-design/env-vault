# Durable E2E baseline update

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
