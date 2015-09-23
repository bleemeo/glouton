import datetime
import logging
import random
import select
import shlex
import socket
import time

import bleemeo_agent.util


# Must match nagios return code
STATUS_OK = 0
STATUS_WARNING = 1
STATUS_CRITICAL = 2
STATUS_UNKNOWN = 3

STATUS_NAME = {
    STATUS_OK: 'ok',
    STATUS_WARNING: 'warning',
    STATUS_CRITICAL: 'critical',
    STATUS_UNKNOWN: 'unknown',
}


NAGIOS_CHECKS = {
    'mysql': "/usr/lib/nagios/plugins/check_mysql "
             "-u '%(user)s' -p '%(password)s' -H %(address)s",
    'apache': '/usr/lib/nagios/plugins/check_http -H %(address)s'
}

DEFAULT_CHECK = '/usr/lib/nagios/plugins/check_tcp -H %(address)s -p %(port)s'


# global variable with all checks created
CHECKS = []


def update_checks(core, old_discovered_services):
    global CHECKS
    for check in CHECKS:
        check.stop()

    CHECKS = []

    for service_info in core.discovered_services:
        try:
            new_check = Check(
                core,
                service_info,
            )
            CHECKS.append(new_check)
        except:
            logging.debug(
                'Failed to initialize check for service %s',
                service_info['service'],
                exc_info=True
            )


def periodic_check():
    """ Run few periodic check:

        * that all TCP socket are still openned
    """
    all_sockets = {}

    for check in CHECKS:
        if check.tcp_socket is not None:
            all_sockets[check.tcp_socket] = check

    (rlist, _, _) = select.select(all_sockets.keys(), [], [], 0)
    for s in rlist:
        all_sockets[s].check_socket()


class Check:
    def __init__(self, core, service_info):

        # Safe because it do not contains password, so it could be logged
        self.check_command_safe = NAGIOS_CHECKS.get(
            service_info['service'],
            DEFAULT_CHECK
        )
        self.service = service_info['service']
        self.instance = service_info['instance']
        self.check_command = self.check_command_safe % service_info
        self.tcp_address = service_info['address']
        self.tcp_port = service_info['port']
        self.core = core

        logging.debug(
            'Created new check for service %s (on %s)',
            self.service,
            self.instance,
        )

        self.tcp_socket = None
        self.last_run = time.time()

        # During startup, schedule all check to be run within the
        # first minute.
        sched_delta = datetime.timedelta(seconds=random.randint(5, 60))

        self.current_job = self.core.scheduler.add_job(
            self.run_check,
            next_run_time=datetime.datetime.now() + sched_delta,
            trigger='interval',
            seconds=60,
        )
        self.open_socket_job = self.core.scheduler.add_job(
            self.open_socket,
            'date',
            run_date=(
                datetime.datetime.now() + datetime.timedelta(seconds=5)
            ),
        )

    def open_socket(self):
        if self.tcp_port is None:
            return

        if self.tcp_socket is not None:
            self.tcp_socket.close()
            self.tcp_socket = None

        self.tcp_socket = socket.socket()
        self.tcp_socket.settimeout(2)
        try:
            self.tcp_socket.connect((self.tcp_address, self.tcp_port))
        except socket.error:
            self.tcp_socket.close()
            self.tcp_socket = None

        if self.tcp_socket is None:
            # open_socket failed, run check now
            logging.debug(
                'check %s (on %s): failed to open socket to %s',
                self.service, self.instance, self.tcp_port
            )
            # reschedule job to be run immediately
            self.current_job.modify(next_run_time=datetime.datetime.now())

    def check_socket(self):
        """ Called when socket is "readable". When a socket is closed,
            it became "readable".
        """
        # this call can NOT block, it is called when socket is readable
        buffer = self.tcp_socket.recv(65536)
        if buffer == '':
            # this means connection was closed!
            logging.debug(
                'check %s (on %s) : connection to port %s closed',
                self.service, self.instance, self.tcp_port
            )
            self.open_socket()

    def run_check(self):
        self.last_run = time.time()
        logging.debug(
            'check %s (on %s): running check command %s',
            self.service, self.instance, self.check_command_safe)
        (return_code, output) = bleemeo_agent.util.run_command_timeout(
            shlex.split(self.check_command))

        if return_code > STATUS_UNKNOWN:
            return_code = STATUS_UNKNOWN

        self.core.emit_metric({
            'measurement': 'check-%s' % self.service,
            'status': STATUS_NAME[return_code],
            'tag': self.instance,
            'service': self.service,
            'time': self.last_run,
            'value': return_code,
        })

        if return_code != STATUS_OK and self.tcp_socket is not None:
            self.tcp_socket.close()
            self.tcp_socket = None

        if (return_code == STATUS_OK
                and self.tcp_port is not None
                and self.tcp_socket is None):
            self.open_socket_job = self.core.scheduler.add_job(
                self.open_socket,
                'date',
                run_date=(
                    datetime.datetime.now() + datetime.timedelta(seconds=5)
                ),
            )

    def stop(self):
        """ Unschedule this check
        """
        logging.debug('Stoping check %s (on %s)', self.service, self.instance)
        self.open_socket_job.remove()
        self.current_job.remove()
