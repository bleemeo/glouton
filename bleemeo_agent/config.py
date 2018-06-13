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


CONFIG_VARS = [
    ('agent.facts_file', 'string'),
    ('agent.installation', 'string'),
    ('agent.netstat_file', 'string'),
    ('agent.public_ip_indicator', 'string'),
    ('agent.state_file', 'string'),
    ('agent.upgrade_file', 'string'),
    ('tags', 'list'),
    ('stack', 'string'),
    ('logging.level', 'string'),
    ('logging.output', 'string'),
    ('container.type', 'string'),
    ('container.pid_namespace_host', 'bool'),
    ('bleemeo.enabled', 'bool'),
    ('bleemeo.account_id', 'string'),
    ('bleemeo.registration_key', 'string'),
    ('bleemeo.api_base', 'string'),
    ('bleemeo.mqtt.host', 'string'),
    ('bleemeo.mqtt.port', 'int'),
    ('bleemeo.mqtt.ssl', 'bool'),
    ('bleemeo.mqtt.cafile', 'string'),
    ('bleemeo.mqtt.ssl_insecure', 'bool'),
    ('bleemeo.sentry.dsn', 'string'),
    ('graphite.metrics_sources', 'string'),
    ('graphite.listener.address', 'string'),
    ('graphite.listener.port', 'int'),
    ('telegraf.statsd.enabled', 'bool'),
    ('telegraf.statsd.address', 'string'),
    ('telegraf.config_file', 'string'),
    ('telegraf.docker_name', 'string'),
    ('telegraf.docker_metrics_enabled', 'bool'),
    ('telegraf.restart_command', 'string'),
    ('collectd.config_file', 'string'),
    ('collectd.docker_name', 'string'),
    ('collectd.restart_command', 'string'),
    ('metric.pull', 'dict'),
    ('metric.prometheus', 'dict'),
    ('metric.softstatus_period_default', 'int'),
    ('metric.softstatus_period', 'int'),
    ('service', 'list'),
    ('service_ignore_metrics', 'list'),
    ('service_ignore_check', 'list'),
    ('web.enabled', 'bool'),
    ('web.listener.address', 'string'),
    ('web.listener.port', 'int'),
    ('influxdb.enabled', 'bool'),
    ('influxdb.host', 'string'),
    ('influxdb.port', 'int'),
    ('influxdb.db_name', 'string'),
    ('network_interface_blacklist', 'list'),
    ('disk_monitor', 'list'),
    ('df.path_ignore', 'list'),
    ('df.host_mount_point', 'string'),
    ('thresholds', 'dict'),
    ('kubernetes.nodename', 'string'),
    ('kubernetes.enabled', 'bool'),
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


def convert_type(value_text, value_type):
    """ Convert string value to given value_type

        Usefull for parameter from environment that must be case to Python type

        Supported value_type:

        * string: no convertion
        * int: int() from Python
        * bool: case-insensitive; "true", "yes", "1" => True
    """
    if value_type == 'string':
        return value_text

    if value_type == 'int':
        return int(value_text)
    elif value_type == 'bool':
        if value_text.lower() in ('true', 'yes', '1'):
            return True
        elif value_text.lower() in ('false', 'no', '0'):
            return False
        else:
            raise ValueError('invalid value %r for boolean' % value_text)
    else:
        raise NotImplementedError('Unknown type %s' % value_type)


def convert_conf_name(conf_name):
    """ Convert the conf_name in env_name for load_conf()."""
    env_name = []
    base = "BLEEMEO_AGENT_"
    if conf_name.startswith('agent.'):
        env_name.append(
            base +
            conf_name.lstrip('agent.').replace('.', '_').upper()
        )
    elif conf_name.startswith('bleemeo.'):
        env_name.append(
            base +
            conf_name.lstrip('bleemeo.').replace('.', '_').upper()
        )

    env_name.append("BLEEMEO_AGENT_" + conf_name.replace('.', '_').upper())
    return env_name


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

    # final configuration
    final_config = functools.reduce(merge_dict, configs)

    # overload of the configuration by the environnement variables
    for(conf_name, conf_type) in CONFIG_VARS:
        env_name = convert_conf_name(conf_name)
        for env_namep in env_name:
            if env_namep in os.environ:
                if conf_type in ['dict', 'list']:
                    errors.append(
                        'Can not overload %s (type %s not supported)'
                        % (env_namep, conf_type)
                    )
                    continue
                try:
                    value = convert_type(os.environ[env_namep], conf_type)
                except ValueError as exc:
                    errors.append(
                        'Bad environ variable %s: %s' % (env_namep, exc)
                    )
                    continue
                final_config.set(conf_name, value)

    return final_config, errors


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
