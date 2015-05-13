import ConfigParser
import io
import subprocess

from bleemeo_agent.plugins import base
import bleemeo_agent.util


class MySQL(base.PluginV1Base):

    def dependencies_present(self):
        installed = bleemeo_agent.util.package_installed('mysql-server')
        if not installed:
            return False

        debian_cnf_raw = subprocess.check_output(
                ['sudo', '--non-interactive', 'cat', '/etc/mysql/debian.cnf'],
        )
        debian_cnf = ConfigParser.SafeConfigParser()
        debian_cnf.readfp(io.BytesIO(debian_cnf_raw))

        self.mysql_socket = debian_cnf.get('client', 'socket')
        self.mysql_user = debian_cnf.get('client', 'user')
        self.mysql_password = debian_cnf.get('client', 'password')
        return installed

    def collectd_configure(self):

        return """
LoadPlugin mysql
<Plugin mysql>
    <Database bleemeo>
        Host "localhost"
        Socket "{socket}"
        User "{user}"
        Password "{password}"
    </Database>
</Plugin>
""".format(
        socket=self.mysql_socket,
        user=self.mysql_user,
        password=self.mysql_password,
    )

    def canonical_metric_name(self, name):
        if name.startswith('mysql-bleemeo.'):
            return name.replace('mysql-bleemeo.', 'mysql-server.')

    def list_checks(self):
        return [(
            'MySQL database',
            "/usr/lib/nagios/plugins/check_mysql -u '%s' -p '%s'" %
                (self.mysql_user, self.mysql_password),
            3306)]
