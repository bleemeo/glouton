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
	"context"
	"fmt"
	"glouton/logger"
	"glouton/prometheus/scrapper"
	"strconv"
	"strings"
	"sync"
)

// Container is the type used by exporter discovery.
type Container interface {
	// Labels return the Docker labels (or nil)
	Labels() map[string]string
	// Annoations return the Kubernetes POD annotations (or nil)
	Annotations() map[string]string
	PrimaryAddress() string
	PodNamespaceName() (string, string)
	Name() string
}

// listExporters return list of exporters based on containers labels/annotations.
func (d *DynamicSrapper) listExporters(containers []Container) []scrapper.Target {
	result := make([]scrapper.Target, 0)

	for _, c := range containers {
		u := urlFromLabels(c.Labels(), c.PrimaryAddress())

		if u == "" {
			u = urlFromLabels(c.Annotations(), c.PrimaryAddress())
		}

		labels := make(map[string]string, 1)

		if ns, podName := c.PodNamespaceName(); podName != "" {
			labels["kubernetes.pod.namespace"] = ns
			labels["kubernetes.pod.name"] = podName
		} else {
			labels["container_name"] = c.Name()
		}

		if u != "" {
			result = append(result, scrapper.Target{
				Name:        d.DynamicJobName,
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

// DynamicSrapper is a Prometheus scrapper that will update its target based on ListExporters.
type DynamicSrapper struct {
	scrapper.Scrapper

	l              sync.Mutex
	init           bool
	DynamicJobName string
	StaticTargets  []scrapper.Target
}

// Run start the scrapper.
func (d *DynamicSrapper) Run(ctx context.Context) error {
	d.l.Lock()

	if !d.init {
		d.update(nil)
	}

	d.l.Unlock()

	return d.Scrapper.Run(ctx)
}

// Update updates the scrappers targets using new containers informations.
func (d *DynamicSrapper) Update(containers []Container) {
	d.l.Lock()
	defer d.l.Unlock()

	d.update(containers)
}

func (d *DynamicSrapper) update(containers []Container) {
	d.init = true
	dynamicTargets := d.listExporters(containers)

	logger.V(3).Printf("Found the following dynamic Prometheus exporter: %v", dynamicTargets)

	dynamicTargets = append(dynamicTargets, d.StaticTargets...)
	d.UpdateTargets(dynamicTargets)
}
