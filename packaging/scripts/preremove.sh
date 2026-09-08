#!/bin/sh

case "$1" in
    upgrade|1)
        touch /var/lib/glouton/upgrade
        ;;
esac

case "$1" in
    upgrade)
        # On Debian, we stop in pre-remove.
	test -e /lib/init/upstart-job && stop glouton
        test -x /usr/bin/systemctl -o -x /bin/systemctl && systemctl stop glouton.service
        ;;
    remove)
	test -e /lib/init/upstart-job && stop glouton
        # Stop every unit we ship and disable none of them, which is all a
        # debhelper-generated prerm does here. `systemctl disable` used to run on this
        # path: it deletes the enable symlink behind deb-systemd-helper's back, so its
        # state file keeps listing a link that no longer exists, was-enabled reports the
        # unit as disabled from then on, and the next install skips both the enable and
        # the restart. postrm's `deb-systemd-helper purge` does the disabling instead,
        # which is what keeps the symlinks and the state file in sync.
        #
        # deb-systemd-invoke rather than systemctl, because it honours policy-rc.d; the
        # guards are debhelper's, and match on systemd actually running rather than on the
        # systemctl binary merely existing. The timer needs stopping too, or dpkg pulls its
        # unit file out from under a running unit and systemd reports it as failed.
        if [ -z "${DPKG_ROOT:-}" ] && [ -d /run/systemd/system ]; then
            deb-systemd-invoke stop glouton.service glouton-auto-upgrade.timer >/dev/null || true
        fi
        ;;
    0)
        # On CentOS, we only stop if uninstall in pre-remove and restart in post-remove.
        # Unlike Debian, disabling here is correct and expected: rpm has no purge phase and
        # no state file to keep in sync, and %systemd_preun -- which every rpm package uses
        # on erase -- is exactly `systemctl --no-reload disable --now` over *every* unit the
        # package ships. The timer has to be in that list too, or its symlink outlives the
        # unit file and systemd reports it as not-found and failed.
        test -x /usr/bin/systemctl -o -x /bin/systemctl && systemctl --no-reload disable glouton.service glouton-auto-upgrade.timer > /dev/null 2>&1 || :
        test -x /usr/bin/systemctl -o -x /bin/systemctl && systemctl stop glouton.service glouton-auto-upgrade.timer > /dev/null 2>&1 || :
        ;;
esac


exit 0
