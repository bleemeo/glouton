// Copyright 2015-2025 Bleemeo
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

package discovery

import (
	"context"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/facts"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

type mockProcess struct {
	result []facts.Process
}

func (mp mockProcess) UpdateProcesses(context.Context) (processes map[int]facts.Process, nextUpdate time.Time, err error) {
	return mp.GetLatest(), time.Time{}, nil
}

func (mp mockProcess) GetLatest() map[int]facts.Process {
	m := make(map[int]facts.Process)

	for _, p := range mp.result {
		m[p.PID] = p
	}

	return m
}

type mockNetstat struct {
	result map[int][]facts.ListenAddress
}

func (mn mockNetstat) Netstat(_ context.Context, processes map[int]facts.Process) (netstat map[int][]facts.ListenAddress, err error) {
	_ = processes
	result := make(map[int][]facts.ListenAddress, len(mn.result))

	for pid, l := range mn.result {
		result[pid] = l
	}

	return result, nil
}

type mockContainerInfo struct {
	containers map[string]facts.FakeContainer
}

func (mci mockContainerInfo) CachedContainer(containerID string) (container facts.Container, found bool) {
	c, ok := mci.containers[containerID]

	return c, ok
}

func (mci mockContainerInfo) Containers(_ context.Context, maxAge time.Duration, includeIgnored bool) (containers []facts.Container, err error) {
	_ = maxAge
	_ = includeIgnored
	res := make([]facts.Container, 0, len(mci.containers))

	for _, v := range mci.containers {
		res = append(res, v)
	}

	return res, nil
}

type mockFileReader struct {
	contents map[string]string
}

func (mfr mockFileReader) ReadFile(_ context.Context, path string) ([]byte, error) {
	content, ok := mfr.contents[path]
	if !ok {
		return nil, os.ErrNotExist
	}

	return []byte(content), nil
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
	t0 := time.Now()

	dd := NewDynamic(Option{
		PS: mockProcess{
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
		Netstat: mockNetstat{result: map[int][]facts.ListenAddress{
			1547: {
				{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 11211},
			},
		}},
		ContainerInfo:      mockContainerInfo{},
		IsContainerIgnored: facts.ContainerFilter{}.ContainerIgnored,
	})
	dd.now = func() time.Time { return t0 }

	ctx := t.Context()

	srv, _, err := dd.Discovery(ctx)
	if err != nil {
		t.Error(err)
	}

	if len(srv) != 1 {
		t.Errorf("len(srv) == %v, want 1", len(srv))
	}

	if srv[0].Name != "memcached" {
		t.Errorf("Name == %#v, want %#v", srv[0].Name, "memcached")
	}

	if srv[0].ServiceType != MemcachedService {
		t.Errorf("Name == %#v, want %#v", srv[0].ServiceType, MemcachedService)
	}

	want := []facts.ListenAddress{{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 11211}}
	if !reflect.DeepEqual(srv[0].ListenAddresses, want) {
		t.Errorf("ListenAddresses == %v, want %v", srv[0].ListenAddresses, want)
	}
}

// Test dynamic Discovery with single process present
// To extract cmdLine array from a running process, one can read /proc/PID/cmdline using "less".
// Less will show the NUL character used to split args.
func TestDynamicDiscoverySingle(t *testing.T) { //nolint:maintidx
	t0 := time.Now()

	cases := []struct {
		testName           string
		cmdLine            []string
		filesContent       map[string]string
		containerID        string
		netstatAddresses   []facts.ListenAddress
		containerAddresses []facts.ListenAddress
		containerIP        string
		containerName      string
		containerEnv       map[string]string
		containerLabels    map[string]string
		want               Service
		noMatch            bool
	}{
		{
			testName:         "simple-bind-all",
			cmdLine:          []string{"/usr/bin/memcached"},
			containerID:      "",
			netstatAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "0.0.0.0", Port: 11211}},
			want: Service{
				Name:            "memcached",
				ServiceType:     MemcachedService,
				ContainerID:     "",
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "0.0.0.0", Port: 11211}},
				IPAddress:       "127.0.0.1",
				Active:          true,
				HasNetstatInfo:  true,
				LastNetstatInfo: t0,
				LastTimeSeen:    t0,
			},
		},
		{
			testName:         "simple-no-netstat",
			cmdLine:          []string{"/usr/bin/memcached"},
			containerID:      "",
			netstatAddresses: nil,
			want: Service{
				Name:            "memcached",
				ServiceType:     MemcachedService,
				ContainerID:     "",
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 11211}},
				IPAddress:       "127.0.0.1",
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		{
			testName:         "simple-bind-specific",
			cmdLine:          []string{"/usr/bin/memcached"},
			containerID:      "",
			netstatAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "192.168.1.1", Port: 11211}},
			want: Service{
				Name:            "memcached",
				ServiceType:     MemcachedService,
				ContainerID:     "",
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "192.168.1.1", Port: 11211}},
				IPAddress:       "192.168.1.1",
				Active:          true,
				HasNetstatInfo:  true,
				LastNetstatInfo: t0,
				LastTimeSeen:    t0,
			},
		},
		{
			testName:         "ignore-highport",
			cmdLine:          []string{"/usr/sbin/haproxy", "-f", "/etc/haproxy/haproxy.cfg"},
			containerID:      "",
			netstatAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "0.0.0.0", Port: 80}, {NetworkFamily: "udp", Address: "0.0.0.0", Port: 42514}},
			want: Service{
				Name:            "haproxy",
				ServiceType:     HAProxyService,
				ContainerID:     "",
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "0.0.0.0", Port: 80}},
				IPAddress:       "127.0.0.1",
				Active:          true,
				HasNetstatInfo:  true,
				LastNetstatInfo: t0,
				LastTimeSeen:    t0,
			},
		},
		{
			testName:           "redis-container",
			cmdLine:            []string{"redis-server *:6379"},
			containerID:        "5b8f83412931055bcc5da35e41ada85fd70015673163d56911cac4fe6693273f",
			netstatAddresses:   nil, // netstat won't provide information
			containerAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "172.17.0.49", Port: 6379}},
			containerIP:        "172.17.0.49",
			want: Service{
				Name:            "redis",
				ServiceType:     RedisService,
				ContainerID:     "5b8f83412931055bcc5da35e41ada85fd70015673163d56911cac4fe6693273f",
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "172.17.0.49", Port: 6379}},
				IPAddress:       "172.17.0.49",
				IgnoredPorts:    map[int]bool{},
				Active:          true,
				HasNetstatInfo:  true,
				LastNetstatInfo: t0,
				LastTimeSeen:    t0,
			},
		},
		{
			testName: "elasticsearch",
			cmdLine:  []string{"/opt/jdk-11.0.1/bin/java", "-Xms1g", "-Xmx1g", "-XX:+UseConcMarkSweepGC", "[...]", "/usr/share/elasticsearch/lib/*", "org.elasticsearch.bootstrap.Elasticsearch"},
			want: Service{
				Name:            "elasticsearch",
				ServiceType:     ElasticSearchService,
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 9200}},
				IPAddress:       "127.0.0.1",
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		{
			testName:     "mysql-container",
			containerID:  "1234",
			containerIP:  "172.17.0.49",
			cmdLine:      []string{"mysqld"},
			containerEnv: map[string]string{"MYSQL_ROOT_PASSWORD": "secret"},
			want: Service{
				Name:            "mysql",
				ServiceType:     MySQLService,
				ContainerID:     "1234",
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "172.17.0.49", Port: 3306}},
				IPAddress:       "172.17.0.49",
				Config: config.Service{
					Username: mysqlDefaultUser,
					Password: "secret",
				},
				IgnoredPorts: map[int]bool{},
				Active:       true,
				LastTimeSeen: t0,
			},
		},
		{
			testName: "mysql-host",
			cmdLine:  []string{"mysqld"},
			filesContent: map[string]string{
				"/etc/mysql/debian.cnf": "[client]\nuser   = root\npassword    = secret\n",
			},
			want: Service{
				Name:            "mysql",
				ServiceType:     MySQLService,
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 3306}},
				IPAddress:       "127.0.0.1",
				Config: config.Service{
					Username:          "root",
					Password:          "secret",
					MetricsUnixSocket: "",
				},
				Active:       true,
				LastTimeSeen: t0,
			},
		},
		{
			testName: "mysql-host2",
			cmdLine:  []string{"mysqld"},
			filesContent: map[string]string{
				"/etc/mysql/debian.cnf": "[client]\nuser   = root\npassword    = secret\nsocket = /tmp/file.sock\n",
			},
			want: Service{
				Name:            "mysql",
				ServiceType:     MySQLService,
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 3306}},
				IPAddress:       "127.0.0.1",
				Config: config.Service{
					Username:          "root",
					Password:          "secret",
					MetricsUnixSocket: "/tmp/file.sock",
				},
				Active:       true,
				LastTimeSeen: t0,
			},
		},
		{
			testName: "rabbitmq",
			cmdLine:  []string{"/usr/lib/erlang/erts-9.3.3.3/bin/beam.smp", "-W", "w", "[...]", "-noinput", "-s", "rabbit", "boot", "-sname", "[...]"},
			want: Service{
				Name:            "rabbitmq",
				ServiceType:     RabbitMQService,
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 5672}},
				IPAddress:       "127.0.0.1",
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		{
			testName: "influxdb.deb",
			cmdLine:  []string{"/opt/influxdb/influxd", "-config", "/etc/opt/influxdb/influxdb.conf"},
			want: Service{
				Name:            "influxdb",
				ServiceType:     InfluxDBService,
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 8086}},
				IPAddress:       "127.0.0.1",
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		// Service from Ubuntu 16.04, default config
		{
			testName: "mysql-ubuntu-14.04",
			cmdLine:  []string{"/usr/sbin/mysqld"},
			want: Service{
				Name:            "mysql",
				ServiceType:     MySQLService,
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 3306}},
				IPAddress:       "127.0.0.1",
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		{
			testName: "ntp-ubuntu-14.04",
			cmdLine:  []string{"/usr/sbin/ntpd", "-p", "/var/run/ntpd.pid", "-g", "-u", "107:114"},
			want: Service{
				Name:            "ntp",
				ServiceType:     NTPService,
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: "udp", Address: "127.0.0.1", Port: 123}},
				IPAddress:       "127.0.0.1",
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		{
			testName: "slapd-ubuntu-14.04",
			cmdLine:  []string{"/usr/sbin/slapd", "-h", "ldap:/// ldapi:///", "-g", "openldap", "-u", "openldap", "-F", "/etc/ldap/slapd.d"},
			want: Service{
				Name:            "openldap",
				ServiceType:     OpenLDAPService,
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 389}},
				IPAddress:       "127.0.0.1",
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		{
			testName: "apache2-ubuntu-14.04",
			cmdLine:  []string{"/usr/sbin/apache2", "-k", "start"},
			want: Service{
				Name:            "apache",
				ServiceType:     ApacheService,
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 80}},
				IPAddress:       "127.0.0.1",
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		{
			testName: "asterisk-ubuntu-14.04",
			cmdLine:  []string{"/usr/sbin/asterisk", "-p", "-U", "asterisk"},
			want: Service{
				Name:            "asterisk",
				ServiceType:     AsteriskService,
				ListenAddresses: nil,
				IPAddress:       "127.0.0.1",
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		{
			testName: "bind-ubuntu-14.04",
			cmdLine:  []string{"/usr/sbin/named", "-u", "bind"},
			want: Service{
				Name:            "bind",
				ServiceType:     BindService,
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 53}},
				IPAddress:       "127.0.0.1",
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		{
			testName: "dovecot-ubuntu-14.04",
			cmdLine:  []string{"/usr/sbin/dovecot", "-F", "-c", "/etc/dovecot/dovecot.conf"},
			want: Service{
				Name:            "dovecot",
				ServiceType:     DovecotService,
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 143}},
				IPAddress:       "127.0.0.1",
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		{
			testName: "ejabberd-ubuntu-14.04",
			cmdLine: []string{
				"/usr/lib/erlang/erts-5.10.4/bin/beam", "-K", "false", "-P", "250000", "--", "-root", "/usr/lib/erlang", "-progname", "erl", "--", "-home", "/var/lib/ejabberd", "--", "-sname",
				"ejabberd", "-pa", "/usr/lib/ejabberd/ebin", "-s", "ejabberd", "-kernel", "inetrc", "\"/etc/ejabberd/inetrc\"", "-ejabberd", "config", "\"/etc/ejabberd/ejabberd.cfg\"", "log_path",
				"\"/var/log/ejabberd/ejabberd.log\"", "erlang_log_path", "\"/var/log/ejabberd/erlang.log\"", "-sasl", "sasl_error_logger", "false", "-mnesia", "dir", "\"/var/lib/ejabberd\"",
				"-smp", "disable", "-noshell", "-noshell", "-noinput",
			},
			want: Service{
				Name:            "ejabberd",
				ServiceType:     EjabberService,
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 5222}},
				IPAddress:       "127.0.0.1",
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		{
			testName: "rabbitmq-ubuntu-14.04",
			cmdLine: []string{
				"/usr/lib/erlang/erts-5.10.4/bin/beam", "-W", "w", "-K", "true", "-A30", "-P", "1048576", "--", "-root", "/usr/lib/erlang", "-progname", "erl", "--", "-home", "/var/lib/rabbitmq",
				"--", "-pa", "/usr/lib/rabbitmq/lib/rabbitmq_server-3.2.4/sbin/../ebin", "-noshell", "-noinput", "-s", "rabbit", "boot", "-sname", "rabbit@trusty", "-boot", "start_sasl", "-kernel",
				"inet_default_connect_options", "[{nodelay,true}]", "-sasl", "errlog_type", "error", "-sasl", "sasl_error_logger", "false", "-rabbit", "error_logger",
				"{file,\"/var/log/rabbitmq/rabbit@trusty.log\"}", "-rabbit", "sasl_error_logger", "{file,\"/var/log/rabbitmq/rabbit@trusty-sasl.log\"}", "-rabbit", "enabled_plugins_file",
				"\"/etc/rabbitmq/enabled_plugins\"", "-rabbit", "plugins_dir", "\"/usr/lib/rabbitmq/lib/rabbitmq_server-3.2.4/sbin/../plugins\"", "-rabbit", "plugins_expand_dir",
				"\"/var/lib/rabbitmq/mnesia/rabbit@trusty-plugins-expand\"", "-os_mon", "start_cpu_sup", "false", "-os_mon", "start_disksup", "false", "-os_mon", "start_memsup", "false", "-mnesia",
				"dir", "\"/var/lib/rabbitmq/mnesia/rabbit@trusty\"",
			},
			want: Service{
				Name:            "rabbitmq",
				ServiceType:     RabbitMQService,
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 5672}},
				IPAddress:       "127.0.0.1",
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		{
			testName: "mongodb-ubuntu-14.04",
			cmdLine:  []string{"/usr/bin/mongod", "--config", "/etc/mongodb.conf"},
			want: Service{
				Name:            "mongodb",
				ServiceType:     MongoDBService,
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 27017}},
				IPAddress:       "127.0.0.1",
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		{
			testName: "mosquitto-ubuntu-14.04",                                               //nolint:misspell
			cmdLine:  []string{"/usr/sbin/mosquitto", "-c", "/etc/mosquitto/mosquitto.conf"}, //nolint:misspell
			want: Service{
				Name:            "mosquitto", //nolint:misspell
				ServiceType:     MosquittoService,
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 1883}},
				IPAddress:       "127.0.0.1",
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		{
			testName: "redis-ubuntu-14.04",
			cmdLine:  []string{"/usr/bin/redis-server 127.0.0.1:6379"},
			want: Service{
				Name:            "redis",
				ServiceType:     RedisService,
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 6379}},
				IPAddress:       "127.0.0.1",
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		{
			testName: "memcached-ubuntu-14.04",
			cmdLine:  []string{"/usr/bin/memcached", "-", "64", "-p", "11211", "-u", "memcache", "-l", "127.0.0.1"},
			want: Service{
				Name:            "memcached",
				ServiceType:     MemcachedService,
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 11211}},
				IPAddress:       "127.0.0.1",
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		{
			testName: "squid-ubuntu-14.04",
			cmdLine:  []string{"/usr/sbin/squid3", "-N", "-YC", "-f", "/etc/squid3/squid.conf"},
			want: Service{
				Name:            "squid",
				ServiceType:     SquidService,
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 3128}},
				IPAddress:       "127.0.0.1",
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		{
			testName: "postgresql-ubuntu-14.04",
			cmdLine:  []string{"/usr/lib/postgresql/9.3/bin/postgres", "-D", "/var/lib/postgresql/9.3/main", "-c", "config_file=/etc/postgresql/9.3/main/postgresql.conf"},
			want: Service{
				Name:            "postgresql",
				ServiceType:     PostgreSQLService,
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 5432}},
				IPAddress:       "127.0.0.1",
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		{
			testName: "zookeeper-ubuntu-14.04",
			cmdLine: []string{
				"/usr/bin/java", "-cp",
				"/etc/zookeeper/conf:/usr/share/java/jline.jar:/usr/share/java/log4j-1.2.jar:/usr/share/java/xercesImpl.jar:/usr/share/java/xmlParserAPIs.jar:/usr/share/java/netty.jar:/usr/share/java/slf4j-api.jar:/usr/share/java/slf4j-log4j12.jar:/usr/share/java/zookeeper.jar",
				"-Dcom.sun.management.jmxremote", "-Dcom.sun.management.jmxremote.local.only=false", "-Dzookeeper.log.dir=/var/log/zookeeper", "-Dzookeeper.root.logger=INFO,ROLLINGFILE",
				"org.apache.zookeeper.server.quorum.QuorumPeerMain", "/etc/zookeeper/conf/zoo.cfg",
			},
			want: Service{
				Name:            "zookeeper",
				ServiceType:     ZookeeperService,
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 2181}},
				IPAddress:       "127.0.0.1",
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		{
			testName: "salt-master-ubuntu-14.04",
			cmdLine:  []string{"/usr/bin/python", "/usr/bin/salt-master"},
			want: Service{
				Name:            "salt_master",
				ServiceType:     SaltMasterService,
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 4505}},
				IPAddress:       "127.0.0.1",
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		{
			testName: "postfix-ubuntu-14.04",
			cmdLine:  []string{"/usr/lib/postfix/master"},
			want: Service{
				Name:            "postfix",
				ServiceType:     PostfixService,
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 25}},
				IPAddress:       "127.0.0.1",
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		{
			testName: "nginx-ubuntu-14.04",
			cmdLine:  []string{"nginx: master process /usr/sbin/nginx"},
			want: Service{
				Name:            "nginx",
				ServiceType:     NginxService,
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 80}},
				IPAddress:       "127.0.0.1",
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		{
			testName: "exim-ubuntu-14.04",
			cmdLine:  []string{"/usr/sbin/exim4", "-bd", "-q30m"},
			want: Service{
				Name:            "exim",
				ServiceType:     EximService,
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 25}},
				IPAddress:       "127.0.0.1",
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		{
			testName: "freeradius-ubuntu-14.04",
			cmdLine:  []string{"/usr/sbin/freeradius", "-f"},
			want: Service{
				Name:            "freeradius",
				ServiceType:     FreeradiusService,
				ListenAddresses: nil,
				IPAddress:       "127.0.0.1",
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		{
			testName: "varnish-ubuntu-14.04",
			cmdLine:  []string{"/usr/sbin/varnishd", "-P", "/var/run/varnishd.pid", "-a", ":6081", "-T", "localhost:6082", "-f", "/etc/varnish/default.vcl", "-S", "/etc/varnish/secret", "-s", "malloc,256m"},
			want: Service{
				Name:            "varnish",
				ServiceType:     VarnishService,
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 6082}},
				IPAddress:       "127.0.0.1",
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		// Service from Ubuntu 16.04, default config
		{
			testName: "elasticsearch-ubuntu-16.04",
			cmdLine: []string{
				"/usr/lib/jvm/java-8-openjdk-amd64/bin/java", "-Xms256m", "-Xmx1g", "-Djava.awt.headless=true", "-XX:+UseParNewGC", "-XX:+UseConcMarkSweepGC", "-XX:CMSInitiatingOccupancyFraction=75",
				"-XX:+UseCMSInitiatingOccupancyOnly", "-XX:+HeapDumpOnOutOfMemoryError", "-XX:+DisableExplicitGC", "-Dfile.encoding=UTF-8", "-Delasticsearch", "-Des.pidfile=/var/run/elasticsearch.pid",
				"-Des.path.home=/usr/share/elasticsearch", "-cp",
				":/usr/share/java/lucene-sandbox-4.10.4.jar:/usr/share/java/sigar.jar:/usr/share/java/lucene-analyzers-morfologik-4.10.4.jar:/usr/share/java/spatial4j-0.4.1.jar:/usr/share/java/lucene-expressions-4.10.4.jar:/usr/share/java/lucene-analyzers-uima-4.10.4.jar:/usr/share/java/groovy-all-2.x.jar:/usr/share/java/lucene-analyzers-kuromoji-4.10.4.jar:/usr/share/java/lucene-facet-4.10.4.jar:/usr/share/java/jna.jar:/usr/share/java/lucene-analyzers-common-4.10.4.jar:/usr/share/java/lucene-core-4.10.4.jar:/usr/share/java/apache-log4j-extras-1.2.17.jar:/usr/share/java/lucene-queries-4.10.4.jar:/usr/share/java/lucene-demo-4.10.4.jar:/usr/share/java/lucene-suggest-4.10.4.jar:/usr/share/java/lucene-analyzers-stempel-4.10.4.jar:/usr/share/java/lucene-highlighter-4.10.4.jar:/usr/share/java/lucene-memory-4.10.4.jar:/usr/share/java/lucene-classification-4.10.4.jar:/usr/share/java/lucene-replicator-4.10.4.jar:/usr/share/java/lucene-grouping-4.10.4.jar:/usr/share/java/log4j-1.2-1.2.17.jar:/usr/share/java/lucene-join-4.10.4.jar:/usr/share/java/lucene-analyzers-smartcn-4.10.4.jar:/usr/share/java/lucene-spatial-4.10.4.jar:/usr/share/java/elasticsearch-1.7.3.jar:/usr/share/java/lucene-codecs-4.10.4.jar:/usr/share/java/lucene-misc-4.10.4.jar:/usr/share/java/lucene-queryparser-4.10.4.jar:/usr/share/java/lucene-test-framework-4.10.4.jar:/usr/share/java/jts.jar:/usr/share/java/lucene-benchmark-4.10.4.jar:/usr/share/java/lucene-analyzers-icu-4.10.4.jar:/usr/share/java/lucene-analyzers-phonetic-4.10.4.jar:",
				"-Des.default.config=/etc/elasticsearch/elasticsearch.yml", "-Des.default.path.home=/usr/share/elasticsearch", "-Des.default.path.logs=/var/log/elasticsearch",
				"-Des.default.path.data=/var/lib/elasticsearch", "-Des.default.path.work=/tmp/elasticsearch", "-Des.default.path.conf=/etc/elasticsearch", "org.elasticsearch.bootstrap.Elasticsearch",
			},
			want: Service{
				Name:            "elasticsearch",
				ServiceType:     ElasticSearchService,
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 9200}},
				IPAddress:       "127.0.0.1",
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		{
			testName: "squid-ubuntu-16.04",
			cmdLine:  []string{"/usr/sbin/squid", "-YC", "-f", "/etc/squid/squid.conf"},
			want: Service{
				Name:            "squid",
				ServiceType:     SquidService,
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 3128}},
				IPAddress:       "127.0.0.1",
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		{
			testName: "openvpn",
			cmdLine:  []string{"/usr/sbin/openvpn", "--writepid", "/run/openvpn/server.pid", "--daemon", "ovpn-server", "--cd", "/etc/openvpn", "--config", "/etc/openvpn/server.conf", "--script-security", "2"},
			want: Service{
				Name:            "openvpn",
				ServiceType:     OpenVPNService,
				ListenAddresses: nil,
				IPAddress:       "127.0.0.1",
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		{
			testName: "libvirt",
			cmdLine:  []string{"/usr/sbin/libvirtd", "-d"},
			want: Service{
				Name:            "libvirt",
				ServiceType:     LibvirtService,
				ListenAddresses: nil,
				IPAddress:       "127.0.0.1",
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		{
			testName: "haproxy",
			cmdLine:  []string{"haproxy", "-f", "/usr/local/etc/haproxy/haproxy.cfg"},
			want: Service{
				Name:            "haproxy",
				ServiceType:     HAProxyService,
				ListenAddresses: nil,
				IPAddress:       "127.0.0.1",
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		{
			testName: "uwsgi",
			cmdLine:  []string{"uwsgi", "--ini", "/srv/app/deploy/uwsgi.ini"},
			want: Service{
				Name:            "uwsgi",
				ServiceType:     UWSGIService,
				ListenAddresses: nil,
				IPAddress:       "127.0.0.1",
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		{
			testName: "uwsgi-with-auto-procname-1",
			cmdLine:  []string{"uWSGI master"},
			want: Service{
				Name:            "uwsgi",
				ServiceType:     UWSGIService,
				ListenAddresses: nil,
				IPAddress:       "127.0.0.1",
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		{
			testName: "uwsgi-with-auto-procname-2",
			cmdLine:  []string{"uWSGI worker 1"},
			want: Service{
				Name:            "uwsgi",
				ServiceType:     UWSGIService,
				ListenAddresses: nil,
				IPAddress:       "127.0.0.1",
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		{
			testName: "service-install",
			cmdLine:  []string{"apt", "install", "apache2", "redis-server", "postgresql", "mosquitto", "slapd", "squid3"}, //nolint:misspell
			noMatch:  true,
		},
		{
			testName: "docker-run",
			cmdLine:  []string{"docker", "run", "-d", "--name", "mysqld", "mysql"},
			noMatch:  true,
		},
		{
			testName: "random-java",
			cmdLine:  []string{"/usr/bin/java", "com.example.HelloWorld"},
			noMatch:  true,
		},
		{
			testName: "random-python",
			cmdLine:  []string{"/usr/bin/python", "random_script.py"},
			noMatch:  true,
		},
		{
			testName: "random-erlang",
			cmdLine:  []string{"/usr/lib/erlang/erts-6.2/bin/beam", "--", "-root", "/usr/lib/erlang", "-progname", "erl", "--", "-home", "/root", "--"},
			noMatch:  true,
		},
		{
			testName: "mysql-with-space",
			cmdLine:  []string{"/opt/program files/mysql/mysqld"},
			want: Service{
				Name:            "mysql",
				ServiceType:     MySQLService,
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 3306}},
				IPAddress:       "127.0.0.1",
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		{
			testName: "php-fpm",
			cmdLine:  []string{"php-fpm: master process (/etc/php/7.0/fpm/php-fpm.conf)"},
			want: Service{
				Name:            "phpfpm",
				ServiceType:     PHPFPMService,
				ListenAddresses: nil,
				IPAddress:       "127.0.0.1",
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		{
			testName: "chrome",
			cmdLine:  []string{"/opt/google/chrome/chrome", "http://127.0.0.1:5000/"},
			noMatch:  true,
		},
		{
			testName: "chrome2",
			cmdLine:  []string{"/opt/ google/chrome/chrome", "http://127.0.0.1:5000/"},
			noMatch:  true,
		},
		{
			testName:    "nginx-docker-net-host",
			containerID: "817ec63d4b4f9e28947a323f9fbfc4596500b42c842bf07bd6ad9641e6805cb5",
			containerIP: "127.0.0.1",
			cmdLine:     []string{"nginx: master process nginx -g daemon off;"},
			want: Service{
				Name:            "nginx",
				ServiceType:     NginxService,
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 80}},
				IPAddress:       "127.0.0.1",
				ContainerID:     "817ec63d4b4f9e28947a323f9fbfc4596500b42c842bf07bd6ad9641e6805cb5",
				IgnoredPorts:    map[int]bool{},
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		{
			testName: "bitbucket",
			cmdLine: []string{
				"/usr/lib/jvm/java-11-openjdk-amd64//bin/java", "-classpath /srv/bitbucket/dist/current/app", "-Datlassian.plugins.enable.wait=300", "-Datlassian.standalone=BITBUCKET",
				"-Dbitbucket.home=/srv/bitbucket/home", "-Dbitbucket.install=/srv/bitbucket/dist/current", "-Xms512m", "-Xmx1g", "-XX:+UseG1GC", "-Dfile.encoding=UTF-8", "-Dsun.jnu.encoding=UTF-8",
				"-Djava.io.tmpdir=/srv/bitbucket/home/tmp", "-Djava.library.path=/srv/bitbucket/dist/current/lib/native;/srv/bitbucket/home/lib/native",
				"com.atlassian.bitbucket.internal.launcher.BitbucketServerLauncher", "start",
			},
			containerID:      "",
			netstatAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "0.0.0.0", Port: 7990}},
			want: Service{
				Name:            "bitbucket",
				ServiceType:     BitBucketService,
				ContainerID:     "",
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "0.0.0.0", Port: 7990}},
				IPAddress:       "127.0.0.1",
				IgnoredPorts: map[int]bool{
					5701: true,
				},
				Active:          true,
				HasNetstatInfo:  true,
				LastNetstatInfo: t0,
				LastTimeSeen:    t0,
			},
		},
		{
			testName: "bitbucket-extra-ignore-port",
			cmdLine: []string{
				"/usr/lib/jvm/java-11-openjdk-amd64//bin/java", "-classpath /srv/bitbucket/dist/current/app", "-Datlassian.plugins.enable.wait=300", "-Datlassian.standalone=BITBUCKET",
				"-Dbitbucket.home=/srv/bitbucket/home", "-Dbitbucket.install=/srv/bitbucket/dist/current", "-Xms512m", "-Xmx1g", "-XX:+UseG1GC", "-Dfile.encoding=UTF-8", "-Dsun.jnu.encoding=UTF-8",
				"-Djava.io.tmpdir=/srv/bitbucket/home/tmp", "-Djava.library.path=/srv/bitbucket/dist/current/lib/native;/srv/bitbucket/home/lib/native",
				"com.atlassian.bitbucket.internal.launcher.BitbucketServerLauncher", "start",
			},
			containerID: "1234",
			containerIP: "127.0.0.1",
			containerLabels: map[string]string{
				"glouton.check.ignore.port.8080": "true",
			},
			netstatAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 7990}},
			want: Service{
				Name:            "bitbucket",
				ServiceType:     BitBucketService,
				ContainerID:     "1234",
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 7990}},
				IPAddress:       "127.0.0.1",
				IgnoredPorts: map[int]bool{
					5701: true,
					8080: true,
				},
				Active:          true,
				HasNetstatInfo:  true,
				LastNetstatInfo: t0,
				LastTimeSeen:    t0,
			},
		},
		{
			testName: "kafka",
			cmdLine: []string{
				"/usr/local/openjdk-11/bin/java", "-Xmx1G", "-Xms1G", "-server", "-XX:+UseG1GC", "-XX:MaxGCPauseMillis=20",
				"-XX:+ExplicitGCInvokesConcurrent", "-XX:MaxInlineLevel=15", "-Djava.awt.headless=true",
				"-Xlog:gc*:file=/opt/kafka/bin/../logs/kafkaServer-gc.log:time,tags:filecount=10,filesize=100M",
				"-Dcom.sun.management.jmxremote", "-Dcom.sun.management.jmxremote.authenticate=false", "-Dcom.sun.management.jmxremote.ssl=false",
				"-Djava.rmi.server.hostname=localhost", "-Dcom.sun.management.jmxremote.rmi.port=1099", "-Dcom.sun.management.jmxremote.port=1099",
				"-Dkafka.logs.dir=/opt/kafka/bin/../logs", "-Dlog4j.configuration=file:/opt/kafka/bin/../config/log4j.properties",
				"-cp", "/opt/kafka/bin/../libs/activation-1.1.1.jar:/opt/kafka/bin/../libs/aopalliance-repackaged-2.6.1.jar",
				"kafka.Kafka", "/opt/kafka/config/server.properties",
			},
			containerID:      "1234",
			containerIP:      "127.0.0.1",
			netstatAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 9092}},
			want: Service{
				Name:            "kafka",
				ServiceType:     KafkaService,
				ContainerID:     "1234",
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 9092}},
				IPAddress:       "127.0.0.1",
				IgnoredPorts:    map[int]bool{},
				Active:          true,
				HasNetstatInfo:  true,
				LastNetstatInfo: t0,
				LastTimeSeen:    t0,
				Config: config.Service{
					JMXPort: 1099,
				},
			},
		},
		{
			testName:         "not kafka",
			cmdLine:          []string{"java", "-dconf.kafka.Kafka.address=1.2.3.4", "com.bleemeo.myapp"},
			containerID:      "1234",
			containerIP:      "127.0.0.1",
			netstatAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 9092}},
			noMatch:          true,
		},
		{
			testName:         "nats",
			cmdLine:          []string{"/nats-server", "--config nats-server.conf"},
			containerID:      "1234",
			containerIP:      "127.0.0.1",
			netstatAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 4222}},
			want: Service{
				Name:            "nats",
				ServiceType:     NatsService,
				ContainerID:     "1234",
				IgnoredPorts:    map[int]bool{},
				IPAddress:       "127.0.0.1",
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 4222}},
				Active:          true,
				HasNetstatInfo:  true,
				LastNetstatInfo: t0,
				LastTimeSeen:    t0,
			},
		},
		{
			testName: "fail2ban",
			cmdLine:  []string{"/usr/bin/python3", "/usr/bin/fail2ban-server", "-xf", "start"},
			want: Service{
				Name:         "fail2ban",
				ServiceType:  Fail2banService,
				IPAddress:    "127.0.0.1",
				Active:       true,
				LastTimeSeen: t0,
			},
		},
		{
			testName: "nfsclient",
			cmdLine:  []string{"nfsiod"},
			want: Service{
				Name:            "nfs",
				ServiceType:     NfsService,
				ListenAddresses: nil,
				IPAddress:       "127.0.0.1",
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		{
			testName: "jenkins",
			cmdLine: []string{
				"java", "-Duser.home=/var/jenkins_home", "-Djenkins.model.Jenkins.slaveAgentPort=50000",
				"-Dhudson.lifecycle=hudson.lifecycle.ExitLifecycle", "-jar", "/usr/share/jenkins/jenkins.war",
			},
			want: Service{
				Name:            "jenkins",
				ServiceType:     JenkinsService,
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 8080}},
				IPAddress:       "127.0.0.1",
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		{
			testName: "upsd",
			cmdLine:  []string{"/lib/nut/upsd"},
			want: Service{
				Name:            "upsd",
				ServiceType:     UPSDService,
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 3493}},
				IPAddress:       "127.0.0.1",
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		{
			testName:         "nginx-alternative-port-ignore",
			cmdLine:          []string{"nginx: master process /usr/sbin/nginx"},
			containerID:      "817ec63d4b4f9e28947a323f9fbfc4596500b42c842bf07bd6ad9641e6805cb5",
			containerIP:      "172.16.0.2",
			netstatAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "172.16.0.2", Port: 80}},
			containerLabels: map[string]string{
				"glouton.check.ignore.port.80": "true",
			},
			want: Service{
				Name:        "nginx",
				ServiceType: NginxService,
				ContainerID: "817ec63d4b4f9e28947a323f9fbfc4596500b42c842bf07bd6ad9641e6805cb5",
				// It's not applyOverride which remove ignored ports, but we are in ignored ports
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "172.16.0.2", Port: 80}},
				IPAddress:       "172.16.0.2",
				IgnoredPorts: map[int]bool{
					80: true,
				},
				Active:          true,
				HasNetstatInfo:  true,
				LastNetstatInfo: t0,
				LastTimeSeen:    t0,
			},
		},
		{
			testName:         "nginx-alternative-port-ignore2",
			cmdLine:          []string{"nginx: master process /usr/sbin/nginx"},
			containerID:      "817ec63d4b4f9e28947a323f9fbfc4596500b42c842bf07bd6ad9641e6805cb5",
			containerName:    "nginx_port_alt",
			containerIP:      "172.16.0.2",
			netstatAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "172.16.0.2", Port: 80}},
			containerLabels: map[string]string{
				"glouton.ignore_ports": "74,75,80",
			},
			want: Service{
				Name:          "nginx",
				Instance:      "nginx_port_alt",
				ServiceType:   NginxService,
				ContainerID:   "817ec63d4b4f9e28947a323f9fbfc4596500b42c842bf07bd6ad9641e6805cb5",
				ContainerName: "nginx_port_alt",
				// It's not applyOverride which remove ignored ports, but we are in ignored ports
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "172.16.0.2", Port: 80}},
				IPAddress:       "172.16.0.2",
				// This test is done in two path. In dynamic.go we add the option to Config.
				// in discovery (applyOverrideInPlance) we apply the config override.
				// IgnoredPorts: map[int]bool{
				//	74: true,
				//	75: true,
				//	80: true,
				// },
				Active:          true,
				HasNetstatInfo:  true,
				LastNetstatInfo: t0,
				Config: config.Service{
					IgnorePorts: []int{74, 75, 80},
				},
				LastTimeSeen: t0,
			},
		},
		{
			testName:         "nginx-alternative-port-update",
			cmdLine:          []string{"nginx: master process /usr/sbin/nginx"},
			containerID:      "817ec63d4b4f9e28947a323f9fbfc4596500b42c842bf07bd6ad9641e6805cb5",
			containerName:    "nginx_port_alt",
			containerIP:      "172.16.0.2",
			netstatAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "172.16.0.2", Port: 80}},
			containerLabels: map[string]string{
				"glouton.port": "8080",
			},
			want: Service{
				Name:          "nginx",
				Instance:      "nginx_port_alt",
				ServiceType:   NginxService,
				ContainerID:   "817ec63d4b4f9e28947a323f9fbfc4596500b42c842bf07bd6ad9641e6805cb5",
				ContainerName: "nginx_port_alt",
				// This test is done in two path. In dynamic.go we add the option to Config.
				// in discovery (applyOverrideInPlance) we apply the config override.
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "172.16.0.2", Port: 80}},
				IPAddress:       "172.16.0.2",
				Active:          true,
				HasNetstatInfo:  true,
				LastNetstatInfo: t0,
				Config: config.Service{
					Port: 8080,
				},
				LastTimeSeen: t0,
			},
		},
	}

	ctx := t.Context()

	for _, c := range cases {
		dd := NewDynamic(Option{
			PS: mockProcess{
				[]facts.Process{
					{
						PID:           42,
						CmdLineList:   c.cmdLine,
						ContainerID:   c.containerID,
						ContainerName: c.containerName,
					},
				},
			},
			Netstat: mockNetstat{result: map[int][]facts.ListenAddress{
				42: c.netstatAddresses,
			}},
			ContainerInfo: mockContainerInfo{
				containers: map[string]facts.FakeContainer{
					c.containerID: {
						FakeContainerName:   c.containerName,
						FakePrimaryAddress:  c.containerIP,
						FakeListenAddresses: c.containerAddresses,
						FakeEnvironment:     c.containerEnv,
						FakeLabels:          c.containerLabels,
					},
				},
			},
			IsContainerIgnored: facts.ContainerFilter{}.ContainerIgnored,
			FileReader: mockFileReader{
				contents: c.filesContent,
			},
		})
		dd.now = func() time.Time { return t0 }

		srv, _, err := dd.Discovery(ctx)
		if err != nil {
			t.Error(err)
		}

		if c.noMatch {
			if len(srv) != 0 {
				t.Errorf("Case %s: len(srv=%v) == %v, want 0", c.testName, srv, len(srv))
			}

			continue
		}

		if len(srv) != 1 {
			t.Errorf("Case %s: len(srv) == %v, want 1", c.testName, len(srv))

			continue
		}

		if diff := cmp.Diff(c.want, srv[0], cmpopts.IgnoreUnexported(Service{}), cmpopts.EquateEmpty()); diff != "" {
			t.Errorf("Case %s: diff: (-want +got)\n %s", c.testName, diff)
		}
	}
}

//nolint:dupl
func TestDynamicDiscovery(t *testing.T) { //nolint:maintidx
	t0 := time.Now()

	cases := []struct {
		name                   string
		processes              []facts.Process
		filesContent           map[string]string
		containers             map[string]facts.FakeContainer
		netstatAddressesPerPID map[int][]facts.ListenAddress
		want                   []Service
	}{
		{
			name: "redis-single",
			processes: []facts.Process{
				{
					PID:         1,
					CmdLineList: []string{"init"},
				},
				{
					PID:         2,
					CmdLineList: []string{"bash", "/srv/start-service.sh", "redis"},
				},
				{
					PID:         3,
					CmdLineList: []string{"redis-server *:6379"},
				},
			},
			netstatAddressesPerPID: map[int][]facts.ListenAddress{
				2: {
					{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 1234},
				},
				3: {
					{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 6379},
				},
			},
			want: []Service{
				{
					Name:            "redis",
					ServiceType:     RedisService,
					ContainerID:     "",
					ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 6379}},
					IPAddress:       "127.0.0.1",
					IgnoredPorts:    map[int]bool{},
					HasNetstatInfo:  true,
					LastNetstatInfo: t0,
					Active:          true,
					LastTimeSeen:    t0,
				},
			},
		},
		{
			name: "redis-single-no-netstat",
			processes: []facts.Process{
				{
					PID:         1,
					CmdLineList: []string{"init"},
				},
				{
					PID:         2,
					CmdLineList: []string{"bash", "/srv/start-service.sh", "redis"},
				},
				{
					PID:         3,
					CmdLineList: []string{"redis-server *:6379"},
				},
			},
			netstatAddressesPerPID: map[int][]facts.ListenAddress{},
			want: []Service{
				{
					Name:            "redis",
					ServiceType:     RedisService,
					ContainerID:     "",
					ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 6379}},
					IPAddress:       "127.0.0.1",
					IgnoredPorts:    map[int]bool{},
					HasNetstatInfo:  false,
					Active:          true,
					LastTimeSeen:    t0,
				},
			},
		},
		{
			name: "redis-multiple",
			processes: []facts.Process{
				{
					PID:         1,
					CmdLineList: []string{"init"},
				},
				{
					PID:         2,
					CmdLineList: []string{"bash", "/srv/start-service.sh", "redis"},
				},
				{
					PID:         3,
					CmdLineList: []string{"redis-server *:6379"},
				},
				{
					PID:         4,
					CmdLineList: []string{"redis-server *:6379"}, // this is the process that dump to RDB file
				},
				{
					PID:         5,
					CmdLineList: []string{"redis-server *:6380"},
				},
			},
			netstatAddressesPerPID: map[int][]facts.ListenAddress{
				2: {
					{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 1234},
				},
				3: {
					{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 6379},
				},
				4: {
					// This will likely not happen, because:
					// 1) the process that does RDB dump may not actually listen on this port.
					// 2) Glouton netstat does NOT provided list all PID that listen on the same port. (netstat neither).
					{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 6379},
				},
				5: {
					{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 6380},
				},
			},
			want: []Service{
				{
					Name:        "redis",
					ServiceType: RedisService,
					ContainerID: "",
					ListenAddresses: []facts.ListenAddress{
						{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 6379},
						{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 6380},
					},
					IPAddress:       "127.0.0.1",
					IgnoredPorts:    map[int]bool{},
					HasNetstatInfo:  true,
					LastNetstatInfo: t0,
					Active:          true,
					LastTimeSeen:    t0,
				},
			},
		},
		{
			name: "nginx-not-default-port",
			processes: []facts.Process{
				{
					PID:         3,
					CmdLineList: []string{"nginx: master process nginx -g daemon off;"},
				},
				{
					PID:         4,
					CmdLineList: []string{"nginx: worker process"},
				},
			},
			netstatAddressesPerPID: map[int][]facts.ListenAddress{
				3: {
					{NetworkFamily: "tcp", Address: "10.2.0.1", Port: 6443},
				},
				// We aren't guarantee to have netstat information for PID 4:
				// netstat only show one PID per listening socket
				// internal netstat (using gopsutil) also show one PID per listening socket
			},
			want: []Service{
				{
					Name:        "nginx",
					ServiceType: NginxService,
					ContainerID: "",
					ListenAddresses: []facts.ListenAddress{
						{NetworkFamily: "tcp", Address: "10.2.0.1", Port: 6443},
					},
					IPAddress:       "127.0.0.1",
					IgnoredPorts:    map[int]bool{},
					HasNetstatInfo:  true,
					LastNetstatInfo: t0,
					Active:          true,
					LastTimeSeen:    t0,
				},
			},
		},
		{
			name: "nginx-not-default-port-2",
			processes: []facts.Process{
				{
					PID:         3,
					CmdLineList: []string{"nginx: master process nginx -g daemon off;"},
				},
				{
					PID:         4,
					CmdLineList: []string{"nginx: worker process"},
				},
			},
			netstatAddressesPerPID: map[int][]facts.ListenAddress{
				// We aren't guarantee to have netstat information for PID 3:
				// netstat only show one PID per listening socket
				// internal netstat (using gopsutil) also show one PID per listening socket
				4: {
					{NetworkFamily: "tcp", Address: "10.0.2.1", Port: 6443},
				},
			},
			want: []Service{
				{
					Name:        "nginx",
					ServiceType: NginxService,
					ContainerID: "",
					ListenAddresses: []facts.ListenAddress{
						{NetworkFamily: "tcp", Address: "10.0.2.1", Port: 6443},
					},
					IPAddress:       "127.0.0.1",
					IgnoredPorts:    map[int]bool{},
					HasNetstatInfo:  true,
					LastNetstatInfo: t0,
					Active:          true,
					LastTimeSeen:    t0,
				},
			},
		},
		{
			name: "two-services-two-containers-two-listen-addresses",
			processes: []facts.Process{
				{
					PID:         3,
					CmdLineList: []string{"nginx: master process nginx -g daemon off;"},
					ContainerID: "id001",
				},
				{
					PID:         4,
					CmdLineList: []string{"php-fpm: master process (/etc/php/7.0/fpm/php-fpm.conf)"},
					ContainerID: "id002",
				},
			},
			netstatAddressesPerPID: nil, // netstat won't provide information for containers
			containers: map[string]facts.FakeContainer{
				"id001": {
					FakePrimaryAddress:  "172.17.0.49",
					FakeListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "172.17.0.49", Port: 80}},
				},
				"id002": {
					FakePrimaryAddress:  "172.17.0.50",
					FakeListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "172.17.0.50", Port: 9000}},
				},
			},
			want: []Service{
				{
					Name:        "nginx",
					ServiceType: NginxService,
					ContainerID: "id001",
					ListenAddresses: []facts.ListenAddress{
						{NetworkFamily: "tcp", Address: "172.17.0.49", Port: 80},
					},
					IPAddress:       "172.17.0.49",
					IgnoredPorts:    map[int]bool{},
					HasNetstatInfo:  true,
					LastNetstatInfo: t0,
					Active:          true,
					LastTimeSeen:    t0,
				},
				{
					Name:        "phpfpm",
					ServiceType: PHPFPMService,
					ContainerID: "id002",
					ListenAddresses: []facts.ListenAddress{
						{NetworkFamily: "tcp", Address: "172.17.0.50", Port: 9000},
					},
					IPAddress:       "172.17.0.50",
					IgnoredPorts:    map[int]bool{},
					HasNetstatInfo:  true,
					LastNetstatInfo: t0,
					Active:          true,
					LastTimeSeen:    t0,
				},
			},
		},
		{
			name: "two-services-two-containers-one-listen-addresses",
			processes: []facts.Process{
				{
					PID:         3,
					CmdLineList: []string{"nginx: master process nginx -g daemon off;"},
					ContainerID: "id001",
				},
				{
					PID:         4,
					CmdLineList: []string{"php-fpm: master process (/etc/php/7.0/fpm/php-fpm.conf)"},
					ContainerID: "id002",
				},
			},
			netstatAddressesPerPID: nil, // netstat won't provide information for containers
			containers: map[string]facts.FakeContainer{
				"id001": {
					FakePrimaryAddress:  "172.17.0.49",
					FakeListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "172.17.0.49", Port: 80}},
				},
				"id002": {
					FakePrimaryAddress: "172.17.0.50",
				},
			},
			want: []Service{
				{
					Name:        "nginx",
					ServiceType: NginxService,
					ContainerID: "id001",
					ListenAddresses: []facts.ListenAddress{
						{NetworkFamily: "tcp", Address: "172.17.0.49", Port: 80},
					},
					IPAddress:       "172.17.0.49",
					IgnoredPorts:    map[int]bool{},
					HasNetstatInfo:  true,
					LastNetstatInfo: t0,
					Active:          true,
					LastTimeSeen:    t0,
				},
				{
					Name:            "phpfpm",
					ServiceType:     PHPFPMService,
					ContainerID:     "id002",
					ListenAddresses: []facts.ListenAddress{},
					IPAddress:       "172.17.0.50",
					IgnoredPorts:    map[int]bool{},
					HasNetstatInfo:  false,
					Active:          true,
					LastTimeSeen:    t0,
				},
			},
		},
		{
			name: "two-services-two-containers-zero-listen-addresses",
			processes: []facts.Process{
				{
					PID:         3,
					CmdLineList: []string{"nginx: master process nginx -g daemon off;"},
					ContainerID: "id001",
				},
				{
					PID:         4,
					CmdLineList: []string{"php-fpm: master process (/etc/php/7.0/fpm/php-fpm.conf)"},
					ContainerID: "id002",
				},
			},
			netstatAddressesPerPID: nil, // netstat won't provide information for containers
			containers: map[string]facts.FakeContainer{
				"id001": {
					FakePrimaryAddress: "172.17.0.49",
				},
				"id002": {
					FakePrimaryAddress: "172.17.0.50",
				},
			},
			want: []Service{
				{
					Name:        "nginx",
					ServiceType: NginxService,
					ContainerID: "id001",
					ListenAddresses: []facts.ListenAddress{
						{NetworkFamily: "tcp", Address: "172.17.0.49", Port: 80},
					},
					IPAddress:      "172.17.0.49",
					IgnoredPorts:   map[int]bool{},
					HasNetstatInfo: false,
					Active:         true,
					LastTimeSeen:   t0,
				},
				{
					Name:            "phpfpm",
					ServiceType:     PHPFPMService,
					ContainerID:     "id002",
					ListenAddresses: []facts.ListenAddress{},
					IPAddress:       "172.17.0.50",
					IgnoredPorts:    map[int]bool{},
					HasNetstatInfo:  false,
					Active:          true,
					LastTimeSeen:    t0,
				},
			},
		},
		{
			name: "two-services-one-containers-two-listen-addresses",
			processes: []facts.Process{
				{
					PID:         3,
					CmdLineList: []string{"nginx: master process nginx -g daemon off;"},
					ContainerID: "id001",
				},
				{
					PID:         4,
					CmdLineList: []string{"php-fpm: master process (/etc/php/7.0/fpm/php-fpm.conf)"},
					ContainerID: "id001",
				},
			},
			netstatAddressesPerPID: nil, // netstat won't provide information for containers
			containers: map[string]facts.FakeContainer{
				"id001": {
					FakePrimaryAddress: "172.17.0.49",
					FakeListenAddresses: []facts.ListenAddress{
						{NetworkFamily: "tcp", Address: "172.17.0.49", Port: 80},
						{NetworkFamily: "tcp", Address: "172.17.0.49", Port: 9000},
					},
				},
			},
			want: []Service{
				{
					Name:        "nginx",
					ServiceType: NginxService,
					ContainerID: "id001",
					ListenAddresses: []facts.ListenAddress{
						{NetworkFamily: "tcp", Address: "172.17.0.49", Port: 80},
					},
					IPAddress:       "172.17.0.49",
					IgnoredPorts:    map[int]bool{},
					HasNetstatInfo:  true,
					LastNetstatInfo: t0,
					Active:          true,
					LastTimeSeen:    t0,
				},
				{
					Name:            "phpfpm",
					ServiceType:     PHPFPMService,
					ContainerID:     "id001",
					ListenAddresses: []facts.ListenAddress{},
					IPAddress:       "172.17.0.49",
					IgnoredPorts:    map[int]bool{},
					HasNetstatInfo:  false,
					Active:          true,
					LastTimeSeen:    t0,
				},
			},
		},
		{
			name: "two-services-one-containers-one-listen-addresses",
			processes: []facts.Process{
				{
					PID:         3,
					CmdLineList: []string{"nginx: master process nginx -g daemon off;"},
					ContainerID: "id001",
				},
				{
					PID:         4,
					CmdLineList: []string{"php-fpm: master process (/etc/php/7.0/fpm/php-fpm.conf)"},
					ContainerID: "id001",
				},
			},
			netstatAddressesPerPID: nil, // netstat won't provide information for containers
			containers: map[string]facts.FakeContainer{
				"id001": {
					FakePrimaryAddress:  "172.17.0.49",
					FakeListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "172.17.0.49", Port: 80}},
				},
			},
			want: []Service{
				{
					Name:        "nginx",
					ServiceType: NginxService,
					ContainerID: "id001",
					ListenAddresses: []facts.ListenAddress{
						{NetworkFamily: "tcp", Address: "172.17.0.49", Port: 80},
					},
					IPAddress:       "172.17.0.49",
					IgnoredPorts:    map[int]bool{},
					HasNetstatInfo:  true,
					LastNetstatInfo: t0,
					Active:          true,
					LastTimeSeen:    t0,
				},
				{
					Name:            "phpfpm",
					ServiceType:     PHPFPMService,
					ContainerID:     "id001",
					ListenAddresses: []facts.ListenAddress{},
					IPAddress:       "172.17.0.49",
					IgnoredPorts:    map[int]bool{},
					HasNetstatInfo:  false,
					Active:          true,
					LastTimeSeen:    t0,
				},
			},
		},
		{
			name: "two-services-one-containers-zero-listen-addresses",
			processes: []facts.Process{
				{
					PID:         3,
					CmdLineList: []string{"nginx: master process nginx -g daemon off;"},
					ContainerID: "id001",
				},
				{
					PID:         4,
					CmdLineList: []string{"php-fpm: master process (/etc/php/7.0/fpm/php-fpm.conf)"},
					ContainerID: "id001",
				},
			},
			netstatAddressesPerPID: nil, // netstat won't provide information for containers
			containers: map[string]facts.FakeContainer{
				"id001": {
					FakePrimaryAddress: "172.17.0.49",
				},
			},
			want: []Service{
				{
					Name:        "nginx",
					ServiceType: NginxService,
					ContainerID: "id001",
					ListenAddresses: []facts.ListenAddress{
						{NetworkFamily: "tcp", Address: "172.17.0.49", Port: 80},
					},
					IPAddress:      "172.17.0.49",
					IgnoredPorts:   map[int]bool{},
					HasNetstatInfo: false,
					Active:         true,
					LastTimeSeen:   t0,
				},
				{
					Name:            "phpfpm",
					ServiceType:     PHPFPMService,
					ContainerID:     "id001",
					ListenAddresses: []facts.ListenAddress{},
					IPAddress:       "172.17.0.49",
					IgnoredPorts:    map[int]bool{},
					HasNetstatInfo:  false,
					Active:          true,
					LastTimeSeen:    t0,
				},
			},
		},
		{
			name: "nginx-phpfpm-port-8080",
			processes: []facts.Process{
				{
					PID:         3,
					CmdLineList: []string{"nginx: master process nginx -g daemon off;"},
					ContainerID: "id001",
				},
				{
					PID:         4,
					CmdLineList: []string{"php-fpm: master process (/etc/php/7.0/fpm/php-fpm.conf)"},
					ContainerID: "id001",
				},
			},
			netstatAddressesPerPID: nil, // netstat won't provide information for containers
			containers: map[string]facts.FakeContainer{
				"id001": {
					FakePrimaryAddress:  "172.17.0.49",
					FakeListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "172.17.0.49", Port: 8080}},
				},
			},
			want: []Service{
				{
					Name:            "nginx",
					ServiceType:     NginxService,
					ContainerID:     "id001",
					ListenAddresses: []facts.ListenAddress{},
					IPAddress:       "172.17.0.49",
					IgnoredPorts:    map[int]bool{},
					HasNetstatInfo:  false,
					Active:          true,
					LastTimeSeen:    t0,
				},
				{
					Name:            "phpfpm",
					ServiceType:     PHPFPMService,
					ContainerID:     "id001",
					ListenAddresses: []facts.ListenAddress{},
					IPAddress:       "172.17.0.49",
					IgnoredPorts:    map[int]bool{},
					HasNetstatInfo:  false,
					Active:          true,
					LastTimeSeen:    t0,
				},
			},
		},
		{
			name: "nginx-apache-port-80",
			processes: []facts.Process{
				{
					PID:         3,
					CmdLineList: []string{"nginx: master process nginx -g daemon off;"},
					ContainerID: "id001",
				},
				{
					PID:         4,
					CmdLineList: []string{"/usr/sbin/apache2", "-k", "start"},
					ContainerID: "id001",
				},
			},
			netstatAddressesPerPID: nil, // netstat won't provide information for containers
			containers: map[string]facts.FakeContainer{
				"id001": {
					FakePrimaryAddress:  "172.17.0.49",
					FakeListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "172.17.0.49", Port: 80}},
				},
			},
			want: []Service{
				{
					Name:            "nginx",
					ServiceType:     NginxService,
					ContainerID:     "id001",
					ListenAddresses: []facts.ListenAddress{},
					IPAddress:       "172.17.0.49",
					IgnoredPorts:    map[int]bool{},
					HasNetstatInfo:  false,
					Active:          true,
					LastTimeSeen:    t0,
				},
				{
					Name:            "apache",
					ServiceType:     ApacheService,
					ContainerID:     "id001",
					ListenAddresses: []facts.ListenAddress{},
					IPAddress:       "172.17.0.49",
					IgnoredPorts:    map[int]bool{},
					HasNetstatInfo:  false,
					Active:          true,
					LastTimeSeen:    t0,
				},
			},
		},
		{
			name: "nginx-apache-port-80-and-8080",
			processes: []facts.Process{
				{
					PID:         3,
					CmdLineList: []string{"nginx: master process nginx -g daemon off;"},
					ContainerID: "id001",
				},
				{
					PID:         4,
					CmdLineList: []string{"/usr/sbin/apache2", "-k", "start"},
					ContainerID: "id001",
				},
			},
			netstatAddressesPerPID: nil, // netstat won't provide information for containers
			containers: map[string]facts.FakeContainer{
				"id001": {
					FakePrimaryAddress: "172.17.0.49",
					FakeListenAddresses: []facts.ListenAddress{
						{NetworkFamily: "tcp", Address: "172.17.0.49", Port: 80},
						{NetworkFamily: "tcp", Address: "172.17.0.49", Port: 8080},
					},
				},
			},
			want: []Service{
				{
					Name:            "nginx",
					ServiceType:     NginxService,
					ContainerID:     "id001",
					ListenAddresses: []facts.ListenAddress{},
					IPAddress:       "172.17.0.49",
					IgnoredPorts:    map[int]bool{},
					HasNetstatInfo:  false,
					Active:          true,
					LastTimeSeen:    t0,
				},
				{
					Name:            "apache",
					ServiceType:     ApacheService,
					ContainerID:     "id001",
					ListenAddresses: []facts.ListenAddress{},
					IPAddress:       "172.17.0.49",
					IgnoredPorts:    map[int]bool{},
					HasNetstatInfo:  false,
					Active:          true,
					LastTimeSeen:    t0,
				},
			},
		},
		{
			name: "nginx-phpfpm-port-80-and-443",
			processes: []facts.Process{
				{
					PID:         3,
					CmdLineList: []string{"nginx: master process nginx -g daemon off;"},
					ContainerID: "id001",
				},
				{
					PID:         4,
					CmdLineList: []string{"php-fpm: master process (/etc/php/7.0/fpm/php-fpm.conf)"},
					ContainerID: "id001",
				},
			},
			netstatAddressesPerPID: nil, // netstat won't provide information for containers
			containers: map[string]facts.FakeContainer{
				"id001": {
					FakePrimaryAddress: "172.17.0.49",
					FakeListenAddresses: []facts.ListenAddress{
						{NetworkFamily: "tcp", Address: "172.17.0.49", Port: 80},
						{NetworkFamily: "tcp", Address: "172.17.0.49", Port: 443},
					},
				},
			},
			want: []Service{
				{
					Name:        "nginx",
					ServiceType: NginxService,
					ContainerID: "id001",
					ListenAddresses: []facts.ListenAddress{
						{NetworkFamily: "tcp", Address: "172.17.0.49", Port: 80},
					},
					IPAddress:       "172.17.0.49",
					IgnoredPorts:    map[int]bool{},
					HasNetstatInfo:  true,
					LastNetstatInfo: t0,
					Active:          true,
					LastTimeSeen:    t0,
				},
				{
					Name:            "phpfpm",
					ServiceType:     PHPFPMService,
					ContainerID:     "id001",
					ListenAddresses: []facts.ListenAddress{},
					IPAddress:       "172.17.0.49",
					IgnoredPorts:    map[int]bool{},
					HasNetstatInfo:  false,
					Active:          true,
					LastTimeSeen:    t0,
				},
			},
		},
		{
			name: "nginx-redis-port-80-and-6379",
			processes: []facts.Process{
				{
					PID:         3,
					CmdLineList: []string{"nginx: master process nginx -g daemon off;"},
					ContainerID: "id001",
				},
				{
					PID:         4,
					CmdLineList: []string{"redis-server *:6379"},
					ContainerID: "id001",
				},
			},
			netstatAddressesPerPID: nil, // netstat won't provide information for containers
			containers: map[string]facts.FakeContainer{
				"id001": {
					FakePrimaryAddress: "172.17.0.49",
					FakeListenAddresses: []facts.ListenAddress{
						{NetworkFamily: "tcp", Address: "172.17.0.49", Port: 80},
						{NetworkFamily: "tcp", Address: "172.17.0.49", Port: 6379},
					},
				},
			},
			want: []Service{
				{
					Name:        "nginx",
					ServiceType: NginxService,
					ContainerID: "id001",
					ListenAddresses: []facts.ListenAddress{
						{NetworkFamily: "tcp", Address: "172.17.0.49", Port: 80},
					},
					IPAddress:       "172.17.0.49",
					IgnoredPorts:    map[int]bool{},
					HasNetstatInfo:  true,
					LastNetstatInfo: t0,
					Active:          true,
					LastTimeSeen:    t0,
				},
				{
					Name:        "redis",
					ServiceType: RedisService,
					ContainerID: "id001",
					ListenAddresses: []facts.ListenAddress{
						{NetworkFamily: "tcp", Address: "172.17.0.49", Port: 6379},
					},
					IPAddress:       "172.17.0.49",
					IgnoredPorts:    map[int]bool{},
					HasNetstatInfo:  true,
					LastNetstatInfo: t0,
					Active:          true,
					LastTimeSeen:    t0,
				},
			},
		},
		{
			name: "nginx-redis-port-8080-and-6379",
			processes: []facts.Process{
				{
					PID:         3,
					CmdLineList: []string{"nginx: master process nginx -g daemon off;"},
					ContainerID: "id001",
				},
				{
					PID:         4,
					CmdLineList: []string{"redis-server *:6379"},
					ContainerID: "id001",
				},
			},
			netstatAddressesPerPID: nil, // netstat won't provide information for containers
			containers: map[string]facts.FakeContainer{
				"id001": {
					FakePrimaryAddress: "172.17.0.49",
					FakeListenAddresses: []facts.ListenAddress{
						{NetworkFamily: "tcp", Address: "172.17.0.49", Port: 8080},
						{NetworkFamily: "tcp", Address: "172.17.0.49", Port: 6379},
					},
				},
			},
			want: []Service{
				{
					Name:            "nginx",
					ServiceType:     NginxService,
					ContainerID:     "id001",
					ListenAddresses: []facts.ListenAddress{},
					IPAddress:       "172.17.0.49",
					IgnoredPorts:    map[int]bool{},
					HasNetstatInfo:  false,
					Active:          true,
					LastTimeSeen:    t0,
				},
				{
					Name:        "redis",
					ServiceType: RedisService,
					ContainerID: "id001",
					ListenAddresses: []facts.ListenAddress{
						{NetworkFamily: "tcp", Address: "172.17.0.49", Port: 6379},
					},
					IPAddress:       "172.17.0.49",
					IgnoredPorts:    map[int]bool{},
					HasNetstatInfo:  true,
					LastNetstatInfo: t0,
					Active:          true,
					LastTimeSeen:    t0,
				},
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx := t.Context()

			t.Parallel()

			dd := NewDynamic(Option{
				PS: mockProcess{
					result: c.processes,
				},
				Netstat: mockNetstat{result: c.netstatAddressesPerPID},
				ContainerInfo: mockContainerInfo{
					containers: c.containers,
				},
				IsContainerIgnored: facts.ContainerFilter{}.ContainerIgnored,
				FileReader: mockFileReader{
					contents: c.filesContent,
				},
			})
			dd.now = func() time.Time { return t0 }

			srv, _, err := dd.Discovery(ctx)
			if err != nil {
				t.Error(err)
			}

			sorter := cmpopts.SortSlices(func(x Service, y Service) bool {
				return x.String() < y.String()
			})

			if diff := cmp.Diff(c.want, srv, cmpopts.IgnoreUnexported(Service{}), cmpopts.EquateEmpty(), sorter); diff != "" {
				t.Errorf("services mismatch (-want +got)\n%s", diff)
			}
		})
	}
}

func Test_fillGenericExtraAttributes(t *testing.T) {
	cases := []struct {
		name                        string
		serviceFromDynamicDiscovery Service
		expectedConfigResult        config.Service
	}{
		{
			name: "ports-and-http_path",
			serviceFromDynamicDiscovery: Service{
				container: facts.FakeContainer{
					FakeLabels: map[string]string{
						"glouton.port":         "8080",
						"glouton.ignore_ports": "9090,9091",
						"glouton.http_path":    "/path",
					},
				},
				Config: config.Service{
					// HTTP Path should be overwritten.
					HTTPPath: "/other",
					// Address should be kept.
					Address: "192.168.0.1",
				},
			},
			expectedConfigResult: config.Service{
				HTTPPath:    "/path",
				Port:        8080,
				IgnorePorts: []int{9090, 9091},
				Address:     "192.168.0.1",
			},
		},
		{
			name: "tags",
			serviceFromDynamicDiscovery: Service{
				container: facts.FakeContainer{
					FakeLabels: map[string]string{
						"glouton.tags": "tags1,tags2",
					},
				},
				Config: config.Service{
					// Dynamic discovery won't fill tags by itself,
					// they always come from container labels/annotations.
				},
			},
			expectedConfigResult: config.Service{
				Tags: []string{"tags1", "tags2"},
			},
		},
		{
			name: "from-annotations",
			serviceFromDynamicDiscovery: Service{
				container: facts.FakeContainer{
					FakeAnnotations: map[string]string{
						"glouton.tags":      "tags1,tags2",
						"glouton.http_path": "/path",
					},
				},
				Config: config.Service{
					// Dynamic discovery won't fill tags by itself,
					// they always come from container labels/annotations.
				},
			},
			expectedConfigResult: config.Service{
				Tags:     []string{"tags1", "tags2"},
				HTTPPath: "/path",
			},
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			service := tt.serviceFromDynamicDiscovery

			dd := NewDynamic(Option{
				PS:                 mockProcess{},
				Netstat:            mockNetstat{},
				ContainerInfo:      mockContainerInfo{},
				IsContainerIgnored: facts.ContainerFilter{}.ContainerIgnored,
			})

			dd.fillConfigFromLabels(&service)

			if diff := cmp.Diff(tt.expectedConfigResult, service.Config); diff != "" {
				t.Fatalf("Unexpected config:\n%s", diff)
			}
		})
	}
}
