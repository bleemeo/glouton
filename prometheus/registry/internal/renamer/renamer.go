// Copyright 2015-2024 Bleemeo
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

package renamer

import (
	"sort"

	"github.com/bleemeo/glouton/prometheus/model"
	"github.com/bleemeo/glouton/types"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/prometheus/model/labels"
	"google.golang.org/protobuf/proto"
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

		for _, m := range mf.GetMetric() {
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
						Type: mf.Type, //nolint:protogetter
					})
					idx = len(mfs) - 1

					nameToIndex[name] = idx
				} else {
					model.FixType(m, mfs[idx].GetType())
				}

				mfs[idx].Metric = append(mfs[idx].GetMetric(), m)
				idToSort[idx] = true
			}
		}

		mf.Metric = mf.GetMetric()[:i]
	}

	for idx := range idToSort {
		sortMetrics(mfs[idx].GetMetric())
	}

	// Drop empty MF
	i := 0

	for _, mf := range mfs {
		if len(mf.GetMetric()) == 0 {
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

		for i := range mA.GetLabel() {
			if len(mB.GetLabel()) <= i {
				return false
			}

			if mA.GetLabel()[i].GetName() < mB.GetLabel()[i].GetName() {
				return true
			}

			if mA.GetLabel()[i].GetName() > mB.GetLabel()[i].GetName() {
				return false
			}

			if mA.GetLabel()[i].GetValue() < mB.GetLabel()[i].GetValue() {
				return true
			}

			if mA.GetLabel()[i].GetValue() > mB.GetLabel()[i].GetValue() {
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
	lblsMap := make(map[string]string, len(metric.GetLabel()))
	for _, v := range metric.GetLabel() {
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

	metric.Label = metric.GetLabel()[:0]

	for k, v := range lblsMap {
		metric.Label = append(metric.GetLabel(), &dto.LabelPair{
			Name:  proto.String(k),
			Value: proto.String(v),
		})
	}

	sort.Slice(metric.GetLabel(), func(i, j int) bool {
		return metric.GetLabel()[i].GetName() < metric.GetLabel()[j].GetName()
	})

	return metric, name, true
}
