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

type mockContainerInfo struct {
	containers map[string]mockContainer
}

type mockContainer struct {
	ipAddress       string
	listenAddresses []listenAddress
	env             []string
}

func (mci mockContainerInfo) Container(containerID string) (container container, found bool) {
	c, ok := mci.containers[containerID]
	return c, ok
}

func (mc mockContainer) ListenAddresses() []net.Addr {
	listenAddresses := make([]net.Addr, 0)
	for _, v := range mc.listenAddresses {
		listenAddresses = append(listenAddresses, v)
	}
	return listenAddresses
}

func (mc mockContainer) Env() []string {
	return mc.env
}

func (mc mockContainer) PrimaryAddress() string {
	return mc.ipAddress
}

func (mc mockContainer) Ignored() bool {
	return false
}

func TestServiceByCommand(t *testing.T) {
	cases := []struct {
		in   []string
		want ServiceName
	}{
		{
			in:   []string{"/usr/bin/memcached", "-m", "64", "-p", "11211", "-u", "memcache", "-l", "127.0.0.1", "-P", "/var/run/memcached/memcached.pid"},
			want: MemcachedService,
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
					CmdLineList: []string{"/usr/bin/memcached", "-m", "64", "-p", "11211", "-u", "memcache", "-l", "127.0.0.1", "-P", "/var/run/memcached/memcached.pid"},
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
	if srv[0].Name != MemcachedService {
		t.Errorf("Name == %#v, want %#v", srv[0].Name, MemcachedService)
	}
	want := []net.Addr{listenAddress{network: "tcp", address: "127.0.0.1:11211"}}
	if !reflect.DeepEqual(srv[0].ListenAddresses, want) {
		t.Errorf("ListenAddresses == %v, want %v", srv[0].ListenAddresses, want)
	}
}

// Test dynamic Discovery with single process present
func TestDynamicDiscoverySingle(t *testing.T) {

	cases := []struct {
		testName           string
		cmdLine            []string
		containerID        string
		netstatAddresses   []listenAddress
		containerAddresses []listenAddress
		containerIP        string
		containerEnv       []string
		want               Service
	}{
		{
			testName:         "simple-bind-all",
			cmdLine:          []string{"/usr/bin/memcached"},
			containerID:      "",
			netstatAddresses: []listenAddress{{network: "tcp", address: "0.0.0.0:11211"}},
			want: Service{
				Name:            MemcachedService,
				ContainerID:     "",
				ListenAddresses: []net.Addr{listenAddress{network: "tcp", address: "0.0.0.0:11211"}},
				IPAddress:       "127.0.0.1",
			},
		},
		{
			testName:         "simple-no-netstat",
			cmdLine:          []string{"/usr/bin/memcached"},
			containerID:      "",
			netstatAddresses: nil,
			want: Service{
				Name:            MemcachedService,
				ContainerID:     "",
				ListenAddresses: []net.Addr{listenAddress{network: "tcp", address: "127.0.0.1:11211"}},
				IPAddress:       "127.0.0.1",
			},
		},
		{
			testName:         "simple-bind-specific",
			cmdLine:          []string{"/usr/bin/memcached"},
			containerID:      "",
			netstatAddresses: []listenAddress{{network: "tcp", address: "192.168.1.1:11211"}},
			want: Service{
				Name:            MemcachedService,
				ContainerID:     "",
				ListenAddresses: []net.Addr{listenAddress{network: "tcp", address: "192.168.1.1:11211"}},
				IPAddress:       "192.168.1.1",
			},
		},
		{
			testName:         "ignore-highport",
			cmdLine:          []string{"/usr/sbin/haproxy", "-f", "/etc/haproxy/haproxy.cfg"},
			containerID:      "",
			netstatAddresses: []listenAddress{{network: "tcp", address: "0.0.0.0:80"}, {network: "udp", address: "0.0.0.0:42514"}},
			want: Service{
				Name:            "haproxy",
				ContainerID:     "",
				ListenAddresses: []net.Addr{listenAddress{network: "tcp", address: "0.0.0.0:80"}},
				IPAddress:       "127.0.0.1",
			},
		},
		{
			testName:           "containers",
			cmdLine:            []string{"redis-server *:6379"},
			containerID:        "5b8f83412931055bcc5da35e41ada85fd70015673163d56911cac4fe6693273f",
			netstatAddresses:   nil, // netstat won't provide information
			containerAddresses: []listenAddress{{network: "tcp", address: "172.17.0.49:6379"}},
			containerIP:        "172.17.0.49",
			want: Service{
				Name:            "redis",
				ContainerID:     "5b8f83412931055bcc5da35e41ada85fd70015673163d56911cac4fe6693273f",
				ListenAddresses: []net.Addr{listenAddress{network: "tcp", address: "172.17.0.49:6379"}},
				IPAddress:       "172.17.0.49",
			},
		},
		{
			testName: "java-process",
			cmdLine:  []string{"/opt/jdk-11.0.1/bin/java", "-Xms1g", "-Xmx1g", "-XX:+UseConcMarkSweepGC", "[...]", "/usr/share/elasticsearch/lib/*", "org.elasticsearch.bootstrap.Elasticsearch"},
			want: Service{
				Name:            "elasticsearch",
				ListenAddresses: []net.Addr{listenAddress{network: "tcp", address: "127.0.0.1:9200"}},
				IPAddress:       "127.0.0.1",
			},
		},
		{
			testName:     "mysql-container",
			containerID:  "1234",
			containerIP:  "172.17.0.49",
			cmdLine:      []string{"mysqld"},
			containerEnv: []string{"MYSQL_ROOT_PASSWORD=secret"},
			want: Service{
				Name:            "mysql",
				ContainerID:     "1234",
				ListenAddresses: []net.Addr{listenAddress{network: "tcp", address: "172.17.0.49:3306"}},
				IPAddress:       "172.17.0.49",
				ExtraAttributes: map[string]string{"username": "root", "password": "secret"},
			},
		},
		{
			testName: "erlang-process",
			cmdLine:  []string{"/usr/lib/erlang/erts-9.3.3.3/bin/beam.smp", "-W", "w", "[...]", "-noinput", "-s", "rabbit", "boot", "-sname", "[...]"},
			want: Service{
				Name:            "rabbitmq",
				ListenAddresses: []net.Addr{listenAddress{network: "tcp", address: "127.0.0.1:5672"}},
				IPAddress:       "127.0.0.1",
			},
		},
	}

	ctx := context.Background()
	for _, c := range cases {
		dd := &DynamicDiscovery{
			ps: mockProcess{
				[]facts.Process{
					{
						PID:         42,
						CmdLineList: c.cmdLine,
						ContainerID: c.containerID,
					},
				},
			},
			netstat: mockNetstat{result: map[int][]listenAddress{
				42: c.netstatAddresses,
			}},
			containerInfo: mockContainerInfo{
				containers: map[string]mockContainer{
					c.containerID: {
						ipAddress:       c.containerIP,
						listenAddresses: c.containerAddresses,
						env:             c.containerEnv,
					},
				},
			},
		}

		srv, err := dd.Discovery(ctx, 0)
		if err != nil {
			t.Error(err)
		}
		if len(srv) != 1 {
			t.Errorf("Case %s: len(srv) == %v, want 1", c.testName, len(srv))
		}
		if srv[0].Name != c.want.Name {
			t.Errorf("Case %s: Name == %#v, want %#v", c.testName, srv[0].Name, c.want.Name)
		}
		if srv[0].ContainerID != c.want.ContainerID {
			t.Errorf("Case %s: ContainerID == %#v, want %#v", c.testName, srv[0].ContainerID, c.want.ContainerID)
		}
		if srv[0].IPAddress != c.want.IPAddress {
			t.Errorf("Case %s: IPAddress == %#v, want %#v", c.testName, srv[0].IPAddress, c.want.IPAddress)
		}
		if !reflect.DeepEqual(srv[0].ListenAddresses, c.want.ListenAddresses) {
			t.Errorf("Case %s: ListenAddresses == %v, want %v", c.testName, srv[0].ListenAddresses, c.want.ListenAddresses)
		}
		if c.want.ExtraAttributes == nil {
			c.want.ExtraAttributes = make(map[string]string)
		}
		if !reflect.DeepEqual(srv[0].ExtraAttributes, c.want.ExtraAttributes) {
			t.Errorf("Case %s: ExtraAttributes == %v, want %v", c.testName, srv[0].ExtraAttributes, c.want.ExtraAttributes)
		}
	}
}
