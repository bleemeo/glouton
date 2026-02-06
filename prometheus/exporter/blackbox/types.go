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
	"crypto/x509"
	"sync"
	"time"

	"github.com/bleemeo/glouton/prometheus/registry"
	bbConf "github.com/prometheus/blackbox_exporter/config"
	"github.com/prometheus/client_golang/prometheus"
)

// configTarget is the information we will supply to the probe() function.
type configTarget struct {
	Name             string
	URL              string
	Module           bbConf.Module
	ModuleName       string
	BleemeoAgentID   string
	CreationDate     time.Time
	RefreshRate      time.Duration
	testInjectCARoot []*x509.Certificate
	nowFunc          func() time.Time
}

// We define labels to apply on a specific collector at registration, as those labels cannot be exposed
// while gathering (e.g. labels prefixed by '__').
type collectorWithLabels struct {
	Collector configTarget
	Labels    map[string]string
}

// We need to keep a reference to the gatherer, to be able to stop the ticker, if it is a TickingGatherer.
type gathererWithConfigTarget struct {
	target   configTarget
	gatherer prometheus.Gatherer
}

// RegisterManager is an abstraction that allows us to reload blackbox at runtime, enabling and disabling
// probes at will.
type RegisterManager struct {
	targets       []collectorWithLabels
	scraperName   string
	registrations map[int]gathererWithConfigTarget
	registry      *registry.Registry
	userAgent     string
	l             sync.Mutex
}
