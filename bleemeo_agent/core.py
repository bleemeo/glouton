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

import argparse
import copy
import datetime
import io
import itertools
import json
import logging
import logging.config
import os
import re
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

import six
from six.moves import configparser
import yaml

import bleemeo_agent
import bleemeo_agent.checker
import bleemeo_agent.config
import bleemeo_agent.facts
import bleemeo_agent.graphite
import bleemeo_agent.util


# Optional dependencies
try:
    import docker
except ImportError:
    docker = None

# Optional dependencies
try:
    import raven
except ImportError:
    raven = None

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

# List of event that trigger discovery.
DOCKER_DISCOVERY_EVENTS = [
    'create',
    'start',
    # die event is sent when container stop (normal stop, OOM, docker kill...)
    'die',
    'destroy',
]

ENVIRON_CONFIG_VARS = [
    ('BLEEMEO_AGENT_ACCOUNT', 'bleemeo.account_id', 'string'),
    ('BLEEMEO_AGENT_REGISTRATION_KEY', 'bleemeo.registration_key', 'string'),
    ('BLEEMEO_AGENT_API_BASE', 'bleemeo.api_base', 'string'),
    ('BLEEMEO_AGENT_MQTT_HOST', 'bleemeo.mqtt.host', 'string'),
    ('BLEEMEO_AGENT_MQTT_PORT', 'bleemeo.mqtt.port', 'int'),
    ('BLEEMEO_AGENT_MQTT_SSL', 'bleemeo.mqtt.ssl', 'bool'),
    ('BLEEMEO_AGENT_LOGGING_LEVEL', 'logging.level', 'string'),
    ('BLEEMEO_AGENT_LOGGING_OUTPUT', 'logging.output', 'string'),
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
    '-s ejabberd': {  # beam process
        'interpreter': 'erlang',
        'service': 'ejabberd',
        'port': 5222,
        'protocol': socket.IPPROTO_TCP,
        'ignore_high_port': True,
    },
    '-s rabbit': {  # beam process
        'interpreter': 'erlang',
        'service': 'rabbitmq',
        'port': 5672,
        'protocol': socket.IPPROTO_TCP,
        'ignore_high_port': True,
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
    'org.apache.zookeeper.server.quorum.QuorumPeerMain': {  # java process
        'interpreter': 'java',
        'service': 'zookeeper',
        'port': 2181,
        'protocol': socket.IPPROTO_TCP,
        'ignore_high_port': True,
    },
    'org.elasticsearch.bootstrap.Elasticsearch': {  # java process
        'interpreter': 'java',
        'service': 'elasticsearch',
        'port': 9200,
        'protocol': socket.IPPROTO_TCP,
        'ignore_high_port': True,
    },
    'salt-master': {  # python process
        'interpreter': 'python',
        'service': 'salt-master',
        'port': 4505,
        'protocol': socket.IPPROTO_TCP,
    },
    'uwsgi': {
        'service': 'uwsgi',
    }
}

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
loggers:
    requests: {level: WARNING}
    urllib3: {level: WARNING}
    werkzeug: {level: WARNING}
    apscheduler: {level: WARNING}
root:
    # Level and handlers will be updated at runtime
    level: INFO
    handlers: console
"""


def main():
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
    name = os.path.basename(cmdline.split()[0])
    # For now, special case for java, erlang or python process.
    # Need a more general way to manage those case. Every interpreter/VM
    # language are affected.
    if name in ('java', 'python') or name.startswith('beam'):
        # For them, we search in the command line
        for (key, service_info) in KNOWN_PROCESS.items():
            # FIXME: we should check that intepreter match the one used.
            if 'interpreter' not in service_info:
                continue
            if key in cmdline:
                return service_info
    else:
        return KNOWN_PROCESS.get(name)


def apply_service_override(services, override_config):
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
            instance = None

        key = (service, instance)
        if key in services:
            tmp = services[(service, instance)].copy()
            tmp.update(service_info)
            service_info = tmp

        service_info = sanitize_service(service, service_info, key in services)
        if service_info is not None:
            services[(service, instance)] = service_info


def sanitize_service(name, service_info, is_discovered_service):
    if 'port' in service_info and service_info['port'] is not None:
        service_info.setdefault('address', '127.0.0.1')
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
    elif (service_info.get('check_type') != 'nagios'
            and 'port' not in service_info and not is_discovered_service):
        # discovered services could exist without port, etc.
        # It means that no check will be performed but service object will
        # be created.
        logging.info(
            'Bad custom service definition: '
            'service "%s" dot not have port settings',
            name,
        )
        return None

    return service_info


def convert_type(value_text, value_type):
    if value_type == 'string':
        return value_text

    if value_type == 'int':
        return int(value_text)
    elif value_type == 'bool':
        if value_text.lower() in ('true', 'yes', '1'):
            return True
        elif value_text.lower() in ('false', 'no', '0'):
            return False
        else:
            raise ValueError('invalid value %r for boolean' % value_text)
    else:
        raise NotImplementedError('Unknown type %s' % value_type)


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
        import requests.packages.urllib3 as urllib3
    except ImportError:
        import urllib3

    try:
        klass = urllib3.exceptions.InsecureRequestWarning
    except AttributeError:
        # urllib3 introduced warning with 1.9. Before InsecureRequestWarning
        # didn't existed.
        return

    urllib3.disable_warnings(klass)


def decode_docker_top(docker_top):
    """ Return a list of (pid, cmdline) from result for docker_client.top()

        Result of docker_client.top() is not always the same. On boot2docker,
        on first boot docker will use ps from busybox which output only few
        column.
    """
    result = []
    container_process = docker_top.get('Processes')

    pid_index = None
    cmdline_index = None
    for (index, name) in enumerate(docker_top.get('Titles', [])):
        if name == 'PID':
            pid_index = index
        elif name in ('CMD', 'COMMAND'):
            cmdline_index = index

    if pid_index is None or cmdline_index is None:
        return result

    # In some case Docker return None instead of process list. Make
    # sure container_process is an iterable
    container_process = container_process or []
    for process in container_process:
        # The PID is from the point-of-view of root pid namespace.
        pid = int(process[pid_index])
        cmdline = process[cmdline_index]
        result.append((pid, cmdline))

    return result


class State:
    """ Persistant store for state of the agent.

        Currently store in a json file
    """
    def __init__(self, filename):
        self.filename = filename
        self._content = {}
        self.reload()
        self._write_lock = threading.RLock()

    def reload(self):
        if os.path.exists(self.filename):
            with open(self.filename) as fd:
                self._content = json.load(fd)

    def save(self):
        with self._write_lock:
            try:
                # Don't simply use open. This file must have limited permission
                open_flags = os.O_WRONLY | os.O_CREAT | os.O_TRUNC
                fileno = os.open(self.filename + '.tmp', open_flags, 0o600)
                with os.fdopen(fileno, 'w') as fd:
                    json.dump(self._content, fd)
                    fd.flush()
                    os.fsync(fd.fileno())
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
        for k, v in value.items():
            json_value.append([k, v])
        self.set(key, json_value)

    def get_complex_dict(self, key, default=None):
        """ Reverse of set_complex_dict
        """
        json_value = self.get(key)
        if json_value is None:
            return default

        value = {}
        for row in json_value:
            (k, v) = row
            value[tuple(k)] = v
        return value


class Core:
    def __init__(self):
        self.sentry_client = None
        self.last_facts = {}
        self.last_facts_update = bleemeo_agent.util.get_clock()
        self.last_discovery_update = bleemeo_agent.util.get_clock()
        self.last_services_autoremove = bleemeo_agent.util.get_clock()
        self.top_info = None

        self.is_terminating = threading.Event()
        self.bleemeo_connector = None
        self.influx_connector = None
        self.graphite_server = None
        self.docker_client = None
        self.docker_containers = {}
        if APSCHEDULE_IS_3X:
            self._scheduler = (
                apscheduler.schedulers.background.BackgroundScheduler()
            )
        else:
            self._scheduler = apscheduler.scheduler.Scheduler()
        self.last_metrics = {}
        self.last_report = datetime.datetime.fromtimestamp(0)

        self._discovery_job = None  # scheduled in schedule_tasks
        self.discovered_services = {}
        self._soft_status_since = {}
        self._trigger_discovery = False
        self._trigger_facts = False
        self._netstat_output_mtime = 0

    def _init(self):
        self.started_at = bleemeo_agent.util.get_clock()
        errors = self.reload_config()
        self._config_logger()
        if errors:
            logging.error(
                'Error while loading configuration: %s', '\n'.join(errors)
            )
            return False

        state_file = self.config.get('agent.state_file', 'state.json')
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
        self.thresholds = self.define_thresholds()
        self.discovered_services = self.state.get_complex_dict(
            'discovered_services', {}
        )

        self._apply_upgrade()

        # Agent does HTTPS requests with verify=False (only for checks, not
        # for communication with Bleemeo Cloud platform).
        # By default requests will emit one warning for EACH request which is
        # too noisy.
        disable_https_warning()

        netstat_file = self.config.get('agent.netstat_file', 'netstat.out')
        try:
            mtime = os.stat(netstat_file).st_mtime
        except OSError:
            mtime = 0
        self._netstat_output_mtime = mtime

        return True

    @property
    def container(self):
        """ Return the container type in which the agent is running.

            It's None if running outside any container.
        """
        return self.config.get('container.type', None)

    def _config_logger(self):
        output = self.config.get('logging.output', 'console')
        log_level = self.config.get('logging.level', 'INFO')

        if output == 'syslog':
            logger_config = yaml.safe_load(LOGGER_CONFIG)
            del logger_config['handlers']['console']
            logger_config['root']['handlers'] = ['syslog']
            logger_config['root']['level'] = log_level
            try:
                logging.config.dictConfig(logger_config)
            except ValueError:
                # Probably /dev/log that does not exists, for example under
                # docker container
                output = 'console'

        if output == 'console':
            logger_config = yaml.safe_load(LOGGER_CONFIG)
            del logger_config['handlers']['syslog']
            logger_config['root']['handlers'] = ['console']
            logger_config['root']['level'] = log_level
            logging.config.dictConfig(logger_config)

    def _sentry_setup(self):
        """ Configure Sentry if enabled
        """
        dsn = self.config.get('bleemeo.sentry.dsn')
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

    def add_scheduled_job(self, func, seconds, args=None, next_run_in=None):  # noqa
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
                    datetime.datetime.now() +
                    datetime.timedelta(seconds=next_run_in)
                )
            else:
                options['trigger'] = 'interval'
                options['seconds'] = seconds

                if next_run_in is not None and next_run_in == 0:
                    options['next_run_time'] = datetime.datetime.now()
                elif next_run_in is not None:
                    options['next_run_time'] = (
                        datetime.datetime.now() +
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

            >>> self.the_job = self.trigger_job(self.the_job)
        """
        if APSCHEDULE_IS_3X:
            job.modify(next_run_time=datetime.datetime.now())
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

    def define_thresholds(self):
        self.thresholds = self.config.get('thresholds', {})
        bleemeo_agent.config.merge_dict(
            self.thresholds,
            self.state.get_complex_dict('thresholds', {})
        )
        return self.thresholds

    def _schedule_metric_pull(self):
        """ Schedule metric which are pulled
        """
        for (name, config) in self.config.get('metric.pull', {}).items():
            interval = config.get('interval', 10)
            self.add_scheduled_job(
                bleemeo_agent.util.pull_raw_metric,
                args=(self, name),
                seconds=interval,
            )

    def run(self):
        if not self._init():
            return

        logging.info(
            'Agent starting... (version=%s)',
            bleemeo_agent.facts.get_agent_version(self),
        )
        upgrade_file = self.config.get('agent.upgrade_file', 'upgrade')
        try:
            os.unlink(upgrade_file)
        except OSError:
            pass
        try:
            self.setup_signal()
            self._docker_connect()
            self.start_threads()
            if self.is_terminating.is_set():
                return
            self.schedule_tasks()
            try:
                self._scheduler.start()
                # Wait forever. Ctrl+C or SIGKILL will abort this wait
                self.is_terminating.wait()
            finally:
                self._scheduler.shutdown()
        except KeyboardInterrupt:
            pass
        finally:
            self.is_terminating.set()

    def setup_signal(self):
        """ Make kill (SIGKILL) send a KeyboardInterrupt

            Make SIGHUP trigger a discovery
        """
        def handler(signum, frame):
            self.is_terminating.set()

        def handler_hup(signum, frame):
            self._trigger_discovery = True
            self._trigger_facts = True

        signal.signal(signal.SIGTERM, handler)
        signal.signal(signal.SIGHUP, handler_hup)

    def _docker_connect(self):
        """ Try to connect to docker remote API
        """
        if docker is None:
            logging.debug(
                'docker-py not installed. Skipping docker-related feature'
            )
            return

        self.docker_client = docker.Client(
            version=DOCKER_API_VERSION,
        )
        try:
            self.docker_client.ping()
        except:
            logging.debug('Docker ping failed. Assume Docker is not used')
            self.docker_client = None

    def _update_docker_info(self):
        self.docker_containers = {}

        if self.docker_client is None:
            return

        for container in self.docker_client.containers(all=True):
            inspect = self.docker_client.inspect_container(container['Id'])
            name = inspect['Name'].lstrip('/')
            self.docker_containers[name] = inspect

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
        self.add_scheduled_job(
            self._gather_metrics_minute,
            seconds=60,
        )
        self.add_scheduled_job(
            self.send_top_info,
            seconds=10,
        )
        self.add_scheduled_job(
            self._check_triggers,
            seconds=10,
        )
        self._schedule_metric_pull()

        # Call jobs we want to run immediatly
        self.update_facts()
        self.update_discovery(first_run=True)

    def start_threads(self):

        self.graphite_server = bleemeo_agent.graphite.GraphiteServer(self)
        self.graphite_server.start()
        self.graphite_server.initialization_done.wait(5)
        if not self.graphite_server.listener_up:
            logging.error('Graphite listener is not working, stopping agent')
            self.is_terminating.set()
            return

        if self.config.get('bleemeo.enabled', True):
            if bleemeo_agent.bleemeo is None:
                logging.warning(
                    'Missing dependency (paho-mqtt), '
                    'can not start Bleemeo connector'
                )
            else:
                self.bleemeo_connector = (
                    bleemeo_agent.bleemeo.BleemeoConnector(self))
                self.bleemeo_connector.start()

        if self.config.get('influxdb.enabled', False):
            if bleemeo_agent.influxdb is None:
                logging.warning(
                    'Missing dependency (influxdb), '
                    'can not start InfluxDB connector'
                )
            else:
                self.influx_connector = (
                    bleemeo_agent.influxdb.InfluxDBConnector(self))
                self.influx_connector.start()

        if self.config.get('web.enabled', True):
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

    def _gather_metrics(self):
        """ Gather and send some metric missing from other sources
        """
        uptime_seconds = bleemeo_agent.util.get_uptime()
        now = time.time()

        if self.graphite_server.metrics_source != 'telegraf':
            self.emit_metric({
                'measurement': 'uptime',
                'time': now,
                'value': uptime_seconds,
            })

        if self.bleemeo_connector and self.bleemeo_connector.connected:
            self.emit_metric({
                'measurement': 'agent_status',
                'time': now,
                'value': 0.0,  # status ok
            })

        metric = self.graphite_server.get_time_elapsed_since_last_data()
        if metric is not None:
            self.emit_metric(metric, soft_status=False)

    def _gather_metrics_minute(self):
        """ Gather and send every minute some metric missing from other sources
        """
        for key in list(self.docker_containers.keys()):
            result = self.docker_containers.get(key)
            if (result is not None
                    and 'Health' in result['State']
                    and self.docker_client is not None):

                self._docker_health_status(result['Id'])

    def _docker_health_status(self, container_id):
        """ Send metric for docker container health status
        """
        try:
            result = self.docker_client.inspect_container(container_id)
        except:
            return  # most probably container was removed

        name = result['Name'].lstrip('/')
        self.docker_containers[name] = result
        if 'Health' not in result['State']:
            return

        if result['State']['Health'].get('Status') == 'healthy':
            status = bleemeo_agent.checker.STATUS_OK
        elif result['State']['Health'].get('Status') == 'unhealthy':
            status = bleemeo_agent.checker.STATUS_CRITICAL
        else:
            status = bleemeo_agent.checker.STATUS_UNKNOWN

        metric = {
            'measurement': 'docker_container_health_status',
            'time': time.time(),
            'value': float(status),
            'status': bleemeo_agent.checker.STATUS_NAME[status],
            'item': name,
            'container': name,
        }

        logs = result['State']['Health'].get('Log', [])
        if len(logs):
            metric['check_output'] = logs[-1].get('Output')

        self.emit_metric(metric)

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
            key: metric
            for (key, metric) in self.last_metrics.items()
            if metric['time'] >= cutoff and key not in deleted_metrics
        }

    def _check_triggers(self):
        if self._trigger_discovery:
            self._discovery_job = self.trigger_job(self._discovery_job)
            self._trigger_discovery = False
        if self._trigger_facts:
            self._update_facts_job = self.trigger_job(self._update_facts_job)
            self._trigger_facts = False

        netstat_file = self.config.get('agent.netstat_file', 'netstat.out')
        try:
            mtime = os.stat(netstat_file).st_mtime
        except OSError:
            mtime = 0

        if mtime != self._netstat_output_mtime:
            # Trigger discovery if netstat.out changed
            self._trigger_discovery = True
            self._netstat_output_mtime = mtime

    def update_discovery(self, first_run=False, deleted_services=None):
        self._update_docker_info()
        discovered_running_services = self._run_discovery()
        if first_run:
            # Should only be needed on first run. In addition to avoid
            # possible race-condition, do not run this while
            # Bleemeo._bleemeo_synchronize could run.
            self._search_old_service(discovered_running_services)
        new_discovered_services = copy.deepcopy(self.discovered_services)

        (new_discovered_services, had_autoremove) = self._purge_services(
            new_discovered_services,
            discovered_running_services,
            deleted_services,
        )

        # Remove container address. If container is still running, address
        # will be re-added from discovered_running_services.
        for service_key, service_info in new_discovered_services.items():
            (service_name, instance) = service_key
            if instance is not None:
                service_info['address'] = None

        new_discovered_services.update(discovered_running_services)
        logging.debug('%s services are present', len(new_discovered_services))

        if new_discovered_services != self.discovered_services:
            if new_discovered_services != self.discovered_services:
                logging.debug(
                    'Update configuration after change in discovered services'
                )
            self.discovered_services = new_discovered_services
            self.state.set_complex_dict(
                'discovered_services', self.discovered_services)

        self.services = self.discovered_services.copy()
        apply_service_override(
            self.services,
            self.config.get('service', [])
        )

        self.graphite_server.update_discovery()
        bleemeo_agent.checker.update_checks(self)

        self.last_discovery_update = bleemeo_agent.util.get_clock()
        if had_autoremove:
            self.last_services_autoremove = bleemeo_agent.util.get_clock()

    def _purge_services(
            self, new_discovered_services, running_services, deleted_services):
        """ Remove deleted_services (service deleted from API) and check
            for service auto-remove
        """
        had_autoremove = False

        if deleted_services is not None:
            deleted_services = list(deleted_services)
        else:
            deleted_services = []

        no_longer_running = (
            set(new_discovered_services) - set(running_services)
        )
        for service_key in no_longer_running:
            (service_name, instance) = service_key
            if instance is not None:
                # Don't process container here
                continue
            exe_path = new_discovered_services[service_key].get('exe_path')
            if instance is None and exe_path and not os.path.exists(exe_path):
                # Binary for service no longer exists. It has been uninstalled.
                logging.info(
                    'Service %s was uninstalled, removing it', service_name
                )
                deleted_services.append(service_key)
                had_autoremove = True

        if deleted_services:
            for key in deleted_services:
                if key in new_discovered_services:
                    del new_discovered_services[key]

        return (new_discovered_services, had_autoremove)

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

        if (self.container is None
                or self.config.get('container.pid_namespace_host')):
            # The host pid namespace see ALL process.
            # They are added in instance "None" (i.e. running in the host),
            # but if they are running in a docker, they will be updated later
            for process in bleemeo_agent.util.get_top_info()['processes']:
                processes[process['pid']] = {
                    'cmdline': process['cmdline'],
                    'instance': None,
                    'exe': process['exe'],
                }

        if self.docker_client is None:
            return processes

        for container in self.docker_client.containers():
            # container has... nameS
            # Also name start with "/". I think it may have mulitple name
            # and/or other "/" with docker-in-docker.
            container_name = container['Names'][0].lstrip('/')
            try:
                docker_top = (
                    self.docker_client.top(container_name)
                )
            except docker.errors.APIError:
                # most probably container is restarting or just stopped
                continue

            for (pid, cmdline) in decode_docker_top(docker_top):
                processes.setdefault(pid, {'cmdline': cmdline})
                processes[pid]['instance'] = container_name

        return processes

    def get_netstat(self):
        """ Parse netstat output and return a mapping pid => list of listening
            port/protocol (e.g. 80/tcp, 127/udp)
        """

        netstat_info = {}

        netstat_file = self.config.get('agent.netstat_file', 'netstat.out')
        netstat_re = re.compile(
            r'^(?P<protocol>udp6?|tcp6?)\s+\d+\s+\d+\s+'
            r'(?P<address>[0-9a-f.:]+):(?P<port>\d+)\s+[0-9a-f.:*]+\s+'
            r'(LISTEN)?\s+(?P<pid>\d+)/(?P<program>.*)$'
        )
        try:
            with open(netstat_file) as file_obj:
                for line in file_obj:
                    match = netstat_re.match(line)
                    if match is None:
                        continue

                    protocol = match.group('protocol')
                    pid = int(match.group('pid'))
                    address = match.group('address')
                    port = int(match.group('port'))

                    # netstat output may have "tcp6" for IPv4 socket.
                    # For example elasticsearch output is:
                    # tcp6       0      0 127.0.0.1:7992          :::*   [...]
                    if protocol in ('tcp6', 'udp6'):
                        # Assume this socket is IPv4 & IPv6
                        protocol = protocol[:3]

                    if address == '::':
                        # "::" is all address in IPv6. Assume the socket
                        # is IPv4 & IPv6 and since agent supports only IPv4
                        # convert to all address in IPv4
                        address = '0.0.0.0'
                    if ':' in address:
                        # No support for IPv6
                        continue

                    key = '%s/%s' % (port, protocol)
                    ports = netstat_info.setdefault(pid, {})

                    # If multiple address exists, prefer 127.0.0.1
                    if key not in ports or address.startswith('127.'):
                        ports[key] = address
        except IOError:
            pass

        return netstat_info

    def _discovery_fill_address_and_ports(
            self, service_info, instance, ports):

        service_name = service_info['service']
        if instance is None:
            default_address = '127.0.0.1'
        else:
            default_address = self.get_docker_container_address(instance)

        default_port = service_info.get('port')

        extra_ports = {}

        for port_proto, address in ports.items():
            port = int(port_proto.split('/')[0])
            if address == '0.0.0.0':
                address = default_address
            if service_info.get('ignore_high_port') and port > 32000:
                continue
            extra_ports[port_proto] = address

        old_service_info = self.discovered_services.get(
            (service_name, instance), {}
        )
        if len(extra_ports) == 0 and 'extra_ports' in old_service_info:
            extra_ports.update(old_service_info['extra_ports'])

        if default_port is not None and len(extra_ports) > 0:
            if service_info['protocol'] == socket.IPPROTO_TCP:
                default_protocol = 'tcp'
            else:
                default_protocol = 'udp'

            key = '%s/%s' % (default_port, default_protocol)

            if key in extra_ports:
                default_address = extra_ports[key]
            else:
                # service is NOT listening on default_port but it is listening
                # on some ports. Don't check default_port and only check
                # extra_ports
                default_port = None

        service_info['extra_ports'] = extra_ports
        service_info['port'] = default_port
        service_info['address'] = default_address

    def _run_discovery(self):
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
                logging.debug(
                    'Discovered service %s on %s',
                    service_name, instance
                )

                if instance is None:
                    ports = netstat_info.get(pid, {})
                else:
                    ports = self.get_docker_ports(instance)

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

        if instance is None:
            # grab maintenace password from debian.cnf
            try:
                debian_cnf_raw = subprocess.check_output(
                    [
                        'sudo', '-n',
                        'cat', '/etc/mysql/debian.cnf'
                    ],
                )
            except subprocess.CalledProcessError:
                debian_cnf_raw = b''

            debian_cnf = configparser.SafeConfigParser()
            debian_cnf.readfp(io.StringIO(debian_cnf_raw.decode('utf-8')))
            try:
                mysql_user = debian_cnf.get('client', 'user')
                mysql_password = debian_cnf.get('client', 'password')
            except (configparser.NoSectionError, configparser.NoOptionError):
                pass
        else:
            # MySQL is running inside a docker.
            container_info = self.docker_client.inspect_container(instance)
            for env in container_info['Config']['Env']:
                # env has the form "VARIABLE=value"
                if env.startswith('MYSQL_ROOT_PASSWORD='):
                    mysql_user = 'root'
                    mysql_password = env.replace('MYSQL_ROOT_PASSWORD=', '')

        service_info['username'] = mysql_user
        service_info['password'] = mysql_password

    def _discover_pgsql(self, instance, service_info):
        """ Find a PostgreSQL user
        """
        user = None
        password = None

        if instance is not None:
            # Only know to extract user/password from Docker container
            container_info = self.docker_client.inspect_container(instance)
            for env in container_info['Config']['Env']:
                # env has the form "VARIABLE=value"
                if env.startswith('POSTGRES_PASSWORD='):
                    password = env.replace('POSTGRES_PASSWORD=', '')
                    user = 'postgres'

        service_info['username'] = user
        service_info['password'] = password

    def _watch_docker_event(self):  # noqa
        """ Watch for docker event and re-run discovery
        """
        last_event_at = time.time()

        while True:
            reconnect_delay = 5
            while self.docker_client is None:
                time.sleep(reconnect_delay)
                self._docker_connect()
                reconnect_delay = min(60, reconnect_delay * 2)

            try:
                self.docker_client.ping()
            except:
                self.docker_client = None
                continue

            try:
                try:
                    generator = self.docker_client.events(
                        decode=True, since=last_event_at,
                    )
                except TypeError:
                    # older version of docker-py does decode=True by default
                    # (and don't have this option)
                    # Also they don't have since option.
                    generator = self.docker_client.events()

                for event in generator:
                    # even older version of docker-py does not support decoding
                    # at all
                    if isinstance(event, six.string_types):

                        event = json.loads(event)

                    if 'Action' in event:
                        action = event['Action']
                    else:
                        # status is depractated. Action was introduced with
                        # Docker 1.10
                        action = event.get('status')
                    event_type = event.get('Type', 'container')
                    last_event_at = event['time']

                    if (action in DOCKER_DISCOVERY_EVENTS
                            and event_type == 'container'):
                        self._trigger_discovery = True
                    elif (action.startswith('health_status:')
                            and event_type == 'container'):
                        container_id = event['Actor']['ID']
                        self._docker_health_status(container_id)
                        # If an health_status event occure, it means that
                        # docker container inspect changed.
                        # Update the discovery date, so BleemeoConnector will
                        # update the containers info
                        self.last_discovery_update = (
                            bleemeo_agent.util.get_clock()
                        )
            except:
                # When docker restart, it breaks the connection and the
                # generator will raise an exception.
                logging.debug('Docker event watcher error', exc_info=True)
                pass

    def update_facts(self):
        """ Update facts """
        self.last_facts = bleemeo_agent.facts.get_facts(self)
        self.last_facts_update = bleemeo_agent.util.get_clock()

    def send_top_info(self):
        self.top_info = bleemeo_agent.util.get_top_info()
        if self.bleemeo_connector is not None:
            self.bleemeo_connector.publish_top_info(self.top_info)

    def reload_config(self):
        (self.config, errors) = bleemeo_agent.config.load_config()

        for (env_name, conf_name, conf_type) in ENVIRON_CONFIG_VARS:
            if env_name in os.environ:
                try:
                    value = convert_type(os.environ[env_name], conf_type)
                except ValueError as exc:
                    errors.append(
                        'Bad environ variable %s: %s' % (env_name, exc)
                    )
                    continue
                self.config.set(conf_name, value)

        return errors

    def _store_last_value(self, metric):
        """ Store the metric in self.last_matrics, replacing the previous value
        """
        item = metric.get('item')
        measurement = metric['measurement']
        self.last_metrics[(measurement, item)] = metric

    def emit_metric(self, metric, soft_status=True, no_emit=False):
        """ Sent a metric to all configured output
        """
        if metric.get('status_of') is None and not no_emit:
            metric = self.check_threshold(metric, soft_status)

        self._store_last_value(metric)

        if no_emit:
            return

        if self.bleemeo_connector is not None:
            self.bleemeo_connector.emit_metric(metric)
        if self.influx_connector is not None:
            self.influx_connector.emit_metric(metric)

    def update_last_report(self):
        self.last_report = datetime.datetime.now()

    def get_threshold(self, metric_name, item=None):
        """ Get threshold definition for given metric

            Return None if no threshold is defined
        """
        threshold = self.thresholds.get((metric_name, item))

        if threshold is None:
            threshold = self.thresholds.get(metric_name)

        if threshold is None:
            return None

        # If all threshold are None, don't run check
        if (threshold.get('low_warning') is None
                and threshold.get('low_critical') is None
                and threshold.get('high_warning') is None
                and threshold.get('high_critical') is None):
            threshold = None

        return threshold

    def check_threshold(self, metric, with_soft_status):  # noqa
        """ Check if threshold is defined for given metric. If yes, check
            it and add a "status" tag.

            Also emit another metric suffixed with _status. The value
            of this metrics is 0, 1, 2 or 3 for ok, warning, critical
            and unknown respectively.
        """
        threshold = self.get_threshold(
            metric['measurement'], metric.get('item')
        )

        if threshold is None:
            return metric

        value = metric['value']
        if value is None:
            return metric

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

        last_metric = self.get_last_metric(
            metric['measurement'], metric.get('item')
        )

        if last_metric is None or last_metric.get('status') is None:
            last_status = soft_status
        else:
            last_status = last_metric.get('status')

        period = 5 * 60
        if not with_soft_status:
            status = soft_status
        else:
            status = self._check_soft_status(
                metric,
                soft_status,
                last_status,
                period,
            )

        if status == 'ok':
            text = 'Current value: %.2f' % metric['value']
            status_value = 0.0
        elif status == 'warning':
            if (threshold.get('low_warning') is not None
                    and value < threshold.get('low_warning')):
                if with_soft_status:
                    text = (
                        'Current value: %.2f\n'
                        'Metric has been below threshold (%.2f) '
                        'for the last 5 minutes.' % (
                            metric['value'],
                            threshold.get('low_warning'),
                        )
                    )
                else:
                    text = (
                        'Current value: %.2f\n'
                        'Metric is below threshold (%.2f).' % (
                            metric['value'],
                            threshold.get('low_warning'),
                        )
                    )
            else:
                if with_soft_status:
                    text = (
                        'Current value: %.2f\n'
                        'Metric has been above threshold (%.2f) '
                        'for the last 5 minutes.' % (
                            metric['value'],
                            threshold.get('high_warning'),
                        )
                    )
                else:
                    text = (
                        'Current value: %.2f\n'
                        'Metric is above threshold (%.2f).' % (
                            metric['value'],
                            threshold.get('high_warning'),
                        )
                    )
            status_value = 1.0
        else:
            if (threshold.get('low_critical') is not None
                    and value < threshold.get('low_critical')):
                if with_soft_status:
                    text = (
                        'Current value: %.2f\n'
                        'Metric has been below threshold (%.2f) '
                        'for the last 5 minutes.' % (
                            metric['value'],
                            threshold.get('low_critical'),
                        )
                    )
                else:
                    text = (
                        'Current value: %.2f\n'
                        'Metric is below threshold (%.2f).' % (
                            metric['value'],
                            threshold.get('low_critical'),
                        )
                    )
            else:
                if with_soft_status:
                    text = (
                        'Current value: %.2f\n'
                        'Metric has been above threshold (%.2f) '
                        'for the last 5 minutes.' % (
                            metric['value'],
                            threshold.get('high_critical'),
                        )
                    )
                else:
                    text = (
                        'Current value: %.2f\n'
                        'Metric is above threshold (%.2f).' % (
                            metric['value'],
                            threshold.get('high_critical'),
                        )
                    )

            status_value = 2.0

        metric = metric.copy()
        metric['status'] = status
        metric['check_output'] = text

        metric_status = metric.copy()
        metric_status['measurement'] = metric['measurement'] + '_status'
        metric_status['value'] = status_value
        metric_status['status_of'] = metric['measurement']
        self.emit_metric(metric_status)

        return metric

    def _check_soft_status(self, metric, soft_status, last_status, period):  # noqa
        """ Check if soft_status was in error for at least the grace period
            of the metric.

            Return the new status
        """

        key = (metric['measurement'], metric.get('item'))
        (warning_since, critical_since) = self._soft_status_since.get(
            key,
            (None, None),
        )

        # Make sure time didn't jump backward. If it does jump
        # backward reset the since timer.
        now = time.time()
        if critical_since and critical_since > now:
            critical_since = None
        if warning_since and warning_since > now:
            warning_since = None

        if soft_status == 'critical':
            critical_since = critical_since or metric['time']
            warning_since = warning_since or metric['time']
        elif soft_status == 'warning':
            critical_since = None
            warning_since = warning_since or metric['time']
        else:
            critical_since = None
            warning_since = None

        warn_duration = warning_since and (metric['time'] - warning_since) or 0
        crit_duration = (
            critical_since and (metric['time'] - critical_since) or 0
        )

        if crit_duration >= period:
            status = 'critical'
        elif warn_duration >= period:
            status = 'warning'
        elif soft_status == 'warning' and last_status == 'critical':
            # Downgrade status from critical to warning immediately
            status = 'warning'
        elif soft_status == 'ok':
            # Downgrade status to ok immediately
            status = 'ok'
        else:
            status = last_status

        self._soft_status_since[key] = (warning_since, critical_since)

        if soft_status != status or last_status != status:
            logging.debug(
                'metric=%s: soft_status=%s, last_status=%s, result=%s. '
                'warn for %d second / crit for %d second',
                key,
                soft_status,
                last_status,
                status,
                warn_duration,
                crit_duration,
            )

        return status

    def get_last_metric(self, name, item):
        """ Return the last metric matching name and item.

            None is returned if the metric is not found
        """
        return self.last_metrics.get((name, item), None)

    def get_last_metric_value(self, name, item, default=None):
        """ Return value for given metric.

            Return default if metric is not found.
        """
        metric = self.get_last_metric(name, item)
        if metric is not None:
            return metric['value']
        else:
            return default

    @property
    def agent_uuid(self):
        """ Return a UUID for this agent.

            Currently, it's the UUID assigned by Bleemeo SaaS during
            registration.
        """
        if self.bleemeo_connector is not None:
            return self.bleemeo_connector.agent_uuid

    def get_docker_container_address(self, container_name):
        container_info = self.docker_client.inspect_container(container_name)
        if container_info['NetworkSettings']['IPAddress']:
            return container_info['NetworkSettings']['IPAddress']

        for key in container_info['NetworkSettings']['Networks']:
            if key == 'host':
                return '127.0.0.1'
            config = container_info['NetworkSettings']['Networks'][key]
            if config['IPAddress']:
                return config['IPAddress']

        return None

    def get_docker_ports(self, container_name):
        container_info = self.docker_client.inspect_container(container_name)
        exposed_ports = container_info['Config'].get('ExposedPorts', {})
        listening_ports = list(exposed_ports.keys())

        # Address "0.0.0.0" will be replaced by container address in
        # _discovery_fill_address_and_ports method.
        ports = {
            x: '0.0.0.0' for x in listening_ports
        }
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
            except (KeyboardInterrupt, SystemExit):
                raise
            except:
                raven_self.captureException(exc_info=sys.exc_info())
                raise
        self.run = run_with_except_hook
    threading.Thread.__init__ = init
