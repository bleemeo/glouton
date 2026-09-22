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
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/bleemeo/glouton/types"
)

// udpResponder starts a UDP socket answering each datagram with whatever reply returns --
// nothing when that is nil, like a port that drops what it doesn't understand -- and
// returns its address. Shared with the NTP check's own fake server.
func udpResponder(t *testing.T, reply func(request []byte) []byte) string {
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

		buffer := make([]byte, 4096)

		for {
			n, addr, err := conn.ReadFrom(buffer)
			if err != nil {
				return // the socket was closed by the cleanup
			}

			answer := reply(buffer[:n])
			if answer == nil {
				continue
			}

			if _, err := conn.WriteTo(answer, addr); err != nil {
				return
			}
		}
	}()

	return conn.LocalAddr().String()
}

// closedUDPPort returns the address of a UDP port nothing listens on: a socket is opened
// to get a port the kernel isn't using, then closed right away.
func closedUDPPort(t *testing.T) string {
	t.Helper()

	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}

	address := conn.LocalAddr().String()

	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}

	return address
}

// TestCheckUDP checks what a UDP probe reports. UDP has no handshake, so nothing but the
// reply to the payload sent tells a listening port from a silent one -- which is why
// send is required and a missing answer is a failure, not a timeout to be forgiven.
func TestCheckUDP(t *testing.T) {
	// A test running in parallel with a check that waits the full 10s timeout would slow
	// the whole package down, so the deadline the check applies is shortened by the
	// context given to it instead.
	const shortTimeout = 500 * time.Millisecond

	answering := udpResponder(t, func([]byte) []byte { return []byte("PONG") })
	silent := udpResponder(t, func([]byte) []byte { return nil })
	closed := closedUDPPort(t)

	cases := []struct {
		name       string
		address    string
		send       []byte
		expect     []byte
		validate   func(reply []byte) error
		want       types.Status
		wantDetail string
	}{
		{
			name:    "a reply to the payload sent",
			address: answering,
			send:    []byte("PING"),
			want:    types.StatusOk,
		},
		{
			name:    "the expected reply",
			address: answering,
			send:    []byte("PING"),
			expect:  []byte("PONG"),
			want:    types.StatusOk,
		},
		{
			name:       "another reply than the expected one",
			address:    answering,
			send:       []byte("PING"),
			expect:     []byte("SOMETHING ELSE"),
			want:       types.StatusCritical,
			wantDetail: "unexpected response",
		},
		{
			// A port that answers nothing is indistinguishable from a closed one, which
			// is the whole reason a UDP check needs a payload the service replies to.
			name:       "a port that never replies",
			address:    silent,
			send:       []byte("PING"),
			want:       types.StatusCritical,
			wantDetail: statusConnectionTimedOut,
		},
		{
			name:    "a closed port",
			address: closed,
			send:    []byte("PING"),
			want:    types.StatusCritical,
		},
		{
			name:       "an address with no port",
			address:    "127.0.0.1",
			send:       []byte("PING"),
			want:       types.StatusUnknown,
			wantDetail: "Invalid UDP address",
		},
		{
			name:       "a port that isn't a number",
			address:    "127.0.0.1:http",
			send:       []byte("PING"),
			want:       types.StatusUnknown,
			wantDetail: "Invalid UDP port",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), shortTimeout)
			defer cancel()

			got := checkUDP(ctx, tc.address, tc.send, tc.expect, tc.validate)

			if got.CurrentStatus != tc.want {
				t.Errorf("checkUDP() = %v (%s), want %v", got.CurrentStatus, got.StatusDescription, tc.want)
			}

			if !strings.Contains(got.StatusDescription, tc.wantDetail) {
				t.Errorf("checkUDP() description = %q, want it to contain %q", got.StatusDescription, tc.wantDetail)
			}

			if got.CurrentStatus == types.StatusOk && got.StatusDescription == "" {
				t.Error("checkUDP() gave no description of what it probed")
			}
		})
	}
}

// TestUDPCheckKeepsItsDescription checks that the description the probe builds survives
// baseCheck, which has no TCP address to check here and used to return a bare Ok --
// leaving the panel with a service that is up for no stated reason.
func TestUDPCheckKeepsItsDescription(t *testing.T) {
	address := udpResponder(t, func([]byte) []byte { return []byte("PONG") })

	uc := NewUDP(address, []byte("PING"), []byte("PONG"), nil, nil, types.MetricAnnotations{}, nil)

	got := uc.baseCheck.doCheck(t.Context())

	if got.CurrentStatus != types.StatusOk {
		t.Fatalf("doCheck() = %v (%s), want %v", got.CurrentStatus, got.StatusDescription, types.StatusOk)
	}

	if !strings.HasPrefix(got.StatusDescription, "UDP OK") {
		t.Errorf("doCheck() description = %q, want the one the UDP probe built", got.StatusDescription)
	}
}

// TestUDPCheckWithoutAddress checks the check reports it couldn't run rather than an Ok
// it never verified: unlike a TCP check, there are no secondary addresses to fall back on.
func TestUDPCheckWithoutAddress(t *testing.T) {
	uc := NewUDP("", []byte("PING"), nil, nil, nil, types.MetricAnnotations{}, nil)

	got := uc.baseCheck.doCheck(t.Context())

	if got.CurrentStatus != types.StatusUnknown {
		t.Errorf("doCheck() = %v (%s), want %v", got.CurrentStatus, got.StatusDescription, types.StatusUnknown)
	}
}
