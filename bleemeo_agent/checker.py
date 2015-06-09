import itertools
import json
import logging
import random
import select
import sched
import shlex
import socket
import threading
import time

import bleemeo_agent.util


# Must match nagios return code
STATUS_GOOD = 0
STATUS_WARNING = 1
STATUS_CRITICAL = 2
STATUS_UNKNOWN = 3

STATUS_NAME = {
    STATUS_GOOD: 'GOOD',
    STATUS_WARNING: 'WARNING',
    STATUS_CRITICAL: 'CRITICAL',
    STATUS_UNKNOWN: 'UNKONW',
}


class Checker(threading.Thread):

    def __init__(self, agent):
        super(Checker, self).__init__()
        self.agent = agent

        self.scheduler = sched.scheduler(time.time, time.sleep)
        self.checks = []

    def run(self):

        if len(self.agent.plugins_v1_mgr.names()) == 0:
            logging.debug(
                'No plugins loaded. Means no check, stop checker thread')
            return

        checks = self.agent.plugins_v1_mgr.map_method('list_checks')

        # list_checks return a list. So checks is a list of list :/
        # itertools.chain is used to "flatten" the list
        for (name, check_command, tcp_port) in itertools.chain(*checks):
            check = Check(self, name, check_command, tcp_port)
            self.checks.append(check)

        try:
            self.periodic_check()
            self.scheduler.run()
        except StopIteration:
            pass

    def periodic_check(self):
        """ Run few periodic check:

            * that agent is not being terminated
            * that all TCP socket are still openned
            * status of "faked failure"
        """

        if self.agent.is_terminating.is_set():
            raise StopIteration

        now = time.time()
        all_sockets = {}

        for check in self.checks:
            if check.tcp_socket is not None:
                all_sockets[check.tcp_socket] = check

            if (check.fake_failure_until
                    and check.fake_failure_until < now):
                check.fake_failure_stop()

        (rlist, _, _) = select.select(all_sockets.keys(), [], [], 0)
        for s in rlist:
            all_sockets[s].check_socket()

        self.scheduler.enter(3, 2, self.periodic_check, ())


class Check:
    def __init__(self, checker, name, check_command, tcp_port):

        self.name = name
        self.check_command = check_command
        self.tcp_port = tcp_port
        self.checker = checker

        self.tcp_socket = None
        self.last_run = time.time()
        self.current_event = None
        self.fake_failure_until = None

        # hard/soft "name" taken from nagios for it's 4-tries before alert
        self.hard_status = STATUS_GOOD
        self.soft_status = STATUS_GOOD
        self.soft_status_try = 4

        self.reschedule(initial=True)
        self.open_socket()

    def reschedule(self, initial=False):
        """ (re-)schedule this check

            If initial is True, it's the first schedule (in this case use a
            random delay).
        """
        if self.soft_status == STATUS_GOOD or self.soft_status_try >= 4:
            delay = 60 * 5
        else:
            delay = 60

        if initial:
            # During startup, schedule all check to be run withing the
            # first 2 minutes:
            # * between 10 seconds and 1 minutes : all check without TCP ports
            # * between 1 and 2 minutes : check with TCP ports (we will
            #   open the socket immediatly, so for them if service is down
            #   it should be detected quickly).
            if self.tcp_port is None:
                delay = random.randint(10, 60)
            else:
                delay = random.randint(60, 120)

        self.current_event = self.checker.scheduler.enter(
            delay,
            1,
            self.run_check,
            (),
        )

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
            if self.current_event is not None:
                self.checker.scheduler.cancel(self.current_event)
                self.current_event = None
            self.run_check()

    def check_socket(self):
        """ Called when socket is "readable". When a socket is closed,
            it became "readable".
        """
        # this call can NOT block, this is called when socket is readable
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
            'check %s: running command: %s', self.name, self.check_command)
        (return_code, output) = bleemeo_agent.util.run_command_timeout(
            shlex.split(self.check_command))

        if return_code > STATUS_UNKNOWN:
            return_code = STATUS_UNKNOWN

        # when status goes GOOD, always go to good immediatly
        # for all other case, we need to have 4 tries before moving from
        # soft-status to hard-status. We generate alert when hard-status
        # change.
        if return_code == STATUS_GOOD:
            self.soft_status_try = 4
        else:
            if self.soft_status != return_code:
                self.soft_status_try = 1
            elif self.soft_status_try < 4:
                self.soft_status_try += 1

            logging.info(
                'check %s: test is %s (soft altert %s/4)',
                self.name, STATUS_NAME[return_code], self.soft_status_try)

        self.soft_status = return_code

        if self.soft_status != self.hard_status and self.soft_status_try >= 4:
            self.hard_status = self.soft_status
            self.alert()

        if self.soft_status != STATUS_GOOD and self.tcp_socket is not None:
            self.tcp_socket.close()
            self.tcp_socket = None

        if (self.soft_status == STATUS_GOOD
                and self.tcp_port is not None
                and self.tcp_socket is None):
            self.checker.scheduler.enter(5, 1, self.open_socket, ())

        self.reschedule()

    def fake_failure_start(self):
        self.fake_failure_until = time.time() + 900
        self.alert(faked_status=STATUS_CRITICAL)

    def fake_failure_stop(self):
        self.fake_failure_until = None
        self.alert(faked_status=STATUS_GOOD)

    def alert(self, faked_status=None):
        message = 'check %s: alert, test is %s'
        if faked_status is not None:
            status = faked_status
            message = '[faked]' + message
        else:
            status = self.hard_status

        logging.warning(
            message,
            self.name, STATUS_NAME[status])
        self.checker.agent.mqtt_connector.publish(
            'api/v1/agent/alert/POST',
            json.dumps({
                'timestamp': time.time(),
                'check': self.name,
                'status': status,
                'fake': faked_status is not None,
            }),
        )
