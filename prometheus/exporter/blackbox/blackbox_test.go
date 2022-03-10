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
	"glouton/prometheus/registry"
	"glouton/types"
	"math"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/prometheus/client_golang/prometheus"
)

type testTarget interface {
	Start()
	Close()
	URL() string
	Certificate() *x509.Certificate
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

func Test_Collect(t *testing.T) { //nolint: cyclop
	monitorID := "7331d6c1-ede1-4483-a3b3-c99f0965f64b"
	agentID := "1d6a2c82-4579-4f7d-91fe-3d4946aacaf7"
	targetNotYetKnown := "this-label-value-will-be-remplaced"
	agentFQDN := "example.com"
	t0 := time.Date(2022, 3, 8, 10, 27, 50, 0, time.UTC)

	certs, err := generateCerts(t0)
	if err != nil {
		t.Fatal(err)
	}

	httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {}))

	tests := []struct {
		name string
		// wantPoints is a subset of result points, that is extra points in result don't result in error.
		// Use absentPoints to check for absence of points.
		// In wantPoints & absentPoints, the value of "instance" label will be remplace by target URL.
		// In wantPoints, value NaN will be remplaced with value from result point. Use NaN when you don't
		// care about value. However NaN will NOT be remplaced with the value 0.
		wantPoints             []types.MetricPoint
		absentPoints           []map[string]string
		target                 testTarget
		trustCert              bool
		probeDurationIsTimeout bool
	}{
		{
			name: "http-only-success-200",
			absentPoints: []map[string]string{
				{
					types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
				},
				{
					types.LabelName:         "probe_ssl_earliest_cert_expiry",
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
				},
				{
					types.LabelName:         "probe_ssl_validation_success",
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
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
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
			},
			target: &httpTestTarget{},
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
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
			},
			trustCert: true,
			target:    &httpTestTarget{TLSCert: []tls.Certificate{certs.CertExpireFar}},
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
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
			},
			trustCert: true,
			target: &httpTestTarget{
				TLSCert: []tls.Certificate{certs.CertExpireFar},
				Handler: http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
					rw.WriteHeader(404)
					_, _ = rw.Write([]byte("not found"))
				}),
			},
		},
		{
			name:         "probe-timeout",
			absentPoints: []map[string]string{},
			wantPoints: []types.MetricPoint{
				{
					Point: types.Point{Time: t0, Value: 0},
					Labels: map[string]string{
						types.LabelName:         "probe_success",
						types.LabelInstance:     targetNotYetKnown,
						types.LabelInstanceUUID: agentID,
						types.LabelScraper:      agentFQDN,
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
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
			},
			trustCert: true,
			target: &httpTestTarget{
				TLSCert: []tls.Certificate{certs.CertExpireFar},
				Handler: http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
					time.Sleep(11 * time.Second)
					_, _ = rw.Write([]byte("ok"))
				}),
			},
			probeDurationIsTimeout: true,
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
				},
				{
					types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
				},
				{
					types.LabelName:         "probe_ssl_validation_success",
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
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
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
			},
			trustCert: true,
			target: &httpTestTarget{
				TLSCert:            []tls.Certificate{certs.CertExpireFar},
				TimeoutInHandshake: true,
			},
			probeDurationIsTimeout: true,
		},
		{
			name: "timeout-in-tcp-accept",
			absentPoints: []map[string]string{
				{
					types.LabelName:         "probe_ssl_earliest_cert_expiry",
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
				},
				{
					types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
				},
				{
					types.LabelName:         "probe_ssl_validation_success",
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
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
			probeDurationIsTimeout: true,
		},
		{
			name: "connection-refused",
			absentPoints: []map[string]string{
				{
					types.LabelName:         "probe_ssl_earliest_cert_expiry",
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
				},
				{
					types.LabelName:         "probe_ssl_validation_success",
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
				},
				{
					types.LabelName:         "probe_ssl_last_chain_expiry_timestamp_seconds",
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
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
			probeDurationIsTimeout: false,
		},
		{
			name: "broken-crypto",
			absentPoints: []map[string]string{
				{
					types.LabelName:         "probe_ssl_earliest_cert_expiry",
					types.LabelInstance:     targetNotYetKnown,
					types.LabelInstanceUUID: agentID,
					types.LabelScraper:      agentFQDN,
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
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
			},
			trustCert: true,
			target: &httpTestTarget{
				TLSCert: []tls.Certificate{certs.CertExpireFar},
				Handler: http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
					hj, ok := rw.(http.Hijacker)
					if !ok {
						panic("can't hijack, so can test broken connection")
					}

					conn, _, err := hj.Hijack()
					if err != nil {
						panic("can't hijack, so can test broken connection")
					}

					conn.Close()
				}),
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
					},
					Annotations: types.MetricAnnotations{
						BleemeoAgentID: agentID,
					},
				},
			},
			trustCert: true,
			target:    &httpTestTarget{TLSCert: []tls.Certificate{certs.CertExpireFar}, CloseInTLSHandshake: true},
		},
	}
	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tt.target.Start()
			defer tt.target.Close()

			monitor := types.Monitor{
				ID:             monitorID,
				BleemeoAgentID: agentID,
				URL:            tt.target.URL(),
			}

			for _, lbls := range tt.absentPoints {
				if _, ok := lbls[types.LabelInstance]; ok {
					lbls[types.LabelInstance] = tt.target.URL()
				}
			}

			for _, want := range tt.wantPoints {
				if _, ok := want.Labels[types.LabelInstance]; ok {
					want.Labels[types.LabelInstance] = tt.target.URL()
				}
			}

			ctx := context.Background()

			var (
				resPoints []types.MetricPoint
				l         sync.Mutex
			)

			reg, err := registry.New(registry.Option{
				FQDN:        agentFQDN,
				GloutonPort: "8015",
				PushPoint: pushFunction(func(ctx context.Context, points []types.MetricPoint) {
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

			if tt.trustCert {
				target.Collector.testInjectCARoot = tt.target.Certificate()
			}

			promReg := prometheus.NewRegistry()
			if err := promReg.Register(target.Collector); err != nil {
				t.Fatal(err)
			}

			id, err := reg.RegisterGatherer(ctx, registry.RegistrationOption{
				DisablePeriodicGather: true,
				ExtraLabels:           target.Labels,
			}, promReg)
			if err != nil {
				t.Fatal(err)
			}

			reg.InternalRunScape(ctx, t0, id)

			gotMap := make(map[string]int, len(resPoints))
			for i, got := range resPoints {
				gotMap[types.LabelsToText(got.Labels)] = i
			}

			for _, lbls := range tt.absentPoints {
				if _, ok := gotMap[types.LabelsToText(lbls)]; ok {
					t.Errorf("got labels %v, expected not present", lbls)
				}
			}

			for _, want := range tt.wantPoints {
				idx, ok := gotMap[types.LabelsToText(want.Labels)]
				if !ok {
					t.Errorf("got not labels %v, expected present", want.Labels)

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
					t.Errorf("points missmatch: (-want +got)\n%s", diff)
				}

				if want.Labels[types.LabelName] == "probe_duration_seconds" {
					if tt.probeDurationIsTimeout && got.Value < 9.5 {
						t.Errorf("probe_duration_seconds = %v, want >= 9.5", got.Value)
					} else if !tt.probeDurationIsTimeout && got.Value > 5 {
						t.Errorf("probe_duration_seconds = %v, want <= 5", got.Value)
					}
				}
			}
		})
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

type httpTestTarget struct {
	TimeoutInHandshake  bool
	TimeoutInTCPAccept  bool
	CloseInTLSHandshake bool
	ServerStopped       bool
	UseBrokenCrypto     bool
	TLSCert             []tls.Certificate
	Handler             http.Handler
	srv                 *httptest.Server
}

type wrapListenner struct {
	net.Listener
	Timeout bool
}

func (w wrapListenner) Accept() (net.Conn, error) {
	if w.Timeout {
		time.Sleep(11 * time.Second)
	}

	return w.Listener.Accept()
}

func (t *httpTestTarget) Start() {
	if t.Handler == nil {
		t.Handler = http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			_, _ = rw.Write([]byte("ok"))
		})
	}

	t.srv = httptest.NewUnstartedServer(t.Handler)

	t.srv.Listener = wrapListenner{
		Listener: t.srv.Listener,
		Timeout:  t.TimeoutInTCPAccept,
	}

	if len(t.TLSCert) > 0 {
		t.srv.TLS = &tls.Config{ //nolint: gosec
			Certificates: t.TLSCert,
		}

		t.srv.TLS.GetCertificate = func(chi *tls.ClientHelloInfo) (*tls.Certificate, error) {
			if t.TimeoutInHandshake {
				time.Sleep(11 * time.Second)
			}

			if t.CloseInTLSHandshake {
				chi.Conn.Close()
			}

			return &t.TLSCert[0], nil
		}

		if t.UseBrokenCrypto {
			t.srv.TLS.MaxVersion = tls.VersionSSL30 //nolint: staticcheck
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
