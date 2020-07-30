#!/usr/bin/env bash

set -e

[ ! -f "dist/glouton_windows_amd64/glouton.exe" -o ! -f "dist/glouton_windows_386/glouton.exe" ] && (echo "Source executables  not found. Please run goreleaser on the project prior to launching this script"; exit 1)

WORKSPACE=`pwd`/work/
VERSION=$(dist/glouton_linux_amd64/glouton --version)

OUTDIR="`pwd`/dist/"

rm -rf "$WORKSPACE"
mkdir "$WORKSPACE"
cd "$WORKSPACE"

cp -r ../packaging/windows "$WORKSPACE"

cd "$WORKSPACE/windows"

sed -i -e "s/^!define PRODUCT_VERSION \"0.1\"$/!define PRODUCT_VERSION \"${VERSION}\"/" bleemeo.nsi

GOOS=windows GOARCH=386 go build -o gen_config.exe gen_config.go
makensis bleemeo.nsi

cp "$WORKSPACE/windows/glouton-installer.exe" "$OUTDIR/windows_installer.exe"

rm -rf "$WORKSPACE"
