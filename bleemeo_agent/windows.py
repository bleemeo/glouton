#
#  Copyright 2017 Bleemeo
#
#  bleemeo.com an infrastructure monitoring solution in the Cloud
#
#   Licensed under the Apache License, Version 2.0 (the "License");
#   you may not use this file except in compliance with the License.
#   You may obtain a copy of the License at
#
#       http://www.apache.org/licenses/LICENSE-2.0
#
#   Unless required by applicable law or agreed to in writing, software
#   distributed under the License is distributed on an "AS IS" BASIS,
#   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
#   See the License for the specific language governing permissions and
#   limitations under the License.
#

import argparse
import logging
import os
import pathlib
import subprocess
import sys

import servicemanager
import win32service
import win32serviceutil

import bleemeo_agent.core


def windows_main():
    # Windows script may be called for 4 different reason:
    # * post/pre install/remove script
    # * To run the service
    # * To run Bleemeo agent from the console
    # * To install/remove the service
    if ('--post-install' in sys.argv
            or '--pre-install' in sys.argv
            or '--pre-remove' in sys.argv):

        parser = argparse.ArgumentParser(description='Bleemeo agent')
        # Internal flag, used by installer
        parser.add_argument(
            '--post-install',
            default=False,
            action='store_true',
            help=argparse.SUPPRESS,
        )
        parser.add_argument(
            '--pre-install',
            default=False,
            action='store_true',
            help=argparse.SUPPRESS,
        )
        parser.add_argument(
            '--pre-remove',
            default=False,
            action='store_true',
            help=argparse.SUPPRESS,
        )
        parser.add_argument(
            '--account',
            help=argparse.SUPPRESS,
        )
        parser.add_argument(
            '--registration',
            help=argparse.SUPPRESS,
        )

        args = parser.parse_args()
        if args.post_install:
            windows_postinstall(args)
        elif args.pre_install:
            windows_preinstall(args)
        elif args.pre_remove:
            windows_preremove(args)
    elif len(sys.argv) == 2 and sys.argv[1] == '--run-service':
        servicemanager.Initialize()
        servicemanager.PrepareToHostSingle(BleemeoAgentService)
        servicemanager.StartServiceCtrlDispatcher()
    elif len(sys.argv) == 1:
        # no argument, run bleemeo-agent on console
        try:
            core = bleemeo_agent.core.Core()
            core.run()
        finally:
            logging.info('Agent stopped')
    else:
        win32serviceutil.HandleCommandLine(BleemeoAgentService)


def windows_installer_logger():
    # Redirecting stdout/stderr is usefull if program terminate due to
    # unexcepted error
    fd = open(r'C:\ProgramData\Bleemeo\log\install.log', 'a')
    sys.stdout = fd
    sys.stderr = fd

    logging.basicConfig(
        filename=r'C:\ProgramData\Bleemeo\log\install.log',
        level=logging.INFO,
        format='%(asctime)s - %(name)s - %(levelname)s - %(message)s',
    )


def windows_preremove(args):
    """ Stop and remove Windows service for Bleemoe agent and Telegraf
    """
    windows_installer_logger()
    logging.info('##### Pre-remove started')
    result = subprocess.run(
        ["net", "stop", "telegraf"],
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
    )
    logging.info(
        'Stopping telegraf service returned %d:\n%s',
        result.returncode,
        result.stdout.decode('utf8'),
    )

    bleemeo_package_dir = os.path.dirname(__file__)
    # bleemeo_agent package is located at $INSTDIR\pkgs\bleemeo_agent
    install_dir = os.path.dirname(os.path.dirname(bleemeo_package_dir))
    telegraf_binary = os.path.join(install_dir, 'telegraf.exe')
    result = subprocess.run(
        [
            telegraf_binary,
            '-config',
            r'C:\ProgramData\Bleemeo\etc\telegraf\telegraf.conf',
            '-config-directory',
            r'C:\ProgramData\Bleemeo\etc\telegraf\telegraf.d',
            '--service',
            'uninstall'
        ],
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
    )
    logging.info(
        'Uninstallating telegraf service returned %d:\n%s',
        result.returncode,
        result.stdout.decode('utf8'),
    )

    result = subprocess.run(
        ["net", "stop", "bleemeo-agent"],
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
    )
    logging.info(
        'Stopping bleemeo-agent service returned %d:\n%s',
        result.returncode,
        result.stdout.decode('utf8'),
    )
    result = subprocess.run(
        [
            sys.executable,
            sys.argv[0],
            'remove',
        ],
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
    )
    logging.info(
        'Uninstallating bleemeo-agent service returned %d:\n%s',
        result.returncode,
        result.stdout.decode('utf8'),
    )
    logging.info('##### Pre-remove ended')


def windows_preinstall(args):
    """ Stop Telegraf and Bleemeo Agent
    """
    windows_installer_logger()
    logging.info('#### Pre-install started')

    pathlib.Path(r'C:\ProgramData\Bleemeo\upgrade').touch()

    result = subprocess.run(
        ["net", "stop", "telegraf"],
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
    )
    logging.info(
        'Stopping telegraf service returned %d:\n%s',
        result.returncode,
        result.stdout.decode('utf8'),
    )
    result = subprocess.run(
        ["net", "stop", "bleemeo-agent"],
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
    )
    logging.info(
        'Stopping bleemeo-agent service returned %d:\n%s',
        result.returncode,
        result.stdout.decode('utf8'),
    )
    logging.info('#### Pre-install ended')


def windows_postinstall(args):
    """ Install and start service for Bleemeo-agent and Telegraf
    """
    os.makedirs(
        r'C:\ProgramData\Bleemeo\log', exist_ok=True
    )
    windows_installer_logger()
    logging.info('#### Post-install started')
    config_dir = r'C:\ProgramData\Bleemeo\etc\agent.conf.d'

    config_file = os.path.join(config_dir, '30-install.conf')
    if not os.path.exists(config_file):
        with open(config_file, 'w') as fd:
            print("""
bleemeo:
    account_id: %s
    registration_key: %s
""" % (args.account, args.registration), file=fd)

    bleemeo_package_dir = os.path.dirname(__file__)
    # bleemeo_agent package is located at $INSTDIR\pkgs\bleemeo_agent
    install_dir = os.path.dirname(os.path.dirname(bleemeo_package_dir))

    os.makedirs(
        r'C:\ProgramData\Bleemeo\etc\telegraf\telegraf.d', exist_ok=True
    )

    telegraf_binary = os.path.join(install_dir, 'telegraf.exe')
    result = subprocess.run(
        [
            telegraf_binary,
            '-config',
            r'C:\ProgramData\Bleemeo\etc\telegraf\telegraf.conf',
            '-config-directory',
            r'C:\ProgramData\Bleemeo\etc\telegraf\telegraf.d',
            '--service',
            'install'
        ],
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
    )
    logging.info(
        'Installating telegraf service returned %d:\n%s',
        result.returncode,
        result.stdout.decode('utf8'),
    )
    result = subprocess.run(
        ["net", "start", "telegraf"],
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
    )
    logging.info(
        'Starting telegraf service returned %d:\n%s',
        result.returncode,
        result.stdout.decode('utf8'),
    )
    result = subprocess.run(
        [
            sys.executable,
            sys.argv[0],
            '--username', r'NT AUTHORITY\LocalService',
            '--startup', 'auto',
            'install',
        ],
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
    )
    logging.info(
        'Installating bleemeo-agent service returned %d:\n%s',
        result.returncode,
        result.stdout.decode('utf8'),
    )
    result = subprocess.run(
        ["net", "start", "bleemeo-agent"],
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
    )
    logging.info(
        'Starting bleemeo-agent service returned %d:\n%s',
        result.returncode,
        result.stdout.decode('utf8'),
    )

    # User running bleemeo-agent don't have permission to delete this file.
    # This file is only used during shutdown, so it's not an issue to delete
    # it here.
    try:
        pathlib.Path(r'C:\ProgramData\Bleemeo\upgrade').unlink()
    except OSError:
        pass
    logging.info('#### Post-install ended')


class BleemeoAgentService(win32serviceutil.ServiceFramework):
    _svc_name_ = "bleemeo-agent"
    _svc_display_name_ = "Bleemeo Agent"
    _svc_description_ = (
        "Bleemeo Agent is a solution of Monitoring as a Service. "
        "See https://bleemeo.com"
    )
    _exe_name_ = sys.executable
    _exe_args_ = '"%s" --run-service' % sys.argv[0]

    def __init__(self, args):
        win32serviceutil.ServiceFramework.__init__(self, args)
        self.core = bleemeo_agent.core.Core(run_as_windows_service=True)

    def SvcDoRun(self):
        servicemanager.LogMsg(
            servicemanager.EVENTLOG_INFORMATION_TYPE,
            servicemanager.PYS_SERVICE_STARTED,
            (self._svc_name_, '')
        )

        try:
            self.core.run()
        finally:
            logging.info('Agent stopped')

    def SvcStop(self):
        self.ReportServiceStatus(win32service.SERVICE_STOP_PENDING)
        self.core.is_terminating.set()
