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

// listExporters return list of exporters based on containers labels/annotations.
func (d *DynamicScrapper) listExporters(containers []facts.Container) []*scrapper.Target {
	result := make([]*scrapper.Target, 0)

	for _, c := range containers {
		u := urlFromLabels(c.Labels(), c.PrimaryAddress())

		if u == "" {
			u = urlFromLabels(c.Annotations(), c.PrimaryAddress())
		}

		if u == "" {
			continue
		}

		tmp, err := url.Parse(u)
		if err != nil {
			logger.Printf("ignoring invalid URL %v: %v", u, err)
			continue
		}

		labels := map[string]string{
			types.LabelMetaScrapeJob:      d.DynamicJobName,
			types.LabelMetaScrapeInstance: scrapper.HostPort(tmp),
		}

		ns := c.PodNamespace()
		podName := c.PodName()

		if podName != "" {
			labels["kubernetes.pod.namespace"] = ns
			labels["kubernetes.pod.name"] = podName
		} else {
			labels[types.LabelContainerName] = c.ContainerName()
		}

		cLabelsAnnotations := facts.LabelsAndAnnotations(c)

		target := &scrapper.Target{
			URL:             tmp,
			ExtraLabels:     labels,
			ContainerLabels: cLabelsAnnotations,
		}
		result = append(result, target)
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
	containersLabels map[string]map[string]string
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
		currentURLs[t.URL.String()] = true

		if labels, ok := d.registeredLabels[t.URL.String()]; ok && reflect.DeepEqual(labels, t.ExtraLabels) {
			continue
		}

		if id, ok := d.registeredID[t.URL.String()]; ok {
			d.Registry.UnregisterGatherer(id)
			delete(d.registeredID, t.URL.String())
			delete(d.registeredLabels, t.URL.String())
		}

		id, err := d.Registry.RegisterGatherer(t, nil, t.ExtraLabels, true)
		if err != nil {
			logger.Printf("Failed to register scrapper for %v: %v", t.URL, err)
			continue
		}

		if d.registeredID == nil {
			d.registeredID = make(map[string]int)
			d.registeredLabels = make(map[string]map[string]string)
			d.containersLabels = make(map[string]map[string]string)
		}

		d.registeredID[t.URL.String()] = id
		d.registeredLabels[t.URL.String()] = t.ExtraLabels
		d.containersLabels[t.URL.String()] = t.ContainerLabels
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

//GetRegisteredLabels returns a copy of registeredLabels.
func (d *DynamicScrapper) GetRegisteredLabels() map[string]map[string]string {
	return copyMap(d.registeredLabels)
}

//GetContainersLabels returns a copy of containersLabels.
func (d *DynamicScrapper) GetContainersLabels() map[string]map[string]string {
	return copyMap(d.containersLabels)
}

func copyMap(toCopy map[string]map[string]string) map[string]map[string]string {
	copyMap := make(map[string]map[string]string)

	for key, value := range toCopy {
		new := make(map[string]string)
		for key2, value2 := range value {
			new[key2] = value2
		}

		copyMap[key] = new
	}

	return copyMap
}
