#!/usr/bin/env bash

set -ex

[ ! -f "dist/glouton_windows_amd64_v1/glouton.exe" -o ! -f "dist/glouton_windows_386/glouton.exe" ] && (echo "Source executables  not found. Please run goreleaser on the project prior to launching this script"; exit 1)

VERSION=$(cat dist/VERSION)

mkdir -p work

cp -r packaging/windows/installer/* work
cp dist/glouton_windows_amd64_v1/glouton.exe work/assets

sed -i "s/Version=\"1.2.3.4\"/Version=\"${VERSION}\"/" work/product.wxs

docker run --rm -v "$(pwd)/work:/wix" dactiv/wix candle -ext WixUtilExtension.dll -ext WixUIExtension -dAssetsPath=/wix/assets/ -dExePath=/wix/assets/ -arch x86 -out 'obj\' *.wxs
docker run --rm -v "$(pwd)/work:/wix" dactiv/wix light -sval -ext WixUtilExtension.dll -ext WixUIExtension -out glouton.msi obj/*.wixobj

cp work/glouton.msi "dist/glouton_${VERSION}.msi"

rm -rf work
