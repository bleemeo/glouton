package discovery

import (
	"agentgo/facts"
	"context"
	"net"
	"reflect"
	"testing"
	"time"
)

type mockProcess struct {
	result []facts.Process
}

func (mp mockProcess) Processes(ctx context.Context, maxAge time.Duration) (processes map[int]facts.Process, err error) {
	m := make(map[int]facts.Process)
	for _, p := range mp.result {
		m[p.PID] = p
	}
	return m, nil
}

type mockNetstat struct {
	result map[int][]listenAddress
}

func (mn mockNetstat) Netstat(ctx context.Context) (netstat map[int][]net.Addr, err error) {
	result := make(map[int][]net.Addr, len(mn.result))
	for pid, m := range mn.result {
		sublist := make([]net.Addr, len(m))
		for i := range m {
			sublist[i] = m[i]
		}
		result[pid] = sublist
	}
	return result, nil
}

func TestServiceByCommand(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{
			in:   []string{"/usr/bin/memcached", "-m", "64", "-p", "11211", "-u", "memcache", "-l", "127.0.0.1", "-P", "/var/run/memcached/memcached.pid"},
			want: "memcached",
		},
	}

	for i, c := range cases {
		got, ok := serviceByCommand(c.in)
		if c.want != "" && got != c.want {
			t.Errorf("serviceByCommand(<case #%d>) == %#v, want %#v", i, got, c.want)
		} else if c.want == "" && ok {
			t.Errorf("serviceByCommand(<case #%d>) == %#v, want nothing", i, got)
		}
	}
}

func TestDynamicDiscoverySimple(t *testing.T) {
	dd := &DynamicDiscovery{
		ps: mockProcess{
			[]facts.Process{
				{
					PID:         1547,
					PPID:        1,
					CreateTime:  time.Now(),
					CmdLine:     []string{"/usr/bin/memcached", "-m", "64", "-p", "11211", "-u", "memcache", "-l", "127.0.0.1", "-P", "/var/run/memcached/memcached.pid"},
					Name:        "memcached",
					MemoryRSS:   0xa88,
					CPUPercent:  0.028360216236998047,
					CPUTime:     98.55000000000001,
					Status:      "S",
					Username:    "memcache",
					Executable:  "",
					ContainerID: "",
				},
			},
		},
		netstat: mockNetstat{result: map[int][]listenAddress{
			1547: {
				{network: "tcp", address: "127.0.0.1:11211"},
			},
		}},
	}
	ctx := context.Background()

	srv, err := dd.Discovery(ctx, 0)
	if err != nil {
		t.Error(err)
	}
	if len(srv) != 1 {
		t.Errorf("len(srv) == %v, want 1", len(srv))
	}
	if srv[0].Name != "memcached" {
		t.Errorf("Name == %#v, want %#v", srv[0].Name, "memcached")
	}
	want := []net.Addr{listenAddress{network: "tcp", address: "127.0.0.1:11211"}}
	if !reflect.DeepEqual(srv[0].ListenAddresses, want) {
		t.Errorf("ListenAddresses == %v, want %v", srv[0].ListenAddresses, want)
	}
}

// Test dynamic Discovery with single process present
func TestDynamicDiscoverySingle(t *testing.T) {

	cases := []struct {
		cmdLine         []string
		containerID     string
		listenAddresses []listenAddress
		want            Service
	}{
		{
			cmdLine:         []string{"/usr/bin/memcached"},
			containerID:     "",
			listenAddresses: []listenAddress{{network: "tcp", address: "0.0.0.0:11211"}},
			want: Service{
				Name:            "memcached",
				ContainerID:     "",
				ListenAddresses: []net.Addr{listenAddress{network: "tcp", address: "0.0.0.0:11211"}},
				IPAddress:       "127.0.0.1",
			},
		},
		{
			cmdLine:         []string{"/usr/bin/memcached"},
			containerID:     "",
			listenAddresses: nil,
			want: Service{
				Name:            "memcached",
				ContainerID:     "",
				ListenAddresses: []net.Addr{listenAddress{network: "tcp", address: "127.0.0.1:11211"}},
				IPAddress:       "127.0.0.1",
			},
		},
		{
			cmdLine:         []string{"/usr/bin/memcached"},
			containerID:     "",
			listenAddresses: []listenAddress{{network: "tcp", address: "192.168.1.1:11211"}},
			want: Service{
				Name:            "memcached",
				ContainerID:     "",
				ListenAddresses: []net.Addr{listenAddress{network: "tcp", address: "192.168.1.1:11211"}},
				IPAddress:       "192.168.1.1",
			},
		},
		{
			cmdLine:         []string{"/usr/sbin/haproxy", "-f", "/etc/haproxy/haproxy.cfg"},
			containerID:     "",
			listenAddresses: []listenAddress{{network: "tcp", address: "0.0.0.0:80"}, {network: "udp", address: "0.0.0.0:42514"}},
			want: Service{
				Name:            "haproxy",
				ContainerID:     "",
				ListenAddresses: []net.Addr{listenAddress{network: "tcp", address: "0.0.0.0:80"}},
				IPAddress:       "127.0.0.1",
			},
		},
	}

	ctx := context.Background()
	for i, c := range cases {
		dd := &DynamicDiscovery{
			ps: mockProcess{
				[]facts.Process{
					{
						PID:         42,
						CmdLine:     c.cmdLine,
						ContainerID: c.containerID,
					},
				},
			},
			netstat: mockNetstat{result: map[int][]listenAddress{
				42: c.listenAddresses,
			}},
		}

		srv, err := dd.Discovery(ctx, 0)
		if err != nil {
			t.Error(err)
		}
		if len(srv) != 1 {
			t.Errorf("Case #%d: len(srv) == %v, want 1", i, len(srv))
		}
		if srv[0].Name != c.want.Name {
			t.Errorf("Case #%d: Name == %#v, want %#v", i, srv[0].Name, c.want.Name)
		}
		if srv[0].ContainerID != c.want.ContainerID {
			t.Errorf("Case #%d: ContainerID == %#v, want %#v", i, srv[0].ContainerID, c.want.ContainerID)
		}
		if srv[0].IPAddress != c.want.IPAddress {
			t.Errorf("Case #%d: IPAddress == %#v, want %#v", i, srv[0].IPAddress, c.want.IPAddress)
		}
		if !reflect.DeepEqual(srv[0].ListenAddresses, c.want.ListenAddresses) {
			t.Errorf("Case #%d: ListenAddresses == %v, want %v", i, srv[0].ListenAddresses, c.want.ListenAddresses)
		}
	}
}
