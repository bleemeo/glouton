import logging
import os
import shlex
import subprocess
import time

import requests

import bleemeo_agent.util


BASE_TELEGRAF_CONFIG = """# Configuration generated by Bleemeo-agent.
# do NOT modify, it will be overwrite on next agent start.
"""

APACHE_TELEGRAF_CONFIG = """
[[inputs.apache]]
  urls = ["http://%(address)s:%(port)s/server-status?auto"]
"""

ELASTICSEARCH_TELEGRAF_CONFIG = """
[[inputs.elasticsearch]]
  servers = ["http://%(address)s:%(port)s"]
  local = true
  cluster_health = false
"""

MEMCACHED_TELEGRAF_CONFIG = """
[[inputs.memcached]]
  servers = ["%(address)s:%(port)s"]
"""

MONGODB_TELEGRAF_CONFIG = """
[[inputs.mongodb]]
  servers = ["%(address)s:%(port)s"]
"""

MYSQL_TELEGRAF_CONFIG = """
[[inputs.mysql]]
  servers = ["%(username)s:%(password)s@tcp(%(address)s:%(port)s)/"]
"""

NGINX_TELEGRAF_CONFIG = """
[[inputs.nginx]]
  urls = ["http://%(address)s:%(port)s/nginx_status"]
"""

RABBITMQ_TELEGRAF_CONFIG = """
[[inputs.rabbitmq]]
  url = "http://%(address)s:%(mgmt_port)s"
  username = "%(username)s"
  password = "%(password)s"
"""

REDIS_TELEGRAF_CONFIG = """
[[inputs.redis]]
  servers = ["tcp://%(address)s:%(port)s"]
"""

ZOOKEEPER_CONFIG = """
[[inputs.zookeeper]]
  servers = ["%(address)s:%(port)s"]
"""


class Telegraf:

    def __init__(self, graphite_server):
        self.core = graphite_server.core
        self.graphite_server = graphite_server

        # used to compute derivated values
        self._raw_value = {}

        self.core.scheduler.add_interval_job(
            self._purge_metrics,
            minutes=5,
        )

    def _purge_metrics(self):
        """ Remove old metrics from self._raw_value
        """
        now = time.time()
        cutoff = now - 60 * 6

        # XXX: concurrent access with emit_metric
        self._raw_value = {
            key: (timestamp, value)
            for key, (timestamp, value) in self._raw_value.items()
            if timestamp >= cutoff
        }

    def update_discovery(self):
        try:
            self._write_config()
        except:
            logging.warning(
                'Failed to write telegraf configuration. '
                'Continuing with current configuration')
            logging.debug('exception is:', exc_info=True)

    def _write_config(self):
        telegraf_config = self._get_telegraf_config()

        telegraf_config_path = self.core.config.get(
            'telegraf.config_file',
            '/etc/telegraf/telegraf.d/bleemeo-generated.conf'
        )

        if os.path.exists(telegraf_config_path):
            with open(telegraf_config_path) as fd:
                current_content = fd.read()

            if telegraf_config == current_content:
                logging.debug('telegraf already configured')
                return

        if (telegraf_config == BASE_TELEGRAF_CONFIG
                and not os.path.exists(telegraf_config_path)):
            logging.debug(
                'telegraf generated config would be empty, skip writting it'
            )
            return

        # Don't simply use open. This file must have limited permission
        # since it may contains password
        open_flags = os.O_WRONLY | os.O_CREAT | os.O_TRUNC
        fileno = os.open(telegraf_config_path, open_flags, 0o600)
        with os.fdopen(fileno, 'w') as fd:
            fd.write(telegraf_config)

        self._restart_telegraf()

    def _get_telegraf_config(self):  # noqa
        telegraf_config = BASE_TELEGRAF_CONFIG

        for (key, service_info) in self.core.services.items():
            (service_name, instance) = key

            service_info = self.core.services[key].copy()
            service_info['instance'] = instance

            if service_name == 'apache':
                telegraf_config += APACHE_TELEGRAF_CONFIG % service_info
            if service_name == 'elasticsearch':
                telegraf_config += ELASTICSEARCH_TELEGRAF_CONFIG % service_info
            if service_name == 'memcached':
                telegraf_config += MEMCACHED_TELEGRAF_CONFIG % service_info
            if (service_name == 'mysql'
                    and service_info.get('password') is not None):
                service_info.setdefault('username', 'root')
                telegraf_config += MYSQL_TELEGRAF_CONFIG % service_info
            if service_name == 'mongodb':
                telegraf_config += MONGODB_TELEGRAF_CONFIG % service_info
            if service_name == 'nginx':
                telegraf_config += NGINX_TELEGRAF_CONFIG % service_info
            if service_name == 'rabbitmq':
                service_info.setdefault('username', 'guest')
                service_info.setdefault('password', 'guest')
                service_info.setdefault('mgmt_port', 15672)
                telegraf_config += RABBITMQ_TELEGRAF_CONFIG % service_info
            if service_name == 'redis':
                telegraf_config += REDIS_TELEGRAF_CONFIG % service_info
            if service_name == 'zookeeper':
                telegraf_config += ZOOKEEPER_CONFIG % service_info

        return telegraf_config

    def _restart_telegraf(self):
        restart_cmd = self.core.config.get(
            'telegraf.restart_command',
            'sudo --non-interactive service telegraf restart')
        telegraf_container = self.core.config.get('telegraf.docker_name')
        if telegraf_container is not None:
            bleemeo_agent.util.docker_restart(
                self.core.docker_client, telegraf_container
            )
        else:
            try:
                output = subprocess.check_output(
                    shlex.split(restart_cmd),
                    stderr=subprocess.STDOUT,
                )
                return_code = 0
            except subprocess.CalledProcessError as exception:
                output = exception.output
                return_code = exception.returncode

            if return_code != 0:
                logging.info(
                    'Failed to restart telegraf after reconfiguration : %s',
                    output
                )
            else:
                logging.debug(
                    'telegraf reconfigured and restarted : %s', output)

    def get_derivate(self, name, item, timestamp, value):
        """ Return derivate of a COUNTER (e.g. something that only goes upward)
        """
        (old_timestamp, old_value) = self._raw_value.get(
            (name, item), (None, None)
        )
        self._raw_value[(name, item)] = (timestamp, value)
        if old_timestamp is None:
            return None

        delta = value - old_value
        delta_time = timestamp - old_timestamp

        if delta_time == 0:
            return None

        if delta < 0:
            return None

        return delta / delta_time

    def get_service_instance(self, service, address, port):
        for (key, service_info) in self.core.services.items():
            (service_name, instance) = key
            if (service_name == service
                    and service_info.get('address') == address
                    and service_info.get('port') == port):
                return instance
            # RabbitMQ use mgmt port
            if (service_name == service
                    and service_name == 'rabbitmq'
                    and service_info.get('address') == address
                    and service_info.get('mgmt_port', 15672) == port):
                return instance

        raise KeyError('service not found')

    def get_elasticsearch_instance(self, node_id):
        for (key, service_info) in self.core.services.items():
            (service_name, instance) = key
            if service_name != 'elasticsearch':
                continue
            if 'es_node_id' not in service_info:
                try:
                    response = requests.get(
                        'http://%(address)s:%(port)s/_nodes/_local/'
                        % service_info
                    )
                    data = response.json()
                    this_node_id = list(data['nodes'].keys())[0]
                except (requests.RequestException, ValueError):
                    continue

                service_info['es_node_id'] = this_node_id

            if service_info.get('es_node_id') == node_id:
                return instance

        raise KeyError('service not found')

    def emit_metric(  # noqa
            self, name, timestamp, value, computed_metrics_pending):

        item = None
        service = None
        derive = False
        no_emit = False

        # name looks like
        # telegraf.HOSTNAME.(ITEM_INFO)*.PLUGIN.METRIC
        # example:
        # telegraf.xps-pierref.ext4./home.disk.total
        # telegraf.xps-pierref.mem.used
        # telegraf.xps-pierref.cpu-total.cpu.usage_steal
        part = name.split('.')

        if part[-2] == 'cpu':
            if part[-3] != 'cpu-total':
                return

            name = part[-1].replace('usage_', 'cpu_')
            if name == 'cpu_irq':
                name = 'cpu_interrupt'
            elif name == 'cpu_iowait':
                name = 'cpu_wait'

            if name == 'cpu_idle':
                self.core.emit_metric({
                    'measurement': 'cpu_used',
                    'time': timestamp,
                    'value': 100 - value,
                })
            computed_metrics_pending.add(('cpu_other', None, timestamp))
        elif part[-2] == 'disk':
            path = part[-3].replace('-', '/')
            path = self.graphite_server._disk_path_rename(path)
            if path is None:
                return
            item = path

            name = 'disk_' + part[-1]
            if name == 'disk_used_percent':
                name = 'disk_used_perc'
        elif part[-2] == 'diskio':
            item = part[2]
            name = part[-1]
            if not name.startswith('io_'):
                name = 'io_' + name
            if self.graphite_server._ignored_disk(item):
                return

            value = self.get_derivate(name, item, timestamp, value)
            if value is None:
                return

            if name == 'io_time':
                self.core.emit_metric({
                    'measurement': 'io_utilization',
                    # io_time is a number of ms spent doing IO (per seconds)
                    # utilization is 100% when we spent 1000ms during one
                    # second
                    'value': value / 1000. * 100.,
                    'time': timestamp,
                    'item': item,
                })
        elif part[-2] == 'mem':
            name = 'mem_' + part[-1]
            if name.endswith('_percent'):
                name = name.replace('_percent', '_perc')
            if name == 'mem_used' or name == 'mem_used_perc':
                # We don't use mem_used of telegraf (which is
                # mem_total - mem_free)
                # We prefere the "collectd one" (which is
                # mem_total - (mem_free + mem_cached + mem_buffered + mem_slab)

                # mem_used will be computed as mem_total - mem_available
                return  # We don't use mem_used of telegraf.

            if name == 'mem_total' or name == 'mem_available':
                computed_metrics_pending.add(('mem_used', None, timestamp))
        elif part[-2] == 'net' and part[-3] != 'all':
            item = part[-3]
            if self.graphite_server.network_interface_blacklist(item):
                return

            name = 'net_' + part[-1]
            if name == 'net_bytes_recv' or name == 'net_bytes_sent':
                name = name.replace('bytes', 'bits')
                value = value * 8

            derive = True
        elif part[-2] == 'swap':
            name = 'swap_' + part[-1]
            if name.endswith('_percent'):
                name = name.replace('_percent', '_perc')
            if name in ('swap_in', 'swap_out'):
                derive = True
        elif part[-2] == 'system':
            name = 'system_' + part[-1]
            if name == 'system_uptime':
                name = 'uptime'
            elif name == 'system_n_users':
                name = 'users_logged'
            elif name not in ('system_load1', 'system_load5', 'system_load15'):
                return
        elif part[-2] == 'processes':
            if part[-1] in ['blocked', 'running', 'sleeping',
                            'stopped', 'zombies', 'paging']:
                name = 'process_status_%s' % part[-1]
            elif part[-1] == 'total':
                name = 'process_total'
            else:
                return
        elif part[-2] == 'apache':
            service = 'apache'
            server_address = part[-3].replace('_', '.')
            server_port = int(part[-4])
            try:
                item = self.get_service_instance(
                    service, server_address, server_port
                )
            except KeyError:
                return

            name = 'apache_' + part[-1]
            if name == 'apache_IdleWorkers':
                name = 'apache_idle_workers'
            elif name == 'apache_TotalAccesses':
                name = 'apache_requests'
                derive = True
            elif name == 'apache_TotalkBytes':
                name = 'apache_bytes'
                derive = True
            elif name == 'apache_ConnsTotal':
                name = 'apache_connections'
            elif name == 'apache_Uptime':
                name = 'apache_uptime'
            elif 'scboard' in name:
                name = name.replace('scboard', 'scoreboard')
            else:
                return
        elif part[-2] == 'memcached':
            service = 'memcached'
            (server_address, server_port) = part[-3].split(':')
            server_address = server_address.replace('_', '.')
            server_port = int(server_port)
            try:
                item = self.get_service_instance(
                    service, server_address, server_port
                )
            except KeyError:
                return

            name = 'memcached_' + part[-1]
            if '_cmd_' in name:
                name = name.replace('_cmd_', '_command_')
                derive = True
            elif name == 'memcached_curr_connections':
                name = 'memcached_connections_current'
            elif name == 'memcached_curr_items':
                name = 'memcached_items_current'
            elif name == 'memcached_bytes_read':
                name = 'memcached_octets_rx'
                derive = True
            elif name == 'memcached_bytes_written':
                name = 'memcached_octets_tx'
                derive = True
            elif name == 'memcached_evictions':
                name = 'memcached_ops_evictions'
                derive = True
            elif name == 'memcached_threads':
                name = 'memcached_ps_count_threads'
            elif name in ('memcached_get_misses', 'memcached_get_hits'):
                name = name.replace('_get_', '_ops_')
                derive = True
            elif name.endswith('_misses') or name.endswith('_hits'):
                name = name.replace('memcached_', 'memcached_ops_')
                derive = True
            elif name != 'memcached_uptime':
                return
        elif part[-2] == 'mysql':
            service = 'mysql'
            (server_address, server_port) = part[-3].split(':')
            server_address = server_address.replace('_', '.')
            server_port = int(server_port)
            try:
                item = self.get_service_instance(
                    service, server_address, server_port
                )
            except KeyError:
                return

            name = 'mysql_' + part[-1]
            derive = True
            if name.startswith('mysql_qcache_'):
                name = name.replace('qcache', 'cache_result_qcache')
                if name == 'mysql_cache_result_qcache_lowmem_prunes':
                    name = 'mysql_cache_result_qcache_prunes'
                elif name == 'mysql_cache_result_qcache_queries_in_cache':
                    name = 'mysql_cache_size_qcache'
                    derive = False
                elif name == 'mysql_cache_result_qcache_total_blocks':
                    name = 'mysql_cache_blocksize_qcache'
                    derive = False
                elif (name == 'mysql_cache_result_qcache_free_blocks'
                        or name == 'mysql_cache_result_qcache_free_memory'):
                    name = name.replace(
                        'mysql_cache_result_qcache_', 'mysql_cache_')
                    derive = False
            elif name.startswith('mysql_table_locks_'):
                name = name.replace('mysql_table_locks_', 'mysql_locks_')
            elif name == 'mysql_bytes_received':
                name = 'mysql_octets_rx'
            elif name == 'mysql_bytes_sent':
                name = 'mysql_octets_tx'
            elif name == 'mysql_threads_created':
                name = 'mysql_total_threads_created'
            elif name.startswith('mysql_threads_'):
                # Other mysql_threads_* name are fine. Accept them unchanged
                derive = False
                pass
            elif name.startswith('mysql_commands_'):
                # mysql_commands_* name are fine. Accept them unchanged
                pass
            elif name.startswith('mysql_handler_'):
                # mysql_handler_* name are fine. Accept them unchanged
                pass
            elif name in ('mysql_queries', 'mysql_slow_queries'):
                pass
            elif name == 'mysql_innodb_row_lock_current_waits':
                derive = False
                name = 'mysql_innodb_locked_transaction'
            else:
                return
        elif part[-2] == 'nginx':
            service = 'nginx'
            server_address = part[-3].replace('_', '.')
            server_port = int(part[-4])
            try:
                item = self.get_service_instance(
                    service, server_address, server_port
                )
            except KeyError:
                return

            name = 'nginx_connections_' + part[-1]
            if name == 'nginx_connections_requests':
                name = 'nginx_requests'
                derive = True
            elif name == 'nginx_connections_accepts':
                name = 'nginx_connections_accepted'
                derive = True
            elif name == 'nginx_connections_handled':
                derive = True
        elif part[-2] == 'redis':
            service = 'redis'
            server_address = part[-3].replace('_', '.')
            server_port = int(part[-4])
            try:
                item = self.get_service_instance(
                    service, server_address, server_port
                )
            except KeyError:
                return

            name = 'redis_' + part[-1]

            if name == 'redis_clients':
                name = 'redis_current_connections_clients'
            elif name == 'redis_connected_slaves':
                name = 'redis_current_connections_slaves'
            elif name.startswith('redis_used_memory'):
                name = name.replace('redis_used_memory', 'redis_memory')
            elif name == 'redis_total_connections_received':
                name = 'redis_total_connections'
                derive = True
            elif name == 'redis_total_commands_processed':
                name = 'redis_total_operations'
                derive = True
            elif name == 'redis_rdb_changes_since_last_save':
                name = 'redis_volatile_changes'
            elif name in ('redis_evicted_keys', 'redis_keyspace_hits',
                          'redis_keyspace_misses', 'redis_expired_keys'):
                derive = True
            elif name in ('redis_uptime', 'redis_pubsub_patterns',
                          'redis_pubsub_channels', 'redis_keyspace_hitrate'):
                pass
            else:
                return
        elif part[-2] == 'zookeeper':
            service = 'zookeeper'
            server_address = part[-3].replace('_', '.')
            server_port = int(part[-4])
            try:
                item = self.get_service_instance(
                    service, server_address, server_port
                )
            except KeyError:
                return

            name = 'zookeeper_' + part[-1]
            if name.startswith('zookeeper_packets_'):
                derive = True
            elif name in ('zookeeper_ephemerals_count',
                          'zookeeper_watch_count', 'zookeeper_znode_count'):
                pass
            elif name == 'zookeeper_num_alive_connections':
                name = 'zookeeper_connections'
            else:
                return
        elif part[-2] == 'mongodb':
            service = 'mongodb'
            (server_address, server_port) = part[-3].split(':')
            server_address = server_address.replace('_', '.')
            server_port = int(server_port)
            try:
                item = self.get_service_instance(
                    service, server_address, server_port
                )
            except KeyError:
                return

            name = 'mongodb_' + part[-1]
            if name in ('mongodb_open_connections', 'mongodb_queued_reads',
                        'mongodb_queued_writes', 'mongodb_active_reads',
                        'mongodb_active_writes'):
                pass
            elif name == 'mongodb_queries_per_sec':
                name = 'mongodb_queries'
            elif name in ('mongodb_net_out_bytes', 'mongodb_net_in_bytes'):
                derive = True
            else:
                return
        elif part[2] == 'elasticsearch':
            service = 'elasticsearch'
            # It can't rely only on part[3] (node_host), because this is the
            # host as think by ES (usually the "public" IP of the node). But
            # Agent use "127.0.0.1" for localhost.
            # server_address = part[3].replace('_', '.')
            node_id = part[4]
            try:
                item = self.get_elasticsearch_instance(node_id)
            except KeyError:
                return

            name = None
            if part[-2] == 'elasticsearch_indices':
                if part[-1] == 'docs_count':
                    name = 'elasticsearch_docs_count'
                elif part[-1] == 'store_size_in_bytes':
                    name = 'elasticsearch_size'
                elif part[-1] == 'search_query_total':
                    name = 'elasticsearch_search'
                    derive = True
                    computed_metrics_pending.add(
                        ('elasticsearch_search_time', item, timestamp)
                    )
                elif part[-1] == 'search_query_time_in_millis':
                    name = 'elasticsearch_search_time_total'
                    derive = True
                    no_emit = True
                    computed_metrics_pending.add(
                        ('elasticsearch_search_time', item, timestamp)
                    )
        elif part[-2] == 'rabbitmq_overview':
            service = 'rabbitmq'

            item = part[-3]
            if not item.startswith('http:--'):
                return  # unknown format
            item = item[len('http:--'):]
            (server_address, server_port) = item.split(':')
            server_address = server_address.replace('_', '.')
            server_port = int(server_port)
            try:
                item = self.get_service_instance(
                    service, server_address, server_port
                )
            except KeyError:
                return

            if part[-1] == 'messages':
                name = 'rabbitmq_messages_count'
            elif part[-1] == 'consumers':
                name = 'rabbitmq_consumers'
            elif part[-1] == 'connections':
                name = 'rabbitmq_connections'
            elif part[-1] == 'queues':
                name = 'rabbitmq_queues'
            elif part[-1] == 'messages_published':
                derive = True
                name = 'rabbitmq_messages_published'
            elif part[-1] == 'messages_delivered':
                derive = True
                name = 'rabbitmq_messages_delivered'
            elif part[-1] == 'messages_acked':
                derive = True
                name = 'rabbitmq_messages_acked'
            elif part[-1] == 'messages_unacked':
                name = 'rabbitmq_messages_unacked_count'
            else:
                return
        else:
            return

        if name is None:
            return

        if derive:
            value = self.get_derivate(name, item, timestamp, value)
            if value is None:
                return
        metric = {
            'measurement': name,
            'time': timestamp,
            'value': value,
        }
        if service is not None:
            metric['service'] = service
        if item is not None:
            metric['item'] = item

        self.core.emit_metric(metric, no_emit=no_emit)
