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
BuildRequires:  python34-devel, python34-setuptools
BuildRequires:  systemd

Requires(pre):  shadow-utils
Requires:       python34-psutil
Requires:       python34-requests
Requires:       python34-paho-mqtt
Requires:       net-tools
Requires:       ca-certificates
Requires:       sudo
Requires:       python34-docker-py
Requires:       python34-APScheduler
Requires:       python34-jinja2
Requires:       python34-six
Requires:       python34-PyYAML
Requires:       python34-setuptools
Requires:       bleemeo-agent-telegraf
#Recommends not available on centos 7
#Recommends:     python34-flask
#Recommends:     python34-influxdb
#Recommends:     python34-raven


License:        Apache
URL:            https://bleemeo.com

%description
Bleemeo is a solution of Monitoring as a Service.
This package contains the agent which send metric to
the SaaS platform

%package -n bleemeo-agent-telegraf
Summary:        Bleemeo agent with Telegraf
Requires:       telegraf

%description -n bleemeo-agent-telegraf
Bleemeo is a solution of Monitoring as a Service.
This package contains the agent which send metric to
the SaaS platform using Telegraf

%prep
%autosetup

%build
%py3_build

%install
%py3_install

rm %{buildroot}/usr/lib/bleemeo/bleemeo-dpkg-hook-postinvoke

install -D -p -m 0440 rpm/bleemeo-agent.sudoers %{buildroot}%{_sysconfdir}/sudoers.d/bleemeo
install -D -p -m 0644 debian/bleemeo-agent.conf %{buildroot}%{_sysconfdir}/bleemeo/agent.conf.d/05-system.conf
install -D -p -m 0644 etc/agent.conf %{buildroot}%{_sysconfdir}/bleemeo/agent.conf
install -D -p -m 0644 debian/bleemeo-agent.service %{buildroot}%{_unitdir}/%{name}.service
install -D -d -m 0755 %{buildroot}%{_sharedstatedir}/bleemeo

install -D -p -m 0644 debian/bleemeo-agent-telegraf.telegraf.conf %{buildroot}%{_sysconfdir}/telegraf/telegraf.d/bleemeo.conf
install -D -p -m 0644 debian/bleemeo-agent-telegraf.telegraf-generated.conf %{buildroot}%{_sysconfdir}/telegraf/telegraf.d/bleemeo-generated.conf
install -D -p -m 0644 debian/bleemeo-agent-telegraf.graphite_metrics_source.conf %{buildroot}%{_sysconfdir}/bleemeo/agent.conf.d/30-graphite_metrics_source.conf

%files
%{python3_sitelib}/*
%{_bindir}/bleemeo-agent
%{_bindir}/bleemeo-agent-gather-facts
%{_bindir}/bleemeo-netstat
%{_sysconfdir}/bleemeo
%{_sysconfdir}/sudoers.d/*
%{_unitdir}/%{name}.service
%{_sharedstatedir}/bleemeo

%files -n bleemeo-agent-telegraf
%{_sysconfdir}/telegraf/telegraf.d/bleemeo.conf
%{_sysconfdir}/telegraf/telegraf.d/bleemeo-generated.conf
%{_sysconfdir}/bleemeo/agent.conf.d/30-graphite_metrics_source.conf

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
if [ $1 -eq 1 ] ; then
    # Initial installation
    systemctl enable --now bleemeo-agent.service
fi

%preun
if [ "$1" -eq 1 ]; then
    # upgrade
    touch /var/lib/bleemeo/upgrade
fi
%systemd_preun bleemeo-agent.service

%postun
%systemd_postun_with_restart bleemeo-agent.service

%pre -n bleemeo-agent-telegraf
getent group bleemeo >/dev/null || groupadd -r bleemeo
getent passwd bleemeo >/dev/null || \
    useradd -r -g bleemeo -d /var/lib/bleemeo -s /sbin/nologin \
    -c "Bleemeo agent daemon" bleemeo
usermod -aG docker bleemeo 2> /dev/null || true
exit 0

%post -n bleemeo-agent-telegraf
chown bleemeo:telegraf /etc/telegraf/telegraf.d/bleemeo-generated.conf
chmod 0640 /etc/telegraf/telegraf.d/bleemeo-generated.conf

# Bleemeo agent modify telegraf configuration.
touch /var/lib/bleemeo/upgrade
systemctl restart telegraf.service

# Bleemeo agent telegraf modify its configuration.
systemctl restart bleemeo-agent.service
exit 0

%changelog
* %{build_date} Bleemeo Packaging Team jenkins@bleemeo.com - %{version}
- Build package based on %{git_commit} commit
