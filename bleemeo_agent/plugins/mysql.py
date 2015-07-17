import io
import subprocess

from six.moves import configparser

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
        debian_cnf = configparser.SafeConfigParser()
        debian_cnf.readfp(io.StringIO(debian_cnf_raw.decode('utf-8')))

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

    def collectd_rename_metric(self, name, timestamp, value):
        if not name.startswith('mysql-bleemeo.'):
            return None

        name = name[len('mysql-bleemeo.'):]
        if not name.startswith('mysql_'):
            name = 'mysql_' + name

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
            'mysql-database',
            'Check that MySQL is alive',
            "/usr/lib/nagios/plugins/check_mysql -u '%s' -p '%s'" % (
                self.mysql_user, self.mysql_password),
            3306)]
