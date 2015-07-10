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

    def collectd_rename_metric(self, name, timestamp, value):

        if name == 'ntpd.time_offset-loop':
            name = 'ntp_time_offset'
        elif name == 'ntpd.frequency_offset-loop':
            name = 'ntp_frequency_offset'
        else:
            return None

        return {
            'measurement': name,
            'time': timestamp,
            'fields': {
                'value': value,
            },
            'tags': {},
        }

    def list_checks(self):
        return [(
            'NTP server',
            '/usr/lib/nagios/plugins/check_ntp -H localhost -w 0.5 -c 1',
            None)]
