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

//nolint:maintidx,dupl
package blackbox

import (
	"context"
	"crypto/x509"
	"fmt"
	"math"
	"net"
	"testing"
	"time"

	"github.com/bleemeo/glouton/types"
	"github.com/google/go-cmp/cmp"
	"github.com/miekg/dns"
	bbConf "github.com/prometheus/blackbox_exporter/config"
)

func TestConfigDNSTarget(t *testing.T) {
	cases := []struct {
		Name             string
		DNSURL           string
		ExpectedContent  string
		ForbiddenContent string
		WantError        bool
		WantProbe        bbConf.DNSProbe
		WantTarget       string
	}{
		{
			Name:            "standard",
			DNSURL:          "dns://1.1.1.1/bleemeo.com",
			ExpectedContent: "",
			WantProbe: bbConf.DNSProbe{
				IPProtocol:         "ip4", // our default from defaultModule()
				IPProtocolFallback: true,  // our default from defaultModule()
				Recursion:          true,  // our default from defaultModule()
				QueryName:          "bleemeo.com",
				QueryType:          "A",
			},
			WantTarget: "1.1.1.1",
		},
		{
			Name:            "standard2",
			DNSURL:          "dns://8.8.8.8/github.com",
			ExpectedContent: "",
			WantProbe: bbConf.DNSProbe{
				IPProtocol:         "ip4", // our default from defaultModule()
				IPProtocolFallback: true,  // our default from defaultModule()
				Recursion:          true,  // our default from defaultModule()
				QueryName:          "github.com",
				QueryType:          "A",
			},
			WantTarget: "8.8.8.8",
		},
		{
			Name:            defaultResolverSentinel,
			DNSURL:          "dns:bleemeo.com",
			ExpectedContent: "",
			WantProbe: bbConf.DNSProbe{
				IPProtocol:         "ip4", // our default from defaultModule()
				IPProtocolFallback: true,  // our default from defaultModule()
				Recursion:          true,  // our default from defaultModule()
				QueryName:          "bleemeo.com",
				QueryType:          "A",
			},
			WantTarget: defaultResolverSentinel,
		},
		{
			Name:            "standard-ipv6",
			DNSURL:          "dns://1.1.1.1/bleemeo.com?type=AAAA",
			ExpectedContent: "",
			WantProbe: bbConf.DNSProbe{
				IPProtocol:         "ip4", // our default from defaultModule()
				IPProtocolFallback: true,  // our default from defaultModule()
				Recursion:          true,  // our default from defaultModule()
				QueryName:          "bleemeo.com",
				QueryType:          "AAAA",
			},
			WantTarget: "1.1.1.1",
		},
		{
			Name:            "default-resolver-ipv6",
			DNSURL:          "dns:bleemeo.com?type=AAAA",
			ExpectedContent: "",
			WantProbe: bbConf.DNSProbe{
				IPProtocol:         "ip4", // our default from defaultModule()
				IPProtocolFallback: true,  // our default from defaultModule()
				Recursion:          true,  // our default from defaultModule()
				QueryName:          "bleemeo.com",
				QueryType:          "AAAA",
			},
			WantTarget: defaultResolverSentinel,
		},
		{
			Name:            "standard-class-in",
			DNSURL:          "dns://1.1.1.1/bleemeo.com?class=IN",
			ExpectedContent: "",
			WantProbe: bbConf.DNSProbe{
				IPProtocol:         "ip4", // our default from defaultModule()
				IPProtocolFallback: true,  // our default from defaultModule()
				Recursion:          true,  // our default from defaultModule()
				QueryName:          "bleemeo.com",
				QueryType:          "A",
			},
			WantTarget: "1.1.1.1",
		},
		{
			Name:            "default-resolver-class-in",
			DNSURL:          "dns:bleemeo.com?class=IN",
			ExpectedContent: "",
			WantProbe: bbConf.DNSProbe{
				IPProtocol:         "ip4", // our default from defaultModule()
				IPProtocolFallback: true,  // our default from defaultModule()
				Recursion:          true,  // our default from defaultModule()
				QueryName:          "bleemeo.com",
				QueryType:          "A",
			},
			WantTarget: defaultResolverSentinel,
		},
		{
			Name:            "standard-class-chaos",
			DNSURL:          "dns://1.1.1.1/bleemeo.com?class=CH",
			ExpectedContent: "",
			WantError:       true,
		},
		{
			Name:            "standard-txt-and-class-in",
			DNSURL:          "dns://1.1.1.1/bleemeo.com?type=TXT;class=IN",
			ExpectedContent: "",
			WantProbe: bbConf.DNSProbe{
				IPProtocol:         "ip4", // our default from defaultModule()
				IPProtocolFallback: true,  // our default from defaultModule()
				Recursion:          true,  // our default from defaultModule()
				QueryName:          "bleemeo.com",
				QueryType:          "TXT",
			},
			WantTarget: "1.1.1.1",
		},
		{
			Name:            "default-resolver-txt-and-class-in",
			DNSURL:          "dns:bleemeo.com?type=TXT;class=IN",
			ExpectedContent: "",
			WantProbe: bbConf.DNSProbe{
				IPProtocol:         "ip4", // our default from defaultModule()
				IPProtocolFallback: true,  // our default from defaultModule()
				Recursion:          true,  // our default from defaultModule()
				QueryName:          "bleemeo.com",
				QueryType:          "TXT",
			},
			WantTarget: defaultResolverSentinel,
		},
		{
			Name:            "standard-txt-and-class-in-2",
			DNSURL:          "dns://1.1.1.1/bleemeo.com?class=IN;type=TXT",
			ExpectedContent: "",
			WantProbe: bbConf.DNSProbe{
				IPProtocol:         "ip4", // our default from defaultModule()
				IPProtocolFallback: true,  // our default from defaultModule()
				Recursion:          true,  // our default from defaultModule()
				QueryName:          "bleemeo.com",
				QueryType:          "TXT",
			},
			WantTarget: "1.1.1.1",
		},
		{
			Name:            "default-resolver-ipv6-2",
			DNSURL:          "dns:bleemeo.com?class=IN;type=TXT",
			ExpectedContent: "",
			WantProbe: bbConf.DNSProbe{
				IPProtocol:         "ip4", // our default from defaultModule()
				IPProtocolFallback: true,  // our default from defaultModule()
				Recursion:          true,  // our default from defaultModule()
				QueryName:          "bleemeo.com",
				QueryType:          "TXT",
			},
			WantTarget: defaultResolverSentinel,
		},
		{
			Name:            "resolver-with-port",
			DNSURL:          "dns://1.1.1.1:53/bleemeo.com?type=TXT",
			ExpectedContent: "",
			WantProbe: bbConf.DNSProbe{
				IPProtocol:         "ip4", // our default from defaultModule()
				IPProtocolFallback: true,  // our default from defaultModule()
				Recursion:          true,  // our default from defaultModule()
				QueryName:          "bleemeo.com",
				QueryType:          "TXT",
			},
			WantTarget: "1.1.1.1:53",
		},
		{
			Name:            "default-resolver-expect-value",
			DNSURL:          "dns:bleemeo.com",
			ExpectedContent: "15.188.205.60",
			WantProbe: bbConf.DNSProbe{
				IPProtocol:         "ip4", // our default from defaultModule()
				IPProtocolFallback: true,  // our default from defaultModule()
				Recursion:          true,  // our default from defaultModule()
				QueryName:          "bleemeo.com",
				QueryType:          "A",
				ValidateAnswer: bbConf.DNSRRValidator{
					FailIfNoneMatchesRegexp: []string{`\s15\.188\.205\.60$`},
				},
			},
			WantTarget: defaultResolverSentinel,
		},
		{
			Name:            "resolver-expect-value",
			DNSURL:          "dns://4.2.0.42/bleemeo.com?type=TXT",
			ExpectedContent: "15.188.205.60",
			WantProbe: bbConf.DNSProbe{
				IPProtocol:         "ip4", // our default from defaultModule()
				IPProtocolFallback: true,  // our default from defaultModule()
				Recursion:          true,  // our default from defaultModule()
				QueryName:          "bleemeo.com",
				QueryType:          "TXT",
				ValidateAnswer: bbConf.DNSRRValidator{
					FailIfNoneMatchesRegexp: []string{`\s15\.188\.205\.60$`},
				},
			},
			WantTarget: "4.2.0.42",
		},
		{
			Name:             "resolver-unexpect-value",
			DNSURL:           "dns://4.2.0.42/bleemeo.com",
			ForbiddenContent: "1.2.3.4",
			WantProbe: bbConf.DNSProbe{
				IPProtocol:         "ip4", // our default from defaultModule()
				IPProtocolFallback: true,  // our default from defaultModule()
				Recursion:          true,  // our default from defaultModule()
				QueryName:          "bleemeo.com",
				QueryType:          "A",
				ValidateAnswer: bbConf.DNSRRValidator{
					FailIfMatchesRegexp: []string{`\s1\.2\.3\.4$`},
				},
			},
			WantTarget: "4.2.0.42",
		},
		{
			Name:             "resolver-both-check-value",
			DNSURL:           "dns://4.2.0.42/bleemeo.com",
			ExpectedContent:  "15.188.205.60",
			ForbiddenContent: "1.2.3.4",
			WantProbe: bbConf.DNSProbe{
				IPProtocol:         "ip4", // our default from defaultModule()
				IPProtocolFallback: true,  // our default from defaultModule()
				Recursion:          true,  // our default from defaultModule()
				QueryName:          "bleemeo.com",
				QueryType:          "A",
				ValidateAnswer: bbConf.DNSRRValidator{
					FailIfNoneMatchesRegexp: []string{`\s15\.188\.205\.60$`},
					FailIfMatchesRegexp:     []string{`\s1\.2\.3\.4$`},
				},
			},
			WantTarget: "4.2.0.42",
		},
		{
			Name:   "resolver-dns-name",
			DNSURL: "dns://a.gtld-servers.net/bleemeo.com",
			WantProbe: bbConf.DNSProbe{
				IPProtocol:         "ip4", // our default from defaultModule()
				IPProtocolFallback: true,  // our default from defaultModule()
				Recursion:          true,  // our default from defaultModule()
				QueryName:          "bleemeo.com",
				QueryType:          "A",
			},
			WantTarget: "a.gtld-servers.net",
		},
		{
			Name: "resolver-but-absent",
			// I'm not really sure this is a valid URI according to RFC 4501,
			// but Glouton support it like "dns:bleemeo.com"
			DNSURL: "dns:///bleemeo.com",
			WantProbe: bbConf.DNSProbe{
				IPProtocol:         "ip4", // our default from defaultModule()
				IPProtocolFallback: true,  // our default from defaultModule()
				Recursion:          true,  // our default from defaultModule()
				QueryName:          "bleemeo.com",
				QueryType:          "A",
			},
			WantTarget: defaultResolverSentinel,
		},
	}

	monitorAgentID := "c508be29-a6a9-4211-888a-41c354f21b3e"
	monitorID := "b0195fe3-e882-46ff-97f3-f01b370499a6"
	creationDate := time.Now()

	for _, tt := range cases {
		t.Run(tt.Name, func(t *testing.T) {
			t.Parallel()

			collector, err := genCollectorFromDynamicTarget(
				types.Monitor{
					ID:                      monitorID,
					MetricMonitorResolution: time.Minute,
					CreationDate:            creationDate,
					URL:                     tt.DNSURL,
					BleemeoAgentID:          monitorAgentID,
					ExpectedContent:         tt.ExpectedContent,
					ForbiddenContent:        tt.ForbiddenContent,
				},
				"not used",
				mockHelpers{},
			)

			if tt.WantError && err == nil {
				t.Fatal("genCollectorFromDynamicTarget don't throw error, want an error")
			} else if tt.WantError {
				return
			}

			if err != nil {
				t.Fatal(err)
			}

			if diff := cmp.Diff(tt.WantProbe, collector.Module.DNS); diff != "" {
				t.Errorf("DNS config mismatch: (-want +got)\n%s", diff)
			}

			if collector.URL != tt.WantTarget {
				t.Errorf("target URL = %s, want %s", collector.URL, tt.WantTarget)
			}
		})
	}
}

func Test_Collect_DNS(t *testing.T) {
	t0 := time.Now().Truncate(time.Millisecond)

	// You can use go run ./prometheus/exporter/blackbox/testdata/blackbox-test-monitor.go -url dns:bleemeo.com to helps
	// building wantPoints / absentPoints.
	// For Rcode & flags (Authority, RecursionAvailable...) looks at logs
	//
	// Currently tests are mostly here to ensure Glouton is able to extract Rcode from a blackbox ProbeDNS run.
	tests := []testCase{
		{
			name: "query-authoritative-dns",
			target: &dnsTestTarget{
				RCode: dns.RcodeSuccess,
				Answers: []string{
					"bleemeo.com. 3600 IN A 127.0.0.1",
				},
				Authority: []string{
					"bleemeo.com. 3600 IN NS ns1.registrar.net.",
					"bleemeo.com. 3600 IN NS ns2.registrar.net.",
				},
				Authoritative: true,
			},
			wantPoints: []types.MetricPoint{
				{
					Point: types.Point{Time: t0, Value: 1},
					Labels: map[string]string{
						types.LabelName:         "probe_success",
						types.LabelInstance:     testTargetNotYetKnown,
						types.LabelInstanceUUID: testAgentID,
						types.LabelScraper:      testAgentFQDN,
						types.LabelServiceUUID:  testMonitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: testAgentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_dns_rcode",
						types.LabelInstance:     testTargetNotYetKnown,
						types.LabelInstanceUUID: testAgentID,
						types.LabelScraper:      testAgentFQDN,
						types.LabelServiceUUID:  testMonitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: testAgentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 1},
					Labels: map[string]string{
						types.LabelName:         "probe_dns_answer_rrs",
						types.LabelInstance:     testTargetNotYetKnown,
						types.LabelInstanceUUID: testAgentID,
						types.LabelScraper:      testAgentFQDN,
						types.LabelServiceUUID:  testMonitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: testAgentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_dns_lookup_time_seconds",
						types.LabelInstance:     testTargetNotYetKnown,
						types.LabelInstanceUUID: testAgentID,
						types.LabelScraper:      testAgentFQDN,
						types.LabelServiceUUID:  testMonitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: testAgentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 1},
					Labels: map[string]string{
						types.LabelName:         "probe_dns_query_succeeded",
						types.LabelInstance:     testTargetNotYetKnown,
						types.LabelInstanceUUID: testAgentID,
						types.LabelScraper:      testAgentFQDN,
						types.LabelServiceUUID:  testMonitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: testAgentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_duration_seconds",
						types.LabelInstance:     testTargetNotYetKnown,
						types.LabelInstanceUUID: testAgentID,
						types.LabelScraper:      testAgentFQDN,
						types.LabelServiceUUID:  testMonitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: testAgentID,
					},
				},
			},
		},
		{
			name: "non-recursive-dns",
			target: &dnsTestTarget{
				RCode:              dns.RcodeRefused,
				Answers:            []string{},
				Authority:          []string{},
				RecursionAvailable: false,
			},
			wantPoints: []types.MetricPoint{
				{
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_success",
						types.LabelInstance:     testTargetNotYetKnown,
						types.LabelInstanceUUID: testAgentID,
						types.LabelScraper:      testAgentFQDN,
						types.LabelServiceUUID:  testMonitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: testAgentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 5},
					Labels: map[string]string{
						types.LabelName:         "probe_dns_rcode",
						types.LabelInstance:     testTargetNotYetKnown,
						types.LabelInstanceUUID: testAgentID,
						types.LabelScraper:      testAgentFQDN,
						types.LabelServiceUUID:  testMonitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: testAgentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_dns_answer_rrs",
						types.LabelInstance:     testTargetNotYetKnown,
						types.LabelInstanceUUID: testAgentID,
						types.LabelScraper:      testAgentFQDN,
						types.LabelServiceUUID:  testMonitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: testAgentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_dns_lookup_time_seconds",
						types.LabelInstance:     testTargetNotYetKnown,
						types.LabelInstanceUUID: testAgentID,
						types.LabelScraper:      testAgentFQDN,
						types.LabelServiceUUID:  testMonitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: testAgentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 1},
					Labels: map[string]string{
						types.LabelName:         "probe_dns_query_succeeded",
						types.LabelInstance:     testTargetNotYetKnown,
						types.LabelInstanceUUID: testAgentID,
						types.LabelScraper:      testAgentFQDN,
						types.LabelServiceUUID:  testMonitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: testAgentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_duration_seconds",
						types.LabelInstance:     testTargetNotYetKnown,
						types.LabelInstanceUUID: testAgentID,
						types.LabelScraper:      testAgentFQDN,
						types.LabelServiceUUID:  testMonitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: testAgentID,
					},
				},
			},
		},
		{
			name: "dnssec-fail",
			target: &dnsTestTarget{
				RCode:              dns.RcodeServerFailure,
				Answers:            []string{},
				Authority:          []string{},
				RecursionAvailable: true,
			},
			wantPoints: []types.MetricPoint{
				{
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_success",
						types.LabelInstance:     testTargetNotYetKnown,
						types.LabelInstanceUUID: testAgentID,
						types.LabelScraper:      testAgentFQDN,
						types.LabelServiceUUID:  testMonitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: testAgentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 2},
					Labels: map[string]string{
						types.LabelName:         "probe_dns_rcode",
						types.LabelInstance:     testTargetNotYetKnown,
						types.LabelInstanceUUID: testAgentID,
						types.LabelScraper:      testAgentFQDN,
						types.LabelServiceUUID:  testMonitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: testAgentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_dns_answer_rrs",
						types.LabelInstance:     testTargetNotYetKnown,
						types.LabelInstanceUUID: testAgentID,
						types.LabelScraper:      testAgentFQDN,
						types.LabelServiceUUID:  testMonitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: testAgentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_dns_lookup_time_seconds",
						types.LabelInstance:     testTargetNotYetKnown,
						types.LabelInstanceUUID: testAgentID,
						types.LabelScraper:      testAgentFQDN,
						types.LabelServiceUUID:  testMonitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: testAgentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 1},
					Labels: map[string]string{
						types.LabelName:         "probe_dns_query_succeeded",
						types.LabelInstance:     testTargetNotYetKnown,
						types.LabelInstanceUUID: testAgentID,
						types.LabelScraper:      testAgentFQDN,
						types.LabelServiceUUID:  testMonitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: testAgentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_duration_seconds",
						types.LabelInstance:     testTargetNotYetKnown,
						types.LabelInstanceUUID: testAgentID,
						types.LabelScraper:      testAgentFQDN,
						types.LabelServiceUUID:  testMonitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: testAgentID,
					},
				},
			},
		},
	}

	t.Parallel()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runTestCase(t, tt, false, testMonitorID, testAgentID, t0)
		})
	}
}

type dnsTestTarget struct {
	server             *dns.Server
	RCode              int
	Answers            []string
	Authority          []string
	RecursionAvailable bool
	Authoritative      bool
}

func (t *dnsTestTarget) Start() {
	// This function is inspired by blackbox dns_test.go's startDNSServer function.
	h := dns.NewServeMux()
	h.HandleFunc(".", t.handler)

	t.server = &dns.Server{Addr: ":0", Net: "udp", Handler: h}

	a, err := net.ResolveUDPAddr(t.server.Net, t.server.Addr)
	if err != nil {
		panic(err)
	}

	l, err := net.ListenUDP(t.server.Net, a)
	if err != nil {
		panic(err)
	}

	t.server.PacketConn = l

	go func() {
		if err := t.server.ActivateAndServe(); err != nil {
			panic(err)
		}
	}()
}

func (t *dnsTestTarget) handler(w dns.ResponseWriter, r *dns.Msg) {
	m := new(dns.Msg)
	m.SetReply(r)

	m.Rcode = t.RCode
	m.RecursionAvailable = t.RecursionAvailable
	m.Authoritative = t.Authoritative

	for _, rr := range t.Answers {
		a, err := dns.NewRR(rr)
		if err != nil {
			panic(err)
		}

		m.Answer = append(m.Answer, a)
	}

	for _, rr := range t.Authority {
		a, err := dns.NewRR(rr)
		if err != nil {
			panic(err)
		}

		m.Ns = append(m.Ns, a)
	}

	if err := w.WriteMsg(m); err != nil {
		panic(err)
	}
}

func (t *dnsTestTarget) Close() {
	_ = t.server.Shutdown()
}

func (t *dnsTestTarget) URL() string {
	return fmt.Sprintf("dns://%s/bleemeo.com", t.server.PacketConn.LocalAddr())
}

func (t *dnsTestTarget) RootCACertificates() []*x509.Certificate {
	return nil
}

func (t *dnsTestTarget) RequestContext(ctx context.Context) context.Context {
	return ctx
}
