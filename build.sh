#!/bin/sh

set -e

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
   *)
      echo "Usage: $0 [go|race|no-js]"
      echo "  go: only compile Go"
      echo "  race: only compile Go with -race"
      echo "  no-js: skip building JS"
      exit 1
esac

if docker volume ls | grep -q glouton-buildcache; then
   GO_MOUNT_CACHE="-v glouton-buildcache:/go/pkg"
   NODE_MOUNT_CACHE="-v glouton-buildcache:/go/pkg"
fi

if [ "${ONLY_GO}" = "1" ]; then
   echo "Skip cleaning workspace because only Go binary build is enabled"
else
   echo "Cleanup workspace"
   rm -fr webui/dist webui/node_modules api/static/assets/css/ api/static/assets/js/ api/api-bindata.go api/api-packr.go api/packrd/
fi

if [ "${SKIP_JS}" != "1" -a "${ONLY_GO}" != "1" ]; then
   echo "Building webui"
   mkdir -p api/static/assets/css/ api/static/assets/js/ webui/node_modules
   docker run --rm -e HOME=/go/pkg/node \
      -v $(pwd):/src --tmpfs /src/webui/node_modules:exec -w /src/webui ${NODE_MOUNT_CACHE} \
      node:lts \
      sh -c "(npm install && npm run deploy); result=\$?; chown -R $USER_UID dist ../api/static/assets/js/ ../api/static/assets/css/; exit \$result"
fi

GORELEASER_VERSION="v1.6.3"

if [ -z "${GLOUTON_VERSION}" ]; then
   GLOUTON_VERSION=$(date -u +%y.%m.%d.%H%M%S)
fi

if [ -z "${GLOUTON_BUILX_OPTION}" ]; then
   GLOUTON_BUILX_OPTION="-t glouton:latest --load"
fi

export GLOUTON_VERSION

echo "Building Go binary"
if [ "${ONLY_GO}" = "1" -a "${WITH_RACE}" != "1" ]; then
   docker run --rm -e HOME=/go/pkg -e CGO_ENABLED=0 \
      -v $(pwd):/src -w /src ${GO_MOUNT_CACHE} \
      --entrypoint '' \
      goreleaser/goreleaser:${GORELEASER_VERSION} sh -c "go build -ldflags='-X main.version=${GLOUTON_VERSION}' . && chown $USER_UID glouton"
elif [ "${ONLY_GO}" = "1" -a "${WITH_RACE}" = "1" ]; then
   docker run --rm -e HOME=/go/pkg -e CGO_ENABLED=1 \
      -v $(pwd):/src -w /src ${GO_MOUNT_CACHE} \
      --entrypoint '' \
      goreleaser/goreleaser:${GORELEASER_VERSION} sh -c "go build -ldflags='-X main.version=${GLOUTON_VERSION} -linkmode external -extldflags=-static' -race . && chown $USER_UID glouton"
else
   docker run --rm -e HOME=/go/pkg -e CGO_ENABLED=0 \
      -v $(pwd):/src -w /src ${GO_MOUNT_CACHE} \
      -v /var/run/docker.sock:/var/run/docker.sock \
      --entrypoint '' \
      -e GLOUTON_VERSION \
      -e GORELEASER_PREVIOUS_TAG=0.1.0 \
      -e GORELEASER_CURRENT_TAG=0.1.1 \
      goreleaser/goreleaser:${GORELEASER_VERSION} sh -c "(goreleaser check && go generate ./... && go test ./... && goreleaser --rm-dist --snapshot --parallelism 2); result=\$?;chown -R $USER_UID dist coverage.html coverage.out api/models_gen.go; exit \$result"

   echo $GLOUTON_VERSION > dist/VERSION

   ./packaging/windows/generate_installer.sh

   # Build Docker image using buildx. We use docker buildx instead of goreleaser because
   # goreleaser use "docker manifest" which require to push image to a registry. This means we ends with 4 tags:
   # 3 for each of the 3 supported architectures and 1 for the multi-architecture image.
   # Using buildx only generate 1 tag on the Docker Hub.
   docker buildx build ${GLOUTON_BUILX_OPTION} .

   sed "s@image: bleemeo/bleemeo-agent:latest@image: bleemeo/bleemeo-agent:${GLOUTON_VERSION}@" k8s.yaml > dist/k8s.yaml
fi
