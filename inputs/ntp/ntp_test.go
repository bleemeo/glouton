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

package ntp

import (
	"bytes"
	"encoding/binary"
	"errors"
	"maps"
	"math"
	"net"
	"slices"
	"testing"

	"github.com/bleemeo/glouton/inputs/internal"
	"github.com/bleemeo/glouton/types"

	"github.com/facebook/time/ntp/control"
)

// fakeNTPD answers the NTP control protocol (mode 6) with the peer variables given, keyed
// by association ID, and returns its address. The variable strings are the k=v payload a
// real ntpd sends, so the whole request/response encoding is exercised, not just the
// parsing of an already-decoded map.
func fakeNTPD(t *testing.T, peers map[uint16]string) string {
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

		buffer := make([]byte, 1024)

		for {
			n, addr, err := conn.ReadFrom(buffer)
			if err != nil {
				return // closed by the cleanup
			}

			if n < 12 {
				continue
			}

			var request control.NTPControlMsgHead

			if err := binary.Read(bytes.NewReader(buffer[:12]), binary.BigEndian, &request); err != nil {
				return
			}

			var data []byte

			switch request.GetOperation() {
			case control.OpReadStatus:
				// The peer list: association ID and status word, 2 uint16 each.
				for _, id := range slices.Sorted(maps.Keys(peers)) {
					data = binary.BigEndian.AppendUint16(data, id)
					data = binary.BigEndian.AppendUint16(data, 0) // the peer status word
				}
			case control.OpReadVariables:
				data = []byte(peers[request.AssociationID])
			}

			reply := control.NTPControlMsgHead{
				VnMode:        control.MakeVnMode(3, control.Mode),
				REMOp:         control.MakeREMOp(true, false, false, int(request.GetOperation())),
				Sequence:      request.Sequence,
				AssociationID: request.AssociationID,
				Count:         uint16(len(data)), //nolint:gosec // the test payloads are a few dozen bytes
			}

			var out bytes.Buffer

			if err := binary.Write(&out, binary.BigEndian, reply); err != nil {
				return
			}

			out.Write(data)

			if _, err := conn.WriteTo(out.Bytes(), addr); err != nil {
				return
			}
		}
	}()

	return conn.LocalAddr().String()
}

// TestGather checks the whole exchange against a daemon speaking the control protocol:
// read status for the peer list, then read variables for each peer. The payloads are the
// ones a real ntpsec 1.2.2 sent (reach as hex, delay/offset/jitter in milliseconds).
func TestGather(t *testing.T) {
	address := fakeNTPD(t, map[uint16]string{
		0x4570: `srcadr=37.59.63.125, srcport=123, stratum=2, reach=0xff, delay=23.179829, offset=43.346313, jitter=8.005333`,
		0x456d: `srcadr=54.38.114.34, srcport=123, stratum=4, reach=0xf0, delay=22.485940, offset=45.617717, jitter=3.749494`,
		// A pool placeholder: ntpd keeps one per configured pool until it picks a source,
		// with no address and no measurement. ntpq shows these as the ".POOL." rows.
		0x4569: `srcadr=0.0.0.0, srcport=0, stratum=16, reach=0x0, delay=0.000000, offset=0.000000, jitter=0.000001`,
	})

	acc := &internal.StoreAccumulator{}

	if err := (&controlInput{address: address}).Gather(acc); err != nil {
		t.Fatalf("Gather() = %v", err)
	}

	if len(acc.Errors) != 0 {
		t.Errorf("Gather() errors = %v", acc.Errors)
	}

	if len(acc.Measurement) != 2 {
		t.Fatalf("Gather() reported %d peers, want 2 (the placeholder must be dropped)", len(acc.Measurement))
	}

	byRemote := make(map[string]map[string]any, len(acc.Measurement))

	for _, m := range acc.Measurement {
		if m.Name != "ntpq" {
			t.Errorf("measurement name = %q, want \"ntpq\"", m.Name)
		}

		byRemote[m.Tags["remote"]] = m.Fields
	}

	fields, ok := byRemote["37.59.63.125"]
	if !ok {
		t.Fatalf("no measurement for the sys.peer, got %v", slices.Sorted(maps.Keys(byRemote)))
	}

	// Milliseconds as ntpd reports them; transformMetrics is what turns them into seconds.
	if got := fields["delay"]; got != 23.179829 {
		t.Errorf("delay = %v, want 23.179829", got)
	}

	if got := fields["offset"]; got != 43.346313 {
		t.Errorf("offset = %v, want 43.346313", got)
	}

	if got := fields["jitter"]; got != 8.005333 {
		t.Errorf("jitter = %v, want 8.005333", got)
	}

	// 0xff: all 8 remembered polls answered.
	if got := fields["reach"]; got != 1.0 {
		t.Errorf("reach = %v, want 1 (a fully reachable peer)", got)
	}

	// 0xf0: 4 of the last 8 polls answered.
	if got := byRemote["54.38.114.34"]["reach"]; got != 0.5 {
		t.Errorf("reach = %v, want 0.5", got)
	}
}

// TestGatherWithoutPeer checks a daemon that reports no usable peer is an error rather
// than a silent success: ntpd always has at least its configured sources, so nothing to
// report means the answer wasn't usable, and reporting Ok would hide that.
func TestGatherWithoutPeer(t *testing.T) {
	address := fakeNTPD(t, map[uint16]string{
		0x4569: `srcadr=0.0.0.0, srcport=0, stratum=16, reach=0x0`,
	})

	acc := &internal.StoreAccumulator{}

	if err := (&controlInput{address: address}).Gather(acc); err == nil {
		t.Error("Gather() = nil, want an error")
	}

	if len(acc.Measurement) != 0 {
		t.Errorf("Gather() reported %v, want nothing", acc.Measurement)
	}
}

// TestGatherMalformedReply checks that a reply cannot be talked into a slice bound.
// control.NTPClient believes the reply's own Count field over the bytes it actually read,
// so a datagram announcing more data than it carries panics there -- and a panic in a
// gather takes the agent down, since crashreport.ProcessPanic re-panics after reporting.
// Whatever answers at the address we are pointed at can send that datagram.
func TestGatherMalformedReply(t *testing.T) {
	cases := map[string]func(request control.NTPControlMsgHead) [][]byte{
		"a header announcing more data than the datagram carries": func(request control.NTPControlMsgHead) [][]byte {
			reply := control.NTPControlMsgHead{
				VnMode:   control.MakeVnMode(3, control.Mode),
				REMOp:    control.MakeREMOp(true, false, false, int(request.GetOperation())),
				Sequence: request.Sequence,
				Count:    2000, // the datagram below carries none
			}

			var out bytes.Buffer

			_ = binary.Write(&out, binary.BigEndian, reply)

			return [][]byte{out.Bytes()}
		},
		"a datagram shorter than a header": func(_ control.NTPControlMsgHead) [][]byte {
			return [][]byte{{6, 2, 0}}
		},
		"a reply continued in more packets than it could ever need": func(request control.NTPControlMsgHead) [][]byte {
			// Every packet keeps the More bit set, so the reply never ends: nothing in
			// the protocol bounds this, which is why exchange caps the total.
			const (
				packets      = 400
				dataPerPaket = 400
			)

			reply := control.NTPControlMsgHead{
				VnMode:   control.MakeVnMode(3, control.Mode),
				REMOp:    control.MakeREMOp(true, false, true, int(request.GetOperation())),
				Sequence: request.Sequence,
				Count:    dataPerPaket,
			}

			var out bytes.Buffer

			_ = binary.Write(&out, binary.BigEndian, reply)
			out.Write(make([]byte, dataPerPaket))

			datagrams := make([][]byte, packets)
			for i := range datagrams {
				datagrams[i] = out.Bytes()
			}

			return datagrams
		},
	}

	for name, reply := range cases {
		t.Run(name, func(t *testing.T) {
			address := rawNTPD(t, reply)

			acc := &internal.StoreAccumulator{}

			// Must be an error, and above all must not panic.
			if err := (&controlInput{address: address}).Gather(acc); err == nil {
				t.Error("Gather() = nil, want an error")
			}
		})
	}
}

// TestGatherRefused checks that a daemon answering with the error bit set is reported as
// having refused the request -- what a restrict policy does -- rather than as a daemon
// with no peers, which sends whoever reads it to the wrong configuration file.
func TestGatherRefused(t *testing.T) {
	address := rawNTPD(t, func(request control.NTPControlMsgHead) [][]byte {
		reply := control.NTPControlMsgHead{
			VnMode:   control.MakeVnMode(3, control.Mode),
			REMOp:    control.MakeREMOp(true, true, false, int(request.GetOperation())), // error bit
			Sequence: request.Sequence,
		}

		var out bytes.Buffer

		_ = binary.Write(&out, binary.BigEndian, reply)

		return [][]byte{out.Bytes()}
	})

	err := (&controlInput{address: address}).Gather(&internal.StoreAccumulator{})

	if !errors.Is(err, errRequestRefused) {
		t.Errorf("Gather() = %v, want %v", err, errRequestRefused)
	}
}

// rawNTPD answers each request with whatever datagrams reply returns, without going
// through the protocol types, so a reply the protocol shouldn't produce can be sent.
func rawNTPD(t *testing.T, reply func(request control.NTPControlMsgHead) [][]byte) string {
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

		buffer := make([]byte, 1024)

		for {
			n, addr, err := conn.ReadFrom(buffer)
			if err != nil {
				return // closed by the cleanup
			}

			var request control.NTPControlMsgHead

			if n >= 12 {
				_ = binary.Read(bytes.NewReader(buffer[:12]), binary.BigEndian, &request)
			}

			for _, datagram := range reply(request) {
				if _, err := conn.WriteTo(datagram, addr); err != nil {
					return
				}
			}
		}
	}()

	return conn.LocalAddr().String()
}

// TestGatherUnreachable checks the failure that matters in practice: a daemon that never
// answers, either because nothing listens or because its "restrict" lines refuse mode-6
// queries from us. The gather must fail rather than hang.
func TestGatherUnreachable(t *testing.T) {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}

	address := conn.LocalAddr().String()

	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}

	acc := &internal.StoreAccumulator{}

	if err := (&controlInput{address: address}).Gather(acc); err == nil {
		t.Error("Gather() = nil, want an error")
	}
}

// TestParseReach checks the "reach" shift register is read in the base the daemon sent it
// in: ntpsec uses hex with a 0x prefix, while ntpq's display and older implementations
// use octal, and misreading one as the other silently reports the wrong reachability.
func TestParseReach(t *testing.T) {
	cases := []struct {
		value string
		want  uint64
		ok    bool
	}{
		{value: "0xff", want: 255, ok: true}, // what ntpsec 1.2.2 sends over the wire
		{value: "0xf0", want: 240, ok: true},
		{value: "0x0", want: 0, ok: true},
		{value: "0377", want: 255, ok: true}, // octal with the prefix Go understands
		{value: "377", want: 255, ok: true},  // bare octal, as ntpq prints it
		{value: "0", want: 0, ok: true},      // an unreachable peer, same in every base
		// Bare digits that fit in the register are ambiguous -- octal 17 is 15 -- and are
		// read as decimal, the base Go's 0 assumes. Nothing distinguishes the two, and
		// the modern implementations send a prefix (see the hex cases above).
		{value: "17", want: 17, ok: true},
		{value: "", want: 0, ok: false},      // the variable was missing
		{value: "yes", want: 0, ok: false},   // not a number at all
		{value: "0x1ff", want: 0, ok: false}, // more than the register can hold
		{value: "-0x1", want: 0, ok: false},  // not unsigned
	}

	for _, tc := range cases {
		t.Run(tc.value, func(t *testing.T) {
			got, ok := parseReach(tc.value)

			if ok != tc.ok {
				t.Fatalf("parseReach(%q) ok = %v, want %v", tc.value, ok, tc.ok)
			}

			if got != tc.want {
				t.Errorf("parseReach(%q) = %d, want %d", tc.value, got, tc.want)
			}
		})
	}
}

// TestPeerAddressBecomesALabel checks where a peer's address ends up: on a label of its
// own, not in the item. The item is the service instance, and modify.AddInstance prefixes
// whatever the input put there with the container name -- which is how the address used
// to end up as "test-ntp_37.59.63.125", hidden behind a name that is supposed to say
// which instance the point is about.
func TestPeerAddressBecomesALabel(t *testing.T) {
	address := fakeNTPD(t, map[uint16]string{
		0x4570: `srcadr=37.59.63.125, reach=0xff, delay=23.179829, offset=43.346313, jitter=8.005333`,
		0x456d: `srcadr=54.38.114.34, reach=0xf0, delay=22.485940, offset=45.617717, jitter=3.749494`,
	})

	store := &internal.StoreAccumulator{}
	acc := internal.Accumulator{
		RenameGlobal:     renameGlobal,
		TransformMetrics: transformMetrics,
		Accumulator:      store,
	}

	acc.PrepareGather()

	if err := (&controlInput{address: address}).Gather(&acc); err != nil {
		t.Fatalf("Gather() = %v", err)
	}

	if len(store.Measurement) != 2 {
		t.Fatalf("got %d measurements, want one per peer: %#v", len(store.Measurement), store.Measurement)
	}

	addresses := map[string]bool{}

	for _, m := range store.Measurement {
		if item := m.Tags[types.LabelItem]; item != "" {
			t.Errorf("item == %q, want it left to the service instance", item)
		}

		if remote := m.Tags[remoteTag]; remote != "" {
			t.Errorf("tag %q == %q, want it moved to %q", remoteTag, remote, types.LabelPeerAddress)
		}

		addresses[m.Tags[types.LabelPeerAddress]] = true
	}

	if !addresses["37.59.63.125"] || !addresses["54.38.114.34"] {
		t.Errorf("%s labels == %v, want both peers represented", types.LabelPeerAddress, addresses)
	}
}

// TestTransformMetrics checks the units the API sees: seconds for durations and a
// percentage for reach, the same as the ntpq plugin this replaced produced.
func TestTransformMetrics(t *testing.T) {
	fields := transformMetrics(internal.GatherContext{}, map[string]float64{
		"delay":  23.179829,
		"offset": 43.346313,
		"jitter": 8.005333,
		"reach":  0.5,
	}, nil)

	want := map[string]float64{
		"delay_seconds":  0.023179829,
		"offset_seconds": 0.043346313,
		"jitter_seconds": 0.008005333,
		"reach_perc":     50,
	}

	if len(fields) != len(want) {
		t.Fatalf("transformMetrics() = %v, want %v", fields, want)
	}

	for name, wantValue := range want {
		// Compared with a tolerance: dividing by 1000 isn't exact in binary floating
		// point (23.179829 ms gives 0.023179829000000002 s).
		if got := fields[name]; math.Abs(got-wantValue) > 1e-12 {
			t.Errorf("transformMetrics()[%q] = %v, want %v", name, got, wantValue)
		}
	}
}
