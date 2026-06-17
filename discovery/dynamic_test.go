// Copyright 2015-2026 Bleemeo
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

//nolint:goconst
package discovery

import (
	"context"
	"maps"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/facts"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

const (
	testIP17217049      = "172.17.0.49"
	testIP17217050      = "172.17.0.50"
	testID001           = "id001"
	testID002           = "id002"
	testNginxAltKey     = "nginx_port_alt"
	testSecret          = "secret"
	testIPZero          = "0.0.0.0"
	testRoot            = "-root"
	testSaslErrorLogger = "sasl_error_logger"
	testErlangLibDir    = "/usr/lib/erlang"
	testFalse           = "false"

	testNginxMasterDaemon = "nginx: master process nginx -g daemon off;"
	testPHPFPMMasterConf  = "php-fpm: master process (/etc/php/7.0/fpm/php-fpm.conf)"
	testNginxMasterSbin   = "nginx: master process /usr/sbin/nginx"
	testRedisServerListen = "redis-server *:6379"
	testMemcachedBin      = "/usr/bin/memcached"
	testMySQL             = string(MySQLService)
	testHAProxy           = string(HAProxyService)
	testUWSGI             = string(UWSGIService)
	testMySQLD            = "mysqld"

	testMemcache         = "memcache"
	testIP192168         = "192.168.1.1"
	testXmx1g            = "-Xmx1g"
	testNoinput          = "-noinput"
	testApache2Sbin      = "/usr/sbin/apache2"
	testHome             = "-home"
	testTrue             = "true"
	testRabbit           = "-rabbit"
	testCp               = "-cp"
	testFileEncodingUTF8 = "-Dfile.encoding=UTF-8"
	testUseG1GC          = "-XX:+UseG1GC"
	testInit             = "init"
	testStartServiceSh   = "/srv/start-service.sh"
	testPath             = "/path"
	testEllipsis         = "[...]"
	testSname            = "-sname"
	testStart            = "start"
	testProgname         = "-progname"
	testSasl             = "-sasl"
	testNoshell          = "-noshell"
	testOsMon            = "-os_mon"
	testBash             = "bash"
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

	maps.Copy(result, mn.result)

	return result, nil
}

type mockContainerInfo struct {
	containers map[string]facts.FakeContainer
}

func (mci mockContainerInfo) CachedContainer(containerID string) (container facts.Container, found bool) {
	c, ok := mci.containers[containerID]

	return c, ok
}

func (mci mockContainerInfo) ContainerLastKill(containerID string) time.Time {
	return time.Time{}
}

func (mci mockContainerInfo) ContainerLastDelete(containerID string) time.Time {
	return time.Time{}
}

func (mci mockContainerInfo) ContainerTerminationGracePeriod(containerID string) time.Duration {
	return 0
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
			in:   []string{testMemcachedBin, "-m", "64", "-p", "11211", "-u", testMemcache, "-l", testIP127001, "-P", "/var/run/memcached/memcached.pid"},
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
					CmdLineList: []string{testMemcachedBin, "-m", "64", "-p", "11211", "-u", testMemcache, "-l", testIP127001, "-P", "/var/run/memcached/memcached.pid"},
					Name:        testMemcached,
					MemoryRSS:   0xa88,
					CPUPercent:  0.028360216236998047,
					CPUTime:     98.55000000000001,
					Status:      "S",
					Username:    testMemcache,
					Executable:  "",
					ContainerID: "",
				},
			},
		},
		Netstat: mockNetstat{result: map[int][]facts.ListenAddress{
			1547: {
				{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 11211},
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

	if srv[0].Name != testMemcached {
		t.Errorf("Name == %#v, want %#v", srv[0].Name, testMemcached)
	}

	if srv[0].ServiceType != MemcachedService {
		t.Errorf("Name == %#v, want %#v", srv[0].ServiceType, MemcachedService)
	}

	want := []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 11211}}
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
			cmdLine:          []string{testMemcachedBin},
			containerID:      "",
			netstatAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIPZero, Port: 11211}},
			want: Service{
				Name:            testMemcached,
				ServiceType:     MemcachedService,
				ContainerID:     "",
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIPZero, Port: 11211}},
				IPAddress:       testIP127001,
				Active:          true,
				HasNetstatInfo:  true,
				LastNetstatInfo: t0,
				LastTimeSeen:    t0,
			},
		},
		{
			testName:         "simple-no-netstat",
			cmdLine:          []string{testMemcachedBin},
			containerID:      "",
			netstatAddresses: nil,
			want: Service{
				Name:            testMemcached,
				ServiceType:     MemcachedService,
				ContainerID:     "",
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 11211}},
				IPAddress:       testIP127001,
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		{
			testName:         "simple-bind-specific",
			cmdLine:          []string{testMemcachedBin},
			containerID:      "",
			netstatAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP192168, Port: 11211}},
			want: Service{
				Name:            testMemcached,
				ServiceType:     MemcachedService,
				ContainerID:     "",
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP192168, Port: 11211}},
				IPAddress:       testIP192168,
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
			netstatAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIPZero, Port: 80}, {NetworkFamily: udpProtocol, Address: testIPZero, Port: 42514}},
			want: Service{
				Name:            testHAProxy,
				ServiceType:     HAProxyService,
				ContainerID:     "",
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIPZero, Port: 80}},
				IPAddress:       testIP127001,
				Active:          true,
				HasNetstatInfo:  true,
				LastNetstatInfo: t0,
				LastTimeSeen:    t0,
			},
		},
		{
			testName:           "redis-container",
			cmdLine:            []string{testRedisServerListen},
			containerID:        "5b8f83412931055bcc5da35e41ada85fd70015673163d56911cac4fe6693273f",
			netstatAddresses:   nil, // netstat won't provide information
			containerAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP17217049, Port: 6379}},
			containerIP:        testIP17217049,
			want: Service{
				Name:            testRedis,
				ServiceType:     RedisService,
				ContainerID:     "5b8f83412931055bcc5da35e41ada85fd70015673163d56911cac4fe6693273f",
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP17217049, Port: 6379}},
				IPAddress:       testIP17217049,
				IgnoredPorts:    map[int]bool{},
				Active:          true,
				HasNetstatInfo:  true,
				LastNetstatInfo: t0,
				LastTimeSeen:    t0,
			},
		},
		{
			testName: string(ElasticSearchService),
			cmdLine:  []string{"/opt/jdk-11.0.1/bin/java", "-Xms1g", testXmx1g, "-XX:+UseConcMarkSweepGC", testEllipsis, "/usr/share/elasticsearch/lib/*", elasticsearchMainClass},
			want: Service{
				Name:            string(ElasticSearchService),
				ServiceType:     ElasticSearchService,
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 9200}},
				IPAddress:       testIP127001,
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		{
			testName:     "mariadb-container",
			containerID:  "1234",
			containerIP:  testIP17217049,
			cmdLine:      []string{mariadbdProcess},
			containerEnv: map[string]string{"MARIADB_ROOT_PASSWORD": testSecret},
			want: Service{
				Name:            string(MariaDBService),
				ServiceType:     MariaDBService,
				ContainerID:     "1234",
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP17217049, Port: 3306}},
				IPAddress:       testIP17217049,
				Config: config.Service{
					Username: mariadbDefaultUser,
					Password: testSecret,
				},
				IgnoredPorts: map[int]bool{},
				Active:       true,
				LastTimeSeen: t0,
			},
		},
		{
			testName: "mariadb-host",
			cmdLine:  []string{mariadbdProcess},
			want: Service{
				Name:            string(MariaDBService),
				ServiceType:     MariaDBService,
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 3306}},
				IPAddress:       testIP127001,
				Config: config.Service{
					Username:          "",
					Password:          "",
					MetricsUnixSocket: "",
				},
				Active:       true,
				LastTimeSeen: t0,
			},
		},
		{
			testName:     "mysql-container",
			containerID:  "1234",
			containerIP:  testIP17217049,
			cmdLine:      []string{testMySQLD},
			containerEnv: map[string]string{"MYSQL_ROOT_PASSWORD": testSecret},
			want: Service{
				Name:            testMySQL,
				ServiceType:     MySQLService,
				ContainerID:     "1234",
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP17217049, Port: 3306}},
				IPAddress:       testIP17217049,
				Config: config.Service{
					Username: mysqlDefaultUser,
					Password: testSecret,
				},
				IgnoredPorts: map[int]bool{},
				Active:       true,
				LastTimeSeen: t0,
			},
		},
		{
			testName: "mysql-host",
			cmdLine:  []string{testMySQLD},
			filesContent: map[string]string{
				"/etc/mysql/debian.cnf": "[client]\nuser   = root\npassword    = secret\n",
			},
			want: Service{
				Name:            testMySQL,
				ServiceType:     MySQLService,
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 3306}},
				IPAddress:       testIP127001,
				Config: config.Service{
					Username:          "root",
					Password:          testSecret,
					MetricsUnixSocket: "",
				},
				Active:       true,
				LastTimeSeen: t0,
			},
		},
		{
			testName: "mysql-host2",
			cmdLine:  []string{testMySQLD},
			filesContent: map[string]string{
				"/etc/mysql/debian.cnf": "[client]\nuser   = root\npassword    = secret\nsocket = /tmp/file.sock\n",
			},
			want: Service{
				Name:            testMySQL,
				ServiceType:     MySQLService,
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 3306}},
				IPAddress:       testIP127001,
				Config: config.Service{
					Username:          "root",
					Password:          testSecret,
					MetricsUnixSocket: "/tmp/file.sock",
				},
				Active:       true,
				LastTimeSeen: t0,
			},
		},
		{
			testName: string(RabbitMQService),
			cmdLine:  []string{"/usr/lib/erlang/erts-9.3.3.3/bin/beam.smp", "-W", "w", testEllipsis, testNoinput, "-s", "rabbit", "boot", testSname, testEllipsis},
			want: Service{
				Name:            string(RabbitMQService),
				ServiceType:     RabbitMQService,
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 5672}},
				IPAddress:       testIP127001,
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
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 8086}},
				IPAddress:       testIP127001,
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		// Service from Ubuntu 16.04, default config
		{
			testName: "mysql-ubuntu-14.04",
			cmdLine:  []string{"/usr/sbin/mysqld"},
			want: Service{
				Name:            testMySQL,
				ServiceType:     MySQLService,
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 3306}},
				IPAddress:       testIP127001,
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
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: udpProtocol, Address: testIP127001, Port: 123}},
				IPAddress:       testIP127001,
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		{
			testName: "slapd-ubuntu-14.04",
			cmdLine:  []string{"/usr/sbin/slapd", "-h", "ldap:/// ldapi:///", "-g", string(OpenLDAPService), "-u", string(OpenLDAPService), "-F", "/etc/ldap/slapd.d"},
			want: Service{
				Name:            string(OpenLDAPService),
				ServiceType:     OpenLDAPService,
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 389}},
				IPAddress:       testIP127001,
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		{
			testName: "apache2-ubuntu-14.04",
			cmdLine:  []string{testApache2Sbin, "-k", testStart},
			want: Service{
				Name:            testApache,
				ServiceType:     ApacheService,
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 80}},
				IPAddress:       testIP127001,
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		{
			testName: "asterisk-ubuntu-14.04",
			cmdLine:  []string{"/usr/sbin/asterisk", "-p", "-U", string(AsteriskService)},
			want: Service{
				Name:            string(AsteriskService),
				ServiceType:     AsteriskService,
				ListenAddresses: nil,
				IPAddress:       testIP127001,
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
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 53}},
				IPAddress:       testIP127001,
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
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 143}},
				IPAddress:       testIP127001,
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		{
			testName: "ejabberd-ubuntu-14.04",
			cmdLine: []string{
				"/usr/lib/erlang/erts-5.10.4/bin/beam", "-K", testFalse, "-P", "250000", "--", testRoot, testErlangLibDir, testProgname, processErl, "--", testHome, "/var/lib/ejabberd", "--", testSname,
				string(EjabberService), "-pa", "/usr/lib/ejabberd/ebin", "-s", string(EjabberService), "-kernel", "inetrc", "\"/etc/ejabberd/inetrc\"", "-ejabberd", "config", "\"/etc/ejabberd/ejabberd.cfg\"", "log_path",
				"\"/var/log/ejabberd/ejabberd.log\"", "erlang_log_path", "\"/var/log/ejabberd/erlang.log\"", testSasl, testSaslErrorLogger, testFalse, "-mnesia", "dir", "\"/var/lib/ejabberd\"",
				"-smp", "disable", testNoshell, testNoshell, testNoinput,
			},
			want: Service{
				Name:            string(EjabberService),
				ServiceType:     EjabberService,
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 5222}},
				IPAddress:       testIP127001,
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		{
			testName: "rabbitmq-ubuntu-14.04",
			cmdLine: []string{
				"/usr/lib/erlang/erts-5.10.4/bin/beam", "-W", "w", "-K", testTrue, "-A30", "-P", "1048576", "--", testRoot, testErlangLibDir, testProgname, processErl, "--", testHome, "/var/lib/rabbitmq",
				"--", "-pa", "/usr/lib/rabbitmq/lib/rabbitmq_server-3.2.4/sbin/../ebin", testNoshell, testNoinput, "-s", "rabbit", "boot", testSname, "rabbit@trusty", "-boot", "start_sasl", "-kernel",
				"inet_default_connect_options", "[{nodelay,true}]", testSasl, "errlog_type", "error", testSasl, testSaslErrorLogger, testFalse, testRabbit, "error_logger",
				"{file,\"/var/log/rabbitmq/rabbit@trusty.log\"}", testRabbit, testSaslErrorLogger, "{file,\"/var/log/rabbitmq/rabbit@trusty-sasl.log\"}", testRabbit, "enabled_plugins_file",
				"\"/etc/rabbitmq/enabled_plugins\"", testRabbit, "plugins_dir", "\"/usr/lib/rabbitmq/lib/rabbitmq_server-3.2.4/sbin/../plugins\"", testRabbit, "plugins_expand_dir",
				"\"/var/lib/rabbitmq/mnesia/rabbit@trusty-plugins-expand\"", testOsMon, "start_cpu_sup", testFalse, testOsMon, "start_disksup", testFalse, testOsMon, "start_memsup", testFalse, "-mnesia",
				"dir", "\"/var/lib/rabbitmq/mnesia/rabbit@trusty\"",
			},
			want: Service{
				Name:            string(RabbitMQService),
				ServiceType:     RabbitMQService,
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 5672}},
				IPAddress:       testIP127001,
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
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 27017}},
				IPAddress:       testIP127001,
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		{
			testName: "mosquitto-ubuntu-14.04",                                               //nolint:misspell
			cmdLine:  []string{"/usr/sbin/mosquitto", "-c", "/etc/mosquitto/mosquitto.conf"}, //nolint:misspell
			want: Service{
				Name:            string(MosquittoService),
				ServiceType:     MosquittoService,
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 1883}},
				IPAddress:       testIP127001,
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		{
			testName: "redis-ubuntu-14.04",
			cmdLine:  []string{"/usr/bin/redis-server 127.0.0.1:6379"},
			want: Service{
				Name:            testRedis,
				ServiceType:     RedisService,
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 6379}},
				IPAddress:       testIP127001,
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		{
			testName: "memcached-ubuntu-14.04",
			cmdLine:  []string{testMemcachedBin, "-", "64", "-p", "11211", "-u", testMemcache, "-l", testIP127001},
			want: Service{
				Name:            testMemcached,
				ServiceType:     MemcachedService,
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 11211}},
				IPAddress:       testIP127001,
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		{
			testName: "squid-ubuntu-14.04",
			cmdLine:  []string{"/usr/sbin/squid3", "-N", "-YC", "-f", "/etc/squid3/squid.conf"},
			want: Service{
				Name:            string(SquidService),
				ServiceType:     SquidService,
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 3128}},
				IPAddress:       testIP127001,
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		{
			testName: "postgresql-ubuntu-14.04",
			cmdLine:  []string{"/usr/lib/postgresql/9.3/bin/postgres", "-D", "/var/lib/postgresql/9.3/main", "-c", "config_file=/etc/postgresql/9.3/main/postgresql.conf"},
			want: Service{
				Name:            testPostgreSQL,
				ServiceType:     PostgreSQLService,
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 5432}},
				IPAddress:       testIP127001,
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		{
			testName: "zookeeper-ubuntu-14.04",
			cmdLine: []string{
				"/usr/bin/java", testCp,
				"/etc/zookeeper/conf:/usr/share/java/jline.jar:/usr/share/java/log4j-1.2.jar:/usr/share/java/xercesImpl.jar:/usr/share/java/xmlParserAPIs.jar:/usr/share/java/netty.jar:/usr/share/java/slf4j-api.jar:/usr/share/java/slf4j-log4j12.jar:/usr/share/java/zookeeper.jar",
				"-Dcom.sun.management.jmxremote", "-Dcom.sun.management.jmxremote.local.only=false", "-Dzookeeper.log.dir=/var/log/zookeeper", "-Dzookeeper.root.logger=INFO,ROLLINGFILE",
				"org.apache.zookeeper.server.quorum.QuorumPeerMain", "/etc/zookeeper/conf/zoo.cfg",
			},
			want: Service{
				Name:            "zookeeper",
				ServiceType:     ZookeeperService,
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 2181}},
				IPAddress:       testIP127001,
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
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 4505}},
				IPAddress:       testIP127001,
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
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 25}},
				IPAddress:       testIP127001,
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		{
			testName: "nginx-ubuntu-14.04",
			cmdLine:  []string{testNginxMasterSbin},
			want: Service{
				Name:            testNginx,
				ServiceType:     NginxService,
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 80}},
				IPAddress:       testIP127001,
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
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 25}},
				IPAddress:       testIP127001,
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
				IPAddress:       testIP127001,
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
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 6082}},
				IPAddress:       testIP127001,
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		// Service from Ubuntu 16.04, default config
		{
			testName: "elasticsearch-ubuntu-16.04",
			cmdLine: []string{
				"/usr/lib/jvm/java-8-openjdk-amd64/bin/java", "-Xms256m", testXmx1g, "-Djava.awt.headless=true", "-XX:+UseParNewGC", "-XX:+UseConcMarkSweepGC", "-XX:CMSInitiatingOccupancyFraction=75",
				"-XX:+UseCMSInitiatingOccupancyOnly", "-XX:+HeapDumpOnOutOfMemoryError", "-XX:+DisableExplicitGC", testFileEncodingUTF8, "-Delasticsearch", "-Des.pidfile=/var/run/elasticsearch.pid",
				"-Des.path.home=/usr/share/elasticsearch", testCp,
				":/usr/share/java/lucene-sandbox-4.10.4.jar:/usr/share/java/sigar.jar:/usr/share/java/lucene-analyzers-morfologik-4.10.4.jar:/usr/share/java/spatial4j-0.4.1.jar:/usr/share/java/lucene-expressions-4.10.4.jar:/usr/share/java/lucene-analyzers-uima-4.10.4.jar:/usr/share/java/groovy-all-2.x.jar:/usr/share/java/lucene-analyzers-kuromoji-4.10.4.jar:/usr/share/java/lucene-facet-4.10.4.jar:/usr/share/java/jna.jar:/usr/share/java/lucene-analyzers-common-4.10.4.jar:/usr/share/java/lucene-core-4.10.4.jar:/usr/share/java/apache-log4j-extras-1.2.17.jar:/usr/share/java/lucene-queries-4.10.4.jar:/usr/share/java/lucene-demo-4.10.4.jar:/usr/share/java/lucene-suggest-4.10.4.jar:/usr/share/java/lucene-analyzers-stempel-4.10.4.jar:/usr/share/java/lucene-highlighter-4.10.4.jar:/usr/share/java/lucene-memory-4.10.4.jar:/usr/share/java/lucene-classification-4.10.4.jar:/usr/share/java/lucene-replicator-4.10.4.jar:/usr/share/java/lucene-grouping-4.10.4.jar:/usr/share/java/log4j-1.2-1.2.17.jar:/usr/share/java/lucene-join-4.10.4.jar:/usr/share/java/lucene-analyzers-smartcn-4.10.4.jar:/usr/share/java/lucene-spatial-4.10.4.jar:/usr/share/java/elasticsearch-1.7.3.jar:/usr/share/java/lucene-codecs-4.10.4.jar:/usr/share/java/lucene-misc-4.10.4.jar:/usr/share/java/lucene-queryparser-4.10.4.jar:/usr/share/java/lucene-test-framework-4.10.4.jar:/usr/share/java/jts.jar:/usr/share/java/lucene-benchmark-4.10.4.jar:/usr/share/java/lucene-analyzers-icu-4.10.4.jar:/usr/share/java/lucene-analyzers-phonetic-4.10.4.jar:",
				"-Des.default.config=/etc/elasticsearch/elasticsearch.yml", "-Des.default.path.home=/usr/share/elasticsearch", "-Des.default.path.logs=/var/log/elasticsearch",
				"-Des.default.path.data=/var/lib/elasticsearch", "-Des.default.path.work=/tmp/elasticsearch", "-Des.default.path.conf=/etc/elasticsearch", elasticsearchMainClass,
			},
			want: Service{
				Name:            string(ElasticSearchService),
				ServiceType:     ElasticSearchService,
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 9200}},
				IPAddress:       testIP127001,
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		{
			testName: "squid-ubuntu-16.04",
			cmdLine:  []string{"/usr/sbin/squid", "-YC", "-f", "/etc/squid/squid.conf"},
			want: Service{
				Name:            string(SquidService),
				ServiceType:     SquidService,
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 3128}},
				IPAddress:       testIP127001,
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		{
			testName: string(OpenVPNService),
			cmdLine:  []string{"/usr/sbin/openvpn", "--writepid", "/run/openvpn/server.pid", "--daemon", "ovpn-server", "--cd", "/etc/openvpn", "--config", "/etc/openvpn/server.conf", "--script-security", "2"},
			want: Service{
				Name:            string(OpenVPNService),
				ServiceType:     OpenVPNService,
				ListenAddresses: nil,
				IPAddress:       testIP127001,
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
				IPAddress:       testIP127001,
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		{
			testName: testHAProxy,
			cmdLine:  []string{testHAProxy, "-f", "/usr/local/etc/haproxy/haproxy.cfg"},
			want: Service{
				Name:            testHAProxy,
				ServiceType:     HAProxyService,
				ListenAddresses: nil,
				IPAddress:       testIP127001,
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		{
			testName: testUWSGI,
			cmdLine:  []string{testUWSGI, "--ini", "/srv/app/deploy/uwsgi.ini"},
			want: Service{
				Name:            testUWSGI,
				ServiceType:     UWSGIService,
				ListenAddresses: nil,
				IPAddress:       testIP127001,
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		{
			testName: "uwsgi-with-auto-procname-1",
			cmdLine:  []string{"uWSGI master"},
			want: Service{
				Name:            testUWSGI,
				ServiceType:     UWSGIService,
				ListenAddresses: nil,
				IPAddress:       testIP127001,
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		{
			testName: "uwsgi-with-auto-procname-2",
			cmdLine:  []string{"uWSGI worker 1"},
			want: Service{
				Name:            testUWSGI,
				ServiceType:     UWSGIService,
				ListenAddresses: nil,
				IPAddress:       testIP127001,
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		{
			testName: "service-install",
			cmdLine:  []string{"apt", "install", "apache2", "redis-server", testPostgreSQL, string(MosquittoService), "slapd", "squid3"},
			noMatch:  true,
		},
		{
			testName: "docker-run",
			cmdLine:  []string{"docker", "run", "-d", "--name", testMySQLD, testMySQL},
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
			cmdLine:  []string{"/usr/lib/erlang/erts-6.2/bin/beam", "--", testRoot, testErlangLibDir, testProgname, processErl, "--", testHome, "/root", "--"},
			noMatch:  true,
		},
		{
			testName: "mysql-with-space",
			cmdLine:  []string{"/opt/program files/mysql/mysqld"},
			want: Service{
				Name:            testMySQL,
				ServiceType:     MySQLService,
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 3306}},
				IPAddress:       testIP127001,
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		{
			testName: "php-fpm",
			cmdLine:  []string{testPHPFPMMasterConf},
			want: Service{
				Name:            testPHPFPM,
				ServiceType:     PHPFPMService,
				ListenAddresses: nil,
				IPAddress:       testIP127001,
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
			containerID: testContainerHash,
			containerIP: testIP127001,
			cmdLine:     []string{testNginxMasterDaemon},
			want: Service{
				Name:            testNginx,
				ServiceType:     NginxService,
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 80}},
				IPAddress:       testIP127001,
				ContainerID:     testContainerHash,
				IgnoredPorts:    map[int]bool{},
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		{
			testName: string(BitBucketService),
			cmdLine: []string{
				"/usr/lib/jvm/java-11-openjdk-amd64//bin/java", "-classpath /srv/bitbucket/dist/current/app", "-Datlassian.plugins.enable.wait=300", "-Datlassian.standalone=BITBUCKET",
				"-Dbitbucket.home=/srv/bitbucket/home", "-Dbitbucket.install=/srv/bitbucket/dist/current", "-Xms512m", testXmx1g, testUseG1GC, testFileEncodingUTF8, "-Dsun.jnu.encoding=UTF-8",
				"-Djava.io.tmpdir=/srv/bitbucket/home/tmp", "-Djava.library.path=/srv/bitbucket/dist/current/lib/native;/srv/bitbucket/home/lib/native",
				bitbucketServerLauncher, testStart,
			},
			containerID:      "",
			netstatAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIPZero, Port: 7990}},
			want: Service{
				Name:            string(BitBucketService),
				ServiceType:     BitBucketService,
				ContainerID:     "",
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIPZero, Port: 7990}},
				IPAddress:       testIP127001,
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
				"-Dbitbucket.home=/srv/bitbucket/home", "-Dbitbucket.install=/srv/bitbucket/dist/current", "-Xms512m", testXmx1g, testUseG1GC, testFileEncodingUTF8, "-Dsun.jnu.encoding=UTF-8",
				"-Djava.io.tmpdir=/srv/bitbucket/home/tmp", "-Djava.library.path=/srv/bitbucket/dist/current/lib/native;/srv/bitbucket/home/lib/native",
				bitbucketServerLauncher, testStart,
			},
			containerID: "1234",
			containerIP: testIP127001,
			containerLabels: map[string]string{
				"glouton.check.ignore.port.8080": testTrue,
			},
			netstatAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 7990}},
			want: Service{
				Name:            string(BitBucketService),
				ServiceType:     BitBucketService,
				ContainerID:     "1234",
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 7990}},
				IPAddress:       testIP127001,
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
			testName: string(KafkaService),
			cmdLine: []string{
				"/usr/local/openjdk-11/bin/java", "-Xmx1G", "-Xms1G", "-server", testUseG1GC, "-XX:MaxGCPauseMillis=20",
				"-XX:+ExplicitGCInvokesConcurrent", "-XX:MaxInlineLevel=15", "-Djava.awt.headless=true",
				"-Xlog:gc*:file=/opt/kafka/bin/../logs/kafkaServer-gc.log:time,tags:filecount=10,filesize=100M",
				"-Dcom.sun.management.jmxremote", "-Dcom.sun.management.jmxremote.authenticate=false", "-Dcom.sun.management.jmxremote.ssl=false",
				"-Djava.rmi.server.hostname=localhost", "-Dcom.sun.management.jmxremote.rmi.port=1099", "-Dcom.sun.management.jmxremote.port=1099",
				"-Dkafka.logs.dir=/opt/kafka/bin/../logs", "-Dlog4j.configuration=file:/opt/kafka/bin/../config/log4j.properties",
				testCp, "/opt/kafka/bin/../libs/activation-1.1.1.jar:/opt/kafka/bin/../libs/aopalliance-repackaged-2.6.1.jar",
				"kafka.Kafka", "/opt/kafka/config/server.properties",
			},
			containerID:      "1234",
			containerIP:      testIP127001,
			netstatAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 9092}},
			want: Service{
				Name:            string(KafkaService),
				ServiceType:     KafkaService,
				ContainerID:     "1234",
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 9092}},
				IPAddress:       testIP127001,
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
			containerIP:      testIP127001,
			netstatAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 9092}},
			noMatch:          true,
		},
		{
			testName:         "nats",
			cmdLine:          []string{"/nats-server", "--config nats-server.conf"},
			containerID:      "1234",
			containerIP:      testIP127001,
			netstatAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 4222}},
			want: Service{
				Name:            "nats",
				ServiceType:     NatsService,
				ContainerID:     "1234",
				IgnoredPorts:    map[int]bool{},
				IPAddress:       testIP127001,
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 4222}},
				Active:          true,
				HasNetstatInfo:  true,
				LastNetstatInfo: t0,
				LastTimeSeen:    t0,
			},
		},
		{
			testName: "fail2ban",
			cmdLine:  []string{"/usr/bin/python3", "/usr/bin/fail2ban-server", "-xf", testStart},
			want: Service{
				Name:         "fail2ban",
				ServiceType:  Fail2banService,
				IPAddress:    testIP127001,
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
				IPAddress:       testIP127001,
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
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 8080}},
				IPAddress:       testIP127001,
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		{
			testName: string(UPSDService),
			cmdLine:  []string{"/lib/nut/upsd"},
			want: Service{
				Name:            string(UPSDService),
				ServiceType:     UPSDService,
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 3493}},
				IPAddress:       testIP127001,
				Active:          true,
				LastTimeSeen:    t0,
			},
		},
		{
			testName:         "nginx-alternative-port-ignore",
			cmdLine:          []string{testNginxMasterSbin},
			containerID:      testContainerHash,
			containerIP:      testIP1721602,
			netstatAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP1721602, Port: 80}},
			containerLabels: map[string]string{
				"glouton.check.ignore.port.80": testTrue,
			},
			want: Service{
				Name:        testNginx,
				ServiceType: NginxService,
				ContainerID: testContainerHash,
				// It's not applyOverride which remove ignored ports, but we are in ignored ports
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP1721602, Port: 80}},
				IPAddress:       testIP1721602,
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
			cmdLine:          []string{testNginxMasterSbin},
			containerID:      testContainerHash,
			containerName:    testNginxAltKey,
			containerIP:      testIP1721602,
			netstatAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP1721602, Port: 80}},
			containerLabels: map[string]string{
				"glouton.ignore_ports": "74,75,80",
			},
			want: Service{
				Name:          testNginx,
				Instance:      testNginxAltKey,
				ServiceType:   NginxService,
				ContainerID:   testContainerHash,
				ContainerName: testNginxAltKey,
				// It's not applyOverride which remove ignored ports, but we are in ignored ports
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP1721602, Port: 80}},
				IPAddress:       testIP1721602,
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
			cmdLine:          []string{testNginxMasterSbin},
			containerID:      testContainerHash,
			containerName:    testNginxAltKey,
			containerIP:      testIP1721602,
			netstatAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP1721602, Port: 80}},
			containerLabels: map[string]string{
				"glouton.port": "8080",
			},
			want: Service{
				Name:          testNginx,
				Instance:      testNginxAltKey,
				ServiceType:   NginxService,
				ContainerID:   testContainerHash,
				ContainerName: testNginxAltKey,
				// This test is done in two path. In dynamic.go we add the option to Config.
				// in discovery (applyOverrideInPlance) we apply the config override.
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP1721602, Port: 80}},
				IPAddress:       testIP1721602,
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
			AllowedLabelOverrides: config.DefaultConfig().Container.EffectiveAllowedLabelOverrides(),
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
					CmdLineList: []string{testInit},
				},
				{
					PID:         2,
					CmdLineList: []string{testBash, testStartServiceSh, testRedis},
				},
				{
					PID:         3,
					CmdLineList: []string{testRedisServerListen},
				},
			},
			netstatAddressesPerPID: map[int][]facts.ListenAddress{
				2: {
					{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 1234},
				},
				3: {
					{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 6379},
				},
			},
			want: []Service{
				{
					Name:            testRedis,
					ServiceType:     RedisService,
					ContainerID:     "",
					ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 6379}},
					IPAddress:       testIP127001,
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
					CmdLineList: []string{testInit},
				},
				{
					PID:         2,
					CmdLineList: []string{testBash, testStartServiceSh, testRedis},
				},
				{
					PID:         3,
					CmdLineList: []string{testRedisServerListen},
				},
			},
			netstatAddressesPerPID: map[int][]facts.ListenAddress{},
			want: []Service{
				{
					Name:            testRedis,
					ServiceType:     RedisService,
					ContainerID:     "",
					ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 6379}},
					IPAddress:       testIP127001,
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
					CmdLineList: []string{testInit},
				},
				{
					PID:         2,
					CmdLineList: []string{testBash, testStartServiceSh, testRedis},
				},
				{
					PID:         3,
					CmdLineList: []string{testRedisServerListen},
				},
				{
					PID:         4,
					CmdLineList: []string{testRedisServerListen}, // this is the process that dump to RDB file
				},
				{
					PID:         5,
					CmdLineList: []string{"redis-server *:6380"},
				},
			},
			netstatAddressesPerPID: map[int][]facts.ListenAddress{
				2: {
					{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 1234},
				},
				3: {
					{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 6379},
				},
				4: {
					// This will likely not happen, because:
					// 1) the process that does RDB dump may not actually listen on this port.
					// 2) Glouton netstat does NOT provided list all PID that listen on the same port. (netstat neither).
					{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 6379},
				},
				5: {
					{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 6380},
				},
			},
			want: []Service{
				{
					Name:        testRedis,
					ServiceType: RedisService,
					ContainerID: "",
					ListenAddresses: []facts.ListenAddress{
						{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 6379},
						{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 6380},
					},
					IPAddress:       testIP127001,
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
					CmdLineList: []string{testNginxMasterDaemon},
				},
				{
					PID:         4,
					CmdLineList: []string{"nginx: worker process"},
				},
			},
			netstatAddressesPerPID: map[int][]facts.ListenAddress{
				3: {
					{NetworkFamily: tcpProtocol, Address: "10.2.0.1", Port: 6443},
				},
				// We aren't guarantee to have netstat information for PID 4:
				// netstat only show one PID per listening socket
				// internal netstat (using gopsutil) also show one PID per listening socket
			},
			want: []Service{
				{
					Name:        testNginx,
					ServiceType: NginxService,
					ContainerID: "",
					ListenAddresses: []facts.ListenAddress{
						{NetworkFamily: tcpProtocol, Address: "10.2.0.1", Port: 6443},
					},
					IPAddress:       testIP127001,
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
					CmdLineList: []string{testNginxMasterDaemon},
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
					{NetworkFamily: tcpProtocol, Address: "10.0.2.1", Port: 6443},
				},
			},
			want: []Service{
				{
					Name:        testNginx,
					ServiceType: NginxService,
					ContainerID: "",
					ListenAddresses: []facts.ListenAddress{
						{NetworkFamily: tcpProtocol, Address: "10.0.2.1", Port: 6443},
					},
					IPAddress:       testIP127001,
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
					CmdLineList: []string{testNginxMasterDaemon},
					ContainerID: testID001,
				},
				{
					PID:         4,
					CmdLineList: []string{testPHPFPMMasterConf},
					ContainerID: testID002,
				},
			},
			netstatAddressesPerPID: nil, // netstat won't provide information for containers
			containers: map[string]facts.FakeContainer{
				testID001: {
					FakePrimaryAddress:  testIP17217049,
					FakeListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP17217049, Port: 80}},
				},
				testID002: {
					FakePrimaryAddress:  testIP17217050,
					FakeListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP17217050, Port: 9000}},
				},
			},
			want: []Service{
				{
					Name:        testNginx,
					ServiceType: NginxService,
					ContainerID: testID001,
					ListenAddresses: []facts.ListenAddress{
						{NetworkFamily: tcpProtocol, Address: testIP17217049, Port: 80},
					},
					IPAddress:       testIP17217049,
					IgnoredPorts:    map[int]bool{},
					HasNetstatInfo:  true,
					LastNetstatInfo: t0,
					Active:          true,
					LastTimeSeen:    t0,
				},
				{
					Name:        testPHPFPM,
					ServiceType: PHPFPMService,
					ContainerID: testID002,
					ListenAddresses: []facts.ListenAddress{
						{NetworkFamily: tcpProtocol, Address: testIP17217050, Port: 9000},
					},
					IPAddress:       testIP17217050,
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
					CmdLineList: []string{testNginxMasterDaemon},
					ContainerID: testID001,
				},
				{
					PID:         4,
					CmdLineList: []string{testPHPFPMMasterConf},
					ContainerID: testID002,
				},
			},
			netstatAddressesPerPID: nil, // netstat won't provide information for containers
			containers: map[string]facts.FakeContainer{
				testID001: {
					FakePrimaryAddress:  testIP17217049,
					FakeListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP17217049, Port: 80}},
				},
				testID002: {
					FakePrimaryAddress: testIP17217050,
				},
			},
			want: []Service{
				{
					Name:        testNginx,
					ServiceType: NginxService,
					ContainerID: testID001,
					ListenAddresses: []facts.ListenAddress{
						{NetworkFamily: tcpProtocol, Address: testIP17217049, Port: 80},
					},
					IPAddress:       testIP17217049,
					IgnoredPorts:    map[int]bool{},
					HasNetstatInfo:  true,
					LastNetstatInfo: t0,
					Active:          true,
					LastTimeSeen:    t0,
				},
				{
					Name:            testPHPFPM,
					ServiceType:     PHPFPMService,
					ContainerID:     testID002,
					ListenAddresses: []facts.ListenAddress{},
					IPAddress:       testIP17217050,
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
					CmdLineList: []string{testNginxMasterDaemon},
					ContainerID: testID001,
				},
				{
					PID:         4,
					CmdLineList: []string{testPHPFPMMasterConf},
					ContainerID: testID002,
				},
			},
			netstatAddressesPerPID: nil, // netstat won't provide information for containers
			containers: map[string]facts.FakeContainer{
				testID001: {
					FakePrimaryAddress: testIP17217049,
				},
				testID002: {
					FakePrimaryAddress: testIP17217050,
				},
			},
			want: []Service{
				{
					Name:        testNginx,
					ServiceType: NginxService,
					ContainerID: testID001,
					ListenAddresses: []facts.ListenAddress{
						{NetworkFamily: tcpProtocol, Address: testIP17217049, Port: 80},
					},
					IPAddress:      testIP17217049,
					IgnoredPorts:   map[int]bool{},
					HasNetstatInfo: false,
					Active:         true,
					LastTimeSeen:   t0,
				},
				{
					Name:            testPHPFPM,
					ServiceType:     PHPFPMService,
					ContainerID:     testID002,
					ListenAddresses: []facts.ListenAddress{},
					IPAddress:       testIP17217050,
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
					CmdLineList: []string{testNginxMasterDaemon},
					ContainerID: testID001,
				},
				{
					PID:         4,
					CmdLineList: []string{testPHPFPMMasterConf},
					ContainerID: testID001,
				},
			},
			netstatAddressesPerPID: nil, // netstat won't provide information for containers
			containers: map[string]facts.FakeContainer{
				testID001: {
					FakePrimaryAddress: testIP17217049,
					FakeListenAddresses: []facts.ListenAddress{
						{NetworkFamily: tcpProtocol, Address: testIP17217049, Port: 80},
						{NetworkFamily: tcpProtocol, Address: testIP17217049, Port: 9000},
					},
				},
			},
			want: []Service{
				{
					Name:        testNginx,
					ServiceType: NginxService,
					ContainerID: testID001,
					ListenAddresses: []facts.ListenAddress{
						{NetworkFamily: tcpProtocol, Address: testIP17217049, Port: 80},
					},
					IPAddress:       testIP17217049,
					IgnoredPorts:    map[int]bool{},
					HasNetstatInfo:  true,
					LastNetstatInfo: t0,
					Active:          true,
					LastTimeSeen:    t0,
				},
				{
					Name:            testPHPFPM,
					ServiceType:     PHPFPMService,
					ContainerID:     testID001,
					ListenAddresses: []facts.ListenAddress{},
					IPAddress:       testIP17217049,
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
					CmdLineList: []string{testNginxMasterDaemon},
					ContainerID: testID001,
				},
				{
					PID:         4,
					CmdLineList: []string{testPHPFPMMasterConf},
					ContainerID: testID001,
				},
			},
			netstatAddressesPerPID: nil, // netstat won't provide information for containers
			containers: map[string]facts.FakeContainer{
				testID001: {
					FakePrimaryAddress:  testIP17217049,
					FakeListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP17217049, Port: 80}},
				},
			},
			want: []Service{
				{
					Name:        testNginx,
					ServiceType: NginxService,
					ContainerID: testID001,
					ListenAddresses: []facts.ListenAddress{
						{NetworkFamily: tcpProtocol, Address: testIP17217049, Port: 80},
					},
					IPAddress:       testIP17217049,
					IgnoredPorts:    map[int]bool{},
					HasNetstatInfo:  true,
					LastNetstatInfo: t0,
					Active:          true,
					LastTimeSeen:    t0,
				},
				{
					Name:            testPHPFPM,
					ServiceType:     PHPFPMService,
					ContainerID:     testID001,
					ListenAddresses: []facts.ListenAddress{},
					IPAddress:       testIP17217049,
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
					CmdLineList: []string{testNginxMasterDaemon},
					ContainerID: testID001,
				},
				{
					PID:         4,
					CmdLineList: []string{testPHPFPMMasterConf},
					ContainerID: testID001,
				},
			},
			netstatAddressesPerPID: nil, // netstat won't provide information for containers
			containers: map[string]facts.FakeContainer{
				testID001: {
					FakePrimaryAddress: testIP17217049,
				},
			},
			want: []Service{
				{
					Name:        testNginx,
					ServiceType: NginxService,
					ContainerID: testID001,
					ListenAddresses: []facts.ListenAddress{
						{NetworkFamily: tcpProtocol, Address: testIP17217049, Port: 80},
					},
					IPAddress:      testIP17217049,
					IgnoredPorts:   map[int]bool{},
					HasNetstatInfo: false,
					Active:         true,
					LastTimeSeen:   t0,
				},
				{
					Name:            testPHPFPM,
					ServiceType:     PHPFPMService,
					ContainerID:     testID001,
					ListenAddresses: []facts.ListenAddress{},
					IPAddress:       testIP17217049,
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
					CmdLineList: []string{testNginxMasterDaemon},
					ContainerID: testID001,
				},
				{
					PID:         4,
					CmdLineList: []string{testPHPFPMMasterConf},
					ContainerID: testID001,
				},
			},
			netstatAddressesPerPID: nil, // netstat won't provide information for containers
			containers: map[string]facts.FakeContainer{
				testID001: {
					FakePrimaryAddress:  testIP17217049,
					FakeListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP17217049, Port: 8080}},
				},
			},
			want: []Service{
				{
					Name:            testNginx,
					ServiceType:     NginxService,
					ContainerID:     testID001,
					ListenAddresses: []facts.ListenAddress{},
					IPAddress:       testIP17217049,
					IgnoredPorts:    map[int]bool{},
					HasNetstatInfo:  false,
					Active:          true,
					LastTimeSeen:    t0,
				},
				{
					Name:            testPHPFPM,
					ServiceType:     PHPFPMService,
					ContainerID:     testID001,
					ListenAddresses: []facts.ListenAddress{},
					IPAddress:       testIP17217049,
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
					CmdLineList: []string{testNginxMasterDaemon},
					ContainerID: testID001,
				},
				{
					PID:         4,
					CmdLineList: []string{testApache2Sbin, "-k", testStart},
					ContainerID: testID001,
				},
			},
			netstatAddressesPerPID: nil, // netstat won't provide information for containers
			containers: map[string]facts.FakeContainer{
				testID001: {
					FakePrimaryAddress:  testIP17217049,
					FakeListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP17217049, Port: 80}},
				},
			},
			want: []Service{
				{
					Name:            testNginx,
					ServiceType:     NginxService,
					ContainerID:     testID001,
					ListenAddresses: []facts.ListenAddress{},
					IPAddress:       testIP17217049,
					IgnoredPorts:    map[int]bool{},
					HasNetstatInfo:  false,
					Active:          true,
					LastTimeSeen:    t0,
				},
				{
					Name:            testApache,
					ServiceType:     ApacheService,
					ContainerID:     testID001,
					ListenAddresses: []facts.ListenAddress{},
					IPAddress:       testIP17217049,
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
					CmdLineList: []string{testNginxMasterDaemon},
					ContainerID: testID001,
				},
				{
					PID:         4,
					CmdLineList: []string{testApache2Sbin, "-k", testStart},
					ContainerID: testID001,
				},
			},
			netstatAddressesPerPID: nil, // netstat won't provide information for containers
			containers: map[string]facts.FakeContainer{
				testID001: {
					FakePrimaryAddress: testIP17217049,
					FakeListenAddresses: []facts.ListenAddress{
						{NetworkFamily: tcpProtocol, Address: testIP17217049, Port: 80},
						{NetworkFamily: tcpProtocol, Address: testIP17217049, Port: 8080},
					},
				},
			},
			want: []Service{
				{
					Name:            testNginx,
					ServiceType:     NginxService,
					ContainerID:     testID001,
					ListenAddresses: []facts.ListenAddress{},
					IPAddress:       testIP17217049,
					IgnoredPorts:    map[int]bool{},
					HasNetstatInfo:  false,
					Active:          true,
					LastTimeSeen:    t0,
				},
				{
					Name:            testApache,
					ServiceType:     ApacheService,
					ContainerID:     testID001,
					ListenAddresses: []facts.ListenAddress{},
					IPAddress:       testIP17217049,
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
					CmdLineList: []string{testNginxMasterDaemon},
					ContainerID: testID001,
				},
				{
					PID:         4,
					CmdLineList: []string{testPHPFPMMasterConf},
					ContainerID: testID001,
				},
			},
			netstatAddressesPerPID: nil, // netstat won't provide information for containers
			containers: map[string]facts.FakeContainer{
				testID001: {
					FakePrimaryAddress: testIP17217049,
					FakeListenAddresses: []facts.ListenAddress{
						{NetworkFamily: tcpProtocol, Address: testIP17217049, Port: 80},
						{NetworkFamily: tcpProtocol, Address: testIP17217049, Port: 443},
					},
				},
			},
			want: []Service{
				{
					Name:        testNginx,
					ServiceType: NginxService,
					ContainerID: testID001,
					ListenAddresses: []facts.ListenAddress{
						{NetworkFamily: tcpProtocol, Address: testIP17217049, Port: 80},
					},
					IPAddress:       testIP17217049,
					IgnoredPorts:    map[int]bool{},
					HasNetstatInfo:  true,
					LastNetstatInfo: t0,
					Active:          true,
					LastTimeSeen:    t0,
				},
				{
					Name:            testPHPFPM,
					ServiceType:     PHPFPMService,
					ContainerID:     testID001,
					ListenAddresses: []facts.ListenAddress{},
					IPAddress:       testIP17217049,
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
					CmdLineList: []string{testNginxMasterDaemon},
					ContainerID: testID001,
				},
				{
					PID:         4,
					CmdLineList: []string{testRedisServerListen},
					ContainerID: testID001,
				},
			},
			netstatAddressesPerPID: nil, // netstat won't provide information for containers
			containers: map[string]facts.FakeContainer{
				testID001: {
					FakePrimaryAddress: testIP17217049,
					FakeListenAddresses: []facts.ListenAddress{
						{NetworkFamily: tcpProtocol, Address: testIP17217049, Port: 80},
						{NetworkFamily: tcpProtocol, Address: testIP17217049, Port: 6379},
					},
				},
			},
			want: []Service{
				{
					Name:        testNginx,
					ServiceType: NginxService,
					ContainerID: testID001,
					ListenAddresses: []facts.ListenAddress{
						{NetworkFamily: tcpProtocol, Address: testIP17217049, Port: 80},
					},
					IPAddress:       testIP17217049,
					IgnoredPorts:    map[int]bool{},
					HasNetstatInfo:  true,
					LastNetstatInfo: t0,
					Active:          true,
					LastTimeSeen:    t0,
				},
				{
					Name:        testRedis,
					ServiceType: RedisService,
					ContainerID: testID001,
					ListenAddresses: []facts.ListenAddress{
						{NetworkFamily: tcpProtocol, Address: testIP17217049, Port: 6379},
					},
					IPAddress:       testIP17217049,
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
					CmdLineList: []string{testNginxMasterDaemon},
					ContainerID: testID001,
				},
				{
					PID:         4,
					CmdLineList: []string{testRedisServerListen},
					ContainerID: testID001,
				},
			},
			netstatAddressesPerPID: nil, // netstat won't provide information for containers
			containers: map[string]facts.FakeContainer{
				testID001: {
					FakePrimaryAddress: testIP17217049,
					FakeListenAddresses: []facts.ListenAddress{
						{NetworkFamily: tcpProtocol, Address: testIP17217049, Port: 8080},
						{NetworkFamily: tcpProtocol, Address: testIP17217049, Port: 6379},
					},
				},
			},
			want: []Service{
				{
					Name:            testNginx,
					ServiceType:     NginxService,
					ContainerID:     testID001,
					ListenAddresses: []facts.ListenAddress{},
					IPAddress:       testIP17217049,
					IgnoredPorts:    map[int]bool{},
					HasNetstatInfo:  false,
					Active:          true,
					LastTimeSeen:    t0,
				},
				{
					Name:        testRedis,
					ServiceType: RedisService,
					ContainerID: testID001,
					ListenAddresses: []facts.ListenAddress{
						{NetworkFamily: tcpProtocol, Address: testIP17217049, Port: 6379},
					},
					IPAddress:       testIP17217049,
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
	defaultOverrides := config.DefaultConfig().Container.EffectiveAllowedLabelOverrides()

	cases := []struct {
		name                        string
		serviceFromDynamicDiscovery Service
		allowedLabelOverrides       []string
		expectedConfigResult        config.Service
	}{
		{
			name:                  "ports-and-http_path",
			allowedLabelOverrides: defaultOverrides,
			serviceFromDynamicDiscovery: Service{
				container: facts.FakeContainer{
					FakeLabels: map[string]string{
						"glouton.port":         "8080",
						"glouton.ignore_ports": "9090,9091",
						"glouton.http_path":    testPath,
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
				HTTPPath:    testPath,
				Port:        8080,
				IgnorePorts: []int{9090, 9091},
				Address:     "192.168.0.1",
			},
		},
		{
			name:                  "tags",
			allowedLabelOverrides: defaultOverrides,
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
			name:                  "from-annotations",
			allowedLabelOverrides: defaultOverrides,
			serviceFromDynamicDiscovery: Service{
				container: facts.FakeContainer{
					FakeAnnotations: map[string]string{
						"glouton.tags":      "tags1,tags2",
						"glouton.http_path": testPath,
					},
				},
				Config: config.Service{
					// Dynamic discovery won't fill tags by itself,
					// they always come from container labels/annotations.
				},
			},
			expectedConfigResult: config.Service{
				Tags:     []string{"tags1", "tags2"},
				HTTPPath: testPath,
			},
		},
		{
			// check_command allows arbitrary command execution and must NOT be
			// honored when it isn't in the allow-list, even though other (safe)
			// labels on the same container are still applied.
			name:                  "dangerous-label-blocked-by-default",
			allowedLabelOverrides: defaultOverrides,
			serviceFromDynamicDiscovery: Service{
				container: facts.FakeContainer{
					FakeLabels: map[string]string{
						"glouton.port":          "8080",
						"glouton.check_command": "/bin/evil --pwn",
					},
				},
			},
			expectedConfigResult: config.Service{
				Port: 8080,
			},
		},
		{
			// When explicitly allowed (trusted environment), check_command is
			// honored. The allow-list is additive with the safe defaults.
			name:                  "dangerous-label-allowed-when-listed",
			allowedLabelOverrides: append(append([]string{}, defaultOverrides...), "check_command"),
			serviceFromDynamicDiscovery: Service{
				container: facts.FakeContainer{
					FakeLabels: map[string]string{
						"glouton.port":          "8080",
						"glouton.check_command": "/bin/check_something",
					},
				},
			},
			expectedConfigResult: config.Service{
				Port:         8080,
				CheckCommand: "/bin/check_something",
			},
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			service := tt.serviceFromDynamicDiscovery

			dd := NewDynamic(Option{
				PS:                    mockProcess{},
				Netstat:               mockNetstat{},
				ContainerInfo:         mockContainerInfo{},
				IsContainerIgnored:    facts.ContainerFilter{}.ContainerIgnored,
				AllowedLabelOverrides: tt.allowedLabelOverrides,
			})

			dd.fillConfigFromLabels(&service)

			if diff := cmp.Diff(tt.expectedConfigResult, service.Config); diff != "" {
				t.Fatalf("Unexpected config:\n%s", diff)
			}
		})
	}
}
