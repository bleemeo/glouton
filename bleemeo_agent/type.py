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

from collections import namedtuple

STATUS_OK = 0
STATUS_WARNING = 1
STATUS_CRITICAL = 2
STATUS_UNKNOWN = 3

STATUS_NAME = {
    STATUS_OK: 'ok',
    STATUS_WARNING: 'warning',
    STATUS_CRITICAL: 'critical',
    STATUS_UNKNOWN: 'unknown',
}

STATUS_NAME_TO_CODE = {
    'ok': STATUS_OK,
    'warning': STATUS_WARNING,
    'critical': STATUS_CRITICAL,
    'unknown': STATUS_UNKNOWN,
}

MetricPoint = namedtuple(
    'MetricPoint',
    [
        'label',
        'time',
        'value',
        'item',
        'service_label',
        'service_instance',
        'container_name',
        'status_code',
        'status_of',
        'problem_origin'
    ]
)

DEFAULT_METRICPOINT = MetricPoint(
    label='',
    time='',
    value='',
    item='',
    service_label='',
    service_instance='',
    container_name='',
    status_code=None,
    status_of='',
    problem_origin='',
)
