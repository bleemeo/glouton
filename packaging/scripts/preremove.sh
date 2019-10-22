#!/bin/sh

case "$1" in
    upgrade|1)
        touch /var/lib/glouton/upgrade
        ;;
esac

systemctl stop glouton.service

exit 0
