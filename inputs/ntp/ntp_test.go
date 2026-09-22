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
	"time"

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
				// The peer list: association ID and status word, 2 uint16 each. Never
				// association 0, which is the daemon itself rather than a peer -- a real
				// ntpd doesn't list it here, and peers[0] only overrides its variables.
				for _, id := range slices.Sorted(maps.Keys(peers)) {
					if id == 0 {
						continue
					}

					data = binary.BigEndian.AppendUint16(data, id)
					data = binary.BigEndian.AppendUint16(data, 0) // the peer status word
				}
			case control.OpReadVariables:
				if request.AssociationID == 0 {
					// Association 0 is the daemon itself. These are the variables a real
					// ntpsec answers with, trimmed to the ones addSystemFields reads.
					// peers[0], when set, replaces them -- a daemon whose own variables
					// are unusable.
					if override, ok := peers[0]; ok {
						data = []byte(override)
					} else {
						data = []byte(systemVariables)
					}
				} else {
					data = []byte(peers[request.AssociationID])
				}
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

	peers := peerMeasurements(acc.Measurement)

	if len(peers) != 2 {
		t.Fatalf("Gather() reported %d peers, want 2 (the placeholder must be dropped)", len(peers))
	}

	byRemote := make(map[string]map[string]any, len(peers))

	for _, m := range peers {
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

	// jitter is one of the peer variables deliberately not read, being unpublished.
	if got, ok := fields["jitter"]; ok {
		t.Errorf("jitter = %v, want it not gathered", got)
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

// TestGatherSystemVariables checks the daemon's own view of the local clock, read from
// association 0. This is the number a user actually watches -- "how far off is this clock"
// -- and the ntpd counterpart of chrony_last_offset, which per-peer offsets don't answer:
// they say how far each source is, not which one ntpd chose to follow.
func TestGatherSystemVariables(t *testing.T) {
	address := fakeNTPD(t, map[uint16]string{
		0x4570: `srcadr=37.59.63.125, srcport=123, stratum=2, reach=0xff, delay=23.179829, offset=43.346313, jitter=8.005333`,
	})

	// Through the same accumulator the input is registered with, so what is checked is the
	// published shape -- transformMetrics has to convert this measurement's own duration
	// fields, which are not the per-peer ones.
	store := &internal.StoreAccumulator{}
	acc := internal.Accumulator{ //nolint:exhaustruct
		RenameGlobal:     renameGlobal,
		TransformMetrics: transformMetrics,
		Accumulator:      store,
	}

	acc.PrepareGather()

	if err := (&controlInput{address: address}).Gather(&acc); err != nil {
		t.Fatalf("Gather() = %v", err)
	}

	fields := systemFieldsGathered(store)
	if fields == nil {
		t.Fatalf("no %q measurement, got %v", systemMeasurement, store.Measurement)
	}

	// The milliseconds of systemVariables, in seconds. Kept apart from the per-peer offset
	// of the same reply (43.346313 ms) so a mix-up between the two would show.
	got, ok := fields["offset_seconds"].(float64)
	if !ok {
		t.Fatalf("offset_seconds is %v (%T), want a float", fields["offset_seconds"], fields["offset_seconds"])
	}

	if math.Abs(got-0.003420363) > 1e-9 {
		t.Errorf("offset_seconds = %v, want 0.003420363", got)
	}

	// The offset is the only one of association 0's twenty variables that is published, so
	// it should be the only one read.
	if len(fields) != 1 {
		t.Errorf("system fields = %v, want only offset_seconds", fields)
	}

	// A peer point and the system point both carry an "offset": they must not be confused,
	// so the peer's has to keep its own value and its own label.
	for _, m := range peerMeasurements(store.Measurement) {
		if m.Tags[types.LabelPeerAddress] == "" {
			t.Errorf("peer point %v has no %s label", m.Fields, types.LabelPeerAddress)
		}

		if peerOffset, _ := m.Fields["offset_seconds"].(float64); math.Abs(peerOffset-0.043346313) > 1e-9 {
			t.Errorf("peer offset_seconds = %v, want 0.043346313 (the peer's, not the system's)", peerOffset)
		}
	}
}

// TestGatherWithoutPeer covers a daemon that reports no usable peer, which is an error only
// when its own variables were missed too.
//
// ntpd always has at least its configured sources, so no peer at all means the answer wasn't
// usable and reporting Ok would hide that. But association 0 carries
// ntpq_system_offset_seconds, the only NTP metric in the default set, and it is answered
// before any peer is read: an ntpd pointed at a pool reports nothing but ".POOL."
// placeholders until it selects peers, and erroring through those minutes would call a
// gather failed while the one metric anyone receives was published normally.
func TestGatherWithoutPeer(t *testing.T) {
	placeholder := `srcadr=0.0.0.0, srcport=0, stratum=16, reach=0x0`

	cases := map[string]struct {
		peers   map[uint16]string
		wantErr bool
	}{
		"the system offset still published": {
			peers:   map[uint16]string{0x4569: placeholder},
			wantErr: false,
		},
		// peers[0] replaces the daemon's own variables: nothing usable anywhere.
		"nothing usable at all": {
			peers:   map[uint16]string{0: `leap=00, stratum=16`, 0x4569: placeholder},
			wantErr: true,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			acc := &internal.StoreAccumulator{}

			err := (&controlInput{address: fakeNTPD(t, tc.peers)}).Gather(acc)
			if tc.wantErr && err == nil {
				t.Error("Gather() = nil, want an error")
			}

			if !tc.wantErr && err != nil {
				t.Errorf("Gather() = %v, want nil: the system offset was published", err)
			}

			// No peer is reported either way: a placeholder is not a peer.
			if peers := peerMeasurements(acc.Measurement); len(peers) != 0 {
				t.Errorf("Gather() reported %v, want no peer", peers)
			}
		})
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
		"a reply continued in more packets than it could ever need": func(request control.NTPControlMsgHead) [][]byte {
			// Every packet keeps the More bit set, so the reply never ends: nothing in
			// the protocol bounds this, which is why exchange caps how many datagrams
			// it will read one reply from.
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

// controlDatagram builds one reply datagram: a header, then the data it announces.
func controlDatagram(head control.NTPControlMsgHead, data []byte) []byte {
	var out bytes.Buffer

	_ = binary.Write(&out, binary.BigEndian, head)

	out.Write(data)

	return out.Bytes()
}

// replyHead builds the header of a well-formed reply to request, for count bytes of data
// starting at offset in the whole reply, with the More bit set when another datagram
// follows.
func replyHead(request control.NTPControlMsgHead, offset int, count int, more bool) control.NTPControlMsgHead {
	return control.NTPControlMsgHead{
		VnMode:        control.MakeVnMode(3, control.Mode),
		REMOp:         control.MakeREMOp(true, false, more, int(request.GetOperation())),
		Sequence:      request.Sequence,
		AssociationID: request.AssociationID,
		Offset:        uint16(offset), //nolint:gosec // the test payloads are a few dozen bytes
		Count:         uint16(count),  //nolint:gosec // the test payloads are a few dozen bytes
	}
}

// peerListDatagram builds the reply to a read-status request: association ID and status
// word, 2 uint16 each.
func peerListDatagram(request control.NTPControlMsgHead, associations ...uint16) []byte {
	var data []byte

	for _, id := range associations {
		data = binary.BigEndian.AppendUint16(data, id)
		data = binary.BigEndian.AppendUint16(data, 0)
	}

	return controlDatagram(replyHead(request, 0, len(data), false), data)
}

// systemVariables is what association 0 answers with: ntpd's own view of the local clock.
// Values from a real ntpsec, cut down to what addSystemFields reads.
const systemVariables = `leap=00, stratum=2, precision=-24, rootdelay=26.352, ` +
	`rootdisp=8.918, offset=3.420363, frequency=-11.234, sys_jitter=0.634474, ` +
	`clk_jitter=0.421, clk_wander=0.503, tc=7, mintc=3`

// remotesGathered returns the address each per-peer measurement is about, sorted. The
// system measurement has no peer and is left out.
func remotesGathered(acc *internal.StoreAccumulator) []string {
	remotes := make([]string, 0, len(acc.Measurement))

	for _, m := range acc.Measurement {
		if m.Name == systemMeasurement {
			continue
		}

		remotes = append(remotes, m.Tags["remote"])
	}

	return slices.Sorted(slices.Values(remotes))
}

// peerMeasurements returns only the per-peer points, leaving out the system one.
func peerMeasurements(measurements []internal.Measurement) []internal.Measurement {
	peers := make([]internal.Measurement, 0, len(measurements))

	for _, m := range measurements {
		if m.Name != systemMeasurement {
			peers = append(peers, m)
		}
	}

	return peers
}

// systemFieldsGathered returns the fields of the system measurement, or nil when there is
// none.
func systemFieldsGathered(acc *internal.StoreAccumulator) map[string]any {
	for _, m := range acc.Measurement {
		if m.Name == systemMeasurement {
			return m.Fields
		}
	}

	return nil
}

// TestGatherIgnoresDatagramsOfAnotherRequest checks that a datagram left over from an
// earlier request is not read as the answer to the current one. All the peers are read
// over one socket, so a reply that stayed queued -- a duplicate the network delivered
// twice, or the rest of a reply that was abandoned halfway -- is waiting there when the
// next peer's request goes out. Reading it as that peer's variables publishes the first
// peer's numbers twice and drops a peer entirely, which no error would ever reveal.
func TestGatherIgnoresDatagramsOfAnotherRequest(t *testing.T) {
	peers := map[uint16]string{
		0x4570: `srcadr=37.59.63.125, srcport=123, stratum=2, reach=0xff, delay=23.179829, offset=43.346313, jitter=8.005333`,
		0x456d: `srcadr=54.38.114.34, srcport=123, stratum=4, reach=0xf0, delay=22.485940, offset=45.617717, jitter=3.749494`,
	}

	address := rawNTPD(t, func(request control.NTPControlMsgHead) [][]byte {
		switch request.GetOperation() {
		case control.OpReadStatus:
			return [][]byte{peerListDatagram(request, slices.Sorted(maps.Keys(peers))...)}
		case control.OpReadVariables:
			data := []byte(peers[request.AssociationID])
			datagram := controlDatagram(replyHead(request, 0, len(data), false), data)

			// The reply, and then a second copy of it: the datagram still queued when
			// the next peer's request goes out, carrying this request's sequence
			// number rather than that one's.
			return [][]byte{datagram, datagram}
		default:
			return nil
		}
	})

	acc := &internal.StoreAccumulator{}

	if err := (&controlInput{address: address}).Gather(acc); err != nil {
		t.Fatalf("Gather() = %v", err)
	}

	want := []string{"37.59.63.125", "54.38.114.34"}

	if got := remotesGathered(acc); !slices.Equal(got, want) {
		t.Errorf("Gather() reported peers %v, want %v", got, want)
	}
}

// TestGatherReassemblesFragments checks a reply spread over several datagrams is put back
// together by the offsets they carry rather than the order they arrive in. This is the
// common path, not an edge case: against the ntpsec 1.2.2 in the test container every
// single peer's variables came back as two fragments (468 bytes at offset 0, then 201 at
// offset 468). UDP may hand those to us either way round, and the split falls wherever
// 468 bytes land -- inside a k=v pair -- so concatenating them in arrival order turns a
// healthy peer's numbers into whatever the halves happen to spell.
func TestGatherReassemblesFragments(t *testing.T) {
	const (
		association = 0x4570
		// A split inside "delay=23.179829", so the two halves only parse in the right order.
		split     = 60
		variables = `srcadr=37.59.63.125, srcport=123, stratum=2, reach=0xff, delay=23.179829, offset=43.346313, jitter=8.005333`
	)

	address := rawNTPD(t, func(request control.NTPControlMsgHead) [][]byte {
		switch request.GetOperation() {
		case control.OpReadStatus:
			return [][]byte{peerListDatagram(request, association)}
		case control.OpReadVariables:
			head, tail := []byte(variables[:split]), []byte(variables[split:])

			// The final fragment first -- the one with the More bit clear, which is
			// also where a real reply keeps the status of the whole.
			return [][]byte{
				controlDatagram(replyHead(request, split, len(tail), false), tail),
				controlDatagram(replyHead(request, 0, len(head), true), head),
			}
		default:
			return nil
		}
	})

	acc := &internal.StoreAccumulator{}

	if err := (&controlInput{address: address}).Gather(acc); err != nil {
		t.Fatalf("Gather() = %v", err)
	}

	if got := remotesGathered(acc); !slices.Equal(got, []string{"37.59.63.125"}) {
		t.Fatalf("Gather() reported peers %v, want [37.59.63.125]", got)
	}

	if got := peerMeasurements(acc.Measurement)[0].Fields["delay"]; got != 23.179829 {
		t.Errorf("delay = %v, want 23.179829 (the value straddling the two fragments)", got)
	}
}

// TestGatherIgnoresStrayDatagrams checks that a datagram which isn't an answer to the
// request in hand is skipped and the real answer still read, rather than the gather being
// lost to it. Anything at all can arrive on a UDP socket, and the reference client
// (ntpq's __validate_packet) skips these for that reason.
func TestGatherIgnoresStrayDatagrams(t *testing.T) {
	const (
		association = 0x4570
		variables   = `srcadr=37.59.63.125, srcport=123, stratum=2, reach=0xff, delay=23.179829, offset=43.346313, jitter=8.005333`
	)

	cases := map[string]func(request control.NTPControlMsgHead) []byte{
		"shorter than a header": func(_ control.NTPControlMsgHead) []byte {
			return []byte{6, 2, 0}
		},
		"a request rather than a response": func(request control.NTPControlMsgHead) []byte {
			head := replyHead(request, 0, 0, false)
			head.REMOp = control.MakeREMOp(false, false, false, int(request.GetOperation()))

			return controlDatagram(head, nil)
		},
		"an answer to another operation": func(request control.NTPControlMsgHead) []byte {
			head := replyHead(request, 0, 0, false)
			head.REMOp = control.MakeREMOp(true, false, false, control.OpReadStatus+control.OpReadVariables)

			return controlDatagram(head, nil)
		},
		"an answer to a later sequence number": func(request control.NTPControlMsgHead) []byte {
			head := replyHead(request, 0, 0, false)
			head.Sequence = request.Sequence + 1

			return controlDatagram(head, nil)
		},
	}

	for name, stray := range cases {
		t.Run(name, func(t *testing.T) {
			address := rawNTPD(t, func(request control.NTPControlMsgHead) [][]byte {
				var reply []byte

				switch request.GetOperation() {
				case control.OpReadStatus:
					reply = peerListDatagram(request, association)
				case control.OpReadVariables:
					data := []byte(variables)
					reply = controlDatagram(replyHead(request, 0, len(data), false), data)
				default:
					return nil
				}

				return [][]byte{stray(request), reply}
			})

			acc := &internal.StoreAccumulator{}

			if err := (&controlInput{address: address}).Gather(acc); err != nil {
				t.Fatalf("Gather() = %v", err)
			}

			if got := remotesGathered(acc); !slices.Equal(got, []string{"37.59.63.125"}) {
				t.Errorf("Gather() reported peers %v, want [37.59.63.125]", got)
			}
		})
	}
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

	peers := peerMeasurements(store.Measurement)

	if len(peers) != 2 {
		t.Fatalf("got %d measurements, want one per peer: %#v", len(peers), peers)
	}

	addresses := map[string]bool{}

	for _, m := range peers {
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

// TestNewUsesTheAddressGiven pins which daemon the input reads. An address is only passed
// for a daemon Glouton's own loopback cannot be -- a containerised ntpd, or one the user
// declared -- so losing it would publish the peers of whichever ntpd sits next to Glouton
// under that service's name.
func TestNewUsesTheAddressGiven(t *testing.T) {
	cases := []struct {
		name    string
		address string
		want    string
	}{
		{name: "an address of its own", address: "172.23.0.2:123", want: "172.23.0.2:123"},
		{name: "the local daemon", address: "", want: "127.0.0.1:123"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input, _, err := New(tc.address)
			if err != nil {
				t.Fatal(err)
			}

			internalInput, ok := input.(*internal.Input)
			if !ok {
				t.Fatalf("New() returned a %T, want *internal.Input", input)
			}

			ci, ok := internalInput.Input.(*controlInput)
			if !ok {
				t.Fatalf("wrapped input is a %T, want *controlInput", internalInput.Input)
			}

			if ci.address != tc.want {
				t.Errorf("address = %q, want %q", ci.address, tc.want)
			}
		})
	}
}

// TestNewGathersSlowlyEnough pins the interval. Reading the peers costs one request per
// peer, and ntpd rate-limits per source address by default ("restrict ... limited"): at the
// registry's 10 s default a daemon with a dozen peers throttles us, and answers the NTP
// check coming from the same address with a Kiss-o'-Death, reporting a healthy server as
// unsynchronized.
func TestNewGathersSlowlyEnough(t *testing.T) {
	_, options, err := New("")
	if err != nil {
		t.Fatal(err)
	}

	if options.MinInterval < time.Minute {
		t.Errorf("MinInterval = %v, want at least a minute", options.MinInterval)
	}
}
