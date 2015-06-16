import json
import logging
import logging.handlers
import os
import signal
import socket
import sys
import time
import threading

import passlib.context
import requests
import stevedore

import bleemeo_agent
import bleemeo_agent.checker
import bleemeo_agent.collectd
import bleemeo_agent.config
import bleemeo_agent.mqtt
import bleemeo_agent.util
import bleemeo_agent.web


def main():
    config = bleemeo_agent.config.load_config()
    setup_logger(config)
    logging.info('Agent starting...')

    sleeper = bleemeo_agent.util.Sleeper()
    while (not config.has_option('agent', 'account_id')
            or not config.has_option('agent', 'registration_key')):
        logging.warning(
            'agent.account_id and/or agent.registration_key is undefine. '
            'Please see https://docs.bleemeo.com/how-to-configure-agent')
        sleeper.sleep()
        config = bleemeo_agent.config.load_config()

    generated_values = bleemeo_agent.config.get_generated_values(config)
    try:
        agent = Agent(config, generated_values)
        agent.run()
    except Exception:
        logging.critical(
            'Unhandled error occured. Agent will terminate',
            exc_info=True)
    finally:
        logging.info('Agent stopped')


def setup_logger(config):
    level_map = {
        'debug': logging.DEBUG,
        'info': logging.INFO,
        'warning': logging.WARNING,
        'error': logging.ERROR,
    }
    level = level_map[config.get('logging', 'level').lower()]

    root_logger = logging.getLogger()
    root_logger.setLevel(level)

    log_file = config.get('logging', 'file')
    if log_file.lower() not in ('-', 'stdout'):
        handler = logging.handlers.WatchedFileHandler(log_file)
    else:
        handler = logging.StreamHandler()

    handler.setLevel(level)
    formatter = logging.Formatter('%(asctime)s - %(levelname)s - %(message)s')
    handler.setFormatter(formatter)
    root_logger.addHandler(handler)

    # Special case for requets. Requests log "Starting new connection" in INFO
    # we don't only want them in debug
    if level != logging.DEBUG:
        logger_request = logging.getLogger('requests')
        logger_request.setLevel(logging.WARNING)


class Agent:
    """ Class that hold "global" information about the agent
    """

    def __init__(self, config, generated_values):
        self.config = config
        self.generated_values = generated_values

        self.registration_done = False
        self.re_exec = False

        self.is_terminating = threading.Event()
        self.mqtt_connector = bleemeo_agent.mqtt.Connector(self)
        self.collectd_server = bleemeo_agent.collectd.Collectd(self)
        self.check_thread = bleemeo_agent.checker.Checker(self)

        self.plugins_v1_mgr = stevedore.enabled.EnabledExtensionManager(
            namespace='bleemeo_agent.plugins_v1',
            invoke_on_load=True,
            invoke_args=(self,),
            check_func=self.check_plugin_v1,
            on_load_failure_callback=self.plugins_on_load_failure,
        )

    def run(self):
        try:
            self.setup_signal()
            self.start_threads()
            self.loop_forever()
        except KeyboardInterrupt:
            pass
        finally:
            self.is_terminating.set()

        if self.re_exec:
            # Wait for other thread to complet
            bleemeo_agent.web.shutdown_server()
            self.mqtt_connector.join()
            self.collectd_server.join()
            self.check_thread.join()

            # Re-exec ourself
            os.execv(sys.executable, [sys.executable] + sys.argv)
            logging.critical('execv failed?!')

    def setup_signal(self):
        """ Make kill (SIGKILL/SIGQUIT) send a KeyboardInterrupt
        """
        def handler(signum, frame):
            raise KeyboardInterrupt

        signal.signal(signal.SIGTERM, handler)
        signal.signal(signal.SIGQUIT, handler)

    def start_threads(self):
        self.mqtt_connector.start()
        self.collectd_server.start()
        self.check_thread.start()
        bleemeo_agent.web.start_server(self)

    def loop_forever(self):
        """ Run forever. This loop will handle periodic events
        """
        sleeper = bleemeo_agent.util.Sleeper(max_duration=1800)
        registration_next_retry = 0
        last_fact_send = 0

        while not self.is_terminating.is_set():
            now = time.time()
            if not self.registration_done and registration_next_retry < now:
                if self.register():
                    logging.info('Agent registered')
                    self.registration_done = True
                else:
                    sleep = sleeper.get_sleep_duration()
                    logging.info(
                        'Registration failed... retyring in %s', sleep)
                    registration_next_retry = now + sleep

            if last_fact_send + 3600 < now:
                self.send_facts()
                last_fact_send = now

            self.is_terminating.wait(10)

    def send_facts(self):
        facts = bleemeo_agent.util.get_facts(self)
        self.mqtt_connector.publish(
            'api/v1/agent/facts/POST',
            json.dumps(facts))

    def register(self):
        """ Register the agent to Bleemeo SaaS service

            Return True if registration succeeded
        """
        if self.config.has_option('agent', 'registration_url'):
            registration_url = self.config.get('agent', 'registration_url')
        else:
            registration_url = (
                'https://%s.bleemeo.com/api/v1/agent/register/'
                % self.account_id)

        myctx = passlib.context.CryptContext(schemes=["sha512_crypt"])
        password_hash = myctx.encrypt(self.generated_values['password'])
        payload = {
            'account_id': self.account_id,
            'registration_key': self.config.get('agent', 'registration_key'),
            'login': self.generated_values['login'],
            'password_hash': password_hash,
            'agent_version': bleemeo_agent.__version__,
            'hostname': socket.getfqdn(),
        }
        try:
            response = requests.post(registration_url, data=payload)
        except requests.exceptions.RequestException:
            logging.debug('Registration failed', exc_info=True)
            return False

        content = None
        if response.status_code == 200:
            try:
                content = response.json()
            except ValueError:
                logging.debug(
                    'registration response is not a json : %s',
                    response.content[:100])

        if content is not None and content.get('registration') == 'success':
            logging.debug('Regisration successfull')
            return True

        logging.debug('Registration failed, content=%s', content)
        return False

    @property
    def account_id(self):
        return self.config.get('agent', 'account_id')

    @property
    def login(self):
        return self.generated_values['login']

    def plugins_on_load_failure(self, manager, entrypoint, exception):
        logging.info('Plugin %s failed to load : %s', entrypoint, exception)

    def check_plugin_v1(self, extension):
        has_dependencies = extension.obj.dependencies_present()
        if not has_dependencies:
            return False

        # TODO: check for a blacklist of plugins
        self.collectd_server.add_config(extension.obj.collectd_configure())

        logging.debug('Enable plugin %s', extension.name)
        return True

    def reload_plugins(self):
        """ Check if list of plugins change. If it does restart agent.

            Return True is list changed.
        """
        plugins_v1_mgr = stevedore.enabled.EnabledExtensionManager(
            namespace='bleemeo_agent.plugins_v1',
            invoke_on_load=True,
            invoke_args=(self,),
            check_func=self.check_plugin_v1,
            on_load_failure_callback=self.plugins_on_load_failure,
        )
        if (sorted(self.plugins_v1_mgr.names())
                == sorted(plugins_v1_mgr.names())):
            logging.debug('No change in plugins list, do not reload')
            return False

        self.restart()
        return True

    def update_server_config(self, configuration):
        """ Update server configuration and restart agent if it changed
        """
        config_path = '/etc/bleemeo/agent.conf.d/server.conf'
        if os.path.exists(config_path):
            with open(config_path) as fd:
                current_content = fd.read()

            if current_content == configuration:
                logging.debug('Server configuration unchanged, do not reload')
                return

        with open(config_path, 'w') as fd:
            fd.write(configuration)

        self.restart()

    def restart(self):
        """ Restart agent.
        """
        logging.info('Restarting...')

        # Note: we can not do action here, because during re-exec we want to
        # give time  to other thread to complet. especially mqtt_connector
        # (sending pending message), but restart may be called from
        # MQTT thread (while processing server sent configuration).
        # That why we only set is_terminating flag and re_exec flag.
        # The main thread will handle the re-exec.

        self.re_exec = True
        self.is_terminating.set()
