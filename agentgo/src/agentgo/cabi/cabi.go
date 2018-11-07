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
	int time;
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
	"agentgo/inputs/disk"
	"agentgo/inputs/diskio"
	"agentgo/inputs/mem"
	"agentgo/inputs/mysql"
	"agentgo/inputs/net"
	"agentgo/inputs/nginx"
	"agentgo/inputs/process"
	"agentgo/inputs/redis"
	"agentgo/inputs/swap"
	"agentgo/inputs/system"
	"agentgo/types"
	"fmt"
	"math/rand"
	"sync"
	"unsafe"

	"github.com/influxdata/telegraf"
)

var (
	group = make(map[int]map[int]telegraf.Input)
	lock  sync.Mutex
)

// InitGroup initialises a metric group and returns his ID
//export InitGroup
func InitGroup() int {
	lock.Lock()
	defer lock.Unlock()
	var id = rand.Intn(32767)
	var selectionNumber int
	for _, ok := group[id]; ok == true && selectionNumber < 10; {
		id = rand.Intn(32767)
		selectionNumber++
	}
	if selectionNumber == 10 {
		return -1
	}
	group[id] = make(map[int]telegraf.Input)
	return id
}

// addInputToGroup add the input to the groupID and return the inputID of the input in the group
func addInputToGroup(groupID int, input telegraf.Input) int {
	var _, ok = group[groupID]
	if !ok {
		return -1
	}

	var inputID = rand.Intn(32767)

	var selectionNumber int
	for _, ok := group[groupID][inputID]; ok == true && selectionNumber < 10; {
		inputID = rand.Intn(32767)
		selectionNumber++
	}
	if selectionNumber == 10 {
		return -1
	}
	group[groupID][inputID] = input
	return inputID
}

// AddSimpleInput add a simple input to the groupID
// return the input ID in the group
// A simple input is only define by its name
//export AddSimpleInput
func AddSimpleInput(groupID int, inputName *C.char) int { // nolint: gocyclo
	var input telegraf.Input
	var err error
	goInputName := C.GoString(inputName)
	switch goInputName {
	case "cpu":
		input, err = cpu.New()
	case "mem":
		input, err = mem.New()
	case "swap":
		input, err = swap.New()
	case "system":
		input, err = system.New()
	case "process":
		input, err = process.New()
	case "net":
		input, err = net.New()
	case "diskio":
		input, err = diskio.New()
	case "disk":
		input, err = disk.New()
	default:
		err = fmt.Errorf("Input \"%s\" unknown", goInputName)
	}
	if err != nil {
		return -1
	}
	lock.Lock()
	defer lock.Unlock()
	return addInputToGroup(groupID, input)
}

// AddInputWithAddress add an input to the groupID
// return the input ID in the group
//export AddInputWithAddress
func AddInputWithAddress(groupID int, inputName *C.char, server *C.char) int {
	var input telegraf.Input
	var err error
	goInputName := C.GoString(inputName)
	if goInputName == "redis" {
		input, err = redis.New(C.GoString(server))
	} else if goInputName == "nginx" {
		input, err = nginx.New(C.GoString(server))
	} else if goInputName == "mysql" {
		input, err = mysql.New(C.GoString(server))
	} else {
		err = fmt.Errorf("Input \"%s\" unknown", goInputName)
	}
	if err != nil {
		return -1
	}
	lock.Lock()
	defer lock.Unlock()
	return addInputToGroup(groupID, input)
}

// FreeGroup deletes a collector
// exit code 0 : the input group has been removed
// exit code -1 : the input group did not exist
//export FreeGroup
func FreeGroup(groupID int) int {
	lock.Lock()
	defer lock.Unlock()
	_, ok := group[groupID]
	if ok {
		delete(group, groupID)
		return 0
	}
	return -1
}

// FreeInput deletes an input in a group
// exit code 0 : the input has been removed
// exit code -1 : the input or the group did not exist
//export FreeInput
func FreeInput(groupID int, inputID int) int {
	lock.Lock()
	defer lock.Unlock()
	inputsGroup, ok := group[groupID]
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

	var metricPointSlice = (*[1<<30 - 1]C.MetricPoint)(unsafe.Pointer(metricVector.metric_point))[:metricVector.metric_point_count:metricVector.metric_point_count]

	for i := 0; i < int(metricVector.metric_point_count); i++ {
		freeMetricPoint(metricPointSlice[i])
	}

	C.free(unsafe.Pointer(metricVector.metric_point))
}

// freeMetricPoint free a C.MetricPoint
func freeMetricPoint(metricPoint C.MetricPoint) {
	C.free(unsafe.Pointer(metricPoint.name))

	var tagsSlice = (*[1<<30 - 1]C.Tag)(unsafe.Pointer(metricPoint.tag))[:metricPoint.tag_count:metricPoint.tag_count]

	for i := 0; i < int(metricPoint.tag_count); i++ {
		C.free(unsafe.Pointer(tagsSlice[i].tag_value))
		C.free(unsafe.Pointer(tagsSlice[i].tag_name))
	}
	C.free(unsafe.Pointer(metricPoint.tag))
}

// Gather returns associated metrics in a slice of groupID given in parameter.
//export Gather
func Gather(groupID int) C.MetricPointVector {
	lock.Lock()
	defer lock.Unlock()
	var inputgroup, ok = group[groupID]
	var result C.MetricPointVector
	if !ok {
		return result
	}
	metrics := make(map[int][]types.MetricPoint)
	var metricsLock sync.Mutex
	var wg sync.WaitGroup

	for inputID, input := range inputgroup {
		wg.Add(1)
		go func(inputID int, input telegraf.Input) {
			defer wg.Done()
			accumulator := types.Accumulator{}
			err := input.Gather(&accumulator)
			if err != nil {
				return
			}
			metricsLock.Lock()
			defer metricsLock.Unlock()
			metrics[inputID] = accumulator.GetMetricPointSlice()
		}(inputID, input)
	}
	wg.Wait()

	var metricsLen int
	for _, metricsSlice := range metrics {
		metricsLen += len(metricsSlice)
	}
	var metricPointArray = C.malloc(C.size_t(metricsLen) * C.sizeof_MetricPoint)
	var metricPointSlice = (*[1<<30 - 1]C.MetricPoint)(metricPointArray)[:metricsLen:metricsLen]
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
	var tagsSlice = (*[1<<30 - 1]C.Tag)(tags)[:tagCount:tagCount] // nolint: gotype

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
		value:       C.float(metricPoint.Value),
		time:        C.int((metricPoint.Time).UnixNano() / 1000000000)}

	return result
}

func main() {}
