// Copyright 2015-2018 Bleemeo
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

// Expose the go metric in C

// nolint: gas, deadcode
package main

/*
#include<stdlib.h>

struct Tag {
	char* tag_name;
	char* tag_value;
};

typedef struct Tag Tag;

enum Type{
	fields = 0,
	gauge = 1,
	counter = 2,
	summary = 3,
	histogram = 4,
};

struct MetricPoint
{
	int input_id;
	char* name;
	Tag *tag;
	int tag_count;
	enum Type metric_type;
	float value;
};

typedef struct MetricPoint MetricPoint;

struct MetricPointVector {
	MetricPoint* metric_point;
	int metric_point_count;
};

typedef struct MetricPointVector MetricPointVector;

*/
import "C"

import (
	"agentgo/inputs/cpu"
	"agentgo/inputs/mem"
	"agentgo/inputs/mysql"
	"agentgo/inputs/nginx"
	"agentgo/inputs/redis"
	"agentgo/inputs/swap"
	"agentgo/types"
	"github.com/influxdata/telegraf"
	"math/rand"
	"unsafe"
)

var inputsgroups = make(map[int]map[int]telegraf.Input)

// InitInputGroup initialises a metric group and returns his ID
//export InitInputGroup
func InitInputGroup() int {
	var id = rand.Intn(10000)

	for _, ok := inputsgroups[id]; ok == true; {
		id = rand.Intn(10000)
	}
	inputsgroups[id] = make(map[int]telegraf.Input)
	return id
}

// addInputToInputGroup add the input to the inputGroupID and return the inputID of the input in the group
func addInputToInputGroup(inputGroupID int, input telegraf.Input) int {
	var _, ok = inputsgroups[inputGroupID]
	if !ok {
		return -1
	}

	var inputID = rand.Intn(10000)

	for _, ok := inputsgroups[inputGroupID][inputID]; ok == true; {
		inputID = rand.Intn(10000)
	}
	inputsgroups[inputGroupID][inputID] = input
	return inputID
}

// AddSimpleInput add a simple input to the inputgroupID
// return the input ID in the group
// A simple input is only define by its name
//export AddSimpleInput
func AddSimpleInput(inputGroupID int, inputName *C.char) int {
	goInputName := C.GoString(inputName)
	if goInputName == "cpu" {
		input := cpu.NewInput()
		return addInputToInputGroup(inputGroupID, input)
	} else if goInputName == "mem" {
		input := mem.NewInput()
		return addInputToInputGroup(inputGroupID, input)
	} else if goInputName == "swap" {
		input := swap.NewInput()
		return addInputToInputGroup(inputGroupID, input)
	}
	return -1
}

// AddInputWithAddress add an input to the inputgroupID
// return the input ID in the group
//export AddInputWithAddress
func AddInputWithAddress(inputGroupID int, inputName *C.char, server *C.char) int {
	goInputName := C.GoString(inputName)
	if goInputName == "redis" {
		return addInputToInputGroup(inputGroupID, redis.NewInput(C.GoString(server)))
	} else if goInputName == "nginx" {
		return addInputToInputGroup(inputGroupID, nginx.NewInput(C.GoString(server)))
	} else if goInputName == "mysql" {
		return addInputToInputGroup(inputGroupID, mysql.NewInput(C.GoString(server)))
	}
	return -1
}

// FreeInputGroup deletes a collector
// exit code 0 : the input group has been removed
// exit code -1 : the input group did not exist
//export FreeInputGroup
func FreeInputGroup(inputgroupID int) int {
	_, ok := inputsgroups[inputgroupID]
	if ok {
		for id := range inputsgroups[inputgroupID] {
			delete(inputsgroups[inputgroupID], id)
		}
		delete(inputsgroups, inputgroupID)
		return 0
	}
	return -1
}

// FreeInput deletes an input in a group
// exit code 0 : the input has been removed
// exit code -1 : the input or the group did not exist
//export FreeInput
func FreeInput(inputgroupID int, inputID int) int {
	inputsGroup, ok := inputsgroups[inputgroupID]
	if ok {
		_, ok := inputsGroup[inputID]
		if ok {
			delete(inputsGroup, inputID)
			return 0
		}
	}
	return -1
}

// FreeMetricPointVector free a C.MetricPointVector given in parameter
//export FreeMetricPointVector
func FreeMetricPointVector(metricVector C.MetricPointVector) {

	var metricPointSlice = (*[1<<30 - 1]C.MetricPoint)(unsafe.Pointer(metricVector.metric_point))

	for i := 0; i < int(metricVector.metric_point_count); i++ {
		freeMetricPoint(metricPointSlice[i])
	}

	C.free(unsafe.Pointer(metricVector.metric_point))
}

// freeMetricPoint free a C.MetricPoint
func freeMetricPoint(metricPoint C.MetricPoint) {
	C.free(unsafe.Pointer(metricPoint.name))

	var tagsSlice = (*[1<<30 - 1]C.Tag)(unsafe.Pointer(metricPoint.tag))

	for i := 0; i < int(metricPoint.tag_count); i++ {
		C.free(unsafe.Pointer(tagsSlice[i].tag_value))
		C.free(unsafe.Pointer(tagsSlice[i].tag_name))
	}
	C.free(unsafe.Pointer(metricPoint.tag))
}

// Gather returns associated metrics in a slice of inputgroupID given in parameter.
//export Gather
func Gather(inputgroupID int) C.MetricPointVector {
	var inputgroup, ok = inputsgroups[inputgroupID]
	var result C.MetricPointVector
	if !ok {
		return result
	}
	metrics := make(map[int][]types.MetricPoint)

	accumulator := types.Accumulator{}
	for inputID, input := range inputgroup {
		err := input.Gather(&accumulator)
		if err != nil {
			return result
		}
		metrics[inputID] = accumulator.GetMetricPointSlice()
	}

	var metricsLen int
	for _, metricsSlice := range metrics {
		metricsLen += len(metricsSlice)
	}
	var metricPointArray = C.malloc(C.size_t(metricsLen) * C.sizeof_MetricPoint)
	var metricPointSlice = (*[1<<30 - 1]C.MetricPoint)(metricPointArray)
	var index int
	for inputID, metricsSlice := range metrics {
		for _, goMetricPoint := range metricsSlice {
			var cMetricPoint = convertMetricPointInC(goMetricPoint, inputID)
			metricPointSlice[index] = cMetricPoint
			index++
		}
	}
	return (C.MetricPointVector{
		metric_point:       (*C.MetricPoint)(metricPointArray),
		metric_point_count: C.int(metricsLen),
	})

}

func convertMetricPointInC(metricPoint types.MetricPoint, inputID int) C.MetricPoint {
	var tagCount int
	for range metricPoint.Tags {
		tagCount++
	}

	var tags = C.malloc(C.size_t(tagCount) * C.sizeof_Tag)
	var tagsSlice = (*[1<<30 - 1]C.Tag)(tags) // nolint: gotype

	var index int
	for key, value := range metricPoint.Tags {
		tagsSlice[index] = C.Tag{
			tag_name:  C.CString(key),
			tag_value: C.CString(value),
		}
		index++
	}

	var metricType C.enum_Type
	switch metricPoint.Type {
	case types.Gauge:
		metricType = C.gauge
	case types.Counter:
		metricType = C.counter
	case types.Histogram:
		metricType = C.histogram
	case types.Summary:
		metricType = C.summary
	case types.Fields:
		metricType = C.fields
	}

	var result C.MetricPoint = C.MetricPoint{
		input_id:    C.int(inputID),
		name:        C.CString(metricPoint.Name),
		tag:         (*C.Tag)(tags),
		tag_count:   C.int(tagCount),
		metric_type: metricType,
		value:       C.float(metricPoint.Value)}

	return result
}

func main() {}
