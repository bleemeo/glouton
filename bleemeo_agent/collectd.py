import logging
import multiprocessing
import os
import re
import shlex
import socket
import subprocess
import threading
import time


BASE_COLLECTD_CONFIG = """# Configuration generated by Bleemeo-agent.
# do NOT modify, it will be overwrite on next agent start.
"""

APACHE_COLLECTD_CONFIG = """
LoadPlugin apache
<Plugin apache>
    <Instance "bleemeo-%(instance)s">
        URL "http://%(address)s:%(port)s/server-status?auto"
    </Instance>
</Plugin>
"""

MYSQL_COLLECTD_CONFIG = """
LoadPlugin mysql
<Plugin mysql>
    <Database "bleemeo-%(instance)s">
        Host "%(address)s"
        User "%(user)s"
        Password "%(password)s"
        ConnectTimeout 2
    </Database>
</Plugin>
"""

# https://collectd.org/wiki/index.php/Naming_schema
# carbon output change "/" in ".".
# Example of metic name:
# cpu.percent-idle
# df-var-lib.df_complex-free
# disk-sda.disk_octets.read
collectd_regex = re.compile(
    r'(?P<plugin>[^-.]+)(-(?P<plugin_instance>[^.]+))?\.'
    r'(?P<type>[^.-]+)([.-](?P<type_instance>.+))?')


class ComputationFail(Exception):
    pass


class MissingMetric(Exception):
    pass


class Collectd(threading.Thread):

    def __init__(self, core):
        super(Collectd, self).__init__()

        self.core = core
        self.computed_metrics_pending = set()
        self.update_discovery()

    def run(self):
        bind_address = self.core.config.get(
            'collectd.graphite_listener.address', '127.0.0.1')
        bind_port = self.core.config.get(
            'collectd.graphite_listener.port', 2003)
        sock_server = socket.socket()
        sock_server.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        sock_server.bind((bind_address, bind_port))
        sock_server.listen(5)
        sock_server.settimeout(1)

        clients = []
        while not self.core.is_terminating.is_set():
            try:
                (sock_client, addr) = sock_server.accept()
                t = threading.Thread(
                    target=self.process_client,
                    args=(sock_client, addr))
                t.start()
                clients.append(t)
            except socket.timeout:
                pass

        sock_server.close()
        [x.join() for x in clients]

    def update_discovery(self):
        try:
            self._write_config()
        except:
            logging.warning(
                'Failed to write collectd configuration. '
                'Continuing with current configuration')
            logging.debug('exception is:', exc_info=True)

    def _get_collectd_config(self):
        collectd_config = BASE_COLLECTD_CONFIG
        for key, service_info in self.core.discovered_services.items():
            (service_name, instance) = key

            service_info = service_info.copy()
            service_info['instance'] = instance
            if service_name == 'apache':
                collectd_config += APACHE_COLLECTD_CONFIG % service_info
            if service_name == 'mysql':
                collectd_config += MYSQL_COLLECTD_CONFIG % service_info

        return collectd_config

    def _write_config(self):
        collectd_config = self._get_collectd_config()

        collectd_config_path = self.core.config.get(
            'collectd.config_file',
            '/etc/collectd/collectd.conf.d/bleemeo-generated.conf'
        )

        if os.path.exists(collectd_config_path):
            with open(collectd_config_path) as fd:
                current_content = fd.read()

            if collectd_config == current_content:
                logging.debug('collectd already configured')
                return

        if (collectd_config == BASE_COLLECTD_CONFIG
                and not os.path.exists(collectd_config_path)):
            logging.debug(
                'collectd generated config would be empty, skip writting it'
            )
            return

        # Don't simply use open. This file must have limited permission
        # since it may contains password
        open_flags = os.O_WRONLY | os.O_CREAT | os.O_TRUNC
        fileno = os.open(collectd_config_path, open_flags, 0o600)
        with os.fdopen(fileno, 'w') as fd:
            fd.write(collectd_config)

        self._restart_collectd()

    def _restart_collectd(self):
        restart_cmd = self.core.config.get(
            'collectd.restart_command',
            'sudo --non-interactive service collectd restart')
        if restart_cmd == 'docker':
            # use docker stop/start to restart the collectd container
            self.core.docker_client.stop('collectd')
            for _ in range(10):
                time.sleep(0.2)
                container_info = self.core.docker_client.inspect_container(
                    'collectd'
                )
                running = container_info['State']['Running']
                if not running:
                    break
            if running:
                logging.info(
                    'Collectd container still running... restart may fail')
            self.core.docker_client.start('collectd')
        else:
            try:
                output = subprocess.check_output(
                    shlex.split(restart_cmd),
                    stderr=subprocess.STDOUT,
                )
                return_code = 0
            except subprocess.CalledProcessError as e:
                output = e.output
                return_code = e.returncode

            if return_code != 0:
                logging.info(
                    'Failed to restart collectd after reconfiguration : %s',
                    output
                )
            else:
                logging.debug(
                    'collectd reconfigured and restarted : %s', output)

    def process_client(self, sock_client, addr):
        logging.debug('collectd: client connectd from %s', addr)

        try:
            self.process_client_inner(sock_client, addr)
        finally:
            sock_client.close()
            logging.debug('collectd: client %s disconnectd', addr)

    def process_client_inner(self, sock_client, addr):
        remain = b''
        sock_client.settimeout(1)
        last_timestamp = 0
        while not self.core.is_terminating.is_set():
            try:
                tmp = sock_client.recv(4096)
            except socket.timeout:
                continue

            if tmp == b'':
                break

            lines = (remain + tmp).split(b'\n')
            remain = b''

            if lines[-1] != b'':
                remain = lines[-1]

            # either it's '' or we moved it to remain.
            del lines[-1]

            for line in lines:
                # inspired from graphite project : lib/carbon/protocols.py
                metric, value, timestamp = line.split()
                (timestamp, value) = (float(timestamp), float(value))

                if timestamp - last_timestamp > 1:
                    # Collectd send us the next "wave" of measure.
                    # Be sure computed metrics of previous one are
                    # done.
                    self._check_computed_metrics()
                last_timestamp = timestamp

                metric = metric.decode('utf-8')
                # the first component is the hostname
                metric = metric.split('.', 1)[1]

                self.emit_metric(metric, timestamp, value)

            self._check_computed_metrics()

    def network_interface_blacklist(self, if_name):
        for pattern in self.core.config.get('network_interface_blacklist', []):
            if if_name.startswith(pattern):
                return True
        return False

    def _check_computed_metrics(self):
        """ Some metric are computed from other one. For example CPU stats
            are aggregated over all CPUs.

            When any cpu state arrive, we flag the aggregate value as "pending"
            and this function check if stats for all CPU core are fresh enough
            to compute the aggregate.

            This function use self.computed_metrics_pending, which old a list
            of (metric_name, item, timestamp).
            Item is something like "sda", "sdb" or "eth0", "eth1".
        """
        processed = set()
        for entry in self.computed_metrics_pending:
            (name, item, timestamp) = entry
            try:
                self._compute_metric(name, item, timestamp)
                processed.add(entry)
            except ComputationFail:
                logging.debug(
                    'Failed to compute metric %s at time %s',
                    name, timestamp)
                # we will never be able to recompute it.
                # mark it as done and continue :/
                processed.add(entry)
            except MissingMetric:
                # Some metric are missing to do computing. Wait a bit by
                # keeping this entry in self.computed_metrics_pending
                pass

        self.computed_metrics_pending.difference_update(processed)

    def _compute_metric(self, name, item, timestamp):  # NOQA
        def get_metric(measurements, searched_item):
            """ Helper that do common task when retriving metrics:

                * check that metric exists and is not too old
                  (or Raise MissingMetric)
                * If the last metric is more recent that the one we want
                  to compute, raise ComputationFail. We will never be
                  able to compute the requested value.
            """
            metric = self.core.get_last_metric(measurements, searched_item)
            if metric is None or metric['time'] < timestamp:
                raise MissingMetric()
            elif metric['time'] > timestamp:
                raise ComputationFail()
            return metric['value']

        if name == 'disk_total':
            used = get_metric('disk_used', item)
            value = used + get_metric('disk_free', item)
            # used_perc could be more that 100% is reserved space is used.
            # We limit it to 100% (105% would be confusing).
            used_perc = min(float(used) / value * 100, 100)

            # But still, total will including reserved space
            value += get_metric('disk_reserved', item)

            self.core.emit_metric({
                'measurement': name.replace('_total', '_used_perc'),
                'time': timestamp,
                'item': item,
                'status': None,
                'service': None,
                'value': used_perc,
            })
        elif name == 'io_utilisation':
            io_time = get_metric('io_time', item)
            # io_time is a number of ms spent doing IO (per seconds)
            # utilisation is 100% when we spent 1000ms during one seconds
            value = io_time / 1000. * 100.
        elif name == 'mem_total':
            used = get_metric('mem_used', item)
            value = used
            for sub_type in ('buffered', 'cached', 'free'):
                value += get_metric('mem_%s' % sub_type, item)
        elif name == 'process_total':
            types = [
                'blocked', 'paging', 'running', 'sleeping',
                'stopped', 'zombies',
            ]
            value = 0
            for sub_type in types:
                value += get_metric('process_status_%s' % sub_type, item)
        elif name == 'swap_total':
            used = get_metric('swap_used', item)
            value = used + get_metric('swap_free', item)
        else:
            logging.debug('Unknown computed metric %s', name)
            return

        if name in ('mem_total', 'swap_total'):
            self.core.emit_metric({
                'measurement': name.replace('_total', '_used_perc'),
                'time': timestamp,
                'item': None,
                'status': None,
                'service': None,
                'value': float(used) / value * 100,
            })

        self.core.emit_metric({
            'measurement': name,
            'time': timestamp,
            'item': item,
            'status': None,
            'service': None,
            'value': value,
        })

    def emit_metric(self, name, timestamp, value):
        """ Rename a metric and pass it to core
        """
        (metric, no_emit) = self._rename_metric(name, timestamp, value)
        if metric is not None:
            self.core.emit_metric(metric, no_emit=no_emit)

    def _rename_metric(self, name, timestamp, value):  # NOQA
        """ Process metric to rename it and add item

            If the metric is used to compute a derrived metric, add it to
            computed_metrics_pending.

            Return None if metric is unknown
        """
        match = collectd_regex.match(name)
        if match is None:
            return None
        match_dict = match.groupdict()

        no_emit = False
        item = None
        service = None

        if match_dict['plugin'] == 'cpu':
            cores = multiprocessing.cpu_count()
            name = 'cpu_%s' % match_dict['type_instance']
            value = value * cores
        elif match_dict['type'] == 'df_complex':
            name = 'disk_%s' % match_dict['type_instance']
            path = match_dict['plugin_instance']
            if path == 'root':
                path = '/'
            else:
                path = '/' + path.replace('-', '/')
            path = self._disk_path_rename(path)
            if path is None:
                # this partition is ignored
                return (None, None)

            item = path
            self.computed_metrics_pending.add(('disk_total', item, timestamp))
        elif match_dict['plugin'] == 'disk':
            if match_dict['type_instance'] == 'io_time':
                name = 'io_time'
            elif match_dict['type_instance'] == 'weighted_io_time':
                name = 'io_time_weighted'
            elif match_dict['type'] == 'pending_operations':
                name = 'io_inprogress'
            else:
                kind_name = {
                    'disk_merged': '_merged',
                    'disk_octets': '_bytes',
                    'disk_ops': 's',  # will become readS and writeS
                    'disk_time': '_time',
                }[match_dict['type']]
                name = 'io_%s%s' % (match_dict['type_instance'], kind_name)

            item = match_dict['plugin_instance']
            if self._ignored_disk(item):
                return (None, None)
            if name == 'io_time':
                self.computed_metrics_pending.add(
                    ('io_utilisation', item, timestamp))
        elif match_dict['plugin'] == 'interface':
            kind_name = {
                'if_errors': 'err',
                'if_octets': 'bytes',
                'if_packets': 'packets',
            }[match_dict['type']]

            if match_dict['type_instance'] == 'rx':
                direction = 'recv'
            else:
                direction = 'sent'

            item = match_dict['plugin_instance']
            if self.network_interface_blacklist(item):
                return (None, None)

            # Special cases:
            # * if it's some error, we use "in" and "out"
            # * for bytes, we need to convert it to bits
            if kind_name == 'err':
                direction = (
                    direction
                    .replace('recv', 'in')
                    .replace('sent', 'out')
                )
            elif kind_name == 'bytes':
                kind_name = 'bits'
                value = value * 8

            name = 'net_%s_%s' % (kind_name, direction)
        elif match_dict['plugin'] == 'load':
            duration = {
                'longterm': 15,
                'midterm': 5,
                'shortterm': 1,
            }[match_dict['type_instance']]
            name = 'system_load%s' % duration
        elif match_dict['plugin'] == 'memory':
            name = 'mem_%s' % match_dict['type_instance']
            self.computed_metrics_pending.add(('mem_total', None, timestamp))
        elif (match_dict['plugin'] == 'processes'
                and match_dict['type'] == 'fork_rate'):
            name = 'process_fork_rate'
        elif (match_dict['plugin'] == 'processes'
                and match_dict['type'] == 'ps_state'):
            name = 'process_status_%s' % match_dict['type_instance']
            self.computed_metrics_pending.add(
                ('process_total', None, timestamp))
        elif match_dict['plugin'] == 'swap' and match_dict['type'] == 'swap':
            name = 'swap_%s' % match_dict['type_instance']
            self.computed_metrics_pending.add(('swap_total', None, timestamp))
        elif (match_dict['plugin'] == 'swap'
                and match_dict['type'] == 'swap_io'):
            name = 'swap_%s' % match_dict['type_instance']
        elif match_dict['plugin'] == 'users':
            name = 'users_logged'
        elif (match_dict['plugin'] == 'apache'
                and match_dict['plugin_instance'].startswith('bleemeo-')):
            name = match_dict['type']
            if match_dict['type_instance']:
                name += '.' + match_dict['type_instance']

            item = match_dict['plugin_instance'].replace('bleemeo-', '')
            service = 'apache'
        elif (match_dict['plugin'] == 'mysql'
                and match_dict['plugin_instance'].startswith('bleemeo-')):
            name = match_dict['type']
            if match_dict['type_instance']:
                name += '.' + match_dict['type_instance']

            if not name.startswith('mysql_'):
                name = 'mysql_' + name

            item = match_dict['plugin_instance'].replace('bleemeo-', '')

            service = 'mysql'
        else:
            return (None, None)

        return ({
            'measurement': name,
            'time': timestamp,
            'value': value,
            'item': item,
            'service': service,
            'status': None,
        }, no_emit)

    def _disk_path_rename(self, path):
        """ Rename (and possibly ignore) a disk partition

            In case of collectd running in a container, it's used to show
            partition as seen by the host, instead of as seen by a container.
        """
        ignored_patterns = self.core.config.get('df.path_ignore', [])
        for pattern in ignored_patterns:
            if path.startswith(pattern):
                return None

        mount_point = self.core.config.get('df.host_mount_point')
        if mount_point is None:
            return path

        if not path.startswith(mount_point):
            # partition don't start with mount_point, so it's a parition
            # which is only inside the container. Ignore it
            return None

        path = path.replace(mount_point, '')
        if not path.startswith('/'):
            path = '/' + path

        return path

    def _ignored_disk(self, disk):
        """ Tell if disk should be monitored. It avoid monitoring sda1 or
            dm-1
        """
        for pattern in self.core.config.get('disk_monitor', []):
            if re.match(pattern, disk):
                return False

        return True
