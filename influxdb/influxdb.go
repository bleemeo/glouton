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

package influxdb

import (
	"context"
	"fmt"
	"glouton/logger"
	"glouton/store"
	"glouton/types"
	"math"
	"sync"
	"time"

	influxDBClient "github.com/influxdata/influxdb1-client/v2"
)

const (
	defaultMaxPendingPoints = 100000
	defaultBatchSize        = 1000
)

// Client is an influxdb client for Bleemeo Cloud platform.
type Client struct {
	serverAddress       string
	dataBaseName        string
	store               *store.Store
	influxDBBatchPoints influxDBClient.BatchPoints
	additionalTags      map[string]string
	maxPendingPoints    int
	maxBatchSize        int
	sendPointsState     struct {
		err       error
		hasChange bool
	}

	lock                 sync.Mutex
	gloutonPendingPoints []types.MetricPoint
	influxClient         influxDBClient.Client
}

// New create a new influxDB client.
func New(serverAddress, dataBaseName string, storeAgent *store.Store, additionalTags map[string]string) *Client {
	return &Client{
		serverAddress:    serverAddress,
		dataBaseName:     dataBaseName,
		influxClient:     nil,
		store:            storeAgent,
		additionalTags:   additionalTags,
		maxPendingPoints: defaultMaxPendingPoints,
		maxBatchSize:     defaultBatchSize,
	}
}

// doConnect connects an influxDB client to the server and returns true if the connection is established.
func (c *Client) doConnect() error {
	// Create the influxBD client
	if c.influxClient == nil {
		influxClient, err := influxDBClient.NewHTTPClient(influxDBClient.HTTPConfig{
			Addr: c.serverAddress,
		})
		if err != nil {
			return err
		}

		c.influxClient = influxClient

		logger.V(2).Printf("InfluxDB client created")
	}

	// Test the conectivity to the server
	_, _, pingErr := c.influxClient.Ping(5 * time.Second)
	if pingErr != nil {
		return pingErr
	}

	// Create the database
	query := influxDBClient.Query{
		Command: fmt.Sprintf("CREATE DATABASE %s", c.dataBaseName),
	}
	answer, err := c.influxClient.Query(query)
	// If the query creation failed
	if err != nil {
		return err
	}

	// If the answer failed we print and return the error
	if answer.Error() != nil {
		return answer.Error()
	}

	// If the query and the answer succed the database is created and we create a BatchPoints
	bp, _ := influxDBClient.NewBatchPoints(influxDBClient.BatchPointsConfig{
		Database:  c.dataBaseName,
		Precision: "s",
	})
	c.influxDBBatchPoints = bp

	logger.V(2).Printf("Database created: %s", c.dataBaseName)

	return nil
}

// connect tries to connect the influxDB client to the server and create the database.
// connect retries this operation after a delay if it fails.
func (c *Client) connect(ctx context.Context) {
	sleepDelay := 10 * time.Second

	for ctx.Err() == nil {
		err := c.doConnect()
		if err != nil {
			logger.V(1).Printf("Connexion to the influxdb server '%s' failed. Next attempt in %v: %s", c.serverAddress, sleepDelay, err.Error())

			select {
			case <-ctx.Done():
				logger.V(2).Printf("The context is ended, stop trying to connect to the influxdb server")

				return
			case <-time.After(sleepDelay):
			}

			sleepDelay = time.Duration(math.Min(sleepDelay.Seconds()*2, 300)) * time.Second
		} else {
			logger.V(1).Printf("Connexion to the influxdb server '%s' succed", c.serverAddress)

			return
		}
	}
}

// addPoints adds metrics points to the client attribute BleemeopendingPoints.
func (c *Client) addPoints(points []types.MetricPoint) {
	c.lock.Lock()
	defer c.lock.Unlock()

	switch {
	case len(points) >= c.maxPendingPoints:
		c.gloutonPendingPoints = make([]types.MetricPoint, c.maxPendingPoints)
		copy(c.gloutonPendingPoints, points[len(points)-c.maxPendingPoints:])
	case len(c.gloutonPendingPoints)+len(points) > c.maxPendingPoints:
		c.gloutonPendingPoints = append(c.gloutonPendingPoints[:0], c.gloutonPendingPoints[len(points):]...)
		c.gloutonPendingPoints = append(c.gloutonPendingPoints, points...)
	default:
		c.gloutonPendingPoints = append(c.gloutonPendingPoints, points...)
	}
}

// convertMetricPoint convert a gloutonMetricPoint in influxDBClient.Point.
func convertMetricPoint(metricPoint types.MetricPoint, additionalTags map[string]string) (*influxDBClient.Point, error) {
	measurement := metricPoint.Labels[types.LabelName]
	time := metricPoint.Point.Time
	fields := map[string]interface{}{
		"value": metricPoint.Point.Value,
	}
	tags := make(map[string]string)

	for key, value := range additionalTags {
		tags[key] = value
	}

	for key, value := range metricPoint.Labels {
		tags[key] = value
	}

	delete(tags, types.LabelName)

	return influxDBClient.NewPoint(measurement, tags, fields, time)
}

// convertPendingPoints converts the 1000 older points from BleemeoPendingPoints in InfluxDBPendingPoints.
func (c *Client) convertPendingPoints() {
	c.lock.Lock()
	defer c.lock.Unlock()

	nbFailConversion := 0
	points := c.influxDBBatchPoints.Points()

	if len(points) >= c.maxBatchSize {
		logger.V(2).Printf("The influxDBBatchPoint is already full")

		return
	}

	for i, metricPoint := range c.gloutonPendingPoints {
		pt, err := convertMetricPoint(metricPoint, c.additionalTags)
		if err != nil {
			logger.V(2).Printf("Error: impossible to create an influxMetricPoint, the %s metric won't be sent to the influxdb server", metricPoint.Labels[types.LabelName])
			nbFailConversion++

			continue
		}

		nbConvertPoints := i - nbFailConversion
		if nbConvertPoints >= c.maxBatchSize {
			logger.V(2).Printf("The influxDBBatchPoint is full: stop converting points")

			c.gloutonPendingPoints = append(c.gloutonPendingPoints[:0], c.gloutonPendingPoints[i:]...)

			return
		}

		c.influxDBBatchPoints.AddPoint(pt)
	}

	c.gloutonPendingPoints = c.gloutonPendingPoints[:0]
}

// sendPoints sends points cointain in the influxDBBatchPoint.
func (c *Client) sendPoints() {
	if c.influxClient == nil {
		logger.Printf("influxdbClient is not initialized, impossible to send points to the influxdb server")

		return
	}

	err := c.influxClient.Write(c.influxDBBatchPoints)
	// If the write function failed we don't refresh the batchPoint and we update c.sendPointState
	if err != nil {
		if c.sendPointsState.err != nil {
			c.sendPointsState.err = err
			c.sendPointsState.hasChange = false

			return
		}

		c.sendPointsState.err = err
		c.sendPointsState.hasChange = true

		return
	}

	// If the write function succed we create a new empty batchPoint
	// to receive the new points
	newBp, _ := influxDBClient.NewBatchPoints(influxDBClient.BatchPointsConfig{
		Database:  c.dataBaseName,
		Precision: "s",
	})

	c.influxDBBatchPoints = newBp

	if c.sendPointsState.err != nil {
		c.sendPointsState.err = nil
		c.sendPointsState.hasChange = true

		return
	}

	c.sendPointsState.hasChange = false
}

// sendCheck performs some health checks after running sendPoints and logs the result.
func (c *Client) sendCheck() bool {
	c.lock.Lock()
	defer c.lock.Unlock()

	if c.sendPointsState.err != nil {
		if c.sendPointsState.hasChange {
			logger.Printf("Fail to send the metrics to the influxdb server: %s", c.sendPointsState.err.Error())
		} else {
			logger.V(2).Printf("Fail to send the metrics to the influxdb server: %s", c.sendPointsState.err.Error())
		}

		return true
	}

	if c.sendPointsState.hasChange {
		logger.Printf("All waiting points have been sent to the influxdb server")
	}

	return false
}

// HealthCheck perform some health check and logger any issue found.
func (c *Client) HealthCheck() bool {
	c.lock.Lock()
	defer c.lock.Unlock()

	ok := true

	if c.influxClient != nil {
		_, _, pingErr := c.influxClient.Ping(5 * time.Second)
		if pingErr != nil {
			ok = false

			logger.Printf("Bleemeo connection influxdb server is currently not responding")
		}
	} else {
		logger.Printf("influxClient is not initialized, impossible to contact the influxdb server")
	}

	if len(c.gloutonPendingPoints) > defaultBatchSize {
		logger.Printf("%d points are waiting to be sent to the influxdb server", len(c.gloutonPendingPoints))
	}

	if len(c.gloutonPendingPoints) >= defaultMaxPendingPoints {
		logger.Printf("%d points are waiting to be sent to the influxdb server. Older points are being dropped", len(c.gloutonPendingPoints))
	}

	return ok
}

// lenGloutonPendingPoints return the len of the slice c.gloutonPendingPoints.
func (c *Client) lenGloutonPendingPoints() int {
	c.lock.Lock()
	defer c.lock.Unlock()

	return len(c.gloutonPendingPoints)
}

// Run runs the influxDB service.
func (c *Client) Run(ctx context.Context) error {
	// Connect the client to the server and create the database
	c.connect(ctx)

	// Suscribe to the Store to receive the metrics
	c.store.AddNotifiee(c.addPoints)

	// Initialize a ticker of 10 seconds
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for ctx.Err() == nil {
		for c.lenGloutonPendingPoints() > 0 {
			// Convert the BleemeoPendingPoints in InfluxDBPendingPoints
			c.convertPendingPoints()

			// Send the point to the server
			// If sendPoints fail we retry after a tick
			c.sendPoints()

			breakstate := c.sendCheck()
			if breakstate {
				break
			}
		}

		// Wait the ticker or the and of the programm
		select {
		case <-ticker.C:
		case <-ctx.Done():
		}
	}

	return nil
}
