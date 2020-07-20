#!/usr/bin/env bash

set -e

[ ! -f "dist/glouton_windows_amd64/glouton.exe" -o ! -f "dist/glouton_windows_386/glouton.exe" ] && (echo "Source executables  not found. Please run goreleaser on the project prior to launching this script"; exit 1)

WORKSPACE=`pwd`/work/
COMMIT_HASH=$(git rev-parse --short HEAD)
VERSION=$(TZ=UTC date +%y.%m.%d.%H%M%S)

mkdir -p "${WORKSPACE}"
cd "${WORKSPACE}"

cp -r ../packaging/windows "${WORKSPACE}"

cd "${WORKSPACE}"/windows

sed -i -e "s/^!define PRODUCT_VERSION \"0.1\"$/!define PRODUCT_VERSION \"${VERSION}\"/" bleemeo.nsi

GOOS=windows GOARCH=386 go build -o gen_config.exe gen_config.go
makensis bleemeo.nsi
