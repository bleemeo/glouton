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

package blackbox

import (
	"bytes"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/prometheus/registry"
	"github.com/bleemeo/glouton/types"
	"github.com/bleemeo/glouton/utils/archivewriter"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	bbConf "github.com/prometheus/blackbox_exporter/config"
	promConfig "github.com/prometheus/common/config"
)

func TestConfigParsing(t *testing.T) {
	config, _, warnings, err := config.Load(false, false, "testdata/blackbox.conf")
	if err != nil {
		t.Fatal(err)
	}

	if warnings != nil {
		t.Fatalf("Got warnings loading config: %v", warnings)
	}

	registry, err := registry.New(registry.Option{})
	if err != nil {
		t.Fatal(err)
	}

	bbManager, err := New(registry, config.Blackbox, func(err ...error) {})
	if err != nil {
		t.Fatal(err)
	}

	noFollowRedirect := true

	// we assume parsing preserves the order, which seems to be the case
	expectedValue := []blackboxCollector{
		genCollectorFromStaticTarget(staticTargetOptions{
			URL: "https://google.com",
			Module: bbConf.Module{
				Prober:  "http",
				Timeout: 5 * time.Second,
				HTTP: bbConf.HTTPProbe{
					ValidHTTPVersions:  []string{"HTTP/1.1", "HTTP/2.0"},
					ValidStatusCodes:   []int{},
					Method:             "GET",
					NoFollowRedirects:  &noFollowRedirect,
					IPProtocol:         ipProtocolV4,
					IPProtocolFallback: false,
					Headers: map[string]string{
						headerUserAgent: "dummy-user-agent",
					},
					HTTPClientConfig: promConfig.HTTPClientConfig{
						EnableHTTP2: true,
					},
				},
				DNS:       bbConf.DefaultDNSProbe,
				ICMP:      bbConf.DefaultICMPProbe,
				TCP:       bbConf.DefaultTCPProbe,
				Websocket: bbConf.DefaultWebsocketProbe,
			},
			ModuleName: "http_2xx",
			Name:       "https://google.com",
		}, mockHelpers{}),
		genCollectorFromStaticTarget(staticTargetOptions{
			URL: "inpt.fr",
			Module: bbConf.Module{
				Prober:  "dns",
				Timeout: defaultTimeout,
				DNS: bbConf.DNSProbe{
					IPProtocol: ipProtocolV4,
					QueryName:  "nightmared.fr",
					QueryType:  "A",
					// by default, this is set to true
					IPProtocolFallback: true,
					Recursion:          true,
				},
				HTTP:      bbConf.DefaultHTTPProbe,
				ICMP:      bbConf.DefaultICMPProbe,
				TCP:       bbConf.DefaultTCPProbe,
				Websocket: bbConf.DefaultWebsocketProbe,
			},
			ModuleName: "dns",
			Name:       "inpt.fr",
		}, mockHelpers{}),
		genCollectorFromStaticTarget(staticTargetOptions{
			URL: "http://neverssl.com",
			Module: bbConf.Module{
				Prober:  "http",
				Timeout: 5 * time.Second,
				HTTP: bbConf.HTTPProbe{
					ValidHTTPVersions:  []string{"HTTP/1.1", "HTTP/2.0"},
					ValidStatusCodes:   []int{},
					Method:             "GET",
					NoFollowRedirects:  &noFollowRedirect,
					IPProtocol:         ipProtocolV4,
					IPProtocolFallback: false,
					Headers: map[string]string{
						headerUserAgent: "dummy-user-agent",
					},
					HTTPClientConfig: promConfig.HTTPClientConfig{
						EnableHTTP2: true,
					},
				},
				DNS:       bbConf.DefaultDNSProbe,
				ICMP:      bbConf.DefaultICMPProbe,
				TCP:       bbConf.DefaultTCPProbe,
				Websocket: bbConf.DefaultWebsocketProbe,
			},
			ModuleName: "http_2xx",
			Name:       "http://neverssl.com",
		}, mockHelpers{}),
	}

	ignoreUnexported := cmpopts.IgnoreUnexported(blackboxCollector{}, bbConf.Module{}.HTTP.HTTPClientConfig.ProxyConfig)
	if diff := cmp.Diff(expectedValue, bbManager.targets, ignoreUnexported); diff != "" {
		t.Errorf("bbManager.targets mismatch (-want +got)\n%s", diff)
	}
}

func TestNoTargetsConfigParsing(t *testing.T) {
	config, _, warnings, err := config.Load(false, false, "testdata/no-target.conf")
	if err != nil {
		t.Fatal(err)
	}

	if warnings != nil {
		t.Fatalf("Got warnings loading config: %v", warnings)
	}

	bbManager, err := New(nil, config.Blackbox, func(err ...error) {})
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(bbManager.targets, []blackboxCollector{}) {
		t.Fatalf("TestConfigParsing() = %+v, want %+v", bbManager.targets, []blackboxCollector{})
	}
}

// TestDynamicTargetCensorsURLCredentials ensures credentials embedded in a
// monitor URL are redacted in the __meta_bleemeo_target_agent label (which is
// relabeled into 'instance' sent to the Bleemeo API and dumped in diagnostics),
// while the actual probe target (collector.URL) keeps the real credentials.
func TestDynamicTargetCensorsURLCredentials(t *testing.T) {
	t.Parallel()

	monitor := types.Monitor{ //nolint:gosec
		ID:             "1cb119d1-df61-41c7-ae6a-5b4e54442e00",
		BleemeoAgentID: "60ed127d-b3d3-4c82-89e2-df74a80a9e9e",
		URL:            "http://user:pass@localhost:1234/path",
	}

	collector, err := genCollectorFromDynamicTarget(monitor, "dummy-user-agent", mockHelpers{})
	if err != nil {
		t.Fatal(err)
	}

	gotLabel := collector.Labels[types.LabelMetaBleemeoTargetAgent]
	wantLabel := "http://user:" + config.CensoredValue + "@localhost:1234/path"

	if gotLabel != wantLabel {
		t.Errorf("target agent label = %q, want %q", gotLabel, wantLabel)
	}

	// The probe target must keep the real credentials to be able to probe.
	if !strings.Contains(collector.URL, "user:pass@") {
		t.Errorf("probe URL = %q, expected it to keep the real credentials", collector.URL)
	}
}

func TestProcessRegexp(t *testing.T) {
	testCases := []struct {
		input               string
		expectedMatching    []string
		expectedNotMatching []string
	}{
		{
			input:            "maintenance",
			expectedMatching: []string{"maintenance", "Maintenance"},
		},
		{
			input:               "abc?",
			expectedMatching:    []string{"abc?", "aBc?", "ABC?"},
			expectedNotMatching: []string{"abcd", "abc"},
		},
		{
			input: "plain string with ( closed range ]",
			// For this case, we just ensure not error is thrown.
		},
		{
			input:               "some regex tokens ...",
			expectedMatching:    []string{"some regex tokens ..."},
			expectedNotMatching: []string{"some regex tokens !!!"},
		},
		{
			input: " !\"#$%&'()*+,-./0123456789:;<=>?@ABCDEFGHIJKLMNOPQRSTUVWXYZ[\\]^_`abcdefghijklmnopqrstuvwxyz{|}~",
			expectedMatching: []string{
				" !\"#$%&'()*+,-./0123456789:;<=>?@ABCDEFGHIJKLMNOPQRSTUVWXYZ[\\]^_`abcdefghijklmnopqrstuvwxyz{|}~",
			},
			expectedNotMatching: []string{
				" !\"#$%&'()*+,-A/0123456789:;<=>?@ABCDEFGHIJKLMNOPQRSTUVWXYZ[\\]^_`abcdefghijklmnopqrstuvwxyz{|}~",
			}, //               ^ the difference is here: if the '.' hadn't been escaped, 'A' would have been a match.
		},
	}

	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			re, err := processRegexp(tc.input)
			if err != nil {
				t.Fatal("Failed to process regexp:", err)
			}

			for _, s := range tc.expectedMatching {
				if !re.MatchString(s) {
					t.Errorf("%q should have matched %s", s, re.String())
				}
			}

			for _, s := range tc.expectedNotMatching {
				if re.MatchString(s) {
					t.Errorf("%q should not have matched %s", s, re.String())
				}
			}
		})
	}
}

// TestDynamicTargetHeadRequest ensures head_request switches HTTP probes to HEAD requests,
// unless a content check needs the body, and that it doesn't change other probes.
func TestDynamicTargetHeadRequest(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name       string
		monitor    types.Monitor
		wantMethod string
	}{
		{
			name:       "http-head-request-false",
			monitor:    types.Monitor{URL: "http://example.com/path"},
			wantMethod: "",
		},
		{
			name:       "http-head-request",
			monitor:    types.Monitor{URL: "http://example.com/path", HeadRequest: true},
			wantMethod: http.MethodHead,
		},
		{
			name:       "https-head-request",
			monitor:    types.Monitor{URL: "https://example.com/path", HeadRequest: true, ExpectedResponseCode: 204},
			wantMethod: http.MethodHead,
		},
		{
			name:       "https-head-request-expected-content",
			monitor:    types.Monitor{URL: "https://example.com/path", HeadRequest: true, ExpectedContent: "welcome"},
			wantMethod: "",
		},
		{
			name:       "https-head-request-forbidden-content",
			monitor:    types.Monitor{URL: "https://example.com/path", HeadRequest: true, ForbiddenContent: "maintenance"},
			wantMethod: "",
		},
		{
			name:    "tcp-head-request",
			monitor: types.Monitor{URL: "tcp://example.com:1234", HeadRequest: true},
		},
		{
			name:    "icmp-head-request",
			monitor: types.Monitor{URL: "icmp://example.com", HeadRequest: true},
		},
		{
			name:    "dns-head-request",
			monitor: types.Monitor{URL: "dns://8.8.8.8/example.com?type=AAAA", HeadRequest: true},
		},
		{
			name:    "ssl-head-request",
			monitor: types.Monitor{URL: "ssl://example.com:443", HeadRequest: true},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			collector, err := genCollectorFromDynamicTarget(tc.monitor, "dummy-user-agent", mockHelpers{})
			if err != nil {
				t.Fatal(err)
			}

			if collector.Module.HTTP.Method != tc.wantMethod {
				t.Errorf("HTTP method = %q, want %q", collector.Module.HTTP.Method, tc.wantMethod)
			}

			// Apart from the method, the module must be the same as without head_request.
			monitorWithoutHead := tc.monitor
			monitorWithoutHead.HeadRequest = false

			collectorWithoutHead, err := genCollectorFromDynamicTarget(monitorWithoutHead, "dummy-user-agent", mockHelpers{})
			if err != nil {
				t.Fatal(err)
			}

			wantModule := collectorWithoutHead.Module
			wantModule.HTTP.Method = tc.wantMethod

			// Use reflect.DeepEqual like blackboxCollector.Equal, cmp can't compare the unexported regexp fields.
			if !reflect.DeepEqual(wantModule, collector.Module) {
				t.Errorf("module = %+v, want %+v", collector.Module, wantModule)
			}

			// Only a change of method makes the collector different, so that it's recreated.
			// When head_request is false, monitorWithoutHead is the same monitor, so this case is trivial.
			wantEqual := tc.wantMethod == ""
			if gotEqual := collector.Equal(collectorWithoutHead); gotEqual != wantEqual {
				t.Errorf("collector.Equal(collectorWithoutHead) = %v, want %v", gotEqual, wantEqual)
			}
		})
	}
}

// TestDiagnosticArchiveHTTPMethod ensures the diagnostic shows the HTTP method of the HTTP probes,
// to know whether a monitor with head_request is probed with HEAD.
func TestDiagnosticArchiveHTTPMethod(t *testing.T) {
	t.Parallel()

	reg, err := registry.New(registry.Option{})
	if err != nil {
		t.Fatal(err)
	}

	bbManager, err := New(reg, config.Blackbox{}, func(...error) {})
	if err != nil {
		t.Fatal(err)
	}

	monitors := []types.Monitor{
		{ID: "1", BleemeoAgentID: "agent-1", URL: "https://example.com/head", HeadRequest: true},
		{ID: "2", BleemeoAgentID: "agent-2", URL: "https://example.com/get"},
		{ID: "3", BleemeoAgentID: "agent-3", URL: "https://example.com/content", HeadRequest: true, ExpectedContent: "welcome"},
		{ID: "4", BleemeoAgentID: "agent-4", URL: "tcp://example.com:1234", HeadRequest: true},
	}

	if err := bbManager.UpdateDynamicTargets(monitors); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer

	if err := bbManager.DiagnosticArchive(t.Context(), archivewriter.NewTextArchive(&buf)); err != nil {
		t.Fatal(err)
	}

	// blackbox-targets.txt is the last file of the archive.
	_, targets, ok := strings.Cut(buf.String(), "-----[ start of filename: blackbox-targets.txt ]-----\n")
	if !ok {
		t.Fatalf("blackbox-targets.txt not in the archive:\n%s", buf.String())
	}

	wantPrefixes := []string{
		"url=https://example.com/head method=HEAD labels=",
		"url=https://example.com/get method=GET labels=",
		"url=https://example.com/content method=GET labels=",
		"url=example.com:1234 labels=",
	}

	lines := strings.Split(strings.TrimSpace(targets), "\n")
	for _, want := range wantPrefixes {
		if !slices.ContainsFunc(lines, func(line string) bool { return strings.HasPrefix(line, want) }) {
			t.Errorf("no line starting with %q in:\n%s", want, targets)
		}
	}
}
