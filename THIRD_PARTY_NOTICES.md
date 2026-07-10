# Third-Party Notices

env-vault is licensed under the MIT License. This project depends on third-party Go modules that remain under their respective licenses.

Direct runtime dependencies:

| Module | Version | License |
|---|---:|---|
| `github.com/99designs/keyring` | `v1.2.2` | MIT |
| `github.com/spf13/cobra` | `v1.10.2` | Apache-2.0 |
| `golang.org/x/term` | `v0.29.0` | BSD-3-Clause |
| `gopkg.in/yaml.v3` | `v3.0.1` | MIT/Apache-2.0 style Go YAML license |

Release CI runs `scripts/license-check.sh`, pinned to `go-licenses v2.0.1`,
before publishing binaries. The gate checks the resolved CLI dependency tree
and allows only the documented permissive license families. Update this direct
dependency table whenever the direct requirements in `go.mod` change.
