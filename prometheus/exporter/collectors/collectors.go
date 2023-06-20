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

package collectors

import (
	"glouton/crashreport"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

// Collectors merge multiple collector in one.
type Collectors []prometheus.Collector

// Describe implement prometheus.Collector. It call sequentially each Describe method.
func (cs Collectors) Describe(ch chan<- *prometheus.Desc) {
	for _, c := range cs {
		c.Describe(ch)
	}
}

// Collect implement prometheus.Collector. It call concurrently each Collect method.
func (cs Collectors) Collect(ch chan<- prometheus.Metric) {
	wg := sync.WaitGroup{}
	wg.Add(len(cs))

	for _, c := range cs {
		go func(c prometheus.Collector) {
			defer crashreport.ProcessPanic()

			c.Collect(ch)
			wg.Done()
		}(c)
	}

	wg.Wait()
}
