#!/usr/bin/python

from setuptools import find_packages, setup

import bleemeo_agent


setup(
    name="bleemeo-agent",
    version=bleemeo_agent.__version__,
    url='https://bleemeo.com',
    license='GPL3',
    description="Agent for Bleemeo",
    author='Bleemeo',
    packages=find_packages(),
    include_package_data=True,
    entry_points={
        'bleemeo_agent.plugins_v1': [
            'apache = bleemeo_agent.plugins.apache:Apache',
            'docker = bleemeo_agent.plugins.docker:Docker',
            'mysql = bleemeo_agent.plugins.mysql:MySQL',
            'nginx = bleemeo_agent.plugins.nginx:Nginx',
            'ntpd = bleemeo_agent.plugins.ntpd:Ntpd',
            'redis = bleemeo_agent.plugins.redis:Redis',
        ],
    },
    scripts=(
        'bin/bleemeo-agent',
    )
)
