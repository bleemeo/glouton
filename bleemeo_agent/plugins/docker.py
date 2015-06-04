import os

from bleemeo_agent.plugins import base
import bleemeo_agent.util


class Docker(base.PluginV1Base):

    def dependencies_present(self):
        has_plugins = bleemeo_agent.util.package_installed(
            'docker-collectd-plugin')
        has_socket = os.path.exists('/var/run/docker.sock')
        return has_plugins and has_socket

    def collectd_configure(self):
        return """
TypesDB "/usr/share/collectd/types.db"
TypesDB "/usr/share/collectd/dockerplugin.db"
LoadPlugin python
<Plugin python>
  ModulePath "/usr/share/collectd"
  Import "dockerplugin"

  <Module dockerplugin>
    BaseURL "unix://var/run/docker.sock"
  </Module>
</Plugin>
"""

    def canonical_metric_name(self, name):
        pass

    def list_checks(self):
        return []
