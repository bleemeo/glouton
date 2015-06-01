import logging

import requests

from bleemeo_agent.plugins import base
import bleemeo_agent.util


class Nginx(base.PluginV1Base):

    def dependencies_present(self):
        has_package = bleemeo_agent.util.package_installed('nginx')
        if not has_package:
            return False

        # Test if /server-status works
        try:
            response = requests.get('http://localhost/server-status')
        except requests.exceptions.RequestException:
            response = None

        if response is not None and response.status_code == 200:
            return True
        else:
            logging.info(
                'Please enable stub_status for Nginx. See '
                'http://doc.bleemeo.com/....')
            return False

    def collectd_configure(self):
        return """
LoadPlugin nginx
<Plugin nginx>
    URL "http://localhost/server-status"
</Plugin>
"""

    def canonical_metric_name(self, name):
        if name.startswith('nginx.'):
            return name.replace('nginx.', 'httpd-server.')

    def list_checks(self):
        return [(
            'Nginx webserver',
            '/usr/lib/nagios/plugins/check_http -H localhost',
            80)]
