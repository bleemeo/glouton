// Copyright 2015-2019 Bleemeo
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
	"net"
	"sync"
	"time"

	"agentgo/logger"
	"agentgo/types"
)

type accumulator interface {
	AddFieldsWithStatus(measurement string, fields map[string]interface{}, tags map[string]string, statuses map[string]types.StatusDescription, createStatusOf bool, t ...time.Time)
}

// baseCheck perform a service.
//
// The check does:
// * use mainCheck to perform the primary check (protocol specific)
// * open & close a TCP connection on all tcpAddresses (with exclusion of mainTCPAddress if set)
//
// If persistentConnection is active, when check successed, this checker will maintain a TCP connection
// to each tcpAddresses + the mainTCPAddress to detect service failture quickly.
//
// The check is run at the first of:
// * One minute after last check
// * 30 seconds after checks change to not Ok (to quickly recover from a service restart)
// * (if persistentConnection is active) after a TCP connection is broken
type baseCheck struct {
	metricName     string
	labels         map[string]string
	mainTCPAddress string
	tcpAddresses   []string
	mainCheck      func(ctx context.Context) types.StatusDescription
	acc            accumulator

	timer    *time.Timer
	dialer   *net.Dialer
	triggerC chan interface{}
	cancel   func()
	wg       sync.WaitGroup

	persistentConnection bool
}

func newBase(mainTCPAddress string, tcpAddresses []string, persistentConnection bool, mainCheck func(context.Context) types.StatusDescription, metricName string, labels map[string]string, acc accumulator) *baseCheck {
	if mainTCPAddress != "" {
		found := false
		for _, v := range tcpAddresses {
			if v == mainTCPAddress {
				found = true
				break
			}
		}
		if !found {
			tmp := make([]string, 0, len(tcpAddresses)+1)
			tmp = append(tmp, mainTCPAddress)
			tmp = append(tmp, tcpAddresses...)
			tcpAddresses = tmp
		}
	}

	return &baseCheck{
		metricName:           metricName,
		labels:               labels,
		mainTCPAddress:       mainTCPAddress,
		tcpAddresses:         tcpAddresses,
		persistentConnection: persistentConnection,
		mainCheck:            mainCheck,
		acc:                  acc,

		dialer:   &net.Dialer{},
		timer:    time.NewTimer(0),
		triggerC: make(chan interface{}),
	}
}

// Run execute the TCP check
func (bc *baseCheck) Run(ctx context.Context) error {
	// Open connectionS to address
	// when openned, keep checking that port stay open
	// when port goes from open to close, back to step 1
	// If step 1 fail => trigger check
	// trigger check every minutes (or 30 seconds)
	result := types.StatusDescription{
		CurrentStatus:     types.StatusOk,
		StatusDescription: "initial status - description is ignored",
	}
	for {
		select {
		case <-ctx.Done():
			if bc.cancel != nil {
				bc.cancel()
				bc.cancel = nil
			}
			bc.wg.Wait()
			return nil
		case <-bc.triggerC:
			if !bc.timer.Stop() {
				<-bc.timer.C
			}
			result = bc.check(ctx, result)
		case <-bc.timer.C:
			result = bc.check(ctx, result)
		}
	}
}

func (bc *baseCheck) check(ctx context.Context, previousStatus types.StatusDescription) types.StatusDescription {
	// do the check
	// if successful, ensure socket are open
	// if fail, ensure socket are closed
	// if just fail (ok -> critical), do a fast check
	result := bc.doCheck(ctx)
	if ctx.Err() != nil {
		return previousStatus
	}
	timerDone := false
	if result.CurrentStatus != types.StatusOk {
		if bc.cancel != nil {
			bc.cancel()
			bc.wg.Wait()
			bc.cancel = nil
		}
		if previousStatus.CurrentStatus == types.StatusOk {
			bc.timer.Reset(30 * time.Second)
			timerDone = true
		}
	} else {
		bc.openSockets(ctx)
	}

	if !timerDone {
		bc.timer.Reset(time.Minute)
	}
	logger.V(2).Printf("check for %#v on %#v: %v", bc.metricName, bc.labels["item"], result)
	bc.acc.AddFieldsWithStatus(
		"",
		map[string]interface{}{
			bc.metricName: result.CurrentStatus.NagiosCode(),
		},
		bc.labels,
		map[string]types.StatusDescription{bc.metricName: result},
		false,
	)
	return result
}

func (bc *baseCheck) doCheck(ctx context.Context) (result types.StatusDescription) {
	if bc.mainCheck != nil {
		if result = bc.mainCheck(ctx); result.CurrentStatus != types.StatusOk {
			return result
		}
	}
	for _, addr := range bc.tcpAddresses {
		if addr == bc.mainTCPAddress {
			continue
		}
		if subResult := checkTCP(ctx, addr, nil, nil, nil); subResult.CurrentStatus != types.StatusOk {
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

func (bc *baseCheck) openSockets(ctx context.Context) {
	if bc.cancel != nil {
		// socket are already open
		return
	}
	ctx2, cancel := context.WithCancel(ctx)
	bc.cancel = cancel

	for _, addr := range bc.tcpAddresses {
		addr := addr
		bc.wg.Add(1)
		go func() {
			defer bc.wg.Done()
			bc.openSocket(ctx2, addr)
		}()
	}
}

func (bc *baseCheck) openSocket(ctx context.Context, addr string) {
	for ctx.Err() == nil {
		longSleep := bc.openSocketOnce(ctx, addr)
		delay := 10 * time.Second
		if !longSleep {
			delay = time.Second
		}
		select {
		case <-time.After(delay):
		case <-ctx.Done():
		}
	}
}

func (bc *baseCheck) openSocketOnce(ctx context.Context, addr string) (longSleep bool) {
	ctx2, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	conn, err := bc.dialer.DialContext(ctx2, "tcp", addr)
	if err != nil {
		logger.V(2).Printf("fail to open TCP connection to %#v: %v", addr, err)
		select {
		case bc.triggerC <- nil:
		default:
		}
		return true
	}
	defer conn.Close()
	buffer := make([]byte, 4096)
	for ctx.Err() == nil {
		err := conn.SetDeadline(time.Now().Add(time.Second))
		if err != nil {
			logger.V(2).Printf("Unable to SetDeadline() for %#v: %v", addr, err)
			return false
		}
		_, err = conn.Read(buffer)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			logger.V(2).Printf("Unable to Read() from %#v: %v", addr, err)
			return false
		}
	}
	return false
}
