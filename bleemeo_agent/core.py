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
# pylint: disable=too-many-lines

import argparse
import collections
import copy
import datetime
import fnmatch
import io
import itertools
import json
import logging
import logging.config
import os
import re
import shlex
import signal
import socket
import subprocess
import sys
import threading
import time

try:
    import apscheduler.scheduler
    APSCHEDULE_IS_3X = False
except ImportError:
    import apscheduler.schedulers.background
    from apscheduler.jobstores.base import JobLookupError
    APSCHEDULE_IS_3X = True

import psutil
import requests
import six
from six.moves import configparser
import yaml

import bleemeo_agent
import bleemeo_agent.checker
import bleemeo_agent.config
import bleemeo_agent.facts
import bleemeo_agent.graphite
import bleemeo_agent.services
import bleemeo_agent.type
import bleemeo_agent.util

# Optional dependencies
try:
    # May fail because of missing mqtt dependency
    import bleemeo_agent.bleemeo
except ImportError:
    bleemeo_agent.bleemeo = None

try:
    # May fail because of missing influxdb dependency
    import bleemeo_agent.influxdb
except ImportError:
    bleemeo_agent.influxdb = None

try:
    # May fail because of missing flask dependency
    import bleemeo_agent.web
except ImportError:
    bleemeo_agent.web = None

try:
    import docker
except ImportError:
    docker = None

try:
    import kubernetes
    import kubernetes.client
    import kubernetes.config
except ImportError:
    kubernetes = None

# Optional dependencies
try:
    import raven
except ImportError:
    raven = None

# List of event that trigger discovery.
DOCKER_DISCOVERY_EVENTS = [
    'create',
    'start',
    # die event is sent when container stop (normal stop, OOM, docker kill...)
    'die',
    'destroy',
]


# Bleemeo agent changed the name of some service
SERVICE_RENAME = {
    'jabber': 'ejabberd',
    'imap': 'dovecot',
    'smtp': ['exim', 'postfix'],
    'mqtt': 'mosquitto',
}

KNOWN_PROCESS = {
    'asterisk': {
        'service': 'asterisk',
    },
    'apache2': {
        'service': 'apache',
        'port': 80,
        'protocol': socket.IPPROTO_TCP,
    },
    'dovecot': {
        'service': 'dovecot',
        'port': 143,
        'protocol': socket.IPPROTO_TCP,
    },
    'exim4': {
        'service': 'exim',
        'port': 25,
        'protocol': socket.IPPROTO_TCP,
    },
    'exim': {
        'service': 'exim',
        'port': 25,
        'protocol': socket.IPPROTO_TCP,
    },
    'freeradius': {
        'service': 'freeradius',
    },
    'haproxy': {
        'service': 'haproxy',
        'ignore_high_port': True,
    },
    'httpd': {
        'service': 'apache',
        'port': 80,
        'protocol': socket.IPPROTO_TCP,
    },
    'influxd': {
        'service': 'influxdb',
        'port': 8086,
        'protocol': socket.IPPROTO_TCP,
    },
    'libvirtd': {
        'service': 'libvirt',
    },
    'mongod': {
        'service': 'mongodb',
        'port': 27017,
        'protocol': socket.IPPROTO_TCP,
    },
    'mosquitto': {
        'service': 'mosquitto',
        'port': 1883,
        'protocol': socket.IPPROTO_TCP,
    },
    'mysqld': {
        'service': 'mysql',
        'port': 3306,
        'protocol': socket.IPPROTO_TCP,
    },
    'named': {
        'service': 'bind',
        'port': 53,
        'protocol': socket.IPPROTO_TCP,
    },
    'master': {  # postfix
        'service': 'postfix',
        'port': 25,
        'protocol': socket.IPPROTO_TCP,
    },
    'nginx:': {
        'service': 'nginx',
        'port': 80,
        'protocol': socket.IPPROTO_TCP,
    },
    'ntpd': {
        'service': 'ntp',
        'port': 123,
        'protocol': socket.IPPROTO_UDP,
    },
    'openvpn': {
        'service': 'openvpn',
    },
    'php-fpm:': {
        'service': 'phpfpm',
    },
    'slapd': {
        'service': 'openldap',
        'port': 389,
        'protocol': socket.IPPROTO_TCP,
    },
    'squid3': {
        'service': 'squid',
        'port': 3128,
        'protocol': socket.IPPROTO_TCP,
    },
    'squid': {
        'service': 'squid',
        'port': 3128,
        'protocol': socket.IPPROTO_TCP,
    },
    'postgres': {
        'service': 'postgresql',
        'port': 5432,
        'protocol': socket.IPPROTO_TCP,
    },
    'redis-server': {
        'service': 'redis',
        'port': 6379,
        'protocol': socket.IPPROTO_TCP,
    },
    'memcached': {
        'service': 'memcached',
        'port': 11211,
        'protocol': socket.IPPROTO_TCP,
    },
    'varnishd': {
        'service': 'varnish',
        'port': 6082,
        'protocol': socket.IPPROTO_TCP,
    },
    'uwsgi': {
        'service': 'uwsgi',
    }
}

KNOWN_INTEPRETED_PROCESS = [
    {
        'cmdline_must_contains': ['-s ejabberd'],
        'interpreter': 'erlang',
        'service': 'ejabberd',
        'port': 5222,
        'protocol': socket.IPPROTO_TCP,
        'ignore_high_port': True,
    },
    {
        'cmdline_must_contains': ['-s rabbit'],
        'interpreter': 'erlang',
        'service': 'rabbitmq',
        'port': 5672,
        'protocol': socket.IPPROTO_TCP,
        'ignore_high_port': True,
    },
    {
        'cmdline_must_contains': [
            'org.apache.zookeeper.server.quorum.QuorumPeerMain',
        ],
        'interpreter': 'java',
        'service': 'zookeeper',
        'port': 2181,
        'protocol': socket.IPPROTO_TCP,
        'ignore_high_port': True,
    },
    {
        'cmdline_must_contains': [
            'org.elasticsearch.bootstrap.Elasticsearch',
        ],
        'interpreter': 'java',
        'service': 'elasticsearch',
        'port': 9200,
        'protocol': socket.IPPROTO_TCP,
        'ignore_high_port': True,
    },
    {
        'cmdline_must_contains': [
            'org.apache.cassandra.service.CassandraDaemon',
        ],
        'interpreter': 'java',
        'service': 'cassandra',
        'port': 9042,
        'protocol': socket.IPPROTO_TCP,
        'ignore_high_port': True,
    },
    {
        'cmdline_must_contains': [
            'com.atlassian.stash.internal.catalina.startup.Bootstrap',
        ],
        'interpreter': 'java',
        'service': 'bitbucket',
        'port': 7990,
        'protocol': socket.IPPROTO_TCP,
        'ignore_high_port': True,
    },
    {
        'cmdline_must_contains': [
            'com.atlassian.bitbucket.internal.launcher.BitbucketServerLauncher'
        ],
        'interpreter': 'java',
        'service': 'bitbucket',
        'port': 7990,
        'protocol': socket.IPPROTO_TCP,
        'ignore_high_port': True,
    },
    {
        'cmdline_must_contains': [
            'org.apache.catalina.startup.Bootstrap',
            'jira',
        ],
        'interpreter': 'java',
        'service': 'jira',
        'port': 8080,
        'protocol': socket.IPPROTO_TCP,
        'ignore_high_port': True,
    },
    {
        'cmdline_must_contains': [
            'org.apache.catalina.startup.Bootstrap',
            'confluence',
        ],
        'interpreter': 'java',
        'service': 'confluence',
        'port': 8090,
        'protocol': socket.IPPROTO_TCP,
        'ignore_high_port': True,
    },
    {  # python process
        'cmdline_must_contains': [
            'salt-master',
        ],
        'interpreter': 'python',
        'service': 'salt-master',
        'port': 4505,
        'protocol': socket.IPPROTO_TCP,
    },
]

DOCKER_API_VERSION = '1.21'

LOGGER_CONFIG = """
version: 1
disable_existing_loggers: false
formatters:
    simple:
        format: "%(asctime)s - %(name)s - %(levelname)s - %(message)s"
    syslog:
        format: "bleemeo-agent[%(process)d]: %(levelname)s - %(message)s"
handlers:
    # One of the handler will be removed at runtime
    console:
        class: logging.StreamHandler
        formatter: simple
    syslog:
        class: logging.handlers.SysLogHandler
        address: /dev/log
        formatter: syslog
    file:
        class: logging.handlers.TimedRotatingFileHandler
        filename: /noexistant/path/to/be/remplaced
        when: midnight
        interval: 1
        backupCount: 7
        formatter: simple
loggers:
    requests: {level: WARNING}
    urllib3: {level: WARNING}
    werkzeug: {level: WARNING}
    apscheduler: {level: WARNING}
    kubernetes.client.rest: {level: INFO}
    apscheduler.executors: {level: ERROR}
    apscheduler.scheduler: {level: ERROR}
root:
    # Level and handlers will be updated at runtime
    level: INFO
    handlers: console
"""


UNIT_UNIT = 0
UNIT_BYTE = 2
UNIT_BIT = 3

MetricSoftStatusState = collections.namedtuple('MetricSoftStatusState', (
    'label',
    'item',
    'last_status',
    'warning_since',
    'critical_since',
))


def main():
    if os.name == 'nt':
        import bleemeo_agent.windows  # pylint: disable=redefined-outer-name
        bleemeo_agent.windows.windows_main()
        sys.exit(0)

    parser = argparse.ArgumentParser(description='Bleemeo agent')
    parser.add_argument(
        '--yes-run-as-root',
        default=False,
        action='store_true',
        help='Allows Bleemeo agent to run as root',
    )
    args = parser.parse_args()

    if os.getuid() == 0 and not args.yes_run_as_root:
        print(
            'Error: trying to run Bleemeo agent as root without'
            ' "--yes-run-as-root" option.'
        )
        print(
            'If Bleemeo agent is installed using standard method,'
            ' start it with:'
        )
        print('    service bleemeo-agent start')
        print('')
        sys.exit(1)

    try:
        core = Core()
        core.run()
    finally:
        logging.info('Agent stopped')


def get_service_info(cmdline):
    """ Return service_info from KNOWN_PROCESS matching this command line
    """
    try:
        arg0 = shlex.split(cmdline)[0]
    except ValueError:
        arg0 = cmdline.split()[0]

    name = os.path.basename(arg0)

    if os.name == 'nt':
        name = name.lower()

    # On Windows, remove the .exe if present
    if os.name == 'nt' and name.endswith('.exe'):
        name = name[:-len('.exe')]

    # Some process alter their name to add information. Redis, nginx
    # or php-fpm do this.
    # Example for Redis: "/usr/bin/redis-server *:6379".
    # Example for nginx: "'nginx: master process /usr/sbin/nginx [...]"
    # To catch first (Redis), take first "word", since no currently supported
    # service include a space.
    name = name.split()[0]
    # To catch second (nginx and php-fpm), ceck if command starts with one word
    # immediatly followed by ":".
    if arg0.strip() and arg0.split()[0][-1] == ':':
        altered_name = arg0.split()[0]
        if altered_name in KNOWN_PROCESS:
            return KNOWN_PROCESS[altered_name]

    # For now, special case for java, erlang or python process.
    # Need a more general way to manage those case. Every interpreter/VM
    # language are affected.

    if name in ('java', 'python', 'erl') or name.startswith('beam'):
        # For them, we search in the command line
        for service_info in KNOWN_INTEPRETED_PROCESS:
            # FIXME: we should check that intepreter match the one used.
            match = all(
                key in cmdline for key in service_info['cmdline_must_contains']
            )
            if match:
                return service_info
        return None
    return KNOWN_PROCESS.get(name)


def _apply_service_override(services, override_config, core):
    for service_info in override_config:
        service_info = service_info.copy()
        try:
            service = service_info.pop('id')
        except KeyError:
            logging.info('A entry in server.override is invalid')
            continue
        try:
            instance = service_info.pop('instance')
        except KeyError:
            instance = ''

        key = (service, instance)
        if key in services:
            tmp = services[(service, instance)].copy()
            tmp.update(service_info)
            service_info = tmp

        service_info = _sanitize_service(
            service, instance, service_info, key in services, core,
        )
        if service_info is not None:
            services[(service, instance)] = service_info


def _sanitize_service(
        name, instance, service_info, is_discovered_service, core):

    if service_info.get('port') or service_info.get('jmx_port'):
        if not instance:
            service_info.setdefault('address', '127.0.0.1')
        elif 'address' not in service_info:
            service_info.setdefault(
                'address',
                core.get_docker_container_address(instance)
            )

    if 'port' in service_info and service_info['port'] is not None:
        service_info.setdefault('protocol', socket.IPPROTO_TCP)
        try:
            service_info['port'] = int(service_info['port'])
        except ValueError:
            logging.info(
                'Bad custom service definition: '
                'service "%s" port is "%s" which is not a number',
                name,
                service_info['port'],
            )
            return None

    if (service_info.get('check_type') == 'nagios'
            and 'check_command' not in service_info):
        logging.info(
            'Bad custom service definition: '
            'service "%s" use type nagios without check_command',
            name,
        )
        return None
    if (service_info.get('check_type') != 'nagios'
            and 'port' not in service_info and not is_discovered_service
            and 'jmx_port' not in service_info):
        # discovered services could exist without port, etc.
        # It means that no check will be performed but service object will
        # be created.
        logging.info(
            'Bad custom service definition: '
            'service "%s" does not have port settings',
            name,
        )
        return None

    return service_info


def _purge_services(
        core, new_discovered_services, running_services,
        deleted_services):
    # pylint: disable=too-many-branches
    # pylint: disable=too-many-nested-blocks
    # pylint: disable=too-many-locals
    """ Remove deleted_services (service deleted from API) and check
        for uninstalled service (to mark them inactive).
    """
    if deleted_services is not None:
        deleted_services = list(deleted_services)
    else:
        deleted_services = []

    no_longer_running = (
        set(new_discovered_services) - set(running_services)
    )
    for service_key in no_longer_running:
        if not new_discovered_services[service_key].get('active', True):
            continue
        (service_name, instance) = service_key
        service_info = new_discovered_services[service_key]
        exe_path = service_info.get('exe_path')
        if instance and instance not in core.docker_containers_by_name:
            logging.info(
                'Service %s (%s): container no longer running,'
                ' marking it as inactive',
                service_name,
                instance,
            )
            service_info['active'] = False
        elif instance and 'pod_uid' in service_info:
            pod_uid = service_info['pod_uid']
            if pod_uid not in core.k8s_pods:
                logging.info(
                    'Service %s (%s): pod no longer running,'
                    ' marking it as inactive',
                    service_name,
                    instance,
                )
                service_info['active'] = False
            else:
                pod = core.k8s_pods[pod_uid]
                if pod.status and pod.status.container_statuses:
                    for container in pod.status.container_statuses:
                        container_id = container.container_id
                        if (not container_id or not
                                container_id.startswith('docker://')):
                            continue
                        container_id = container_id[len('docker://'):]
                        if container_id == service_info.get('container_id'):
                            continue
                        other_inspect = core.docker_containers.get(
                            container_id
                        )
                        if not other_inspect:
                            continue

                        other_key = (
                            service_name,
                            other_inspect['Name'].lstrip('/'),
                        )
                        if (other_key in new_discovered_services or
                                other_key in running_services):
                            logging.debug(
                                'Service %s (%s): pod restarted,'
                                ' marking old service as inactive',
                                service_name,
                                instance,
                            )
                            new_discovered_services[service_key]['active'] = (
                                False
                            )
        elif not instance and exe_path and not os.path.exists(exe_path):
            # Binary for service no longer exists. It has been uninstalled.
            service_info['active'] = False
            logging.info(
                'Service %s was uninstalled, marking it as inactive',
                service_name,
            )

    if deleted_services:
        for key in deleted_services:
            if key in new_discovered_services:
                del new_discovered_services[key]

    return new_discovered_services


def _guess_jmx_config(service_info, process):
    """ Guess if remote JMX is available for this process
    """
    jmx_options = [
        '-Dcom.sun.management.jmxremote.port=',
        '-Dcassandra.jmx.remote.port=',
    ]
    if service_info['address'] == '127.0.0.1':
        jmx_options.extend([
            '-Dcassandra.jmx.local.port=',
        ])

    for opt in jmx_options:
        try:
            index = process['cmdline'].find(opt)
        except ValueError:
            continue

        value = process['cmdline'][index + len(opt):].split()[0]
        try:
            jmx_port = int(value)
        except ValueError:
            continue

        service_info['jmx_port'] = jmx_port


def disable_https_warning():
    """
    Agent does HTTPS requests with verify=False (only for checks, not
    for communication with Bleemeo Cloud platform).
    By default requests will emit one warning for EACH request which is
    too noisy.
    """

    # urllib3 may be unvendored from requests.packages (at least Debian
    # does this). Older version of requests don't have requests.packages at
    # all. Newer version have a stub that makes requests.packages.urllib3 being
    # urllib3.
    # Try first to access requests.packages.urllib3 (which should works on
    # recent Debian version and virtualenv version) and fallback to urllib3
    # directly.
    try:
        from requests.packages import urllib3
    except ImportError:
        import urllib3

    try:
        klass = urllib3.exceptions.InsecureRequestWarning
    except AttributeError:
        # urllib3 introduced warning with 1.9. Before InsecureRequestWarning
        # didn't existed.
        return

    urllib3.disable_warnings(klass)


def format_value(value, unit, unit_text):
    """ Format a value for human

        >>> format_value(4096, UNIT_BYTE, 'Byte')
        '4.00 KBytes'

        unit is a number (like UNIT_BYTE). unit_text is used if unit is an
        unknown number.

        If unit or unit_text is None, do not format value.
    """
    if unit is None or unit_text is None or unit == UNIT_UNIT:
        return '%.2f' % value

    if unit in (UNIT_BYTE, UNIT_BIT):
        scale = ['', 'K', 'M', 'G', 'T', 'P', 'E']
        current_scale = scale.pop(0)
        while abs(value) >= 1024 and scale:
            current_scale = scale.pop(0)
            value = value / 1024

        return '%.2f %s%ss' % (
            value,
            current_scale,
            unit_text,
        )

    return '%.2f %s' % (
        value,
        unit_text,
    )


def format_duration(value):
    """ Format a duration (in seconds) for human

        >>> format_duration(300)
        '5 minutes'

        >>> format_duration(86400)
        '1 day'

        >>> format_duration(86500)
        '1 day'

        >>> format_duration(86300)
        '1 day'

        >>> format_duration(89)
        '1 minute'

        >>> format_duration(91)
        '2 minutes'

        >>> format_duration(0)
        ''
    """
    if value <= 0:
        return ""

    units = [
        (1, "second"),
        (60, "minute"),
        (60, "hour"),
        (24, "day"),
    ]

    current_unit = ""
    for (scale, unit_name) in units:
        if round(value / scale) >= 1:
            value = value / scale
            current_unit = unit_name
        else:
            break

    value = round(value)
    if value > 1:
        current_unit += 's'
    return '%d %s' % (value, current_unit)


def _check_soft_status(softstatus_state, metric, soft_status, period, now):
    if softstatus_state is None:
        softstatus_state = MetricSoftStatusState(
            metric.label,
            metric.item,
            soft_status,
            None,
            None,
        )

    critical_since = softstatus_state.critical_since
    warning_since = softstatus_state.warning_since
    # Make sure time didn't jump backward. If it does jump
    # backward reset the since timer.
    if critical_since and critical_since > now:
        critical_since = None
    if warning_since and warning_since > now:
        warning_since = None

    if soft_status == 'critical':
        critical_since = critical_since or metric.time
        warning_since = warning_since or metric.time
    elif soft_status == 'warning':
        critical_since = None
        warning_since = warning_since or metric.time
    else:
        critical_since = None
        warning_since = None

    warn_duration = (
        (metric.time - warning_since) if warning_since else 0
    )
    crit_duration = (
        (metric.time - critical_since) if critical_since else 0
    )

    if period == 0:
        status = soft_status
    elif crit_duration >= period:
        status = 'critical'
    elif warn_duration >= period:
        status = 'warning'
    elif (soft_status == 'warning'
          and softstatus_state.last_status == 'critical'):
        # Downgrade status from critical to warning immediately
        status = 'warning'
    elif soft_status == 'ok':
        # Downgrade status to ok immediately
        status = 'ok'
    else:
        status = softstatus_state.last_status

    return MetricSoftStatusState(
        softstatus_state.label,
        softstatus_state.item,
        status,
        warning_since,
        critical_since,
    )


def _service_ignore(rules, service_name, instance):
    """ Return True if the service should be ignored
    """
    filter_re = re.compile(r'(host|container):\s?([^ ]+)(\s|$)')
    for item in rules:
        # Compatibility with old rules
        if not isinstance(item, dict):
            item = {
                'name': item,
                'instance': 'container:* host:*',
            }
        if 'id' in item:
            if item.get('instance', '') == '':
                item = {
                    'name': item['id'],
                    'instance': 'host:*',
                }
            elif item['instance'] == '*':
                item = {
                    'name': item['id'],
                    'instance': 'host:* container:*',
                }
            else:
                item = {
                    'name': item['id'],
                    'instance': 'container:%s' % item['instance'],
                }

        ignore_name = item.get('name', '')
        if service_name != ignore_name:
            continue

        rule = item.get('instance', 'host:* container:*')
        host = None
        container = None
        for match in filter_re.findall(rule):
            if match[0] == 'host':
                host = match[1]
            elif match[0] == 'container':
                container = match[1]

        if instance == '' and host is not None:
            return True
        if (instance != '' and container is not None
                and fnmatch.fnmatch(instance, container)):
            return True
    return False


class State:
    """ Persistant store for state of the agent.

        Currently store in a json file
    """

    def __init__(self, filename):
        self.filename = filename
        self._content = {}
        self._write_lock = threading.RLock()
        self.reload()

    def reload(self):
        with self._write_lock:
            if os.path.exists(self.filename):
                with open(self.filename) as state_file:
                    self._content = json.load(state_file)
            else:
                self._content = {}

    def save(self):
        with self._write_lock:
            try:
                # Don't simply use open. This file must have limited permission
                open_flags = os.O_WRONLY | os.O_CREAT | os.O_TRUNC
                fileno = os.open(self.filename + '.tmp', open_flags, 0o600)
                with os.fdopen(fileno, 'w') as state_file:
                    json.dump(
                        self._content,
                        state_file,
                        cls=bleemeo_agent.util.JSONEncoder,
                    )
                    state_file.flush()
                    os.fsync(state_file.fileno())
                if os.name == 'nt':
                    try:
                        os.remove(self.filename)
                    except OSError:
                        pass
                os.rename(self.filename + '.tmp', self.filename)
                return True
            except OSError as exc:
                logging.warning('Failed to store file: %s', exc)
                return False

    def get(self, key, default=None):
        return self._content.get(key, default)

    def set(self, key, value):
        with self._write_lock:
            self._content[key] = value
            self.save()

    def delete(self, key):
        with self._write_lock:
            del self._content[key]
            self.save()

    def set_complex_dict(self, key, value):
        """ Store a dictionary as list in JSON file.

            This is usefull when you dictionary has key which could
            not be stored in JSON. For example the key is a couple of
            value (e.g. metric_name, item_tag).
        """
        json_value = []
        for sub_key, sub_value in value.items():
            json_value.append([sub_key, sub_value])
        self.set(key, json_value)

    def get_complex_dict(self, key, default=None):
        """ Reverse of set_complex_dict
        """
        json_value = self.get(key)
        if json_value is None:
            return default

        value = {}
        for row in json_value:
            (sub_key, sub_value) = row
            value[tuple(sub_key)] = sub_value
        return value


class Cache:
    """ In-memory cache backed with state file.

        It store information that could be lost but may (temporary) reduce
        functionality if lost.

        For example soft_status state is stored in the cache. Losing them means
        that status may change too fast and not honour the grace period.
    """
    CACHE_VERSION = 1

    def __init__(self, state, skip_load=False):
        self._state = state
        self.softstatus_by_labelitem = {}

        if not skip_load:
            self._reload()

    def copy(self):
        new = Cache(self._state, skip_load=True)
        new.softstatus_by_labelitem = self.softstatus_by_labelitem.copy()
        return new

    def _reload(self):
        cache = self._state.get('_core_cache')
        if cache is None:
            return
        if cache['version'] > self.CACHE_VERSION:
            return

        self.softstatus_by_labelitem = {}
        for value in cache['softstatus']:
            softstatus = MetricSoftStatusState(*value)
            key = (softstatus.label, softstatus.item)
            self.softstatus_by_labelitem[key] = softstatus

    def save(self):
        cache = {
            'version': self.CACHE_VERSION,
            'softstatus': list(self.softstatus_by_labelitem.values()),
        }
        self._state.set('_core_cache', cache)


class Core:
    # pylint: disable=too-many-instance-attributes
    # pylint: disable=too-many-public-methods
    def __init__(self, run_as_windows_service=False):
        self.run_as_windows_service = run_as_windows_service

        self.sentry_client = None
        self.last_facts = {}
        self.last_facts_update = bleemeo_agent.util.get_clock()
        self.last_discovery_update = bleemeo_agent.util.get_clock()
        self.top_info = None

        self.is_terminating = threading.Event()
        self.bleemeo_connector = None
        self.influx_connector = None
        self.graphite_server = None
        self.docker_client = None
        self._docker_client_cond = threading.Condition()
        self.k8s_client = None
        self.k8s_pods = {}
        self.k8s_docker_to_pods = {}
        self.docker_containers = {}
        self.docker_containers_by_name = {}
        self.docker_containers_ignored = {}
        self.docker_networks = {}
        if APSCHEDULE_IS_3X:
            self._scheduler = (
                apscheduler.schedulers.background.BackgroundScheduler(
                    timezone='UTC',
                )
            )
        else:
            self._scheduler = apscheduler.scheduler.Scheduler()  # noqa pylint: disable=no-member
        self.last_metrics = {}
        self.last_report = None

        self._discovery_job = None  # scheduled in schedule_tasks
        self._topinfo_job = None
        self._topinfo_period = 10
        self.discovered_services = {}
        self.services = {}
        self.metrics_unit = {}
        self._trigger_discovery = False
        self._trigger_facts = False
        self._trigger_updates_count = False
        self._netstat_output_mtime = 0

        # This is needed on Windows to compute mem_*_perc and mem_total
        self.total_memory_size = psutil.virtual_memory().total
        # This is needed on Windows to compute swap_used and swap_total:
        self.total_swap_size = psutil.swap_memory().total

        self.http_user_agent = None
        self.started_at = None
        self.state = None
        self.cache = None
        self.thresholds = {}
        self._update_facts_job = None
        self._gather_update_metrics_job = None
        self.config = None

    def _init(self):
        # pylint: disable=too-many-branches
        # pylint: disable=too-many-statements
        self.started_at = bleemeo_agent.util.get_clock()
        (errors, warnings) = self.reload_config()
        self._config_logger()
        if errors:
            logging.error(
                'Error while loading configuration: %s', '\n'.join(errors)
            )
            return False
        if warnings:
            logging.warning(
                'Warning while loading configuration: %s', '\n'.join(warnings)
            )

        state_file = self.config['agent.state_file']
        try:
            self.state = State(state_file)
        except (OSError, IOError) as exc:
            logging.error('Error while loading state file: %s', exc)
            return False
        except ValueError as exc:
            logging.error(
                'Error while reading state file %s: %s',
                state_file,
                exc,
            )
            return False

        if not self.state.save():
            logging.error('State file is not writable, stopping agent')
            return False

        self._sentry_setup()
        self.thresholds = {
            label: value
            for (label, value) in self.config['thresholds'].items()
        }
        self.discovered_services = self.state.get_complex_dict(
            'discovered_services', {}
        )

        self._apply_upgrade()

        # Agent does HTTPS requests with verify=False (only for checks, not
        # for communication with Bleemeo Cloud platform).
        # By default requests will emit one warning for EACH request which is
        # too noisy.
        disable_https_warning()

        netstat_file = self.config['agent.netstat_file']
        try:
            mtime = os.stat(netstat_file).st_mtime
        except OSError:
            mtime = 0
        self._netstat_output_mtime = mtime

        self.http_user_agent = (
            'Bleemeo Agent %s' % bleemeo_agent.facts.get_agent_version(self)
        )

        self.cache = Cache(self.state)

        cloudimage_creation_file = self.config[
            'agent.cloudimage_creation_file'
        ]
        if os.path.exists(cloudimage_creation_file):
            (_, current_mac) = (
                bleemeo_agent.facts.get_primary_addresses()
            )

            initial_ip_output = ''
            try:
                with open(cloudimage_creation_file) as file_obj:
                    initial_ip_output = file_obj.read()
            except (OSError, IOError) as exc:
                logging.warning(
                    'Unable to read content of %s file: %s',
                    cloudimage_creation_file,
                    exc,
                )

            (_, initial_mac) = (
                bleemeo_agent.facts.get_primary_addresses(initial_ip_output)
            )

            if (current_mac == initial_mac
                    or current_mac is None
                    or initial_mac is None):
                logging.info(
                    'Not starting bleemeo-agent since installation'
                    ' for creation of a cloud image was requested and agent is'
                    ' still running on the same machine',
                )
                logging.info(
                    'If this is wrong and agent should run on this machine,'
                    ' remove %s file',
                    cloudimage_creation_file,
                )
                return False
        try:
            os.unlink(cloudimage_creation_file)
        except OSError:
            pass

        self.graphite_server = bleemeo_agent.graphite.GraphiteServer(self)
        if self.config['bleemeo.enabled']:
            if bleemeo_agent.bleemeo is None:
                logging.warning(
                    'Missing dependency (paho-mqtt), '
                    'can not start Bleemeo connector'
                )
            else:
                self.bleemeo_connector = (
                    bleemeo_agent.bleemeo.BleemeoConnector(self)
                )
        if self.config['influxdb.enabled']:
            if bleemeo_agent.influxdb is None:
                logging.warning(
                    'Missing dependency (influxdb), '
                    'can not start InfluxDB connector'
                )
            else:
                self.influx_connector = (
                    bleemeo_agent.influxdb.InfluxDBConnector(self))

        if self.bleemeo_connector:
            self.bleemeo_connector.init()

        return True

    @property
    def container(self):
        """ Return the container type in which the agent is running.

            It's None if running outside any container.
        """
        return self.config['container.type']

    def set_topinfo_period(self, period):
        self._topinfo_period = period
        if self._topinfo_job is not None:
            # Only reschedule topinfo job if already scheduled.
            # This is needed because APscheduler 2.x does not allow to
            # unschedule a job while the scheduler it not yet started.
            self.schedule_topinfo()
        logging.debug(
            'Changed topinfo frequency to every %d second', period,
        )

    def _config_logger(self):
        output = self.config['logging.output']
        log_level = self.config['logging.level']

        if output == 'syslog':
            logger_config = yaml.safe_load(LOGGER_CONFIG)
            del logger_config['handlers']['console']
            del logger_config['handlers']['file']
            logger_config['root']['handlers'] = ['syslog']
            logger_config['root']['level'] = log_level
            try:
                logging.config.dictConfig(logger_config)
            except ValueError:
                # Probably /dev/log that does not exists, for example under
                # docker container
                output = 'console'

        if output == 'file':
            logger_config = yaml.safe_load(LOGGER_CONFIG)
            del logger_config['handlers']['console']
            del logger_config['handlers']['syslog']
            logger_config['root']['handlers'] = ['file']
            logger_config['root']['level'] = log_level
            logger_config['handlers']['file']['filename'] = self.config[
                'logging.output_file'
            ]
            try:
                logging.config.dictConfig(logger_config)
            except ValueError:
                output = 'console'

        if output == 'console':
            logger_config = yaml.safe_load(LOGGER_CONFIG)
            del logger_config['handlers']['syslog']
            del logger_config['handlers']['file']
            logger_config['root']['handlers'] = ['console']
            logger_config['root']['level'] = log_level
            logging.config.dictConfig(logger_config)

    def _sentry_setup(self):
        """ Configure Sentry if enabled
        """
        dsn = self.config['bleemeo.sentry.dsn']
        if not dsn:
            return

        if raven is not None:
            self.sentry_client = raven.Client(
                dsn,
                release=bleemeo_agent.facts.get_agent_version(self),
                include_paths=['bleemeo_agent'],
            )
            # FIXME: remove when raven-python PR #723 is merged
            # https://github.com/getsentry/raven-python/pull/723
            install_thread_hook(self.sentry_client)

    def add_scheduled_job(self, func, seconds, args=None, next_run_in=None):
        """ Schedule a recuring job using APScheduler

            It's a wrapper to add_job/add_interval_job+add_date_job depending
            on APScheduler version.

            if seconds is 0 or None, job will run only once based on
            next_run_in. In this case next_run_in could not be None

            next_run_in if not None, specify a delay for next run (in second).
            If None, it lets APScheduler choose the next run date.

            If next_run_in is 0, the next_run is scheduled as soon as possible.
            With APScheduler 3.x it means that next run is scheduled for now.
            With APScheduler 2.x, it means that next run is scheduled for
            now + 1 seconds.
        """
        options = {}
        if args is not None:
            options['args'] = args

        if APSCHEDULE_IS_3X:
            if seconds is None or seconds == 0:
                if next_run_in is None:
                    raise ValueError(
                        'next_run_in could not be None if seconds is 0'
                    )
                options['trigger'] = 'date'
                options['run_date'] = (
                    datetime.datetime.utcnow() +
                    datetime.timedelta(seconds=next_run_in)
                )
            else:
                options['trigger'] = 'interval'
                options['seconds'] = seconds

                if next_run_in is not None and next_run_in == 0:
                    options['next_run_time'] = datetime.datetime.utcnow()
                elif next_run_in is not None:
                    options['next_run_time'] = (
                        datetime.datetime.utcnow() +
                        datetime.timedelta(seconds=next_run_in)
                    )

            job = self._scheduler.add_job(
                func,
                **options
            )
        else:
            if seconds is None or seconds == 0:
                if next_run_in is None:
                    raise ValueError(
                        'next_run_in could not be None if seconds is 0'
                    )
                options['date'] = (
                    datetime.datetime.now() +
                    datetime.timedelta(seconds=next_run_in)
                )

                job = self._scheduler.add_date_job(
                    func,
                    **options
                )
            else:
                if next_run_in is not None and next_run_in < 1.0:
                    next_run_in = 1

                if next_run_in is not None:
                    options['start_date'] = (
                        datetime.datetime.now() +
                        datetime.timedelta(seconds=next_run_in)
                    )

                job = self._scheduler.add_interval_job(
                    func,
                    seconds=seconds,
                    **options
                )

        return job

    def trigger_job(self, job):
        """ Trigger a job to run immediately

            In APScheduler 2.x it will trigger the job in 1 seconds.

            In APScheduler 2.x, it will recreate a NEW job. For all version it
            will return the job that is still valid. Caller must use the
            returned job e.g.::

            >>> self.the_job = self.trigger_job(self.the_job)  # doctest: +SKIP
        """
        if APSCHEDULE_IS_3X:
            job.modify(next_run_time=datetime.datetime.utcnow())
        else:
            self._scheduler.unschedule_job(job)
            job = self._scheduler.add_interval_job(
                job.func,
                args=job.args,
                seconds=job.trigger.interval.total_seconds(),
                start_date=(
                    datetime.datetime.now() + datetime.timedelta(seconds=1)
                )
            )
        return job

    def unschedule_job(self, job):
        """ Unschedule and remove a job
        """
        if APSCHEDULE_IS_3X:
            if job:
                try:
                    job.remove()
                except JobLookupError:
                    pass
        else:
            try:
                self._scheduler.unschedule_job(job)
            except KeyError:
                pass

    def update_thresholds(self, state_threshold):
        """ Update threshold definition

            Threshold has two sources:

            * threshold from configuration
            * threshold from Bleemeo Cloud platform (stored in state)

            This method update definition for the later one. It will
            store the input thresholds in the state, merge the two sources
            and returns the result.
        """

        old_thresholds = self.thresholds

        new_thresholds = {
            label: value
            for (label, value) in self.config['thresholds'].items()
        }
        bleemeo_agent.config.merge_dict(
            new_thresholds,
            state_threshold,
        )
        self.thresholds = new_thresholds

        for update_name in ('system_pending_updates',
                            'system_pending_security_updates'):
            if (self.get_threshold(update_name, thresholds=old_thresholds) !=
                    self.get_threshold(update_name)):
                self._trigger_updates_count = True

        return self.thresholds

    def _schedule_metric_pull(self):
        """ Schedule metric which are pulled
        """
        for (name, config) in self.config['metric.pull'].items():
            interval = config.get('interval', 10)
            self.add_scheduled_job(
                bleemeo_agent.util.pull_raw_metric,
                args=(self, name),
                seconds=interval,
            )

    def run(self):
        # pylint: disable=too-many-branches
        if not self._init():
            return

        logging.info(
            'Agent starting... (version=%s)',
            bleemeo_agent.facts.get_agent_version(self),
        )
        upgrade_file = self.config['agent.upgrade_file']
        try:
            os.unlink(upgrade_file)
        except OSError:
            pass

        threads_started = False
        try:
            self.setup_signal()
            with self._docker_client_cond:
                self._docker_connect()
            self._k8s_connect()

            self.schedule_tasks()
            if not self.start_threads():
                self.is_terminating.set()
                return
            threads_started = True
            try:
                self._scheduler.start()
                # This loop is break by KeyboardInterrupt (ctrl+c or SIGTERM).
                # It wait with a timeout because under Windows the wait() is
                # uninterruptible. Using a 500ms wait allow to process
                # signal every 500ms.
                while not self.is_terminating.is_set():
                    self.is_terminating.wait(0.5)
            finally:
                self._scheduler.shutdown()
        except KeyboardInterrupt:
            pass
        finally:
            self.is_terminating.set()
            if threads_started:
                self.graphite_server.join()
                if self.bleemeo_connector is not None:
                    self.bleemeo_connector.join()
                if self.influx_connector is not None:
                    self.influx_connector.join()
            self.cache.save()

    def setup_signal(self):
        """ Make kill (SIGKILL) send a KeyboardInterrupt

            Make SIGHUP trigger a discovery
        """
        def handler(_signum, _frame):
            self.is_terminating.set()

        def handler_hup(_signum, _frame):
            self._trigger_discovery = True
            self._trigger_updates_count = True
            self._trigger_facts = True
            if self.bleemeo_connector:
                self.bleemeo_connector.trigger_full_sync = True

        if not self.run_as_windows_service:
            # Windows service don't use signal to shutdown
            signal.signal(signal.SIGTERM, handler)
        if os.name != 'nt':
            signal.signal(signal.SIGHUP, handler_hup)

    def _docker_connect(self):
        """ Try to connect to docker remote API

            Assume _docker_client_cond is held
        """
        if docker is None:
            logging.debug(
                'docker-py not installed. Skipping docker-related feature'
            )
            return

        if hasattr(docker, 'APIClient'):
            self.docker_client = docker.APIClient(
                version=DOCKER_API_VERSION,
                timeout=10,
            )
        else:
            self.docker_client = docker.Client(  # pylint: disable=no-member
                version=DOCKER_API_VERSION,
                timeout=10,
            )
        try:
            self.docker_client.ping()
            self._docker_client_cond.notify_all()
        except Exception as exc:  # pylint: disable=broad-except
            logging.debug(
                'Docker ping failed. Assume Docker is not used: %s', exc
            )
            self.docker_client = None

    def _k8s_connect(self):
        if not self.config['kubernetes.enabled']:
            return
        if kubernetes is None:
            logging.warning('Missing Kubernetes dependencies.')
            return

        try:
            kubernetes.config.load_incluster_config()
            self.k8s_client = kubernetes.client.CoreV1Api()
        except Exception as exc:  # pylint: disable=broad-except
            logging.error('Failed to initialize Kubernetes client: %s', exc)

    def _update_docker_info(self):
        # pylint: disable=too-many-locals
        docker_containers = {}
        docker_containers_by_name = {}
        docker_networks = {}
        docker_containers_ignored = {}

        with self._docker_client_cond:
            if self.docker_client is None:
                self._docker_connect()
            docker_client = self.docker_client

        containers = []
        if docker_client is not None:
            try:
                containers = docker_client.containers(all=True)
            except (docker.errors.APIError,
                    requests.exceptions.RequestException) as exc:
                logging.info('Failed to list containers: %s', exc)

        for container in containers:
            docker_id = container['Id']
            try:
                inspect = docker_client.inspect_container(docker_id)
            except (docker.errors.APIError,
                    requests.exceptions.RequestException):
                continue  # most probably container was removed
            labels = inspect.get('Config', {}).get('Labels', {})
            if labels is None:
                labels = {}
            bleemeo_enable = labels.get('bleemeo.enable', '').lower()
            if bleemeo_enable in ('0', 'off', 'false', 'no'):
                docker_containers_ignored[docker_id] = (
                    inspect['Name'].lstrip('/')
                )
                continue
            name = inspect['Name'].lstrip('/')
            docker_containers[docker_id] = inspect
            docker_containers_by_name[name] = inspect

        if (hasattr(docker_client, 'networks') and
                hasattr(docker_client, 'inspect_network')):
            networks = []
            try:
                containers = docker_client.containers(all=True)
            except (docker.errors.APIError,
                    requests.exceptions.RequestException) as exc:
                logging.info('Failed to list Docker network: %s', exc)
            for network in networks:
                if 'Name' not in network:
                    continue
                name = network['Name']
                if name == 'docker_gwbridge':
                    # For this network, the list of containers is needed. This
                    # is not returned on listing, and require direct inspection
                    # of the network
                    network = docker_client.inspect_network(name)

                docker_networks[name] = network

        self.docker_containers = docker_containers
        self.docker_containers_by_name = docker_containers_by_name
        self.docker_networks = docker_networks
        self.docker_containers_ignored = docker_containers_ignored

    def _update_kubernetes_info(self):
        self.k8s_pods = {}
        self.k8s_docker_to_pods = {}

        if self.k8s_client is None:
            return

        my_node = self.config['kubernetes.nodename']
        if my_node:
            pods = self.k8s_client.list_pod_for_all_namespaces(
                field_selector='spec.nodeName=%s' % my_node
            )
        else:
            # fallback to query ALL pods
            pods = self.k8s_client.list_pod_for_all_namespaces()
        for pod in pods.items:
            self.k8s_pods[pod.metadata.uid] = pod
            if pod.status and pod.status.container_statuses:
                for container in pod.status.container_statuses:
                    if (not container.container_id or not
                            container.container_id.startswith('docker://')):
                        continue
                    docker_id = container.container_id[len('docker://'):]
                    self.k8s_docker_to_pods[docker_id] = pod

    def schedule_tasks(self):
        self.add_scheduled_job(
            func=bleemeo_agent.checker.periodic_check,
            seconds=3,
        )
        self.add_scheduled_job(
            self.purge_metrics,
            seconds=5 * 60,
        )
        self._update_facts_job = self.add_scheduled_job(
            self.update_facts,
            seconds=24 * 60 * 60,
        )
        self._discovery_job = self.add_scheduled_job(
            self.update_discovery,
            seconds=1 * 60 * 60 + 10 * 60,  # 1 hour 10 minutes
        )
        self.add_scheduled_job(
            self._gather_metrics,
            seconds=10,
        )
        self.schedule_topinfo()
        self.add_scheduled_job(
            self._gather_metrics_minute,
            seconds=60,
            next_run_in=12,
        )
        self._gather_update_metrics_job = self.add_scheduled_job(
            self._gather_update_metrics,
            seconds=3600,
            next_run_in=15,
        )
        self.add_scheduled_job(
            self._check_triggers,
            seconds=10,
        )
        self.add_scheduled_job(
            self.cache.save,
            seconds=10 * 60,
        )
        self._schedule_metric_pull()

        # Call jobs we want to run immediatly
        self.update_facts()
        self.update_discovery(first_run=True)

    def schedule_topinfo(self):
        if self._topinfo_job is not None:
            self.unschedule_job(self._topinfo_job)
            self._topinfo_job = None

        self._topinfo_job = self.add_scheduled_job(
            self.send_top_info,
            seconds=self._topinfo_period,
        )

    def start_threads(self):
        self.graphite_server.start()
        self.graphite_server.initialization_done.wait(5)
        if not self.graphite_server.listener_up:
            logging.error('Graphite listener is not working, stopping agent')
            return False

        if self.bleemeo_connector:
            self.bleemeo_connector.start()

        if self.influx_connector:
            self.influx_connector.start()

        if self.config['web.enabled']:
            if bleemeo_agent.web is None:
                logging.warning(
                    'Missing dependency (flask), '
                    'can not start local WebServer'
                )
            else:
                bleemeo_agent.web.start_server(self)

        thread = threading.Thread(target=self._watch_docker_event)
        thread.daemon = True
        thread.start()

        return True

    def _gather_metrics(self):
        """ Gather and send some metric missing from other sources
        """
        uptime_seconds = bleemeo_agent.util.get_uptime()
        now = time.time()

        if self.graphite_server.metrics_source != 'telegraf':
            self.emit_metric(
                bleemeo_agent.type.DEFAULT_METRICPOINT._replace(
                    label='uptime',
                    time=now,
                    value=uptime_seconds,
                )
            )

        if self.bleemeo_connector and self.bleemeo_connector.connected:
            self.emit_metric(
                bleemeo_agent.type.DEFAULT_METRICPOINT._replace(
                    label='agent_status',
                    time=now,
                    value=0.0,  # status ok
                )
            )

        if os.name == 'nt':
            self.emit_metric(
                bleemeo_agent.type.DEFAULT_METRICPOINT._replace(
                    label='mem_total',
                    time=now,
                    value=float(self.total_memory_size),
                )
            )
            if self.last_facts.get('swap_present', False):
                self.total_swap_size = psutil.swap_memory().total
                self.emit_metric(
                    bleemeo_agent.type.DEFAULT_METRICPOINT._replace(
                        label='swap_total',
                        time=now,
                        value=float(self.total_swap_size),
                    )
                )
        metric = self.graphite_server.get_time_elapsed_since_last_data()
        if metric is not None:
            self.emit_metric(metric)

    def _gather_metrics_minute(self):
        """ Gather and send every minute some metric missing from other sources
        """
        # Read of (single) attribute is atomic, no lock needed
        docker_client = self.docker_client

        for key in list(self.docker_containers.keys()):
            result = self.docker_containers.get(key)
            if (result is not None
                    and 'Health' in result['State']
                    and docker_client is not None):

                self._docker_health_status(docker_client, key)

        # Some service have additional metrics. Currently only Postfix and exim
        # for mail queue size
        for (service_name, instance) in self.services:
            if service_name == 'postfix':
                bleemeo_agent.services.gather_postfix_queue_size(
                    instance, self,
                )
            if service_name == 'exim':
                bleemeo_agent.services.gather_exim_queue_size(
                    instance, self,
                )

    def _gather_update_metrics(self):
        """ Gather and send metrics from system updates
        """
        now = time.time()
        (pending_update, pending_security_update) = (
            bleemeo_agent.util.get_pending_update(self)
        )
        if pending_update is not None:
            self.emit_metric(
                bleemeo_agent.type.DEFAULT_METRICPOINT._replace(
                    label='system_pending_updates',
                    time=now,
                    value=float(pending_update),
                )
            )
        if pending_security_update is not None:
            self.emit_metric(
                bleemeo_agent.type.DEFAULT_METRICPOINT._replace(
                    label='system_pending_security_updates',
                    time=now,
                    value=float(pending_security_update),
                )
            )

    def _docker_health_status(self, docker_client, container_id):
        # pylint: disable=too-many-branches
        """ Send metric for docker container health status
        """
        try:
            result = docker_client.inspect_container(container_id)
        except (docker.errors.APIError,
                requests.exceptions.RequestException):
            return  # most probably container was removed

        name = result['Name'].lstrip('/')
        self.docker_containers[container_id] = result
        self.docker_containers_by_name[name] = result
        if 'Health' not in result['State']:
            return

        now = datetime.datetime.utcnow().replace(tzinfo=datetime.timezone.utc)
        try:
            started_at = datetime.datetime.strptime(
                result['State'].get('StartedAt', '').split('.')[0],
                '%Y-%m-%dT%H:%M:%S',
            ).replace(tzinfo=datetime.timezone.utc)
        except (ValueError, AttributeError):
            started_at = now

        docker_status = result.get('State', {}).get('Status', 'running')
        health_status = result['State'].get('Health', {}).get('Status')
        if docker_status != 'running':
            status = bleemeo_agent.type.STATUS_CRITICAL
        elif health_status == 'healthy':
            status = bleemeo_agent.type.STATUS_OK
        elif health_status == 'starting':
            if now - started_at < datetime.timedelta(minutes=1):
                status = bleemeo_agent.type.STATUS_OK
            else:
                status = bleemeo_agent.type.STATUS_WARNING
        elif health_status == 'unhealthy':
            status = bleemeo_agent.type.STATUS_CRITICAL
        else:
            logging.debug(
                'Docker container status is unknown: %r',
                health_status,
            )
            status = bleemeo_agent.type.STATUS_UNKNOWN

        logs = result['State']['Health'].get('Log', [])
        problem_origin = ''
        if docker_status != 'running':
            problem_origin = 'Container stopped'
        elif (health_status == 'starting'
              and status == bleemeo_agent.type.STATUS_WARNING):
            problem_origin = 'Container is still starting'
        elif logs:
            problem_origin = logs[-1].get('Output')
            if problem_origin is None:
                problem_origin = ''

        metric_point = bleemeo_agent.type.DEFAULT_METRICPOINT._replace(
            label='docker_container_health_status',
            time=time.time(),
            value=float(status),
            item=name,
            container_name=name,
            status_code=status,
            problem_origin=problem_origin,
        )
        self.emit_metric(metric_point)

    def purge_metrics(self, deleted_metrics=None):
        """ Remove old metrics from self.last_metrics

            Some metric may stay in last_metrics unupdated, for example
            disk usage from an unmounted partition.

            For this reason, from time to time, scan last_metrics and drop
            any value older than 6 minutes.

            deleted_metrics is a list of couple (measurement, item) of metrics
            that must be purged regardless of their age.
        """
        now = time.time()
        cutoff = now - 60 * 6

        if deleted_metrics is None:
            deleted_metrics = []

        # XXX: concurrent access with emit_metric.
        self.last_metrics = {
            key: metric_point
            for (key, metric_point) in self.last_metrics.items()
            if metric_point.time >= cutoff and key not in deleted_metrics
        }

    def fire_triggers(self, updates_count=None, discovery=None):
        if updates_count:
            self._trigger_updates_count = True
        if discovery:
            self._trigger_discovery = True

    def _check_triggers(self):
        if self._trigger_discovery:
            self._discovery_job = self.trigger_job(self._discovery_job)
            self._trigger_discovery = False
        if self._trigger_updates_count:
            self._gather_update_metrics_job = self.trigger_job(
                self._gather_update_metrics_job
            )
            self._trigger_updates_count = False
        if self._trigger_facts:
            self._update_facts_job = self.trigger_job(self._update_facts_job)
            self._trigger_facts = False

        netstat_file = self.config['agent.netstat_file']
        try:
            mtime = os.stat(netstat_file).st_mtime
        except OSError:
            mtime = 0

        if mtime != self._netstat_output_mtime:
            # Trigger discovery if netstat.out changed
            self._trigger_discovery = True
            self._netstat_output_mtime = mtime

    def update_discovery(self, first_run=False, deleted_services=None):
        # pylint: disable=too-many-locals
        self._trigger_discovery = False
        self._update_docker_info()
        self._update_kubernetes_info()
        discovered_running_services = self._run_discovery()
        if first_run:
            # Should only be needed on first run. In addition to avoid
            # possible race-condition, do not run this while
            # Bleemeo._bleemeo_synchronize could run.
            self._search_old_service(discovered_running_services)
        new_discovered_services = copy.deepcopy(self.discovered_services)

        new_discovered_services = _purge_services(
            self,
            new_discovered_services,
            discovered_running_services,
            deleted_services,
        )

        # Remove container address. If container is still running, address
        # will be re-added from discovered_running_services.
        # Also mark it as container_running=False. Also will be updated if
        # still running.
        for service_key, service_info in new_discovered_services.items():
            (service_name, instance) = service_key
            if instance:
                service_info['address'] = None
                service_info['container_running'] = False

        new_discovered_services.update(discovered_running_services)
        logging.debug('%s services are present', len(new_discovered_services))

        services = copy.deepcopy(new_discovered_services)
        _apply_service_override(
            services,
            self.config['service'],
            self,
        )
        self.apply_service_defaults(services)

        ignored_checks = self.config['service_ignore_check']
        ignored_metrics = self.config['service_ignore_metrics']

        for (key, service) in services.items():
            (service_name, instance) = key
            if not instance:
                name = service_name
            else:
                name = '%s (%s)' % (service_name, instance)

            if 'jmx_metrics' in service and 'jmx_port' not in service:
                logging.warning(
                    'Service %s: jmx_metrics require jmx_port',
                    name,
                )
                del service['jmx_metrics']

            if _service_ignore(ignored_checks, service_name, instance):
                service['ignore_check'] = True

            if _service_ignore(ignored_metrics, service_name, instance):
                service['ignore_metrics'] = True

        if new_discovered_services != self.discovered_services:
            self.discovered_services = new_discovered_services
            self.state.set_complex_dict(
                'discovered_services', self.discovered_services)
        self.services = services

        self.graphite_server.update_discovery()
        bleemeo_agent.checker.update_checks(self)

        self.last_discovery_update = bleemeo_agent.util.get_clock()

    def apply_service_defaults(self, services):
        """ Apply defaults to services.

            Currently only "stack" is set.
        """
        for service_info in services.values():
            if service_info.get('stack', None) is None:
                service_info['stack'] = self.config['stack']

    def _search_old_service(self, running_service):
        """ Search and rename any service that use an old name
        """
        for (service_name, instance) in list(self.discovered_services.keys()):
            if service_name in SERVICE_RENAME:
                new_name = SERVICE_RENAME[service_name]
                if isinstance(new_name, (list, tuple)):
                    # 2 services shared the same name (e.g. smtp=>postfix/exim)
                    # Search for the new name in running service
                    for candidate in new_name:
                        if (candidate, instance) in running_service:
                            self._rename_service(
                                service_name,
                                candidate,
                                instance,
                            )
                            break
                else:
                    self._rename_service(service_name, new_name, instance)

    def _rename_service(self, old_name, new_name, instance):
        logging.info('Renaming service "%s" to "%s"', old_name, new_name)
        old_key = (old_name, instance)
        new_key = (new_name, instance)

        self.discovered_services[new_key] = self.discovered_services[old_key]
        del self.discovered_services[old_key]

        if old_key in self.bleemeo_connector.services_uuid:
            self.bleemeo_connector.services_uuid[new_key] = (
                self.bleemeo_connector.services_uuid[old_key]
            )
            del self.bleemeo_connector.services_uuid[old_key]
            self.state.set_complex_dict(
                'services_uuid', self.bleemeo_connector.services_uuid
            )

    def _apply_upgrade(self):
        # Bogus test caused "udp6" to be keeps in netstat extra_ports.
        for service_info in self.discovered_services.values():
            extra_ports = service_info.get('extra_ports', {})
            for port_protocol in list(extra_ports):
                if port_protocol.endswith('/udp6'):
                    del extra_ports[port_protocol]

            # extra_ports is renamed in netstat_ports with compatible content
            # new netstat_ports contains more information (unix socket & port
            # above 32000).
            if 'extra_ports' in service_info:
                service_info.setdefault(
                    'netstat_ports', service_info['extra_ports'],
                )
                del service_info['extra_ports']

        # absence of item is now '' instead of None (like Bleemeo API)
        self.discovered_services = {
            (service_name, item if item else ''): value
            for ((service_name, item), value)
            in self.discovered_services.items()
        }

    def _get_processes_map(self):
        """ Return a mapping from PID to name and container in which
            process is running.

            When running in host / root pid namespace, associate None
            for the container (else it's the docker container name)
        """
        # Contains list of all processes from root pid_namespace point-of-view
        # key is the PID, value is {'name': 'mysqld', 'instance': 'db'}
        # instance is the container name. In case of processes running
        # outside docker, it's None
        processes = {}

        for process in bleemeo_agent.util.get_top_info(self)['processes']:
            processes[process['pid']] = process

        return processes

    def get_netstat(self):
        # pylint: disable=too-many-branches
        # pylint: disable=too-many-statements
        # pylint: disable=too-many-locals
        """ Parse netstat output and return a mapping pid => list of listening
            port/protocol (e.g. 80/tcp, 127/udp)
        """

        netstat_info = {}

        netstat_file = self.config['agent.netstat_file']
        netstat_re = re.compile(
            r'^(?P<protocol>udp6?|tcp6?)\s+\d+\s+\d+\s+'
            r'(?P<address>[0-9a-f.:]+):(?P<port>\d+)\s+[0-9a-f.:*]+\s+'
            r'(LISTEN)?\s+(?P<pid>\d+)/(?P<program>.*)$'
        )
        netstat_unix_re = re.compile(
            r'^(?P<protocol>unix)\s+\d+\s+\[\s+(ACC |W |N )+\s*\]\s+'
            r'(DGRAM|STREAM)\s+LISTENING\s+(\d+\s+)?'
            r'(?P<pid>\d+)/(?P<program>.*)\s+(?P<address>.+)$'
        )
        try:
            with open(netstat_file) as file_obj:
                for line in file_obj:
                    match = netstat_re.match(line)
                    if match is None:
                        match = netstat_unix_re.match(line)
                    if match is None:
                        continue

                    protocol = match.group('protocol')
                    pid = int(match.group('pid'))
                    address = match.group('address')

                    if protocol == 'unix':
                        port = 0
                    else:
                        port = int(match.group('port'))

                    # netstat output may have "tcp6" for IPv4 socket.
                    # For example elasticsearch output is:
                    # tcp6       0      0 127.0.0.1:7992          :::*   [...]
                    if protocol in ('tcp6', 'udp6'):
                        # Assume this socket is IPv4 & IPv6
                        protocol = protocol[:3]

                    if address == '::' and protocol != 'unix':
                        # "::" is all address in IPv6. Assume the socket
                        # is IPv4 & IPv6 and since agent supports only IPv4
                        # convert to all address in IPv4
                        address = '0.0.0.0'
                    if ':' in address and protocol != 'unix':
                        # No support for IPv6
                        continue

                    if protocol == 'unix':
                        key = protocol
                    else:
                        key = '%s/%s' % (port, protocol)
                    ports = netstat_info.setdefault(pid, {})

                    # FIXME: if multiple unix socket exists, only the
                    # first is kept. Not a issue today since we don't use
                    # addresses of unix socket.

                    # If multiple IP address exists, prefer 127.0.0.1
                    if key not in ports or address.startswith('127.'):
                        ports[key] = address
        except IOError:
            pass

        # also use psutil to fill current information, but due to privilege
        # this may be very limited.
        try:
            for conn in psutil.net_connections():
                if conn.pid is None:
                    continue
                if conn.status != psutil.CONN_LISTEN:
                    continue

                (address, port) = conn.laddr

                if address == '::':
                    # "::" is all address in IPv6. Assume the socket
                    # is IPv4 & IPv6 and since agent supports only IPv4
                    # convert to all address in IPv4
                    address = '0.0.0.0'
                if ':' in address:
                    # No support for IPv6
                    continue

                if conn.type == socket.SOCK_STREAM:
                    protocol = 'tcp'
                elif conn.type == socket.SOCK_DGRAM:
                    protocol = 'udp'
                else:
                    continue

                key = '%s/%s' % (port, protocol)
                ports = netstat_info.setdefault(conn.pid, {})

                # If multiple address exists, prefer 127.0.0.1
                if key not in ports or address.startswith('127.'):
                    ports[key] = address
        except OSError:
            pass

        return netstat_info

    def _discovery_fill_address_and_ports(
            self, service_info, instance, ports):

        service_name = service_info['service']
        if not instance:
            default_address = '127.0.0.1'
        else:
            default_address = self.get_docker_container_address(instance)

        default_port = service_info.get('port')

        netstat_ports = {}

        for port_proto, address in ports.items():
            if address == '0.0.0.0':
                address = default_address
            netstat_ports[port_proto] = address

        old_service_info = self.discovered_services.get(
            (service_name, instance), {}
        )
        if not netstat_ports and 'netstat_ports' in old_service_info:
            netstat_ports = old_service_info['netstat_ports'].copy()

        if default_port is not None and netstat_ports:
            if service_info['protocol'] == socket.IPPROTO_TCP:
                default_protocol = 'tcp'
            else:
                default_protocol = 'udp'

            key = '%s/%s' % (default_port, default_protocol)

            if key in netstat_ports:
                default_address = netstat_ports[key]
            else:
                # service is NOT listening on default_port but it is listening
                # on some ports. Don't check default_port and only check
                # netstat_ports
                default_port = None

        service_info['netstat_ports'] = netstat_ports
        service_info['port'] = default_port
        service_info['address'] = default_address

    def _run_discovery(self):
        # pylint: disable=too-many-branches
        """ Try to discover some service based on known port/process
        """
        discovered_services = {}
        processes = self._get_processes_map()

        netstat_info = self.get_netstat()

        # Process PID present in netstat output before other PID, because
        # two process may listen on same port (e.g. multiple Apache process)
        # but netstat only see one of them.
        for pid in itertools.chain(netstat_info.keys(), processes.keys()):
            process = processes.get(pid)
            if process is None:
                continue

            service_info = get_service_info(process['cmdline'])
            if service_info is not None:
                service_info = service_info.copy()
                service_info['exe_path'] = process.get('exe') or ''
                instance = process['instance']
                service_name = service_info['service']
                if (service_name, instance) in discovered_services:
                    # Service already found
                    continue
                if instance in self.docker_containers_ignored.values():
                    continue
                logging.debug(
                    'Discovered service %s on %s',
                    service_name, instance
                )

                service_info['active'] = True

                if not instance:
                    ports = netstat_info.get(pid, {})
                else:
                    docker_inspect = self.docker_containers_by_name.get(
                        instance
                    )
                    if docker_inspect is None:
                        continue  # container was removed or just created
                    ports = self.get_docker_ports(docker_inspect)
                    docker_id = docker_inspect.get('Id')
                    labels = docker_inspect.get('Config', {}).get('Labels', {})
                    if labels is None:
                        labels = {}
                    service_info['stack'] = labels.get('bleemeo.stack', None)
                    service_info['container_id'] = docker_id
                    # At this point, current is running because we are
                    # iterating over processes.
                    service_info['container_running'] = True

                    pod = self.k8s_docker_to_pods.get(docker_id)
                    if pod:
                        service_info['pod_uid'] = pod.metadata.uid
                        ports = self.get_kubernetes_ports(
                            pod, docker_id, ports,
                        )
                    if pod and pod.metadata.annotations:
                        service_info['stack'] = pod.metadata.annotations.get(
                            'bleemeo.stack', service_info['stack'],
                        )

                self._discovery_fill_address_and_ports(
                    service_info,
                    instance,
                    ports,
                )

                # some service may need additionnal information, like password
                if service_name == 'mysql':
                    self._discover_mysql(instance, service_info)
                if service_name == 'postgresql':
                    self._discover_pgsql(instance, service_info)

                if service_info.get('interpreter') == 'java':
                    _guess_jmx_config(service_info, process)

                discovered_services[(service_name, instance)] = service_info

        logging.debug(
            'Discovery found %s running services', len(discovered_services)
        )
        return discovered_services

    def _discover_mysql(self, instance, service_info):
        """ Find a MySQL user
        """
        mysql_user = None
        mysql_password = None

        # Read of (single) attribute is atomic, no lock needed
        docker_client = self.docker_client
        if not instance:
            # grab maintenace password from debian.cnf
            try:
                debian_cnf_raw = subprocess.check_output(
                    [
                        'sudo', '-n',
                        'cat', '/etc/mysql/debian.cnf'
                    ],
                )
            except (subprocess.CalledProcessError, OSError):
                debian_cnf_raw = b''

            debian_cnf = configparser.SafeConfigParser()
            debian_cnf.read_file(io.StringIO(debian_cnf_raw.decode('utf-8')))
            try:
                mysql_user = debian_cnf.get('client', 'user')
                mysql_password = debian_cnf.get('client', 'password')
            except (configparser.NoSectionError, configparser.NoOptionError):
                pass
        elif docker_client is not None:
            # MySQL is running inside a docker and Docker connection is up.
            try:
                container_info = docker_client.inspect_container(instance)
                for env in container_info['Config']['Env']:
                    # env has the form "VARIABLE=value"
                    if env.startswith('MYSQL_ROOT_PASSWORD='):
                        mysql_user = 'root'
                        mysql_password = env.replace(
                            'MYSQL_ROOT_PASSWORD=', ''
                        )
            except (docker.errors.APIError,
                    requests.exceptions.RequestException):
                pass  # most probably container was removed

        service_info['username'] = mysql_user
        service_info['password'] = mysql_password

    def _discover_pgsql(self, instance, service_info):
        """ Find a PostgreSQL user
        """
        user = None
        password = None

        # Read of (single) attribute is atomic, no lock needed
        docker_client = self.docker_client
        if instance and docker_client is not None:
            # Only know to extract user/password from Docker container
            try:
                container_info = docker_client.inspect_container(instance)
                for env in container_info['Config']['Env']:
                    # env has the form "VARIABLE=value"
                    if env.startswith('POSTGRES_PASSWORD='):
                        password = env.replace('POSTGRES_PASSWORD=', '')
                        if user is None:
                            user = 'postgres'
                    elif env.startswith('POSTGRES_USER='):
                        user = env.replace('POSTGRES_USER=', '')
            except (docker.errors.APIError,
                    requests.exceptions.RequestException):
                pass  # most probably container was removed

        service_info['username'] = user
        service_info['password'] = password

    def _watch_docker_event(self):
        """ Watch for docker event and re-run discovery
        """
        last_event_at = time.time()

        while True:
            reconnect_delay = 5
            with self._docker_client_cond:
                while self.docker_client is None:
                    self._docker_client_cond.wait(
                        reconnect_delay,
                    )
                    reconnect_delay = min(60, reconnect_delay * 2)

                    if self.docker_client is None:
                        self._docker_connect()

                try:
                    self.docker_client.ping()
                except Exception:  # pylint: disable=broad-except
                    self.docker_client = None
                    continue

                try:
                    try:
                        generator = self.docker_client.events(
                            decode=True, since=last_event_at,
                        )
                    except TypeError:
                        # older version of docker-py does decode=True by
                        # default (and don't have this option)
                        # Also they don't have since option.
                        generator = self.docker_client.events()
                except Exception as exc:  # pylint: disable=broad-except
                    logging.debug(
                        'Unable to create the Docker event watcher: %s',
                        exc,
                    )
                    continue

            try:
                for event in generator:
                    # even older version of docker-py does not support decoding
                    # at all
                    if isinstance(event, six.string_types):
                        event = json.loads(event)

                    last_event_at = event['time']
                    self._process_docker_event(event)
            except Exception:  # pylint: disable=broad-except
                # When docker restart, it breaks the connection and the
                # generator will raise an exception.
                logging.debug('Docker event watcher error', exc_info=True)

    def _process_docker_event(self, event):
        # pylint: disable=too-many-branches

        if 'Action' in event:
            action = event['Action']
        else:
            # status is depractated. Action was introduced with
            # Docker 1.10
            action = event.get('status')
        event_type = event.get('Type', 'container')

        if 'Actor' in event:
            actor_id = event['Actor'].get('ID')
        else:
            # id is deprecated. Actor was introduced with
            # Docker 1.10
            actor_id = event.get('id')

        if actor_id in self.docker_containers_ignored.keys():
            return

        if (action in DOCKER_DISCOVERY_EVENTS
                and event_type == 'container'):
            self._trigger_discovery = True
            if action == 'destroy':
                # Mark immediately any service from this container
                # as inactive. It avoid that a service check detect
                # the service as down before the discovery was run.
                for service_info in self.services.values():
                    if ('container_id' in service_info
                            and service_info['container_id'] == actor_id):
                        service_info['active'] = False
            if action == 'die':
                # Mark immediately any service from this container
                # as "container_stopped". It will avoid error message to be
                # "TCP connection refused" but directly "Container stopped"
                for service_info in self.services.values():
                    if ('container_id' in service_info
                            and service_info['container_id'] == actor_id):
                        service_info['container_running'] = False

        elif (action.startswith('health_status:')
              and event_type == 'container'):

            # Read of (single) attribute is atomic, no lock needed
            docker_client = self.docker_client

            if docker_client is not None:
                self._docker_health_status(docker_client, actor_id)
            # If an health_status event occure, it means that
            # docker container inspect changed.
            # Update the discovery date, so BleemeoConnector will
            # update the containers info
            self.last_discovery_update = (
                bleemeo_agent.util.get_clock()
            )

    def update_facts(self):
        """ Update facts """
        self.last_facts = bleemeo_agent.facts.get_facts(self)
        self.last_facts_update = bleemeo_agent.util.get_clock()

    def send_top_info(self):
        self.top_info = bleemeo_agent.util.get_top_info(self)
        if self.bleemeo_connector is not None:
            self.bleemeo_connector.publish_top_info(self.top_info)

    def reload_config(self):
        # pylint: disable=too-many-branches
        # pylint: disable=too-many-locals
        (self.config, errors, warnings) = (
            bleemeo_agent.config.load_config_with_default()
        )
        metric_prometheus = self.config['metric.prometheus']
        for name in list(metric_prometheus):
            if 'url' not in metric_prometheus[name]:
                warnings.append(
                    'Missing URL for prometheus exporter "%s". Ignoring it' % (
                        name,
                    )
                )
                del metric_prometheus[name]

        valid_services = []
        for service in self.config['service']:
            if 'id' not in service:
                warnings.append(
                    'Ignoring invalid service entry without id'
                )
                continue
            valid_services.append(service)

            name = service['id']
            if 'instance' in service:
                name = '%s (%s)' % (name, service['instance'])

            if 'jmx_username' in service and 'jmx_password' not in service:
                warnings.append(
                    'Service %s: jmx_username is set without jmx_password' % (
                        name,
                    )
                )
                del service['jmx_metrics']
            elif 'jmx_username' not in service and 'jmx_password' in service:
                warnings.append(
                    'Service %s: jmx_password is set without jmx_username' % (
                        name,
                    )
                )
                del service['jmx_metrics']
            elif 'jmx_metrics' in service:
                valid_metrics = []
                jmx_mandatory_option = set((
                    'name',
                    'mbean',
                    'attribute',
                ))
                for jmx_metric in service['jmx_metrics']:
                    missing_option = (
                        jmx_mandatory_option - set(jmx_metric.keys())
                    )
                    if missing_option and 'name' in jmx_metric:
                        warnings.append(
                            'Service %s has an invalid jmx_metrics "%s":'
                            ' missing %s option(s)' % (
                                name,
                                jmx_metric['name'],
                                ', '.join(missing_option),
                            )
                        )
                    elif missing_option:
                        warnings.append(
                            'Service %s has an invalid jmx_metrics:'
                            ' missing %s option(s)' % (
                                name,
                                ', '.join(missing_option),
                            )
                        )
                    else:
                        valid_metrics.append(jmx_metric)
                service['jmx_metrics'] = valid_metrics

        self.config['service'] = valid_services

        return (errors, warnings)

    def _store_last_value(self, metric_point):
        """ Store the metric in self.last_matrics, replacing the previous value
        """
        item = metric_point.item
        measurement = metric_point.label
        self.last_metrics[(measurement, item)] = metric_point

    def emit_metric(self, metric_point, no_emit=False):
        """ Sent a metric to all configured output
        """
        if metric_point.status_code is None and not no_emit:
            metric_point = self.check_threshold(metric_point)
        self._store_last_value(metric_point)

        if no_emit:
            return

        if self.bleemeo_connector is not None:
            self.bleemeo_connector.emit_metric(metric_point)
        if self.influx_connector is not None:
            self.influx_connector.emit_metric(metric_point)

    def update_last_report(self):
        self.last_report = datetime.datetime.now()

    @property
    def registration_at(self):
        return self.bleemeo_connector.registration_at

    def get_threshold(self, metric_name, item='', thresholds=None):
        """ Get threshold definition for given metric

            Return None if no threshold is defined

            If thresholds is not None, use it as definition of thresholds.
            If it's None, use self.thresholds
        """

        if thresholds is None:
            thresholds = self.thresholds

        threshold = thresholds.get((metric_name, item))
        if threshold is None:
            threshold = thresholds.get(metric_name)

        if threshold is None:
            return None

        # If all threshold are None, don't run check
        if (threshold.get('low_warning') is None
                and threshold.get('low_critical') is None
                and threshold.get('high_warning') is None
                and threshold.get('high_critical') is None):
            threshold = None

        return threshold

    def check_threshold(self, metric_point):
        # pylint: disable=too-many-branches
        # pylint: disable=too-many-locals
        # pylint: disable=too-many-statements
        """ Check if threshold is defined for given metric. If yes, check
            it and add a "status" tag.

            Also emit another metric suffixed with _status. The value
            of this metrics is 0, 1, 2 or 3 for ok, warning, critical
            and unknown respectively.
        """
        threshold = self.get_threshold(
            metric_point.label, metric_point.item
        )

        if threshold is None:
            return metric_point

        value = metric_point.value
        if value is None:
            return metric_point

        # there is a "soft" status (name taken from Nagios), which is a kind
        # of instant status. As soon as the value cross a threshold, its
        # soft-status change. But its status only change if soft-status stay
        # in error for a period of time (5 minutes by default).
        # Note: as soon as soft-status is OK, status is OK, there is no period
        # to wait in this case.

        if (threshold.get('low_critical') is not None
                and value < threshold.get('low_critical')):
            soft_status = 'critical'
        elif (threshold.get('low_warning') is not None
              and value < threshold.get('low_warning')):
            soft_status = 'warning'
        elif (threshold.get('high_critical') is not None
              and value > threshold.get('high_critical')):
            soft_status = 'critical'
        elif (threshold.get('high_warning') is not None
              and value > threshold.get('high_warning')):
            soft_status = 'warning'
        else:
            soft_status = 'ok'

        period = self._get_softstatus_period(metric_point.label)
        status = self._check_soft_status(
            metric_point,
            soft_status,
            period,
        )

        if status == 'ok':
            status_value = 0.0
            threshold_value = None
        elif status == 'warning':
            if (threshold.get('low_warning') is not None
                    and value < threshold.get('low_warning')):
                threshold_value = threshold.get('low_warning')
            else:
                threshold_value = threshold.get('high_warning')
            status_value = 1.0
        else:
            if (threshold.get('low_critical') is not None
                    and value < threshold.get('low_critical')):
                threshold_value = threshold.get('low_critical')
            else:
                threshold_value = threshold.get('high_critical')

            status_value = 2.0

        (unit, unit_text) = self.metrics_unit.get(
            (metric_point.label, metric_point.item),
            (None, None),
        )

        text = 'Current value: %s' % format_value(
            metric_point.value, unit, unit_text
        )

        if status != 'ok':
            if period:
                text += (
                    ' threshold (%s) exceeded'
                    ' over last %s' % (
                        format_value(threshold_value, unit, unit_text),
                        format_duration(period),
                    )
                )
            else:
                text += (
                    ' threshold (%s) exceeded' % (
                        format_value(threshold_value, unit, unit_text),
                    )
                )

        metric_point = metric_point._replace(
            status_code=bleemeo_agent.type.STATUS_NAME_TO_CODE[status],
            problem_origin=text
        )

        metric_status = metric_point._replace(
            label=metric_point.label + '_status',
            value=status_value,
            status_of=metric_point.label,
        )
        self.emit_metric(metric_status)

        return metric_point

    def _check_soft_status(self, metric_point, soft_status, period):
        """ Check if soft_status was in error for at least the grace period
            of the metric.

            Return the new status
        """
        key = (metric_point.label, metric_point.item)
        softstatus_state = self.cache.softstatus_by_labelitem.get(key)

        new_softstatus_state = _check_soft_status(
            softstatus_state,
            metric_point,
            soft_status,
            period,
            time.time(),
        )
        self.cache.softstatus_by_labelitem[key] = new_softstatus_state
        return new_softstatus_state.last_status

    def _get_softstatus_period(self, label):
        softstatus_periods = self.config['metric.softstatus_period']
        default_period = self.config['metric.softstatus_period_default']
        return int(softstatus_periods.get(label, default_period))

    def get_last_metric(self, name, item):
        """ Return the last metric matching name and item.

            None is returned if the metric is not found
        """
        return self.last_metrics.get((name, item), None)

    def get_last_metric_value(self, name, item, default=None):
        """ Return value for given metric.

            Return default if metric is not found.
        """
        metric_point = self.get_last_metric(name, item)
        if metric_point is not None:
            return metric_point.value
        return default

    @property
    def agent_uuid(self):
        """ Return a UUID for this agent.

            Currently, it's the UUID assigned by Bleemeo SaaS during
            registration.
        """
        if self.bleemeo_connector is not None:
            return self.bleemeo_connector.agent_uuid
        return None

    def get_docker_container_address(self, container_name):
        # pylint: disable=too-many-return-statements
        # pylint: disable=too-many-locals
        # pylint: disable=too-many-branches
        """ Return address where the container may be reachable from host

            This may not be possible. This could return None or an IP only
            accessible from an overlay network.

            Possible source (in order of preference):

            * config.NetworkSettings.IPAddress: only present for container
              in the default network named "bridge"
            * 127.0.0.1 if the container is in host network
            * the IP address from the first network with driver == bridge
            * the IP address of this container in the docker_gwbridge
            * the IP address from the first network
            * the IP address from io.rancher.container.ip label
        """
        if docker is None:
            return None

        # Read of (single) attribute is atomic, no lock needed
        docker_client = self.docker_client
        if docker_client is None:
            return None

        try:
            container_info = docker_client.inspect_container(
                container_name,
            )
        except (docker.errors.APIError,
                requests.exceptions.RequestException):
            return None

        container_id = container_info.get('Id')

        if container_info['NetworkSettings']['IPAddress']:
            return container_info['NetworkSettings']['IPAddress']

        address_first_network = None

        for key in container_info['NetworkSettings']['Networks']:
            if key == 'host':
                return '127.0.0.1'
            driver = self.docker_networks.get(key, {}).get('Driver', 'unknown')
            config = container_info['NetworkSettings']['Networks'][key]
            if config['IPAddress']:
                if driver == 'bridge':
                    return config['IPAddress']
                if address_first_network is None:
                    address_first_network = config['IPAddress']

        docker_gwbridge = (
            self.docker_networks
            .get('docker_gwbridge', {})
            .get('Containers', {})
        )
        if container_id in docker_gwbridge:
            address_with_netmask = (
                docker_gwbridge[container_id].get('IPv4Address', '')
            )
            address = address_with_netmask.split('/')[0]
            if address:
                return address

        if address_first_network:
            return address_first_network

        labels = container_info.get('Config', {}).get('Labels', {})
        if 'io.rancher.container.ip' in labels:
            ip_mask = labels['io.rancher.container.ip']
            (ip_address, _) = ip_mask.split('/')
            return ip_address

        # Try k8s
        if container_id in self.k8s_docker_to_pods:
            return self.k8s_docker_to_pods[container_id].status.pod_ip

        return None

    def get_docker_ports(self, container_info):
        # pylint: disable=no-self-use
        exposed_ports = container_info['Config'].get('ExposedPorts', {})
        listening_ports = list(exposed_ports.keys())

        # Address "0.0.0.0" will be replaced by container address in
        # _discovery_fill_address_and_ports method.
        ports = {
            x: '0.0.0.0' for x in listening_ports
        }
        return ports

    def get_kubernetes_ports(self, pod, docker_id, ports):
        # pylint: disable=no-self-use
        name = None
        for container in pod.status.container_statuses:
            container_id = container.container_id
            if (not container_id or not
                    container_id.startswith('docker://')):
                continue
            container_id = container_id[len('docker://'):]
            if container_id == docker_id:
                name = container.name
                break

        if name is None:
            return ports

        for container in pod.spec.containers:
            if container.name != name:
                continue
            ports = {}
            if container.ports:
                # Address "0.0.0.0" will be replaced by container address in
                # _discovery_fill_address_and_ports method.
                for ent in container.ports:
                    if ent.container_port:
                        portproto = '%s/%s' % (
                            ent.container_port,
                            ent.protocol.lower(),
                        )
                        ports[portproto] = '0.0.0.0'
                break

        return ports


def install_thread_hook(raven_self):
    """
    Workaround for sys.excepthook thread bug
    http://bugs.python.org/issue1230540

    PR submitted to raven-python
    https://github.com/getsentry/raven-python/pull/723
    """
    init_old = threading.Thread.__init__

    def init(self, *args, **kwargs):
        init_old(self, *args, **kwargs)
        run_old = self.run

        def run_with_except_hook(*args, **kw):
            try:
                run_old(*args, **kw)
            except Exception:
                raven_self.captureException(exc_info=sys.exc_info())
                raise
        self.run = run_with_except_hook
    threading.Thread.__init__ = init
