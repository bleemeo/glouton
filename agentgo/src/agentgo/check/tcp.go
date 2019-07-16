package check

import (
	"context"
	"fmt"
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
}

// NewTCP ...
func NewTCP(address string, otherAddresses []string, metricName string, item string, acc accumulator) *TCPCheck {

	tc := &TCPCheck{
		mainAddress:    address,
		otherAddresses: otherAddresses,
	}
	addresses := make([]string, len(otherAddresses), len(otherAddresses)+1)
	copy(addresses, otherAddresses)
	addresses = append(addresses, address)
	tc.baseCheck = newBase(addresses, metricName, item, tc.doCheck, acc)
	return tc
}

func (tc *TCPCheck) doCheck(ctx context.Context) types.StatusDescription {
	var result types.StatusDescription
	if tc.checkForMain != nil {
		if result = tc.checkForMain(ctx); result.CurrentStatus != types.StatusOk {
			return result
		}
	} else {
		if result = checkTCP(ctx, tc.mainAddress); result.CurrentStatus != types.StatusOk {
			return result
		}
	}
	for _, addr := range tc.otherAddresses {
		if subResult := checkTCP(ctx, addr); subResult.CurrentStatus != types.StatusOk {
			return subResult
		}
	}
	return result
}

func checkTCP(ctx context.Context, address string) types.StatusDescription {
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

	return types.StatusDescription{
		CurrentStatus:     types.StatusOk,
		StatusDescription: fmt.Sprintf("TCP OK - %v response time", time.Since(start)),
	}
}
