#!/usr/bin/env bash
# Package a release from downloaded binary artifacts (ADR 0011).
#
# usage: package-release.sh DOWNLOADS_DIR ARCHIVES_DIR [SUBJECTS_DIR]
#
# DOWNLOADS_DIR holds one binary-<target> directory per target, as
# actions/download-artifact writes them. The archives are packaged twice at
# the commit time of the current checkout, and both runs must give the same
# bytes. SUBJECTS_DIR, when given, receives each binary as env-vault-<target>
# (env-vault-<target>.exe for Windows), so that every attestation subject has
# its own name. With GITHUB_STEP_SUMMARY set, the summary records the tool
# versions the archive bytes depend on and the checksums.
set -euo pipefail
export LC_ALL=C

targets=(linux-amd64 linux-arm64 darwin-amd64 darwin-arm64 windows-amd64)

die() {
  printf 'package-release: %s\n' "$1" >&2
  exit 1
}

[[ $# -eq 2 || $# -eq 3 ]] || {
  printf 'usage: %s DOWNLOADS_DIR ARCHIVES_DIR [SUBJECTS_DIR]\n' "$(basename "$0")" >&2
  exit 2
}
downloads=$1
archives=$2
subjects=${3:-}
script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
[[ -d "$downloads" ]] || die "downloads directory $downloads does not exist"
[[ -z "$subjects" || ( ! -e "$subjects" && ! -L "$subjects" ) ]] || die "refusing to reuse $subjects"

work=$(mktemp -d)
trap 'rm -rf -- "$work"' EXIT
mkdir -- "$work/binaries"
for target in "${targets[@]}"; do
  binary=env-vault
  [[ "$target" == windows-* ]] && binary=env-vault.exe
  source="$downloads/binary-$target/$binary"
  [[ -f "$source" && ! -L "$source" ]] || die "missing $source"
  mkdir -- "$work/binaries/$target"
  cp -- "$source" "$work/binaries/$target/$binary"
done

SOURCE_DATE_EPOCH=$(git log -1 --format=%ct HEAD)
export SOURCE_DATE_EPOCH
"$script_dir/package-archives.sh" "$work/binaries" "$archives"
# A second run from the same inputs must give the same bytes.
"$script_dir/package-archives.sh" "$work/binaries" "$work/again"
diff -r -- "$archives" "$work/again" >&2 || die "two packaging runs gave different bytes"
(cd "$archives" && sha256sum --check --quiet ./*.sha256) || die "a checksum sidecar does not match its archive"

if [[ -n "$subjects" ]]; then
  mkdir -p -- "$subjects"
  for target in "${targets[@]}"; do
    if [[ "$target" == windows-* ]]; then
      cp -- "$work/binaries/$target/env-vault.exe" "$subjects/env-vault-$target.exe"
    else
      cp -- "$work/binaries/$target/env-vault" "$subjects/env-vault-$target"
    fi
  done
fi

if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]]; then
  {
    echo '```'
    tar --version | sed -n 1p
    gzip --version | sed -n 1p
    python3 -c 'import sys, zlib; print("Python", sys.version.split()[0], "zlib", zlib.ZLIB_RUNTIME_VERSION)'
    echo
    cat -- "$archives"/*.sha256
    echo '```'
  } >> "$GITHUB_STEP_SUMMARY"
fi
