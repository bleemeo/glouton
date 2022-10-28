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

package blackbox

import (
	"glouton/config"
	"glouton/prometheus/registry"
	"glouton/types"
	"reflect"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	bbConf "github.com/prometheus/blackbox_exporter/config"
	promConfig "github.com/prometheus/common/config"
)

func TestConfigParsing(t *testing.T) {
	config, warnings, err := config.Load(false, "testdata/blackbox.conf")
	if err != nil {
		t.Fatal(err)
	}

	if warnings != nil {
		t.Fatalf("Got warnings loading config: %v", warnings)
	}

	registry := &registry.Registry{}

	bbManager, err := New(registry, config.Blackbox, types.MetricFormatPrometheus)
	if err != nil {
		t.Fatal(err)
	}

	noFollowRedirect := true

	// we assume parsing preserves the order, which seems to be the case
	expectedValue := []collectorWithLabels{
		genCollectorFromStaticTarget(configTarget{
			URL: "https://google.com",
			Module: bbConf.Module{
				Prober:  "http",
				Timeout: 5 * time.Second,
				HTTP: bbConf.HTTPProbe{
					ValidHTTPVersions:  []string{"HTTP/1.1", "HTTP/2.0"},
					ValidStatusCodes:   []int{},
					Method:             "GET",
					NoFollowRedirects:  &noFollowRedirect,
					IPProtocol:         "ip4",
					IPProtocolFallback: false,
					Headers: map[string]string{
						"User-Agent": "dummy-user-agent",
					},
					HTTPClientConfig: promConfig.HTTPClientConfig{
						EnableHTTP2: true,
					},
				},
				DNS:  bbConf.DefaultDNSProbe,
				ICMP: bbConf.DefaultICMPProbe,
				TCP:  bbConf.DefaultTCPProbe,
			},
			ModuleName: "http_2xx",
			Name:       "https://google.com",
			nowFunc:    time.Now,
		}),
		genCollectorFromStaticTarget(configTarget{
			URL: "inpt.fr",
			Module: bbConf.Module{
				Prober:  "dns",
				Timeout: 9500 * time.Millisecond,
				DNS: bbConf.DNSProbe{
					IPProtocol: "ip4",
					QueryName:  "nightmared.fr",
					QueryType:  "A",
					// by default, this is set to true
					IPProtocolFallback: true,
					Recursion:          true,
				},
				HTTP: bbConf.DefaultHTTPProbe,
				ICMP: bbConf.DefaultICMPProbe,
				TCP:  bbConf.DefaultTCPProbe,
			},
			ModuleName: "dns",
			Name:       "inpt.fr",
			nowFunc:    time.Now,
		}),
		genCollectorFromStaticTarget(configTarget{
			URL: "http://neverssl.com",
			Module: bbConf.Module{
				Prober:  "http",
				Timeout: 5 * time.Second,
				HTTP: bbConf.HTTPProbe{
					ValidHTTPVersions:  []string{"HTTP/1.1", "HTTP/2.0"},
					ValidStatusCodes:   []int{},
					Method:             "GET",
					NoFollowRedirects:  &noFollowRedirect,
					IPProtocol:         "ip4",
					IPProtocolFallback: false,
					Headers: map[string]string{
						"User-Agent": "dummy-user-agent",
					},
					HTTPClientConfig: promConfig.HTTPClientConfig{
						EnableHTTP2: true,
					},
				},
				DNS:  bbConf.DefaultDNSProbe,
				ICMP: bbConf.DefaultICMPProbe,
				TCP:  bbConf.DefaultTCPProbe,
			},
			ModuleName: "http_2xx",
			Name:       "http://neverssl.com",
			nowFunc:    time.Now,
		}),
	}

	if diff := cmp.Diff(expectedValue, bbManager.targets, cmpopts.IgnoreUnexported(configTarget{})); diff != "" {
		t.Errorf("bbManager.targets mismatch (-want +got)\n%s", diff)
	}
}

func TestNoTargetsConfigParsing(t *testing.T) {
	config, warnings, err := config.Load(false, "testdata/no-target.conf")
	if err != nil {
		t.Fatal(err)
	}

	if warnings != nil {
		t.Fatalf("Got warnings loading config: %v", warnings)
	}

	bbManager, err := New(nil, config.Blackbox, types.MetricFormatPrometheus)
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(bbManager.targets, []collectorWithLabels{}) {
		t.Fatalf("TestConfigParsing() = %+v, want %+v", bbManager.targets, []collectorWithLabels{})
	}
}
