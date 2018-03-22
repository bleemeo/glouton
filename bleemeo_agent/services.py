#
#  Copyright 2018 Bleemeo
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


import re
import subprocess
import time


def gather_exim_queue_size(instance, core):
    """ Gather and send metric for queue size

        instance may be unset (empty string) which means gather metric
        for a postfix running outside any container. This is only done
        if the agent is running outside any container.

        instance may also be set to the Docker container name running
        the postfix. This require core.docker_client to be set.

        In all case, this will run "exim4 -bpc" (subprocess or docker exec)
    """
    if instance and core.docker_client is not None:
        result = core.docker_client.exec_create(
            instance,
            ['exim4', '-bpc'],
        )
        output = core.docker_client.exec_start(result['Id'])
    elif not instance and not core.container:
        try:
            output = subprocess.check_output(
                ['exim4', '-bpc'],
                stderr=subprocess.STDOUT,
            )
        except (subprocess.CalledProcessError, IOError, OSError):
            return
    else:
        return

    if isinstance(output, bytes):
        output = output.decode('utf-8')

    try:
        count = int(output)
        core.emit_metric({
            'measurement': 'exim_queue_size',
            'time': time.time(),
            'value': float(count),
            'instance': instance,
            'item': instance,
            'service': 'exim',
        })
    except ValueError:
        return


def gather_postfix_queue_size(instance, core):
    """ Gather and send metric for queue size

        instance may be unset (empty string) which means gather metric
        for a postfix running outside any container. This is only done
        if the agent is running outside any container.

        instance may also be set to the Docker container name running
        the postfix. This require core.docker_client to be set.

        In all case, this will run "postqueue -p" (subprocess or docker exec)
    """
    if instance and core.docker_client is not None:
        result = core.docker_client.exec_create(
            instance,
            ['postqueue', '-p'],
        )
        output = core.docker_client.exec_start(result['Id'])
    elif not instance and not core.container:
        try:
            output = subprocess.check_output(
                ['postqueue', '-p'],
                stderr=subprocess.STDOUT,
            )
        except subprocess.CalledProcessError:
            return
    else:
        return

    if isinstance(output, bytes):
        output = output.decode('utf-8')

    match = re.search(r'-- \d+ Kbytes in (\d+) Request.', output)
    if match:
        count = int(match.group(1))
        core.emit_metric({
            'measurement': 'postfix_queue_size',
            'time': time.time(),
            'value': float(count),
            'instance': instance,
            'item': instance,
            'service': 'postfix',
        })
