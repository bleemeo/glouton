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

package model

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/types"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql"
	"github.com/prometheus/prometheus/storage"
	"google.golang.org/protobuf/proto"
)

var (
	errEmptySamples    = errors.New("samples list is empty")
	errInvalidSample   = errors.New("sample is invalid")
	errUnsupportedType = errors.New("unsupported metric type")
)

func FamiliesToMetricPoints(
	defaultTS time.Time,
	families []*dto.MetricFamily,
	dropMetaLabels bool,
) []types.MetricPoint {
	samples, err := expfmt.ExtractSamples(
		&expfmt.DecodeOptions{Timestamp: model.Time(defaultTS.UnixMilli())},
		families...,
	)
	if err != nil {
		logger.Printf("Conversion of metrics failed, some metrics may be missing: %v", err)
	}

	result := make([]types.MetricPoint, len(samples))

	for i, sample := range samples {
		builder := labels.NewBuilder(labels.EmptyLabels())

		for k, v := range sample.Metric {
			builder.Set(string(k), string(v))
		}

		lbls := builder.Labels()
		annotations := MetaLabelsToAnnotation(lbls)

		if dropMetaLabels {
			lbls = DropMetaLabels(lbls)
		}

		ts := sample.Timestamp.Time()
		if sample.Timestamp == 0 {
			ts = defaultTS
		}

		result[i] = types.MetricPoint{
			Labels:      lbls.Map(),
			Annotations: annotations,
			Point: types.Point{
				Time:  ts,
				Value: float64(sample.Value),
			},
		}
	}

	return result
}

type fixedCollector struct {
	Metrics []prometheus.Metric
}

func (c fixedCollector) Collect(ch chan<- prometheus.Metric) {
	for _, m := range c.Metrics {
		ch <- m
	}
}

func (c fixedCollector) Describe(chan<- *prometheus.Desc) {
}

func CollectorToFamilies(metrics []prometheus.Metric) ([]*dto.MetricFamily, error) {
	gatherer := prometheus.NewRegistry()
	if err := gatherer.Register(fixedCollector{Metrics: metrics}); err != nil {
		return nil, err
	}

	return gatherer.Gather()
}

// FamiliesToCollector convert metric family to prometheus.Metric.
// Note: meta-label are not kept in this conversion.
func FamiliesToCollector(families []*dto.MetricFamily) ([]prometheus.Metric, error) {
	var errs prometheus.MultiError

	result := make([]prometheus.Metric, 0)

	for _, mf := range families {
		metrics := mf.GetMetric()
		if len(metrics) == 0 {
			continue
		}

		for _, metric := range metrics {
			labels := make([]string, 0, len(metric.GetLabel()))
			labelsValues := make([]string, 0, len(metric.GetLabel()))

			// we assume labels to be unique
			for _, labelPair := range metric.GetLabel() {
				labels = append(labels, labelPair.GetName())
				labelsValues = append(labelsValues, labelPair.GetValue())
			}

			desc := prometheus.NewDesc(
				prometheus.BuildFQName("", "", mf.GetName()),
				mf.GetHelp(),
				labels,
				nil,
			)

			switch {
			case metric.GetCounter() != nil:
				result = append(result, prometheus.MustNewConstMetric(desc, prometheus.CounterValue, metric.GetCounter().GetValue(), labelsValues...))
			case metric.GetGauge() != nil:
				result = append(result, prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, metric.GetGauge().GetValue(), labelsValues...))
			default:
				errs = append(errs, fmt.Errorf("%w: got %v", errUnsupportedType, metric))
			}
		}
	}

	return result, errs.MaybeUnwrap()
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

		lbls := AnnotationToMetaLabels(labels.FromMap(p.Labels), p.Annotations)

		ts := proto.Int64(p.Time.UnixMilli())
		if p.Time.IsZero() || p.Time.UnixMilli() == 0 {
			ts = nil
		}

		labelsCount := lbls.Len()
		if labelsCount == 0 {
			// This shouldn't happen, but it happened once on a TrueNAS with Glouton version 23.06.30.134546
			continue
		}

		metric := &dto.Metric{
			Label:       make([]*dto.LabelPair, 0, labelsCount-1),
			TimestampMs: ts,
			Untyped: &dto.Untyped{
				Value: proto.Float64(p.Value),
			},
		}

		lbls.Range(func(l labels.Label) {
			if l.Name == types.LabelName {
				return
			}

			metric.Label = append(metric.GetLabel(), &dto.LabelPair{
				Name:  proto.String(l.Name),
				Value: proto.String(l.Value),
			})
		})

		sort.Slice(metric.GetLabel(), func(i, j int) bool {
			return metric.GetLabel()[i].GetName() < metric.GetLabel()[j].GetName()
		})

		families[idx].Metric = append(families[idx].GetMetric(), metric)
	}

	sort.Slice(families, func(i, j int) bool {
		return families[i].GetName() < families[j].GetName()
	})

	for _, fam := range families {
		sort.Slice(fam.GetMetric(), func(i, j int) bool {
			builder := labels.NewBuilder(labels.EmptyLabels())

			for _, pair := range fam.GetMetric()[i].GetLabel() {
				builder.Set(pair.GetName(), pair.GetValue())
			}

			lblsA := builder.Labels()

			builder.Reset(labels.EmptyLabels())

			for _, pair := range fam.GetMetric()[j].GetLabel() {
				builder.Set(pair.GetName(), pair.GetValue())
			}

			lblsB := builder.Labels()

			return labels.Compare(lblsA, lblsB) < 0
		})
	}

	return families
}

// DropMetaLabels delete all labels which start with __ (with exception to __name__).
// The input is modified.
func DropMetaLabels(lbls labels.Labels) labels.Labels {
	finalLbls := make([]labels.Label, 0, lbls.Len())

	lbls.Range(func(l labels.Label) {
		if l.Name != types.LabelName && strings.HasPrefix(l.Name, model.ReservedLabelPrefix) {
			return
		}

		if l.Value == "" {
			return
		}

		finalLbls = append(finalLbls, l)
	})

	return labels.New(finalLbls...)
}

// DropMetaLabelsFromFamilies delete all labels which start with __ (with exception to __name__).
// The input is modified.
func DropMetaLabelsFromFamilies(families []*dto.MetricFamily) {
	for _, mf := range families {
		for _, m := range mf.GetMetric() {
			m.Label = dropMetaLabelsFromPair(m.GetLabel())
		}
	}
}

func dropMetaLabelsFromPair(lbls []*dto.LabelPair) []*dto.LabelPair {
	i := 0

	for _, l := range lbls {
		if l.GetName() != types.LabelName && strings.HasPrefix(l.GetName(), model.ReservedLabelPrefix) {
			continue
		}

		if l.GetValue() == "" {
			continue
		}

		lbls[i] = l
		i++
	}

	return lbls[:i]
}

func FamiliesDeepCopy(families []*dto.MetricFamily) []*dto.MetricFamily {
	result := make([]*dto.MetricFamily, len(families))

	for i, mf := range families {
		b, err := proto.Marshal(mf)
		if err != nil {
			panic(err)
		}

		var tmp dto.MetricFamily

		err = proto.Unmarshal(b, &tmp)
		if err != nil {
			panic(err)
		}

		result[i] = &tmp
	}

	return result
}

// FamiliesToNameAndItem converts labels of each metrics to just name + item. It kept
// meta-label unchanged.
// The input is modified.
func FamiliesToNameAndItem(families []*dto.MetricFamily) {
	for _, mf := range families {
		for _, m := range mf.GetMetric() {
			m.Label = itemAndMetaLabel(m.GetLabel())
		}
	}
}

func itemAndMetaLabel(lbls []*dto.LabelPair) []*dto.LabelPair {
	i := 0

	for _, l := range lbls {
		if !strings.HasPrefix(l.GetName(), model.ReservedLabelPrefix) && l.GetName() != types.LabelItem {
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
		labelsCount := pt.Metric.Len()
		if labelsCount == 0 {
			return nil, errInvalidSample
		}

		ts := proto.Int64(pt.T)
		if pt.T == 0 {
			ts = nil
		}

		metric := &dto.Metric{
			Label:       make([]*dto.LabelPair, 0, labelsCount-1),
			TimestampMs: ts,
		}

		metric.Label = Labels2DTO(pt.Metric)

		switch mType.String() {
		case dto.MetricType_COUNTER.Enum().String():
			metric.Counter = &dto.Counter{Value: proto.Float64(pt.F)}
		case dto.MetricType_GAUGE.Enum().String():
			metric.Gauge = &dto.Gauge{Value: proto.Float64(pt.F)}
		case dto.MetricType_UNTYPED.Enum().String():
			metric.Untyped = &dto.Untyped{Value: proto.Float64(pt.F)}
		default:
			return nil, errUnsupportedType
		}

		mf.Metric = append(mf.GetMetric(), metric)
	}

	return mf, nil
}

// SendPointsToAppender append all points to given appender. It will mutate points's labels
// to include some meta-labels known by Registry for some annotation.
// This method will not Commit or Rollback on the Appender.
func SendPointsToAppender(points []types.MetricPoint, app storage.Appender) error {
	for _, pts := range points {
		promLabels := AnnotationToMetaLabels(labels.FromMap(pts.Labels), pts.Annotations)

		ts := pts.Time.UnixMilli()
		if pts.Time.IsZero() {
			ts = 0
		}

		_, err := app.Append(0, promLabels, ts, pts.Value)
		if err != nil {
			return err
		}
	}

	return nil
}

// AnnotationToMetaLabels convert annotation to meta-labels (labels starting with __) and append them to existing labels.
// It's valid to provide nil for initial labels.
func AnnotationToMetaLabels(lbls labels.Labels, annotation types.MetricAnnotations) labels.Labels {
	builder := labels.NewBuilder(lbls)

	if annotation.ServiceName != "" {
		builder.Set(types.LabelMetaServiceName, annotation.ServiceName)
	}

	if annotation.ServiceInstance != "" {
		builder.Set(types.LabelMetaServiceInstance, annotation.ServiceInstance)
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

	if annotation.StatusOf != "" {
		builder.Set(types.LabelMetaStatusOf, annotation.StatusOf)
	}

	if annotation.Status.CurrentStatus.IsSet() {
		builder.Set(types.LabelMetaCurrentStatus, annotation.Status.CurrentStatus.String())
		builder.Set(types.LabelMetaCurrentDescription, annotation.Status.StatusDescription)
	}

	return builder.Labels()
}

// MetaLabelsToAnnotation extract from meta-labels some annotations. It mostly does the opposite of AnnotationToMetaLabels.
// Labels aren't modified.
func MetaLabelsToAnnotation(lbls labels.Labels) types.MetricAnnotations {
	annotations := types.MetricAnnotations{
		ServiceName:     lbls.Get(types.LabelMetaServiceName),
		ServiceInstance: lbls.Get(types.LabelMetaServiceInstance),
		ContainerID:     lbls.Get(types.LabelMetaContainerID),
		BleemeoAgentID:  lbls.Get(types.LabelMetaBleemeoTargetAgentUUID),
		SNMPTarget:      lbls.Get(types.LabelMetaSNMPTarget),
		StatusOf:        lbls.Get(types.LabelMetaStatusOf),
	}

	if statusText := lbls.Get(types.LabelMetaCurrentStatus); statusText != "" {
		annotations.Status.CurrentStatus = types.FromString(statusText)
		annotations.Status.StatusDescription = lbls.Get(types.LabelMetaCurrentDescription)
	}

	return annotations
}

func DTO2Labels(name string, input []*dto.LabelPair) map[string]string {
	lbls := make(map[string]string, len(input)+1)
	for _, lp := range input {
		lbls[lp.GetName()] = lp.GetValue()
	}

	lbls["__name__"] = name

	return lbls
}

func Labels2DTO(lbls labels.Labels) []*dto.LabelPair {
	labelsCount := lbls.Len()
	if labelsCount == 0 {
		return nil
	}

	result := make([]*dto.LabelPair, 0, labelsCount-1)

	lbls.Range(func(l labels.Label) {
		if l.Name == types.LabelName {
			return
		}

		result = append(result, &dto.LabelPair{
			Name:  proto.String(l.Name),
			Value: proto.String(l.Value),
		})
	})

	return result
}

// FamilyConvertType convert a MetricFamilty to another type.
// Some information may be lost in the process.
func FamilyConvertType(mf *dto.MetricFamily, targetType dto.MetricType) {
	mf.Type = &targetType
	for _, metric := range mf.GetMetric() {
		FixType(metric, targetType)
	}
}

// FixType changes the type of a metric.
// Some information may be lost in the process.
func FixType(m *dto.Metric, wantType dto.MetricType) {
	var (
		value   *float64
		gotType dto.MetricType
	)

	switch {
	case m.GetCounter() != nil:
		value = proto.Float64(m.GetCounter().GetValue())
		gotType = dto.MetricType_COUNTER
	case m.GetGauge() != nil:
		value = proto.Float64(m.GetGauge().GetValue())
		gotType = dto.MetricType_GAUGE
	case m.GetHistogram() != nil:
		value = proto.Float64(m.GetHistogram().GetSampleSum())
		gotType = dto.MetricType_HISTOGRAM
	case m.GetSummary() != nil:
		value = proto.Float64(m.GetSummary().GetSampleSum())
		gotType = dto.MetricType_SUMMARY
	case m.GetUntyped() != nil:
		value = proto.Float64(m.GetUntyped().GetValue())
		gotType = dto.MetricType_UNTYPED
	}

	if gotType == wantType {
		return
	}

	switch wantType {
	case dto.MetricType_COUNTER:
		m.Counter = &dto.Counter{Value: value}
	case dto.MetricType_GAUGE:
		m.Gauge = &dto.Gauge{Value: value}
	case dto.MetricType_HISTOGRAM, dto.MetricType_GAUGE_HISTOGRAM:
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
	case dto.MetricType_HISTOGRAM, dto.MetricType_GAUGE_HISTOGRAM:
		m.Histogram = nil
	case dto.MetricType_SUMMARY:
		m.Summary = nil
	case dto.MetricType_UNTYPED:
		m.Untyped = nil
	}
}
