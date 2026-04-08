#!/bin/sh
# This file is called by build.sh inside the goreleaser Docker image

set -eu

echo "-- Checking goreleaser.yml file"
goreleaser check

if [ "${SKIP_GO_TEST}" != "1" ]; then
   echo "--- Runnning Go test..."
   go test ./...
fi

echo "--- Building Go binary..."
if [ "${SKIP_GO_BUILD}" != "1" ]; then

   BUILD_ID=glouton
   if [ "$TARGET_TO_BUILD" = "race" ]; then
      BUILD_ID=glouton-race
   fi
   
   EXTRA_OPTIONS=""
   if [ "$TARGET_TO_BUILD" != "release" ]; then
      EXTRA_OPTIONS=--single-target
   fi

   goreleaser build --clean --snapshot --parallelism 2 --timeout 45m $EXTRA_OPTIONS --id $BUILD_ID

   if [ "$TARGET_TO_BUILD" = "race" ]; then
      # Because Docker image building assume dist/glouton_linux*/glouton not dist/glouton-race_linux*/glouton,
      # rename folders to drop the -race
      for dirname in dist/glouton-race*; do
         mv "$dirname" "dist/glouton${dirname#dist/glouton-race}"
      done
   fi
fi

if [ "$TARGET_TO_BUILD" != "release" -a ${GOOS:-linux} = "linux" ]; then
   # Ensure binary for other arch exists, because Docker image building assume glouton binaries for all arch exists.
   createEmptyGloutonBinary() {
      local dirname
      local target="$1"
      if [ ! -e "$target" ]; then
         dirname="$(dirname "$target")"
         if [ ! -d "$dirname" ]; then
            mkdir "$dirname"
         fi

         touch "$target"
      fi
   }

   createEmptyGloutonBinary dist/glouton_linux_amd64_v1/glouton
   createEmptyGloutonBinary dist/glouton_linux_arm64_v8.0/glouton
   createEmptyGloutonBinary dist/glouton_linux_arm_6/glouton
fi
