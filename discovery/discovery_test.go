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

package discovery

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bleemeo/glouton/agent/state"
	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/facts"
	"github.com/bleemeo/glouton/prometheus/registry"
	"github.com/bleemeo/glouton/utils/gloutonexec"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/influxdata/telegraf"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/model"
)

//nolint:gochecknoinits
func init() {
	// We want to keep the strict name validation.
	// It is done globally in agent/agent.go,
	// but since the agent package isn't loaded during these tests,
	// we must do it here too.
	model.NameValidationScheme = model.LegacyValidation
}

var (
	errNotImplemented = errors.New("not implemented")
	errNotCalled      = errors.New("not called, want call")
	errWantName       = errors.New("want name")
)

type mockState struct {
	DiscoveredService []Service
}

func (ms mockState) Set(key string, object any) error {
	_ = key
	_ = object

	return errNotImplemented
}

func (ms mockState) Get(key string, object any) error {
	_ = key

	if services, ok := object.(*[]Service); ok {
		*services = ms.DiscoveredService

		return nil
	}

	return errNotImplemented
}

type mockRegistry struct {
	ExpectedAddedContains []string
	NewIDs                []int
	ExpectedRemoveIDs     []int
	err                   error
}

func (m *mockRegistry) RegisterGatherer(opt registry.RegistrationOption, gatherer prometheus.Gatherer) (int, error) {
	_ = gatherer

	if len(m.ExpectedAddedContains) == 0 {
		m.err = fmt.Errorf("%w: RegisterGatherer() ExpectedAddedContains empty when called with description %s", errWantName, opt.Description)
	}

	if !strings.Contains(opt.Description, m.ExpectedAddedContains[0]) {
		m.err = fmt.Errorf("%w: RegisterGatherer() Description=%s, want %s", errWantName, opt.Description, m.ExpectedAddedContains[0])

		return 0, m.err
	}

	newID := m.NewIDs[0]
	m.ExpectedAddedContains = m.ExpectedAddedContains[1:]
	m.NewIDs = m.NewIDs[1:]

	return newID, nil
}

func (m *mockRegistry) RegisterInput(opt registry.RegistrationOption, input telegraf.Input) (int, error) {
	_ = input

	if len(m.ExpectedAddedContains) == 0 {
		m.err = fmt.Errorf("%w: RegisterInput() ExpectedAddedContains empty when called with description %s", errWantName, opt.Description)
	}

	if !strings.Contains(opt.Description, m.ExpectedAddedContains[0]) {
		m.err = fmt.Errorf("%w: RegisterInput() Description=%s, want %s", errWantName, opt.Description, m.ExpectedAddedContains[0])

		return 0, m.err
	}

	newID := m.NewIDs[0]
	m.ExpectedAddedContains = m.ExpectedAddedContains[1:]
	m.NewIDs = m.NewIDs[1:]

	return newID, nil
}

func (m *mockRegistry) Unregister(id int) bool {
	if len(m.ExpectedRemoveIDs) == 0 {
		m.err = fmt.Errorf("%w: Unregister() ExpectedRemoveIDs empty when called with id %d", errWantName, id)
	}

	if id != m.ExpectedRemoveIDs[0] {
		m.err = fmt.Errorf("%w: Unregister(%d) want %d", errWantName, id, m.ExpectedRemoveIDs[0])

		return true
	}

	m.ExpectedRemoveIDs = m.ExpectedRemoveIDs[1:]

	return true
}

func (m *mockRegistry) ExpectationFulfilled() error {
	if m.err != nil {
		return m.err
	}

	if len(m.ExpectedAddedContains) > 0 {
		return fmt.Errorf("%w: Register*() missing: %v", errNotCalled, m.ExpectedAddedContains)
	}

	if len(m.ExpectedRemoveIDs) > 0 {
		return fmt.Errorf("%w: Unregister() missing: %v", errNotCalled, m.ExpectedRemoveIDs)
	}

	return nil
}

// Test dynamic Discovery with single service present.
func TestDiscoverySingle(t *testing.T) {
	t0 := time.Now()

	cases := []struct {
		dynamicResult   Service
		previousService Service
		want            Service
	}{
		{
			previousService: Service{},
			dynamicResult: Service{
				Name:            "memcached",
				ServiceType:     MemcachedService,
				ContainerID:     "",
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 11211}},
				IPAddress:       "127.0.0.1",
				HasNetstatInfo:  true,
				LastNetstatInfo: t0,
			},
			want: Service{
				Name:            "memcached",
				ServiceType:     MemcachedService,
				ContainerID:     "",
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 11211}},
				IPAddress:       "127.0.0.1",
				HasNetstatInfo:  true,
				LastNetstatInfo: t0,
			},
		},
		{
			previousService: Service{
				Name:            "memcached",
				ServiceType:     MemcachedService,
				ContainerID:     "",
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 11211}},
				IPAddress:       "127.0.0.1",
				HasNetstatInfo:  true,
				LastNetstatInfo: t0,
			},
			dynamicResult: Service{
				Name:            "memcached",
				ServiceType:     MemcachedService,
				ContainerID:     "",
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 11211}},
				IPAddress:       "127.0.0.1",
				HasNetstatInfo:  true,
				LastNetstatInfo: t0,
			},
			want: Service{
				Name:            "memcached",
				ServiceType:     MemcachedService,
				ContainerID:     "",
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 11211}},
				IPAddress:       "127.0.0.1",
				HasNetstatInfo:  true,
				LastNetstatInfo: t0,
			},
		},
		{
			previousService: Service{
				Name:            "memcached",
				ServiceType:     MemcachedService,
				ContainerID:     "",
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "10.0.0.5", Port: 11211}},
				IPAddress:       "10.0.0.5",
				HasNetstatInfo:  true,
				LastNetstatInfo: t0,
			},
			dynamicResult: Service{
				Name:            "memcached",
				ServiceType:     MemcachedService,
				ContainerID:     "",
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 11211}},
				IPAddress:       "",
				HasNetstatInfo:  false,
			},
			want: Service{
				Name:            "memcached",
				ServiceType:     MemcachedService,
				ContainerID:     "",
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "10.0.0.5", Port: 11211}},
				IPAddress:       "10.0.0.5",
				HasNetstatInfo:  true,
				LastNetstatInfo: t0,
			},
		},
		{
			previousService: Service{
				Name:            "memcached",
				ServiceType:     MemcachedService,
				ContainerID:     "",
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "10.0.0.5", Port: 11211}},
				IPAddress:       "10.0.0.5",
				HasNetstatInfo:  true,
				LastNetstatInfo: t0,
			},
			dynamicResult: Service{
				Name:            "memcached",
				ServiceType:     MemcachedService,
				ContainerID:     "",
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 11211}},
				IPAddress:       "127.0.0.1",
				HasNetstatInfo:  true,
			},
			want: Service{
				Name:            "memcached",
				ServiceType:     MemcachedService,
				ContainerID:     "",
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 11211}},
				IPAddress:       "127.0.0.1",
				HasNetstatInfo:  true,
				LastNetstatInfo: t0,
			},
		},
	}

	ctx := t.Context()

	for i, c := range cases {
		var previousService []Service

		if c.previousService.ServiceType != "" {
			previousService = append(previousService, c.previousService)
		}

		state := mockState{
			DiscoveredService: previousService,
		}
		disc, _ := New(
			&MockDiscoverer{result: []Service{c.dynamicResult}},
			gloutonexec.New("/"),
			nil,
			state,
			mockContainerInfo{},
			nil,
			nil,
			nil,
			nil,
			facts.ContainerFilter{}.ContainerIgnored,
			nil,
			time.Hour,
			config.OpenTelemetry{},
		)

		srv, _, err := disc.discovery(ctx)
		if err != nil {
			t.Error(err)
		}

		if len(srv) != 1 {
			t.Errorf("Case #%d: len(srv) == %v, want 1", i, len(srv))
		}

		if srv[0].Name != c.want.Name {
			t.Errorf("Case #%d: Name == %#v, want %#v", i, srv[0].Name, c.want.Name)
		}

		if srv[0].ServiceType != c.want.ServiceType {
			t.Errorf("Case #%d: ServiceType == %#v, want %#v", i, srv[0].ServiceType, c.want.ServiceType)
		}

		if srv[0].ContainerID != c.want.ContainerID {
			t.Errorf("Case #%d: ContainerID == %#v, want %#v", i, srv[0].ContainerID, c.want.ContainerID)
		}

		if srv[0].IPAddress != c.want.IPAddress {
			t.Errorf("Case #%d: IPAddress == %#v, want %#v", i, srv[0].IPAddress, c.want.IPAddress)
		}

		if !reflect.DeepEqual(srv[0].ListenAddresses, c.want.ListenAddresses) {
			t.Errorf("Case #%d: ListenAddresses == %v, want %v", i, srv[0].ListenAddresses, c.want.ListenAddresses)
		}

		if srv[0].HasNetstatInfo != c.want.HasNetstatInfo {
			t.Errorf("Case #%d: hasNetstatInfo == %#v, want %#v", i, srv[0].HasNetstatInfo, c.want.HasNetstatInfo)
		}
	}
}

func Test_applyOverride(t *testing.T) { //nolint:maintidx
	type args struct {
		discoveredServicesMap map[NameInstance]Service
		servicesOverride      []config.Service
	}

	t0 := time.Now()

	tests := []struct {
		name string
		args args
		want map[NameInstance]Service
	}{
		{
			name: "empty",
			args: args{
				discoveredServicesMap: nil,
				servicesOverride:      nil,
			},
			want: make(map[NameInstance]Service),
		},
		{
			name: "no override",
			args: args{
				discoveredServicesMap: map[NameInstance]Service{
					{Name: "apache"}: {
						Name:            "apache",
						ServiceType:     ApacheService,
						IPAddress:       "127.0.0.1",
						ListenAddresses: []facts.ListenAddress{},
					},
				},
				servicesOverride: nil,
			},
			want: map[NameInstance]Service{
				{Name: "apache"}: {
					Name:            "apache",
					ServiceType:     ApacheService,
					IPAddress:       "127.0.0.1",
					ListenAddresses: []facts.ListenAddress{},
				},
			},
		},
		{
			name: "address override",
			args: args{
				discoveredServicesMap: map[NameInstance]Service{
					{Name: "apache"}: {
						Name:        "apache",
						ServiceType: ApacheService,
					},
				},
				servicesOverride: []config.Service{
					{
						Type:    "apache",
						Address: "10.0.1.2",
					},
				},
			},
			want: map[NameInstance]Service{
				{Name: "apache"}: {
					Name:        "apache",
					ServiceType: ApacheService,
					Config: config.Service{
						Type:    "apache",
						Address: "10.0.1.2",
					},
					ListenAddresses: []facts.ListenAddress{
						{
							NetworkFamily: "tcp",
							Address:       "10.0.1.2",
							Port:          80,
						},
					},
					IPAddress: "10.0.1.2",
				},
			},
		},
		{
			name: "add custom check",
			args: args{
				discoveredServicesMap: map[NameInstance]Service{
					{Name: "apache"}: {
						Name:        "apache",
						ServiceType: ApacheService,
					},
				},
				servicesOverride: []config.Service{
					{
						Type:         "myapplication",
						Port:         8080,
						CheckType:    customCheckNagios,
						CheckCommand: "command-to-run",
					},
					{
						Type: "custom_webserver",
						Port: 8081,
					},
				},
			},
			want: map[NameInstance]Service{
				{Name: "apache"}: {
					Name:        "apache",
					ServiceType: ApacheService,
				},
				{Name: "myapplication"}: {
					ServiceType: CustomService,
					Config: config.Service{
						Type:         "myapplication",
						Address:      "127.0.0.1", // default as soon as port is set
						Port:         8080,
						CheckType:    customCheckNagios,
						CheckCommand: "command-to-run",
					},
					Name:   "myapplication",
					Active: true,
				},
				{Name: "custom_webserver"}: {
					ServiceType: CustomService,
					Config: config.Service{
						Type:      "custom_webserver",
						Address:   "127.0.0.1", // default as soon as port is set
						Port:      8081,
						CheckType: customCheckTCP, // default as soon as port is set,
					},
					Name:   "custom_webserver",
					Active: true,
				},
			},
		},
		{
			name: "bad custom check",
			args: args{
				discoveredServicesMap: nil,
				servicesOverride: []config.Service{
					{ // the check_command is missing
						Type:      "myapplication",
						Port:      8080,
						CheckType: customCheckNagios,
					},
					{ // port is missing
						Type:      "custom_webserver",
						CheckType: customCheckHTTP,
					},
				},
			},
			want: map[NameInstance]Service{},
		},
		{
			name: "ignore ports",
			args: args{
				discoveredServicesMap: map[NameInstance]Service{
					{Name: "apache"}: {
						Name:        "apache",
						ServiceType: ApacheService,
						IPAddress:   "127.0.0.1",
						ListenAddresses: []facts.ListenAddress{
							{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 80},
							{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 443},
						},
					},
				},
				servicesOverride: []config.Service{
					{
						Type:        "apache",
						IgnorePorts: []int{443, 22},
					},
				},
			},
			want: map[NameInstance]Service{
				{Name: "apache"}: {
					Name:        "apache",
					ServiceType: ApacheService,
					IPAddress:   "127.0.0.1",
					ListenAddresses: []facts.ListenAddress{
						{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 80},
						// It's not applyOverride which remove ignored ports
						{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 443},
					},
					IgnoredPorts: map[int]bool{
						22:  true,
						443: true,
					},
					Config: config.Service{
						Type:        "apache",
						IgnorePorts: []int{443, 22},
					},
				},
			},
		},
		{
			name: "ignore ports with space",
			args: args{
				discoveredServicesMap: map[NameInstance]Service{
					{Name: "apache"}: {
						Name:        "apache",
						ServiceType: ApacheService,
					},
				},
				servicesOverride: []config.Service{
					{
						Type:        "apache",
						IgnorePorts: []int{443, 22},
					},
				},
			},
			want: map[NameInstance]Service{
				{Name: "apache"}: {
					Name:        "apache",
					ServiceType: ApacheService,
					IgnoredPorts: map[int]bool{
						22:  true,
						443: true,
					},
					Config: config.Service{
						Type:        "apache",
						IgnorePorts: []int{443, 22},
					},
				},
			},
		},
		{
			name: "create tags list",
			args: args{
				discoveredServicesMap: map[NameInstance]Service{
					{Name: "apache"}: {
						Name:        "apache",
						ServiceType: ApacheService,
					},
				},
				servicesOverride: []config.Service{
					{
						Type: "apache",
						Tags: []string{"website"},
					},
				},
			},
			want: map[NameInstance]Service{
				{Name: "apache"}: {
					Name:        "apache",
					ServiceType: ApacheService,
					Tags:        []string{"website"},
					Config: config.Service{
						Type: "apache",
						Tags: []string{"website"},
					},
				},
			},
		},
		{
			name: "extend tags list",
			args: args{
				discoveredServicesMap: map[NameInstance]Service{
					{Name: "apache"}: {
						Name:        "apache",
						ServiceType: ApacheService,
						Tags:        []string{"tag-from-dynamic-discovery-like-docker-labels"},
					},
				},
				servicesOverride: []config.Service{
					{
						Type: "apache",
						Tags: []string{"website"},
					},
				},
			},
			want: map[NameInstance]Service{
				{Name: "apache"}: {
					Name:        "apache",
					ServiceType: ApacheService,
					Tags:        []string{"tag-from-dynamic-discovery-like-docker-labels", "website"},
					Config: config.Service{
						Type: "apache",
						Tags: []string{"website"},
					},
				},
			},
		},
		{
			name: "override port from jmx port",
			args: args{
				discoveredServicesMap: map[NameInstance]Service{},
				servicesOverride: []config.Service{
					{
						Type:    "jmx_custom",
						JMXPort: 1000,
					},
				},
			},
			want: map[NameInstance]Service{
				{Name: "jmx_custom"}: {
					Name:        "jmx_custom",
					ServiceType: CustomService,
					Config: config.Service{
						Type:      "jmx_custom",
						Address:   "127.0.0.1",
						Port:      1000,
						JMXPort:   1000,
						CheckType: customCheckTCP,
					},
					Active: true,
				},
			},
		},
		{
			name: "no override port from jmx port",
			args: args{
				discoveredServicesMap: map[NameInstance]Service{},
				servicesOverride: []config.Service{
					{
						Type:    "jmx_custom",
						Port:    8000,
						JMXPort: 1000,
					},
				},
			},
			want: map[NameInstance]Service{
				{Name: "jmx_custom"}: {
					Name:        "jmx_custom",
					ServiceType: CustomService,
					Config: config.Service{
						Type:      "jmx_custom",
						Address:   "127.0.0.1",
						Port:      8000,
						JMXPort:   1000,
						CheckType: customCheckTCP,
					},
					Active: true,
				},
			},
		},
		{
			name: "override docker labels",
			args: args{
				discoveredServicesMap: map[NameInstance]Service{
					{Name: "kafka"}: {
						Name:        "kafka",
						ServiceType: KafkaService,
						// This case happens when "glouton.port" and
						// "glouton.jmx_port" docker labels are set.
						Config: config.Service{
							Port:    8000,
							JMXPort: 1000,
						},
					},
				},
				servicesOverride: []config.Service{
					{
						Type:    "kafka",
						Port:    9000,
						JMXPort: 2000,
					},
				},
			},
			want: map[NameInstance]Service{
				{Name: "kafka"}: {
					Name:        "kafka",
					ServiceType: KafkaService,
					Config: config.Service{
						Type:    "kafka",
						Port:    9000,
						JMXPort: 2000,
					},
				},
			},
		},
		{
			name: "redis with customized ports",
			// In this test, we have 2 redis running on host. Using configuration we tells which redis listen
			// on which ports.
			args: args{
				discoveredServicesMap: map[NameInstance]Service{
					{Name: "redis"}: {
						Name:        "redis",
						ServiceType: RedisService,
						ListenAddresses: []facts.ListenAddress{
							{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 6379},
							{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 6380},
						},
						IPAddress:       "127.0.0.1",
						HasNetstatInfo:  true,
						LastNetstatInfo: t0,
						Active:          true,
					},
				},
				servicesOverride: []config.Service{
					{
						Type:     "redis",
						Instance: "duplicate",
						Address:  "127.0.0.1",
						Port:     6379,
					},
				},
			},
			want: map[NameInstance]Service{
				{Name: "redis"}: {
					Name:        "redis",
					ServiceType: RedisService,
					ListenAddresses: []facts.ListenAddress{
						{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 6380},
					},
					IPAddress:       "127.0.0.1",
					HasNetstatInfo:  true,
					LastNetstatInfo: t0,
					Active:          true,
				},
				{Name: "redis", Instance: "duplicate"}: {
					Name:        "redis",
					Instance:    "duplicate",
					ServiceType: RedisService,
					ListenAddresses: []facts.ListenAddress{
						{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 6379},
					},
					IPAddress: "127.0.0.1",
					Active:    true,
					Config: config.Service{
						Type:     "redis",
						Instance: "duplicate",
						Address:  "127.0.0.1",
						Port:     6379,
					},
				},
			},
		},
		{
			name: "nginx-alternative-port-ignore2",
			args: args{
				discoveredServicesMap: map[NameInstance]Service{
					{Name: "nginx", Instance: "nginx_port_alt"}: {
						Name:          "nginx",
						Instance:      "nginx_port_alt",
						ServiceType:   NginxService,
						ContainerID:   "817ec63d4b4f9e28947a323f9fbfc4596500b42c842bf07bd6ad9641e6805cb5",
						ContainerName: "nginx_port_alt",
						// It's not applyOverride which remove ignored ports, but we are in ignored ports
						ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "172.16.0.2", Port: 80}},
						IPAddress:       "172.16.0.2",
						// This test is done in two path. In dynamic.go we add the option to Config.
						// in discovery (applyOverrideInPlance) we apply the config override.
						// IgnoredPorts: map[int]bool{
						//	74: true,
						//	75: true,
						//	80: true,
						// },
						Active:          true,
						HasNetstatInfo:  true,
						LastNetstatInfo: t0,
						Config: config.Service{
							IgnorePorts: []int{74, 75, 80},
						},
					},
				},
			},
			want: map[NameInstance]Service{
				{Name: "nginx", Instance: "nginx_port_alt"}: {
					Name:          "nginx",
					Instance:      "nginx_port_alt",
					ServiceType:   NginxService,
					ContainerID:   "817ec63d4b4f9e28947a323f9fbfc4596500b42c842bf07bd6ad9641e6805cb5",
					ContainerName: "nginx_port_alt",
					// It's not applyOverride which remove ignored ports, but we are in ignored ports
					ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "172.16.0.2", Port: 80}},
					IPAddress:       "172.16.0.2",
					// This test is done in two path. In dynamic.go we add the option to Config.
					// in discovery (applyOverrideInPlance) we apply the config override.
					IgnoredPorts: map[int]bool{
						74: true,
						75: true,
						80: true,
					},
					Active:          true,
					HasNetstatInfo:  true,
					LastNetstatInfo: t0,
					Config: config.Service{
						IgnorePorts: []int{74, 75, 80},
					},
				},
			},
		},
		{
			name: "nginx-alternative-port-update",
			args: args{
				discoveredServicesMap: map[NameInstance]Service{
					{Name: "nginx", Instance: "nginx_port_alt"}: {
						Name:          "nginx",
						Instance:      "nginx_port_alt",
						ServiceType:   NginxService,
						ContainerID:   "817ec63d4b4f9e28947a323f9fbfc4596500b42c842bf07bd6ad9641e6805cb5",
						ContainerName: "nginx_port_alt",
						// This test is done in two path. In dynamic.go we add the option to Config.
						// in discovery (applyOverrideInPlance) we apply the config override.
						ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "172.16.0.2", Port: 80}},
						IPAddress:       "172.16.0.2",
						Active:          true,
						HasNetstatInfo:  true,
						LastNetstatInfo: t0,
						Config: config.Service{
							Port: 8080,
						},
					},
				},
			},
			want: map[NameInstance]Service{
				{Name: "nginx", Instance: "nginx_port_alt"}: {
					Name:            "nginx",
					Instance:        "nginx_port_alt",
					ServiceType:     NginxService,
					ContainerID:     "817ec63d4b4f9e28947a323f9fbfc4596500b42c842bf07bd6ad9641e6805cb5",
					ContainerName:   "nginx_port_alt",
					ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "172.16.0.2", Port: 8080}},
					IPAddress:       "172.16.0.2",
					Active:          true,
					HasNetstatInfo:  true,
					LastNetstatInfo: t0,
					Config: config.Service{
						Port: 8080,
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			servicesOverrideMap, warnings := validateServices(tt.args.servicesOverride)
			if warnings != nil {
				t.Errorf("validateServices had warning: %s", warnings)
			}

			got := copyAndMergeServiceWithOverride(tt.args.discoveredServicesMap, servicesOverrideMap)
			applyOverrideInPlace(got)

			if diff := cmp.Diff(tt.want, got, cmpopts.IgnoreUnexported(Service{})); diff != "" {
				t.Errorf("applyOverride diff: (-want +got)\n %s", diff)
			}
		})
	}
}

func TestUpdateMetricsAndCheck(t *testing.T) {
	reg := &mockRegistry{
		ExpectedAddedContains: []string{"Service input nginx", "check for nginx"},
		NewIDs:                []int{42, 43},
	}
	mockDynamic := &MockDiscoverer{}
	docker := mockContainerInfo{
		containers: map[string]facts.FakeContainer{
			"1234": {},
		},
	}
	state := mockState{}

	disc, _ := New(mockDynamic, gloutonexec.New("/"), reg, state, nil, nil, nil, nil, nil, facts.ContainerFilter{}.ContainerIgnored, nil, time.Hour, config.OpenTelemetry{})
	disc.containerInfo = docker

	mockDynamic.result = []Service{
		{
			Name:            "nginx",
			Instance:        "nginx1",
			ServiceType:     NginxService,
			Active:          true,
			ContainerID:     "1234",
			ContainerName:   "nginx1",
			IPAddress:       "172.16.0.2",
			ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "172.16.0.2", Port: 80}},
		},
	}

	if _, _, err := disc.discovery(t.Context()); err != nil {
		t.Error(err)
	}

	if err := reg.ExpectationFulfilled(); err != nil {
		t.Error(err)
	}

	mockDynamic.result = []Service{
		{
			Name:            "nginx",
			Instance:        "nginx1",
			ServiceType:     NginxService,
			Active:          true,
			ContainerID:     "1234",
			ContainerName:   "nginx1",
			IPAddress:       "172.16.0.2",
			ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "172.16.0.2", Port: 80}},
		},
		{
			Name:            "memcached",
			Instance:        "",
			ServiceType:     MemcachedService,
			Active:          true,
			IPAddress:       "127.0.0.1",
			ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 11211}},
		},
	}
	reg.ExpectedAddedContains = []string{"Service input memcached", "check for memcached"}
	reg.NewIDs = []int{1337, 666}

	if _, _, err := disc.discovery(t.Context()); err != nil {
		t.Error(err)
	}

	if err := reg.ExpectationFulfilled(); err != nil {
		t.Error(err)
	}

	docker.containers = map[string]facts.FakeContainer{
		"1239": {},
	}
	mockDynamic.result = []Service{
		{
			Name:            "nginx",
			Instance:        "nginx1",
			ServiceType:     NginxService,
			Active:          true,
			ContainerID:     "1239",
			ContainerName:   "nginx1",
			IPAddress:       "172.16.0.2",
			ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "172.16.0.2", Port: 80}},
		},
		{
			Name:            "memcached",
			Instance:        "",
			ServiceType:     MemcachedService,
			Active:          true,
			IPAddress:       "127.0.0.1",
			ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 11211}},
		},
	}

	reg.ExpectedAddedContains = []string{"Service input nginx", "check for nginx"}
	reg.NewIDs = []int{9999, 99999}
	reg.ExpectedRemoveIDs = []int{42, 43}

	if _, _, err := disc.discovery(t.Context()); err != nil {
		t.Error(err)
	}

	if err := reg.ExpectationFulfilled(); err != nil {
		t.Error(err)
	}
}

func Test_usePreviousNetstat(t *testing.T) {
	t0 := time.Now()

	tests := []struct {
		name            string
		now             time.Time
		previousService Service
		newService      Service
		want            bool
	}{
		{
			name: "service restarted",
			now:  t0,
			previousService: Service{
				Name:            "nginx",
				ContainerID:     "",
				HasNetstatInfo:  true,
				LastNetstatInfo: t0.Add(-30 * time.Minute),
			},
			newService: Service{
				Name:            "nginx",
				ContainerID:     "",
				HasNetstatInfo:  false,
				LastNetstatInfo: time.Time{},
			},
			want: true,
		},
		{
			name: "service restarted, netstat available",
			now:  t0,
			previousService: Service{
				Name:            "nginx",
				ContainerID:     "",
				HasNetstatInfo:  true,
				LastNetstatInfo: t0.Add(-30 * time.Minute),
			},
			newService: Service{
				Name:            "nginx",
				ContainerID:     "",
				HasNetstatInfo:  true,
				LastNetstatInfo: t0,
			},
			want: false,
		},
		{
			name: "missing LastNetstatInfo on previous",
			now:  t0,
			previousService: Service{
				Name:            "nginx",
				ContainerID:     "",
				HasNetstatInfo:  true,
				LastNetstatInfo: time.Time{},
			},
			newService: Service{
				Name:            "nginx",
				ContainerID:     "",
				HasNetstatInfo:  false,
				LastNetstatInfo: time.Time{},
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := usePreviousNetstat(tt.now, tt.previousService, tt.newService); got != tt.want {
				t.Errorf("usePreviousNetstat() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidateServices(t *testing.T) {
	services := []config.Service{
		{
			Type:     "apache",
			Instance: "",
			Port:     80,
			Address:  "127.0.0.1",
			HTTPPath: "/",
			HTTPHost: "127.0.0.1:80",
		},
		{
			Type:     "apache",
			Instance: "",
			Port:     81,
			Address:  "127.0.0.1",
			HTTPPath: "/",
			HTTPHost: "127.0.0.1:80",
		},
		{
			Type:     "apache",
			Instance: "CONTAINER_NAME",
			Port:     80,
			Address:  "127.17.0.2",
			HTTPPath: "/",
			HTTPHost: "127.0.0.1:80",
		},
		{
			Type:     "apache",
			Instance: "CONTAINER_NAME",
			Port:     81,
			Address:  "127.17.0.2",
			HTTPPath: "/",
			HTTPHost: "127.0.0.1:80",
		},
		{
			CheckType:    "nagios",
			CheckCommand: "azerty",
		},
		{
			Type:         "myapplication",
			Port:         80,
			CheckType:    "nagios",
			CheckCommand: "command-to-run",
		},
		{
			Type:         " not fixable@",
			CheckType:    "nagios",
			CheckCommand: "azerty",
		},
		{
			Type:      "custom_webserver",
			Port:      8181,
			CheckType: "http",
		},
		{
			Type:         "custom-bad.name",
			CheckType:    "nagios",
			CheckCommand: "azerty",
		},
		{
			Type:     "ssl_and_starttls",
			SSL:      true,
			StartTLS: true,
		},
		{
			Type:          "good_stats_protocol",
			StatsProtocol: "http",
		},
		{
			Type:          "bad_stats_protocol",
			StatsProtocol: "bad",
		},
	}

	wantWarnings := []string{
		"invalid config value: a service override is duplicated for 'apache'",
		"invalid config value: a service override is duplicated for 'apache' on instance 'CONTAINER_NAME'",
		"invalid config value: the key \"type\" is missing in one of your service override",
		"invalid config value: service type \" not fixable@\" can only contains letters, digits and underscore",
		"invalid config value: service type \"custom-bad.name\" can not contains dot (.) or dash (-). Changed to \"custom_bad_name\"",
		"invalid config value: service 'ssl_and_starttls' can't set both SSL and StartTLS, StartTLS will be used",
		"invalid config value: service 'bad_stats_protocol' has an unsupported stats protocol: 'bad'",
	}

	wantServices := map[NameInstance]config.Service{
		{
			Name:     "apache",
			Instance: "",
		}: {
			Type:     "apache",
			Instance: "",
			Port:     81,
			Address:  "127.0.0.1",
			HTTPPath: "/",
			HTTPHost: "127.0.0.1:80",
		},
		{
			Name:     "apache",
			Instance: "CONTAINER_NAME",
		}: {
			Type:     "apache",
			Instance: "CONTAINER_NAME",
			Port:     81,
			Address:  "127.17.0.2",
			HTTPPath: "/",
			HTTPHost: "127.0.0.1:80",
		},
		{
			Name:     "myapplication",
			Instance: "",
		}: {
			Type:         "myapplication",
			Port:         80,
			CheckType:    "nagios",
			CheckCommand: "command-to-run",
		},
		{
			Name:     "custom_webserver",
			Instance: "",
		}: {
			Type:      "custom_webserver",
			Port:      8181,
			CheckType: "http",
		},
		{
			Name:     "custom_bad_name",
			Instance: "",
		}: {
			Type:         "custom_bad_name",
			CheckType:    "nagios",
			CheckCommand: "azerty",
		},
		{
			Name: "ssl_and_starttls",
		}: {
			Type:     "ssl_and_starttls",
			SSL:      false,
			StartTLS: true,
		},
		{
			Name: "good_stats_protocol",
		}: {
			Type:          "good_stats_protocol",
			StatsProtocol: "http",
		},
		{
			Name: "bad_stats_protocol",
		}: {
			Type:          "bad_stats_protocol",
			StatsProtocol: "",
		},
	}

	gotServices, gotWarnings := validateServices(services)

	if diff := cmp.Diff(gotServices, wantServices); diff != "" {
		t.Fatalf("Validate returned unexpected services:\n%s", diff)
	}

	gotWarningsStr := make([]string, 0, len(gotWarnings))
	for _, warning := range gotWarnings {
		gotWarningsStr = append(gotWarningsStr, warning.Error())
	}

	if diff := cmp.Diff(gotWarningsStr, wantWarnings); diff != "" {
		t.Fatalf("Validate returned unexpected warnings:\n%s", diff)
	}
}

func Test_servicesFromState(t *testing.T) {
	tests := []struct {
		name              string
		stateFileBaseName string
		want              []Service
	}{
		{
			name:              "no-version",
			stateFileBaseName: "no-version",
			want: []Service{
				{
					Name:          "redis",
					Instance:      "redis",
					ContainerID:   "399366e861976b77e5574c6b956f70dd2473944d822196e8bd6735da7e1d373f",
					ContainerName: "redis",
					IPAddress:     "172.17.0.2",
					ListenAddresses: []facts.ListenAddress{
						{NetworkFamily: "tcp", Address: "172.17.0.2", Port: 6379},
					},
					ExePath:     "/usr/local/bin/redis-server",
					ServiceType: RedisService,
				},
			},
		},
		{
			name:              "no-version-port-conflict",
			stateFileBaseName: "no-version-port-conflict",
			want: []Service{
				{
					Active:        true,
					Name:          "nginx",
					Instance:      "composetest-phpfpm_and_nginx-1",
					ContainerID:   "231aa25b7994847ea8b672cff7cd1d6a95a301dacece589982fd0de78470d7e3",
					ContainerName: "composetest-phpfpm_and_nginx-1",
					IPAddress:     "172.18.0.7",
					ListenAddresses: []facts.ListenAddress{
						{NetworkFamily: "tcp", Address: "172.18.0.7", Port: 80},
					},
					ServiceType:     NginxService,
					HasNetstatInfo:  true,
					LastNetstatInfo: time.Date(2023, 6, 20, 15, 18, 37, 267699930, time.UTC),
				},
				{
					Name:            "phpfpm",
					Instance:        "composetest-phpfpm_and_nginx-1",
					ContainerID:     "231aa25b7994847ea8b672cff7cd1d6a95a301dacece589982fd0de78470d7e3",
					ContainerName:   "composetest-phpfpm_and_nginx-1",
					IPAddress:       "172.18.0.7",
					ListenAddresses: []facts.ListenAddress{},
					ServiceType:     PHPFPMService,
					Active:          true,
					HasNetstatInfo:  false,
				},
				{
					Active:          true,
					Name:            "nginx",
					Instance:        "conflict-other-port",
					ContainerID:     "741852963",
					ContainerName:   "conflict-other-port",
					IPAddress:       "172.18.0.7",
					ListenAddresses: []facts.ListenAddress{},
					ServiceType:     NginxService,
					HasNetstatInfo:  false,
				},
				{
					Name:            "phpfpm",
					Instance:        "conflict-other-port",
					ContainerID:     "741852963",
					ContainerName:   "conflict-other-port",
					IPAddress:       "172.18.0.7",
					ListenAddresses: []facts.ListenAddress{},
					ServiceType:     PHPFPMService,
					Active:          true,
					HasNetstatInfo:  false,
				},
				{
					Active:        true,
					Name:          "postgresql",
					Instance:      "",
					ContainerID:   "",
					ContainerName: "",
					IPAddress:     "127.0.0.1",
					ListenAddresses: []facts.ListenAddress{
						{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 5432},
						{NetworkFamily: "unix", Address: "/var/run/postgresql/.s.PGSQL.5432", Port: 0},
					},
					ServiceType:     PostgreSQLService,
					HasNetstatInfo:  true,
					LastNetstatInfo: time.Date(2023, 6, 20, 15, 18, 37, 249753388, time.UTC),
				},
				{
					Active:        true,
					Name:          "nginx",
					Instance:      "conflict-inactive",
					ContainerID:   "123456789",
					ContainerName: "conflict-inactive",
					IPAddress:     "172.18.0.9",
					ListenAddresses: []facts.ListenAddress{
						{NetworkFamily: "tcp", Address: "172.18.0.9", Port: 80},
					},
					ServiceType:     NginxService,
					HasNetstatInfo:  true,
					LastNetstatInfo: time.Date(2023, 6, 20, 15, 18, 37, 267699930, time.UTC),
				},
				{
					Active:          false,
					Name:            "phpfpm",
					Instance:        "conflict-inactive",
					ContainerID:     "123456789",
					ContainerName:   "conflict-inactive",
					IPAddress:       "172.18.0.9",
					ListenAddresses: []facts.ListenAddress{},
					ServiceType:     PHPFPMService,
					HasNetstatInfo:  false,
				},
			},
		},
		{
			// This state is likely impossible to produce. fixListenAddressConflict should have remove the
			// listening address on one service.
			// This state was made-up but allow to test that migration is only applied when migrating from v0 to v1.
			name:              "version1-no-two-migration",
			stateFileBaseName: "version1-no-two-migration",
			want: []Service{
				{
					Active:        true,
					Name:          "nginx",
					Instance:      "",
					ContainerID:   "",
					ContainerName: "",
					IPAddress:     "127.0.0.1",
					ListenAddresses: []facts.ListenAddress{
						{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 80},
					},
					ServiceType:     NginxService,
					HasNetstatInfo:  true,
					LastNetstatInfo: time.Date(2023, 6, 20, 15, 18, 37, 267699930, time.UTC),
				},
				{
					Active:        true,
					Name:          "phpfpm",
					Instance:      "",
					ContainerID:   "",
					ContainerName: "",
					IPAddress:     "127.0.0.1",
					ListenAddresses: []facts.ListenAddress{
						{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 80},
					},
					ServiceType:     PHPFPMService,
					HasNetstatInfo:  true,
					LastNetstatInfo: time.Date(2023, 6, 20, 15, 18, 37, 267699930, time.UTC),
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			memoryState, err := state.LoadReadOnly(
				fmt.Sprintf("testdata/state-%s.json", tt.stateFileBaseName),
				fmt.Sprintf("testdata/state-%s.cache.json", tt.stateFileBaseName),
			)
			if err != nil {
				t.Fatal(err)
			}

			cmpOptions := []cmp.Option{
				cmpopts.IgnoreUnexported(Service{}),
				cmpopts.IgnoreFields(Service{}, "LastTimeSeen"),
				cmpopts.EquateEmpty(),
				cmpopts.SortSlices(func(x Service, y Service) bool {
					if x.Name < y.Name {
						return true
					}

					if x.Name == y.Name && x.Instance < y.Instance {
						return true
					}

					return false
				}),
			}

			got := servicesFromState(memoryState)

			if diff := cmp.Diff(tt.want, got, cmpOptions...); diff != "" {
				t.Errorf("servicesFromState() mismatch (-want +got)\n%s", diff)
			}

			// Check that write/re-read yield the same result
			emptyState, err := state.LoadReadOnly("", "")
			if err != nil {
				t.Fatal(err)
			}

			saveState(emptyState, serviceListToMap(got))

			got = servicesFromState(emptyState)

			if diff := cmp.Diff(tt.want, got, cmpOptions...); diff != "" {
				t.Errorf("servicesFromState(saveState()) mismatch (-want +got)\n%s", diff)
			}
		})
	}
}
