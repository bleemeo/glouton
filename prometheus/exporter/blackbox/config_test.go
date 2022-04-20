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

package blackbox

import (
	"context"
	gloutonConfig "glouton/config"
	"glouton/prometheus/registry"
	"glouton/types"
	"reflect"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	bbConf "github.com/prometheus/blackbox_exporter/config"
	"github.com/prometheus/common/config"
)

func TestConfigParsing(t *testing.T) {
	cfg := &gloutonConfig.Configuration{}

	conf := `
    blackbox:
      targets:
        - {url: "https://google.com", module: "http_2xx"}
        - {url: "inpt.fr", module: "dns", timeout: 5}
        - url: "http://neverssl.com"
          module: "http_2xx"
          timeout: 2
      modules:
        http_2xx:
          prober: http
          timeout: 5s
          http:
            valid_http_versions: ["HTTP/1.1", "HTTP/2.0"]
            valid_status_codes: []  # Defaults to 2xx
            method: GET
            no_follow_redirects: true
            preferred_ip_protocol: "ip4" # defaults to "ip6"
            ip_protocol_fallback: false  # no fallback to "ip6"
        dns:
          prober: dns
          dns:
            preferred_ip_protocol: "ip4"
            query_name: "nightmared.fr"
            query_type: "A"`

	if err := cfg.LoadByte([]byte(conf)); err != nil {
		t.Fatal(err)
	}

	blackboxConf, present := cfg.Get("blackbox")
	if !present {
		t.Fatalf("Couldn't parse the yaml configuration")
	}

	registry := &registry.Registry{}

	bbManager, err := New(context.Background(), registry, blackboxConf, "dummy-user-agent", types.MetricFormatPrometheus)
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
					HTTPClientConfig: config.HTTPClientConfig{
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
					HTTPClientConfig: config.HTTPClientConfig{
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
	cfg := &gloutonConfig.Configuration{}

	conf := `
    blackbox:
      modules:
        http_2xx:
          prober: http
          http:
            valid_http_versions: ["HTTP/1.1", "HTTP/2.0"]`

	if err := cfg.LoadByte([]byte(conf)); err != nil {
		t.Fatal(err)
	}

	blackboxConf, present := cfg.Get("blackbox")
	if !present {
		t.Fatalf("Couldn't parse the yaml configuration")
	}

	bbManager, err := New(context.Background(), nil, blackboxConf, "dummy-user-agent", types.MetricFormatPrometheus)
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(bbManager.targets, []collectorWithLabels{}) {
		t.Fatalf("TestConfigParsing() = %+v, want %+v", bbManager.targets, []collectorWithLabels{})
	}
}
