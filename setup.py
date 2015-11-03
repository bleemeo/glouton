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
        'docker-py',
        'influxdb > 2.6.0',
        'paho-mqtt',
        'flask',
        'psutil',
        'requests',
        'six',
        'pyyaml',
    ],
    scripts=(
        'bin/bleemeo-agent',
    )
)
