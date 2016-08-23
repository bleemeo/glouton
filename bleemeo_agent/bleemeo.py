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

import datetime
import hashlib
import json
import logging
import os
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


MQTT_QUEUE_MAX_SIZE = 2000


def api_iterator(url, params, auth, headers=None):
    """ Call Bleemeo API on a list endpoints and return a iterator
        that request all pages
    """
    params = params.copy()
    if 'page_size' not in params:
        params['page_size'] = 100

    response = requests.get(
        url,
        params=params,
        auth=auth,
        headers=headers,
    )

    data = response.json()
    if isinstance(data, list):
        # Old API without pagination
        for item in data:
            yield item

        return

    for item in data['results']:
        yield item

    while data['next']:
        response = requests.get(data['next'], auth=auth)
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

    extra_ports = service_info.get('extra_ports', {}).copy()
    if service_info.get('port') is not None and len(extra_ports) == 0:
        if service_info['protocol'] == socket.IPPROTO_TCP:
            extra_ports['%s/tcp' % service_info['port']] = address
        elif service_info['protocol'] == socket.IPPROTO_UDP:
            extra_ports['%s/udp' % service_info['port']] = address

    return ','.join(
        '%s:%s' % (address, port_proto)
        for (port_proto, address) in extra_ports.items()
    )


class BleemeoConnector(threading.Thread):

    def __init__(self, core):
        super(BleemeoConnector, self).__init__()
        self.core = core

        self._metric_queue = queue.Queue()
        self.connected = False
        self._mqtt_queue_size = 0
        self._last_facts_sent = datetime.datetime(1970, 1, 1)
        self._last_discovery_sent = datetime.datetime(1970, 1, 1)
        self._last_update = datetime.datetime(1970, 1, 1)
        self.mqtt_client = mqtt.Client()
        self.metrics_uuid = self.core.state.get_complex_dict(
            'metrics_uuid', {}
        )
        self.services_uuid = self.core.state.get_complex_dict(
            'services_uuid', {}
        )
        self.metrics_info = {}

        # Make sure this metrics exists and try to be registered
        self.metrics_uuid.setdefault(('agent_status', None, None), None)
        self.metrics_info.setdefault(('agent_status', None, None), {})

        self._apply_upgrade()

    def on_connect(self, client, userdata, flags, rc):
        if rc == 0 and not self.core.is_terminating.is_set():
            self.connected = True
            msg = {
                'public_ip': self.core.last_facts.get('public_ip'),
            }
            self.publish(
                'v1/agent/%s/connect' % self.agent_uuid,
                json.dumps(msg),
            )
            # FIXME: PRODUCT-137: to be removed when upstream bug is fixed
            if self.mqtt_client._ssl is not None:
                self.mqtt_client._ssl.setblocking(0)
            logging.info('MQTT connection established')

    def on_disconnect(self, client, userdata, rc):
        if self.connected:
            logging.info('MQTT connection lost')
        self.connected = False

    def on_publish(self, client, userdata, mid):
        self._mqtt_queue_size -= 1
        self.core.update_last_report()

    def check_config_requirement(self):
        config = self.core.config
        sleep_delay = 10
        while (config.get('bleemeo.account_id') is None
                or config.get('bleemeo.registration_key') is None):
            logging.warning(
                'bleemeo.account_id and/or '
                'bleemeo.registration_key is undefine. '
                'Please see https://docs.bleemeo.com/how-to-configure-agent')
            self.core.is_terminating.wait(sleep_delay)
            if self.core.is_terminating.is_set():
                raise StopIteration
            config = self.core.reload_config()
            sleep_delay = min(sleep_delay * 2, 600)

        if self.core.state.get('password') is None:
            self.core.state.set(
                'password', bleemeo_agent.util.generate_password())

    def run(self):
        self.core.scheduler.add_interval_job(
            self._bleemeo_health_check,
            seconds=60,
        )

        if self.core.sentry_client and self.agent_uuid:
            self.core.sentry_client.site = self.agent_uuid

        try:
            self.check_config_requirement()
        except StopIteration:
            return

        self.core.scheduler.add_interval_job(
            self._bleemeo_synchronize,
            start_date=datetime.datetime.now() + datetime.timedelta(seconds=4),
            seconds=15,
        )

        while not self._ready_for_mqtt():
            self.core.is_terminating.wait(1)
            if self.core.is_terminating.is_set():
                return

        self._mqtt_setup()

        while not self.core.is_terminating.is_set():
            self._loop()

        if self.connected and not self.upgrade_in_progress:
            self.publish(
                'v1/agent/%s/disconnect' % self.agent_uuid,
                json.dumps({'disconnect-cause': 'Clean shutdown'}),
                force=True
            )

        # Wait up to 5 second for MQTT queue to be empty before disconnecting
        deadline = time.time() + 5
        while self._mqtt_queue_size > 0 and time.time() < deadline:
            time.sleep(0.1)

        self.mqtt_client.disconnect()
        self.mqtt_client.loop_stop()

    def _apply_upgrade(self):
        # PRODUCT-279: elasticsearch_search_time was previously not associated
        # with the service elasticsearch
        for key in list(self.metrics_uuid):
            (metric_name, service, item) = key
            if metric_name == 'elasticsearch_search_time' and service is None:
                value = self.metrics_uuid[key]
                new_key = (metric_name, 'elasticsearch', item)
                self.metrics_uuid.setdefault(new_key, value)
                del self.metrics_uuid[key]
                self.core.state.set_complex_dict(
                    'metrics_uuid', self.metrics_uuid
                )

    def _ready_for_mqtt(self):
        """ Check for requirement needed before MQTT connection

            * agent must be registered
            * it need initial facts
            * "agent_status" metrics must be registered
        """
        agent_status_key = ('agent_status', None, None)
        return (
            self.agent_uuid is not None and
            self.core.last_facts and
            self.metrics_uuid.get(agent_status_key) is not None
        )

    def _bleemeo_health_check(self):
        """ Check the Bleemeo connector works correctly. Log any issue found
        """
        now = time.time()

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
                now - self.core.graphite_server.data_last_seen_at > 60):
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
            self.mqtt_client.tls_set(
                self.core.config.get(
                    'bleemeo.mqtt.cafile',
                    '/etc/ssl/certs/ca-certificates.crt'
                ),
                tls_version=tls_version,
            )
            self.mqtt_client.tls_insecure_set(
                self.core.config.get('bleemeo.mqtt.ssl_insecure', False)
            )

        self.mqtt_client.on_connect = self.on_connect
        self.mqtt_client.on_disconnect = self.on_disconnect
        self.mqtt_client.on_publish = self.on_publish

        mqtt_host = self.core.config.get(
            'bleemeo.mqtt.host',
            'mqtt.bleemeo.com'
        )
        mqtt_port = self.core.config.get(
            'bleemeo.mqtt.port',
            8883,
        )

        self.mqtt_client.username_pw_set(
            self.agent_username,
            self.agent_password,
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
        """ Call as long as agent is running. It's the "main" method for
            Bleemeo connector thread.
        """
        metrics = []
        repush_metric = None
        timeout = 3

        try:
            while True:
                metric = self._metric_queue.get(timeout=timeout)
                timeout = 0.3  # Long wait only for the first get
                key = (
                    metric['measurement'],
                    metric.get('service'),
                    metric.get('item')
                )
                metric_uuid = self.metrics_uuid.get(key)
                if metric_uuid is None:
                    # UUID is not available now. Ignore this metric for now
                    self._metric_queue.put(metric)
                    if repush_metric is metric:
                        # It has looped, the first re-pushed metric was
                        # re-read.
                        # Sleep a short time to avoid looping for nothing
                        # and consuming all CPU
                        time.sleep(0.5)
                        break

                    if repush_metric is None:
                        repush_metric = metric

                    continue

                bleemeo_metric = metric.copy()
                bleemeo_metric['uuid'] = metric_uuid
                metrics.append(bleemeo_metric)
                if len(metrics) > 1000:
                    break
        except queue.Empty:
            pass

        if len(metrics) != 0:
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

        content = None
        try:
            response = requests.post(
                registration_url,
                data=json.dumps(payload),
                auth=('%s@bleemeo.com' % self.account_id, registration_key),
                headers={
                    'X-Requested-With': 'XMLHttpRequest',
                    'Content-type': 'application/json',
                },
            )
            if response.status_code == 201:
                content = response.json()
            else:
                logging.debug(
                    'Registration failed, content = %s',
                    response.content
                )
        except requests.exceptions.RequestException:
            response = None
        except ValueError:
            logging.debug(
                'Registration failed, response is not a json: %s',
                response.content[:100])

        if content is not None and 'id' in content:
            self.core.state.set('agent_uuid', content['id'])
            logging.debug('Regisration successfull')
        elif content is not None:
            logging.debug(
                'Registration failed, content (json) = %s',
                content
            )

        if self.core.sentry_client and self.agent_uuid:
            self.core.sentry_client.site = self.agent_uuid

    def _bleemeo_synchronize(self):
        """ Synchronize object between local state and Bleemeo SaaS
        """
        if self.agent_uuid is None:
            self.register()

        if self.agent_uuid is None:
            return

        now = datetime.datetime.now()
        if (now - self._last_update > datetime.timedelta(hours=1)
                or self.core.last_services_autoremove >= self._last_update):
            self._purge_deleted_services()
            self._retrive_threshold()
            self._last_update = now

        self._register_services()
        self._register_metric()

        if self._last_facts_sent < self.core.last_facts_update:
            self.send_facts()

        now = datetime.datetime.now()
        if self._last_discovery_sent < self.core.last_discovery_update:
            self._register_containers()
            self._last_discovery_sent = now

    def _retrive_threshold(self):
        """ Retrieve threshold for all registered metrics

            Also remove from state any deleted metrics and remove it from
            core.last_metrics (in memory cache of last value)
        """
        logging.debug('Retrieving thresholds')
        thresholds = {}
        base_url = self.bleemeo_base_url
        metric_url = urllib_parse.urljoin(base_url, '/v1/metric/')
        metrics = api_iterator(
            metric_url,
            params={'agent': self.agent_uuid},
            auth=(self.agent_username, self.agent_password),
        )

        metrics_registered = set()

        for data in metrics:
            metrics_registered.add(data['id'])
            item = data['item']
            if item == '':
                # API use "" for no item. Agent use None
                item = None

            thresholds[(data['label'], item)] = {
                'low_warning': data['threshold_low_warning'],
                'low_critical': data['threshold_low_critical'],
                'high_warning': data['threshold_high_warning'],
                'high_critical': data['threshold_high_critical'],
            }

        deleted_metrics = []
        for key in list(self.metrics_uuid.keys()):
            (metric_name, service_name, item) = key
            value = self.metrics_uuid[key]
            if value is None or value in metrics_registered:
                continue

            del self.metrics_uuid[key]
            deleted_metrics.append((metric_name, item))

        if deleted_metrics:
            self.core.state.set_complex_dict('metrics_uuid', self.metrics_uuid)
            self.core.purge_metrics(deleted_metrics)

        self.core.state.set_complex_dict('thresholds', thresholds)
        self.core.define_thresholds()

    def _purge_deleted_services(self):
        """ Remove from state any deleted service on API and vice-versa

            Also remove them from discovered service.
        """
        base_url = self.bleemeo_base_url
        service_url = urllib_parse.urljoin(base_url, '/v1/service/')

        deleted_services_from_state = (
            set(self.services_uuid) - set(self.core.services)
        )
        for key in deleted_services_from_state:
            service_uuid = self.services_uuid[key]['uuid']
            response = requests.delete(
                service_url + '%s/' % service_uuid,
                auth=(self.agent_username, self.agent_password),
                headers={
                    'X-Requested-With': 'XMLHttpRequest',
                },
            )
            if response.status_code not in (204, 404):
                logging.debug(
                    'Service deletion failed. Server response = %s',
                    response.content
                )
                continue
            del self.services_uuid[key]
            self.core.state.set_complex_dict(
                'services_uuid', self.services_uuid
            )

        services = api_iterator(
            service_url,
            params={'agent': self.agent_uuid},
            auth=(self.agent_username, self.agent_password),
        )

        services_registred = set()
        for data in services:
            services_registred.add(data['id'])

        deleted_services = []
        for key in list(self.services_uuid.keys()):
            (service_name, instance) = key
            entry = self.services_uuid[key]
            if entry is None or entry['uuid'] in services_registred:
                continue

            del self.services_uuid[key]
            deleted_services.append(key)

        self.core.state.set_complex_dict(
            'services_uuid', self.services_uuid
        )

        if deleted_services:
            logging.debug(
                'API deleted the following services: %s',
                deleted_services
            )
            self.core.update_discovery(deleted_services=deleted_services)

    def _register_services(self):
        """ Check for any unregistered services and register them

            Also check for changed services and update them
        """
        base_url = self.bleemeo_base_url
        registration_url = urllib_parse.urljoin(base_url, '/v1/service/')

        for key, service_info in self.core.services.items():
            (service_name, instance) = key

            entry = {
                'listen_addresses':
                    get_listen_addresses(service_info),
                'label': service_name,
                'exe_path': service_info.get('exe_path', ''),
            }
            if instance is not None:
                entry['instance'] = instance

            if key in self.services_uuid:
                entry['uuid'] = self.services_uuid[key]['uuid']
                # check for possible update
                if self.services_uuid[key] == entry:
                    continue
                method = requests.put
                service_uuid = self.services_uuid[key]['uuid']
                url = registration_url + str(service_uuid) + '/'
                expected_code = 200
            else:
                method = requests.post
                url = registration_url
                expected_code = 201

            payload = entry.copy()
            payload.update({
                'account': self.account_id,
                'agent': self.agent_uuid,
            })

            response = method(
                url,
                data=json.dumps(payload),
                auth=(self.agent_username, self.agent_password),
                headers={
                    'X-Requested-With': 'XMLHttpRequest',
                    'Content-type': 'application/json',
                },
            )
            if response.status_code != expected_code:
                logging.debug(
                    'Service registration failed. Server response = %s',
                    response.content
                )
                continue
            entry['uuid'] = response.json()['id']
            self.services_uuid[key] = entry
            self.core.state.set_complex_dict(
                'services_uuid', self.services_uuid
            )

    def _register_metric(self):
        """ Check for any unregistered metrics and register them
        """
        base_url = self.bleemeo_base_url
        registration_url = urllib_parse.urljoin(base_url, '/v1/metric/')
        thresholds = self.core.state.get_complex_dict('thresholds', {})

        for metric_key, metric_uuid in list(self.metrics_uuid.items()):
            if metric_key not in self.metrics_info:
                # This should only occur when metric were seen on a previous
                # run (and stored in state.json) but not yet registered.
                # If the metric still exists, it should be quickly fixed. If
                # not... we will never register that metric (anyway it doesn't
                # have any points sent)
                continue
            (metric_name, service, item) = metric_key
            status_of = self.metrics_info[metric_key].get('status_of')
            from_metric_key = (status_of, service, item)
            if metric_uuid is None:
                if (status_of is not None
                        and self.metrics_uuid.get(from_metric_key) is None):
                    logging.debug(
                        'Metric %s is status_of unregistered metric %s',
                        metric_name,
                        status_of,
                    )
                    continue
                logging.debug('Registering metric %s', metric_name)
                payload = {
                    'agent': self.agent_uuid,
                    'label': metric_name,
                }
                if item is not None:
                    payload['item'] = item
                if service is not None:
                    instance = self.metrics_info[metric_key]['instance']
                    payload['service'] = (
                        self.services_uuid[(service, instance)]['uuid']
                    )
                if status_of is not None:
                    payload['status_of'] = self.metrics_uuid[from_metric_key]

                response = requests.post(
                    registration_url,
                    data=json.dumps(payload),
                    auth=(self.agent_username, self.agent_password),
                    headers={
                        'X-Requested-With': 'XMLHttpRequest',
                        'Content-type': 'application/json',
                    },
                )
                if response.status_code != 201:
                    logging.debug(
                        'Metric registration failed. Server response = %s',
                        response.content
                    )
                    return
                data = response.json()
                self.metrics_uuid[metric_key] = (
                    data['id']
                )
                thresholds[(metric_name, item)] = {
                    'low_warning': data['threshold_low_warning'],
                    'low_critical': data['threshold_low_critical'],
                    'high_warning': data['threshold_high_warning'],
                    'high_critical': data['threshold_high_critical'],
                }
                logging.debug(
                    'Metric %s registered with uuid %s',
                    metric_name,
                    self.metrics_uuid[metric_key],
                )

                self.core.state.set_complex_dict(
                    'metrics_uuid', self.metrics_uuid
                )
                self.core.state.set_complex_dict('thresholds', thresholds)
                self.core.define_thresholds()

    def _register_containers(self):
        registration_url = urllib_parse.urljoin(
            self.bleemeo_base_url, '/v1/container/',
        )
        container_uuid = self.core.state.get('docker_container_uuid', {})

        for name, inspect in self.core.docker_containers.items():
            new_hash = hashlib.sha1(
                json.dumps(inspect, sort_keys=True).encode('utf-8')
            ).hexdigest()
            old_hash, obj_uuid = container_uuid.get(name, (None, None))

            if old_hash == new_hash:
                continue

            if obj_uuid is None:
                method = requests.post
                url = registration_url
            else:
                method = requests.put
                url = registration_url + obj_uuid + '/'

            cmd = inspect.get('Config', {}).get('Cmd', [])
            if cmd is None:
                cmd = []

            payload = {
                'host': self.agent_uuid,
                'name': name,
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
                'docker_id': inspect.get('Id', ''),
                'docker_image_id': inspect.get('Image', ''),
                'docker_image_name':
                    inspect.get('Config', '').get('Image', ''),
                'docker_inspect': json.dumps(inspect),
            }

            response = method(
                url,
                data=json.dumps(payload),
                auth=(self.agent_username, self.agent_password),
                headers={
                    'X-Requested-With': 'XMLHttpRequest',
                    'Content-type': 'application/json',
                },
            )

            if response.status_code not in (200, 201):
                logging.debug(
                    'Container registration failed. Server response = %s',
                    response.content
                )
                continue
            obj_uuid = response.json()['id']
            container_uuid[name] = (new_hash, obj_uuid)
            self.core.state.set('docker_container_uuid', container_uuid)

        deleted_containers = (
            set(container_uuid) - set(self.core.docker_containers)
        )
        for name in deleted_containers:
            (_, obj_uuid) = container_uuid[name]
            url = registration_url + obj_uuid + '/'
            response = requests.delete(
                url,
                auth=(self.agent_username, self.agent_password),
                headers={
                    'X-Requested-With': 'XMLHttpRequest',
                },
            )
            if response.status_code not in (204, 404):
                logging.debug(
                    'Container deletion failed. Server response = %s',
                    response.content,
                )
                continue
            del container_uuid[name]
            self.core.state.set('docker_container_uuid', container_uuid)

    def emit_metric(self, metric):
        if self._metric_queue.qsize() < 100000:
            self._metric_queue.put(metric)
        metric_name = metric['measurement']
        service = metric.get('service')
        item = metric.get('item')

        if (metric_name, service, item) not in self.metrics_info:
            self.metrics_info.setdefault(
                (metric_name, service, item),
                {
                    'status_of': metric.get('status_of'),
                    'instance': metric.get('instance'),
                }
            )
        if (metric_name, service, item) not in self.metrics_uuid:
            self.metrics_uuid.setdefault((metric_name, service, item), None)

    def send_facts(self):
        base_url = self.bleemeo_base_url
        fact_url = urllib_parse.urljoin(base_url, '/v1/agentfact/')

        if self.core.state.get('facts_uuid') is not None:
            # facts_uuid were used in older version of Agent
            self.core.state.delete('facts_uuid')

        # Action:
        # * get list of all old facts
        # * create new updated facts
        # * delete old facts

        old_facts = api_iterator(
            fact_url,
            params={'agent': self.agent_uuid, 'page_size': 100},
            auth=(self.agent_username, self.agent_password),
            headers={'X-Requested-With': 'XMLHttpRequest'},
        )

        # Do request(s) now. New fact should not be in this list.
        old_facts = list(old_facts)

        # create new facts
        for fact_name, value in self.core.last_facts.items():
            payload = {
                'agent': self.agent_uuid,
                'key': fact_name,
                'value': str(value),
            }
            response = requests.post(
                fact_url,
                data=json.dumps(payload),
                auth=(self.agent_username, self.agent_password),
                headers={
                    'X-Requested-With': 'XMLHttpRequest',
                    'Content-type': 'application/json',
                },
            )
            if response.status_code == 201:
                logging.debug(
                    'Send fact %s, stored with uuid %s',
                    fact_name,
                    response.json()['id'],
                )
            else:
                logging.debug(
                    'Fact registration failed. Server response = %s',
                    response.content
                )
                return

        # delete old facts
        for fact in old_facts:
            logging.debug(
                'Deleting fact %s (uuid=%s)', fact['key'], fact['id']
            )
            response = requests.delete(
                urllib_parse.urljoin(fact_url, '%s/' % fact['id']),
                auth=(self.agent_username, self.agent_password),
                headers={'X-Requested-With': 'XMLHttpRequest'},
            )
            if response.status_code != 204:
                logging.debug(
                    'Delete failed, excepted code=204, recveived %s',
                    response.status_code
                )
                return

        self._last_facts_sent = datetime.datetime.now()

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
            'bleemeo.api_base',
            'https://api.bleemeo.com/'
        )

    @property
    def upgrade_in_progress(self):
        upgrade_file = self.core.config.get('agent.upgrade_file', 'upgrade')
        return os.path.exists(upgrade_file)
