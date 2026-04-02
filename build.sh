#!/bin/sh

set -eu

GORELEASER_VERSION="v2.14.0"
USER_UID=$(id -u)

rm -fr work

USE_SINGLE_TARGET=0
SKIP_DOCKER=0
SKIP_JS=0
SKIP_GO_BUILD=0
SKIP_GO_TEST=1
SKIP_TEST=0
SKIP_MSI=0
ENABLE_RACE=0

while [ $# -gt 0 ]; do
   case "$1" in
      "single-target")
         USE_SINGLE_TARGET=1
         ;;
      "race")
         USE_SINGLE_TARGET=1
         ENABLE_RACE=1
         ;;
      "no-js"|"skip-js")
         SKIP_JS=1
         ;;
      "skip-test")
         SKIP_GO_TEST=1
         ;;
      "skip-build")
         SKIP_GO_BUILD=1
         ;;
      "skip-docker")
         SKIP_DOCKER=1
         ;;
      "skip-windows-installer")
         SKIP_MSI=1
         ;;
      "only-js")
         SKIP_GO_BUILD=1
         SKIP_DOCKER=1
         SKIP_MSI=1
         ;;
      "go")
         USE_SINGLE_TARGET=1
         SKIP_JS=1
         SKIP_GO_TEST=1
         SKIP_DOCKER=1
         ;;
      "docker-fast")
         USE_SINGLE_TARGET=1
         SKIP_GO_TEST=1
         SKIP_JS=1
         SKIP_MSI=1
         ;;
      *)
         echo "Usage: $0 [single-target|race|skip-js|skip-test|skip-build|skip-docker|skip-windows-installer]"
         echo "  single-target: only build one target, specified by GOOS & GOARM (default to current os/arch)"
         echo "  race: imply single-target and compile with -race"
         echo "  skip-js: skip building JS (note: JS must be built at least once after a new checkout)"
         echo "  skip-test: skip running Go test"
         echo "  skip-build: skip building Go binary"
         echo "  skip-docker: skip building Docker image"
         echo "  skip-windows-installer: skip building Windows installer"
         echo ""
         echo "Some other option exists as alias or compatibility"
         echo "  go: only compile Go (alias of skip-js, skip-test, skip-docker, single-target)"
         echo "  no-js: skip building JS (alias of skip-js)"
         echo "  only-js: just JS (alias of skip-build, skip-docker, skip-windows-installer)"
         echo "  docker-fast: build Docker image (alias of skip-js, single-target, skip-windows-installer)"
         exit 1
   esac
   shift 1
done

if docker volume ls | grep -q glouton-buildcache; then
   GO_MOUNT_CACHE="-v glouton-buildcache:/go/pkg"
   NODE_MOUNT_CACHE="-v glouton-buildcache:/go/pkg"
else
   GO_MOUNT_CACHE=""
   NODE_MOUNT_CACHE=""
fi

if [ -z "${GLOUTON_VERSION:-}" ]; then
   GLOUTON_VERSION=$(date -u +%y.%m.%d.%H%M%S)
fi

if [ -z "${GLOUTON_BUILDX_OPTION:-}" ]; then
   GLOUTON_BUILDX_OPTION="-t glouton:latest --load"
fi

export GLOUTON_VERSION

COMMIT=$(git rev-parse --short HEAD || echo "unknown")

if [ $USE_SINGLE_TARGET = "1" ]; then
   if [ ${GOOS:-linux} != "linux" ]; then
      echo "(i) skipping Docker image build since Linux binary isn't built"
      SKIP_DOCKER=1
   fi

   if [ ${GOOS:-linux} != "windows" ]; then
      echo "(i) skipping Windows installer since Windows binary isn't built"
      SKIP_MSI=1
   fi
fi

if [ "${SKIP_JS}" = "1" ]; then
   echo "--- JS build is skipped, don't cleanup webui files"
else
   echo "--- Cleanup webui files"
   rm -fr webui/dist api/assets/*.css api/assets/*.js
fi

if [ "${SKIP_JS}" != "1" ]; then
   echo "--- Building webui"
   mkdir -p webui/node_modules
   docker build webui --target jsdist --output api/assets/
fi

if [ "${SKIP_GO_BUILD}" != "1" -o "${SKIP_GO_TEST}" != "1" ]; then
   docker run --rm -e HOME=/go/pkg -e CGO_ENABLED=0 \
      -v $(pwd):/src -w /src ${GO_MOUNT_CACHE} \
      -v /var/run/docker.sock:/var/run/docker.sock \
      --entrypoint '' \
      -e GLOUTON_VERSION \
      -e GORELEASER_PREVIOUS_TAG=0.1.0 \
      -e GORELEASER_CURRENT_TAG=0.1.1 \
      -e SKIP_GO_BUILD=$SKIP_GO_BUILD \
      -e SKIP_GO_TEST=$SKIP_GO_TEST \
      -e USE_SINGLE_TARGET=$USE_SINGLE_TARGET \
      -e ENABLE_RACE=$ENABLE_RACE \
      -e GOOS -e GOARCH \
      goreleaser/goreleaser:${GORELEASER_VERSION} \
      tini -g -- sh -ec "
      mkdir -p /go/pkg
      git config --global --add safe.directory /src
      trap 'chown -R $USER_UID dist' EXIT
      sh build_inner.sh
      "

   # Only create dist/VERSION when a Glouton binary is created
   if [ "${SKIP_GO_BUILD}" != "1" ]; then
      echo $GLOUTON_VERSION > dist/VERSION

      sed "s@image: bleemeo/bleemeo-agent:latest@image: bleemeo/bleemeo-agent:${GLOUTON_VERSION}@" k8s.yaml > dist/k8s.yaml
   fi
fi

if [ "${SKIP_DOCKER}" != "1" ]; then
   echo "--- Building Docker image"

   # Build Docker image using buildx. We use docker buildx instead of goreleaser because
   # goreleaser use "docker manifest" which require to push image to a registry. This means we ends with 4 tags:
   # 3 for each of the 3 supported architectures and 1 for the multi-architecture image.
   # Using buildx only generate 1 tag on the Docker Hub.
   docker buildx build ${GLOUTON_BUILDX_OPTION} .
fi

if [ "${SKIP_MSI}" != "1" ]; then
   echo "--- Building Windows installer"
   ./packaging/windows/generate_installer.sh
fi
