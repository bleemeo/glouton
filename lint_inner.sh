#!/bin/sh

set -eu

echo "-- Download modules"
go mod download


if [ "${COVERAGE}" = "1" ]; then
   echo "-- Run tests coverage"
   go test ./... --coverprofile=coverage.out --covermode=count
   go tool cover -html=coverage.out -o coverage.html
fi

if [ "${LINT}" != "1" ]; then
   echo "-- Run tests"
   go test ./...
   # -race require CGO_ENABLED, ensure it's not set and default is used.
   echo "-- Run tests with -race"
   CGO_ENABLED= go test -race ./... -short
fi

echo "Start lint Linux"
GOOS=linux GOARCH=amd64 golangci-lint run

echo "Start lint FreeBSD"
GOOS=freebsd GOARCH=amd64 golangci-lint run --build-tags noexec,nomeminfo,nozfs,nonetdev,nonetisr


echo "Start lint Windows"
GOOS=windows GOARCH=amd64 golangci-lint run

echo "Success"
