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

package jmxtrans

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"sync"
	"time"

	"github.com/bleemeo/glouton/crashreport"
	"github.com/bleemeo/glouton/discovery"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/types"
)

const (
	graphitePrefix = "jmxtrans"
)

var (
	errJmxAlreadyStopped = errors.New("JMX is already stopped, can't update config")
	errJmxStopping       = errors.New("JMX is stopping, can't update config")
)

// JMX allow to gather metrics from JVM using JMX
// It use jmxtrans to achieve this goal.
type JMX struct {
	Pusher                  types.PointPusher
	OutputConfigurationFile string
	// ContactAddress is the address where jmxtrans can reach Glouton.
	// If this is an IP address, Glouton will only listen on this address.
	// If this is an hostname, Glouton will listen on 0.0.0.0 and use this hostname
	// in jmxtrans configuration
	// The default is "127.0.0.1".
	ContactAddress string
	// ContactPort is the preferred port to bind to.
	ContactPort                   int
	OutputConfigurationPermission os.FileMode

	stopped             bool
	l                   sync.Mutex
	jmxConfig           jmxtransConfig
	services            []discovery.Service
	metricResolution    time.Duration
	triggerConfigUpdate chan updateRequest
}

type updateRequest struct {
	reply chan<- error
}

// UpdateConfig update the jmxtrans configuration.
func (j *JMX) UpdateConfig(services []discovery.Service, metricResolution time.Duration) error {
	j.l.Lock()
	j.services = services
	j.metricResolution = metricResolution

	if j.triggerConfigUpdate == nil {
		// this means we are not yet started, Update config in this go-routing.
		err := j.jmxConfig.UpdateConfig(j.services, j.metricResolution)

		j.l.Unlock()

		return err
	}

	if j.stopped {
		j.l.Unlock()

		return errJmxAlreadyStopped
	}

	j.l.Unlock()

	replyChan := make(chan error)
	j.triggerConfigUpdate <- updateRequest{reply: replyChan}

	return <-replyChan
}

// Run configure jmxtrans to send metrics to a local graphite server.
func (j *JMX) Run(ctx context.Context) error {
	j.l.Lock()

	j.stopped = false

	if j.triggerConfigUpdate == nil {
		j.triggerConfigUpdate = make(chan updateRequest)
	}

	if j.ContactAddress == "" {
		j.jmxConfig.UpdateTarget("127.0.0.1", j.ContactPort)
	} else {
		j.jmxConfig.UpdateTarget(j.ContactAddress, j.ContactPort)
	}

	j.l.Unlock()

	var (
		serverContext   context.Context
		serverCancel    context.CancelFunc
		serverWaitGroup sync.WaitGroup
	)

	for ctx.Err() == nil {
		var req updateRequest

		select {
		case <-ctx.Done():
		case req = <-j.triggerConfigUpdate:
		}

		if ctx.Err() != nil {
			break
		}

		j.l.Lock()

		err := j.jmxConfig.UpdateConfig(j.services, j.metricResolution)

		j.l.Unlock()

		req.reply <- err

		if err != nil {
			continue
		}

		config := j.jmxConfig.CurrentConfig()

		err = j.writeConfig(config)
		if err != nil && serverCancel != nil {
			logger.V(2).Println("JMX configuration update failed, stopping graphite server")
			serverCancel()
			serverWaitGroup.Wait()

			serverCancel = nil
		}

		if j.jmxConfig.IsEmpty(config) && serverCancel != nil {
			logger.V(2).Println("JMX configuration is empty, stopping graphite server")
			serverCancel()
			serverWaitGroup.Wait()

			serverCancel = nil
		}

		if serverCancel == nil && err == nil && !j.jmxConfig.IsEmpty(config) {
			logger.V(2).Println("JMX configuration not empty, starting graphite server")

			serverContext, serverCancel = context.WithCancel(ctx) //nolint: fatcontext

			serverWaitGroup.Add(1)

			go func() {
				defer crashreport.ProcessPanic()
				defer serverWaitGroup.Done()

				if err := j.runServer(serverContext); err != nil {
					logger.V(1).Printf("unable to start JMX's graphite listenner: %v", err)
				}

				<-serverContext.Done()
			}()
		}
	}

	if serverCancel != nil {
		serverCancel()
		serverWaitGroup.Wait()

		serverContext = nil
	}

	j.l.Lock()

	j.stopped = true

	j.l.Unlock()

	// Purge the possibly non-empty triggerConfigUpdate
	for {
		select {
		case req := <-j.triggerConfigUpdate:
			req.reply <- errJmxStopping
		case <-time.After(time.Second):
			return nil
		}
	}
}

func (j *JMX) runServer(ctx context.Context) error {
	bindIP := net.ParseIP(j.ContactAddress)
	if j.ContactAddress == "" {
		bindIP = net.IPv4(127, 0, 0, 1)
	}

	if bindIP == nil {
		bindIP = net.IPv4(0, 0, 0, 0)
	}

	bindAddress := net.TCPAddr{
		IP:   bindIP,
		Port: j.ContactPort,
	}

	serverSocket, err := net.ListenTCP("tcp", &bindAddress)
	if err != nil {
		return err
	}

	defer serverSocket.Close()

	var wg sync.WaitGroup

	subContext, cancel := context.WithCancel(ctx)

	for ctx.Err() == nil {
		err = serverSocket.SetDeadline(time.Now().Add(time.Second))
		if err != nil {
			break
		}

		var c *net.TCPConn

		c, err = serverSocket.AcceptTCP()

		if errNet, ok := err.(net.Error); ok && errNet.Timeout() {
			err = nil

			continue
		}

		if ctx.Err() != nil {
			break
		}

		wg.Add(1)

		go func() {
			defer crashreport.ProcessPanic()
			defer wg.Done()

			client := jmxtransClient{
				Connection: c,
				Config:     &j.jmxConfig,
				EmitPoint:  j.emitPoint,
			}
			client.Run(subContext)
		}()
	}

	cancel()
	wg.Wait()

	return err
}

func (j *JMX) writeConfig(content []byte) error {
	if content == nil {
		logger.V(2).Printf("jmxtrans is not yet configured")

		return nil
	}

	if j.OutputConfigurationFile == "" {
		return nil
	}

	currentContent, err := os.ReadFile(j.OutputConfigurationFile)
	if os.IsNotExist(err) {
		currentContent = []byte("{\"servers\":[]}")
	}

	if bytes.Equal(currentContent, content) {
		logger.V(1).Printf("jmxtrans configuration is up-to-date")

		return nil
	}

	perm := j.OutputConfigurationPermission
	if perm == 0 {
		perm = 0o640
	}

	err = os.WriteFile(j.OutputConfigurationFile, content, perm)
	if err != nil {
		logger.V(2).Printf("unable to write jmxtrans configuration, is glouton-jmx installed ? %v", err)
	}

	return err
}

func (j *JMX) emitPoint(ctx context.Context, point types.MetricPoint) {
	if j.Pusher == nil {
		return
	}

	j.Pusher.PushPoints(ctx, []types.MetricPoint{point})
}
