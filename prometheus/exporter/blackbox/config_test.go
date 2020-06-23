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

package blackbox_test

import (
	"glouton/config"
	"glouton/prometheus/exporter/blackbox"
	"reflect"
	"sync"
	"testing"
	"time"

	bbConf "github.com/prometheus/blackbox_exporter/config"
)

//nolint:gochecknoglobals
var mutex sync.Mutex = sync.Mutex{}

func TestConfigParsing(t *testing.T) {
	cfg := &config.Configuration{}

	conf := `
    blackbox:
      targets:
        - {url: "https://google.com", module: "http_2xx"}
        - {url: "https://inpt.fr", module: "dns", timeout: 5}
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

	mutex.Lock()
	defer mutex.Unlock()

	if err := blackbox.InitConfig(blackboxConf); err != nil {
		t.Fatal(err)
	}

	expectedValue := blackbox.Config{
		Modules: map[string]bbConf.Module{
			"http_2xx": {
				Prober:  "http",
				Timeout: 5 * time.Second,
				HTTP: bbConf.HTTPProbe{
					ValidHTTPVersions:  []string{"HTTP/1.1", "HTTP/2.0"},
					ValidStatusCodes:   []int{},
					Method:             "GET",
					NoFollowRedirects:  true,
					IPProtocol:         "ip4",
					IPProtocolFallback: false,
				},
				DNS:  bbConf.DefaultDNSProbe,
				ICMP: bbConf.DefaultICMPProbe,
				TCP:  bbConf.DefaultTCPProbe,
			},
			"dns": {
				Prober:  "dns",
				Timeout: 9500 * time.Millisecond,
				DNS: bbConf.DNSProbe{
					IPProtocol: "ip4",
					QueryName:  "nightmared.fr",
					QueryType:  "A",
					// by default, this is set to true
					IPProtocolFallback: true,
				},
				HTTP: bbConf.DefaultHTTPProbe,
				ICMP: bbConf.DefaultICMPProbe,
				TCP:  bbConf.DefaultTCPProbe,
			},
		},
		// we assume parsing preserves the order, which seems to be the case
		Targets: []blackbox.ConfigTarget{
			{URL: "https://google.com", ModuleName: "http_2xx", FromStaticConfig: true},
			{URL: "https://inpt.fr", ModuleName: "dns", FromStaticConfig: true},
			{URL: "http://neverssl.com", ModuleName: "http_2xx", FromStaticConfig: true},
		},
	}

	if !reflect.DeepEqual(blackbox.Conf, expectedValue) {
		t.Fatalf("TestConfigParsing() = %+v, want %+v", blackbox.Conf, expectedValue)
	}
}

func TestNoTargetsConfigParsing(t *testing.T) {
	cfg := &config.Configuration{}

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

	mutex.Lock()
	defer mutex.Unlock()

	if err := blackbox.InitConfig(blackboxConf); err != nil {
		t.Fatal(err)
	}

	expectedValue := blackbox.Config{
		Modules: map[string]bbConf.Module{
			"http_2xx": {
				Prober:  "http",
				Timeout: 9500 * time.Millisecond,
				HTTP: bbConf.HTTPProbe{
					ValidHTTPVersions:  []string{"HTTP/1.1", "HTTP/2.0"},
					IPProtocolFallback: true,
				},
				DNS:  bbConf.DefaultDNSProbe,
				ICMP: bbConf.DefaultICMPProbe,
				TCP:  bbConf.DefaultTCPProbe,
			},
		},
		// we assume parsing preserves the order, which seems to be the case
		Targets: []blackbox.ConfigTarget{},
	}

	if !reflect.DeepEqual(blackbox.Conf, expectedValue) {
		t.Fatalf("TestConfigParsing() = %+v, want %+v", blackbox.Conf, expectedValue)
	}
}
