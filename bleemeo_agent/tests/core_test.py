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

import socket

import bleemeo_agent.core

# List of process cmdline and the expected service type
PROCESS_SERVICE = [
    (
        # Installation of some package, where package name is daemon name.
        # It shout not match any service
        'apt install apache2 redis-server postgresql mosquitto slapd squid3',
        None
    ),
    (
        # Running service in docker should match the service
        'docker run -d --name mysqld mysql',
        None
    ),
    (
        'docker run -d --name zookeeper zookeeperd',
        None
    ),
    (
        # Random java process is not a service
        '/usr/bin/java com.example.HelloWorld',
        None,
    ),
    (
        # Random python process is not a service
        '/usr/bin/python random_script.py',
        None,
    ),
    (
        # Random erlang process is not a service
        (
            '/usr/lib/erlang/erts-6.2/bin/beam -- -root /usr/lib/erlang '
            '-progname erl -- -home /root --'
        ),
        None
    ),
    (
        # InfluxDB from .deb package
        '/opt/influxdb/influxd -config /etc/opt/influxdb/influxdb.conf',
        'influxdb'
    ),

    # Service from Ubuntu 14.04. Default config
    (
        '/usr/sbin/mysqld',
        'mysql'
    ),
    (
        '/usr/sbin/ntpd -p /var/run/ntpd.pid -g -u 107:114',
        'ntp'
    ),
    (
        (
            '/usr/sbin/slapd -h ldap:/// ldapi:/// -g openldap -u openldap '
            '-F /etc/ldap/slapd.d'
        ),
        'openldap'
    ),
    (
        '/usr/sbin/apache2 -k start',
        'apache'
    ),
    (
        '/usr/sbin/asterisk -p -U asterisk',
        'asterisk'
    ),
    (
        '/usr/sbin/named -u bind',
        'bind'
    ),
    (
        '/usr/sbin/dovecot -F -c /etc/dovecot/dovecot.conf',
        'dovecot',
    ),
    (
        (
            '/usr/lib/erlang/erts-5.10.4/bin/beam -K false -P 250000 '
            '-- -root /usr/lib/erlang -progname erl '
            '-- -home /var/lib/ejabberd -- -sname ejabberd '
            '-pa /usr/lib/ejabberd/ebin -s ejabberd '
            '-kernel inetrc "/etc/ejabberd/inetrc" '
            '-ejabberd config "/etc/ejabberd/ejabberd.cfg" '
            'log_path "/var/log/ejabberd/ejabberd.log" '
            'erlang_log_path "/var/log/ejabberd/erlang.log" '
            '-sasl sasl_error_logger false -mnesia dir "/var/lib/ejabberd" '
            '-smp disable -noshell -noshell -noinput'
        ),
        'ejabberd'
    ),
    (
        (
            '/usr/lib/erlang/erts-5.10.4/bin/beam -W w -K true -A30 '
            '-P 1048576 -- -root /usr/lib/erlang -progname erl '
            '-- -home /var/lib/rabbitmq '
            '-- -pa /usr/lib/rabbitmq/lib/rabbitmq_server-3.2.4/sbin/../ebin '
            '-noshell -noinput -s rabbit boot -sname rabbit@trusty '
            '-boot start_sasl '
            '-kernel inet_default_connect_options [{nodelay,true}] '
            '-sasl errlog_type error -sasl sasl_error_logger false '
            '-rabbit error_logger '
            '{file,"/var/log/rabbitmq/rabbit@trusty.log"} '
            '-rabbit sasl_error_logger '
            '{file,"/var/log/rabbitmq/rabbit@trusty-sasl.log"} '
            '-rabbit enabled_plugins_file "/etc/rabbitmq/enabled_plugins" '
            '-rabbit plugins_dir '
            '"/usr/lib/rabbitmq/lib/rabbitmq_server-3.2.4/sbin/../plugins" '
            '-rabbit plugins_expand_dir '
            '"/var/lib/rabbitmq/mnesia/rabbit@trusty-plugins-expand" '
            '-os_mon start_cpu_sup false -os_mon start_disksup false '
            '-os_mon start_memsup false '
            '-mnesia dir "/var/lib/rabbitmq/mnesia/rabbit@trusty"'
        ),
        'rabbitmq'
    ),
    (
        '/usr/bin/mongod --config /etc/mongodb.conf',
        'mongodb'
    ),
    (
        '/usr/sbin/mosquitto -c /etc/mosquitto/mosquitto.conf',
        'mosquitto'
    ),
    (
        '/usr/bin/redis-server 127.0.0.1:6379',
        'redis',
    ),
    (
        '/usr/bin/memcached -m 64 -p 11211 -u memcache -l 127.0.0.1',
        'memcached'
    ),
    (
        '/usr/sbin/squid3 -N -YC -f /etc/squid3/squid.conf',
        'squid'
    ),
    (
        (
            '/usr/lib/postgresql/9.3/bin/postgres '
            '-D /var/lib/postgresql/9.3/main '
            '-c config_file=/etc/postgresql/9.3/main/postgresql.conf'
        ),
        'postgresql'
    ),
    (
        (
            '/usr/bin/java -cp /etc/zookeeper/conf:/usr/share/java/jline.jar'
            ':/usr/share/java/log4j-1.2.jar:/usr/share/java/xercesImpl.jar'
            ':/usr/share/java/xmlParserAPIs.jar:/usr/share/java/netty.jar'
            ':/usr/share/java/slf4j-api.jar:/usr/share/java/slf4j-log4j12.jar'
            ':/usr/share/java/zookeeper.jar -Dcom.sun.management.jmxremote '
            '-Dcom.sun.management.jmxremote.local.only=false '
            '-Dzookeeper.log.dir=/var/log/zookeeper '
            '-Dzookeeper.root.logger=INFO,ROLLINGFILE '
            'org.apache.zookeeper.server.quorum.QuorumPeerMain '
            '/etc/zookeeper/conf/zoo.cfg'
        ),
        'zookeeper'
    ),
    (
        '/usr/bin/python /usr/bin/salt-master',
        'salt-master'
    ),
    (
        '/usr/lib/postfix/master',
        'postfix'
    ),
    (
        'nginx: master process /usr/sbin/nginx',
        'nginx'
    ),
    (
        '/usr/sbin/exim4 -bd -q30m',
        'exim'
    ),
    (
        '/usr/sbin/freeradius -f',
        'freeradius'
    ),
    (
        (
            '/usr/sbin/varnishd -P /var/run/varnishd.pid -a :6081 '
            '-T localhost:6082 -f /etc/varnish/default.vcl '
            '-S /etc/varnish/secret -s malloc,256m'
        ),
        'varnish'
    ),

    # Service from Ubunut 16.04, default config
    (
        (
            '/usr/lib/jvm/java-8-openjdk-amd64/bin/java '
            '-Xms256m -Xmx1g -Djava.awt.headless=true -XX:+UseParNewGC '
            '-XX:+UseConcMarkSweepGC -XX:CMSInitiatingOccupancyFraction=75 '
            '-XX:+UseCMSInitiatingOccupancyOnly '
            '-XX:+HeapDumpOnOutOfMemoryError -XX:+DisableExplicitGC '
            '-Dfile.encoding=UTF-8 -Delasticsearch '
            '-Des.pidfile=/var/run/elasticsearch.pid '
            '-Des.path.home=/usr/share/elasticsearch '
            '-cp :/usr/share/java/lucene-sandbox-4.10.4.jar:'
            '/usr/share/java/sigar.jar:'
            '/usr/share/java/lucene-analyzers-morfologik-4.10.4.jar:'
            '/usr/share/java/spatial4j-0.4.1.jar:'
            '/usr/share/java/lucene-expressions-4.10.4.jar:'
            '/usr/share/java/lucene-analyzers-uima-4.10.4.jar:'
            '/usr/share/java/groovy-all-2.x.jar:'
            '/usr/share/java/lucene-analyzers-kuromoji-4.10.4.jar:'
            '/usr/share/java/lucene-facet-4.10.4.jar:'
            '/usr/share/java/jna.jar:'
            '/usr/share/java/lucene-analyzers-common-4.10.4.jar:'
            '/usr/share/java/lucene-core-4.10.4.jar:'
            '/usr/share/java/apache-log4j-extras-1.2.17.jar:'
            '/usr/share/java/lucene-queries-4.10.4.jar:'
            '/usr/share/java/lucene-demo-4.10.4.jar:'
            '/usr/share/java/lucene-suggest-4.10.4.jar:'
            '/usr/share/java/lucene-analyzers-stempel-4.10.4.jar:'
            '/usr/share/java/lucene-highlighter-4.10.4.jar:'
            '/usr/share/java/lucene-memory-4.10.4.jar:'
            '/usr/share/java/lucene-classification-4.10.4.jar:'
            '/usr/share/java/lucene-replicator-4.10.4.jar:'
            '/usr/share/java/lucene-grouping-4.10.4.jar:'
            '/usr/share/java/log4j-1.2-1.2.17.jar:'
            '/usr/share/java/lucene-join-4.10.4.jar:'
            '/usr/share/java/lucene-analyzers-smartcn-4.10.4.jar:'
            '/usr/share/java/lucene-spatial-4.10.4.jar:'
            '/usr/share/java/elasticsearch-1.7.3.jar:'
            '/usr/share/java/lucene-codecs-4.10.4.jar:'
            '/usr/share/java/lucene-misc-4.10.4.jar:'
            '/usr/share/java/lucene-queryparser-4.10.4.jar:'
            '/usr/share/java/lucene-test-framework-4.10.4.jar:'
            '/usr/share/java/jts.jar:'
            '/usr/share/java/lucene-benchmark-4.10.4.jar:'
            '/usr/share/java/lucene-analyzers-icu-4.10.4.jar:'
            '/usr/share/java/lucene-analyzers-phonetic-4.10.4.jar: '
            '-Des.default.config=/etc/elasticsearch/elasticsearch.yml '
            '-Des.default.path.home=/usr/share/elasticsearch '
            '-Des.default.path.logs=/var/log/elasticsearch '
            '-Des.default.path.data=/var/lib/elasticsearch '
            '-Des.default.path.work=/tmp/elasticsearch '
            '-Des.default.path.conf=/etc/elasticsearch '
            'org.elasticsearch.bootstrap.Elasticsearch'
        ),
        'elasticsearch'
    ),
    (
        '/usr/sbin/squid -YC -f /etc/squid/squid.conf',
        'squid'
    ),

    # Other command / service
    (
        (
            '/usr/sbin/openvpn --writepid /run/openvpn/server.pid '
            '--daemon ovpn-server --cd /etc/openvpn '
            '--config /etc/openvpn/server.conf --script-security 2'
        ),
        'openvpn',
    ),
    (
        '/usr/sbin/libvirtd -d',
        'libvirt'
    ),
    (
        'haproxy -f /usr/local/etc/haproxy/haproxy.cfg',
        'haproxy'
    ),
    (
        'uwsgi --ini /srv/app/deploy/uwsgi.ini',
        'uwsgi'
    ),
]


def test_get_service_info():
    for (cmdline, service) in PROCESS_SERVICE:
        result = bleemeo_agent.core.get_service_info(cmdline)
        if service is None:
            assert result is None, 'Found a service for cmdline %s' % cmdline
        elif result is None:
            assert False, 'Expected service %s' % service
        else:
            assert result['service'] == service


def test_sanitize_service():
    sanitize_service = bleemeo_agent.core.sanitize_service

    # First check custom services
    service_info = {}
    assert sanitize_service('test', service_info, False) is None

    service_info = {'check_type': 'nagios'}
    assert sanitize_service('test', service_info, False) is None

    service_info = {'port': 'non-numeric'}
    assert sanitize_service('test', service_info, False) is None

    service_info = {'port': 1234}
    wanted = {
        'port': 1234,
        'address': '127.0.0.1',
        'protocol': socket.IPPROTO_TCP
    }
    assert sanitize_service('test', service_info, False) == wanted

    service_info = {'check_type': 'nagios', 'check_command': 'true'}
    wanted = service_info
    assert sanitize_service('test', service_info, False) == wanted

    service_info = {'check_type': 'nagios', 'check_command': 'true', 'port': 1}
    wanted = {
        'check_type': 'nagios',
        'check_command': 'true',
        'port': 1,
        'address': '127.0.0.1',
        'protocol': socket.IPPROTO_TCP
    }
    assert sanitize_service('test', service_info, False) == wanted

    # discovered services are allowed to exists without service_info
    service_info = {}
    assert sanitize_service('test', service_info, True) == {}


def test_apply_service_override():

    services = {
        ('apache', None): {'placeholder': 'apache'},
        ('mysql', None): {'placeholder': 'mysql'},
        ('mysql', 'container-1'): {'placeholder': 'mysql2'},
        ('memcached', None): {'address': '127.0.0.1', 'placeholder': 'memc'},
    }

    override = [
        {'id': 'mysql', 'username': 'user1'},
        {'id': 'mysql', 'instance': 'container-1', 'username': 'user2'},
        {'id': 'memcached', 'address': '10.1.1.2'},
        {'id': 'myservice', 'this-is-a-bad-servcie': 'no port/nagios check'},
        {'id': 'mywebapp', 'port': 8080, 'check_type': 'http'},
    ]

    wanted = {
        ('apache', None): {'placeholder': 'apache'},
        ('mysql', None): {'placeholder': 'mysql', 'username': 'user1'},
        ('mysql', 'container-1'): {
            'placeholder': 'mysql2',
            'username': 'user2'
        },
        ('memcached', None): {'address': '10.1.1.2', 'placeholder': 'memc'},
        ('mywebapp', None): {
            'address': '127.0.0.1',
            'port': 8080,
            'protocol': socket.IPPROTO_TCP,
            'check_type': 'http',
        },
    }

    bleemeo_agent.core.apply_service_override(services, override)
    assert services == wanted
