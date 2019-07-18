package check

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/smtp"
	"time"

	"agentgo/types"
)

// SMTPCheck perform a SMTP check
type SMTPCheck struct {
	*baseCheck
	mainAddress string
}

// NewSMTP create a new SMTP check.
//
// All addresses use the format "IP:port".
//
// For each persitentAddresses this checker will maintain a TCP connection open, if broken (and unable to re-open), the check will
// be immediately run.
func NewSMTP(address string, persitentAddresses []string, metricName string, item string, acc accumulator) *SMTPCheck {

	sc := &SMTPCheck{
		mainAddress: address,
	}
	sc.baseCheck = newBase("", persitentAddresses, true, sc.doCheck, metricName, item, acc)
	return sc
}

func (sc *SMTPCheck) doCheck(ctx context.Context) types.StatusDescription {
	if sc.mainAddress == "" {
		return types.StatusDescription{
			CurrentStatus: types.StatusOk,
		}
	}
	host, _, err := net.SplitHostPort(sc.mainAddress)
	if err != nil {
		return types.StatusDescription{
			CurrentStatus:     types.StatusUnknown,
			StatusDescription: fmt.Sprintf("Invalid TCP address %#v", sc.mainAddress),
		}
	}
	start := time.Now()
	ctx2, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx2, "tcp", sc.mainAddress)
	if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
		return types.StatusDescription{
			CurrentStatus:     types.StatusCritical,
			StatusDescription: fmt.Sprintf("Connection timed out after 10 seconds"),
		}
	} else if err != nil {
		return types.StatusDescription{
			CurrentStatus:     types.StatusCritical,
			StatusDescription: fmt.Sprintf("Unable to connect to SMTP server: connection refused"),
		}
	}
	err = conn.SetDeadline(time.Now().Add(10 * time.Second))
	if err != nil {
		log.Printf("DBG: Unable to set Deadline: %v", err)
		return types.StatusDescription{
			CurrentStatus:     types.StatusUnknown,
			StatusDescription: "Checker error. Unable to set Deadline",
		}
	}
	cl, err := smtp.NewClient(conn, host)
	if err != nil {
		return types.StatusDescription{
			CurrentStatus:     types.StatusCritical,
			StatusDescription: fmt.Sprintf("Unable to connect to SMTP server: %v", err),
		}
	}
	err = cl.Noop()
	if err != nil {
		return types.StatusDescription{
			CurrentStatus:     types.StatusCritical,
			StatusDescription: fmt.Sprintf("SMTP error: %v", err),
		}
	}
	err = cl.Quit()
	if err != nil {
		return types.StatusDescription{
			CurrentStatus:     types.StatusCritical,
			StatusDescription: fmt.Sprintf("SMTP error: %v", err),
		}
	}

	return types.StatusDescription{
		CurrentStatus:     types.StatusOk,
		StatusDescription: fmt.Sprintf("SMTP OK - %v response time", time.Since(start)),
	}
}
