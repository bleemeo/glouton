import json
import logging
import socket
import ssl
import threading
import time
import uuid

import paho.mqtt.client as mqtt
import passlib.context
import requests

import bleemeo_agent


class BleemeoConnector(threading.Thread):

    def __init__(self, core):
        super(BleemeoConnector, self).__init__()
        self.core = core

        self._queue_size = 0
        self._queue_full_last_warning = 0
        self._queue_full_count_warning = 0
        self.mqtt_client = mqtt.Client()
        self.uuid_connection = uuid.uuid4()
        self.connected = False

    def on_connect(self, client, userdata, flags, rc):
        if rc == 0:
            logging.debug('MQTT connection established')
            self.connected = True

        # Subscribing in on_connect() means that if we lose the connection and
        # reconnect then subscriptions will be renewed.
        client.subscribe(str('%s/api/v1/agent/+/GET' % self.login))

    def on_message(self, client, userdata, message):
        config_topic = '%s/api/v1/agent/configuration/GET' % self.login
        reload_topic = '%s/api/v1/agent/reload_plugins/GET' % self.login
        fact_topic = '%s/api/v1/agent/request_facts/GET' % self.login
        if message.topic == config_topic:
            self.core.update_server_config(message.payload)
        elif message.topic == reload_topic:
            self.core.reload_plugins()
        elif message.topic == fact_topic:
            logging.debug('Sending facts on server request')
            self.core.send_facts()
        else:
            logging.info('Unknown message on topic %s', message.topic)

    def on_disconnect(self, client, userdata, rc):
        logging.debug('MQTT connection lost')
        self.connected = False

    def on_publish(self, client, userdata, mid):
        self._queue_size -= 1

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
            self._warn_queue_full()

        if (self.core.stored_values.get('login') is None
                or self.core.stored_values.get('password') is None):
            # XXX: set may silently fail to store on disk. It means
            # that login will be only stored in-memory. So each time agent
            # restart, it will register with new login
            self.core.stored_values.set('login', str(uuid.uuid4()))
            self.core.stored_values.set(
                'password', bleemeo_agent.util.generate_password())

    def run(self):
        try:
            self.check_config_requirement()
        except StopIteration:
            return

        self.register()

        self.mqtt_client.will_set(
            'api/v0/agent/%s/disconnect' % self.login,
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
        self.mqtt_client.on_message = self.on_message
        self.mqtt_client.on_publish = self.on_publish

        mqtt_host = self.core.config.get(
            'bleemeo.mqtt.host',
            '%s.bleemeo.com' % self.account_id)

        self.mqtt_client.username_pw_set(
            self.login,
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
            'api/v0/agent/%s/connect' % self.login,
            'connect %s' % self.uuid_connection)

        while not self.core.is_terminating.is_set():
            self._warn_queue_full()
            self.core.is_terminating.wait(3)

        self.publish(
            'api/v0/agent/%s/disconnect' % self.login,
            'disconnect %s' % self.uuid_connection)
        self.mqtt_client.loop_stop()

        self.mqtt_client.disconnect()
        self.mqtt_client.loop()

    def publish(self, topic, message):
        if self._queue_size > 5000:
            self._queue_full_count_warning += 1
            return

        self.mqtt_client.publish(
            topic,
            message,
            1)
        self._queue_size += 1

    def register(self, sleep_delay=10):
        """ Register the agent to Bleemeo SaaS service

            Reschedule itself until it success.
        """
        default_url = (
            'https://%s.bleemeo.com/api/v1/agent/register/' % self.account_id)

        registration_url = self.core.config.get(
            'bleemeo.registration.url', default_url)

        myctx = passlib.context.CryptContext(schemes=["sha512_crypt"])
        password_hash = myctx.encrypt(
            self.core.stored_values.get('password'))
        payload = {
            'account_id': self.account_id,
            'registration_key': self.core.config.get(
                'bleemeo.registration.key'),
            'login': self.login,
            'password_hash': password_hash,
            'agent_version': bleemeo_agent.__version__,
            'hostname': socket.getfqdn(),
        }
        try:
            response = requests.post(registration_url, data=payload)
        except requests.exceptions.RequestException:
            response = None

        content = None
        if response is not None and response.status_code == 200:
            try:
                content = response.json()
            except ValueError:
                logging.debug(
                    'registration response is not a json : %s',
                    response.content[:100])

        if content is not None and content.get('registration') == 'success':
            logging.debug('Regisration successfull')
            return
        elif content is not None:
            logging.debug('Registration failed, content=%s', content)

        logging.info(
            'Registration failed... retyring in %s', sleep_delay)
        new_sleep_delay = min(sleep_delay * 2, 1800)
        self.core.scheduler.enter(
            sleep_delay, 1, self.register, (new_sleep_delay,))

    def emit_metric(self, metric):
        self.publish(
            'api/v0/agent/%s/data' % self.login,
            json.dumps([metric])
        )

    def _warn_queue_full(self):
        now = time.time()
        if (self._queue_full_count_warning
                and self._queue_full_last_warning < now - 60):
            logging.warning(
                'Bleemeo connector: %s metric(s) were dropped due to '
                'overflow of the sending queue',
                self._queue_full_count_warning)
            self._queue_full_last_warning = now
            self._queue_full_count_warning = 0

    @property
    def account_id(self):
        return self.core.config.get('bleemeo.account_id')

    @property
    def login(self):
        return self.core.stored_values.get('login')
