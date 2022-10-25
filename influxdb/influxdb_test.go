// Copyright 2015-2022 Bleemeo
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

package influxdb

import (
	"fmt"
	"glouton/types"
	"reflect"
	"testing"
	"time"

	influxDBClient "github.com/influxdata/influxdb1-client/v2"
)

const (
	metricName0  = "MetricPoint0"
	metricName1  = "MetricPoint1"
	metricName2  = "MetricPoint2"
	metricName3  = "MetricPoint3"
	metricName4  = "MetricPoint4"
	metricName5  = "MetricPoint5"
	metricName49 = "MetricPoint49"
)

func TestConvertMetricPoint(t *testing.T) {
	metricPoint1 := types.MetricPoint{
		Point: types.Point{
			Time:  time.Date(2009, 11, 17, 20, 34, 58, 651387237, time.UTC),
			Value: 4.2,
		},
		Labels: map[string]string{
			types.LabelName: "metric_test1",
			"type":          "int",
		},
	}

	metricPoint2 := types.MetricPoint{
		Point: types.Point{
			Time:  time.Date(2009, 11, 17, 20, 34, 58, 651387237, time.UTC),
			Value: 2.4,
		},
		Labels: map[string]string{
			types.LabelName: "metric_test1",
			"unit":          "no unit",
		},
	}

	additionalFlags := map[string]string{
		"hostname":   "Athena",
		"ip_address": "192.168.0.1",
	}

	influxPoint1expected, err := influxDBClient.NewPoint(
		"metric_test1",
		map[string]string{
			"hostname":   "Athena",
			"ip_address": "192.168.0.1",
			"type":       "int",
		},
		map[string]interface{}{
			"value": 4.2,
		},
		time.Date(2009, 11, 17, 20, 34, 58, 651387237, time.UTC),
	)
	if err != nil {
		t.Error(err)
	}

	influxPoint2expected, err := influxDBClient.NewPoint(
		"metric_test1",
		map[string]string{
			"hostname":   "Athena",
			"ip_address": "192.168.0.1",
			"unit":       "no unit",
		},
		map[string]interface{}{
			"value": 2.4,
		},
		time.Date(2009, 11, 17, 20, 34, 58, 651387237, time.UTC),
	)
	if err != nil {
		t.Error(err)
	}

	cases := []struct {
		MetricPoint types.MetricPoint
		AddFlags    map[string]string
		InfluxPoint *influxDBClient.Point
	}{
		{
			MetricPoint: metricPoint1,
			AddFlags:    additionalFlags,
			InfluxPoint: influxPoint1expected,
		},
		{
			MetricPoint: metricPoint2,
			AddFlags:    additionalFlags,
			InfluxPoint: influxPoint2expected,
		},
	}

	for i, c := range cases {
		got, err := convertMetricPoint(c.MetricPoint, c.AddFlags)
		if err != nil {
			t.Error(err)
		}

		if !reflect.DeepEqual(got, c.InfluxPoint) {
			t.Errorf("convertMetricPoint([case %v]) = %v, want %v", i, got, c.InfluxPoint)
		}
	}
}

func TestAddPoints(t *testing.T) {
	var client Client

	client.maxPendingPoints = 3
	metricPoints := make([]types.MetricPoint, 6)

	for i := range metricPoints {
		metricPoints[i] = types.MetricPoint{
			Point: types.Point{
				Time:  time.Date(2009, 11, 17, 20, 34, 58, 651387237, time.UTC),
				Value: 4.2,
			},
			Labels: map[string]string{
				types.LabelName: fmt.Sprintf("MetricPoint%v", i),
			},
		}
	}

	client.addPoints(metricPoints[0:2])

	if len(client.gloutonPendingPoints) != 2 {
		t.Errorf("len(client.gloutonPendingPoints) = %v want 2", len(client.gloutonPendingPoints))
	}

	if client.gloutonPendingPoints[0].Labels[types.LabelName] != metricName0 {
		t.Errorf("client.gloutonPendingPoints[0] = %s want MetricPoint0", client.gloutonPendingPoints[0].Labels[types.LabelName])
	}

	if client.gloutonPendingPoints[1].Labels[types.LabelName] != metricName1 {
		t.Errorf("client.gloutonPendingPoints[1] = %s want MetricPoint1", client.gloutonPendingPoints[0].Labels[types.LabelName])
	}

	client.addPoints(metricPoints[2:3])

	if len(client.gloutonPendingPoints) != 3 {
		t.Errorf("len(client.gloutonPendingPoints) = %v want 3", len(client.gloutonPendingPoints))
	}

	if client.gloutonPendingPoints[0].Labels[types.LabelName] != metricName0 {
		t.Errorf("client.gloutonPendingPoints[0].Labels[%s] = %s want MetricPoint0", types.LabelName, client.gloutonPendingPoints[0].Labels[types.LabelName])
	}

	if client.gloutonPendingPoints[1].Labels[types.LabelName] != metricName1 {
		t.Errorf("client.gloutonPendingPoints[1].Labels[%s] = %s want MetricPoint1", types.LabelName, client.gloutonPendingPoints[1].Labels[types.LabelName])
	}

	if client.gloutonPendingPoints[2].Labels[types.LabelName] != metricName2 {
		t.Errorf("client.gloutonPendingPoints[2].Labels[%s]: %s want MetricPoint2", types.LabelName, client.gloutonPendingPoints[2].Labels[types.LabelName])
	}

	client.addPoints(metricPoints[3:4])

	if len(client.gloutonPendingPoints) != 3 {
		t.Errorf("len(client.gloutonPendingPoints) = %v want 3", len(client.gloutonPendingPoints))
	}

	if client.gloutonPendingPoints[0].Labels[types.LabelName] != metricName1 {
		t.Errorf("client.gloutonPendingPoints[0].Labels[%s]: %s want MetricPoint1", types.LabelName, client.gloutonPendingPoints[0].Labels[types.LabelName])
	}

	if client.gloutonPendingPoints[1].Labels[types.LabelName] != metricName2 {
		t.Errorf("client.gloutonPendingPoints[1].Labels[%s]: %s want MetricPoint2", types.LabelName, client.gloutonPendingPoints[1].Labels[types.LabelName])
	}

	if client.gloutonPendingPoints[2].Labels[types.LabelName] != metricName3 {
		t.Errorf("client.gloutonPendingPoints[2].Labels[%s]: %s want MetricPoint3", types.LabelName, client.gloutonPendingPoints[2].Labels[types.LabelName])
	}

	client.addPoints(metricPoints)

	if len(client.gloutonPendingPoints) != 3 {
		t.Errorf("len(client.gloutonPendingPoints) = %v want 3", len(client.gloutonPendingPoints))
	}

	if client.gloutonPendingPoints[0].Labels[types.LabelName] != metricName3 {
		t.Errorf("client.gloutonPendingPoints[0].Labels[%s]: %s want MetricPoint3", types.LabelName, client.gloutonPendingPoints[0].Labels[types.LabelName])
	}

	if client.gloutonPendingPoints[1].Labels[types.LabelName] != metricName4 {
		t.Errorf("client.gloutonPendingPoints[1].Labels[%s]: %s want MetricPoint4", types.LabelName, client.gloutonPendingPoints[1].Labels[types.LabelName])
	}

	if client.gloutonPendingPoints[2].Labels[types.LabelName] != metricName5 {
		t.Errorf("client.gloutonPendingPoints[2].Labels[%s]: %s want MetricPoint5", types.LabelName, client.gloutonPendingPoints[2].Labels[types.LabelName])
	}
}

func TestConvertPendingPoints(t *testing.T) {
	var client Client

	client.maxPendingPoints = 50
	client.maxBatchSize = 5
	bp, _ := influxDBClient.NewBatchPoints(influxDBClient.BatchPointsConfig{
		Database:  client.dataBaseName,
		Precision: "s",
	})
	client.influxDBBatchPoints = bp
	metricPoints := make([]types.MetricPoint, 50)

	for i := range metricPoints {
		metricPoints[i] = types.MetricPoint{
			Point: types.Point{
				Time:  time.Date(2009, 11, 17, 20, 34, 58, 651387237, time.UTC),
				Value: 4.2,
			},
			Labels: map[string]string{
				types.LabelName: fmt.Sprintf("MetricPoint%v", i),
			},
		}
	}

	client.addPoints(metricPoints)

	if len(client.gloutonPendingPoints) != 50 {
		t.Errorf("len(client.gloutonPendingPoints) = %v want 50", len(client.gloutonPendingPoints))
	}

	if client.gloutonPendingPoints[0].Labels[types.LabelName] != metricName0 {
		t.Errorf("client.gloutonPendingPoints[0].Labels[%s] = %s want MetricPoint0", types.LabelName, client.gloutonPendingPoints[1].Labels[types.LabelName])
	}

	if client.gloutonPendingPoints[49].Labels[types.LabelName] != metricName49 {
		t.Errorf("client.gloutonPendingPoints[49].Labels[%s] = %s want MetricPoint49", types.LabelName, client.gloutonPendingPoints[1].Labels[types.LabelName])
	}

	client.convertPendingPoints()

	if len(client.gloutonPendingPoints) != 45 {
		t.Errorf("len(client.gloutonPendingPoints) = %v want 45", len(client.gloutonPendingPoints))
	}

	if client.gloutonPendingPoints[0].Labels[types.LabelName] != metricName5 {
		t.Errorf("client.gloutonPendingPoints[0].Labels[%s] = %s want MetricPoint5", types.LabelName, client.gloutonPendingPoints[1].Labels[types.LabelName])
	}

	if client.gloutonPendingPoints[44].Labels[types.LabelName] != metricName49 {
		t.Errorf("client.gloutonPendingPoints[44].Labels[%s] = %s want MetricPoint49", types.LabelName, client.gloutonPendingPoints[1].Labels[types.LabelName])
	}

	points := client.influxDBBatchPoints.Points()
	if len(points) != 5 {
		t.Errorf("len(points) = %v want 5", len(points))
	}

	for i, pt := range points {
		if pt.Name() != fmt.Sprintf("MetricPoint%v", i) {
			t.Errorf("points[%v].Name() = %v want MetricPoint%v", i, pt.Name(), i)
		}
	}
}
