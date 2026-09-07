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
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math/bits"
	"net"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/bleemeo/glouton/inputs/internal"
	"github.com/bleemeo/glouton/prometheus/registry"
	"github.com/bleemeo/glouton/types"

	"github.com/facebook/time/ntp/control"
	"github.com/influxdata/telegraf"
)

const (
	// ntpDefaultPort is the NTP port, which ntpd also answers its control protocol on.
	ntpDefaultPort = 123
	// dialTimeout and gatherTimeout bound a gather. The whole exchange is a handful of
	// small UDP round trips (measured at 3 ms for 19 peers against an ntpd on the same
	// Docker host), so these only matter for a daemon that stopped answering.
	dialTimeout   = 5 * time.Second
	gatherTimeout = 10 * time.Second
	// maxPollsRemembered is the width of the "reach" shift register: ntpd remembers
	// whether each of the last 8 polls was answered, one bit each.
	maxPollsRemembered = 8
	// remoteTag is the tag addPeerFields reports a peer's address under, named after
	// ntpq's own column so the measurement still looks like the ntpq plugin's.
	remoteTag = "remote"
	// controlHeaderSize is the fixed part of a control message, before its data
	// (RFC1305 appendix B): version/mode, operation, sequence, status, association ID,
	// offset and count, 2 bytes each but the first two.
	controlHeaderSize = 12
	// maxControlPacketSize is the read buffer for one reply datagram. ntpd keeps a reply's
	// data under 468 bytes and continues in another packet, so this is roomy on purpose.
	maxControlPacketSize = 4096
	// maxControlDatagrams bounds how many datagrams one reply may be read from, which
	// nothing in the protocol bounds by itself. It is ntpq's own bound: twice its
	// MAXFRAGS of 32, because a datagram that turns out not to belong to this reply is
	// discarded and still costs a read ("Discarding various invalid packets can cause us
	// to loop more than MAXFRAGS times, but enforce a sane bound on how long we're
	// willing to spend here" -- ntp/packet.py).
	maxControlDatagrams = 2 * 32
	// maxControlReplySize bounds the reassembled reply. ntpd sends at most 468 bytes of
	// data per fragment, so ntpq's 32 of them come to 14976 bytes; the round number above
	// that also keeps the reassembled Count within the uint16 it is stored in.
	maxControlReplySize = 16 * 1024
)

var (
	errNoPeer         = errors.New("ntpd reported no peer")
	errMalformedReply = errors.New("malformed control reply from ntpd")
	errRequestRefused = errors.New("ntpd refused the request, check its restrict lines")
)

// New initialise ntp.Input, which reads ntpd's peers over its control protocol (NTP
// mode 6, RFC1305 appendix B) on the given "host:port", or on 127.0.0.1:123 when address
// is empty.
//
// The protocol is spoken directly, in Go, rather than through telegraf's ntpq plugin
// which shells out to the ntpq CLI. That plugin can query a remote daemon (its Servers
// option becomes ntpq's host argument), but it needs the tool installed next to Glouton,
// and in ntpsec ntpq is a *Python* program: pulling it into the agent image costs a
// Python runtime (Alpine's ntpsec package brings 28 packages and ~48 MiB for a 72 KB
// script). An ntpd in a container of its own is the ordinary case here, so the query has
// to work without that.
//
// What the daemon answers is unchanged either way, and remains the limiting factor: mode
// 6 is only served to whoever the target's own "restrict" lines allow, which by default
// (restrict default ... noquery, with 127.0.0.1 and ::1 unrestricted) is the local host
// only. That is why an address is only used for a daemon Glouton's loopback cannot be --
// see discovery's ntpdAddress.
func New(address string) (telegraf.Input, registry.RegistrationOption, error) {
	if address == "" {
		address = net.JoinHostPort("127.0.0.1", strconv.Itoa(ntpDefaultPort))
	}

	internalInput := &internal.Input{
		Input: &controlInput{address: address},
		Accumulator: internal.Accumulator{
			RenameGlobal:     renameGlobal,
			TransformMetrics: transformMetrics,
		},
		Name: "NTP",
	}

	// Registered with its own options rather than the default compatibility naming,
	// which keeps only the item: there is one point per peer, and the peer has to be
	// part of the series identity or they all collapse into one. With labels kept it
	// can be a label of its own (see renameGlobal) instead of being concatenated into
	// the item behind the container name.
	//
	// The interval is well above the 10 s default because reading the peers costs one
	// request per peer, the way "ntpq -p" does, and ntpd rate-limits per source address
	// by default ("restrict default ... limited", wanting 8 s between packets on
	// average). At 10 s a daemon with a dozen peers throttles us -- and answers the NTP
	// check coming from the same address with a Kiss-o'-Death instead, reporting a
	// healthy server as unsynchronized. Nothing here changes faster than the daemon's
	// poll interval (64 s to 1024 s) anyway.
	options := registry.RegistrationOption{ //nolint:exhaustruct
		MinInterval: time.Minute,
	}

	return internalInput, options, nil
}

// controlInput gathers one metric set per ntpd peer, with the measurement, tag and field
// names telegraf's ntpq plugin used, so what reaches the API is the same as before.
type controlInput struct {
	address string
}

func (ci *controlInput) SampleConfig() string {
	return ""
}

func (ci *controlInput) Gather(acc telegraf.Accumulator) error {
	// Gather takes no context, so the deadline is this input's own.
	ctx, cancel := context.WithTimeout(context.Background(), gatherTimeout)
	defer cancel()

	dialer := net.Dialer{Timeout: dialTimeout} //nolint:exhaustruct

	conn, err := dialer.DialContext(ctx, "udp", ci.address)
	if err != nil {
		return fmt.Errorf("ntpd control protocol on %s: %w", ci.address, err)
	}

	defer conn.Close()

	// Every read below is on this one connection, so a single deadline bounds the whole
	// gather -- there is no per-request timeout to set, and the client is synchronous.
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return err
		}
	}

	sequence := uint16(0)

	status, err := exchange(conn, readPacket(control.OpReadStatus, 0), &sequence)
	if err != nil {
		return fmt.Errorf("read status from ntpd on %s: %w", ci.address, err)
	}

	// The association IDs are the peer list: one further request each reads that peer's
	// variables, the same two-step ntpq -p does.
	associations, err := status.GetAssociations()
	if err != nil {
		return fmt.Errorf("peer list from ntpd on %s: %w", ci.address, err)
	}

	// Association 0 is the daemon itself rather than a peer: its variables are what ntpd
	// concluded about the local clock, including the offset it is actually correcting for.
	// That is the number chrony reports as chrony_last_offset, and without it an ntpd host
	// has only per-peer offsets and no answer to "how far off is this clock".
	system, err := exchange(conn, readPacket(control.OpReadVariables, 0), &sequence)
	if err != nil {
		acc.AddError(fmt.Errorf("read system variables from ntpd on %s: %w", ci.address, err))
	} else if systemVariables, err := system.GetAssociationInfo(); err != nil {
		acc.AddError(fmt.Errorf("system variables from ntpd on %s: %w", ci.address, err))
	} else {
		addSystemFields(acc, systemVariables)
	}

	gathered := 0

	for associationID := range associations {
		peer, err := exchange(conn, readPacket(control.OpReadVariables, associationID), &sequence)
		if err != nil {
			// One peer whose reply was dropped shouldn't cost us the others -- a daemon
			// rate-limiting us drops individual replies. Once the deadline is gone
			// though, every remaining read fails instantly, so there is nothing left to
			// try: stop and let what was gathered stand.
			acc.AddError(fmt.Errorf("read variables of peer %#x from ntpd on %s: %w", associationID, ci.address, err))

			if errors.Is(err, os.ErrDeadlineExceeded) {
				break
			}

			continue
		}

		peerVariables, err := peer.GetAssociationInfo()
		if err != nil {
			acc.AddError(fmt.Errorf("variables of peer %#x from ntpd on %s: %w", associationID, ci.address, err))

			continue
		}

		if addPeerFields(acc, peerVariables) {
			gathered++
		}
	}

	if gathered == 0 {
		// Reporting nothing without an error would look like a healthy daemon with no
		// peers, which ntpd can't be: it always has at least the ones it is configured
		// with. This is what a daemon that just started, or one whose peers are all
		// placeholders, looks like. A daemon that refused the request doesn't reach here
		// -- exchange says so instead, which is the more precise answer.
		return errNoPeer
	}

	return nil
}

// exchange sends one control request and returns the reply, bumping the sequence number
// the reply is expected to carry back.
//
// The reply is read here rather than through control.NTPClient because that client
// believes the reply's own Count field over the number of bytes it actually read: it
// reads into a 1024-byte buffer, discards the read length, and then slices
// buffer[12:12+Count] (ntp/control/client.go). A datagram claiming more data than it
// carries makes that panic on a slice bound -- and a panic inside a gather takes the whole
// agent down, since crashreport.ProcessPanic re-panics once it has reported. Whoever we
// are pointed at gets to send that datagram, so its header cannot be taken on trust.
//
// That client also takes none of the other precautions ntpq takes, which real replies turn
// out to need: measured against the ntpsec 1.2.2 in the test container, every peer's
// variables come back as two fragments (468 bytes at offset 0, then 201 at offset 468), so
// reassembling a reply is the normal path and not an exotic one. The checks here are the
// ones ntpq's own mode-6 client makes in __validate_packet and its fragment collection
// loop (ntp/packet.py), for the reasons its comments give.
func exchange(conn net.Conn, request *control.NTPControlMsgHead, sequence *uint16) (*control.NTPControlMsg, error) {
	request.Sequence = *sequence
	*sequence++

	var out bytes.Buffer

	if err := binary.Write(&out, binary.BigEndian, request); err != nil {
		return nil, err
	}

	if _, err := conn.Write(out.Bytes()); err != nil {
		return nil, err
	}

	var (
		fragments []replyFragment
		lastHead  control.NTPControlMsgHead
		seenLast  bool
	)

	for range maxControlDatagrams {
		buffer := make([]byte, maxControlPacketSize)

		n, err := conn.Read(buffer)
		if err != nil {
			return nil, err
		}

		if n < controlHeaderSize {
			continue
		}

		var head control.NTPControlMsgHead

		if err := binary.Read(bytes.NewReader(buffer[:controlHeaderSize]), binary.BigEndian, &head); err != nil {
			return nil, err
		}

		if !isReplyTo(head, request) {
			continue
		}

		if head.HasError() {
			// The daemon answered, and its answer is a refusal -- an unsupported request,
			// or one its access policy rejects. Saying so beats the "no peer" this used to
			// become, which points at the daemon's peers instead of at its policy.
			return nil, fmt.Errorf("%w (operation %d)", errRequestRefused, head.GetOperation())
		}

		count := int(head.Count)
		if count > n-controlHeaderSize {
			return nil, fmt.Errorf("%w: header announces %d bytes of data, the datagram carries %d",
				errMalformedReply, count, n-controlHeaderSize)
		}

		if int(head.Offset)+count > maxControlReplySize {
			return nil, fmt.Errorf("%w: a fragment ends past the %d bytes a reply may hold",
				errMalformedReply, maxControlReplySize)
		}

		fragment := replyFragment{offset: int(head.Offset), data: buffer[controlHeaderSize : controlHeaderSize+count]}

		// A fragment covering bytes another already covers is dropped rather than kept,
		// as ntpq drops it: a duplicate is something UDP is entitled to deliver, and
		// keeping it would leave the reply unassemblable for good.
		if overlapsHeldFragment(fragments, fragment) {
			continue
		}

		fragments = append(fragments, fragment)

		if !head.HasMore() {
			// The status of the reply as a whole is the last fragment's, which is the one
			// GetPeerStatus and GetSystemStatus read.
			seenLast = true
			lastHead = head
		}

		if !seenLast {
			continue
		}

		data, complete := assembleReply(fragments)
		if !complete {
			continue // a fragment in the middle is still on its way
		}

		// Count has to describe the reassembled data rather than stay the last
		// fragment's, or GetAssociations -- which walks Data by Count/4 -- would stop
		// after the associations the final fragment happened to carry.
		lastHead.Count = uint16(len(data)) //nolint:gosec // bounded by maxControlReplySize above

		return &control.NTPControlMsg{NTPControlMsgHead: lastHead, Data: data}, nil
	}

	return nil, fmt.Errorf("%w: no complete reply in %d datagrams", errMalformedReply, maxControlDatagrams)
}

// replyFragment is the data one datagram of a reply carries, kept with the offset that
// datagram claims for it inside the whole reply.
type replyFragment struct {
	offset int
	data   []byte
}

// isReplyTo reports whether a datagram is the reply to this request rather than a stray
// one: a late answer to an earlier request on the same socket, a duplicate, or something
// else that happened to arrive. Without this, a reply left queued by a request that
// errored out is read as the next peer's variables -- publishing one peer's numbers twice
// and losing another peer entirely.
//
// A mismatched association ID is deliberately not part of this. ntpq only warns about one
// instead of rejecting the datagram, and the sequence number already pins the datagram to
// a request that named a single association.
func isReplyTo(head control.NTPControlMsgHead, request *control.NTPControlMsgHead) bool {
	switch {
	case head.GetVersion() < 1 || head.GetVersion() > 4:
		return false
	case head.GetMode() != control.Mode:
		return false
	case !head.IsResponse():
		return false
	case head.Sequence != request.Sequence:
		return false
	case head.GetOperation() != request.GetOperation():
		return false
	default:
		return true
	}
}

// assembleReply puts a reply's fragments back together in the order their offsets say,
// which is not necessarily the order they arrived in -- UDP is free to reorder them, and
// a reply needing several datagrams is the common case here. It reports false while the
// reply still has a hole in it, meaning a fragment is yet to arrive.
//
// Requiring each fragment to start exactly where the previous one ended is ntpq's test,
// and it covers the first fragment being missing (nothing starts at 0) as well as any gap.
func assembleReply(fragments []replyFragment) ([]byte, bool) {
	sorted := slices.SortedFunc(slices.Values(fragments), func(a, b replyFragment) int {
		return a.offset - b.offset
	})

	data := make([]byte, 0, maxControlPacketSize)

	for _, fragment := range sorted {
		if fragment.offset != len(data) {
			return nil, false
		}

		data = append(data, fragment.data...)
	}

	return data, true
}

// overlapsHeldFragment reports whether any fragment already held covers a byte the new one
// also covers.
func overlapsHeldFragment(held []replyFragment, fragment replyFragment) bool {
	return slices.ContainsFunc(held, func(other replyFragment) bool {
		return other.offset < fragment.offset+len(fragment.data) &&
			fragment.offset < other.offset+len(other.data)
	})
}

// readPacket builds a control request. Version 3 is what the protocol defines (mode 6 was
// never versioned past NTPv3, and ntpd answers it whatever its own version) and what
// ntpq itself sends.
func readPacket(operation uint8, associationID uint16) *control.NTPControlMsgHead {
	return &control.NTPControlMsgHead{
		VnMode:        control.MakeVnMode(3, control.Mode),
		REMOp:         operation,
		AssociationID: associationID,
	}
}

// systemMeasurement holds the daemon's own view of the local clock, kept apart from the
// per-peer "ntpq" measurement: those points are identified by their peer, these have no
// peer at all, and sharing a measurement would make one look like the other with a missing
// label.
const systemMeasurement = "ntpq_system"

// addSystemFields reports what ntpd concluded about the local clock, from association 0.
//
// Only "offset" is read of the twenty variables association 0 answers with, because it is
// the only one published: the daemon's own estimate of how far off this clock is, which is
// what chrony reports as chrony_last_offset. The others (sys_jitter, clk_wander, stratum,
// leap, rootdisp, frequency, precision, tc, ...) were left out on purpose -- see
// PRODUCT-3300-ntp-metric-catalogue.md for what each holds, should one be wanted later.
func addSystemFields(acc telegraf.Accumulator, systemVariables map[string]string) {
	// Milliseconds, as everything ntpd reports; transformMetrics turns it into seconds.
	offset, err := strconv.ParseFloat(systemVariables["offset"], 64)
	if err != nil {
		return
	}

	acc.AddFields(systemMeasurement, map[string]any{"offset": offset}, nil)
}

// addPeerFields reports one peer, and whether it was one worth reporting.
func addPeerFields(acc telegraf.Accumulator, peerVariables map[string]string) bool {
	remote := peerVariables["srcadr"]

	// A pool line ntpd keeps as a placeholder for a source it hasn't picked yet has no
	// address and no measurement: it would only add a permanently-zero series. ntpq shows
	// those as the ".POOL." rows. The address it reports is the unspecified one, which is
	// "0.0.0.0" for an IPv4 pool and "::" for an IPv6 one -- both have to be recognized,
	// or the IPv6 placeholder becomes exactly the series this drops the other for.
	if ip := net.ParseIP(remote); remote == "" || ip == nil || ip.IsUnspecified() {
		return false
	}

	fields := make(map[string]any, 4)

	// delay and offset are the milliseconds ntpd reports; transformMetrics turns them into
	// seconds, as the ntpq plugin's own values were. jitter, stratum and unreach are the
	// other numbers a peer carries and are deliberately not read, being unpublished --
	// PRODUCT-3300-ntp-metric-catalogue.md says what each is.
	for _, name := range []string{"delay", "offset"} {
		value, err := strconv.ParseFloat(peerVariables[name], 64)
		if err != nil {
			continue
		}

		fields[name] = value
	}

	// flash is the bit field of the sanity checks this peer failed: zero means ntpd is
	// happy with it, and each bit is a reason it isn't (control.ReadFlashStatusWord names
	// them, e.g. 0x400 peer_dist). Always read as hexadecimal, which is how ntpq prints it
	// and what makes the bits line up with those names; ntpsec sends it prefixed ("0x0" on
	// the wire, checked against 1.2.2) and older implementations bare, so the prefix is
	// removed rather than relying on base detection -- a bare "400" is peer_dist, not 400.
	flash := strings.TrimPrefix(strings.TrimPrefix(peerVariables["flash"], "0x"), "0X")
	if value, err := strconv.ParseUint(flash, 16, 16); err == nil {
		fields["flash"] = float64(value)
	}

	if reach, ok := parseReach(peerVariables["reach"]); ok {
		// As a 0..1 ratio of the polls that were answered, which is what the ntpq
		// plugin's "ratio" reach format produced and what transformMetrics scales to a
		// percentage. The raw register is a bit field: as a number it reads as
		// nonsensically out of range (255, or 377 in the octal ntpq prints).
		fields["reach"] = float64(bits.OnesCount64(reach)) / maxPollsRemembered
	}

	if len(fields) == 0 {
		return false
	}

	acc.AddFields("ntpq", fields, map[string]string{remoteTag: remote})

	return true
}

// parseReach reads ntpd's "reach" peer variable: the shift register recording which of
// the last 8 polls the peer answered, one bit each.
//
// ntpsec sends it as hex with a 0x prefix ("0xff", checked against 1.2.2 over the wire),
// while ntpq's own display and older implementations use octal, so it is read with Go's
// base-detecting 0 first and re-read as octal when that doesn't fit the register -- a
// bare "377" is 255 in octal, and no NTP implementation would print 377 decimal, a value
// the register cannot hold. Bare digits that do fit stay ambiguous and are read as
// decimal, which is the best that can be done without a prefix to go on.
//
// The value is returned as a uint64 to be counted with bits.OnesCount64: only how many
// of its 8 bits are set is ever used, and that count is the same at any width.
func parseReach(value string) (uint64, bool) {
	reach, err := strconv.ParseUint(value, 0, 8)
	if err == nil {
		return reach, true
	}

	if errors.Is(err, strconv.ErrRange) {
		if reach, err = strconv.ParseUint(value, 8, 8); err == nil {
			return reach, true
		}
	}

	return 0, false
}

// renameGlobal moves the peer address to types.LabelPeerAddress, the label it shares with
// inputs/chrony, so both daemons' per-source metrics are read the same way.
//
// The item is deliberately left alone: it is the service instance (the container name),
// and putting the address there too gave items like "test-ntp_37.59.63.125" -- the address
// hidden behind a container name, in a label that is supposed to say which instance
// this is.
func renameGlobal(gatherContext internal.GatherContext) (internal.GatherContext, bool) {
	if remote := gatherContext.Tags[remoteTag]; remote != "" {
		delete(gatherContext.Tags, remoteTag)

		gatherContext.Tags[types.LabelPeerAddress] = remote
	}

	return gatherContext, false
}

// transformMetrics converts the durations ntpd reports in milliseconds into seconds,
// matching every other duration metric in this codebase, and renames each field so the unit
// is visible in the name. It also converts reach from the 0..1 ratio addPeerFields reports
// into a 0..100 percentage, matching every other percentage metric Glouton publishes.
//
// The two measurements carry different fields: the per-peer one reports the measurement
// towards that peer, the system one what ntpd concluded from all of them.
func transformMetrics(_ internal.GatherContext, fields map[string]float64, _ map[string]any) map[string]float64 {
	// Both measurements report their durations in milliseconds and name them the same way,
	// so one list covers the peer points (delay, offset) and the system one (offset).
	for _, name := range []string{"delay", "jitter", "offset"} {
		if value, ok := fields[name]; ok {
			delete(fields, name)

			fields[name+"_seconds"] = value / 1000
		}
	}

	if value, ok := fields["reach"]; ok {
		delete(fields, "reach")

		fields["reach_perc"] = value * 100
	}

	return fields
}
