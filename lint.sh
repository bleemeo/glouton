#!/bin/sh

set -e

UID=$(id -u)

LINTER_VERSION=v1.27

if [ -e .build-cache ]; then
   GO_MOUNT_CACHE="-v $(pwd)/.build-cache:/go/pkg"
fi

docker run --rm -v $(pwd):/app -u $UID ${GO_MOUNT_CACHE} -e HOME=/go/pkg \
    -w /app golangci/golangci-lint:${LINTER_VERSION} \
    sh -c 'go test ./... && go test -race ./...'

docker run --rm -v $(pwd):/app -u $UID ${GO_MOUNT_CACHE} -e HOME=/go/pkg \
    -e GOOS=linux -e GOARCH=amd64 -w /app golangci/golangci-lint:${LINTER_VERSION} \
    golangci-lint run

docker run --rm -v $(pwd):/app -u $UID ${GO_MOUNT_CACHE} -e HOME=/go/pkg \
    -e GOOS=windows -e GOARCH=amd64 -w /app golangci/golangci-lint:${LINTER_VERSION} \
    golangci-lint run

echo "Success"
