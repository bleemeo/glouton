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

package check

import (
	"bytes"
	"encoding/binary"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/bleemeo/glouton/types"
)

// fakeNTPServer answers every client request with reply, its transmit and receive
// timestamps set to now so the check's clock comparison passes, and returns its address.
func fakeNTPServer(t *testing.T, reply ntpV3Packet) string {
	t.Helper()

	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})

	t.Cleanup(func() {
		_ = conn.Close()

		<-done
	})

	go func() {
		defer close(done)

		buffer := make([]byte, 48)

		for {
			_, addr, err := conn.ReadFrom(buffer)
			if err != nil {
				return // closed by the cleanup
			}

			// A Kiss-o'-Death carries no timestamps, and the check must not need them:
			// they are only filled in when the server is answering for real.
			if reply.Stratum != 0 {
				now := timeToNTP(time.Now())
				reply.ReceiveTS = now
				reply.Transmit = now
			}

			var out bytes.Buffer

			if err := binary.Write(&out, binary.BigEndian, reply); err != nil {
				return
			}

			if _, err := conn.WriteTo(out.Bytes(), addr); err != nil {
				return
			}
		}
	}()

	return conn.LocalAddr().String()
}

// serverLeapVersionMode is the first byte of a server's reply: the leap indicator, then
// NTP version 3 and mode 4 (server). Built here rather than through the production
// encodeLeapVersionMode, whose callers all being version 3 is the point of that helper.
func serverLeapVersionMode(leapIndicator uint8) uint8 {
	const versionAndServerMode = 3*8 + 4

	return leapIndicator*64 + versionAndServerMode
}

// timeToNTP is the inverse of ntpTimestamp.Time(), for building a server's answer: NTP
// counts seconds from 1 January 1900, Unix from 1970.
func timeToNTP(t time.Time) ntpTimestamp {
	const secondsFrom1900To1970 = 2208988800

	return ntpTimestamp{Second: uint32(t.Unix()) + secondsFrom1900To1970} //nolint:gosec,exhaustruct // test timestamps are well inside range
}

// TestNTPCheckStatus checks what a server's answer is reported as, and in particular that
// a refusal isn't reported as a clock problem: a Kiss-o'-Death carries stratum 0 just like
// an unsynchronized server, but it says the server won't answer us -- typically because we
// query more often than its "restrict ... limited" allows -- and reporting that as
// "not synchronized" sends whoever reads it after the wrong thing entirely.
func TestNTPCheckStatus(t *testing.T) {
	cases := []struct {
		name       string
		reply      ntpV3Packet
		want       types.Status
		wantDetail string
	}{
		{
			name: "a synchronized server",
			reply: ntpV3Packet{
				LeapVersionMode: serverLeapVersionMode(0),
				Stratum:         2,
			},
			want:       types.StatusOk,
			wantDetail: "NTP OK",
		},
		{
			name: "an unsynchronized server",
			reply: ntpV3Packet{
				LeapVersionMode: serverLeapVersionMode(3),
				Stratum:         16,
			},
			want:       types.StatusCritical,
			wantDetail: "not (yet) synchronized",
		},
		{
			// What a monitored ntpd sends once its rate limit is exceeded, and what this
			// check reported as an unsynchronized clock before the reference ID was read.
			name: "rate-limited (Kiss-o'-Death)",
			reply: ntpV3Packet{
				LeapVersionMode: serverLeapVersionMode(3),
				Stratum:         0,
				ReferenceID:     [4]byte{'R', 'A', 'T', 'E'},
			},
			want:       types.StatusCritical,
			wantDetail: "querying it too often",
		},
		{
			name: "access denied",
			reply: ntpV3Packet{
				LeapVersionMode: serverLeapVersionMode(3),
				Stratum:         0,
				ReferenceID:     [4]byte{'D', 'E', 'N', 'Y'},
			},
			want:       types.StatusCritical,
			wantDetail: "access denied",
		},
		{
			// Stratum 0 with no kiss code is a server with no usable clock, not a refusal.
			name: "stratum 0 without a kiss code",
			reply: ntpV3Packet{
				LeapVersionMode: serverLeapVersionMode(3),
				Stratum:         0,
			},
			want:       types.StatusCritical,
			wantDetail: "not (yet) synchronized",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			address := fakeNTPServer(t, tc.reply)

			nc := NewNTP(address, nil, false, nil, types.MetricAnnotations{}, nil)

			got := nc.ntpMainCheck(t.Context())

			if got.CurrentStatus != tc.want {
				t.Errorf("ntpMainCheck() = %v (%s), want %v", got.CurrentStatus, got.StatusDescription, tc.want)
			}

			if !strings.Contains(got.StatusDescription, tc.wantDetail) {
				t.Errorf("ntpMainCheck() description = %q, want it to contain %q", got.StatusDescription, tc.wantDetail)
			}
		})
	}
}
