package blackbox

import (
	"crypto/x509"
	"glouton/prometheus/registry"
	"glouton/types"
	"sync"
	"time"

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
	testInjectCARoot *x509.Certificate
}

// We define labels to apply on a specific collector at registration, as those labels cannot be exposed
// while gathering (e.g. labels prefixed by '__').
type collectorWithLabels struct {
	collector configTarget
	labels    map[string]string
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
	metricFormat  types.MetricFormat
	userAgent     string
	l             sync.Mutex
}
