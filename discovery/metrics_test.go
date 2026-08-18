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
	"os"
	"path/filepath"
	"testing"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/facts"
)

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
			name: "statistics-channel not listening -> still attempted on the default port",
			service: Service{
				ServiceType:     BindService,
				IPAddress:       testIP127001,
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
				ServiceType:     DovecotService,
				IPAddress:       testIP127001,
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
				ServiceType: DovecotService,
				IPAddress:   testIP127001,
				Config:      config.Service{MetricsUnixSocket: "/nonexistent/dovecot-stats"},
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

// TestIsChronyDaemon checks the NTP daemon switch: chronyd and ntpd are the same
// service but are queried with a different telegraf plugin.
func TestIsChronyDaemon(t *testing.T) {
	cases := []struct {
		name    string
		service Service
		want    bool
	}{
		{
			name:    "chronyd",
			service: Service{ServiceType: NTPService, ExePath: "/usr/sbin/chronyd"},
			want:    true,
		},
		{
			name:    "ntpd",
			service: Service{ServiceType: NTPService, ExePath: "/usr/sbin/ntpd"},
			want:    false,
		},
		{
			// Happens for a service declared by the user: ntpd is assumed.
			name:    "unknown executable",
			service: Service{ServiceType: NTPService},
			want:    false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isChronyDaemon(tc.service); got != tc.want {
				t.Errorf("isChronyDaemon() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestPostfixQueuesReadable checks the permission probe done before creating the
// Postfix input: on a default install the spool directory is only readable by the
// postfix user, and the input would only report errors.
func TestPostfixQueuesReadable(t *testing.T) {
	newSpool := func(t *testing.T, queues []string) string {
		t.Helper()

		spool := t.TempDir()

		for _, queue := range queues {
			if err := os.Mkdir(filepath.Join(spool, queue), 0o750); err != nil {
				t.Fatal(err)
			}
		}

		return spool
	}

	t.Run("every queue readable", func(t *testing.T) {
		if !postfixQueuesReadable(newSpool(t, postfixQueues)) {
			t.Error("postfixQueuesReadable() = false, want true")
		}
	})

	t.Run("one queue missing", func(t *testing.T) {
		if postfixQueuesReadable(newSpool(t, postfixQueues[1:])) {
			t.Error("postfixQueuesReadable() = true, want false")
		}
	})

	t.Run("spool directory doesn't exist", func(t *testing.T) {
		if postfixQueuesReadable(filepath.Join(t.TempDir(), "nonexistent")) {
			t.Error("postfixQueuesReadable() = true, want false")
		}
	})

	t.Run("one queue not readable", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root bypasses the directory permissions this checks")
		}

		spool := newSpool(t, postfixQueues)

		// Drop every permission bit but write, which is what a queue the Glouton user
		// isn't granted access to looks like: opening it fails.
		if err := os.Chmod(filepath.Join(spool, postfixQueues[0]), 0o200); err != nil {
			t.Fatal(err)
		}

		if postfixQueuesReadable(spool) {
			t.Error("postfixQueuesReadable() = true, want false")
		}
	})
}
