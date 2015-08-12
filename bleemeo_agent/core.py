import copy
import datetime
import json
import logging
import logging.handlers
import multiprocessing
import os
import random
import sched
import signal
import threading
import time

import jinja2
import psutil
import stevedore

import bleemeo_agent
import bleemeo_agent.bleemeo
import bleemeo_agent.checker
import bleemeo_agent.collectd
import bleemeo_agent.config
import bleemeo_agent.influxdb
import bleemeo_agent.util
import bleemeo_agent.web


def main():
    config = bleemeo_agent.config.load_config()
    setup_logger(config)
    logging.info('Agent starting...')

    try:
        core = Core(config)
        core.run()
    except Exception:
        logging.critical(
            'Unhandled error occured. Agent will terminate',
            exc_info=True)
    finally:
        logging.info('Agent stopped')


def setup_logger(config):
    level_map = {
        'debug': logging.DEBUG,
        'info': logging.INFO,
        'warning': logging.WARNING,
        'error': logging.ERROR,
    }
    level = level_map[config.get('logging.level', 'info').lower()]

    root_logger = logging.getLogger()
    root_logger.setLevel(level)

    log_file = config.get('logging.file', '-')
    if log_file.lower() not in ('-', 'stdout'):
        handler = logging.handlers.WatchedFileHandler(log_file)
    else:
        handler = logging.StreamHandler()

    handler.setLevel(level)
    formatter = logging.Formatter('%(asctime)s - %(levelname)s - %(message)s')
    handler.setFormatter(formatter)
    root_logger.addHandler(handler)

    # Special case for requets.
    # Requests log "Starting new connection" in INFO
    # Requests log each query in DEBUG
    if level != logging.DEBUG:
        # When not in debug, log neither of above
        logger_request = logging.getLogger('requests')
        logger_request.setLevel(logging.WARNING)
    else:
        # Even in debug, don't log every query
        logger_request = logging.getLogger('requests')
        logger_request.setLevel(logging.INFO)


class StoredValue:
    """ Persistant store for value used by agent.

        Currently store in a json file
    """
    def __init__(self, filename):
        self.filename = filename
        self._content = {}
        self.reload()

    def reload(self):
        if os.path.exists(self.filename):
            with open(self.filename) as fd:
                self._content = json.load(fd)

    def save(self):
        try:
            # Don't simply use open. This file must have limited permission
            open_flags = os.O_WRONLY | os.O_CREAT
            fileno = os.open(self.filename, open_flags, 0o600)
            with os.fdopen(fileno, 'w') as fd:
                json.dump(self._content, fd)
        except IOError as exc:
            logging.warning('Failed to store file : %s', exc)

    def get(self, key, default=None):
        return self._content.get(key, default)

    def set(self, key, value):
        self._content[key] = value
        self.save()


class Core:
    def __init__(self, config):
        self.config = config
        self.stored_values = StoredValue(
            config.get(
                'agent.stored_values_file',
                '/var/lib/bleemeo/store.json'))
        self.checks = []
        self.last_facts = {}
        self.thresholds = {}

        self.is_terminating = threading.Event()
        self.bleemeo_connector = None
        self.influx_connector = None
        self.collectd_server = None
        self.scheduler = sched.scheduler(time.time, time.sleep)
        self.last_metrics = {}

        self.plugins_v1_mgr = stevedore.enabled.EnabledExtensionManager(
            namespace='bleemeo_agent.plugins_v1',
            invoke_on_load=True,
            invoke_args=(self,),
            check_func=self.check_plugin_v1,
            on_load_failure_callback=self.plugins_on_load_failure,
        )
        self._define_thresholds()
        self._schedule_metric_pull()

    def _define_thresholds(self):
        """ Fill self.thresholds from config.thresholds

            It mostly a "copy", only cpu_* are multiplied by the number of
            cpu cores.
        """
        num_core = multiprocessing.cpu_count()
        self.thresholds = copy.deepcopy(self.config.get('thresholds'))
        for key, value in self.thresholds.items():
            if key.startswith('cpu_'):
                for threshold_name in value:
                    value[threshold_name] *= num_core

    def _schedule_metric_pull(self):
        """ Schedule metric which are pulled
        """
        for (name, config) in self.config.get('metric.pull', {}).items():
            config_type = config.get('type', 'raw')
            interval = config.get('interval', 10)
            if config_type == 'raw':
                self.scheduler.enter(
                    random.randint(1, interval),
                    1,
                    bleemeo_agent.util.pull_raw_metric,
                    (self, name)
                )
            elif config_type == 'json':
                self.scheduler.enter(
                    random.randint(1, interval),
                    1,
                    bleemeo_agent.util.pull_json_metric,
                    (self, name)
                )
            else:
                logging.warning(
                    'Unsupported type "%s" for pulled metric %s',
                    config_type,
                    name
                )

    def run(self):
        try:
            self.setup_signal()
            self.start_threads()
            bleemeo_agent.checker.initialize_checks(self)
            self.periodic_check()
            self._purge_metrics()
            self.send_facts()
            self.send_process_info()
            self.scheduler.run()
        except (KeyboardInterrupt, StopIteration):
            pass
        finally:
            self.is_terminating.set()

    def setup_signal(self):
        """ Make kill (SIGKILL/SIGQUIT) send a KeyboardInterrupt
        """
        def handler(signum, frame):
            raise KeyboardInterrupt

        signal.signal(signal.SIGTERM, handler)
        signal.signal(signal.SIGQUIT, handler)

    def start_threads(self):

        if self.config.get('bleemeo.enabled', True):
            self.bleemeo_connector = (
                bleemeo_agent.bleemeo.BleemeoConnector(self))
            self.bleemeo_connector.start()

        if self.config.get('influxdb.enabled', True):
            self.influx_connector = (
                bleemeo_agent.influxdb.InfluxDBConnector(self))
            self.influx_connector.start()

        self.collectd_server = bleemeo_agent.collectd.Collectd(self)
        self.collectd_server.start()

        bleemeo_agent.web.start_server(self)

    def periodic_check(self):
        """ Run few periodic check:

            * that agent is not being terminated
            * call bleemeo_agent.checker.periodic_check
            * reschedule itself every 3 seconds
        """
        if self.is_terminating.is_set():
            raise StopIteration

        bleemeo_agent.checker.periodic_check(self)
        self.scheduler.enter(3, 1, self.periodic_check, ())

    def _purge_metrics(self):
        """ Remove old metrics from self.last_metrics

            Some metric may stay in last_metrics unupdated, for example
            when a process with PID=42 terminated, no metric will update the
            metric for this process.

            For this reason, from time to time, scan last_metrics and drop
            any value older than 6 minutes.
        """
        now = time.time()
        cutoff = now - 60 * 6

        def exclude_old_metric(item):
            return item['time'] >= cutoff

        # XXX: concurrent access with emit_metric.
        for (measurement, metrics) in self.last_metrics.items():
            self.last_metrics[measurement] = list(filter(
                exclude_old_metric, metrics))
        self.scheduler.enter(300, 1, self._purge_metrics, ())

    def send_facts(self):
        """ Send facts to Bleemeo SaaS and reschedule itself """
        # Note: even if we do not sent them to Bleemeo SaaS, calling this
        # method is still usefull. Web UI use last_facts.
        self.last_facts = bleemeo_agent.util.get_facts(self)
        if self.bleemeo_connector is not None:
            self.bleemeo_connector.publish(
                'api/v1/agent/facts/POST',
                json.dumps(self.last_facts))
        self.scheduler.enter(3600, 1, self.send_facts, ())

    def send_process_info(self):
        now = time.time()
        info = bleemeo_agent.util.get_processes_info()
        for process_info in info:
            self.emit_metric({
                'measurement': 'process_info',
                'time': now,
                'tags': {
                    'pid': str(process_info.pop('pid')),
                    'create_time': str(process_info.pop('create_time')),
                },
                'fields': process_info,
            })
        self.scheduler.enter(60, 1, self.send_process_info, ())

    def plugins_on_load_failure(self, manager, entrypoint, exception):
        logging.info('Plugin %s failed to load : %s', entrypoint, exception)

    def check_plugin_v1(self, extension):
        has_dependencies = extension.obj.dependencies_present()
        if not has_dependencies:
            return False

        logging.debug('Enable plugin %s', extension.name)
        return True

    def reload_config(self):
        self.config = bleemeo_agent.config.load_config()
        self.stored_values = StoredValue(
            self.config.get(
                'agent.stored_values_file',
                '/var/lib/bleemeo/store.json'))

        return self.config

    def _store_last_value(self, metric):
        """ Store the metric in self.last_matrics, replacing the previous value
        """
        metric_tags = metric['tags'].copy()
        if 'status' in metric_tags:
            del metric_tags['status']

        def exclude_same_metric(item):
            item_tags = item['tags']
            if 'status' in item_tags:
                item_tags = item_tags.copy()
                del item_tags['status']

            return item_tags != metric_tags

        # We use list(...) to force evaluation of the result and avoid a
        # possible memory leak. In Python3 filter return a "filter object".
        # Without list() we may end with a filter object on a filter object
        # on a filter object ...
        measurement = metric['measurement']
        # XXX: concurrent access.
        # Note: different thread should not access the SAME
        # measurement, so it should be safe.
        self.last_metrics[measurement] = list(filter(
            exclude_same_metric, self.last_metrics.get(measurement, [])))
        self.last_metrics[measurement].append(metric)

    def emit_metric(self, metric, store_last_value=True):
        """ Sent a metric to all configured output
        """
        metric = copy.deepcopy(metric)
        if 'status' in metric['tags']:
            del metric['tags']['status']

        if not metric.get('ignore'):
            self.check_threshold(metric)

        if store_last_value:
            self._store_last_value(metric)

        if not metric.get('ignore'):
            if 'ignore' in metric:
                del metric['ignore']

            if self.config.get('bleemeo.enabled', True):
                self.bleemeo_connector.emit_metric(copy.deepcopy(metric))
            if self.config.get('influxdb.enabled', True):
                self.influx_connector.emit_metric(copy.deepcopy(metric))

    def check_threshold(self, metric):
        """ Check if threshold is defined for given metric. If yes, check
            it and add a "status" tag.
        """
        threshold = self.thresholds.get(metric['measurement'])
        if threshold is None:
            return

        value = metric['fields'].get('value')
        if value is None:
            return

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

        metric['tags']['status'] = status

    def get_last_metric(self, name, tags):
        """ Return the last metric matching name and tags.

            None is returned if the metric is not found
        """
        if 'status' in tags:
            tags = tags.copy()
            del tags['status']

        for metric in self.last_metrics.get(name, []):
            metric_tags = metric['tags']
            if 'status' in metric_tags:
                metric_tags = metric_tags.copy()
                del metric_tags['status']

            if metric_tags == tags:
                return metric

        return None

    def get_last_metric_value(self, name, tags, default=None):
        """ Return value for given metric.

            It use self.get_last_metric and assume the metric only
            contains one field named "value".

            Return default if metric is not found or if the metric don't have
            a field "value".
        """
        metric = self.get_last_metric(name, tags)
        if metric is not None and 'value' in metric['fields']:
            return metric['fields']['value']
        else:
            return default

    def get_loads(self):
        """ Return (load1, load5, load15).

            Value are took from last_metrics, so collectd need to feed the
            value or "?" is used instead of real value
        """
        loads = []
        for term in [1, 5, 15]:
            metric = self.get_last_metric('system_load%s' % term, {})
            if metric is None:
                loads.append('?')
            else:
                loads.append('%s' % metric['fields']['value'])
        return loads

    def get_top_output(self):
        """ Return a top-like output
        """
        env = jinja2.Environment(
            loader=jinja2.PackageLoader('bleemeo_agent', 'templates'))
        template = env.get_template('top.txt')

        timestamp = 0
        for metric in self.last_metrics.get('process_info', []):
            timestamp = max(timestamp, metric['time'])

        if timestamp == 0:
            # use time from last cpu_* metrics
            metric = self.get_last_metric('cpu_idle', {})
            if metric is None:
                return 'top - waiting for metrics...'
            timestamp = metric['time']

        memory_total = psutil.virtual_memory().total

        processes = []
        # Sort process by CPU consumption (then PID, when cpu % is the same)
        # Since we want a descending order for CPU usage, we have
        # reverse=True... but for PID we want a ascending order. That's why we
        # use a negation for the PID.
        sorted_process = sorted(
            self.last_metrics.get('process_info', []),
            key=lambda x: (x['fields']['cpu_percent'], -int(x['tags']['pid'])),
            reverse=True)
        for metric in sorted_process:
            if metric['time'] < timestamp:
                # stale metric, probably a process that has terminated
                continue

            # convert status (like "sleeping", "running") to one char status
            status = {
                psutil.STATUS_RUNNING: 'R',
                psutil.STATUS_SLEEPING: 'S',
                psutil.STATUS_DISK_SLEEP: 'D',
                psutil.STATUS_STOPPED: 'T',
                psutil.STATUS_TRACING_STOP: 'T',
                psutil.STATUS_ZOMBIE: 'Z',
            }.get(metric['fields']['status'], '?')
            processes.append(
                ('%(pid)5s %(ppid)5s %(res)6d %(status)s '
                    '%(cpu)5.1f %(mem)4.1f %(cmd)s') %
                {
                    'pid': metric['tags']['pid'],
                    'ppid': metric['fields']['ppid'],
                    'res': metric['fields']['memory_rss'] / 1024,
                    'status': status,
                    'cpu': metric['fields']['cpu_percent'],
                    'mem':
                        float(metric['fields']['memory_rss']) / memory_total,
                    'cmd': metric['fields']['name'],
                })
            if len(processes) >= 25:
                # show only top-25 process (sorted by CPU consumption)
                break

        time_top = datetime.datetime.fromtimestamp(timestamp).time()
        time_top = time_top.replace(microsecond=0)
        uptime_second = bleemeo_agent.util.get_uptime()
        num_core = multiprocessing.cpu_count()
        return template.render(
            time_top=time_top,
            uptime=bleemeo_agent.util.format_uptime(uptime_second),
            users=int(self.get_last_metric_value('users_logged', {}, 0)),
            loads=', '.join(self.get_loads()),
            process_total='%3d' % self.get_last_metric_value(
                'process_total', {}, 0),
            process_running='%3d' % self.get_last_metric_value(
                'process_status_running', {}, 0),
            process_sleeping='%3d' % self.get_last_metric_value(
                'process_status_sleeping', {}, 0),
            process_stopped='%3d' % self.get_last_metric_value(
                'process_status_stopped', {}, 0),
            process_zombie='%3d' % self.get_last_metric_value(
                'process_status_zombies', {}, 0),
            cpu_user='%5.1f' % (
                self.get_last_metric_value('cpu_user', {}, 0) / num_core),
            cpu_system='%5.1f' % (
                self.get_last_metric_value('cpu_system', {}, 0) / num_core),
            cpu_nice='%5.1f' % (
                self.get_last_metric_value('cpu_nice', {}, 0) / num_core),
            cpu_idle='%5.1f' % (
                self.get_last_metric_value('cpu_idle', {}, 0)/num_core),
            cpu_wait='%5.1f' % (
                self.get_last_metric_value('cpu_wait', {}, 0) / num_core),
            mem_total='%8d' % (
                self.get_last_metric_value('mem_total', {}, 0)/1024),
            mem_used='%8d' % (
                self.get_last_metric_value('mem_used', {}, 0)/1024),
            mem_free='%8d' % (
                self.get_last_metric_value('mem_free', {}, 0)/1024),
            mem_buffered='%8d' % (
                self.get_last_metric_value('mem_buffered', {}, 0)/1024),
            mem_cached='%8d' % (
                self.get_last_metric_value('mem_cached', {}, 0)/1024),
            swap_total='%8d' % (
                self.get_last_metric_value('swap_total', {}, 0)/1024),
            swap_used='%8d' % (
                self.get_last_metric_value('swap_used', {}, 0)/1024),
            swap_free='%8d' % (
                self.get_last_metric_value('swap_free', {}, 0)/1024),
            processes=processes,
        )
