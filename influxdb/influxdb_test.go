// Copyright 2015-2019 Bleemeo
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
	"glouton/store"
	"glouton/types"
	"reflect"
	"testing"
	"time"

	influxDBClient "github.com/influxdata/influxdb1-client/v2"
)

func TestConvertMetricPoint(t *testing.T) {
	metricPoint1 := types.MetricPoint{
		PointStatus: types.PointStatus{
			Point: types.Point{
				Time:  time.Date(2009, 11, 17, 20, 34, 58, 651387237, time.UTC),
				Value: 4.2,
			},
			StatusDescription: types.StatusDescription{
				CurrentStatus:     0,
				StatusDescription: "StatusOk",
			},
		},
		Labels: map[string]string{
			"__name__": "metric_test1",
			"type":     "int",
		},
	}

	metricPoint2 := types.MetricPoint{
		PointStatus: types.PointStatus{
			Point: types.Point{
				Time:  time.Date(2009, 11, 17, 20, 34, 58, 651387237, time.UTC),
				Value: 2.4,
			},
			StatusDescription: types.StatusDescription{
				CurrentStatus:     0,
				StatusDescription: "StatusOk",
			},
		},
		Labels: map[string]string{
			"__name__": "metric_test1",
			"unit":     "no unit",
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
	var client = New("localhost", "db_name", store.New(), make(map[string]string))
	client.maxPendingPoints = 3

	metricPoint0 := types.MetricPoint{
		PointStatus: types.PointStatus{
			Point: types.Point{
				Time:  time.Date(2009, 11, 17, 20, 34, 58, 651387237, time.UTC),
				Value: 4.2,
			},
			StatusDescription: types.StatusDescription{
				CurrentStatus:     0,
				StatusDescription: "StatusOk",
			},
		},
		Labels: map[string]string{
			"__name__": "MetricPoint0",
		},
	}

	metricPoint1 := types.MetricPoint{
		PointStatus: types.PointStatus{
			Point: types.Point{
				Time:  time.Date(2009, 11, 17, 20, 34, 58, 651387237, time.UTC),
				Value: 2.4,
			},
			StatusDescription: types.StatusDescription{
				CurrentStatus:     0,
				StatusDescription: "StatusOk",
			},
		},
		Labels: map[string]string{
			"__name__": "MetricPoint1",
		},
	}

	metricPoint2 := types.MetricPoint{
		PointStatus: types.PointStatus{
			Point: types.Point{
				Time:  time.Date(2009, 11, 17, 20, 34, 58, 651387237, time.UTC),
				Value: 1.7,
			},
			StatusDescription: types.StatusDescription{
				CurrentStatus:     0,
				StatusDescription: "StatusOk",
			},
		},
		Labels: map[string]string{
			"__name__": "MetricPoint2",
		},
	}

	var metricPointSlice1 []types.MetricPoint
	metricPointSlice1 = append(metricPointSlice1, metricPoint0, metricPoint1)
	client.addPoints(metricPointSlice1)

	if len(client.gloutonPendingPoints) != 2 {
		t.Errorf("len(client.gloutonPendingPoints) = %v want 2", len(client.gloutonPendingPoints))
	}

	if client.gloutonPendingPoints[0].Labels["__name__"] != "MetricPoint0" {
		t.Errorf("client.gloutonPendingPoints[0] = %s want MetricPoint0", client.gloutonPendingPoints[0].Labels["__name__"])
	}

	if client.gloutonPendingPoints[1].Labels["__name__"] != "MetricPoint1" {
		t.Errorf("client.gloutonPendingPoints[1] = %s want MetricPoint1", client.gloutonPendingPoints[0].Labels["__name__"])
	}

	var metricPointSlice2 []types.MetricPoint
	metricPointSlice2 = append(metricPointSlice2, metricPoint2)
	client.addPoints(metricPointSlice2)

	if len(client.gloutonPendingPoints) != 3 {
		t.Errorf("len(client.gloutonPendingPoints) = %v want 3", len(client.gloutonPendingPoints))
	}

	if client.gloutonPendingPoints[0].Labels["__name__"] != "MetricPoint0" {
		t.Errorf("client.gloutonPendingPoints[0].Labels['__name__'] = %s want MetricPoint0", client.gloutonPendingPoints[0].Labels["__name__"])
	}

	if client.gloutonPendingPoints[1].Labels["__name__"] != "MetricPoint1" {
		t.Errorf("client.gloutonPendingPoints[1].Labels['__name__'] = %s want MetricPoint1", client.gloutonPendingPoints[1].Labels["__name__"])
	}

	if client.gloutonPendingPoints[2].Labels["__name__"] != "MetricPoint2" {
		t.Errorf("client.gloutonPendingPoints[2].Labels['__name__'] : %s want MetricPoint2", client.gloutonPendingPoints[2].Labels["__name__"])
	}

	var metricPointSlice3 []types.MetricPoint
	metricPointSlice3 = append(metricPointSlice3, metricPoint0)
	client.addPoints(metricPointSlice3)

	if len(client.gloutonPendingPoints) != 3 {
		t.Errorf("len(client.gloutonPendingPoints) = %v want 3", len(client.gloutonPendingPoints))
	}

	if client.gloutonPendingPoints[0].Labels["__name__"] != "MetricPoint1" {
		t.Errorf("client.gloutonPendingPoints[0].Labels['__name__'] : %s want MetricPoint1", client.gloutonPendingPoints[0].Labels["__name__"])
	}

	if client.gloutonPendingPoints[1].Labels["__name__"] != "MetricPoint2" {
		t.Errorf("client.gloutonPendingPoints[1].Labels['__name__'] : %s want MetricPoint2", client.gloutonPendingPoints[1].Labels["__name__"])
	}

	if client.gloutonPendingPoints[2].Labels["__name__"] != "MetricPoint0" {
		t.Errorf("client.gloutonPendingPoints[2].Labels['__name__'] : %s want MetricPoint0", client.gloutonPendingPoints[2].Labels["__name__"])
	}

	var metricPointSlice4 []types.MetricPoint
	metricPointSlice4 = append(metricPointSlice3, metricPoint0, metricPoint2, metricPoint1, metricPoint0)
	client.addPoints(metricPointSlice4)

	if len(client.gloutonPendingPoints) != 3 {
		t.Errorf("len(client.gloutonPendingPoints) = %v want 3", len(client.gloutonPendingPoints))
	}

	if client.gloutonPendingPoints[0].Labels["__name__"] != "MetricPoint2" {
		t.Errorf("client.gloutonPendingPoints[0].Labels['__name__'] : %s want MetricPoint2", client.gloutonPendingPoints[0].Labels["__name__"])
	}

	if client.gloutonPendingPoints[1].Labels["__name__"] != "MetricPoint1" {
		t.Errorf("client.gloutonPendingPoints[1].Labels['__name__'] : %s want MetricPoint1", client.gloutonPendingPoints[1].Labels["__name__"])
	}

	if client.gloutonPendingPoints[2].Labels["__name__"] != "MetricPoint0" {
		t.Errorf("client.gloutonPendingPoints[2].Labels['__name__'] : %s want MetricPoint0", client.gloutonPendingPoints[2].Labels["__name__"])
	}
}
