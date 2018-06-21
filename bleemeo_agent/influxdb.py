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

import bleemeo_agent.other_types
import bleemeo_agent.util


class InfluxDBConnector(threading.Thread):

    def __init__(self, core):
        super(InfluxDBConnector, self).__init__()
        self.core = core

        self.db_name = self.core.config.get('influxdb.db_name', 'metrics')
        self.retention_policy_name = 'standard_policy'

        self.influx_client = None
        self._queue = queue.Queue(5000)

        # Used to avoid "flooding" logs about dropped messages
        self._queue_full_last_warning = None
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
        timeout = 3
        try:
            while True:
                metric = self._queue.get(timeout=timeout)
                timeout = 0  # Only wait for the first get
                metrics.append(metric)
        except queue.Empty:
            pass

        if not metrics:
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
            time.sleep(3)
            # re-enqueue the metric
            for metric in metrics:
                self._enqueue(metric)

    def run(self):
        self._connect()

        while not self.core.is_terminating.is_set():
            self._process_queue()
            self._warn_queue_full()

    def emit_metric(self, metric_point):
        # InfluxDB can't store "NaN" (not a number)...
        # drop any metric that contain a NaN
        value = metric_point.value
        if isinstance(value, float) and math.isnan(value):
            return

        influx_metric = {
            'measurement': metric_point.label,
            'time': int(metric_point.time),
            'fields': {
                'value': metric_point.value,
            },
            'tags': {
            },
        }

        if metric_point.item:
            influx_metric['tags']['item'] = metric_point.item
        if metric_point.status_code is not None:
            influx_metric['tags']['status'] = metric_point.status_code

        if self.core.agent_uuid is None:
            influx_metric['tags']['hostname'] = socket.getfqdn()
        else:
            influx_metric['tags']['agent_uuid'] = self.core.agent_uuid

        self._enqueue(influx_metric)

    def _enqueue(self, metric):
        try:
            self._queue.put_nowait(metric)
        except queue.Full:
            self._queue_full_count_warning += 1

    def _warn_queue_full(self):
        """ Log a warning is metric were dropped
        """
        clock_now = bleemeo_agent.util.get_clock()
        if (self._queue_full_count_warning
                and (
                    self._queue_full_last_warning is None
                    or self._queue_full_last_warning < clock_now - 60)):
            logging.warning(
                'InfluxDB connector: %s metric(s) were dropped due to '
                'overflow of the sending queue',
                self._queue_full_count_warning)
            self._queue_full_last_warning = clock_now
            self._queue_full_count_warning = 0
