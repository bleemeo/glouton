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

package types

import (
	"glouton/logger"
	"sort"
	"strings"
	"time"

	"github.com/prometheus/prometheus/promql/parser"
)

// Status is an enumeration of status (ok, warning, critical, unknown).
type Status uint8

// Possible values for the StatusValue enum.
const (
	StatusUnset Status = iota
	StatusOk
	StatusWarning
	StatusCritical
	StatusUnknown
)

// MetricFormat specify the metric format used.
type MetricFormat int

// List of known metrics format.
// Currently only Bleemeo and Prometheus are supported.
// The Bleemeo format is the initial format supported. It provide fewer metrics
// which are usually directly queriable (e.g. disk_used_perc instead of a free bytes and total bytes)
// The Prometheus format use same format as node_exporter and try to be as close as Prometheus way.
const (
	MetricFormatUnknown MetricFormat = iota
	MetricFormatBleemeo
	MetricFormatPrometheus
)

//nolint:gochecknoglobals
var quoter = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)

// StringToMetricFormat convert a string to a MetricFormat. Return MetricFormatUnknown if input is invalid.
func StringToMetricFormat(input string) MetricFormat {
	switch strings.ToLower(input) {
	case "bleemeo":
		return MetricFormatBleemeo
	case "prometheus":
		return MetricFormatPrometheus
	default:
		return MetricFormatUnknown
	}
}

func (f MetricFormat) String() string {
	switch f {
	case MetricFormatBleemeo:
		return "Bleemeo"
	case MetricFormatPrometheus:
		return "Prometheus"
	default:
		return "unknown"
	}
}

// List of label names that some part of Glouton will assume to be named
// as such.
// Using constant here allow to change their name only here.
// LabelName constants is duplicated in JavaScript file.
const (
	LabelName = "__name__"

	// Label starting with "__" are dropped after collections and are only accessible internally (e.g. not present on /metrics, on Bleemeo Cloud or in the local store)
	// They are actually dropped by the metric registry and/or the
	LabelMetaContainerName    = "__meta_container_name"
	LabelMetaContainerID      = "__meta_container_id"
	LabelMetaServiceName      = "__meta_service_name"
	LabelMetaGloutonFQDN      = "__meta__fqdn"
	LabelMetaGloutonPort      = "__meta_glouton_port"
	LabelMetaServicePort      = "__meta_service_port"
	LabelMetaPort             = "__meta_port"
	LabelMetaScrapeInstance   = "__meta_scrape_instance"
	LabelMetaScrapeJob        = "__meta_scrape_job"
	LabelMetaBleemeoUUID      = "__meta_bleemeo_uuid"
	LabelMetaProbeTarget      = "__meta_probe_target"
	LabelMetaProbeServiceUUID = "__meta_probe_service_uuid"
	LabelMetaProbeAgentUUID   = "__meta_probe_agent_uuid"
	LabelMetaProbeScraperName = "__meta_probe_scraper_name"
	LabelMetaSendScraperUUID  = "__meta_probe_send_agent_uuid"
	LabelInstanceUUID         = "instance_uuid"
	LabelItem                 = "item"
	LabelScraperUUID          = "scraper_uuid"
	LabelScraper              = "scraper"
	LabelSNMPTarget           = "snmp_target"
	LabelInstance             = "instance"
	LabelContainerName        = "container_name"
	LabelScrapeJob            = "scrape_job"
	LabelScrapeInstance       = "scrape_instance"
)

// IsSet return true if the status is set.
func (s Status) IsSet() bool {
	return s != StatusUnset
}

func (s Status) String() string {
	switch s {
	case StatusUnset:
		return "unset"
	case StatusOk:
		return "ok"
	case StatusWarning:
		return "warning"
	case StatusCritical:
		return "critical"
	default:
		return "unknown"
	}
}

// NagiosCode return the Nagios value for a Status.
func (s Status) NagiosCode() int {
	switch s {
	case StatusOk:
		return 0
	case StatusWarning:
		return 1
	case StatusCritical:
		return 2
	default:
		return 3
	}
}

// FromNagios return a Status from a Nagios status code.
func FromNagios(value int) Status {
	switch value {
	case 0:
		return StatusOk
	case 1:
		return StatusWarning
	case 2:
		return StatusCritical
	default:
		return StatusUnknown
	}
}

// Metric represent a metric object.
type Metric interface {
	// Labels returns labels of the metric. A metric is identified by its labels
	Labels() map[string]string

	// Annotations of this metric. A annotation is similar to a label but do not participate
	// in the metric identification and may change.
	Annotations() MetricAnnotations

	// Points returns points between the two given time range (boundary are included).
	Points(start, end time.Time) ([]Point, error)
}

// MetricAnnotations contains additional information about a metrics.
type MetricAnnotations struct {
	BleemeoItem string
	ContainerID string
	ServiceName string
	StatusOf    string
	// store the agent for which we want to emit the metric
	BleemeoAgentID string
	Status         StatusDescription
}

// Point is the value of one metric at a given time.
type Point struct {
	Time  time.Time
	Value float64
}

// MetricPoint is one point for one metrics (identified by labels) with its annotation at the time of emission.
type MetricPoint struct {
	Point
	Labels      map[string]string
	Annotations MetricAnnotations
}

// PointPusher push new points. Points must not be mutated after call.
type PointPusher interface {
	PushPoints(points []MetricPoint)
}

// StatusDescription store a service/metric status with an optional description.
type StatusDescription struct {
	CurrentStatus     Status
	StatusDescription string
}

// LabelsToText return a text version of a labels set
// The text representation has a one-to-one relation with labels set.
// It does because:
// * labels are sorted by label name
// * labels values are quoted
//
// Result looks like __name__="node_cpu_seconds_total",cpu="0",mode="idle".
func LabelsToText(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}

	labelNames := make([]string, 0, len(labels))
	for k := range labels {
		labelNames = append(labelNames, k)
	}

	sort.Strings(labelNames)

	strLabels := make([]string, 0, len(labels))

	for _, name := range labelNames {
		value := labels[name]
		if value == "" {
			continue
		}

		str := name + "=\"" + quoter.Replace(value) + "\""
		strLabels = append(strLabels, str)
	}

	str := strings.Join(strLabels, ",")

	return str
}

// TextToLabels is the reverse of LabelsToText.
func TextToLabels(text string) map[string]string {
	labels, err := parser.ParseMetricSelector("{" + text + "}")
	if err != nil {
		logger.Printf("unable to decode labels %#v: %v", text, err)
		return nil
	}

	results := make(map[string]string, len(labels))
	for _, v := range labels {
		results[v.Name] = v.Value
	}

	return results
}

//Monitor represents a monitor instance.
type Monitor struct {
	ID                      string
	MetricMonitorResolution int
	CreationDate            string
	URL                     string
	BleemeoAgentID          string
	ExpectedContent         string
	ExpectedResponseCode    int
	ForbiddenContent        string
}
