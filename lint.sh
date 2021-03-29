#!/bin/sh

set -e

USER_UID=$(id -u)

LINTER_VERSION=v1.27


case "$1" in
   "")
      ;;
   "coverage")
      GEN_COVERFILE=1
      ;;
    "html")
      GEN_COVERFILE=1
      GEN_HTML=1
      ;;
   *)
      echo "Usage: $0 [coverage|html]"
      echo "  coverage: Generates a coverage report as well as a coverage.out file"
      echo "  html: Generates a coverage report file then generates an html report"
      exit 1
esac


if [ -e .build-cache ]; then
   GO_MOUNT_CACHE="-v $(pwd)/.build-cache:/go/pkg"
fi

if [ "${GEN_COVERFILE}" != "1" ]; then
    docker run --rm -v "$(pwd)":/app -u "$USER_UID" ${GO_MOUNT_CACHE} -e HOME=/go/pkg \
        -w /app golangci/golangci-lint:${LINTER_VERSION} \
        sh -c 'go test ./... && go test -race ./...'
else
    docker run --rm -v "$(pwd)":/app -u "$USER_UID" ${GO_MOUNT_CACHE} -e HOME=/go/pkg \
        -w /app golangci/golangci-lint:${LINTER_VERSION} \
        sh -c 'go test ./... --coverprofile=coverage.out && go test -race ./...'
fi
if [ "${GEN_HTML}" = "1" ]; then
    docker run --rm -v "$(pwd)":/app -u "$USER_UID" ${GO_MOUNT_CACHE} -e HOME=/go/pkg \
        -w /app golangci/golangci-lint:${LINTER_VERSION} \
        sh -c 'go tool cover -html=coverage.out -o coverage.html'
fi


docker run --rm -v "$(pwd)":/app -u "$USER_UID" ${GO_MOUNT_CACHE} -e HOME=/go/pkg \
    -e GOOS=linux -e GOARCH=amd64 -w /app golangci/golangci-lint:${LINTER_VERSION} \
    golangci-lint run

docker run --rm -v "$(pwd)":/app -u "$USER_UID" ${GO_MOUNT_CACHE} -e HOME=/go/pkg \
    -e GOOS=windows -e GOARCH=amd64 -w /app golangci/golangci-lint:${LINTER_VERSION} \
    golangci-lint run

echo "Success"
