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
        'diskstats-sda.counter-sectors_read')
    assert match.groupdict() == {
        'plugin': 'diskstats',
        'plugin_instance': 'sda',
        'type': 'counter',
        'type_instance': 'sectors_read',
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
    # For this test, Collectd object does not really need Core object
    core = None
    collectd = bleemeo_agent.collectd.Collectd(core)
    (result, no_emit) = collectd._rename_metric(
        'cpu-0.cpu-idle',
        12345,
        42,
    )
    assert result == {
        'measurement': 'cpu_idle',
        'time': 12345,
        'value': 42,
        'tag': '0',
        'status': None,
        'service': None,
    }
    assert no_emit is True
    assert len(collectd.computed_metrics_pending) == 1
    assert ('cpu_idle', None, 12345) in collectd.computed_metrics_pending

    collectd.computed_metrics_pending = set()
    (result, no_emit) = collectd._rename_metric(
        'users.users',
        12345,
        42,
    )
    assert result == {
        'measurement': 'users_logged',
        'time': 12345,
        'value': 42,
        'tag': None,
        'status': None,
        'service': None,
    }
    assert no_emit is False
    assert len(collectd.computed_metrics_pending) == 0

    collectd.computed_metrics_pending = set()
    (result, no_emit) = collectd._rename_metric(
        'df-var-lib.df_complex-free',
        12345,
        42,
    )
    assert result == {
        'measurement': 'disk_free',
        'time': 12345,
        'value': 42,
        'tag': '/var/lib',
        'status': None,
        'service': None,
    }
    assert no_emit is False
    assert len(collectd.computed_metrics_pending) == 1
    assert (
        ('disk_total', '/var/lib', 12345)
        in collectd.computed_metrics_pending
    )

    collectd.computed_metrics_pending = set()
    (result, no_emit) = collectd._rename_metric(
        'diskstats-sda.counter-reads_completed',
        12345,
        42,
    )
    assert result == {
        'measurement': 'io_reads',
        'time': 12345,
        'value': 42,
        'tag': 'sda',
        'status': None,
        'service': None,
    }
    assert no_emit is False
    assert len(collectd.computed_metrics_pending) == 0
