// Copyright 2015-2021 Bleemeo
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
	"fmt"
	"glouton/types"
	"net/url"
	"strings"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/prometheus/pkg/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

type Matchers []*labels.Matcher

func globToRegex(str string) string {
	r := strings.NewReplacer(
		".", "\\.", "$", "\\$", "^", "\\^", "*", ".*",
	)

	return r.Replace(str)
}

func NormalizeMetric(metric string) (Matchers, error) {
	if !strings.Contains(metric, "{") {
		matchType := "="

		if strings.Contains(metric, "*") {
			// metric is in the blob format: we need to convert it in a regex
			matchType += "~"
		}

		metric = globToRegex(metric)
		metric = fmt.Sprintf("{%s%s\"%s\"}", types.LabelName, matchType, metric)
	}

	m, err := parser.ParseMetricSelector(metric)
	if err != nil {
		return nil, err
	}

	return m, nil
}

//Get returns the matcher with the specided label as Name.
// nil will be returned if not matcher were found.
func (m *Matchers) Get(label string) *labels.Matcher {
	for _, val := range *m {
		if val.Name == label {
			return val
		}
	}

	return nil
}

//Add will add a new matcher to the metric.
func (m *Matchers) Add(label string, value string, labelType labels.MatchType) error {
	new, err := labels.NewMatcher(labelType, label, value)
	if err != nil {
		return err
	}

	*m = append(*m, new)

	return nil
}

func (m *Matchers) String() string {
	res := "{"

	for i, value := range *m {
		res += value.String()
		if i+1 < len(*m) {
			res += ","
		}
	}

	res += "}"

	return res
}

func (m *Matchers) MatchesPoint(point types.MetricPoint) bool {
	for _, matcher := range *m {
		if !matchesLabels(matcher, point.Labels) {
			return false
		}
	}

	return true
}

func dto2Labels(name string, input *dto.Metric) map[string]string {
	lbls := make(map[string]string, len(input.Label)+1)
	for _, lp := range input.Label {
		lbls[*lp.Name] = *lp.Value
	}

	lbls["__name__"] = name

	return lbls
}

func matchesLabels(m *labels.Matcher, lbls map[string]string) bool {
	val, found := lbls[m.Name]
	if !found {
		val = ""
	}

	return m.Matches(val)
}

func (m *Matchers) MatchesMetric(name string, mt *dto.Metric) bool {
	didMatch := true

	for _, matcher := range *m {
		labels := dto2Labels(name, mt)

		if !matchesLabels(matcher, labels) {
			didMatch = false
		}
	}

	return didMatch
}

// HostPort return host:port.
func HostPort(u *url.URL) string {
	hostname := u.Hostname()
	port := u.Port()

	return hostname + ":" + port
}
