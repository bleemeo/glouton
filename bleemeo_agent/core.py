import datetime
import io
import json
import logging
import logging.config
import os
import signal
import socket
import subprocess
import threading
import time

import apscheduler.scheduler
import psutil
from six.moves import configparser

import bleemeo_agent
import bleemeo_agent.checker
import bleemeo_agent.collectd
import bleemeo_agent.config
import bleemeo_agent.util
import bleemeo_agent.web


# Optional dependencies
try:
    import docker
except ImportError:
    docker = None

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


ENVIRON_CONFIG_VARS = [
    ('BLEEMEO_AGENT_ACCOUNT', 'bleemeo.account_id'),
    ('BLEEMEO_AGENT_REGISTRATION_KEY', 'bleemeo.registration_key'),
]

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
        'service': 'jabber',
        'port': 5222,
        'protocol': socket.IPPROTO_TCP,
    },
    'dovecot': {
        'service': 'imap',
        'port': 143,
        'protocol': socket.IPPROTO_TCP,
    },
    'exim4': {
        'service': 'smtp',
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
    'mosquitto': {
        'service': 'mqtt',
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
        'service': 'smtp',
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
    'varnishd': {
        'service': 'varnish',
    },
    'org.apache.zookeeper.server.quorum.QuorumPeerMain': {  # java process
        'service': 'zookeeper',
        'port': 2181,
        'protocol': socket.IPPROTO_TCP,
    },
}

DOCKER_API_VERSION = '1.17'


def main():
    logging.basicConfig(
        level=logging.INFO,
        format='%(asctime)s - %(name)s - %(levelname)s - %(message)s'
    )

    try:
        core = Core()
        core.run()
    except Exception:
        logging.critical(
            'Unhandled error occured. Agent will terminate',
            exc_info=True)
    finally:
        logging.info('Agent stopped')


def get_service_info(cmdline):
    """ Return service_info from KNOWN_PROCESS matching this command line
    """
    name = os.path.basename(cmdline.split()[0])
    # For now, special case for java and erlang process.
    # Need a more general way to manage those case. Every interpreter/VM
    # language are affected.
    if name == 'java' or name.startswith('beam'):
        # For them, we search in the command line
        for (key, service_info) in KNOWN_PROCESS.items():
            if key in cmdline:
                return service_info
    else:
        return KNOWN_PROCESS.get(name)


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
        self.reload_config()
        self._config_logger()
        logging.info('Agent starting...')
        self.last_facts = {}
        self.top_info = None

        self.is_terminating = threading.Event()
        self.bleemeo_connector = None
        self.influx_connector = None
        self.collectd_server = None
        self.docker_client = None
        self.scheduler = apscheduler.scheduler.Scheduler(standalone=True)
        self.last_metrics = {}
        self.last_report = datetime.datetime.fromtimestamp(0)

        self.thresholds = self.config.get('thresholds', {})
        self._discovery_job = None  # scheduled in schedule_tasks
        self.discovered_services = self.state.get_complex_dict(
            'discovered_services', {}
        )

    @property
    def container(self):
        """ Return the container type in which the agent is running.

            It's None if running outside any container.
        """
        return self.config.get('container', None)

    def _config_logger(self):
        logger_config = {
            'version': 1,
            'disable_existing_loggers': False,
            'loggers': {
                'requests': {'level': logging.WARNING},
                'urllib3': {'level': logging.WARNING},
                'werkzeug': {'level': logging.WARNING},
                'apscheduler': {'level': logging.WARNING},
            },
        }
        logger_config.update(self.config.get('logging', {}))
        logging.config.dictConfig(logger_config)

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
            self.schedule_tasks()
            self.start_threads()
            bleemeo_agent.checker.update_checks(self)
            try:
                self.scheduler.start()
            finally:
                self.scheduler.shutdown()
        except KeyboardInterrupt:
            pass
        finally:
            self.is_terminating.set()

    def setup_signal(self):
        """ Make kill (SIGKILL/SIGQUIT) send a KeyboardInterrupt

            Make SIGHUP trigger a discovery
        """
        def handler(signum, frame):
            raise KeyboardInterrupt

        def handler_hup(signum, frame):
            now = datetime.datetime.now()
            self.scheduler.unschedule_job(self._discovery_job)
            self._discovery_job = self.scheduler.add_interval_job(
                self.update_discovery,
                start_date=now + datetime.timedelta(seconds=1),
                hours=1,
            )

        signal.signal(signal.SIGTERM, handler)
        signal.signal(signal.SIGQUIT, handler)
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
        # If we schedule a job with start_date = now, when scheduler pickup
        # the job, start_date will be in the past and next run will be
        # current time + interval. In case of daily job, we don't want this.
        # With now + 3 seconds, when scheduler pickup the job, start_date
        # is no in the past and used as next run time.
        # This assume that scheduler will be started in less than 3 seconds
        now_scheduler = datetime.datetime.now() + datetime.timedelta(seconds=3)
        self.scheduler.add_interval_job(
            func=bleemeo_agent.checker.periodic_check,
            seconds=3,
        )
        self.scheduler.add_interval_job(
            self._purge_metrics,
            minutes=5,
        )
        self.scheduler.add_interval_job(
            self.send_facts,
            start_date=now_scheduler,
            hours=24,
        )
        self._discovery_job = self.scheduler.add_interval_job(
            self.update_discovery,
            start_date=now_scheduler,
            hours=1,
        )
        self.scheduler.add_interval_job(
            self._gather_metrics,
            seconds=10,
        )
        self.scheduler.add_interval_job(
            self.send_top_info,
            seconds=3,
        )
        self._schedule_metric_pull()

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

        self.collectd_server = bleemeo_agent.collectd.Collectd(self)
        self.collectd_server.start()

        bleemeo_agent.web.start_server(self)

        if self.docker_client is not None:
            thread = threading.Thread(target=self._watch_docker_event)
            thread.daemon = True
            thread.start()

    def _gather_metrics(self):
        """ Gather and send some metric missing from collectd

            Currently only uptime is sent.
        """
        uptime_seconds = bleemeo_agent.util.get_uptime()
        now = time.time()

        self.emit_metric({
            'measurement': 'uptime',
            'time': now,
            'value': uptime_seconds,
        })

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

    def update_discovery(self):
        discovered_running_services = self._run_discovery()
        new_discovered_services = self.discovered_services.copy()
        new_discovered_services.update(discovered_running_services)
        logging.debug('%s services are present', len(new_discovered_services))

        if new_discovered_services != self.discovered_services:
            self.discovered_services = new_discovered_services
            self.state.set_complex_dict(
                'discovered_services', self.discovered_services)
            logging.debug(
                'Update configuration after change in discovered services'
            )
            self.collectd_server.update_discovery()
            bleemeo_agent.checker.update_checks(self)

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

        # host (a.k.a the root pid namespace) see ALL process.
        # They are added in instance "None" (i.e. running in the host),
        # but if they are running in a docker, they will be updated later
        if self.container is None:
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
            for process in self.docker_client.top(container_name)['Processes']:
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
                    container_info = self.docker_client.inspect_container(
                        instance
                    )
                    address = container_info['NetworkSettings']['IPAddress']

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
                debian_cnf_raw = ''

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

        service_info['user'] = mysql_user
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

        service_info['user'] = user
        service_info['password'] = password

    def _watch_docker_event(self):
        """ Watch for docker event and re-run discovery
        """
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
            now = datetime.datetime.now()
            self.scheduler.unschedule_job(self._discovery_job)
            self._discovery_job = self.scheduler.add_interval_job(
                self.update_discovery,
                start_date=now + datetime.timedelta(seconds=10),
                hours=1,
            )

    def send_facts(self):
        """ Send facts to Bleemeo SaaS """
        # Note: even if we do not sent them to Bleemeo SaaS, calling this
        # method is still usefull. Web UI use last_facts.
        self.last_facts = bleemeo_agent.util.get_facts(self)
        if self.bleemeo_connector is not None:
            self.bleemeo_connector.send_facts(self.last_facts)

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

    def emit_metric(self, metric, no_emit=False):
        """ Sent a metric to all configured output

            When no_emit is True, metric is only stored in last_metric
            and not sent to backend (InfluxDB and Bleemeo).
            Usefull for metric needed to compute derivated one like CPU
            usage from one core.
        """
        if not no_emit:
            metric = self.check_threshold(metric)

        self._store_last_value(metric)

        if not no_emit:
            if self.bleemeo_connector is not None:
                self.bleemeo_connector.emit_metric(metric)
            if self.influx_connector is not None:
                self.influx_connector.emit_metric(metric)

    def update_last_report(self):
        self.last_report = max(datetime.datetime.now(), self.last_report)

    def check_threshold(self, metric):
        """ Check if threshold is defined for given metric. If yes, check
            it and add a "status" tag.
        """
        threshold = self.thresholds.get(metric['measurement'])
        if threshold is None:
            return metric

        value = metric['value']
        if value is None:
            return metric

        if (threshold.get('low_critical') is not None
                and value < threshold.get('low_critical')):
            status = 'critical'
        elif (threshold.get('low_warning') is not None
                and value < threshold.get('low_warning')):
            status = 'warning'
        elif (threshold.get('high_critical') is not None
                and value > threshold.get('high_critical')):
            status = 'critical'
        elif (threshold.get('high_warning') is not None
                and value > threshold.get('high_warning')):
            status = 'warning'
        else:
            status = 'ok'

        metric = metric.copy()
        metric['status'] = status
        return metric

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
