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
	"encoding/json"
	"fmt"
	"net"
	"slices"
	"sync"
	"time"

	"github.com/bleemeo/glouton/crashreport"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/types"
)

// baseCheck perform a service check.
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
// * (if persistentConnection is active) after a persistent TCP connection is broken.
type baseCheck struct {
	metricName     string
	labels         map[string]string
	annotations    types.MetricAnnotations
	mainTCPAddress string
	tcpAddresses   []string
	mainCheck      func(ctx context.Context) types.StatusDescription

	dialer *net.Dialer
	wg     sync.WaitGroup

	persistentConnection bool
	// Map of addresses with disabled persistent connection.
	// We use a sync.Map to avoid using the lock in the openSocket() goroutine,
	// which can cause a deadlock when Check() waits for all goroutines to end.
	disabledPersistent sync.Map

	l              sync.Mutex
	cancel         func()
	previousStatus types.StatusDescription
}

func newBase(mainTCPAddress string, tcpAddresses []string, persistentConnection bool, mainCheck func(context.Context) types.StatusDescription, labels map[string]string, annotations types.MetricAnnotations) *baseCheck {
	if mainTCPAddress != "" {
		found := slices.Contains(tcpAddresses, mainTCPAddress)

		if !found {
			tmp := make([]string, 0, len(tcpAddresses)+1)
			tmp = append(tmp, mainTCPAddress)
			tmp = append(tmp, tcpAddresses...)
			tcpAddresses = tmp
		}
	}

	metricName := labels[types.LabelName]

	return &baseCheck{
		metricName:           metricName,
		labels:               labels,
		annotations:          annotations,
		mainTCPAddress:       mainTCPAddress,
		tcpAddresses:         tcpAddresses,
		persistentConnection: persistentConnection,
		mainCheck:            mainCheck,

		dialer: &net.Dialer{},
		previousStatus: types.StatusDescription{
			CurrentStatus:     types.StatusOk,
			StatusDescription: "initial status - description is ignored",
		},
	}
}

func (bc *baseCheck) DiagnosticArchive(_ context.Context, archive types.ArchiveWriter) error {
	file, err := archive.Create("check-base.json")
	if err != nil {
		return err
	}

	bc.l.Lock()
	defer bc.l.Unlock()

	disabledPersistent := make([]string, 0)

	bc.disabledPersistent.Range(func(key, _ any) bool {
		keyStr, ok := key.(string)
		if !ok {
			keyStr = fmt.Sprintf("%v", key)
		}

		disabledPersistent = append(disabledPersistent, keyStr)

		return true
	})

	obj := struct {
		MetricName              string
		MetricLabels            map[string]string
		MetricAnnotations       types.MetricAnnotations
		MainTCPAddress          string
		TCPAddresses            []string
		UsePersistentConnection bool
		DisabledPersistent      []string
		PreviousStatus          types.StatusDescription
	}{
		MetricName:              bc.metricName,
		MetricLabels:            bc.labels,
		MetricAnnotations:       bc.annotations,
		MainTCPAddress:          bc.mainTCPAddress,
		TCPAddresses:            bc.tcpAddresses,
		UsePersistentConnection: bc.persistentConnection,
		DisabledPersistent:      disabledPersistent,
		PreviousStatus:          bc.previousStatus,
	}

	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")

	return enc.Encode(obj)
}

// Check runs the Check and returns the resulting point.
// If the Check is successful, it ensures the sockets are opened.
// If the fails, it ensures the sockets are closed.
// If it fails for the first time (ok -> critical), a new Check will be scheduled sooner.
func (bc *baseCheck) Check(ctx context.Context, scheduleUpdate func(runAt time.Time)) types.MetricPoint {
	bc.l.Lock()
	defer bc.l.Unlock()

	status := bc.doCheck(ctx)

	if ctx.Err() != nil {
		status = types.StatusDescription{
			CurrentStatus:     types.StatusUnknown,
			StatusDescription: "Check has timed out",
		}
	}

	if status.CurrentStatus != types.StatusOk {
		if bc.cancel != nil {
			bc.cancel()
			bc.wg.Wait()

			bc.cancel = nil
		}

		if bc.previousStatus.CurrentStatus == types.StatusOk && scheduleUpdate != nil {
			// The check just started failing, schedule another check sooner.
			scheduleUpdate(time.Now().Add(30 * time.Second))
		}
	} else {
		// The context used in openSockets must outlive the Check() since
		// it's used to maintain the persistent connection.
		bc.openSockets(scheduleUpdate)
	}

	bc.previousStatus = status

	annotations := bc.annotations
	annotations.Status = status

	point := types.MetricPoint{
		Point: types.Point{
			Time:  time.Now().Truncate(time.Second),
			Value: float64(status.CurrentStatus.NagiosCode()),
		},
		Labels:      bc.labels,
		Annotations: annotations,
	}

	return point
}

// doCheck runs the check and returns its status.
func (bc *baseCheck) doCheck(ctx context.Context) types.StatusDescription {
	var status types.StatusDescription

	if bc.mainCheck != nil {
		if status = bc.mainCheck(ctx); status.CurrentStatus != types.StatusOk {
			return status
		}
	}

	if len(bc.tcpAddresses) == 0 {
		statusOK := types.StatusDescription{
			CurrentStatus: types.StatusOk,
		}

		return statusOK
	}

	for _, addr := range bc.tcpAddresses {
		if addr == bc.mainTCPAddress {
			continue
		}

		if status = checkTCP(ctx, addr, nil, nil, nil); status.CurrentStatus != types.StatusOk {
			return status
		}
	}

	return status
}

func (bc *baseCheck) openSockets(scheduleUpdate func(runAt time.Time)) {
	if bc.cancel != nil {
		// socket are already open
		return
	}

	if !bc.persistentConnection {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	bc.cancel = cancel

	for _, addr := range bc.tcpAddresses {
		if _, ok := bc.disabledPersistent.Load(addr); ok {
			continue
		}

		bc.wg.Add(1)

		go func() {
			defer crashreport.ProcessPanic()
			defer bc.wg.Done()

			bc.openSocket(ctx, addr, scheduleUpdate)
		}()
	}
}

func (bc *baseCheck) openSocket(ctx context.Context, addr string, scheduleUpdate func(runAt time.Time)) {
	delay := 1 * time.Second / 2
	consecutiveFailure := 0

	for ctx.Err() == nil {
		lastConnect := time.Now()
		longSleep := bc.openSocketOnce(ctx, addr, scheduleUpdate)

		if time.Since(lastConnect) < time.Minute {
			consecutiveFailure++
		}

		if consecutiveFailure > 12 {
			logger.V(1).Printf("persistent connection to check %s keep getting closed quickly. Disabled persistent connection for this port", addr)
			bc.disabledPersistent.Store(addr, true)

			return
		}

		delay *= 2

		if time.Since(lastConnect) > 5*time.Minute {
			delay = 1 * time.Second
			consecutiveFailure = 0
		}

		if delay > 40*time.Second {
			delay = 40 * time.Second
		}

		if longSleep && delay < 10*time.Second {
			delay = 10 * time.Second
		}

		select {
		case <-time.After(delay):
		case <-ctx.Done():
		}
	}
}

func (bc *baseCheck) openSocketOnce(ctx context.Context, addr string, scheduleUpdate func(runAt time.Time)) (longSleep bool) {
	ctx2, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	conn, err := bc.dialer.DialContext(ctx2, "tcp", addr)
	if err != nil {
		logger.V(2).Printf("fail to open TCP connection to %#v: %v", addr, err)

		// Connection failed, trigger a check.
		if scheduleUpdate != nil {
			scheduleUpdate(time.Now())
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

func (bc *baseCheck) Close() {
	bc.l.Lock()
	defer bc.l.Unlock()

	// Close open TCP connections.
	if bc.cancel != nil {
		bc.cancel()
	}
}
