#
#  Copyright 2017 Bleemeo
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


import hashlib
import json
import logging
import os
import time

from bleemeo_agent.telegraf import services_sorted
import bleemeo_agent.util


JMX_METRICS = {
    'java': [
        {
            'name': 'jvm_heap_used',
            'mbean': 'java.lang:type=Memory',
            'attribute': 'HeapMemoryUsage',
            'path': 'used',
        },
        {
            'name': 'jvm_non_heap_used',
            'mbean': 'java.lang:type=Memory',
            'attribute': 'NonHeapMemoryUsage',
            'path': 'used',
        },
        {
            'name': 'jvm_gc',
            'mbean': 'java.lang:type=GarbageCollector,name=*',
            'attribute': 'CollectionCount',
            'derive': True,
            'sum': True,
            'typeNames': 'name',
        },
        {
            'name': 'jvm_gc_time',
            'mbean': 'java.lang:type=GarbageCollector,name=*',
            'attribute': 'CollectionTime',
            'derive': True,
            'sum': True,
            'typeNames': 'name',
        },
        {
            'name': 'jvm_gc_utilization',
            'mbean': 'java.lang:type=GarbageCollector,name=*',
            'attribute': 'CollectionTime',
            'derive': True,
            'sum': True,
            'typeNames': 'name',
            'scale': 0.1,  # time is in ms/s. Convert in %
        },
    ],
    'cassandra': [
        {
            'name': 'read_requests',
            'mbean':
                'org.apache.cassandra.metrics:'
                'type=ClientRequest,scope=Read,name=Latency',
            'attribute': 'Count',
            'derive': True,
        },
        {
            'name': 'read_time',
            'mbean':
                'org.apache.cassandra.metrics:'
                'type=ClientRequest,scope=Read,name=TotalLatency',
            'attribute': 'Count',
            'scale': 0.001,  # convert from microsecond to millisecond
            'derive': True,
        },
        {
            'name': 'write_requests',
            'mbean':
                'org.apache.cassandra.metrics:'
                'type=ClientRequest,scope=Write,name=Latency',
            'attribute': 'Count',
            'derive': True,
        },
        {
            'name': 'write_time',
            'mbean':
                'org.apache.cassandra.metrics:'
                'type=ClientRequest,scope=Write,name=TotalLatency',
            'attribute': 'Count',
            'scale': 0.001,  # convert from microsecond to millisecond
            'derive': True,
        },
        {
            'name': 'bloom_filter_false_ratio',
            'mbean':
                'org.apache.cassandra.metrics:'
                'type=Type,name=BloomFilterFalseRatio',
            'attribute': 'Value',
            'scale': 100,  # convert from ratio (0 to 1) to percent
        },
        {
            'name': 'sstable',
            'mbean':
                'org.apache.cassandra.metrics:'
                'type=Table,name=LiveSSTableCount',
            'attribute': 'Value',
        },
    ],
}


def update_discovery(core):
    try:
        current_config.write_config(core)
    except:
        logging.warning(
            'Failed to write jmxtrans configuration. '
            'Continuing with current configuration'
        )
        logging.debug('exception is:', exc_info=True)


class Jmxtrans:
    """ Configure and process graphite data from jmxtrans
    """

    def __init__(self, graphite_client):
        self.core = graphite_client.core
        self.graphite_client = graphite_client

        self.last_timestamp = 0

        # used to compute derivated values
        self._raw_value = {}

        self._sum_value = {}
        self._ratio_value = {}

        self._values_cache = {}

        self.last_timestamp = 0

        self.last_purge = bleemeo_agent.util.get_clock()

    def close(self):
        self.flush(self.last_timestamp)

    def emit_metric(self, name, timestamp, value):
        if timestamp - self.last_timestamp > 1:
            self.flush(self.last_timestamp)
        self.last_timestamp = timestamp

        clock = bleemeo_agent.util.get_clock()
        if clock - self.last_purge > 60:
            self.purge_metrics()
            self.last_purge = clock

        # Example of name: jmxtrans.f5[...]d6.20[...]7b.HeapMemoryUsage_used
        part = name.split('.')

        if len(part) == 4:
            (_, md5_service, md5_mbean, attr) = part
            type_names = None
        elif len(part) == 5:
            (_, md5_service, md5_mbean, type_names, attr) = part
        else:
            logging.debug(
                'Unexpected number of part for jmxtrans metrics: %s',
                name,
            )
            return

        try:
            (service_name, instance) = current_config.to_service[md5_service]
        except KeyError:
            logging.debug('Service not found for %s', name)
            return

        metric_key = (md5_service, md5_mbean, attr)
        try:
            jmx_metrics = current_config.to_metric[metric_key]
        except KeyError:
            return

        for jmx_metric in jmx_metrics:
            new_name = '%s_%s' % (service_name, jmx_metric['name'])

            if instance is not None and type_names is not None:
                item = instance + '_' + type_names
            elif type_names is not None:
                item = type_names
            else:
                item = instance

            if jmx_metric.get('derive'):
                new_value = self.get_derivate(
                    new_name, item, timestamp, value
                )
                if new_value is None:
                    continue
            else:
                new_value = value

            if jmx_metric.get('scale'):
                new_value = new_value * jmx_metric['scale']

            metric = {
                'measurement': new_name,
                'time': timestamp,
                'value': new_value,
                'service': service_name,
                'instance': instance,
            }

            if item is not None:
                metric['item'] = item

            if jmx_metric.get('sum', False):
                item = instance
                self._sum_value.setdefault(
                    (new_name, instance, service_name), (jmx_metric, [])
                )[1].append(new_value)
                continue
            elif jmx_metric.get('ratio') is not None:
                key = (new_name, instance, service_name)
                self._ratio_value[key] = (jmx_metric, new_value)
                continue

            if new_name in current_config.divisors:
                self._values_cache[(new_name, item)] = (timestamp, new_value)

            self.core.emit_metric(metric)

    def packet_finish(self):
        """ Called when graphite_client finished processing one TCP packet
        """
        pass

    def flush(self, timestamp):
        for key, (jmx_metric, values) in self._sum_value.items():
            (name, item, service_name) = key
            metric = {
                'measurement': name,
                'time': timestamp,
                'value': sum(values),
                'service': service_name,
                'instance': item,
            }

            if item is not None:
                metric['item'] = item

            if jmx_metric.get('ratio') is not None:
                self._ratio_value[key] = (jmx_metric, sum(values))
            else:
                if name in current_config.divisors:
                    self._values_cache[(name, item)] = (timestamp, sum(values))
                self.core.emit_metric(metric)
        self._sum_value = {}

        for key, (jmx_metric, value) in self._ratio_value.items():
            (name, item, service_name) = key
            divisor_name = "%s_%s" % (service_name, jmx_metric['ratio'])
            divisor = self._values_cache.get((divisor_name, item))

            new_value = None
            if divisor is None or divisor[0] != timestamp:
                logging.debug(
                    'Failed to compute ratio metric %s (%s) at time %s',
                    name,
                    item,
                    timestamp,
                )
            elif divisor_metric['value'] != 0:
                metric = {
                    'measurement': name,
                    'time': timestamp,
                    'value': value / divisor_metric['value'],
                    'service': service_name,
                    'instance': item,
                }

                if item is not None:
                    metric['item'] = item

                self.core.emit_metric(metric)

        self._ratio_value = {}

    def get_derivate(self, name, item, timestamp, value):
        """ Return derivate of a COUNTER (e.g. something that only goes upward)
        """
        # self.lock is acquired by caller
        (old_timestamp, old_value) = self._raw_value.get(
            (name, item), (None, None)
        )
        self._raw_value[(name, item)] = (timestamp, value)
        if old_timestamp is None:
            return None

        delta = value - old_value
        delta_time = timestamp - old_timestamp

        if delta_time == 0:
            return None

        if delta < 0:
            return None

        return delta / delta_time

    def purge_metrics(self):
        """ Remove old metrics from self._raw_value
        """
        now = time.time()
        cutoff = now - 60 * 6

        self._raw_value = {
            key: (timestamp, value)
            for key, (timestamp, value) in self._raw_value.items()
            if timestamp >= cutoff
        }

        self._values_cache = {
            key: (timestamp, value)
            for key, (timestamp, value) in self._values_cache.items()
            if timestamp >= cutoff
        }


class JmxConfig:

    def __init__(self, core):
        self.core = core

        # map md5_service to (service_name, instance)
        self.to_service = {}

        # map (md5_service, md5_bean, attr) to a list of jmx_metrics
        self.to_metric = {}

        # list of divisor for a ratio
        self.divisors = set()

    def get_jmxtrans_config(self, empty=False):
        config = {
            'servers': []
        }

        to_service = {}
        to_metric = {}
        divisors = set()

        if empty:
            return json.dumps(config)

        output_config = {
            "@class":
                "com.googlecode.jmxtrans.model.output.GraphiteWriterFactory",
            "rootPrefix": "jmxtrans",
            "port": self.core.config.get(
                'graphite.listener.port', 2003
            ),
            "host": self.core.config.get(
                'graphite.listener.address', '127.0.0.1'
            ),
            "flushStrategy": "timeBased",
            "flushDelayInSeconds": 10,
        }
        if output_config['host'] == '0.0.0.0':
            output_config['host'] = '127.0.0.1'

        for (key, service_info) in services_sorted(self.core.services.items()):
            if not service_info.get('active', True):
                continue

            (service_name, instance) = key
            if service_info.get('address') is None and instance is not None:
                # Address is None if this check is associated with a stopped
                # container. In such case, no metrics could be gathered.
                continue

            if 'jmx_port' in service_info and 'address' in service_info:
                jmx_port = service_info['jmx_port']

                md5_service = hashlib.md5(service_name.encode('utf-8'))
                if instance is not None:
                    md5_service.update(instance.encode('utf-8'))
                md5_service = md5_service.hexdigest()

                to_service[md5_service] = (service_name, instance)

                server = {
                    'host': service_info['address'],
                    'alias': md5_service,
                    'port': jmx_port,
                    'queries': [],
                    'outputWriters': [output_config],
                    'runPeriodSeconds': 10,
                }

                if 'jmx_username' in service_info:
                    server['username'] = service_info['jmx_username']
                    server['password'] = service_info['jmx_password']

                jmx_metrics = list(service_info.get('jmx_metrics', []))
                jmx_metrics.extend(JMX_METRICS['java'])
                jmx_metrics.extend(JMX_METRICS.get(service_name, []))

                for jmx_metric in jmx_metrics:
                    if 'path' in jmx_metric:
                        attr = '%s_%s' % (
                            jmx_metric['attribute'], jmx_metric['path'],
                        )
                    else:
                        attr = jmx_metric['attribute']

                    md5_mbean = hashlib.md5(
                        jmx_metric['mbean'].encode('utf-8')
                    ).hexdigest()

                    metric_key = (md5_service, md5_mbean, attr)
                    to_metric.setdefault(metric_key, []).append(jmx_metric)

                    if 'ratio' in jmx_metric:
                        divisors.add(
                            "%s_%s" % (service_name, jmx_metric['ratio'])
                        )

                    query = {
                        "obj": jmx_metric['mbean'],
                        "outputWriters": [],
                        "resultAlias": md5_mbean,
                    }
                    query['attr'] = [jmx_metric['attribute']]

                    if 'typeNames' in jmx_metric:
                        query['typeNames'] = (
                            jmx_metric['typeNames'].split(',')
                        )

                    server['queries'].append(query)
                config['servers'].append(server)

        self.to_metric = to_metric
        self.to_service = to_service
        self.divisors = divisors

        return json.dumps(config, sort_keys=True)

    def write_config(self, core):
        if self.core is None:
            self.core = core

        config = self.get_jmxtrans_config()

        config_path = self.core.config.get(
            'jmxtrans.config_file',
            '/var/lib/jmxtrans/bleemeo-generated.json',
        )

        if os.path.exists(config_path):
            with open(config_path) as fd:
                current_content = fd.read()

            if config == current_content:
                logging.debug('jmxtrans already configured')
                return

        if (config == '{}' == self.get_jmxtrans_config(empty=True)
                and not os.path.exists(config_path)):
            logging.debug(
                'jmxtrans generated config would be empty, skip writing it'
            )
            return

        # Don't simply use open. This file must have limited permission
        # since it may contains password
        open_flags = os.O_WRONLY | os.O_CREAT | os.O_TRUNC
        try:
            fileno = os.open(config_path, open_flags, 0o600)
        except OSError:
            if not os.path.exists(config_path):
                logging.debug(
                    'Failed to write jmxtrans configuration.'
                    ' Target file does not exists,'
                    ' bleemeo-agent-jmx is installed ?'
                )
                return
            raise
        with os.fdopen(fileno, 'w') as fd:
            fd.write(config)


current_config = JmxConfig(None)
