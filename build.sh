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

if [ -e .build-cache ]; then
   mkdir -p .build-cache/node

   GO_MOUNT_CACHE="-v $(pwd)/.build-cache:/go/pkg"
   NODE_MOUNT_CACHE="-v $(pwd)/.build-cache/node:/tmp/home"
fi

if [ "${SKIP_JS}" != "1" -a "${ONLY_GO}" != "1" ]; then
   docker run --rm -u $USER_UID -e HOME=/tmp/home \
      -v $(pwd):/src -w /src/webui ${NODE_MOUNT_CACHE} \
      node:lts \
      sh -c 'rm -fr node_modules && npm install && npm run deploy'
fi

GORELEASER_VERSION="v0.176.0"

if [ "${ONLY_GO}" = "1" -a "${WITH_RACE}" != "1" ]; then
   docker run --rm -u $USER_UID:`getent group docker|cut -d: -f 3` -e HOME=/go/pkg -e CGO_ENABLED=0 \
      -v $(pwd):/src -w /src ${GO_MOUNT_CACHE} \
      -v /var/run/docker.sock:/var/run/docker.sock \
      --entrypoint '' \
      goreleaser/goreleaser:${GORELEASER_VERSION} sh -c 'go build .'
elif [ "${ONLY_GO}" = "1" -a "${WITH_RACE}" = "1" ]; then
   docker run --rm -u $USER_UID:`getent group docker|cut -d: -f 3` -e HOME=/go/pkg -e CGO_ENABLED=1 \
      -v $(pwd):/src -w /src ${GO_MOUNT_CACHE} \
      -v /var/run/docker.sock:/var/run/docker.sock \
      --entrypoint '' \
      goreleaser/goreleaser:${GORELEASER_VERSION} sh -c 'go build -ldflags="-linkmode external -extldflags=-static" -race .'
else
   docker run --rm -u $USER_UID:`getent group docker|cut -d: -f 3` -e HOME=/go/pkg -e CGO_ENABLED=0 \
      -v $(pwd):/src -w /src ${GO_MOUNT_CACHE} \
      -v /var/run/docker.sock:/var/run/docker.sock \
      --entrypoint '' \
      goreleaser/goreleaser:${GORELEASER_VERSION} sh -c 'go generate ./... && go test ./... && goreleaser --rm-dist --snapshot --parallelism 2'

   ./packaging/windows/generate_installer.sh
fi

