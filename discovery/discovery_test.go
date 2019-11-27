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
	"glouton/facts"
	"reflect"
	"testing"
	"time"
)

type mockDiscoverer struct {
	result []Service
}

func (md mockDiscoverer) Discovery(ctx context.Context, maxAge time.Duration) (services []Service, err error) {
	return md.result, nil
}

func (md mockDiscoverer) LastUpdate() time.Time {
	return time.Now()
}

type mockState struct {
	DiscoveredService []Service
}

func (ms mockState) Set(key string, object interface{}) error {
	return errors.New("not implemented")
}

func (ms mockState) Get(key string, object interface{}) error {
	if services, ok := object.(*[]Service); ok {
		*services = ms.DiscoveredService
		return nil
	}
	return errors.New("not implemented")
}

// Test dynamic Discovery with single service present
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
		disc := New(mockDiscoverer{result: []Service{c.dynamicResult}}, nil, nil, state, nil, nil, nil)

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
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			if got := applyOveride(tt.args.discoveredServicesMap, tt.args.servicesOverride); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("applyOveride() = %v, want %v", got, tt.want)
			}
		})
	}
}
