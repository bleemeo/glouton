from __future__ import absolute_import

import logging
import math
import socket
import threading
import time

import influxdb
import influxdb.exceptions
import requests
from six.moves import queue


class InfluxDBConnector(threading.Thread):

    def __init__(self, core):
        super(InfluxDBConnector, self).__init__()
        self.core = core

        self.db_name = self.core.config.get('influxdb.db_name', 'bleemeo')
        self.retention_policy_name = 'standard_policy'

        self.influx_client = None
        self._queue = queue.Queue(5000)

        # Used to avoid "flooding" logs about dropped messages
        self._queue_full_last_warning = 0
        self._queue_full_count_warning = 0

    def _do_connect(self):
        self.influx_client = influxdb.InfluxDBClient(
            host=self.core.config.get('influxdb.server.host', 'localhost'),
            port=self.core.config.get('influxdb.server.port', 8086),
        )
        try:
            self.influx_client.create_database(self.db_name)
            logging.info('Database %s created', self.db_name)
        except influxdb.client.InfluxDBClientError as exc:
            if exc.content != 'database already exists':
                raise

        self.influx_client.switch_database(self.db_name)
        self._create_retention_policy()

    def _connect(self):
        sleep_delay = 10
        while not self.core.is_terminating.is_set():
            try:
                self._do_connect()
                logging.debug('Connected to InfluxDB')
                break
            except (requests.exceptions.ConnectionError,
                    influxdb.exceptions.InfluxDBClientError):
                self.influx_client = None
                logging.info(
                    'Failed to connect to InfluxDB. Retrying in %s second',
                    sleep_delay)
                self.core.is_terminating.wait(sleep_delay)
                sleep_delay = min(sleep_delay * 2, 300)
                self._warn_queue_full()

    def _create_retention_policy(self, replication_factor=3):
        try:
            self.influx_client.create_retention_policy(
                self.retention_policy_name,
                '365d',
                replication_factor,
                default=True)
            logging.debug(
                'Retention policy %s created', self.retention_policy_name)
        except influxdb.client.InfluxDBClientError as exc:
            if ('replication factor must match cluster size' in exc.content
                    and replication_factor == 3):
                # assume it on development environement with a single node
                self._create_retention_policy(replication_factor=1)
            elif exc.content != 'retention policy already exists':
                raise

    def _process_queue(self):
        metrics = []
        try:
            while True:
                metric = self._queue.get_nowait()
                metrics.append(metric)
        except queue.Empty:
            pass

        if len(metrics) == 0:
            return

        try:
            self.influx_client.write_points(
                metrics,
                retention_policy=self.retention_policy_name,
                time_precision='s')
            self.core.update_last_report()
        except (requests.exceptions.ConnectionError,
                influxdb.exceptions.InfluxDBClientError):
            logging.debug('InfluxDB write error... retrying')
            # re-enqueue the metric
            for metric in metrics:
                self._enqueue(metric)

    def run(self):
        self._connect()

        while not self.core.is_terminating.is_set():
            self._process_queue()
            self._warn_queue_full()
            self.core.is_terminating.wait(10)

    def emit_metric(self, metric):
        # InfluxDB can't store "NaN" (not a number)...
        # drop any metric that contain a NaN
        value = metric.pop('value')
        if isinstance(value, float) and math.isnan(value):
            return

        tag = metric.pop('tag')
        status = metric.pop('status')
        metric['tags'] = {}
        if tag is not None:
            metric['tags']['item'] = tag
        if status is not None:
            metric['tags']['status'] = status

        # InfluxDB want an integer for timestamp, not a float
        metric['time'] = int(metric['time'])

        metric['fields'] = {'value': value}
        metric['tags']['hostname'] = socket.getfqdn()

        self._enqueue(metric)

    def _enqueue(self, metric):
        try:
            self._queue.put_nowait(metric)
        except queue.Full:
            self._queue_full_count_warning += 1

    def _warn_queue_full(self):
        """ Log a warning is metric were dropped
        """
        now = time.time()
        if (self._queue_full_count_warning
                and self._queue_full_last_warning < now - 60):
            logging.warning(
                'InfluxDB connector: %s metric(s) were dropped due to '
                'overflow of the sending queue',
                self._queue_full_count_warning)
            self._queue_full_last_warning = now
            self._queue_full_count_warning = 0
