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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"glouton/discovery"
	"glouton/logger"
	"strconv"
	"sync"
	"time"
)

type jmxtransConfig struct {
	l               sync.Mutex
	services        []discovery.Service
	resolution      time.Duration
	targetAddress   string
	targetPort      int
	sha256ToService map[string]discovery.Service
	knownMetrics    map[metricKey][]jmxMetric
	isDivisor       map[string]bool
}

type metricKey struct {
	sha256Service string
	sha256Bean    string
	attr          string
}

type configRoot struct {
	Servers []configServer `json:"servers"`
}

type configServer struct {
	Host             string         `json:"host"`
	Alias            string         `json:"alias"`
	Port             int            `json:"port"`
	Queries          []configQuery  `json:"queries"`
	OutputWriters    []configWriter `json:"outputWriters"`
	RunPeriodSeconds int            `json:"runPeriodSeconds"`
	Username         string         `json:"username,omitempty"`
	Password         string         `json:"password,omitempty"`
}

type configQuery struct {
	Object        string         `json:"obj"`
	OutputWriters []configWriter `json:"outputWriters"`
	ResultAlias   string         `json:"resultAlias"`
	Attr          []string       `json:"attr"`
	TypeNames     []string       `json:"typeNames,omitempty"`
}

type configWriter struct {
	Class               string `json:"@class"`
	RootPrefix          string `json:"rootPrefix"`
	Host                string `json:"host"`
	Port                int    `json:"port"`
	FlushStrategy       string `json:"flushStrategy"`
	FlushDelayInSeconds int    `json:"flushDelayInSeconds"`
}

func (cfg *jmxtransConfig) UpdateConfig(services []discovery.Service, metricResolution time.Duration) error {
	cfg.l.Lock()
	defer cfg.l.Unlock()

	cfg.resolution = metricResolution
	cfg.services = services

	return nil
}

// UpdateTarget set the address where jmxtrans send metrics
func (cfg *jmxtransConfig) UpdateTarget(targetAddress string, targetPort int) {
	cfg.l.Lock()
	defer cfg.l.Unlock()

	cfg.targetAddress = targetAddress
	cfg.targetPort = targetPort
}

func (cfg *jmxtransConfig) IsEmpty(config []byte) bool {
	return len(config) == 0 || bytes.Equal(config, []byte("{\"servers\":[]}"))
}

func (cfg *jmxtransConfig) CurrentConfig() []byte {
	cfg.l.Lock()
	defer cfg.l.Unlock()

	if cfg.resolution == 0 || cfg.targetAddress == "" {
		return nil
	}

	cfg.sha256ToService = make(map[string]discovery.Service)
	cfg.knownMetrics = make(map[metricKey][]jmxMetric)
	cfg.isDivisor = make(map[string]bool)

	outputConfig := configWriter{
		Class:               "com.googlecode.jmxtrans.model.output.GraphiteWriterFactory",
		RootPrefix:          graphitePrefix,
		Host:                cfg.targetAddress,
		Port:                cfg.targetPort,
		FlushStrategy:       "timeBased",
		FlushDelayInSeconds: int(cfg.resolution.Seconds()),
	}
	rootConfig := configRoot{
		Servers: []configServer{},
	}

	for _, service := range cfg.services {
		if !service.Active {
			continue
		}

		if service.ExtraAttributes["jmx_port"] == "" {
			continue
		}

		port, err := strconv.ParseInt(service.ExtraAttributes["jmx_port"], 10, 0)
		if err != nil {
			continue
		}

		if service.IPAddress == "" {
			continue
		}

		hash := sha256.New()
		_, _ = hash.Write([]byte(service.ServiceType))

		if service.ContainerName != "" {
			_, _ = hash.Write([]byte(service.ContainerName))
		}

		sha256Service := hex.EncodeToString(hash.Sum(nil))
		cfg.sha256ToService[sha256Service] = service
		server := configServer{
			Alias:            sha256Service,
			Host:             service.IPAddress,
			Port:             int(port),
			OutputWriters:    []configWriter{outputConfig},
			RunPeriodSeconds: int(cfg.resolution.Seconds()),
		}

		if service.ExtraAttributes["jmx_username"] != "" {
			server.Username = service.ExtraAttributes["jmx_username"]
			server.Password = service.ExtraAttributes["jmx_password"]
		}

		metrics := getJMXMetrics(service)
		for _, m := range metrics {
			hash := sha256.New() // nolint: gosec
			_, _ = hash.Write([]byte(m.MBean))

			attr := m.Attribute
			if m.Path != "" {
				attr += "_" + m.Path
			}

			sha256Bean := hex.EncodeToString(hash.Sum(nil))
			key := metricKey{
				sha256Service: sha256Service,
				sha256Bean:    sha256Bean,
				attr:          attr,
			}
			cfg.knownMetrics[key] = append(cfg.knownMetrics[key], m)

			if m.Ratio != "" {
				cfg.isDivisor[fmt.Sprintf("%s_%s", service.Name, m.Ratio)] = true
			}

			query := configQuery{
				Object:        m.MBean,
				OutputWriters: []configWriter{},
				ResultAlias:   sha256Bean,
				Attr:          []string{m.Attribute},
				TypeNames:     m.TypeNames,
			}
			server.Queries = append(server.Queries, query)
		}

		rootConfig.Servers = append(rootConfig.Servers, server)
	}

	result, err := json.Marshal(rootConfig)
	if err != nil {
		logger.V(1).Printf("unable to marshal jmxtrans config: %v", err)
		return nil
	}

	return result
}

func (cfg *jmxtransConfig) GetService(sha256Service string) (discovery.Service, bool) {
	cfg.l.Lock()
	defer cfg.l.Unlock()

	service, found := cfg.sha256ToService[sha256Service]

	return service, found
}

func (cfg *jmxtransConfig) GetMetrics(sha256Service string, sha256Bean string, attr string) (metrics []jmxMetric, usedInRatio bool) {
	cfg.l.Lock()
	defer cfg.l.Unlock()

	service, ok := cfg.sha256ToService[sha256Service]
	if !ok {
		return nil, false
	}

	key := metricKey{
		sha256Service: sha256Service,
		sha256Bean:    sha256Bean,
		attr:          attr,
	}
	metrics = cfg.knownMetrics[key]

	for _, m := range metrics {
		if cfg.isDivisor[service.Name+"_"+m.Name] {
			return metrics, true
		}
	}

	return metrics, false
}
