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
	"errors"
	"glouton/config"
	"glouton/logger"
	"glouton/prometheus/matcher"
	"glouton/types"
	"net/url"
	"strings"
	"sync"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/prometheus/pkg/labels"
)

var errEmptyMetaData = errors.New("missing scrape instance or scrape job for metric")

//MetricFilter is a thread-safe holder of an allow / deny metrics list
type MetricFilter struct {
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

	m.allowList, err = buildMatcherList(config, "allow")
	if err != nil {
		return err
	}

	m.denyList, err = buildMatcherList(config, "deny")

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
					// if point.Labels[types.LabelName] == "process_cpu_seconds_total" {
					// 	fmt.Println("HERE", point)
					// }
					didMatch = true
					break
				}
			}
			if !didMatch {
				// if point.Labels[types.LabelName] == "process_cpu_seconds_total" {
				// 	fmt.Println("HERE2", point)
				// }
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

func dto2Labels(name string, input *dto.Metric) labels.Labels {
	lbls := make(map[string]string, len(input.Label)+1)
	for _, lp := range input.Label {
		lbls[*lp.Name] = *lp.Value
	}

	lbls["__name__"] = name

	return labels.FromMap(lbls)
}

func dtotoMetricLabels(input []*dto.MetricFamily) []labels.Labels {
	result := make([]labels.Labels, 0, len(input))

	for _, mf := range input {
		for _, m := range mf.Metric {
			result = append(result, dto2Labels(*mf.Name, m))
		}
	}

	return result
}

func (m *MetricFilter) FilterFamilies(f []*dto.MetricFamily) []*dto.MetricFamily {
	i := 0
	// tmp := dtotoMetricLabels(f)
	// logger.V(0).Println(tmp)

	if len(m.denyList) != 0 {
		for _, family := range f {
			didMatch := false
			for _, denyVal := range m.denyList {
				matched := denyVal.MatchesMetricFamily(family)
				if matched {
					didMatch = true
					break
				}
			}
			if !didMatch {
				f[i] = family
				i++
			}
		}
		f = f[:i]
		i = 0
	}

	for _, family := range f {
		for _, allowVal := range m.allowList {
			matched := allowVal.MatchesMetricFamily(family)
			if matched {
				f[i] = family
				i++
				break
			}
		}
	}

	f = f[:i]

	return f
}

func (m *MetricFilter) UpdateFilters(labels map[string]string) error {
	return nil
	toProcessAllow := []string{}
	toProcessDeny := []string{}

	if allow, ok := labels["glouton.allow_metrics"]; ok {
		if allow != "" {
			toProcessAllow = append(toProcessAllow, strings.Split(allow, ",")...)
		}
	}

	if deny, ok := labels["glouton.deny_metrics"]; ok {
		if deny != "" {
			toProcessDeny = append(toProcessDeny, strings.Split(deny, ",")...)
		}
	}

	if len(toProcessAllow) != 0 {
		for _, val := range toProcessAllow {
			new, err := matcher.NormalizeMetric(val)
			if err != nil {
				return err
			}

			scrapeInstance := new.Get(types.LabelScrapeInstance)
			scrapeJob := new.Get(types.LabelScrapeJob)
			if scrapeInstance == "" || scrapeJob == "" {
				return errEmptyMetaData
			}

			found := false

			for _, val := range m.allowList {
				listScrapeInstance := val.Get(types.LabelScrapeInstance)
				listScrapeJob := val.Get(types.LabelScrapeJob)

				if listScrapeInstance == "" || listScrapeJob == "" {
					continue
				}

				if listScrapeInstance == scrapeInstance && listScrapeJob == scrapeJob {
					found = true
				}
			}

			if !found {
				logger.V(0).Println("Adding new metric allow:", new)
				m.allowList = append(m.allowList, new)
			}
		}
	}

	if len(toProcessDeny) != 0 {
		for _, val := range toProcessDeny {
			new, err := matcher.NormalizeMetric(val)
			if err != nil {
				return err
			}

			m.denyList = append(m.allowList, new)
		}
	}
	return nil
}
