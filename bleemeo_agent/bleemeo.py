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
# pylint: disable=too-many-lines

import collections
import copy
import datetime
import hashlib
import json
import logging
import os
import random
import socket
import ssl
import threading
import time
import zlib

import paho.mqtt.client as mqtt
import requests
from six.moves import queue
from six.moves import urllib_parse

import bleemeo_agent
import bleemeo_agent.util


MQTT_QUEUE_MAX_SIZE = 2000
REQUESTS_TIMEOUT = 15.0

# API don't accept full length for some object.
API_METRIC_ITEM_LENGTH = 100
API_CONTAINER_NAME_LENGTH = 100
API_SERVICE_INSTANCE_LENGTH = 50


MetricThreshold = collections.namedtuple('MetricThreshold', (
    'low_warning',
    'low_critical',
    'high_warning',
    'high_critical',
))
Metric = collections.namedtuple('Metric', (
    'uuid',
    'label',
    'item',
    'service_uuid',
    'container_uuid',
    'status_of',
    'thresholds',
    'unit',
    'unit_text',
    'active',
))
MetricRegistrationReq = collections.namedtuple('MetricRegistrationReq', (
    'label',
    'item',
    'service_label',
    'instance',
    'container_name',
    'status_of_label',
    'last_status',
    'last_problem_origins',
    'last_seen',
))
Service = collections.namedtuple('Service', (
    'uuid',
    'label',
    'instance',
    'listen_addresses',
    'exe_path',
    'stack',
    'active',
))
Container = collections.namedtuple('Container', (
    'uuid', 'name', 'docker_id', 'inspect_hash',
))
AgentConfig = collections.namedtuple('AgentConfig', (
    'uuid',
    'name',
    'docker_integration',
    'topinfo_period',
    'metrics_whitelist',
    'metrics_blacklist',
))
AgentFact = collections.namedtuple('AgentFact', (
    'uuid', 'key', 'value',
))

STATUS_OK = 0
STATUS_WARNING = 1
STATUS_CRITICAL = 2
STATUS_UNKNOWN = 3

STATUS_NAME_TO_CODE = {
    'ok': STATUS_OK,
    'warning': STATUS_WARNING,
    'critical': STATUS_CRITICAL,
    'unknown': STATUS_UNKNOWN,
}


def services_to_short_key(services):
    reverse_lookup = {}
    for key, service_info in services.items():
        (service_name, instance) = key
        short_key = (service_name, instance[:API_SERVICE_INSTANCE_LENGTH])

        if short_key not in reverse_lookup:
            reverse_lookup[short_key] = key
        else:
            other_info = services[reverse_lookup[short_key]]
            if (service_info.get('active', True)
                    and not other_info.get('active', True)):
                # Prefer the active service
                reverse_lookup[short_key] = key
            elif (service_info.get('container_id', '') >
                  other_info.get('container_id', '')):
                # Completly arbitrary condition that will hopefully keep
                # a consistent result whatever the services order is.
                reverse_lookup[short_key] = key

    result = {
        key: short_key for (short_key, key) in reverse_lookup.items()
    }
    return result


def sort_docker_inspect(inspect):
    """ Sort the docker inspect to have consistent hash value

        Mounts order does not matter but is not consistent between
        call to docker inspect (at least on minikube).
    """
    if inspect.get('Mounts'):
        inspect['Mounts'].sort(
            key=lambda x: (x.get('Source', ''), x.get('Destination', '')),
        )
    return inspect


class ApiError(Exception):
    def __init__(self, response):
        super(ApiError, self).__init__()
        self.response = response


class BleemeoAPI:
    """ class to handle communication with Bleemeo API
    """

    def __init__(self, base_url, auth, user_agent):
        self.auth = auth
        self.user_agent = user_agent
        self.base_url = base_url
        self.requests_session = requests.Session()
        self._jwt_token = None

    def _get_jwt(self):
        url = urllib_parse.urljoin(self.base_url, 'v1/jwt-auth/')
        response = self.requests_session.post(
            url,
            headers={
                'X-Requested-With': 'XMLHttpRequest',
                'Content-type': 'application/json',
            },
            data=json.dumps({
                'username': self.auth[0],
                'password': self.auth[1],
            }),
            timeout=10,
        )
        if response.status_code != 200:
            logging.debug(
                'Failed to retrieve JWT, status=%s, content=%s',
                response.status_code,
                response.text,
            )
            raise ApiError(response)
        return response.json()['token']

    def api_call(self, url, method='get', params=None, data=None):
        headers = {
            'X-Requested-With': 'XMLHttpRequest',
            'User-Agent': self.user_agent,
        }
        if data:
            headers['Content-type'] = 'application/json'

        url = urllib_parse.urljoin(self.base_url, url)

        first_call = True
        while True:
            if self._jwt_token is None:
                self._jwt_token = self._get_jwt()
            headers['Authorization'] = 'JWT %s' % self._jwt_token
            response = self.requests_session.request(
                method=method,
                url=url,
                headers=headers,
                params=params,
                data=data,
                timeout=REQUESTS_TIMEOUT,
            )
            if response.status_code == 401 and first_call:
                # If authentication failed for the first call,
                # retry immediatly
                logging.debug('JWT token expired, retry authentication')
                self._jwt_token = None
                first_call = False
                continue
            first_call = False

            return response

    def api_iterator(self, url, params=None):
        """ Call Bleemeo API on a list endpoints and return a iterator
            that request all pages
        """
        if params is None:
            params = {}
        else:
            params = params.copy()

        if 'page_size' not in params:
            params['page_size'] = 100

        data = {'next': url}
        while data['next']:
            response = self.api_call(
                data['next'],
                params=params,
            )

            if response.status_code == 404:
                break

            if response.status_code != 200:
                raise ApiError(response)

            # After first call, params are present in URL data['next']
            params = None

            data = response.json()
            for item in data['results']:
                yield item


def convert_docker_date(input_date):
    """ Take a string representing a date using Docker inspect format and return
        None if the date is "0001-01-01T00:00:00Z"
    """
    if input_date is None:
        return None

    if input_date == '0001-01-01T00:00:00Z':
        return None
    return input_date


def get_listen_addresses(service_info):
    """ Return the listen_addresses for a service_info
    """
    try:
        address = socket.gethostbyname(service_info['address'])
    except (socket.gaierror, TypeError, KeyError):
        # gaierror => unable to resolv name
        # TypeError => service_info['address'] is None (happen when
        #              service is on a stopped container)
        # KeyError => no 'address' in service_info (happen when service
        #             is a customer defined using Nagios check).
        address = None

    netstat_ports = {}
    for port_proto, address in service_info.get('netstat_ports', {}).items():
        if port_proto == 'unix':
            continue
        port = int(port_proto.split('/')[0])
        if service_info.get('ignore_high_port') and port > 32000:
            continue
        netstat_ports[port_proto] = address

    if service_info.get('port') is not None and not netstat_ports:
        if service_info['protocol'] == socket.IPPROTO_TCP:
            netstat_ports['%s/tcp' % service_info['port']] = address
        elif service_info['protocol'] == socket.IPPROTO_UDP:
            netstat_ports['%s/udp' % service_info['port']] = address

    return set(
        '%s:%s' % (address, port_proto)
        for (port_proto, address) in netstat_ports.items()
    )


class BleemeoCache:
    # pylint: disable=too-many-instance-attributes
    """ In-memory cache backed with state file for Bleemeo API
        objects

        All information stored in this cache could be lost on Agent restart
        (e.g. rebuilt from Bleemeo API)
    """
    # Version 1: initial version
    # Version 2: Added docker_id to Containers
    # Version 3: Added active to Metric
    CACHE_VERSION = 3

    def __init__(self, state, skip_load=False):
        self._state = state

        self.metrics = {}
        self.services = {}
        self.tags = []
        self.containers = {}
        self.facts = {}
        self.current_config = None
        self.next_config_at = None

        self.metrics_by_labelitem = {}
        self.containers_by_name = {}
        self.containers_by_docker_id = {}
        self.services_by_labelinstance = {}

        if not skip_load:
            cache = self._state.get("_bleemeo_cache")
            if cache is None:
                self._load_compatibility()
            self._reload()

    def copy(self):
        new = BleemeoCache(self._state, skip_load=True)
        new.metrics = self.metrics.copy()
        new.services = self.services.copy()
        new.tags = list(self.tags)
        new.containers = self.containers.copy()
        new.facts = self.facts.copy()
        new.current_config = self.current_config
        new.next_config_at = self.next_config_at
        new.update_lookup_map()
        return new

    def _reload(self):
        self._state.reload()
        cache = self._state.get("_bleemeo_cache")

        if cache['version'] > self.CACHE_VERSION:
            return

        self.metrics = {}
        self.services = {}
        self.tags = list(cache['tags'])
        self.containers = {}

        for metric_uuid, values in cache['metrics'].items():
            values[6] = MetricThreshold(*values[6])
            if cache['version'] < 3:
                # Add default "True" to active field.
                # It will be fixed on next full synchronization that
                # will happen quickly
                values.append(True)
            self.metrics[metric_uuid] = Metric(*values)

        for service_uuid, values in cache['services'].items():
            values[3] = set(values[3])
            self.services[service_uuid] = Service(*values)

        # Can't load containers from cache version 1
        if cache['version'] > 1:
            for container_uuid, values in cache['containers'].items():
                self.containers[container_uuid] = Container(*values)

        config = cache.get('current_config')
        if config:
            config[4] = set(config[4])
            config[5] = set(config[5])
            self.current_config = AgentConfig(*config)

        next_config_at = cache.get('next_config_at')
        if next_config_at:
            self.next_config_at = (
                datetime.datetime
                .utcfromtimestamp(next_config_at)
                .replace(tzinfo=datetime.timezone.utc)
            )

        self.update_lookup_map()

    def update_lookup_map(self):
        self.metrics_by_labelitem = {}
        self.containers_by_name = {}
        self.containers_by_docker_id = {}
        self.services_by_labelinstance = {}

        for metric in self.metrics.values():
            self.metrics_by_labelitem[(metric.label, metric.item)] = metric

        for container in self.containers.values():
            self.containers_by_name[container.name] = container
            self.containers_by_docker_id[container.docker_id] = container

        for service in self.services.values():
            key = (service.label, service.instance)
            self.services_by_labelinstance[key] = service

    def get_core_thresholds(self):
        """ Return thresholds in a format adapted for bleemeo_agent.core
        """
        thresholds = {}
        for metric in self.metrics.values():
            thresholds[(metric.label, metric.item)] = (
                metric.thresholds._asdict()
            )
        return thresholds

    def get_core_units(self):
        """ Return units in a format adapted for bleemeo_agent.core
        """
        units = {}
        for metric in self.metrics.values():
            units[(metric.label, metric.item)] = (
                metric.unit, metric.unit_text
            )
        return units

    def save(self):
        cache = {
            'version': self.CACHE_VERSION,
            'metrics': self.metrics,
            'services': self.services,
            'tags': self.tags,
            'containers': self.containers,
            'current_config': self.current_config,
            'next_config_at':
                self.next_config_at.timestamp()
                if self.next_config_at else None,
        }
        self._state.set('_bleemeo_cache', cache)

    def _load_compatibility(self):
        # pylint: disable=too-many-locals
        # pylint: disable=too-many-branches
        """ Load cache information from old keys and remove thems
        """
        metrics_uuid = self._state.get_complex_dict('metrics_uuid', {})
        thresholds = self._state.get_complex_dict('thresholds', {})
        services_uuid = self._state.get_complex_dict('services_uuid', {})

        for (key, metric_uuid) in metrics_uuid.items():
            (metric_name, service_name, item) = key
            if metric_uuid is None:
                continue

            # PRODUCT-279: elasticsearch_search_time was previously not
            # associated with the service elasticsearch
            if (metric_name == 'elasticsearch_search_time'
                    and service_name is None):
                service_name = 'elasticsearch'

            if (metric_name, item) in thresholds:
                tmp = thresholds[(metric_name, item)]
                threshold = MetricThreshold(
                    tmp['low_warning'],
                    tmp['low_critical'],
                    tmp['high_warning'],
                    tmp['high_critical'],
                )
            else:
                threshold = MetricThreshold(None, None, None, None)

            service = services_uuid.get((service_name, item))

            if service_name and not service:
                continue
            if service_name:
                service_uuid = service['uuid']
            else:
                service_uuid = None

            if item is None:
                item = ''

            self.metrics[metric_uuid] = Metric(
                metric_uuid,
                metric_name,
                item,
                service_uuid,
                None,
                None,
                threshold,
                None,
                None,
                True,
            )
        services_uuid = self._state.get_complex_dict('services_uuid', {})
        for service_info in services_uuid.values():
            if service_info.get('uuid') is None:
                continue

            listen_addresses = set(
                service_info.get('listen_addresses', '').split(',')
            )
            if '' in listen_addresses:
                listen_addresses.remove('')
            self.services[service_info['uuid']] = Service(
                service_info['uuid'],
                service_info['label'],
                service_info.get('instance'),
                listen_addresses,
                service_info.get('exe_path', ''),
                service_info.get('stack', ''),
                service_info.get('active', True),
            )

        self.tags = list(self._state.get('tags_uuid', {}))

        self.save()
        self._reload()
        try:
            self._state.delete('metrics_uuid')
            self._state.delete('services_uuid')
            self._state.delete('thresholds')
            self._state.delete('tags_uuid')
            self._state.delete('docker_container_uuid')
        except KeyError:
            pass


class BleemeoConnector(threading.Thread):
    # pylint: disable=too-many-instance-attributes

    def __init__(self, core):
        super(BleemeoConnector, self).__init__()
        self.core = core

        self._metric_queue = queue.Queue()
        self.connected = False
        self._last_disconnects = []
        self._mqtt_queue_size = 0

        self.trigger_full_sync = False
        self.last_containers_removed = bleemeo_agent.util.get_clock()
        self.last_config_will_change_msg = bleemeo_agent.util.get_clock()
        self._bleemeo_cache = None

        self.mqtt_client = mqtt.Client()

        self._api_support_metric_update = True
        self._current_metrics = {}
        self._current_metrics_lock = threading.Lock()
        # Make sure this metrics exists and try to be registered
        self._current_metrics[('agent_status', '')] = MetricRegistrationReq(
            'agent_status',
            '',
            None,
            '',
            '',
            None,
            STATUS_OK,
            '',
            bleemeo_agent.util.get_clock(),
        )

    def on_connect(self, _client, _userdata, _flags, result_code):
        if result_code == 0 and not self.core.is_terminating.is_set():
            self.connected = True
            msg = {
                'public_ip': self.core.last_facts.get('public_ip'),
            }
            self.publish(
                'v1/agent/%s/connect' % self.agent_uuid,
                json.dumps(msg),
            )
            # FIXME: PRODUCT-137: to be removed when upstream bug is fixed
            # pylint: disable=protected-access
            if (self.mqtt_client._ssl is not None
                    and not isinstance(self.mqtt_client._ssl, bool)):
                # pylint: disable=no-member
                self.mqtt_client._ssl.setblocking(0)

            self.mqtt_client.subscribe(
                'v1/agent/%s/notification' % self.agent_uuid
            )
            logging.info('MQTT connection established')

    def on_disconnect(self, _client, _userdata, _result_code):
        if self.connected:
            logging.info('MQTT connection lost')
        self._last_disconnects.append(bleemeo_agent.util.get_clock())
        self._last_disconnects = self._last_disconnects[-15:]
        self.connected = False

    def on_message(self, _client, _userdata, msg):
        notify_topic = 'v1/agent/%s/notification' % self.agent_uuid
        if msg.topic == notify_topic and len(msg.payload) < 1024 * 64:
            try:
                body = json.loads(msg.payload.decode('utf-8'))
            except Exception as exc:  # pylint: disable=broad-except
                logging.info('Failed to decode message for Bleemeo: %s', exc)
                return

            if 'message_type' not in body:
                return
            if body['message_type'] == 'threshold-update':
                logging.debug('Got "threshold-update" message from Bleemeo')
                self.trigger_full_sync = True
            if body['message_type'] == 'config-changed':
                logging.debug('Got "config-changed" message from Bleemeo')
                self.trigger_full_sync = True
            if body['message_type'] == 'config-will-change':
                logging.debug('Got "config-will-change" message from Bleemeo')
                self.last_config_will_change_msg = (
                    bleemeo_agent.util.get_clock()
                )

    def on_publish(self, _client, _userdata, _mid):
        self._mqtt_queue_size -= 1
        self.core.update_last_report()

    def check_config_requirement(self):
        sleep_delay = 10
        while (self.core.config.get('bleemeo.account_id') is None
               or self.core.config.get('bleemeo.registration_key') is None):
            logging.warning(
                'bleemeo.account_id and/or '
                'bleemeo.registration_key is undefine. '
                'Please see https://docs.bleemeo.com/how-to-configure-agent')
            self.core.is_terminating.wait(sleep_delay)
            if self.core.is_terminating.is_set():
                raise StopIteration
            self.core.reload_config()
            sleep_delay = min(sleep_delay * 2, 600)

        if self.core.state.get('password') is None:
            self.core.state.set(
                'password', bleemeo_agent.util.generate_password())

    def init(self):
        if self.core.sentry_client and self.agent_uuid:
            self.core.sentry_client.site = self.agent_uuid

        try:
            self._bleemeo_cache = BleemeoCache(self.core.state)
        except Exception:  # pylint: disable=broad-except
            logging.warning(
                'Error while loading the cache. Starting with empty cache',
                exc_info=True,
            )
            self._bleemeo_cache = BleemeoCache(self.core.state, skip_load=True)

        if self._bleemeo_cache.current_config:
            self.core.set_topinfo_period(
                self._bleemeo_cache.current_config.topinfo_period,
            )

    def run(self):
        self.core.add_scheduled_job(
            self._bleemeo_health_check,
            seconds=60,
        )

        try:
            self.check_config_requirement()
        except StopIteration:
            return

        sync_thread = threading.Thread(target=self._bleemeo_synchronizer)
        sync_thread.daemon = True
        sync_thread.start()

        while not self._ready_for_mqtt():
            self.core.is_terminating.wait(1)
            if self.core.is_terminating.is_set():
                return

        self._mqtt_setup()

        _mqtt_reconnect_at = 0
        while not self.core.is_terminating.is_set():
            clock_now = bleemeo_agent.util.get_clock()
            if (not _mqtt_reconnect_at
                    and len(self._last_disconnects) >= 6
                    and self._last_disconnects[-6] > clock_now - 60):
                logging.info(
                    'Too many attempt to connect to MQTT on last minute.'
                    ' Disabling MQTT for 60 seconds'
                )
                self.mqtt_client.disconnect()
                self.mqtt_client.loop_stop()
                _mqtt_reconnect_at = clock_now + 60
            if (not _mqtt_reconnect_at
                    and len(self._last_disconnects) >= 15
                    and self._last_disconnects[-15] > clock_now - 600):
                logging.info(
                    'Too many attempt to connect to MQTT on last 10 minutes.'
                    ' Disabling MQTT for 5 minutes'
                )
                self.mqtt_client.disconnect()
                self.mqtt_client.loop_stop()
                _mqtt_reconnect_at = clock_now + 300
                self._last_disconnects = []
            elif _mqtt_reconnect_at and _mqtt_reconnect_at < clock_now:
                logging.info('Re-enabling MQTT connection')
                _mqtt_reconnect_at = 0
                self._mqtt_start()

            self._loop()

        if self.connected and self.upgrade_in_progress:
            self.publish(
                'v1/agent/%s/disconnect' % self.agent_uuid,
                json.dumps({'disconnect-cause': 'Upgrade'}),
                force=True
            )
        elif self.connected:
            self.publish(
                'v1/agent/%s/disconnect' % self.agent_uuid,
                json.dumps({'disconnect-cause': 'Clean shutdown'}),
                force=True
            )

        # Wait up to 5 second for MQTT queue to be empty before disconnecting
        deadline = bleemeo_agent.util.get_clock() + 5
        while (self._mqtt_queue_size > 0
               and bleemeo_agent.util.get_clock() < deadline):
            time.sleep(0.1)

        self.mqtt_client.disconnect()
        self.mqtt_client.loop_stop()
        sync_thread.join(5)

    def _ready_for_mqtt(self):
        """ Check for requirement needed before MQTT connection

            * agent must be registered
            * it need initial facts
            * "agent_status" metrics must be registered
        """
        agent_status = self._bleemeo_cache.metrics_by_labelitem.get(
            ('agent_status', '')
        )
        return (
            self.agent_uuid is not None and
            self.core.last_facts and
            agent_status is not None
        )

    def _bleemeo_health_check(self):
        """ Check the Bleemeo connector works correctly. Log any issue found
        """
        clock_now = bleemeo_agent.util.get_clock()

        if self.agent_uuid is None:
            logging.info('Agent not yet registered')

        if not self.connected:
            logging.info(
                'Bleemeo connection (MQTT) is currently not established'
            )

        if self._mqtt_queue_size >= MQTT_QUEUE_MAX_SIZE:
            logging.warning(
                'Sending queue to Bleemeo Cloud is full. '
                'New messages are dropped'
            )
        elif self._mqtt_queue_size > 10:
            logging.info(
                '%s messages waiting to be sent to Bleemeo Cloud',
                self._mqtt_queue_size,
            )

        if self._metric_queue.qsize() > 10:
            logging.info(
                '%s metric points blocked due to metric not yet registered',
                self._metric_queue.qsize(),
            )

        if (self.core.graphite_server.data_last_seen_at is None or
                clock_now - self.core.graphite_server.data_last_seen_at > 60):
            logging.info(
                'Issue with metrics collector: no metric received from %s',
                self.core.graphite_server.metrics_source,
            )

    def _mqtt_setup(self):
        self.mqtt_client.will_set(
            'v1/agent/%s/disconnect' % self.agent_uuid,
            json.dumps({'disconnect-cause': 'disconnect-will'}),
            1,
        )
        if hasattr(ssl, 'PROTOCOL_TLSv1_2'):
            # Need Python 3.4+ or 2.7.9+
            tls_version = ssl.PROTOCOL_TLSv1_2
        else:
            tls_version = ssl.PROTOCOL_TLSv1

        if self.core.config.get('bleemeo.mqtt.ssl', True):
            cafile = self.core.config.get(
                'bleemeo.mqtt.cafile',
                '/etc/ssl/certs/ca-certificates.crt'
            )
            if cafile is not None and '$INSTDIR' in cafile and os.name == 'nt':
                # Under Windows, $INSTDIR is remplaced by installation
                # directory
                cafile = cafile.replace(
                    '$INSTDIR',
                    bleemeo_agent.util.windows_instdir()
                )
            self.mqtt_client.tls_set(
                cafile,
                tls_version=tls_version,
            )
            self.mqtt_client.tls_insecure_set(
                self.core.config.get('bleemeo.mqtt.ssl_insecure', False)
            )

        self.mqtt_client.on_connect = self.on_connect
        self.mqtt_client.on_disconnect = self.on_disconnect
        self.mqtt_client.on_message = self.on_message
        self.mqtt_client.on_publish = self.on_publish

        self.mqtt_client.username_pw_set(
            self.agent_username,
            self.agent_password,
        )
        self._mqtt_start()

    def _mqtt_start(self):

        mqtt_host = self.core.config.get(
            'bleemeo.mqtt.host',
            'mqtt.bleemeo.com'
        )
        mqtt_port = self.core.config.get(
            'bleemeo.mqtt.port',
            8883,
        )

        try:
            logging.debug('Connecting to MQTT broker at %s', mqtt_host)
            self.mqtt_client.connect(
                mqtt_host,
                mqtt_port,
                60,
            )
        except socket.error:
            pass

        self.mqtt_client.loop_start()

    def _loop(self):
        # pylint: disable=too-many-branches
        """ Call as long as agent is running. It's the "main" method for
            Bleemeo connector thread.
        """
        metrics = []
        repush_metric = None
        timeout = 3

        try:
            while True:
                metric_point = self._metric_queue.get(timeout=timeout)
                timeout = 0.3  # Long wait only for the first get
                short_item = (
                    metric_point.get('item', '')[:API_METRIC_ITEM_LENGTH]
                )
                if metric_point.get('instance'):
                    short_item = short_item[:API_SERVICE_INSTANCE_LENGTH]
                key = (
                    metric_point['measurement'],
                    short_item,
                )
                metric = self._bleemeo_cache.metrics_by_labelitem.get(key)

                if metric is None:
                    if time.time() - metric_point['time'] > 7200:
                        continue
                    elif key not in self._current_metrics:
                        continue
                    else:
                        self._metric_queue.put(metric_point)

                    if repush_metric is metric_point:
                        # It has looped, the first re-pushed metric was
                        # re-read.
                        # Sleep a short time to avoid looping for nothing
                        # and consuming all CPU
                        time.sleep(0.5)
                        break

                    if repush_metric is None:
                        repush_metric = metric_point

                    continue

                bleemeo_metric = {
                    'uuid': metric.uuid,
                    'measurement': metric.label,
                    'time': metric_point['time'],
                    'value': metric_point['value'],
                }
                if metric.item:
                    bleemeo_metric['item'] = metric.item
                if 'status' in metric_point:
                    bleemeo_metric['status'] = metric_point['status']
                if 'check_output' in metric_point:
                    bleemeo_metric['check_output'] = (
                        metric_point['check_output']
                    )
                metrics.append(bleemeo_metric)
                if len(metrics) > 1000:
                    break
        except queue.Empty:
            pass

        if metrics:
            self.publish(
                'v1/agent/%s/data' % self.agent_uuid,
                json.dumps(metrics)
            )

    def publish_top_info(self, top_info):
        if self.agent_uuid is None:
            return

        if not self.connected:
            return

        self.publish(
            'v1/agent/%s/top_info' % self.agent_uuid,
            bytearray(zlib.compress(json.dumps(top_info).encode('utf8')))
        )

    def publish(self, topic, message, force=False):
        if self._mqtt_queue_size > MQTT_QUEUE_MAX_SIZE and not force:
            return

        self._mqtt_queue_size += 1
        self.mqtt_client.publish(
            topic,
            message,
            1)

    def register(self):
        """ Register the agent to Bleemeo SaaS service
        """
        base_url = self.bleemeo_base_url
        registration_url = urllib_parse.urljoin(base_url, '/v1/agent/')

        name = self.core.last_facts.get('fqdn')
        if not name:
            logging.debug('Register delayed, fact fqdn not available')
            return

        registration_key = self.core.config.get('bleemeo.registration_key')
        payload = {
            'account': self.account_id,
            'initial_password': self.core.state.get('password'),
            'display_name': name,
            'fqdn': name,
        }

        initial_config_name = self.core.config.get(
            'bleemeo.initial_config_name'
        )
        if initial_config_name:
            payload['initial_config_name'] = initial_config_name

        content = None
        try:
            response = requests.post(
                registration_url,
                data=json.dumps(payload),
                auth=('%s@bleemeo.com' % self.account_id, registration_key),
                headers={
                    'X-Requested-With': 'XMLHttpRequest',
                    'Content-type': 'application/json',
                    'User-Agent': self.core.http_user_agent,
                },
                timeout=REQUESTS_TIMEOUT,
            )
            content = response.json()
        except requests.exceptions.RequestException:
            response = None
        except ValueError:
            pass  # unable to decode JSON

        if (response is not None
                and response.status_code == 201
                and content is not None
                and 'id' in content):
            self.core.state.set('agent_uuid', content['id'])
            logging.debug('Regisration successfull')
        elif content is not None:
            logging.info(
                'Registration failed: %s',
                content
            )
        elif response is not None:
            logging.debug(
                'Registration failed, response is not a json: %s',
                response.content[:100])
        else:
            logging.debug('Registration failed, unable to connect to API')

        if self.core.sentry_client and self.agent_uuid:
            self.core.sentry_client.site = self.agent_uuid

    def _bleemeo_synchronizer(self):
        if self._ready_for_mqtt():
            # Not a new agent. Most thing must be already synced.
            # Give a small jitter to avoid lots of agent to sync
            # at the same time.
            time.sleep(random.randint(5, 30))
        while not self.core.is_terminating.is_set():
            try:
                self._sync_loop()
            except Exception:  # pylint: disable=broad-except
                logging.warning(
                    'Bleemeo synchronization loop crashed.'
                    ' Restarting it in 60 seconds',
                    exc_info=True,
                )
            finally:
                self.core.is_terminating.wait(60)

    def _sync_loop(self):
        # pylint: disable=too-many-locals
        # pylint: disable=too-many-branches
        # pylint: disable=too-many-statements
        """ Synchronize object between local state and Bleemeo SaaS
        """
        next_full_sync = 0
        last_sync = 0
        bleemeo_cache = self._bleemeo_cache.copy()

        bleemeo_api = None

        last_metrics_count = 0
        successive_errors = 0

        while not self.core.is_terminating.is_set():
            if self.agent_uuid is None:
                self.register()

            if self.agent_uuid is None:
                self.core.is_terminating.wait(15)
                continue

            if bleemeo_api is None:
                bleemeo_api = BleemeoAPI(
                    self.bleemeo_base_url,
                    (self.agent_username, self.agent_password),
                    self.core.http_user_agent,
                )

            if (bleemeo_cache.next_config_at is not None and
                    bleemeo_cache.next_config_at.timestamp() < time.time()):
                self.trigger_full_sync = True

            if self.trigger_full_sync:
                next_full_sync = 0
                time.sleep(random.randint(5, 15))
                self.trigger_full_sync = False

            clock_now = bleemeo_agent.util.get_clock()
            with self._current_metrics_lock:
                metrics_count = len(self._current_metrics)

            has_error = False
            sync_run = False
            metrics_sync = False

            if (next_full_sync < clock_now
                    or last_sync <= self.last_config_will_change_msg):
                try:
                    self._sync_agent(bleemeo_cache, bleemeo_api)
                    sync_run = True
                except ApiError as exc:
                    logging.info(
                        'Unable to synchronize agent. API responded: %s',
                        exc.response.content,
                    )
                    self.core.is_terminating.wait(5)
                    has_error = True

            if (next_full_sync <= clock_now or
                    last_sync <= self.last_containers_removed or
                    last_sync <= self.core.last_discovery_update):
                try:
                    full = (
                        next_full_sync <= clock_now or
                        last_sync <= self.last_containers_removed or
                        # After 3 successive_errors force a full sync.
                        successive_errors == 3
                    )
                    self._sync_services(bleemeo_cache, bleemeo_api, full)
                    # Metrics registration may need services to be synced.
                    # For a pass of metric registrations
                    metrics_sync = True
                    sync_run = True
                except ApiError as exc:
                    logging.info(
                        'Unable to synchronize services. API responded: %s',
                        exc.response.content,
                    )
                    self.core.is_terminating.wait(5)
                    has_error = True

            if (next_full_sync <= clock_now or
                    last_sync <= self.core.last_discovery_update):
                try:
                    full = (
                        next_full_sync <= clock_now or
                        # After 3 successive_errors force a full sync.
                        successive_errors == 3
                    )
                    self._sync_containers(bleemeo_cache, bleemeo_api, full)
                    # Metrics registration may need containers to be synced.
                    # For a pass of metric registrations
                    metrics_sync = True
                    sync_run = True
                except ApiError as exc:
                    logging.info(
                        'Unable to synchronize containers. API responded: %s',
                        exc.response.content,
                    )
                    self.core.is_terminating.wait(5)
                    has_error = True

            if (metrics_sync or
                    next_full_sync <= clock_now or
                    last_sync <= self.last_containers_removed or
                    last_sync <= self.core.last_discovery_update or
                    last_metrics_count != metrics_count):
                try:
                    with self._current_metrics_lock:
                        self._current_metrics = {
                            key: value
                            for (key, value) in self._current_metrics.items()
                            if self.sent_metric(
                                value.label,
                                value.last_status is not None,
                                bleemeo_cache,
                            )
                        }
                    full = (
                        next_full_sync <= clock_now or
                        last_sync <= self.last_containers_removed or
                        # After 3 successive_errors force a full sync.
                        successive_errors == 3
                    )
                    self._sync_metrics(bleemeo_cache, bleemeo_api, full)
                    last_metrics_count = metrics_count
                    sync_run = True
                except ApiError as exc:
                    logging.info(
                        'Unable to synchronize metrics. API responded: %s',
                        exc.response.content,
                    )
                    self.core.is_terminating.wait(5)
                    has_error = True

            if (next_full_sync < clock_now or
                    last_sync < self.core.last_facts_update):
                try:
                    self._sync_facts(bleemeo_cache, bleemeo_api)
                    sync_run = True
                except ApiError as exc:
                    logging.info(
                        'Unable to synchronize facts. API responded: %s',
                        exc.response.content,
                    )
                    self.core.is_terminating.wait(5)
                    has_error = True

            if next_full_sync < clock_now and not has_error:
                next_full_sync = (
                    clock_now +
                    random.randint(3500, 3700)
                )
                bleemeo_cache.save()
                logging.debug(
                    'Next full sync in %d seconds',
                    next_full_sync - clock_now,
                )

            if sync_run and not has_error:
                last_sync = clock_now
            if has_error:
                successive_errors += 1
                delay = min(successive_errors * 15, 60)
            else:
                successive_errors = 0
                delay = 15

            self._bleemeo_cache = bleemeo_cache.copy()
            self.core.is_terminating.wait(delay)

    def _sync_agent(self, bleemeo_cache, bleemeo_api):
        logging.debug('Synchronize agent')
        tags = set(self.core.config.get('tags', []))

        response = bleemeo_api.api_call(
            'v1/agent/%s/' % self.agent_uuid,
            'patch',
            params={'fields': 'tags,current_config,next_config_at'},
            data=json.dumps({'tags': [
                {'name': x} for x in tags if x and len(x) <= 100
            ]}),
        )
        if response.status_code >= 400:
            raise ApiError(response)

        data = response.json()

        bleemeo_cache.tags = []
        for tag in data['tags']:
            if not tag['is_automatic']:
                bleemeo_cache.tags.append(tag['name'])

        if data.get('next_config_at'):
            bleemeo_cache.next_config_at = datetime.datetime.strptime(
                data['next_config_at'],
                '%Y-%m-%dT%H:%M:%SZ',
            ).replace(tzinfo=datetime.timezone.utc)
        else:
            bleemeo_cache.next_config_at = None

        config_uuid = data.get('current_config')
        if config_uuid is None:
            bleemeo_cache.current_config = None
            return

        response = bleemeo_api.api_call(
            '/v1/config/%s/' % config_uuid,
        )
        if response.status_code >= 400:
            raise ApiError(response)

        data = response.json()
        if data['metrics_whitelist']:
            whitelist = set(data['metrics_whitelist'].split(','))
        else:
            whitelist = set()

        if data.get('metrics_blacklist', ''):
            blacklist = set(data['metrics_blacklist'].split(','))
        else:
            blacklist = set()

        config = AgentConfig(
            data['id'],
            data['name'],
            data['docker_integration'],
            data['topinfo_period'],
            whitelist,
            blacklist,
        )
        if bleemeo_cache.current_config == config:
            return
        bleemeo_cache.current_config = config

        self.core.set_topinfo_period(config.topinfo_period)
        self.core.fire_triggers(updates_count=True)
        logging.info('Changed to configuration %s', config.name)

    def _sync_metrics(self, bleemeo_cache, bleemeo_api, full=True):
        # pylint: disable=too-many-locals
        # pylint: disable=too-many-branches
        # pylint: disable=too-many-statements
        """ Synchronize metrics with Bleemeo SaaS
        """
        logging.debug('Synchronize metrics (full=%s)', full)
        clock_now = bleemeo_agent.util.get_clock()
        metric_url = 'v1/metric/'

        if full:
            # retry metric update
            self._api_support_metric_update = True

        # Step 1: refresh cache from API
        if full:
            api_metrics = bleemeo_api.api_iterator(
                metric_url,
                params={
                    'agent': self.agent_uuid,
                    'fields':
                        'id,item,label,unit,unit_text,active'
                        ',threshold_low_warning,threshold_low_critical'
                        ',threshold_high_warning,threshold_high_critical'
                        ',service,container,status_of',
                },
            )

            old_metrics = bleemeo_cache.metrics
            new_metrics = {}

            for data in api_metrics:
                metric = Metric(
                    data['id'],
                    data['label'],
                    data['item'],
                    data['service'],
                    data['container'],
                    data['status_of'],
                    MetricThreshold(
                        data['threshold_low_warning'],
                        data['threshold_low_critical'],
                        data['threshold_high_warning'],
                        data['threshold_high_critical'],
                    ),
                    data['unit'],
                    data['unit_text'],
                    data['active'],
                )
                new_metrics[metric.uuid] = metric
            bleemeo_cache.metrics = new_metrics
            bleemeo_cache.update_lookup_map()
        else:
            old_metrics = bleemeo_cache.metrics

        # Step 2: delete local object that are deleted from API
        deleted_metrics = []
        for metric_uuid in set(old_metrics) - set(bleemeo_cache.metrics):
            metric = old_metrics[metric_uuid]
            deleted_metrics.append((metric.label, metric.item))

        if deleted_metrics:
            self.core.purge_metrics(deleted_metrics)

        # Step 3: register/update object present in local but not in API
        registration_error = 0
        last_error = None
        with self._current_metrics_lock:
            current_metrics = list(self._current_metrics.values())
        # If one metric fail to register, it may block other metric that would
        # register correctly. To reduce this risk, randomize the list, so on
        # next run, the metric that failed to register may no longer block
        # other.
        random.shuffle(current_metrics)

        metric_last_seen = {}
        service_short_lookup = services_to_short_key(self.core.services)
        metrics_req_count = len(current_metrics)
        count = 0
        while current_metrics:
            reg_req = current_metrics.pop()
            count += 1
            short_item = reg_req.item[:API_METRIC_ITEM_LENGTH]
            if reg_req.service_label:
                short_item = short_item[:API_SERVICE_INSTANCE_LENGTH]
            key = (reg_req.label, short_item)

            if key in deleted_metrics:
                continue
            metric = bleemeo_cache.metrics_by_labelitem.get(key)

            if metric:
                metric_last_seen[metric.uuid] = reg_req.last_seen
                if (not metric.active
                        and reg_req.last_seen > clock_now - 600
                        and self._api_support_metric_update):
                    logging.debug(
                        'Mark active the metric %s: %s (%s)',
                        metric.uuid,
                        metric.label,
                        metric.item,
                    )
                    response = bleemeo_api.api_call(
                        urllib_parse.urljoin(metric_url, '%s/' % metric.uuid),
                        'patch',
                        params={
                            'fields': 'active',
                        },
                        data=json.dumps({
                            'active': True,
                        }),
                    )
                    if response.status_code == 403:
                        self._api_support_metric_update = False
                        logging.debug(
                            'API does not yet support metric update.'
                        )
                    elif response.status_code != 200:
                        raise ApiError(response)
                continue

            payload = {
                'agent': self.agent_uuid,
                'label': reg_req.label,
            }
            if reg_req.status_of_label:
                status_of_key = (reg_req.status_of_label, short_item)
                if status_of_key not in bleemeo_cache.metrics_by_labelitem:
                    if count >= metrics_req_count:
                        logging.debug(
                            'Metric %s is status_of unregistered metric %s',
                            reg_req.label,
                            reg_req.status_of_label,
                        )
                    else:
                        current_metrics.append(reg_req)
                    continue
                payload['status_of'] = (
                    bleemeo_cache.metrics_by_labelitem[status_of_key].uuid
                )
            if reg_req.container_name:
                container = bleemeo_cache.containers_by_name.get(
                    reg_req.container_name,
                )
                if container is None:
                    # Container not yet registered
                    continue
                payload['container'] = container.uuid
            if reg_req.service_label:
                key = (reg_req.service_label, reg_req.instance)
                if key not in service_short_lookup:
                    continue
                short_key = service_short_lookup[key]
                service = bleemeo_cache.services_by_labelinstance.get(
                    short_key
                )
                if service is None:
                    continue
                payload['service'] = service.uuid

            if reg_req.item:
                payload['item'] = short_item

            if reg_req.last_status is not None:
                payload['last_status'] = reg_req.last_status
                payload['last_status_changed_at'] = (
                    datetime.datetime.utcnow()
                    .replace(tzinfo=datetime.timezone.utc)
                    .isoformat()
                )
                payload['problem_origins'] = [reg_req.last_problem_origins]

            response = bleemeo_api.api_call(
                metric_url,
                method='post',
                data=json.dumps(payload),
                params={
                    'fields': 'id,label,item,service,container,active,'
                              'threshold_low_warning,threshold_low_critical,'
                              'threshold_high_warning,threshold_high_critical,'
                              'unit,unit_text,agent,status_of,service,'
                              'last_status,last_status_changed_at,'
                              'problem_origins',
                },
            )
            if 400 <= response.status_code < 500:
                logging.debug(
                    'Metric registration failed for %s. '
                    'Server reported a client error: %s',
                    reg_req.label,
                    response.content,
                )
                registration_error += 1
                last_error = ApiError(response)
                if registration_error > 3:
                    raise last_error  # pylint: disable=raising-bad-type
                continue
            elif response.status_code != 201:
                raise ApiError(response)
            data = response.json()

            metric = Metric(
                data['id'],
                data['label'],
                data['item'],
                data['service'],
                data['container'],
                data['status_of'],
                MetricThreshold(
                    data['threshold_low_warning'],
                    data['threshold_low_critical'],
                    data['threshold_high_warning'],
                    data['threshold_high_critical'],
                ),
                data['unit'],
                data['unit_text'],
                data['active'],
            )
            bleemeo_cache.metrics[metric.uuid] = metric
            metric_last_seen[metric.uuid] = reg_req.last_seen
            if metric.item:
                logging.debug(
                    'Metric %s (item %s) registered with uuid %s',
                    metric.label,
                    metric.item,
                    metric.uuid,
                )
            else:
                logging.debug(
                    'Metric %s registered with uuid %s',
                    metric.label,
                    metric.uuid,
                )
        bleemeo_cache.update_lookup_map()

        # Step 4: delete object present in API by not in local
        # Only metric $SERVICE_NAME_status from service with ignore_check=True
        # are deleted
        service_short_lookup = services_to_short_key(self.core.services)
        for (key, service_info) in self.core.services.items():
            if not service_info.get('ignore_check', False):
                continue
            if key not in service_short_lookup:
                continue
            short_key = service_short_lookup[key]
            (service_name, instance) = short_key
            metric = bleemeo_cache.metrics_by_labelitem.get(
                ('%s_status' % service_name, instance)
            )
            if metric is None:
                continue

            response = bleemeo_api.api_call(
                metric_url + '%s/' % metric.uuid,
                'delete',
            )
            if response.status_code == 403:
                logging.debug(
                    "Metric deletion failed for %s. Skip metrics deletion",
                    key,
                )
                break
            elif response.status_code not in (204, 404):
                raise ApiError(response)
            if metric.item:
                logging.debug(
                    'Metric %s (%s) deleted', metric.label, metric.item,
                )
            else:
                logging.debug(
                    'Metric %s deleted', metric.label,
                )

        # Extra step: mark inactive metric (not seen for last hour + 10min)
        # But only if agent is running for at least 1 hours & 10 min
        if (self.core.started_at < clock_now - 4200
                and self._api_support_metric_update):
            for metric in bleemeo_cache.metrics.values():
                if metric.label == 'agent_sent_message':
                    # This metric is managed by Bleemeo Cloud platform
                    continue
                last_seen = metric_last_seen.get(metric.uuid)
                if ((last_seen is None or last_seen < clock_now - 4200)
                        and metric.active):
                    logging.debug(
                        'Mark inactive the metric %s: %s (%s)',
                        metric.uuid,
                        metric.label,
                        metric.item,
                    )
                    response = bleemeo_api.api_call(
                        urllib_parse.urljoin(metric_url, '%s/' % metric.uuid),
                        'patch',
                        params={
                            'fields': 'active',
                        },
                        data=json.dumps({
                            'active': False,
                        }),
                    )
                    if response.status_code == 403:
                        self._api_support_metric_update = False
                        logging.debug(
                            'API does not yet support metric update.'
                        )
                        break
                    elif response.status_code != 200:
                        raise ApiError(response)

        self.core.update_thresholds(bleemeo_cache.get_core_thresholds())
        self.core.metrics_unit = bleemeo_cache.get_core_units()

        # During full sync, also drop metric not seen for last hour + 10min
        # or deleted by API.
        with self._current_metrics_lock:
            cutoff = bleemeo_agent.util.get_clock() - 4200
            result = {}
            for (key, value) in self._current_metrics.items():
                if value.last_seen < cutoff:
                    continue
                short_item = value.item[:API_METRIC_ITEM_LENGTH]
                if value.service_label:
                    short_item = short_item[:API_SERVICE_INSTANCE_LENGTH]
                if (value.label, short_item) not in deleted_metrics:
                    result[key] = value
            self._current_metrics = result

        if last_error is not None:
            raise last_error  # pylint: disable=raising-bad-type

    def _sync_services(self, bleemeo_cache, bleemeo_api, full=True):
        # pylint: disable=too-many-locals
        # pylint: disable=too-many-branches
        # pylint: disable=too-many-statements
        """ Synchronize services with Bleemeo SaaS
        """
        logging.debug('Synchronize services (full=%s)', full)
        service_url = 'v1/service/'

        # Step 1: refresh cache from API
        if full:
            api_services = bleemeo_api.api_iterator(
                service_url,
                params={
                    'agent': self.agent_uuid,
                    'fields': 'id,label,instance,listen_addresses,exe_path,'
                              'stack,active',
                },
            )

            old_services = bleemeo_cache.services
            new_services = {}
            for data in api_services:
                listen_addresses = set(data['listen_addresses'].split(','))
                if '' in listen_addresses:
                    listen_addresses.remove('')
                service = Service(
                    data['id'],
                    data['label'],
                    data['instance'],
                    listen_addresses,
                    data['exe_path'],
                    data['stack'],
                    data['active'],
                )
                new_services[service.uuid] = service
            bleemeo_cache.services = new_services
            bleemeo_cache.update_lookup_map()
        else:
            old_services = bleemeo_cache.services

        # Step 2: delete local object that are deleted from API
        deleted_services = []
        for service_uuid in set(old_services) - set(bleemeo_cache.services):
            service = old_services[service_uuid]
            deleted_services.append((service.label, service.instance))

        if deleted_services:
            logging.debug(
                'API deleted the following services: %s',
                deleted_services
            )
            self.core.update_discovery(deleted_services=deleted_services)

        # Step 3: register/update object present in local but not in API
        service_short_lookup = services_to_short_key(self.core.services)
        for key, service_info in self.core.services.items():
            if key not in service_short_lookup:
                continue
            short_key = service_short_lookup[key]
            (service_name, instance) = short_key
            listen_addresses = get_listen_addresses(service_info)

            service = bleemeo_cache.services_by_labelinstance.get(short_key)
            if (service is not None and
                    service.listen_addresses == listen_addresses and
                    service.exe_path == service_info.get('exe_path', '') and
                    service.stack == service_info.get('stack', '') and
                    service.active == service_info.get('active', True)):
                continue

            payload = {
                'listen_addresses': ','.join(listen_addresses),
                'label': service_name,
                'exe_path': service_info.get('exe_path', ''),
                'stack': service_info.get('stack', ''),
                'active': service_info.get('active', True),
            }
            if instance is not None:
                payload['instance'] = instance

            if service is not None:
                method = 'put'
                action_text = 'updated'
                url = service_url + str(service.uuid) + '/'
                expected_code = 200
            else:
                method = 'post'
                action_text = 'registrered'
                url = service_url
                expected_code = 201

            payload.update({
                'account': self.account_id,
                'agent': self.agent_uuid,
            })

            response = bleemeo_api.api_call(
                url,
                method,
                data=json.dumps(payload),
                params={
                    'fields': 'id,listen_addresses,label,exe_path,stack'
                              ',active,instance,account,agent'
                },
            )
            if response.status_code != expected_code:
                raise ApiError(response)
            data = response.json()
            listen_addresses = set(data['listen_addresses'].split(','))
            if '' in listen_addresses:
                listen_addresses.remove('')

            service = Service(
                data['id'],
                data['label'],
                data['instance'],
                listen_addresses,
                data['exe_path'],
                data['stack'],
                data['active'],
            )
            bleemeo_cache.services[service.uuid] = service

            if service.instance:
                logging.debug(
                    'Service %s on %s %s with uuid %s',
                    service.label,
                    service.instance,
                    action_text,
                    service.uuid,
                )
            else:
                logging.debug(
                    'Service %s %s with uuid %s',
                    service.label,
                    action_text,
                    service.uuid,
                )
        bleemeo_cache.update_lookup_map()

        # Step 4: delete object present in API by not in local
        try:
            local_uuids = set(
                bleemeo_cache.services_by_labelinstance[
                    service_short_lookup[key]
                ].uuid
                for key in self.core.services if key in service_short_lookup
            )
        except KeyError:
            logging.info(
                'Some services are not registered, skipping deleting phase',
            )
            return
        deleted_services_from_state = set(bleemeo_cache.services) - local_uuids
        for service_uuid in deleted_services_from_state:
            service = bleemeo_cache.services[service_uuid]
            response = bleemeo_api.api_call(
                service_url + '%s/' % service_uuid,
                'delete',
            )
            if response.status_code not in (204, 404):
                logging.debug(
                    'Service deletion failed. Server response = %s',
                    response.content
                )
                continue
            del bleemeo_cache.services[service_uuid]
            key = (service.label, service.instance)
            if service.instance:
                logging.debug(
                    'Service %s on %s deleted',
                    service.label,
                    service.instance,
                )
            else:
                logging.debug(
                    'Service %s deleted',
                    service.label,
                )
        bleemeo_cache.update_lookup_map()

    def _sync_containers(self, bleemeo_cache, bleemeo_api, full=True):
        # pylint: disable=too-many-branches
        # pylint: disable=too-many-locals
        # pylint: disable=too-many-statements
        logging.debug('Synchronize containers (full=%s)', full)
        container_url = 'v1/container/'

        # Step 1: refresh cache from API
        if full:
            api_containers = bleemeo_api.api_iterator(
                container_url,
                params={
                    'agent': self.agent_uuid,
                    'fields': 'id,name,docker_id,docker_inspect'
                },
            )

            new_containers = {}
            for data in api_containers:
                docker_inspect = json.loads(data['docker_inspect'])
                docker_inspect = sort_docker_inspect(docker_inspect)
                name = docker_inspect['Name'].lstrip('/')
                inspect_hash = hashlib.sha1(
                    json.dumps(docker_inspect, sort_keys=True).encode('utf-8')
                ).hexdigest()
                container = Container(
                    data['id'],
                    name,
                    data['docker_id'],
                    inspect_hash,
                )
                new_containers[container.uuid] = container
            bleemeo_cache.containers = new_containers
            bleemeo_cache.update_lookup_map()

        # Step 2: delete local object that are deleted from API
        # Not done for containers. API never delete a container

        local_containers = self.core.docker_containers
        if (bleemeo_cache.current_config is not None
                and not bleemeo_cache.current_config.docker_integration):
            local_containers = {}

        # Step 3: register/update object present in local but not in API
        for docker_id, inspect in local_containers.items():
            name = inspect['Name'].lstrip('/')
            inspect = sort_docker_inspect(copy.deepcopy(inspect))
            new_hash = hashlib.sha1(
                json.dumps(inspect, sort_keys=True).encode('utf-8')
            ).hexdigest()
            container = bleemeo_cache.containers_by_docker_id.get(docker_id)

            if container is not None and container.inspect_hash == new_hash:
                continue

            if container is None:
                method = 'post'
                action_text = 'registered'
                url = container_url
            else:
                method = 'put'
                action_text = 'updated'
                url = container_url + container.uuid + '/'

            cmd = inspect.get('Config', {}).get('Cmd', [])
            if cmd is None:
                cmd = []

            payload = {
                'host': self.agent_uuid,
                'name': name[:API_CONTAINER_NAME_LENGTH],
                'command': ' '.join(cmd),
                'docker_status': inspect.get('State', {}).get('Status', ''),
                'docker_created_at': convert_docker_date(
                    inspect.get('Created')
                ),
                'docker_started_at': convert_docker_date(
                    inspect.get('State', {}).get('StartedAt')
                ),
                'docker_finished_at': convert_docker_date(
                    inspect.get('State', {}).get('FinishedAt')
                ),
                'docker_api_version': self.core.last_facts.get(
                    'docker_api_version', ''
                ),
                'docker_id': docker_id,
                'docker_image_id': inspect.get('Image', ''),
                'docker_image_name':
                    inspect.get('Config', '').get('Image', ''),
                'docker_inspect': json.dumps(inspect),
            }

            response = bleemeo_api.api_call(
                url,
                method,
                data=json.dumps(payload),
                params={'fields': ','.join(['id'] + list(payload.keys()))},
            )

            if response.status_code not in (200, 201):
                raise ApiError(response)
            obj_uuid = response.json()['id']
            container = Container(
                obj_uuid,
                name,
                docker_id,
                new_hash,
            )
            bleemeo_cache.containers[obj_uuid] = container
            logging.debug('Container %s %s', container.name, action_text)
        bleemeo_cache.update_lookup_map()

        # Step 4: delete object present in API by not in local
        try:
            local_uuids = set(
                bleemeo_cache.containers_by_docker_id[key].uuid
                for key in local_containers
            )
        except KeyError:
            logging.info(
                'Some containers are not registered, skipping deleting phase',
            )
            return
        deleted_containers_from_state = (
            set(bleemeo_cache.containers) - local_uuids
        )
        deleted_container_names = set()
        for container_uuid in deleted_containers_from_state:
            container = bleemeo_cache.containers[container_uuid]
            url = container_url + container_uuid + '/'
            response = bleemeo_api.api_call(
                url,
                'delete',
            )
            if response.status_code not in (204, 404):
                logging.debug(
                    'Container deletion failed. Server response = %s',
                    response.content,
                )
                continue
            self.last_containers_removed = bleemeo_agent.util.get_clock()
            del bleemeo_cache.containers[container_uuid]
            deleted_container_names.add(container.name)
            logging.debug('Container %s deleted', container.name)
        bleemeo_cache.update_lookup_map()

        with self._current_metrics_lock:
            self._current_metrics = {
                key: value
                for (key, value) in self._current_metrics.items()
                if not value.container_name
                or value.container_name in bleemeo_cache.containers_by_name
            }

    def sent_metric(self, metric_name, metric_has_status, bleemeo_cache=None):
        """ Return True if the metric should be sent to Bleemeo Cloud platform
        """
        if bleemeo_cache is None:
            bleemeo_cache = self._bleemeo_cache
        if bleemeo_cache.current_config is None:
            return True

        blacklist = bleemeo_cache.current_config.metrics_blacklist
        if metric_name in blacklist:
            return False

        whitelist = bleemeo_cache.current_config.metrics_whitelist
        if not whitelist:
            # Always sent metrics if whitelist is empty
            return True

        if metric_has_status:
            return True

        if metric_name in whitelist:
            return True

        return False

    def _sync_facts(self, bleemeo_cache, bleemeo_api):
        # pylint: disable=too-many-locals
        logging.debug('Synchronize facts')
        fact_url = 'v1/agentfact/'

        if self.core.state.get('facts_uuid') is not None:
            # facts_uuid were used in older version of Agent
            self.core.state.delete('facts_uuid')

        # Step 1: refresh cache from API
        api_facts = bleemeo_api.api_iterator(
            fact_url,
            params={'agent': self.agent_uuid, 'page_size': 100},
        )

        bleemeo_cache.facts = {}
        bleemeo_cache.facts_by_key = {}

        for data in api_facts:
            fact = AgentFact(
                data['id'],
                data['key'],
                data['value'],
            )
            bleemeo_cache.facts[fact.uuid] = fact
            bleemeo_cache.facts_by_key[fact.key] = fact

        # Step 2: delete local object that are deleted from API
        # Not done with facts. API never delete facts.

        if bleemeo_cache.current_config is not None:
            docker_integration = (
                bleemeo_cache.current_config.docker_integration
            )
        else:
            docker_integration = True
        facts = {
            fact_name: value
            for (fact_name, value) in self.core.last_facts.items()
            if docker_integration or not fact_name.startswith('docker_')
        }

        # Step 3: register/update object present in local but not in API
        for fact_name, value in facts.items():
            fact = bleemeo_cache.facts_by_key.get(fact_name)

            if fact is not None and fact.value == str(value):
                continue

            # Agent is not allowed to update fact. Always
            # do a create and it will be removed later.

            payload = {
                'agent': self.agent_uuid,
                'key': fact_name,
                'value': str(value),
            }
            response = bleemeo_api.api_call(
                fact_url,
                'post',
                data=json.dumps(payload),
            )
            if response.status_code == 201:
                logging.debug(
                    'Send fact %s, stored with uuid %s',
                    fact_name,
                    response.json()['id'],
                )
            else:
                raise ApiError(response)

            data = response.json()
            fact = AgentFact(
                data['id'],
                data['key'],
                data['value'],
            )
            bleemeo_cache.facts[fact.uuid] = fact
            bleemeo_cache.facts_by_key[fact.key] = fact

        # Step 4: delete object present in API by not in local
        try:
            local_uuids = set(
                bleemeo_cache.facts_by_key[key].uuid
                for key in facts
            )
        except KeyError:
            logging.info(
                'Some facts are not registered, skipping delete phase',
            )
            return
        deleted_facts_from_state = set(bleemeo_cache.facts) - local_uuids
        for fact_uuid in deleted_facts_from_state:
            fact = bleemeo_cache.facts[fact_uuid]
            response = bleemeo_api.api_call(
                urllib_parse.urljoin(fact_url, '%s/' % fact_uuid),
                'delete',
            )
            if response.status_code != 204:
                raise ApiError(response)
            del bleemeo_cache.facts[fact_uuid]
            if (fact.key in bleemeo_cache.facts_by_key and
                    bleemeo_cache.facts_by_key[fact.key].uuid == fact_uuid):
                del bleemeo_cache.facts_by_key[fact.key]
            logging.debug(
                'Fact %s deleted (uuid=%s)',
                fact.key,
                fact.uuid,
            )

    def emit_metric(self, metric):
        metric_name = metric['measurement']
        metric_has_status = metric.get('status') is not None
        if not self.sent_metric(metric_name, metric_has_status):
            return

        if (self._bleemeo_cache.current_config is not None
                and not self._bleemeo_cache.current_config.docker_integration
                and metric.get('container')):
            return

        if self._metric_queue.qsize() > 100000:
            # Remove one message to make room.
            self._metric_queue.get_nowait()
        self._metric_queue.put(metric)
        metric_name = metric['measurement']
        service = metric.get('service')
        item = metric.get('item', '')

        with self._current_metrics_lock:
            self._current_metrics[(metric_name, item)] = MetricRegistrationReq(
                metric_name,
                item,
                service,
                metric.get('instance', ''),
                metric.get('container', ''),
                metric.get('status_of', ''),
                STATUS_NAME_TO_CODE.get(metric.get('status')),
                metric.get('check_output', ''),
                bleemeo_agent.util.get_clock(),
            )

    @property
    def account_id(self):
        return self.core.config.get('bleemeo.account_id')

    @property
    def agent_uuid(self):
        return self.core.state.get('agent_uuid')

    @property
    def agent_username(self):
        return '%s@bleemeo.com' % self.agent_uuid

    @property
    def agent_password(self):
        return self.core.state.get('password')

    @property
    def bleemeo_base_url(self):
        return self.core.config.get(
            'bleemeo.api_base', 'https://api.bleemeo.com/',
        )

    @property
    def upgrade_in_progress(self):
        upgrade_file = self.core.config.get('agent.upgrade_file', 'upgrade')
        return os.path.exists(upgrade_file)
