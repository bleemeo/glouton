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
	"maps"
	"reflect"
	"sync"
	"time"

	"github.com/bleemeo/glouton/prometheus/registry"
	bbConf "github.com/prometheus/blackbox_exporter/config"
	"github.com/prometheus/client_golang/prometheus"
)

// configTarget is the information we will supply to the probe() function.
type staticTargetOptions struct {
	Name       string
	URL        string
	Module     bbConf.Module
	ModuleName string
}

type commonHelpers interface {
	DNSResolver() (string, error)
	NotifyNeedForDefaultResolver(err error)
}

// We define labels to apply on a specific blackboxCollector at registration, as those labels cannot be exposed
// while gathering (e.g. labels prefixed by '__').
type blackboxCollector struct {
	Name           string
	ModuleName     string
	URL            string
	OriginalURL    string
	Module         bbConf.Module
	BleemeoAgentID string
	CreationDate   time.Time
	RefreshRate    time.Duration
	Labels         map[string]string
	CommonHelpers  commonHelpers
}

func (target blackboxCollector) Equal(other blackboxCollector) bool {
	// return a.BleemeoAgentID == b.BleemeoAgentID && a.URL == b.URL && a.RefreshRate == b.RefreshRate && reflect.DeepEqual(a.Module, b.Module)
	return (target.Name == other.Name &&
		target.ModuleName == other.ModuleName &&
		target.URL == other.URL &&
		reflect.DeepEqual(target.Module, other.Module) &&
		target.BleemeoAgentID == other.BleemeoAgentID &&
		target.CreationDate.Equal(other.CreationDate) &&
		target.RefreshRate == other.RefreshRate &&
		maps.Equal(target.Labels, other.Labels))
}

// We need to keep a reference to the gatherer, to be able to stop the ticker, if it is a TickingGatherer.
type gathererRegistration struct {
	collector blackboxCollector
	gatherer  prometheus.Gatherer
}

// RegisterManager is an abstraction that allows us to reload blackbox at runtime, enabling and disabling
// probes at will.
type RegisterManager struct {
	targets                        []blackboxCollector
	scraperName                    string
	registrations                  map[int]gathererRegistration
	registry                       *registry.Registry
	userAgent                      string
	dnsResolver                    string
	systemDNSResolver              string
	systemDNSLastUpdate            time.Time
	notifiedNeedForDefaultResolver bool
	addWarnings                    func(...error)
	l                              sync.Mutex
}
