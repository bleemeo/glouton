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

import bleemeo_agent.collectd


class DummyCore:
    def __init__(self):
        self.config = {
            'disk_monitor': [
                '^(hd|sd|vd|xvd)[a-z]$',
            ]
        }


def test_COLLECTD_REGEX():
    match = bleemeo_agent.collectd.COLLECTD_REGEX.match('cpu-0.cpu-idle')
    assert match.groupdict() == {
        'plugin': 'cpu',
        'plugin_instance': '0',
        'type': 'cpu',
        'type_instance': 'idle',
    }

    match = bleemeo_agent.collectd.COLLECTD_REGEX.match(
        'df-var-lib.df_complex-free')
    assert match.groupdict() == {
        'plugin': 'df',
        'plugin_instance': 'var-lib',
        'type': 'df_complex',
        'type_instance': 'free',
    }

    match = bleemeo_agent.collectd.COLLECTD_REGEX.match(
        'diskstats-sda.counter-sectors_read')
    assert match.groupdict() == {
        'plugin': 'diskstats',
        'plugin_instance': 'sda',
        'type': 'counter',
        'type_instance': 'sectors_read',
    }

    match = bleemeo_agent.collectd.COLLECTD_REGEX.match(
        'users.users')
    assert match.groupdict() == {
        'plugin': 'users',
        'plugin_instance': None,
        'type': 'users',
        'type_instance': None,
    }
