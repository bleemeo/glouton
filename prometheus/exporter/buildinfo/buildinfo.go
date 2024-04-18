// Copyright 2015-2023 Bleemeo
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

package buildinfo

import (
	"fmt"
	"runtime"

	"github.com/bleemeo/glouton/prometheus/exporter/collectors"

	"github.com/prometheus/client_golang/prometheus"
)

// AddBuildInfo add the build info present in official Prometheus collector
//
// We can't reusing github.com/prometheus/common/version because it rely on global
// variable set at build-time.
//
// This method re-generate the same metric from argument instead of global variable.
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
