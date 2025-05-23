#!/bin/sh

getent group glouton >/dev/null || groupadd -r glouton

if ! getent passwd glouton > /dev/null; then
    useradd -r -g glouton -d /var/lib/glouton -s /sbin/nologin \
        -c "Glouton daemon" glouton
fi

# Glouton need access to Docker socket, so it need to create
# the docker group to avoid requiring to run as root.
if ! getent group docker >/dev/null; then
    groupadd -r docker
    if [ -e /var/run/docker.sock -a "`stat -c %G /var/run/docker.sock 2>/dev/null`" = "root" ]; then
        chgrp docker /var/run/docker.sock
    fi
fi

# If jmxtrans is installed, Glouton need to be able to write a jmxtrans configuration
if [ -e /var/lib/jmxtrans -a ! -e /var/lib/jmxtrans/glouton-generated.json ]; then
    if getent group jmxtrans >/dev/null; then
        echo '{"servers":[]}' > /var/lib/jmxtrans/glouton-generated.json
        chown glouton:jmxtrans /var/lib/jmxtrans/glouton-generated.json
        chmod 0640 /var/lib/jmxtrans/glouton-generated.json
    fi
fi

usermod -aG docker glouton 2> /dev/null || true

if [ -e /var/lib/bleemeo/state.json -a ! -e /var/lib/glouton/state.json ]; then
    echo "Migrating from bleemeo-agent to Glouton"
    echo "Stopping bleemeo-agent and copying state.json from bleemeo-agent to Glouton"
    touch /var/lib/bleemeo/upgrade
    systemctl stop bleemeo-agent.service
    systemctl stop telegraf.service 2> /dev/null
    cp -a /var/lib/bleemeo/state.json /var/lib/glouton/state.json
    chown glouton:glouton /var/lib/glouton/state.json
    for filename in `ls /etc/bleemeo/agent.conf.d`; do
        if [ "$filename" = "zzz-disabled-by-glouton.conf" ]; then
            continue
        fi
        if [ "$filename" = "32-graphite_metrics_source.conf" -o "$filename" = "30-graphite_metrics_source.conf" ]; then
            continue
        fi
        if [ ! -e "/etc/glouton/conf.d/$filename" ]; then
            echo "Copy config $filename"
            cp -a "/etc/bleemeo/agent.conf.d/$filename" "/etc/glouton/conf.d/$filename"
            if [ "`stat -c %U "/etc/glouton/conf.d/$filename"`" = "bleemeo" ]; then
                chown glouton "/etc/glouton/conf.d/$filename"
            fi
            if [ "`stat -c %G "/etc/glouton/conf.d/$filename"`" = "bleemeo" ]; then
                chgrp glouton "/etc/glouton/conf.d/$filename"
            fi
        fi
    done
    # We only want to migrate /etc/bleemeo/agent.conf if it was modified.
    if [ -e /usr/bin/dpkg ]; then
        # The space after filename in the grep are important to match agent.conf and not agent.conf.d
        PACKAGE_MD5=`dpkg-query --show --showformat='${Conffiles}\n' bleemeo-agent 2>/dev/null | grep '/etc/bleemeo/agent.conf ' | awk '{print $2}'`
        CURRENT_MD5=`md5sum /etc/bleemeo/agent.conf 2> /dev/null | awk '{print $1}'`
        if [ ! -z "${PACKAGE_MD5}" -a ! -z "${CURRENT_MD5}" -a "${PACKAGE_MD5}" != "${CURRENT_MD5}" ]; then
            echo "Copy config /etc/bleemeo/agent.conf"
            cp /etc/bleemeo/agent.conf /etc/glouton/glouton.conf
        fi
    elif [ -e /usr/bin/rpm ]; then
        # rpm --verify only print line for file modified
        # the "grep 5" match line with bad md5sum
        # the if will check the result code of the last grep. If true the line was found so
        # file content (md5sum missmatch) was modified.
        if rpm --verify bleemeo-agent 2> /dev/null | grep 5 | grep --quiet /etc/bleemeo/agent.conf$; then
            echo "Copy config /etc/bleemeo/agent.conf"
            cp /etc/bleemeo/agent.conf /etc/glouton/glouton.conf
        fi
    fi
    cat > /etc/bleemeo/agent.conf.d/zzz-disabled-by-glouton.conf << EOF
        # Bleemeo agent is disabled and is replaced by Glouton.
        # If you want to rollback from Glouton to Bleemeo-agent:
        # * uninstall glouton
        # * remove this file
        # * restart bleemeo-agent
        bleemeo:
          enabled: false
        web:
          enabled: false
        telegraf:
          statsd:
            enabled: false
        influxdb:
          enabled: False
EOF
    cat > /etc/telegraf/telegraf.d/bleemeo-generated.conf << EOF
# Configuration generated by Bleemeo-agent.
# do NOT modify, it will be overwrite on next agent start.
#
# File emptied by Glouton to disable Telegraf statsd listener.
# Telegraf is no longer needed for Glouton and could be uninstalled.
EOF
fi

# Create Fluent Bit directories and initial configuration.
mkdir -p /var/lib/glouton/fluent-bit/db
mkdir -p /var/lib/glouton/fluent-bit/config
touch /var/lib/glouton/fluent-bit/config/fluent-bit.conf

chown glouton:glouton -R /var/lib/glouton

if [ -d /etc/init ]; then
   cat > /etc/init/glouton.conf <<EOF
# glouton - Monitoring agent for Bleemeo solution

description     "Glouton"

start on runlevel [2345]
stop on runlevel [!2345]

respawn
respawn limit 10 5
umask 022

setuid glouton
exec /usr/sbin/glouton

EOF
fi

if [ -e /etc/sudoers.d/glouton ]; then
   chmod 0440 /etc/sudoers.d/glouton
fi

if [ -e /etc/glouton/conf.d/30-install.conf ]; then
    chown glouton:glouton /etc/glouton/conf.d/30-install.conf
    chmod 0640 /etc/glouton/conf.d/30-install.conf
fi

# Retrive fact that needs root privilege
glouton-gather-facts 2> /dev/null
# Retrive netstat that also needs root privilege
glouton-netstat 2> /dev/null


if [ "$1" = "configure" ] ; then
    # Installation or upgrade on Debian-like system
    test -e /lib/init/upstart-job && start --quiet glouton

    if [ -d /run/systemd/system ]; then
        systemctl daemon-reload

        if deb-systemd-helper --quiet was-enabled 'glouton.service'; then
            deb-systemd-helper enable 'glouton.service' >/dev/null || true
        fi

        if deb-systemd-helper --quiet was-enabled glouton.service; then
            deb-systemd-invoke restart glouton.service
        fi
    fi


    # Glouton version before 20.09.14.12xxxx had the cron.hourly/glouton script not
    # marked as executable. Fix it.
    # We only need to fix on upgrade from older version, because fresh install use permission
    # from package. It's only upgrade that kept permission from filesystem.
    # (RPM based don't have this behavior and always use permission from package).
    if dpkg --compare-versions "$2" lt 20.09.14.120000; then
        chmod +x /etc/cron.hourly/glouton
    fi
elif [ "$1" = "1" ] ; then
    # Initial installation on rpm-like system
    test -x /usr/bin/systemctl -o -x /bin/systemctl && systemctl daemon-reload
    test -x /usr/bin/systemctl -o -x /bin/systemctl && systemctl enable --quiet glouton.service
    test -x /usr/bin/systemctl -o -x /bin/systemctl && systemctl start --quiet glouton.service
fi

exit 0
