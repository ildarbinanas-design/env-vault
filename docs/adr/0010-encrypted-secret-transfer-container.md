# ADR 0010: Encrypted Secret Transfer Container

## Status

Accepted

## Date

2026-08-14

## Context

Until now a secret value existed in exactly two places: the OS keychain, and
the environment of a child process started by `exec`. Both are managed by the
operating system and neither outlives the machine.

That leaves no way to move to a second machine. Every value has to be
re-entered through `secret set`, one hidden prompt at a time, and the operator
has to remember what the values were.

`backlog.md` carried a P1 item for this, scoped as "Profile import/export
**without values**". That scoping solved the wrong half of the problem. Profile
mappings are already portable: `.env-vault.yaml` contains only
`secret-name -> ENV_NAME` pairs, no values, and is meant to be committed to the
repository it belongs to. What cannot follow the operator is the keychain
contents.

`docs/project-charter.md` lists "No command that returns secret values" as a
current non-goal, and `AGENTS.md` forbids a `secret get` command. An export
that carries values has to be reconciled with both.

This ADR was authorized by the owner on 2026-08-14, which is also the explicit
"does this serve PersonalOS" decision required by ADR 0008 for a P1 item.

## Decision

Add `env-vault export --out <path>` and `env-vault import <path>`, moving
stored secret **values** through a single authenticated encrypted file.

The container carries values and nothing else. Neither command reads or writes
a config file. Profiles were deliberately excluded after the first
implementation included them: duplicating a file that is already committed to
git bought nothing and widened what a leaked container would disclose.

### Why this is not `secret get`

The non-goal exists so that no command hands a caller a readable secret value.
`export` writes AES-256-GCM ciphertext under a key derived from a passphrase
the operator types at a hidden prompt. No command prints a value, no value
reaches a command-line argument, and no value reaches a log or the JSON
envelope. The `secret get` prohibition stands unchanged.

The non-goal in `project-charter.md` is amended to say values are never
returned **in readable form**, and to name this ADR as the authority for the
encrypted-container exception.

### What actually changes in the threat model

Two properties are genuinely lost, and they are the reason this needs a
recorded decision rather than a quiet feature commit:

- **Strength drops from the operating system to a passphrase.** A keychain
  entry is protected by the login session, platform key storage, and OS-level
  rate limiting. A container file is offline-attackable with no rate limit at
  all. Its security is exactly the strength of the passphrase and the cost of
  the key-derivation function.
- **There is no revocation.** Deleting a secret from the keychain destroys it.
  A container that has already reached a backup, a cloud-sync folder, a git
  commit, or a chat message survives any later rotation, and env-vault cannot
  know it exists.

What does **not** change: an attacker who already controls an unlocked session
gains nothing new. `exec` with an arbitrary child command already places values
in that child's environment (`docs/design.md`, Exec Flow), so bulk extraction
was always available to code running as the user.

ADR 0001 is unaffected. A transfer container is not a storage backend: nothing
reads secrets from it at run time, `exec` cannot resolve against it, and the
production backend allowlist is untouched.

### Scope of an export

Export covers `secretstore.DefaultService` plus any service named with
`--with-services`, which accepts a comma-separated list and may also be
repeated.

The flag is deliberately not called `--service`. On `secret set`, `--service`
*replaces* the default service; on `export` the default is always included and
the named services are *added* to it. Reusing one flag name for the opposite
relationship would be a trap, so the additive meaning is carried in the name.

Export cannot do better than an explicit list. `Store.List` is service-scoped,
and the underlying keychains offer no way to enumerate the services an
application has used. A secret stored with `secret set --service <name>` is
therefore silently absent from a container unless that service is named again
at export time. This is documented in `docs/security.md` rather than worked
around, because the alternative — guessing service names — would be worse than
an honest gap.

### Format

A JSON document. The header carries the key-derivation and cipher parameters in
the clear because they are needed before a passphrase can become a key. The
header carries **no secret names** — those are inside the ciphertext, so a
container discloses nothing about its contents to someone holding only the
file.

```json
{
  "schema": "env-vault.bundle.v1",
  "version": 1,
  "created_at": "2026-08-14T10:00:00Z",
  "tool_version": "v0.1.0",
  "kdf": {
    "algorithm": "argon2id",
    "salt": "<base64, 16 bytes>",
    "time": 3,
    "memory_kib": 65536,
    "parallelism": 4,
    "key_length": 32
  },
  "cipher": { "algorithm": "aes-256-gcm", "nonce": "<base64, 12 bytes>" },
  "payload": "<base64 ciphertext with tag>"
}
```

The plaintext is `{"secrets":[{"service":…,"name":…,"value":"<base64>"}]}`,
unique by `(service, name)`.

The header is passed to AES-GCM as additional authenticated data. Note what
this does and does not buy, because it is easy to overstate: the cost
parameters are **already** bound to the ciphertext implicitly, since they
determine the key — an attacker who lowers `time` or `memory_kib` in a stolen
container only derives a different, wrong key, and gains nothing for an offline
attack. What the AAD actually protects are the header fields that do not feed
key derivation, `created_at` and `tool_version`, which would otherwise be
rewritable without detection. It also keeps the format honest if a future
revision adds a header field that does matter.

The protection that does carry weight against a hostile container is the
**bounds check on the declared parameters**, applied before any key derivation:
`memory_kib` in [8192, 1048576], `time` in [1, 10], `parallelism` in [1, 8],
`key_length` exactly 32, salt exactly 16 bytes, nonce exactly 12 bytes,
ciphertext at most 16 MiB, whole file at most 24 MiB. Without it a container
declaring `memory_kib: 4194304` is a 4 GiB allocation that lands before the
passphrase is ever checked.

A fresh salt and nonce are drawn per export, so the key/nonce pair is never
reused and two exports are unrelated ciphertexts.

### Key derivation

Argon2id from `golang.org/x/crypto`, with the second parameter set recommended
by RFC 9106: 64 MiB, three passes, four lanes. This is the first cryptographic
dependency in the project.

PBKDF2 was the alternative and is available in the standard library from Go
1.24, which would have kept the dependency count at zero. It was rejected
because the container's only defense is resistance to offline guessing, and
PBKDF2 is cheap to accelerate on GPUs in a way Argon2id's memory-hardness is
not. Paying one dependency for the property the whole format rests on is the
right trade.

The parameters live in the header, so the cost can be raised later without
invalidating existing containers, and the `kdf.algorithm` field leaves room for
a second function without a schema break.

### Passphrase handling

The passphrase is read from a hidden terminal prompt only. There is no
`--passphrase` flag, no passphrase environment variable, and no passphrase
file, mirroring the existing rule that a secret value never appears in argv.
Export prompts twice and compares, because a typo would produce a container
nobody can open. A minimum of 12 characters is enforced on export.

One exception exists for testability: when the complete insecure test-backend
gate is already active (`ENV_VAULT_BACKEND=test` plus
`ENV_VAULT_ALLOW_INSECURE_TEST_BACKEND=1` plus a temp-directory
`ENV_VAULT_TEST_STORE`), the passphrase may come from stdin. Under that gate
every secret already lives in a throwaway store, so this discloses nothing the
store does not. It exists so the black-box E2E suite, which runs the binary
without a terminal, can still perform a real round trip and scan the resulting
container for leaked values. `AGENTS.md` already permits behavior behind an
explicit environment gate that cannot be enabled by accident.

### Conflict handling on import

`--on-conflict fail` (default), `skip`, or `overwrite`, applied per secret.
Under `fail` the import stops before writing anything.

## Options Considered

### Metadata-only export (the original backlog scoping)

Pros:

- Violates no existing rule and needs no ADR.
- Cannot leak a value even if the file is published.

Cons:

- Solves the half of the problem that was never broken. Mappings already travel
  with the repository; values do not.

Rejected.

### Profile-scoped container carrying mappings and values

Pros:

- One file restores a project's config and credentials together.
- Smaller blast radius per file than a whole-vault export.

Cons:

- Duplicates `.env-vault.yaml`, which is already portable and committed.
- Puts profile and secret names into a file whose only defense is a passphrase.
- Needs `--as`, a profile conflict policy, and config mutation on import.
- Does not answer "move me to a new laptop" without one file per profile and
  one passphrase entry per file.

Implemented first, then removed. The extra machinery bought nothing that git
did not already provide.

### Value-only container over the secret store

Pros:

- Matches the one thing that is genuinely not portable.
- No config reading or writing, no `--as`, no profile conflict policy.
- One command and one passphrase move a whole machine.

Cons:

- Cannot enumerate custom keychain services; they must be named explicitly.
- Creates a long-lived, offline-attackable artifact with no revocation path.

Selected.

### PBKDF2 instead of Argon2id

Pros:

- In the Go standard library from 1.24; zero new dependencies, no change to
  the pinned license gate.

Cons:

- Cheaply parallelized on GPUs, which is precisely the attack the container's
  security rests on.

Rejected. See "Key derivation" above.

## Consequences

- `AGENTS.md` gains `export` and `import` in the allowed command surface, and
  its `secret get` rule is clarified rather than weakened.
- `docs/security.md` documents the container's limitations: passphrase-bound
  strength, no revocation, the custom-service gap, keep containers out of git
  and cloud sync, and best-effort memory wiping only.
- `golang.org/x/crypto` is now a direct dependency and must clear the pinned
  `go-licenses` matrix and `THIRD_PARTY_NOTICES.md`.
- All cryptography is confined to `internal/bundle`, which depends on no other
  env-vault package and is therefore fully unit-testable without a terminal or
  a keychain.
- Rotating a secret does not invalidate containers already exported. Operators
  who rotate must treat old containers as still-valid copies and destroy them.
