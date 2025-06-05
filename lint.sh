#!/bin/sh

set -e

LINTER_VERSION=v2.1.6

USER_UID=$(id -u)

case "$1" in
   "")
      ;;
   "coverage")
      COVERAGE=1
      ;;
   "lint")
      LINT=1
      ;;
   "shell")
      OPEN_SHELL=1
      ;;
   *)
      echo "Usage: $0 [coverage|lint|shell]"
      echo "  coverage: run test coverage"
      echo "  lint: run linter only, skip tests"
      echo "  shell: open a shell inside linter container"
      exit 1
esac

if docker volume ls | grep -q glouton-buildcache; then
   GO_MOUNT_CACHE="-v glouton-buildcache:/go/pkg"
fi

if [ "${OPEN_SHELL}" = "1" ]; then
   docker run --rm -ti -v "$(pwd)":/app ${GO_MOUNT_CACHE} -e HOME=/go/pkg \
      -w /app golangci/golangci-lint:${LINTER_VERSION} \
      bash

   exit
fi

if [ "${COVERAGE}" = "1" ]; then
   docker run --rm -v "$(pwd)":/app ${GO_MOUNT_CACHE} -e HOME=/go/pkg \
      -w /app golangci/golangci-lint:${LINTER_VERSION} \
      sh -exc "
      go test ./... --coverprofile=coverage.out
      go tool cover -html=coverage.out -o coverage.html
      chown $USER_UID coverage.out coverage.html
      "

   exit
fi

if [ "${LINT}" != "1" ]; then
   docker run --rm -v "$(pwd)":/app ${GO_MOUNT_CACHE} -e HOME=/go/pkg \
      -w /app golangci/golangci-lint:${LINTER_VERSION} \
      sh -exc "
      go test ./...
      go test -race ./... -short
      "
fi

echo "Start lint Linux"

docker run --rm -v "$(pwd)":/app ${GO_MOUNT_CACHE} -e HOME=/go/pkg \
   -e GOOS=linux -e GOARCH=amd64 --tmpfs /app/webui/node_modules:exec -w /app golangci/golangci-lint:${LINTER_VERSION} \
   bash -ec "
   git config --global --add safe.directory /app
   golangci-lint run
   "

echo "Start lint FreeBSD"

docker run --rm -v "$(pwd)":/app ${GO_MOUNT_CACHE} -e HOME=/go/pkg \
   -e GOOS=freebsd -e GOARCH=amd64 --tmpfs /app/webui/node_modules:exec -w /app golangci/golangci-lint:${LINTER_VERSION} \
   bash -ec "
   git config --global --add safe.directory /app
   golangci-lint run --build-tags noexec,nomeminfo,nozfs,nonetdev,nonetisr
   "

echo "Start lint Windows"

docker run --rm -v "$(pwd)":/app ${GO_MOUNT_CACHE} -e HOME=/go/pkg \
   -e GOOS=windows -e GOARCH=amd64 --tmpfs /app/webui/node_modules:exec -w /app golangci/golangci-lint:${LINTER_VERSION} \
   bash -ec "
   git config --global --add safe.directory /app
   golangci-lint run
   "

echo "Success"
