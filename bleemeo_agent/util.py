#
#  Copyright 2015-2016 Bleemeo
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

import datetime
import json
import logging
import os
import random
import re
import shlex
import subprocess
import sys
import threading
import time

import jinja2
import psutil
import requests
from six.moves import urllib_parse

import bleemeo_agent


# With generate_password, taken from Django project
# Use the system PRNG if possible
try:
    random = random.SystemRandom()  # pylint: disable=invalid-name
except NotImplementedError:
    import warnings
    warnings.warn('A secure pseudo-random number generator is not available '
                  'on your system. Falling back to Mersenne Twister.')

try:
    import docker
except ImportError:
    docker = None


def decode_docker_top(docker_top):
    # pylint: disable=too-many-branches
    """ Return a list of process dict from docker_client.top()

        Result of docker_client.top() is not always the same. On boot2docker,
        on first boot docker will use ps from busybox which output only few
        column.

        In addition we first try with a "ps waux" and then with default
        ps ("ps -ef").

        The process dict is a dictonary with the same key as the one generated
        by get_top_info from psutil data. Field not found in Docker ps are
        omitted.

        All process will at least have pid, cmdline and name key.
    """
    result = []
    container_process = docker_top.get('Processes')

    user_index = None
    pid_index = None
    pcpu_index = None
    rss_index = None
    time_index = None
    cmdline_index = None
    stat_index = None
    for (index, name) in enumerate(docker_top.get('Titles', [])):
        if name == 'PID':
            pid_index = index
        elif name in ('CMD', 'COMMAND'):
            cmdline_index = index
        elif name in ('UID', 'USER'):
            user_index = index
        elif name == '%CPU':
            pcpu_index = index
        elif name == 'RSS':
            rss_index = index
        elif name == 'TIME':
            time_index = index
        elif name == 'STAT':
            stat_index = index

    if pid_index is None or cmdline_index is None:
        return result

    # In some case Docker return None instead of process list. Make
    # sure container_process is an iterable
    container_process = container_process or []
    for row in container_process:
        # The PID is from the point-of-view of root pid namespace.
        process = {
            'pid': int(row[pid_index]),
            'cmdline': row[cmdline_index],
            'name': os.path.basename(row[cmdline_index].split()[0]),
        }
        if user_index is not None:
            process['username'] = row[user_index]
        try:
            process['cpu_percent'] = float(row[pcpu_index])
        except (TypeError, ValueError):
            pass
        try:
            process['memory_rss'] = int(row[rss_index])
        except (TypeError, ValueError):
            pass
        try:
            process['cpu_times'] = pstime_to_second(row[time_index])
        except (TypeError, ValueError):
            pass
        if stat_index is not None:
            process['status'] = psstat_to_status(row[stat_index])
        result.append(process)

    return result


# Taken from Django project
def generate_password(length=10,
                      allowed_chars='abcdefghjkmnpqrstuvwxyz'
                                    'ABCDEFGHJKLMNPQRSTUVWXYZ'
                                    '23456789'):
    """
    Generates a random password with the given length and given
    allowed_chars. Note that the default value of allowed_chars does not
    have "I" or "O" or letters and digits that look similar -- just to
    avoid confusion.
    """
    return ''.join(random.choice(allowed_chars) for i in range(length))


def get_uptime():
    """ Return system uptime in seconds
    """
    boot_time = psutil.boot_time()
    now = time.time()
    return now - boot_time


def get_loadavg(core):
    """ Return system load average for last minutes, 5 minutes, 15 minutes
    """
    system_load1 = core.get_last_metric_value('system_load1', '', 0.0)
    system_load5 = core.get_last_metric_value('system_load5', '', 0.0)
    system_load15 = core.get_last_metric_value('system_load15', '', 0.0)

    return [system_load1, system_load5, system_load15]


def get_clock():
    """ Return a number of second since a unspecified point in time

        If will use CLOCK_MONOTONIC if available or fallback to time.time()

        It's useful to know of some event occurred before/after another one.
        It could be also useful to run on action every N seconds (note that
        system suspend might stop that clock).
    """
    if sys.version_info[0] >= 3 and sys.version_info[1] >= 3:
        return time.monotonic()
    return time.time()


def psstat_to_status(psstat):
    """ Convert a ps STAT to status string returned by psutil

        Only "ps waux" return this field. It something like

        * S   => sleeping
        * Ss  => sleeping
        * R+  => running

        The possible second (or more) char are ignored. They indicate
        additional status that we don't display.
    """
    char = psstat[0]

    mapping = {
        'D': 'disk-sleep',
        'R': 'running',
        'S': 'sleeping',
        'T': 'stopped',
        't': 'tracing-stop',
        'X': 'dead',
        'Z': 'zombie',
    }
    return mapping.get(char, '?')


def pstime_to_second(pstime):
    """ Convert a ps CPU time to a number of second

        Only time format from "ps -ef" or "ps waux" is considered.
        Example of format:

        * 00:16:42   => 1002
        * 16:42      => 1002
        * 1-02:27:14 => 95234
        * 1587:14    => 95234
    """

    if pstime.count(':') == 1:
        # format is MM:SS
        minute, second = pstime.split(':')
        return int(minute) * 60 + int(second)
    elif pstime.count(':') == 2 and '-' in pstime:
        # format is DD-HH:MM:SS
        day, rest = pstime.split('-')
        hour, minute, second = rest.split(':')
        return (
            int(day) * 86400 +
            int(hour) * 3600 +
            int(minute) * 60 +
            int(second)
        )
    elif pstime.count(':') == 2:
        # format is HH:MM:SS
        hour, minute, second = pstime.split(':')
        return int(hour) * 3600 + int(minute) * 60 + int(second)
    elif 'h' in pstime:
        # format is HHhMM
        hour, minute = pstime.split('h')
        return int(hour) * 3600 + int(minute) * 60
    elif 'd' in pstime:
        # format is DDdHH
        day, hour = pstime.split('d')
        return int(day) * 86400 + int(hour) * 3600
    else:
        raise ValueError('Unknown pstime format "%s"' % pstime)


def format_uptime(uptime_seconds):
    """ Format uptime to human readable format

        Output will be something like "1 hour" or "3 days, 7 hours"
    """
    uptime_days = int(uptime_seconds / (24 * 60 * 60))
    uptime_hours = int((uptime_seconds % (24 * 60 * 60)) / (60 * 60))
    uptime_minutes = int((uptime_seconds % (60 * 60)) / 60)

    if uptime_minutes > 1:
        text_minutes = 'minutes'
    else:
        text_minutes = 'minute'
    if uptime_hours > 1:
        text_hours = 'hours'
    else:
        text_hours = 'hour'
    if uptime_days > 1:
        text_days = 'days'
    else:
        text_days = 'day'

    if uptime_days == 0 and uptime_hours == 0:
        uptime_string = '%s %s' % (uptime_minutes, text_minutes)
    elif uptime_days == 0:
        uptime_string = '%s %s' % (uptime_hours, text_hours)
    else:
        uptime_string = '%s %s, %s %s' % (
            uptime_days, text_days, uptime_hours, text_hours)

    return uptime_string


def format_cpu_time(cpu_time):
    """ Format CPU time to top-like format

        Input is time in seconds.

        Output will be "7:29.31" (e.g. 7 minutes, 29.31 second).

        For large number (4 digits minute), we show second as integer.
    """
    minutes = int(cpu_time / 60)
    if minutes > 999:
        return '%s:%.0f' % (minutes, cpu_time % 60)
    return '%s:%.2f' % (minutes, cpu_time % 60)


def run_command_timeout(command, timeout=10):
    """ Run a command and wait at most timeout seconds

        Both stdout and stderr and captured and returned.

        Returns (return_code, output)
    """
    def _kill_proc(proc, wait_event, timeout):
        """ function used in a separate thread to kill process """
        if not wait_event.wait(timeout):
            # event is not set, so process didn't finished itself
            proc.terminate()

    try:
        proc = subprocess.Popen(
            command,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
        )
    except OSError:
        # Most probably: command not found
        return (127, b"Unable to run command")
    proc_finished = threading.Event()
    killer_thread = threading.Thread(
        target=_kill_proc, args=(proc, proc_finished, timeout))
    killer_thread.start()

    (output, _) = proc.communicate()
    proc_finished.set()
    killer_thread.join()

    returncode = proc.returncode
    if returncode == -15:
        # code -15 means SIGKILL, which is used by _kill_proc thread
        # to implement timeout.
        # Change returncode from timeout to a critical status
        returncode = 2

    return (returncode, output)


def clean_cmdline(cmdline):
    """ Remove character that may cause trouble.

        Known problem:

        * new-line: for InfluxDB line-protocol
    """
    return cmdline.replace('\r', '\\r').replace('\n', '\\n')


def get_pending_update(core):
    # pylint: disable=too-many-locals
    # pylint: disable=too-many-return-statements
    # pylint: disable=too-many-branches
    # pylint: disable=too-many-statements
    """ Returns the number of pending update for this system

        It return a couple (update_count, security_update_count).
        update_count include any security update.

        Both counter could be None. It means that this method could
        not retrieve the value.
    """
    # If running inside a Docker container, it can't run commands
    if core.container is not None:
        updates_file_name = os.path.join(
            core.config.get('df.host_mount_point', '/does-no-exists'),
            'var/lib/update-notifier/updates-available',
        )
        update_count = None
        security_count = None
        try:
            update_file = open(updates_file_name, 'rb')
        except (OSError, IOError):
            # File does not exists or permission denied
            return (None, None)
        else:
            with update_file:
                data = update_file.read().decode('utf-8')

            first_match = True
            for line in data.splitlines():
                # The RE can't contain exact string like
                # "(\d+) packages can be updated" because this
                # string get localized.
                match = re.search(
                    r'^(\d+) [\w\s]+.$',
                    line,
                )
                if match and first_match:
                    update_count = int(match.group(1))
                    first_match = False
                elif match:
                    security_count = int(match.group(1))

        return (update_count, security_count)

    # At the point, agent is not running inside a container, it can
    # use commands

    env = os.environ.copy()
    if 'LANG' in env:
        del env['LANG']

    try:
        proc = subprocess.Popen(
            ['/usr/lib/update-notifier/apt-check'],
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            env=env,
        )
        (output, _) = proc.communicate()
        (update_count, security_count) = output.split(b';')
        return (int(update_count), int(security_count))
    except (OSError, ValueError):
        pass

    try:
        proc = subprocess.Popen(
            [
                'apt-get',
                '--simulate',
                '-o', 'Debug::NoLocking=true',
                '--quiet', '--quiet',
                'dist-upgrade',
            ],
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            env=env,
        )
        update_count = 0
        security_count = 0
        security_re = re.compile(
            b'[^\\(]*\\(.* (Debian-Security|Ubuntu:[^/]*/[^-]*-security)'
        )
        (output, _) = proc.communicate()
        for line in output.splitlines():
            if not line.startswith(b'Inst'):
                continue
            update_count += 1
            if security_re.match(line):
                security_count += 1

        return (update_count, security_count)
    except OSError:
        pass

    try:
        proc = subprocess.Popen(
            [
                'dnf',
                '--cacheonly',
                '--quiet',
                'updateinfo',
            ],
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            env=env,
        )
        (output, _) = proc.communicate()

        update_count = 0
        security_count = 0

        match = re.search(
            b'^\\s+(\\d+) Security notice\\(s\\)$',
            output,
            re.MULTILINE,
        )
        if match is not None:
            security_count = int(match.group(1))

        results = re.findall(
            b'^\\s+(\\d+) \\w+ notice\\(s\\)$',
            output,
            re.MULTILINE,
        )
        update_count = sum(int(x) for x in results)
        return (update_count, security_count)
    except OSError:
        pass

    try:
        proc = subprocess.Popen(
            [
                'yum',
                '--cacheonly',
                '--quiet',
                'list', 'updates',
            ],
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            env=env,
        )
        (output, _) = proc.communicate()

        update_count = 0
        for line in output.splitlines():
            if line == b'Updated Packages':
                continue
            # yum list could add newline when package name is too long,
            # in this case the next line with version will start with
            # few whitespace.
            if line.startswith(b' '):
                continue
            update_count += 1

        proc = subprocess.Popen(
            [
                'yum',
                '--cacheonly',
                '--quiet',
                '--security',
                'list', 'updates',
            ],
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            env=env,
        )
        (output, _) = proc.communicate()

        security_count = 0
        for line in output.splitlines():
            if line == b'Updated Packages':
                continue
            security_count += 1

        return (update_count, security_count)
    except OSError:
        pass

    return (None, None)


def get_top_info(core):
    # pylint: disable=too-many-branches
    """ Return informations needed to build a "top" view.
    """
    gather_started_at = time.time()

    processes = {}
    if core.docker_client is not None:
        processes = _get_docker_process(core.docker_client)

    if (core.container is None
            or core.config.get('container.pid_namespace_host')):
        _update_process_psutil(processes, gather_started_at)

    now = time.time()
    cpu_usage = psutil.cpu_times_percent()
    memory_usage = psutil.virtual_memory()
    swap_usage = psutil.swap_memory()

    result = {
        'time': now,
        'uptime': get_uptime(),
        'loads': get_loadavg(core),
        'users': len(psutil.users()),
        'processes': list(processes.values()),
        'cpu': {
            'user': cpu_usage.user,
            'nice': getattr(cpu_usage, 'nice', 0.0),
            'system': cpu_usage.system,
            'idle': cpu_usage.idle,
            'iowait': getattr(cpu_usage, 'iowait', 0.0),
            'guest': getattr(cpu_usage, 'guest', None),
            'guest_nice': getattr(cpu_usage, 'guest_nice', None),
            'irq': getattr(cpu_usage, 'irq', None),
            'softirq': getattr(cpu_usage, 'softirq', None),
            'steal': getattr(cpu_usage, 'steal', None),
        },
        'memory': {
            'total': memory_usage.total / 1024,
            'used': memory_usage.used / 1024,
            'free': memory_usage.free / 1024,
            'buffers': getattr(memory_usage, 'buffers', 0.0) / 1024,
            'cached': getattr(memory_usage, 'cached', 0.0) / 1024,
        },
        'swap': {
            'total': swap_usage.total / 1024,
            'used': swap_usage.used / 1024,
            'free': swap_usage.free / 1024,
        }
    }

    if psutil.version_info < (4, 4):
        result['memory']['used'] -= result['memory']['buffers']
        result['memory']['used'] -= result['memory']['cached']

    return result


def get_top_output(top_info):
    """ Return a top-like output
    """
    env = jinja2.Environment(
        loader=jinja2.PackageLoader('bleemeo_agent', 'templates'),
        autoescape=True)
    template = env.get_template('top.txt')

    if top_info is None:
        return 'top - waiting for metrics...'

    memory_total = top_info['memory']['total']
    processes = []
    # Sort process by CPU consumption (then PID, when cpu % is the same)
    # Since we want a descending order for CPU usage, we have
    # reverse=True... but for PID we want a ascending order. That's why we
    # use a negation for the PID.
    sorted_process = sorted(
        top_info['processes'],
        key=lambda x: (x.get('cpu_percent', 0), -int(x['pid'])),
        reverse=True)
    for metric in sorted_process[:25]:
        # convert status (like "sleeping", "running") to one char status
        status = {
            psutil.STATUS_RUNNING: 'R',
            psutil.STATUS_SLEEPING: 'S',
            psutil.STATUS_DISK_SLEEP: 'D',
            psutil.STATUS_STOPPED: 'T',
            psutil.STATUS_TRACING_STOP: 'T',
            psutil.STATUS_ZOMBIE: 'Z',
        }.get(metric.get('status'), '?')
        processes.append(
            ('%(pid)5s %(user)-9.9s %(res)6d %(status)s '
             '%(cpu)5.1f %(mem)4.1f %(time)9s %(cmd)s') %
            {
                'pid': metric['pid'],
                'user': metric.get('username', ''),
                'res': metric.get('memory_rss', 0),
                'status': status,
                'cpu': metric.get('cpu_percent', 0),
                'mem':
                    float(metric.get('memory_rss', 0)) / memory_total * 100,
                'time': format_cpu_time(metric.get('cpu_times', 0)),
                'cmd': metric['name'],
            })

    process_total = len(top_info['processes'])
    process_running = len([
        x for x in top_info['processes']
        if x.get('status') == psutil.STATUS_RUNNING
    ])
    process_sleeping = len([
        x for x in top_info['processes']
        if x.get('status') == psutil.STATUS_SLEEPING
    ])
    process_stopped = len([
        x for x in top_info['processes']
        if x.get('status') == psutil.STATUS_STOPPED
    ])
    process_zombie = len([
        x for x in top_info['processes']
        if x.get('status') == psutil.STATUS_ZOMBIE
    ])

    date_top = datetime.datetime.fromtimestamp(top_info['time'])
    time_top = date_top.time().replace(microsecond=0)

    return template.render(
        time_top=time_top,
        uptime=bleemeo_agent.util.format_uptime(top_info['uptime']),
        top_info=top_info,
        loads=', '.join('%.2f' % x for x in top_info['loads']),
        process_total='%3d' % process_total,
        process_running='%3d' % process_running,
        process_sleeping='%3d' % process_sleeping,
        process_stopped='%3d' % process_stopped,
        process_zombie='%3d' % process_zombie,
        cpu_user='%5.1f' % top_info['cpu']['user'],
        cpu_system='%5.1f' % top_info['cpu']['system'],
        cpu_nice='%5.1f' % top_info['cpu']['nice'],
        cpu_idle='%5.1f' % top_info['cpu']['idle'],
        cpu_wait='%5.1f' % top_info['cpu']['iowait'],
        mem_total='%8d' % top_info['memory']['total'],
        mem_used='%8d' % top_info['memory']['used'],
        mem_free='%8d' % top_info['memory']['free'],
        mem_buffered='%8d' % top_info['memory']['buffers'],
        mem_cached='%8d' % top_info['memory']['cached'],
        swap_total='%8d' % top_info['swap']['total'],
        swap_used='%8d' % top_info['swap']['used'],
        swap_free='%8d' % top_info['swap']['free'],
        processes=processes,
    )


def _get_url(core, name, metric_config):
    url = metric_config['url']

    url_parsed = urllib_parse.urlparse(url)
    if url_parsed.scheme == '' or url_parsed.scheme == 'file':
        try:
            with open(url_parsed.path) as file_obj:
                return file_obj.read()
        except (IOError, OSError) as exc:
            logging.warning(
                'Failed to retrive metric %s: %s',
                name,
                exc,
            )
            return None

    args = {
        'verify': metric_config.get('ssl_check', True),
        'timeout': 3.0,
        'headers': {'User-Agent': core.http_user_agent},
    }
    if metric_config.get('username') is not None:
        args['auth'] = (
            metric_config.get('username'),
            metric_config.get('password', '')
        )
    try:
        response = requests.get(
            url,
            **args
        )
    except requests.exceptions.ConnectionError as exc:
        logging.warning(
            'Failed to retrieve metric %s: '
            'failed to establish connection to %s: %s',
            name,
            url,
            exc
        )
        return None
    except requests.exceptions.RequestException as exc:
        logging.warning(
            'Failed to retrieve metric %s: %s',
            name,
            exc,
        )
        return None

    return response.content


def pull_raw_metric(core, name):
    """ Pull a metrics (on HTTP(s)) in "raw" format.

        "raw" format means that the URL must return one number in plain/text.

        We expect to have the following configuration key under
        section "metric.pull.$NAME.":

        * url: where to fetch the metric [mandatory]
        * item: item to add on your metric [default: '' - no item]
        * interval: retrive the metric every interval seconds [default: 10s]
        * username: username used for basic authentication [default: no auth]
        * password: password used for basic authentication [default: ""]
        * ssl_check: should we check that SSL certificate are valid
          [default: yes]
    """
    metric_config = core.config.get('metric.pull.%s' % name, {})

    if 'url' not in metric_config:
        logging.warning('Missing URL for metric %s. Ignoring it', name)
        return

    response = _get_url(core, name, metric_config)
    if response is not None:
        value = None
        try:
            value = float(response)
        except ValueError:
            logging.warning(
                'Failed to retrive metric %s: response it not a number',
                name)

        if value is not None:
            metric = {
                'time': time.time(),
                'measurement': name,
                'value': value,
            }
            if metric_config.get('item', ''):
                metric['item'] = metric_config.get('item', '')
            core.emit_metric(metric)


def docker_restart(docker_client, container_name):
    """ Restart a Docker container
    """
    docker_client.stop(container_name)
    for _ in range(10):
        time.sleep(0.2)
        container_info = docker_client.inspect_container(container_name)
        running = container_info['State']['Running']
        if not running:
            break
    if running:
        logging.info(
            'container "%s" still running... restart may fail',
            container_name
        )
    docker_client.start(container_name)


def windows_instdir():
    """ Return Windows installation directory
    """
    bleemeo_package_dir = os.path.dirname(__file__)
    # bleemeo_agent package is located at $INSTDIR\pkgs\bleemeo_agent
    install_dir = os.path.dirname(os.path.dirname(bleemeo_package_dir))
    return install_dir


def windows_telegraf_path(default="telegraf"):
    """ Return path to telegraf. If not found, return default
    """
    # On Windows, when installed, telegraf is located as $INSTDIR\telegraf.exe
    instdir = windows_instdir()
    telegraf = os.path.join(instdir, "telegraf.exe")
    if os.path.exists(telegraf):
        return telegraf
    return default


class JSONEncoder(json.JSONEncoder):

    def default(self, o):  # pylint: disable=method-hidden
        if isinstance(o, set):
            return list(o)
        return super().default(o)


def _get_docker_process(docker_client):
    if docker is None:
        return {}

    processes = {}

    for container in docker_client.containers():
        # container has... nameS
        # Also name start with "/". I think it may have mulitple name
        # and/or other "/" with docker-in-docker.
        container_name = container['Names'][0].lstrip('/')
        try:
            try:
                docker_top = (
                    docker_client.top(container_name, ps_args="waux")
                )
            except TypeError:
                # Older version of Docker-py don't support ps_args option
                docker_top = (
                    docker_client.top(container_name)
                )
        except docker.errors.APIError:
            # most probably container is restarting or just stopped
            continue

        for process in decode_docker_top(docker_top):
            pid = process['pid']
            processes[pid] = process
            processes[pid]['instance'] = container_name

    return processes


def _update_process_psutil(processes, only_started_before):
    # pylint: disable=too-many-branches
    for process in psutil.process_iter():
        try:
            if process.pid == 0:
                # PID 0 on Windows use it for "System Idle Process".
                # PID 0 is not used Linux don't use it.
                # Other system are currently not supported.
                continue
            if process.create_time() > only_started_before:
                # Ignore process created very recently. This is done to avoid
                # issue with process created in a container between the listing
                # of container process and this update from psutil. Such
                # process would be marked as running outside any container
                # could lead to discovery error.
                continue
            try:
                username = process.username()
            except (KeyError, psutil.AccessDenied):
                # the uid can't be resolved by the system
                if os.name == 'nt':
                    username = ''
                else:
                    username = str(process.uids().real)

            # Cmdline may be unavailable (permission issue ?)
            # When unavailable, depending on psutil version, it returns
            # either [] or ['']
            try:
                cmdline = process.cmdline()
                if cmdline and cmdline[0]:
                    # shlex.quote is needed if the program path has space in
                    # the name. This is usually true under Windows but Windows
                    # has shlex.quote (Python 3.3+).
                    if hasattr(shlex, 'quote'):
                        cmdline = ' '.join(shlex.quote(x) for x in cmdline)
                    else:
                        cmdline = ' '.join(cmdline)
                    name = process.name()
                else:
                    cmdline = process.name()
                    name = cmdline
            except psutil.AccessDenied:
                cmdline = process.name()
                name = cmdline

            cpu_times = process.cpu_times()
            process_info = {
                'pid': process.pid,
                'create_time': process.create_time(),
                'cmdline': cmdline,
                'name': name,
                'memory_rss': process.memory_info().rss / 1024,
                'cpu_percent': process.cpu_percent(),
                'cpu_times':
                    cpu_times.user + cpu_times.system,
                'status': process.status(),
                'username': username,
                '_psutil': True,
            }
            try:
                process_info['exe'] = process.exe()
            except psutil.AccessDenied:
                process_info['exe'] = ''

            # Keep instance if the process is running in a Docker
            if process.pid in processes:
                process_info['instance'] = processes[process.pid]['instance']
            else:
                process_info['instance'] = ''

            processes[process.pid] = process_info
        except psutil.NoSuchProcess:
            continue

    return processes
