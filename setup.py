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
    scripts=(
        'bin/bleemeo-agent',
    )
)
