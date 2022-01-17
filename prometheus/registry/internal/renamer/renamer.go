package renamer

import (
	"glouton/types"
	"sort"

	"github.com/gogo/protobuf/proto"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/prometheus/model/labels"
)

type Rule struct {
	MetricName    string
	LabelMatchers []*labels.Matcher
	RewriteRules  []RewriteRule
}

type RewriteRule struct {
	LabelName    string
	NewLabelName string
	NewValue     string
}

type Renamer struct {
	Rules map[string][]Rule
}

func LoadRules(rules []Rule) *Renamer {
	renamer := &Renamer{
		Rules: make(map[string][]Rule),
	}

	for _, r := range rules {
		for i, rw := range r.RewriteRules {
			if rw.NewLabelName == "" {
				r.RewriteRules[i].NewLabelName = rw.LabelName
			}
		}

		renamer.Rules[r.MetricName] = append(renamer.Rules[r.MetricName], r)
	}

	return renamer
}

func (r *Renamer) Rename(points []types.MetricPoint) []types.MetricPoint {
	for i, pts := range points {
		rules, ok := r.Rules[pts.Labels[types.LabelName]]
		if !ok {
			continue
		}

		for _, rule := range rules {
			points[i], ok = rule.rename(pts)
			if ok {
				break
			}
		}
	}

	return points
}

func (r *Renamer) RenameMFS(mfs []*dto.MetricFamily) []*dto.MetricFamily {
	idToSort := make(map[int]bool)
	nameToIndex := make(map[string]int, len(mfs))

	for i, mf := range mfs {
		nameToIndex[mf.GetName()] = i
	}

	for _, mf := range mfs {
		rules, ok := r.Rules[mf.GetName()]
		if !ok {
			continue
		}

		i := 0

		for _, m := range mf.Metric {
			name := mf.GetName()
			for _, rule := range rules {
				m, name, ok = rule.renameMetric(m, name)
				if ok {
					break
				}
			}

			if name == mf.GetName() {
				mf.Metric[i] = m
				idToSort[i] = true

				i++
			} else {
				idx, ok := nameToIndex[name]

				if !ok {
					mfs = append(mfs, &dto.MetricFamily{
						Name: proto.String(name),
						Help: proto.String(mf.GetHelp()),
						Type: mf.Type,
					})
					idx = len(mfs) - 1

					nameToIndex[name] = idx
				} else {
					m = fixType(m, mfs[idx].GetType())
				}

				mfs[idx].Metric = append(mfs[idx].Metric, m)
				idToSort[idx] = true
			}
		}

		mf.Metric = mf.Metric[:i]
	}

	for idx := range idToSort {
		sortMetrics(mfs[idx].Metric)
	}

	// Drop empty MF
	i := 0

	for _, mf := range mfs {
		if len(mf.Metric) == 0 {
			continue
		}

		mfs[i] = mf
		i++
	}

	mfs = mfs[:i]

	sort.Slice(mfs, func(i, j int) bool {
		return mfs[i].GetName() < mfs[j].GetName()
	})

	return mfs
}

func sortMetrics(input []*dto.Metric) []*dto.Metric {
	sort.Slice(input, func(i, j int) bool {
		mA := input[i]
		mB := input[j]

		for i := range mA.Label {
			if len(mB.Label) <= i {
				return false
			}

			if mA.Label[i].GetName() < mB.Label[i].GetName() {
				return true
			}

			if mA.Label[i].GetName() > mB.Label[i].GetName() {
				return false
			}

			if mA.Label[i].GetValue() < mB.Label[i].GetValue() {
				return true
			}

			if mA.Label[i].GetValue() > mB.Label[i].GetValue() {
				return false
			}
		}

		return false
	})

	return input
}

func (r Rule) rename(point types.MetricPoint) (types.MetricPoint, bool) {
	for _, m := range r.LabelMatchers {
		if !m.Matches(point.Labels[m.Name]) {
			return point, false
		}
	}

	for _, rw := range r.RewriteRules {
		value := rw.NewValue

		if rw.NewValue == "$1" {
			value = point.Labels[rw.LabelName]
		}

		if rw.NewLabelName != rw.LabelName {
			delete(point.Labels, rw.LabelName)
		}

		if value == "" {
			delete(point.Labels, rw.NewLabelName)
		} else {
			point.Labels[rw.NewLabelName] = value
		}
	}

	return point, true
}

func (r Rule) renameMetric(metric *dto.Metric, name string) (*dto.Metric, string, bool) {
	lblsMap := make(map[string]string, len(metric.Label))
	for _, v := range metric.Label {
		lblsMap[v.GetName()] = v.GetValue()
	}

	lblsMap[types.LabelName] = name

	for _, m := range r.LabelMatchers {
		if !m.Matches(lblsMap[m.Name]) {
			return metric, name, false
		}
	}

	for _, rw := range r.RewriteRules {
		value := rw.NewValue

		if rw.NewValue == "$1" {
			value = lblsMap[rw.LabelName]
		}

		if rw.NewLabelName != rw.LabelName {
			delete(lblsMap, rw.LabelName)
		}

		if value == "" {
			delete(lblsMap, rw.NewLabelName)
		} else {
			lblsMap[rw.NewLabelName] = value
		}
	}

	name = lblsMap[types.LabelName]
	delete(lblsMap, types.LabelName)

	metric.Label = metric.Label[:0]

	for k, v := range lblsMap {
		metric.Label = append(metric.Label, &dto.LabelPair{
			Name:  proto.String(k),
			Value: proto.String(v),
		})
	}

	sort.Slice(metric.Label, func(i, j int) bool {
		return metric.Label[i].GetName() < metric.Label[j].GetName()
	})

	return metric, name, true
}

func fixType(m *dto.Metric, wantType dto.MetricType) *dto.Metric { //nolint: cyclop
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
		value = m.Counter.Value
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
