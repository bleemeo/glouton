#
#  Copyright 2018 Bleemeo
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

# Can't run flake8 as we need to include verbatim output from ip command which
# include whitespace at the end of line and line too long.
# flake8: noqa

import bleemeo_agent.facts


def test_get_primary_addresses():

    # Output from "ip route get 8.8.8.8;ip address show"
    ip_output = """8.8.8.8 via 10.0.2.2 dev eth0  src 10.0.2.15 
    cache
1: lo: <LOOPBACK,UP,LOWER_UP> mtu 65536 qdisc noqueue state UNKNOWN group default 
    link/loopback 00:00:00:00:00:00 brd 00:00:00:00:00:00
    inet 127.0.0.1/8 scope host lo
       valid_lft forever preferred_lft forever
    inet6 ::1/128 scope host 
       valid_lft forever preferred_lft forever
2: eth0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 qdisc pfifo_fast state UP group default qlen 1000
    link/ether 08:00:27:72:ca:f1 brd ff:ff:ff:ff:ff:ff
    inet 10.0.2.15/24 brd 10.0.2.255 scope global eth0
       valid_lft forever preferred_lft forever
    inet6 fe80::a00:27ff:fe72:caf1/64 scope link 
       valid_lft forever preferred_lft forever
3: docker0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 qdisc noqueue state UP group default 
    link/ether 02:42:8d:e2:74:26 brd ff:ff:ff:ff:ff:ff
    inet 172.17.0.1/16 brd 172.17.255.255 scope global docker0
       valid_lft forever preferred_lft forever
    inet6 fe80::42:8dff:fee2:7426/64 scope link 
       valid_lft forever preferred_lft forever
5: vethde299e2: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 qdisc noqueue master docker0 state UP group default 
    link/ether fe:b8:9f:9e:36:aa brd ff:ff:ff:ff:ff:ff
    inet6 fe80::fcb8:9fff:fe9e:36aa/64 scope link 
       valid_lft forever preferred_lft forever
"""

    (ip_address, mac_address) = bleemeo_agent.facts.get_primary_addresses(
        ip_output
    )
    assert ip_address == '10.0.2.15'
    assert mac_address == '08:00:27:72:ca:f1'


def test_empty_get_primary_addresses():
    """ get_primary_addresses should not crash with empty input
    """
    result = bleemeo_agent.facts.get_primary_addresses('')
    assert result == (None, None)
