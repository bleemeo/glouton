#!/bin/sh

systemctl daemon-reload >/dev/null 2>&1

case "$1" in
    remove)
        rm -f /var/lib/jmxtrans/glouton-generated.json
        ;;
    purge)
        # Both directories belong to the package, so purge takes them whole, the way nginx,
        # chrony and collectd take theirs. It has to be the whole tree rather than a list of
        # known files: state.cache.json, stderr.log and tsdb/ appear at runtime, and a conf.d
        # drop-in an operator adds is not dpkg's either. Anything surviving here outlives the
        # uid that userdel frees below, so the next system user created on the host inherits
        # it -- including state.json and the credentials in it -- and a leftover drop-in
        # comes back into effect on the next install.
        rm -rf /var/lib/glouton
        rm -rf /etc/glouton
        # The directory is jmxtrans', so only the file we generated in it is ours to remove.
        rm -f /var/lib/jmxtrans/glouton-generated.json
        userdel --force glouton > /dev/null
        groupdel glouton > /dev/null 2> /dev/null
        # Take away both the enable symlinks deb-systemd-helper recorded and the state file
        # listing them, the way a debhelper-generated postrm does. Purge therefore leaves no
        # bookkeeping behind, and the next install starts from deb-systemd-helper's default:
        # no state file means was-enabled is true, so postinst enables Glouton and starts it.
        # The timer's symlink goes at the same time as its unit file, so systemd is not left
        # with a link to a unit that no longer exists. Neither call needs the unit files dpkg
        # has already deleted.
        if [ -x "/usr/bin/deb-systemd-helper" ]; then
            deb-systemd-helper purge 'glouton.service' >/dev/null || true
            deb-systemd-helper purge 'glouton-auto-upgrade.timer' >/dev/null || true
        fi
        ;;
    0)
        # Remove on rpm-distribution
        rm -f /var/lib/jmxtrans/glouton-generated.json
        ;;
    1)
        # Upgrade on rpm-distribution
        test -x /usr/bin/systemctl -o -x /bin/systemctl && systemctl try-restart glouton.service
        ;;
esac

exit 0
