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
output=${3:-${HOMEBREW_FORMULA_OUTPUT:-tap-out/$RELEASE_HOMEBREW_FORMULA_NAME.rb}}
[[ -n "$version" && -n "$asset_dir" && -n "$output" ]] || usage

release_require_version "$version"
[[ -d "$asset_dir" && ! -L "$asset_dir" ]] || release_die "asset directory not found: $asset_dir"

darwin_arm64_name=$(release_homebrew_archive_for_target darwin arm64)
darwin_amd64_name=$(release_homebrew_archive_for_target darwin amd64)
linux_arm64_name=$(release_homebrew_archive_for_target linux arm64)
linux_amd64_name=$(release_homebrew_archive_for_target linux amd64)
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
formula_version=${version#"$RELEASE_TAG_PREFIX"}
homepage=${RELEASE_HOMEBREW_HOMEPAGE_TEMPLATE//\{repository\}/$RELEASE_SOURCE_REPOSITORY}
render_download_url() {
  local asset=$1
  local rendered=${RELEASE_HOMEBREW_DOWNLOAD_TEMPLATE//\{repository\}/$RELEASE_SOURCE_REPOSITORY}
  rendered=${rendered//\{version\}/$version}
  rendered=${rendered//\{asset\}/$asset}
  [[ "$rendered" =~ ^https://github\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+/releases/download/[^/]+/[^/]+$ ]] ||
    release_die "rendered Homebrew download URL is invalid"
  printf '%s\n' "$rendered"
}
darwin_arm64_url=$(render_download_url "$darwin_arm64_name")
darwin_amd64_url=$(render_download_url "$darwin_amd64_name")
linux_arm64_url=$(render_download_url "$linux_arm64_name")
linux_amd64_url=$(render_download_url "$linux_amd64_name")
formula_class=$(awk -F- '{ for (i = 1; i <= NF; i++) printf "%s%s", toupper(substr($i,1,1)), substr($i,2) }' <<< "$RELEASE_HOMEBREW_FORMULA_NAME")
[[ "$formula_class" =~ ^[A-Z][A-Za-z0-9]*$ ]] || release_die "Homebrew formula class is invalid"

output_parent=$(dirname "$output")
mkdir -p "$output_parent"
temporary=$(mktemp "$output_parent/.${RELEASE_HOMEBREW_FORMULA_NAME}-formula.XXXXXX")
trap 'rm -f -- "$temporary"' EXIT

cat > "$temporary" <<EOF
class $formula_class < Formula
  desc "Secure environment variable vault for running commands with profiles"
  homepage "$homepage"
  version "$formula_version"
  license "MIT"

  on_macos do
    depends_on macos: :sequoia

    on_arm do
      url "$darwin_arm64_url"
      sha256 "$darwin_arm64"
    end

    on_intel do
      url "$darwin_amd64_url"
      sha256 "$darwin_amd64"
    end
  end

  on_linux do
    on_arm do
      url "$linux_arm64_url"
      sha256 "$linux_arm64"
    end

    on_intel do
      url "$linux_amd64_url"
      sha256 "$linux_amd64"
    end
  end

  def install
    bin.install "$RELEASE_PRODUCT"
    doc.install %w[README.md LICENSE THIRD_PARTY_NOTICES.md]
  end

  test do
    assert_equal "$RELEASE_TAG_PREFIX#{version}", shell_output("#{bin}/$RELEASE_PRODUCT --version").strip
  end
end
EOF

chmod 0644 "$temporary"
mv "$temporary" "$output"
trap - EXIT
printf 'generated Homebrew formula for %s\n' "$version"
