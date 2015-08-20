import copy
import datetime
import json
import logging
import logging.config
import multiprocessing
import os
import signal
import threading
import time

import apscheduler.schedulers.blocking
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
    logging.basicConfig()

    try:
        core = Core()
        core.run()
    except Exception:
        logging.critical(
            'Unhandled error occured. Agent will terminate',
            exc_info=True)
    finally:
        logging.info('Agent stopped')


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
    def __init__(self):
        self.reload_config()
        self._config_logger()
        logging.info('Agent starting...')
        self.checks = []
        self.last_facts = {}
        self.thresholds = {}
        self.top_info = None

        self.is_terminating = threading.Event()
        self.bleemeo_connector = None
        self.influx_connector = None
        self.collectd_server = None
        self.scheduler = apscheduler.schedulers.blocking.BlockingScheduler()
        self.last_metrics = {}
        self.last_report = datetime.datetime.fromtimestamp(0)

        self.plugins_v1_mgr = stevedore.enabled.EnabledExtensionManager(
            namespace='bleemeo_agent.plugins_v1',
            invoke_on_load=True,
            invoke_args=(self,),
            check_func=self.check_plugin_v1,
            on_load_failure_callback=self.plugins_on_load_failure,
        )
        self._define_thresholds()
        self._schedule_metric_pull()

    def _config_logger(self):
        logger_config = {
            'version': 1,
            'disable_existing_loggers': False,
        }
        logger_config.update(self.config.get('logging', {}))
        logging.config.dictConfig(logger_config)

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
            interval = config.get('interval', 10)
            self.scheduler.add_job(
                bleemeo_agent.util.pull_raw_metric,
                args=(self, name),
                trigger='interval',
                seconds=interval,
            )

    def run(self):
        try:
            self.setup_signal()
            self.start_threads()
            self.schedule_tasks()
            bleemeo_agent.checker.initialize_checks(self)
            self.scheduler.start()
        except KeyboardInterrupt:
            pass
        finally:
            self.is_terminating.set()
            self.scheduler.shutdown()

    def setup_signal(self):
        """ Make kill (SIGKILL/SIGQUIT) send a KeyboardInterrupt
        """
        def handler(signum, frame):
            raise KeyboardInterrupt

        signal.signal(signal.SIGTERM, handler)
        signal.signal(signal.SIGQUIT, handler)

    def schedule_tasks(self):
        self.scheduler.add_job(
            bleemeo_agent.checker.periodic_check,
            args=(self,),
            trigger='interval',
            seconds=3,
        )
        self.scheduler.add_job(
            self._purge_metrics,
            trigger='interval',
            minutes=5,
        )
        self.scheduler.add_job(
            self.send_facts,
            next_run_time=datetime.datetime.now(),
            trigger='interval',
            hours=1,
        )
        self.scheduler.add_job(
            self._gather_metrics,
            trigger='interval',
            seconds=10,
        )
        self.scheduler.add_job(
            self.send_top_info,
            trigger='interval',
            seconds=3,
        )

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

    def _gather_metrics(self):
        """ Gather and send some metric missing from collectd

            Currently only uptime is sent.
        """
        uptime_seconds = bleemeo_agent.util.get_uptime()
        now = time.time()

        self.emit_metric({
            'measurement': 'uptime',
            'tags': {},
            'time': now,
            'value': uptime_seconds,
        })

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

    def send_facts(self):
        """ Send facts to Bleemeo SaaS """
        # Note: even if we do not sent them to Bleemeo SaaS, calling this
        # method is still usefull. Web UI use last_facts.
        self.last_facts = bleemeo_agent.util.get_facts(self)
        if self.bleemeo_connector is not None:
            self.bleemeo_connector.publish(
                'api/v1/agent/facts/POST',
                json.dumps(self.last_facts))

    def send_top_info(self):
        self.top_info = bleemeo_agent.util.get_top_info()
        if self.bleemeo_connector is not None:
            self.bleemeo_connector.publish_top_info(self.top_info)

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

    def update_last_report(self):
        self.last_report = max(datetime.datetime.now(), self.last_report)

    def check_threshold(self, metric):
        """ Check if threshold is defined for given metric. If yes, check
            it and add a "status" tag.
        """
        threshold = self.thresholds.get(metric['measurement'])
        if threshold is None:
            return

        value = metric['value']
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

            Return default if metric is not found.
        """
        metric = self.get_last_metric(name, tags)
        if metric is not None:
            return metric['value']
        else:
            return default
