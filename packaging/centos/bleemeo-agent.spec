%define version 0.1
%define git_commit unknown
%define build_date Thu Jan 01 1970

%{?systemd_requires}

Name:           bleemeo-agent
Version:        %{version}
Release:        1%{?dist}
Summary:        Bleemeo agent

Source0:        bleemeo-agent_%{version}.tar
BuildArch:      noarch
BuildRequires:  python36-devel, python36-setuptools >= 30.3.0
BuildRequires:  systemd

Requires(pre):  shadow-utils
Requires:       python36-psutil
Requires:       python36-requests
Requires:       python36-paho-mqtt
Requires:       net-tools
Requires:       ca-certificates
Requires:       sudo
Requires:       python36-docker
Requires:       python36-APScheduler
Requires:       python36-jinja2
Requires:       python36-six
Requires:       python36-PyYAML
Requires:       python36-setuptools
Requires:       bleemeo-agent-collector
Requires:       yum-plugin-post-transaction-actions
#Recommends not available on centos 7
#Recommends:     python34-flask
#Recommends:     python34-influxdb
#Recommends:     python34-raven


License:        ASL 2.0
URL:            https://bleemeo.com

%description
Bleemeo is a solution of Monitoring as a Service.
This package contains the agent which send metric to
the SaaS platform

%package telegraf
Summary:        Bleemeo agent with Telegraf
Requires:       telegraf >= 1.7
Provides:       bleemeo-agent-collector = %{version}
Conflicts:      bleemeo-agent-collectd, bleemeo-agent-single

%description telegraf
Bleemeo is a solution of Monitoring as a Service.
This package contains the agent which send metric to
the SaaS platform using Telegraf

%package single
Summary:        Bleemeo agent for Docker images
Provides:       bleemeo-agent-collector = %{version}
Conflicts:      bleemeo-agent-telegraf, bleemeo-agent-collectd

%description single
Bleemeo is a solution of Monitoring as a Service.
This package contains the agent which send metric to
the SaaS platform with no dependency on daemon.
This package is appropriate for Docker images.

%package jmx
Summary:        Bleemeo agent plugin for JMX
Requires:       bleemeo-agent
Requires:       jmxtrans

%description jmx
Bleemeo is a solution of Monitoring as a Service.
This package contains the agent which send metric to
the SaaS platform.
This package contains part needed to monitor JMX
metrics.

%prep
%autosetup

%build
%py3_build

%install
%py3_install

install -D -p -m 0440 packaging/centos/bleemeo-agent.sudoers %{buildroot}%{_sysconfdir}/sudoers.d/bleemeo
install -D -p -m 0644 packaging/common/bleemeo-05-system.conf %{buildroot}%{_sysconfdir}/bleemeo/agent.conf.d/05-system.conf
install -D -p -m 0644 packaging/centos/bleemeo-06-distribution.conf %{buildroot}%{_sysconfdir}/bleemeo/agent.conf.d/06-distribution.conf
install -D -p -m 0644 etc/agent.conf %{buildroot}%{_sysconfdir}/bleemeo/agent.conf
install -D -p -m 0644 debian/bleemeo-agent.service %{buildroot}%{_unitdir}/%{name}.service
install -D -d -m 0755 %{buildroot}%{_sharedstatedir}/bleemeo
install -D -p -m 0755 packaging/common/bleemeo-hook-package-modified %{buildroot}%{_prefix}/lib/bleemeo/bleemeo-hook-package-modified
install -D -p -m 0644 packaging/centos/bleemeo.action %{buildroot}%{_sysconfdir}/yum/post-actions/bleemeo.action
install -D -p -m 0755 debian/bleemeo-agent.cron.hourly %{buildroot}%{_sysconfdir}/cron.hourly/bleemeo-agent

# -telegraf
install -D -p -m 0644 packaging/common/telegraf.conf %{buildroot}%{_sysconfdir}/telegraf/telegraf.d/bleemeo.conf
install -D -p -m 0644 packaging/common/telegraf-generated.conf %{buildroot}%{_sysconfdir}/telegraf/telegraf.d/bleemeo-generated.conf
install -D -p -m 0644 packaging/common/bleemeo-telegraf-graphite_metrics_source.conf %{buildroot}%{_sysconfdir}/bleemeo/agent.conf.d/32-graphite_metrics_source.conf

# -jmx
install -D -p -m 0644 packaging/centos/bleemeo-agent-jmx.service %{buildroot}%{_unitdir}/%{name}-jmx.service
install -D -p -m 0640 packaging/common/jmxtrans-bleemeo-generated.json %{buildroot}%{_sharedstatedir}/jmxtrans/bleemeo-generated.json
install -D -p -m 0755 debian/bleemeo-agent-jmx.cron.daily %{buildroot}%{_sysconfdir}/cron.daily/bleemeo-agent-jmx

%files
%{python3_sitelib}/*
%{_bindir}/bleemeo-agent
%{_bindir}/bleemeo-agent-gather-facts
%{_bindir}/bleemeo-netstat
%config(noreplace) %{_sysconfdir}/bleemeo/agent.conf
%config(noreplace) %{_sysconfdir}/bleemeo/agent.conf.d/05-system.conf
%config(noreplace) %{_sysconfdir}/bleemeo/agent.conf.d/06-distribution.conf
%config(noreplace) %{_sysconfdir}/sudoers.d/*
%config(noreplace) %{_sysconfdir}/yum/post-actions/bleemeo.action
%config(noreplace) %{_sysconfdir}/cron.hourly/bleemeo-agent
%{_unitdir}/%{name}.service
%{_sharedstatedir}/bleemeo
%{_prefix}/lib/bleemeo/

%files telegraf
%config(noreplace) %{_sysconfdir}/telegraf/telegraf.d/bleemeo.conf
%config(noreplace) %{_sysconfdir}/telegraf/telegraf.d/bleemeo-generated.conf
%config(noreplace) %{_sysconfdir}/bleemeo/agent.conf.d/32-graphite_metrics_source.conf

%files single

%files jmx
%{_sharedstatedir}/jmxtrans/bleemeo-generated.json
%config(noreplace) %{_sysconfdir}/cron.daily/bleemeo-agent-jmx
%{_unitdir}/%{name}-jmx.service

%pre
getent group bleemeo >/dev/null || groupadd -r bleemeo
getent passwd bleemeo >/dev/null || \
    useradd -r -g bleemeo -d /var/lib/bleemeo -s /sbin/nologin \
    -c "Bleemeo agent daemon" bleemeo
usermod -aG docker bleemeo 2> /dev/null || true
exit 0

%post
chown bleemeo:bleemeo /var/lib/bleemeo
if [ -e /etc/bleemeo/agent.conf.d/30-install.conf ]; then
    chown bleemeo:bleemeo /etc/bleemeo/agent.conf.d/30-install.conf
    chmod 0640 /etc/bleemeo/agent.conf.d/30-install.conf
fi
# Retrive fact that needs root privilege
bleemeo-agent-gather-facts
# Retrive netstat that also needs root privilege
bleemeo-netstat

# Bleemeo agent need access to Docker socket, so it need to create
# the docker group to avoid requiring to run as root.
if ! getent group docker >/dev/null; then
    groupadd -r docker
    if [ -e /var/run/docker.sock -a `stat -c %G /var/run/docker.sock` = "root" ]; then
        chgrp docker /var/run/docker.sock
    fi
    /usr/lib/bleemeo/bleemeo-hook-package-modified > /dev/null 2>&1
fi

if [ $1 -eq 1 ] ; then
    # Initial installation
    systemctl enable --quiet --now bleemeo-agent.service
fi

%preun
if [ "$1" -eq 1 ]; then
    # upgrade
    touch /var/lib/bleemeo/upgrade
fi
%systemd_preun bleemeo-agent.service

%postun
%systemd_postun_with_restart bleemeo-agent.service

%pre telegraf
getent group bleemeo >/dev/null || groupadd -r bleemeo
getent passwd bleemeo >/dev/null || \
    useradd -r -g bleemeo -d /var/lib/bleemeo -s /sbin/nologin \
    -c "Bleemeo agent daemon" bleemeo
usermod -aG docker telegraf 2> /dev/null || true
exit 0

%post telegraf
chown bleemeo:telegraf /etc/telegraf/telegraf.d/bleemeo-generated.conf
chmod 0640 /etc/telegraf/telegraf.d/bleemeo-generated.conf

# Bleemeo agent modify telegraf configuration.
systemctl reset-failed telegraf.service || true
systemctl restart telegraf.service 2>/dev/null

if [ $1 -eq 1 ] ; then
    # Bleemeo agent telegraf modify its configuration.
    # On first installation of bleemeo-agent-telegraf, restart the agent
    touch /var/lib/bleemeo/upgrade 2>/dev/null
    systemctl reset-failed bleemeo-agent.service 2>/dev/null || true
    systemctl restart bleemeo-agent.service 2>/dev/null
fi
exit 0

%post jmx
chown bleemeo:jmxtrans /var/lib/jmxtrans/bleemeo-generated.json
chmod 0640 /var/lib/jmxtrans/bleemeo-generated.json
if [ $1 -eq 1 ] ; then
    # Initial installation
    systemctl enable --quiet --now bleemeo-agent-jmx.service
fi
/etc/init.d/jmxtrans start || true

%changelog
* %{build_date} Bleemeo Packaging Team jenkins@bleemeo.com - %{version}
- Build package based on %{git_commit} commit
