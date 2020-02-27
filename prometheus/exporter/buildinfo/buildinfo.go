package buildinfo

import (
	"fmt"
	"glouton/prometheus/exporter/collectors"
	"runtime"

	"github.com/prometheus/client_golang/prometheus"
)

// AddBuildInfo add the build info present in official Prometheus collector
//
// We can't reusing github.com/prometheus/common/version because it rely on global
// variable set at build-time.
//
// This method re-generate the same metric from argument instead of global variable
func AddBuildInfo(collector prometheus.Collector, program string, version string, revision string, branch string) prometheus.Collector {

	info := prometheus.NewGaugeFunc(
		prometheus.GaugeOpts{
			Namespace: program,
			Name:      "build_info",
			Help: fmt.Sprintf(
				"A metric with a constant '1' value labeled by version, revision, branch, and goversion from which %s was built.",
				program,
			),
			ConstLabels: prometheus.Labels{
				"version":   version,
				"revision":  revision,
				"branch":    branch,
				"goversion": runtime.Version(),
			},
		},
		func() float64 { return 1 },
	)

	return collectors.Collectors{
		collector,
		info,
	}
}
