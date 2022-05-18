package model

import (
	"errors"
	"glouton/logger"
	"glouton/types"
	"sort"
	"strings"
	"time"

	"github.com/gogo/protobuf/proto"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql"
	"github.com/prometheus/prometheus/storage"
)

var (
	errEmptySamples    = errors.New("samples list is empty")
	errInvalidSample   = errors.New("sample is invalid")
	errUnsupportedType = errors.New("unsupported metric type")
)

func FamiliesToMetricPoints(now time.Time, families []*dto.MetricFamily) []types.MetricPoint {
	samples, err := expfmt.ExtractSamples(
		&expfmt.DecodeOptions{Timestamp: model.TimeFromUnixNano(now.UnixNano())},
		families...,
	)
	if err != nil {
		logger.Printf("Conversion of metrics failed, some metrics may be missing: %v", err)
	}

	result := make([]types.MetricPoint, len(samples))

	for i, sample := range samples {
		builder := labels.NewBuilder(nil)

		for k, v := range sample.Metric {
			builder.Set(string(k), string(v))
		}

		lbls := builder.Labels()
		annotations := MetaLabelsToAnnotation(lbls)
		lbls = DropMetaLabels(lbls)

		result[i] = types.MetricPoint{
			Labels:      lbls.Map(),
			Annotations: annotations,
			Point: types.Point{
				Time:  sample.Timestamp.Time(),
				Value: float64(sample.Value),
			},
		}
	}

	return result
}

func MetricPointsToFamilies(points []types.MetricPoint) []*dto.MetricFamily {
	families := []*dto.MetricFamily{}
	indexByName := make(map[string]int)

	for _, p := range points {
		name := p.Labels[types.LabelName]
		idx, exists := indexByName[name]

		if !exists {
			tmp := &dto.MetricFamily{
				Name: proto.String(name),
				Help: proto.String(""),
				Type: dto.MetricType_UNTYPED.Enum(),
			}
			idx = len(families)
			indexByName[name] = idx

			families = append(families, tmp)
		}

		metric := &dto.Metric{
			Label:       make([]*dto.LabelPair, 0, len(p.Labels)-1),
			TimestampMs: proto.Int64(p.Time.UnixMilli()),
			Untyped: &dto.Untyped{
				Value: proto.Float64(p.Value),
			},
		}

		for k, v := range p.Labels {
			if k == types.LabelName {
				continue
			}

			metric.Label = append(metric.Label, &dto.LabelPair{
				Name:  proto.String(k),
				Value: proto.String(v),
			})
		}

		sort.Slice(metric.Label, func(i, j int) bool {
			return metric.Label[i].GetName() < metric.Label[j].GetName()
		})

		families[idx].Metric = append(families[idx].Metric, metric)
	}

	sort.Slice(families, func(i, j int) bool {
		return families[i].GetName() < families[j].GetName()
	})

	for _, fam := range families {
		sort.Slice(fam.Metric, func(i, j int) bool {
			builder := labels.NewBuilder(nil)

			for _, pair := range fam.Metric[i].Label {
				builder.Set(pair.GetName(), pair.GetValue())
			}

			lblsA := builder.Labels()

			builder.Reset(nil)
			for _, pair := range fam.Metric[j].Label {
				builder.Set(pair.GetName(), pair.GetValue())
			}

			lblsB := builder.Labels()

			return labels.Compare(lblsA, lblsB) < 0
		})
	}

	return families
}

// DropMetaLabels delete all labels which start with __ (with exception to __name__).
func DropMetaLabels(lbls labels.Labels) labels.Labels {
	i := 0

	for _, l := range lbls {
		if l.Name != types.LabelName && strings.HasPrefix(l.Name, model.ReservedLabelPrefix) {
			continue
		}

		if l.Value == "" {
			continue
		}

		lbls[i] = l
		i++
	}

	return lbls[:i]
}

// SamplesToMetricFamily convert a list of sample to a MetricFamilty of given type.
// The mType could be nil which will use the default of MetricType_UNTYPED.
// All samples must belong to the same family, that is have the same name.
func SamplesToMetricFamily(samples []promql.Sample, mType *dto.MetricType) (*dto.MetricFamily, error) {
	if mType == nil {
		mType = dto.MetricType_UNTYPED.Enum()
	}

	if len(samples) == 0 {
		return nil, errEmptySamples
	}

	mf := &dto.MetricFamily{
		Name:   proto.String(samples[0].Metric.Get(types.LabelName)),
		Type:   mType,
		Help:   proto.String(""),
		Metric: make([]*dto.Metric, 0, len(samples)),
	}

	for _, pt := range samples {
		if len(pt.Metric) == 0 {
			return nil, errInvalidSample
		}

		metric := &dto.Metric{
			Label:       make([]*dto.LabelPair, 0, len(pt.Metric)-1),
			TimestampMs: proto.Int64(pt.T),
		}

		for _, l := range pt.Metric {
			if l.Name == types.LabelName {
				continue
			}

			metric.Label = append(metric.Label, &dto.LabelPair{
				Name:  proto.String(l.Name),
				Value: proto.String(l.Value),
			})
		}

		switch mType.String() {
		case dto.MetricType_COUNTER.Enum().String():
			metric.Counter = &dto.Counter{Value: proto.Float64(pt.V)}
		case dto.MetricType_GAUGE.Enum().String():
			metric.Gauge = &dto.Gauge{Value: proto.Float64(pt.V)}
		case dto.MetricType_UNTYPED.Enum().String():
			metric.Untyped = &dto.Untyped{Value: proto.Float64(pt.V)}
		default:
			return nil, errUnsupportedType
		}

		mf.Metric = append(mf.Metric, metric)
	}

	return mf, nil
}

// SendPointsToAppender append all points to given appender. It will mutate points's labels
// to include some meta-labels known by Registry for some annotation.
// This method will not Commit or Rollback on the Appender.
func SendPointsToAppender(points []types.MetricPoint, app storage.Appender) error {
	for _, pts := range points {
		promLabels := AnnotationToMetaLabels(labels.FromMap(pts.Labels), pts.Annotations)

		_, err := app.Append(0, promLabels, pts.Time.UnixMilli(), pts.Value)
		if err != nil {
			return err
		}
	}

	return nil
}

// AnnotationToMetaLabels convert annotation to meta-labels (labels starting with __) and append them to existing labels.
// It's valid to provide nil for initial labels.
// Currently not all annotation are converted. List of converted annotation may change.
func AnnotationToMetaLabels(lbls labels.Labels, annotation types.MetricAnnotations) labels.Labels {
	builder := labels.NewBuilder(lbls)

	if annotation.ServiceName != "" {
		builder.Set(types.LabelMetaServiceName, annotation.ServiceName)
	}

	if annotation.ContainerID != "" {
		builder.Set(types.LabelMetaContainerID, annotation.ContainerID)
	}

	if annotation.BleemeoAgentID != "" {
		builder.Set(types.LabelMetaBleemeoTargetAgentUUID, annotation.BleemeoAgentID)
	}

	if annotation.SNMPTarget != "" {
		builder.Set(types.LabelMetaSNMPTarget, annotation.SNMPTarget)
	}

	if annotation.AlertingRuleID != "" {
		builder.Set(types.LabelMetaAlertingRuleUUID, annotation.AlertingRuleID)
	}

	if annotation.Status.CurrentStatus.IsSet() {
		builder.Set(types.LabelMetaCurrentStatus, annotation.Status.CurrentStatus.String())
		builder.Set(types.LabelMetaCurrentDescription, annotation.Status.StatusDescription)
	}

	return builder.Labels()
}

// MetaLabelsToAnnotation extract from meta-labels some annotations. It mostly does the opposit of AnnotationToMetaLabels.
// Labels aren't modified.
func MetaLabelsToAnnotation(lbls labels.Labels) types.MetricAnnotations {
	annotations := types.MetricAnnotations{
		ServiceName:    lbls.Get(types.LabelMetaServiceName),
		ContainerID:    lbls.Get(types.LabelMetaContainerID),
		BleemeoAgentID: lbls.Get(types.LabelMetaBleemeoTargetAgentUUID),
		SNMPTarget:     lbls.Get(types.LabelMetaSNMPTarget),
		AlertingRuleID: lbls.Get(types.LabelMetaAlertingRuleUUID),
	}

	if statusText := lbls.Get(types.LabelMetaCurrentStatus); statusText != "" {
		annotations.Status.CurrentStatus = types.FromString(statusText)
		annotations.Status.StatusDescription = lbls.Get(types.LabelMetaCurrentDescription)
	}

	return annotations
}

func DTO2Labels(name string, input *dto.Metric) map[string]string {
	lbls := make(map[string]string, len(input.Label)+1)
	for _, lp := range input.Label {
		lbls[*lp.Name] = *lp.Value
	}

	lbls["__name__"] = name

	return lbls
}

// FixType changes the type of a metric.
// Some information may be lost in the process.
func FixType(m *dto.Metric, wantType dto.MetricType) *dto.Metric {
	var (
		value   *float64
		gotType dto.MetricType
	)

	switch {
	case m.Counter != nil:
		value = m.Counter.Value
		gotType = dto.MetricType_COUNTER
	case m.Gauge != nil:
		value = m.Gauge.Value
		gotType = dto.MetricType_GAUGE
	case m.Histogram != nil:
		value = m.Histogram.SampleSum
		gotType = dto.MetricType_HISTOGRAM
	case m.Summary != nil:
		value = m.Summary.SampleSum
		gotType = dto.MetricType_SUMMARY
	case m.Untyped != nil:
		value = m.Untyped.Value
		gotType = dto.MetricType_UNTYPED
	}

	if gotType == wantType {
		return m
	}

	switch wantType {
	case dto.MetricType_COUNTER:
		m.Counter = &dto.Counter{Value: value}
	case dto.MetricType_GAUGE:
		m.Gauge = &dto.Gauge{Value: value}
	case dto.MetricType_HISTOGRAM:
		m.Histogram = &dto.Histogram{SampleCount: proto.Uint64(1), SampleSum: value}
	case dto.MetricType_SUMMARY:
		m.Summary = &dto.Summary{SampleCount: proto.Uint64(1), SampleSum: value}
	case dto.MetricType_UNTYPED:
		m.Untyped = &dto.Untyped{Value: value}
	}

	switch gotType {
	case dto.MetricType_COUNTER:
		m.Counter = nil
	case dto.MetricType_GAUGE:
		m.Gauge = nil
	case dto.MetricType_HISTOGRAM:
		m.Histogram = nil
	case dto.MetricType_SUMMARY:
		m.Summary = nil
	case dto.MetricType_UNTYPED:
		m.Untyped = nil
	}

	return m
}
