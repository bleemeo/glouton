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
        ],
    },
    # entry_points={
    #    'console_scripts': [
    #        'gnucash-convert = compta_tools.convert:main',
    #        'compta-list = compta_tools.script:list_account',
    #        'compta-cli = compta_tools.cli:main',
    #        'compta-shell = compta_tools.script:shell',
    #        'compta-export = compta_tools.script:export',
    #    ],
    # },
    scripts=(
        'bin/bleemeo-agent',
    )
)
