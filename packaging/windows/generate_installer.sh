#!/usr/bin/env bash

set -ex

if [ ! -f "dist/glouton_windows_amd64_v1/glouton.exe" -o ! -f "dist/glouton_windows_386/glouton.exe" ]
then
    echo "Source executables  not found. Please run goreleaser on the project prior to launching this script"
    exit 1
fi

VERSION=$(cat dist/VERSION)

WORK_PATH="$(pwd)/work"
INSTALLER_PATH="${WORK_PATH}/installer"
CHOCOLATEY_PATH="${WORK_PATH}/chocolatey"

mkdir -p "${INSTALLER_PATH}/obj"
cp -r packaging/windows/* ${WORK_PATH}

# Allow the wine user in the wix container to create files.
chmod 700 "${WORK_PATH}"
chmod 777 -R "${INSTALLER_PATH}/obj"

# Create MSI package.
cp dist/glouton_windows_amd64_v1/glouton.exe ${INSTALLER_PATH}/assets

sed -i -e "s/Version=\"1.2.3.4\"/Version=\"${VERSION}\"/" ${INSTALLER_PATH}/product.wxs

docker run --rm -v "${INSTALLER_PATH}:/wix" dactiv/wix candle -ext WixUtilExtension.dll -ext WixUIExtension -dAssetsPath=/wix/assets/ -dExePath=/wix/assets/ -arch x86 -out 'obj\' *.wxs
docker run --rm -v "${INSTALLER_PATH}:/wix" dactiv/wix light -sval -ext WixUtilExtension.dll -ext WixUIExtension -out obj/glouton.msi obj/*.wixobj

cp ${INSTALLER_PATH}/obj/glouton.msi "dist/glouton_${VERSION}.msi"

# Create chocolatey package.
cp ${INSTALLER_PATH}/obj/glouton.msi ${CHOCOLATEY_PATH}/tools

sed -i -e "s/<version>1.2.3.4<\/version>/<version>${VERSION}<\/version>/" ${CHOCOLATEY_PATH}/glouton.nuspec

docker run --rm -v "${CHOCOLATEY_PATH}:/root/glouton" chocolatey/choco sh -c "cd glouton && choco pack"

cp ${CHOCOLATEY_PATH}/*.nupkg dist

rm -rf ${WORK_PATH}
