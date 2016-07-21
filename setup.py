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
    install_requires=[
        'apscheduler < 3.0.0',
        'jinja2',
        'psutil >= 2.0.0',
        'requests',
        'six',
        'pyyaml',
    ],
    extras_require={
        'docker': ['docker-py'],
        'influxdb': ['influxdb > 2.6.0'],
        'bleemeo': ['paho-mqtt'],
        'sentry': ['raven'],
        'web': ['flask']
    },
    scripts=(
        'bin/bleemeo-agent',
        'bin/bleemeo-agent-gather-facts',
        'bin/bleemeo-netstat',
    ),
    data_files=[
        (
            'lib/bleemeo/',
            (
                'debian/bleemeo-dpkg-hook-postinvoke',
            ),
        )
    ],
)
