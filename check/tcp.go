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
	"fmt"
	"io"
	"net"
	"strconv"
	"time"

	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/types"
)

// TCPCheck perform a TCP check.
type TCPCheck struct {
	*baseCheck

	mainAddress string

	send     []byte
	expect   []byte
	closeMsg []byte
}

// NewTCP create a new TCP check.
//
// All addresses use the format "IP:port".
//
// If set, on the main address it will send specified byte and expect the specified byte.
//
// On tcpAddresses (which are supposed to contains addresse) a TCP connection is openned and closed on each check.
//
// If persistentConnection is set, a persistent TCP connection will be openned to detect service incident quickyl.
func NewTCP(
	address string,
	tcpAddresses []string,
	persistentConnection bool,
	send []byte,
	expect []byte,
	closeMsg []byte,
	labels map[string]string,
	annotations types.MetricAnnotations,
) *TCPCheck {
	tc := &TCPCheck{
		mainAddress: address,
		send:        send,
		expect:      expect,
		closeMsg:    closeMsg,
	}
	mainCheck := tc.tcpMainCheck

	if address == "" {
		mainCheck = nil
	}

	tc.baseCheck = newBase(address, tcpAddresses, persistentConnection, mainCheck, labels, annotations)

	return tc
}

func (tc *TCPCheck) tcpMainCheck(ctx context.Context) types.StatusDescription {
	if tc.mainAddress == "" {
		return types.StatusDescription{}
	}

	return checkTCP(ctx, tc.mainAddress, tc.send, tc.expect, tc.closeMsg)
}

func checkTCP(ctx context.Context, address string, send []byte, expect []byte, closeMsg []byte) types.StatusDescription {
	_, portStr, err := net.SplitHostPort(address)
	if err != nil {
		return types.StatusDescription{
			CurrentStatus:     types.StatusUnknown,
			StatusDescription: fmt.Sprintf("Invalid TCP address %#v", address),
		}
	}

	port, err := strconv.ParseInt(portStr, 10, 0)
	if err != nil {
		return types.StatusDescription{
			CurrentStatus:     types.StatusUnknown,
			StatusDescription: fmt.Sprintf("Invalid TCP port %#v", portStr),
		}
	}

	start := time.Now()

	ctx2, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	var dialer net.Dialer

	conn, err := dialer.DialContext(ctx2, "tcp", address)
	if err != nil {
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			return types.StatusDescription{
				CurrentStatus:     types.StatusCritical,
				StatusDescription: fmt.Sprintf("TCP port %d, connection timed out after 10 seconds", port),
			}
		}

		return types.StatusDescription{
			CurrentStatus:     types.StatusCritical,
			StatusDescription: fmt.Sprintf("TCP port %d, Connection refused", port),
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

	if len(send) > 0 {
		n, err := conn.Write(send)
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			return types.StatusDescription{
				CurrentStatus:     types.StatusCritical,
				StatusDescription: fmt.Sprintf("TCP port %d, connection timed out after 10 seconds", port),
			}
		}

		if err != nil || n != len(send) {
			return types.StatusDescription{
				CurrentStatus:     types.StatusCritical,
				StatusDescription: fmt.Sprintf("TCP port %d, connection closed too early", port),
			}
		}
	}

	if len(expect) > 0 {
		firstBytes, found, err := readUntilPatternFound(conn, expect)
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() && len(firstBytes) == 0 {
			return types.StatusDescription{
				CurrentStatus:     types.StatusCritical,
				StatusDescription: fmt.Sprintf("TCP port %d, connection timed out after 10 seconds", port),
			}
		} else if err != nil && (!ok || !netErr.Timeout()) {
			return types.StatusDescription{
				CurrentStatus:     types.StatusCritical,
				StatusDescription: fmt.Sprintf("TCP port %d, connection closed", port),
			}
		}

		if !found {
			if len(firstBytes) == 0 {
				return types.StatusDescription{
					CurrentStatus:     types.StatusCritical,
					StatusDescription: fmt.Sprintf("TCP port %d, no data received from host", port),
				}
			}

			return types.StatusDescription{
				CurrentStatus:     types.StatusCritical,
				StatusDescription: fmt.Sprintf("TCP port %d, unexpected response %#v", port, string(firstBytes)),
			}
		}
	}

	if len(closeMsg) > 0 {
		// Write the close message, but ignore any errors
		_, _ = conn.Write(closeMsg)
		readBuffer := make([]byte, 4096)

		err = conn.SetDeadline(time.Now().Add(1 * time.Second))
		if err != nil {
			logger.V(1).Printf("Unable to set Deadline: %v", err)

			return types.StatusDescription{
				CurrentStatus:     types.StatusUnknown,
				StatusDescription: "Checker error. Unable to set Deadline",
			}
		}
		// Give a 1 second delay for the server to close the connection
		_, _ = conn.Read(readBuffer)
	}

	return types.StatusDescription{
		CurrentStatus:     types.StatusOk,
		StatusDescription: fmt.Sprintf("TCP OK - %v response time", time.Since(start)),
	}
}

func readUntilPatternFound(conn io.Reader, expect []byte) (firstBytes []byte, found bool, err error) {
	// The following assume expect is less that 4096 bytes
	readBuffer := make([]byte, 4096)
	workBuffer := make([]byte, 0, 8192)

	for {
		n, err := conn.Read(readBuffer)
		if n > 0 {
			if cap(workBuffer) < len(workBuffer)+n {
				copy(workBuffer[:cap(workBuffer)-n], workBuffer[n:])
				workBuffer = workBuffer[:cap(workBuffer)-n]
			}

			workBuffer = append(workBuffer, readBuffer[:n]...)

			if len(firstBytes) < 100 && len(workBuffer) > len(firstBytes) {
				if len(workBuffer) > 50 {
					firstBytes = workBuffer[:50]
				} else {
					firstBytes = workBuffer
				}
			}

			if bytes.Contains(workBuffer, expect) {
				return firstBytes, true, nil
			}
		}

		if err != nil && err == io.EOF {
			return firstBytes, false, nil
		}

		if err != nil {
			return firstBytes, false, err
		}
	}
}
