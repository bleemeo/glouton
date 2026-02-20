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
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
	"net/url"
	"os"
	"regexp/syntax"
	"strings"
	"time"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/prometheus/registry"
	"github.com/bleemeo/glouton/types"

	bbConf "github.com/prometheus/blackbox_exporter/config"
	promConfig "github.com/prometheus/common/config"
)

var (
	errUnknownModule  = errors.New("unknown blackbox module found in your configuration")
	errUnsupportedURL = errors.New("URL is unsupported")
)

const defaultTimeout = 12 * time.Second

const (
	defaultResolverSentinel = "<default-resolver>"
	errorResolverSentinel   = "<error-resolver>"
)

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
			Recursion:          true,
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

func genCollectorFromDynamicTarget(monitor types.Monitor, userAgent string, helpers commonHelpers) (blackboxCollector, error) {
	mod := defaultModule(userAgent)

	url, err := url.Parse(monitor.URL)
	if err != nil {
		logger.V(2).Printf("Invalid URL: '%s'", monitor.URL)

		return blackboxCollector{}, err
	}

	uri := monitor.URL

	expectedContentRegex, err := processRegexp(monitor.ExpectedContent)
	if err != nil {
		return blackboxCollector{}, err
	}

	forbiddenContentRegex, err := processRegexp(monitor.ForbiddenContent)
	if err != nil {
		return blackboxCollector{}, err
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
		// Default
		mod.Prober = proberNameDNS
		mod.DNS.QueryType = "A"

		// DNS URL will be either dns://dns-server/dns-name OR dns:dns-name
		// In the first case, url.Path, url.Host... are filled.
		// In the second case, we need to use url.Opaque
		if url.Path != "" {
			mod.DNS.QueryName = strings.ToLower(strings.TrimLeft(url.Path, "/"))
			uri = strings.ToLower(url.Host)

			if uri == "" {
				uri = defaultResolverSentinel
			}
		} else {
			mod.DNS.QueryName = strings.ToLower(url.Opaque)
			uri = defaultResolverSentinel
		}

		for keyValue := range strings.SplitSeq(url.RawQuery, ";") {
			if keyValue == "" {
				continue
			}

			part := strings.SplitN(keyValue, "=", 2)
			if len(part) != 2 {
				return blackboxCollector{}, fmt.Errorf("%w: can't parse dnsquery \"%s\"", errUnsupportedURL, url.RawQuery)
			}

			// The whole DNS URL is case insensitive, so we use ToLower() everywhere.
			// But value is used for DNS type (A, AAAA, TXT...) which by convention are in upper case, so
			// use ToUpper() for the value.
			key := strings.ToLower(part[0])
			value := strings.ToUpper(part[1])

			if key == "class" && value != "" && value != "IN" {
				return blackboxCollector{}, fmt.Errorf("%w: unknown DNS class %s", errUnsupportedURL, part[1])
			}

			if key == "type" {
				mod.DNS.QueryType = value
			}
		}

		if monitor.ExpectedContent != "" {
			// Prefix Regex with \s because the RegEx match the full line, e.g. something
			// like "bleemeo.com IN A 15.188.205.60" (possibly with tab rather than space).
			// If we don't prefix, "5.188.205.60" would match.
			// Suffix by $ for similar reason
			mod.DNS.ValidateAnswer.FailIfNoneMatchesRegexp = []string{`\s` + expectedContentRegex.String() + "$"}
		}

		if monitor.ForbiddenContent != "" {
			mod.DNS.ValidateAnswer.FailIfMatchesRegexp = []string{`\s` + forbiddenContentRegex.String() + "$"}
		}
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

	return blackboxCollector{
		Module:         mod,
		Name:           monitor.URL,
		BleemeoAgentID: monitor.BleemeoAgentID,
		URL:            uri,
		OriginalURL:    monitor.URL,
		CreationDate:   monitor.CreationDate,
		RefreshRate:    monitor.MetricMonitorResolution,
		Labels: map[string]string{
			types.LabelMetaBleemeoTargetAgent:     monitor.URL,
			types.LabelMetaProbeServiceUUID:       monitor.ID,
			types.LabelMetaBleemeoTargetAgentUUID: monitor.BleemeoAgentID,
		},
		CommonHelpers: helpers,
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

func genCollectorFromStaticTarget(option staticTargetOptions, helpers commonHelpers) blackboxCollector {
	// Exposing the module name allows the client to differentiate local probes when
	// the same URL is scrapped by different modules.
	// Note that this doesn't matter when "remote probes" (aka. probes supplied by the API
	// instead of the local config file) are involved, as those metrics have the 'instance_uuid'
	// label to distinguish monitors.
	return blackboxCollector{
		Name:        option.Name,
		ModuleName:  option.ModuleName,
		URL:         option.URL,
		OriginalURL: option.URL,
		Module:      option.Module,
		Labels: map[string]string{
			types.LabelMetaBleemeoTargetAgent: option.Name,
			"module":                          option.ModuleName,
		},
		CommonHelpers: helpers,
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
func New(registry *registry.Registry, config config.Blackbox, addWarnings func(...error)) (*RegisterManager, error) {
	setUserAgent(config.Modules, config.UserAgent)

	for idx, v := range config.Modules {
		if v.Timeout == 0 {
			v.Timeout = defaultTimeout
			config.Modules[idx] = v
		}
	}

	targets := make([]blackboxCollector, 0, len(config.Targets))

	manager := &RegisterManager{
		targets:       targets,
		registrations: make(map[int]gathererRegistration, len(config.Targets)),
		registry:      registry,
		scraperName:   config.ScraperName,
		userAgent:     config.UserAgent,
		dnsResolver:   config.DefaultDNSResolver,
		addWarnings:   addWarnings,
	}

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

		manager.targets = append(manager.targets, genCollectorFromStaticTarget(staticTargetOptions{
			Name:       config.Targets[idx].Name,
			URL:        config.Targets[idx].URL,
			Module:     module,
			ModuleName: config.Targets[idx].Module,
		}, manager))
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

	resolver, err := m.DNSResolver()

	errMsg := ""
	if err != nil {
		errMsg = err.Error()
	}

	obj := struct {
		DNSResolver string
		ResolverErr string
		ScraperName string
		UserAgent   string
	}{
		DNSResolver: resolver,
		ResolverErr: errMsg,
		ScraperName: m.scraperName,
		UserAgent:   m.userAgent,
	}

	file, err := archive.Create("blackbox.json")
	if err != nil {
		return err
	}

	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")

	if err := enc.Encode(obj); err != nil {
		return err
	}

	file, err = archive.Create("blackbox-targets.txt")
	if err != nil {
		return err
	}

	for _, t := range targets {
		fmt.Fprintf(file, "url=%s labels=%v\n", t.URL, t.Labels)
	}

	return nil
}

// UpdateDynamicTargets generates a config we can ingest into blackbox (from the dynamic probes).
func (m *RegisterManager) UpdateDynamicTargets(monitors []types.Monitor) error {
	// it is easier to keep only the static monitors and rebuild the dynamic config
	// than to compute the difference between the new and the old configuration.
	// This is simple because calling UpdateDynamicTargets with the same argument should be idempotent.
	newTargets := make([]blackboxCollector, 0, len(monitors)+len(m.targets))

	// get a list of static monitors
	for _, currentTarget := range m.targets {
		if currentTarget.BleemeoAgentID == "" {
			newTargets = append(newTargets, currentTarget)
		}
	}

	for _, monitor := range monitors {
		collector, err := genCollectorFromDynamicTarget(monitor, m.userAgent, m)
		if err != nil {
			logger.V(1).Printf("Monitor with URL %s is ignored: %v", monitor.URL, err)

			continue
		}

		newTargets = append(newTargets, collector)
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
