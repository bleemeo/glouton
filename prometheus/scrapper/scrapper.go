// Copyright 2015-2019 Bleemeo
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

package scrapper

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"glouton/logger"
	"glouton/types"
	"glouton/version"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/prometheus/pkg/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

var errIncorrectStatus = errors.New("incorrect status")

// nolint: gochecknoglobals
var defaultMetrics = []string{
	"process_cpu_seconds_total",
	"process_resident_memory_bytes",
}

type metricSelector []*labels.Matcher

// Target is an URL to scrape.
type Target struct {
	URL            *url.URL
	AllowList      []string
	DenyList       []string
	IncludeDefault bool
	ExtraLabels    map[string]string

	l            sync.Mutex
	allowGlob    []string
	allowMatcher []metricSelector
	denyGlob     []string
	denyMatcher  []metricSelector
}

// HostPort return host:port.
func HostPort(u *url.URL) string {
	hostname := u.Hostname()
	port := u.Port()

	return hostname + ":" + port
}

// Gather implement prometheus.Gatherer.
func (t *Target) Gather() ([]*dto.MetricFamily, error) {
	u := t.URL

	logger.V(2).Printf("Scrapping Prometheus exporter %s", u.String())

	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("prepare request to Prometheus exporter %s: %w", u.String(), err)
	}

	req.Header.Add("Accept", "text/plain;version=0.0.4")
	req.Header.Set("User-Agent", version.UserAgent())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := http.DefaultClient.Do(req.WithContext(ctx))
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Ensure response body is read to allow HTTP keep-alive to works
		_, _ = io.Copy(ioutil.Discard, resp.Body)

		return nil, fmt.Errorf("%w: exporter %s HTTP status is %s", errIncorrectStatus, u.String(), resp.Status)
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read from %s: %w", u.String(), err)
	}

	reader := bytes.NewReader(body)

	var parser expfmt.TextParser

	resultMap, err := parser.TextToMetricFamilies(reader)
	if err != nil {
		return nil, fmt.Errorf("parse metrics from %s: %w", u.String(), err)
	}

	result := make([]*dto.MetricFamily, 0, len(resultMap))

	for _, family := range resultMap {
		result = append(result, family)
	}

	return t.filter(result), nil
}

func (t *Target) filter(result []*dto.MetricFamily) []*dto.MetricFamily {
	t.l.Lock()

	if t.allowGlob == nil {
		for _, x := range t.AllowList {
			if strings.Contains(x, "{") {
				matcher, err := parser.ParseMetricSelector(x)
				if err != nil {
					logger.V(1).Printf("ignoring invalid matcher %v: %w", x, err)
					continue
				}

				// Move matcher on __name__ labels first
				sort.Slice(matcher, func(i, j int) bool {
					return matcher[i].Name == types.LabelName
				})

				t.allowMatcher = append(t.allowMatcher, matcher)
			} else {
				t.allowGlob = append(t.allowGlob, x)
			}
		}

		for _, x := range t.DenyList {
			if strings.Contains(x, "{") {
				matcher, err := parser.ParseMetricSelector(x)
				if err != nil {
					logger.V(1).Printf("ignoring invalid matcher %v: %w", x, err)
					continue
				}

				// Move matcher on __name__ labels first
				sort.Slice(matcher, func(i, j int) bool {
					return matcher[i].Name == types.LabelName
				})

				t.denyMatcher = append(t.denyMatcher, matcher)
			} else {
				t.denyGlob = append(t.denyGlob, x)
			}
		}

		if t.IncludeDefault {
			t.allowGlob = append(t.allowGlob, defaultMetrics...)
		}
	}

	t.l.Unlock()

	n := 0

	for _, x := range result {
		if t.filterMF(x) {
			result[n] = x
			n++
		}
	}

	result = result[:n]

	return result
}

func (t *Target) filterMF(mf *dto.MetricFamily) bool {
	allowed := false

	for _, glob := range t.allowGlob {
		if ok, err := path.Match(glob, *mf.Name); ok && err == nil {
			allowed = true
			break
		}
	}

	for _, glob := range t.denyGlob {
		if ok, err := path.Match(glob, *mf.Name); ok && err == nil {
			return false
		}
	}

	var allowMatcher []metricSelector

	// We only need to test allowMatcher if allowGlob didn't already allowed it
	if !allowed {
		allowMatcher = t.matcherForMetricFamily(mf, t.allowMatcher)
		if len(allowMatcher) == 0 {
			return false
		}
	}

	denyMatcher := t.matcherForMetricFamily(mf, t.denyMatcher)

	if !allowed && len(allowMatcher) == 0 {
		return false
	}

	n := 0

	for _, x := range mf.Metric {
		if t.keepMetric(*mf.Name, x, allowed, allowMatcher, denyMatcher) {
			mf.Metric[n] = x
			n++
		}
	}

	mf.Metric = mf.Metric[:n]

	return true
}

func (t *Target) matcherForMetricFamily(mf *dto.MetricFamily, allSelectors []metricSelector) []metricSelector {
	results := make([]metricSelector, 0, len(allSelectors))

	for _, selector := range allSelectors {
		candidate := true

		for _, m := range selector {
			if m.Name != types.LabelName {
				break
			}

			if !m.Matches(*mf.Name) {
				candidate = false
				break
			}
		}

		if candidate {
			results = append(results, selector)
		}
	}

	return results
}

func (t *Target) keepMetric(name string, m *dto.Metric, globalAllow bool, allowMatcher []metricSelector, denyMatcher []metricSelector) bool {
	allow := globalAllow

	for _, selector := range allowMatcher {
		if selector.Matches(dto2Labels(name, m)) {
			allow = true
			break
		}
	}

	if !allow {
		return false
	}

	for _, selector := range denyMatcher {
		if selector.Matches(dto2Labels(name, m)) {
			return false
		}
	}

	return true
}

func dto2Labels(name string, input *dto.Metric) labels.Labels {
	lbls := make(map[string]string, len(input.Label)+1)
	for _, lp := range input.Label {
		lbls[*lp.Name] = *lp.Value
	}

	lbls["__name__"] = name

	return labels.FromMap(lbls)
}

func (matchers metricSelector) Matches(lbls labels.Labels) bool {
	for _, m := range matchers {
		value := lbls.Get(m.Name)
		if !m.Matches(value) {
			return false
		}
	}

	return true
}
