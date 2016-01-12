import datetime
import logging
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
    'apache': '/usr/lib/nagios/plugins/check_http -H %(address)s',
    'imap': '/usr/lib/nagios/plugins/check_imap -H %(address)s',
    'influxdb': '/usr/lib/nagios/plugins/check_http '
                '-H %(address)s -p %(port)s '
                '-u http://%(address)s:%(port)s/ping',
    'ntp': '/usr/lib/nagios/plugins/check_ntp_peer -H %(address)s',
    'openldap': '/usr/lib/nagios/plugins/check_ldap -H %(address)s -3 -b ""',
    'postgresql': '/usr/lib/nagios/plugins/check_pgsql '
                  '-H %(address)s -l %(user)s -p %(password)s',
    'rabbitmq': "/usr/lib/nagios/plugins/check_tcp -H %(address)s -p %(port)s "
                "-s 'PINGAMQP' -e AMQP",
    'redis': "/usr/lib/nagios/plugins/check_tcp -H %(address)s -p %(port)s "
             "-E -s 'PING\\n' -e +PONG",
    'memcached': "/usr/lib/nagios/plugins/check_tcp -H %(address)s "
                 "-p %(port)s -E -s 'version\\r\\n' -e VERSION",
    'smtp': '/usr/lib/nagios/plugins/check_smtp -H %(address)s',
    'squid': '/usr/lib/nagios/plugins/check_http '
             '-H %(address)s -p %(port)s -e HTTP',
    'zookeeper': "/usr/lib/nagios/plugins/check_tcp "
                 "-H %(address)s -p %(port)s -E -s 'ruok\n' -e imok",

}

DEFAULT_TCP_CHECK = (
    '/usr/lib/nagios/plugins/check_tcp -H %(address)s -p %(port)s'
)


# global variable with all checks created
CHECKS = []


def update_checks(core):
    global CHECKS
    for check in CHECKS:
        check.stop()

    CHECKS = []

    for key, service_info in core.discovered_services.items():
        (service_name, instance) = key
        try:
            new_check = Check(
                core,
                service_name,
                instance,
                service_info,
            )
            CHECKS.append(new_check)
        except NotImplementedError:
            logging.debug(
                'No check exists for service %s', service_info['service']
            )
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
    def __init__(self, core, service_name, instance, service_info):

        # Safe because it do not contains password, so it could be logged
        self.check_command_safe = NAGIOS_CHECKS.get(service_name)
        if (service_info.get('password') is None
                and service_name in ('mysql', 'postgresql')):
            # For those check, if password is not set the dedicated check
            # will fail.
            self.check_command_safe = None

        self.service = service_name
        self.instance = instance
        self.address = service_info['address']
        self.core = core

        if service_info.get('protocol') == socket.IPPROTO_TCP:
            self.tcp_port = service_info['port']
            if self.check_command_safe is None:
                self.check_command_safe = DEFAULT_TCP_CHECK
        else:
            self.tcp_port = None

        if self.check_command_safe is None:
            raise NotImplementedError("No check for this service")
        self.check_command = self.check_command_safe % service_info

        logging.debug(
            'Created new check for service %s (on %s)',
            self.service,
            self.instance,
        )

        self.tcp_socket = None
        self.last_run = time.time()

        self.current_job = self.core.scheduler.add_interval_job(
            self.run_check,
            start_date=datetime.datetime.now() + datetime.timedelta(seconds=1),
            seconds=60,
        )
        self.open_socket_job = None

    def open_socket(self):
        if self.tcp_port is None:
            return

        if self.tcp_socket is not None:
            self.tcp_socket.close()
            self.tcp_socket = None

        self.tcp_socket = socket.socket()
        self.tcp_socket.settimeout(2)
        try:
            self.tcp_socket.connect((self.address, self.tcp_port))
        except socket.error:
            self.tcp_socket.close()
            self.tcp_socket = None

        if self.tcp_socket is None:
            # open_socket failed, run check now
            logging.debug(
                'check %s (on %s): failed to open socket to %s:%s',
                self.service, self.instance, self.address, self.tcp_port
            )
            # reschedule job to be run immediately
            self.core.scheduler.unschedule_job(self.current_job)
            self.current_job = self.core.scheduler.add_interval_job(
                self.run_check,
                start_date=(
                    datetime.datetime.now() +
                    datetime.timedelta(seconds=1)
                ),
                seconds=60,
            )

    def check_socket(self):
        """ Called when socket is "readable". When a socket is closed,
            it became "readable".
        """
        # this call can NOT block, it is called when socket is readable
        buffer = self.tcp_socket.recv(65536)
        if buffer == '':
            # this means connection was closed!
            logging.debug(
                'check %s (on %s) : connection to %s:%s closed',
                self.service, self.instance, self.address, self.tcp_port
            )
            self.open_socket()

    def run_check(self):
        self.last_run = time.time()
        (return_code, output) = bleemeo_agent.util.run_command_timeout(
            shlex.split(self.check_command))

        output = output.decode('utf-8', 'ignore')

        logging.debug(
            'check %s (on %s): return code is %s for command %s',
            self.service, self.instance, return_code, self.check_command_safe
        )

        if return_code > STATUS_UNKNOWN or return_code < 0:
            return_code = STATUS_UNKNOWN

        metric = {
            'measurement': '%s_status' % self.service,
            'status': STATUS_NAME[return_code],
            'service': self.service,
            'time': self.last_run,
            'value': float(return_code),
            'check_output': output,
        }
        if self.instance is not None:
            metric['item'] = self.instance
        self.core.emit_metric(metric)

        if return_code != STATUS_OK and self.tcp_socket is not None:
            self.tcp_socket.close()
            self.tcp_socket = None

        if (return_code == STATUS_OK
                and self.tcp_port is not None
                and self.tcp_socket is None):
            self.open_socket_job = self.core.scheduler.add_date_job(
                self.open_socket,
                date=(
                    datetime.datetime.now() + datetime.timedelta(seconds=5)
                ),
            )

    def stop(self):
        """ Unschedule this check
        """
        logging.debug('Stoping check %s (on %s)', self.service, self.instance)
        try:
            self.core.scheduler.unschedule_job(self.open_socket_job)
        except KeyError:
            logging.debug(
                'Job for check %s (on %s) was already unscheduled',
                self.service, self.instance
            )
        try:
            self.core.scheduler.unschedule_job(self.current_job)
        except KeyError:
            logging.debug(
                'Job for check %s (on %s) was already unscheduled',
                self.service, self.instance
            )
