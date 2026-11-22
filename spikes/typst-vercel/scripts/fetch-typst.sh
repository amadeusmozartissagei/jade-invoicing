#!/bin/sh
# PROTOTYPE: fetch the pinned Typst release at build time and check its digest.
set -eu
VERSION=v0.15.1
ASSET=typst-x86_64-unknown-linux-musl.tar.xz
SHA256=a6d077d0a95eed5a2eba715b2dae06be954f624ccbf85758a03f389ded33118c
mkdir -p cmd/api/assets/bin
curl -fsSL -o "/tmp/$ASSET" "https://github.com/typst/typst/releases/download/$VERSION/$ASSET"
echo "$SHA256  /tmp/$ASSET" | sha256sum -c -
tar -xJf "/tmp/$ASSET" -C /tmp
cp /tmp/typst-x86_64-unknown-linux-musl/typst cmd/api/assets/bin/typst
chmod +x cmd/api/assets/bin/typst
ls -la cmd/api/assets/bin/typst
