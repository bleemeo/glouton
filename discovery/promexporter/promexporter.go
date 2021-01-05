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

// Package promexporter implement an discovery of Prometheus exporter based on Docker labels / Kubernetes annotations
package promexporter

import (
	"fmt"
	"glouton/facts"
	"glouton/logger"
	"glouton/prometheus/registry"
	"glouton/prometheus/scrapper"
	"glouton/types"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"
)

type target struct {
	URL         string
	ExtraLabels map[string]string
}

// listExporters return list of exporters based on containers labels/annotations.
func (d *DynamicScrapper) listExporters(containers []facts.Container) []target {
	result := make([]target, 0)

	for _, c := range containers {
		u := urlFromLabels(c.Labels(), c.PrimaryAddress())

		if u == "" {
			u = urlFromLabels(c.Annotations(), c.PrimaryAddress())
		}

		labels := map[string]string{
			types.LabelMetaScrapeJob: d.DynamicJobName,
		}

		ns := c.PodNamespace()
		podName := c.PodName()

		if podName != "" {
			labels["kubernetes.pod.namespace"] = ns
			labels["kubernetes.pod.name"] = podName
		} else {
			labels[types.LabelContainerName] = c.ContainerName()
		}

		if u != "" {
			result = append(result, target{
				URL:         u,
				ExtraLabels: labels,
			})
		}
	}

	return result
}

func urlFromLabels(labels map[string]string, address string) string {
	if strings.ToLower(labels["prometheus.io/scrape"]) != "true" {
		return ""
	}

	path := labels["prometheus.io/path"]
	if path == "" {
		path = "/metrics"
	}

	portStr := labels["prometheus.io/port"]
	if portStr == "" {
		portStr = "9102"
	}

	port, err := strconv.ParseInt(portStr, 10, 0)
	if err != nil {
		return ""
	}

	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	return fmt.Sprintf("http://%s:%d%s", address, port, path)
}

// DynamicScrapper is a Prometheus scrapper that will update its target based on ListExporters.
type DynamicScrapper struct {
	l                sync.Mutex
	registeredID     map[string]int
	registeredLabels map[string]map[string]string
	DynamicJobName   string
	Registry         *registry.Registry
}

// Update updates the scrappers targets using new containers informations.
func (d *DynamicScrapper) Update(containers []facts.Container) {
	d.l.Lock()
	defer d.l.Unlock()

	d.update(containers)
}

func (d *DynamicScrapper) update(containers []facts.Container) {
	dynamicTargets := d.listExporters(containers)

	logger.V(3).Printf("Found the following dynamic Prometheus exporter: %v", dynamicTargets)

	currentURLs := make(map[string]bool, len(dynamicTargets))

	for _, t := range dynamicTargets {
		currentURLs[t.URL] = true

		if labels, ok := d.registeredLabels[t.URL]; ok && reflect.DeepEqual(labels, t.ExtraLabels) {
			continue
		}

		if id, ok := d.registeredID[t.URL]; ok {
			d.Registry.UnregisterGatherer(id)
			delete(d.registeredID, t.URL)
			delete(d.registeredLabels, t.URL)
		}

		u, err := url.Parse(t.URL)
		if err != nil {
			logger.Printf("ignoring invalid URL %v: %v", t.URL, err)
			continue
		}

		target := (*scrapper.Target)(u)

		id, err := d.Registry.RegisterGatherer(target, nil, t.ExtraLabels)
		if err != nil {
			logger.Printf("Failed to register scrapper for %v: %v", t.URL, err)
			continue
		}

		if d.registeredID == nil {
			d.registeredID = make(map[string]int)
			d.registeredLabels = make(map[string]map[string]string)
		}

		d.registeredID[t.URL] = id
		d.registeredLabels[t.URL] = t.ExtraLabels
	}

	for u, id := range d.registeredID {
		if currentURLs[u] {
			continue
		}

		d.Registry.UnregisterGatherer(id)
		delete(d.registeredID, u)
		delete(d.registeredLabels, u)
	}
}
