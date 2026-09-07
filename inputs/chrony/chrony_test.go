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

package chrony

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
	"time"

	"github.com/bleemeo/glouton/inputs/internal"
	"github.com/bleemeo/glouton/types"
	"github.com/google/go-cmp/cmp"

	fbchrony "github.com/facebook/time/ntp/chrony"
)

// TestTagsDropped checks that the tags describing the current synchronization state
// are dropped: their value changes while chronyd runs, and each change would
// otherwise start a new metric series.
func TestTagsDropped(t *testing.T) {
	store := &internal.StoreAccumulator{}
	acc := internal.Accumulator{
		RenameGlobal:     renameGlobal,
		TransformMetrics: transformMetrics,
		Accumulator:      store,
	}

	acc.PrepareGather()
	acc.AddFields("chrony", map[string]any{
		"frequency":       -1.5,
		"system_time":     0.000012,
		"last_offset":     0.000001,
		"rms_offset":      0.000003,
		"root_delay":      0.02,
		"root_dispersion": 0.001,
		"skew":            0.05,
	}, map[string]string{
		"leap_status":  "normal",
		"reference_id": "C0248F97",
		"stratum":      "3",
		"source":       "/run/chrony/chronyd.sock",
	}, time.Now())

	if len(store.Measurement) != 1 {
		t.Fatalf("got %d measurements, want 1: %#v", len(store.Measurement), store.Measurement)
	}

	if diff := cmp.Diff(map[string]string{}, store.Measurement[0].Tags); diff != "" {
		t.Errorf("tags of measurement %q (-want +got):\n%s", store.Measurement[0].Name, diff)
	}

	// The metrics themselves must pass through untouched: they are all gauges.
	if value, _ := store.Measurement[0].Fields["skew"].(float64); value != 0.05 {
		t.Errorf("fields[skew] == %v, want 0.05", store.Measurement[0].Fields["skew"])
	}
}

// TestActivityPassesThrough checks that chrony_activity's fields (counts of
// configured sources by reachability) pass through untouched.
func TestActivityPassesThrough(t *testing.T) {
	store := &internal.StoreAccumulator{}
	acc := internal.Accumulator{
		RenameGlobal:     renameGlobal,
		TransformMetrics: transformMetrics,
		Accumulator:      store,
	}

	acc.PrepareGather()
	acc.AddFields("chrony_activity", map[string]any{
		"online":        3,
		"offline":       1,
		"burst_online":  0,
		"burst_offline": 0,
		"unresolved":    0,
	}, map[string]string{"source": "/run/chrony/chronyd.sock"}, time.Now())

	fields := store.Measurement[0].Fields

	want := map[string]float64{"online": 3, "offline": 1}
	for name, wantValue := range want {
		if got, _ := fields[name].(float64); got != wantValue {
			t.Errorf("fields[%q] == %v, want %v", name, fields[name], wantValue)
		}
	}
}

// TestSourcesIPBecomesALabelAndFieldsConverted checks that chrony_sources' "ip" field
// becomes a label of its own -- so each source gets its own series without the address
// being buried in the item next to the container name -- that reachability, the raw
// 0..255 value of the 8-bit reach shift register, is converted into the percentage of
// the last 8 polls that succeeded (a bit count, not the register's numeric value), and
// that latest_measurement (already in seconds) is renamed accordingly.
func TestSourcesIPBecomesALabelAndFieldsConverted(t *testing.T) {
	store := &internal.StoreAccumulator{}
	acc := internal.Accumulator{
		RenameGlobal:     renameGlobal,
		TransformMetrics: transformMetrics,
		Accumulator:      store,
	}

	acc.PrepareGather()
	acc.AddFields("chrony_sources", map[string]any{
		"ip":                 "17.253.108.125",
		"reachability":       uint16(0b0011_1111), // 63: 6 of the last 8 polls succeeded
		"latest_measurement": 0.000048,
		"stratum":            uint16(2),
	}, map[string]string{
		"peer":   "time.apple.com",
		"source": "/run/chrony/chronyd.sock",
	}, time.Now())

	// No item: it is the service instance, set once for the whole input, and the peer
	// name stays as chronyd reported it (several pool members share one).
	wantTags := map[string]string{"peer": "time.apple.com", types.LabelPeerAddress: "17.253.108.125"}
	if diff := cmp.Diff(wantTags, store.Measurement[0].Tags); diff != "" {
		t.Errorf("tags of measurement %q (-want +got):\n%s", store.Measurement[0].Name, diff)
	}

	fields := store.Measurement[0].Fields

	want := map[string]float64{
		"reachability_perc":          75, // 6/8 * 100
		"latest_measurement_seconds": 0.000048,
	}
	for name, wantValue := range want {
		if got, _ := fields[name].(float64); got != wantValue {
			t.Errorf("fields[%q] == %v, want %v", name, fields[name], wantValue)
		}
	}

	for _, name := range []string{"reachability", "latest_measurement"} {
		if _, ok := fields[name]; ok {
			t.Errorf("raw field %q should have been renamed, still present", name)
		}
	}
}

// TestSourcesFromSamePoolGetDistinctAddresses checks that two sources resolved from the
// same "pool" directive -- which chronyd reports under the identical "peer" name -- still
// end up as two distinct series, told apart by their (always unique) address.
func TestSourcesFromSamePoolGetDistinctAddresses(t *testing.T) {
	store := &internal.StoreAccumulator{}
	acc := internal.Accumulator{
		RenameGlobal:     renameGlobal,
		TransformMetrics: transformMetrics,
		Accumulator:      store,
	}

	acc.PrepareGather()

	for _, ip := range []string{"17.253.108.125", "17.253.108.253"} {
		acc.AddFields("chrony_sources", map[string]any{
			"ip":           ip,
			"reachability": uint16(255),
		}, map[string]string{"peer": "time.apple.com"}, time.Now())
	}

	if len(store.Measurement) != 2 {
		t.Fatalf("got %d measurements, want 2 (one per IP): %#v", len(store.Measurement), store.Measurement)
	}

	addresses := map[string]bool{}
	for _, m := range store.Measurement {
		addresses[m.Tags[types.LabelPeerAddress]] = true
	}

	if !addresses["17.253.108.125"] || !addresses["17.253.108.253"] {
		t.Errorf("%s labels == %v, want both pool IPs represented", types.LabelPeerAddress, addresses)
	}
}

// TestProbePacket checks the payload the status check sends: a request the daemon really
// answers, since chrony's command protocol is hardened against amplification abuse and
// may drop anything else without replying.
func TestProbePacket(t *testing.T) {
	packet := ProbePacket()

	// chrony pads its requests to the size of the largest reply it may send, so the
	// request is fixed-size -- an empty or truncated one would never get a reply. The
	// padding is an unexported field of the library's packet, which is exactly what a
	// hand-rolled encoding of the header alone would have missed.
	if want := binary.Size(fbchrony.NewTrackingPacket()); len(packet) != want {
		t.Fatalf("ProbePacket() is %d bytes, want %d", len(packet), want)
	}

	// The header the daemon reads, in the order it reads it (chrony's candm.h):
	// version, packet type, 2 reserved bytes, command, attempt, sequence.
	if got := packet[0]; got != 6 {
		t.Errorf("ProbePacket() version = %d, want 6 (the current protocol version)", got)
	}

	if got := packet[1]; got != 1 {
		t.Errorf("ProbePacket() packet type = %d, want 1 (a command request)", got)
	}

	if got := binary.BigEndian.Uint16(packet[4:6]); got != 33 {
		t.Errorf("ProbePacket() command = %d, want 33 (REQ_TRACKING)", got)
	}

	if got := binary.BigEndian.Uint32(packet[8:12]); got != probeSequence {
		t.Errorf("ProbePacket() sequence = %d, want %d (the reply must be matchable to the request)", got, probeSequence)
	}
}

// TestValidateReply checks which replies count as a healthy chronyd. A reply arriving at
// all is not enough: chronyd answers a request it refuses with a status reply instead of
// dropping it, so the status is the difference between "the daemon is fine" and "it isn't
// letting us ask" -- which is the same configuration mistake that leaves the metrics empty.
func TestValidateReply(t *testing.T) {
	reply := func(packetType uint8, command fbchrony.CommandType, replyType fbchrony.ReplyType, status fbchrony.ResponseStatusType) []byte {
		head := fbchrony.ReplyHead{
			Version: 6,
			PKTType: fbchrony.PacketType(packetType),
			Command: command,
			Reply:   replyType,
			Status:  status,
		}

		var buf bytes.Buffer

		if err := binary.Write(&buf, binary.BigEndian, head); err != nil {
			t.Fatal(err)
		}

		return buf.Bytes()
	}

	cases := []struct {
		name    string
		reply   []byte
		wantErr error
	}{
		{
			name:  "a tracking reply from a chronyd that let us in",
			reply: reply(2, 33, fbchrony.RpyTracking, 0),
		},
		{
			// STT_NOHOSTACCESS: the querying host isn't in cmdallow. chronyd says so
			// rather than dropping the request, so this is a reply that must not pass.
			name:    "refused for lack of host access",
			reply:   reply(2, 33, fbchrony.RpyTracking, 10),
			wantErr: errRequestRefused,
		},
		{
			// STT_BADPKTVERSION: the protocol version we send is one it doesn't speak.
			name:    "refused for the protocol version",
			reply:   reply(2, 33, fbchrony.RpyTracking, 18),
			wantErr: errRequestRefused,
		},
		{
			// Whatever answered, it isn't chronyd's command protocol.
			name:    "a request echoed back rather than a reply",
			reply:   reply(1, 33, fbchrony.RpyTracking, 0),
			wantErr: errBadReply,
		},
		{
			name:    "an answer to another request",
			reply:   reply(2, 33, fbchrony.RpyNSources, 0),
			wantErr: errBadReply,
		},
		{
			name:    "too short to be a reply header",
			reply:   []byte{6, 2, 0},
			wantErr: errBadReply,
		},
		{
			name:    "nothing at all",
			reply:   nil,
			wantErr: errBadReply,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateReply(tc.reply)

			if !errors.Is(err, tc.wantErr) {
				t.Errorf("ValidateReply() = %v, want %v", err, tc.wantErr)
			}
		})
	}
}
