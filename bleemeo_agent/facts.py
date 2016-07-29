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
import logging
import os
import shlex
import socket
import subprocess

import requests
import yaml

import bleemeo_agent.config


# Optional dependencies
try:
    import apt_pkg
except ImportError:
    apt_pkg = None


DMI_DIR = '/sys/devices/virtual/dmi/id/'


def get_file_content(file_name):
    """ Read file content. If error occur, ignore and return None
    """
    try:
        with open(file_name) as file_obj:
            return file_obj.read().strip()
    except IOError:
        return None


def get_package_version(package_name, default=None):
    if apt_pkg is None:
        return default

    try:
        apt_pkg.init()
        cache = apt_pkg.Cache(progress=None)
    except:
        logging.info(
            'Failed to initialize APT cache to retrieve package %s version',
            package_name,
        )
        logging.debug('Exception is:', exc_info=True)
        return default

    if (package_name in cache
            and cache[package_name].current_ver is not None):
        return cache[package_name].current_ver.ver_str

    return default


def get_agent_version():
    return get_package_version('bleemeo-agent', bleemeo_agent.__version__)


def get_docker_version(core):
    """ return a couple (docker-engine-version, docker-api-version)
    """
    api_version = None
    package_version = None

    if core.docker_client is not None:
        try:
            versions = core.docker_client.version()
            api_version = versions.get('ApiVersion')
            package_version = versions.get('Version')
            return (package_version, api_version)
        except requests.exceptions.RequestException:
            logging.debug('error getting docker verion', exc_info=True)

    package_version = get_package_version('docker-engine', package_version)
    if package_version is None:
        package_version = get_package_version('docker.io', package_version)

    return (package_version, api_version)


def get_telegraf_version():
    package_version = get_package_version('telegraf')
    if package_version is None:
        try:
            output = subprocess.check_output(['telegraf', '-version'])
            output = output.decode('utf-8').strip()
        except (subprocess.CalledProcessError, OSError, UnicodeDecodeError):
            return None

        prefix = 'Telegraf - version '
        if output.startswith(prefix):
            package_version = output[len(prefix):]

    return package_version


def read_os_release():
    """ Read os-release file and returns its content as dict

        os-relase is a FreeDesktop standard:
        http://www.freedesktop.org/software/systemd/man/os-release.html
    """
    result = {}
    with open('/etc/os-release') as fd:
        for line in fd:
            line = line.strip()
            (key, value) = line.split('=', 1)
            # value is a quoted string (single or double quote).
            # Use shlex.split to convert to normal string (handling
            # correctly if the string contains escaped quote)
            value = shlex.split(value)[0]
            result[key] = value
    return result


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
    except (subprocess.CalledProcessError, OSError):
        # Either "ip" is not found... or you don't have a route to 8.8.8.8
        # (no internet ?).
        # We could try with psutil, but "ip" is present on all recent ditro
        # and you should have internet :)
        pass

    return None


def get_public_ip(core):
    """ Return public IP used by this agent
    """
    url = core.config.get(
        'agent.public_ip_indicator',
        'https://myip.bleemeo.com'
    )
    try:
        response = requests.get(url, timeout=5)
    except requests.exceptions.RequestException:
        return None

    if response.status_code == 200:
        return response.text

    return None


def get_virtual_type(privileged_facts):  # noqa
    """ Return what virtualization is used. "physical" if it's bare-metal.
    """
    result = 'physical'
    vendor_name = get_file_content(os.path.join(DMI_DIR, 'sys_vendor'))
    bios_vendor = get_file_content(os.path.join(DMI_DIR, 'bios_vendor'))

    if vendor_name is None:
        # OpenVZ don't have DMI sys_vendor file, is it OpenVZ ?
        if os.path.exists('/proc/user_beancounters'):
            return 'openvz'
        return result

    if ('qemu' in vendor_name.lower()
            or 'bochs' in vendor_name.lower()
            or 'digitalocean' in vendor_name.lower()):
        result = 'kvm'
    elif 'xen' in vendor_name.lower():
        result = 'xen'
    elif 'innotek' in vendor_name.lower():
        result = 'virtualbox'
    elif 'microsoft' in vendor_name.lower():
        result = 'hyper-v'
    elif 'google' in vendor_name.lower():
        result = 'gce'
    elif 'vmware' in vendor_name.lower():
        result = 'vmware'
    elif 'openstack' in vendor_name.lower():
        # At least OvH seem to use this for its Cloud platform.
        if bios_vendor is not None and 'bochs' in bios_vendor.lower():
            result = 'kvm'
        elif 'vmware' in privileged_facts.get('serial_number', '').lower():
            # VMware use serial_number like "VMware-42 1d 8c ..."
            result = 'vmware'
        else:
            # unknown hypervisor at this point
            result = 'openstack'

    return result


def get_facts_root():
    """ Gather facts that need root privilege and write them in yaml file
    """

    config, errors = bleemeo_agent.config.load_config()
    if errors:
        logging.error(
            'Error while loading configuration: %s', '\n'.join(errors)
        )
        return

    facts_file = config.get('agent.facts_file', 'facts.yaml')

    facts = {
        'serial_number': get_file_content(
            os.path.join(DMI_DIR, 'product_serial')
        ),
    }
    facts = strip_empty(facts)

    with open(facts_file, 'w') as file_obj:
        yaml.safe_dump(facts, file_obj, default_flow_style=False)

    return facts


def get_facts(core):
    """ Return facts/grains/information about current machine.

        Returned facts are informations like hostname, OS type/version, etc
    """
    # Load facts that need root privilege from facts_file
    facts_file = core.config.get('agent.facts_file', 'facts.yaml')
    try:
        with open(facts_file) as file_obj:
            facts = yaml.safe_load(file_obj)
    except IOError:
        facts = {}

    os_information = read_os_release()
    try:
        os_codename = subprocess.check_output(
            ['lsb_release', '--codename', '--short']
        ).decode('utf8').strip()
    except OSError:
        os_codename = None

    primary_address = get_primary_address()
    architecture = subprocess.check_output(
        ['uname', '--machine']
    ).decode('utf8').strip()
    fqdn = socket.getfqdn()
    if fqdn == 'localhost':
        fqdn = socket.gethostname()

    if '.' in fqdn:
        (hostname, domain) = fqdn.split('.', 1)
    else:
        (hostname, domain) = (fqdn, None)
    kernel = subprocess.check_output(
        ['uname', '--kernel-name']
    ).decode('utf8').strip()
    kernel_release = subprocess.check_output(
        ['uname', '--kernel-release']
    ).decode('utf8').strip()
    kernel_version = kernel_release.split('-')[0]
    kernel_major_version = '.'.join(kernel_release.split('.')[0:2])
    virtual = get_virtual_type(facts)

    (docker_version, docker_api_version) = get_docker_version(core)

    facts.update({
        'agent_version': get_agent_version(),
        'architecture': architecture,
        'bios_released_at': get_file_content(
            os.path.join(DMI_DIR, 'bios_date')
        ),
        'bios_vendor': get_file_content(
            os.path.join(DMI_DIR, 'bios_vendor')
        ),
        'bios_version': get_file_content(
            os.path.join(DMI_DIR, 'bios_version')
        ),
        'fact_updated_at': datetime.datetime.utcnow().isoformat() + 'Z',
        'collectd_version': get_package_version('collectd'),
        'docker_api_version': docker_api_version,
        'docker_version': docker_version,
        'domain': domain,
        'public_ip': get_public_ip(core),
        'fqdn': fqdn,
        'hostname': hostname,
        'kernel': kernel,
        'kernel_major_version': kernel_major_version,
        'kernel_release': kernel_release,
        'kernel_version': kernel_version,
        'metrics_source': core.graphite_server.metrics_source,
        'os_codename': os_codename,
        'os_family': os_information.get('ID_LIKE', None),
        'os_name': os_information.get('NAME', None),
        'os_pretty_name': os_information.get('PRETTY_NAME', None),
        'os_version': os_information.get('VERSION_ID', None),
        'os_version_long': os_information.get('VERSION', None),
        'primary_address': primary_address,
        'product_name': get_file_content(
            os.path.join(DMI_DIR, 'product_name')
        ),
        'system_vendor': get_file_content(
            os.path.join(DMI_DIR, 'sys_vendor')
        ),
        'telegraf_version': get_telegraf_version(),
        'timezone': get_file_content('/etc/timezone'),
        'virtual': virtual,
    })

    facts = strip_empty(facts)

    return facts


def strip_empty(facts):
    """ Remove facts with "None" as value or empty string
    """
    return {
        key: value for (key, value) in facts.items()
        if value is not None and value != ''
    }
