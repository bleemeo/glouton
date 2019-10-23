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
	"context"
	"fmt"
	"glouton/store"
	"glouton/types"
	"glouton/logger"
	"math"
	"sync"
	"time"

	influxDBClient "github.com/influxdata/influxdb1-client/v2"
)

// Client is an MQTT client for Bleemeo Cloud platform
type Client struct {
	serverAddress        string
	dataBaseName         string
	influxClient         influxDBClient.Client
	store                *store.Store
	lock                 sync.Mutex
	gloutonPendingPoints []types.MetricPoint
	influxDBBatchPoints  influxDBClient.BatchPoints
}

// New create a new influxDB client
func New(serverAddress, dataBaseName string, storeAgent *store.Store) *Client {
	return &Client{
		serverAddress: serverAddress,
		dataBaseName:  dataBaseName,
		influxClient:  nil,
		store:         storeAgent,
	}
}

// doConnect connects an influxDB client to the server and returns true if the connection is established
func (c *Client) doConnect() error {
	// Create the influxBD client
	influxClient, err := influxDBClient.NewHTTPClient(influxDBClient.HTTPConfig{
		Addr: c.serverAddress,
	})
	if err != nil {
		logger.V(1).Printl("Error creating InfluxDB Client: ", err.Error())
		return err
	}
	logger.V(1).Printl("Connexion influxDB succed")
	c.influxClient = influxClient

	// Create the database
	query := influxDBClient.Query{
		Command: fmt.Sprintf("CREATE DATABASE %s", c.dataBaseName),
	}
	response, err := influxClient.Query(query)
	if err == nil && response.Error() == nil {
		logger.V(1).Printl("Database created: ", response.Results)
		bp, _ := influxDBClient.NewBatchPoints(influxDBClient.BatchPointsConfig{
			Database:  c.dataBaseName,
			Precision: "s",
		})
		c.influxDBBatchPoints = bp
		return nil
	}

	// If the database creation failed we print and return the error
	if response.Error() != nil {
		logger.V(1).Printl("Error creating InfluxDB DATABASE: ", response.Error())
		return response.Error()
	}
	logger.V(1).Printl("Error creating InfluxDB DATABASE: ", err.Error())
	return err
}

// connect tries to connect the influxDB client to the server and create the database.
// connect retries this operation after a delay if it fails.
func (c *Client) connect(ctx context.Context) {
	var sleepDelay time.Duration = 10 * time.Second
	for ctx.Err() != nil {
		err := c.doConnect()
		if err == nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(sleepDelay * time.Second):
		}
		sleepDelay = time.Duration(math.Min(sleepDelay.Seconds()*2, 300)) * time.Second
	}
}

// addPoints adds metrics points to the the client attribute BleemeopendingPoints
func (c *Client) addPoints(points []types.MetricPoint) {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.gloutonPendingPoints = append(c.gloutonPendingPoints, points...)
}

// convertPendingPoints converts the BleemeoPendingPoints in InfluxDBPendingPoints
func (c *Client) convertPendingPoints() {
	c.lock.Lock()
	defer c.lock.Unlock()
	// For each metricPoint received in gloutonPendingPoints this block creates an influxDBPoint
	// and adds it to the influxDBBatchPoints
	for _, metricPoint := range c.gloutonPendingPoints {
		measurement := metricPoint.Labels["__name__"]
		time := metricPoint.PointStatus.Point.Time
		fields := map[string]interface{}{
			"value": metricPoint.PointStatus.Point.Value,
		}
		tags := make(map[string]string)
		for key, value := range metricPoint.Labels {
			tags[key] = value
		}
		delete(tags, "__name__")
		tags["status"] = metricPoint.PointStatus.StatusDescription.StatusDescription

		pt, err := influxDBClient.NewPoint(measurement, tags, fields, time)
		if err != nil {
			logger.V(1).Printl("Error : impossible to create the influxMetricPoint: ", measurement)
		}
		c.influxDBBatchPoints.AddPoint(pt)
	}
}

// sendPoints sends points and retry when it fails
func (c *Client) sendPoints() error {
	c.lock.Lock()
	defer c.lock.Unlock()
	err := c.influxClient.Write(c.influxDBBatchPoints)

	// If the write function failed we don't refresh the batchPoint and send an error
	// to retry later
	if err != nil {
		logger.V(1).Printl("Error while sending metrics to influxDB server: ", err.Error())
		return err
	}

	// If the write function succed we create a new empty batchPoint
	// to receive the new points
	newBp, _ := influxDBClient.NewBatchPoints(influxDBClient.BatchPointsConfig{
		Database:  c.dataBaseName,
		Precision: "s",
	})
	c.influxDBBatchPoints = newBp
	return nil
}

// Run runs the influxDB service
func (c *Client) Run(ctx context.Context) error {

	// Connect the client to the server and create the database
	c.connect(ctx)

	// Suscribe to the Store to receive the metrics
	c.store.AddNotifiee(c.addPoints)

	// Initialize a ticker of 10 seconds
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for ctx.Err() == nil {
		// Convert the BleemeoPendingPoints in InfluxDBPendingPoints
		c.convertPendingPoints()

		// Send the point to the server
		c.sendPoints()

		// Wait the ticker or the and of the programm
		select {
		case <-ticker.C:
		case <-ctx.Done():
		}
	}
	return nil
}
