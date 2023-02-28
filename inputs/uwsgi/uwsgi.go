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

package uwsgi

import (
	"glouton/inputs"
	"glouton/inputs/internal"
	"glouton/types"

	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/uwsgi"
)

// New returns a uWSGI input.
func New(url string) (telegraf.Input, *inputs.GathererOptions, error) {
	input, ok := telegraf_inputs.Inputs["uwsgi"]
	if !ok {
		return nil, nil, inputs.ErrDisabledInput
	}

	uwsgiInput, ok := input().(*uwsgi.Uwsgi)
	if !ok {
		return nil, nil, inputs.ErrUnexpectedType
	}

	uwsgiInput.Servers = []string{url}

	internalInput := &internal.Input{
		Input: uwsgiInput,
		Accumulator: internal.Accumulator{
			DerivatedMetrics: []string{
				"requests",
				"exceptions",
				"tx",
				"harakiri_count",
			},
		},
		Name: "uWSGI",
	}

	options := &inputs.GathererOptions{
		Rules: []types.SimpleRule{
			{
				TargetName:  "uwsgi_requests",
				PromQLQuery: "sum without (worker_id) (uwsgi_workers_requests)",
			},
			{
				TargetName:  "uwsgi_transmitted",
				PromQLQuery: "sum without (worker_id) (uwsgi_workers_tx)",
			},
			{
				TargetName:  "uwsgi_avg_request_time",
				PromQLQuery: "avg without (worker_id) (uwsgi_workers_avg_rt)/1e6",
			},
			{
				TargetName:  "uwsgi_memory_used",
				PromQLQuery: "sum without (worker_id) (uwsgi_workers_rss)/8",
			},
			{
				TargetName:  "uwsgi_exceptions",
				PromQLQuery: "sum without (worker_id) (uwsgi_workers_exceptions)",
			},
			{
				TargetName:  "uwsgi_harakiri_count",
				PromQLQuery: "sum without (worker_id) (uwsgi_workers_harakiri_count)",
			},
		},
	}

	return internalInput, options, nil
}
