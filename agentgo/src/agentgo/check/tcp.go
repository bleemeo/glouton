package check

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"time"

	"agentgo/types"
)

// TCPCheck perform a TCP check
type TCPCheck struct {
	*baseCheck
	mainAddress    string
	otherAddresses []string
	checkForMain   func(ctx context.Context) types.StatusDescription

	send   []byte
	expect []byte
}

// NewTCP create a new TCP check.
//
// All addresses use the format "IP:port".
//
// If set, on the main address it will send specified byte and expect the specified byte.
//
// For each persitentAddresses this checker will maintain a TCP connection open, if broken (and unable to re-open), the check will
// be immediately run.
func NewTCP(address string, persitentAddresses []string, send []byte, expect []byte, metricName string, item string, acc accumulator) *TCPCheck {

	tc := &TCPCheck{
		mainAddress:    address,
		otherAddresses: persitentAddresses,
		send:           send,
		expect:         expect,
	}
	tc.baseCheck = newBase(persitentAddresses, metricName, item, tc.doCheck, acc)
	return tc
}

func (tc *TCPCheck) doCheck(ctx context.Context) types.StatusDescription {
	var result types.StatusDescription
	if tc.checkForMain != nil {
		if result = tc.checkForMain(ctx); result.CurrentStatus != types.StatusOk {
			return result
		}
	} else if tc.mainAddress != "" {
		if result = checkTCP(ctx, tc.mainAddress, tc.send, tc.expect); result.CurrentStatus != types.StatusOk {
			return result
		}
	}
	if result.CurrentStatus == types.StatusCritical || result.CurrentStatus == types.StatusUnknown {
		return result
	}
	for _, addr := range tc.otherAddresses {
		if addr == tc.mainAddress {
			continue
		}
		if subResult := checkTCP(ctx, addr, nil, nil); subResult.CurrentStatus != types.StatusOk {
			return subResult
		} else if !result.CurrentStatus.IsSet() {
			result = subResult
		}
	}
	if !result.CurrentStatus.IsSet() {
		return types.StatusDescription{
			CurrentStatus: types.StatusOk,
		}
	}
	return result
}

func checkTCP(ctx context.Context, address string, send []byte, expect []byte) types.StatusDescription {
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
		log.Printf("DBG: Unable to set Deadline: %v", err)
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
