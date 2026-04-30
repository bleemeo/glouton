#!/bin/sh
# This file is called by build.sh inside the goreleaser Docker image

set -eu

do_test_and_build() {
   echo "-- Checking goreleaser.yml file"
   goreleaser check "${GORELEASER_CONFIG}"

   if [ "${SKIP_GO_TEST}" != "1" ]; then
      echo "--- Runnning Go test..."
      go test ./...
   fi

   if [ "${SKIP_GO_BUILD}" != "1" ]; then
      echo "--- Building Go binary..."

      GORELEASER_SKIP=""
      if [ "$TARGET_TO_BUILD" != "release" ] && [ "${GOOS}" != "linux" ]; then
         GORELEASER_SKIP="--skip=nfpm"
      fi

      goreleaser release --config "${GORELEASER_CONFIG}" --clean --snapshot --parallelism 2 --timeout 45m ${GORELEASER_SKIP}
   fi

   if [ "$TARGET_TO_BUILD" != "release" ] && [ "${GOOS}" = "linux" ]; then
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
}

create_single_target_config() {
   TARGET="${GOOS}_${GOARCH}"
   if [ "$GOARCH" = "arm" ]; then
      TARGET="${GOOS}_arm_6"
   fi

   TEMPLATE_CONFIG=".goreleaser.yml"
   if [ "$TARGET_TO_BUILD" = "single-target-race" ]; then
      TEMPLATE_CONFIG=".goreleaser-race.yml"
   elif [ "$TARGET_TO_BUILD" = "single-target-debug" ] || [ "$TARGET_TO_BUILD" = "single-target-debug-no-optimize" ]; then
      TEMPLATE_CONFIG=".goreleaser-dbg.yml"
   fi

   # Removes all targets from .goreleaser.yml and re-add only $TARGET.
   # This result in "--single-target" (option of `goreleaser build`) but working
   # on `gorelease release`.
   awk -v target="$TARGET" '
      /^[[:space:]]+targets:/ {
          match($0, /^[[:space:]]+/)
          indent = substr($0, 1, RLENGTH)
          print indent "targets:"
          print indent "  - " target
          in_targets = 1
          next
      }
      in_targets && /^[[:space:]]+-/ { next }
      { in_targets = 0; print }
  ' ${TEMPLATE_CONFIG} > work/goreleaser-single-target.yml

   if [ "$TARGET_TO_BUILD" = "single-target-debug-no-optimize" ]; then
      sed -i "s/# gcflags: \[/gcflags: \[/" work/goreleaser-single-target.yml
   fi
}

if [ "$TARGET_TO_BUILD" != "release" ]; then
   # For non release build, we need to alter goreleaser.yml file, because we want
   # to do "goreleaser release --single-target", but it's not supported.
   # So we take input .goreleaser.yml and remove all targets but the one we want.
   GORELEASER_CONFIG="work/goreleaser-single-target.yml"
   GOOS="${GOOS:-$(go env GOOS)}"
   GOARCH="${GOARCH:-$(go env GOARCH)}"
   create_single_target_config
else
   GORELEASER_CONFIG=".goreleaser.yml"
fi

if [ "${TARGET_TO_BUILD}" = "interactive-shell" ]; then
   echo "--- Opening interactive shell in build container"
   # Reset TARGET_TO_BUILD to single-target-debug, so you could easily run a build.
   echo " * If you need to do a build (which will ensure dependencies are fetched), you can run"
   echo "sh build_inner.sh"
   exec bash
fi

do_test_and_build
