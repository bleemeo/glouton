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

TIMER=glouton-auto-upgrade.timer
TIMER_DSH_ALSO="$DSH_ENABLED/$TIMER.dsh-also"
TIMER_DSH_LINK="$DSH_ENABLED/timers.target.wants/$TIMER"
TIMER_WANTS="/etc/systemd/system/timers.target.wants/$TIMER"

FAILED=0

ok()   { echo "    ok    | $*"; }
fail() { echo "    FAIL  | $*"; FAILED=1; }
step() { echo "    step  > $*"; }

# Every leftover this suite looks for is a symlink into a unit file that dpkg has already
# deleted, so it is dangling and `test -e` -- which follows the link -- reports it as
# absent. Only -L sees it.
exists() { [ -e "$1" ] || [ -L "$1" ]; }
present() { exists "$1" && echo present || echo absent; }

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

# The real agent must not need Bleemeo credentials to start. Written before every install
# rather than once at startup, because purge now takes /etc/glouton with it.
prepare_real_conf() {
    [ "$VARIANT" = "real" ] || return 0
    mkdir -p /etc/glouton/conf.d
    printf 'bleemeo:\n  enabled: false\n' > /etc/glouton/conf.d/99-packaging-test.conf
}

install_deb() {
    prepare_real_conf
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
    if exists "$DSH_ALSO"; then
        fail "leftover state file $DSH_ALSO"
        clean=0
    fi
    if exists "$DSH_LINK"; then
        fail "leftover state marker $DSH_LINK"
        clean=0
    fi
    if exists "$WANTS"; then
        fail "leftover unit symlink $WANTS"
        clean=0
    fi
    [ "$clean" -eq 1 ] && ok "no deb-systemd-helper state left behind"
}

report_dsh_state() {
    echo "    info  | dsh-also:    $(present "$DSH_ALSO")"
    echo "    info  | dsh marker:  $(present "$DSH_LINK")"
    echo "    info  | wants link:  $(present "$WANTS")"
    echo "    info  | was-enabled: $(DPKG_MAINTSCRIPT_PACKAGE=glouton \
        deb-systemd-helper --quiet was-enabled glouton.service >/dev/null 2>&1 \
        && echo true || echo false)"
}

# ---------------------------------------------------------------- the auto-upgrade timer

# What the get.bleemeo.com installer does. The package itself never enables the timer, so
# plain systemctl is the only thing that ever turns it on -- and it records nothing in
# deb-systemd-helper's state, which is what leaves the symlink orphaned on purge.
enable_timer_like_installer() {
    step "systemctl enable --now $TIMER  (as the get.bleemeo.com installer does)"
    if ! systemctl enable --now "$TIMER" >/tmp/timer.log 2>&1; then
        fail "could not enable $TIMER"
        sed 's/^/           | /' /tmp/timer.log
        return 1
    fi
    if ! exists "$TIMER_WANTS"; then
        fail "$TIMER_WANTS was not created, so this scenario would test nothing"
        return 1
    fi
    return 0
}

assert_no_timer_state() {
    clean=1
    if exists "$TIMER_WANTS"; then
        fail "leftover unit symlink $TIMER_WANTS"
        clean=0
    fi
    if exists "$TIMER_DSH_ALSO"; then
        fail "leftover state file $TIMER_DSH_ALSO"
        clean=0
    fi
    if exists "$TIMER_DSH_LINK"; then
        fail "leftover state marker $TIMER_DSH_LINK"
        clean=0
    fi
    [ "$clean" -eq 1 ] && ok "no $TIMER state left behind"
}

# LoadState is not-found either way once dpkg has deleted the unit file, so it cannot tell
# a stopped timer from one still running with its unit file pulled out from under it.
# ActiveState can.
assert_timer_stopped() {
    astate=$(systemctl show -p ActiveState --value "$TIMER" 2>/dev/null)
    if [ "$astate" = "inactive" ]; then
        ok "$TIMER is inactive"
    else
        fail "$TIMER is not inactive (ActiveState=$astate)"
    fi
}

assert_no_failed_units() {
    failed=$(systemctl list-units --failed --plain --no-legend 2>/dev/null | awk '{print $1}')
    if [ -z "$failed" ]; then
        ok "no failed units"
    else
        fail "failed units left behind: $(echo $failed)"
    fi
}

# ---------------------------------------------------------------- purge completeness

# Files the agent writes while running that no purge ever listed by name. The stub package
# does not run anything, so plant them; the real one has written its own by now, and
# planting them again is harmless.
plant_runtime_data() {
    step "plant runtime files and an operator drop-in"
    mkdir -p /var/lib/glouton/tsdb/wal /etc/glouton/conf.d
    touch /var/lib/glouton/state.cache.json /var/lib/glouton/stderr.log \
          /var/lib/glouton/tsdb/wal/00000000
    chown -R glouton:glouton /var/lib/glouton 2>/dev/null || true
    # dpkg owns none of this, so nothing but postrm will ever remove it.
    printf 'logging:\n  level: 2\n' > /etc/glouton/conf.d/90-operator.conf
}

assert_purged() {
    clean=1
    if [ -d /var/lib/glouton ]; then
        fail "/var/lib/glouton survived: $(ls -A /var/lib/glouton | tr '\n' ' ')"
        clean=0
    fi
    if [ -d /etc/glouton ]; then
        fail "/etc/glouton survived: $(find /etc/glouton -mindepth 1 | tr '\n' ' ')"
        clean=0
    fi
    # postrm frees the glouton uid, so anything it failed to delete is now unowned -- and
    # inherited by whichever system user is created next. state.json holds credentials.
    orphans=$(find /var /etc -nouser 2>/dev/null)
    if [ -n "$orphans" ]; then
        fail "left owned by the freed uid: $(echo $orphans | cut -c1-160)"
        clean=0
    fi
    [ "$clean" -eq 1 ] && ok "purge removed the package's data and configuration"
}

assert_timer_state_recorded() {
    if ! exists "$TIMER_DSH_ALSO"; then
        fail "no $TIMER_DSH_ALSO: deb-systemd-helper has nothing to clean up on purge"
        return
    fi
    if grep -qx -- "$TIMER_WANTS" "$TIMER_DSH_ALSO"; then
        ok "$TIMER_DSH_ALSO records $TIMER_WANTS"
    else
        fail "$TIMER_DSH_ALSO does not record $TIMER_WANTS (holds: $(tr '\n' ' ' < "$TIMER_DSH_ALSO"))"
    fi
}

# update-state writes bookkeeping only. If it ever started creating the symlink too, every
# apt-repo host would silently gain unattended upgrades on its next install.
assert_timer_not_enabled_by_package() {
    state=$(systemctl is-enabled "$TIMER" 2>&1)
    if [ "$state" = "enabled" ]; then
        fail "$TIMER was enabled by the package; recording its state must not turn it on"
    else
        ok "$TIMER is still not enabled by the package (is-enabled=$state)"
    fi
}

report_timer_state() {
    echo "    info  | timer wants link:  $(present "$TIMER_WANTS")"
    echo "    info  | timer dsh-also:    $(present "$TIMER_DSH_ALSO")"
    echo "    info  | timer dsh marker:  $(present "$TIMER_DSH_LINK")"
    echo "    info  | timer ActiveState: $(systemctl show -p ActiveState --value "$TIMER" 2>/dev/null)"
    echo "    info  | timer LoadState:   $(systemctl show -p LoadState --value "$TIMER" 2>/dev/null)"
}

# ---------------------------------------------------------------- scenarios

case "$VARIANT" in
    old)  V1=$(deb old "$OLD_V1"); V2=$(deb old "$OLD_V2") ;;
    new)  V1=$(deb new "$NEW_V1"); V2=$(deb new "$NEW_V2") ;;
    real) V1=/src/real.deb;        V2=/src/real.deb ;;
    *)    echo "unknown variant: $VARIANT" >&2; exit 2 ;;
esac

if [ "$VARIANT" != "real" ]; then
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

    heals-stale-state)
        # A host purged before the purge cleanup shipped: the state file it left behind is
        # still there when the fixed package arrives, and would make the fresh install come
        # up disabled and stopped exactly as the ticket describes. Always purged with the
        # pre-fix scripts, whichever variant is under test, so the two columns answer "does
        # installing this package onto such a host recover it?"
        install_deb "$(deb old "$OLD_V1")" || exit 1
        assert_active
        purge_pkg || exit 1
        report_dsh_state
        install_deb "$V1" || exit 1
        report_dsh_state
        assert_active
        assert_enabled
        ;;

    remove-reinstall)
        # The ticket's failure mode reached through `apt remove` instead of `apt purge`.
        # prerm used to run `systemctl disable`, which deletes the enable symlink without
        # telling deb-systemd-helper; its state file then lists a link that is gone, so
        # was-enabled stays false and the reinstall never starts Glouton. Nothing clears
        # that state on remove, so unlike the purge path it does not even self-heal.
        install_deb "$V1" || exit 1
        assert_active
        remove_pkg || exit 1
        report_dsh_state
        install_deb "$V1" || exit 1
        report_dsh_state
        assert_active
        assert_enabled
        ;;

    remove-honors-disable)
        # The mirror of upgrade-honors-disable, and the reason postrm must not do purge's
        # work on the remove path: remove has to *preserve* the enable state so that a
        # reinstall restores whatever the admin chose. If postrm purged deb-systemd-helper's
        # state here, the state file would be gone, was-enabled would fall back to its
        # "no state file means enabled" default, and the reinstall would switch a
        # deliberately disabled unit back on.
        install_deb "$V1" || exit 1
        assert_active
        step "systemctl disable --now glouton"
        systemctl disable --now glouton >/dev/null 2>&1
        remove_pkg || exit 1
        install_deb "$V1" || exit 1
        report_dsh_state
        assert_disabled
        assert_not_active
        ;;

    purge-removes-data)
        # "Purge is incomplete" in the most literal sense. postrm used to delete four files
        # by name and rmdir the directory, so anything written at runtime kept it alive --
        # and the uid was freed out from under whatever was left.
        install_deb "$V1" || exit 1
        plant_runtime_data
        purge_pkg || exit 1
        assert_purged
        ;;

    remove-then-purge)
        # `apt remove` and `apt purge` later is a different dpkg sequence from purging in
        # one go: postrm purge runs on a package already in config-files state. Worth its
        # own scenario because dropping the `systemctl disable` from prerm changed what
        # remove leaves for that purge to find -- the enable symlink now survives it.
        install_deb "$V1" || exit 1
        assert_active
        remove_pkg || exit 1
        report_dsh_state
        purge_pkg || exit 1
        report_dsh_state
        assert_no_dsh_state
        assert_no_failed_units
        ;;

    install-records-timer-state)
        # Guards the postinst's update-state call from quietly becoming a no-op, which
        # would leave purge-clears-timer-state passing for some unrelated reason. Also
        # pins the other half of the bargain: recording the timer's links must not enable
        # it, or apt-repo hosts would start auto-upgrading without anyone asking.
        install_deb "$V1" || exit 1
        report_timer_state
        assert_timer_state_recorded
        assert_timer_not_enabled_by_package
        ;;

    purge-clears-timer-state)
        # Same class of leak for the other unit. The timer is enabled outside dpkg, so
        # deb-systemd-helper has no state for it and purge leaves its symlink pointing at a
        # unit file that no longer exists.
        install_deb "$V1" || exit 1
        enable_timer_like_installer || exit 1
        purge_pkg || exit 1
        report_timer_state
        assert_no_timer_state
        assert_no_failed_units
        ;;

    remove-stops-timer)
        # prerm stops glouton.service but used to leave the timer running, so dpkg pulled
        # the unit file out from under an active unit and systemd reported it as failed.
        install_deb "$V1" || exit 1
        enable_timer_like_installer || exit 1
        remove_pkg || exit 1
        report_timer_state
        assert_timer_stopped
        assert_no_failed_units
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
