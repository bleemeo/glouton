#!/bin/sh

systemctl daemon-reload >/dev/null 2>&1

case "$1" in
    purge)
        rm -f /var/lib/glouton/state.json
        rm -f /var/lib/glouton/facts.yaml
        rm -f /var/lib/glouton/netstat.out
        rm -f /var/lib/glouton/cloudimage_creation
        rm -f /etc/glouton/agent.conf.d/30-install.conf
        if [ -d /var/lib/glouton ]; then
            rmdir --ignore-fail-on-non-empty /var/lib/glouton
        fi
        if [ -d /etc/glouton/agent.conf.d ]; then
            rmdir --ignore-fail-on-non-empty /etc/glouton/agent.conf.d
        fi
        if [ -d /etc/glouton ]; then
            rmdir --ignore-fail-on-non-empty /etc/glouton
        fi
        userdel --force glouton > /dev/null
        groupdel glouton > /dev/null
        ;;
    1)
        # Upgrade on rpm-distribution
        systemctl try-restart glouton.service
esac

exit 0
