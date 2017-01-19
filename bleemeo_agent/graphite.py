#
#  Copyright 2015-2016 Bleemeo
#
#  bleemeo.com an infrastructure monitoring solution in the Cloud
#
#   Licensed under the Apache License, Version 2.0 (the "License");
#   you may not use this file except in compliance with the License.
#   You may obtain a copy of the License at
#
#       http://www.apache.org/licenses/LICENSE-2.0
#
#   Unless required by applicable law or agreed to in writing, software
#   distributed under the License is distributed on an "AS IS" BASIS,
#   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
#   See the License for the specific language governing permissions and
#   limitations under the License.
#

import logging
import os
import re
import shlex
import socket
import threading
import time

import psutil

import bleemeo_agent.collectd
import bleemeo_agent.telegraf
import bleemeo_agent.util


class ComputationFail(Exception):
    pass


class MissingMetric(Exception):
    pass


def graphite_split_line(line):
    """ Split a "graphite" line.

        >>> # 42 is the value, 1000 is the timestamp
        >>> graphite_split_line(b'metric.name 42 1000')
        ('metric.name', 42.0, 1000.0)
    """
    part = shlex.split(line.decode('utf-8'))
    timestamp = part[-1]
    value = part[-2]
    metric = ' '.join(part[0:-2])

    timestamp = float(timestamp)
    try:
        value = float(value)
    except ValueError:
        # assume value is a string, like "20 days, 23:26"
        pass

    return (metric, value, timestamp)


def _disk_path_rename(path, mount_point, ignored_patterns):
    if mount_point is not None:
        if mount_point.endswith('/'):
            mount_point = mount_point[:-1]

        if not path.startswith(mount_point):
            # partition don't start with mount_point, so it's a parition
            # which is only inside the container. Ignore it
            return None

        path = path.replace(mount_point, '')
        if not path.startswith('/'):
            path = '/' + path

    for pattern in ignored_patterns:
        if pattern.endswith('/'):
            pattern = pattern[:-1]

        if path == pattern or path.startswith(pattern + os.sep):
            return None

    return path


class GraphiteServer(threading.Thread):

    def __init__(self, core):
        super(GraphiteServer, self).__init__()

        self.data_last_seen_at = None
        self.core = core
        self.listener_up = False
        self.initialization_done = threading.Event()
        if self.metrics_source == 'collectd':
            self.collectd = bleemeo_agent.collectd.Collectd(self)
        elif self.metrics_source == 'telegraf':
            self.telegraf = bleemeo_agent.telegraf.Telegraf(self)

    @property
    def metrics_source(self):
        return self.core.config.get('graphite.metrics_source', 'telegraf')

    def run(self):
        bind_address = self.core.config.get(
            'graphite.listener.address', '127.0.0.1')
        bind_port = self.core.config.get(
            'graphite.listener.port', 2003)
        sock_server = socket.socket()
        sock_server.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        try:
            sock_server.bind((bind_address, bind_port))
        except socket.error as exc:
            logging.error(
                'Failed to listen on graphite port %s:%s: %s',
                bind_address, bind_port, exc
            )
            self.initialization_done.set()
            return

        sock_server.listen(5)
        sock_server.settimeout(1)
        self.listener_up = True
        self.initialization_done.set()

        clients = []
        while not self.core.is_terminating.is_set():
            try:
                (sock_client, addr) = sock_server.accept()
                client_thread = threading.Thread(
                    target=self.process_client,
                    args=(sock_client, addr))
                client_thread.start()
                clients.append(client_thread)
            except socket.timeout:
                pass

        sock_server.close()
        [x.join() for x in clients]

    def update_discovery(self):
        if self.metrics_source == 'collectd':
            self.collectd.update_discovery()
        elif self.metrics_source == 'telegraf':
            self.telegraf.update_discovery()

    def get_time_elapsed_since_last_data(self):
        clock_now = bleemeo_agent.util.get_clock()
        threshold = self.core.get_threshold('time_elapsed_since_last_data')
        highest_threshold = 0
        if threshold is not None:
            if threshold.get('high_critical') is not None:
                highest_threshold = threshold.get('high_critical')
            elif threshold.get('high_warning') is not None:
                highest_threshold = threshold.get('high_warning')

        if self.data_last_seen_at is not None:
            delay = clock_now - self.data_last_seen_at
        else:
            delay = clock_now - self.core.started_at

        # It only emit the metric if:
        # * either it actually had seen some data (e.g. metric is exact, not
        #   approximated base on agent start date).
        # * or no threshold is defined
        # * or the highest threshold is already crossed
        # It does this to avoid changing state of this metric after an agent
        # restart. E.g. collector is dead: status is critical; user restart
        # agent, status must NOT goes OK, then few minute later critical.
        if (self.data_last_seen_at is None
                and threshold is not None
                and delay < highest_threshold):
            return None

        return {
            'measurement': 'time_elapsed_since_last_data',
            'time': time.time(),
            'value': delay,
        }

    def process_client(self, sock_client, addr):
        logging.debug('graphite: client connectd from %s', addr)

        try:
            self.process_client_inner(sock_client)
        finally:
            sock_client.close()
            logging.debug('graphite: client %s disconnectd', addr)

    def process_client_inner(self, sock_client):  # noqa
        remain = b''
        sock_client.settimeout(1)
        last_timestamp = 0
        computed_metrics_pending = set()
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
                if line == b'':
                    continue

                metric, value, timestamp = graphite_split_line(line)

                if timestamp - last_timestamp > 1:
                    # Collectd send us the next "wave" of measure.
                    # Be sure computed metrics of previous one are
                    # done.
                    self._check_computed_metrics(computed_metrics_pending)
                last_timestamp = timestamp

                self.emit_metric(
                    metric, timestamp, value, computed_metrics_pending)

            self._check_computed_metrics(computed_metrics_pending)

    def network_interface_blacklist(self, if_name):
        for pattern in self.core.config.get('network_interface_blacklist', []):
            if if_name.startswith(pattern):
                return True
        return False

    def _check_computed_metrics(self, computed_metrics_pending):
        """ Some metric are computed from other one. For example CPU stats
            are aggregated over all CPUs.

            When any cpu state arrive, we flag the aggregate value as "pending"
            and this function check if stats for all CPU core are fresh enough
            to compute the aggregate.

            This function use computed_metrics_pending, which old a list
            of (metric_name, item, timestamp).
            Item is something like "sda", "sdb" or "eth0", "eth1".
        """
        processed = set()
        new_item = set()
        for entry in computed_metrics_pending:
            (name, item, instance, timestamp) = entry
            try:
                self._compute_metric(name, item, instance, timestamp, new_item)
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
                # keeping this entry in computed_metrics_pending
                pass

        computed_metrics_pending.difference_update(processed)
        if new_item:
            computed_metrics_pending.update(new_item)
            self._check_computed_metrics(computed_metrics_pending)

    def _compute_metric(self, name, item, instance, timestamp, new_item):  # NOQA
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

        service = None

        if name == 'disk_total' and os.name != 'nt':
            used = get_metric('disk_used', item)
            value = used + get_metric('disk_free', item)
            # used_perc could be more that 100% if reserved space is used.
            # We limit it to 100% (105% would be confusing).
            used_perc = min(float(used) / value * 100, 100)

            # But still, total will including reserved space
            value += get_metric('disk_reserved', item)

            self.core.emit_metric({
                'measurement': name.replace('_total', '_used_perc'),
                'time': timestamp,
                'item': item,
                'value': used_perc,
            })
        elif name == 'disk_total' and os.name:
            used_perc = get_metric('disk_used_perc', item)
            free = get_metric('disk_free', item)
            free_perc = 100 - used_perc
            disk_total = free / (free_perc / 100.0)
            disk_used = disk_total * (used_perc / 100.0)
            value = disk_total
            self.core.emit_metric({
                'measurement': 'disk_used',
                'time': timestamp,
                'item': item,
                'value': disk_used,
            })
        elif name == 'cpu_other':
            value = get_metric('cpu_used', None)
            value -= get_metric('cpu_user', None)
            value -= get_metric('cpu_system', None)
        elif name == 'mem_total':
            used = get_metric('mem_used', item)
            value = used
            for sub_type in ('buffered', 'cached', 'free'):
                value += get_metric('mem_%s' % sub_type, item)
        elif name == 'mem_free':
            used = get_metric('mem_used', item)
            cached = get_metric('mem_cached', item)
            value = self.core.total_memory_size - used - cached
        elif name == 'mem_cached':
            value = 0
            for sub_type in (
                    'Standby_Cache_Reserve_Bytes',
                    'Standby_Cache_Normal_Priority_Bytes',
                    'Standby_Cache_Core_Bytes'):
                value += get_metric(sub_type, item)
            new_item.add(
                ('mem_free', None, None, timestamp)
            )
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
        elif name == 'mem_used':
            total = get_metric('mem_total', None)
            value = total - get_metric('mem_available', None)
            self.core.emit_metric({
                'measurement': 'mem_used_perc',
                'time': timestamp,
                'value': value / total * 100.,
            })
        elif name == 'system_load1':
            # Unix load represent the number of running and runnable tasks
            # which include task waiting for disk IO.

            # To re-create the value on Windows:
            # Number of running task will be number of CPU core * CPU usage
            # Number of runnable tasks will be "Processor Queue Length", but
            # this probably does not include task waiting for disk IO.

            cpu_used = get_metric('cpu_used', None)
            runq = get_metric('Processor_Queue_Length', None)
            core_count = psutil.cpu_count()
            if core_count is None:
                core_count = 1
            value = core_count * (cpu_used / 100.) + runq
        elif name == 'elasticsearch_search_time':
            service = 'elasticsearch'
            total = get_metric('elasticsearch_search_time_total', item)
            count = get_metric('elasticsearch_search', item)
            if count == 0:
                # If not query during the period, the average time
                # has not meaning. Don't emit it at all.
                return
            value = total / count
        else:
            logging.debug('Unknown computed metric %s', name)
            return

        if name in ('mem_total', 'swap_total'):
            if value == 0:
                value_perc = 0.0
            else:
                value_perc = float(used) / value * 100

            self.core.emit_metric({
                'measurement': name.replace('_total', '_used_perc'),
                'time': timestamp,
                'value': value_perc,
            })

        metric = {
            'measurement': name,
            'time': timestamp,
            'value': value,
        }
        if item is not None:
            metric['item'] = item
        if service is not None:
            metric['service'] = service
            metric['instance'] = instance
        self.core.emit_metric(metric)

    def emit_metric(self, name, timestamp, value, computed_metrics_pending):
        """ Rename a metric and pass it to core

            If the metric is used to compute a derrived metric, add it to
            computed_metrics_pending.

            Nothing is emitted if metric is unknown
        """
        if name.startswith('telegraf.') and self.metrics_source == 'telegraf':
            self.data_last_seen_at = bleemeo_agent.util.get_clock()
            self.telegraf.emit_metric(
                name, timestamp, value, computed_metrics_pending,
            )
        elif self.metrics_source == 'collectd':
            self.data_last_seen_at = bleemeo_agent.util.get_clock()
            self.collectd.emit_metric(
                name, timestamp, value, computed_metrics_pending
            )

    def _ignored_disk(self, disk):
        """ Tell if disk should be monitored. It avoid monitoring sda1 or
            dm-1
        """
        for pattern in self.core.config.get('disk_monitor', []):
            if re.match(pattern, disk):
                return False

        return True

    def disk_path_rename(self, path):
        """ Rename (and possibly ignore) a disk partition

            In case of collectd running in a container, it's used to show
            partition as seen by the host, instead of as seen by a container.
        """
        mount_point = self.core.config.get('df.host_mount_point')
        ignored_patterns = self.core.config.get('df.path_ignore', [])

        return _disk_path_rename(path, mount_point, ignored_patterns)
