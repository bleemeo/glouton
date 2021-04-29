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

func NormalizeMetricScrapper(metric string, instance string, job string) (Matchers, error) {
	promMetric, err := NormalizeMetric(metric)
	if err != nil {
		return nil, err
	}

	promMetric.Add(types.LabelScrapeInstance, instance, labels.MatchEqual)
	promMetric.Add(types.LabelScrapeJob, job, labels.MatchEqual)

	return promMetric, err
}

func NormalizeMetric(metric string) (Matchers, error) {
	if !strings.Contains(metric, "{") {
		matchType := "="

		metric = globToRegex(metric)
		if strings.ContainsAny(metric, "*.$^") {
			// metric is in the blob format: we need to convert it in a regex
			matchType += "~"
		}
		metric = fmt.Sprintf("{%s%s\"%s\"}", types.LabelName, matchType, metric)
	}

	m, err := parser.ParseMetricSelector(metric)
	if err != nil {
		return nil, err
	}

	return m, nil
}

//Add will add a new matcher to the metric. If the name and the type are the same,
// Add will overwrite the old value.
func (m *Matchers) Add(label string, value string, labelType labels.MatchType) error {
	new, err := labels.NewMatcher(labelType, label, value)
	if err != nil {
		return err
	}
	for _, val := range *m {
		if val.Name == new.Name {
			if val.Type == new.Type {
				val.Value = new.Value
			} else {
				break
			}
			return nil
		}
	}
	*m = append(*m, new)

	return nil
}

//MatchesLabels will check in the label List matches the Matchers
// func (matchers *Matchers) MatchesLabels(lbls labels.Labels) bool {
// 	for _, m := range *matchers {
// 		value := lbls.Get(m.Name)
// 		if !m.Matches(value) {
// 			return false
// 		}
// 	}

// 	return true
// }

func (matchers *Matchers) MatchesPoint(point types.MetricPoint) bool {
	for _, m := range *matchers {
		value, found := point.Labels[m.Name]
		if !found {
			return false
		}

		match := m.Matches(value)
		if !match {
			return false
		}
	}
	return true
}

func (matchers *Matchers) MatchesMetricFamily(fm *dto.MetricFamily) bool {

	for _, m := range *matchers {
		if m.Name == types.LabelName {
			if m.Matches(fm.GetName()) {
				return true
			}
		}
	}

	return false
}

//Get returns the value of the label for this matcher, or an empty string if none were found.
func (m *Matchers) Get(label string) string {
	for _, val := range *m {
		if val.Name == label {
			return val.Value
		}
	}

	return ""
}

// HostPort return host:port.
func HostPort(u *url.URL) string {
	hostname := u.Hostname()
	port := u.Port()

	return hostname + ":" + port
}
