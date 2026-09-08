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
	"math"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
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

// TestServerStatsCountersBecomeRates checks the serverstats counters -- what this chronyd
// has served to clients -- are turned into per-second rates. chronyd accumulates them from
// its start, so the raw value only ever grows; what a user watches is requests per second
// and drops per second.
//
// This group is only asked for when chronyd is reached over its unix socket, since it
// answers "not authorised" over the command port -- see New.
func TestServerStatsCountersBecomeRates(t *testing.T) {
	store := &internal.StoreAccumulator{}
	acc := internal.Accumulator{ //nolint:exhaustruct
		RenameGlobal:          renameGlobal,
		TransformMetrics:      transformMetrics,
		DifferentiatedMetrics: []string{"ntp_hits", "ntp_drops", "log_drops"},
		Accumulator:           store,
	}

	t0 := time.Now()
	t1 := t0.Add(10 * time.Second)

	acc.PrepareGather()
	acc.AddFields("chrony_serverstats", map[string]any{
		"ntp_hits":         uint64(1000),
		"ntp_drops":        uint64(10),
		"log_drops":        uint64(0),
		"ntp_span_seconds": uint64(600),
	}, nil, t0)

	// Discard the first gather: a differentiated counter has no rate without a previous
	// point to compare against.
	store.Measurement = nil

	acc.PrepareGather()
	acc.AddFields("chrony_serverstats", map[string]any{
		"ntp_hits":         uint64(1000 + 500), // rate = 50/s
		"ntp_drops":        uint64(10 + 20),    // rate = 2/s
		"log_drops":        uint64(0 + 1),      // rate = 0.1/s
		"ntp_span_seconds": uint64(610),        // a gauge, untouched
	}, nil, t1)

	if len(store.Measurement) != 1 {
		t.Fatalf("got %d measurements, want 1: %#v", len(store.Measurement), store.Measurement)
	}

	fields := store.Measurement[0].Fields

	for name, want := range map[string]float64{
		"ntp_hits":  50,
		"ntp_drops": 2,
		"log_drops": 0.1,
		// Seconds covered by the timestamps chronyd holds, not a counter.
		"ntp_span_seconds": 610,
	} {
		got, _ := fields[name].(float64)
		if math.Abs(got-want) > 0.0001 {
			t.Errorf("fields[%s] = %v, want %v", name, fields[name], want)
		}
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

// TestCanUseChronydSocket checks the gate on asking chronyd for serverstats. Getting it
// wrong is not harmless in either direction: asking when the socket cannot be reached
// means the command port refuses the request on every single gather, and not asking when
// it can means three metrics that only exist there never arrive.
func TestCanUseChronydSocket(t *testing.T) {
	ownGroup, err := user.LookupGroupId(strconv.Itoa(os.Getgid()))
	if err != nil {
		t.Skipf("cannot resolve this process's own group: %v", err)
	}

	t.Run("no socket at all", func(t *testing.T) {
		// What the agent container looks like: chronyd runs on the host and its directory
		// isn't mounted, so the plugin falls back to the command port.
		socket := filepath.Join(t.TempDir(), "chronyd.sock")

		if canUseChronydSocket(socket, ownGroup.Name) {
			t.Error("canUseChronydSocket() = true with no socket present")
		}
	})

	t.Run("socket in a directory we can write", func(t *testing.T) {
		dir := t.TempDir()
		socket := filepath.Join(dir, "chronyd.sock")

		if err := os.WriteFile(socket, nil, 0o600); err != nil {
			t.Fatal(err)
		}

		if !canUseChronydSocket(socket, ownGroup.Name) {
			t.Error("canUseChronydSocket() = false though the directory is ours")
		}

		// The probe must not leave anything next to chronyd's own socket.
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}

		if len(entries) != 1 {
			t.Errorf("the probe left %d files behind, want only the socket", len(entries)-1)
		}
	})

	t.Run("group chronyd does not run as", func(t *testing.T) {
		socket := filepath.Join(t.TempDir(), "chronyd.sock")

		if err := os.WriteFile(socket, nil, 0o600); err != nil {
			t.Fatal(err)
		}

		// setSocketGroup returns "" when neither chrony nor _chrony resolves, and the
		// plugin cannot hand its socket to a group that doesn't exist.
		if canUseChronydSocket(socket, "") {
			t.Error("canUseChronydSocket() = true with no group to give the socket to")
		}
	})

	t.Run("directory we cannot write", func(t *testing.T) {
		if os.Getuid() == 0 {
			t.Skip("root ignores the permissions this case is about")
		}

		// The packaged install: /run/chrony is drwxr-x--- _chrony:_chrony and Glouton runs
		// as the "glouton" user, so the plugin cannot create its own socket beside chronyd's.
		dir := filepath.Join(t.TempDir(), "chrony")
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}

		// chronyd's socket goes in while we still can, then the directory is closed to us
		// the way the package leaves it: readable and traversable, not writable.
		socket := filepath.Join(dir, "chronyd.sock")
		if err := os.WriteFile(socket, nil, 0o600); err != nil {
			t.Fatal(err)
		}

		// A directory keeps its execute bit or nothing can be reached inside it, which is
		// also what the real /run/chrony looks like: r-x for the group, no w.
		if err := os.Chmod(dir, 0o500); err != nil { //nolint:gosec
			t.Fatal(err)
		}

		t.Cleanup(func() { _ = os.Chmod(dir, 0o700) }) //nolint:gosec

		if canUseChronydSocket(socket, ownGroup.Name) {
			t.Error("canUseChronydSocket() = true though the directory is not writable")
		}
	})
}
