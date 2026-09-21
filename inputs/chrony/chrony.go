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
	"fmt"
	"math/bits"
	"time"

	"github.com/bleemeo/glouton/inputs"
	"github.com/bleemeo/glouton/inputs/internal"
	"github.com/bleemeo/glouton/prometheus/registry"
	"github.com/bleemeo/glouton/types"

	fbchrony "github.com/facebook/time/ntp/chrony"
	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/chrony"
)

const (
	// probeSequence is the sequence number ProbePacket asks its reply to carry. Only one
	// request is ever in flight, so it never has to vary.
	probeSequence = 1
	// replyPacketType is what chronyd sets on a command reply (a request is 1).
	replyPacketType = 2
	// statusSuccess is the status of a reply chronyd accepted (STT_SUCCESS).
	statusSuccess = 0
)

var (
	errBadReply       = errors.New("not a chrony command reply")
	errRequestRefused = errors.New("chronyd refused the request, check its cmdallow lines")
)

// New initialise chrony.Input, reading the chronyd at address -- a "host:port" -- over
// chrony's command protocol. The address is always used and is always UDP.
func New(address string) (telegraf.Input, registry.RegistrationOption, error) {
	input, ok := telegraf_inputs.Inputs["chrony"]
	if !ok {
		return nil, registry.RegistrationOption{}, inputs.ErrDisabledInput //nolint:exhaustruct
	}

	chronyInput, ok := input().(*chrony.Chrony)
	if !ok {
		return nil, registry.RegistrationOption{}, inputs.ErrUnexpectedType //nolint:exhaustruct
	}

	chronyInput.Server = "udp://" + address

	// tracking is the system-wide summary (last_offset, rms_offset, root_delay). activity
	// counts how many configured sources are actually reachable right now. sources gives
	// per-source detail.
	//
	// The two the plugin also offers are left out: "sourcestats" (how good each source's
	// estimate is) publishes no metric and costs a request per source, and "serverstats" is
	// only answered over chronyd's unix socket -- the command port replies "not authorised"
	// -- while that socket is 0700 _chrony, unreachable for a packaged Glouton running as
	// its own user.
	chronyInput.Metrics = []string{"tracking", "activity", "sources"}

	internalInput := &internal.Input{
		Input: chronyInput,
		Accumulator: internal.Accumulator{
			RenameGlobal:     renameGlobal,
			TransformMetrics: transformMetrics,
		},
		Name: "chrony",
	}

	// Registered with its own options rather than the default compatibility naming, which
	// keeps only the item: chrony_sources reports one point per time source, and the source
	// has to stay a label of its own (see renameGlobal) or the sources all collapse into a
	// single series.
	//
	// Gathering less often than the default 10 s because reading the sources costs one
	// request per source, and nothing chronyd reports here changes faster than its own poll
	// interval (64 s to 1024 s).
	options := registry.RegistrationOption{ //nolint:exhaustruct
		MinInterval: time.Minute,
	}

	return internalInput, options, nil
}

// ProbePacket returns the wire bytes of a chrony "tracking" request, which is what the
// status check sends. It is a real request -- the one this input sends for the
// chrony_last_offset/rms_offset metrics -- because chrony's command protocol is hardened
// against amplification abuse and may drop arbitrary bytes instead of replying, making a
// check built on a guessed payload report "down" for a healthy chronyd.
func ProbePacket() []byte {
	packet := fbchrony.NewTrackingPacket()
	packet.SetSequence(probeSequence)

	var buf bytes.Buffer

	// Cannot fail: the packet is a fixed-size struct and a bytes.Buffer never errors.
	_ = binary.Write(&buf, binary.BigEndian, packet)

	return buf.Bytes()
}

// ValidateReply reports what is wrong with a reply to ProbePacket, or nil if it is the
// answer of a chronyd that let us in.
//
// A reply arriving at all is not enough: chronyd answers a request it refuses with a status
// reply rather than dropping it, so a chronyd that doesn't have the querying host in its
// cmdallow would otherwise count as healthy while this input fails every gather.
func ValidateReply(reply []byte) error {
	var head fbchrony.ReplyHead

	if err := binary.Read(bytes.NewReader(reply), binary.BigEndian, &head); err != nil {
		return fmt.Errorf("%w: %d bytes, too short for a reply header", errBadReply, len(reply))
	}

	if head.PKTType != replyPacketType {
		return fmt.Errorf("%w: packet type %d, want %d (a command reply)", errBadReply, head.PKTType, replyPacketType)
	}

	if head.Status != statusSuccess {
		// The status is chronyd's own word for why: "NO_HOST_ACCESS" for a host missing
		// from cmdallow, "BAD_PKT_VERSION" for a protocol it doesn't speak.
		return fmt.Errorf("%w: %s", errRequestRefused, head.Status)
	}

	if head.Reply != fbchrony.RpyTracking {
		return fmt.Errorf("%w: answered request %d, not the tracking request", errBadReply, head.Reply)
	}

	return nil
}

// renameGlobal drops the tags describing the current synchronization state
// (leap_status, reference_id and stratum) and the socket we queried. Their value
// changes while chronyd runs -- each change would start a new metric series -- and
// they don't tell which metric this is.
func renameGlobal(gatherContext internal.GatherContext) (internal.GatherContext, bool) {
	delete(gatherContext.Tags, "leap_status")
	delete(gatherContext.Tags, "reference_id")
	delete(gatherContext.Tags, "stratum")
	delete(gatherContext.Tags, "source")

	// chrony_sources reports one point per configured time source, tagged with the "peer"
	// name it was configured under. Sources coming from the same "pool" directive all share
	// that pool's name, so the peer alone doesn't identify a source: the resolved address
	// does, and chronyd reports it as a field. Promote it to types.LabelPeerAddress, the
	// label inputs/ntp puts ntpd's peers on.
	if ip, ok := gatherContext.OriginalFields["ip"].(string); ok && ip != "" {
		gatherContext.Tags[types.LabelPeerAddress] = ip
	}

	return gatherContext, false
}

// transformMetrics converts chrony_sources' reachability from its raw 0..255 value (an
// 8-bit shift register, one bit per poll) into a 0..100 percentage of the last 8 polls that
// succeeded -- the count of bits set, not the register's numeric value -- and renames
// latest_measurement (the per-source offset, already in seconds) accordingly.
func transformMetrics(_ internal.GatherContext, fields map[string]float64, _ map[string]any) map[string]float64 {
	if value, ok := fields["reachability"]; ok {
		delete(fields, "reachability")

		fields["reachability_perc"] = float64(bits.OnesCount8(uint8(uint32(value)&0xFF))) / 8 * 100
	}

	if value, ok := fields["latest_measurement"]; ok {
		delete(fields, "latest_measurement")

		fields["latest_measurement_seconds"] = value
	}

	return fields
}
