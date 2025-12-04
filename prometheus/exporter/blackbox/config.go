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
	"errors"
	"fmt"
	"maps"
	"net"
	"net/url"
	"os"
	"regexp/syntax"
	"time"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/prometheus/registry"
	"github.com/bleemeo/glouton/types"

	bbConf "github.com/prometheus/blackbox_exporter/config"
	promConfig "github.com/prometheus/common/config"
)

var errUnknownModule = errors.New("unknown blackbox module found in your configuration")

const defaultTimeout = 12 * time.Second

func defaultModule(userAgent string) bbConf.Module {
	return bbConf.Module{
		HTTP: bbConf.HTTPProbe{
			IPProtocol: "ip4",
			HTTPClientConfig: promConfig.HTTPClientConfig{
				FollowRedirects: true,
				TLSConfig: promConfig.TLSConfig{
					// We manually do the TLS verification after probing.
					// This allow to gather information on self-signed server.
					InsecureSkipVerify: true,
				},
			},
			Headers: map[string]string{
				"User-Agent": userAgent,
			},
		},
		DNS: bbConf.DNSProbe{
			IPProtocol:         "ip4",
			IPProtocolFallback: true,
		},
		TCP: bbConf.TCPProbe{
			IPProtocol:         "ip4",
			IPProtocolFallback: true,
		},
		ICMP: bbConf.ICMPProbe{
			IPProtocol:         "ip4",
			IPProtocolFallback: true,
		},
		// Sadly, the API does allow to specify the timeout AFAIK.
		Timeout: defaultTimeout,
	}
}

func genCollectorFromDynamicTarget(monitor types.Monitor, userAgent string) (*collectorWithLabels, error) {
	mod := defaultModule(userAgent)

	url, err := url.Parse(monitor.URL)
	if err != nil {
		logger.V(2).Printf("Invalid URL: '%s'", monitor.URL)

		return nil, err
	}

	uri := monitor.URL

	expectedContentRegex, err := processRegexp(monitor.ExpectedContent)
	if err != nil {
		return nil, err
	}

	forbiddenContentRegex, err := processRegexp(monitor.ForbiddenContent)
	if err != nil {
		return nil, err
	}

	switch url.Scheme {
	case proberNameHTTP, "https":
		// we default to ipv4, due to blackbox limitations with the protocol fallback
		mod.Prober = proberNameHTTP
		if monitor.ExpectedContent != "" {
			mod.HTTP.FailIfBodyNotMatchesRegexp = []bbConf.Regexp{expectedContentRegex}
		}

		if monitor.ForbiddenContent != "" {
			mod.HTTP.FailIfBodyMatchesRegexp = []bbConf.Regexp{forbiddenContentRegex}
		}

		if monitor.ExpectedResponseCode != 0 {
			mod.HTTP.ValidStatusCodes = []int{monitor.ExpectedResponseCode}
		}

		mod.HTTP.HTTPClientConfig.TLSConfig.CAFile = monitor.CAFile

		// Add the custom headers to the default headers.
		if mod.HTTP.Headers == nil {
			mod.HTTP.Headers = make(map[string]string, len(monitor.Headers))
		}

		maps.Copy(mod.HTTP.Headers, monitor.Headers)

		uri, mod = preprocessHTTPTarget(url, mod)
	case proberNameDNS:
		mod.Prober = proberNameDNS
		// TODO: user some better defaults - or even better: use the local resolver
		mod.DNS.QueryName = url.Host
		// TODO: quid of ipv6 ?
		mod.DNS.QueryType = "A"
		uri = "1.1.1.1"
	case proberNameTCP:
		mod.Prober = proberNameTCP
		uri = url.Host
	case proberNameICMP:
		mod.Prober = proberNameICMP
		uri = url.Host
	case proberNameSSL:
		mod.Prober = proberNameTCP
		uri = url.Host
		mod.TCP.TLS = true
		mod.TCP.TLSConfig = promConfig.TLSConfig{
			CAFile: monitor.CAFile,
			// We manually do the TLS verification after probing.
			// This allow to gather information on self-signed server.
			InsecureSkipVerify: true,
		}
	}

	confTarget := configTarget{
		Module:         mod,
		Name:           monitor.URL,
		BleemeoAgentID: monitor.BleemeoAgentID,
		URL:            uri,
		CreationDate:   monitor.CreationDate,
		nowFunc:        time.Now,
	}

	if monitor.MetricMonitorResolution != 0 {
		confTarget.RefreshRate = monitor.MetricMonitorResolution
	}

	return &collectorWithLabels{
		Collector: confTarget,
		Labels: map[string]string{
			types.LabelMetaBleemeoTargetAgent:     confTarget.Name,
			types.LabelMetaProbeServiceUUID:       monitor.ID,
			types.LabelMetaBleemeoTargetAgentUUID: monitor.BleemeoAgentID,
		},
	}, nil
}

// processRegexp returns a regexp matching the given string
// in a case-insensitive way, while ensuring each one of its chars
// are considered as a normal character and not a joker.
func processRegexp(input string) (bbConf.Regexp, error) {
	literalRegexp, err := syntax.Parse(input, syntax.Literal|syntax.FoldCase)
	if err != nil {
		return bbConf.Regexp{}, err
	}

	return bbConf.NewRegexp(literalRegexp.String())
}

func preprocessHTTPTarget(targetURL *url.URL, module bbConf.Module) (string, bbConf.Module) {
	// For the host "kubernetes.default.svc", we will likely be unable to resolve it using DNS
	// (because Glouton with hostNetwork=true so it won't use Kubernetes DNS).
	// Use environment for the name resolution.
	if targetURL.Hostname() == "kubernetes.default.svc" {
		host, port := os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT")
		if len(host) != 0 && len(port) != 0 {
			module.HTTP.HTTPClientConfig.TLSConfig.ServerName = "kubernetes.default.svc"
			targetURL.Host = net.JoinHostPort(host, port)
		}

		// In addition, some API implementation require to always provide the certificate (e.g. k3s).
		// We should do something better. It's possible to pass credentials using kubernetes.kubeconfig config.
		if _, err := os.Stat("/run/secrets/kubernetes.io/serviceaccount/token"); err == nil {
			module.HTTP.HTTPClientConfig.BearerTokenFile = "/run/secrets/kubernetes.io/serviceaccount/token"
		}
	}

	return targetURL.String(), module
}

func genCollectorFromStaticTarget(ct configTarget) collectorWithLabels {
	// Exposing the module name allows the client to differentiate local probes when
	// the same URL is scrapped by different modules.
	// Note that this doesn't matter when "remote probes" (aka. probes supplied by the API
	// instead of the local config file) are involved, as those metrics have the 'instance_uuid'
	// label to distinguish monitors.
	return collectorWithLabels{
		Collector: ct,
		Labels: map[string]string{
			types.LabelMetaBleemeoTargetAgent: ct.Name,
			"module":                          ct.ModuleName,
		},
	}
}

// set user-agent on HTTP prober is not already set.
func setUserAgent(modules map[string]bbConf.Module, userAgent string) {
	for k, m := range modules {
		if m.Prober != "http" {
			continue
		}

		if m.HTTP.Headers == nil {
			m.HTTP.Headers = make(map[string]string)
		}

		if m.HTTP.Headers["User-Agent"] != "" {
			continue
		}

		m.HTTP.Headers["User-Agent"] = userAgent
		modules[k] = m
	}
}

// New sets the static part of blackbox configuration (aka. targets that must be scrapped no matter what).
// This completely resets the configuration.
func New(registry *registry.Registry, config config.Blackbox) (*RegisterManager, error) {
	setUserAgent(config.Modules, config.UserAgent)

	for idx, v := range config.Modules {
		if v.Timeout == 0 {
			v.Timeout = defaultTimeout
			config.Modules[idx] = v
		}
	}

	targets := make([]collectorWithLabels, 0, len(config.Targets))

	for idx := range config.Targets {
		if config.Targets[idx].Name == "" {
			config.Targets[idx].Name = config.Targets[idx].URL
		}

		module, present := config.Modules[config.Targets[idx].Module]
		// if the module is unknown, add it to the list
		if !present {
			return nil, fmt.Errorf("%w for %s (module '%v'). "+
				"This is a probably bug, please contact us", errUnknownModule, config.Targets[idx].Name, config.Targets[idx].Module)
		}

		targets = append(targets, genCollectorFromStaticTarget(configTarget{
			Name:       config.Targets[idx].Name,
			URL:        config.Targets[idx].URL,
			Module:     module,
			ModuleName: config.Targets[idx].Module,
			nowFunc:    time.Now,
		}))
	}

	manager := &RegisterManager{
		targets:       targets,
		registrations: make(map[int]gathererWithConfigTarget, len(config.Targets)),
		registry:      registry,
		scraperName:   config.ScraperName,
		userAgent:     config.UserAgent,
	}

	if err := manager.updateRegistrations(); err != nil {
		return nil, err
	}

	return manager, nil
}

// DiagnosticArchive add diagnostic information.
func (m *RegisterManager) DiagnosticArchive(_ context.Context, archive types.ArchiveWriter) error {
	m.l.Lock()
	targets := m.targets
	m.l.Unlock()

	file, err := archive.Create("blackbox.txt")
	if err != nil {
		return err
	}

	for _, t := range targets {
		fmt.Fprintf(file, "url=%s labels=%v\n", t.Collector.URL, t.Labels)
	}

	return nil
}

// UpdateDynamicTargets generates a config we can ingest into blackbox (from the dynamic probes).
func (m *RegisterManager) UpdateDynamicTargets(monitors []types.Monitor) error {
	// it is easier to keep only the static monitors and rebuild the dynamic config
	// than to compute the difference between the new and the old configuration.
	// This is simple because calling UpdateDynamicTargets with the same argument should be idempotent.
	newTargets := make([]collectorWithLabels, 0, len(monitors)+len(m.targets))

	// get a list of static monitors
	for _, currentTarget := range m.targets {
		if currentTarget.Collector.BleemeoAgentID == "" {
			newTargets = append(newTargets, currentTarget)
		}
	}

	for _, monitor := range monitors {
		collector, err := genCollectorFromDynamicTarget(monitor, m.userAgent)
		if err != nil {
			logger.V(1).Printf("Monitor with URL %s is ignored: %v", monitor.URL, err)

			continue
		}

		newTargets = append(newTargets, *collector)
	}

	if m.scraperName != "" {
		for idx := range newTargets {
			newTargets[idx].Labels[types.LabelMetaProbeScraperName] = m.scraperName
		}
	}

	m.l.Lock()
	m.targets = newTargets
	m.l.Unlock()

	logger.V(2).Println("blackbox_exporter: Internal configuration successfully updated.")

	return m.updateRegistrations()
}
