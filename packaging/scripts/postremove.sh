#!/bin/sh

systemctl daemon-reload >/dev/null 2>&1

case "$1" in
    remove)
        rm -f /var/lib/jmxtrans/glouton-generated.json
        ;;
    purge)
        # Both directories belong to the package, so purge takes them whole, the way nginx,
        # chrony and collectd take theirs. Removing known files and then rmdir'ing did not
        # work: state.cache.json, stderr.log and tsdb/ are written at runtime and were never
        # on the list, so the rmdir always failed and /var/lib/glouton survived every purge.
        # userdel below then freed the uid with those files still carrying it, leaving the
        # next system user created on the host owning Glouton's state -- including
        # state.json, which holds its credentials. /etc/glouton had the same problem via any
        # conf.d drop-in dpkg does not own, and a leftover drop-in would silently come back
        # into effect on the next install.
        rm -rf /var/lib/glouton
        rm -rf /etc/glouton
        # The directory is jmxtrans', so only the file we generated in it is ours to remove.
        rm -f /var/lib/jmxtrans/glouton-generated.json
        userdel --force glouton > /dev/null
        groupdel glouton > /dev/null 2> /dev/null
        # Drop deb-systemd-helper's bookkeeping and the enable symlinks it lists, the way a
        # debhelper-generated postrm does. Left behind, glouton.service.dsh-also makes
        # was-enabled report the unit as disabled on the next install, so Glouton is
        # installed but never started; the timer's symlink outlives its unit file and shows
        # up as a failed unit. Neither call needs the unit files dpkg has already deleted.
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
