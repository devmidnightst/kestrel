#!/bin/sh
# Install a prebuilt kestrel compiler from GitHub releases.
#
#   curl -fsSL https://raw.githubusercontent.com/devmidnightst/kestrel/main/install.sh | sh
#
# Environment:
#   KESTREL_VERSION      release tag to install (default: the latest release)
#   KESTREL_INSTALL_DIR  where to put the binary (default: /usr/local/bin)
#
# The archive is checked against the release's SHA256SUMS before anything is
# installed. kestrel still needs a C compiler (cc, gcc or clang) to build
# programs.
set -eu

repo="devmidnightst/kestrel"
version="${KESTREL_VERSION:-}"
dir="${KESTREL_INSTALL_DIR:-/usr/local/bin}"

fail() {
	echo "install.sh: $*" >&2
	exit 1
}

case "$(uname -s)" in
Linux) os=linux ;;
Darwin) os=darwin ;;
*) fail "unsupported operating system $(uname -s) (linux and macos only)" ;;
esac
case "$(uname -m)" in
x86_64 | amd64) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
*) fail "unsupported architecture $(uname -m) (amd64 and arm64 only)" ;;
esac

if command -v curl >/dev/null 2>&1; then
	fetch() { curl -fsSL "$1" -o "$2"; }
elif command -v wget >/dev/null 2>&1; then
	fetch() { wget -q "$1" -O "$2"; }
else
	fail "curl or wget is required"
fi

if command -v sha256sum >/dev/null 2>&1; then
	sha256() { sha256sum "$1" | cut -d ' ' -f 1; }
elif command -v shasum >/dev/null 2>&1; then
	sha256() { shasum -a 256 "$1" | cut -d ' ' -f 1; }
else
	fail "sha256sum or shasum is required"
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT INT TERM

if [ -z "$version" ]; then
	fetch "https://api.github.com/repos/$repo/releases/latest" "$tmp/latest.json" ||
		fail "could not query the latest release"
	version="$(sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' "$tmp/latest.json" | head -n 1)"
	[ -n "$version" ] || fail "could not find the latest release tag"
fi

name="kestrel-$version-$os-$arch.tar.gz"
base="https://github.com/$repo/releases/download/$version"
echo "downloading $name"
fetch "$base/$name" "$tmp/$name" || fail "could not download $base/$name"
fetch "$base/SHA256SUMS" "$tmp/SHA256SUMS" || fail "could not download $base/SHA256SUMS"

want="$(awk -v n="$name" '$2 == n { print $1 }' "$tmp/SHA256SUMS")"
got="$(sha256 "$tmp/$name")"
[ -n "$want" ] || fail "$name is not listed in SHA256SUMS"
[ "$want" = "$got" ] || fail "checksum mismatch for $name (expected $want, got $got)"

tar -xzf "$tmp/$name" -C "$tmp" kestrel
if [ ! -d "$dir" ] || [ ! -w "$dir" ]; then
	fail "$dir is not writable; rerun with sudo or set KESTREL_INSTALL_DIR"
fi
install -m 0755 "$tmp/kestrel" "$dir/kestrel"
echo "installed kestrel $version to $dir/kestrel"
if ! command -v cc >/dev/null 2>&1 && ! command -v gcc >/dev/null 2>&1 && ! command -v clang >/dev/null 2>&1; then
	echo "note: no c compiler found; install gcc or clang to build kestrel programs" >&2
fi
