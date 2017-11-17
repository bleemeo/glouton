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
import win32console
import win32security
import win32service
import win32serviceutil

import bleemeo_agent.core
import bleemeo_agent.util


def run_process(cmd):
    """ Run a command and return a couple (return_code, output)

        Return code is -127 is command could not be started
    """
    try:
        result = subprocess.run(
            cmd,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
        )
        return (result.returncode, decode_console_output(result.stdout))
    except OSError:
        return (-127, "Failed to start command")


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


def decode_console_output(data):
    """ Decode output from a console program.

        Windows don't use UTF-8 for console output, but something like cp850.
    """
    try:
        return data.decode('cp%s' % win32console.GetConsoleCP())
    except Exception:
        return data.decode('utf-8', errors='ignore')


def windows_preremove(args):
    """ Stop and remove Windows service for Bleemoe agent and Telegraf
    """
    windows_installer_logger()
    logging.info('##### Pre-remove started')
    (return_code, output) = run_process(
        ["net", "stop", "telegraf"],
    )
    logging.info(
        'Stopping telegraf service returned %d:\n%s',
        return_code,
        output,
    )

    telegraf_binary = bleemeo_agent.util.windows_telegraf_path()
    (return_code, output) = run_process(
        [
            telegraf_binary,
            '-config',
            r'C:\ProgramData\Bleemeo\etc\telegraf\telegraf.conf',
            '-config-directory',
            r'C:\ProgramData\Bleemeo\etc\telegraf\telegraf.d',
            '--service',
            'uninstall'
        ],
    )
    logging.info(
        'Uninstallating telegraf service returned %d:\n%s',
        return_code,
        output,
    )

    (return_code, output) = run_process(
        ["net", "stop", "bleemeo-agent"],
    )
    logging.info(
        'Stopping bleemeo-agent service returned %d:\n%s',
        return_code,
        output,
    )
    (return_code, output) = run_process(
        [
            sys.executable,
            sys.argv[0],
            'remove',
        ],
    )
    logging.info(
        'Uninstallating bleemeo-agent service returned %d:\n%s',
        return_code,
        output,
    )
    logging.info('##### Pre-remove ended')


def windows_preinstall(args):
    """ Stop Telegraf and Bleemeo Agent
    """
    windows_installer_logger()
    logging.info('#### Pre-install started')

    pathlib.Path(r'C:\ProgramData\Bleemeo\upgrade').touch()

    (return_code, output) = run_process(
        ["net", "stop", "telegraf"],
    )
    logging.info(
        'Stopping telegraf service returned %d:\n%s',
        return_code,
        output,
    )
    (return_code, output) = run_process(
        ["net", "stop", "bleemeo-agent"],
    )
    logging.info(
        'Stopping bleemeo-agent service returned %d:\n%s',
        return_code,
        output,
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

    os.makedirs(
        r'C:\ProgramData\Bleemeo\etc\telegraf\telegraf.d', exist_ok=True
    )

    # Uninstall telegraf service just before re-installing it. This is
    # needed to update service information (for example the -config-directory
    # value)
    telegraf_binary = bleemeo_agent.util.windows_telegraf_path()
    (return_code, output) = run_process(
        [
            telegraf_binary,
            '-config',
            r'C:\ProgramData\Bleemeo\etc\telegraf\telegraf.conf',
            '-config-directory',
            r'C:\ProgramData\Bleemeo\etc\telegraf\telegraf.d',
            '--service',
            'uninstall'
        ],
    )
    logging.info(
        'Uninstallating telegraf service returned %d:\n%s',
        return_code,
        output,
    )
    (return_code, output) = run_process(
        [
            telegraf_binary,
            '-config',
            r'C:\ProgramData\Bleemeo\etc\telegraf\telegraf.conf',
            '-config-directory',
            r'C:\ProgramData\Bleemeo\etc\telegraf\telegraf.d',
            '--service',
            'install'
        ],
    )
    logging.info(
        'Installating telegraf service returned %d:\n%s',
        return_code,
        output,
    )

    # Allow Local service to stop/start telegraf service
    hscm = win32service.OpenSCManager(
        None,
        None,
        win32service.SC_MANAGER_ALL_ACCESS
    )
    try:
        hs = win32service.OpenService(
            hscm, "telegraf", win32service.SERVICE_ALL_ACCESS
        )
        try:
            sid = win32security.LookupAccountName(
                None,
                r"NT AUTHORITY\LocalService",
            )[0]
            sec_descriptor = win32service.QueryServiceObjectSecurity(
                hs,
                win32security.DACL_SECURITY_INFORMATION
            )
            sec_dacl = sec_descriptor.GetSecurityDescriptorDacl()
            sec_dacl.AddAccessAllowedAce(
                win32security.ACL_REVISION,
                win32service.SERVICE_START | win32service.SERVICE_STOP,
                sid
            )
            sec_descriptor.SetSecurityDescriptorDacl(1, sec_dacl, 0)
            win32service.SetServiceObjectSecurity(
                hs,
                win32security.DACL_SECURITY_INFORMATION,
                sec_descriptor,
            )
        except Exception:
            logging.info(
                'Failed to change permission on Telegraf service',
                exc_info=True,
            )
        finally:
            win32service.CloseServiceHandle(hs)
    except Exception:
        logging.info('Failed to find Telegraf service', exc_info=True)
    finally:
        win32service.CloseServiceHandle(hscm)

    (return_code, output) = run_process(
        ["net", "start", "telegraf"],
    )
    logging.info(
        'Starting telegraf service returned %d:\n%s',
        return_code,
        output,
    )
    (return_code, output) = run_process(
        [
            sys.executable,
            sys.argv[0],
            '--username', r'NT AUTHORITY\LocalService',
            '--startup', 'auto',
            'install',
        ],
    )
    logging.info(
        'Installating bleemeo-agent service returned %d:\n%s',
        return_code,
        output,
    )
    (return_code, output) = run_process(
        ["net", "start", "bleemeo-agent"],
    )
    logging.info(
        'Starting bleemeo-agent service returned %d:\n%s',
        return_code,
        output,
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
        except Exception:
            logging.error('Unhandled error:', exc_info=True)
            raise
        finally:
            logging.info('Agent stopped')

    def SvcStop(self):
        self.ReportServiceStatus(win32service.SERVICE_STOP_PENDING)
        self.core.is_terminating.set()
