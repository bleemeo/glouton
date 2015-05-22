from bleemeo_agent.plugins import base
import bleemeo_agent.util


class Disk(base.PluginV1Base):

    def dependencies_present(self):
        return bleemeo_agent.util.package_installed('nagios-plugins')

    def collectd_configure(self):
        return ""

    def canonical_metric_name(self, name):
        pass

    def list_checks(self):
        return [(
            'disk usage',
            ('/usr/lib/nagios/plugins/check_disk -w 10% -c 5% -l '
                '-X tmpfs -X devtmpfs'),
            None)]
