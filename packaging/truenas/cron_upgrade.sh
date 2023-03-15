#!/bin/sh

# Trigger glouton upgrade after a random delay
# This cron will be started at 7h every weekday. This script will wait up to 12h.
# This could make the TrueNAS auto-upgrade works the same way as Linux systemd timer.


RANDOM_SLEEP=$(( $(dd if=/dev/random bs=2 count=1 2> /dev/null | cksum | cut -d' ' -f1) % 43200 ))

sleep ${RANDOM_SLEEP}
exec service glouton upgrade
