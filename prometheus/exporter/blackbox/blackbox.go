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
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http/httptrace"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/prometheus/registry"
	"github.com/bleemeo/glouton/types"

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

//nolint:gochecknoglobals
var (
	probeSuccessDesc = prometheus.NewDesc(
		prometheus.BuildFQName("", "", "probe_success"),
		"Displays whether or not the probe was a success",
		[]string{"instance"},
		nil,
	)
	probeDNSRcode = prometheus.NewDesc(
		prometheus.BuildFQName("", "", "probe_dns_rcode"),
		"The DNS answer rcode value",
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

var (
	errNoCertificates     = errors.New("no server certificate")
	errDNSResolvedUnknown = errors.New("unknown default DNS resolver")
	errConfigWarning      = errors.New("some DNS monitor(s) need default DNS resolver to be configured. Set `blackbox.default_dns_resolver`")
)

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
func (target blackboxCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- probeSuccessDesc

	ch <- probeDurationDesc
}

// CollectWithContext implements the prometheus.Collector interface.
// It is where we do the actual "probing".
func (target blackboxCollector) CollectWithContext(ctx context.Context, ch chan<- prometheus.Metric) { //nolint:maintidx
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

	var extLogger *slog.Logger

	if target.Module.Prober == proberNameDNS {
		// DNS is a bit odd in that target.URL is actually the DNS server,
		// we prefer to show the DNS name to resolve.
		extLogger = logger.NewSlog().With("url", target.OriginalURL)
	} else {
		extLogger = logger.NewSlog().With("url", target.URL)
	}

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

	targetURL := target.URL
	if targetURL == defaultResolverSentinel {
		var err error

		targetURL, err = target.CommonHelpers.DNSResolver()
		if err != nil {
			target.CommonHelpers.NotifyNeedForDefaultResolver(err)
			// notify the "client" that scraping this target resulted in an error
			ch <- prometheus.MustNewConstMetric(probeSuccessDesc, prometheus.GaugeValue, 0., target.Name)

			return
		}
	}

	hackLogger := newDNSHackLogger(extLogger)

	// do all the actual work
	success := probeFn(subCtx, targetURL, target.Module, registry, hackLogger.Logger())

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
	} else if roundTripsTLS := verifyTLS(ctx, target, extLogger, roundTrips); roundTripsTLS.HadTLS() {
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

	if target.Module.Prober == proberNameDNS {
		rcode, err := hackLogger.GetRCode()
		if err == nil {
			ch <- prometheus.MustNewConstMetric(probeDNSRcode, prometheus.GaugeValue, float64(rcode), target.Name)
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
func verifyTLS(ctx context.Context, collector blackboxCollector, extLogger *slog.Logger, roundTrips []roundTrip) roundTripTLSVerifyList {
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

	if collector.Module.Prober != proberNameHTTP {
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

		httpClientConfig := collector.Module.HTTP.HTTPClientConfig

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
			tmp, err := url.Parse(collector.URL)
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

		opts := x509.VerifyOptions{
			Roots:         cfg.RootCAs,
			CurrentTime:   blackboxNow(ctx),
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

func collectorInMap(value blackboxCollector, iterable map[types.Registration]gathererRegistration) bool {
	for _, mapValue := range iterable {
		if value.Equal(mapValue.collector) {
			return true
		}
	}

	return false
}

func gathererInArray(value gathererRegistration, iterable []blackboxCollector) bool {
	return slices.ContainsFunc(iterable, value.collector.Equal)
}

// updateRegistrations registers and deregisters collectors to sync the internal state with the configuration.
func (m *RegisterManager) updateRegistrations() error {
	// register new probes
	for _, collector := range m.targets {
		if !collectorInMap(collector, m.registrations) {
			gatherer, err := newGatherer(collector)
			if err != nil {
				return err
			}

			// wrap our gatherer in ProbeGatherer, to only collect metrics when necessary
			g := registry.NewProbeGatherer(gatherer, collector.RefreshRate > time.Minute)

			hash := labels.FromMap(collector.Labels).Hash()

			refreshRate := collector.RefreshRate
			creationDate := collector.CreationDate

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
					Description: "blackbox for " + collector.URL,
					JitterSeed:  hash,
					MinInterval: collector.RefreshRate,
					ExtraLabels: collector.Labels,
				},
				g,
			)
			if err != nil {
				return err
			}

			g.SetScheduleUpdate(id.ScheduleRun)

			if refreshRate > 0 && time.Since(creationDate) < refreshRate {
				// For new monitor, trigger a schedule immediately
				id.ScheduleRun(types.ScheduleOption{WantedTime: time.Now()})
			}

			m.registrations[id] = gathererRegistration{
				collector: collector,
				gatherer:  g,
			}

			logger.V(2).Printf("New probe registered for '%s'", collector.Name)
		}
	}

	// unregister any obsolete probe
	for reg, gatherer := range m.registrations {
		if gatherer.collector.BleemeoAgentID != "" && !gathererInArray(gatherer, m.targets) {
			logger.V(2).Printf("The probe for '%s' is now deactivated", gatherer.collector.Name)

			reg.Unregister()
			delete(m.registrations, reg)
		}
	}

	return nil
}

func getSystemResolver() (string, error) {
	// Try guessing the system resolver. It use resolv.conf to get the first nameserver.
	// If you need better guess, use setting `blackbox.default_dns_resolver`.``
	f, err := os.Open("/etc/resolv.conf")
	if err != nil {
		return "", err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "nameserver") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				return fields[1], nil
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return "", err
	}

	return "", errDNSResolvedUnknown
}

func (m *RegisterManager) DNSResolver() (string, error) {
	if m.dnsResolver != "" {
		return m.dnsResolver, nil
	}

	m.l.Lock()
	defer m.l.Unlock()

	if m.systemDNSLastUpdate.IsZero() || time.Since(m.systemDNSLastUpdate) < time.Minute {
		m.systemDNSLastUpdate = time.Now()

		tmp, err := getSystemResolver()
		if err != nil && m.systemDNSResolver == "" {
			return "", err
		}

		if err != nil {
			logger.V(1).Printf("failed to get system DNS resolved, continue with old one (%s): %s", m.systemDNSResolver, err)
		}

		if err == nil {
			m.systemDNSResolver = tmp
		}
	}

	return m.systemDNSResolver, nil
}

func (m *RegisterManager) NotifyNeedForDefaultResolver(err error) {
	m.l.Lock()
	defer m.l.Unlock()

	if m.notifiedNeedForDefaultResolver {
		return
	}

	m.notifiedNeedForDefaultResolver = true

	m.addWarnings(errConfigWarning)
	logger.V(1).Printf("Unable to guess system DNS resolver due to: %s", err)
}

type pushFunction func(ctx context.Context, points []types.MetricPoint)

func (f pushFunction) PushPoints(ctx context.Context, points []types.MetricPoint) {
	f(ctx, points)
}

func convertToPEM(certs []*x509.Certificate) (string, error) {
	var buf bytes.Buffer

	for _, cert := range certs {
		if cert == nil || cert.Raw == nil {
			return "", errNoCertificates
		}

		if err := pem.Encode(&buf, &pem.Block{
			Type:  "CERTIFICATE",
			Bytes: cert.Raw,
		}); err != nil {
			return "", err
		}
	}

	return buf.String(), nil
}

const (
	// Context key to get the time function.
	contextKeyNowValue contextKey = iota
)

func blackboxNow(ctx context.Context) time.Time {
	now, ok := ctx.Value(contextKeyNowValue).(time.Time)
	if !ok {
		return time.Now()
	}

	return now
}

func blackboxOverrideNow(ctx context.Context, now time.Time) context.Context {
	return context.WithValue(ctx, contextKeyNowValue, now)
}

type mockHelpers struct {
	dnsResolver string
}

func (m mockHelpers) DNSResolver() (string, error) {
	if m.dnsResolver == errorResolverSentinel {
		// Allow to test this failure condition
		return "", errDNSResolvedUnknown
	}

	if m.dnsResolver != "" {
		return m.dnsResolver, nil
	}

	return getSystemResolver()
}

func (mockHelpers) NotifyNeedForDefaultResolver(err error) {
	logger.V(1).Printf("Unable to guess system DNS resolver due to: %s", err)
}

// InternalRunProbe allow to run one Probe and return points. It's intended for tests.
// Override options, when using zero-value means do not override.
func InternalRunProbe(ctx context.Context, monitor types.Monitor, overrideNow time.Time, overrideCARoot []*x509.Certificate, overrideSystemDNS string) ([]types.MetricPoint, error) {
	var (
		l         sync.Mutex
		resPoints []types.MetricPoint
	)

	reg, err := registry.New(registry.Option{
		FQDN:        "example.com",
		GloutonPort: "8015",
		PushPoint: pushFunction(func(_ context.Context, points []types.MetricPoint) {
			l.Lock()
			defer l.Unlock()

			resPoints = append(resPoints, points...)
		}),
	})
	if err != nil {
		return nil, err
	}

	collector, err := genCollectorFromDynamicTarget(monitor, "Glouton unittest", mockHelpers{dnsResolver: overrideSystemDNS})
	if err != nil {
		return nil, err
	}

	if !overrideNow.IsZero() {
		ctx = blackboxOverrideNow(ctx, overrideNow)
	}

	if overrideCARoot != nil {
		pem, err := convertToPEM(overrideCARoot)
		if err != nil {
			return nil, err
		}

		collector.Module.TCP.TLSConfig.CA = pem
		collector.Module.HTTP.HTTPClientConfig.TLSConfig.CA = pem
	}

	gatherer, err := newGatherer(collector)
	if err != nil {
		return nil, err
	}

	id, err := reg.RegisterGatherer(
		registry.RegistrationOption{
			Description: "blackbox for " + collector.URL,
			JitterSeed:  0,
			MinInterval: collector.RefreshRate,
			ExtraLabels: collector.Labels,
		},
		gatherer,
	)
	if err != nil {
		return nil, err
	}

	if overrideNow.IsZero() {
		overrideNow = time.Now()
	}

	id.InternalRunScrape(ctx, ctx, overrideNow)

	return resPoints, nil
}
