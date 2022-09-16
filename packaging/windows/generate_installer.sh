#!/usr/bin/env bash

set -ex

[ ! -f "dist/glouton_windows_amd64_v1/glouton.exe" -o ! -f "dist/glouton_windows_386/glouton.exe" ] && (echo "Source executables  not found. Please run goreleaser on the project prior to launching this script"; exit 1)

VERSION=$(cat dist/VERSION)

mkdir -p work
cp -r packaging/windows/* work

INSTALLER_PATH="work/installer"
CHOCOLATEY_PATH="work/chocolatey"

# Create MSI package.
cp dist/glouton_windows_amd64_v1/glouton.exe ${INSTALLER_PATH}/assets

sed -i -e "s/Version=\"1.2.3.4\"/Version=\"${VERSION}\"/" ${INSTALLER_PATH}/product.wxs

docker run --rm -v "$(pwd)/${INSTALLER_PATH}:/wix" dactiv/wix candle -ext WixUtilExtension.dll -ext WixUIExtension -dAssetsPath=/wix/assets/ -dExePath=/wix/assets/ -arch x86 -out 'obj\' *.wxs
docker run --rm -v "$(pwd)/${INSTALLER_PATH}:/wix" dactiv/wix light -sval -ext WixUtilExtension.dll -ext WixUIExtension -out glouton.msi obj/*.wixobj

cp ${INSTALLER_PATH}/glouton.msi "dist/glouton_${VERSION}.msi"

# Create chocolatey package.
cp ${INSTALLER_PATH}/glouton.msi ${CHOCOLATEY_PATH}/tools

sed -i -e "s/<version>1.2.3.4<\/version>/<version>${VERSION}<\/version>/" ${CHOCOLATEY_PATH}/glouton.nuspec

docker run --rm -v "$(pwd)/${CHOCOLATEY_PATH}:/root/glouton" chocolatey/choco sh -c "cd glouton && choco pack"

cp ${CHOCOLATEY_PATH}/*.nupkg dist

rm -rf work
