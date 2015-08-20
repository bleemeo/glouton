import datetime
import itertools
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
    STATUS_UNKNOWN: 'unkonw',
}


def initialize_checks(core):
    if len(core.plugins_v1_mgr.names()) == 0:
        logging.debug(
            'No plugins loaded. Initialization of checks skipped')
        return

    checks = core.plugins_v1_mgr.map_method('list_checks')

    # list_checks return a list. So checks is a list of list :/
    # itertools.chain is used to "flatten" the list
    for (name, descr, check_command, tcp_port) in itertools.chain(*checks):
        check = Check(core, name, descr, check_command, tcp_port)
        core.checks.append(check)


def periodic_check(core):
    """ Run few periodic check:

        * that all TCP socket are still openned
    """
    all_sockets = {}

    for check in core.checks:
        if check.tcp_socket is not None:
            all_sockets[check.tcp_socket] = check

    (rlist, _, _) = select.select(all_sockets.keys(), [], [], 0)
    for s in rlist:
        all_sockets[s].check_socket()


class Check:
    def __init__(self, core, short_name, description, check_command, tcp_port):

        logging.debug(
            'Created new check with name=%s, check_command=%s, tcp_port=%s',
            short_name, check_command, tcp_port)
        self.short_name = short_name
        self.description = description
        self.check_command = check_command
        self.tcp_port = tcp_port
        self.core = core

        self.tcp_socket = None
        self.last_run = time.time()

        # During startup, schedule all check to be run within the
        # first minute:
        # * between 10 seconds and 30 seconds : all check without TCP ports
        # * between 30 seconds and 1 minute : check with TCP ports (we will
        #   open the socket immediatly, so for them if service is down
        #   it should be detected quickly).
        if self.tcp_port is None:
            sched_delta = datetime.timedelta(seconds=random.randint(10, 30))
        else:
            sched_delta = datetime.timedelta(seconds=random.randint(30, 60))

        self.current_job = self.core.scheduler.add_job(
            self.run_check,
            next_run_time=datetime.datetime.now() + sched_delta,
            trigger='interval',
            seconds=60,
        )
        self.open_socket()

    def open_socket(self):
        if self.tcp_port is None:
            return

        if self.tcp_socket is not None:
            self.tcp_socket.close()
            self.tcp_socket = None

        self.tcp_socket = socket.socket()
        try:
            self.tcp_socket.connect(('127.0.0.1', self.tcp_port))
        except socket.error:
            self.tcp_socket.close()
            self.tcp_socket = None

        if self.tcp_socket is None:
            # open_socket failed, run check now
            logging.debug(
                'check %s: failed to open socket to %s',
                self.name, self.tcp_port)
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
                'check %s : connection to port %s closed',
                self.name, self.tcp_port)
            self.open_socket()

    def run_check(self):
        self.last_run = time.time()
        logging.debug(
            'check %s: running command: %s',
            self.short_name, self.check_command)
        (return_code, output) = bleemeo_agent.util.run_command_timeout(
            shlex.split(self.check_command))

        if return_code > STATUS_UNKNOWN:
            return_code = STATUS_UNKNOWN

        self.core.emit_metric({
            'measurement': 'check-%s' % self.short_name,
            'tags': {
                'status': STATUS_NAME[return_code],
            },
            'time': self.last_run,
            'value': return_code,
        })

        if return_code != STATUS_OK and self.tcp_socket is not None:
            self.tcp_socket.close()
            self.tcp_socket = None

        if (return_code == STATUS_OK
                and self.tcp_port is not None
                and self.tcp_socket is None):
            self.core.scheduler.add_job(
                self.open_socket,
                'date',
                run_date=datetime.dateime.now() + datetime.timedelta(seconds=5)
            )
