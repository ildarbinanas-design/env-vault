#!/usr/bin/env bash
# Package the five release archives deterministically (ADR 0011).
#
# usage: package-archives.sh BINARIES_DIR OUTPUT_DIR
#
# BINARIES_DIR holds one directory per target, named after the target, with the
# built binary inside: env-vault, or env-vault.exe for Windows. README.md,
# LICENSE and THIRD_PARTY_NOTICES.md come from the current directory, which
# must be the repository root. SOURCE_DATE_EPOCH, the release commit time,
# fixes every timestamp. Zip stores times in two-second steps, so zip entries
# round an odd time down by one second. Entries are sorted, owners are 0:0,
# modes are fixed, and gzip stores no name or time, so the same inputs always
# give the same archive bytes. The archive layout matches the releases before
# ADR 0011: one top-level directory named env-vault-<target>.
set -euo pipefail
export LC_ALL=C TZ=UTC
umask 022

targets=(linux-amd64 linux-arm64 darwin-amd64 darwin-arm64 windows-amd64)
documents=(README.md LICENSE THIRD_PARTY_NOTICES.md)

die() {
  printf 'package-archives: %s\n' "$1" >&2
  exit 1
}

[[ $# -eq 2 ]] || {
  printf 'usage: %s BINARIES_DIR OUTPUT_DIR\n' "$(basename "$0")" >&2
  exit 2
}
binaries=$1
output=$2
[[ "${SOURCE_DATE_EPOCH:-}" =~ ^[0-9]+$ ]] || die "SOURCE_DATE_EPOCH must be a Unix timestamp"
[[ -d "$binaries" ]] || die "binaries directory $binaries does not exist"
[[ ! -e "$output" && ! -L "$output" ]] || die "refusing to reuse $output"
for document in "${documents[@]}"; do
  [[ -f "$document" && ! -L "$document" ]] || die "missing $document; run from the repository root"
done
tar --version 2>/dev/null | grep -q 'GNU tar' || die "GNU tar is required"
command -v python3 >/dev/null 2>&1 || die "python3 is required"
command -v sha256sum >/dev/null 2>&1 || die "sha256sum is required"

stage=$(mktemp -d)
trap 'rm -rf -- "$stage"' EXIT
mkdir -p -- "$output"

for target in "${targets[@]}"; do
  binary=env-vault
  [[ "$target" == windows-* ]] && binary=env-vault.exe
  source_binary="$binaries/$target/$binary"
  [[ -f "$source_binary" && ! -L "$source_binary" ]] || die "missing $source_binary"
  name="env-vault-$target"
  mkdir -- "$stage/$name"
  install -m 0755 -- "$source_binary" "$stage/$name/$binary"
  install -m 0644 -- "${documents[@]}" "$stage/$name/"

  if [[ "$target" == windows-* ]]; then
    archive="$name.zip"
    python3 - "$stage" "$name" "$output/$archive" "$SOURCE_DATE_EPOCH" <<'PY'
import os
import sys
import time
import zipfile

stage, name, archive, epoch = sys.argv[1], sys.argv[2], sys.argv[3], int(sys.argv[4])
stamp = time.gmtime(epoch)[:6]
with zipfile.ZipFile(archive, "w") as bundle:
    for entry in sorted(os.listdir(os.path.join(stage, name))):
        info = zipfile.ZipInfo(f"{name}/{entry}", date_time=stamp)
        info.create_system = 3
        mode = 0o755 if entry.endswith(".exe") else 0o644
        info.external_attr = (0o100000 | mode) << 16
        with open(os.path.join(stage, name, entry), "rb") as source:
            bundle.writestr(info, source.read(), compress_type=zipfile.ZIP_DEFLATED, compresslevel=9)
PY
  else
    archive="$name.tar.gz"
    tar --sort=name --format=gnu --owner=0 --group=0 --numeric-owner \
      --mtime="@$SOURCE_DATE_EPOCH" -C "$stage" -cf - "$name" |
      gzip -n -9 > "$output/$archive"
  fi
  (cd "$output" && sha256sum "$archive" > "$archive.sha256")
done
