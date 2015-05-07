"""
Load configuration (using ConfigParser) from a "conf.d" folder.

Path to configuration are hardcoded, in this order:

* /etc/bleemeo/agent.conf
* /etc/bleemeo/agent.conf.d/*.conf
* etc/agent.conf
* etc/agent.conf.d/*.conf

"""


import ConfigParser
import glob
import io
import os
import pkgutil
import uuid

import bleemeo_agent.util


PATHS = [
    '/etc/bleemeo/agent.conf',
    '/etc/bleemeo/agent.conf.d',
    'etc/agent.conf',
    'etc/agent.conf.d'
]


def load_config(paths=None):
    """ Load configuration from given paths (a list) and return a ConfigParser

        If paths is not provided, use default value (PATH, see doc from module)
    """
    if paths is None:
        paths = PATHS

    default_config = pkgutil.get_data(
        'bleemeo_agent.resources', 'default.conf')

    config = ConfigParser.SafeConfigParser()
    config.readfp(io.BytesIO(default_config))
    config.read(config_files(paths))
    return config


def config_files(paths):
    files = []
    for path in paths:
        if os.path.isfile(path):
            files.append(path)
        elif os.path.isdir(path):
            files.extend(sorted(glob.glob(os.path.join(path, '*.conf'))))

    return files


def get_credentials(config):
    """ Load (or generate and save) credentials from credential_file.
    """
    filepath = config.get('agent', 'credential_file')
    if os.path.exists(filepath):
        with open(filepath) as fd:
            (login, password) = fd.readline().split()
    else:
        login = str(uuid.uuid4())
        password = bleemeo_agent.util.generate_password()
        with open(filepath, 'w') as fd:
            fd.write('%s %s\n' % (login, password))

    return (login, password)
