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

package blackbox

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"glouton/logger"
	"glouton/prometheus/registry"
	"net"
	"net/http/httptrace"
	"net/url"
	"reflect"
	"time"

	"github.com/go-kit/log"
	"github.com/prometheus/blackbox_exporter/prober"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/config"
	"github.com/prometheus/prometheus/model/labels"
)

const (
	proberNameHTTP string = "http"
	proberNameTCP  string = "tcp"
	proberNameICMP string = "icmp"
	proberNameDNS  string = "dns"
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
		proberNameTCP:  prober.ProbeTCP,
		proberNameICMP: prober.ProbeICMP,
		proberNameDNS:  prober.ProbeDNS,
	}
)

var errNoCertificates = errors.New("no server certificate")

type roundTrip struct {
	HostPort string
	TLSState *tls.ConnectionState
}

type roundTripTLSVerify struct {
	hadTLS     bool
	trustedTLS bool
	expiry     time.Time
	err        error
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

// Collect implements the prometheus.Collector interface.
// It is where we do the actual "probing".
func (target configTarget) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), target.Module.Timeout)
	// Let's ensure we don't end up with stray queries running somewhere
	defer cancel()

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

	extLogger := log.With(logger.GoKitLoggerWrapper(logger.V(2)), "url", target.URL)
	start := time.Now()

	var (
		roundTrips       []roundTrip
		currentRoundTrip roundTrip
	)

	// This is done to capture the last response TLS handshake state.
	subCtx := httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		GetConn: func(hostPort string) {
			if currentRoundTrip.HostPort != "" {
				roundTrips = append(roundTrips, currentRoundTrip)
			}

			currentRoundTrip = roundTrip{
				HostPort: hostPort,
				TLSState: nil,
			}
		},
		TLSHandshakeDone: func(cs tls.ConnectionState, e error) {
			currentRoundTrip.TLSState = &cs
		},
	})

	// do all the actual work
	success := probeFn(subCtx, target.URL, target.Module, registry, extLogger)

	end := time.Now()
	duration := end.Sub(start)
	_ = extLogger.Log("msg", fmt.Sprintf("check started at %s, ended at %s (duration %s); success=%v", start, end, duration, success))

	mfs, err := registry.Gather()
	if err != nil {
		logger.V(1).Printf("blackbox_exporter: error while gathering metrics: %v", err)
		// notify the "client" that scraping this target resulted in an error
		ch <- prometheus.MustNewConstMetric(probeSuccessDesc, prometheus.GaugeValue, 0., target.Name)

		return
	}

	if currentRoundTrip.HostPort != "" {
		roundTrips = append(roundTrips, currentRoundTrip)
	}

	// write all the gathered metrics to our upper registry
	writeMFsToChan(filterMFs(mfs, func(mf *dto.MetricFamily) bool {
		return mf.GetName() != "probe_ssl_last_chain_expiry_timestamp_seconds"
	}), ch)

	roundTripsTLS := target.verifyTLS(extLogger, roundTrips)
	if roundTripsTLS.HadTLS() {
		if roundTripsTLS.AllTrusted() {
			ch <- prometheus.MustNewConstMetric(probeTLSSuccess, prometheus.GaugeValue, 1, target.Name)
		} else {
			ch <- prometheus.MustNewConstMetric(probeTLSSuccess, prometheus.GaugeValue, 0, target.Name)
		}

		if roundTripsTLS[len(roundTripsTLS)-1].hadTLS {
			ch <- prometheus.MustNewConstMetric(probeTLSExpiry, prometheus.GaugeValue, float64(roundTripsTLS[len(roundTripsTLS)-1].expiry.Unix()), target.Name)
		}
	}

	successVal := 0.
	if success {
		successVal = 1
	}
	ch <- prometheus.MustNewConstMetric(probeDurationDesc, prometheus.GaugeValue, duration.Seconds(), target.Name)
	ch <- prometheus.MustNewConstMetric(probeSuccessDesc, prometheus.GaugeValue, successVal, target.Name)
}

// verifyTLS returns the last round-trip TLS expiration and whether all TLS round-trip were trusted.
func (target configTarget) verifyTLS(extLogger log.Logger, roundTrips []roundTrip) roundTripTLSVerifyList {
	start := time.Now()
	defer func() {
		_ = extLogger.Log("verifyTLS took", time.Since(start))
	}()

	if len(roundTrips) == 0 {
		return nil
	}

	firstHost, _, err := net.SplitHostPort(roundTrips[0].HostPort)
	if err != nil {
		_ = extLogger.Log("net.SplitHostPort fail", err)

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
			_ = extLogger.Log("net.SplitHostPort fail", err)

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
				_ = extLogger.Log("url.Parse fail", err)

				result = append(result, roundTripTLSVerify{
					hadTLS: false,
				})

				continue
			}

			httpClientConfig.TLSConfig.ServerName = tmp.Hostname()
		}

		_ = extLogger.Log("Using ServerName", httpClientConfig.TLSConfig.ServerName, "firstHost", firstHost, "currentHostPort", currentHost)

		cfg, err := config.NewTLSConfig(&httpClientConfig.TLSConfig)
		if err != nil {
			_ = extLogger.Log("config.NewTLSConfig fail", err)

			result = append(result, roundTripTLSVerify{
				hadTLS: false,
			})

			continue
		}

		if target.testInjectCARoot != nil {
			if cfg.RootCAs == nil {
				cfg.RootCAs = x509.NewCertPool()
			}

			cfg.RootCAs.AddCert(target.testInjectCARoot)
		}

		opts := x509.VerifyOptions{
			Roots:         cfg.RootCAs,
			CurrentTime:   time.Now(),
			DNSName:       cfg.ServerName,
			Intermediates: x509.NewCertPool(),
		}

		for _, cert := range rt.TLSState.PeerCertificates[1:] {
			opts.Intermediates.AddCert(cert)
		}

		verifiedChains, err := rt.TLSState.PeerCertificates[0].Verify(opts)
		expiry := getLastChainExpiry(verifiedChains)
		_ = extLogger.Log("numberVerifiedChains", len(verifiedChains), "expiry", expiry, "err", err)

		result = append(result, roundTripTLSVerify{
			hadTLS:     true,
			trustedTLS: len(verifiedChains) > 0,
			expiry:     expiry,
			err:        err,
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

// compareConfigTargets returns true if the monitors are identical, and false otherwise.
func compareConfigTargets(a configTarget, b configTarget) bool {
	return a.BleemeoAgentID == b.BleemeoAgentID && a.URL == b.URL && a.RefreshRate == b.RefreshRate && reflect.DeepEqual(a.Module, b.Module)
}

func collectorInMap(value collectorWithLabels, iterable map[int]gathererWithConfigTarget) bool {
	for _, mapValue := range iterable {
		if compareConfigTargets(value.collector, mapValue.target) {
			return true
		}
	}

	return false
}

func gathererInArray(value gathererWithConfigTarget, iterable []collectorWithLabels) bool {
	for _, arrayValue := range iterable {
		// see inMap() above
		if compareConfigTargets(value.target, arrayValue.collector) {
			return true
		}
	}

	return false
}

// updateRegistrations registers and deregisters collectors to sync the internal state with the configuration.
func (m *RegisterManager) updateRegistrations(ctx context.Context) error {
	// register new probes
	for _, collectorFromConfig := range m.targets {
		if !collectorInMap(collectorFromConfig, m.registrations) {
			reg := prometheus.NewRegistry()

			if err := reg.Register(collectorFromConfig.collector); err != nil {
				return err
			}

			var g prometheus.Gatherer = reg

			// wrap our gatherer in ProbeGatherer, to only collect metrics when necessary
			g = registry.NewProbeGatherer(g, collectorFromConfig.collector.RefreshRate > time.Minute)

			hash := labels.FromMap(collectorFromConfig.labels).Hash()

			refreshRate := collectorFromConfig.collector.RefreshRate
			creationDate := collectorFromConfig.collector.CreationDate

			if refreshRate > 0 {
				// We want public probe to run at known time
				hash = uint64(creationDate.UnixNano()) % uint64(refreshRate)
			}

			// this weird "dance" where we create a registry and add it to the registererGatherer
			// for each probe is the product of our unability to expose a "__meta_something"
			// label while doing Collect(). We end up adding the meta labels statically at
			// registration.
			id, err := m.registry.RegisterGatherer(
				ctx,
				registry.RegistrationOption{
					Description: "blackbox for " + collectorFromConfig.collector.URL,
					JitterSeed:  hash,
					Interval:    collectorFromConfig.collector.RefreshRate,
					ExtraLabels: collectorFromConfig.labels,
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
				target:   collectorFromConfig.collector,
				gatherer: g,
			}

			logger.V(2).Printf("New probe registered for '%s'", collectorFromConfig.collector.Name)
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
