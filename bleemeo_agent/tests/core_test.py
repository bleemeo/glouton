
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
