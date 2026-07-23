#!/bin/sh
# Emits the Homebrew formula for a released version to stdout, reading the
# artifact digests from dist/. Run `make dist VERSION=vX.Y.Z` first.
set -eu

VERSION=${1:?usage: brew-formula.sh vX.Y.Z}
DIST_VERSION=${VERSION#v}
CHECKSUMS="dist/oocla_${DIST_VERSION}_checksums.txt"
BASE="https://github.com/kazufusa/oocla/releases/download/${VERSION}"

sha() {
	name="oocla_${DIST_VERSION}_$1.tar.gz"
	out=$(awk -v f="$name" '$2 == f { print $1 }' "$CHECKSUMS")
	if [ -z "$out" ]; then
		echo "brew-formula.sh: $name is missing from $CHECKSUMS" >&2
		exit 1
	fi
	echo "$out"
}

cat <<EOF
class Oocla < Formula
  desc "Ollama- and OpenAI-compatible API server backed by the claude CLI"
  homepage "https://github.com/kazufusa/oocla"
  license "MIT"
  version "${DIST_VERSION}"

  on_macos do
    on_arm do
      url "${BASE}/oocla_${DIST_VERSION}_darwin_arm64.tar.gz"
      sha256 "$(sha darwin_arm64)"
    end
    on_intel do
      url "${BASE}/oocla_${DIST_VERSION}_darwin_amd64.tar.gz"
      sha256 "$(sha darwin_amd64)"
    end
  end

  on_linux do
    on_arm do
      url "${BASE}/oocla_${DIST_VERSION}_linux_arm64.tar.gz"
      sha256 "$(sha linux_arm64)"
    end
    on_intel do
      url "${BASE}/oocla_${DIST_VERSION}_linux_amd64.tar.gz"
      sha256 "$(sha linux_amd64)"
    end
  end

  def install
    bin.install "oocla"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/oocla version")
  end
end
EOF
