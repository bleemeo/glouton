import datetime
import imaplib
import logging
import select
import smtplib
import socket
import struct
import time

import requests
from six.moves.urllib import parse as urllib_parse


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


CHECKS_INFO = {
    'mysql': {
        'type': 'tcp',
    },
    'apache': {
        'type': 'http',
    },
    'dovecot': {
        'type': 'imap',
    },
    'influxdb': {
        'type': 'http',
        'url': '/ping'
    },
    'ntp': {
        'type': 'ntp',
    },
    'openldap': {
        'type': 'tcp',
    },
    'postgresql': {
        'type': 'tcp',
    },
    'rabbitmq': {
        'type': 'tcp',
        'send': 'PINGAMQP',
        'expect': 'AMQP',
    },
    'redis': {
        'type': 'tcp',
        'send': 'PING\n',
        'expect': '+PONG',
    },
    'memcached': {
        'type': 'tcp',
        'send': 'version\r\n',
        'expect': 'VERSION',
    },
    'nginx': {
        'type': 'http',
    },
    'postfix': {
        'type': 'smtp',
    },
    'exim': {
        'type': 'smtp',
    },
    'squid': {
        'type': 'http',
        '4xx_is_ok': True,
    },
    'varnish': {
        'type': 'tcp',
        'send': 'ping\n',
        'expect': 'PONG'
    },
    'zookeeper': {
        'type': 'tcp',
        'send': 'ruok\n',
        'expect': 'imok',
    },
}


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
        self.check_info = CHECKS_INFO.get(service_name)

        if (service_info.get('password') is None
                and service_name in ('mysql', 'postgresql')):
            # For those check, if password is not set the dedicated check
            # will fail.
            self.check_info = None

        self.service = service_name
        self.instance = instance
        self.service_info = service_info
        self.address = service_info['address']
        self.core = core

        self.port = service_info.get('port')
        self.protocol = service_info.get('protocol')
        if self.protocol == socket.IPPROTO_TCP and self.check_info is None:
            self.check_info = {'type': 'tcp'}

        if self.check_info is None:
            raise NotImplementedError("No check for this service")

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
        if self.port is None or self.protocol != socket.IPPROTO_TCP:
            return

        if self.tcp_socket is not None:
            self.tcp_socket.close()
            self.tcp_socket = None

        self.tcp_socket = socket.socket()
        self.tcp_socket.settimeout(2)
        try:
            self.tcp_socket.connect((self.address, self.port))
        except socket.error:
            self.tcp_socket.close()
            self.tcp_socket = None

        if self.tcp_socket is None:
            # open_socket failed, run check now
            logging.debug(
                'check %s (on %s): failed to open socket to %s:%s',
                self.service, self.instance, self.address, self.port
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
        try:
            buffer = self.tcp_socket.recv(65536)
        except socket.error:
            buffer = b''

        if buffer == b'':
            # this means connection was closed!
            logging.debug(
                'check %s (on %s) : connection to %s:%s closed',
                self.service, self.instance, self.address, self.port
            )
            self.open_socket()

    def run_check(self):  # noqa
        self.last_run = time.time()

        if self.address is None:
            # Address is None if this check is associated with a stopped
            # container. In such case none of our test could pass
            (return_code, output) = (
                STATUS_CRITICAL, 'Container stopped: connection refused'
            )
        elif self.check_info['type'] == 'tcp':
            (return_code, output) = self.check_tcp()
        elif self.check_info['type'] == 'http':
            (return_code, output) = self.check_http()
        elif self.check_info['type'] == 'imap':
            (return_code, output) = self.check_imap()
        elif self.check_info['type'] == 'smtp':
            (return_code, output) = self.check_smtp()
        elif self.check_info['type'] == 'ntp':
            (return_code, output) = self.check_ntp()
        else:
            raise NotImplementedError(
                'Unknown check type %s' % self.check_info['type']
            )

        logging.debug(
            'check %s (on %s): return code is %s (output=%s)',
            self.service, self.instance, return_code, output,
        )

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
                and self.port is not None
                and self.protocol == socket.IPPROTO_TCP
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
                'Job open_socket for check %s (on %s) was already unscheduled',
                self.service, self.instance
            )
        self.core.scheduler.unschedule_job(self.current_job)

    def check_tcp_recv(self, sock, start):
        received = ''
        while not self.check_info['expect'] in received:
            try:
                tmp = sock.recv(4096)
            except socket.timeout:
                return (
                    STATUS_CRITICAL,
                    'Connection timed out after 10 seconds'
                )
            if tmp == b'':
                break
            received += tmp.decode('utf8', 'ignore')

        if self.check_info['expect'] not in received:
            if received == '':
                return (STATUS_CRITICAL, 'No data received from host')
            else:
                return (
                    STATUS_CRITICAL,
                    'Unexpected response: %s' % received
                )

        sock.close()
        end = time.time()
        return (STATUS_OK, 'TCP OK - %.3f second response time' % (end-start))

    def check_tcp(self):
        start = time.time()
        sock = socket.socket()
        sock.settimeout(10)
        try:
            sock.connect((self.address, self.port))
        except socket.timeout:
            return (STATUS_CRITICAL, 'Connection timed out after 10 seconds')
        except socket.error:
            return (STATUS_CRITICAL, 'Connection refused')

        if self.check_info.get('send'):
            try:
                sock.send(self.check_info['send'].encode('utf8'))
            except socket.timeout:
                return (
                    STATUS_CRITICAL,
                    'Connection timed out after 10 seconds'
                )
            except socket.error:
                return (STATUS_CRITICAL, 'Connection closed too early')

        if self.check_info.get('expect'):
            return self.check_tcp_recv(sock, start)

        sock.close()
        end = time.time()
        return (STATUS_OK, 'TCP OK - %.3f second response time' % (end-start))

    def check_http(self):
        base_url = 'http://%s:%s' % (self.address, self.port)
        url = urllib_parse.urljoin(base_url, self.check_info.get('url', '/'))
        start = time.time()
        try:
            response = requests.get(url, timeout=10, allow_redirects=False)
        except requests.exceptions.ConnectTimeout:
            return (STATUS_CRITICAL, 'Connection timed out after 10 seconds')
        except requests.exceptions.RequestException:
            return (STATUS_CRITICAL, 'Connection refused')

        end = time.time()

        if response.status_code >= 500:
            return (
                STATUS_CRITICAL,
                'HTTP CRITICAL - http_code=%s / %.3f second response time' % (
                    response.status_code,
                    end-start,
                )
            )
        elif (response.status_code >= 400
                and not self.check_info.get('4xx_is_ok', False)):
            return (
                STATUS_WARNING,
                'HTTP WARN - status_code=%s / %.3f second response time' % (
                    response.status_code,
                    end-start,
                )
            )
        else:
            return (
                STATUS_OK,
                'HTTP OK - %.3f second response time' % (end-start)
            )

    def check_imap(self):
        start = time.time()

        try:
            client = IMAP4Timeout(self.address, self.port)
            client.noop()
            client.logout()
        except (imaplib.IMAP4.error, socket.error):
            return (
                STATUS_CRITICAL,
                'Unable to connect to IMAP server',
            )
        except socket.timeout:
            return (
                STATUS_CRITICAL,
                'Connection timed out after 10 seconds',
            )

        end = time.time()
        return (STATUS_OK, 'IMAP OK - %.3f second response time' % (end-start))

    def check_smtp(self):
        start = time.time()

        try:
            client = smtplib.SMTP(self.address, self.port, timeout=10)
            client.noop()
            client.quit()
        except (smtplib.SMTPException, socket.error):
            return (
                STATUS_CRITICAL,
                'Unable to connect to SMTP server',
            )
        except socket.timeout:
            return (
                STATUS_CRITICAL,
                'Connection timed out after 10 seconds',
            )

        end = time.time()
        return (STATUS_OK, 'SMTP OK - %.3f second response time' % (end-start))

    def check_ntp(self):
        # Ntp use 1900-01-01 00:00:00 as epoc.
        # Since Unix use 1970-01-01 as epoc, we have this delta
        NTP_DELTA = 2208988800

        start = time.time()

        client = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        client.settimeout(10)

        msg = b'\x1b' + 47 * b'\0'
        try:
            client.sendto(msg, (self.address, self.port))
            msg, address = client.recvfrom(1024)
        except socket.timeout:
            return (STATUS_CRITICAL, 'Connection timed out after 10 seconds')

        unpacked = struct.unpack("!BBBB11I", msg)
        stratum = unpacked[1]
        server_time = unpacked[11] - NTP_DELTA

        end = time.time()

        if stratum == 0 or stratum == 16:
            return (STATUS_CRITICAL, 'NTP server not (yet) synchronized')
        elif abs(server_time - end) > 10:
            return (STATUS_CRITICAL, 'Local time and NTP time does not match')
        else:
            return (
                STATUS_OK, 'NTP OK - %.3f second response time' % (end-start)
            )


class IMAP4Timeout(imaplib.IMAP4):
    """ IMAP4 with timeout of 10 second
    """

    def open(self, host='', port=imaplib.IMAP4_PORT):
        self.host = host
        self.port = port
        self.sock = socket.create_connection((host, port), timeout=10)
        self.file = self.sock.makefile('rb')
