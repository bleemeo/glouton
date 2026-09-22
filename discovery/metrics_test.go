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

package discovery

import (
	"testing"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/facts"
)

func TestActiveMQURL(t *testing.T) {
	const activeMQPort = 8161

	listening := []facts.ListenAddress{{Address: testIP127001, Port: activeMQPort, NetworkFamily: tcpProtocol}}

	cases := []struct {
		name         string
		service      Service
		wantURL      string
		wantUsername string
		wantPassword string
	}{
		{
			// The console always requires authentication, and a default install still
			// answers to the factory account, so nothing configured is worth trying rather
			// than skipping the service outright.
			name: "no credentials -> the factory account",
			service: Service{
				ServiceType:     ActiveMQService,
				IPAddress:       testIP127001,
				ListenAddresses: listening,
			},
			wantURL:      "http://127.0.0.1:8161",
			wantUsername: "admin",
			wantPassword: "admin",
		},
		{
			// A password alone would send ":<password>" and get a 401, so the factory user
			// is filled in.
			name: "password without username -> the factory user",
			service: Service{
				ServiceType:     ActiveMQService,
				IPAddress:       testIP127001,
				ListenAddresses: listening,
				Config:          config.Service{Password: "secret"},
			},
			wantURL:      "http://127.0.0.1:8161",
			wantUsername: "admin",
			wantPassword: "secret",
		},
		{
			name: "explicit username is kept",
			service: Service{
				ServiceType:     ActiveMQService,
				IPAddress:       testIP127001,
				ListenAddresses: listening,
				Config:          config.Service{Username: "bob", Password: "secret"},
			},
			wantURL:      "http://127.0.0.1:8161",
			wantUsername: "bob",
			wantPassword: "secret",
		},
		{
			// The other half of the same fallback: an install that renamed the user and
			// kept the factory password is as plausible as the reverse.
			name: "username without password -> the factory password",
			service: Service{
				ServiceType:     ActiveMQService,
				IPAddress:       testIP127001,
				ListenAddresses: listening,
				Config:          config.Service{Username: "bob"},
			},
			wantURL:      "http://127.0.0.1:8161",
			wantUsername: "bob",
			wantPassword: "admin",
		},
		{
			name: "user-set StatsURL wins over the discovered address",
			service: Service{
				ServiceType:     ActiveMQService,
				IPAddress:       testIP127001,
				ListenAddresses: listening,
				Config:          config.Service{StatsURL: "http://activemq.example:8161", Password: "secret"},
			},
			wantURL:      "http://activemq.example:8161",
			wantUsername: "admin",
			wantPassword: "secret",
		},
		{
			// A console reachable only at a configured URL, e.g. one behind TLS or on a
			// port the container doesn't publish.
			name: "StatsURL is used when no address is known",
			service: Service{
				ServiceType: ActiveMQService,
				Config:      config.Service{StatsURL: "https://activemq.example", Password: "secret"},
			},
			wantURL:      "https://activemq.example",
			wantUsername: "admin",
			wantPassword: "secret",
		},
		{
			name: "StatsURL without credentials -> the factory account",
			service: Service{
				ServiceType: ActiveMQService,
				Config:      config.Service{StatsURL: "http://activemq.example:8161"},
			},
			wantURL:      "http://activemq.example:8161",
			wantUsername: "admin",
			wantPassword: "admin",
		},
		{
			// The one case left with no input: credentials say who to ask, never where.
			name: "no address known and no StatsURL -> no input",
			service: Service{
				ServiceType: ActiveMQService,
				Config:      config.Service{Password: "secret"},
			},
			wantURL: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			url, username, password := activeMQURL(tc.service)

			if url != tc.wantURL {
				t.Errorf("activeMQURL() url = %q, want %q", url, tc.wantURL)
			}

			if username != tc.wantUsername {
				t.Errorf("activeMQURL() username = %q, want %q", username, tc.wantUsername)
			}

			if password != tc.wantPassword {
				t.Errorf("activeMQURL() password = %q, want %q", password, tc.wantPassword)
			}
		})
	}
}

func TestApacheStatusURL(t *testing.T) {
	cases := []struct {
		name string
		ip   string
		port int
		want string
	}{
		{name: "ipv4 default port", ip: "192.168.1.42", port: 80, want: "http://192.168.1.42/server-status?auto"},
		{name: "ipv4 custom port", ip: "192.168.1.42", port: 8080, want: "http://192.168.1.42:8080/server-status?auto"},
		{name: "ipv6 default port", ip: "2001:db8::1", port: 80, want: "http://[2001:db8::1]/server-status?auto"},
		{name: "ipv6 custom port", ip: "2001:db8::1", port: 8080, want: "http://[2001:db8::1]:8080/server-status?auto"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := apacheStatusURL(c.ip, c.port); got != c.want {
				t.Errorf("apacheStatusURL(%q, %d) = %q, want %q", c.ip, c.port, got, c.want)
			}
		})
	}
}

func TestClickHouseAddress(t *testing.T) {
	cases := []struct {
		name     string
		service  Service
		wantIP   string
		wantPort int
	}{
		{
			name: "port 8123 explicitly published",
			service: Service{
				ServiceType:     ClickHouseService,
				IPAddress:       "172.20.0.16",
				ListenAddresses: []facts.ListenAddress{{Address: "172.20.0.16", Port: 8123, NetworkFamily: "tcp"}},
			},
			wantIP:   "172.20.0.16",
			wantPort: 8123,
		},
		{
			name: "only port 9000 published, no user override -> fallback to 8123",
			service: Service{
				ServiceType:     ClickHouseService,
				IPAddress:       "172.20.0.16",
				ListenAddresses: []facts.ListenAddress{{Address: "172.20.0.16", Port: 9000, NetworkFamily: "tcp"}},
			},
			wantIP:   "172.20.0.16",
			wantPort: 8123,
		},
		{
			name: "user-set Port not actually listening -> still attempted via IPAddress",
			service: Service{
				ServiceType:     ClickHouseService,
				IPAddress:       "172.20.0.16",
				Config:          config.Service{Port: 8124},
				ListenAddresses: []facts.ListenAddress{{Address: "172.20.0.16", Port: 9000, NetworkFamily: "tcp"}},
			},
			wantIP:   "172.20.0.16",
			wantPort: 8124,
		},
		{
			name: "StatsPort takes priority over Port",
			service: Service{
				ServiceType: ClickHouseService,
				IPAddress:   "172.20.0.16",
				Config:      config.Service{Port: 9000, StatsPort: 8123},
			},
			wantIP:   "172.20.0.16",
			wantPort: 8123,
		},
		{
			name: "port not found and IPAddress unknown -> no fallback, no crash",
			service: Service{
				ServiceType:     ClickHouseService,
				ListenAddresses: []facts.ListenAddress{{Address: "172.20.0.16", Port: 9000, NetworkFamily: "tcp"}},
			},
			wantIP:   "",
			wantPort: 8123,
		},
		{
			name: "explicit Config.Address bypasses everything (existing behavior, unaffected)",
			service: Service{
				ServiceType: ClickHouseService,
				Config:      config.Service{Address: "10.0.0.5"},
			},
			wantIP:   "10.0.0.5",
			wantPort: 8123,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotIP, gotPort := clickHouseAddress(tc.service)
			if gotIP != tc.wantIP || gotPort != tc.wantPort {
				t.Errorf("clickHouseAddress() = (%q, %d), want (%q, %d)", gotIP, gotPort, tc.wantIP, tc.wantPort)
			}
		})
	}
}

func TestBindStatsURL(t *testing.T) {
	cases := []struct {
		name    string
		service Service
		want    string
	}{
		{
			// The statistics-channel is unrelated to the DNS port found by discovery,
			// so it's looked up on its own default port.
			name: "no config, statistics-channel on its default port",
			service: Service{
				ServiceType: BindService,
				IPAddress:   testIP127001,
				ListenAddresses: []facts.ListenAddress{
					{Address: testIP127001, Port: 53, NetworkFamily: udpProtocol},
					{Address: testIP127001, Port: 8053, NetworkFamily: tcpProtocol},
				},
			},
			want: "http://127.0.0.1:8053/xml/v3",
		},
		{
			// The statistics-channel is disabled by default: creating an input for a BIND
			// that doesn't have one would only report a connection error on every gather.
			name: "statistics-channel not listening -> no input",
			service: Service{
				ServiceType:     BindService,
				IPAddress:       testIP127001,
				ListenAddresses: []facts.ListenAddress{{Address: testIP127001, Port: 53, NetworkFamily: udpProtocol}},
			},
			want: "",
		},
		{
			// A container only publishes its DNS port as a rule, so its listen addresses
			// say nothing about the statistics-channel and it is attempted anyway.
			name: "containerized, statistics-channel not published -> still attempted",
			service: Service{
				ServiceType:     BindService,
				IPAddress:       testIP127001,
				ContainerID:     "1234",
				ListenAddresses: []facts.ListenAddress{{Address: testIP127001, Port: 53, NetworkFamily: udpProtocol}},
			},
			want: "http://127.0.0.1:8053/xml/v3",
		},
		{
			name: "user-set StatsPort",
			service: Service{
				ServiceType: BindService,
				IPAddress:   testIP127001,
				Config:      config.Service{StatsPort: 8080},
			},
			want: "http://127.0.0.1:8080/xml/v3",
		},
		{
			// Needed for the older XML v2 and JSON v1 formats, which live on another path.
			name: "user-set StatsURL wins over everything",
			service: Service{
				ServiceType: BindService,
				IPAddress:   testIP127001,
				Config:      config.Service{StatsURL: "http://127.0.0.1:8053/json/v1", StatsPort: 8080},
			},
			want: "http://127.0.0.1:8053/json/v1",
		},
		{
			name:    "no address known -> no input",
			service: Service{ServiceType: BindService},
			want:    "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := bindStatsURL(tc.service); got != tc.want {
				t.Errorf("bindStatsURL() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDovecotStatsServer(t *testing.T) {
	cases := []struct {
		name    string
		service Service
		want    string
	}{
		{
			// The old_stats listener is unrelated to the IMAP port found by discovery.
			name: "no config, old_stats listener on its default port",
			service: Service{
				ServiceType: DovecotService,
				IPAddress:   testIP127001,
				ListenAddresses: []facts.ListenAddress{
					{Address: testIP127001, Port: 143, NetworkFamily: tcpProtocol},
					{Address: testIP127001, Port: 24242, NetworkFamily: tcpProtocol},
				},
			},
			want: "127.0.0.1:24242",
		},
		{
			// old_stats is opt-in: without its listener, an input would only report
			// connection errors.
			name: "no old_stats listener -> no input",
			service: Service{
				ServiceType:     DovecotService,
				IPAddress:       testIP127001,
				ListenAddresses: []facts.ListenAddress{{Address: testIP127001, Port: 143, NetworkFamily: tcpProtocol}},
			},
			want: "",
		},
		{
			// The listen addresses of a container are the ports it publishes, which say
			// nothing about the old_stats listener, so it is attempted anyway.
			name: "containerized, old_stats port not published -> still attempted",
			service: Service{
				ServiceType:     DovecotService,
				IPAddress:       testIP127001,
				ContainerID:     "1234",
				ListenAddresses: []facts.ListenAddress{{Address: testIP127001, Port: 143, NetworkFamily: tcpProtocol}},
			},
			want: "127.0.0.1:24242",
		},
		{
			name: "user-set StatsPort",
			service: Service{
				ServiceType: DovecotService,
				IPAddress:   testIP127001,
				Config:      config.Service{StatsPort: 24243},
			},
			want: "127.0.0.1:24243",
		},
		{
			// A socket that doesn't exist is ignored by getMetricsSocket, so the TCP
			// listener is used instead.
			name: "unix socket not found -> fallback to TCP",
			service: Service{
				ServiceType:     DovecotService,
				IPAddress:       testIP127001,
				ListenAddresses: []facts.ListenAddress{{Address: testIP127001, Port: 24242, NetworkFamily: tcpProtocol}},
				Config:          config.Service{MetricsUnixSocket: "/nonexistent/dovecot-stats"},
			},
			want: "127.0.0.1:24242",
		},
		{
			name:    "no address known -> no input",
			service: Service{ServiceType: DovecotService},
			want:    "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := dovecotStatsServer(tc.service); got != tc.want {
				t.Errorf("dovecotStatsServer() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestChronyAddress checks which address chronyd's command protocol is looked for on:
// the local auto-detection of telegraf's plugin (its control socket, then
// udp://127.0.0.1:323) works out of the box but only reaches a chronyd sharing Glouton's
// network namespace, while any other address needs bindcmdaddress/cmdallow to have been
// set for it -- so one is only used when the auto-detection cannot be what we want.
func TestChronyAddress(t *testing.T) {
	localCommandAddress := "127.0.0.1:323"

	cases := []struct {
		name      string
		service   Service
		wantCmd   string
		wantCmdOK bool
	}{
		{
			// The plain host case: chronyd next to Glouton, nothing declared. The
			// loopback command port is named rather than left for the input to find, so
			// that the check reads the same daemon.
			name:      "local daemon",
			service:   Service{ServiceType: NTPService, IPAddress: "127.0.0.1"},
			wantCmd:   localCommandAddress,
			wantCmdOK: true,
		},
		{
			// IPAddress comes from the NTP port's bind address, which says nothing about
			// where the command port is: a host chronyd serving NTP on a specific address
			// ("bindaddress 192.168.1.5") still has its command port on loopback only, and
			// pointing the input at 192.168.1.5:323 would break what auto-detection handles.
			name:      "local daemon serving NTP on a specific address",
			service:   Service{ServiceType: NTPService, IPAddress: "192.168.1.5"},
			wantCmd:   localCommandAddress,
			wantCmdOK: true,
		},
		{
			// Glouton's loopback isn't the container's: auto-detection would report the
			// numbers of whatever chronyd runs next to Glouton under this service's name.
			name:      "container",
			service:   Service{ServiceType: NTPService, ContainerID: "1234", IPAddress: "172.23.0.2"},
			wantCmd:   "172.23.0.2:323",
			wantCmdOK: true,
		},
		{
			name:      "container with a non-default command port",
			service:   Service{ServiceType: NTPService, ContainerID: "1234", IPAddress: "172.23.0.2", Config: config.Service{StatsPort: 3230}},
			wantCmd:   "172.23.0.2:3230",
			wantCmdOK: true,
		},
		{
			// A container the runtime reports no address for (network_mode: none, or
			// container:<other>, both of which leave PrimaryAddress() empty). Neither half
			// may fall back to our own loopback: it would report on whatever chronyd runs
			// next to Glouton -- Ok while this container is down, critical while it is
			// healthy, and metrics belonging to another daemon either way. Not ok means
			// no input at all, and an empty address makes the check say it couldn't run.
			name:      "container without an address",
			service:   Service{ServiceType: NTPService, ContainerID: "1234"},
			wantCmd:   "",
			wantCmdOK: false,
		},
		{
			// Same, with a command port declared: the port says which port to use, never
			// which host, so it cannot rescue a service whose host is unknown. Filling in
			// the loopback here would read the local chronyd on a non-default port.
			name:      "container without an address but a command port",
			service:   Service{ServiceType: NTPService, ContainerID: "1234", Config: config.Service{StatsPort: 3230}},
			wantCmd:   "",
			wantCmdOK: false,
		},
		{
			// How a chronyd reachable but not auto-detectable is monitored.
			name:      "declared address",
			service:   Service{ServiceType: NTPService, Config: config.Service{Address: "10.0.0.1"}, IPAddress: "10.0.0.1"},
			wantCmd:   "10.0.0.1:323",
			wantCmdOK: true,
		},
		{
			// A local chronyd with "cmdport 3230": the port has to be spelled out for the
			// input too, or it would go back to the default 323 and gather nothing while
			// the check succeeds on the declared port.
			name:      "local daemon with a non-default command port",
			service:   Service{ServiceType: NTPService, IPAddress: "127.0.0.1", Config: config.Service{StatsPort: 3230}},
			wantCmd:   "127.0.0.1:3230",
			wantCmdOK: true,
		},
		{
			name:      "declared address and command port",
			service:   Service{ServiceType: NTPService, Config: config.Service{Address: "10.0.0.1", StatsPort: 3230}, IPAddress: "10.0.0.1"},
			wantCmd:   "10.0.0.1:3230",
			wantCmdOK: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, gotOK := chronyCmdAddress(tc.service)
			if got != tc.wantCmd || gotOK != tc.wantCmdOK {
				t.Errorf("chronyCmdAddress() = %q, %t, want %q, %t", got, gotOK, tc.wantCmd, tc.wantCmdOK)
			}
		})
	}
}

// TestNTPDAddress checks which address ntpd's control protocol is read on. Mode 6 is
// served on the NTP port itself, so a "port" override moves it, and the daemon's own
// restrict policy is why an address is only used for a daemon Glouton's loopback can't be.
func TestNTPDAddress(t *testing.T) {
	cases := []struct {
		name    string
		service Service
		want    string
		wantOK  bool
	}{
		{
			// The plain host case: ntpd next to Glouton, nothing declared. The input
			// falls back to 127.0.0.1:123, which is what its restrict lines allow.
			name:    "local daemon",
			service: Service{ServiceType: NTPService, IPAddress: "127.0.0.1"},
			want:    "",
			wantOK:  true,
		},
		{
			// Serving NTP on a specific address says nothing about what its restrict
			// lines allow the control protocol from, and loopback is what they do allow.
			name:    "local daemon serving NTP on a specific address",
			service: Service{ServiceType: NTPService, IPAddress: "192.168.1.5"},
			want:    "",
			wantOK:  true,
		},
		{
			name:    "container",
			service: Service{ServiceType: NTPService, ContainerID: "1234", IPAddress: "172.23.0.2"},
			want:    "172.23.0.2:123",
			wantOK:  true,
		},
		{
			// A declared port has to be honoured even for a local daemon: the check reads
			// it from AddressPort either way, so ignoring it here would have the check
			// probing 1123 while the input reads nothing on 123.
			name:    "local daemon on a declared port",
			service: Service{ServiceType: NTPService, IPAddress: "127.0.0.1", Config: config.Service{Port: 1123}},
			want:    "127.0.0.1:1123",
			wantOK:  true,
		},
		{
			name:    "declared address",
			service: Service{ServiceType: NTPService, Config: config.Service{Address: "10.0.0.1"}, IPAddress: "10.0.0.1"},
			want:    "10.0.0.1:123",
			wantOK:  true,
		},
		{
			name:    "declared address and port",
			service: Service{ServiceType: NTPService, Config: config.Service{Address: "10.0.0.1", Port: 1123}, IPAddress: "10.0.0.1"},
			want:    "10.0.0.1:1123",
			wantOK:  true,
		},
		{
			// Nothing locates this daemon, and 127.0.0.1:123 is another one: the ntpd next
			// to Glouton, whose peers would be published under this container's name.
			name:    "container without an address",
			service: Service{ServiceType: NTPService, ContainerID: "1234"},
			want:    "",
			wantOK:  false,
		},
		{
			// A port cannot rescue an unknown host, same as chrony's command port.
			name:    "container without an address but a declared port",
			service: Service{ServiceType: NTPService, ContainerID: "1234", Config: config.Service{Port: 1123}},
			want:    "",
			wantOK:  false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, gotOK := ntpdAddress(tc.service)
			if got != tc.want || gotOK != tc.wantOK {
				t.Errorf("ntpdAddress() = %q, %t, want %q, %t", got, gotOK, tc.want, tc.wantOK)
			}
		})
	}
}
