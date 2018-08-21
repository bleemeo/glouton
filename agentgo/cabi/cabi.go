package main

/*
struct Tag {
	char* tag_name;
	char* tag_value;
};

typedef struct Tag Tag;

enum Unit{
	NoUnit = 0,
	Bytes = 1,
	Percent = 2,
	BytesPerSecond = 3,
	BitsPerSecond = 4,
	MillissecondPerSecond = 5,
	ReadsPerSecond = 6,
	WritesPerSecond = 7,
	PacketsPerSecond = 8,
	ErrorsPerSecond = 9,
};

struct MetricPoint
{
	char* name;
	Tag *tag;
	int tag_count;
	char* chart;
	enum Unit unit;
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
	"../system"
	"../types"
	"math/rand"
)

var collectors map[int]types.Collector = make(map[int]types.Collector)

// FunctionTest export an int in C
//export FunctionTest
func FunctionTest() int64 { return 42 }

// InitMemoryCollector initialises a memory collector and returns his ID
// export InitMemoryCollector
func InitMemoryCollector() int {
	var id = rand.Intn(10000)

	for _, ok := collectors[id]; ok == true; {
		id = rand.Intn(10000)
	}
	collectors[id] = system.InitMemoryCollector()

	return id
}

// FreeCollector deletes a collector
// exit code 0 : the collector has been removed
// exit code 1 : the collector did not exist
// export FreeCollector
func FreeCollector(collectorID int) int {
	_, ok := collectors[collectorID]
	if ok {
		delete(collectors, collectorID)
		return 0
	}
	return 1
}

// Gather returns associated metrics of collectorID given in parameter.
// export Gather
func Gather(collectorID int) C.MetricPointVector {
	var collector, ok = collectors[collectorID]
	var result C.MetricPointVector
	if !ok {
		return result
	}
	var metrics = collector.Gather()
	var metricPointSlice = make([]C.MetricPoint, cap(metrics), len(metrics))
	for index, metricPoint := range metrics {
		metricPointSlice[index] = convertMetricPointInC(metricPoint)
	}
	return (C.MetricPointVector{metric_point: &metricPointSlice[0], metric_point_count: C.int(len(metricPointSlice))})

}

func convertMetricPointInC(metricPoint types.MetricPoint) C.MetricPoint {
	var tagCount int
	for range metricPoint.Metric.Tag {
		tagCount++
	}

	var tag []C.Tag = make([]C.Tag, tagCount, tagCount)
	var index int

	for key, value := range metricPoint.Metric.Tag {
		tag[index] = C.Tag{tag_name: C.CString(key), tag_value: C.CString(value)}
		index++
	}

	var unit C.enum_Unit
	switch metricPoint.Metric.Unit {
	case 0:
		unit = C.NoUnit
	case 1:
		unit = C.Bytes
	case 2:
		unit = C.Percent
	case 3:
		unit = C.BytesPerSecond
	case 4:
		unit = C.BitsPerSecond
	case 5:
		unit = C.MillissecondPerSecond
	case 6:
		unit = C.ReadsPerSecond
	case 7:
		unit = C.WritesPerSecond
	case 8:
		unit = C.PacketsPerSecond
	case 9:
		unit = C.ErrorsPerSecond
	}

	var result C.MetricPoint = C.MetricPoint{
		name:      C.CString(metricPoint.Metric.Name),
		tag:       &tag[0],
		tag_count: C.int(tagCount),
		chart:     C.CString(metricPoint.Metric.Chart),
		unit:      unit,
		value:     C.float(metricPoint.Value)}

	return result
}

func main() {}
