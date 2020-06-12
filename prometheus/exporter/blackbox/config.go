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
	"glouton/logger"

	bbConf "github.com/prometheus/blackbox_exporter/config"

	"gopkg.in/yaml.v3"
)

// Config is the subset of glouton config that deals with probes.
type Config struct {
	Targets []ConfigTarget           `yaml:"targets"`
	Modules map[string]bbConf.Module `yaml:"modules"`
}

// ConfigTarget is the information we will supply to the probe() function.
type ConfigTarget struct {
	URL        string `yaml:"url"`
	ModuleName string `yaml:"module"`
	Timeout    int    `yaml:"timeout,omitempty"`
}

// ReadConfig generates a config we can ingest into glouton.prometheus.exporter.blackbox.
func ReadConfig(conf interface{}) (res Config, ok bool) {
	// the default value of slices is not, and not a slice litteral
	res.Targets = []ConfigTarget{}
	res.Modules = map[string]bbConf.Module{}

	// the conf cannot be missing here as it have been checked prior to calling ReadConfig()
	marshalled, err := yaml.Marshal(conf)
	if err != nil {
		logger.V(1).Printf("blackbox_exporter: Couldn't marshall blackbox_exporter configuration")
		return res, false
	}

	if err = yaml.Unmarshal(marshalled, &res); err != nil {
		logger.V(1).Printf("blackbox_exporter: Cannot parse blackbox_exporter config: %v", err)
		return res, false
	}

	logger.V(2).Println("blackbox_exporter: Internal configuration successfully parsed.")

	return res, true
}
