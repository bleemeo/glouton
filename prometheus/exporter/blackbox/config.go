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
	"glouton/bleemeo/types"
	"glouton/logger"
	"net/url"
	"time"

	bbConf "github.com/prometheus/blackbox_exporter/config"
	"gopkg.in/yaml.v3"
)

const maxTimeout time.Duration = 9500 * time.Millisecond

// Config is the subset of glouton config that deals with probes.
type Config struct {
	Targets []ConfigTarget           `yaml:"targets"`
	Modules map[string]bbConf.Module `yaml:"modules"`
}

// ConfigTarget is the information we will supply to the probe() function.
type ConfigTarget struct {
	Name       string `yaml:"name,omitempty"`
	URL        string `yaml:"url"`
	ModuleName string `yaml:"module"`
	// we keep track of the origin of probes, in order to keep static configuration alive across synchronisations
	FromStaticConfig bool   `yaml:"-"`
	ServiceID        string `yaml:"-"`
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

func genModule(uri string, monitor types.Monitor) (string, bbConf.Module, error) {
	mod := defaultModule()

	url, err := url.Parse(uri)
	if err != nil {
		logger.V(2).Printf("Invalid URL: '%s'", uri)
		return uri, mod, err
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
		// TODO: user some better defaults, or even better, and use the local resolver
		mod.DNS.QueryName = url.Host
		// TODO: quid of ipv6
		mod.DNS.QueryType = "A"
		uri = "1.1.1.1"
	case proberNameTCP:
		mod.Prober = proberNameTCP
		uri = url.Host
	case proberNameICMP:
		mod.Prober = proberNameICMP
	}

	return uri, mod, nil
}

// InitConfig sets the static part of blackbox configuration (aka. targets that must be scrapped no matter what).
// This completely resets the configuration.
func InitConfig(conf interface{}) error {
	Conf.Targets = []ConfigTarget{}
	Conf.Modules = map[string]bbConf.Module{}

	// read static config
	// the conf cannot be missing here as it have been checked prior to calling InitConfig()
	marshalled, err := yaml.Marshal(conf)
	if err != nil {
		logger.V(1).Printf("blackbox_exporter: Couldn't marshall blackbox_exporter configuration")
		return err
	}

	if err = yaml.Unmarshal(marshalled, &Conf); err != nil {
		logger.V(1).Printf("blackbox_exporter: Cannot parse blackbox_exporter config: %v", err)
		return err
	}

	for idx := range Conf.Targets {
		Conf.Targets[idx].FromStaticConfig = true
		if Conf.Targets[idx].Name == "" {
			Conf.Targets[idx].Name = Conf.Targets[idx].URL
		}
	}

	for idx, v := range Conf.Modules {
		// override user timeouts when too high or undefined. This is important !
		if v.Timeout > maxTimeout || v.Timeout == 0 {
			v.Timeout = maxTimeout
			Conf.Modules[idx] = v
		}
	}

	return nil
}

// UpdateDynamicTargets generates a config we can ingest into blackbox (from the dynamic probes).
func UpdateDynamicTargets(monitors []types.Monitor) error {
	for _, monitor := range monitors {
		url, module, err := genModule(monitor.URL, monitor)
		if err != nil {
			return err
		}

		Conf.Modules[monitor.ID] = module
		confTarget := ConfigTarget{
			// We allow different modules to act on the same URL, and this is why we use a unique
			// value (the ID of the service) as the module name
			ModuleName: monitor.ID,
			Name:       monitor.URL,
			ServiceID:  monitor.ID,
			URL:        url,
		}

		found := false

		for _, v := range Conf.Targets {
			if v == confTarget {
				found = true
				break
			}
		}

		if !found {
			Conf.Targets = append(Conf.Targets, confTarget)
		}
	}

	logger.V(2).Println("blackbox_exporter: Internal configuration successfully updated.")

	return nil
}
