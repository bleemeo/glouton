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

package agent

import (
	"fmt"
	"glouton/config"
	"glouton/discovery/promexporter"
	"glouton/logger"
	"glouton/prometheus/matcher"
	"glouton/types"
	"net/url"
	"strings"
	"sync"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/prometheus/pkg/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

// var errEmptyMetaData = errors.New("missing scrape instance or scrape job for metric")

//MetricFilter is a thread-safe holder of an allow / deny metrics list
type MetricFilter struct {

	// staticList contains the matchers generated from static source (config file).
	// They won't change at runtime, and don't need to be rebuilt
	staticAllowList []matcher.Matchers
	staticDenyList  []matcher.Matchers

	// these list are the actual list used while filtering.
	allowList []matcher.Matchers
	denyList  []matcher.Matchers
	l         sync.Mutex
	config    *config.Configuration
}

func buildMatcherList(config *config.Configuration, listType string) ([]matcher.Matchers, error) {
	metricListType := listType + "_metrics"
	metricList := []matcher.Matchers{}
	globalList := config.StringList("metric." + metricListType)

	for _, str := range globalList {
		new, err := matcher.NormalizeMetric(str)
		if err != nil {
			return nil, err
		}

		metricList = append(metricList, new)
	}

	metricList = addScrappersList(config, metricList, metricListType)

	return metricList, nil
}

func addScrappersList(config *config.Configuration, metricList []matcher.Matchers,
	metricListType string) []matcher.Matchers {
	promTargets, _ := config.Get("metric.prometheus.targets")
	promTargetList, ok := promTargets.([]interface{})

	if !ok {
		logger.V(2).Println("Could not cast prometheus.targets as a list of map")
		return metricList
	}
	for _, val := range promTargetList {
		vMap, ok := val.(map[string]interface{})
		if !ok {
			continue
		}

		name, ok := vMap["name"].(string)
		if !ok {
			continue
		}

		instance, ok := vMap["url"].(string)
		if !ok {
			continue
		}

		parsedURL, err := url.Parse(instance)
		if err != nil {
			logger.V(2).Println("Could not parse url for target", name)
		}

		instance = matcher.HostPort(parsedURL)

		allowTargetRaw, ok := vMap[metricListType]
		if !ok {
			// No metrics for this target
			continue
		}

		allowTargetString, ok := allowTargetRaw.([]interface{})
		if !ok {
			logger.V(2).Printf("Could not cast prometheus.targets.%s as a []interface{} for target %s", metricListType, name)
		}

		for _, str := range allowTargetString {
			allowString, ok := str.(string)
			if !ok {
				logger.V(2).Printf("Could not cast prometheus.targets.%s field as a string for target %s", metricListType, name)
				continue
			}
			new, err := matcher.NormalizeMetricScrapper(allowString, instance, name)
			if err != nil {
				logger.V(2).Printf("Could not normalize metric %s with instance %s and job %s", allowString, instance, name)
			}
			metricList = append(metricList, new)
		}
	}
	return metricList
}

func (m *MetricFilter) buildList(config *config.Configuration) error {
	var err error

	m.l.Lock()
	defer m.l.Unlock()

	m.staticAllowList, err = buildMatcherList(config, "allow")
	if err != nil {
		return err
	}

	m.staticDenyList, err = buildMatcherList(config, "deny")
	m.allowList = m.staticAllowList
	m.denyList = m.staticDenyList

	return err
}

func NewMetricFilter(config *config.Configuration) (*MetricFilter, error) {
	new := MetricFilter{}
	err := new.buildList(config)

	new.config = config

	return &new, err
}

func (m *MetricFilter) FilterPoints(points []types.MetricPoint) []types.MetricPoint {
	i := 0

	if len(m.denyList) != 0 {
		for _, point := range points {
			didMatch := false
			for _, denyVal := range m.denyList {
				matched := denyVal.MatchesPoint(point)
				if matched {
					didMatch = true
					break
				}
			}
			if !didMatch {
				points[i] = point
				i++
			}
		}
		points = points[:i]
		i = 0
	}

	for _, point := range points {
		for _, allowVal := range m.allowList {
			matched := allowVal.MatchesPoint(point)
			if matched {
				points[i] = point
				i++
				break
			}
		}
	}

	points = points[:i]

	return points
}

func (m *MetricFilter) filterFamily(f *dto.MetricFamily) {
	i := 0

	if len(m.denyList) > 0 {
		for _, metric := range f.Metric {
			didMatch := false
			for _, denyVal := range m.denyList {
				if denyVal.MatchesMetric(*f.Name, metric) {
					didMatch = true
					break
				}
			}
			if !didMatch {
				f.Metric[i] = metric
				i++
			}
		}

		f.Metric = f.Metric[:i]
		i = 0
	}

	for _, metric := range f.Metric {
		for _, allowVal := range m.allowList {
			if allowVal.MatchesMetric(*f.Name, metric) {
				f.Metric[i] = metric
				i++
				break
			}
		}
	}
}

func (m *MetricFilter) FilterFamilies(f []*dto.MetricFamily) []*dto.MetricFamily {
	for _, family := range f {
		m.filterFamily(family)
	}

	return f
}

// func (m *MetricFilter) updateGlobalLists() {
// 	m.allowList = m.staticAllowList
// 	m.denyList = m.staticDenyList

// 	for _, source := range m.dynamicMatcherSources {
// 		m.allowList = append(m.allowList, source.allowList...)
// 		m.denyList = append(m.denyList, source.denyList...)
// 	}
// }

// func containsMetric(refList []matcher.Matchers, toCheck *matcher.Matchers) bool {
// 	//TODO: correctly determine if the matchers already exist in this list
// 	return false
// }

// func updateList(refList []matcher.Matchers, list []string) ([]matcher.Matchers, error) {
// 	new := []matcher.Matchers{}

// 	for _, val := range list {
// 		metricMatchers, err := matcher.NormalizeMetric(val)
// 		if err != nil {
// 			return nil, err
// 		}

// 		new = append(new, metricMatchers)
// 	}

// 	for _, m := range new {
// 		if !containsMetric(refList, &m) {
// 			refList = append(refList, m)
// 		}
// 		//TODO: update of the list with the correct value when it does contain
// 	}

// 	return refList, nil
// }

// func (s *matcherSource) updateSource(allowList []string, denyList []string) (bool, error) {

// 	if len(allowList) != 0 {
// 		newList, err := updateList(s.allowList, allowList)
// 		if err != nil {
// 			return false, err
// 		}
// 		s.allowList = newList
// 	}

// 	if len(denyList) != 0 {
// 		newList, err := updateList(s.denyList, denyList)
// 		if err != nil {
// 			return false, err
// 		}

// 		s.denyList = newList
// 	}

// 	return true, nil
// }

// func (m *MetricFilter) UpdateFilters(name string, labels map[string]string) error {
// 	changed := false

// 	var err error

// 	if name != "" && len(labels) > 0 {
// 		changed, err = m.addNewSource(name, labels)
// 		if err != nil {
// 			return err
// 		}
// 	}

// 	if changed {
// 		m.updateGlobalLists()
// 	}
// 	logger.V(0).Println("End of filter update\n", len(m.allowList), m.allowList)
// 	return nil
// }

// func (m *MetricFilter) NewDynamicSource(name string, allowList []string, denyList []string) (*matcherSource, error) {
// 	allowMatchers := []matcher.Matchers{}
// 	denyMatchers := []matcher.Matchers{}

// 	for _, val := range allowList {
// 		res, err := matcher.NormalizeMetric(val)
// 		if err != nil {
// 			return nil, err
// 		}

// 		allowMatchers = append(allowMatchers, res)
// 	}

// 	for _, val := range denyList {
// 		res, err := matcher.NormalizeMetric(val)
// 		if err != nil {
// 			return nil, err
// 		}

// 		denyMatchers = append(denyMatchers, res)
// 	}

// 	new := &matcherSource{
// 		name:      name,
// 		allowList: allowMatchers,
// 		denyList:  denyMatchers,
// 	}

// 	return new, nil

// }

func (m *MetricFilter) RebuildDynamicLists(scrapper *promexporter.DynamicScrapper) error {
	allowList := []matcher.Matchers{}
	denyList := []matcher.Matchers{}

	for key, val := range scrapper.RegisteredLabels {
		allowMatchers, denyMatchers, err := addNewSource(scrapper.ContainersLabels[key], val)
		if err != nil {
			return err
		}
		allowList = append(allowList, allowMatchers...)
		denyList = append(denyList, denyMatchers...)
	}

	m.allowList = append(m.staticAllowList, allowList...)
	m.denyList = append(m.staticDenyList, denyList...)

	return nil
}

func addNewSource(cLabels map[string]string, extraLabels map[string]string) ([]matcher.Matchers, []matcher.Matchers, error) {
	allowList := []string{}
	denyList := []string{}

	if allow, ok := cLabels["glouton.allow_metrics"]; ok && allow != "" {
		allowList = strings.Split(allow, ",")
	}

	if deny, ok := cLabels["glouton.deny_metrics"]; ok && deny != "" {
		denyList = strings.Split(deny, ",")
	}

	logger.V(0).Println(allowList)
	logger.V(0).Println(denyList)
	allowMatchers, denyMatchers, err := newMatcherSource(allowList, denyList,
		extraLabels[types.LabelMetaScrapeInstance], extraLabels[types.LabelMetaScrapeJob])
	if err != nil {
		return nil, nil, err
	}

	return allowMatchers, denyMatchers, nil
}

func addMetaLabels(metric string, scrapeInstance string, scrapeJob string) (string, error) {
	if !strings.Contains(metric, "{") {
		return fmt.Sprintf("%s{%s=\"%s\",%s=\"%s\"}", metric, types.LabelScrapeInstance, scrapeInstance, types.LabelScrapeJob, scrapeJob), nil
	}

	var m matcher.Matchers

	var err error

	m, err = parser.ParseMetricSelector(metric)

	if err != nil {
		return "", err
	}

	m.Add(types.LabelScrapeInstance, scrapeInstance, labels.MatchEqual)
	m.Add(types.LabelScrapeJob, scrapeJob, labels.MatchEqual)

	return m.String(), nil
}

func newMatcherSource(allowList []string, denyList []string, scrapeInstance string, scrapeJob string) ([]matcher.Matchers, []matcher.Matchers, error) {
	allowMatchers := []matcher.Matchers{}
	denyMatchers := []matcher.Matchers{}

	for _, val := range allowList {
		allowVal, err := addMetaLabels(val, scrapeInstance, scrapeJob)
		if err != nil {
			return nil, nil, err
		}

		res, err := matcher.NormalizeMetric(allowVal)
		if err != nil {
			return nil, nil, err
		}

		allowMatchers = append(allowMatchers, res)
	}

	for _, val := range denyList {
		denyVal, err := addMetaLabels(val, scrapeInstance, scrapeJob)
		if err != nil {
			return nil, nil, err
		}

		res, err := matcher.NormalizeMetric(denyVal)
		if err != nil {
			return nil, nil, err
		}

		denyMatchers = append(denyMatchers, res)
	}

	return allowMatchers, denyMatchers, nil

}
