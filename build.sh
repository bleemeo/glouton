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

GORELEASER_VERSION="v0.176.0"

echo "Building Go binary"
if [ "${ONLY_GO}" = "1" -a "${WITH_RACE}" != "1" ]; then
   docker run --rm -e HOME=/go/pkg -e CGO_ENABLED=0 \
      -v $(pwd):/src -w /src ${GO_MOUNT_CACHE} \
      --entrypoint '' \
      goreleaser/goreleaser:${GORELEASER_VERSION} sh -c "go build . && chown $USER_UID glouton"
elif [ "${ONLY_GO}" = "1" -a "${WITH_RACE}" = "1" ]; then
   docker run --rm -e HOME=/go/pkg -e CGO_ENABLED=1 \
      -v $(pwd):/src -w /src ${GO_MOUNT_CACHE} \
      --entrypoint '' \
      goreleaser/goreleaser:${GORELEASER_VERSION} sh -c "go build -ldflags='-linkmode external -extldflags=-static' -race . && chown $USER_UID glouton"
else
   docker run --rm -e HOME=/go/pkg -e CGO_ENABLED=0 \
      -v $(pwd):/src -w /src ${GO_MOUNT_CACHE} \
      -v /var/run/docker.sock:/var/run/docker.sock \
      --entrypoint '' \
      goreleaser/goreleaser:${GORELEASER_VERSION} sh -c "(go generate ./... && go test ./... && goreleaser --rm-dist --snapshot --parallelism 2); result=\$?;chown -R $USER_UID dist coverage.html coverage.out api/models_gen.go; exit \$result"

   # This isn't valid on all system. When building on Linux/ARM64 it don't work.
   # VERSION=$(dist/glouton_linux_amd64/glouton --version)
   # Use the filename instead
   filename=$(echo dist/glouton_*_linux_amd64.deb)
   VERSION=${filename#"dist/glouton_"}
   VERSION=${VERSION%"_linux_amd64.deb"}

   echo $VERSION > dist/VERSION

   ./packaging/windows/generate_installer.sh

   sed "s@image: bleemeo/bleemeo-agent:latest@image: bleemeo/bleemeo-agent:${VERSION}@" k8s.yaml > dist/k8s.yaml
fi
