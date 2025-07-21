// Copyright 2015-2025 Bleemeo
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
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"net"
	"time"

	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/types"
)

// NTPCheck perform a NTP check.
type NTPCheck struct {
	*baseCheck

	mainAddress string
}

// NewNTP create a new NTP check.
//
// All addresses use the format "IP:port".
//
// For each persistentAddresses this checker will maintain a TCP connection open, if broken (and unable to re-open), the check will
// be immediately run.
func NewNTP(
	address string,
	persistentAddresses []string,
	persistentConnection bool,
	labels map[string]string,
	annotations types.MetricAnnotations,
) *NTPCheck {
	nc := &NTPCheck{
		mainAddress: address,
	}

	nc.baseCheck = newBase("", persistentAddresses, persistentConnection, nc.ntpMainCheck, labels, annotations)

	return nc
}

type ntpTimestamp struct {
	Second  uint32
	Faction uint32
}

func (nt ntpTimestamp) Time() time.Time {
	// NTP timestamp use a number of second since 1 January 1900
	// Unix timestamp use 1970
	deltaEpoc := uint32(2208988800)

	// NTP faction is a number of 2*-32 seconds (that is 232 picoseconds)
	nanoFaction := int64(nt.Faction) / 1000 * 232

	return time.Unix(int64(nt.Second-deltaEpoc), nanoFaction)
}

type ntpV3Packet struct {
	LeapVersionMode uint8 // 2 bits (leap indicator) 3 bits (version) + 3 bits (mode)
	Stratum         uint8
	Poll            int8
	Precision       int8
	RootDelay       int32 // 2 bits are used as faction. E.g. divide by 4 to get a number of seconds
	RootDispersion  int32 // 2 bits are used as faction. E.g. divide by 4 to get a number of seconds
	ReferenceID     [4]byte
	ReferenceTS     ntpTimestamp
	OriginateTS     ntpTimestamp
	ReceiveTS       ntpTimestamp
	Transmit        ntpTimestamp
}

func encodeLeapVersionMode(leapIndicator int, version int, mode int) uint8 {
	// leapIndicator is
	// 0: No leap second adjustment
	// 1: Last minute of the day has 61 seconds
	// 2: Last minute of the day has 59 seconds
	// 3: Clock is unsynchronized
	// Version is 0 to 7
	// Mode is:
	// 0: Reserved
	// 1: Symmetric active
	// 2: Symmetric passive
	// 3: Client
	// 4: Server
	// 5: Broadcast
	// 6: NTP control message
	// 7: Reserved for private use
	return uint8(leapIndicator*64 + version*8 + mode) //nolint:gosec
}

func decodeLeapVersionMode(value uint8) (leapIndicator int, version int, mode int) {
	leapIndicator = int(value / 64)
	version = int((value / 8) & 0x7)
	mode = int(value & 0x7)

	return
}

func (nc *NTPCheck) ntpMainCheck(context.Context) types.StatusDescription {
	if nc.mainAddress == "" {
		return types.StatusDescription{
			CurrentStatus: types.StatusOk,
		}
	}

	start := time.Now()

	conn, err := net.ListenPacket("udp", ":0")
	if err != nil {
		logger.V(1).Printf("Unable to create UDP socket: %v", err)

		return types.StatusDescription{
			CurrentStatus:     types.StatusUnknown,
			StatusDescription: "Checker error. Unable to create UDP socket",
		}
	}

	defer conn.Close()

	err = conn.SetDeadline(time.Now().Add(10 * time.Second))
	if err != nil {
		logger.V(1).Printf("Unable to set Deadline: %v", err)

		return types.StatusDescription{
			CurrentStatus:     types.StatusUnknown,
			StatusDescription: "Checker error. Unable to set Deadline",
		}
	}

	dst, err := net.ResolveUDPAddr("udp", nc.mainAddress)
	if err != nil {
		logger.V(1).Printf("Unable to resolve UDP address: %v", err)

		return types.StatusDescription{
			CurrentStatus:     types.StatusCritical,
			StatusDescription: fmt.Sprintf("Unable to resolve address %#v", nc.mainAddress),
		}
	}

	buf := new(bytes.Buffer)
	packet := ntpV3Packet{
		LeapVersionMode: encodeLeapVersionMode(0, 3, 3),
	}

	err = binary.Write(buf, binary.BigEndian, packet)
	if err != nil {
		logger.V(1).Printf("Unable to encode NTP packet: %v", err)

		return types.StatusDescription{
			CurrentStatus:     types.StatusUnknown,
			StatusDescription: "Checker error. Unable to encode NTP packet",
		}
	}

	_, err = conn.WriteTo(buf.Bytes(), dst)
	if err != nil {
		logger.V(1).Printf("ntp check, failed to send data: %v", err)
	}

	data := make([]byte, 48)

	n, _, err := conn.ReadFrom(data)
	if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
		return types.StatusDescription{
			CurrentStatus:     types.StatusCritical,
			StatusDescription: "Connection timed out after 10 seconds",
		}
	}

	if err != nil || n != len(data) {
		return types.StatusDescription{
			CurrentStatus:     types.StatusCritical,
			StatusDescription: "No data received from server",
		}
	}

	err = binary.Read(bytes.NewReader(data), binary.BigEndian, &packet)
	if err != nil {
		logger.V(1).Printf("NTP packet format unknown: %v", err)

		return types.StatusDescription{
			CurrentStatus:     types.StatusUnknown,
			StatusDescription: "Unknown response from NTP server",
		}
	}

	if packet.Stratum == 0 || packet.Stratum == 16 {
		return types.StatusDescription{
			CurrentStatus:     types.StatusCritical,
			StatusDescription: "NTP server not (yet) synchronized",
		}
	}

	if math.Abs(time.Since(packet.ReceiveTS.Time()).Seconds()) > 10 {
		return types.StatusDescription{
			CurrentStatus:     types.StatusCritical,
			StatusDescription: "Local time and NTP time does not match",
		}
	}

	return types.StatusDescription{
		CurrentStatus:     types.StatusOk,
		StatusDescription: fmt.Sprintf("NTP OK - %v response time", time.Since(start)),
	}
}
