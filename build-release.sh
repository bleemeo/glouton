#!/bin/sh

set -ex

: ${COMMIT_HASH:=$(git rev-parse --short HEAD)}
: ${VERSION:=$(TZ=UTC date +%y.%m.%d.%H%M%S)}

export CGO_ENABLED=0
go generate agentgo/...
GOOS=linux GOARCH=amd64 go build -o agentgo-x86_64 -ldflags "-s -w -X agentgo/version.Version=$VERSION -X agentgo/version.BuildHash=$COMMIT_HASH" agentgo
GOOS=linux GOARCH=386 go build -o agentgo-i386 -ldflags "-s -w -X agentgo/version.Version=$VERSION -X agentgo/version.BuildHash=$COMMIT_HASH" agentgo
# TODO: support for ARM
#GOOS=linux GOARCH=arm GOARM=6 go build -o agentgo-arm -ldflags "-s -w -X agentgo/version.Version=$VERSION -X agentgo/version.BuildHash=$COMMIT_HASH" agentgo
