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
    STATUS_UNKNOWN: 'unkonw',
}


NAGIOS_CHECKS = {
    'mysql': "/usr/lib/nagios/plugins/check_mysql "
             "-u '%(user)s' -p '%(password)s' -H %(address)s",
    'apache': '/usr/lib/nagios/plugins/check_http -H %(address)s'
}

DEFAULT_CHECK = '/usr/lib/nagios/plugins/check_tcp -H %(address)s -p %(port)s'


# global variable with all checks created
CHECKS = {}


def update_checks(core, old_discovered_services):
    for service, config in core.discovered_services.items():
        if old_discovered_services.get(service) == config:
            # configuration didn't changed. Skip it
            continue

        try:
            check_command = NAGIOS_CHECKS.get(service, DEFAULT_CHECK)
            check_command = check_command % config
            new_check = Check(
                core,
                service,
                check_command,
                config['address'],
                config['port'],
            )
            old_check = CHECKS.get(service)
            if old_check is not None:
                old_check.stop()
            CHECKS[service] = new_check
        except:
            logging.debug(
                'Failed to initialize check for service %s',
                service,
                exc_info=True
            )

    old_services = set(CHECKS.keys())
    services = set(core.discovered_services.keys())
    for removed_service in (old_services - services):
        CHECKS[removed_service].stop()
        del CHECKS[removed_service]


def periodic_check():
    """ Run few periodic check:

        * that all TCP socket are still openned
    """
    all_sockets = {}

    for check in CHECKS.values():
        if check.tcp_socket is not None:
            all_sockets[check.tcp_socket] = check

    (rlist, _, _) = select.select(all_sockets.keys(), [], [], 0)
    for s in rlist:
        all_sockets[s].check_socket()


class Check:
    def __init__(self, core, name, check_command, tcp_address, tcp_port):

        logging.debug(
            'Created new check with name=%s, tcp_port=%s',
            name, tcp_port)
        self.name = name
        self.check_command = check_command
        self.tcp_address = tcp_address
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
            'check %s: running check command',
            self.name)
        (return_code, output) = bleemeo_agent.util.run_command_timeout(
            shlex.split(self.check_command))

        if return_code > STATUS_UNKNOWN:
            return_code = STATUS_UNKNOWN

        self.core.emit_metric({
            'measurement': 'check-%s' % self.name,
            'status': STATUS_NAME[return_code],
            'tag': None,
            'service': None,
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
        logging.debug('Stoping check %s', self.name)
        self.open_socket_job.remove()
        self.current_job.remove()
