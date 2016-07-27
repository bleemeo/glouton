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

import bleemeo_agent.config


def test_config_object():
    conf = bleemeo_agent.config.Config()
    conf['test'] = {
        'a': 'a',
        'one': 1,
        'sub-level': {
            'two': 2.0,
        },
    }

    assert conf.get('test.a') == 'a'
    assert conf.get('test.one') == 1
    assert conf.get('test.sub-level.two') == 2.0
    assert conf.get('test.does.not.exists') is None
    assert conf.get('test.does.not.exists', 'default') == 'default'

    conf.set('test.b', 'B')
    assert conf.get('test.b') == 'B'
    assert conf.get('test.one') == 1

    conf.set('test.now.does.exists.value', 42)
    assert conf.get('test.now.does.exists.value') == 42


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


def test_merge_list():
    assert bleemeo_agent.config.merge_dict({'a': []}, {'a': []}) == {'a': []}
    assert (
        bleemeo_agent.config.merge_dict({'a': []}, {'a': [1]}) ==
        {'a': [1]})

    d1 = {
        'a': [1, 2],
        'merge_create_duplicate': [1, 2, 3],
        'sub_dict': {
            'a': [1, 2],
            'b': [1, 2],
        }
    }
    d2 = {
        'a': [3, 4],
        'merge_create_duplicate': [3, 4],
        'sub_dict': {
            'a': [3, 4],
            'c': [3, 4],
        }
    }
    want = {
        'a': [1, 2, 3, 4],
        'merge_create_duplicate': [1, 2, 3, 3, 4],
        'sub_dict': {
            'a': [1, 2, 3, 4],
            'b': [1, 2],
            'c': [3, 4],
        }
    }
    assert bleemeo_agent.config.merge_dict(d1, d2) == want


def test_config_files():
    assert bleemeo_agent.config.config_files(['/does-not-exsits']) == []
    assert bleemeo_agent.config.config_files(
        ['/etc/passwd']) == ['/etc/passwd']

    wants = [
        'bleemeo_agent/tests/configs/main.conf',
        'bleemeo_agent/tests/configs/conf.d/empty.conf',
        'bleemeo_agent/tests/configs/conf.d/first.conf',
        'bleemeo_agent/tests/configs/conf.d/second.conf',
    ]
    paths = [
        'bleemeo_agent/tests/configs/main.conf',
        'bleemeo_agent/tests/configs/conf.d'
    ]
    assert bleemeo_agent.config.config_files(paths) == wants


def test_load_config():
    config, errors = bleemeo_agent.config.load_config([
        'bleemeo_agent/tests/configs/main.conf',
        'bleemeo_agent/tests/configs/conf.d',
    ])

    assert len(errors) == 0

    assert config == {
        'main_conf_loaded': True,
        'first_conf_loaded': True,
        'second_conf_loaded': True,
        'overridden_value': 'second',
        'merged_dict': {
            'main': 1.0,
            'first': 'yes',
        },
        'merged_list': [
            'duplicated between main.conf & second.conf',
            'item from main.conf',
            'item from first.conf',
            'item from second.conf',
            'duplicated between main.conf & second.conf',
        ],
        'sub_section': {
            'nested': None,
        },
    }
    assert config.get('second_conf_loaded') is True
    assert config.get('merged_dict.main') == 1.0

    # Ensure that when value is defined to None, we return None and not the
    # default
    assert config.get('sub_section.nested', 'a value') is None
