package main

/*
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
	"../types"
	"github.com/influxdata/telegraf"
	// Needed to run this package
	"github.com/influxdata/telegraf/plugins/inputs"
	_ "github.com/influxdata/telegraf/plugins/inputs/mem"
	"math/rand"
)

var inputsgroups = make(map[int]map[int]telegraf.Input)

// FunctionTest export an int in C
//export FunctionTest
func FunctionTest() int64 { return 42 }

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
	var inputID = rand.Intn(10000)

	for _, ok := inputsgroups[inputGroupID][inputID]; ok == true; {
		inputID = rand.Intn(10000)
	}
	inputsgroups[inputGroupID][inputID] = input
	return inputID
}

// MemoryInput add a memory input to the inputgroupID and return the input ID in the group
//export MemoryInput
func MemoryInput(inputGroupID int) int {
	return addInputToInputGroup(inputGroupID, inputs.Inputs["mem"]())
}

// FreeInputGroup deletes a collector
// exit code 0 : the input group has been removed
// exit code 1 : the input group did not exist
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
	return 1
}

// Gather returns associated metrics in a slice of inputgroupID given in parameter.
//export Gather
func Gather(inputgroupID int) C.MetricPointVector {
	var inputgroup, ok = inputsgroups[inputgroupID]
	var result C.MetricPointVector
	if !ok {
		return result
	}
	accumulator := types.InitAccumulator()

	for _, input := range inputgroup {
		input.Gather(&accumulator)
	}
	metrics := accumulator.GetMetricPointSlice()
	var metricPointArray = C.malloc(C.size_t(len(metrics)) * C.sizeof_MetricPoint)
	var metricPointSlice = (*[1<<30 - 1]C.MetricPoint)(metricPointArray)

	for index, metricPoint := range metrics {
		var cMetricPoint = convertMetricPointInC(metricPoint)
		metricPointSlice[index] = cMetricPoint
	}
	return (C.MetricPointVector{
		metric_point:       (*C.MetricPoint)(metricPointArray),
		metric_point_count: C.int(len(metrics)),
	})

}

func convertMetricPointInC(metricPoint types.MetricPoint) C.MetricPoint {
	var tagCount int
	for range metricPoint.Tags {
		tagCount++
	}

	var tags = C.malloc(C.size_t(tagCount) * C.sizeof_Tag)
	var tagsSlice = (*[1<<30 - 1]C.Tag)(tags)

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
		name:        C.CString(metricPoint.Name),
		tag:         (*C.Tag)(tags),
		tag_count:   C.int(tagCount),
		metric_type: metricType,
		value:       C.float(metricPoint.Value)}

	return result
}

func main() {}
