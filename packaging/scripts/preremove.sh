#!/bin/sh

case "$1" in
    upgrade|1)
        touch /var/lib/glouton/upgrade
        ;;
esac

case "$1" in
    upgrade|remove)
        # On Debian, we stop in pre-remove.
        systemctl stop glouton.service
        ;;
    0)
        # On CentOS, we only stop if uninstall in pre-remove and restart in post-remove
        systemctl --no-reload disable glouton.service > /dev/null 2>&1 || :
        systemctl stop glouton.service > /dev/null 2>&1 || :
        ;;
esac


exit 0
