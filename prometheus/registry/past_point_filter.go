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

package registry

import (
	"context"
	"sync"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/model"
)

type GathererWithOrWithoutState interface {
	Gather() ([]*dto.MetricFamily, error)
	GatherWithState(ctx context.Context, state GatherState) ([]*dto.MetricFamily, error)
}

type point struct {
	timestampMs int64
	recordedAt  time.Time
}

type pastPointFilter struct {
	gatherer GathererWithOrWithoutState

	latestPointByLabelsByMetric map[string]map[uint64]point

	purgeInterval time.Duration
	lastPurgeAt   time.Time

	timeNow func() time.Time // For testing purposes

	l sync.Mutex
}

// WithPastPointFilter wraps the given gatherer with a filter that will ensure that
// every emitted point will not have a timestamp before the latest sent.
// The filter will purge its cache at the given purgeInterval.
func WithPastPointFilter(gatherer GathererWithOrWithoutState, purgeInterval time.Duration) GathererWithOrWithoutState {
	return &pastPointFilter{
		gatherer:                    gatherer,
		latestPointByLabelsByMetric: make(map[string]map[uint64]point),
		purgeInterval:               purgeInterval,
		lastPurgeAt:                 time.Now(),
		timeNow:                     time.Now,
	}
}

// SecretCount allows getting the secret count of the underlying gatherer.
func (ppf *pastPointFilter) SecretCount() int {
	if si, ok := ppf.gatherer.(interface{ SecretCount() int }); ok {
		return si.SecretCount()
	}

	return 0
}

func (ppf *pastPointFilter) Gather() ([]*dto.MetricFamily, error) {
	return ppf.filter(ppf.gatherer.Gather())
}

func (ppf *pastPointFilter) GatherWithState(ctx context.Context, state GatherState) ([]*dto.MetricFamily, error) {
	return ppf.filter(ppf.gatherer.GatherWithState(ctx, state))
}

func (ppf *pastPointFilter) filter(mfs []*dto.MetricFamily, err error) ([]*dto.MetricFamily, error) {
	if err != nil {
		return mfs, err
	}

	ppf.l.Lock()
	defer ppf.l.Unlock()

	now := ppf.timeNow().Truncate(time.Second)

	f := 0

	for fi, mf := range mfs {
		if mf == nil {
			continue
		}

		latestPointByLabelSignatures, found := ppf.latestPointByLabelsByMetric[mf.GetName()]
		if !found {
			latestPointByLabelSignatures = make(map[uint64]point, len(mf.GetMetric()))
		}

		m := 0

		for mi := range len(mf.Metric) { //nolint:protogetter
			metric := mf.Metric[mi] //nolint:protogetter
			if metric == nil {
				continue
			}

			signature := labelPairsToSignature(metric.GetLabel())
			currentTimestamp := metric.GetTimestampMs()

			if latestPoint, found := latestPointByLabelSignatures[signature]; found {
				if latestPoint.timestampMs > currentTimestamp {
					// This metric jumped backward, drop it.
					// We do NOT drop the metric that emitted the same timestamp, this is allowed by design:
					// the gatherer is allowed to return the same cached result
					// or return the same value because it is called quicker than its resolution.
					continue
				}
			}

			latestPointByLabelSignatures[signature] = point{currentTimestamp, now}
			mf.Metric[m] = metric
			m++
		}

		if m != 0 {
			mf.Metric = mf.Metric[:m] //nolint:protogetter
			mfs[fi] = mf
			f++
		}

		ppf.latestPointByLabelsByMetric[mf.GetName()] = latestPointByLabelSignatures
	}

	mfs = mfs[:f]

	ppf.runPurge(now)

	return mfs, nil
}

func (ppf *pastPointFilter) runPurge(now time.Time) {
	if now.Sub(ppf.lastPurgeAt) < ppf.purgeInterval {
		return
	}

	for metric, latestPointByLabelSignatures := range ppf.latestPointByLabelsByMetric {
		for signature, point := range latestPointByLabelSignatures {
			if now.Sub(point.recordedAt) > ppf.purgeInterval {
				delete(latestPointByLabelSignatures, signature)
			}
		}

		if len(latestPointByLabelSignatures) == 0 {
			delete(ppf.latestPointByLabelsByMetric, metric)
		}
	}

	ppf.lastPurgeAt = now
}

func labelPairsToSignature(labelPairs []*dto.LabelPair) uint64 {
	labels := make(map[string]string, len(labelPairs))

	for _, labelPair := range labelPairs {
		labels[labelPair.GetName()] = labelPair.GetValue()
	}

	return model.LabelsToSignature(labels)
}
