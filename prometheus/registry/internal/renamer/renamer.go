package renamer

import (
	"glouton/types"
	"sort"

	"github.com/gogo/protobuf/proto"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/prometheus/pkg/labels"
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

func (r *Renamer) RenameOne(point types.MetricPoint) types.MetricPoint {
	rules, ok := r.Rules[point.Labels[types.LabelName]]
	if !ok {
		return point
	}

	for _, rule := range rules {
		point, ok = rule.rename(point)
		if ok {
			break
		}
	}

	return point
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
				}

				mfs[idx].Metric = append(mfs[idx].Metric, m)
			}
		}

		mf.Metric = mf.Metric[:i]
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
