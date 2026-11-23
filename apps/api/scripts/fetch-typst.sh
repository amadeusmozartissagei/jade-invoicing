#!/bin/sh
# Downloads the pinned Typst release, checks it against its SHA-256, and puts
# the binary in DIR (default: .bin). Run from apps/api. ADR 0017, D9.
#
#   sh scripts/fetch-typst.sh                                  # local, CI
#   sh scripts/fetch-typst.sh internal/invoicepdf/embedded     # Vercel build
#
# Typst publishes no checksum file; the digests are GitHub's per-asset SHA-256
# for release v0.15.1 (docs/research/2026-11-22-pdf-storage-and-typst.md, B1).
set -eu
VERSION=v0.15.1
DIR=${1:-.bin}

case "$(uname -s)" in
Linux)
	ASSET=typst-x86_64-unknown-linux-musl.tar.xz
	SHA256=a6d077d0a95eed5a2eba715b2dae06be954f624ccbf85758a03f389ded33118c
	BIN=typst
	;;
MINGW* | MSYS* | CYGWIN*)
	ASSET=typst-x86_64-pc-windows-msvc.zip
	SHA256=19ce3551153c2fe7ee9fa2f95208310c8f4d3209fedb699e0333faf8913f6736
	BIN=typst.exe
	;;
Darwin)
	ASSET=typst-x86_64-apple-darwin.tar.xz
	SHA256=7f9fdd9584866245de9a79e0add8f9236fae6f40a8a45e2c4771ccc14db4e0fa
	BIN=typst
	;;
*)
	echo "fetch-typst: unsupported OS $(uname -s)" >&2
	exit 1
	;;
esac

if [ -x "$DIR/$BIN" ] && [ "$("$DIR/$BIN" --version | cut -d' ' -f2)" = "${VERSION#v}" ]; then
	echo "fetch-typst: $DIR/$BIN is already $VERSION"
	exit 0
fi

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
curl -fsSL -o "$TMP/$ASSET" "https://github.com/typst/typst/releases/download/$VERSION/$ASSET"
echo "$SHA256  $TMP/$ASSET" | sha256sum -c -
case "$ASSET" in
*.zip) unzip -q "$TMP/$ASSET" -d "$TMP" ;;
*) tar -xJf "$TMP/$ASSET" -C "$TMP" ;;
esac
mkdir -p "$DIR"
# Remove first: Git Bash's cp would write through to an existing typst.exe.
rm -f "$DIR/$BIN"
cp "$TMP/${ASSET%.tar.xz}/$BIN" "$DIR/$BIN" 2>/dev/null || cp "$TMP/${ASSET%.zip}/$BIN" "$DIR/$BIN"
chmod +x "$DIR/$BIN"
"$DIR/$BIN" --version
