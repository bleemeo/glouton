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
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"testing"
	"time"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/discovery"
)

func Test_jmxtransConfig_CurrentConfig(t *testing.T) {
	type fields struct {
		services      []discovery.Service
		resolution    time.Duration
		targetAddress string
		targetPort    int
	}

	tests := []struct {
		name   string
		fields fields
		want   []byte
	}{
		{
			name:   "unconfigured",
			fields: fields{},
			want:   nil,
		},
		{
			name: "empty",
			fields: fields{
				resolution:    10 * time.Second,
				services:      nil,
				targetAddress: "127.0.0.1",
				targetPort:    2003,
			},
			want: []byte(`{"servers":[]}`),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &jmxtransConfig{
				services:      tt.fields.services,
				resolution:    tt.fields.resolution,
				targetAddress: tt.fields.targetAddress,
				targetPort:    tt.fields.targetPort,
			}
			if got := cfg.CurrentConfig(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("jmxtransConfig.CurrentConfig() = %v, want %v", string(got), string(tt.want))
			}
		})
	}
}

func Test_jmxtransConfig_cassandra_Config(t *testing.T) {
	services := []discovery.Service{
		{
			Name:        "cassandra",
			Instance:    "",
			ServiceType: discovery.CassandraService,
			Active:      true,
			Config: config.Service{
				JMXPort:       1212,
				DetailedItems: []string{"squirreldb.data"},
			},
			IPAddress: "127.0.0.1",
		},
		{
			Name:        "cassandra",
			Instance:    "squirreldb-cassandra",
			ServiceType: discovery.CassandraService,
			Active:      true,
			Config: config.Service{
				JMXPort: 1217,
			},
			IPAddress:     "10.1.2.3",
			ContainerID:   "abc",
			ContainerName: "squirreldb-cassandra",
		},
	}

	cfg := jmxtransConfig{}
	cfg.UpdateTarget("127.5.4.3", 4242)
	_ = cfg.UpdateConfig(services, 11*time.Second)

	if content := cfg.CurrentConfig(); len(content) < len(`{"servers": []}`) {
		t.Errorf("cfg.CurrentConfig() = %#v, want non-empty", string(content))
	}

	cassandraSHA256 := hash("cassandra")
	cassandraContainerSHA256 := hash("cassandrasquirreldb-cassandra")
	memoryMBean := hash("java.lang:type=Memory")
	detailMBean := hash("org.apache.cassandra.metrics:type=Table,keyspace=squirreldb,scope=data,name=WriteLatency")

	service, ok := cfg.GetService(cassandraSHA256)
	if !ok || service.Name != services[0].Name || service.ContainerID != services[0].ContainerID {
		t.Errorf("cfg.GetService(%s) = %v, want %v", cassandraSHA256, service, services[0])
	}

	service, ok = cfg.GetService(cassandraContainerSHA256)
	if !ok || service.Name != services[1].Name || service.ContainerID != services[1].ContainerID {
		t.Errorf("cfg.GetService(cassandraSHA256) = %v, want %v", service, services[1])
	}

	metrics, _ := cfg.GetMetrics(cassandraContainerSHA256, memoryMBean, "HeapMemoryUsage_used")
	if len(metrics) == 0 {
		t.Errorf("cfg.GetMetrics(..., ..., HeapMemoryUsage_used) = %v, want metrics", metrics)
	}

	metrics, _ = cfg.GetMetrics(cassandraSHA256, detailMBean, "Count")
	if len(metrics) == 0 {
		t.Errorf("cfg.GetMetrics(..., ..., Count) = %v, want metrics", metrics)
	}
}

func hash(in string) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(in))

	return hex.EncodeToString(hash.Sum(nil))
}
