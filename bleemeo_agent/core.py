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
            open_flags = os.O_WRONLY | os.O_CREAT | os.O_TRUNC
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
            hours=24,
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
            'tag': None,
            'status': None,
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
            (measurement, tag): metric
            for ((measurement, tag), metric) in self.last_metrics.items()
            if metric['time'] >= cutoff
        }

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
        tag = metric['tag']
        measurement = metric['measurement']
        self.last_metrics[(measurement, tag)] = metric

    def emit_metric(self, metric, store_last_value=True):
        """ Sent a metric to all configured output
        """
        metric = copy.deepcopy(metric)

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

        metric['status'] = status

    def get_last_metric(self, name, tag):
        """ Return the last metric matching name and tag.

            None is returned if the metric is not found
        """
        return self.last_metrics.get((name, tag), None)

    def get_last_metric_value(self, name, tag, default=None):
        """ Return value for given metric.

            Return default if metric is not found.
        """
        metric = self.get_last_metric(name, tag)
        if metric is not None:
            return metric['value']
        else:
            return default
