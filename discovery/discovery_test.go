// Copyright 2015-2026 Bleemeo
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
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bleemeo/glouton/agent/state"
	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/facts"
	"github.com/bleemeo/glouton/prometheus/registry"
	"github.com/bleemeo/glouton/types"
	"github.com/bleemeo/glouton/utils/gloutonexec"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/influxdata/telegraf"
	"github.com/prometheus/client_golang/prometheus"
)

const (
	testApache           = string(ApacheService)
	testNginx            = string(NginxService)
	testRedis            = string(RedisService)
	testMemcached        = string(MemcachedService)
	testKafka            = string(KafkaService)
	testPHPFPM           = string(PHPFPMService)
	testPostgreSQL       = string(PostgreSQLService)
	testMyApplication    = "myapplication"
	testCommandToRun     = "command-to-run"
	testCustomWebserver  = "custom_webserver"
	testWebsite          = "website"
	testJMXCustom        = "jmx_custom"
	testDuplicate        = "duplicate"
	testNginx1           = "nginx1"
	testNoopFmt          = "noop-fmt"
	testNoopFlt          = "noop-flt"
	testContainerName    = "CONTAINER_NAME"
	testAzerty           = "azerty"
	testSSLAndStartTLS   = "ssl_and_starttls"
	testGoodStatsProto   = "good_stats_protocol"
	testBadStatsProto    = "bad_stats_protocol"
	testBadLogCfg        = "bad_log_cfg"
	testBadLogFiles      = "bad_log_files"
	testComposePHPNginx  = "composetest-phpfpm_and_nginx-1"
	testConflictOther    = "conflict-other-port"
	testConflictInactive = "conflict-inactive"

	testIP127001   = "127.0.0.1"
	testIP10005    = "10.0.0.5"
	testIP10012    = "10.0.1.2"
	testIP1721602  = "172.16.0.2"
	testIP1721807  = "172.18.0.7"
	testIP1721809  = "172.18.0.9"
	testIP12717002 = "127.17.0.2"
	testAddr127080 = "127.0.0.1:80"

	testContainerHash = "817ec63d4b4f9e28947a323f9fbfc4596500b42c842bf07bd6ad9641e6805cb5"
)

var (
	errNotImplemented  = errors.New("not implemented")
	errUnregisterTwice = errors.New("calling unregistred twice")
	errNotCalled       = errors.New("not called, want call")
	errWantName        = errors.New("want name")
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
	l                     sync.Mutex
	regs                  []*mockRegistration
	ExpectedAddedContains []string
	err                   error
}

type mockRegistration struct {
	l                   sync.Mutex
	reg                 *mockRegistry
	opt                 registry.RegistrationOption
	unregisterWasCalled bool
}

func (m *mockRegistry) RegisterGatherer(opt registry.RegistrationOption, gatherer prometheus.Gatherer) (types.Registration, error) {
	_ = gatherer

	if len(m.ExpectedAddedContains) == 0 {
		m.err = fmt.Errorf("%w: RegisterGatherer() ExpectedAddedContains empty when called with description %s", errWantName, opt.Description)
	}

	if !strings.Contains(opt.Description, m.ExpectedAddedContains[0]) {
		m.err = fmt.Errorf("%w: RegisterGatherer() Description=%s, want %s", errWantName, opt.Description, m.ExpectedAddedContains[0])

		return nil, m.err
	}

	m.ExpectedAddedContains = m.ExpectedAddedContains[1:]

	r := &mockRegistration{reg: m, opt: opt}

	m.l.Lock()
	defer m.l.Unlock()

	m.regs = append(m.regs, r)

	return r, nil
}

func (m *mockRegistry) RegisterInput(opt registry.RegistrationOption, input telegraf.Input) (types.Registration, error) {
	_ = input

	if len(m.ExpectedAddedContains) == 0 {
		m.err = fmt.Errorf("%w: RegisterInput() ExpectedAddedContains empty when called with description %s", errWantName, opt.Description)
	}

	if !strings.Contains(opt.Description, m.ExpectedAddedContains[0]) {
		m.err = fmt.Errorf("%w: RegisterInput() Description=%s, want %s", errWantName, opt.Description, m.ExpectedAddedContains[0])

		return nil, m.err
	}

	m.ExpectedAddedContains = m.ExpectedAddedContains[1:]

	r := &mockRegistration{reg: m, opt: opt}

	m.l.Lock()
	defer m.l.Unlock()

	m.regs = append(m.regs, r)

	return r, nil
}

func (m *mockRegistry) RegistrationByDescription(desc string) *mockRegistration {
	m.l.Lock()
	defer m.l.Unlock()

	var found *mockRegistration

	for _, row := range m.regs {
		if row.opt.Description == desc {
			if found != nil {
				panic("multiple match")
			}

			found = row
		}
	}

	return found
}

func (m *mockRegistry) GetAllRegistrations() []*mockRegistration {
	m.l.Lock()
	defer m.l.Unlock()

	return slices.Clone(m.regs)
}

func (m *mockRegistration) Unregister() {
	m.l.Lock()
	defer m.l.Unlock()

	if m.unregisterWasCalled {
		m.reg.err = fmt.Errorf("%w on reg with description %v", errUnregisterTwice, m.opt.Description)
	}

	m.unregisterWasCalled = true
}

func (m *mockRegistration) ScheduleRun(_ types.ScheduleOption) {
	m.reg.err = fmt.Errorf("unexpected call to ScheduleRun: %w", errNotImplemented)
}

func (m *mockRegistration) InternalRunScrape(ctx context.Context, loopCtx context.Context, t0 time.Time) {
	_ = ctx
	_ = loopCtx
	_ = t0

	m.reg.err = fmt.Errorf("unexpected call to InternalRunScrape: %w", errNotImplemented)
}

func (m *mockRegistry) ExpectationFulfilled() error {
	if m.err != nil {
		return m.err
	}

	if len(m.ExpectedAddedContains) > 0 {
		return fmt.Errorf("%w: Register*() missing: %v", errNotCalled, m.ExpectedAddedContains)
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
				Name:            testMemcached,
				ServiceType:     MemcachedService,
				ContainerID:     "",
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 11211}},
				IPAddress:       testIP127001,
				HasNetstatInfo:  true,
				LastNetstatInfo: t0,
			},
			want: Service{
				Name:            testMemcached,
				ServiceType:     MemcachedService,
				ContainerID:     "",
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 11211}},
				IPAddress:       testIP127001,
				HasNetstatInfo:  true,
				LastNetstatInfo: t0,
			},
		},
		{
			previousService: Service{
				Name:            testMemcached,
				ServiceType:     MemcachedService,
				ContainerID:     "",
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 11211}},
				IPAddress:       testIP127001,
				HasNetstatInfo:  true,
				LastNetstatInfo: t0,
			},
			dynamicResult: Service{
				Name:            testMemcached,
				ServiceType:     MemcachedService,
				ContainerID:     "",
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 11211}},
				IPAddress:       testIP127001,
				HasNetstatInfo:  true,
				LastNetstatInfo: t0,
			},
			want: Service{
				Name:            testMemcached,
				ServiceType:     MemcachedService,
				ContainerID:     "",
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 11211}},
				IPAddress:       testIP127001,
				HasNetstatInfo:  true,
				LastNetstatInfo: t0,
			},
		},
		{
			previousService: Service{
				Name:            testMemcached,
				ServiceType:     MemcachedService,
				ContainerID:     "",
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP10005, Port: 11211}},
				IPAddress:       testIP10005,
				HasNetstatInfo:  true,
				LastNetstatInfo: t0,
			},
			dynamicResult: Service{
				Name:            testMemcached,
				ServiceType:     MemcachedService,
				ContainerID:     "",
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 11211}},
				IPAddress:       "",
				HasNetstatInfo:  false,
			},
			want: Service{
				Name:            testMemcached,
				ServiceType:     MemcachedService,
				ContainerID:     "",
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP10005, Port: 11211}},
				IPAddress:       testIP10005,
				HasNetstatInfo:  true,
				LastNetstatInfo: t0,
			},
		},
		{
			previousService: Service{
				Name:            testMemcached,
				ServiceType:     MemcachedService,
				ContainerID:     "",
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP10005, Port: 11211}},
				IPAddress:       testIP10005,
				HasNetstatInfo:  true,
				LastNetstatInfo: t0,
			},
			dynamicResult: Service{
				Name:            testMemcached,
				ServiceType:     MemcachedService,
				ContainerID:     "",
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 11211}},
				IPAddress:       testIP127001,
				HasNetstatInfo:  true,
			},
			want: Service{
				Name:            testMemcached,
				ServiceType:     MemcachedService,
				ContainerID:     "",
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 11211}},
				IPAddress:       testIP127001,
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
					{Name: testApache}: {
						Name:            testApache,
						ServiceType:     ApacheService,
						IPAddress:       testIP127001,
						ListenAddresses: []facts.ListenAddress{},
					},
				},
				servicesOverride: nil,
			},
			want: map[NameInstance]Service{
				{Name: testApache}: {
					Name:            testApache,
					ServiceType:     ApacheService,
					IPAddress:       testIP127001,
					ListenAddresses: []facts.ListenAddress{},
				},
			},
		},
		{
			name: "address override",
			args: args{
				discoveredServicesMap: map[NameInstance]Service{
					{Name: testApache}: {
						Name:        testApache,
						ServiceType: ApacheService,
					},
				},
				servicesOverride: []config.Service{
					{
						Type:    testApache,
						Address: testIP10012,
					},
				},
			},
			want: map[NameInstance]Service{
				{Name: testApache}: {
					Name:        testApache,
					ServiceType: ApacheService,
					Config: config.Service{
						Type:    testApache,
						Address: testIP10012,
					},
					ListenAddresses: []facts.ListenAddress{
						{
							NetworkFamily: tcpProtocol,
							Address:       testIP10012,
							Port:          80,
						},
					},
					IPAddress: testIP10012,
				},
			},
		},
		{
			name: "add custom check",
			args: args{
				discoveredServicesMap: map[NameInstance]Service{
					{Name: testApache}: {
						Name:        testApache,
						ServiceType: ApacheService,
					},
				},
				servicesOverride: []config.Service{
					{
						Type:         testMyApplication,
						Port:         8080,
						CheckType:    customCheckNagios,
						CheckCommand: testCommandToRun,
					},
					{
						Type: testCustomWebserver,
						Port: 8081,
					},
				},
			},
			want: map[NameInstance]Service{
				{Name: testApache}: {
					Name:        testApache,
					ServiceType: ApacheService,
				},
				{Name: testMyApplication}: {
					ServiceType: CustomService,
					Config: config.Service{
						Type:         testMyApplication,
						Address:      testIP127001, // default as soon as port is set
						Port:         8080,
						CheckType:    customCheckNagios,
						CheckCommand: testCommandToRun,
					},
					Name:   testMyApplication,
					Active: true,
				},
				{Name: testCustomWebserver}: {
					ServiceType: CustomService,
					Config: config.Service{
						Type:      testCustomWebserver,
						Address:   testIP127001, // default as soon as port is set
						Port:      8081,
						CheckType: customCheckTCP, // default as soon as port is set,
					},
					Name:   testCustomWebserver,
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
						Type:      testMyApplication,
						Port:      8080,
						CheckType: customCheckNagios,
					},
					{ // port is missing
						Type:      testCustomWebserver,
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
					{Name: testApache}: {
						Name:        testApache,
						ServiceType: ApacheService,
						IPAddress:   testIP127001,
						ListenAddresses: []facts.ListenAddress{
							{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 80},
							{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 443},
						},
					},
				},
				servicesOverride: []config.Service{
					{
						Type:        testApache,
						IgnorePorts: []int{443, 22},
					},
				},
			},
			want: map[NameInstance]Service{
				{Name: testApache}: {
					Name:        testApache,
					ServiceType: ApacheService,
					IPAddress:   testIP127001,
					ListenAddresses: []facts.ListenAddress{
						{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 80},
						// It's not applyOverride which remove ignored ports
						{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 443},
					},
					IgnoredPorts: map[int]bool{
						22:  true,
						443: true,
					},
					Config: config.Service{
						Type:        testApache,
						IgnorePorts: []int{443, 22},
					},
				},
			},
		},
		{
			name: "ignore ports with space",
			args: args{
				discoveredServicesMap: map[NameInstance]Service{
					{Name: testApache}: {
						Name:        testApache,
						ServiceType: ApacheService,
					},
				},
				servicesOverride: []config.Service{
					{
						Type:        testApache,
						IgnorePorts: []int{443, 22},
					},
				},
			},
			want: map[NameInstance]Service{
				{Name: testApache}: {
					Name:        testApache,
					ServiceType: ApacheService,
					IgnoredPorts: map[int]bool{
						22:  true,
						443: true,
					},
					Config: config.Service{
						Type:        testApache,
						IgnorePorts: []int{443, 22},
					},
				},
			},
		},
		{
			name: "create tags list",
			args: args{
				discoveredServicesMap: map[NameInstance]Service{
					{Name: testApache}: {
						Name:        testApache,
						ServiceType: ApacheService,
					},
				},
				servicesOverride: []config.Service{
					{
						Type: testApache,
						Tags: []string{testWebsite},
					},
				},
			},
			want: map[NameInstance]Service{
				{Name: testApache}: {
					Name:        testApache,
					ServiceType: ApacheService,
					Tags:        []string{testWebsite},
					Config: config.Service{
						Type: testApache,
						Tags: []string{testWebsite},
					},
				},
			},
		},
		{
			name: "extend tags list",
			args: args{
				discoveredServicesMap: map[NameInstance]Service{
					{Name: testApache}: {
						Name:        testApache,
						ServiceType: ApacheService,
						Tags:        []string{"tag-from-dynamic-discovery-like-docker-labels"},
					},
				},
				servicesOverride: []config.Service{
					{
						Type: testApache,
						Tags: []string{testWebsite},
					},
				},
			},
			want: map[NameInstance]Service{
				{Name: testApache}: {
					Name:        testApache,
					ServiceType: ApacheService,
					Tags:        []string{"tag-from-dynamic-discovery-like-docker-labels", testWebsite},
					Config: config.Service{
						Type: testApache,
						Tags: []string{testWebsite},
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
						Type:    testJMXCustom,
						JMXPort: 1000,
					},
				},
			},
			want: map[NameInstance]Service{
				{Name: testJMXCustom}: {
					Name:        testJMXCustom,
					ServiceType: CustomService,
					Config: config.Service{
						Type:      testJMXCustom,
						Address:   testIP127001,
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
						Type:    testJMXCustom,
						Port:    8000,
						JMXPort: 1000,
					},
				},
			},
			want: map[NameInstance]Service{
				{Name: testJMXCustom}: {
					Name:        testJMXCustom,
					ServiceType: CustomService,
					Config: config.Service{
						Type:      testJMXCustom,
						Address:   testIP127001,
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
					{Name: testKafka}: {
						Name:        testKafka,
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
						Type:    testKafka,
						Port:    9000,
						JMXPort: 2000,
					},
				},
			},
			want: map[NameInstance]Service{
				{Name: testKafka}: {
					Name:        testKafka,
					ServiceType: KafkaService,
					Config: config.Service{
						Type:    testKafka,
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
					{Name: testRedis}: {
						Name:        testRedis,
						ServiceType: RedisService,
						ListenAddresses: []facts.ListenAddress{
							{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 6379},
							{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 6380},
						},
						IPAddress:       testIP127001,
						HasNetstatInfo:  true,
						LastNetstatInfo: t0,
						Active:          true,
					},
				},
				servicesOverride: []config.Service{
					{
						Type:     testRedis,
						Instance: testDuplicate,
						Address:  testIP127001,
						Port:     6379,
					},
				},
			},
			want: map[NameInstance]Service{
				{Name: testRedis}: {
					Name:        testRedis,
					ServiceType: RedisService,
					ListenAddresses: []facts.ListenAddress{
						{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 6380},
					},
					IPAddress:       testIP127001,
					HasNetstatInfo:  true,
					LastNetstatInfo: t0,
					Active:          true,
				},
				{Name: testRedis, Instance: testDuplicate}: {
					Name:        testRedis,
					Instance:    testDuplicate,
					ServiceType: RedisService,
					ListenAddresses: []facts.ListenAddress{
						{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 6379},
					},
					IPAddress: testIP127001,
					Active:    true,
					Config: config.Service{
						Type:     testRedis,
						Instance: testDuplicate,
						Address:  testIP127001,
						Port:     6379,
					},
				},
			},
		},
		{
			name: "nginx-alternative-port-ignore2",
			args: args{
				discoveredServicesMap: map[NameInstance]Service{
					{Name: testNginx, Instance: testNginxAltKey}: {
						Name:          testNginx,
						Instance:      testNginxAltKey,
						ServiceType:   NginxService,
						ContainerID:   testContainerHash,
						ContainerName: testNginxAltKey,
						// It's not applyOverride which remove ignored ports, but we are in ignored ports
						ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP1721602, Port: 80}},
						IPAddress:       testIP1721602,
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
				{Name: testNginx, Instance: testNginxAltKey}: {
					Name:          testNginx,
					Instance:      testNginxAltKey,
					ServiceType:   NginxService,
					ContainerID:   testContainerHash,
					ContainerName: testNginxAltKey,
					// It's not applyOverride which remove ignored ports, but we are in ignored ports
					ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP1721602, Port: 80}},
					IPAddress:       testIP1721602,
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
					{Name: testNginx, Instance: testNginxAltKey}: {
						Name:          testNginx,
						Instance:      testNginxAltKey,
						ServiceType:   NginxService,
						ContainerID:   testContainerHash,
						ContainerName: testNginxAltKey,
						// This test is done in two path. In dynamic.go we add the option to Config.
						// in discovery (applyOverrideInPlance) we apply the config override.
						ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP1721602, Port: 80}},
						IPAddress:       testIP1721602,
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
				{Name: testNginx, Instance: testNginxAltKey}: {
					Name:            testNginx,
					Instance:        testNginxAltKey,
					ServiceType:     NginxService,
					ContainerID:     testContainerHash,
					ContainerName:   testNginxAltKey,
					ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP1721602, Port: 8080}},
					IPAddress:       testIP1721602,
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

			servicesOverrideMap, warnings := validateServices(tt.args.servicesOverride, config.OpenTelemetry{})
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
			Name:            testNginx,
			Instance:        testNginx1,
			ServiceType:     NginxService,
			Active:          true,
			ContainerID:     "1234",
			ContainerName:   testNginx1,
			IPAddress:       testIP1721602,
			ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP1721602, Port: 80}},
		},
	}

	if _, _, err := disc.discovery(t.Context()); err != nil {
		t.Error(err)
	}

	if err := reg.ExpectationFulfilled(); err != nil {
		t.Error(err)
	}

	willBeRemoved := reg.GetAllRegistrations()

	mockDynamic.result = []Service{
		{
			Name:            testNginx,
			Instance:        testNginx1,
			ServiceType:     NginxService,
			Active:          true,
			ContainerID:     "1234",
			ContainerName:   testNginx1,
			IPAddress:       testIP1721602,
			ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP1721602, Port: 80}},
		},
		{
			Name:            testMemcached,
			Instance:        "",
			ServiceType:     MemcachedService,
			Active:          true,
			IPAddress:       testIP127001,
			ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 11211}},
		},
	}
	reg.ExpectedAddedContains = []string{"Service input memcached", "check for memcached"}

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
			Name:            testNginx,
			Instance:        testNginx1,
			ServiceType:     NginxService,
			Active:          true,
			ContainerID:     "1239",
			ContainerName:   testNginx1,
			IPAddress:       testIP1721602,
			ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP1721602, Port: 80}},
		},
		{
			Name:            testMemcached,
			Instance:        "",
			ServiceType:     MemcachedService,
			Active:          true,
			IPAddress:       testIP127001,
			ListenAddresses: []facts.ListenAddress{{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 11211}},
		},
	}

	reg.ExpectedAddedContains = []string{"Service input nginx", "check for nginx"}

	if _, _, err := disc.discovery(t.Context()); err != nil {
		t.Error(err)
	}

	if err := reg.ExpectationFulfilled(); err != nil {
		t.Error(err)
	}

	for _, row := range willBeRemoved {
		if !row.unregisterWasCalled {
			t.Errorf("Expected that %s was unregistered", row.opt.Description)
		}
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
				Name:            testNginx,
				ContainerID:     "",
				HasNetstatInfo:  true,
				LastNetstatInfo: t0.Add(-30 * time.Minute),
			},
			newService: Service{
				Name:            testNginx,
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
				Name:            testNginx,
				ContainerID:     "",
				HasNetstatInfo:  true,
				LastNetstatInfo: t0.Add(-30 * time.Minute),
			},
			newService: Service{
				Name:            testNginx,
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
				Name:            testNginx,
				ContainerID:     "",
				HasNetstatInfo:  true,
				LastNetstatInfo: time.Time{},
			},
			newService: Service{
				Name:            testNginx,
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
	otelCfg := config.OpenTelemetry{
		Enable: true,
		AutoDiscovery: config.AutoDiscovery{
			ContainerAndServiceEnable: true,
		},
		KnownLogFormats: map[string][]config.OTELOperator{
			testNoopFmt: {},
		},
		KnownLogFilters: map[string]config.OTELFilters{
			testNoopFlt: {},
		},
	}

	services := []config.Service{
		{
			Type:     testApache,
			Instance: "",
			Port:     80,
			Address:  testIP127001,
			HTTPPath: "/",
			HTTPHost: testAddr127080,
		},
		{
			Type:     testApache,
			Instance: "",
			Port:     81,
			Address:  testIP127001,
			HTTPPath: "/",
			HTTPHost: testAddr127080,
		},
		{
			Type:     testApache,
			Instance: testContainerName,
			Port:     80,
			Address:  testIP12717002,
			HTTPPath: "/",
			HTTPHost: testAddr127080,
		},
		{
			Type:     testApache,
			Instance: testContainerName,
			Port:     81,
			Address:  testIP12717002,
			HTTPPath: "/",
			HTTPHost: testAddr127080,
		},
		{
			CheckType:    customCheckNagios,
			CheckCommand: testAzerty,
		},
		{
			Type:         testMyApplication,
			Port:         80,
			CheckType:    customCheckNagios,
			CheckCommand: testCommandToRun,
			LogFormat:    testNoopFmt,
			LogFilter:    testNoopFlt,
		},
		{
			Type:         " not fixable@",
			CheckType:    customCheckNagios,
			CheckCommand: testAzerty,
		},
		{
			Type:      testCustomWebserver,
			Port:      8181,
			CheckType: customCheckHTTP,
		},
		{
			Type:         "custom-bad.name",
			CheckType:    customCheckNagios,
			CheckCommand: testAzerty,
		},
		{
			Type:     testSSLAndStartTLS,
			SSL:      true,
			StartTLS: true,
		},
		{
			Type:          testGoodStatsProto,
			StatsProtocol: customCheckHTTP,
		},
		{
			Type:          testBadStatsProto,
			StatsProtocol: "bad",
		},
		{
			Type:      testBadLogCfg,
			LogFormat: "bad-fmt",
			LogFilter: "bad-flt",
		},
		{
			Type: testBadLogFiles,
			LogFiles: []config.ServiceLogFile{
				{
					FilePath:  "",
					LogFormat: "another-bad-format",
					LogFilter: "another-bad-filter",
				},
			},
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
		"invalid config value: service 'bad_log_cfg': requires an unknown log format \"bad-fmt\"",
		"invalid config value: service 'bad_log_cfg': requires an unknown log filter \"bad-flt\"",
		"invalid config value: service 'bad_log_files': no path provided for log file n°1",
	}

	wantServices := map[NameInstance]config.Service{
		{
			Name:     testApache,
			Instance: "",
		}: {
			Type:     testApache,
			Instance: "",
			Port:     81,
			Address:  testIP127001,
			HTTPPath: "/",
			HTTPHost: testAddr127080,
		},
		{
			Name:     testApache,
			Instance: testContainerName,
		}: {
			Type:     testApache,
			Instance: testContainerName,
			Port:     81,
			Address:  testIP12717002,
			HTTPPath: "/",
			HTTPHost: testAddr127080,
		},
		{
			Name:     testMyApplication,
			Instance: "",
		}: {
			Type:         testMyApplication,
			Port:         80,
			CheckType:    customCheckNagios,
			CheckCommand: testCommandToRun,
			LogFormat:    testNoopFmt,
			LogFilter:    testNoopFlt,
		},
		{
			Name:     testCustomWebserver,
			Instance: "",
		}: {
			Type:      testCustomWebserver,
			Port:      8181,
			CheckType: customCheckHTTP,
		},
		{
			Name:     "custom_bad_name",
			Instance: "",
		}: {
			Type:         "custom_bad_name",
			CheckType:    customCheckNagios,
			CheckCommand: testAzerty,
		},
		{
			Name: testSSLAndStartTLS,
		}: {
			Type:     testSSLAndStartTLS,
			SSL:      false,
			StartTLS: true,
		},
		{
			Name: testGoodStatsProto,
		}: {
			Type:          testGoodStatsProto,
			StatsProtocol: customCheckHTTP,
		},
		{
			Name: testBadStatsProto,
		}: {
			Type:          testBadStatsProto,
			StatsProtocol: "",
		},
		{
			Name: testBadLogCfg,
		}: {
			Type:      testBadLogCfg,
			LogFormat: "bad-fmt",
			LogFilter: "bad-flt",
		},
		{
			Name: testBadLogFiles,
		}: {
			Type: testBadLogFiles,
			LogFiles: []config.ServiceLogFile{
				{
					FilePath:  "",
					LogFormat: "another-bad-format",
					LogFilter: "another-bad-filter",
				},
			},
		},
	}

	gotServices, gotWarnings := validateServices(services, otelCfg)

	if diff := cmp.Diff(gotServices, wantServices); diff != "" {
		t.Fatalf("Validate returned unexpected services:\n%s", diff)
	}

	gotWarningsStr := make([]string, 0, len(gotWarnings))
	for _, warning := range gotWarnings {
		gotWarningsStr = append(gotWarningsStr, warning.Error())
	}

	if diff := cmp.Diff(wantWarnings, gotWarningsStr); diff != "" {
		t.Fatalf("Validate returned unexpected warnings (-want +got):\n%s", diff)
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
					Name:          testRedis,
					Instance:      testRedis,
					ContainerID:   "399366e861976b77e5574c6b956f70dd2473944d822196e8bd6735da7e1d373f",
					ContainerName: testRedis,
					IPAddress:     "172.17.0.2",
					ListenAddresses: []facts.ListenAddress{
						{NetworkFamily: tcpProtocol, Address: "172.17.0.2", Port: 6379},
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
					Name:          testNginx,
					Instance:      testComposePHPNginx,
					ContainerID:   "231aa25b7994847ea8b672cff7cd1d6a95a301dacece589982fd0de78470d7e3",
					ContainerName: testComposePHPNginx,
					IPAddress:     testIP1721807,
					ListenAddresses: []facts.ListenAddress{
						{NetworkFamily: tcpProtocol, Address: testIP1721807, Port: 80},
					},
					ServiceType:     NginxService,
					HasNetstatInfo:  true,
					LastNetstatInfo: time.Date(2023, 6, 20, 15, 18, 37, 267699930, time.UTC),
				},
				{
					Name:            testPHPFPM,
					Instance:        testComposePHPNginx,
					ContainerID:     "231aa25b7994847ea8b672cff7cd1d6a95a301dacece589982fd0de78470d7e3",
					ContainerName:   testComposePHPNginx,
					IPAddress:       testIP1721807,
					ListenAddresses: []facts.ListenAddress{},
					ServiceType:     PHPFPMService,
					Active:          true,
					HasNetstatInfo:  false,
				},
				{
					Active:          true,
					Name:            testNginx,
					Instance:        testConflictOther,
					ContainerID:     "741852963",
					ContainerName:   testConflictOther,
					IPAddress:       testIP1721807,
					ListenAddresses: []facts.ListenAddress{},
					ServiceType:     NginxService,
					HasNetstatInfo:  false,
				},
				{
					Name:            testPHPFPM,
					Instance:        testConflictOther,
					ContainerID:     "741852963",
					ContainerName:   testConflictOther,
					IPAddress:       testIP1721807,
					ListenAddresses: []facts.ListenAddress{},
					ServiceType:     PHPFPMService,
					Active:          true,
					HasNetstatInfo:  false,
				},
				{
					Active:        true,
					Name:          testPostgreSQL,
					Instance:      "",
					ContainerID:   "",
					ContainerName: "",
					IPAddress:     testIP127001,
					ListenAddresses: []facts.ListenAddress{
						{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 5432},
						{NetworkFamily: unixProtocol, Address: "/var/run/postgresql/.s.PGSQL.5432", Port: 0},
					},
					ServiceType:     PostgreSQLService,
					HasNetstatInfo:  true,
					LastNetstatInfo: time.Date(2023, 6, 20, 15, 18, 37, 249753388, time.UTC),
				},
				{
					Active:        true,
					Name:          testNginx,
					Instance:      testConflictInactive,
					ContainerID:   "123456789",
					ContainerName: testConflictInactive,
					IPAddress:     testIP1721809,
					ListenAddresses: []facts.ListenAddress{
						{NetworkFamily: tcpProtocol, Address: testIP1721809, Port: 80},
					},
					ServiceType:     NginxService,
					HasNetstatInfo:  true,
					LastNetstatInfo: time.Date(2023, 6, 20, 15, 18, 37, 267699930, time.UTC),
				},
				{
					Active:          false,
					Name:            testPHPFPM,
					Instance:        testConflictInactive,
					ContainerID:     "123456789",
					ContainerName:   testConflictInactive,
					IPAddress:       testIP1721809,
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
					Name:          testNginx,
					Instance:      "",
					ContainerID:   "",
					ContainerName: "",
					IPAddress:     testIP127001,
					ListenAddresses: []facts.ListenAddress{
						{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 80},
					},
					ServiceType:     NginxService,
					HasNetstatInfo:  true,
					LastNetstatInfo: time.Date(2023, 6, 20, 15, 18, 37, 267699930, time.UTC),
				},
				{
					Active:        true,
					Name:          testPHPFPM,
					Instance:      "",
					ContainerID:   "",
					ContainerName: "",
					IPAddress:     testIP127001,
					ListenAddresses: []facts.ListenAddress{
						{NetworkFamily: tcpProtocol, Address: testIP127001, Port: 80},
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
