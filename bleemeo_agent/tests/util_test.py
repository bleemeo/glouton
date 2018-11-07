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

import bleemeo_agent.util


def test_decode_docker_top():
    """ docker top <container> don't always return the same output.

        For example with docker-machine, the *first* boot output looks like:
        PID                 USER                COMMAND
        3028                root                bash

        Test case are generated using:

        docker run --rm -ti --name test \
            -v /var/run/docker.sock:/var/run/docker.sock \
            bleemeo/bleemeo-agent \
            python3 -c 'import docker;
                print(docker.Client(version="1.21").top("test"))'
    """

    docker_top_result = [
        # Boot2Docker 1.12.3 first boot
        {
            'Processes': [
                ['3216', 'root',
                    'python3 -c import docker;'
                    'print(docker.Client(version="1.21").top("test"))']
            ],
            'Titles': ['PID', 'USER', 'COMMAND']
        },
        # Boot2Docker 1.12.3 second boot
        {
            'Titles': [
                'UID', 'PID', 'PPID', 'C', 'STIME', 'TTY', 'TIME', 'CMD'
            ],
            'Processes': [
                ['root', '1551', '1542', '0', '14:13', 'pts/1', '00:00:00',
                    'python3 -c import docker;'
                    'print(docker.Client(version="1.21").top("test"))']
            ]
        },
        # Ubuntu 16.04
        {
            'Processes': [
                ['root', '5017', '4988', '0', '15:15', 'pts/29', '00:00:00',
                    'python3 -c import docker;'
                    'print(docker.Client(version="1.21").top("test"))']
            ],
            'Titles': [
                'UID', 'PID', 'PPID', 'C', 'STIME', 'TTY', 'TIME', 'CMD'
            ]
        },
    ]

    for case in docker_top_result:
        result = bleemeo_agent.util.decode_docker_top(case)
        assert len(result) == 1
        # result[0][0] is a PID, e.g. a number
        assert isinstance(result[0]['pid'], int)
        assert result[0]['cmdline'].startswith('python3')


def test_format_uptime():
    # edge case, uptime below 1 minute are shown are 0 minute.
    assert bleemeo_agent.util.format_uptime(59) == '0 minute'

    assert bleemeo_agent.util.format_uptime(1*60) == '1 minute'
    assert bleemeo_agent.util.format_uptime(42*60) == '42 minutes'
    # minutes are not shown when we switch to hours
    assert bleemeo_agent.util.format_uptime(2*60*60 + 5*60) == '2 hours'
    assert bleemeo_agent.util.format_uptime(
        2*24*60*60 + 1*60*60 + 5) == '2 days, 1 hour'
    assert bleemeo_agent.util.format_uptime(
        800*24*60*60 + 2*60*60 + 5) == '800 days, 2 hours'

    # Giving float instead of int also works well
    assert bleemeo_agent.util.format_uptime(float(42*60)) == '42 minutes'
    assert bleemeo_agent.util.format_uptime(float(2*60*60 + 5*60)) == '2 hours'
    assert bleemeo_agent.util.format_uptime(
        float(2*24*60*60 + 1*60*60 + 5)) == '2 days, 1 hour'


def test_pstime_to_second():
    tests = [
        ('00:16:42', 16*60+42),
        ('16:42', 16*60+42),
        ('1-02:27:14', 24*3600+2*3600+27*60+14),
        ('1587:14', 1587*60+14),

        # busybox time
        ('12h27', 12*3600+27*60),
        ('6d09', 6*24*3600+9*3600),
        ('18d12', 18*24*3600+12*3600),
    ]

    for case in tests:
        assert bleemeo_agent.util.pstime_to_second(case[0]) == case[1]


def test_psstat_to_status():
    tests = [
        ('D', 'disk-sleep'),
        ('I', '?'),
        ('I<', '?'),
        ('R+', 'running'),
        ('Rl', 'running'),
        ('S', 'sleeping'),
        ('S<', 'sleeping'),
        ('S<l', 'sleeping'),
        ('SLl+', 'sleeping'),
        ('Ss', 'sleeping'),
        ('Z+', 'zombie'),
        ('T', 'stopped'),
        ('t', 'tracing-stop'),
    ]

    for case in tests:
        assert bleemeo_agent.util.psstat_to_status(case[0]) == case[1]


def test_docker_cgroup_re():
    """ Test that DOCKER_CRGOUP_RE match expecte cgroup content
    """

    # minikube v0.28.2
    kube_cgroup = """11:freezer:/kubepods/besteffort/pod8f469a2e-bcd6-11e8-abe9-080027ae1159/bc4dd7f3f935c6798df001b908b05544fdadb29bc55e12635ea3558e0a4b87f6
10:perf_event:/kubepods/besteffort/pod8f469a2e-bcd6-11e8-abe9-080027ae1159/bc4dd7f3f935c6798df001b908b05544fdadb29bc55e12635ea3558e0a4b87f6
[...]
1:name=systemd:/kubepods/besteffort/pod8f469a2e-bcd6-11e8-abe9-080027ae1159/bc4dd7f3f935c6798df001b908b05544fdadb29bc55e12635ea3558e0a4b87f6
    """
    # Docker on Ubuntu
    docker_cgroup = """12:pids:/docker/bc4dd7f3f935c6798df001b908b05544fdadb29bc55e12635ea3558e0a4b87f6
11:hugetlb:/docker/bc4dd7f3f935c6798df001b908b05544fdadb29bc55e12635ea3558e0a4b87f6
[...]
1:cpuset:/docker/bc4dd7f3f935c6798df001b908b05544fdadb29bc55e12635ea3558e0a4b87f6
"""
    # Docker on CentOS
    docker2_cgroup = """11:cpuset:/system.slice/docker-bc4dd7f3f935c6798df001b908b05544fdadb29bc55e12635ea3558e0a4b87f6.scope
10:devices:/system.slice/docker-bc4dd7f3f935c6798df001b908b05544fdadb29bc55e12635ea3558e0a4b87f6.scope
[...]
1:name=systemd:/system.slice/docker-bc4dd7f3f935c6798df001b908b05544fdadb29bc55e12635ea3558e0a4b87f6.scope
"""
    want = set([
        'bc4dd7f3f935c6798df001b908b05544fdadb29bc55e12635ea3558e0a4b87f6'
    ])
    result = bleemeo_agent.util.get_docker_id_from_cgroup(kube_cgroup)
    assert result == want

    result = bleemeo_agent.util.get_docker_id_from_cgroup(docker_cgroup)
    assert result == want

    result = bleemeo_agent.util.get_docker_id_from_cgroup(docker2_cgroup)
    assert result == want
