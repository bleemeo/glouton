#!/usr/bin/python

from setuptools import setup, find_packages

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
            'disk = bleemeo_agent.plugins.disk:Disk',
            'mysql = bleemeo_agent.plugins.mysql:MySQL',
            'ntpd = bleemeo_agent.plugins.ntpd:Ntpd',
            'redis = bleemeo_agent.plugins.redis:Redis',
        ],
    },
    scripts=(
        'bin/bleemeo-agent',
    )
)
