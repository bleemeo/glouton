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
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/types"
)

// udpCheckTimeout is how long the check waits for a reply, matching the TCP and NTP
// checks' own timeout.
const udpCheckTimeout = 10 * time.Second

// UDPCheck perform a UDP check.
//
// Unlike TCP, dialing UDP never fails on its own -- there is no handshake, so a
// non-listening port looks identical to a listening one until data is actually exchanged.
// send is therefore required: a bare "can I open a UDP socket" check carries no information
// about reachability.
type UDPCheck struct {
	*baseCheck

	mainAddress string

	send     []byte
	expect   []byte
	validate func(reply []byte) error
}

// NewUDP create a new UDP check.
//
// All addresses use the format "IP:port". send is written once the socket is open,
// then glouton waits up to 10 seconds for a reply.
//
// What counts as a good reply is up to the caller. expect, when non-empty, requires the
// reply to start with those bytes. validate, when non-nil, is given the whole reply and says
// what is wrong with it -- for a protocol that answers a request it refuses instead of
// dropping it. With neither, any non-empty reply counts as OK.
//
// There is no persistent connection to maintain over UDP, so no secondary addresses are
// taken.
func NewUDP(
	address string,
	send []byte,
	expect []byte,
	validate func(reply []byte) error,
	labels map[string]string,
	annotations types.MetricAnnotations,
	containerRuntime containerInfoProvider,
) *UDPCheck {
	uc := &UDPCheck{
		mainAddress: address,
		send:        send,
		expect:      expect,
		validate:    validate,
	}

	uc.baseCheck = newBase("", nil, false, uc.udpMainCheck, labels, annotations, containerRuntime)

	return uc
}

// DiagnosticArchive add the address probed to the diagnostic, which baseCheck's version
// can't know: it only records TCP addresses, and this check has none.
func (uc *UDPCheck) DiagnosticArchive(ctx context.Context, archive types.ArchiveWriter) error {
	file, err := archive.Create("check-udp.json")
	if err != nil {
		return err
	}

	obj := struct {
		MainAddress string
		Send        string
		Expect      string
		Validated   bool
	}{
		MainAddress: uc.mainAddress,
		Send:        hex.EncodeToString(uc.send),
		Expect:      hex.EncodeToString(uc.expect),
		Validated:   uc.validate != nil,
	}

	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")

	if err := enc.Encode(obj); err != nil {
		return err
	}

	return uc.baseCheck.DiagnosticArchive(ctx, archive)
}

func (uc *UDPCheck) udpMainCheck(ctx context.Context) types.StatusDescription {
	if uc.mainAddress == "" {
		// Nothing to send a packet to, and no secondary address to fall back to: reporting
		// Ok would be reporting on a check that never ran.
		return types.StatusDescription{
			CurrentStatus:     types.StatusUnknown,
			StatusDescription: "No UDP address to check",
		}
	}

	return checkUDP(ctx, uc.mainAddress, uc.send, uc.expect, uc.validate)
}

func checkUDP(ctx context.Context, address string, send []byte, expect []byte, validate func(reply []byte) error) types.StatusDescription {
	_, portStr, err := net.SplitHostPort(address)
	if err != nil {
		return types.StatusDescription{
			CurrentStatus:     types.StatusUnknown,
			StatusDescription: fmt.Sprintf("Invalid UDP address %#v", address),
		}
	}

	port, err := strconv.ParseInt(portStr, 10, 0)
	if err != nil {
		return types.StatusDescription{
			CurrentStatus:     types.StatusUnknown,
			StatusDescription: fmt.Sprintf("Invalid UDP port %#v", portStr),
		}
	}

	start := time.Now()

	ctx2, cancel := context.WithTimeout(ctx, udpCheckTimeout)
	defer cancel()

	var dialer net.Dialer

	conn, err := dialer.DialContext(ctx2, "udp", address)
	if err != nil {
		return types.StatusDescription{
			CurrentStatus:     types.StatusCritical,
			StatusDescription: fmt.Sprintf("UDP port %d, unable to open socket: %v", port, err),
		}
	}

	defer conn.Close()

	// The socket deadline is the context's, not another 10 seconds of its own: the read
	// below is where all the waiting happens, and a caller that cancels or has less time
	// than udpCheckTimeout must not be made to wait for it.
	deadline, _ := ctx2.Deadline()

	err = conn.SetDeadline(deadline)
	if err != nil {
		logger.V(1).Printf("Unable to set Deadline: %v", err)

		return types.StatusDescription{
			CurrentStatus:     types.StatusUnknown,
			StatusDescription: statusDeadlineError,
		}
	}

	if len(send) > 0 {
		_, err = conn.Write(send)
		if err != nil {
			return types.StatusDescription{
				CurrentStatus:     types.StatusCritical,
				StatusDescription: fmt.Sprintf("UDP port %d, failed to send data: %v", port, err),
			}
		}
	}

	buffer := make([]byte, 4096)

	n, err := conn.Read(buffer)
	if netErr, ok := err.(net.Error); ok && netErr.Timeout() { //nolint:errorlint
		return types.StatusDescription{
			CurrentStatus:     types.StatusCritical,
			StatusDescription: fmt.Sprintf("UDP port %d, %s", port, statusConnectionTimedOut),
		}
	}

	if err != nil {
		return types.StatusDescription{
			CurrentStatus:     types.StatusCritical,
			StatusDescription: fmt.Sprintf("UDP port %d, %v", port, err),
		}
	}

	if n == 0 {
		// A zero-length datagram carries no information about the service behind the
		// port, so it isn't the reply the check is waiting for.
		return types.StatusDescription{
			CurrentStatus:     types.StatusCritical,
			StatusDescription: fmt.Sprintf("UDP port %d, empty response", port),
		}
	}

	if len(expect) > 0 && (n < len(expect) || string(buffer[:len(expect)]) != string(expect)) {
		return types.StatusDescription{
			CurrentStatus:     types.StatusCritical,
			StatusDescription: fmt.Sprintf("UDP port %d, unexpected response %#v", port, string(buffer[:n])),
		}
	}

	if validate != nil {
		if err := validate(buffer[:n]); err != nil {
			return types.StatusDescription{
				CurrentStatus:     types.StatusCritical,
				StatusDescription: fmt.Sprintf("UDP port %d, %v", port, err),
			}
		}
	}

	return types.StatusDescription{
		CurrentStatus:     types.StatusOk,
		StatusDescription: fmt.Sprintf("UDP OK - %v response time", time.Since(start)),
	}
}
