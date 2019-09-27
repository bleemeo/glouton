#!/bin/sh

set -ex

: ${COMMIT_HASH:=$(git rev-parse --short HEAD)}
: ${VERSION:=$(TZ=UTC date +%y.%m.%d.%H%M%S)}

export CGO_ENABLED=0
go generate glouton/...
GOOS=linux GOARCH=amd64 go build -o glouton-x86_64 -ldflags "-s -w -X glouton/version.Version=$VERSION -X glouton/version.BuildHash=$COMMIT_HASH" glouton
GOOS=linux GOARCH=386 go build -o glouton-i386 -ldflags "-s -w -X glouton/version.Version=$VERSION -X glouton/version.BuildHash=$COMMIT_HASH" glouton
GOOS=linux GOARCH=arm GOARM=6 go build -o glouton-arm -ldflags "-s -w -X glouton/version.Version=$VERSION -X glouton/version.BuildHash=$COMMIT_HASH" glouton
