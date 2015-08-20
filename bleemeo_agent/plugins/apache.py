from bleemeo_agent.plugins import base
import bleemeo_agent.util


class Apache(base.PluginV1Base):

    def dependencies_present(self):
        return bleemeo_agent.util.package_installed('apache2')

    def collectd_configure(self):
        return """
LoadPlugin apache
<Plugin apache>
    <Instance "bleemeo">
        URL "http://localhost/server-status?auto"
    </Instance>
</Plugin>
"""

    def collectd_rename_metric(self, name, timestamp, value):
        if name.startswith('apache-bleemeo.'):
            name = name[len('apache-bleemeo.'):]
            return {
                'measurement': name,
                'time': timestamp,
                'value': value,
                'tags': {},
            }

    def list_checks(self):
        return [(
            'apache-webserver',
            'Check that Apache server is alive',
            '/usr/lib/nagios/plugins/check_http -H localhost',
            80)]
