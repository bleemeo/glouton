import datetime
import logging
import os
import random
import shlex
import socket
import subprocess
import threading
import time

import jinja2
import psutil
import requests

import bleemeo_agent


# With generate_password, taken from Django project
# Use the system PRNG if possible
try:
    random = random.SystemRandom()
    using_sysrandom = True
except NotImplementedError:
    import warnings
    warnings.warn('A secure pseudo-random number generator is not available '
                  'on your system. Falling back to Mersenne Twister.')
    using_sysrandom = False


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


def get_facts(core):
    """ Return facts/grains/information about current machine.

        Returned facts are informations like hostname, OS type/version, etc
    """
    if os.path.exists('/etc/os-release'):
        pretty_name = get_os_pretty_name()
    else:
        pretty_name = 'Unknown OS'

    if os.path.exists('/sys/devices/virtual/dmi/id/product_name'):
        with open('/sys/devices/virtual/dmi/id/product_name') as fd:
            product_name = fd.read()
    else:
        product_name = ''

    uptime_seconds = get_uptime()
    uptime_string = format_uptime(uptime_seconds)

    primary_address = get_primary_address()

    # Basic "minimal" facts
    facts = {
        'hostname': socket.gethostname(),
        'fqdn': socket.getfqdn(),
        'os_pretty_name': pretty_name,
        'uptime': uptime_string,
        'uptime_seconds': uptime_seconds,
        'primary_address': primary_address,
        'product_name': product_name,
        'agent_version': bleemeo_agent.__version__,
        'current_time': datetime.datetime.now().isoformat(),
    }

    if core.bleemeo_connector is not None:
        facts['account_uuid'] = core.bleemeo_connector.account_id

    return facts


def get_uptime():
    with open('/proc/uptime', 'r') as f:
        uptime_seconds = float(f.readline().split()[0])
        return uptime_seconds


def get_loadavg():
    with open('/proc/loadavg', 'r') as fd:
        loads = fd.readline().split()[:3]

    return [float(x) for x in loads]


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
    else:
        return '%s:%.2f' % (minutes, cpu_time % 60)


def get_os_pretty_name():
    """ Return the PRETTY_NAME from os-release
    """
    with open('/etc/os-release') as fd:
        for line in fd:
            line = line.strip()
            if line.startswith('PRETTY_NAME'):
                (_, value) = line.split('=')
                # value is a quoted string (single or double quote).
                # Use shlex.split to convert to normal string (handling
                # correctly if the string contains escaped quote)
                value = shlex.split(value)[0]
                return value


def get_primary_address():
    """ Return the primary IP(v4) address.

        This should be the address that this server use to communicate
        on internet. It may be the private IP if the box is NATed
    """
    # Any python library doing the job ?
    # psutils could retrive IP address from interface, but we don't
    # known which is the "primary" interface.
    # For now rely on "ip" command
    try:
        output = subprocess.check_output(
            ['ip', 'route', 'get', '8.8.8.8'])
        split_output = output.decode('utf-8').split()
        for (index, word) in enumerate(split_output):
            if word == 'src':
                return split_output[index+1]
    except subprocess.CalledProcessError:
        # Either "ip" is not found... or you don't have a route to 8.8.8.8
        # (no internet ?).
        # We could try with psutil, but "ip" is present on all recent ditro
        # and you should have internet :)
        pass

    return None


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
        # Most probably : command not found
        return (127, "Unable to run command")
    proc_finished = threading.Event()
    killer_thread = threading.Thread(
        target=_kill_proc, args=(proc, proc_finished, timeout))
    killer_thread.start()

    (output, _) = proc.communicate()
    proc_finished.set()
    killer_thread.join()

    return (proc.returncode, output)


def package_installed(package_name):
    """ Return True if given package is installed.

        Work on Debian/Ubuntu derivate
    """
    try:
        output = subprocess.check_output(
            ['dpkg-query', '--show', '--showformat=${Status}', package_name],
            stderr=subprocess.STDOUT,
        )
        installed = output.startswith(b'install')
    except subprocess.CalledProcessError:
        installed = False

    return installed


def clean_cmdline(cmdline):
    """ Remove character that may cause trouble.

        Known problem:

        * new-line : for InfluxDB line-protocol
    """
    return cmdline.replace('\r', '\\r').replace('\n', '\\n')


def get_top_info():
    """ Return informations needed to build a "top" view.
    """
    processes = []
    for process in psutil.process_iter():
        try:
            username = process.username()
        except KeyError:
            # the uid can't be resolved by the system
            username = str(process.uids().real)
        processes.append({
            'pid': process.pid,
            'create_time': process.create_time(),
            'name': process.name(),
            'cmdline': process.cmdline(),
            'ppid': process.ppid(),
            'memory_rss': process.memory_info().rss,
            'cpu_percent': process.cpu_percent(),
            'cpu_times': process.cpu_times().user + process.cpu_times().system,
            'status': process.status(),
            'username': username,
        })

    now = time.time()
    cpu_usage = psutil.cpu_times_percent()
    memory_usage = psutil.virtual_memory()
    swap_usage = psutil.swap_memory()

    result = {
        'time': now,
        'uptime': get_uptime(),
        'loads': get_loadavg(),
        'users': len(psutil.users()),
        'processes': processes,
        'cpu': {
            'user': cpu_usage.user,
            'nice': cpu_usage.nice,
            'system': cpu_usage.system,
            'idle': cpu_usage.idle,
            'iowait': cpu_usage.iowait,
        },
        'memory': {
            'total': memory_usage.total,
            'used': memory_usage.used,
            'free': memory_usage.free,
            'buffers': memory_usage.buffers,
            'cached': memory_usage.cached,
        },
        'swap': {
            'total': swap_usage.total,
            'used': swap_usage.used,
            'free': swap_usage.free,
        }
    }

    return result


def get_top_output(top_info):
    """ Return a top-like output
    """
    env = jinja2.Environment(
        loader=jinja2.PackageLoader('bleemeo_agent', 'templates'))
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
        key=lambda x: (x['cpu_percent'], -int(x['pid'])),
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
        }.get(metric['status'], '?')
        processes.append(
            ('%(pid)5s %(user)-9.9s %(res)6d %(status)s '
                '%(cpu)5.1f %(mem)4.1f %(time)9s %(cmd)s') %
            {
                'pid': metric['pid'],
                'user': metric['username'],
                'res': metric['memory_rss'] / 1024,
                'status': status,
                'cpu': metric['cpu_percent'],
                'mem':
                    float(metric['memory_rss']) / memory_total * 100,
                'time': format_cpu_time(metric['cpu_times']),
                'cmd': metric['name'],
            })

    process_total = len(top_info['processes'])
    process_running = len(filter(
        lambda x: x['status'] == psutil.STATUS_RUNNING,
        top_info['processes']
    ))
    process_sleeping = len(filter(
        lambda x: x['status'] == psutil.STATUS_SLEEPING,
        top_info['processes']
    ))
    process_stopped = len(filter(
        lambda x: x['status'] == psutil.STATUS_STOPPED,
        top_info['processes']
    ))
    process_zombie = len(filter(
        lambda x: x['status'] == psutil.STATUS_ZOMBIE,
        top_info['processes']
    ))

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
        mem_total='%8d' % (top_info['memory']['total'] / 1024),
        mem_used='%8d' % (top_info['memory']['used'] / 1024),
        mem_free='%8d' % (top_info['memory']['free'] / 1024),
        mem_buffered='%8d' % (top_info['memory']['buffers'] / 1024),
        mem_cached='%8d' % (top_info['memory']['cached'] / 1024),
        swap_total='%8d' % (top_info['swap']['total'] / 1024),
        swap_used='%8d' % (top_info['swap']['used'] / 1024),
        swap_free='%8d' % (top_info['swap']['free'] / 1024),
        processes=processes,
    )


def _get_url(name, metric_config):
    response = None
    try:
        response = requests.get(
            metric_config['url'],
            verify=metric_config.get('ssl_check', True),
            timeout=3.0,
        )
    except requests.exceptions.ConnectionError:
        logging.warning(
            'Failed to retrive metric %s : failed to establish connection',
            name)
    except requests.exceptions.ConnectionError:
        logging.warning(
            'Failed to retrive metric %s : request timed out',
            name)
    except requests.exceptions.RequestException:
        logging.warning(
            'Failed to retrive metric %s',
            name)

    return response


def pull_raw_metric(core, name):
    """ Pull a metrics (on HTTP(s)) in "raw" format.

        "raw" format means that the URL must return one number in plain/text.

        We expect to have the following configuration key under
        section "metric.pull.$NAME.":

        * url : where to fetch the metric [mandatory]
        * item: item to add on your metric [default: None - no item]
        * interval : retrive the metric every interval seconds [default: 10s]
        * ssl_check : should we check that SSL certificate are valid
          [default: yes]
    """
    metric_config = core.config.get('metric.pull.%s' % name, {})

    if 'url' not in metric_config:
        logging.warning('Missing URL for metric %s. Ignoring it', name)
        return

    response = _get_url(name, metric_config)
    if response is not None:
        value = None
        try:
            value = float(response.content)
        except ValueError:
            logging.warning(
                'Failed to retrive metric %s : response it not a number',
                name)

        if value is not None:
            metric = {
                'time': time.time(),
                'measurement': name,
                'item': metric_config.get('item', None),
                'status': None,
                'service': None,
                'value': value,
            }
            core.emit_metric(metric)
