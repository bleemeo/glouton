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

package jmxtrans

import (
	"bytes"
	"context"
	"errors"
	"glouton/discovery"
	"glouton/logger"
	"glouton/types"
	"io/ioutil"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/influxdata/telegraf"
)

const (
	graphitePrefix = "jmxtrans"
)

// JMX allow to gather metrics from JVM using JMX
// It use jmxtrans to achieve this goal
type JMX struct {
	Accumulator             telegraf.Accumulator
	OutputConfigurationFile string
	// ContactAddress is the address where jmxtrans can reach Glouton.
	// If this is an IP address, Glouton will only listen on this address.
	// If this is an hostname, Glouton will listen on 0.0.0.0 and use this hostname
	// in jmxtrans configuration
	// The default is "127.0.0.1".
	ContactAddress string
	// ContactPort is the preferred port to bind to. If 0 no preferred port
	// will be used.
	ContactPort int
	// ContactPortForced spefify the behavior if preferred port is already used.
	// If forced, it's an error to have preferred port already used and JMX connector will stop
	// If not forced, a dynamic port will be used if preferred port is already used.
	ContactPortForced bool

	jmxConfig jmxtransConfig
}

// UpdateConfig update the jmxtrans configuration
func (j *JMX) UpdateConfig(services []discovery.Service, metricResolution time.Duration) error {
	err := j.jmxConfig.UpdateConfig(services, metricResolution)
	if err != nil {
		return err
	}

	j.writeConfig()

	return nil
}

// Run configure jmxtrans to send metrics to a local graphite server
func (j *JMX) Run(ctx context.Context) error {
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
	if err != nil && !j.ContactPortForced && strings.Contains(err.Error(), "address already in use") {
		bindAddress.Port = 0
		serverSocket, err = net.ListenTCP("tcp", &bindAddress)
	}

	if err != nil {
		return err
	}

	defer serverSocket.Close()

	addr := serverSocket.Addr()

	tcpAddr, ok := addr.(*net.TCPAddr)

	switch {
	case !ok:
		return errors.New("TCP listen didn't returned a TCP address")
	case j.ContactAddress == "":
		j.jmxConfig.UpdateTarget("127.0.0.1", tcpAddr.Port)
	default:
		j.jmxConfig.UpdateTarget(j.ContactAddress, tcpAddr.Port)
	}

	j.writeConfig()

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

func (j *JMX) writeConfig() {
	content := j.jmxConfig.CurrentConfig()
	if content == nil {
		logger.V(2).Printf("jmxtrans is not yet configured")
		return
	}

	if j.OutputConfigurationFile == "" {
		return
	}

	fileExists := true

	currentContent, err := ioutil.ReadFile(j.OutputConfigurationFile)
	if os.IsNotExist(err) {
		currentContent = []byte("{\"servers\":[]}")
		fileExists = false
	}

	if bytes.Equal(currentContent, content) {
		logger.V(1).Printf("jmxtrans configuration is up-to-date")
		return
	}

	err = ioutil.WriteFile(j.OutputConfigurationFile, content, 0600)
	if err != nil {
		if fileExists {
			logger.V(1).Printf("unable to write jmxtrans configuration, keeping current config: %v", err)
		} else {
			logger.V(2).Printf("unable to write jmxtrans configuration, is glouton-jmx installed ? %v", err)
		}
	}
}

func (j *JMX) emitPoint(point types.MetricPoint) {
	if j.Accumulator == nil {
		return
	}

	labels := make(map[string]string, len(point.Labels))

	for k, v := range point.Labels {
		if k == "__name__" {
			continue
		}

		labels[k] = v
	}

	j.Accumulator.AddFields(
		"",
		map[string]interface{}{point.Labels["__name__"]: point.Value},
		point.Labels,
		point.Time,
	)
}
