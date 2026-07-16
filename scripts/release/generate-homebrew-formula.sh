#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=scripts/release/lib.sh
source "$SCRIPT_DIR/lib.sh"

usage() {
  printf 'usage: %s [vMAJOR.MINOR.PATCH] [ASSET_DIR] [OUTPUT_FORMULA]\n' "$(basename "$0")" >&2
  exit 2
}

[[ $# -le 3 ]] || usage
version=${1:-${VERSION:-}}
asset_dir=${2:-${RELEASE_ASSET_DIR:-dist}}
output=${3:-${HOMEBREW_FORMULA_OUTPUT:-tap-out/env-vault.rb}}
[[ -n "$version" && -n "$asset_dir" && -n "$output" ]] || usage

release_require_version "$version"
[[ -d "$asset_dir" && ! -L "$asset_dir" ]] || release_die "asset directory not found: $asset_dir"

darwin_arm64_name=$(release_archive_for_platform darwin-arm64)
darwin_amd64_name=$(release_archive_for_platform darwin-amd64)
linux_arm64_name=$(release_archive_for_platform linux-arm64)
linux_amd64_name=$(release_archive_for_platform linux-amd64)
darwin_arm64_archive="$asset_dir/$darwin_arm64_name"
darwin_amd64_archive="$asset_dir/$darwin_amd64_name"
linux_arm64_archive="$asset_dir/$linux_arm64_name"
linux_amd64_archive="$asset_dir/$linux_amd64_name"

release_verify_checksum_pair "$darwin_arm64_archive" "$darwin_arm64_archive.sha256"
release_verify_checksum_pair "$darwin_amd64_archive" "$darwin_amd64_archive.sha256"
release_verify_checksum_pair "$linux_arm64_archive" "$linux_arm64_archive.sha256"
release_verify_checksum_pair "$linux_amd64_archive" "$linux_amd64_archive.sha256"

darwin_arm64=$(release_sha256_file "$darwin_arm64_archive")
darwin_amd64=$(release_sha256_file "$darwin_amd64_archive")
linux_arm64=$(release_sha256_file "$linux_arm64_archive")
linux_amd64=$(release_sha256_file "$linux_amd64_archive")
formula_version=${version#v}

output_parent=$(dirname "$output")
mkdir -p "$output_parent"
temporary=$(mktemp "$output_parent/.env-vault-formula.XXXXXX")
trap 'rm -f -- "$temporary"' EXIT

cat > "$temporary" <<EOF
class EnvVault < Formula
  desc "Secure environment variable vault for running commands with profiles"
  homepage "https://github.com/ildarbinanas-design/env-vault"
  version "$formula_version"
  license "MIT"

  on_macos do
    depends_on macos: :sequoia

    on_arm do
      url "https://github.com/ildarbinanas-design/env-vault/releases/download/$version/$darwin_arm64_name"
      sha256 "$darwin_arm64"
    end

    on_intel do
      url "https://github.com/ildarbinanas-design/env-vault/releases/download/$version/$darwin_amd64_name"
      sha256 "$darwin_amd64"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/ildarbinanas-design/env-vault/releases/download/$version/$linux_arm64_name"
      sha256 "$linux_arm64"
    end

    on_intel do
      url "https://github.com/ildarbinanas-design/env-vault/releases/download/$version/$linux_amd64_name"
      sha256 "$linux_amd64"
    end
  end

  def install
    bin.install "env-vault"
    doc.install %w[README.md LICENSE THIRD_PARTY_NOTICES.md]
  end

  test do
    assert_equal "v#{version}", shell_output("#{bin}/env-vault --version").strip
  end
end
EOF

chmod 0644 "$temporary"
mv "$temporary" "$output"
trap - EXIT
printf 'generated Homebrew formula for %s\n' "$version"
