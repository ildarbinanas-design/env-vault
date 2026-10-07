# Design

## Context

See [proposal.md](proposal.md) for the owner incident and compatibility change.
Read-only inspection at `f5012e1` confirms these extension points:

- `internal/cli/prompt_unix.go` clears `ECHO` but sets `ICANON | ISIG`,
  reads one byte at a time, and is shared by `secret set`, `export` and
  `import`. The signal registration, restoration and interruption flush
  already have PTY regression tests in `prompt_unix_test.go`.
- `readSecret` reads before constructing its store, but currently uses
  unbounded `io.ReadAll` for stdin. `secret set` then calls `Exists` and `Set`.
- `secretstore.ValueLimiter`, `ErrValueTooLarge`, `CodeSecretTooLarge` and
  `refuseOversizedValues` already express the Windows limit. Production stores
  open lazily; querying their advertised size limit does not open a backend.
- Import selects writes after applying conflict policy, then checks all of
  their values before writing. Its `skip` exemption and dry-run preflight
  remain useful. `runner.CommandRunner.Run` currently maps ordinary start
  errors to one generic `RUNTIME_ERROR` message and remediation.

The reported macOS canonical limit of 1023 bytes, Linux limit of 4095 bytes,
Linux per-string process limit of 131072 bytes and macOS aggregate limit of
1 MiB are supplied observations. This planning phase inspects the responsible
code; implementation records native reproduction and `sysctl kern.argmax`.
No owner's existing secret is needed for any check.

## Goals / Non-Goals

**Goals:** preserve every accepted input byte, keep terminal recovery intact,
bound newly stored values, and reuse the existing error/preflight architecture.
Before/after tests must establish the terminal fix independently of the new
size rejection and must expose only lengths, digests and non-secret statuses.

**Non-Goals:** changes to `prompt_other.go` (Windows and BSD), signals or
`restoreInterruptedPrompt`, storage backends, the container format or its
aggregate bounds, NUL handling, environment-name validation, child execution
semantics, exit codes, flags, dependencies, release machinery or `CHANGELOG.md`.
No automatic rewriting or deletion of existing oversized records is planned.

## Decisions

### 1. Noncanonical input with the existing signal lifecycle

For darwin/linux, copy the saved termios and apply:

```text
Lflag &^= ECHO | ICANON | IEXTEN
Lflag |= ISIG
Iflag |= ICRNL
Cc[VMIN] = 1
Cc[VTIME] = 0
```

Removing `ICANON` avoids the kernel's canonical line buffer; removing `ECHO`
keeps input hidden; removing `IEXTEN` avoids extra line-discipline editing
that conflicts with application editing. Keep `ISIG` so Ctrl+C and the
existing signal handlers retain their behavior, and `ICRNL` so the ordinary
Enter key is accepted. `VMIN=1`, `VTIME=0` waits for a byte without introducing
a timeout. Leave other flags and the established signal lifecycle unchanged.

Keep exactly one byte in each `unix.Read` call, retrying `EINTR` as today.
A buffered or chunked read could consume the second line of one paste into
the first passphrase prompt and strand the confirmation prompt. Stop at the
first line terminator without reading the next byte.

Hold the same hidden terminal session across export's first passphrase,
validation and confirmation. Native macOS tests also exposed corruption of
the queued second line when canonical mode was restored and immediately
disabled between the two reads: byte lengths matched but digests differed.
Factor the existing termios and signal lifecycle around a callback that can
read both lines; keep its notification, interruption flush and restoration
body unchanged. Restore the original state once the entire operation returns,
including validation failures. Injected test readers and Windows/BSD retain
their existing input behavior. This is an implementation refinement of the
already specified two-line paste contract, not a new passphrase policy.

| Input | Application behavior before overflow |
| --- | --- |
| LF (`0x0a`) or CR (`0x0d`) | Complete the current line; exclude the terminator |
| DEL (`0x7f`) or Backspace (`0x08`) | Remove the final complete UTF-8 code point; for an invalid tail remove one byte; empty input is unchanged |
| Ctrl+U (`0x15`) | Wipe and clear the entire current buffer |
| Ctrl+W (`0x17`) | Remove trailing whitespace, then the preceding word |
| Ctrl+D (`0x04`) | Return `io.EOF` if empty; otherwise ignore it |
| Any other byte delivered by the terminal | Append it unchanged |

For Ctrl+W, define a word as consecutive non-whitespace code points and use
Unicode whitespace (`unicode.IsSpace`); an invalid UTF-8 tail is a one-byte
non-whitespace unit. This is a small deterministic editing rule, not Unicode
normalization. Byte storage remains lossless, including invalid UTF-8 input.
Terminal-generated signals still take the existing interruption path rather
than the ordinary-byte path. Preserve existing handling of actual read EOF
and read errors; do not mistake a typed Ctrl+D in noncanonical mode for a
kernel EOF on a nonempty line.

Keep `readHiddenPassword(fd)` available to transfer prompts with no value
maximum. Add a bounded secret-input entry point sharing the same Unix editor;
its budget is 65536 bytes. A small other-platform adapter can call the existing
`readHiddenPassword` and check the returned size without modifying
`prompt_other.go` or its native editing. This keeps the 64 KiB rule specific
to newly stored values: import must still accept any nonempty passphrase, and
export's existing minimum/confirmation rules remain unchanged.

Keeping canonical mode or merely enlarging the Go buffer cannot fix the
kernel's line limit. Full raw mode would change signal and unrelated terminal
behavior. Neither alternative fits the agreed boundary.

### 2. Bounded input, overflow draining and memory ownership

For the bounded Unix prompt, accept up to the limit in the current edited
buffer. On the first byte that would exceed it, latch overflow. Do not append
that byte or any later bytes, and do not let later editing recover the line.
Continue reading one byte at a time through the next LF or CR, wipe the
retained buffer with `bundle.Wipe`, and return `secretstore.ErrValueTooLarge`
without a value. The caller maps it to `SECRET_TOO_LARGE`, exit `2`, before
calling the store factory. No part of the rejected line may reach the shell
or the next prompt. The signal path remains active while draining.
If EOF or a read failure ends draining before a terminator, wipe the buffer
and return `ErrValueTooLarge`; overflow must never become partial success.

Grow editable buffers explicitly: allocate a replacement, copy the live
bytes, wipe the old allocation with `bundle.Wipe`, then retain the new one.
Wipe removed suffixes before shortening on Backspace, Ctrl+U or Ctrl+W; wipe
owned input on errors, overflow and after the caller finishes using it. Wipe
the one-byte read scratch storage too. Do not convert input to immutable
strings for editing. Transfer confirmation keeps independently owned slices
so wiping the confirmation cannot corrupt the retained passphrase. Wiping
remains best-effort under Go's memory model.

For `--stdin`, retain the terminal refusal and read with
`io.ReadAll(io.LimitReader(stdin, limit+3))`, where `limit` is 65536. Remove
exactly one final LF or CRLF before checking length; a lone CR is retained.
The extra three bytes cover a legal two-byte suffix plus one excess byte:
even after trimming, any truncated over-limit read remains oversized.
Unlike interactive input, stdin need not be drained. Preserve and wipe the
owned allocation, including the trimmed suffix, on failure and after use.

### 3. One write limit layered over the existing backend limit

Add `secretstore.MaxValueBytes = 64 << 10`. Compute the effective write limit
as the minimum of this constant and a positive `ValueLimiter.MaxValueBytes()`;
an absent limiter or a nonpositive backend limit still gets the common cap.
Reuse `ErrValueTooLarge` and `CodeSecretTooLarge`. Update size messages and
remediation so non-Windows errors no longer claim a Windows backend; state
the applicable byte limit without exposing values.

`secret set` rejects common-limit input before store construction. For an
otherwise accepted input, inspect the lazily constructed store's optional
limit and reject any smaller-limit violation before `Exists` or `Set`, so
the production backend is not opened. Keep the backend's own defensive size
check and the existing handling of its sentinel. Preserve `--dry-run`:
validate selection and names, but do not read input or open the backend.

Extend `refuseOversizedValues` to apply the common/effective limit to all
selected import writes, not only stores advertising a limit. Preserve
identity validation and conflict-policy ordering; an entry skipped by
`--on-conflict skip` is not a write. No selected write may occur until every
selected value fits. Import preflight may read backend metadata to determine
conflicts; the no-backend-open guarantee applies to rejected `secret set`
input, not to import. Keep `import --dry-run` running the same preflight.

Do not put the cap into backend `Get`, the resolver, `check`, export or the
container parser. Existing large values can still be checked, injected where
the OS permits, or exported as authenticated ciphertext within existing
container bounds. Rejecting reads would strand existing data and make the
write policy retroactive.

### 4. Why 65536 bytes and why E2BIG still needs a diagnostic

65536 is half the reported Linux 131072-byte limit for one environment string
`NAME=value\0` (the name, equals sign and final NUL count). It leaves generous
space for ordinary variable names while avoiding a value near that boundary.
Environment names have no small fixed length bound in env-vault, so 64 KiB
does not guarantee that an arbitrary `NAME=value\0` fits. On the owner's
macOS host the reported `kern.argmax` is 1 MiB for arguments and environment
together; inherited variables, argv and many individually valid secrets can
still exceed it. The Windows Credential Manager cap remains 2560 bytes.

Keep OS enforcement authoritative. In the `cmd.Start` error path, match
`E2BIG` through error wrapping and retain `RUNTIME_ERROR`, exit `1` and the
existing `Unable to start command` message. Use remediation:
`The environment exceeds the operating system limit; reduce the size or number of secrets`.
Do not inspect or print the child environment. Other start errors and all
child stream/status/signal handling remain unchanged. An aggregate preflight
would duplicate platform rules and still be unreliable; it is out of scope.

### 5. Evidence before implementation and native validation

First extend the existing PTY helper and long-input tests without changing
production code. Build disposable values from `testutil.EphemeralValue`;
the helper's successful stdout carries only byte length and hex SHA-256.
An inequality reports only `length/digest mismatch`, never buffers or values.
Wait until echo is disabled before sending input. Run writes from a goroutine
with bounded completion, including one large write and chunked delivery for
each required length; this distinguishes the known canonical failure from a
test harness blocked in `Write`.

Save commands, platform, source/test commit, exit status, timeout/mismatch
result and run URLs in `tasks.md`, without input payloads. Record macOS timeout
and Linux length mismatch on the original code before fixing the prompt.
Linux can use an authorized manual CI dispatch of the test-only branch head;
no PR needs to be opened to collect evidence. Native post-fix Linux execution
must prove the new tests ran on `ubuntu-latest`, not merely that a cross-build
or a skipped test succeeded. Retain Windows limit tests and native CI results.

Use the test backend only with all three gates and an absolute temporary
store path. Manual macOS checks use disposable clipboard data and compare
only digests/lengths, then remove the test store and disposable clipboard
contents. The separate Keychain probe writes and verifies only
`size-probe` in service `env-vault-size-probe`, then deletes that entry with
the required `--confirm size-probe`; it never reads existing owner entries.

## Risks / Trade-offs

- Custom editing can regress terminal behavior → retain every existing signal
  and flush test; add UTF-8, invalid-tail, editing, EOF, CR and pasted
  confirmation cases, plus Ctrl+C after more than 4096 partial bytes.
- One-byte reads add syscall overhead → the maximum stored value is 64 KiB;
  preserve confirmation correctness and verify large-paste completion.
- An oversized prompt waits for Enter → make this intentional in docs, drain
  with bounded memory, and preserve interruption recovery.
- Valid old containers may no longer import → explicitly document the
  acceptance break, test refusal before any write, and preserve skip policy
  and encrypted export of existing records.
- A 64 KiB value is not an OS launch guarantee → retain legacy reads and
  diagnose actual `E2BIG`, including a Linux regression using 131072 bytes
  seeded directly into the disposable test store.
- Native interactive checks or required external tools may be unavailable →
  record the exact limitation; never mark a task complete or a platform
  verified without its acceptance evidence.

## Migration Plan

After proposal approval, execute [tasks.md](tasks.md) in order, with tests
failing on the original code before the fix. Preserve the source and results
of those tests. If implementation requires a contract change, update the
planning artifacts with `openspec-update-change` before changing code.

No data migration or backend format change is required. Existing oversized
records remain untouched; replacing one must satisfy the new write limit.
A rollback to the prior binary restores the former input behavior and import
acceptance without converting data. Release publication remains outside this
task's authority.

Synchronize all four deltas and archive the completed change in the same PR,
with verification results and completed task criteria recorded. Fix relative
repository links for the extra archive path level. Open the requested PR only
then, retain the explicitly requested title, describe the import compatibility
break in its body, and leave it unmerged for independent review and owner
handling. No implementation is authorized by this planning commit alone.
