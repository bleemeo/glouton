#!/bin/sh

systemctl daemon-reload >/dev/null 2>&1

case "$1" in
    remove)
        rm -f /var/lib/jmxtrans/glouton-generated.json
        ;;
    purge)
        rm -f /var/lib/glouton/state.json
        rm -f /var/lib/glouton/facts.yaml
        rm -f /var/lib/glouton/netstat.out
        rm -f /var/lib/glouton/cloudimage_creation
        rm -f /var/lib/jmxtrans/glouton-generated.json
        rm -f /etc/glouton/conf.d/30-install.conf
        if [ -d /var/lib/glouton ]; then
            rmdir --ignore-fail-on-non-empty /var/lib/glouton
        fi
        if [ -d /etc/glouton/conf.d ]; then
            rmdir --ignore-fail-on-non-empty /etc/glouton/conf.d
        fi
        if [ -d /etc/glouton ]; then
            rmdir --ignore-fail-on-non-empty /etc/glouton
        fi
        userdel --force glouton > /dev/null
        groupdel glouton > /dev/null 2> /dev/null
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
