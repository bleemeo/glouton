#!/usr/bin/env bash
#
# Packaging regression tests for the Debian maintainer scripts (PRODUCT-3181).
#
# Boots Ubuntu 24.04 with systemd as PID 1 in a container and drives install / upgrade /
# remove / purge cycles against two builds of the package: one carrying the maintainer
# scripts from --old-ref (the pre-fix baseline) and one carrying the working tree's.
#
# The suite asserts that the *old* scripts fail the remove and purge scenarios. If one
# stops failing, that bug is no longer being reproduced and the passing result for the new
# scripts means nothing -- so that is reported as an error too.
#
# Everything runs inside throwaway containers, which are --privileged so that systemd can
# run as PID 1. On macOS that privilege applies to the Docker Desktop VM rather than to
# your workstation. No host directory is bind-mounted; the packaging files are baked into
# the image with COPY, and the container gets its own cgroup namespace.
#
# Usage:
#   ./run.sh                          # full matrix against the pinned pre-fix baseline
#   ./run.sh --old-ref <git-ref>      # compare against a different baseline
#   ./run.sh --scenario purge-reinstall
#   ./run.sh --deb dist/glouton_..._arm64.deb    # add a real-package smoke test
#   ./run.sh --keep                   # leave failed containers around to inspect
#
set -euo pipefail

HERE=$(cd "$(dirname "$0")" && pwd)
REPO_ROOT=$(git -C "$HERE" rev-parse --show-toplevel)

# Pinned to the last commit before this fix, not to "main": once the fix merges, "main"
# would carry it too, the "expected: fail" rows would all pass, and the suite would report
# failure forever. Override with --old-ref to compare against something else.
OLD_REF=67e1c83e80b0211cbe9e469e3c87e3fd0b9f8888
REAL_DEB=""
ONLY=""
KEEP=0
IMAGE=glouton-pkgtest
PREFIX=glouton-pkgtest

# Print the header comment block, stopping at the first line that is not a comment, so
# this never has to track line numbers as the header grows.
usage() { awk 'NR > 1 { if (!/^#/) exit; sub(/^# ?/, ""); print }' "$0"; }

# Every scenario run.sh knows about. Used to reject a mistyped --scenario, which would
# otherwise silently run nothing and exit 0.
SCENARIOS="fresh-install upgrade
           upgrade-honors-disable remove-honors-disable
           purge-clears-state purge-reinstall purge-removes-data
           remove-reinstall remove-then-purge
           install-records-timer-state purge-clears-timer-state remove-stops-timer"

while [ $# -gt 0 ]; do
    case "$1" in
        --old-ref)  OLD_REF=$2; shift 2 ;;
        --deb)      REAL_DEB=$2; shift 2 ;;
        --scenario) ONLY=$2; shift 2 ;;
        --keep)     KEEP=1; shift ;;
        -h|--help)  usage; exit 0 ;;
        *) echo "unknown option: $1" >&2; usage >&2; exit 2 ;;
    esac
done

if [ -n "$ONLY" ]; then
    case " $(echo $SCENARIOS) " in
        *" $ONLY "*) ;;
        *) echo "unknown scenario: $ONLY" >&2
           echo "known scenarios: $(echo $SCENARIOS)" >&2
           exit 2 ;;
    esac
fi

command -v docker >/dev/null || { echo "docker is required" >&2; exit 1; }
docker info >/dev/null 2>&1 || { echo "cannot reach the Docker daemon" >&2; exit 1; }

# ---------------------------------------------------------------- build context

CTX=$(mktemp -d)
cleanup_ctx() { rm -rf "$CTX"; }
trap cleanup_ctx EXIT

mkdir -p "$CTX/old" "$CTX/new"
for f in postinstall.sh preremove.sh postremove.sh; do
    git -C "$REPO_ROOT" show "$OLD_REF:packaging/scripts/$f" > "$CTX/old/$f" \
        || { echo "cannot read packaging/scripts/$f at $OLD_REF" >&2; exit 1; }
    cp "$REPO_ROOT/packaging/scripts/$f" "$CTX/new/$f"
done

# preinstall.sh arrived with this fix, so the baseline legitimately may not have one; a
# package simply ships no preinst in that case.
if git -C "$REPO_ROOT" cat-file -e "$OLD_REF:packaging/scripts/preinstall.sh" 2>/dev/null; then
    git -C "$REPO_ROOT" show "$OLD_REF:packaging/scripts/preinstall.sh" > "$CTX/old/preinstall.sh"
fi
if [ -f "$REPO_ROOT/packaging/scripts/preinstall.sh" ]; then
    cp "$REPO_ROOT/packaging/scripts/preinstall.sh" "$CTX/new/preinstall.sh"
fi
for u in glouton.service glouton-auto-upgrade.service glouton-auto-upgrade.timer; do
    cp "$REPO_ROOT/packaging/common/$u" "$CTX/$u"
done
cp "$HERE/scenarios.sh" "$HERE/Dockerfile" "$CTX/"

if [ -n "$REAL_DEB" ]; then
    [ -f "$REAL_DEB" ] || { echo "no such .deb: $REAL_DEB" >&2; exit 1; }
    cp "$REAL_DEB" "$CTX/real.deb"
fi

# Compare every maintainer script, not just the removal pair: a change confined to
# postinstall.sh or preinstall.sh is still a change worth testing. If nothing differs there
# is no baseline to reproduce the bug against, and the "expected: fail" rows below would
# all pass and be reported as failures -- so say why and stop, rather than exiting 1 with a
# summary that looks like a real regression.
identical=1
for f in preinstall.sh postinstall.sh preremove.sh postremove.sh; do
    old_f="$CTX/old/$f"
    new_f="$CTX/new/$f"
    if [ -f "$old_f" ] && [ -f "$new_f" ]; then
        diff -q "$old_f" "$new_f" >/dev/null 2>&1 || identical=0
    elif [ -f "$old_f" ] || [ -f "$new_f" ]; then
        # A script added or dropped by the change is a difference too. Absent on both
        # sides is not: neither revision has a preinst, and comparing two missing files
        # would otherwise make this guard permanently report a difference.
        identical=0
    fi
done
if [ "$identical" -eq 1 ]; then
    echo "the maintainer scripts at $OLD_REF are identical to the working tree's," >&2
    echo "so there is no baseline to reproduce the bug against." >&2
    echo "Pass --old-ref <a-ref-before-the-fix>." >&2
    exit 2
fi

echo "building test image (ubuntu:24.04 + systemd)..."
docker build -q -t "$IMAGE" "$CTX" >/dev/null

# ---------------------------------------------------------------- scenario runner

PASS=0; FAILED=0; UNEXPECTED=0
RESULTS=""

# run_scenario <variant> <scenario> <expected: pass|fail>
run_scenario() {
    local variant=$1 scenario=$2 expected=$3
    local cname="${PREFIX}-${variant}-${scenario}"
    local rc=0 verdict state i

    if [ -n "$ONLY" ] && [ "$ONLY" != "$scenario" ]; then
        return 0
    fi

    printf '\n=== %-28s [%s scripts]  expected: %s\n' "$scenario" "$variant" "$expected"

    docker rm -f "$cname" >/dev/null 2>&1 || true
    # Ubuntu 24.04 is cgroup-v2 only, so systemd as PID 1 needs no host cgroup bind mount
    # and can have its own cgroup namespace -- nothing of the host is writable from here.
    docker run -d --name "$cname" --privileged --cgroupns=private \
        --tmpfs /run --tmpfs /run/lock "$IMAGE" >/dev/null

    # Wait for systemd: without /run/systemd/system the postinst skips the whole
    # enable/restart path and every scenario would be meaningless.
    state=""
    for i in $(seq 1 30); do
        state=$(docker exec "$cname" systemctl is-system-running 2>/dev/null || true)
        case "$state" in running|degraded) break ;; esac
        sleep 1
    done
    case "$state" in
        running|degraded) ;;
        *) echo "    ERROR | systemd did not come up (is-system-running=${state:-none})"
           docker rm -f "$cname" >/dev/null 2>&1 || true
           RESULTS+=$(printf '\n  %-28s %-5s  ERROR (no systemd)' "$scenario" "$variant")
           UNEXPECTED=$((UNEXPECTED + 1))
           return 0 ;;
    esac

    docker exec "$cname" /src/scenarios.sh "$variant" "$scenario" || rc=$?

    if [ "$rc" -eq 0 ]; then verdict=pass; else verdict=fail; fi

    if [ "$verdict" = "$expected" ]; then
        if [ "$expected" = fail ]; then
            echo "    => FAILED AS EXPECTED (bug reproduced)"
            RESULTS+=$(printf '\n  %-28s %-5s  reproduced the bug (expected fail)' "$scenario" "$variant")
        else
            echo "    => PASS"
            RESULTS+=$(printf '\n  %-28s %-5s  pass' "$scenario" "$variant")
        fi
        PASS=$((PASS + 1))
    else
        if [ "$expected" = fail ]; then
            echo "    => UNEXPECTED PASS -- the bug did not reproduce, so this suite proves nothing"
            RESULTS+=$(printf '\n  %-28s %-5s  UNEXPECTED PASS (bug did not reproduce)' "$scenario" "$variant")
            UNEXPECTED=$((UNEXPECTED + 1))
        else
            echo "    => UNEXPECTED FAILURE"
            RESULTS+=$(printf '\n  %-28s %-5s  UNEXPECTED FAILURE' "$scenario" "$variant")
            FAILED=$((FAILED + 1))
        fi
    fi

    if [ "$KEEP" -eq 1 ] && [ "$verdict" != "$expected" ]; then
        echo "    (container kept for inspection: docker exec -it $cname bash)"
    else
        docker rm -f "$cname" >/dev/null 2>&1 || true
    fi
}

# ---------------------------------------------------------------- the matrix
#
# Against the pre-fix scripts, every removal scenario must fail -- that is the
# reproduction. The install and upgrade ones must pass for both, old and new alike: the
# fixes cannot be bought at the cost of the ordinary paths.

run_scenario old fresh-install                pass
run_scenario old upgrade                      pass
run_scenario old upgrade-honors-disable       pass
run_scenario old remove-honors-disable        pass
run_scenario old purge-clears-state           fail
run_scenario old purge-reinstall              fail
run_scenario old purge-removes-data           fail
run_scenario old remove-reinstall             fail
run_scenario old remove-then-purge            fail
run_scenario old install-records-timer-state  fail
run_scenario old purge-clears-timer-state     fail
run_scenario old remove-stops-timer           fail

run_scenario new fresh-install                pass
run_scenario new upgrade                      pass
run_scenario new upgrade-honors-disable       pass
run_scenario new remove-honors-disable        pass
run_scenario new purge-clears-state           pass
run_scenario new purge-reinstall              pass
run_scenario new purge-removes-data           pass
run_scenario new remove-reinstall             pass
run_scenario new remove-then-purge            pass
run_scenario new install-records-timer-state  pass
run_scenario new purge-clears-timer-state     pass
run_scenario new remove-stops-timer           pass


if [ -n "$REAL_DEB" ]; then
    echo
    echo "### real package: $(basename "$REAL_DEB")"
    run_scenario real fresh-install                pass
    run_scenario real remove-honors-disable        pass
    run_scenario real purge-clears-state           pass
    run_scenario real purge-reinstall              pass
    run_scenario real purge-removes-data           pass
    run_scenario real remove-reinstall             pass
    run_scenario real remove-then-purge            pass
    run_scenario real install-records-timer-state  pass
    run_scenario real purge-clears-timer-state     pass
    run_scenario real remove-stops-timer           pass
fi

# ---------------------------------------------------------------- summary

echo
echo "================================ summary ================================"
echo "$RESULTS"
echo
echo "  as expected: $PASS   unexpected failures: $FAILED   did-not-reproduce: $UNEXPECTED"
echo "========================================================================="

[ "$FAILED" -eq 0 ] && [ "$UNEXPECTED" -eq 0 ]
