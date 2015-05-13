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

    def canonical_metric_name(self, name):
        if name.startswith('apache-bleemeo.'):
            return name.replace('apache-bleemeo.', 'httpd-server.')

    def list_checks(self):
        return [(
            'apache webserver',
            '/usr/lib/nagios/plugins/check_http -H localhost',
            80)]
