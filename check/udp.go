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
// non-listening port looks identical to a listening one until data is actually
// exchanged. send is therefore required, not optional like TCP's NewTCP: a bare
// "can I open a UDP socket" check carries no information about reachability.
type UDPCheck struct {
	*baseCheck

	mainAddress string

	send   []byte
	expect []byte
}

// NewUDP create a new UDP check.
//
// All addresses use the format "IP:port". send is written once the socket is open,
// then glouton waits up to 10 seconds for any reply. If expect is non-empty, the
// reply's leading bytes must match it; otherwise any non-empty reply counts as OK --
// many UDP protocols validate a request and reply with an error rather than silently
// dropping malformed input, so a plain "got some response" is already a meaningful
// positive signal without the check needing to speak the target's protocol precisely.
//
// UDP has no persistent-connection concept the way TCP's baseCheck maintains one
// (there is no long-lived stream whose breaking can be detected the same way), so
// unlike NewTCP there is no secondary-addresses/persistentConnection parameter here.
func NewUDP(
	address string,
	send []byte,
	expect []byte,
	labels map[string]string,
	annotations types.MetricAnnotations,
	containerRuntime containerInfoProvider,
) *UDPCheck {
	uc := &UDPCheck{
		mainAddress: address,
		send:        send,
		expect:      expect,
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
	}{
		MainAddress: uc.mainAddress,
		Send:        hex.EncodeToString(uc.send),
		Expect:      hex.EncodeToString(uc.expect),
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
		// Nothing to send a packet to, and unlike a TCP check there are no secondary
		// addresses this could fall back to: reporting Ok would be reporting on a
		// check that never ran.
		return types.StatusDescription{
			CurrentStatus:     types.StatusUnknown,
			StatusDescription: "No UDP address to check",
		}
	}

	return checkUDP(ctx, uc.mainAddress, uc.send, uc.expect)
}

func checkUDP(ctx context.Context, address string, send []byte, expect []byte) types.StatusDescription {
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

	return types.StatusDescription{
		CurrentStatus:     types.StatusOk,
		StatusDescription: fmt.Sprintf("UDP OK - %v response time", time.Since(start)),
	}
}
