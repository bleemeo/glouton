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
	"time"

	psutilNet "github.com/shirou/gopsutil/net"
)

// (partial) output of sudo netstat -lnp with LANG=fr_FR.UTF-8.
const fileContent = `Connexions Internet actives (seulement serveurs)
Proto Recv-Q Send-Q Adresse locale          Adresse distante        Etat       PID/Program name
tcp        0      0 127.0.0.1:46319         0.0.0.0:*               LISTEN      5534/cli
tcp        0      0 0.0.0.0:111             0.0.0.0:*               LISTEN      323667/rpcbind
tcp        0      0 172.17.0.1:9100         0.0.0.0:*               LISTEN      3541/node_exporter
tcp6       0      0 :::111                  :::*                    LISTEN      323667/rpcbind
tcp6       0      0 ::1:631                 :::*                    LISTEN      14250/cupsd
udp        0      0 0.0.0.0:5353            0.0.0.0:*                           1375/avahi-daemon:
udp        0      0 192.168.122.1:53        0.0.0.0:*                           2158/dnsmasq
udp        0      0 0.0.0.0:609             0.0.0.0:*                           323667/rpcbind
udp6       0      0 :::8125                 :::*                                1560/telegraf
raw6       0      0 :::58                   :::*                    7           1424/NetworkManager
Sockets du domaine UNIX actives (seulement serveurs)
Proto RefCnt Flags       Type       State         I-Node   PID/Program name     Chemin
unix  2      [ ACC ]     STREAM     LISTENING     66929    5108/gnome-session-  @/tmp/.ICE-unix/5108
unix  2      [ ACC ]     SEQPACKET  LISTENING     18666    1/init               /run/udev/control
`

func getMockNetstat() []psutilNet.ConnectionStat {
	// (partial) output of a psutil.Connections().
	return []psutilNet.ConnectionStat{
		{Fd: 6, Family: 2, Type: 1, Laddr: psutilNet.Addr{IP: "0.0.0.0", Port: 4242}, Raddr: psutilNet.Addr{IP: "1.2.3.4", Port: 0}, Status: "LISTEN", Uids: []int32{1000, 1000, 1000, 1000}, Pid: 323668},
		{Fd: 6, Family: 2, Type: 1, Laddr: psutilNet.Addr{IP: "0.0.0.0", Port: 6379}, Raddr: psutilNet.Addr{IP: "1.2.3.4", Port: 0}, Status: "LISTEN", Uids: []int32{1000, 1000, 1000, 1000}, Pid: 323667},
		{Fd: 33, Family: 2, Type: 1, Laddr: psutilNet.Addr{IP: "192.168.1.40", Port: 47740}, Raddr: psutilNet.Addr{IP: "1.2.3.4", Port: 443}, Status: "ESTABLISHED", Uids: []int32{1000, 1000, 1000, 1000}, Pid: 3191},
		{Fd: 66, Family: 2, Type: 1, Laddr: psutilNet.Addr{IP: "192.168.1.40", Port: 43479}, Raddr: psutilNet.Addr{IP: "1.2.3.4", Port: 443}, Status: "ESTABLISHED", Uids: []int32{1000, 1000, 1000, 1000}, Pid: 4587},
		{Fd: 196, Family: 2, Type: 1, Laddr: psutilNet.Addr{IP: "192.168.1.40", Port: 50596}, Raddr: psutilNet.Addr{IP: "1.2.3.4", Port: 80}, Status: "CLOSE_WAIT", Uids: []int32{1000, 1000, 1000, 1000}, Pid: 3943},
		{Fd: 30, Family: 2, Type: 1, Laddr: psutilNet.Addr{IP: "192.168.1.40", Port: 46290}, Raddr: psutilNet.Addr{IP: "1.2.3.4", Port: 443}, Status: "ESTABLISHED", Uids: []int32{1000, 1000, 1000, 1000}, Pid: 94042},
		{Fd: 32, Family: 2, Type: 1, Laddr: psutilNet.Addr{IP: "192.168.1.40", Port: 40308}, Raddr: psutilNet.Addr{IP: "1.2.3.4", Port: 443}, Status: "ESTABLISHED", Uids: []int32{1000, 1000, 1000, 1000}, Pid: 1898},
		{Fd: 25, Family: 2, Type: 1, Laddr: psutilNet.Addr{IP: "192.168.1.40", Port: 49634}, Raddr: psutilNet.Addr{IP: "1.2.3.4", Port: 443}, Status: "ESTABLISHED", Uids: []int32{1000, 1000, 1000, 1000}, Pid: 1898},
		{Fd: 49, Family: 2, Type: 1, Laddr: psutilNet.Addr{IP: "192.168.1.40", Port: 51010}, Raddr: psutilNet.Addr{IP: "1.2.3.4", Port: 443}, Status: "ESTABLISHED", Uids: []int32{1000, 1000, 1000, 1000}, Pid: 3544},
		{Fd: 170, Family: 2, Type: 1, Laddr: psutilNet.Addr{IP: "192.168.1.40", Port: 33884}, Raddr: psutilNet.Addr{IP: "1.2.3.4", Port: 443}, Status: "ESTABLISHED", Uids: []int32{1000, 1000, 1000, 1000}, Pid: 3544},
		{Fd: 11, Family: 2, Type: 1, Laddr: psutilNet.Addr{IP: "192.168.1.40", Port: 54268}, Raddr: psutilNet.Addr{IP: "1.2.3.4", Port: 139}, Status: "CLOSE_WAIT", Uids: []int32{1000, 1000, 1000, 1000}, Pid: 4507},
		{Fd: 34, Family: 2, Type: 1, Laddr: psutilNet.Addr{IP: "192.168.1.40", Port: 42536}, Raddr: psutilNet.Addr{IP: "1.2.3.4", Port: 443}, Status: "ESTABLISHED", Uids: []int32{1000, 1000, 1000, 1000}, Pid: 3021},
		{Fd: 0, Family: 2, Type: 1, Laddr: psutilNet.Addr{IP: "192.168.1.40", Port: 57478}, Raddr: psutilNet.Addr{IP: "1.2.3.4", Port: 443}, Status: "TIME_WAIT", Uids: []int32{}, Pid: 0},
		{Fd: 7, Family: 10, Type: 1, Laddr: psutilNet.Addr{IP: "::", Port: 6379}, Raddr: psutilNet.Addr{IP: "::", Port: 0}, Status: "LISTEN", Uids: []int32{1000, 1000, 1000, 1000}, Pid: 323667},
		{Fd: 158, Family: 10, Type: 1, Laddr: psutilNet.Addr{IP: "2a01:cb19:820e:5b00:7fa:37be:7396:8d8e", Port: 36116}, Raddr: psutilNet.Addr{IP: "FE80:0000:0000:5EFE:0192.0168.0001.0123", Port: 443}, Status: "CLOSE_WAIT", Uids: []int32{1000, 1000, 1000, 1000}, Pid: 3943},
		{Fd: 0, Family: 2, Type: 2, Laddr: psutilNet.Addr{IP: "192.168.1.40", Port: 68}, Raddr: psutilNet.Addr{IP: "1.2.3.4", Port: 67}, Status: "NONE", Uids: []int32{}, Pid: 0},
		{Fd: 76, Family: 10, Type: 2, Laddr: psutilNet.Addr{IP: "::", Port: 46429}, Raddr: psutilNet.Addr{IP: "::", Port: 0}, Status: "NONE", Uids: []int32{1000, 1000, 1000, 1000}, Pid: 4587},
		{Fd: 0, Family: 10, Type: 2, Laddr: psutilNet.Addr{IP: "fe80::92d0:93a3:f56:b588", Port: 546}, Raddr: psutilNet.Addr{IP: "FE80:0000:0000:5EFE:0192.0168.0001.0123", Port: 0}, Status: "NONE", Uids: []int32{}, Pid: 0},
		{Fd: 38, Family: 10, Type: 2, Laddr: psutilNet.Addr{IP: "::", Port: 60918}, Raddr: psutilNet.Addr{IP: "::", Port: 0}, Status: "NONE", Uids: []int32{1000, 1000, 1000, 1000}, Pid: 4587},
	}
}

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
	want := map[int][]ListenAddress{
		5534: {
			{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 46319},
		},
		323667: {
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
func TestMergeNetstats(t *testing.T) {
	netstat := decodeNetstatFile(fileContent)
	mockNetstat := getMockNetstat()

	np := &NetstatProvider{
		"null",
	}

	np.mergeNetstats(netstat, mockNetstat)

	data, ok := netstat[323668]

	if !ok {
		t.Errorf("PID 323668 not in merged Netstat.")
	}

	_, ok = netstat[5534]

	if !ok {
		t.Errorf("PID only in netstat file was incorrectly removed")
	}

	nbAddress := len(netstat[323667])

	if nbAddress != 3 {
		t.Errorf("PID 323667 did not merge addresses correctly")
	}

	if len(data) == 0 {
		t.Errorf("PID 323668 should not be empty")
	}

	if data[0].Port != 4242 || data[0].Address != "0.0.0.0" {
		t.Errorf("Unexpected created data in netstat results for port 32668. Got %s:%d, want 0.0.0.0:4242", data[0].Address, data[0].Port)
	}
}

func TestCleanRecycledPIDs(t *testing.T) {
	netstat := decodeNetstatFile(fileContent)
	np := &NetstatProvider{
		"null",
	}
	mockProcesses := make(map[int]Process)
	modTime, _ := time.Parse(time.RFC3339, "2020-11-01T22:08:41+00:00")
	createTime, _ := time.Parse(time.RFC3339, "2021-11-01T22:08:41+00:00")

	mockProcesses[323667] = Process{
		PID:             323667,
		PPID:            318465,
		CreateTime:      createTime,
		CreateTimestamp: 1618214640,
		CmdLineList: []string{
			"./redis-server *:6379",
		},
		CmdLine:       "./redis-server *:6379",
		Name:          "redis-server",
		MemoryRSS:     5888,
		CPUPercent:    0,
		CPUTime:       19.02,
		Status:        "running",
		Username:      "dummy",
		Executable:    "/path/to/executable/redis-server",
		ContainerID:   "",
		ContainerName: "",
		NumThreads:    5,
	}

	fmt.Println(modTime.Before(createTime))
	np.cleanRecycledPIDs(netstat, mockProcesses, modTime)

	_, ok := netstat[323667]

	if ok {
		t.Errorf("Netstat PID 323667 was not removed")
	}

	modTime, _ = time.Parse(time.RFC3339, "2021-12-01T22:08:41+00:00")
	netstat[323667] = []ListenAddress{
		{
			Address:       "0.0.0.0",
			NetworkFamily: "tcp",
			Port:          111,
		},
	}

	np.cleanRecycledPIDs(netstat, mockProcesses, modTime)

	_, ok = netstat[323667]

	if !ok {
		t.Errorf("Netstat PID 323667 was incorrectly removed")
	}
}
