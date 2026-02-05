// Copyright 2015-2025 Bleemeo
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
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/bleemeo/glouton/logger"

	paho "github.com/eclipse/paho.mqtt.golang"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/labels"
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

const PrometheusValidationScheme = model.LegacyValidation

const MaxMQTTPayloadSize = 1024 * 1024 // 1MiB

var ErrBackPressureSignal = errors.New("back-pressure signal")

//nolint:gochecknoglobals
var quoter = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)

// List of label names that some part of Glouton will assume to be named
// as such.
// Using constant here allow to change their name only here.
// LabelName constants is duplicated in JavaScript file.
const (
	LabelName = "__name__"

	// ReservedLabelPrefix is a prefix which is not legal in user-supplied label names.
	ReservedLabelPrefix = "__"
	//nolint: godoclint // Label starting with "__" are dropped after collections and are only accessible
	// internally (e.g. not present on /metrics, on Bleemeo Cloud or in the local store),
	// they are dropped by the metric registry.
	// They can be used to know the origin of a metric. Unlike label which don't start by "__",
	// they can only be set by Glouton itself because it's not a valid user defined label.
	LabelMetaContainerName            = "__meta_container_name"
	LabelMetaInstanceUseContainerName = "__meta_instance_use_container_name" // Used to indicate that instance label should use the container name
	LabelMetaContainerID              = "__meta_container_id"
	LabelMetaServiceName              = "__meta_service_name"
	LabelMetaServiceInstance          = "__meta_service_instance"
	LabelMetaServiceUUID              = "__meta_service_uuid"
	LabelMetaGloutonFQDN              = "__meta__fqdn"
	LabelMetaGloutonPort              = "__meta_glouton_port"
	LabelMetaBleemeoRelabelHookOk     = "__meta_bleemeo_hook_ok"
	LabelMetaServicePort              = "__meta_service_port"
	LabelMetaStatusOf                 = "__meta_status_of"
	LabelMetaPort                     = "__meta_port"
	LabelMetaScrapeInstance           = "__meta_scrape_instance"
	LabelMetaScrapeJob                = "__meta_scrape_job"
	LabelMetaSNMPTarget               = "__meta_snmp_target"
	LabelMetaKubernetesCluster        = "__meta_kubernetes_cluster"
	LabelMetaVSphere                  = "__meta_vsphere"
	LabelMetaVSphereMOID              = "__meta_vsphere_moid"
	LabelMetaAgentTypes               = "__meta_agent_types"
	LabelMetaBleemeoTargetAgent       = "__meta_bleemeo_target_agent"
	LabelMetaBleemeoTargetAgentUUID   = "__meta_bleemeo_target_agent_uuid"
	LabelMetaBleemeoUUID              = "__meta_bleemeo_uuid"
	LabelMetaProbeServiceUUID         = "__meta_probe_service_uuid"
	LabelMetaProbeScraperName         = "__meta_probe_scraper_name"
	LabelMetaSendScraperUUID          = "__meta_probe_send_agent_uuid"
	LabelMetaCurrentStatus            = "__meta_current_status"
	LabelMetaCurrentDescription       = "__meta_current_description"
	LabelK8SPODName                   = "kubernetes_pod_name"
	LabelK8SNamespace                 = "kubernetes_namespace"
	LabelInstanceUUID                 = "instance_uuid"
	LabelItem                         = "item"
	LabelScraperUUID                  = "scraper_uuid"
	LabelScraper                      = "scraper"
	LabelSNMPTarget                   = "snmp_target"
	LabelInstance                     = "instance"
	LabelContainerName                = "container_name"
	LabelScrapeJob                    = "scrape_job"
	LabelScrapeInstance               = "scrape_instance"
	LabelService                      = "service"
	LabelServiceInstance              = "service_instance"
	LabelServiceUUID                  = "service_uuid"
	LabelDevice                       = "device"
	LabelModel                        = "model"
	LabelUPSName                      = "ups_name"
	//nolint: godoclint // Kubernetes pods labels.
	LabelState     = "state"
	LabelOwnerKind = "owner_kind"
	LabelOwnerName = "owner_name"
	LabelPodName   = "pod_name"
	LabelNamespace = "namespace"
)

const (
	MetricServiceStatus = "service_status"
)

// MissingContainerID is the container ID annotation set on metrics that belong
// to a container when the container is not known yet.
const MissingContainerID = "missing-container-id"

// IsSet return true if the status is set.
func (s Status) IsSet() bool {
	return s != StatusUnset
}

func (s Status) String() string {
	switch s { //nolint:exhaustive
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

func FromString(s string) Status {
	switch strings.ToLower(s) {
	case "unset":
		return StatusUnset
	case "ok":
		return StatusOk
	case "warning":
		return StatusWarning
	case "critical":
		return StatusCritical
	default:
		return StatusUnknown
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
	case StatusUnknown, StatusUnset:
		return 3
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
	// Labels returns labels of the metric. A metric is identified by its labels.
	// The returned map must not be modified, copy it if you need mutation.
	Labels() map[string]string

	// Annotations of this metric. A annotation is similar to a label but do not participate
	// in the metric identification and may change.
	Annotations() MetricAnnotations

	// Points returns points between the two given time range (boundary are included).
	Points(start, end time.Time) ([]Point, error)

	// LastPointReceivedAt return the last time a point was received
	LastPointReceivedAt() time.Time
}

// MetricAnnotations contains additional information about a metrics.
type MetricAnnotations struct {
	ContainerID     string
	ServiceName     string
	ServiceInstance string
	StatusOf        string
	SNMPTarget      string
	// store the agent for which we want to emit the metric, only set when we don't emit for main agent
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

type LabelsAndAnnotation struct {
	Labels      map[string]string
	Annotations MetricAnnotations
}

// PointPusher push new points. Points must not be mutated after call.
type PointPusher interface {
	PushPoints(ctx context.Context, points []MetricPoint)
}

// StatusDescription store a service/metric status with an optional description.
type StatusDescription struct {
	CurrentStatus     Status
	StatusDescription string
}

// Merge merge two annotations. Annotations from other when set win.
func (a MetricAnnotations) Merge(other MetricAnnotations) MetricAnnotations {
	if other.ContainerID != "" {
		a.ContainerID = other.ContainerID
	}

	if other.ServiceName != "" {
		a.ServiceName = other.ServiceName
	}

	if other.ServiceInstance != "" {
		a.ServiceInstance = other.ServiceInstance
	}

	if other.StatusOf != "" {
		a.StatusOf = other.StatusOf
	}

	if other.SNMPTarget != "" {
		a.SNMPTarget = other.SNMPTarget
	}

	if other.BleemeoAgentID != "" {
		a.BleemeoAgentID = other.BleemeoAgentID
	}

	if other.Status.CurrentStatus.IsSet() {
		a.Status = other.Status
	}

	return a
}

// Changed tells whether two annotation are different or not. Status isn't considered when comparing the annotations.
func (a MetricAnnotations) Changed(other MetricAnnotations) bool {
	return (a.ContainerID != other.ContainerID ||
		a.ServiceName != other.ServiceName ||
		a.ServiceInstance != other.ServiceInstance ||
		a.StatusOf != other.StatusOf ||
		a.SNMPTarget != other.SNMPTarget ||
		a.BleemeoAgentID != other.BleemeoAgentID)
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

// Monitor represents a monitor instance.
type Monitor struct {
	ID                      string
	MetricMonitorResolution time.Duration
	CreationDate            time.Time
	URL                     string
	BleemeoAgentID          string
	ExpectedContent         string
	ExpectedResponseCode    int
	ForbiddenContent        string
	CAFile                  string
	Headers                 map[string]string
}

// MultiErrors is a type containing multiple errors. It implements the error interface.
type MultiErrors []error

func (errs MultiErrors) Error() string {
	list := make([]string, len(errs))

	for i, err := range errs {
		list[i] = err.Error()
	}

	return strings.Join(list, ", ")
}

func (errs MultiErrors) Is(target error) bool {
	for _, err := range errs {
		if errors.Is(err, target) {
			return true
		}
	}

	return false
}

type ArchiveWriter interface {
	Create(filename string) (io.Writer, error)
	CurrentFileName() string
}

type CustomTransportOptions struct {
	// UserAgentHeader will be used as the User-Agent for each HTTP request.
	UserAgentHeader string
	// RequestCounter will be incremented for each HTTP transaction.
	RequestCounter *atomic.Uint32
	EnableLogger   bool
}

type customTransport struct {
	opts      CustomTransportOptions
	transport http.RoundTripper
}

func (tc *customTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r.Header.Set("User-Agent", tc.opts.UserAgentHeader)
	tc.opts.RequestCounter.Add(1)

	if tc.opts.EnableLogger {
		logger.V(2).Printf("Sending API request %v %v", r.Method, r.URL)
	}

	return tc.transport.RoundTrip(r)
}

// NewHTTPTransport returns a default Transport with a modified TLSClientConfig.
// If options are provided, both UserAgentHeader and RequestCounter must be defined.
func NewHTTPTransport(tlsConfig *tls.Config, options *CustomTransportOptions) http.RoundTripper {
	dt, _ := http.DefaultTransport.(*http.Transport)

	t := dt.Clone()
	t.TLSClientConfig = tlsConfig

	if options != nil {
		return &customTransport{
			opts:      *options,
			transport: t,
		}
	}

	return t
}

// MQTTReloadState is the state kept between reloads for MQTT.
type MQTTReloadState interface {
	Client() paho.Client
	SetClient(cli paho.Client)
	OnConnectionLost(cli paho.Client, err error)
	ConnectionLostChannel() <-chan error
	Close()
	AddPendingMessage(ctx context.Context, m Message, shouldWait bool) bool
	PendingMessage(ctx context.Context) (Message, bool)
	PendingMessagesCount() int
}

// Message contains all information to send a message to MQTT.
type Message struct {
	Token   paho.Token
	Retry   bool
	Topic   string
	Payload []byte
}

// SimpleRule is a PromQL run on output from the Gatherer.
// It's similar to a recording rule, but it's not able to use historical data and can
// only works on latest point (so no rate, avg_over_time, ...).
type SimpleRule struct {
	TargetName  string
	PromQLQuery string
}

// Matcher allow to match value.
type Matcher interface {
	Match(item string) bool
}

// MatcherRegexp allow to match value and could return a deny regexp.
type MatcherRegexp interface {
	Matcher
	AsDenyRegexp() string
}

type ReaderWithLen interface {
	io.ReadCloser
	Len() int
}

type DiagnosticFile interface {
	Filename() string
	Reader() (ReaderWithLen, error)
	MarkUploaded() error
}

// DiffMetricPoints return a diff between want and got. Mostly useful in tests.
// Points are sorted before being compared. If approximate is true, float are compared
// with a approximation (compare only 3 first digits).
func DiffMetricPoints(want []MetricPoint, got []MetricPoint, approximate bool) string {
	optMetricSort := cmpopts.SortSlices(func(x MetricPoint, y MetricPoint) bool {
		lblsX := labels.FromMap(x.Labels)
		lblsY := labels.FromMap(y.Labels)

		lblCmp := labels.Compare(lblsX, lblsY)
		if lblCmp != 0 {
			return lblCmp < 0
		}

		// labels are equal, sort by timestamp
		return x.Point.Time.Before(y.Point.Time)
	})

	opts := []cmp.Option{
		optMetricSort,
		cmpopts.EquateEmpty(),
	}

	if approximate {
		opts = append(opts, cmpopts.EquateApprox(0.001, 0))
	}

	return cmp.Diff(want, got, opts...)
}

// DiffMetricFamilies return a diff between want and got. Mostly useful in tests.
func DiffMetricFamilies(want []*dto.MetricFamily, got []*dto.MetricFamily, approximate bool, ignoreFamilyOrder bool) string {
	opts := []cmp.Option{
		cmpopts.IgnoreUnexported(dto.MetricFamily{}),
		cmpopts.IgnoreUnexported(dto.Metric{}),
		cmpopts.IgnoreUnexported(dto.LabelPair{}),
		cmpopts.IgnoreUnexported(dto.Gauge{}),
		cmpopts.IgnoreUnexported(dto.Counter{}),
		cmpopts.IgnoreUnexported(dto.Summary{}),
		cmpopts.IgnoreUnexported(dto.Untyped{}),
		cmpopts.IgnoreUnexported(dto.Histogram{}),
		cmpopts.IgnoreUnexported(dto.Exemplar{}),
		cmpopts.IgnoreUnexported(dto.Quantile{}),
		cmpopts.IgnoreUnexported(dto.Bucket{}),
		cmpopts.IgnoreUnexported(dto.BucketSpan{}),
		cmpopts.EquateEmpty(),
	}

	if ignoreFamilyOrder {
		opts = append(opts, cmpopts.SortSlices(func(x, y *dto.MetricFamily) bool {
			return x.GetName() < y.GetName()
		}))
	}

	if approximate {
		opts = append(opts, cmpopts.EquateApprox(0.001, 0))
	}

	return cmp.Diff(want, got, opts...)
}

type ProcIter interface {
	// Next returns true if the iterator is not exhausted.
	Next() bool
	// Close releases any resources the iterator uses.
	Close() error
	// The interface also have "Proc" interface... but this interface use architecture depedend type
}
