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

import bleemeo_agent.util


def test_format_uptime():
    # edge case, uptime below 1 minute are shown are 0 minute.
    assert bleemeo_agent.util.format_uptime(59) == '0 minute'

    assert bleemeo_agent.util.format_uptime(1*60) == '1 minute'
    assert bleemeo_agent.util.format_uptime(42*60) == '42 minutes'
    # minutes are not shown when we switch to hours
    assert bleemeo_agent.util.format_uptime(2*60*60 + 5*60) == '2 hours'
    assert bleemeo_agent.util.format_uptime(
        2*24*60*60 + 1*60*60 + 5) == '2 days, 1 hour'
    assert bleemeo_agent.util.format_uptime(
        800*24*60*60 + 2*60*60 + 5) == '800 days, 2 hours'

    # Giving float instead of int also works well
    assert bleemeo_agent.util.format_uptime(float(42*60)) == '42 minutes'
    assert bleemeo_agent.util.format_uptime(float(2*60*60 + 5*60)) == '2 hours'
    assert bleemeo_agent.util.format_uptime(
        float(2*24*60*60 + 1*60*60 + 5)) == '2 days, 1 hour'
