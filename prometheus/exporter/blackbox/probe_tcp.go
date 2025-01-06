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

package blackbox

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
	"unsafe"

	"github.com/bleemeo/glouton/logger"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/prometheus/client_golang/prometheus"
	pconfig "github.com/prometheus/common/config"

	"github.com/prometheus/blackbox_exporter/config"
)

// Prevent gofmt from removing "unsafe", //go:linkname is only allowed in Go files that import "unsafe".
var _ unsafe.Pointer

//go:linkname dialTCP github.com/prometheus/blackbox_exporter/prober.dialTCP
func dialTCP(ctx context.Context, target string, module config.Module, registry *prometheus.Registry, logger log.Logger) (net.Conn, error)

//go:linkname getEarliestCertExpiry github.com/prometheus/blackbox_exporter/prober.getEarliestCertExpiry
func getEarliestCertExpiry(state *tls.ConnectionState) time.Time

//go:linkname getFingerprint github.com/prometheus/blackbox_exporter/prober.getFingerprint
func getFingerprint(state *tls.ConnectionState) string

//go:linkname getTLSVersion github.com/prometheus/blackbox_exporter/prober.getTLSVersion
func getTLSVersion(state *tls.ConnectionState) string

// ProbeTCP is a modified version of the blackbox v0.20.0 ProbeTCP.
// https://github.com/prometheus/blackbox_exporter/blob/v0.20.0/prober/tcp.go
//
// We had to modify this function to fill the verified chains of the TLS connection state
// by ourselves because it's empty when we set insecure_skip_verify to true.
//
// We also added a way to distinguish TLS errors from TCP errors with the metric "probe_failed_due_to_tls_error".
func ProbeTCP(ctx context.Context, target string, module config.Module, registry *prometheus.Registry, logger log.Logger) bool {
	probeSSLEarliestCertExpiry := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "probe_ssl_earliest_cert_expiry",
		Help: "Returns earliest SSL cert expiry date",
	})
	probeSSLLastChainExpiryTimestampSeconds := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "probe_ssl_last_chain_expiry_timestamp_seconds",
		Help: "Returns last SSL chain expiry in unixtime",
	})
	probeSSLLastInformation := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "probe_ssl_last_chain_info",
			Help: "Contains SSL leaf certificate information",
		},
		[]string{"fingerprint_sha256"},
	)
	probeTLSVersion := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "probe_tls_version_info",
			Help: "Returns the TLS version used, or NaN when unknown",
		},
		[]string{"version"},
	)
	probeFailedDueToRegex := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "probe_failed_due_to_regex",
		Help: "Indicates if probe failed due to regex",
	})
	probeFailedDueToTLSError := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "probe_failed_due_to_tls_error",
		Help: "Indicates if probe failed due to a TLS error",
	})

	registry.MustRegister(probeFailedDueToRegex)

	deadline, _ := ctx.Deadline()

	conn, err := dialTCP(ctx, target, module, registry, logger)
	if err != nil {
		if module.TCP.TLS {
			registry.MustRegister(probeFailedDueToTLSError)

			var netErr net.Error

			if errors.As(err, &netErr) && netErr.Timeout() ||
				strings.Contains(err.Error(), "tls:") || err.Error() == "EOF" {
				probeFailedDueToTLSError.Set(1)
			} else {
				probeFailedDueToTLSError.Set(0)
			}
		}

		_ = level.Error(logger).Log("msg", "Error dialing TCP", "err", err)

		return false
	}
	defer conn.Close()

	_ = level.Info(logger).Log("msg", "Successfully dialed")

	// Set a deadline to prevent the following code from blocking forever.
	// If a deadline cannot be set, better fail the probe by returning an error
	// now rather than blocking forever.
	if err := conn.SetDeadline(deadline); err != nil {
		_ = level.Error(logger).Log("msg", "Error setting deadline", "err", err)

		return false
	}

	if module.TCP.TLS {
		tlsConn, _ := conn.(*tls.Conn)
		state := tlsConn.ConnectionState()

		registry.MustRegister(probeSSLEarliestCertExpiry, probeTLSVersion, probeSSLLastChainExpiryTimestampSeconds, probeSSLLastInformation)
		probeSSLEarliestCertExpiry.Set(float64(getEarliestCertExpiry(&state).Unix()))
		probeTLSVersion.WithLabelValues(getTLSVersion(&state)).Set(1)

		verifiedChains := getVerifiedChains(ctx, state, module.TCP.TLSConfig)
		probeSSLLastChainExpiryTimestampSeconds.Set(float64(getLastChainExpiry(verifiedChains).Unix()))
		probeSSLLastInformation.WithLabelValues(getFingerprint(&state)).Set(1)
	}

	scanner := bufio.NewScanner(conn)

	for i, qr := range module.TCP.QueryResponse {
		_ = level.Info(logger).Log("msg", "Processing query response entry", "entry_number", i)

		send := qr.Send

		if qr.Expect.Regexp != nil {
			var match []int
			// Read lines until one of them matches the configured regexp.
			for scanner.Scan() {
				_ = level.Debug(logger).Log("msg", "Read line", "line", scanner.Text())

				match = qr.Expect.Regexp.FindSubmatchIndex(scanner.Bytes())
				if match != nil {
					_ = level.Info(logger).Log("msg", "Regexp matched", "regexp", qr.Expect.Regexp, "line", scanner.Text())

					break
				}
			}

			if scanner.Err() != nil {
				_ = level.Error(logger).Log("msg", "Error reading from connection", "err", scanner.Err().Error())

				return false
			}

			if match == nil {
				probeFailedDueToRegex.Set(1)

				_ = level.Error(logger).Log("msg", "Regexp did not match", "regexp", qr.Expect.Regexp, "line", scanner.Text())

				return false
			}

			probeFailedDueToRegex.Set(0)

			send = string(qr.Expect.Regexp.Expand(nil, []byte(send), scanner.Bytes(), match))
		}

		if send != "" {
			_ = level.Debug(logger).Log("msg", "Sending line", "line", send)

			if _, err := fmt.Fprintf(conn, "%s\n", send); err != nil {
				_ = level.Error(logger).Log("msg", "Failed to send", "err", err)

				return false
			}
		}

		if qr.StartTLS {
			// Upgrade TCP connection to TLS.
			tlsConfig, err := pconfig.NewTLSConfig(&module.TCP.TLSConfig)
			if err != nil {
				_ = level.Error(logger).Log("msg", "Failed to create TLS configuration", "err", err)

				return false
			}

			if tlsConfig.ServerName == "" {
				// Use target-hostname as default for TLS-servername.
				targetAddress, _, _ := net.SplitHostPort(target) // Had succeeded in dialTCP already.
				tlsConfig.ServerName = targetAddress
			}

			tlsConn := tls.Client(conn, tlsConfig)
			defer tlsConn.Close()

			// Initiate TLS handshake (required here to get TLS state).
			if err := tlsConn.Handshake(); err != nil {
				_ = level.Error(logger).Log("msg", "TLS Handshake (client) failed", "err", err)

				return false
			}

			_ = level.Info(logger).Log("msg", "TLS Handshake (client) succeeded.")

			conn = net.Conn(tlsConn)
			scanner = bufio.NewScanner(conn)

			// Get certificate expiry.
			state := tlsConn.ConnectionState()

			registry.MustRegister(probeSSLEarliestCertExpiry, probeSSLLastChainExpiryTimestampSeconds)
			probeSSLEarliestCertExpiry.Set(float64(getEarliestCertExpiry(&state).Unix()))
			probeTLSVersion.WithLabelValues(getTLSVersion(&state)).Set(1)

			verifiedChains := getVerifiedChains(ctx, state, module.TCP.TLSConfig)
			probeSSLLastChainExpiryTimestampSeconds.Set(float64(getLastChainExpiry(verifiedChains).Unix()))
			probeSSLLastInformation.WithLabelValues(getFingerprint(&state)).Set(1)
		}
	}

	return true
}

func getVerifiedChains(ctx context.Context, state tls.ConnectionState, tlsConfig pconfig.TLSConfig) [][]*x509.Certificate {
	cfg, err := pconfig.NewTLSConfig(&tlsConfig)
	if err != nil {
		logger.V(2).Printf("config.NewTLSConfig failed: %v", err)

		return nil
	}

	testInjectCARoot, _ := ctx.Value(contextKeyTestInjectCARoot).(*x509.Certificate)
	if testInjectCARoot != nil {
		if cfg.RootCAs == nil {
			cfg.RootCAs = x509.NewCertPool()
		}

		cfg.RootCAs.AddCert(testInjectCARoot)
	}

	now, _ := ctx.Value(contextKeyNowFunc).(func() time.Time)
	opts := x509.VerifyOptions{
		Roots:         cfg.RootCAs,
		CurrentTime:   now(),
		DNSName:       state.ServerName,
		Intermediates: x509.NewCertPool(),
	}

	for _, cert := range state.PeerCertificates[1:] {
		opts.Intermediates.AddCert(cert)
	}

	verifiedChains, err := state.PeerCertificates[0].Verify(opts)
	if err != nil {
		logger.V(2).Printf("Failed to verify chains: %v", err)
	}

	return verifiedChains
}
