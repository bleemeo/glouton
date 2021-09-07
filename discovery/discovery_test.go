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

package discovery

import (
	"context"
	"errors"
	"fmt"
	"glouton/facts"
	"glouton/types"
	"reflect"
	"testing"

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
			},
			want: Service{
				Name:            "memcached",
				ServiceType:     MemcachedService,
				ContainerID:     "",
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 11211}},
				IPAddress:       "127.0.0.1",
				HasNetstatInfo:  true,
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
		disc := New(&MockDiscoverer{result: []Service{c.dynamicResult}}, nil, nil, nil, state, nil, mockContainerInfo{}, nil, nil, nil, types.MetricFormatBleemeo)

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

func Test_applyOveride(t *testing.T) {
	type args struct {
		discoveredServicesMap map[NameContainer]Service
		servicesOverride      map[NameContainer]map[string]string
	}

	tests := []struct {
		name string
		args args
		want map[NameContainer]Service
	}{
		{
			name: "empty",
			args: args{
				discoveredServicesMap: nil,
				servicesOverride:      nil,
			},
			want: make(map[NameContainer]Service),
		},
		{
			name: "no override",
			args: args{
				discoveredServicesMap: map[NameContainer]Service{
					{Name: "apache"}: {
						Name:            "apache",
						ServiceType:     ApacheService,
						IPAddress:       "127.0.0.1",
						ListenAddresses: []facts.ListenAddress{},
					},
				},
				servicesOverride: nil,
			},
			want: map[NameContainer]Service{
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
				discoveredServicesMap: map[NameContainer]Service{
					{Name: "apache"}: {
						Name:        "apache",
						ServiceType: ApacheService,
					},
				},
				servicesOverride: map[NameContainer]map[string]string{
					{Name: "apache"}: {
						"address": "10.0.1.2",
					},
				},
			},
			want: map[NameContainer]Service{
				{Name: "apache"}: {
					Name:        "apache",
					ServiceType: ApacheService,
					ExtraAttributes: map[string]string{
						"address": "10.0.1.2",
					},
				},
			},
		},
		{
			name: "address override & ignore unknown override",
			args: args{
				discoveredServicesMap: map[NameContainer]Service{
					{Name: "apache"}: {
						Name:        "apache",
						ServiceType: ApacheService,
					},
				},
				servicesOverride: map[NameContainer]map[string]string{
					{Name: "apache"}: {
						"address":         "10.0.1.2",
						"this-is-unknown": "so-unused",
					},
				},
			},
			want: map[NameContainer]Service{
				{Name: "apache"}: {
					Name:        "apache",
					ServiceType: ApacheService,
					ExtraAttributes: map[string]string{
						"address": "10.0.1.2",
					},
				},
			},
		},
		{
			name: "add custom check",
			args: args{
				discoveredServicesMap: map[NameContainer]Service{
					{Name: "apache"}: {
						Name:        "apache",
						ServiceType: ApacheService,
					},
				},
				servicesOverride: map[NameContainer]map[string]string{
					{Name: "myapplication"}: {
						"port":          "8080",
						"check_type":    customCheckNagios,
						"check_command": "command-to-run",
					},
					{Name: "custom_webserver"}: {
						"port": "8081",
					},
				},
			},
			want: map[NameContainer]Service{
				{Name: "apache"}: {
					Name:        "apache",
					ServiceType: ApacheService,
				},
				{Name: "myapplication"}: {
					ServiceType: CustomService,
					ExtraAttributes: map[string]string{
						"address":       "127.0.0.1", // default as soon as port is set
						"port":          "8080",
						"check_type":    customCheckNagios,
						"check_command": "command-to-run",
					},
					Name:   "myapplication",
					Active: true,
				},
				{Name: "custom_webserver"}: {
					ServiceType: CustomService,
					ExtraAttributes: map[string]string{
						"address":    "127.0.0.1", // default as soon as port is set
						"port":       "8081",
						"check_type": customCheckTCP, // default as soon as port is set
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
				servicesOverride: map[NameContainer]map[string]string{
					{Name: "myapplication"}: { // the check_command is missing
						"port":       "8080",
						"check_type": customCheckNagios,
					},
					{Name: "custom_webserver"}: { // port is missing
						"check_type": customCheckHTTP,
					},
				},
			},
			want: map[NameContainer]Service{},
		},
		{
			name: "ignore ports",
			args: args{
				discoveredServicesMap: map[NameContainer]Service{
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
				servicesOverride: map[NameContainer]map[string]string{
					{Name: "apache"}: {
						"ignore_ports": "443,22",
					},
				},
			},
			want: map[NameContainer]Service{
				{Name: "apache"}: {
					Name:        "apache",
					ServiceType: ApacheService,
					IPAddress:   "127.0.0.1",
					ListenAddresses: []facts.ListenAddress{
						{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 80},
						// It's not applyOveride which remove ignored ports
						{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 443},
					},
					IgnoredPorts: map[int]bool{
						22:  true,
						443: true,
					},
					ExtraAttributes: map[string]string{},
				},
			},
		},
		{
			name: "ignore ports with space",
			args: args{
				discoveredServicesMap: map[NameContainer]Service{
					{Name: "apache"}: {
						Name:        "apache",
						ServiceType: ApacheService,
					},
				},
				servicesOverride: map[NameContainer]map[string]string{
					{Name: "apache"}: {
						"ignore_ports": "   443  , 22   ",
					},
				},
			},
			want: map[NameContainer]Service{
				{Name: "apache"}: {
					Name:        "apache",
					ServiceType: ApacheService,
					IgnoredPorts: map[int]bool{
						22:  true,
						443: true,
					},
					ExtraAttributes: map[string]string{},
				},
			},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			if got := applyOveride(tt.args.discoveredServicesMap, tt.args.servicesOverride); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("applyOveride() = %#v, want %#v", got, tt.want)
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
	disc := New(mockDynamic, fakeCollector, nil, nil, state, nil, nil, nil, nil, nil, types.MetricFormatBleemeo)
	disc.containerInfo = docker

	mockDynamic.result = []Service{
		{
			Name:            "nginx",
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
			ServiceType:     NginxService,
			Active:          true,
			ContainerID:     "1234",
			ContainerName:   "nginx1",
			IPAddress:       "172.16.0.2",
			ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "172.16.0.2", Port: 80}},
		},
		{
			Name:            "memcached",
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
			ServiceType:     NginxService,
			Active:          true,
			ContainerID:     "1239",
			ContainerName:   "nginx1",
			IPAddress:       "172.16.0.2",
			ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "172.16.0.2", Port: 80}},
		},
		{
			Name:            "memcached",
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
