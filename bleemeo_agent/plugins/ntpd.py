from bleemeo_agent.plugins import base
import bleemeo_agent.util


class Ntpd(base.PluginV1Base):

    def dependencies_present(self):
        return bleemeo_agent.util.package_installed('ntp')

    def collectd_configure(self):
        return """
LoadPlugin ntpd
<Plugin ntpd>
</Plugin>
"""

    def canonical_metric_name(self, name):
        pass

    def list_checks(self):
        return [(
            'NTP server',
            '/usr/lib/nagios/plugins/check_ntp -H localhost -w 0.5 -c 1',
            None)]
