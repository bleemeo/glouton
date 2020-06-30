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
	"fmt"
	"glouton/bleemeo/types"
	"glouton/logger"
	"glouton/prometheus/registry"
	gloutonTypes "glouton/types"
	"net/url"
	"time"

	bbConf "github.com/prometheus/blackbox_exporter/config"
	"gopkg.in/yaml.v3"
)

const maxTimeout time.Duration = 9500 * time.Millisecond

// yamlConfig is the subset of glouton config that deals with probes.
type yamlConfig struct {
	Targets     []yamlConfigTarget       `yaml:"targets"`
	Modules     map[string]bbConf.Module `yaml:"modules"`
	ScraperName string                   `yaml:"scraper_name,omitempty"`
	BleemeoMode bool                     `yaml:"bleemeo_mode,omitempty"`
}

// ConfigTarget is the information we will supply to the probe() function.
type yamlConfigTarget struct {
	Name       string `yaml:"name,omitempty"`
	URL        string `yaml:"url"`
	ModuleName string `yaml:"module"`
}

func defaultModule() bbConf.Module {
	return bbConf.Module{
		HTTP: bbConf.HTTPProbe{
			IPProtocol:         "ip4",
			IPProtocolFallback: true,
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
		// This value is deliberately lower than our scrape time of 10s, so as to prevent timeouts
		// from exceeding the total scrape time. Otherwise, the outer context could be cancelled
		// en route, thus preventing the collection of ANY metric from blackbox !
		Timeout: maxTimeout,
	}
}

func genCollectorFromDynamicTarget(uri string, monitor types.Monitor) (*collectorWithLabels, error) {
	mod := defaultModule()

	url, err := url.Parse(uri)
	if err != nil {
		logger.V(2).Printf("Invalid URL: '%s'", uri)
		return nil, err
	}

	switch url.Scheme {
	case proberNameHTTP, "https":
		// we default to ipv4, due to blackbox limitations with the protocol fallback
		mod.Prober = proberNameHTTP
		if monitor.ExpectedContent != "" {
			mod.HTTP.FailIfBodyNotMatchesRegexp = []string{monitor.ExpectedContent}
		}

		if monitor.ForbiddenContent != "" {
			mod.HTTP.FailIfBodyMatchesRegexp = []string{monitor.ForbiddenContent}
		}

		if monitor.ExpectedResponseCode != 0 {
			mod.HTTP.ValidStatusCodes = []int{monitor.ExpectedResponseCode}
		}
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
	}

	confTarget := configTarget{
		Module:         mod,
		Name:           monitor.URL,
		BleemeoAgentID: monitor.AgentID,
		URL:            uri,
	}

	return &collectorWithLabels{
		collector: confTarget,
		labels: map[string]string{
			gloutonTypes.LabelMetaProbeTarget:      confTarget.Name,
			gloutonTypes.LabelMetaProbeServiceUUID: monitor.ID,
			gloutonTypes.LabelMetaProbeAgentUUID:   monitor.AgentID,
		},
	}, nil
}

func genCollectorFromStaticTarget(ct configTarget) collectorWithLabels {
	// Exposing the module name allows the client to differentiate local probes when
	// the same URL is scrapped by different modules.
	// Note that this doesn't matter when "remote probes" (aka. probes supplied by the API
	// instead of the local config file) are involved, as those metrics have the 'instance_uuid'
	// label to distinguish monitors.
	return collectorWithLabels{
		collector: ct,
		labels: map[string]string{
			gloutonTypes.LabelMetaProbeTarget: ct.Name,
			"module":                          ct.ModuleName,
		},
	}
}

// New sets the static part of blackbox configuration (aka. targets that must be scrapped no matter what).
// This completely resets the configuration.
func New(registry *registry.Registry, externalConf interface{}) (*RegisterManager, error) {
	conf := yamlConfig{}

	// read static config
	// the conf cannot be missing here as it have been checked prior to calling InitConfig()
	marshalled, err := yaml.Marshal(externalConf)
	if err != nil {
		logger.V(1).Printf("blackbox_exporter: Couldn't marshal blackbox_exporter configuration")
		return nil, err
	}

	if err = yaml.Unmarshal(marshalled, &conf); err != nil {
		logger.V(1).Printf("blackbox_exporter: Cannot parse blackbox_exporter config: %v", err)
		return nil, err
	}

	for idx, v := range conf.Modules {
		// override user timeouts when too high or undefined. This is important !
		if v.Timeout > maxTimeout || v.Timeout == 0 {
			v.Timeout = maxTimeout
			conf.Modules[idx] = v
		}
	}

	targets := make([]collectorWithLabels, 0, len(conf.Targets))

	for idx := range conf.Targets {
		if conf.Targets[idx].Name == "" {
			conf.Targets[idx].Name = conf.Targets[idx].URL
		}

		module, present := conf.Modules[conf.Targets[idx].ModuleName]
		// if the module is unknown, add it to the list
		if !present {
			return nil, fmt.Errorf("blackbox_exporter: unknown blackbox module found in your configuration for %s (module '%v'). "+
				"This is a probably bug, please contact us", conf.Targets[idx].Name, conf.Targets[idx].ModuleName)
		}

		targets = append(targets, genCollectorFromStaticTarget(configTarget{
			Name:       conf.Targets[idx].Name,
			URL:        conf.Targets[idx].URL,
			Module:     module,
			ModuleName: conf.Targets[idx].ModuleName,
		}))
	}

	manager := &RegisterManager{
		targets:       targets,
		registrations: make(map[int]collectorWithLabels, len(conf.Targets)),
		registry:      registry,
		scraperName:   conf.ScraperName,
		dynamicMode:   conf.BleemeoMode,
	}

	if err := manager.updateRegistrations(); err != nil {
		return nil, err
	}

	return manager, nil
}

// UpdateDynamicTargets generates a config we can ingest into blackbox (from the dynamic probes).
func (m *RegisterManager) UpdateDynamicTargets(monitors []types.Monitor) error {
	// it is easier to keep only the static monitors and rebuild the dynamic config
	// than to compute the difference between the new and the old configuration.
	// This is simple because calling UpdateDynamicTargets with the same argument should be idempotent.
	newTargets := make([]collectorWithLabels, 0, len(monitors)+len(m.targets))

	// get a list of static monitors
	for _, currentTarget := range m.targets {
		if currentTarget.collector.BleemeoAgentID == "" {
			newTargets = append(newTargets, currentTarget)
		}
	}

	// append all dynamic target to the list, when the bleemeo mode is enabled
	if m.dynamicMode {
		for _, monitor := range monitors {
			collector, err := genCollectorFromDynamicTarget(monitor.URL, monitor)
			if err != nil {
				return err
			}

			newTargets = append(newTargets, *collector)
		}
	}

	if m.scraperName != "" {
		for idx := range newTargets {
			newTargets[idx].labels[gloutonTypes.LabelMetaProbeScraperName] = m.scraperName
		}
	}

	m.targets = newTargets

	logger.V(2).Println("blackbox_exporter: Internal configuration successfully updated.")

	return m.updateRegistrations()
}
