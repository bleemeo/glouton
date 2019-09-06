// Copyright 2015-2019 Bleemeo
//
// bleemeo.com an infrastructure monitoring solution in the Cloud
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package facts

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

func cmpAddresses(t *testing.T, msgPrefix string, got []ListenAddress, want []ListenAddress) {
	if len(got) != len(want) {
		t.Errorf("%s == %v, want %v", msgPrefix, got, want)
	}
	sort.Slice(got, func(i, j int) bool {
		return strings.Compare(got[i].Network(), got[j].Network()) < 0 || strings.Compare(got[i].String(), got[j].String()) < 0
	})
	for i, x := range got {
		y := want[i]
		if x.Network() != y.Network() || x.String() != y.String() {
			t.Errorf("%s[%v] == %v/%v, want %v/%v", msgPrefix, i, x.Network(), x.String(), y.Network(), y.String())
		}
	}
}

func TestAddAddress(t *testing.T) {
	cases := []struct {
		adds []ListenAddress
		want []ListenAddress
	}{
		{
			adds: []ListenAddress{{NetworkFamily: "tcp", Address: "0.0.0.0:22"}},
			want: []ListenAddress{{NetworkFamily: "tcp", Address: "0.0.0.0:22"}},
		},
		{
			adds: []ListenAddress{{NetworkFamily: "unix", Address: "@/tmp/.ICE-unix/5108"}},
			want: []ListenAddress{{NetworkFamily: "unix", Address: "@/tmp/.ICE-unix/5108"}},
		},
	}

	for i, c := range cases {
		var got []ListenAddress
		for _, newAddr := range c.adds {
			got = addAddress(got, newAddr)
		}
		cmpAddresses(t, fmt.Sprintf("addAddresses(<case #%d>)", i), got, c.want)
	}
}

func TestDecodeNetstatFile(t *testing.T) {
	// (partial) output of sudo netstat -lnp with LANG=fr_FR.UTF-8
	fileContent := `Connexions Internet actives (seulement serveurs)
Proto Recv-Q Send-Q Adresse locale          Adresse distante        Etat       PID/Program name    
tcp        0      0 127.0.0.1:46319         0.0.0.0:*               LISTEN      5534/cli            
tcp        0      0 0.0.0.0:111             0.0.0.0:*               LISTEN      1281/rpcbind        
tcp        0      0 172.17.0.1:9100         0.0.0.0:*               LISTEN      3541/node_exporter  
tcp6       0      0 :::111                  :::*                    LISTEN      1281/rpcbind        
tcp6       0      0 ::1:631                 :::*                    LISTEN      14250/cupsd     
udp        0      0 0.0.0.0:5353            0.0.0.0:*                           1375/avahi-daemon:  
udp        0      0 192.168.122.1:53        0.0.0.0:*                           2158/dnsmasq   
udp        0      0 0.0.0.0:609             0.0.0.0:*                           1281/rpcbind        
udp6       0      0 :::8125                 :::*                                1560/telegraf       
raw6       0      0 :::58                   :::*                    7           1424/NetworkManager 
Sockets du domaine UNIX actives (seulement serveurs)
Proto RefCnt Flags       Type       State         I-Node   PID/Program name     Chemin
unix  2      [ ACC ]     STREAM     LISTENING     66929    5108/gnome-session-  @/tmp/.ICE-unix/5108
unix  2      [ ACC ]     SEQPACKET  LISTENING     18666    1/init               /run/udev/control
`

	want := map[int][]ListenAddress{
		5534: {
			{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 46319},
		},
		1281: {
			{NetworkFamily: "tcp", Address: "0.0.0.0", Port: 111},
			{NetworkFamily: "udp", Address: "0.0.0.0", Port: 609},
		},
		3541: {
			{NetworkFamily: "tcp", Address: "172.17.0.1", Port: 9100},
		},
		14250: {
			{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 631}, // tcp6 as assumed to be tcp6+4. We only work with tcp4 for now
		},
		1375: {
			{NetworkFamily: "udp", Address: "0.0.0.0", Port: 5353},
		},
		2158: {
			{NetworkFamily: "udp", Address: "192.168.122.1", Port: 53},
		},
		1560: {
			{NetworkFamily: "udp", Address: "0.0.0.0", Port: 8125},
		},
		5108: {
			{NetworkFamily: "unix", Address: "@/tmp/.ICE-unix/5108"},
		},
	}

	got := decodeNetstatFile(fileContent)
	if len(got) != len(want) {
		t.Errorf("decodeNetstatFile(...) == %v, want %v", got, want)
	} else {
		for pid, g := range got {
			w := want[pid]
			cmpAddresses(t, "decodeNetstatFile(...)[%v]", g, w)
		}
	}
}
