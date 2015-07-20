"""
Load configuration (in yaml) from a "conf.d" folder.

Path to configuration are hardcoded, in this order:

* /etc/bleemeo/agent.yml
* /etc/bleemeo/agent.conf.d/*.yml
* etc/agent.yml
* etc/agent.conf.d/*.yml

"""


import functools
import glob
import os

import yaml


PATHS = [
    '/etc/bleemeo/agent.yml',
    '/etc/bleemeo/agent.conf.d',
    'etc/agent.yml',
    'etc/agent.conf.d'
]


class Config(dict):
    """
    Work exacly like a normal dict, but "get" method known about sub-dict
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


def merge_dict(destination, source):
    """ Merge two dictionary (recursivly). destination is modified
    """
    for (key, value) in source.items():
        if (key in destination
                and isinstance(value, dict)
                and isinstance(destination[key], dict)):
            destination[key] = merge_dict(destination[key], value)
        else:
            destination[key] = value
    return destination


def load_config(paths=None):
    """ Load configuration from given paths (a list) and return a ConfigParser

        If paths is not provided, use default value (PATH, see doc from module)
    """
    if paths is None:
        paths = PATHS

    default_config = Config()

    configs = [default_config]
    for filepath in config_files(paths):
        with open(filepath) as fd:
            configs.append(yaml.load(fd))

    return functools.reduce(merge_dict, configs)


def config_files(paths):
    """ Return config files present in given paths.

        For each path, if:

        * it is a directory, return all *.yml files inside the directory
        * it is a file, return the path
        * no config file exists for the path, skip it

        So, if path is ['/etc/bleemeo/agent.yml', '/etc/bleemeo/agent.conf.d']
        you will get /etc/bleemeo/agent.yml (if it exists) and all
        existings *.yml under /etc/bleemeo/agent.conf.d
    """
    files = []
    for path in paths:
        if os.path.isfile(path):
            files.append(path)
        elif os.path.isdir(path):
            files.extend(sorted(glob.glob(os.path.join(path, '*.yml'))))

    return files
