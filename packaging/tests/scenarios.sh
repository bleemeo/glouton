#!/bin/sh
#
# Runs inside the test container. Builds two stub Glouton packages -- one carrying the
# maintainer scripts from before the fix, one carrying the current ones -- then runs a
# single named scenario against them.
#
#   scenarios.sh <old|new|real> <scenario>
#
# Exits 0 when every assertion held, 1 otherwise. run.sh knows which combinations are
# *expected* to fail: the "old" scripts must fail the purge/remove scenarios, otherwise
# we have not reproduced the bug and the rest of the suite proves nothing.

set -u

VARIANT="${1:?usage: scenarios.sh <old|new|real> <scenario>}"
SCENARIO="${2:?usage: scenarios.sh <old|new|real> <scenario>}"

DSH_ENABLED=/var/lib/systemd/deb-systemd-helper-enabled
DSH_ALSO="$DSH_ENABLED/glouton.service.dsh-also"
DSH_LINK="$DSH_ENABLED/multi-user.target.wants/glouton.service"
WANTS=/etc/systemd/system/multi-user.target.wants/glouton.service

FAILED=0

ok()   { echo "    ok    | $*"; }
fail() { echo "    FAIL  | $*"; FAILED=1; }
step() { echo "    step  > $*"; }

# ---------------------------------------------------------------- package building

build_stub_deb() {
    variant="$1"
    version="$2"
    root="/build/${variant}-${version}"

    rm -rf "$root"
    mkdir -p "$root/DEBIAN" "$root/lib/systemd/system" "$root/usr/sbin" \
             "$root/usr/lib/glouton" "$root/etc/glouton/conf.d" "$root/var/lib/glouton"

    # The maintainer scripts and the unit file are the real artifacts under test.
    cp "/src/$variant/postinstall.sh" "$root/DEBIAN/postinst"
    cp "/src/$variant/preremove.sh"   "$root/DEBIAN/prerm"
    cp "/src/$variant/postremove.sh"  "$root/DEBIAN/postrm"
    chmod 0755 "$root/DEBIAN/postinst" "$root/DEBIAN/prerm" "$root/DEBIAN/postrm"
    # The baseline has no preinst; only ship one when the variant provides it.
    if [ -e "/src/$variant/preinstall.sh" ]; then
        cp "/src/$variant/preinstall.sh" "$root/DEBIAN/preinst"
        chmod 0755 "$root/DEBIAN/preinst"
    fi
    # All three units ship in the real package. The timer in particular must be here, or
    # the auto-upgrade scenarios would silently test nothing.
    for u in glouton.service glouton-auto-upgrade.service glouton-auto-upgrade.timer; do
        cp "/src/$u" "$root/lib/systemd/system/$u"
    done
    printf '#!/bin/sh\nexit 0\n' > "$root/usr/lib/glouton/glouton-auto-upgrade"
    chmod 0755 "$root/usr/lib/glouton/glouton-auto-upgrade"

    # The Go binary plays no part in this bug, so stub it out and keep the build instant.
    printf '#!/bin/sh\nexec sleep infinity\n' > "$root/usr/sbin/glouton"
    printf '#!/bin/sh\nexit 0\n' > "$root/usr/sbin/glouton-netstat"
    printf '#!/bin/sh\nexit 0\n' > "$root/usr/sbin/glouton-gather-facts"
    chmod 0755 "$root/usr/sbin/glouton" "$root/usr/sbin/glouton-netstat" \
               "$root/usr/sbin/glouton-gather-facts"

    cat > "$root/DEBIAN/control" <<EOF
Package: glouton
Version: $version
Section: admin
Priority: optional
Architecture: $(dpkg --print-architecture)
Maintainer: Glouton packaging tests <noreply@bleemeo.com>
Description: Glouton packaging test stub ($variant maintainer scripts)
 Ships the real maintainer scripts and unit file with a stub binary, so the
 deb-systemd-helper state machine can be exercised without a Go build.
EOF

    mkdir -p /debs
    dpkg-deb --build --root-owner-group "$root" "/debs/glouton_${variant}_${version}.deb" >/dev/null
}

# V1/V2 exist so upgrades can be tested; versions are ordered old < new so that an
# old -> new install is a genuine dpkg upgrade.
OLD_V1=1.0.0
OLD_V2=1.1.0
NEW_V1=2.0.0
NEW_V2=2.1.0

deb() { echo "/debs/glouton_$1_$2.deb"; }

# ---------------------------------------------------------------- actions

install_deb() {
    step "dpkg -i $(basename "$1")"
    if ! dpkg -i "$1" >/tmp/dpkg.log 2>&1; then
        fail "dpkg -i $(basename "$1") failed"
        sed 's/^/           | /' /tmp/dpkg.log
        return 1
    fi
    return 0
}

purge_pkg() {
    step "apt-get purge glouton"
    if ! apt-get -y purge glouton >/tmp/apt.log 2>&1; then
        fail "apt-get purge failed"
        sed 's/^/           | /' /tmp/apt.log
        return 1
    fi
    return 0
}

remove_pkg() {
    step "apt-get remove glouton"
    if ! apt-get -y remove glouton >/tmp/apt.log 2>&1; then
        fail "apt-get remove failed"
        sed 's/^/           | /' /tmp/apt.log
        return 1
    fi
    return 0
}

# ---------------------------------------------------------------- assertions

assert_active() {
    i=0
    while [ "$i" -lt 20 ]; do
        if systemctl is-active --quiet glouton; then
            ok "glouton.service is active"
            return
        fi
        i=$((i + 1))
        sleep 0.5
    done
    fail "glouton.service is not active (is-active=$(systemctl is-active glouton 2>&1))"
}

assert_not_active() {
    sleep 2   # give a wrongly-started unit time to show up, so this can't pass by racing
    if systemctl is-active --quiet glouton; then
        fail "glouton.service is active but should not be"
    else
        ok "glouton.service is not active (as expected)"
    fi
}

assert_enabled() {
    state=$(systemctl is-enabled glouton 2>&1)
    if [ "$state" = "enabled" ]; then
        ok "glouton.service is enabled"
    else
        fail "glouton.service is not enabled (is-enabled=$state)"
    fi
}

assert_disabled() {
    state=$(systemctl is-enabled glouton 2>&1)
    if [ "$state" = "enabled" ]; then
        fail "glouton.service is enabled but should not be"
    else
        ok "glouton.service is not enabled (is-enabled=$state)"
    fi
}

# The heart of PRODUCT-3181: purge must leave no deb-systemd-helper bookkeeping behind.
assert_no_dsh_state() {
    clean=1
    if [ -e "$DSH_ALSO" ]; then
        fail "leftover state file $DSH_ALSO"
        clean=0
    fi
    if [ -e "$DSH_LINK" ]; then
        fail "leftover state marker $DSH_LINK"
        clean=0
    fi
    if [ -e "$WANTS" ]; then
        fail "leftover unit symlink $WANTS"
        clean=0
    fi
    [ "$clean" -eq 1 ] && ok "no deb-systemd-helper state left behind"
}

report_dsh_state() {
    echo "    info  | dsh-also:    $([ -e "$DSH_ALSO" ] && echo present || echo absent)"
    echo "    info  | dsh marker:  $([ -e "$DSH_LINK" ] && echo present || echo absent)"
    echo "    info  | wants link:  $([ -e "$WANTS" ] && echo present || echo absent)"
    echo "    info  | was-enabled: $(DPKG_MAINTSCRIPT_PACKAGE=glouton \
        deb-systemd-helper --quiet was-enabled glouton.service >/dev/null 2>&1 \
        && echo true || echo false)"
}

# ---------------------------------------------------------------- scenarios

case "$VARIANT" in
    old)  V1=$(deb old "$OLD_V1"); V2=$(deb old "$OLD_V2") ;;
    new)  V1=$(deb new "$NEW_V1"); V2=$(deb new "$NEW_V2") ;;
    real) V1=/src/real.deb;        V2=/src/real.deb ;;
    *)    echo "unknown variant: $VARIANT" >&2; exit 2 ;;
esac

if [ "$VARIANT" = "real" ]; then
    # Let the real agent run standalone; it must not need Bleemeo credentials to start.
    mkdir -p /etc/glouton/conf.d
    printf 'bleemeo:\n  enabled: false\n' > /etc/glouton/conf.d/99-packaging-test.conf
else
    build_stub_deb old "$OLD_V1"
    build_stub_deb old "$OLD_V2"
    build_stub_deb new "$NEW_V1"
    build_stub_deb new "$NEW_V2"
fi

case "$SCENARIO" in

    fresh-install)
        # Baseline: a clean machine must end up with Glouton running and enabled.
        install_deb "$V1" || exit 1
        assert_active
        assert_enabled
        ;;

    purge-clears-state)
        # The literal bug report: after purge, glouton.service.dsh-also must be gone.
        install_deb "$V1" || exit 1
        assert_active
        purge_pkg || exit 1
        report_dsh_state
        assert_no_dsh_state
        ;;

    purge-reinstall)
        # The reported consequence: "Glouton is installed but don't start".
        install_deb "$V1" || exit 1
        purge_pkg || exit 1
        install_deb "$V1" || exit 1
        report_dsh_state
        assert_active
        assert_enabled
        ;;

    upgrade)
        # A plain upgrade must leave Glouton running.
        install_deb "$V1" || exit 1
        assert_active
        install_deb "$V2" || exit 1
        assert_active
        assert_enabled
        ;;

    upgrade-honors-disable)
        # Guard on the repair: a deliberate `systemctl disable` by the admin must survive
        # an upgrade. Stale state and an intentional disable look identical on disk, so
        # the repair is gated on a first install ($2 empty) and must not fire here.
        install_deb "$V1" || exit 1
        assert_active
        step "systemctl disable --now glouton"
        systemctl disable --now glouton >/dev/null 2>&1
        install_deb "$V2" || exit 1
        assert_disabled
        assert_not_active
        ;;

    *)
        echo "unknown scenario: $SCENARIO" >&2
        exit 2
        ;;
esac

exit "$FAILED"
