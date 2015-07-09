"""
Load configuration (using ConfigParser) from a "conf.d" folder.

Path to configuration are hardcoded, in this order:

* /etc/bleemeo/agent.conf
* /etc/bleemeo/agent.conf.d/*.conf
* etc/agent.conf
* etc/agent.conf.d/*.conf

"""


import glob
import io
import os
import pkgutil

from six.moves import configparser


PATHS = [
    '/etc/bleemeo/agent.conf',
    '/etc/bleemeo/agent.conf.d',
    'etc/agent.conf',
    'etc/agent.conf.d'
]


class FallbackConfigParser:
    """ A wrapper around ConfigParser that allow "get" method with a fallback
        default value.
    """

    def __init__(self, config):
        self._config = config

    def has_option(self, section, option):
        return self._config.has_option(section, option)

    def get(self, section, option, default=None):
        if self._config.has_option(section, option):
            return self._config.get(section, option)
        else:
            return default


def load_config(paths=None):
    """ Load configuration from given paths (a list) and return a ConfigParser

        If paths is not provided, use default value (PATH, see doc from module)
    """
    if paths is None:
        paths = PATHS

    default_config = pkgutil.get_data(
        'bleemeo_agent.resources', 'default.conf').decode('utf8')

    config = configparser.SafeConfigParser()
    config.readfp(io.StringIO(default_config))
    config.read(config_files(paths))
    return FallbackConfigParser(config)


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
