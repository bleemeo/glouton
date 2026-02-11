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
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http/httptrace"
	"net/url"
	"os"
	"reflect"
	"sync"
	"time"

	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/prometheus/registry"

	"github.com/prometheus/blackbox_exporter/prober"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/config"
	"github.com/prometheus/prometheus/model/labels"
)

type contextKey int

const (
	proberNameHTTP string = "http"
	proberNameTCP  string = "tcp"
	proberNameSSL  string = "ssl"
	proberNameICMP string = "icmp"
	proberNameDNS  string = "dns"
)

const (
	// Context key to get the CA Root, used only in tests.
	contextKeyTestInjectCARoot contextKey = iota
	// Context key to get the time function.
	contextKeyNowFunc contextKey = iota
)

//nolint:gochecknoglobals
var (
	probeSuccessDesc = prometheus.NewDesc(
		prometheus.BuildFQName("", "", "probe_success"),
		"Displays whether or not the probe was a success",
		[]string{"instance"},
		nil,
	)
	probeTLSExpiry = prometheus.NewDesc(
		prometheus.BuildFQName("", "", "probe_ssl_last_chain_expiry_timestamp_seconds"),
		"Returns last SSL chain expiry in timestamp seconds",
		[]string{"instance"},
		nil,
	)
	probeSSLCertificateLifespan = prometheus.NewDesc(
		prometheus.BuildFQName("", "", "probe_ssl_leaf_certificate_lifespan"),
		"Returns leaf certificate lifespan",
		[]string{"instance"},
		nil,
	)
	probeTLSSuccess = prometheus.NewDesc(
		prometheus.BuildFQName("", "", "probe_ssl_validation_success"),
		"Returns whether all SSL connections were valid",
		[]string{"instance"},
		nil,
	)
	probeDurationDesc = prometheus.NewDesc(
		prometheus.BuildFQName("", "", "probe_duration_seconds"),
		"Returns how long the probe took to complete in seconds",
		[]string{"instance"},
		nil,
	)
	probers = map[string]prober.ProbeFn{
		proberNameHTTP: prober.ProbeHTTP,
		proberNameTCP:  ProbeTCP,
		proberNameICMP: prober.ProbeICMP,
		proberNameDNS:  prober.ProbeDNS,
	}
)

var errNoCertificates = errors.New("no server certificate")

type roundTrip struct {
	HostPort string
	TLSState *tls.ConnectionState
	TLSError error
}

type roundTripTLSVerify struct {
	hadTLS       bool
	trustedTLS   bool
	expiry       time.Time
	leafLifespan time.Duration
	err          error
}

type roundTripTLSVerifyList []roundTripTLSVerify

func (rts roundTripTLSVerifyList) HadTLS() bool {
	for _, rt := range rts {
		if rt.hadTLS {
			return true
		}
	}

	return false
}

func (rts roundTripTLSVerifyList) AllTrusted() bool {
	for _, rt := range rts {
		if !rt.hadTLS {
			continue
		}

		if !rt.trustedTLS {
			return false
		}
	}

	return true
}

// Describe implements the prometheus.Collector interface.
func (target configTarget) Describe(ch chan<- *prometheus.Desc) {
	ch <- probeSuccessDesc

	ch <- probeDurationDesc
}

// CollectWithContext implements the prometheus.Collector interface.
// It is where we do the actual "probing".
func (target configTarget) CollectWithContext(ctx context.Context, ch chan<- prometheus.Metric) {
	probeFn, present := probers[target.Module.Prober]
	if !present {
		logger.V(1).Printf("blackbox_exporter: no prober registered under the name '%s', cannot check '%s'.",
			target.Module.Prober, target.Name)
		// notify the "client" that scraping this target resulted in an error
		ch <- prometheus.MustNewConstMetric(probeSuccessDesc, prometheus.GaugeValue, 0., target.Name)

		return
	}

	// The current state (sad) of affairs in blackbox_exporter in June 2020 is the following:
	// - First, the probber functions defined in prometheus/blackbox_exporter/prober are registering
	//   Collectors internally, instead of having those metrics defined first (and critically, once) and then
	//   collected over the lifetime of the program. This is the result of the design of blackbox_exporter:
	//   the idea is that targets are supplied on the fly via the /probe HTTP endpoint, and a new register is
	//   then created for the specific probe operation, with his lifetime tied to the HTTP request's. One may
	//   wonder why I said "critically" earlier on when speaking about unicity of Collectors' registration,
	//   and the reason is simple: if you declare more than once the same collector on a registry, you get a
	//   nice "panic: duplicate metrics collector registration attempted" at runtime ;). That behavior is
	//   actually forbidden by the prometheus/client_golang library. One could think of a custom registry
	//   that fakes registration when a collector is already declared. Except that's not possible :/
	// - Which brings us to the second point: the interface for prober functions is "func(ctx context.Context,
	//       target string, config config.Module, registry *prometheus.Registry, logger log.Logger) bool".
	//   As we see, the probers expect a 'prometheus.Registry', and that is a struct and not an interface, so
	//   we cannot redefine our own to abstract away all those nitty-gritty details.
	// And those are the reasons that made us choose to build a prometheus registry per query, so in effect
	// we will build nb_targets/scrape_duration registry creations per second, for the whole lifetime of the
	// program. Let's hope this won't have too much of a negative performance impact !

	registry := prometheus.NewRegistry()

	extLogger := logger.NewSlog().With("url", target.URL)

	ctx, cancel := context.WithTimeout(ctx, target.Module.Timeout)
	// Let's ensure we don't end up with stray queries running somewhere
	defer cancel()

	start := time.Now()

	var (
		l                sync.Mutex
		roundTrips       []roundTrip
		currentRoundTrip roundTrip
	)

	// This is done to capture the last response TLS handshake state.
	subCtx := httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		GetConn: func(hostPort string) {
			l.Lock()
			defer l.Unlock()

			if ctx.Err() != nil {
				return
			}

			if currentRoundTrip.HostPort != "" {
				roundTrips = append(roundTrips, currentRoundTrip)
			}

			currentRoundTrip = roundTrip{
				HostPort: hostPort,
				TLSState: nil,
			}
		},
		TLSHandshakeDone: func(cs tls.ConnectionState, e error) {
			l.Lock()
			defer l.Unlock()

			if ctx.Err() != nil {
				return
			}

			currentRoundTrip.TLSState = &cs
			currentRoundTrip.TLSError = e
		},
	})

	// ProbeTCP needs the test CA Root and the current time, to keep using the
	// ProbeFn type we pass these values inside the context.
	subCtx = context.WithValue(subCtx, contextKeyTestInjectCARoot, target.testInjectCARoot)
	subCtx = context.WithValue(subCtx, contextKeyNowFunc, target.nowFunc)

	// do all the actual work
	success := probeFn(subCtx, target.URL, target.Module, registry, extLogger)

	end := time.Now()
	duration := end.Sub(start)

	extLogger.Log(ctx, slog.LevelInfo, fmt.Sprintf("check started at %s, ended at %s (duration %s)", start, end, duration), "success", success)

	mfs, err := registry.Gather()
	if err != nil {
		logger.V(1).Printf("blackbox_exporter: error while gathering metrics: %v", err)
		// notify the "client" that scraping this target resulted in an error
		ch <- prometheus.MustNewConstMetric(probeSuccessDesc, prometheus.GaugeValue, 0., target.Name)

		return
	}

	l.Lock()

	if currentRoundTrip.HostPort != "" {
		roundTrips = append(roundTrips, currentRoundTrip)
	}

	l.Unlock()

	if target.Module.TCP.TLS { // ssl check
		tlsSuccess := false

		for _, mf := range mfs {
			if mf.GetName() == "probe_ssl_last_chain_expiry_timestamp_seconds" {
				if metrics := mf.GetMetric(); len(metrics) > 0 && metrics[0].GetGauge() != nil {
					// When the ssl connection failed, we have no verified chain, so the last chain expiry
					// is a zero time.Time, which is then converted to a unix timestamp and to a float64.
					tlsSuccess = mf.GetMetric()[0].GetGauge().GetValue() != float64(time.Time{}.Unix())
				}

				break
			}
		}

		if tlsSuccess {
			ch <- prometheus.MustNewConstMetric(probeTLSSuccess, prometheus.GaugeValue, 1, target.Name)
		} else {
			ch <- prometheus.MustNewConstMetric(probeTLSSuccess, prometheus.GaugeValue, 0, target.Name)
		}
	} else if roundTripsTLS := target.verifyTLS(ctx, extLogger, roundTrips); roundTripsTLS.HadTLS() {
		// We implement our own probe_ssl_last_chain_expiry_timestamp_seconds for
		// https checks to support self-signed and expired certificates.
		mfs = filterMFs(mfs, func(mf *dto.MetricFamily) bool {
			return mf.GetName() != "probe_ssl_last_chain_expiry_timestamp_seconds"
		})

		if roundTripsTLS.AllTrusted() {
			ch <- prometheus.MustNewConstMetric(probeTLSSuccess, prometheus.GaugeValue, 1, target.Name)
		} else {
			ch <- prometheus.MustNewConstMetric(probeTLSSuccess, prometheus.GaugeValue, 0, target.Name)
		}

		if roundTripsTLS[len(roundTripsTLS)-1].hadTLS {
			ch <- prometheus.MustNewConstMetric(probeTLSExpiry, prometheus.GaugeValue, float64(roundTripsTLS[len(roundTripsTLS)-1].expiry.Unix()), target.Name)

			if lifespan := roundTripsTLS[len(roundTripsTLS)-1].leafLifespan; lifespan != 0 {
				ch <- prometheus.MustNewConstMetric(probeSSLCertificateLifespan, prometheus.GaugeValue, float64(lifespan.Seconds()), target.Name)
			}
		}
	}

	// Write all the gathered metrics to our upper registry.
	writeMFsToChan(mfs, ch)

	successVal := 0.
	if success {
		successVal = 1
	}

	var timeoutInTLS bool

	type timeoutIntf interface {
		Timeout() bool
	}

	for _, roundTrip := range roundTrips {
		timeoutErr, ok := roundTrip.TLSError.(timeoutIntf)
		if errors.Is(roundTrip.TLSError, os.ErrDeadlineExceeded) || (ok && timeoutErr.Timeout()) {
			timeoutInTLS = true
		}
	}

	if ctx.Err() == nil && !timeoutInTLS {
		// only emit probe_duration_seconds if it didn't timeout
		ch <- prometheus.MustNewConstMetric(probeDurationDesc, prometheus.GaugeValue, duration.Seconds(), target.Name)
	}

	ch <- prometheus.MustNewConstMetric(probeSuccessDesc, prometheus.GaugeValue, successVal, target.Name)
}

// verifyTLS returns the last round-trip TLS expiration and whether all TLS round-trip were trusted.
func (target configTarget) verifyTLS(ctx context.Context, extLogger *slog.Logger, roundTrips []roundTrip) roundTripTLSVerifyList {
	start := time.Now()

	defer func() {
		extLogger.InfoContext(ctx, "verifyTLS took "+time.Since(start).String())
	}()

	if len(roundTrips) == 0 {
		return nil
	}

	firstHost, _, err := net.SplitHostPort(roundTrips[0].HostPort)
	if err != nil {
		extLogger.InfoContext(ctx, "net.SplitHostPort failed: "+err.Error())

		return nil
	}

	if target.Module.Prober != proberNameHTTP {
		return nil
	}

	result := make([]roundTripTLSVerify, 0, len(roundTrips))

	for _, rt := range roundTrips {
		if rt.TLSState == nil {
			result = append(result, roundTripTLSVerify{
				hadTLS: false,
			})

			continue
		}

		if len(rt.TLSState.PeerCertificates) == 0 {
			result = append(result, roundTripTLSVerify{
				hadTLS:     true,
				trustedTLS: false,
				err:        errNoCertificates,
			})

			continue
		}

		httpClientConfig := target.Module.HTTP.HTTPClientConfig

		currentHost, _, err := net.SplitHostPort(rt.HostPort)
		if err != nil {
			extLogger.InfoContext(ctx, "net.SplitHostPort failed: "+err.Error())

			result = append(result, roundTripTLSVerify{
				hadTLS: false,
			})

			continue
		}

		if firstHost != currentHost {
			// If there had redirection, use the current hostname
			httpClientConfig.TLSConfig.ServerName = currentHost
		} else if len(httpClientConfig.TLSConfig.ServerName) == 0 {
			// If there is no `server_name` in tls_config, use
			// the hostname of the target.
			tmp, err := url.Parse(target.URL)
			if err != nil {
				extLogger.InfoContext(ctx, "url.Parse failed: "+err.Error())

				result = append(result, roundTripTLSVerify{
					hadTLS: false,
				})

				continue
			}

			httpClientConfig.TLSConfig.ServerName = tmp.Hostname()
		}

		extLogger.InfoContext(ctx, fmt.Sprintf("Using ServerName %q, firstHost %q, currentHostPort %q", httpClientConfig.TLSConfig.ServerName, firstHost, currentHost))

		cfg, err := config.NewTLSConfig(&httpClientConfig.TLSConfig)
		if err != nil {
			extLogger.InfoContext(ctx, "config.NewTLSConfig failed: "+err.Error())

			result = append(result, roundTripTLSVerify{
				hadTLS: false,
			})

			continue
		}

		if len(target.testInjectCARoot) > 0 {
			if cfg.RootCAs == nil {
				cfg.RootCAs = x509.NewCertPool()
			}

			for _, cert := range target.testInjectCARoot {
				cfg.RootCAs.AddCert(cert)
			}
		}

		opts := x509.VerifyOptions{
			Roots:         cfg.RootCAs,
			CurrentTime:   target.nowFunc(),
			DNSName:       cfg.ServerName,
			Intermediates: x509.NewCertPool(),
		}

		for _, cert := range rt.TLSState.PeerCertificates[1:] {
			opts.Intermediates.AddCert(cert)
		}

		verifiedChains, err := rt.TLSState.PeerCertificates[0].Verify(opts)
		expiry := getLastChainExpiry(verifiedChains)
		extLogger.InfoContext(ctx, fmt.Sprintf("numberVerifiedChains %d, expiry: %s, err: %v", len(verifiedChains), expiry, err))

		result = append(result, roundTripTLSVerify{
			hadTLS:       true,
			trustedTLS:   len(verifiedChains) > 0,
			expiry:       expiry,
			leafLifespan: getLeafLifespan(rt.TLSState),
			err:          err,
		})
	}

	return result
}

func getLastChainExpiry(verifiedChains [][]*x509.Certificate) time.Time {
	lastChainExpiry := time.Time{}

	for _, chain := range verifiedChains {
		earliestCertExpiry := time.Time{}

		for _, cert := range chain {
			if (earliestCertExpiry.IsZero() || cert.NotAfter.Before(earliestCertExpiry)) && !cert.NotAfter.IsZero() {
				earliestCertExpiry = cert.NotAfter
			}
		}

		if lastChainExpiry.IsZero() || lastChainExpiry.Before(earliestCertExpiry) {
			lastChainExpiry = earliestCertExpiry
		}
	}

	return lastChainExpiry
}

func getLeafLifespan(state *tls.ConnectionState) time.Duration {
	if state == nil {
		return 0
	}

	if len(state.PeerCertificates) == 0 {
		return 0
	}

	return state.PeerCertificates[0].NotAfter.Sub(state.PeerCertificates[0].NotBefore)
}

// compareConfigTargets returns true if the monitors are identical, and false otherwise.
func compareConfigTargets(a configTarget, b configTarget) bool {
	return a.BleemeoAgentID == b.BleemeoAgentID && a.URL == b.URL && a.RefreshRate == b.RefreshRate && reflect.DeepEqual(a.Module, b.Module)
}

func collectorInMap(value collectorWithLabels, iterable map[int]gathererWithConfigTarget) bool {
	for _, mapValue := range iterable {
		if compareConfigTargets(value.Collector, mapValue.target) {
			return true
		}
	}

	return false
}

func gathererInArray(value gathererWithConfigTarget, iterable []collectorWithLabels) bool {
	for _, arrayValue := range iterable {
		// see inMap() above
		if compareConfigTargets(value.target, arrayValue.Collector) {
			return true
		}
	}

	return false
}

// updateRegistrations registers and deregisters collectors to sync the internal state with the configuration.
func (m *RegisterManager) updateRegistrations() error {
	// register new probes
	for _, collectorFromConfig := range m.targets {
		if !collectorInMap(collectorFromConfig, m.registrations) {
			gatherer, err := newGatherer(collectorFromConfig.Collector)
			if err != nil {
				return err
			}

			var g prometheus.Gatherer = gatherer

			// wrap our gatherer in ProbeGatherer, to only collect metrics when necessary
			g = registry.NewProbeGatherer(g, collectorFromConfig.Collector.RefreshRate > time.Minute)

			hash := labels.FromMap(collectorFromConfig.Labels).Hash()

			refreshRate := collectorFromConfig.Collector.RefreshRate
			creationDate := collectorFromConfig.Collector.CreationDate

			if !creationDate.IsZero() && refreshRate > 0 {
				// Use creationDate for the jitter is present.
				// This creation date already had a jitter applied by synchronizer between multiple probes.
				hash = registry.JitterForTime(creationDate, refreshRate)
			}

			// this weird "dance" where we create a registry and add it to the registererGatherer
			// for each probe is the product of our unability to expose a "__meta_something"
			// label while doing Collect(). We end up adding the meta labels statically at
			// registration.
			id, err := m.registry.RegisterGatherer(
				registry.RegistrationOption{
					Description: "blackbox for " + collectorFromConfig.Collector.URL,
					JitterSeed:  hash,
					MinInterval: collectorFromConfig.Collector.RefreshRate,
					ExtraLabels: collectorFromConfig.Labels,
				},
				g,
			)
			if err != nil {
				return err
			}

			if refreshRate > 0 && time.Since(creationDate) < refreshRate {
				// For new monitor, trigger a schedule immediately
				m.registry.ScheduleScrape(id, time.Now())
			}

			m.registrations[id] = gathererWithConfigTarget{
				target:   collectorFromConfig.Collector,
				gatherer: g,
			}

			logger.V(2).Printf("New probe registered for '%s'", collectorFromConfig.Collector.Name)
		}
	}

	// unregister any obsolete probe
	for idx, gatherer := range m.registrations {
		if gatherer.target.BleemeoAgentID != "" && !gathererInArray(gatherer, m.targets) {
			logger.V(2).Printf("The probe for '%s' is now deactivated", gatherer.target.Name)

			m.registry.Unregister(idx)
			delete(m.registrations, idx)
		}
	}

	return nil
}
