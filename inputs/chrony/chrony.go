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

// New initialise chrony.Input. With no address, it queries the local chronyd through
// its control socket (/run/chrony/chronyd.sock) or, failing that, over UDP on
// localhost:323. Unlike Varnish/ntpq, chrony's control protocol is a plain UDP call
// (see telegraf's chrony plugin, which never shells out to a binary), so a remote
// chronyd genuinely is reachable: pass its "host:port" as address to query it instead
// of the local one -- e.g. a chronyd running in a different container than Glouton.
func New(address string) (telegraf.Input, registry.RegistrationOption, error) {
	input, ok := telegraf_inputs.Inputs["chrony"]
	if !ok {
		return nil, registry.RegistrationOption{}, inputs.ErrDisabledInput //nolint:exhaustruct
	}

	chronyInput, ok := input().(*chrony.Chrony)
	if !ok {
		return nil, registry.RegistrationOption{}, inputs.ErrUnexpectedType //nolint:exhaustruct
	}

	if address != "" {
		chronyInput.Server = "udp://" + address
	}

	// tracking is the system-wide summary (last_offset, rms_offset). activity
	// counts how many configured sources are actually reachable right now.
	// sources gives per-source detail, mirroring what ntpq already reports
	// per-peer for ntpd.
	chronyInput.Metrics = []string{"tracking", "activity", "sources"}

	internalInput := &internal.Input{
		Input: chronyInput,
		Accumulator: internal.Accumulator{
			RenameGlobal:     renameGlobal,
			TransformMetrics: transformMetrics,
		},
		Name: "chrony",
	}

	// Registered with its own options rather than the default compatibility naming,
	// which keeps only the item: chrony_sources reports one point per time source, and
	// the source has to be part of the series identity or they all collapse into one.
	// With labels kept it can be a label of its own (see renameGlobal) instead of being
	// concatenated into the item behind the container name.
	//
	// Reading the sources costs one request per source, and nothing chronyd reports here
	// changes faster than its own poll interval (64 s to 1024 s), so the default 10 s
	// would only buy request volume -- which for ntpd next door is enough to trip its
	// rate limiting, see inputs/ntp.
	options := registry.RegistrationOption{ //nolint:exhaustruct
		MinInterval: time.Minute,
	}

	return internalInput, options, nil
}

// ProbePacket returns the wire bytes of a real chrony "tracking" request -- the same
// request this input sends for the chrony_last_offset/rms_offset metrics, known to get a
// real reply from any chronyd that allows us in (cmdallow). It is what the status check
// sends: chrony's command protocol is deliberately hardened against amplification abuse,
// so it doesn't reply to just anything, and there is no guarantee malformed or arbitrary
// bytes would get a response at all rather than being dropped -- which would make a check
// built on a guessed payload report "down" for a perfectly healthy chronyd.
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
// A reply arriving at all is not enough: chronyd answers a request it refuses with a
// status reply rather than dropping it, so a chronyd that doesn't have the querying host
// in its cmdallow -- exactly the configuration mistake worth reporting -- would otherwise
// count as healthy while this input fails every gather.
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

	// chrony_sources reports one point per configured time source, tagged with the
	// "peer" name it was configured under. Sources coming from the same "pool"
	// directive all share that pool's name, so the peer alone doesn't identify a
	// source: the resolved address does, and chronyd reports it as a field. It is
	// promoted to a label of its own here -- types.LabelPeerAddress, the one
	// inputs/ntp puts ntpd's peers on, and the name chronyd itself uses for it.
	//
	// The item is deliberately left alone: it is the service instance (the container
	// name), and putting the address there too gave items like
	// "test-chrony_17.253.14.251" -- the address hidden behind a container name, in a
	// label that is supposed to say which instance this is.
	if ip, ok := gatherContext.OriginalFields["ip"].(string); ok && ip != "" {
		gatherContext.Tags[types.LabelPeerAddress] = ip
	}

	return gatherContext, false
}

// transformMetrics converts chrony_sources' reachability from its raw 0..255 value
// (the decimal form of the same 8-bit reach shift register ntpq reports in octal,
// see inputs/ntp) into a 0..100 percentage of the last 8 polls that succeeded --
// the count of bits set, not the register's numeric value -- and renames
// latest_measurement (chrony_sources' per-source offset, already in seconds)
// accordingly.
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
