import bleemeo_agent.config


def test_merge_dict():
    assert bleemeo_agent.config.merge_dict({}, {}) == {}
    assert (
        bleemeo_agent.config.merge_dict({'a': 1}, {'b': 2}) ==
        {'a': 1, 'b': 2})

    d1 = {
        'd1': 1,
        'remplaced': 1,
        'sub_dict': {
            'd1': 1,
            'remplaced': 1,
        }
    }
    d2 = {
        'd2': 2,
        'remplaced': 2,
        'sub_dict': {
            'd2': 2,
            'remplaced': 2,
        }
    }
    want = {
        'd1': 1,
        'd2': 2,
        'remplaced': 2,
        'sub_dict': {
            'd1': 1,
            'd2': 2,
            'remplaced': 2,
        }
    }
    assert bleemeo_agent.config.merge_dict(d1, d2) == want


def test_config_files():
    assert bleemeo_agent.config.config_files(['/does-not-exsits']) == []
    assert bleemeo_agent.config.config_files(
        ['/etc/passwd']) == ['/etc/passwd']

    wants = [
        'bleemeo_agent/tests/configs/main.yml',
        'bleemeo_agent/tests/configs/conf.d/first.yml',
        'bleemeo_agent/tests/configs/conf.d/second.yml',
    ]
    paths = [
        'bleemeo_agent/tests/configs/main.yml',
        'bleemeo_agent/tests/configs/conf.d'
    ]
    assert bleemeo_agent.config.config_files(paths) == wants


def test_load_config():
    config = bleemeo_agent.config.load_config([
        'bleemeo_agent/tests/configs/main.yml',
        'bleemeo_agent/tests/configs/conf.d',
    ])

    assert config == {
        'main_conf_loaded': True,
        'first_conf_loaded': True,
        'second_conf_loaded': True,
        'overridden_value': 'second',
        'merged_dict': {
            'main': 1.0,
            'first': 'yes',
        },
        'sub_section': {
            'nested': None,
        },
    }
    assert config.get('second_conf_loaded') is True
    assert config.get('merged_dict.main') == 1.0

    # Ensure that when value is defined to None, we return None and not the
    # default
    assert config.get('sub_section.nested', 'a value') is None
