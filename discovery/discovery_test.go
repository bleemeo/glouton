// Copyright 2015-2022 Bleemeo
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
	"context"
	"errors"
	"fmt"
	"glouton/config"
	"glouton/facts"
	"glouton/prometheus/registry"
	"glouton/types"
	"reflect"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/influxdata/telegraf"
)

var (
	errNotImplemented = errors.New("not implemented")
	errNotCalled      = errors.New("not called, want call")
	errWantName       = errors.New("want name")
)

type mockState struct {
	DiscoveredService []Service
}

func (ms mockState) Set(key string, object interface{}) error {
	return errNotImplemented
}

func (ms mockState) Get(key string, object interface{}) error {
	if services, ok := object.(*[]Service); ok {
		*services = ms.DiscoveredService

		return nil
	}

	return errNotImplemented
}

type mockCollector struct {
	ExpectedAddedName string
	NewID             int
	ExpectedRemoveID  int
	err               error
}

func (m *mockCollector) AddInput(_ telegraf.Input, name string) (int, error) {
	if name != m.ExpectedAddedName {
		m.err = fmt.Errorf("AddInput(_, %s), %w=%s", name, errWantName, m.ExpectedAddedName)

		return 0, m.err
	}

	m.ExpectedAddedName = ""

	return m.NewID, nil
}

func (m *mockCollector) RemoveInput(id int) {
	if id != m.ExpectedRemoveID {
		m.err = fmt.Errorf("RemoveInput(%d), %w=%d", id, errWantName, m.ExpectedRemoveID)

		return
	}

	m.ExpectedRemoveID = 0
}

func (m *mockCollector) ExpectationFullified() error {
	if m.err != nil {
		return m.err
	}

	if m.ExpectedAddedName != "" {
		return fmt.Errorf("AddInput() %w with name=%s", errNotCalled, m.ExpectedAddedName)
	}

	if m.ExpectedRemoveID != 0 {
		return fmt.Errorf("RemoveInput() %w with id=%d", errNotCalled, m.ExpectedRemoveID)
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

	ctx := context.Background()

	for i, c := range cases {
		var previousService []Service

		if c.previousService.ServiceType != "" {
			previousService = append(previousService, c.previousService)
		}

		state := mockState{
			DiscoveredService: previousService,
		}
		disc, _ := New(&MockDiscoverer{result: []Service{c.dynamicResult}}, nil, nil, nil, state, nil, mockContainerInfo{}, nil, nil, nil, facts.ContainerFilter{}.ContainerIgnored, types.MetricFormatBleemeo, nil)

		srv, err := disc.Discovery(ctx, 0)
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

func Test_applyOverride(t *testing.T) {
	type args struct {
		discoveredServicesMap map[NameInstance]Service
		servicesOverride      map[NameInstance]config.Service
	}

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
				servicesOverride: map[NameInstance]config.Service{
					{Name: "apache"}: {
						Address: "10.0.1.2",
					},
				},
			},
			want: map[NameInstance]Service{
				{Name: "apache"}: {
					Name:        "apache",
					ServiceType: ApacheService,
					Config: config.Service{
						Address: "10.0.1.2",
					},
					ListenAddresses: []facts.ListenAddress{
						{
							NetworkFamily: "tcp",
							Address:       "10.0.1.2",
							Port:          80,
						},
					},
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
				servicesOverride: map[NameInstance]config.Service{
					{Name: "myapplication"}: {
						Port:         8080,
						CheckType:    customCheckNagios,
						CheckCommand: "command-to-run",
					},
					{Name: "custom_webserver"}: {
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
				servicesOverride: map[NameInstance]config.Service{
					{Name: "myapplication"}: { // the check_command is missing
						Port:      8080,
						CheckType: customCheckNagios,
					},
					{Name: "custom_webserver"}: { // port is missing
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
				servicesOverride: map[NameInstance]config.Service{
					{Name: "apache"}: {
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
				servicesOverride: map[NameInstance]config.Service{
					{Name: "apache"}: {
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
						IgnorePorts: []int{443, 22},
					},
				},
			},
		},
		{
			name: "override stack",
			args: args{
				discoveredServicesMap: map[NameInstance]Service{
					{Name: "apache"}: {
						Name:        "apache",
						ServiceType: ApacheService,
					},
				},
				servicesOverride: map[NameInstance]config.Service{
					{Name: "apache"}: {
						Stack: "website",
					},
				},
			},
			want: map[NameInstance]Service{
				{Name: "apache"}: {
					Name:        "apache",
					ServiceType: ApacheService,
					Stack:       "website",
					Config: config.Service{
						Stack: "website",
					},
				},
			},
		},
		{
			name: "no override stack",
			args: args{
				discoveredServicesMap: map[NameInstance]Service{
					{Name: "apache"}: {
						Name:        "apache",
						ServiceType: ApacheService,
						Stack:       "website",
					},
				},
				servicesOverride: map[NameInstance]config.Service{
					{Name: "apache"}: {
						Stack: "",
					},
				},
			},
			want: map[NameInstance]Service{
				{Name: "apache"}: {
					Name:        "apache",
					ServiceType: ApacheService,
					Stack:       "website",
					Config: config.Service{
						Stack: "",
					},
				},
			},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got := applyOverride(tt.args.discoveredServicesMap, tt.args.servicesOverride)
			if diff := cmp.Diff(tt.want, got, cmpopts.IgnoreUnexported(Service{})); diff != "" {
				t.Errorf("applyOverride diff:\n %s", diff)
			}
		})
	}
}

func TestUpdateMetricsAndCheck(t *testing.T) {
	fakeCollector := &mockCollector{
		ExpectedAddedName: "nginx",
		NewID:             42,
	}
	mockDynamic := &MockDiscoverer{}
	docker := mockContainerInfo{
		containers: map[string]facts.FakeContainer{
			"1234": {},
		},
	}
	state := mockState{}

	reg, err := registry.New(registry.Option{})
	if err != nil {
		t.Fatal(err)
	}

	disc, _ := New(mockDynamic, fakeCollector, reg, nil, state, nil, nil, nil, nil, nil, facts.ContainerFilter{}.ContainerIgnored, types.MetricFormatBleemeo, nil)
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

	if _, err := disc.Discovery(context.Background(), 0); err != nil {
		t.Error(err)
	}

	if err := fakeCollector.ExpectationFullified(); err != nil {
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
	fakeCollector.ExpectedAddedName = "memcached"
	fakeCollector.NewID = 1337

	if _, err := disc.Discovery(context.Background(), 0); err != nil {
		t.Error(err)
	}

	if err := fakeCollector.ExpectationFullified(); err != nil {
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
	fakeCollector.ExpectedAddedName = "nginx"
	fakeCollector.NewID = 9999
	fakeCollector.ExpectedRemoveID = 42

	if _, err := disc.Discovery(context.Background(), 0); err != nil {
		t.Error(err)
	}

	if err := fakeCollector.ExpectationFullified(); err != nil {
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
		tt := tt

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
			ID:       "apache",
			Instance: "",
			Port:     80,
			Address:  "127.0.0.1",
			HTTPPath: "/",
			HTTPHost: "127.0.0.1:80",
		},
		{
			ID:       "apache",
			Instance: "",
			Port:     81,
			Address:  "127.0.0.1",
			HTTPPath: "/",
			HTTPHost: "127.0.0.1:80",
		},
		{
			ID:       "apache",
			Instance: "CONTAINER_NAME",
			Port:     80,
			Address:  "127.17.0.2",
			HTTPPath: "/",
			HTTPHost: "127.0.0.1:80",
		},
		{
			ID:       "apache",
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
			ID:           "myapplication",
			Port:         80,
			CheckType:    "nagios",
			CheckCommand: "command-to-run",
		},
		{
			ID:           " not fixable@",
			CheckType:    "nagios",
			CheckCommand: "azerty",
		},
		{
			ID:        "custom_webserver",
			Port:      8181,
			CheckType: "http",
		},
		{
			ID:           "custom-bad.name",
			CheckType:    "nagios",
			CheckCommand: "azerty",
		},
	}

	wantWarnings := []string{
		"invalid config value: a service override is duplicated for 'apache'",
		"invalid config value: a service override is duplicated for 'apache' on instance 'CONTAINER_NAME'",
		"invalid config value: a key \"id\" is missing in one of your service override",
		"invalid config value: service id \" not fixable@\" can only contains letters, digits and underscore",
		"invalid config value: service id \"custom-bad.name\" can not contains dot (.) or dash (-). Changed to \"custom_bad_name\"",
	}

	wantServices := map[NameInstance]config.Service{
		{
			Name:     "apache",
			Instance: "",
		}: {
			ID:       "apache",
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
			ID:       "apache",
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
			ID:           "myapplication",
			Port:         80,
			CheckType:    "nagios",
			CheckCommand: "command-to-run",
		},
		{
			Name:     "custom_webserver",
			Instance: "",
		}: {
			ID:        "custom_webserver",
			Port:      8181,
			CheckType: "http",
		},
		{
			Name:     "custom_bad_name",
			Instance: "",
		}: {
			ID:           "custom_bad_name",
			CheckType:    "nagios",
			CheckCommand: "azerty",
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
