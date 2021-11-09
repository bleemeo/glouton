#!/bin/sh

set -e

USER_UID=$(id -u)

LINTER_VERSION=v1.43.0


if docker volume ls | grep -q glouton-buildcache; then
   GO_MOUNT_CACHE="-v glouton-buildcache:/go/pkg"
fi

docker run --rm -v "$(pwd)":/app ${GO_MOUNT_CACHE} -e HOME=/go/pkg \
   -w /app golangci/golangci-lint:${LINTER_VERSION} \
   sh -c 'go test ./... --coverprofile=coverage.out -count 1 && go tool cover -html=coverage.out -o coverage.html && go test -race ./... -count 1'

docker run --rm -v "$(pwd)":/app ${GO_MOUNT_CACHE} -e HOME=/go/pkg \
   -e GOOS=linux -e GOARCH=amd64 -w /app golangci/golangci-lint:${LINTER_VERSION} \
   golangci-lint run

docker run --rm -v "$(pwd)":/app ${GO_MOUNT_CACHE} -e HOME=/go/pkg \
   -e GOOS=windows -e GOARCH=amd64 -w /app golangci/golangci-lint:${LINTER_VERSION} \
   golangci-lint run

echo "Success"
