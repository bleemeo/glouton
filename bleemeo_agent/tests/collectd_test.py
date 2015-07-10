import bleemeo_agent.collectd


def test_collectd_regex():
    match = bleemeo_agent.collectd.collectd_regex.match('cpu-0.cpu-idle')
    assert match.groupdict() == {
        'plugin': 'cpu',
        'plugin_instance': '0',
        'type': 'cpu',
        'type_instance': 'idle',
    }

    match = bleemeo_agent.collectd.collectd_regex.match(
        'df-var-lib.df_complex-free')
    assert match.groupdict() == {
        'plugin': 'df',
        'plugin_instance': 'var-lib',
        'type': 'df_complex',
        'type_instance': 'free',
    }

    match = bleemeo_agent.collectd.collectd_regex.match(
        'disk-sda.disk_octets.read')
    assert match.groupdict() == {
        'plugin': 'disk',
        'plugin_instance': 'sda',
        'type': 'disk_octets',
        'type_instance': 'read',
    }

    match = bleemeo_agent.collectd.collectd_regex.match(
        'users.users')
    assert match.groupdict() == {
        'plugin': 'users',
        'plugin_instance': None,
        'type': 'users',
        'type_instance': None,
    }


def test_rename_metric():
    pending = set()
    result = bleemeo_agent.collectd._rename_metric(
        'cpu-0.cpu-idle',
        12345,
        42,
        pending)
    assert result == {
        'measurement': 'cpu_idle',
        'time': 12345,
        'fields': {'value': 42},
        'tags': {'cpu': 0},
        'ignore': True,
    }
    assert len(pending) == 1
    assert ('cpu_idle', None, 12345) in pending

    pending = set()
    result = bleemeo_agent.collectd._rename_metric(
        'users.users',
        12345,
        42,
        pending)
    assert result == {
        'measurement': 'users_logged',
        'time': 12345,
        'fields': {'value': 42},
        'tags': {},
        'ignore': False,
    }
    assert len(pending) == 0

    pending = set()
    result = bleemeo_agent.collectd._rename_metric(
        'df-var-lib.df_complex-free',
        12345,
        42,
        pending)
    assert result == {
        'measurement': 'disk_free',
        'time': 12345,
        'fields': {'value': 42},
        'tags': {'path': '/var/lib'},
        'ignore': False,
    }
    assert len(pending) == 1
    assert ('disk_total', '/var/lib', 12345) in pending

    pending = set()
    result = bleemeo_agent.collectd._rename_metric(
        'disk-sda.disk_ops.read',
        12345,
        42,
        pending)
    assert result == {
        'measurement': 'io_reads',
        'time': 12345,
        'fields': {'value': 42},
        'tags': {'name': 'sda'},
        'ignore': False,
    }
    assert len(pending) == 0
