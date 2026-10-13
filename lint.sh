#!/bin/sh

set -e

LINTER_VERSION=v2.13.2

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
   GO_CACHE_PERSISTED=1
else
   GO_CACHE_PERSISTED=0
fi

# Cap the linter's heap. Loading and type-checking the whole dependency graph is what
# dominates the memory used, and without this the Go runtime lets the heap grow until the
# container gets OOM-killed (measured: >9 GiB, killed). Forcing the GC to work instead
# brings the peak back to ~7.3 GiB and lets the run complete, at the cost of some CPU.
# The linter set has almost no influence here: 87 linters peak at 7303 MiB, 29 at 7218 MiB.
LINTER_MEMORY_LIMIT="-e GOMEMLIMIT=5GiB -e GOGC=50"

DOCKER_RUN_EXTRA_OPTION=""
if [ "${OPEN_SHELL}" = "1" ]; then
   DOCKER_RUN_EXTRA_OPTION="-it"
fi

docker run --rm -e HOME=/go/pkg -e CGO_ENABLED=0 \
   -v $(pwd):/src -w /src ${GO_MOUNT_CACHE} \
   --entrypoint '' \
   -e LINT=$LINT -e COVERAGE=$COVERAGE -e OPEN_SHELL=$OPEN_SHELL \
   -e GO_CACHE_PERSISTED=$GO_CACHE_PERSISTED \
   -e GOOS -e GOARCH \
   -w /src \
   --tmpfs /app/webui/node_modules:exec \
   ${LINTER_MEMORY_LIMIT} ${DOCKER_RUN_EXTRA_OPTION} \
   golangci/golangci-lint:${LINTER_VERSION} \
   sh -euc "
      mkdir -p /go/pkg
      git config --global --add safe.directory /app
      if [ \"${OPEN_SHELL}\" = \"1\" ]; then
         exec bash
      fi
      if [ \"${COVERAGE}\" = \"1\" ]; then
         trap 'chown $USER_UID coverage.out coverage.html' EXIT
      fi
      sh lint_inner.sh
   "

