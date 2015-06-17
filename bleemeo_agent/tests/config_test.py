import os
import tempfile

from six.moves import configparser

from bleemeo_agent.config import (
    config_files, load_config, get_generated_values)


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


def test_get_generated_values():
    tmpdir = tempfile.mkdtemp()
    filepath = os.path.join(tmpdir, 'generated_values.json')
    config = configparser.SafeConfigParser()
    config.add_section('agent')
    config.set('agent', 'generated_values_file', filepath)
    try:
        values = get_generated_values(config)
        # re-read
        values2 = get_generated_values(config)
        assert values == values2
        assert os.path.isfile(filepath)
    finally:
        try:
            os.unlink(filepath)
        except OSError:
            pass  # probably file does not exists
        os.rmdir(tmpdir)
