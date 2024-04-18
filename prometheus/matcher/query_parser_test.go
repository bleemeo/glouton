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

package matcher

import (
	"github.com/bleemeo/glouton/types"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

func TestMatchersFromQuery(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  []Matchers
	}{
		{
			name:  "simple",
			query: "cpu_used",
			want: []Matchers{
				{
					&labels.Matcher{
						Type:  labels.MatchEqual,
						Name:  types.LabelName,
						Value: "cpu_used",
					},
				},
			},
		},
		{
			name:  "operation",
			query: "cpu_user + cpu_system",
			want: []Matchers{
				{
					&labels.Matcher{
						Type:  labels.MatchEqual,
						Name:  types.LabelName,
						Value: "cpu_user",
					},
				},
				{
					&labels.Matcher{
						Type:  labels.MatchEqual,
						Name:  types.LabelName,
						Value: "cpu_system",
					},
				},
			},
		},
		{
			name:  "aggregation",
			query: "sum(disk_used_perc)",
			want: []Matchers{
				{
					&labels.Matcher{
						Type:  labels.MatchEqual,
						Name:  types.LabelName,
						Value: "disk_used_perc",
					},
				},
			},
		},
		{
			name:  "rate",
			query: "rate(request_per_seconds_total[1m])",
			want: []Matchers{
				{
					&labels.Matcher{
						Type:  labels.MatchEqual,
						Name:  types.LabelName,
						Value: "request_per_seconds_total",
					},
				},
			},
		},
		{
			name:  "subquery",
			query: "rate(request_per_seconds_total[1m:10s])",
			want: []Matchers{
				{
					&labels.Matcher{
						Type:  labels.MatchEqual,
						Name:  types.LabelName,
						Value: "request_per_seconds_total",
					},
				},
			},
		},
		{
			name:  "plus_one",
			query: "uptime+1",
			want: []Matchers{
				{
					&labels.Matcher{
						Type:  labels.MatchEqual,
						Name:  types.LabelName,
						Value: "uptime",
					},
				},
			},
		},
		{
			name:  "parenthesis",
			query: "(uptime+1)",
			want: []Matchers{
				{
					&labels.Matcher{
						Type:  labels.MatchEqual,
						Name:  types.LabelName,
						Value: "uptime",
					},
				},
			},
		},
		{
			name:  "minus",
			query: "-uptime",
			want: []Matchers{
				{
					&labels.Matcher{
						Type:  labels.MatchEqual,
						Name:  types.LabelName,
						Value: "uptime",
					},
				},
			},
		},
		{
			name:  "matcher_label",
			query: `disk_used_perc{item="/home"}`,
			want: []Matchers{
				{
					&labels.Matcher{
						Type:  labels.MatchEqual,
						Name:  "item",
						Value: "/home",
					},
					&labels.Matcher{
						Type:  labels.MatchEqual,
						Name:  types.LabelName,
						Value: "disk_used_perc",
					},
				},
			},
		},
		{
			name:  "matcher_noteq",
			query: `disk_used_perc{item!="/home"}`,
			want: []Matchers{
				{
					&labels.Matcher{
						Type:  labels.MatchNotEqual,
						Name:  "item",
						Value: "/home",
					},
					&labels.Matcher{
						Type:  labels.MatchEqual,
						Name:  types.LabelName,
						Value: "disk_used_perc",
					},
				},
			},
		},
		{
			name:  "matcher_re",
			query: `disk_used_perc{item=~"(/home|/)"}`,
			want: []Matchers{
				{
					&labels.Matcher{
						Type:  labels.MatchRegexp,
						Name:  "item",
						Value: "(/home|/)",
					},
					&labels.Matcher{
						Type:  labels.MatchEqual,
						Name:  types.LabelName,
						Value: "disk_used_perc",
					},
				},
			},
		},
		{
			name:  "re_name",
			query: `{__name__=~"disk_used.*"}`,
			want: []Matchers{
				{
					&labels.Matcher{
						Type:  labels.MatchRegexp,
						Name:  types.LabelName,
						Value: "disk_used.*",
					},
				},
			},
		},
		{
			name:  "complex",
			query: `1/(max(avg_over_time(request_total{status="200"}[1m] offset 5m))) * (max(uptime{instance="host",item!~"ok?"}) + min_over_time(plop_total[1m:10s] offset 1m))`,
			want: []Matchers{
				{
					&labels.Matcher{
						Type:  labels.MatchEqual,
						Name:  "status",
						Value: "200",
					},
					&labels.Matcher{
						Type:  labels.MatchEqual,
						Name:  types.LabelName,
						Value: "request_total",
					},
				},
				{
					&labels.Matcher{
						Type:  labels.MatchEqual,
						Name:  "instance",
						Value: "host",
					},
					&labels.Matcher{
						Type:  labels.MatchNotRegexp,
						Name:  "item",
						Value: "ok?",
					},
					&labels.Matcher{
						Type:  labels.MatchEqual,
						Name:  types.LabelName,
						Value: "uptime",
					},
				},
				{
					&labels.Matcher{
						Type:  labels.MatchEqual,
						Name:  types.LabelName,
						Value: "plop_total",
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			expr, err := parser.ParseExpr(tt.query)
			if err != nil {
				t.Fatal(err)
			}

			got := MatchersFromQuery(expr)

			res := cmp.Diff(got, tt.want, cmpopts.IgnoreUnexported(labels.Matcher{}))
			if res != "" {
				t.Errorf("got() != expected(): =%s", res)
			}
		})
	}
}
