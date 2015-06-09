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
import json
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


def get_generated_values(config):
    """ Load (or generate and save) some generated values.

        return a dictionary with:

        * login / password : used to authenticate on MQTT
        * secret_key : used for Flask session/cookie
    """
    filepath = config.get('agent', 'generated_values_file')
    file_change = False
    values = {}

    if os.path.exists(filepath):
        with open(filepath) as fd:
            values = json.load(fd)

    if 'login' not in values or 'password' not in values:
        values['login'] = str(uuid.uuid4())
        values['password'] = bleemeo_agent.util.generate_password()
        file_change = True

    if 'secret_key' not in values:
        values['secret_key'] = bleemeo_agent.util.generate_password()
        file_change = True

    if file_change:
        with open(filepath, 'w') as fd:
            json.dump(values, fd)

    return values
