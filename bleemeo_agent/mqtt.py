import logging
import socket
import threading
import uuid

import paho.mqtt.client as mqtt


class Connector(threading.Thread):

    def __init__(self, agent):
        super(Connector, self).__init__()
        self.agent = agent

        self.mqtt_client = mqtt.Client()
        self.uuid_connection = uuid.uuid4()
        self.connected = False

    def on_connect(self, client, userdata, flags, rc):
        if rc == 0:
            logging.debug('MQTT connection established')
            self.connected = True

        # Subscribing in on_connect() means that if we lose the connection and
        # reconnect then subscriptions will be renewed.
        client.subscribe('%s/agent/+/GET' % self.agent.login)

    def on_message(self, client, userdata, message):
        if message.topic == '%s/agent/configuration/GET' % self.agent.login:
            self.agent.update_server_config(message.payload)
        elif message.topic == '%s/agent/reload_plugins/GET' % self.agent.login:
            self.agent.reload_plugins()
        else:
            logging.info('Unknown message on topic %s', message.topic)

    def on_disconnect(self, client, userdata, rc):
        logging.debug('MQTT connection lost')
        self.connected = False

    def run(self):
        self.mqtt_client.will_set(
            '%s/agent/disconnect/POST' % self.agent.login,
            'disconnect-will %s' % self.uuid_connection,
            1)

        self.mqtt_client.on_connect = self.on_connect
        self.mqtt_client.on_disconnect = self.on_disconnect
        self.mqtt_client.on_message = self.on_message

        if self.agent.config.has_option('agent', 'mqtt_host'):
            mqtt_host = self.agent.config.get('agent', 'mqtt_host')
        else:
            mqtt_host = '%s.bleemeo.com' % self.agent.account_id

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

        self.mqtt_client.publish(
            '%s/agent/connect/POST' % self.agent.login,
            'connect %s' % self.uuid_connection,
            1)

        self.agent.is_terminating.wait()
        self.mqtt_client.publish(
            '%s/agent/disconnect/POST' % self.agent.login,
            'disconnect %s' % self.uuid_connection,
            1)
        self.mqtt_client.loop_stop()

        self.mqtt_client.disconnect()
        self.mqtt_client.loop()

    def publish(self, topic, message):
        self.mqtt_client.publish(
            '%s/%s' % (self.agent.login, topic),
            message,
            1)
