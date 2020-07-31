#!/usr/bin/env bash

set -e

[ ! -f "dist/glouton_windows_amd64/glouton.exe" -o ! -f "dist/glouton_windows_386/glouton.exe" ] && (echo "Source executables  not found. Please run goreleaser on the project prior to launching this script"; exit 1)

VERSION=$(dist/glouton_linux_amd64/glouton --version)
COMMIT_HASH=$(git rev-parse --short HEAD)

mkdir -p work

cp -r packaging/windows work

sed -i -e "s/^!define PRODUCT_VERSION \"0.1\"$/!define PRODUCT_VERSION \"${VERSION}\"/" "work/windows/bleemeo.nsi"

docker run --rm -v "$(pwd):/work" nsisbuilder makensis work/windows/bleemeo.nsi

cp "work/windows/glouton-installer.exe" "dist/glouton_${VERSION}_windows_installer.exe"
