from bleemeo_agent.config import (
    config_files, load_config)


def test_config_files():
    assert config_files(['/does-not-exsits']) == []
    assert config_files(['/etc/passwd']) == ['/etc/passwd']
    wants = [
        'bleemeo_agent/tests/configs/main.conf',
        'bleemeo_agent/tests/configs/conf.d/first.conf',
        'bleemeo_agent/tests/configs/conf.d/second.conf',
    ]
    paths = [
        'bleemeo_agent/tests/configs/main.conf',
        'bleemeo_agent/tests/configs/conf.d'
    ]
    assert config_files(paths) == wants


def test_load_config():
    config = load_config([
        'bleemeo_agent/tests/configs/main.conf',
        'bleemeo_agent/tests/configs/conf.d',
    ])

    # From default config
    assert config.get('logging', 'level') == 'info'

    assert config.get('test', 'overriden_value') == 'second'
    assert config.get('test', 'main_conf_loaded') == 'yes'
    assert config.get('test', 'first_conf_loaded') == 'yes'
    assert config.get('test', 'second_conf_loaded') == 'yes'
