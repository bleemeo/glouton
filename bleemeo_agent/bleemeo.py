import datetime
import json
import logging
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


class BleemeoConnector(threading.Thread):

    def __init__(self, core):
        super(BleemeoConnector, self).__init__()
        self.core = core

        self._metric_queue = queue.Queue()
        self.connected = False
        self._mqtt_queue_size = 0
        self._last_facts_sent = datetime.datetime(1970, 1, 1)
        self._last_threshold_update = datetime.datetime(1970, 1, 1)
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

    def on_connect(self, client, userdata, flags, rc):
        if rc == 0:
            self.connected = True
            msg = {
                'public_ip': self.core.last_facts.get('public_ip'),
            }
            self.publish(
                'v1/agent/%s/connect' % self.agent_uuid,
                json.dumps(msg),
            )
            # FIXME: PRODUCT-137 : to be removed when upstream bug is fixed
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

        self.mqtt_client.loop_stop()
        if self.connected:
            self.publish(
                'v1/agent/%s/disconnect' % self.agent_uuid,
                json.dumps({'disconnect-cause': 'Clean shutdown'}),
            )
            self.mqtt_client.loop()

        self.mqtt_client.disconnect()
        self.mqtt_client.loop()

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
                'Issue with metrics collector : no metric received from %s',
                self.core.graphite_server.metrics_source,
            )

    def _mqtt_setup(self):
        self.mqtt_client.will_set(
            'v1/agent/%s/disconnect' % self.agent_uuid,
            json.dumps({'disconnect-cause': 'disconnect-will'}),
            1,
        )
        if self.core.config.get('bleemeo.mqtt.ssl', True):
            self.mqtt_client.tls_set(
                self.core.config.get(
                    'bleemeo.mqtt.cafile',
                    '/etc/ssl/certs/ca-certificates.crt'
                ),
                tls_version=ssl.PROTOCOL_TLSv1_2,
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

    def publish(self, topic, message):
        if self._mqtt_queue_size > MQTT_QUEUE_MAX_SIZE:
            return

        self.mqtt_client.publish(
            topic,
            message,
            1)
        self._mqtt_queue_size += 1

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
                'Registration failed, response is not a json : %s',
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
        if now - self._last_threshold_update > datetime.timedelta(hours=1):
            self._retrive_threshold()

        self._register_services()
        self._register_metric()

        if self._last_facts_sent < self.core.last_facts_update:
            self.send_facts()

    def _retrive_threshold(self):
        """ Retrieve threshold for all registered metrics
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

        for data in metrics:
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

        self.core.state.set_complex_dict('thresholds', thresholds)
        self.core.define_thresholds()
        self._last_threshold_update = datetime.datetime.now()

    def _register_services(self):
        """ Check for any unregistered services and register them

            Also check for changed services and update them
        """
        base_url = self.bleemeo_base_url
        registration_url = urllib_parse.urljoin(base_url, '/v1/service/')

        for key, service_info in self.core.services.items():
            (service_name, instance) = key
            try:
                address = socket.gethostbyname(service_info['address'])
            except (socket.gaierror, TypeError, KeyError):
                # gaierror => unable to resolv name
                # TypeError => service_info['address'] is None (happen when
                #              service is on a stopped container)
                # KeyError => no 'address' in service_info (happen when service
                #             is a customer defined using Nagios check).
                address = None

            entry = {
                'address': address,
                'label': service_name,
            }
            if instance is not None:
                entry['instance'] = instance

            if service_info.get('protocol') is not None:
                entry['port'] = service_info['port']
                entry['protocol'] = service_info['protocol']
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
                    # When a service is set, item == instance
                    payload['service'] = (
                        self.services_uuid[(service, item)]['uuid']
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

    def emit_metric(self, metric):
        if self._metric_queue.qsize() < 100000:
            self._metric_queue.put(metric)
        metric_name = metric['measurement']
        service = metric.get('service')
        item = metric.get('item')

        if (metric_name, service, item) not in self.metrics_info:
            self.metrics_info.setdefault(
                (metric_name, service, item),
                {'status_of': metric.get('status_of')}
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
