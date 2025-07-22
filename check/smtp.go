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
	"context"
	"fmt"
	"net"
	"net/smtp"
	"time"

	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/types"
)

// SMTPCheck perform a SMTP check.
type SMTPCheck struct {
	*baseCheck

	mainAddress string
}

// NewSMTP create a new SMTP check.
//
// All addresses use the format "IP:port".
//
// For each persistentAddresses this checker will maintain a TCP connection open, if broken (and unable to re-open), the check will
// be immediately run.
func NewSMTP(
	address string,
	persistentAddresses []string,
	persistentConnection bool,
	labels map[string]string,
	annotations types.MetricAnnotations,
) *SMTPCheck {
	sc := &SMTPCheck{
		mainAddress: address,
	}

	sc.baseCheck = newBase("", persistentAddresses, persistentConnection, sc.smtpMainCheck, labels, annotations)

	return sc
}

func (sc *SMTPCheck) smtpMainCheck(ctx context.Context) types.StatusDescription {
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
			StatusDescription: "Connection timed out after 10 seconds",
		}
	} else if err != nil {
		return types.StatusDescription{
			CurrentStatus:     types.StatusCritical,
			StatusDescription: "Unable to connect to SMTP server: connection refused",
		}
	}

	err = conn.SetDeadline(time.Now().Add(10 * time.Second))
	if err != nil {
		logger.V(1).Printf("Unable to set Deadline: %v", err)

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
