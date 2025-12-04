#!/bin/sh

set -e

GORELEASER_VERSION="v2.13.0"
USER_UID=$(id -u)

rm -fr work

case "$1" in
   "")
      ;;
   "go")
      ONLY_GO=1
      ;;
   "race")
      ONLY_GO=1
      WITH_RACE=1
      ;;
   "no-js")
      SKIP_JS=1
      ;;
   "only-js")
      ONLY_JS=1
      ;;
   "docker-fast")
      ONLY_DOCKER_FAST=1
      SKIP_JS=1
      ;;
   *)
      echo "Usage: $0 [go|race|no-js|only-js|docker-fast]"
      echo "  go: only compile Go"
      echo "  race: only compile Go with -race"
      echo "  no-js: skip building JS"
      echo "  only-js: just JS (and go generate)"
      echo "  docker-fast: build a docker from ./glouton (so you should run ./build.sh go before)"
      exit 1
esac

if [ "$2" != "" ]; then
   echo "This script doesn't support multiple options"
   exit 1
fi

if docker volume ls | grep -q glouton-buildcache; then
   GO_MOUNT_CACHE="-v glouton-buildcache:/go/pkg"
   NODE_MOUNT_CACHE="-v glouton-buildcache:/go/pkg"
fi

if [ "${ONLY_GO}" = "1" ] || [ "${ONLY_DOCKER_FAST}" = "1" ] || [ "${SKIP_JS}" = "1" ]; then
   echo "Skip cleaning workspace because only Go binary build is enabled, or Docker fast is enabled, or JS skipping is enabled"
else
   echo "Cleanup workspace"
   rm -fr webui/dist api/assets/*.css api/assets/*.js
fi

if [ "${SKIP_JS}" != "1" ] && [ "${ONLY_GO}" != "1" ]; then
   echo "Building webui"
   mkdir -p webui/node_modules
   docker run --rm -e HOME=/go/pkg/node \
      -v $(pwd):/src --tmpfs /src/webui/node_modules:exec -w /src/webui ${NODE_MOUNT_CACHE} \
      node:24 \
      sh -exc "
      wget -qO- https://get.pnpm.io/install.sh | ENV=\"\$HOME/.shrc\" SHELL=\"\$(which sh)\" sh - && \
      export PNPM_HOME=\"/go/pkg/node/.local/share/pnpm\" && \
      export PATH=\"\$PNPM_HOME:\$PATH\" && \
      mkdir -p /go/pkg/node && \
      chown node -R /go/pkg/node && \
      trap 'chown -R $USER_UID dist ../api/assets/' EXIT && \
      pnpm install --frozen-lockfile --ignore-scripts && \
      pnpm run deploy
      "
fi

if [ -z "${GLOUTON_VERSION}" ]; then
   GLOUTON_VERSION=$(date -u +%y.%m.%d.%H%M%S)
fi

if [ -z "${GLOUTON_BUILDX_OPTION}" ]; then
   GLOUTON_BUILDX_OPTION="-t glouton:latest --load"
fi

export GLOUTON_VERSION

COMMIT=$(git rev-parse --short HEAD || echo "unknown")

if [ "${ONLY_JS}" = "1" ]; then
   exit 0
fi

echo "Building Go binary"
if [ "${ONLY_DOCKER_FAST}" = "1" ]; then
   echo "Building a Docker image using ./glouton"
   sed 's@COPY dist/glouton_linux_.*/glouton /glouton.@COPY glouton /glouton.@' Dockerfile | docker buildx build ${GLOUTON_BUILDX_OPTION} -f - .
elif [ "${ONLY_GO}" = "1" ] && [ "${WITH_RACE}" != "1" ]; then
   docker run --rm -e HOME=/go/pkg -e CGO_ENABLED=0 \
      -v $(pwd):/src -w /src ${GO_MOUNT_CACHE} \
      --entrypoint '' \
      goreleaser/goreleaser:${GORELEASER_VERSION} \
      tini -g -- sh -exc "
      mkdir -p /go/pkg
      trap 'chown $USER_UID glouton' EXIT
      git config --global --add safe.directory /src
      go build -ldflags='-X main.version=${GLOUTON_VERSION} -X main.commit=${COMMIT}' .
      "
elif [ "${ONLY_GO}" = "1" ] && [ "${WITH_RACE}" = "1" ]; then
   docker run --rm -e HOME=/go/pkg -e CGO_ENABLED=1 \
      -v $(pwd):/src -w /src ${GO_MOUNT_CACHE} \
      --entrypoint '' \
      goreleaser/goreleaser:${GORELEASER_VERSION} \
      tini -g -- sh -exc "
      mkdir -p /go/pkg
      trap 'chown $USER_UID glouton' EXIT
      git config --global --add safe.directory /src
      go build -ldflags='-X main.version=${GLOUTON_VERSION} -X main.commit=${COMMIT} -linkmode external -extldflags=-static' -race .
      "
else
   docker run --rm -e HOME=/go/pkg -e CGO_ENABLED=0 \
      -v $(pwd):/src -w /src ${GO_MOUNT_CACHE} \
      -v /var/run/docker.sock:/var/run/docker.sock \
      --entrypoint '' \
      -e GLOUTON_VERSION \
      -e GORELEASER_PREVIOUS_TAG=0.1.0 \
      -e GORELEASER_CURRENT_TAG=0.1.1 \
      goreleaser/goreleaser:${GORELEASER_VERSION} \
      tini -g -- sh -exc "
      mkdir -p /go/pkg
      git config --global --add safe.directory /src
      trap 'chown -R $USER_UID dist' EXIT
      goreleaser check
      go generate ./...
      go test ./...
      goreleaser --clean --snapshot --parallelism 2 --timeout 45m
      "

   echo $GLOUTON_VERSION > dist/VERSION

   # Build Docker image using buildx. We use docker buildx instead of goreleaser because
   # goreleaser use "docker manifest" which require to push image to a registry. This means we ends with 4 tags:
   # 3 for each of the 3 supported architectures and 1 for the multi-architecture image.
   # Using buildx only generate 1 tag on the Docker Hub.
   docker buildx build ${GLOUTON_BUILDX_OPTION} .

   sed "s@image: bleemeo/bleemeo-agent:latest@image: bleemeo/bleemeo-agent:${GLOUTON_VERSION}@" k8s.yaml > dist/k8s.yaml

   ./packaging/windows/generate_installer.sh
fi
