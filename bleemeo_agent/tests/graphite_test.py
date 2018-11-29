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

import bleemeo_agent.graphite
from bleemeo_agent.graphite import _disk_path_rename


def test_graphite_split_line():

    # Simple metric
    (metric, value, timestamp) = bleemeo_agent.graphite.graphite_split_line(
        b'hostname_example_com.df-boot.df_complex-used 119843840 1459257933',
    )
    assert metric == 'hostname_example_com.df-boot.df_complex-used'
    assert value == 119843840.0
    assert timestamp == 1459257933.0

    # Simple telegraf metric
    (metric, value, timestamp) = bleemeo_agent.graphite.graphite_split_line(
        b'telegraf.hostname.ext4./boot.disk.used 119843840 1459257420'
    )
    assert metric == 'telegraf.hostname.ext4./boot.disk.used'
    assert value == 119843840.0
    assert timestamp == 1459257420.0

    # non-float value
    (metric, value, timestamp) = bleemeo_agent.graphite.graphite_split_line(
        b'telegraf.hostname.system.uptime_format "20 days, 23:26" 1459257790'
    )
    assert metric == 'telegraf.hostname.system.uptime_format'
    assert value == '20 days, 23:26'
    assert timestamp == 1459257790

    # Space in metric name
    (metric, value, timestamp) = bleemeo_agent.graphite.graphite_split_line(
        b'telegraf.hostname.elasticsearch.172_17_0_5.uVowpVl3RmO_S22rVTgWBA.'
        b'Thomas Halloway.elasticsearch_indices.percolate_current 0 1459257790'
    )
    assert metric == ('telegraf.hostname.elasticsearch.172_17_0_5.'
                      'uVowpVl3RmO_S22rVTgWBA.Thomas Halloway.'
                      'elasticsearch_indices.percolate_current'
                      )
    assert value == 0.0
    assert timestamp == 1459257790.0


def test_disk_path_rename():
    ignore = [
        '/media'
    ]
    assert _disk_path_rename('/media/usb', None, ignore) is None
    assert _disk_path_rename('/media', None, ignore) is None
    assert (
        _disk_path_rename('/var/lib/docker/overlay', None, ignore) ==
        '/var/lib/docker/overlay'
    )
    assert (
        _disk_path_rename('/var/lib/docker/overlay2', None, ignore) ==
        '/var/lib/docker/overlay2'
    )
    assert _disk_path_rename('/srv', None, ignore) == '/srv'
    assert _disk_path_rename('/srv/', None, ignore) == '/srv/'

    ignore = [
        '/srv'
    ]
    assert _disk_path_rename('/media/usb', None, ignore) == '/media/usb'
    assert _disk_path_rename('/media', None, ignore) == '/media'
    assert (
        _disk_path_rename('/var/lib/docker/overlay', None, ignore) ==
        '/var/lib/docker/overlay'
    )
    assert (
        _disk_path_rename('/var/lib/docker/overlay2', None, ignore) ==
        '/var/lib/docker/overlay2'
    )
    assert _disk_path_rename('/srv', None, ignore) is None
    assert _disk_path_rename('/srv/', None, ignore) is None

    ignore = [
        '/srv/'
    ]
    assert _disk_path_rename('/media/usb', None, ignore) == '/media/usb'
    assert _disk_path_rename('/media', None, ignore) == '/media'
    assert (
        _disk_path_rename('/var/lib/docker/overlay', None, ignore) ==
        '/var/lib/docker/overlay'
    )
    assert (
        _disk_path_rename('/var/lib/docker/overlay2', None, ignore) ==
        '/var/lib/docker/overlay2'
    )
    assert _disk_path_rename('/srv', None, ignore) is None
    assert _disk_path_rename('/srv/', None, ignore) is None

    ignore = [
        '/var/lib/docker/overlay'
    ]
    assert _disk_path_rename('/media/usb', None, ignore) == '/media/usb'
    assert _disk_path_rename('/media', None, ignore) == '/media'
    assert _disk_path_rename('/var/lib/docker/overlay', None, ignore) is None
    assert (
        _disk_path_rename('/var/lib/docker/overlay2', None, ignore) ==
        '/var/lib/docker/overlay2'
    )
    assert _disk_path_rename('/srv', None, ignore) == '/srv'
    assert _disk_path_rename('/srv/', None, ignore) == '/srv/'

    ignore = [
        '/srv'
    ]
    assert _disk_path_rename('/', '/hostroot', ignore) is None
    assert _disk_path_rename('/hostroot', '/hostroot', ignore) == '/'
    assert _disk_path_rename('/hostroot/', '/hostroot', ignore) == '/'
    assert _disk_path_rename('/hostroot', '/hostroot/', ignore) == '/'
    assert _disk_path_rename('/hostroot/srv', '/hostroot', ignore) is None
    assert (
        _disk_path_rename('/hostroot/media', '/hostroot', ignore) ==
        '/media'
    )
