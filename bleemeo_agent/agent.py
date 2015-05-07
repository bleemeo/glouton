import json
import logging
import logging.handlers
import signal
import time
import threading

import stevedore

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

    (login, password) = bleemeo_agent.config.get_credentials(config)
    try:
        daemon = Agent(config, login, password)
        daemon.run()
    except Exception:
        logging.critical(
            'Unhandled error occured. Agent is will terminate',
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


class Agent:
    """ Class that hold "global" information about the agent
    """

    def __init__(self, config, login, password):
        self.config = config
        self.login = login
        self.password = password

        self.registration_done = False

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

        while True:
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

            time.sleep(10)

    def send_facts(self):
        facts = bleemeo_agent.util.get_facts()
        self.mqtt_connector.publish(
            'agents/facts/POST',
            json.dumps(facts))

    def register(self):
        """ Register the agent to Bleemeo SaaS service

            Return True if registration succeeded
        """
        return False

    @property
    def account_id(self):
        return self.config.get('agent', 'account_id')

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
