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

package discovery

import (
	"context"
	"glouton/facts"
	"os"
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
	result map[int][]facts.ListenAddress
}

func (mn mockNetstat) Netstat(ctx context.Context, processes map[int]facts.Process) (netstat map[int][]facts.ListenAddress, err error) {
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

func (mci mockContainerInfo) Containers(ctx context.Context, maxAge time.Duration, includeIgnored bool) (containers []facts.Container, err error) {
	res := make([]facts.Container, 0, len(mci.containers))

	for _, v := range mci.containers {
		res = append(res, v)
	}

	return res, nil
}

type mockFileReader struct {
	contents map[string]string
}

func (mfr mockFileReader) ReadFile(path string) ([]byte, error) {
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
		netstat: mockNetstat{result: map[int][]facts.ListenAddress{
			1547: {
				{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 11211},
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
func TestDynamicDiscoverySingle(t *testing.T) {
	cases := []struct {
		testName           string
		cmdLine            []string
		filesContent       map[string]string
		containerID        string
		netstatAddresses   []facts.ListenAddress
		containerAddresses []facts.ListenAddress
		containerIP        string
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
				ExtraAttributes: map[string]string{"username": "root", "password": "secret"},
				IgnoredPorts:    map[int]bool{},
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
				ExtraAttributes: map[string]string{"username": "root", "password": "secret"},
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
			},
		},
		{
			testName: "dovecot-ubuntu-14.04",
			cmdLine:  []string{"/usr/sbin/dovecot", "-F", "-c", "/etc/dovecot/dovecot.conf"},
			want: Service{
				Name:            "dovecot",
				ServiceType:     DovecoteService,
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 143}},
				IPAddress:       "127.0.0.1",
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
			},
		},
		{
			testName: "salt-master-ubuntu-14.04",
			cmdLine:  []string{"/usr/bin/python", "/usr/bin/salt-master"},
			want: Service{
				Name:            "salt-master",
				ServiceType:     SaltMasterService,
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 4505}},
				IPAddress:       "127.0.0.1",
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
			},
		},
		// Service from Ubunut 16.04, default config
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
			},
		},
		{
			testName: "chrome",
			cmdLine:  []string{"/opt/google/chrome/chrome http://127.0.0.1:5000/"},
			noMatch:  true,
		},
		{
			testName: "chrome2",
			cmdLine:  []string{"/opt/ google/chrome/chrome http://127.0.0.1:5000/"},
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
			netstat: mockNetstat{result: map[int][]facts.ListenAddress{
				42: c.netstatAddresses,
			}},
			containerInfo: mockContainerInfo{
				containers: map[string]facts.FakeContainer{
					c.containerID: {
						FakePrimaryAddress:  c.containerIP,
						FakeListenAddresses: c.containerAddresses,
						FakeEnvironment:     c.containerEnv,
						FakeLabels:          c.containerLabels,
					},
				},
			},
			fileReader: mockFileReader{
				contents: c.filesContent,
			},
		}

		srv, err := dd.Discovery(ctx, 0)
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

		if srv[0].Name != c.want.Name {
			t.Errorf("Case %s: Name == %#v, want %#v", c.testName, srv[0].Name, c.want.Name)
		}

		if srv[0].ServiceType != c.want.ServiceType {
			t.Errorf("Case %s: ServiceType == %#v, want %#v", c.testName, srv[0].ServiceType, c.want.ServiceType)
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

		if !reflect.DeepEqual(srv[0].IgnoredPorts, c.want.IgnoredPorts) {
			t.Errorf("Case %s: IgnoredPorts == %v, want %v", c.testName, srv[0].IgnoredPorts, c.want.IgnoredPorts)
		}

		if c.want.ExtraAttributes == nil {
			c.want.ExtraAttributes = make(map[string]string)
		}

		if !reflect.DeepEqual(srv[0].ExtraAttributes, c.want.ExtraAttributes) {
			t.Errorf("Case %s: ExtraAttributes == %v, want %v", c.testName, srv[0].ExtraAttributes, c.want.ExtraAttributes)
		}
	}
}
