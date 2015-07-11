import copy
import json
import logging
import logging.handlers
import os
import sched
import signal
import sys
import time
import threading

import stevedore

import bleemeo_agent
import bleemeo_agent.bleemeo
import bleemeo_agent.checker
import bleemeo_agent.collectd
import bleemeo_agent.config
import bleemeo_agent.influxdb
import bleemeo_agent.util
import bleemeo_agent.web


def main():
    config = bleemeo_agent.config.load_config()
    setup_logger(config)
    logging.info('Agent starting...')

    try:
        core = Core(config)
        core.run()
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

    # Special case for requets.
    # Requests log "Starting new connection" in INFO
    # Requests log each query in DEBUG
    if level != logging.DEBUG:
        # When not in debug, log neither of above
        logger_request = logging.getLogger('requests')
        logger_request.setLevel(logging.WARNING)
    else:
        # Even in debug, don't log every query
        logger_request = logging.getLogger('requests')
        logger_request.setLevel(logging.INFO)


class StoredValue:
    """ Persistant store for value used by agent.

        Currently store in a json file
    """
    def __init__(self, filename):
        self.filename = filename
        self._content = {}
        self.reload()

    def reload(self):
        if os.path.exists(self.filename):
            with open(self.filename) as fd:
                self._content = json.load(fd)

    def save(self):
        try:
            with open(self.filename, 'w') as fd:
                json.dump(self._content, fd)
        except IOError as exc:
            logging.warning('Failed to store file : %s', exc)

    def get(self, key, default=None):
        return self._content.get(key, default)

    def set(self, key, value):
        self._content[key] = value
        self.save()


class Core:
    def __init__(self, config):
        self.config = config
        self.stored_values = StoredValue(
            config.get(
                'agent',
                'stored_values_file',
                '/var/lib/bleemeo/store.json'))
        self.checks = []

        self.re_exec = False

        self.is_terminating = threading.Event()
        self.bleemeo_connector = None
        self.influx_connector = None
        self.collectd_server = None
        self.scheduler = sched.scheduler(time.time, time.sleep)
        self.last_metrics = {}

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
            bleemeo_agent.checker.initialize_checks(self)
            self.periodic_check()
            self.send_facts()
            self.send_process_info()
            self.scheduler.run()
        except (KeyboardInterrupt, StopIteration):
            pass
        finally:
            self.is_terminating.set()

        if self.re_exec:
            # Wait for other thread to complet
            bleemeo_agent.web.shutdown_server()
            self.mqtt_connector.join()
            self.collectd_server.join()

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

        self.bleemeo_connector = bleemeo_agent.bleemeo.BleemeoConnector(self)
        self.bleemeo_connector.start()

        self.influx_connector = bleemeo_agent.influxdb.InfluxDBConnector(self)
        self.influx_connector.start()

        self.collectd_server = bleemeo_agent.collectd.Collectd(self)
        self.collectd_server.start()

        bleemeo_agent.web.start_server(self)

    def periodic_check(self):
        """ Run few periodic check:

            * that agent is not being terminated
            * call bleemeo_agent.checker.periodic_check
            * reschedule itself every 3 seconds
        """
        if self.is_terminating.is_set():
            raise StopIteration

        bleemeo_agent.checker.periodic_check(self)
        self.scheduler.enter(3, 1, self.periodic_check, ())

    def send_facts(self):
        """ Send facts to Bleemeo SaaS and reschedule itself """
        facts = bleemeo_agent.util.get_facts(self)
        self.bleemeo_connector.publish(
            'api/v1/agent/facts/POST',
            json.dumps(facts))
        self.scheduler.enter(3600, 1, self.send_facts, ())

    def send_process_info(self):
        now = time.time()
        info = bleemeo_agent.util.get_processes_info()
        for process_info in info:
            self.emit_metric({
                'measurement': 'process_info',
                'time': now,
                'tags': {
                    'pid': str(process_info.pop('pid')),
                    'create_time': str(process_info.pop('create_time')),
                },
                'fields': process_info,
            }, store_last_value=False)
        self.scheduler.enter(60, 1, self.send_process_info, ())

    def plugins_on_load_failure(self, manager, entrypoint, exception):
        logging.info('Plugin %s failed to load : %s', entrypoint, exception)

    def check_plugin_v1(self, extension):
        has_dependencies = extension.obj.dependencies_present()
        if not has_dependencies:
            return False

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

    def reload_config(self):
        self.config = bleemeo_agent.config.load_config()
        self.stored_values = StoredValue(
            self.config.get(
                'agent',
                'stored_values_file',
                '/var/lib/bleemeo/store.json'))

        return self.config

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

    def emit_metric(self, metric, store_last_value=True):
        """ Sent a metric to all configured output
        """
        def exclude_same_metric(item):
            if item['tags'] == metric['tags']:
                return False
            else:
                return True

        if store_last_value:
            # We use list(...) to force evaluation of the result and avoid a
            # possible memory leak. In Python3 filter return a "filter object".
            # Without list() we may end with a filter object on a filter object
            # on a filter object ...
            measurement = metric['measurement']
            self.last_metrics[measurement] = list(filter(
                exclude_same_metric, self.last_metrics.get(measurement, [])))
            self.last_metrics[measurement].append(metric)

        if not metric.get('ignore'):
            if 'ignore' in metric:
                del metric['ignore']

            self.bleemeo_connector.emit_metric(copy.deepcopy(metric))
            self.influx_connector.emit_metric(copy.deepcopy(metric))

    def get_last_metric(self, name, tags):
        """ Return the last metric matching name and tags.

            None is returned if the metric is not found
        """
        for metric in self.last_metrics.get(name, []):
            if metric['tags'] == tags:
                return metric

        return None
