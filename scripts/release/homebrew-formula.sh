#!/usr/bin/env bash
# Generate the Homebrew formula for a published release (ADR 0011).
#
# usage: homebrew-formula.sh vX.Y.Z ASSET_DIR OUTPUT
#
# ASSET_DIR holds the four macOS and Linux archives of the release with their
# .sha256 sidecars, and every sidecar must match its archive. The formula
# keeps its `version` line, which the tap's pins check reads, and its test
# checks the version and a profile lifecycle without opening a secret store.
set -euo pipefail
export LC_ALL=C

repository=ildarbinanas-design/env-vault

die() {
  printf 'homebrew-formula: %s\n' "$1" >&2
  exit 1
}

[[ $# -eq 3 ]] || {
  printf 'usage: %s vX.Y.Z ASSET_DIR OUTPUT\n' "$(basename "$0")" >&2
  exit 2
}
tag=$1
assets=$2
output=$3
[[ "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || die "invalid tag $tag"
[[ -d "$assets" ]] || die "asset directory $assets does not exist"

declare -A sums=()
for target in darwin-arm64 darwin-amd64 linux-arm64 linux-amd64; do
  archive="env-vault-$target.tar.gz"
  [[ -f "$assets/$archive" && ! -L "$assets/$archive" ]] || die "missing $archive"
  [[ -f "$assets/$archive.sha256" && ! -L "$assets/$archive.sha256" ]] || die "missing $archive.sha256"
  sidecar=$(<"$assets/$archive.sha256")
  [[ "$sidecar" =~ ^([0-9a-f]{64})\ \ (.+)$ && "${BASH_REMATCH[2]}" == "$archive" ]] ||
    die "$archive.sha256 is not a single '<sha256>  $archive' line"
  sum=${BASH_REMATCH[1]}
  actual=$(sha256sum -- "$assets/$archive")
  [[ "${actual%% *}" == "$sum" ]] || die "$archive does not match $archive.sha256"
  sums[$target]=$sum
done

url() {
  printf 'https://github.com/%s/releases/download/%s/env-vault-%s.tar.gz' "$repository" "$tag" "$1"
}

temporary=$(mktemp "$(dirname "$output")/.env-vault-formula.XXXXXX")
trap 'rm -f -- "$temporary"' EXIT
cat > "$temporary" <<EOF
class EnvVault < Formula
  desc "Secure environment variable vault for running commands with profiles"
  homepage "https://github.com/$repository"
  version "${tag#v}"
  license "MIT"

  on_macos do
    depends_on macos: :sequoia

    on_arm do
      url "$(url darwin-arm64)"
      sha256 "${sums[darwin-arm64]}"
    end

    on_intel do
      url "$(url darwin-amd64)"
      sha256 "${sums[darwin-amd64]}"
    end
  end

  on_linux do
    on_arm do
      url "$(url linux-arm64)"
      sha256 "${sums[linux-arm64]}"
    end

    on_intel do
      url "$(url linux-amd64)"
      sha256 "${sums[linux-amd64]}"
    end
  end

  def install
    bin.install "env-vault"
    doc.install %w[README.md LICENSE THIRD_PARTY_NOTICES.md]
  end

  test do
    assert_match "v#{version}", shell_output("#{bin}/env-vault --version")

    # Profile mappings are metadata; this never opens a secret store.
    config = testpath/"config.yaml"
    system bin/"env-vault", "--config", config, "profile", "create", "brew-test"
    system bin/"env-vault", "--config", config, "profile", "add", "brew-test", "brew-token:BREW_TEST_TOKEN"
    show = "#{bin}/env-vault --config #{config} --json profile show brew-test"
    data = JSON.parse(shell_output(show)).fetch("data")
    assert_equal "brew-test", data.fetch("profile")
    assert_equal ["BREW_TEST_TOKEN"], data.fetch("secrets").map { |mapping| mapping.fetch("env") }
    system bin/"env-vault", "--config", config, "profile", "remove", "brew-test", "BREW_TEST_TOKEN"
    assert_empty JSON.parse(shell_output(show)).fetch("data").fetch("secrets")
  end
end
EOF
chmod 0644 "$temporary"
mv -- "$temporary" "$output"
trap - EXIT
