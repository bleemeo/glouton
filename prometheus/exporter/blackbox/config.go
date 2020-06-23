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
	URL        string `yaml:"url"`
	ModuleName string `yaml:"module"`
	// we keep track of the origin of probes, in order to keep static configuration alive across synchronisations
	FromStaticConfig bool `yaml:"-"`
}

func genModule(url string, monitor types.Monitor) bbConf.Module {
	// TODO: detect probe kind (http/tcp/dns/...)
	mod := bbConf.Module{
		HTTP: bbConf.HTTPProbe{
			IPProtocol: "ip4",
		},
		Prober: "http",
		// Sadly, the API does allow to specify the timeout AFAIK.
		// This value is deliberately lower than our scrape time of 10s, so as to prevent timeouts
		// from exceeding the total scrape time. Otherwise, the outer context could be cancelled
		// en route, thus preventing the collection of ANY metric from blackbox !
		Timeout: maxTimeout,
	}
	if monitor.ExpectedContent != "" {
		mod.HTTP.FailIfBodyNotMatchesRegexp = []string{monitor.ExpectedContent}
	}
	if monitor.ForbiddenContent != "" {
		mod.HTTP.FailIfBodyMatchesRegexp = []string{monitor.ForbiddenContent}
	}
	if monitor.ExpectedResponseCode != 0 {
		mod.HTTP.ValidStatusCodes = []int{monitor.ExpectedResponseCode}
	}

	return mod
}

// InitConfig sets the static part of blackbox configuration (aka. targets that muste be scraped no matter what).
// This resets completely the configuration.
func InitConfig(conf interface{}) error {
	Conf.Targets = []ConfigTarget{}
	Conf.Modules = map[string]bbConf.Module{}

	// read static config
	// the conf cannot be missing here as it have been checked prior to calling ReadConfig()
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

// UpdateConfig generates a config we can ingest into blackbox (from the dynamic probes).
func UpdateConfig(monitors map[string]types.Monitor) {
	for url, monitor := range monitors {
		Conf.Modules[url] = genModule(url, monitor)

		Conf.Targets = append(Conf.Targets, ConfigTarget{
			URL: url,
			// TODO: allow different modules to act on the same URL
			ModuleName: url,
		})
	}

	logger.V(2).Println("blackbox_exporter: Internal configuration successfully updated.")

	return
}
