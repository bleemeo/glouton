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

//nolint:maintidx
package blackbox

import (
	"testing"
	"time"

	"github.com/bleemeo/glouton/types"
	"github.com/google/go-cmp/cmp"
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
