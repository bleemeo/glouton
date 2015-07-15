from __future__ import absolute_import

import logging
import math
import socket
import threading

import influxdb
import influxdb.exceptions
import requests
from six.moves import queue


class InfluxDBConnector(threading.Thread):

    def __init__(self, core):
        super(InfluxDBConnector, self).__init__()
        self.core = core

        self.db_name = 'bleemeo'
        self.retention_policy_name = 'standard_policy'

        self.influx_client = None
        self._queue = queue.Queue(1000)

    def _do_connect(self):
        self.influx_client = influxdb.InfluxDBClient()
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
        try:
            while True:
                metric = self._queue.get_nowait()
                metric['tags']['hostname'] = socket.getfqdn()
                try:
                    self.influx_client.write_points(
                        [metric], retention_policy=self.retention_policy_name)
                except (requests.exceptions.ConnectionError,
                        influxdb.exceptions.InfluxDBClientError):
                    logging.debug('InfluxDB write error... retrying')
                    self.emit_metric(metric)  # re-enqueue the metric
                    break
        except queue.Empty:
            pass

    def run(self):
        self._connect()

        while not self.core.is_terminating.is_set():
            self._process_queue()
            self.core.is_terminating.wait(10)

    def emit_metric(self, metric):
        # InfluxDB can't store "NaN" (not a number)...
        # drop any metric that contain a NaN
        # TODO: the NaN filter only support metric with ONE fields named
        # "value". Currently only collectd generate NaN value are use one
        # the field "value".
        if math.isnan(metric['fields'].get('value', 0.0)):
            return

        # InfluxDB want an integer for timestamp, not a float
        metric['time'] = int(metric['time'])

        try:
            self._queue.put_nowait(metric)
        except queue.Full:
            logging.warning(
                'InfluxDB connector queue is full. Dropping metrics')
