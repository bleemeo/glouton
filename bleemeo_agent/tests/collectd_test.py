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
