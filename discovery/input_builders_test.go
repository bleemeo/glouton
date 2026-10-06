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
	"errors"
	"testing"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/facts"
	"github.com/bleemeo/glouton/prometheus/registry"
	"github.com/bleemeo/glouton/types"

	"github.com/influxdata/telegraf"
	"github.com/prometheus/client_golang/prometheus"
)

// recordingRegistry registers anything, and keeps the options of the last registration.
type recordingRegistry struct {
	opts *registry.RegistrationOption
}

func (r recordingRegistry) RegisterGatherer(opt registry.RegistrationOption, _ prometheus.Gatherer) (types.Registration, error) {
	*r.opts = opt

	return bareRegistration{}, nil
}

func (r recordingRegistry) RegisterInput(opt registry.RegistrationOption, _ telegraf.Input) (types.Registration, error) {
	*r.opts = opt

	return bareRegistration{}, nil
}

// builderExpectation is what createInput gives for a type, without then with an address and
// credentials.
type builderExpectation struct {
	// needsNothing: the input reads a local daemon or command, so it is built without an
	// address. What it ends with depends on the platform: an input, no input on this platform,
	// or a missing command.
	needsNothing bool
	// fromStatsURL: an input is built from the stats URL or port, with this naming.
	fromStatsURL bool
	compat       bool
	// needsCredentials: without a username and a password, no input is built even from the
	// stats URL.
	needsCredentials bool
}

// builderExpectations is the behaviour of each builder. It must list every one.
var builderExpectations = map[ServiceName]builderExpectation{ //nolint:gochecknoglobals
	ActiveMQService:      {fromStatsURL: true, compat: false},
	ApacheService:        {},
	BindService:          {fromStatsURL: true, compat: true},
	ChronyService:        {needsNothing: true},
	ClickHouseService:    {fromStatsURL: true, compat: true},
	ConsulService:        {fromStatsURL: true, compat: false},
	DovecotService:       {fromStatsURL: true, compat: true},
	ElasticSearchService: {},
	Fail2banService:      {needsNothing: true},
	HAProxyService:       {fromStatsURL: true, compat: true},
	InfluxDBService:      {fromStatsURL: true, compat: false},
	JenkinsService:       {fromStatsURL: true, compat: false, needsCredentials: true},
	MariaDBService:       {},
	MemcachedService:     {},
	MongoDBService:       {},
	MySQLService:         {},
	NSQService:           {fromStatsURL: true, compat: true},
	NatsService:          {fromStatsURL: true, compat: false},
	NfsService:           {needsNothing: true},
	NginxService:         {},
	NTPService:           {needsNothing: true},
	OpenBaoService:       {fromStatsURL: true, compat: true},
	OpenLDAPService:      {},
	PHPFPMService:        {fromStatsURL: true, compat: true},
	PgBouncerService:     {},
	PostgreSQLService:    {},
	RabbitMQService:      {fromStatsURL: true, compat: true},
	RedisService:         {},
	TomcatService:        {fromStatsURL: true, compat: false, needsCredentials: true},
	UPSDService:          {},
	UWSGIService:         {fromStatsURL: true, compat: false},
	ValkeyService:        {},
	VarnishService:       {needsNothing: true},
	VaultService:         {fromStatsURL: true, compat: true},
	ZookeeperService:     {},
}

// TestInputBuilders pins, for each builder, what a service without an address or credentials
// ends with, then what one with a stats URL and stats port does, without then with credentials.
func TestInputBuilders(t *testing.T) {
	for serviceType := range inputBuilders {
		want, ok := builderExpectations[serviceType]
		if !ok {
			t.Errorf("no expectation for the builder of %s: add it to builderExpectations", serviceType)

			continue
		}

		t.Run(string(serviceType), func(t *testing.T) {
			bare := Service{Name: string(serviceType), ServiceType: serviceType, Active: true}

			full := bare
			full.IPAddress = "127.0.0.1"
			full.ListenAddresses = []facts.ListenAddress{{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 9999}}
			full.Config.Username = "user"
			full.Config.Password = "password"
			full.Config.StatsURL = "http://127.0.0.1:9999/stats"
			full.Config.StatsPort = 9999

			outcome, _, err := createInputRecording(bare)
			if err != nil {
				t.Fatalf("without an address: %v", err)
			}

			switch {
			case want.needsNothing:
				if outcome == inputMissingInfo {
					t.Errorf("without an address: missing info, want an input or a platform reason")
				}
			case outcome != inputMissingInfo:
				t.Errorf("without an address: outcome %d, want missing info", outcome)
			}

			outcome, opts, err := createInputRecording(full)
			if err != nil {
				t.Fatalf("with a stats URL: %v", err)
			}

			if !want.fromStatsURL {
				return
			}

			withoutCredentials := full
			withoutCredentials.Config.Username = ""
			withoutCredentials.Config.Password = ""

			noCredsOutcome, _, err := createInputRecording(withoutCredentials)
			if err != nil {
				t.Fatalf("without credentials: %v", err)
			}

			if want.needsCredentials != (noCredsOutcome == inputMissingInfo) {
				t.Errorf("without credentials: outcome %d, needs credentials %v", noCredsOutcome, want.needsCredentials)
			}

			// No input on this platform is fine: the builder still built nothing else.
			if outcome != inputRegistered && outcome != inputUnsupportedPlatform {
				t.Errorf("with a stats URL: outcome %d, want an input", outcome)
			}

			if outcome == inputRegistered && opts.CompatibilityNameItem != want.compat {
				t.Errorf("compatibility naming %v, want %v", opts.CompatibilityNameItem, want.compat)
			}
		})
	}
}

func createInputRecording(service Service) (inputOutcome, registry.RegistrationOption, error) {
	var opts registry.RegistrationOption

	d := newTestDiscovery()
	d.metricRegistry = recordingRegistry{opts: &opts}

	outcome, err := d.createInput(service)

	return outcome, opts, err
}

// TestCreateInputBuilderWithoutReason checks a builder returning neither an input nor a reason
// is reported as a failure, not as missing credentials: that would be a false diagnosis.
func TestCreateInputBuilderWithoutReason(t *testing.T) {
	previous := inputBuilders[ApacheService]
	inputBuilders[ApacheService] = func(*Discovery, Service, *registry.RegistrationOption) (telegraf.Input, error) {
		return nil, nil //nolint:nilnil
	}

	t.Cleanup(func() { inputBuilders[ApacheService] = previous })

	outcome, err := newTestDiscovery().createInput(Service{Name: "apache", ServiceType: ApacheService, Active: true})
	if outcome != inputCreateFailed || !errors.Is(err, errNoInputBuilt) {
		t.Errorf("createInput() = %d, %v; want a failure with errNoInputBuilt", outcome, err)
	}
}

// TestNATSMonitoringURL checks the monitoring server is read on the address listening on its
// port, as for uWSGI, rather than always on the service's address.
func TestNATSMonitoringURL(t *testing.T) {
	cases := []struct {
		name    string
		service Service
		want    string
	}{
		{
			name: "listening on loopback only",
			service: Service{
				IPAddress:       "192.168.1.2",
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "127.0.0.1", Port: 8222}},
			},
			want: "http://127.0.0.1:8222",
		},
		{
			name: "listening on every address",
			service: Service{
				IPAddress:       "192.168.1.2",
				ListenAddresses: []facts.ListenAddress{{NetworkFamily: "tcp", Address: "0.0.0.0", Port: 8222}},
			},
			want: "http://192.168.1.2:8222",
		},
		{
			name:    "configured address and port",
			service: Service{IPAddress: "192.168.1.2", Config: config.Service{Address: "10.0.0.5", StatsPort: 9000}},
			want:    "http://10.0.0.5:9000",
		},
		{
			name:    "port not seen listening",
			service: Service{IPAddress: "192.168.1.2"},
			want:    "http://192.168.1.2:8222",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got, ok := natsMonitoringURL(tc.service); !ok || got != tc.want {
				t.Errorf("natsMonitoringURL() = %q, %v; want %q", got, ok, tc.want)
			}
		})
	}

	if _, ok := natsMonitoringURL(Service{}); ok {
		t.Error("natsMonitoringURL() found a URL for a service without an address")
	}
}
