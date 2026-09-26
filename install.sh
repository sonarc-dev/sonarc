#!/bin/sh
# Install sonarc: download the latest release for this machine, check it
# against the release's checksums, and put it in ~/.local/bin.
#
#   curl -fsSL https://sonarc-dev.github.io/sonarc/install.sh | sh
#
# No root needed. Environment:
#   SONARC_INSTALL_DIR  where to install (default: $HOME/.local/bin)
#   SONARC_VERSION      a release tag such as v0.1.0 (default: the latest)
#   SONARC_BASE_URL     where the release files are, in place of the
#                       GitHub release (for testing)

set -eu

repo="https://github.com/sonarc-dev/sonarc"
dir="${SONARC_INSTALL_DIR:-$HOME/.local/bin}"

fail() {
	echo "sonarc install: $*" >&2
	exit 1
}

case "$(uname -s)" in
Linux) os=linux ;;
Darwin) os=darwin ;;
*) fail "no prebuilt binary for $(uname -s); build from source: $repo" ;;
esac

case "$(uname -m)" in
x86_64 | amd64) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
*) fail "no prebuilt binary for $(uname -m); build from source: $repo" ;;
esac

if [ -n "${SONARC_BASE_URL:-}" ]; then
	base="$SONARC_BASE_URL"
elif [ -n "${SONARC_VERSION:-}" ]; then
	base="$repo/releases/download/$SONARC_VERSION"
else
	base="$repo/releases/latest/download"
fi

if command -v curl >/dev/null 2>&1; then
	fetch() { curl -fsSL -o "$2" "$1"; }
elif command -v wget >/dev/null 2>&1; then
	fetch() { wget -q -O "$2" "$1"; }
else
	fail "needs curl or wget"
fi

if command -v sha256sum >/dev/null 2>&1; then
	sha256() { sha256sum "$1" | cut -d' ' -f1; }
elif command -v shasum >/dev/null 2>&1; then
	sha256() { shasum -a 256 "$1" | cut -d' ' -f1; }
else
	fail "needs sha256sum or shasum to verify the download"
fi

name="sonarc-$os-$arch"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

echo "downloading $name"
fetch "$base/$name" "$tmp/$name" || fail "could not download $base/$name"
fetch "$base/checksums.txt" "$tmp/checksums.txt" || fail "could not download $base/checksums.txt"

want="$(awk -v n="$name" '$2 == n { print $1 }' "$tmp/checksums.txt")"
[ -n "$want" ] || fail "checksums.txt has no entry for $name"
got="$(sha256 "$tmp/$name")"
[ "$got" = "$want" ] || fail "checksum mismatch for $name (expected $want, got $got)"

mkdir -p "$dir"
chmod 755 "$tmp/$name"
# Moving into place, rather than copying over, leaves a running sonarc alone.
mv "$tmp/$name" "$dir/sonarc.new"
mv "$dir/sonarc.new" "$dir/sonarc"

echo "installed $("$dir/sonarc" -version) to $dir/sonarc"
case ":$PATH:" in
*":$dir:"*) echo "run: sonarc -doctor" ;;
*) echo "$dir is not on your PATH; add it, e.g.: echo 'export PATH=\"$dir:\$PATH\"' >> ~/.profile" ;;
esac
