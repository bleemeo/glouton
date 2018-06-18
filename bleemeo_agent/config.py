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
    ('agent.facts_file', 'string', 'facts.yaml'),
    ('agent.installation_format', 'string', 'manual'),
    ('agent.netstat_file', 'string', 'netstat.out'),
    ('agent.public_ip_indicator', 'string', 'https://myip.bleemeo.com'),
    ('agent.state_file', 'string', 'state.json'),
    ('agent.upgrade_file', 'string', 'upgrade'),
    ('agent.cloudimage_creation_file', 'string', 'cloudimage_creation'),
    ('tags', 'list', []),
    ('stack', 'string', ''),
    ('logging.level', 'string', 'INFO'),
    ('logging.output', 'string', 'console'),
    ('logging.output_file', 'string', None),
    ('container.type', 'string', None),
    ('container.pid_namespace_host', 'bool', False),
    ('bleemeo.enabled', 'bool', True),
    ('bleemeo.account_id', 'string', None),
    ('bleemeo.registration_key', 'string', None),
    ('bleemeo.api_base', 'string', 'https://api.bleemeo.com/'),
    ('bleemeo.mqtt.host', 'string', 'mqtt.bleemeo.com'),
    ('bleemeo.mqtt.port', 'int', 8883),
    ('bleemeo.mqtt.ssl', 'bool', True),
    (
        'bleemeo.mqtt.cafile',
        'string',
        '/etc/ssl/certs/ca-certificates.crt'
    ),
    ('bleemeo.mqtt.ssl_insecure', 'bool', False),
    ('bleemeo.sentry.dsn', 'string', None),
    ('graphite.metrics_source', 'string', 'telegraf'),
    ('graphite.listener.address', 'string', '127.0.0.1'),
    ('graphite.listener.port', 'int', 2003),
    ('telegraf.statsd.enabled', 'bool', True),
    ('telegraf.statsd.address', 'string', '127.0.0.1'),
    ('telegraf.statsd.port', 'int', '8125'),
    (
        'telegraf.config_file',
        'string',
        '/etc/telegraf/telegraf.d/bleemeo-generated.conf'
    ),
    ('telegraf.docker_name', 'string', None),
    ('telegraf.docker_metrics_enabled', 'bool', None),
    (
        'telegraf.restart_command',
        'string',
        'sudo -n service telegraf restart'
    ),
    (
        'collectd.config_file',
        'string',
        '/etc/collectd/collectd.conf.d/bleemeo-generated.conf'
    ),
    ('collectd.docker_name', 'string', None),
    (
        'collectd.restart_command',
        'string',
        'sudo -n service collectd restart'
    ),
    ('metric.pull', 'dict', {}),
    ('metric.prometheus', 'dict', {}),
    ('metric.softstatus_period_default', 'int', 5 * 60),
    ('metric.softstatus_period', 'dict', {}),
    ('service', 'list', []),
    ('service_ignore_metrics', 'list', []),
    ('service_ignore_check', 'list', []),
    ('web.enabled', 'bool', True),
    ('web.listener.address', 'string', '127.0.0.1'),
    ('web.listener.port', 'int', 8015),
    ('influxdb.enabled', 'bool', False),
    ('influxdb.host', 'string', 'localhost'),
    ('influxdb.port', 'int', 8086),
    ('influxdb.db_name', 'string', 'metrics'),
    ('network_interface_blacklist', 'list', []),
    ('disk_monitor', 'list', []),
    ('df.path_ignore', 'list', []),
    ('df.host_mount_point', 'string', '/does-no-exists'),
    ('thresholds', 'dict', {}),
    ('kubernetes.nodename', 'string', None),
    ('kubernetes.enabled', 'bool', False),
    ('distribution', 'string', None),
    ('jmx.enabled', 'bool', True),
    (
        'jmxtrans.config_file',
        'string',
        '/var/lib/jmxtrans/bleemeo-generated.json'
    ),
]


class Config:
    """
    Work exacly like a normal dict, but "get" method known about sub-dict

    Also add "set" method that known about sub-dict.
    """
    def __init__(self, initial_dict=None):
        """ init function of Config class """
        if initial_dict is None:
            self._internal_dict = {}
        else:
            self._internal_dict = initial_dict

    def __getitem__(self, key):
        """ If the name contains separator ('.'), it will search in sub-dict.

            Example, if tou config is {'category': {'value': 5}}, then
            config['category.value'] wil return 5
        """
        current = self._internal_dict
        for path in key.split('.'):
            if path not in current:
                raise KeyError("{} is not a valid key in config".format(key))
            current = current[path]
        return current

    def __setitem__(self, key, value):
        """ If name contains separator ("." by default), it will search
            in sub-dict.

            Example, set(category.value, 5) write result in
            self['category']['value'] = 5.
            It does create intermediary dict as needed (in your example,
            self['category'] = {} if not already an dict).
        """
        current = self._internal_dict
        splitted_key = key.split('.')
        (paths, last_key) = (splitted_key[:-1], splitted_key[-1])
        for path in paths:
            if not isinstance(current.get(path), dict):
                current[path] = {}
            current = current[path]
        current[last_key] = value

    def __delitem__(self, key):
        """ If name name contains separator ("." by default), it will search
            in sub-dict.

            Example, del config["category.value"] will result in
            del self['category']['value'].
            It does NOT delete empty parent.
        """
        current = self._internal_dict
        splitted_key = key.split('.')
        (paths, last_key) = (splitted_key[:-1], splitted_key[-1])
        for path in paths:
            current = current[path]
        del current[last_key]

    def merge(self, source):
        if isinstance(source, Config):
            self._internal_dict = merge_dict(
                self._internal_dict,
                source._internal_dict  # pylint: disable=W0212
            )
        return self


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
            conf_name[len('agent.'):].replace('.', '_').upper()
        )
    elif conf_name.startswith('bleemeo.'):
        env_name.append(
            base +
            conf_name[len('bleemeo.'):].replace('.', '_').upper()
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


def load_default_config():
    """ Initialization of the default configuration """
    default_config = Config()
    for(conf_name, conf_type, conf_value) in CONFIG_VARS:
        if conf_type == 'list':
            default_config[conf_name] = list(conf_value)
        elif conf_type == 'dict':
            default_config[conf_name] = dict(conf_value)
        else:
            default_config[conf_name] = conf_value
    return default_config


def load_config(paths=None):
    """ Load configuration from given paths (a list) and return a ConfigParser

        If paths is not provided, use default value (PATH, see doc from module)
    """
    if paths is None and os.name == 'nt':
        paths = WINDOWS_PATHS
    elif paths is None:
        paths = PATHS

    default_config = {}
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
    final_config = Config(functools.reduce(merge_dict, configs))

    # overload of the final configuration by the environnement variables
    for (conf_name, conf_type, _conf_value) in CONFIG_VARS:
        env_names = convert_conf_name(conf_name)
        for env_name in env_names:
            if env_name in os.environ:
                if conf_type in ['dict', 'list']:
                    errors.append(
                        'Update %s from environment variable is not supported'
                        % (env_name)
                    )
                    continue
                try:
                    value = convert_type(os.environ[env_name], conf_type)
                except ValueError as exc:
                    errors.append(
                        'Bad environ variable %s: %s' % (env_name, exc)
                    )
                    continue
                final_config[conf_name] = value

    return final_config, errors


def load_config_with_default(paths=None):
    """ Merge the default config with the config from load_config"""
    (final_config, errors) = load_config(paths)
    default_config = load_default_config()
    return default_config.merge(final_config), errors


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
