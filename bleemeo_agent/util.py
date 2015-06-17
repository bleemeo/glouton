import datetime
import json
import time
import logging
import random
import socket
import subprocess
import threading

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


class Sleeper:
    """ Helper to manage exponential sleep time.

        You can get the next duration of sleep with self.current_duration
    """

    def __init__(self, start_duration=10, max_duration=600):
        self.start_duration = start_duration
        self.max_duration = max_duration

        # number of sleep done with minimal duration
        self.grace_count = 3
        self.current_duration = start_duration

    def get_sleep_duration(self):
        if self.grace_count > 0:
            self.grace_count -= 1
        else:
            self.current_duration = min(
                self.max_duration, self.current_duration * 2)

        return self.current_duration

    def sleep(self):
        duration = self.get_sleep_duration()
        logging.debug('Sleeping %s seconds', duration)
        time.sleep(duration)


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


def get_facts(agent):
    """ Return facts/grains/information about current machine.

        Returned facts are informations like hostname, OS type/version, etc

        It will use facter tools if available
    """
    # Basic "minimal" facts
    facts = {
        'hostname': socket.gethostname(),
        'fqdn': socket.getfqdn(),
        'agent_version': bleemeo_agent.__version__,
        'current_time': datetime.datetime.now().isoformat(),
        'account_uuid': agent.account_id,
    }

    # Update with facter facts
    try:
        facter_raw = subprocess.check_output([
            'facter', '--json'
        ]).decode('utf-8')
        facts.update(json.loads(facter_raw))
    except OSError:
        facts.setdefault('errors', []).append('facter not installed')
        logging.warning(
            'facter is not installed. Only limited facts are sents')

    return facts


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
