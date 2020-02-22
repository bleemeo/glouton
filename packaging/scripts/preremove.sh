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
        test -x /usr/bin/systemctl -o -x /bin/systemctl && systemctl --no-reload disable glouton.service
        test -x /usr/bin/systemctl -o -x /bin/systemctl && systemctl stop glouton.service
        ;;
    0)
        # On CentOS, we only stop if uninstall in pre-remove and restart in post-remove
        test -x /usr/bin/systemctl -o -x /bin/systemctl && systemctl --no-reload disable glouton.service > /dev/null 2>&1 || :
        test -x /usr/bin/systemctl -o -x /bin/systemctl && systemctl stop glouton.service > /dev/null 2>&1 || :
        ;;
esac


exit 0
