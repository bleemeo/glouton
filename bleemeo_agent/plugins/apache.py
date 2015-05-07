import subprocess

from bleemeo_agent.plugins import base


class Apache(base.PluginV1Base):

    def dependencies_present(self):
        try:
            output = subprocess.check_output(
                ['dpkg-query', '--show', '--showformat=${Status}',
                    'apache2'],
                stderr=subprocess.STDOUT,
            )
            apache_installed = output.startswith('install')
        except subprocess.CalledProcessError:
            apache_installed = False

        return apache_installed

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
