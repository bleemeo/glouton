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
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"maps"
	"math"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bleemeo/glouton/prometheus/registry"
	"github.com/bleemeo/glouton/types"

	"github.com/google/go-cmp/cmp"
)

type testTarget interface {
	Start()
	Close()
	URL() string
	Certificate() *x509.Certificate
	RequestContext(ctx context.Context) context.Context
}

type testingCerts struct {
	CertExpireFar     tls.Certificate
	CertExpireSoon    tls.Certificate
	CertExpired       tls.Certificate
	CertFarAndExpired tls.Certificate
	NotAfterFar       time.Time
	NotAfterSoon      time.Time
	NotAfterExpired   time.Time
}

type testCase struct {
	name string
	// wantPoints is a subset of result points, that is extra points in result don't result in error.
	// Use absentPoints to check for absence of points.
	// In wantPoints & absentPoints, the value of "instance" label will be replaced by target URL.
	// In wantPoints, value NaN will be remplaced with value from result point. Use NaN when you don't
	// care about value. However NaN will NOT be replaced with the value 0.
	wantPoints   []types.MetricPoint
	absentPoints []map[string]string
	target       testTarget
	trustCert    bool
	// Check that probe duration is between given value.
	// If the min or max value is 0, no check is done.
	probeDurationMinValue float64
	probeDurationMaxValue float64
}

// timeoutTime is a time longer any of our timeouts.
const timeoutTime = 15 * time.Second

// Test_Collect_HTTPS tests HTTPS (and HTTP) probes.
func Test_Collect_HTTPS(t *testing.T) { //nolint:maintidx
	monitorID := "7331d6c1-ede1-4483-a3b3-c99f0965f64b"
	agentID := "1d6a2c82-4579-4f7d-91fe-3d4946aacaf7"
	targetNotYetKnown := "this-label-value-will-be-replaced"
	agentFQDN := "example.com"
	t0 := time.Date(2022, 3, 8, 10, 27, 50, 0, time.UTC)

	certs, err := generateCerts(t0)
	if err != nil {
		t.Fatal(err)
	}

	httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))

	tests := []testCase{
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
					Point: types.Point{Time: t0, Value: float64(certs.NotAfterFar.Unix())},
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
					Point: types.Point{Time: t0, Value: float64(certs.NotAfterFar.Unix())},
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
			trustCert:             true,
			target:                &httpTestTarget{TLSCert: []tls.Certificate{certs.CertExpireFar}},
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
					Point: types.Point{Time: t0, Value: float64(certs.NotAfterFar.Unix())},
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
					Point: types.Point{Time: t0, Value: float64(certs.NotAfterFar.Unix())},
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
			trustCert:             true,
			target: &httpTestTarget{
				TLSCert:    []tls.Certificate{certs.CertExpireFar},
				StatusCode: 404,
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
					Point: types.Point{Time: t0, Value: float64(certs.NotAfterFar.Unix())},
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
			trustCert: true,
			target: &httpTestTarget{
				TLSCert:   []tls.Certificate{certs.CertExpireFar},
				HTTPDelay: timeoutTime,
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
			trustCert: false,
			target: &httpTestTarget{
				TLSCert:               []tls.Certificate{certs.CertExpireFar},
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
					Point: types.Point{Time: t0, Value: float64(certs.NotAfterSoon.Unix())},
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
					Point: types.Point{Time: t0, Value: float64(certs.NotAfterSoon.Unix())},
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
			trustCert: true,
			target:    &httpTestTarget{TLSCert: []tls.Certificate{certs.CertExpireSoon}},
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
					Point: types.Point{Time: t0, Value: float64(certs.NotAfterFar.Unix())},
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
					Point: types.Point{Time: t0, Value: float64(certs.NotAfterExpired.Unix())},
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
			trustCert: true,
			target:    &httpTestTarget{TLSCert: []tls.Certificate{certs.CertFarAndExpired}},
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
					Point: types.Point{Time: t0, Value: float64(certs.NotAfterFar.Unix())},
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
			trustCert: false,
			target:    &httpTestTarget{TLSCert: []tls.Certificate{certs.CertExpireFar}},
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
					Point: types.Point{Time: t0, Value: float64(certs.NotAfterExpired.Unix())},
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
			trustCert: false,
			target:    &httpTestTarget{TLSCert: []tls.Certificate{certs.CertExpired}},
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
					Point: types.Point{Time: t0, Value: float64(certs.NotAfterExpired.Unix())},
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
			trustCert: true,
			target:    &httpTestTarget{TLSCert: []tls.Certificate{certs.CertExpired}},
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
			trustCert:             true,
			target: &httpTestTarget{
				TLSCert:               []tls.Certificate{certs.CertExpireFar},
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
			trustCert:             true,
			target: &httpTestTarget{
				TLSCert:            []tls.Certificate{certs.CertExpireFar},
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
			trustCert: true,
			target: &httpTestTarget{
				TLSCert:       []tls.Certificate{certs.CertExpireFar},
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
			trustCert: true,
			target:    &httpTestTarget{TLSCert: []tls.Certificate{certs.CertExpireFar}, UseBrokenCrypto: true},
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
					Point: types.Point{Time: t0, Value: float64(certs.NotAfterFar.Unix())},
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
			trustCert: true,
			target: &httpTestTarget{
				TLSCert:     []tls.Certificate{certs.CertExpireFar},
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
			trustCert: true,
			target:    &httpTestTarget{TLSCert: []tls.Certificate{certs.CertExpireFar}, CloseInTLSHandshake: true},
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
			target: noDNSTarget{useSSL: false},
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
			target: noDNSTarget{useSSL: true},
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
					Point: types.Point{Time: t0, Value: float64(certs.NotAfterFar.Unix())},
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
					Point: types.Point{Time: t0, Value: float64(certs.NotAfterFar.Unix())},
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
			trustCert:             true,
			target: &httpTestTarget{
				TLSCert:    []tls.Certificate{certs.CertExpireFar},
				HTTPDelay:  10 * time.Second,
				StatusCode: http.StatusBadGateway,
			},
		},
	}

	t.Parallel()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runTest(t, tt, false, monitorID, agentID, agentFQDN, t0)
		})
	}
}

func runTest(t *testing.T, test testCase, usePlainTCPOrSSL bool, monitorID, agentID, agentFQDN string, t0 time.Time) {
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

	var (
		resPoints []types.MetricPoint
		l         sync.Mutex
	)

	reg, err := registry.New(registry.Option{
		FQDN:        agentFQDN,
		GloutonPort: "8015",
		PushPoint: pushFunction(func(_ context.Context, points []types.MetricPoint) {
			l.Lock()
			defer l.Unlock()

			resPoints = append(resPoints, points...)
		}),
	})
	if err != nil {
		t.Fatal(err)
	}

	target, err := genCollectorFromDynamicTarget(monitor, "Glouton unittest")
	if err != nil {
		t.Fatal(err)
	}

	target.Collector.nowFunc = func() time.Time {
		return t0
	}

	if test.trustCert {
		target.Collector.testInjectCARoot = test.target.Certificate()
	}

	gatherer, err := newGatherer(target.Collector)
	if err != nil {
		t.Fatal(err)
	}

	id, err := reg.RegisterGatherer(registry.RegistrationOption{
		ExtraLabels: target.Labels,
	}, gatherer)
	if err != nil {
		t.Fatal(err)
	}

	reg.InternalRunScrape(test.target.RequestContext(ctx), t.Context(), t0, id)

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

type pushFunction func(ctx context.Context, points []types.MetricPoint)

func (f pushFunction) PushPoints(ctx context.Context, points []types.MetricPoint) {
	f(ctx, points)
}

func generateCerts(t0 time.Time) (testingCerts, error) {
	var err error

	notBefore := t0.Add(-25 * time.Hour)
	result := testingCerts{
		NotAfterFar:     t0.Add(365 * 24 * time.Hour),
		NotAfterSoon:    t0.Add(24 * time.Hour),
		NotAfterExpired: t0.Add(-24 * time.Hour),
	}

	result.CertExpireFar, err = buildCert(notBefore, result.NotAfterFar, false, "Acme Co nice")
	if err != nil {
		return result, err
	}

	result.CertExpireSoon, err = buildCert(notBefore, result.NotAfterSoon, false, "Acme Co need renew")
	if err != nil {
		return result, err
	}

	result.CertExpired, err = buildCert(notBefore, result.NotAfterExpired, false, "Acme Co expired")
	if err != nil {
		return result, err
	}

	result.CertFarAndExpired = result.CertExpireFar
	result.CertFarAndExpired.Certificate = append(result.CertFarAndExpired.Certificate, result.CertExpired.Certificate[0])

	return result, nil
}

func buildCert(notBefore time.Time, notAfter time.Time, useInvalidName bool, org string) (tls.Certificate, error) {
	// Inspired by generate_cert.go from crypto/tls
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return tls.Certificate{}, err
	}

	keyUsage := x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment

	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)

	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return tls.Certificate{}, err
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{org},
		},
		NotBefore: notBefore,
		NotAfter:  notAfter,

		KeyUsage:              keyUsage,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
		IsCA:                  true,
	}

	if useInvalidName {
		template.IPAddresses = []net.IP{net.ParseIP("1.2.3.4")}
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return tls.Certificate{}, err
	}

	certBytes := bytes.NewBuffer(nil)
	keyBytes := bytes.NewBuffer(nil)

	if err := pem.Encode(certBytes, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes}); err != nil {
		return tls.Certificate{}, err
	}

	privBytes, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return tls.Certificate{}, err
	}

	if err := pem.Encode(keyBytes, &pem.Block{Type: "PRIVATE KEY", Bytes: privBytes}); err != nil {
		return tls.Certificate{}, err
	}

	return tls.X509KeyPair(certBytes.Bytes(), keyBytes.Bytes())
}

type noDNSTarget struct {
	useSSL bool
}

func (t noDNSTarget) Start() {
}

func (t noDNSTarget) Close() {
}

func (t noDNSTarget) URL() string {
	if t.useSSL {
		return "https://this-does-not-exists.bleemeo.com:81"
	}

	return "http://this-does-not-exists.bleemeo.com:81"
}

func (t noDNSTarget) Certificate() *x509.Certificate {
	return nil
}

func (t noDNSTarget) RequestContext(ctx context.Context) context.Context {
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
	TLSCert               []tls.Certificate
	srv                   *httptest.Server
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
	t.srv = httptest.NewUnstartedServer(http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
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

	t.srv.Listener = wrapListenner{
		Listener: t.srv.Listener,
		Timeout:  t.TimeoutInTCPAccept,
	}

	if len(t.TLSCert) > 0 {
		t.srv.TLS = &tls.Config{ //nolint: gosec
			Certificates: t.TLSCert,
		}

		t.srv.TLS.GetCertificate = func(chi *tls.ClientHelloInfo) (*tls.Certificate, error) {
			if t.TimeoutInTLSHandshake {
				time.Sleep(timeoutTime)
			}

			if t.CloseInTLSHandshake {
				_ = chi.Conn.Close()
			}

			return &t.TLSCert[0], nil
		}

		if t.UseBrokenCrypto {
			t.srv.TLS.MaxVersion = tls.VersionSSL30 //nolint: staticcheck,nolintlint
		}

		t.srv.StartTLS()

		t.srv.TLS.Certificates = nil
	} else {
		t.srv.Start()
	}

	if t.ServerStopped {
		t.srv.Close()
	}
}

func (t *httpTestTarget) URL() string {
	return t.srv.URL
}

func (t *httpTestTarget) Close() {
	t.srv.Close()
}

func (t *httpTestTarget) Certificate() *x509.Certificate {
	return t.srv.Certificate()
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
	t0 := time.Date(2022, 3, 8, 10, 27, 50, 0, time.UTC)

	certs, err := generateCerts(t0)
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
					Point: types.Point{Time: t0, Value: float64(certs.NotAfterFar.Unix())},
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
					Point: types.Point{Time: t0, Value: float64(certs.NotAfterFar.Unix())},
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
			trustCert:             true,
			target:                &httpTestTarget{TLSCert: []tls.Certificate{certs.CertExpireFar}},
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
					Point: types.Point{Time: t0, Value: float64(certs.NotAfterSoon.Unix())},
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
					Point: types.Point{Time: t0, Value: float64(certs.NotAfterSoon.Unix())},
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
			trustCert: true,
			target:    &httpTestTarget{TLSCert: []tls.Certificate{certs.CertExpireSoon}},
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
					Point: types.Point{Time: t0, Value: float64(certs.NotAfterFar.Unix())},
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
					Point: types.Point{Time: t0, Value: float64(certs.NotAfterExpired.Unix())},
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
			trustCert: true,
			target:    &httpTestTarget{TLSCert: []tls.Certificate{certs.CertFarAndExpired}},
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
					Point: types.Point{Time: t0, Value: float64(certs.NotAfterFar.Unix())},
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
			trustCert: false,
			target:    &httpTestTarget{TLSCert: []tls.Certificate{certs.CertExpireFar}},
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
					Point: types.Point{Time: t0, Value: float64(certs.NotAfterExpired.Unix())},
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
			trustCert: false,
			target:    &httpTestTarget{TLSCert: []tls.Certificate{certs.CertExpired}},
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
					Point: types.Point{Time: t0, Value: float64(certs.NotAfterExpired.Unix())},
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
			trustCert: true,
			target:    &httpTestTarget{TLSCert: []tls.Certificate{certs.CertExpired}},
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
			trustCert: true,
			target: &httpTestTarget{
				TLSCert:               []tls.Certificate{certs.CertExpireFar},
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
			trustCert: true,
			target: &httpTestTarget{
				TLSCert:            []tls.Certificate{certs.CertExpireFar},
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
			trustCert: true,
			target: &httpTestTarget{
				TLSCert:       []tls.Certificate{certs.CertExpireFar},
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
			trustCert: true,
			target:    &httpTestTarget{TLSCert: []tls.Certificate{certs.CertExpireFar}, UseBrokenCrypto: true},
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
			trustCert: true,
			target:    &httpTestTarget{TLSCert: []tls.Certificate{certs.CertExpireFar}, CloseInTLSHandshake: true},
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
			trustCert: true,
			target:    noDNSTarget{useSSL: true},
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
			target: &noDNSTarget{useSSL: false},
		},
	}

	t.Parallel()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runTest(t, tt, true, monitorID, agentID, agentFQDN, t0)
		})
	}
}
