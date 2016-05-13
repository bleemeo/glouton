import datetime
import io
import json
import logging
import logging.config
import os
import signal
import socket
import subprocess
import sys
import threading
import time

import apscheduler.scheduler
import psutil
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
    ('BLEEMEO_AGENT_ACCOUNT', 'bleemeo.account_id'),
    ('BLEEMEO_AGENT_REGISTRATION_KEY', 'bleemeo.registration_key'),
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
    },
    '-s rabbit': {  # beam process
        'interpreter': 'erlang',
        'service': 'rabbitmq',
        'port': 5672,
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
    'freeradius': {
        'service': 'freeradius',
    },
    'haproxy': {
        'service': 'haproxy',
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
    },
    'org.elasticsearch.bootstrap.Elasticsearch': {  # java process
        'interpreter': 'java',
        'service': 'elasticsearch',
        'port': 9200,
        'protocol': socket.IPPROTO_TCP,
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
    if 'port' in service_info:
        service_info.setdefault('address', '127.0.0.1')
        service_info.setdefault('protocol', socket.IPPROTO_TCP)
        try:
            service_info['port'] = int(service_info['port'])
        except ValueError:
            logging.info(
                'Bad custom service definition : '
                'service "%s" port is "%s" which is not a number',
                name,
                service_info['port'],
            )
            return None

    if (service_info.get('check_type') == 'nagios'
            and 'check_command' not in service_info):
        logging.info(
            'Bad custom service definition : '
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
            'Bad custom service definition : '
            'service "%s" dot not have port settings',
            name,
        )
        return None

    return service_info


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
            except IOError as exc:
                logging.warning('Failed to store file : %s', exc)

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
        self.started_at = time.time()
        self.reload_config()
        self._config_logger()
        logging.info(
            'Agent starting... (version=%s)',
            bleemeo_agent.facts.get_agent_version(),
        )
        self.sentry_client = None
        self.last_facts = {}
        self.last_facts_update = datetime.datetime(1970, 1, 1)
        self.top_info = None

        self.is_terminating = threading.Event()
        self.bleemeo_connector = None
        self.influx_connector = None
        self.graphite_server = None
        self.docker_client = None
        self.scheduler = apscheduler.scheduler.Scheduler(standalone=True)
        self.last_metrics = {}
        self.last_report = datetime.datetime.fromtimestamp(0)

        self._sentry_setup()
        self.thresholds = self.define_thresholds()
        self._discovery_job = None  # scheduled in schedule_tasks
        self.discovered_services = self.state.get_complex_dict(
            'discovered_services', {}
        )
        self._soft_status_since = {}

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
                release=bleemeo_agent.facts.get_agent_version(),
                include_paths=['bleemeo_agent'],
            )
            # FIXME: remove when raven-python PR #723 is merged
            # https://github.com/getsentry/raven-python/pull/723
            install_thread_hook(self.sentry_client)

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
            self.scheduler.add_interval_job(
                bleemeo_agent.util.pull_raw_metric,
                args=(self, name),
                seconds=interval,
            )

    def run(self):
        try:
            self.setup_signal()
            self._docker_connect()
            self.start_threads()
            self.schedule_tasks()
            try:
                self.scheduler.start()
            finally:
                self.scheduler.shutdown()
        except KeyboardInterrupt:
            pass
        finally:
            self.is_terminating.set()

    def setup_signal(self):
        """ Make kill (SIGKILL) send a KeyboardInterrupt

            Make SIGHUP trigger a discovery
        """
        def handler(signum, frame):
            raise KeyboardInterrupt

        def handler_hup(signum, frame):
            now = datetime.datetime.now()
            try:
                self.scheduler.unschedule_job(self._discovery_job)
            except KeyError:
                # Job is not scheduled, cause:
                # * agent is starting and scheduler has not yet started
                # * something else (docker watcher ?) is currently rescheduling
                #   that job
                # In both case, discovery has just happened or will happened in
                # close future. Do nothing
                return
            self._discovery_job = self.scheduler.add_interval_job(
                self.update_discovery,
                start_date=now + datetime.timedelta(seconds=1),
                hours=1,
            )

            try:
                self.scheduler.unschedule_job(self._update_facts_job)
            except KeyError:
                # Job is not scheduled, cause:
                # * agent is starting and scheduler has not yet started
                # * something else (docker watcher ?) is currently rescheduling
                #   that job
                # In both case, facts was just happened or will happened in
                # close future. Do nothing
                return
            self._update_facts_job = self.scheduler.add_interval_job(
                self.update_facts,
                start_date=now + datetime.timedelta(seconds=1),
                hours=24,
            )

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

    def schedule_tasks(self):
        self.scheduler.add_interval_job(
            func=bleemeo_agent.checker.periodic_check,
            seconds=3,
        )
        self.scheduler.add_interval_job(
            self._purge_metrics,
            minutes=5,
        )
        self._update_facts_job = self.scheduler.add_interval_job(
            self.update_facts,
            hours=24,
        )
        self._discovery_job = self.scheduler.add_interval_job(
            self.update_discovery,
            hours=1,
        )
        self.scheduler.add_interval_job(
            self._gather_metrics,
            seconds=10,
        )
        self.scheduler.add_interval_job(
            self.send_top_info,
            seconds=10,
        )
        self._schedule_metric_pull()

        # Call jobs we want to run immediatly
        self.update_facts()
        self.update_discovery(first_run=True)

    def start_threads(self):

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

        self.graphite_server = bleemeo_agent.graphite.GraphiteServer(self)
        self.graphite_server.start()

        if self.config.get('web.enabled', True):
            if bleemeo_agent.web is None:
                logging.warning(
                    'Missing dependency (flask), '
                    'can not start local WebServer'
                )
            else:
                bleemeo_agent.web.start_server(self)

        if self.docker_client is not None:
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

    def _purge_metrics(self):
        """ Remove old metrics from self.last_metrics

            Some metric may stay in last_metrics unupdated, for example
            disk usage from an unmounted partition.

            For this reason, from time to time, scan last_metrics and drop
            any value older than 6 minutes.
        """
        now = time.time()
        cutoff = now - 60 * 6

        # XXX: concurrent access with emit_metric.
        self.last_metrics = {
            (measurement, item): metric
            for ((measurement, item), metric) in self.last_metrics.items()
            if metric['time'] >= cutoff
        }

    def update_discovery(self, first_run=False):
        discovered_running_services = self._run_discovery()
        if first_run:
            # Should only be needed on first run. In addition to avoid
            # possible race-condition, do not run this while
            # Bleemeo._bleemeo_synchronize could run.
            self._search_old_service(discovered_running_services)
        new_discovered_services = self.discovered_services.copy()

        # Remove container address. If container is still running, address
        # will be re-added from discovered_running_services.
        for service_key, service_info in new_discovered_services.items():
            (service_name, instance) = service_key
            if instance is not None:
                service_info['address'] = None

        new_discovered_services.update(discovered_running_services)
        logging.debug('%s services are present', len(new_discovered_services))

        if new_discovered_services != self.discovered_services or first_run:
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
            for process in psutil.process_iter():
                # Cmdline may be unavailable (permission issue ?)
                # When unavailable, depending on psutil version, it returns
                # either [] or ['']
                if process.cmdline() and process.cmdline()[0]:
                    cmdline = ' '.join(process.cmdline())
                else:
                    cmdline = process.name()
                processes[process.pid] = {
                    'cmdline': cmdline,
                    'instance': None,
                }

        if self.docker_client is None:
            return processes

        for container in self.docker_client.containers():
            # container has... nameS
            # Also name start with "/". I think it may have mulitple name
            # and/or other "/" with docker-in-docker.
            container_name = container['Names'][0].lstrip('/')
            try:
                container_process = (
                    self.docker_client.top(container_name)['Processes']
                )
            except docker.errors.APIError:
                # most probably container is restarting or just stopped
                continue

            # In some case Docker return None instead of process list. Make
            # sure container_process is an iterable
            container_process = container_process or []
            for process in container_process:
                # process[1] is the pid as string. It is the PID from the
                # point-of-view of root pid namespace.
                pid = int(process[1])
                # process[7] is command line. e.g. /usr/bin/mysqld param
                cmdline = process[7]
                processes.setdefault(pid, {'cmdline': cmdline})
                processes[pid]['instance'] = container_name

        return processes

    def _run_discovery(self):
        """ Try to discover some service based on known port/process
        """
        discovered_services = {}
        processes = self._get_processes_map()

        for process in processes.values():
            service_info = get_service_info(process['cmdline'])
            if service_info is not None:
                service_info = service_info.copy()
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
                    address = '127.0.0.1'
                else:
                    address = self.get_docker_container_address(instance)

                service_info['address'] = address
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
                        'sudo', '--non-interactive',
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
        while True:
            try:
                try:
                    generator = self.docker_client.events(decode=True)
                except TypeError:
                    # older version of docker-py does decode=True by default
                    # (and don't have this option)
                    generator = self.docker_client.events()

                for event in generator:
                    # We request discovery in 10 seconds to allow newly created
                    # container to start (e.g. "mysqld" process to start, and
                    # not just the wrapper shell script)

                    status = event.get('status')
                    event_type = event.get('Type', 'container')

                    if (status not in DOCKER_DISCOVERY_EVENTS
                            or event_type != 'container'):
                        continue

                    now = datetime.datetime.now()
                    try:
                        self.scheduler.unschedule_job(self._discovery_job)
                    except KeyError:
                        # Job is not scheduled, cause:
                        # * agent is starting and scheduler has not yet started
                        # * something else (SIGHUP handler) is currently
                        #   rescheduling that job
                        # In both case, discovery has just happened or will
                        # happened in close future. Do nothing.
                        return
                    self._discovery_job = self.scheduler.add_interval_job(
                        self.update_discovery,
                        start_date=now + datetime.timedelta(seconds=10),
                        hours=1,
                    )
            except:
                # When docker restart, it breaks the connection and the
                # generator will raise an exception.
                pass

            time.sleep(5)

    def update_facts(self):
        """ Update facts """
        self.last_facts = bleemeo_agent.facts.get_facts(self)
        self.last_facts_update = datetime.datetime.now()

    def send_top_info(self):
        self.top_info = bleemeo_agent.util.get_top_info()
        if self.bleemeo_connector is not None:
            self.bleemeo_connector.publish_top_info(self.top_info)

    def reload_config(self):
        self.config = bleemeo_agent.config.load_config()
        for (env_name, conf_name) in ENVIRON_CONFIG_VARS:
            if env_name in os.environ:
                self.config.set(conf_name, os.environ[env_name])

        self.state = State(
            self.config.get(
                'agent.state_file',
                'state.json'))

        return self.config

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
        self.last_report = max(datetime.datetime.now(), self.last_report)

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

    def _check_soft_status(self, metric, soft_status, last_status, period):
        """ Check if soft_status was in error for at least the grace period
            of the metric.

            Return the new status
        """

        key = (metric['measurement'], metric.get('item'))
        (warning_since, critical_since) = self._soft_status_since.get(
            key,
            (None, None),
        )

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
                'metric=%s : soft_status=%s, last_status=%s, result=%s. '
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

        for config in container_info['NetworkSettings']['Networks'].values():
            if config['IPAddress']:
                return config['IPAddress']

        return None


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
