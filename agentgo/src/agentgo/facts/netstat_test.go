package facts

import (
	"fmt"
	"net"
	"sort"
	"strings"
	"testing"
)

func cmpAddresses(t *testing.T, msgPrefix string, got []net.Addr, want []listenAddress) {
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
		adds []listenAddress
		want []listenAddress
	}{
		{
			adds: []listenAddress{{network: "tcp", address: "0.0.0.0:22"}},
			want: []listenAddress{{network: "tcp", address: "0.0.0.0:22"}},
		},
		{
			adds: []listenAddress{{network: "unix", address: "@/tmp/.ICE-unix/5108"}},
			want: []listenAddress{{network: "unix", address: "@/tmp/.ICE-unix/5108"}},
		},
	}

	for i, c := range cases {
		var got []net.Addr
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

	want := map[int][]listenAddress{
		5534: {
			{network: "tcp", address: "127.0.0.1:46319"},
		},
		1281: {
			{network: "tcp", address: "0.0.0.0:111"},
			{network: "udp", address: "0.0.0.0:609"},
		},
		3541: {
			{network: "tcp", address: "172.17.0.1:9100"},
		},
		14250: {
			{network: "tcp", address: "127.0.0.1:631"}, // tcp6 as assumed to be tcp6+4. We only work with tcp4 for now
		},
		1375: {
			{network: "udp", address: "0.0.0.0:5353"},
		},
		2158: {
			{network: "udp", address: "192.168.122.1:53"},
		},
		1560: {
			{network: "udp", address: "0.0.0.0:8125"},
		},
		5108: {
			{network: "unix", address: "@/tmp/.ICE-unix/5108"},
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
