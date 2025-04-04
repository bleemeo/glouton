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

package jmxtrans

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/discovery"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/types"
)

type jmxtransClient struct {
	Connection *net.TCPConn
	Config     configInterface
	EmitPoint  func(context.Context, types.MetricPoint)

	lastPurgeAt time.Time
	lastTime    time.Time
	// The following 4 are used to metric that require some computation like
	// derivated, ratio & sum
	pendingSum        map[nameItem][]metricInfo
	pendingRatio      map[nameItem]metricInfo
	underivatedValues map[nameItem]metricInfo
	valuesForRatio    map[nameItem]metricInfo
}

type nameItem struct {
	Name string
	Item string
}

type metricInfo struct {
	Service     discovery.Service
	Metric      config.JmxMetric
	Labels      map[string]string
	Annotations types.MetricAnnotations
	Timestamp   time.Time
	Value       float64
	UsedInRatio bool
}

type configInterface interface {
	GetService(md5Service string) (discovery.Service, bool)
	GetMetrics(md5Service string, md5Bean string, attr string) (metrics []config.JmxMetric, usedInRatio bool)
}

func (c *jmxtransClient) init() {
	c.pendingSum = make(map[nameItem][]metricInfo)
	c.pendingRatio = make(map[nameItem]metricInfo)
	c.underivatedValues = make(map[nameItem]metricInfo)
	c.valuesForRatio = make(map[nameItem]metricInfo)

	// avoir nil-pointer dereference
	if c.EmitPoint == nil {
		c.EmitPoint = func(context.Context, types.MetricPoint) {}
	}
}

func (c *jmxtransClient) Run(ctx context.Context) {
	logger.V(1).Printf("New jmxtrans connection from %s", c.Connection.RemoteAddr())

	c.init()

	var buffer []byte

	readBuffer := make([]byte, 4096)

	for ctx.Err() == nil {
		err := c.Connection.SetDeadline(time.Now().Add(3 * time.Second))
		if err != nil {
			logger.V(1).Printf("setdeadline error on jmxtrans connection: %v", err)

			break
		}

		n, err := c.Connection.Read(readBuffer)
		if err != nil {
			if errNet, ok := err.(net.Error); ok && errNet.Timeout() {
				continue
			}

			if errors.Is(err, io.EOF) {
				logger.V(1).Printf("read error on jmxtrans connection: %v", err)
			}

			break
		}

		buffer = append(buffer, readBuffer[0:n]...)

		lines := bytes.Split(buffer, []byte{'\n'})

		for i, line := range lines {
			if i == len(lines)-1 {
				// last line is not yet terminated, re-add it to buffer.
				buffer = line

				continue
			}

			c.processLine(ctx, string(line))
		}

		c.purge()
	}

	c.flush(ctx)

	logger.V(2).Printf("Closing jmxtrans connection from %s", c.Connection.RemoteAddr())
	_ = c.Connection.Close()
}

func (c *jmxtransClient) processLine(ctx context.Context, line string) {
	parts := strings.Split(line, " ")
	if len(parts) != 3 {
		return
	}

	value, err := strconv.ParseFloat(parts[1], 64)
	if err != nil {
		return
	}

	timestamp, err := strconv.ParseInt(parts[2], 10, 0)
	if err != nil {
		return
	}

	lineTime := time.Unix(timestamp, 0)

	if !c.lastTime.IsZero() && math.Abs(lineTime.Sub(c.lastTime).Seconds()) > 1 {
		c.flush(ctx)
	}

	c.lastTime = lineTime

	var (
		linePrefix string
		md5Service string
		md5Bean    string
		attr       string
		typeNames  string
	)

	metricParts := strings.Split(parts[0], ".")
	switch len(metricParts) {
	case 4:
		linePrefix = metricParts[0]
		md5Service = metricParts[1]
		md5Bean = metricParts[2]
		attr = metricParts[3]
	case 5:
		linePrefix = metricParts[0]
		md5Service = metricParts[1]
		md5Bean = metricParts[2]
		typeNames = metricParts[3]
		attr = metricParts[4]
	default:
		return
	}

	if linePrefix != graphitePrefix {
		logger.V(2).Printf("wrong line prefix %#v", linePrefix)

		return
	}

	service, ok := c.Config.GetService(md5Service)
	if !ok {
		logger.V(2).Printf("service not found for hash %s", md5Service)

		return
	}

	metrics, usedInRatio := c.Config.GetMetrics(md5Service, md5Bean, attr)
	if len(metrics) == 0 {
		logger.V(5).Printf("metric not found for %s, %s, %s", md5Service, md5Bean, attr)

		return
	}

	for _, metric := range metrics {
		name := fmt.Sprintf("%s_%s", service.Name, metric.Name)

		var item string

		finalValue := value

		switch {
		case service.Instance != "" && typeNames != "":
			item = service.Instance + "_" + typeNames
		case typeNames != "":
			item = typeNames
		default:
			item = service.Instance
		}

		labels := map[string]string{
			types.LabelName: name,
		}
		if item != "" {
			labels[types.LabelItem] = item
		}

		annotations := types.MetricAnnotations{
			ServiceName:     service.Name,
			ServiceInstance: service.Instance,
			ContainerID:     service.ContainerID,
		}

		if metric.Derive {
			finalValue, ok = c.deriveValue(name, item, finalValue, lineTime)
			if !ok {
				continue
			}
		}

		if metric.Scale != 0 {
			finalValue *= metric.Scale
		}

		switch {
		case metric.Sum:
			// we are summing over typesName, drop them from item
			item = service.Instance

			if item != "" {
				labels[types.LabelItem] = item
			} else {
				delete(labels, types.LabelItem)
			}

			key := nameItem{
				Name: name,
				Item: item,
			}

			c.pendingSum[key] = append(c.pendingSum[key], metricInfo{
				Service:     service,
				Metric:      metric,
				Labels:      labels,
				Annotations: annotations,
				Timestamp:   lineTime,
				Value:       finalValue,
				UsedInRatio: usedInRatio,
			})
		case metric.Ratio != "":
			key := nameItem{
				Name: name,
				Item: item,
			}

			c.pendingRatio[key] = metricInfo{
				Service:     service,
				Metric:      metric,
				Labels:      labels,
				Annotations: annotations,
				Timestamp:   lineTime,
				Value:       finalValue,
			}
		default:
			c.EmitPoint(ctx, types.MetricPoint{
				Labels:      labels,
				Annotations: annotations,
				Point: types.Point{
					Time:  lineTime,
					Value: finalValue,
				},
			})
		}

		if usedInRatio {
			key := nameItem{
				Name: name,
				Item: item,
			}
			c.valuesForRatio[key] = metricInfo{
				Service:     service,
				Metric:      metric,
				Annotations: annotations,
				Labels:      labels,
				Timestamp:   lineTime,
				Value:       finalValue,
			}
		}
	}
}

func (c *jmxtransClient) deriveValue(name string, item string, value float64, timestamp time.Time) (float64, bool) {
	key := nameItem{name, item}
	previousValue := c.underivatedValues[key]
	c.underivatedValues[key] = metricInfo{
		Timestamp: timestamp,
		Value:     value,
	}

	if previousValue.Timestamp.IsZero() {
		return 0, false
	}

	deltaT := timestamp.Sub(previousValue.Timestamp)

	if deltaT <= 0 {
		return 0, false
	}

	return (value - previousValue.Value) / deltaT.Seconds(), true
}

func (c *jmxtransClient) flush(ctx context.Context) {
	for key, points := range c.pendingSum {
		if len(points) == 0 {
			continue
		}

		sum := 0.0

		for _, pts := range points {
			sum += pts.Value
		}

		if points[0].Metric.Ratio != "" {
			c.pendingRatio[key] = metricInfo{
				Service:     points[0].Service,
				Metric:      points[0].Metric,
				Labels:      points[0].Labels,
				Annotations: points[0].Annotations,
				Timestamp:   points[0].Timestamp,
				Value:       sum,
			}

			continue
		}

		c.EmitPoint(ctx, types.MetricPoint{
			Labels:      points[0].Labels,
			Annotations: points[0].Annotations,
			Point: types.Point{
				Time:  points[0].Timestamp,
				Value: sum,
			},
		})

		if points[0].UsedInRatio {
			c.valuesForRatio[key] = metricInfo{
				Service:   points[0].Service,
				Metric:    points[0].Metric,
				Labels:    points[0].Labels,
				Timestamp: points[0].Timestamp,
				Value:     sum,
			}
		}
	}

	for key := range c.pendingSum {
		delete(c.pendingSum, key)
	}

	for key, point := range c.pendingRatio {
		divisorName := fmt.Sprintf("%s_%s", point.Service.Name, point.Metric.Ratio)
		divisorKey := nameItem{divisorName, key.Item}
		divisor := c.valuesForRatio[divisorKey]

		if divisor.Timestamp.IsZero() || math.Abs(point.Timestamp.Sub(divisor.Timestamp).Seconds()) > 1 {
			logger.V(2).Printf("can't compute ratio for metric %s due to missing or outdated divisor value", key.Name)

			continue
		}

		value := 0.0

		if divisor.Value != 0 {
			value = point.Value / divisor.Value
		}

		c.EmitPoint(ctx, types.MetricPoint{
			Labels:      point.Labels,
			Annotations: point.Annotations,
			Point: types.Point{
				Time:  point.Timestamp,
				Value: value,
			},
		})
	}

	for key := range c.pendingRatio {
		delete(c.pendingRatio, key)
	}
}

func (c *jmxtransClient) purge() {
	if time.Since(c.lastPurgeAt) < time.Minute {
		return
	}

	c.lastPurgeAt = time.Now()
	cutoff := time.Now().Add(-6 * time.Minute)

	for key, point := range c.underivatedValues {
		if point.Timestamp.Before(cutoff) {
			delete(c.underivatedValues, key)
		}
	}

	for key, point := range c.valuesForRatio {
		if point.Timestamp.Before(cutoff) {
			delete(c.underivatedValues, key)
		}
	}
}
