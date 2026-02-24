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

//nolint:dupl
package blackbox

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"testing"
	"time"

	"github.com/bleemeo/glouton/types"
)

// Test_Collect_HTTPS tests HTTPS (and HTTP) probes.
func Test_Collect_HTTPS(t *testing.T) { //nolint:maintidx
	t0 := time.Now().Truncate(time.Second)

	certs, err := generateCerts(t, t0)
	if err != nil {
		t.Fatal(err)
	}

	tests := []testCase{
		{
			name: "http-only-success-200",
			absentPoints: []map[string]string{
				{
					types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
					types.LabelInstance:     testTargetNotYetKnown,
					types.LabelInstanceUUID: testAgentID,
					types.LabelScraper:      testAgentFQDN,
					types.LabelServiceUUID:  testMonitorID,
				},
				{
					types.LabelName:         "probe_ssl_earliest_cert_expiry",
					types.LabelInstance:     testTargetNotYetKnown,
					types.LabelInstanceUUID: testAgentID,
					types.LabelScraper:      testAgentFQDN,
					types.LabelServiceUUID:  testMonitorID,
				},
				{
					types.LabelName:         "probe_ssl_validation_success",
					types.LabelInstance:     testTargetNotYetKnown,
					types.LabelInstanceUUID: testAgentID,
					types.LabelScraper:      testAgentFQDN,
					types.LabelServiceUUID:  testMonitorID,
				},
				{
					types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
					types.LabelInstance:     testTargetNotYetKnown,
					types.LabelInstanceUUID: testAgentID,
					types.LabelScraper:      testAgentFQDN,
					types.LabelServiceUUID:  testMonitorID,
				},
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
					Point: types.Point{Time: t0, Value: 200},
					Labels: map[string]string{
						types.LabelName:         "probe_http_status_code",
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
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_http_duration_seconds",
						types.LabelInstance:     testTargetNotYetKnown,
						types.LabelInstanceUUID: testAgentID,
						types.LabelScraper:      testAgentFQDN,
						types.LabelServiceUUID:  testMonitorID,
						"phase":                 "connect",
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
			probeDurationMaxValue: 5,
			target:                &httpTestTarget{},
		},
		{
			// This test is mostly here to ensure test code work:
			// The self-signed certificate is in the trusted CA root, which isn't realistic, but allows
			// to test without involving multiple certiciates.
			name:         "success-200-single-cert",
			absentPoints: []map[string]string{},
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
					Point: types.Point{Time: t0, Value: 200},
					Labels: map[string]string{
						types.LabelName:         "probe_http_status_code",
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
						types.LabelName:         "probe_http_duration_seconds",
						types.LabelInstance:     testTargetNotYetKnown,
						types.LabelInstanceUUID: testAgentID,
						types.LabelScraper:      testAgentFQDN,
						types.LabelServiceUUID:  testMonitorID,
						"phase":                 "connect",
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
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedOk.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
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
					Point: types.Point{Time: t0, Value: float64(certs.LongLiveDuration.Seconds())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
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
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedOk.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_earliest_cert_expiry",
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
						types.LabelName:         "probe_ssl_validation_success",
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
			probeDurationMaxValue: 5,
			target: &httpTestTarget{
				TLSCert:     certs.CertLongLivedSelfSigned,
				RootCACerts: []*x509.Certificate{certs.CertLongLivedSelfSigned.Leaf},
			},
		},
		{
			name:         "success-200",
			absentPoints: []map[string]string{},
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
					Point: types.Point{Time: t0, Value: 200},
					Labels: map[string]string{
						types.LabelName:         "probe_http_status_code",
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
						types.LabelName:         "probe_http_duration_seconds",
						types.LabelInstance:     testTargetNotYetKnown,
						types.LabelInstanceUUID: testAgentID,
						types.LabelScraper:      testAgentFQDN,
						types.LabelServiceUUID:  testMonitorID,
						"phase":                 "connect",
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
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedOk.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
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
					Point: types.Point{Time: t0, Value: float64(certs.LongLiveDuration.Seconds())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
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
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedOk.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_earliest_cert_expiry",
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
						types.LabelName:         "probe_ssl_validation_success",
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
			probeDurationMaxValue: 5,
			target: &httpTestTarget{
				TLSCert:     certs.CertLongLivedOk,
				RootCACerts: []*x509.Certificate{certs.RootCA},
			},
		},
		{
			name:         "fail-404",
			absentPoints: []map[string]string{},
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
					Point: types.Point{Time: t0, Value: 404},
					Labels: map[string]string{
						types.LabelName:         "probe_http_status_code",
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
						types.LabelName:         "probe_http_duration_seconds",
						types.LabelInstance:     testTargetNotYetKnown,
						types.LabelInstanceUUID: testAgentID,
						types.LabelScraper:      testAgentFQDN,
						types.LabelServiceUUID:  testMonitorID,
						"phase":                 "connect",
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
				{
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedOk.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
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
					Point: types.Point{Time: t0, Value: float64(certs.LongLiveDuration.Seconds())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
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
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedOk.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_earliest_cert_expiry",
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
						types.LabelName:         "probe_ssl_validation_success",
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
			probeDurationMaxValue: 5,

			target: &httpTestTarget{
				TLSCert:     certs.CertLongLivedOk,
				RootCACerts: []*x509.Certificate{certs.RootCA},
				StatusCode:  404,
			},
		},
		{
			name: "probe-timeout",
			absentPoints: []map[string]string{
				{
					types.LabelName:         "probe_ssl_earliest_cert_expiry",
					types.LabelInstance:     testTargetNotYetKnown,
					types.LabelInstanceUUID: testAgentID,
					types.LabelScraper:      testAgentFQDN,
					types.LabelServiceUUID:  testMonitorID,
				},
				{
					types.LabelName:         "probe_duration_seconds",
					types.LabelInstance:     testTargetNotYetKnown,
					types.LabelInstanceUUID: testAgentID,
					types.LabelScraper:      testAgentFQDN,
					types.LabelServiceUUID:  testMonitorID,
				},
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
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_http_status_code",
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
						types.LabelName:         "probe_http_duration_seconds",
						types.LabelInstance:     testTargetNotYetKnown,
						types.LabelInstanceUUID: testAgentID,
						types.LabelScraper:      testAgentFQDN,
						types.LabelServiceUUID:  testMonitorID,
						"phase":                 "connect",
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
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedOk.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
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
					Point: types.Point{Time: t0, Value: float64(certs.LongLiveDuration.Seconds())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
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
						types.LabelName:         "probe_ssl_validation_success",
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
			target: &httpTestTarget{
				TLSCert:     certs.CertLongLivedOk,
				RootCACerts: []*x509.Certificate{certs.RootCA},
				HTTPDelay:   timeoutTime,
			},
		},
		{
			name: "probe-timeout2-self-signed",
			absentPoints: []map[string]string{
				{
					types.LabelName:         "probe_ssl_earliest_cert_expiry",
					types.LabelInstance:     testTargetNotYetKnown,
					types.LabelInstanceUUID: testAgentID,
					types.LabelScraper:      testAgentFQDN,
					types.LabelServiceUUID:  testMonitorID,
				},
				{
					types.LabelName:         "probe_duration_seconds",
					types.LabelInstance:     testTargetNotYetKnown,
					types.LabelInstanceUUID: testAgentID,
					types.LabelScraper:      testAgentFQDN,
					types.LabelServiceUUID:  testMonitorID,
				},
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
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_http_status_code",
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
						types.LabelName:         "probe_http_duration_seconds",
						types.LabelInstance:     testTargetNotYetKnown,
						types.LabelInstanceUUID: testAgentID,
						types.LabelScraper:      testAgentFQDN,
						types.LabelServiceUUID:  testMonitorID,
						"phase":                 "connect",
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
					Point: types.Point{Time: t0, Value: float64(time.Time{}.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
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
					Point: types.Point{Time: t0, Value: float64(certs.LongLiveDuration.Seconds())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
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
						types.LabelName:         "probe_ssl_validation_success",
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
			target: &httpTestTarget{
				TLSCert:               certs.CertLongLivedSelfSigned,
				RootCACerts:           []*x509.Certificate{certs.RootCA},
				TimeoutAfterHandshake: true,
			},
		},
		{
			name:         "ssl-expire-soon",
			absentPoints: []map[string]string{},
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
					Point: types.Point{Time: t0, Value: 200},
					Labels: map[string]string{
						types.LabelName:         "probe_http_status_code",
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
						types.LabelName:         "probe_http_duration_seconds",
						types.LabelInstance:     testTargetNotYetKnown,
						types.LabelInstanceUUID: testAgentID,
						types.LabelScraper:      testAgentFQDN,
						types.LabelServiceUUID:  testMonitorID,
						"phase":                 "connect",
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
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedCritical.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
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
					Point: types.Point{Time: t0, Value: float64(certs.LongLiveDuration.Seconds())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
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
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedCritical.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_earliest_cert_expiry",
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
						types.LabelName:         "probe_ssl_validation_success",
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
			target: &httpTestTarget{
				TLSCert:     certs.CertLongLivedCritical,
				RootCACerts: []*x509.Certificate{certs.RootCA},
			},
		},
		{
			name:         "ssl-short-lived-expire-warning",
			absentPoints: []map[string]string{},
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
					Point: types.Point{Time: t0, Value: 200},
					Labels: map[string]string{
						types.LabelName:         "probe_http_status_code",
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
						types.LabelName:         "probe_http_duration_seconds",
						types.LabelInstance:     testTargetNotYetKnown,
						types.LabelInstanceUUID: testAgentID,
						types.LabelScraper:      testAgentFQDN,
						types.LabelServiceUUID:  testMonitorID,
						"phase":                 "connect",
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
					Point: types.Point{Time: t0, Value: float64(certs.TSShortLivedWarning.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
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
					Point: types.Point{Time: t0, Value: float64(certs.ShortLiveDuration.Seconds())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
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
					Point: types.Point{Time: t0, Value: float64(certs.TSShortLivedWarning.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_earliest_cert_expiry",
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
						types.LabelName:         "probe_ssl_validation_success",
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
			target: &httpTestTarget{
				TLSCert:     certs.CertShortLivedWarning,
				RootCACerts: []*x509.Certificate{certs.RootCA},
			},
		},
		{
			name:         "ssl-short-lived-expire-critical",
			absentPoints: []map[string]string{},
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
					Point: types.Point{Time: t0, Value: 200},
					Labels: map[string]string{
						types.LabelName:         "probe_http_status_code",
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
						types.LabelName:         "probe_http_duration_seconds",
						types.LabelInstance:     testTargetNotYetKnown,
						types.LabelInstanceUUID: testAgentID,
						types.LabelScraper:      testAgentFQDN,
						types.LabelServiceUUID:  testMonitorID,
						"phase":                 "connect",
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
					Point: types.Point{Time: t0, Value: float64(certs.TSShortLivedCritical.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
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
					Point: types.Point{Time: t0, Value: float64(certs.ShortLiveDuration.Seconds())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
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
					Point: types.Point{Time: t0, Value: float64(certs.TSShortLivedCritical.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_earliest_cert_expiry",
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
						types.LabelName:         "probe_ssl_validation_success",
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
			target: &httpTestTarget{
				TLSCert:     certs.CertShortLivedCritical,
				RootCACerts: []*x509.Certificate{certs.RootCA},
			},
		},
		{
			name:         "intermediary-ca",
			absentPoints: []map[string]string{},
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
					Point: types.Point{Time: t0, Value: 200},
					Labels: map[string]string{
						types.LabelName:         "probe_http_status_code",
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
						types.LabelName:         "probe_http_duration_seconds",
						types.LabelInstance:     testTargetNotYetKnown,
						types.LabelInstanceUUID: testAgentID,
						types.LabelScraper:      testAgentFQDN,
						types.LabelServiceUUID:  testMonitorID,
						"phase":                 "connect",
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
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedOk.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
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
					Point: types.Point{Time: t0, Value: float64(certs.LongLiveDuration.Seconds())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
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
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedOk.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_earliest_cert_expiry",
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
						types.LabelName:         "probe_ssl_validation_success",
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
			target: &httpTestTarget{
				TLSCert:     certs.CertSubCAExpireFar,
				RootCACerts: []*x509.Certificate{certs.RootCA},
			},
		},
		{
			name:         "intermediary-ca-old-intermediary",
			absentPoints: []map[string]string{},
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
					Point: types.Point{Time: t0, Value: 200},
					Labels: map[string]string{
						types.LabelName:         "probe_http_status_code",
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
						types.LabelName:         "probe_http_duration_seconds",
						types.LabelInstance:     testTargetNotYetKnown,
						types.LabelInstanceUUID: testAgentID,
						types.LabelScraper:      testAgentFQDN,
						types.LabelServiceUUID:  testMonitorID,
						"phase":                 "connect",
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
					Point: types.Point{Time: t0, Value: float64(certs.TSCACritical.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
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
					Point: types.Point{Time: t0, Value: float64(certs.LongLiveDuration.Seconds())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
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
					Point: types.Point{Time: t0, Value: float64(certs.TSCACritical.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_earliest_cert_expiry",
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
						types.LabelName:         "probe_ssl_validation_success",
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
			target: &httpTestTarget{
				TLSCert:     certs.CertSubCAExpireFarOldIntermediary,
				RootCACerts: []*x509.Certificate{certs.RootCA},
			},
		},
		{
			name:         "expired-unneeded-certs",
			absentPoints: []map[string]string{},
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
					Point: types.Point{Time: t0, Value: 200},
					Labels: map[string]string{
						types.LabelName:         "probe_http_status_code",
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
						types.LabelName:         "probe_http_duration_seconds",
						types.LabelInstance:     testTargetNotYetKnown,
						types.LabelInstanceUUID: testAgentID,
						types.LabelScraper:      testAgentFQDN,
						types.LabelServiceUUID:  testMonitorID,
						"phase":                 "connect",
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
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedOk.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
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
					Point: types.Point{Time: t0, Value: float64(certs.LongLiveDuration.Seconds())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
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
					Point: types.Point{Time: t0, Value: float64(certs.TSCAExpired.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_earliest_cert_expiry",
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
						types.LabelName:         "probe_ssl_validation_success",
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
			target: &httpTestTarget{
				TLSCert:     certs.CertUselessExpiredIntermediary,
				RootCACerts: []*x509.Certificate{certs.RootCA},
			},
		},
		{
			name:         "intermediary-ca-intermediary-expire-soon",
			absentPoints: []map[string]string{},
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
					Point: types.Point{Time: t0, Value: 200},
					Labels: map[string]string{
						types.LabelName:         "probe_http_status_code",
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
						types.LabelName:         "probe_http_duration_seconds",
						types.LabelInstance:     testTargetNotYetKnown,
						types.LabelInstanceUUID: testAgentID,
						types.LabelScraper:      testAgentFQDN,
						types.LabelServiceUUID:  testMonitorID,
						"phase":                 "connect",
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
					Point: types.Point{Time: t0, Value: float64(certs.TSCACritical.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
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
					Point: types.Point{Time: t0, Value: float64(certs.LongLiveDuration.Seconds())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
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
					Point: types.Point{Time: t0, Value: float64(certs.TSCACritical.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_earliest_cert_expiry",
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
						types.LabelName:         "probe_ssl_validation_success",
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
			target: &httpTestTarget{
				TLSCert:     certs.CertSubCAWithCAExpireSoon,
				RootCACerts: []*x509.Certificate{certs.RootCA},
			},
		},
		{
			name:         "missing-intermediary",
			absentPoints: []map[string]string{},
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
					Point: types.Point{Time: t0, Value: 200},
					Labels: map[string]string{
						types.LabelName:         "probe_http_status_code",
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
						types.LabelName:         "probe_http_duration_seconds",
						types.LabelInstance:     testTargetNotYetKnown,
						types.LabelInstanceUUID: testAgentID,
						types.LabelScraper:      testAgentFQDN,
						types.LabelServiceUUID:  testMonitorID,
						"phase":                 "connect",
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
					Point: types.Point{Time: t0, Value: float64(time.Time{}.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
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
					Point: types.Point{Time: t0, Value: float64(certs.LongLiveDuration.Seconds())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
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
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedOk.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_earliest_cert_expiry",
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
						types.LabelName:         "probe_ssl_validation_success",
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
			target: &httpTestTarget{
				TLSCert:     certs.CertMissingIntermediary,
				RootCACerts: []*x509.Certificate{certs.RootCA},
			},
		},
		{
			name:         "tls-self-signed",
			absentPoints: []map[string]string{},
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
					Point: types.Point{Time: t0, Value: 200},
					Labels: map[string]string{
						types.LabelName:         "probe_http_status_code",
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
						types.LabelName:         "probe_http_duration_seconds",
						types.LabelInstance:     testTargetNotYetKnown,
						types.LabelInstanceUUID: testAgentID,
						types.LabelScraper:      testAgentFQDN,
						types.LabelServiceUUID:  testMonitorID,
						"phase":                 "connect",
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
					Point: types.Point{Time: t0, Value: float64(time.Time{}.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
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
					Point: types.Point{Time: t0, Value: float64(certs.LongLiveDuration.Seconds())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
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
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedOk.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_earliest_cert_expiry",
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
						types.LabelName:         "probe_ssl_validation_success",
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
			target: &httpTestTarget{
				TLSCert:     certs.CertLongLivedSelfSigned,
				RootCACerts: []*x509.Certificate{certs.RootCA},
			},
		},
		{
			name:         "tls-self-signed-expiring-warning",
			absentPoints: []map[string]string{},
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
					Point: types.Point{Time: t0, Value: 200},
					Labels: map[string]string{
						types.LabelName:         "probe_http_status_code",
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
						types.LabelName:         "probe_http_duration_seconds",
						types.LabelInstance:     testTargetNotYetKnown,
						types.LabelInstanceUUID: testAgentID,
						types.LabelScraper:      testAgentFQDN,
						types.LabelServiceUUID:  testMonitorID,
						"phase":                 "connect",
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
					Point: types.Point{Time: t0, Value: float64(time.Time{}.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
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
					Point: types.Point{Time: t0, Value: float64(certs.LongLiveDuration.Seconds())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
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
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedWarning.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_earliest_cert_expiry",
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
						types.LabelName:         "probe_ssl_validation_success",
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
			target: &httpTestTarget{
				TLSCert:     certs.CertLongLivedSelfSignedWarning,
				RootCACerts: []*x509.Certificate{certs.RootCA},
			},
		},
		{
			name:         "tls-self-signed-expiring",
			absentPoints: []map[string]string{},
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
					Point: types.Point{Time: t0, Value: 200},
					Labels: map[string]string{
						types.LabelName:         "probe_http_status_code",
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
						types.LabelName:         "probe_http_duration_seconds",
						types.LabelInstance:     testTargetNotYetKnown,
						types.LabelInstanceUUID: testAgentID,
						types.LabelScraper:      testAgentFQDN,
						types.LabelServiceUUID:  testMonitorID,
						"phase":                 "connect",
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
					Point: types.Point{Time: t0, Value: float64(time.Time{}.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
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
					Point: types.Point{Time: t0, Value: float64(certs.LongLiveDuration.Seconds())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
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
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedExpired.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_earliest_cert_expiry",
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
						types.LabelName:         "probe_ssl_validation_success",
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
			target: &httpTestTarget{
				TLSCert:     certs.CertLongLivedSelfSignedExpired,
				RootCACerts: []*x509.Certificate{certs.RootCA},
			},
		},
		{
			name:         "tls-expired",
			absentPoints: []map[string]string{},
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
					Point: types.Point{Time: t0, Value: 200},
					Labels: map[string]string{
						types.LabelName:         "probe_http_status_code",
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
						types.LabelName:         "probe_http_duration_seconds",
						types.LabelInstance:     testTargetNotYetKnown,
						types.LabelInstanceUUID: testAgentID,
						types.LabelScraper:      testAgentFQDN,
						types.LabelServiceUUID:  testMonitorID,
						"phase":                 "connect",
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
					Point: types.Point{Time: t0, Value: float64(time.Time{}.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
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
					Point: types.Point{Time: t0, Value: float64(certs.LongLiveDuration.Seconds())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
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
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedExpired.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_earliest_cert_expiry",
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
						types.LabelName:         "probe_ssl_validation_success",
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
			target: &httpTestTarget{
				TLSCert:     certs.CertLongLivedExpired,
				RootCACerts: []*x509.Certificate{certs.RootCA},
			},
		},
		{
			name:         "tls-redirect-error",
			absentPoints: []map[string]string{},
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
					Point: types.Point{Time: t0, Value: 200},
					Labels: map[string]string{
						types.LabelName:         "probe_http_status_code",
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
						types.LabelName:         "probe_http_duration_seconds",
						types.LabelInstance:     testTargetNotYetKnown,
						types.LabelInstanceUUID: testAgentID,
						types.LabelScraper:      testAgentFQDN,
						types.LabelServiceUUID:  testMonitorID,
						"phase":                 "connect",
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
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedOk.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
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
					Point: types.Point{Time: t0, Value: float64(certs.LongLiveDuration.Seconds())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
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
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedOk.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_earliest_cert_expiry",
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
						types.LabelName:         "probe_ssl_validation_success",
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
			probeDurationMaxValue: 5,
			target: &httpTestTarget{
				TLSCert:            certs.CertLongLivedOk,
				FirstTLSCert:       certs.CertLongLivedExpired,
				RootCACerts:        []*x509.Certificate{certs.RootCA},
				UseHTTPRedirection: true,
			},
		},
		{
			name: "timeout-tls-handshake",
			absentPoints: []map[string]string{
				{
					types.LabelName:         "probe_ssl_earliest_cert_expiry",
					types.LabelInstance:     testTargetNotYetKnown,
					types.LabelInstanceUUID: testAgentID,
					types.LabelScraper:      testAgentFQDN,
					types.LabelServiceUUID:  testMonitorID,
				},
				{
					types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
					types.LabelInstance:     testTargetNotYetKnown,
					types.LabelInstanceUUID: testAgentID,
					types.LabelScraper:      testAgentFQDN,
					types.LabelServiceUUID:  testMonitorID,
				},
				{
					types.LabelName:         "probe_duration_seconds",
					types.LabelInstance:     testTargetNotYetKnown,
					types.LabelInstanceUUID: testAgentID,
					types.LabelScraper:      testAgentFQDN,
					types.LabelServiceUUID:  testMonitorID,
				},
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
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_validation_success",
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
					Point: types.Point{Time: t0, Value: float64(time.Time{}.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
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
						types.LabelName:         "probe_http_status_code",
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
						types.LabelName:         "probe_http_duration_seconds",
						types.LabelInstance:     testTargetNotYetKnown,
						types.LabelInstanceUUID: testAgentID,
						types.LabelScraper:      testAgentFQDN,
						types.LabelServiceUUID:  testMonitorID,
						"phase":                 "connect",
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
			},
			// blackbox exporter have a default timeout of 10 second for TLS handshake.
			probeDurationMinValue: 10,
			probeDurationMaxValue: defaultTimeout.Seconds(),

			target: &httpTestTarget{
				TLSCert:               certs.CertLongLivedOk,
				RootCACerts:           []*x509.Certificate{certs.RootCA},
				TimeoutInTLSHandshake: true,
			},
		},
		{
			// This test don't work as expected. It's a timeout, but not where I initially expected.
			// It's not the same as trying to connect to blackhole IP (e.g. http://1.2.3.4):
			// * when connecting to blackhole target, we receive no response at all and we hit the context deadline
			// * in this test, the TCP connection is established (so TCP syn packet is received and we
			//   timeout in the TLS handshake).
			//   At the end this test is probably the same as timeout-tls-handshake
			name: "timeout-in-tcp-accept",
			absentPoints: []map[string]string{
				{
					types.LabelName:         "probe_ssl_earliest_cert_expiry",
					types.LabelInstance:     testTargetNotYetKnown,
					types.LabelInstanceUUID: testAgentID,
					types.LabelScraper:      testAgentFQDN,
					types.LabelServiceUUID:  testMonitorID,
				},
				{
					types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
					types.LabelInstance:     testTargetNotYetKnown,
					types.LabelInstanceUUID: testAgentID,
					types.LabelScraper:      testAgentFQDN,
					types.LabelServiceUUID:  testMonitorID,
				},
				{
					types.LabelName:         "probe_duration_seconds",
					types.LabelInstance:     testTargetNotYetKnown,
					types.LabelInstanceUUID: testAgentID,
					types.LabelScraper:      testAgentFQDN,
					types.LabelServiceUUID:  testMonitorID,
				},
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
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_validation_success",
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
					Point: types.Point{Time: t0, Value: float64(time.Time{}.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
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
						types.LabelName:         "probe_http_status_code",
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
						types.LabelName:         "probe_http_duration_seconds",
						types.LabelInstance:     testTargetNotYetKnown,
						types.LabelInstanceUUID: testAgentID,
						types.LabelScraper:      testAgentFQDN,
						types.LabelServiceUUID:  testMonitorID,
						"phase":                 "connect",
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
			},
			// blackbox exporter have a default timeout of 10 second for TLS handshake.
			probeDurationMinValue: 10,
			probeDurationMaxValue: defaultTimeout.Seconds(),

			target: &httpTestTarget{
				TLSCert:            certs.CertLongLivedOk,
				RootCACerts:        []*x509.Certificate{certs.RootCA},
				TimeoutInTCPAccept: true,
			},
		},
		{
			name: "connection-refused",
			absentPoints: []map[string]string{
				{
					types.LabelName:         "probe_ssl_earliest_cert_expiry",
					types.LabelInstance:     testTargetNotYetKnown,
					types.LabelInstanceUUID: testAgentID,
					types.LabelScraper:      testAgentFQDN,
					types.LabelServiceUUID:  testMonitorID,
				},
				{
					types.LabelName:         "probe_ssl_validation_success",
					types.LabelInstance:     testTargetNotYetKnown,
					types.LabelInstanceUUID: testAgentID,
					types.LabelScraper:      testAgentFQDN,
					types.LabelServiceUUID:  testMonitorID,
				},
				{
					types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
					types.LabelInstance:     testTargetNotYetKnown,
					types.LabelInstanceUUID: testAgentID,
					types.LabelScraper:      testAgentFQDN,
					types.LabelServiceUUID:  testMonitorID,
				},
				{
					types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
					types.LabelInstance:     testTargetNotYetKnown,
					types.LabelInstanceUUID: testAgentID,
					types.LabelScraper:      testAgentFQDN,
					types.LabelServiceUUID:  testMonitorID,
				},
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
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_http_status_code",
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
						types.LabelName:         "probe_http_duration_seconds",
						types.LabelInstance:     testTargetNotYetKnown,
						types.LabelInstanceUUID: testAgentID,
						types.LabelScraper:      testAgentFQDN,
						types.LabelServiceUUID:  testMonitorID,
						"phase":                 "connect",
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
			},
			target: &httpTestTarget{
				TLSCert:       certs.CertLongLivedOk,
				RootCACerts:   []*x509.Certificate{certs.RootCA},
				ServerStopped: true,
			},
		},
		{
			name: "broken-crypto",
			absentPoints: []map[string]string{
				{
					types.LabelName:         "probe_ssl_earliest_cert_expiry",
					types.LabelInstance:     testTargetNotYetKnown,
					types.LabelInstanceUUID: testAgentID,
					types.LabelScraper:      testAgentFQDN,
					types.LabelServiceUUID:  testMonitorID,
				},
				{
					types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
					types.LabelInstance:     testTargetNotYetKnown,
					types.LabelInstanceUUID: testAgentID,
					types.LabelScraper:      testAgentFQDN,
					types.LabelServiceUUID:  testMonitorID,
				},
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
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_http_status_code",
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
						types.LabelName:         "probe_http_duration_seconds",
						types.LabelInstance:     testTargetNotYetKnown,
						types.LabelInstanceUUID: testAgentID,
						types.LabelScraper:      testAgentFQDN,
						types.LabelServiceUUID:  testMonitorID,
						"phase":                 "connect",
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
					Point: types.Point{Time: t0, Value: float64(time.Time{}.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
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
						types.LabelName:         "probe_ssl_validation_success",
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
			target: &httpTestTarget{
				TLSCert:         certs.CertLongLivedOk,
				RootCACerts:     []*x509.Certificate{certs.RootCA},
				UseBrokenCrypto: true,
			},
		},
		{
			name: "break-connection-in-http",
			absentPoints: []map[string]string{
				{
					types.LabelName:         "probe_ssl_earliest_cert_expiry",
					types.LabelInstance:     testTargetNotYetKnown,
					types.LabelInstanceUUID: testAgentID,
					types.LabelScraper:      testAgentFQDN,
					types.LabelServiceUUID:  testMonitorID,
				},
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
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_http_status_code",
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
						types.LabelName:         "probe_http_duration_seconds",
						types.LabelInstance:     testTargetNotYetKnown,
						types.LabelInstanceUUID: testAgentID,
						types.LabelScraper:      testAgentFQDN,
						types.LabelServiceUUID:  testMonitorID,
						"phase":                 "connect",
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
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedOk.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
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
					Point: types.Point{Time: t0, Value: float64(certs.LongLiveDuration.Seconds())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
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
						types.LabelName:         "probe_ssl_validation_success",
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
			target: &httpTestTarget{
				TLSCert:     certs.CertLongLivedOk,
				RootCACerts: []*x509.Certificate{certs.RootCA},
				CloseInHTTP: true,
			},
		},
		{
			name: "break-connection-in-tls",
			absentPoints: []map[string]string{
				{
					types.LabelName:         "probe_ssl_earliest_cert_expiry",
					types.LabelInstance:     testTargetNotYetKnown,
					types.LabelInstanceUUID: testAgentID,
					types.LabelScraper:      testAgentFQDN,
					types.LabelServiceUUID:  testMonitorID,
				},
				{
					types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
					types.LabelInstance:     testTargetNotYetKnown,
					types.LabelInstanceUUID: testAgentID,
					types.LabelScraper:      testAgentFQDN,
					types.LabelServiceUUID:  testMonitorID,
				},
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
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_http_status_code",
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
						types.LabelName:         "probe_http_duration_seconds",
						types.LabelInstance:     testTargetNotYetKnown,
						types.LabelInstanceUUID: testAgentID,
						types.LabelScraper:      testAgentFQDN,
						types.LabelServiceUUID:  testMonitorID,
						"phase":                 "connect",
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
					Point: types.Point{Time: t0, Value: float64(time.Time{}.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
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
						types.LabelName:         "probe_ssl_validation_success",
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
			target: &httpTestTarget{
				TLSCert:             certs.CertLongLivedOk,
				RootCACerts:         []*x509.Certificate{certs.RootCA},
				CloseInTLSHandshake: true,
			},
		},
		{
			name: "http-bad-dns",
			absentPoints: []map[string]string{
				{
					types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
					types.LabelInstance:     testTargetNotYetKnown,
					types.LabelInstanceUUID: testAgentID,
					types.LabelScraper:      testAgentFQDN,
					types.LabelServiceUUID:  testMonitorID,
				},
				{
					types.LabelName:         "probe_ssl_earliest_cert_expiry",
					types.LabelInstance:     testTargetNotYetKnown,
					types.LabelInstanceUUID: testAgentID,
					types.LabelScraper:      testAgentFQDN,
					types.LabelServiceUUID:  testMonitorID,
				},
				{
					types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
					types.LabelInstance:     testTargetNotYetKnown,
					types.LabelInstanceUUID: testAgentID,
					types.LabelScraper:      testAgentFQDN,
					types.LabelServiceUUID:  testMonitorID,
				},
				{
					types.LabelName:         "probe_ssl_validation_success",
					types.LabelInstance:     testTargetNotYetKnown,
					types.LabelInstanceUUID: testAgentID,
					types.LabelScraper:      testAgentFQDN,
					types.LabelServiceUUID:  testMonitorID,
				},
				{
					types.LabelName:         "probe_http_duration_seconds",
					types.LabelInstance:     testTargetNotYetKnown,
					types.LabelInstanceUUID: testAgentID,
					types.LabelScraper:      testAgentFQDN,
					types.LabelServiceUUID:  testMonitorID,
					"phase":                 "connect",
				},
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
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_http_status_code",
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
			target: HTTPTargetWithNoDNS{useSSL: false},
		},
		{
			name: "https-bad-dns",
			absentPoints: []map[string]string{
				{
					types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
					types.LabelInstance:     testTargetNotYetKnown,
					types.LabelInstanceUUID: testAgentID,
					types.LabelScraper:      testAgentFQDN,
					types.LabelServiceUUID:  testMonitorID,
				},
				{
					types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
					types.LabelInstance:     testTargetNotYetKnown,
					types.LabelInstanceUUID: testAgentID,
					types.LabelScraper:      testAgentFQDN,
					types.LabelServiceUUID:  testMonitorID,
				},
				{
					types.LabelName:         "probe_ssl_earliest_cert_expiry",
					types.LabelInstance:     testTargetNotYetKnown,
					types.LabelInstanceUUID: testAgentID,
					types.LabelScraper:      testAgentFQDN,
					types.LabelServiceUUID:  testMonitorID,
				},
				{
					types.LabelName:         "probe_ssl_validation_success",
					types.LabelInstance:     testTargetNotYetKnown,
					types.LabelInstanceUUID: testAgentID,
					types.LabelScraper:      testAgentFQDN,
					types.LabelServiceUUID:  testMonitorID,
				},
				{
					types.LabelName:         "probe_http_duration_seconds",
					types.LabelInstance:     testTargetNotYetKnown,
					types.LabelInstanceUUID: testAgentID,
					types.LabelScraper:      testAgentFQDN,
					types.LabelServiceUUID:  testMonitorID,
					"phase":                 "connect",
				},
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
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_http_status_code",
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
			target: HTTPTargetWithNoDNS{useSSL: true},
		},
		{
			name: "https-502-after-10seconds",
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
					Point: types.Point{Time: t0, Value: http.StatusBadGateway},
					Labels: map[string]string{
						types.LabelName:         "probe_http_status_code",
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
						types.LabelName:         "probe_http_duration_seconds",
						types.LabelInstance:     testTargetNotYetKnown,
						types.LabelInstanceUUID: testAgentID,
						types.LabelScraper:      testAgentFQDN,
						types.LabelServiceUUID:  testMonitorID,
						"phase":                 "connect",
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
				{
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedOk.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
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
					Point: types.Point{Time: t0, Value: float64(certs.LongLiveDuration.Seconds())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
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
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedOk.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_earliest_cert_expiry",
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
						types.LabelName:         "probe_ssl_validation_success",
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
			probeDurationMinValue: 10,

			target: &httpTestTarget{
				TLSCert:     certs.CertLongLivedOk,
				RootCACerts: []*x509.Certificate{certs.RootCA},
				HTTPDelay:   10 * time.Second,
				StatusCode:  http.StatusBadGateway,
			},
		},
		{
			name:         "with-redirect-http-then-https",
			absentPoints: []map[string]string{},
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
					Point: types.Point{Time: t0, Value: 200},
					Labels: map[string]string{
						types.LabelName:         "probe_http_status_code",
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
						types.LabelName:         "probe_http_duration_seconds",
						types.LabelInstance:     testTargetNotYetKnown,
						types.LabelInstanceUUID: testAgentID,
						types.LabelScraper:      testAgentFQDN,
						types.LabelServiceUUID:  testMonitorID,
						"phase":                 "connect",
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
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedOk.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
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
					Point: types.Point{Time: t0, Value: float64(certs.LongLiveDuration.Seconds())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
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
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedOk.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_earliest_cert_expiry",
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
						types.LabelName:         "probe_ssl_validation_success",
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
			probeDurationMaxValue: 5,
			target: &httpTestTarget{
				TLSCert:            certs.CertLongLivedOk,
				RootCACerts:        []*x509.Certificate{certs.RootCA},
				UseHTTPRedirection: true,
			},
		},
		{
			name:         "with-redirect-short-lived-then-long-lived",
			absentPoints: []map[string]string{},
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
					Point: types.Point{Time: t0, Value: 200},
					Labels: map[string]string{
						types.LabelName:         "probe_http_status_code",
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
						types.LabelName:         "probe_http_duration_seconds",
						types.LabelInstance:     testTargetNotYetKnown,
						types.LabelInstanceUUID: testAgentID,
						types.LabelScraper:      testAgentFQDN,
						types.LabelServiceUUID:  testMonitorID,
						"phase":                 "connect",
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
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedOk.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
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
					Point: types.Point{Time: t0, Value: float64(certs.LongLiveDuration.Seconds())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
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
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedOk.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_earliest_cert_expiry",
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
						types.LabelName:         "probe_ssl_validation_success",
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
			probeDurationMaxValue: 5,
			target: &httpTestTarget{
				TLSCert:            certs.CertLongLivedOk,
				FirstTLSCert:       certs.CertShortLivedCritical,
				RootCACerts:        []*x509.Certificate{certs.RootCA},
				UseHTTPRedirection: true,
			},
		},
		{
			name:         "with-redirect-long-lived-then-short-lived",
			absentPoints: []map[string]string{},
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
					Point: types.Point{Time: t0, Value: 200},
					Labels: map[string]string{
						types.LabelName:         "probe_http_status_code",
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
						types.LabelName:         "probe_http_duration_seconds",
						types.LabelInstance:     testTargetNotYetKnown,
						types.LabelInstanceUUID: testAgentID,
						types.LabelScraper:      testAgentFQDN,
						types.LabelServiceUUID:  testMonitorID,
						"phase":                 "connect",
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
					Point: types.Point{Time: t0, Value: float64(certs.TSShortLivedCritical.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
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
					Point: types.Point{Time: t0, Value: float64(certs.ShortLiveDuration.Seconds())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
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
					Point: types.Point{Time: t0, Value: float64(certs.TSShortLivedCritical.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_earliest_cert_expiry",
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
						types.LabelName:         "probe_ssl_validation_success",
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
			probeDurationMaxValue: 5,
			target: &httpTestTarget{
				TLSCert:            certs.CertShortLivedCritical,
				FirstTLSCert:       certs.CertLongLivedOk,
				RootCACerts:        []*x509.Certificate{certs.RootCA},
				UseHTTPRedirection: true,
			},
		},
		{
			name:         "with-redirect-expired-then-ok",
			absentPoints: []map[string]string{},
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
					Point: types.Point{Time: t0, Value: 200},
					Labels: map[string]string{
						types.LabelName:         "probe_http_status_code",
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
						types.LabelName:         "probe_http_duration_seconds",
						types.LabelInstance:     testTargetNotYetKnown,
						types.LabelInstanceUUID: testAgentID,
						types.LabelScraper:      testAgentFQDN,
						types.LabelServiceUUID:  testMonitorID,
						"phase":                 "connect",
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
					Point: types.Point{Time: t0, Value: float64(certs.TSShortLivedCritical.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
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
					Point: types.Point{Time: t0, Value: float64(certs.ShortLiveDuration.Seconds())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
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
					Point: types.Point{Time: t0, Value: float64(certs.TSShortLivedCritical.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_earliest_cert_expiry",
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
						types.LabelName:         "probe_ssl_validation_success",
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
			probeDurationMaxValue: 5,
			target: &httpTestTarget{
				TLSCert:            certs.CertShortLivedCritical,
				FirstTLSCert:       certs.CertLongLivedExpired,
				RootCACerts:        []*x509.Certificate{certs.RootCA},
				UseHTTPRedirection: true,
			},
		},
		{
			name:         "with-redirect-ok-then-expired",
			absentPoints: []map[string]string{},
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
					Point: types.Point{Time: t0, Value: 200},
					Labels: map[string]string{
						types.LabelName:         "probe_http_status_code",
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
						types.LabelName:         "probe_http_duration_seconds",
						types.LabelInstance:     testTargetNotYetKnown,
						types.LabelInstanceUUID: testAgentID,
						types.LabelScraper:      testAgentFQDN,
						types.LabelServiceUUID:  testMonitorID,
						"phase":                 "connect",
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
					Point: types.Point{Time: t0, Value: float64(time.Time{}.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
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
					Point: types.Point{Time: t0, Value: float64(certs.LongLiveDuration.Seconds())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
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
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedExpired.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_earliest_cert_expiry",
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
						types.LabelName:         "probe_ssl_validation_success",
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
			probeDurationMaxValue: 5,
			target: &httpTestTarget{
				TLSCert:            certs.CertLongLivedExpired,
				FirstTLSCert:       certs.CertShortLivedCritical,
				RootCACerts:        []*x509.Certificate{certs.RootCA},
				UseHTTPRedirection: true,
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

// HTTPTargetWithNoDNS is a target whose URL use a unknown DNS name.
type HTTPTargetWithNoDNS struct {
	useSSL bool
}

func (t HTTPTargetWithNoDNS) Start() {
}

func (t HTTPTargetWithNoDNS) Close() {
}

func (t HTTPTargetWithNoDNS) URL() string {
	if t.useSSL {
		return "https://this-does-not-exists.bleemeo.com:81"
	}

	return "http://this-does-not-exists.bleemeo.com:81"
}

func (t HTTPTargetWithNoDNS) RootCACertificates() []*x509.Certificate {
	return nil
}

func (t HTTPTargetWithNoDNS) RequestContext(ctx context.Context) context.Context {
	return ctx
}

type httpTestTarget struct {
	TimeoutInTCPAccept    bool
	TimeoutInTLSHandshake bool
	TimeoutAfterHandshake bool
	HTTPDelay             time.Duration
	ServerStopped         bool
	CloseInTLSHandshake   bool
	CloseInHTTP           bool
	StatusCode            int // StatusCode of 0 will be replaced by the default 200.
	UseBrokenCrypto       bool
	TLSCert               tls.Certificate
	FirstTLSCert          tls.Certificate
	RootCACerts           []*x509.Certificate
	UseHTTPRedirection    bool
	srvLast               *httptest.Server
	srvFirst              *httptest.Server
}

type wrapListenner struct {
	net.Listener

	Timeout bool
}

func (w wrapListenner) Accept() (net.Conn, error) {
	if w.Timeout {
		time.Sleep(timeoutTime)
	}

	return w.Listener.Accept()
}

func (t *httpTestTarget) Start() {
	t.srvLast = httptest.NewUnstartedServer(http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		if t.HTTPDelay > 0 {
			time.Sleep(t.HTTPDelay)
		}

		if t.CloseInHTTP {
			hj, ok := rw.(http.Hijacker)
			if !ok {
				panic("can't hijack, so can test broken connection")
			}

			conn, _, err := hj.Hijack()
			if err != nil {
				panic("can't hijack, so can test broken connection")
			}

			_ = conn.Close()

			return
		}

		if t.StatusCode != 0 {
			rw.WriteHeader(t.StatusCode)
		}

		_, _ = rw.Write([]byte("ok"))
	}))

	t.srvLast.Listener = wrapListenner{
		Listener: t.srvLast.Listener,
		Timeout:  t.TimeoutInTCPAccept,
	}

	if t.TLSCert.PrivateKey != nil {
		t.srvLast.TLS = &tls.Config{ //nolint: gosec
			// Certificates will in reality come from GetCertificate, but for
			// StartTLS to not override our cert, we must set a value.
			// Once StartTLS we will remove Certificates to rely only on GetCertificate
			Certificates: []tls.Certificate{t.TLSCert},
		}

		t.srvLast.TLS.GetCertificate = func(chi *tls.ClientHelloInfo) (*tls.Certificate, error) {
			if t.TimeoutInTLSHandshake {
				time.Sleep(timeoutTime)
			}

			if t.CloseInTLSHandshake {
				_ = chi.Conn.Close()
			}

			return &t.TLSCert, nil
		}

		if t.UseBrokenCrypto {
			t.srvLast.TLS.MaxVersion = tls.VersionSSL30 //nolint: staticcheck,nolintlint
		}

		t.srvLast.StartTLS()

		t.srvLast.TLS.Certificates = nil // Set to nil, we rely on GetCertificate()
	} else {
		t.srvLast.Start()
	}

	if t.UseHTTPRedirection {
		t.srvFirst = httptest.NewUnstartedServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			http.Redirect(rw, req, t.srvLast.URL, http.StatusTemporaryRedirect)
		}))

		if t.FirstTLSCert.PrivateKey != nil {
			t.srvFirst.TLS = &tls.Config{ //nolint: gosec
				Certificates: []tls.Certificate{t.FirstTLSCert},
			}

			t.srvFirst.StartTLS()
		} else {
			t.srvFirst.Start()
		}
	}

	if t.ServerStopped {
		t.srvLast.Close()
	}
}

func (t *httpTestTarget) URL() string {
	if t.srvFirst != nil {
		return t.srvFirst.URL
	}

	return t.srvLast.URL
}

func (t *httpTestTarget) Close() {
	if t.srvFirst != nil {
		t.srvFirst.Close()
	}

	t.srvLast.Close()
}

func (t *httpTestTarget) RootCACertificates() []*x509.Certificate {
	return t.RootCACerts
}

func (t *httpTestTarget) RequestContext(ctx context.Context) context.Context {
	return httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		TLSHandshakeDone: func(_ tls.ConnectionState, _ error) {
			if t.TimeoutAfterHandshake {
				time.Sleep(timeoutTime)
			}
		},
	})
}

// Test_Collect_TCP tests tcp:// and ssl:// probes.
func Test_Collect_TCP(t *testing.T) { //nolint:maintidx
	t0 := time.Now().Truncate(time.Second)

	certs, err := generateCerts(t, t0)
	if err != nil {
		t.Fatal(err)
	}

	httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))

	tests := []testCase{
		{
			name: "ssl-success-200",
			absentPoints: []map[string]string{
				{
					types.LabelName:         "probe_failed_due_to_tls_error",
					types.LabelInstance:     testTargetNotYetKnown,
					types.LabelInstanceUUID: testAgentID,
					types.LabelScraper:      testAgentFQDN,
					types.LabelServiceUUID:  testMonitorID,
				},
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
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedOk.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
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
					Point: types.Point{Time: t0, Value: float64(certs.LongLiveDuration.Seconds())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
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
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedOk.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_earliest_cert_expiry",
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
						types.LabelName:         "probe_ssl_validation_success",
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
			probeDurationMaxValue: 5,

			target: &httpTestTarget{
				TLSCert:     certs.CertLongLivedOk,
				RootCACerts: []*x509.Certificate{certs.RootCA},
			},
		},
		{
			name: "ssl-expire-soon",
			absentPoints: []map[string]string{
				{
					types.LabelName:         "probe_failed_due_to_tls_error",
					types.LabelInstance:     testTargetNotYetKnown,
					types.LabelInstanceUUID: testAgentID,
					types.LabelScraper:      testAgentFQDN,
					types.LabelServiceUUID:  testMonitorID,
				},
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
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedCritical.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
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
					Point: types.Point{Time: t0, Value: float64(certs.LongLiveDuration.Seconds())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
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
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedCritical.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_earliest_cert_expiry",
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
						types.LabelName:         "probe_ssl_validation_success",
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
			target: &httpTestTarget{
				TLSCert:     certs.CertLongLivedCritical,
				RootCACerts: []*x509.Certificate{certs.RootCA},
			},
		},
		{
			name: "expired-unneeded-certs",
			absentPoints: []map[string]string{
				{
					types.LabelName:         "probe_failed_due_to_tls_error",
					types.LabelInstance:     testTargetNotYetKnown,
					types.LabelInstanceUUID: testAgentID,
					types.LabelScraper:      testAgentFQDN,
					types.LabelServiceUUID:  testMonitorID,
				},
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
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedOk.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
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
					Point: types.Point{Time: t0, Value: float64(certs.LongLiveDuration.Seconds())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
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
					Point: types.Point{Time: t0, Value: float64(certs.TSCAExpired.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_earliest_cert_expiry",
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
						types.LabelName:         "probe_ssl_validation_success",
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
			target: &httpTestTarget{
				TLSCert:     certs.CertUselessExpiredIntermediary,
				RootCACerts: []*x509.Certificate{certs.RootCA},
			},
		},
		{
			name: "tls-self-signed",
			absentPoints: []map[string]string{
				{
					types.LabelName:         "probe_failed_due_to_tls_error",
					types.LabelInstance:     testTargetNotYetKnown,
					types.LabelInstanceUUID: testAgentID,
					types.LabelScraper:      testAgentFQDN,
					types.LabelServiceUUID:  testMonitorID,
				},
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
					Point: types.Point{Time: t0, Value: float64(time.Time{}.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
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
					Point: types.Point{Time: t0, Value: float64(certs.LongLiveDuration.Seconds())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
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
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedOk.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_earliest_cert_expiry",
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
						types.LabelName:         "probe_ssl_validation_success",
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
			target: &httpTestTarget{
				TLSCert:     certs.CertLongLivedSelfSigned,
				RootCACerts: []*x509.Certificate{certs.RootCA},
			},
		},
		{
			name: "tls-self-signed-expiring-warning",
			absentPoints: []map[string]string{
				{
					types.LabelName:         "probe_failed_due_to_tls_error",
					types.LabelInstance:     testTargetNotYetKnown,
					types.LabelInstanceUUID: testAgentID,
					types.LabelScraper:      testAgentFQDN,
					types.LabelServiceUUID:  testMonitorID,
				},
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
					Point: types.Point{Time: t0, Value: float64(time.Time{}.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
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
					Point: types.Point{Time: t0, Value: float64(certs.LongLiveDuration.Seconds())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
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
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedWarning.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_earliest_cert_expiry",
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
						types.LabelName:         "probe_ssl_validation_success",
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
			target: &httpTestTarget{
				TLSCert:     certs.CertLongLivedSelfSignedWarning,
				RootCACerts: []*x509.Certificate{certs.RootCA},
			},
		},
		{
			name: "tls-self-signed-expiring",
			absentPoints: []map[string]string{
				{
					types.LabelName:         "probe_failed_due_to_tls_error",
					types.LabelInstance:     testTargetNotYetKnown,
					types.LabelInstanceUUID: testAgentID,
					types.LabelScraper:      testAgentFQDN,
					types.LabelServiceUUID:  testMonitorID,
				},
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
					Point: types.Point{Time: t0, Value: float64(time.Time{}.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
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
					Point: types.Point{Time: t0, Value: float64(certs.LongLiveDuration.Seconds())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
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
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedExpired.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_earliest_cert_expiry",
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
						types.LabelName:         "probe_ssl_validation_success",
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
			target: &httpTestTarget{
				TLSCert:     certs.CertLongLivedSelfSignedExpired,
				RootCACerts: []*x509.Certificate{certs.RootCA},
			},
		},
		{
			name: "tls-expired",
			absentPoints: []map[string]string{
				{
					types.LabelName:         "probe_failed_due_to_tls_error",
					types.LabelInstance:     testTargetNotYetKnown,
					types.LabelInstanceUUID: testAgentID,
					types.LabelScraper:      testAgentFQDN,
					types.LabelServiceUUID:  testMonitorID,
				},
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
					Point: types.Point{Time: t0, Value: float64(time.Time{}.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
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
					Point: types.Point{Time: t0, Value: float64(certs.LongLiveDuration.Seconds())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
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
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedExpired.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_earliest_cert_expiry",
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
						types.LabelName:         "probe_ssl_validation_success",
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
			target: &httpTestTarget{
				TLSCert:     certs.CertLongLivedExpired,
				RootCACerts: []*x509.Certificate{certs.RootCA},
			},
		},
		{
			name: "timeout-tls-handshake",
			absentPoints: []map[string]string{
				{
					types.LabelName:         "probe_ssl_earliest_cert_expiry",
					types.LabelInstance:     testTargetNotYetKnown,
					types.LabelInstanceUUID: testAgentID,
					types.LabelScraper:      testAgentFQDN,
					types.LabelServiceUUID:  testMonitorID,
				},
				{
					types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
					types.LabelInstance:     testTargetNotYetKnown,
					types.LabelInstanceUUID: testAgentID,
					types.LabelScraper:      testAgentFQDN,
					types.LabelServiceUUID:  testMonitorID,
				},
				{
					types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
					types.LabelInstance:     testTargetNotYetKnown,
					types.LabelInstanceUUID: testAgentID,
					types.LabelScraper:      testAgentFQDN,
					types.LabelServiceUUID:  testMonitorID,
				},
				{
					types.LabelName:         "probe_duration_seconds",
					types.LabelInstance:     testTargetNotYetKnown,
					types.LabelInstanceUUID: testAgentID,
					types.LabelScraper:      testAgentFQDN,
					types.LabelServiceUUID:  testMonitorID,
				},
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
						types.LabelName:         "probe_failed_due_to_tls_error",
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
						types.LabelName:         "probe_ssl_validation_success",
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
			target: &httpTestTarget{
				TLSCert:               certs.CertLongLivedOk,
				RootCACerts:           []*x509.Certificate{certs.RootCA},
				TimeoutInTLSHandshake: true,
			},
		},
		{
			name: "timeout-in-tcp-accept",
			absentPoints: []map[string]string{
				{
					types.LabelName:         "probe_ssl_earliest_cert_expiry",
					types.LabelInstance:     testTargetNotYetKnown,
					types.LabelInstanceUUID: testAgentID,
					types.LabelScraper:      testAgentFQDN,
					types.LabelServiceUUID:  testMonitorID,
				},
				{
					types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
					types.LabelInstance:     testTargetNotYetKnown,
					types.LabelInstanceUUID: testAgentID,
					types.LabelScraper:      testAgentFQDN,
					types.LabelServiceUUID:  testMonitorID,
				},
				{
					types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
					types.LabelInstance:     testTargetNotYetKnown,
					types.LabelInstanceUUID: testAgentID,
					types.LabelScraper:      testAgentFQDN,
					types.LabelServiceUUID:  testMonitorID,
				},
				{
					types.LabelName:         "probe_duration_seconds",
					types.LabelInstance:     testTargetNotYetKnown,
					types.LabelInstanceUUID: testAgentID,
					types.LabelScraper:      testAgentFQDN,
					types.LabelServiceUUID:  testMonitorID,
				},
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
						types.LabelName:         "probe_failed_due_to_tls_error",
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
						types.LabelName:         "probe_ssl_validation_success",
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
			target: &httpTestTarget{
				TLSCert:            certs.CertLongLivedOk,
				RootCACerts:        []*x509.Certificate{certs.RootCA},
				TimeoutInTCPAccept: true,
			},
		},
		{
			name: "connection-refused",
			absentPoints: []map[string]string{
				{
					types.LabelName:         "probe_ssl_earliest_cert_expiry",
					types.LabelInstance:     testTargetNotYetKnown,
					types.LabelInstanceUUID: testAgentID,
					types.LabelScraper:      testAgentFQDN,
					types.LabelServiceUUID:  testMonitorID,
				},
				{
					types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
					types.LabelInstance:     testTargetNotYetKnown,
					types.LabelInstanceUUID: testAgentID,
					types.LabelScraper:      testAgentFQDN,
					types.LabelServiceUUID:  testMonitorID,
				},
				{
					types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
					types.LabelInstance:     testTargetNotYetKnown,
					types.LabelInstanceUUID: testAgentID,
					types.LabelScraper:      testAgentFQDN,
					types.LabelServiceUUID:  testMonitorID,
				},
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
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_failed_due_to_tls_error",
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
						types.LabelName:         "probe_ssl_validation_success",
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
			target: &httpTestTarget{
				TLSCert:       certs.CertLongLivedOk,
				RootCACerts:   []*x509.Certificate{certs.RootCA},
				ServerStopped: true,
			},
		},
		{
			name: "broken-crypto",
			absentPoints: []map[string]string{
				{
					types.LabelName:         "probe_ssl_earliest_cert_expiry",
					types.LabelInstance:     testTargetNotYetKnown,
					types.LabelInstanceUUID: testAgentID,
					types.LabelScraper:      testAgentFQDN,
					types.LabelServiceUUID:  testMonitorID,
				},
				{
					types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
					types.LabelInstance:     testTargetNotYetKnown,
					types.LabelInstanceUUID: testAgentID,
					types.LabelScraper:      testAgentFQDN,
					types.LabelServiceUUID:  testMonitorID,
				},
				{
					types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
					types.LabelInstance:     testTargetNotYetKnown,
					types.LabelInstanceUUID: testAgentID,
					types.LabelScraper:      testAgentFQDN,
					types.LabelServiceUUID:  testMonitorID,
				},
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
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_validation_success",
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
						types.LabelName:         "probe_failed_due_to_tls_error",
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
			target: &httpTestTarget{
				TLSCert:         certs.CertLongLivedOk,
				RootCACerts:     []*x509.Certificate{certs.RootCA},
				UseBrokenCrypto: true,
			},
		},
		{
			name: "break-connection-in-tls",
			absentPoints: []map[string]string{
				{
					types.LabelName:         "probe_ssl_earliest_cert_expiry",
					types.LabelInstance:     testTargetNotYetKnown,
					types.LabelInstanceUUID: testAgentID,
					types.LabelScraper:      testAgentFQDN,
					types.LabelServiceUUID:  testMonitorID,
				},
				{
					types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
					types.LabelInstance:     testTargetNotYetKnown,
					types.LabelInstanceUUID: testAgentID,
					types.LabelScraper:      testAgentFQDN,
					types.LabelServiceUUID:  testMonitorID,
				},
				{
					types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
					types.LabelInstance:     testTargetNotYetKnown,
					types.LabelInstanceUUID: testAgentID,
					types.LabelScraper:      testAgentFQDN,
					types.LabelServiceUUID:  testMonitorID,
				},
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
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_validation_success",
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
						types.LabelName:         "probe_failed_due_to_tls_error",
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
			target: &httpTestTarget{
				TLSCert:             certs.CertLongLivedOk,
				RootCACerts:         []*x509.Certificate{certs.RootCA},
				CloseInTLSHandshake: true,
			},
		},
		{
			name: "ssl-bad-dns",
			absentPoints: []map[string]string{
				{
					types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
					types.LabelInstance:     testTargetNotYetKnown,
					types.LabelInstanceUUID: testAgentID,
					types.LabelScraper:      testAgentFQDN,
					types.LabelServiceUUID:  testMonitorID,
				},
				{
					types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
					types.LabelInstance:     testTargetNotYetKnown,
					types.LabelInstanceUUID: testAgentID,
					types.LabelScraper:      testAgentFQDN,
					types.LabelServiceUUID:  testMonitorID,
				},
				{
					types.LabelName:         "probe_ssl_earliest_cert_expiry",
					types.LabelInstance:     testTargetNotYetKnown,
					types.LabelInstanceUUID: testAgentID,
					types.LabelScraper:      testAgentFQDN,
					types.LabelServiceUUID:  testMonitorID,
				},
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
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_failed_due_to_tls_error",
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
						types.LabelName:         "probe_ssl_validation_success",
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
			target: HTTPTargetWithNoDNS{useSSL: true},
		},
		{
			name: "tcp-success",
			absentPoints: []map[string]string{
				{
					types.LabelName:         "probe_failed_due_to_tls_error",
					types.LabelInstance:     testTargetNotYetKnown,
					types.LabelInstanceUUID: testAgentID,
					types.LabelScraper:      testAgentFQDN,
					types.LabelServiceUUID:  testMonitorID,
				},
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
			probeDurationMaxValue: 5,
			target:                &httpTestTarget{},
		},
		/*{
		I'm not sure we can easily simulare a TCP connect() timeout.
		I've trying just bind() and/or listen() a socket (e.g. never call accept()) but it's don't
		work on all OS (on Linux it don't timeout, the connection succeed).
		name: "tcp-timeout",
		[...]
		*/
		{
			name: "tcp-connection-refused",
			absentPoints: []map[string]string{
				{
					types.LabelName:         "probe_failed_due_to_tls_error",
					types.LabelInstance:     testTargetNotYetKnown,
					types.LabelInstanceUUID: testAgentID,
					types.LabelScraper:      testAgentFQDN,
					types.LabelServiceUUID:  testMonitorID,
				},
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
			target: &httpTestTarget{
				ServerStopped: true,
			},
		},
		{
			name: "tcp-bad-dns",
			absentPoints: []map[string]string{
				{
					types.LabelName:         "probe_failed_due_to_tls_error",
					types.LabelInstance:     testTargetNotYetKnown,
					types.LabelInstanceUUID: testAgentID,
					types.LabelScraper:      testAgentFQDN,
					types.LabelServiceUUID:  testMonitorID,
				},
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
			},
			target: &HTTPTargetWithNoDNS{useSSL: false},
		},
	}

	t.Parallel()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runTestCase(t, tt, true, testMonitorID, testAgentID, t0)
		})
	}
}
