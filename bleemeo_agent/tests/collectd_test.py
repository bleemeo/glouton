import bleemeo_agent.collectd


class DummyCore:
    def __init__(self):
        self.config = {
            'disk_monitor': [
                '^(hd|sd|vd|xvd)[a-z]$',
            ]
        }


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
    core = DummyCore()
    collectd = bleemeo_agent.collectd.Collectd(core)
    computed_metrics_pending = set()
    (result, no_emit) = collectd._rename_metric(
        'cpu.percent-idle',
        12345,
        42,
        computed_metrics_pending,
    )
    assert result == {
        'measurement': 'cpu_idle',
        'time': 12345,
        'value': 42,
    }
    assert no_emit is False
    assert len(computed_metrics_pending) == 0

    computed_metrics_pending = set()
    (result, no_emit) = collectd._rename_metric(
        'users.users',
        12345,
        42,
        computed_metrics_pending,
    )
    assert result == {
        'measurement': 'users_logged',
        'time': 12345,
        'value': 42,
    }
    assert no_emit is False
    assert len(computed_metrics_pending) == 0

    computed_metrics_pending = set()
    (result, no_emit) = collectd._rename_metric(
        'df-var-lib.df_complex-free',
        12345,
        42,
        computed_metrics_pending,
    )
    assert result == {
        'measurement': 'disk_free',
        'time': 12345,
        'value': 42,
        'item': '/var/lib',
    }
    assert no_emit is False
    assert len(computed_metrics_pending) == 1
    assert (
        ('disk_total', '/var/lib', 12345)
        in computed_metrics_pending
    )

    computed_metrics_pending = set()
    (result, no_emit) = collectd._rename_metric(
        'disk-sda.disk_ops.read',
        12345,
        42,
        computed_metrics_pending,
    )
    assert result == {
        'measurement': 'io_reads',
        'time': 12345,
        'value': 42,
        'item': 'sda',
    }
    assert no_emit is False
    assert len(computed_metrics_pending) == 0
