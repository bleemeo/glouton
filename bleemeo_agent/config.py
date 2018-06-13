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

r"""
Load configuration (in yaml) from a "conf.d" folder.

Path to configuration are hardcoded, in this order:

* /etc/bleemeo/agent.conf
* /etc/bleemeo/agent.conf.d/*.conf
* etc/agent.conf
* etc/agent.conf.d/*.conf

Under Windows, paths are:

* C:\ProgramData\Bleemeo\etc\agent.conf
* C:\ProgramData\Bleemeo\etc\agent.conf.d
* etc\agent.conf
* etc\agent.conf.d\*.conf
"""


import functools
import glob
import os

import yaml


PATHS = [
    '/etc/bleemeo/agent.conf',
    '/etc/bleemeo/agent.conf.d',
    'etc/agent.conf',
    'etc/agent.conf.d'
]


WINDOWS_PATHS = [
    r'C:\ProgramData\Bleemeo\etc\agent.conf',
    r'C:\ProgramData\Bleemeo\etc\agent.conf.d',
    r'etc\agent.conf',
    r'etc\agent.conf.d',
]


ENVIRON_CONFIG_VARS = [
    ('BLEEMEO_AGENT_FACT_FILE', 'agent.facts_file', 'string'),
    ('BLEEMEO_AGENT_INSTALLATION', 'agent.installation', 'string'),
    ('BLEEMEO_AGENT_NETSTAT_FILE', 'agent.netstat_file', 'string'),
    ('BLEEMEO_AGENT_PUBLIC_IP_INDICATOR', 'agent.public_ip_indicator', 'string'),
    ('BLEEMEO_AGENT_STATE_FILE', 'agent.state_file', 'string'),
    ('BLEEMEO_AGENT_UPGRADE_FILE', 'agent.upgrade_file', 'string'),
    ('BLEEMEO_AGENT_TAGS', 'tags', 'list'),
    ('BLEEMEO_AGENT_STACK', 'stack', 'string'),
    ('BLEEMEO_AGENT_LOGGING_LEVEL', 'logging.level', 'string'),
    ('BLEEMEO_AGENT_LOGGING_OUTPUT', 'logging.output', 'string'),
    ('BLEEMEO_AGENT_CONTAINER_TYPE', 'container.type', 'string'),
    ('BLEEMEO_AGENT_CONTAINER_PID_NAMESPACE_HOST', 'container.pid_namespace_host', 'bool'),
    ('BLEEMEO_AGENT_ENABLED', 'bleemeo.enabled', 'bool'),
    ('BLEEMEO_AGENT_ACCOUNT_ID', 'bleemeo.account_id', 'string'),
    ('BLEEMEO_AGENT_REGISTRATION_KEY', 'bleemeo.registration_key', 'string'),
    ('BLEEMEO_AGENT_API_BASE', 'bleemeo.api_base', 'string'),
    ('BLEEMEO_AGENT_MQTT_HOST', 'bleemeo.mqtt.host', 'string'),
    ('BLEEMEO_AGENT_MQTT_PORT', 'bleemeo.mqtt.port', 'int'),
    ('BLEEMEO_AGENT_MQTT_SSL', 'bleemeo.mqtt.ssl', 'bool'),
    ('BLEEMEO_AGENT_MQTT_CAFILE', 'bleemeo.mqtt.cafile', 'string'),
    ('BLEEMEO_AGENT_SSL_INSECURE', 'bleemeo.mqtt.ssl_insecure', 'bool'),
    ('BLEEMEO_AGENT_SENTRY_DSN', 'bleemeo.sentry.dsn', 'string'),
    ('BLEEMEO_AGENT_GRAPHITE_METRICS_SOURCES', 'graphite.metrics_sources', 'string'),
    ('BLEEMEO_AGENT_GRAPHITE_LISTENER_ADDRESS', 'graphite.listener.address', 'string'),
    ('BLEEMEO_AGENT_GRAPHITE_LISTENER_PORT', 'graphite.listener.port', 'int'),
    ('BLEEMEO_AGENT_TELEGRAF_STATSD_ENABLED', 'telegraf.statsd.enabled', 'bool'),
    ('BLEEMEO_AGENT_TELEGRAF_STATSD_ADDRESS', 'telegraf.statsd.address', 'string'),
    ('BLEEMEO_AGENT_TELEGRAF_CONFIG_FILE', 'telegraf.config_file', 'string'),
    ('BLEEMEO_AGENT_TELEGRAF_DOCKER_NAME', 'telegraf.docker_name', 'string'),
    ('BLEEMEO_AGENT_TELEGRAF_DOCKER_METRICS_ENABLED', 'telegraf.docker_metrics_enabled', 'bool'),
    ('BLEEMEO_AGENT_TELEGRF_RESTART_COMMAND', 'telegraf.restart_command', 'string'),
    ('BLEEMEO_AGENT_COLLECTD_CONFIG_FILE', 'collectd.config_file', 'string'),
    ('BLEEMEO_AGENT_COLLECTD_DOCKER_NAME', 'collectd.docker_name', 'string'),
    ('BLEEMEO_AGENT_COLLECTD_RESTART_COMMAND', 'collectd.restart_command', 'string'),
    ('BLEEMEO_AGENT_METRIC_PULL', 'metric.pull', 'dict'),
    ('BLEEMEO_AGENT_METRIC_PROMETHEUS', 'metric.prometheus', 'dict'),
    ('BLEEMEO_AGENT_METRIC_SOFTSTATUS_PERIOD_DEFAULT', 'metric.softstatus_period_default', 'int'),
    ('BLEEMEO_AGENT_METRIC_SOFTSTATUS_PERIOD', 'metric.softstatus_period', 'int'),
    ('BLEEMEO_AGENT_SERVICE', 'service', 'list'),
    ('BLEEMEO_AGENT_SERVICE_IGNORE_METRICS', 'service_ignore_metrics', 'list'),
    ('BLEEMEO_AGENT_SERVICE_IGNORE_CHECK', 'service_ignore_check', 'list'),
    ('BLEEMEO_AGENT_WEB_ENABLED', 'web.enabled', 'bool'),
    ('BLEEMEO_AGENT_WEB_LISTENER_ADDRESS', 'web.listener.address', 'string'),
    ('BLEEMEO_AGENT_WEB_LISTENER_PORT', 'web.listener.port', 'int'),
    ('BLEEMEO_AGENT_INFLUXDB_ENABLED', 'influxdb.enabled', 'bool'),
    ('BLEEMEO_AGENT_INFLUXDB_HOST', 'influxdb.host', 'string'),
    ('BLEEMEO_AGENT_INFLUXDB_PORT', 'influxdb.port', 'int'),
    ('BLEEMEO_AGENT_INFLUXDB_DB_NAME', 'influxdb.db_name', 'string'),
    ('BLEEMEO_AGENT_NETWORK_INTERFACE_BLACKLIST', 'network_interface_blacklist', 'list'),
    ('BLEEMEO_AGENT_DISK_MONITOR', 'disk_monitor', 'list'),
    ('BLEEMEO_AGGENT_DF_PATH_IGNORE', 'df.path_ignore', 'list'),
    ('BLEEMEO_AGENT_DF_HOST_MOUNT_POINT', 'df.host_mount_point', 'string'),
    ('BLEEMEO_AGENT_THRESHOLDS', 'thresholds', 'dict'),
    ('BLEEMEO_AGENT_KUBERNETES_NODENAME', 'kubernetes.nodename', 'string'),
    ('BLEEMEO_AGENT_KUBERNETES_ENABLED', 'kubernetes.enabled', 'bool'),
]


class Config(dict):
    """
    Work exacly like a normal dict, but "get" method known about sub-dict

    Also add "set" method that known about sub-dict.
    """

    def get(self, name, default=None, separator='.'):
        """ If name contains separator ("." by default), it will search
            in sub-dict.

            Example, if you config is {'category': {'value': 5}}, then
            get('category.value') will return 5.
        """
        current = self
        for path in name.split(separator):
            if path not in current:
                return default
            current = current[path]
        return current

    def set(self, name, value, separator='.'):
        """ If name contains separator ("." by default), it will search
            in sub-dict.

            Example, set(category.value, 5) write result in
            self['category']['value'] = 5.
            It does create intermediary dict as needed (in your example,
            self['category'] = {} if not already an dict).
        """
        current = self
        splitted_name = name.split(separator)
        (paths, last_name) = (splitted_name[:-1], splitted_name[-1])
        for path in paths:
            if not isinstance(current.get(path), dict):
                current[path] = {}
            current = current[path]
        current[last_name] = value

    def delete(self, name, separator='.'):
        """ If name name contains separator ("." by default), it will search
            in sub-dict.

            Example, delete("category.value") will result in
            del self['category']['value'].
            It does NOT delete empty parent.
        """
        current = self
        splitted_name = name.split(separator)
        (paths, last_name) = (splitted_name[:-1], splitted_name[-1])
        for path in paths:
            current = current[path]
        del current[last_name]


def merge_dict(destination, source):
    """ Merge two dictionary (recursivly). destination is modified

        List are merged by appending source' list to destination' list
    """
    for (key, value) in source.items():
        if (key in destination
                and isinstance(value, dict)
                and isinstance(destination[key], dict)):
            destination[key] = merge_dict(destination[key], value)
        elif (key in destination
              and isinstance(value, list)
              and isinstance(destination[key], list)):
            destination[key].extend(value)
        else:
            destination[key] = value
    return destination


def load_config(paths=None):
    """ Load configuration from given paths (a list) and return a ConfigParser

        If paths is not provided, use default value (PATH, see doc from module)
    """
    if paths is None and os.name == 'nt':
        paths = WINDOWS_PATHS
    elif paths is None:
        paths = PATHS

    default_config = Config()
    errors = []

    configs = [default_config]
    for filepath in config_files(paths):
        try:
            with open(filepath) as config_file:
                config = yaml.safe_load(config_file)

                # config could be None if file is empty.
                # config could be non-dict if top-level of file is another YAML
                # type, like a list or just a string.
                if config is not None and isinstance(config, dict):
                    configs.append(config)
                elif config is not None:
                    errors.append(
                        'wrong format for file "%s"' % filepath
                    )

        except Exception as exc:  # pylint: disable=broad-except
            errors.append(str(exc).replace('\n', ' '))

    return functools.reduce(merge_dict, configs), errors


def config_files(paths):
    """ Return config files present in given paths.

        For each path, if:

        * it is a directory, return all *.conf files inside the directory
        * it is a file, return the path
        * no config file exists for the path, skip it

        So, if path is ['/etc/bleemeo/agent.conf', '/etc/bleemeo/agent.conf.d']
        you will get /etc/bleemeo/agent.conf (if it exists) and all
        existings *.conf under /etc/bleemeo/agent.conf.d
    """
    files = []
    for path in paths:
        if os.path.isfile(path):
            files.append(path)
        elif os.path.isdir(path):
            files.extend(sorted(glob.glob(os.path.join(path, '*.conf'))))

    return files
