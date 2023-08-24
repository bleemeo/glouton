// Copyright 2015-2023 Bleemeo
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

// Package collector do the metric point gathering for all configured input every fixed time interval
package collector

import (
	"context"
	"errors"
	"glouton/crashreport"
	"glouton/inputs"
	"glouton/logger"
	"glouton/types"
	"math"
	"sync"
	"time"

	"github.com/influxdata/telegraf"
	"github.com/prometheus/prometheus/model/value"
)

var errTooManyInputs = errors.New("too many inputs in the collectors. Unable to find new slot")

// Collector implement running Gather on inputs every fixed time interval.
type Collector struct {
	acc          telegraf.Accumulator
	inputs       map[int]telegraf.Input
	currentDelay time.Duration
	updateDelayC chan interface{}
	l            sync.Mutex
	fieldCaches  map[int]map[string]map[string]map[string]fieldCache
}

// New returns a Collector with default option
//
// By default, no input are added (use AddInput) and collection is done every
// 10 seconds.
func New(acc telegraf.Accumulator) *Collector {
	c := &Collector{
		acc:          acc,
		inputs:       make(map[int]telegraf.Input),
		currentDelay: 10 * time.Second,
		updateDelayC: make(chan interface{}),
		fieldCaches:  make(map[int]map[string]map[string]map[string]fieldCache),
	}

	return c
}

// AddInput add an input to this collector and return an ID.
func (c *Collector) AddInput(input telegraf.Input, shortName string) (int, error) {
	_ = shortName

	c.l.Lock()
	defer c.l.Unlock()

	if si, ok := input.(telegraf.Initializer); ok {
		if err := si.Init(); err != nil {
			// Don't add the input if the initialization failed.
			return 0, err
		}
	}

	id := 1

	_, ok := c.inputs[id]
	for ok {
		id++
		if id == 0 {
			return 0, errTooManyInputs
		}

		_, ok = c.inputs[id]
	}

	c.inputs[id] = input
	c.fieldCaches[id] = make(map[string]map[string]map[string]fieldCache)

	if si, ok := input.(telegraf.ServiceInput); ok {
		if err := si.Start(nil); err != nil {
			return 0, err
		}
	}

	return id, nil
}

// RemoveInput removes an input by its ID.
func (c *Collector) RemoveInput(id int) {
	c.l.Lock()
	defer c.l.Unlock()

	if input, ok := c.inputs[id]; ok {
		if si, ok := input.(telegraf.ServiceInput); ok {
			si.Stop()
		}
	} else {
		logger.V(2).Printf("called RemoveInput with unexisting ID %d", id)
	}

	delete(c.inputs, id)
	delete(c.fieldCaches, id)
}

// Close stops all inputs.
func (c *Collector) Close() {
	c.l.Lock()
	defer c.l.Unlock()

	for _, input := range c.inputs {
		if si, ok := input.(telegraf.ServiceInput); ok {
			si.Stop()
		}
	}
}

// RunGather run one gather and send metric through the accumulator.
func (c *Collector) RunGather(_ context.Context, t0 time.Time) {
	c.runOnce(t0)
}

func (c *Collector) inputsForCollection() map[int]telegraf.Input {
	c.l.Lock()
	defer c.l.Unlock()

	inputsCopy := make(map[int]telegraf.Input, len(c.inputs))

	for i, v := range c.inputs {
		inputsCopy[i] = v
	}

	return inputsCopy
}

func (c *Collector) runOnce(t0 time.Time) {
	inputsCopy := c.inputsForCollection()
	acc := inputs.FixedTimeAccumulator{
		Time: t0,
		Acc:  c.acc,
	}

	var wg sync.WaitGroup

	for i, input := range inputsCopy {
		i, input := i, input

		wg.Add(1)

		go func() {
			defer crashreport.ProcessPanic()
			defer wg.Done()

			ima := &inactiveMarkerAccumulator{
				FixedTimeAccumulator: acc,
				latestValues:         make(map[string]map[string]map[string]fieldCache),
				fieldCaches:          c.fieldCaches[i],
			}
			// Errors are already logged by the input.
			_ = input.Gather(ima)

			ima.deactivateUnseenMetrics()
		}()
	}

	wg.Wait()
}

type accCallback func(acc inputs.FixedTimeAccumulator, measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time)

type fieldCache struct {
	annotations types.MetricAnnotations
	callback    accCallback
}

// inactiveMarkerAccumulator wraps a telegraf Accumulator while marking inactive metrics it receives as such.
type inactiveMarkerAccumulator struct {
	inputs.FixedTimeAccumulator
	latestValues map[string]map[string]map[string]fieldCache
	fieldCaches  map[string]map[string]map[string]fieldCache
	l            sync.Mutex
}

func (ima *inactiveMarkerAccumulator) deactivateUnseenMetrics() {
	ima.l.Lock()
	defer ima.l.Unlock()

	// Check if a metric has been seen for each measurement, field and tags "pair".
	for oldMeasurement, oldFields := range ima.fieldCaches {
		for oldField, oldTags := range oldFields {
			for oldTag, cache := range oldTags {
				if _, ok := ima.latestValues[oldMeasurement]; ok {
					if _, ok = ima.latestValues[oldMeasurement][oldField]; ok {
						if _, ok = ima.latestValues[oldMeasurement][oldField][oldTag]; ok {
							continue // This metric has been seen, everything's all right
						}
					}
				}
				// Publish the StaleNaN value to mark the metric as inactive.
				fieldsMap := map[string]any{
					// We need to convert the StaleNaN value to a float this way to avoid
					// the conversion float64(uint64) which implies a precision loss.
					// This would have resulted in the value not being handled as StaleNaN later.
					oldField: math.Float64frombits(value.StaleNaN),
				}
				tagsMap := types.TextToLabels(oldTag)

				if cache.callback != nil {
					cache.callback(ima.FixedTimeAccumulator, oldMeasurement, fieldsMap, tagsMap, ima.Time)
				} else {
					ima.FixedTimeAccumulator.AddFieldsWithAnnotations(oldMeasurement, fieldsMap, tagsMap, cache.annotations, ima.Time)
				}
			}
		}
	}

	// Deleting all entries to prepare for the update with the latest metrics
	for k := range ima.fieldCaches {
		delete(ima.fieldCaches, k)
	}

	// Update the cache with the metrics that are existing right now
	for measurement, fields := range ima.latestValues {
		if _, ok := ima.fieldCaches[measurement]; !ok {
			ima.fieldCaches[measurement] = make(map[string]map[string]fieldCache)
		}

		for field, tags := range fields {
			if _, ok := ima.fieldCaches[measurement][field]; !ok {
				ima.fieldCaches[measurement][field] = make(map[string]fieldCache)
			}

			for tag, v := range tags {
				ima.fieldCaches[measurement][field][tag] = v
			}
		}
	}

	// ima.latestValues will be dropped as the inactiveMarkerAccumulator itself will be.
} //nolint:wsl

func (ima *inactiveMarkerAccumulator) doAdd(accCb accCallback, measurement string, fields map[string]interface{}, tags map[string]string, t []time.Time, annotations ...types.MetricAnnotations) {
	ima.l.Lock()

	// Using a closure to make the deferred call to unlock happening before the call
	// to the accumulator callback to release the lock a little bit quicker, while
	// keeping the assurance of having the lock released regardless of the behavior.
	func() {
		defer ima.l.Unlock()

		m, ok := ima.latestValues[measurement]
		if !ok {
			m = make(map[string]map[string]fieldCache)
		}

		strTags := types.LabelsToText(tags)

		for field := range fields {
			cache := fieldCache{
				callback: accCb,
			}

			if len(annotations) == 1 {
				cache.annotations = annotations[0]
			}

			if _, ok = m[field]; !ok {
				m[field] = make(map[string]fieldCache)
			}

			m[field][strTags] = cache
		}

		ima.latestValues[measurement] = m
	}()

	if accCb != nil { //nolint:gocritic
		accCb(ima.FixedTimeAccumulator, measurement, fields, tags, t...)
	} else if len(annotations) == 1 {
		ima.FixedTimeAccumulator.AddFieldsWithAnnotations(measurement, fields, tags, annotations[0], ima.Time)
	} else {
		logger.V(2).Printf("No callback or annotations given for %s / %s with fields %v", measurement, types.LabelsToText(tags), fields)
	}
}

func (ima *inactiveMarkerAccumulator) AddFields(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	ima.doAdd(inputs.FixedTimeAccumulator.AddFields, measurement, fields, tags, t)
}

func (ima *inactiveMarkerAccumulator) AddGauge(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	ima.doAdd(inputs.FixedTimeAccumulator.AddGauge, measurement, fields, tags, t)
}

func (ima *inactiveMarkerAccumulator) AddCounter(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	ima.doAdd(inputs.FixedTimeAccumulator.AddCounter, measurement, fields, tags, t)
}

func (ima *inactiveMarkerAccumulator) AddSummary(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	ima.doAdd(inputs.FixedTimeAccumulator.AddSummary, measurement, fields, tags, t)
}

func (ima *inactiveMarkerAccumulator) AddHistogram(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	ima.doAdd(inputs.FixedTimeAccumulator.AddHistogram, measurement, fields, tags, t)
}

func (ima *inactiveMarkerAccumulator) AddFieldsWithAnnotations(measurement string, fields map[string]interface{}, tags map[string]string, annotations types.MetricAnnotations, t ...time.Time) {
	// When given a nil callback but annotations, doAdd() will call ima.FixedTimeAccumulator.AddFieldsWithAnnotations()
	ima.doAdd(nil, measurement, fields, tags, t, annotations)
}
