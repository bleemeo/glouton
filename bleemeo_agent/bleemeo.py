import datetime
import json
import logging
import socket
import ssl
import threading
import time
import uuid

import paho.mqtt.client as mqtt
import requests
from six.moves import queue
from six.moves import urllib_parse

import bleemeo_agent


class BleemeoConnector(threading.Thread):

    def __init__(self, core):
        super(BleemeoConnector, self).__init__()
        self.core = core

        self._metric_queue = queue.Queue()
        self._mqtt_queue_size = 0
        self._mqtt_queue_full_last_warning = 0
        self._mqtt_queue_full_count_warning = 0
        self.mqtt_client = mqtt.Client()
        self.uuid_connection = uuid.uuid4()
        self.connected = False

    def on_connect(self, client, userdata, flags, rc):
        if rc == 0:
            logging.debug('MQTT connection established')
            self.connected = True

    def on_disconnect(self, client, userdata, rc):
        logging.debug('MQTT connection lost')
        self.connected = False

    def on_publish(self, client, userdata, mid):
        self._mqtt_queue_size -= 1
        self.core.update_last_report()

    def check_config_requirement(self):
        config = self.core.config
        sleep_delay = 10
        while (config.get('bleemeo.account_id') is None
                or config.get('bleemeo.registration.key') is None):
            logging.warning(
                'bleemeo.account_id and/or '
                'bleemeo.registration.key is undefine. '
                'Please see https://docs.bleemeo.com/how-to-configure-agent')
            self.core.is_terminating.wait(sleep_delay)
            if self.core.is_terminating.is_set():
                raise StopIteration
            config = self.core.reload_config()
            sleep_delay = min(sleep_delay * 2, 600)
            self._warn_mqtt_queue_full()

        if self.core.stored_values.get('password') is None:
            self.core.stored_values.set(
                'password', bleemeo_agent.util.generate_password())
        if self.core.stored_values.get('metrics_uuid') is None:
            self.core.stored_values.set('metrics_uuid', {})

    def run(self):
        try:
            self.check_config_requirement()
        except StopIteration:
            return

        if self.agent_uuid is None:
            self.register()

        # first fact are gathered before agent had registred. Since
        # the job is run every day, it a bit too long to wait.
        # If at this point facts_uuid is undefined, facts were never sent.
        # In this case, just re-send last facts
        if (self.core.last_facts
                and self.core.stored_values.get('facts_uuid') is None):
            self.send_facts(self.core.last_facts)

        # Register return on two case:
        # 1) registration is done => continue normal processing
        # 2) agent is stopping    => we must exit (self.agent_uuid is not
        #    defined and is needed to next step)
        if self.core.is_terminating.is_set():
            return

        self.core.scheduler.add_job(
            self._register_metric,
            trigger='interval',
            minutes=1,
        )

        self._mqtt_setup()

        while not self.core.is_terminating.is_set():
            self._loop()

        self.publish(
            'api/v0/agent/%s/disconnect' % self.agent_uuid,
            'disconnect %s' % self.uuid_connection)
        self.mqtt_client.loop_stop()

        self.mqtt_client.disconnect()
        self.mqtt_client.loop()

    def _mqtt_setup(self):
        self.mqtt_client.will_set(
            'api/v0/agent/%s/disconnect' % self.agent_uuid,
            'disconnect-will %s' % self.uuid_connection,
            1)
        if self.core.config.get('bleemeo.mqtt.ssl', True):
            self.mqtt_client.tls_set(
                self.core.config.get(
                    'bleemeo.mqtt.cafile',
                    '/etc/ssl/certs/ca-certificates.crt'
                ),
                tls_version=ssl.PROTOCOL_TLSv1_2
            )
            self.mqtt_client.tls_insecure_set(
                self.core.config.get('bleemeo.mqtt.ssl_insecure', False)
            )

        self.mqtt_client.on_connect = self.on_connect
        self.mqtt_client.on_disconnect = self.on_disconnect
        self.mqtt_client.on_publish = self.on_publish

        mqtt_host = self.core.config.get(
            'bleemeo.mqtt.host',
            '%s.bleemeo.com' % self.account_id)

        self.mqtt_client.username_pw_set(
            self.agent_uuid,
            self.core.stored_values.get('password'),
        )

        try:
            logging.debug('Connecting to MQTT broker at %s', mqtt_host)
            self.mqtt_client.connect(
                mqtt_host,
                1883,
                60,
            )
        except socket.error:
            pass

        self.mqtt_client.loop_start()

        self.publish(
            'api/v0/agent/%s/connect' % self.agent_uuid,
            'connect %s' % self.uuid_connection)

    def _loop(self):
        """ Call as long as agent is running. It's the "main" method for
            Bleemeo connector thread.
        """
        metrics = []
        timeout = 3

        try:
            while True:
                metric = self._metric_queue.get(timeout=timeout)
                timeout = 0  # Only wait for the first get
                metrics.append(metric)
        except queue.Empty:
            pass

        if len(metrics) != 0:
            self.publish(
                'api/v0/agent/%s/data' % self.agent_uuid,
                json.dumps(metrics)
            )

        self._warn_mqtt_queue_full()

    def publish_top_info(self, top_info):
        self.publish(
            'api/v0/agent/%s/top_info' % self.agent_uuid,
            json.dumps(top_info)
        )

    def publish(self, topic, message):
        if self._mqtt_queue_size > 500:
            self._mqtt_queue_full_count_warning += 1
            return

        self.mqtt_client.publish(
            topic,
            message,
            1)
        self._mqtt_queue_size += 1

    def register(self):
        """ Register the agent to Bleemeo SaaS service
        """
        sleep_delay = datetime.timedelta(seconds=10)
        base_url = self.bleemeo_base_url
        registration_url = urllib_parse.urljoin(base_url, '/v1/agent/')

        payload = {
            'account': '/v1/account/%s/' % self.account_id,
            'password': self.core.stored_values.get('password'),
            'display_name': socket.getfqdn(),
            'fqdn': socket.getfqdn(),
        }

        while not self.core.is_terminating.is_set():
            try:
                response = requests.post(registration_url, data=payload)
            except requests.exceptions.RequestException:
                response = None

            content = None
            if response is not None and response.status_code == 201:
                try:
                    content = response.json()
                except ValueError:
                    logging.debug(
                        'registration response is not a json : %s',
                        response.content[:100])

            if content is not None and 'id' in content:
                self.core.stored_values.set('agent_uuid', content['id'])
                logging.debug('Regisration successfull')
                return
            elif content is not None:
                logging.debug(
                    'Registration failed, content (json) = %s',
                    content
                )
            else:
                logging.debug(
                    'Registration failed, content = %s',
                    response.content
                )

            logging.info(
                'Registration failed... retyring in %s', sleep_delay)
            self.core.is_terminating.wait(sleep_delay.total_seconds())
            sleep_delay = min(sleep_delay * 2, datetime.timedelta(minutes=30))

    def _register_metric(self):
        """ Check for any unregistered metrics and register them
        """
        base_url = self.bleemeo_base_url
        registration_url = urllib_parse.urljoin(base_url, '/v1/metric/')

        metrics_uuid = self.core.stored_values.get('metrics_uuid')
        changed = False

        try:
            for metric_name, metric_uuid in metrics_uuid.items():
                if metric_uuid is None:
                    logging.debug('Registering metric %s', metric_name)
                    payload = {
                        'agent': '/v1/agent/%s/' % self.agent_uuid,
                        'label': metric_name,
                    }
                    response = requests.post(registration_url, data=payload)
                    if response.status_code != 201:
                        logging.debug(
                            'Metric registration failed. Server response = %s',
                            response.content
                        )
                        return
                    metrics_uuid[metric_name] = response.json()['id']
                    logging.debug(
                        'Metric %s registered with uuid %s',
                        metric_name,
                        metrics_uuid[metric_name],
                    )
                    changed = True
        finally:
            if changed:
                self.core.stored_values.set('metrics_uuid', metrics_uuid)

    def emit_metric(self, metric):
        self._metric_queue.put(metric)
        metric_name = metric['measurement']

        metrics_uuid = self.core.stored_values.get('metrics_uuid', {})
        if metric_name not in metrics_uuid:
            metrics_uuid.setdefault(metric_name, None)
            self.core.stored_values.set('metrics_uuid', metrics_uuid)

    def send_facts(self, facts):
        if self.agent_uuid is None:
            logging.debug('Do not sent fact before agent registration')
            return

        base_url = self.bleemeo_base_url
        fact_url = urllib_parse.urljoin(base_url, '/v1/agentfact/')
        facts_uuid = self.core.stored_values.get('facts_uuid', {})

        # first delete any already sent facts
        try:
            for fact_name, fact_uuid in facts_uuid.items():
                logging.debug(
                    'Deleting fact %s (uuid=%s)', fact_name, fact_uuid)
                requests.delete(
                    urllib_parse.urljoin(fact_url, '%s/' % fact_uuid)
                )
        finally:
            self.core.stored_values.set('facts_uuid', facts_uuid)

        # then create one agentfact for item in the mapping.
        try:
            for fact_name, value in facts.items():
                payload = {
                    'agent': '/v1/agent/%s/' % self.agent_uuid,
                    'key': fact_name,
                    'value': str(value),
                }
                response = requests.post(fact_url, data=payload)
                if response.status_code == 201:
                    facts_uuid[fact_name] = response.json()['id']
                    logging.debug(
                        'Send fact %s, stored with uuid %s',
                        fact_name,
                        facts_uuid[fact_name]
                    )
        finally:
            self.core.stored_values.set('facts_uuid', facts_uuid)

    def _warn_mqtt_queue_full(self):
        now = time.time()
        if (self._mqtt_queue_full_count_warning
                and self._mqtt_queue_full_last_warning < now - 60):
            logging.warning(
                'Bleemeo connector: %s message(s) were dropped due to '
                'overflow of the sending queue',
                self._mqtt_queue_full_count_warning)
            self._mqtt_queue_full_last_warning = now
            self._mqtt_queue_full_count_warning = 0

    @property
    def account_id(self):
        return self.core.config.get('bleemeo.account_id')

    @property
    def agent_uuid(self):
        return self.core.stored_values.get('agent_uuid')

    @property
    def bleemeo_base_url(self):
        return self.core.config.get(
            'bleemeo.api_base',
            'https://api.bleemeo.com/'
        )
