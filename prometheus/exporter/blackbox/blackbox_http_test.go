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
	"maps"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"strings"
	"testing"
	"time"

	"github.com/bleemeo/glouton/types"

	"github.com/google/go-cmp/cmp"
)

// testHTTPCase will also be used for TCP/SSL test.
type testHTTPCase struct {
	name string
	// wantPoints is a subset of result points, that is extra points in result don't result in error.
	// Use absentPoints to check for absence of points.
	// In wantPoints & absentPoints, the value of "instance" label will be replaced by target URL.
	// In wantPoints, value NaN will be remplaced with value from result point. Use NaN when you don't
	// care about value. However NaN will NOT be replaced with the value 0.
	wantPoints   []types.MetricPoint
	absentPoints []map[string]string
	target       testTarget
	// Check that probe duration is between given value.
	// If the min or max value is 0, no check is done.
	probeDurationMinValue float64
	probeDurationMaxValue float64
}

// Test_Collect_HTTPS tests HTTPS (and HTTP) probes.
func Test_Collect_HTTPS(t *testing.T) { //nolint:maintidx
	monitorID := "7331d6c1-ede1-4483-a3b3-c99f0965f64b"
	agentID := "1d6a2c82-4579-4f7d-91fe-3d4946aacaf7"
	targetNotYetKnown := "this-label-value-will-be-replaced"
	agentFQDN := "example.com"
	t0 := time.Now().Truncate(time.Second)

	certs, err := generateCerts(t, t0)
	if err != nil {
		t.Fatal(err)
	}

	tests := []testHTTPCase{
		{
			name: "http-only-success-200",
			absentPoints: []map[string]string{
				{
					types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
					types.LabelServiceUUID:  monitorID,
				},
				{
					types.LabelName:         "probe_ssl_earliest_cert_expiry",
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
					types.LabelServiceUUID:  monitorID,
				},
				{
					types.LabelName:         "probe_ssl_validation_success",
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
					types.LabelServiceUUID:  monitorID,
				},
				{
					types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
					types.LabelServiceUUID:  monitorID,
				},
			},
			wantPoints: []types.MetricPoint{
				{
					Point: types.Point{Time: t0, Value: 1},
					Labels: map[string]string{
						types.LabelName:         "probe_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 200},
					Labels: map[string]string{
						types.LabelName:         "probe_http_status_code",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_dns_lookup_time_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_http_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
						"phase":                 "connect",
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
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
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 200},
					Labels: map[string]string{
						types.LabelName:         "probe_http_status_code",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_http_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
						"phase":                 "connect",
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_dns_lookup_time_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedOk.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.LongLiveDuration.Seconds())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedOk.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_earliest_cert_expiry",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 1},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_validation_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
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
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 200},
					Labels: map[string]string{
						types.LabelName:         "probe_http_status_code",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_http_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
						"phase":                 "connect",
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_dns_lookup_time_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedOk.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.LongLiveDuration.Seconds())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedOk.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_earliest_cert_expiry",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 1},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_validation_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
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
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 404},
					Labels: map[string]string{
						types.LabelName:         "probe_http_status_code",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_http_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
						"phase":                 "connect",
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_dns_lookup_time_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedOk.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.LongLiveDuration.Seconds())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedOk.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_earliest_cert_expiry",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 1},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_validation_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
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
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
					types.LabelServiceUUID:  monitorID,
				},
				{
					types.LabelName:         "probe_duration_seconds",
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
					types.LabelServiceUUID:  monitorID,
				},
			},
			wantPoints: []types.MetricPoint{
				{
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_http_status_code",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_http_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
						"phase":                 "connect",
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_dns_lookup_time_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedOk.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.LongLiveDuration.Seconds())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 1},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_validation_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
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
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
					types.LabelServiceUUID:  monitorID,
				},
				{
					types.LabelName:         "probe_duration_seconds",
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
					types.LabelServiceUUID:  monitorID,
				},
			},
			wantPoints: []types.MetricPoint{
				{
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_http_status_code",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_http_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
						"phase":                 "connect",
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_dns_lookup_time_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(time.Time{}.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.LongLiveDuration.Seconds())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_validation_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
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
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 200},
					Labels: map[string]string{
						types.LabelName:         "probe_http_status_code",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_http_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
						"phase":                 "connect",
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_dns_lookup_time_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedCritical.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.LongLiveDuration.Seconds())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedCritical.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_earliest_cert_expiry",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 1},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_validation_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
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
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 200},
					Labels: map[string]string{
						types.LabelName:         "probe_http_status_code",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_http_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
						"phase":                 "connect",
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_dns_lookup_time_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.TSShortLivedWarning.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.ShortLiveDuration.Seconds())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.TSShortLivedWarning.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_earliest_cert_expiry",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 1},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_validation_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
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
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 200},
					Labels: map[string]string{
						types.LabelName:         "probe_http_status_code",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_http_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
						"phase":                 "connect",
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_dns_lookup_time_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.TSShortLivedCritical.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.ShortLiveDuration.Seconds())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.TSShortLivedCritical.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_earliest_cert_expiry",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 1},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_validation_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
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
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 200},
					Labels: map[string]string{
						types.LabelName:         "probe_http_status_code",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_http_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
						"phase":                 "connect",
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_dns_lookup_time_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedOk.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.LongLiveDuration.Seconds())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedOk.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_earliest_cert_expiry",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 1},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_validation_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
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
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 200},
					Labels: map[string]string{
						types.LabelName:         "probe_http_status_code",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_http_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
						"phase":                 "connect",
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_dns_lookup_time_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.TSCACritical.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.LongLiveDuration.Seconds())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.TSCACritical.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_earliest_cert_expiry",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 1},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_validation_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
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
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 200},
					Labels: map[string]string{
						types.LabelName:         "probe_http_status_code",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_http_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
						"phase":                 "connect",
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_dns_lookup_time_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedOk.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.LongLiveDuration.Seconds())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.TSCAExpired.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_earliest_cert_expiry",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 1},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_validation_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
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
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 200},
					Labels: map[string]string{
						types.LabelName:         "probe_http_status_code",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_http_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
						"phase":                 "connect",
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_dns_lookup_time_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.TSCACritical.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.LongLiveDuration.Seconds())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.TSCACritical.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_earliest_cert_expiry",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 1},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_validation_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
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
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 200},
					Labels: map[string]string{
						types.LabelName:         "probe_http_status_code",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_http_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
						"phase":                 "connect",
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_dns_lookup_time_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(time.Time{}.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.LongLiveDuration.Seconds())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedOk.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_earliest_cert_expiry",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_validation_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
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
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 200},
					Labels: map[string]string{
						types.LabelName:         "probe_http_status_code",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_http_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
						"phase":                 "connect",
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_dns_lookup_time_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(time.Time{}.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.LongLiveDuration.Seconds())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedOk.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_earliest_cert_expiry",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_validation_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
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
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 200},
					Labels: map[string]string{
						types.LabelName:         "probe_http_status_code",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_http_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
						"phase":                 "connect",
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_dns_lookup_time_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(time.Time{}.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.LongLiveDuration.Seconds())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedWarning.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_earliest_cert_expiry",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_validation_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
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
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 200},
					Labels: map[string]string{
						types.LabelName:         "probe_http_status_code",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_http_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
						"phase":                 "connect",
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_dns_lookup_time_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(time.Time{}.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.LongLiveDuration.Seconds())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedExpired.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_earliest_cert_expiry",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_validation_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
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
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 200},
					Labels: map[string]string{
						types.LabelName:         "probe_http_status_code",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_http_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
						"phase":                 "connect",
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_dns_lookup_time_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(time.Time{}.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.LongLiveDuration.Seconds())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedExpired.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_earliest_cert_expiry",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_validation_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
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
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 200},
					Labels: map[string]string{
						types.LabelName:         "probe_http_status_code",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_http_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
						"phase":                 "connect",
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_dns_lookup_time_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedOk.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.LongLiveDuration.Seconds())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedOk.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_earliest_cert_expiry",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_validation_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
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
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
					types.LabelServiceUUID:  monitorID,
				},
				{
					types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
					types.LabelServiceUUID:  monitorID,
				},
				{
					types.LabelName:         "probe_duration_seconds",
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
					types.LabelServiceUUID:  monitorID,
				},
			},
			wantPoints: []types.MetricPoint{
				{
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_validation_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(time.Time{}.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_http_status_code",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_http_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
						"phase":                 "connect",
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_dns_lookup_time_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
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
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
					types.LabelServiceUUID:  monitorID,
				},
				{
					types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
					types.LabelServiceUUID:  monitorID,
				},
				{
					types.LabelName:         "probe_duration_seconds",
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
					types.LabelServiceUUID:  monitorID,
				},
			},
			wantPoints: []types.MetricPoint{
				{
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_validation_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(time.Time{}.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_http_status_code",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_http_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
						"phase":                 "connect",
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_dns_lookup_time_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
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
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
					types.LabelServiceUUID:  monitorID,
				},
				{
					types.LabelName:         "probe_ssl_validation_success",
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
					types.LabelServiceUUID:  monitorID,
				},
				{
					types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
					types.LabelServiceUUID:  monitorID,
				},
				{
					types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
					types.LabelServiceUUID:  monitorID,
				},
			},
			wantPoints: []types.MetricPoint{
				{
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_http_status_code",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_http_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
						"phase":                 "connect",
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_dns_lookup_time_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
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
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
					types.LabelServiceUUID:  monitorID,
				},
				{
					types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
					types.LabelServiceUUID:  monitorID,
				},
			},
			wantPoints: []types.MetricPoint{
				{
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_http_status_code",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_http_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
						"phase":                 "connect",
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_dns_lookup_time_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(time.Time{}.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_validation_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
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
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
					types.LabelServiceUUID:  monitorID,
				},
			},
			wantPoints: []types.MetricPoint{
				{
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_http_status_code",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_http_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
						"phase":                 "connect",
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_dns_lookup_time_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedOk.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.LongLiveDuration.Seconds())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 1},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_validation_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
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
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
					types.LabelServiceUUID:  monitorID,
				},
				{
					types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
					types.LabelServiceUUID:  monitorID,
				},
			},
			wantPoints: []types.MetricPoint{
				{
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_http_status_code",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_http_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
						"phase":                 "connect",
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_dns_lookup_time_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(time.Time{}.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_validation_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
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
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
					types.LabelServiceUUID:  monitorID,
				},
				{
					types.LabelName:         "probe_ssl_earliest_cert_expiry",
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
					types.LabelServiceUUID:  monitorID,
				},
				{
					types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
					types.LabelServiceUUID:  monitorID,
				},
				{
					types.LabelName:         "probe_ssl_validation_success",
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
					types.LabelServiceUUID:  monitorID,
				},
				{
					types.LabelName:         "probe_http_duration_seconds",
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
					types.LabelServiceUUID:  monitorID,
					"phase":                 "connect",
				},
			},
			wantPoints: []types.MetricPoint{
				{
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_dns_lookup_time_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_http_status_code",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
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
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
					types.LabelServiceUUID:  monitorID,
				},
				{
					types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
					types.LabelServiceUUID:  monitorID,
				},
				{
					types.LabelName:         "probe_ssl_earliest_cert_expiry",
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
					types.LabelServiceUUID:  monitorID,
				},
				{
					types.LabelName:         "probe_ssl_validation_success",
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
					types.LabelServiceUUID:  monitorID,
				},
				{
					types.LabelName:         "probe_http_duration_seconds",
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
					types.LabelServiceUUID:  monitorID,
					"phase":                 "connect",
				},
			},
			wantPoints: []types.MetricPoint{
				{
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_dns_lookup_time_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_http_status_code",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
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
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: http.StatusBadGateway},
					Labels: map[string]string{
						types.LabelName:         "probe_http_status_code",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_http_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
						"phase":                 "connect",
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_dns_lookup_time_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedOk.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.LongLiveDuration.Seconds())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedOk.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_earliest_cert_expiry",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 1},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_validation_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
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
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 200},
					Labels: map[string]string{
						types.LabelName:         "probe_http_status_code",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_http_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
						"phase":                 "connect",
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_dns_lookup_time_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedOk.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.LongLiveDuration.Seconds())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedOk.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_earliest_cert_expiry",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 1},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_validation_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
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
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 200},
					Labels: map[string]string{
						types.LabelName:         "probe_http_status_code",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_http_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
						"phase":                 "connect",
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_dns_lookup_time_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedOk.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.LongLiveDuration.Seconds())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedOk.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_earliest_cert_expiry",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 1},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_validation_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
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
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 200},
					Labels: map[string]string{
						types.LabelName:         "probe_http_status_code",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_http_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
						"phase":                 "connect",
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_dns_lookup_time_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.TSShortLivedCritical.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.ShortLiveDuration.Seconds())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.TSShortLivedCritical.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_earliest_cert_expiry",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 1},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_validation_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
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
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 200},
					Labels: map[string]string{
						types.LabelName:         "probe_http_status_code",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_http_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
						"phase":                 "connect",
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_dns_lookup_time_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.TSShortLivedCritical.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.ShortLiveDuration.Seconds())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.TSShortLivedCritical.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_earliest_cert_expiry",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_validation_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
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
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 200},
					Labels: map[string]string{
						types.LabelName:         "probe_http_status_code",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_http_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
						"phase":                 "connect",
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_dns_lookup_time_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(time.Time{}.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.LongLiveDuration.Seconds())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedExpired.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_earliest_cert_expiry",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_validation_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
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
			runTestHTTPorTCP(t, tt, false, monitorID, agentID, t0)
		})
	}
}

// runTestHTTPorTCP is able to run test on http:// and tcp:// (and their SSL equivalent).
func runTestHTTPorTCP(t *testing.T, test testHTTPCase, usePlainTCPOrSSL bool, monitorID string, agentID string, t0 time.Time) {
	t.Helper()
	t.Parallel()

	test.target.Start()
	defer test.target.Close()

	targetURL := test.target.URL()
	if usePlainTCPOrSSL {
		targetURL = strings.Replace(targetURL, "https://", "ssl://", 1)
		targetURL = strings.Replace(targetURL, "http://", "tcp://", 1)
	}

	monitor := types.Monitor{
		ID:             monitorID,
		BleemeoAgentID: agentID,
		URL:            targetURL,
	}

	// To avoid conflicts between tests that use the same test case (HTTPS and SSL), we need
	// to make a copy of the absentPoints map and the wantPoints labels map before modifying them.
	absentPoints := make([]map[string]string, len(test.absentPoints))
	copy(absentPoints, test.absentPoints)

	for _, lbls := range absentPoints {
		if _, ok := lbls[types.LabelInstance]; ok {
			lbls[types.LabelInstance] = targetURL
		}
	}

	wantPoints := make([]types.MetricPoint, 0, len(test.wantPoints))
	for _, point := range test.wantPoints {
		lbls := make(map[string]string, len(point.Labels))
		maps.Copy(lbls, point.Labels)

		lbls[types.LabelInstance] = targetURL

		newPoint := types.MetricPoint{
			Point:       point.Point,
			Labels:      lbls,
			Annotations: point.Annotations,
		}

		wantPoints = append(wantPoints, newPoint)
	}

	ctx := t.Context()

	resPoints, err := InternalRunProbe(test.target.RequestContext(ctx), monitor, t0, test.target.RootCACertificates(), errorResolverSentinel)
	if err != nil {
		t.Fatal(err)
	}

	gotMap := make(map[string]int, len(resPoints))
	for i, got := range resPoints {
		gotMap[types.LabelsToText(got.Labels)] = i
	}

	for _, lbls := range absentPoints {
		if _, ok := gotMap[types.LabelsToText(lbls)]; ok {
			t.Errorf("got labels %v, expected not present", lbls)
		}
	}

	for _, want := range wantPoints {
		idx, ok := gotMap[types.LabelsToText(want.Labels)]
		if !ok {
			t.Errorf("got no labels %v, expected present", want.Labels)

			for _, got := range resPoints {
				if got.Labels[types.LabelName] == want.Labels[types.LabelName] {
					t.Logf("Similar labels in resPoints include: %v", got.Labels)
				}
			}

			continue
		}

		got := resPoints[idx]
		if math.IsNaN(want.Value) && got.Value != 0 {
			want.Value = got.Value
		}

		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("points mismatch: (-want +got)\n%s", diff)
		}

		if want.Labels[types.LabelName] == "probe_duration_seconds" {
			if test.probeDurationMaxValue != 0 {
				if got.Value > test.probeDurationMaxValue {
					t.Errorf("probe_duration_seconds = %v, want <= %v", got.Value, test.probeDurationMaxValue)
				}
			}

			if test.probeDurationMinValue != 0 {
				if got.Value < test.probeDurationMinValue {
					t.Errorf("probe_duration_seconds = %v, want >= %v", got.Value, test.probeDurationMinValue)
				}
			}
		}
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
	monitorID := "7331d6c1-ede1-4483-a3b3-c99f0965f64b"
	agentID := "1d6a2c82-4579-4f7d-91fe-3d4946aacaf7"
	targetNotYetKnown := "this-label-value-will-be-replaced"
	agentFQDN := "example.com"
	t0 := time.Now().Truncate(time.Second)

	certs, err := generateCerts(t, t0)
	if err != nil {
		t.Fatal(err)
	}

	httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))

	tests := []testHTTPCase{
		{
			name: "ssl-success-200",
			absentPoints: []map[string]string{
				{
					types.LabelName:         "probe_failed_due_to_tls_error",
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
					types.LabelServiceUUID:  monitorID,
				},
			},
			wantPoints: []types.MetricPoint{
				{
					Point: types.Point{Time: t0, Value: 1},
					Labels: map[string]string{
						types.LabelName:         "probe_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_dns_lookup_time_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedOk.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.LongLiveDuration.Seconds())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedOk.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_earliest_cert_expiry",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 1},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_validation_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
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
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
					types.LabelServiceUUID:  monitorID,
				},
			},
			wantPoints: []types.MetricPoint{
				{
					Point: types.Point{Time: t0, Value: 1},
					Labels: map[string]string{
						types.LabelName:         "probe_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_dns_lookup_time_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedCritical.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.LongLiveDuration.Seconds())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedCritical.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_earliest_cert_expiry",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 1},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_validation_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
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
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
					types.LabelServiceUUID:  monitorID,
				},
			},
			wantPoints: []types.MetricPoint{
				{
					Point: types.Point{Time: t0, Value: 1},
					Labels: map[string]string{
						types.LabelName:         "probe_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_dns_lookup_time_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedOk.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.LongLiveDuration.Seconds())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.TSCAExpired.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_earliest_cert_expiry",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 1},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_validation_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
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
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
					types.LabelServiceUUID:  monitorID,
				},
			},
			wantPoints: []types.MetricPoint{
				{
					Point: types.Point{Time: t0, Value: 1},
					Labels: map[string]string{
						types.LabelName:         "probe_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_dns_lookup_time_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(time.Time{}.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.LongLiveDuration.Seconds())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedOk.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_earliest_cert_expiry",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_validation_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
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
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
					types.LabelServiceUUID:  monitorID,
				},
			},
			wantPoints: []types.MetricPoint{
				{
					Point: types.Point{Time: t0, Value: 1},
					Labels: map[string]string{
						types.LabelName:         "probe_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_dns_lookup_time_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(time.Time{}.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.LongLiveDuration.Seconds())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedWarning.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_earliest_cert_expiry",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_validation_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
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
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
					types.LabelServiceUUID:  monitorID,
				},
			},
			wantPoints: []types.MetricPoint{
				{
					Point: types.Point{Time: t0, Value: 1},
					Labels: map[string]string{
						types.LabelName:         "probe_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_dns_lookup_time_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(time.Time{}.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.LongLiveDuration.Seconds())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedExpired.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_earliest_cert_expiry",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_validation_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
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
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
					types.LabelServiceUUID:  monitorID,
				},
			},
			wantPoints: []types.MetricPoint{
				{
					Point: types.Point{Time: t0, Value: 1},
					Labels: map[string]string{
						types.LabelName:         "probe_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_dns_lookup_time_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(time.Time{}.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.LongLiveDuration.Seconds())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: float64(certs.TSLongLivedExpired.Unix())},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_earliest_cert_expiry",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_validation_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
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
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
					types.LabelServiceUUID:  monitorID,
				},
				{
					types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
					types.LabelServiceUUID:  monitorID,
				},
				{
					types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
					types.LabelServiceUUID:  monitorID,
				},
				{
					types.LabelName:         "probe_duration_seconds",
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
					types.LabelServiceUUID:  monitorID,
				},
			},
			wantPoints: []types.MetricPoint{
				{
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_dns_lookup_time_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 1},
					Labels: map[string]string{
						types.LabelName:         "probe_failed_due_to_tls_error",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_validation_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
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
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
					types.LabelServiceUUID:  monitorID,
				},
				{
					types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
					types.LabelServiceUUID:  monitorID,
				},
				{
					types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
					types.LabelServiceUUID:  monitorID,
				},
				{
					types.LabelName:         "probe_duration_seconds",
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
					types.LabelServiceUUID:  monitorID,
				},
			},
			wantPoints: []types.MetricPoint{
				{
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_dns_lookup_time_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 1},
					Labels: map[string]string{
						types.LabelName:         "probe_failed_due_to_tls_error",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_validation_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
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
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
					types.LabelServiceUUID:  monitorID,
				},
				{
					types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
					types.LabelServiceUUID:  monitorID,
				},
				{
					types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
					types.LabelServiceUUID:  monitorID,
				},
			},
			wantPoints: []types.MetricPoint{
				{
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_dns_lookup_time_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_failed_due_to_tls_error",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_validation_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
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
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
					types.LabelServiceUUID:  monitorID,
				},
				{
					types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
					types.LabelServiceUUID:  monitorID,
				},
				{
					types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
					types.LabelServiceUUID:  monitorID,
				},
			},
			wantPoints: []types.MetricPoint{
				{
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_dns_lookup_time_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_validation_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 1},
					Labels: map[string]string{
						types.LabelName:         "probe_failed_due_to_tls_error",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
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
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
					types.LabelServiceUUID:  monitorID,
				},
				{
					types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
					types.LabelServiceUUID:  monitorID,
				},
				{
					types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
					types.LabelServiceUUID:  monitorID,
				},
			},
			wantPoints: []types.MetricPoint{
				{
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_dns_lookup_time_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_validation_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 1},
					Labels: map[string]string{
						types.LabelName:         "probe_failed_due_to_tls_error",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
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
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
					types.LabelServiceUUID:  monitorID,
				},
				{
					types.LabelName:         "probe_ssl_leaf_certificate_lifespan",
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
					types.LabelServiceUUID:  monitorID,
				},
				{
					types.LabelName:         "probe_ssl_earliest_cert_expiry",
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
					types.LabelServiceUUID:  monitorID,
				},
			},
			wantPoints: []types.MetricPoint{
				{
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_dns_lookup_time_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_failed_due_to_tls_error",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_ssl_validation_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
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
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
					types.LabelServiceUUID:  monitorID,
				},
			},
			wantPoints: []types.MetricPoint{
				{
					Point: types.Point{Time: t0, Value: 1},
					Labels: map[string]string{
						types.LabelName:         "probe_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
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
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
					types.LabelServiceUUID:  monitorID,
				},
			},
			wantPoints: []types.MetricPoint{
				{
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
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
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
					types.LabelServiceUUID:  monitorID,
				},
			},
			wantPoints: []types.MetricPoint{
				{
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_duration_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
				{
					Point: types.Point{Time: t0, Value: math.NaN()},
					Labels: map[string]string{
						types.LabelName:         "probe_dns_lookup_time_seconds",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
						types.LabelServiceUUID:  monitorID,
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
			},
			target: &HTTPTargetWithNoDNS{useSSL: false},
		},
	}

	t.Parallel()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runTestHTTPorTCP(t, tt, true, monitorID, agentID, t0)
		})
	}
}
