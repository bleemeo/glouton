// Copyright 2015-2026 Bleemeo
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

package internal

import (
	"maps"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

func TestAvgDuration(t *testing.T) {
	cases := []struct {
		name        string
		fields      map[string]float64
		unitDivisor float64
		want        map[string]float64
	}{
		{
			// Tomcat's numbers: 400 ms of processing and 10 requests per second is 40 ms
			// spent on each request.
			name:        "milliseconds",
			fields:      map[string]float64{"duration": 400, "count": 10},
			unitDivisor: MsPerSecond,
			want:        map[string]float64{"count": 10, "duration_seconds": 0.04},
		},
		{
			// PgBouncer's numbers.
			name:        "microseconds",
			fields:      map[string]float64{"duration": 500000, "count": 5},
			unitDivisor: UsPerSecond,
			want:        map[string]float64{"count": 5, "duration_seconds": 0.1},
		},
		{
			name:        "nanoseconds",
			fields:      map[string]float64{"duration": 5e9, "count": 2},
			unitDivisor: NsPerSecond,
			want:        map[string]float64{"count": 2, "duration_seconds": 2.5},
		},
		{
			name:        "the other fields are left alone",
			fields:      map[string]float64{"duration": 400, "count": 10, "bytes_sent": 7},
			unitDivisor: MsPerSecond,
			want:        map[string]float64{"count": 10, "bytes_sent": 7, "duration_seconds": 0.04},
		},
		{
			// Dividing by it would be a division by zero. The raw duration is dropped all
			// the same: a rate of milliseconds per second means nothing to a user.
			name:        "no operation completed -> no average, and the raw duration still goes",
			fields:      map[string]float64{"duration": 400, "count": 0},
			unitDivisor: MsPerSecond,
			want:        map[string]float64{"count": 0},
		},
		{
			// Requests were served but took no measurable time, which is a real 0 and not
			// the missing value above.
			name:        "no time spent -> an average of zero",
			fields:      map[string]float64{"duration": 0, "count": 10},
			unitDivisor: MsPerSecond,
			want:        map[string]float64{"count": 10, "duration_seconds": 0},
		},
		{
			// The service restarted: the accumulator reports a counter that went backwards
			// as a negative rate, and a negative average duration means nothing.
			name:        "the duration counter reset -> no average",
			fields:      map[string]float64{"duration": -400, "count": 10},
			unitDivisor: MsPerSecond,
			want:        map[string]float64{"count": 10},
		},
		{
			// The usual shape of a restart, both counters back to zero together. Already
			// covered by the count guard, kept so a change to either one is caught.
			name:        "both counters reset -> no average",
			fields:      map[string]float64{"duration": -400, "count": -10},
			unitDivisor: MsPerSecond,
			want:        map[string]float64{"count": -10},
		},
		{
			// The count isn't differentiated, or the plugin stopped reporting it.
			name:        "no count field -> no average",
			fields:      map[string]float64{"duration": 400},
			unitDivisor: MsPerSecond,
			want:        map[string]float64{},
		},
		{
			// The first gather of a differentiated field has no rate yet.
			name:        "no duration field -> nothing to do",
			fields:      map[string]float64{"count": 10},
			unitDivisor: MsPerSecond,
			want:        map[string]float64{"count": 10},
		},
		{
			name:        "neither field",
			fields:      map[string]float64{"bytes_sent": 7},
			unitDivisor: MsPerSecond,
			want:        map[string]float64{"bytes_sent": 7},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			AvgDuration(tc.fields, "duration", "count", "duration_seconds", tc.unitDivisor)

			if diff := cmp.Diff(tc.want, tc.fields, cmpopts.EquateApprox(0, 1e-9)); diff != "" {
				t.Errorf("AvgDuration() fields (-want +got):\n%s", diff)
			}
		})
	}
}

func TestJoinNonEmptyTags(t *testing.T) {
	cases := []struct {
		name string
		tags map[string]string
		keys []string
		want string
	}{
		{
			// The order is the caller's, so that the item of a series is stable whatever
			// order the map happens to iterate in.
			name: "joined in the order of the keys, not of the map",
			tags: map[string]string{"a": "1", "b": "2", "c": "3"},
			keys: []string{"c", "a", "b"},
			want: "3_1_2",
		},
		{
			name: "a single key needs no separator",
			tags: map[string]string{"a": "1"},
			keys: []string{"a"},
			want: "1",
		},
		{
			// A label the service left unset must not show up as a stray separator.
			name: "a missing tag is skipped",
			tags: map[string]string{"a": "1", "c": "3"},
			keys: []string{"a", "b", "c"},
			want: "1_3",
		},
		{
			name: "an empty value is skipped",
			tags: map[string]string{"a": "1", "b": "", "c": "3"},
			keys: []string{"a", "b", "c"},
			want: "1_3",
		},
		{
			// ActiveMQ reads its tags from an XML document that pads them.
			name: "values are trimmed",
			tags: map[string]string{"a": "  1 ", "b": "\t2\n"},
			keys: []string{"a", "b"},
			want: "1_2",
		},
		{
			name: "a blank value is skipped, not joined as empty",
			tags: map[string]string{"a": "1", "b": "   ", "c": "3"},
			keys: []string{"a", "b", "c"},
			want: "1_3",
		},
		{
			name: "a tag that isn't asked for is ignored",
			tags: map[string]string{"a": "1", "ignored": "2"},
			keys: []string{"a"},
			want: "1",
		},
		{
			name: "no keys",
			tags: map[string]string{"a": "1"},
			keys: nil,
			want: "",
		},
		{
			name: "every value empty",
			tags: map[string]string{"a": "", "b": "  "},
			keys: []string{"a", "b"},
			want: "",
		},
		{
			name: "no tags at all",
			tags: nil,
			keys: []string{"a", "b"},
			want: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := maps.Clone(tc.tags)

			if got := JoinNonEmptyTags(tc.tags, tc.keys); got != tc.want {
				t.Errorf("JoinNonEmptyTags() = %q, want %q", got, tc.want)
			}

			// The tags are the gather context's own map, which the caller goes on using.
			if diff := cmp.Diff(before, tc.tags, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("JoinNonEmptyTags() modified its tags (-before +after):\n%s", diff)
			}
		})
	}
}
