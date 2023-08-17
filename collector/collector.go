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
	"fmt"
	"glouton/crashreport"
	"glouton/inputs"
	"glouton/logger"
	"glouton/types"
	"sync"
	"time"

	"github.com/influxdata/telegraf"
	"github.com/prometheus/prometheus/model/value"
)

var (
	errTooManyInputs = errors.New("too many inputs in the collectors. Unable to find new slot")
	errMissingMethod = errors.New("AddFieldsWithAnnotations method missing, the annotation is lost")
)

// Collector implement running Gather on inputs every fixed time interval.
type Collector struct {
	acc          telegraf.Accumulator
	inputs       map[int]telegraf.Input
	fieldsCache  map[int]map[string]map[string]struct{}
	currentDelay time.Duration
	updateDelayC chan interface{}
	l            sync.Mutex
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
		fieldsCache:  make(map[int]map[string]map[string]struct{}),
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
	c.fieldsCache[id] = make(map[string]map[string]struct{})

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
	delete(c.fieldsCache, id)
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

			// Errors are already logged by the input.
			_ = input.Gather(&inactiveMarkerAccumulator{Accumulator: acc, fieldsCache: c.fieldsCache[i]})
		}()
	}

	wg.Wait()
}

// inactiveMarkerAccumulator wraps a telegraf Accumulator while marking inactive metrics it receives as such.
type inactiveMarkerAccumulator struct {
	telegraf.Accumulator

	fieldsCache map[string]map[string]struct{}
	l           sync.Mutex
}

type accCallback func(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time)

func (ima *inactiveMarkerAccumulator) doAdd(kind string, accCb accCallback, measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	ima.l.Lock()

	// Using a closure to make the deferred call to unlock happening before the call
	// to the accumulator callback to release the lock a little bit quicker, while
	// keeping the assurance of having the lock released regardless of the behavior.
	func() {
		defer ima.l.Unlock()

		newMetrics := make(map[string]struct{})
		// Update the cache with the metrics that are existing right now
		for newField := range fields {
			newMetrics[newField] = struct{}{}
		}

		strTags := fmt.Sprint(tags)

		metrics := ima.fieldsCache[kind+"__"+measurement+"__"+strTags]
		// Setting a NaN value for metrics that are now longer updated to mark them as inactive
		for oldField := range metrics {
			if _, exists := fields[oldField]; !exists {
				fields[oldField] = value.StaleNaN
			}
		}

		ima.fieldsCache[kind+"__"+measurement+"__"+strTags] = newMetrics
	}()

	if accCb != nil {
		accCb(measurement, fields, tags, t...)
	}
}

func (ima *inactiveMarkerAccumulator) AddFields(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	ima.doAdd("fields", ima.Accumulator.AddFields, measurement, fields, tags, t...)
}

func (ima *inactiveMarkerAccumulator) AddGauge(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	ima.doAdd("gauge", ima.Accumulator.AddGauge, measurement, fields, tags, t...)
}

func (ima *inactiveMarkerAccumulator) AddCounter(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	ima.doAdd("counter", ima.Accumulator.AddCounter, measurement, fields, tags, t...)
}

func (ima *inactiveMarkerAccumulator) AddSummary(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	ima.doAdd("summary", ima.Accumulator.AddSummary, measurement, fields, tags, t...)
}

func (ima *inactiveMarkerAccumulator) AddHistogram(measurement string, fields map[string]interface{}, tags map[string]string, t ...time.Time) {
	ima.doAdd("histogram", ima.Accumulator.AddHistogram, measurement, fields, tags, t...)
}

func (ima *inactiveMarkerAccumulator) AddFieldsWithAnnotations(measurement string, fields map[string]interface{}, tags map[string]string, annotations types.MetricAnnotations, t ...time.Time) {
	// With a nil callback, doAdd() will just mutate the fields map and update fieldsCache.
	ima.doAdd("annotation", nil, measurement, fields, tags, t...)

	if annotationAcc, ok := ima.Accumulator.(inputs.AnnotationAccumulator); ok {
		annotationAcc.AddFieldsWithAnnotations(measurement, fields, tags, annotations, t...)

		return
	}

	ima.Accumulator.AddGauge(measurement, fields, tags, t...)
	ima.Accumulator.AddError(errMissingMethod)
}

func (ima *inactiveMarkerAccumulator) AddError(err error) {
	if err != nil {
		logger.V(1).Printf("Add error called with: %v", err)
	}
}
