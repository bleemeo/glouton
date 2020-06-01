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
	"glouton/logger"
	"glouton/version"
	"io"
	"io/ioutil"
	"net/http"
	"sync"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
)

// Scrapper is a scrapper to Prometheus exporters.
type Scrapper struct {
	l       sync.Mutex
	targets []Target
	metrics map[string]*dto.MetricFamily
}

// Target describe a scraping target.
type Target struct {
	URL         string
	Name        string
	Prefix      string
	ExtraLabels map[string]string
}

// New initialise Prometheus scrapper.
func New(targets []Target) *Scrapper {
	return &Scrapper{
		targets: targets,
	}
}

// UpdateTargets define the new list of targets.
func (s *Scrapper) UpdateTargets(new []Target) {
	s.l.Lock()
	defer s.l.Unlock()

	s.targets = new
}

// Run start the scrapper.
func (s *Scrapper) Run(ctx context.Context) error {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.run(ctx)
		case <-ctx.Done():
			return nil
		}
	}
}

// Gather implement prometheus.Gatherer.
func (s *Scrapper) Gather() ([]*dto.MetricFamily, error) {
	s.l.Lock()
	defer s.l.Unlock()

	result := make([]*dto.MetricFamily, 0, len(s.metrics))

	for _, m := range s.metrics {
		result = append(result, m)
	}

	return result, nil
}

func (s *Scrapper) run(ctx context.Context) {
	var (
		wg sync.WaitGroup
		l  sync.Mutex
	)

	s.l.Lock()

	targets := make([]Target, len(s.targets))
	copy(targets, s.targets)

	s.l.Unlock()

	wg.Add(len(targets))

	result := make(map[string]*dto.MetricFamily)

	for _, target := range targets {
		target := target

		go func() {
			defer wg.Done()

			tmp := fetchURL(ctx, target)
			if len(tmp) > 0 {
				l.Lock()
				defer l.Unlock()

				result = merge(result, tmp)
			}
		}()
	}

	wg.Wait()
	l.Lock()
	s.l.Lock()

	s.metrics = result

	s.l.Unlock()
	l.Unlock()
}

// Merge entries from right into left. Left is modified. Pointer to right may be copied into left.
func merge(left map[string]*dto.MetricFamily, right map[string]*dto.MetricFamily) map[string]*dto.MetricFamily {
	for metricName, rightValue := range right {
		leftValue, ok := left[metricName]
		if !ok {
			left[metricName] = rightValue
			continue
		}
		// Assume help & type didn't change.
		// Assume no duplicate
		leftValue.Metric = append(leftValue.Metric, rightValue.Metric...)
	}

	return left
}

func fetchURL(ctx context.Context, target Target) map[string]*dto.MetricFamily {
	logger.V(2).Printf("Prometheus fetching %s at %s", target.Name, target.URL)

	req, err := http.NewRequest("GET", target.URL, nil)
	if err != nil {
		logger.V(1).Printf("Failed to prepare request for Prometheus exporter %s: %v", target.Name, err)
		return nil
	}

	req.Header.Add("Accept", "text/plain;version=0.0.4")
	req.Header.Set("User-Agent", version.UserAgent())

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	resp, err := http.DefaultClient.Do(req.WithContext(ctx))
	if err != nil {
		logger.V(1).Printf("Failed to get metrics for Prometheus exporter %s: %v", target.Name, err)
		return nil
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		logger.V(1).Printf("Failed to read metrics for Prometheus exporter %s: HTTP status is %s", target.Name, resp.Status)
		// Ensure response body is read to allow HTTP keep-alive to works
		_, _ = io.Copy(ioutil.Discard, resp.Body)

		return nil
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		logger.V(1).Printf("Failed to read metrics for Prometheus exporter %s: %v", target.Name, err)
		return nil
	}

	reader := bytes.NewReader(body)

	var parser expfmt.TextParser

	result, err := parser.TextToMetricFamilies(reader)
	if err != nil {
		logger.V(1).Printf("Failed to parse metrics for Prometheus exporter %s: %v", target.Name, err)
		return nil
	}

	if target.Prefix != "" {
		newResult := make(map[string]*dto.MetricFamily, len(result))

		for k, v := range result {
			newResult[target.Prefix+k] = v
		}

		result = newResult
	}

	for _, family := range result {
		for _, m := range family.Metric {
			key := "glouton_job"
			x := dto.LabelPair{Name: &key, Value: &target.Name}
			m.Label = append(m.Label, &x)

			for k, v := range target.ExtraLabels {
				k := k
				v := v
				x := dto.LabelPair{Name: &k, Value: &v}
				m.Label = append(m.Label, &x)
			}
		}
	}

	return result
}
