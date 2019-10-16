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
	"glouton/types"
	"math"
	"time"

	"sync"

	influxDBClient "github.com/influxdata/influxdb1-client/v2"
)

// Point is the influxBD MetricPoint
type Point struct {
	// à vérifier si cette struct n'existe pas dans le clien influxdb
}

// Client is an MQTT client for Bleemeo Cloud platform
type Client struct {
	serverAddress         string
	dataBaseName          string
	influxClient          influxDBClient.Client
	lock                  sync.Mutex
	bleemeoPendingPoints  []types.MetricPoint
	InfluxDBPendingPoints []Point
}

// New create a new influxDB client
func New(serverAddress, dataBaseName string) *Client {
	return &Client{
		serverAddress: serverAddress,
		dataBaseName:  dataBaseName,
		influxClient:  nil,
	}
}

// Connect influxDB client to the server and returns true if the connection is established
func (c *Client) doConnect() (bool, error) {
	// Create the influxBD client
	influxClient, err := influxDBClient.NewHTTPClient(influxDBClient.HTTPConfig{
		Addr: c.serverAddress,
	})
	if err != nil {
		fmt.Println("Error creating InfluxDB Client: ", err.Error())
		return false, err
	}
	fmt.Println("Connexion influxDB succed")
	c.influxClient = influxClient

	// Create the database
	query := influxDBClient.Query{
		Command: fmt.Sprintf("CREATE DATABASE %s", c.dataBaseName),
	}
	response, err := influxClient.Query(query)
	if err == nil && response.Error() == nil {
		fmt.Println("Database created: ", response.Results)
		return true, nil
	}

	// If the database creation failed we print and return the error
	if response.Error() != nil {
		fmt.Println("Error creating InfluxDB DATABASE: ", response.Error())
		return false, response.Error()
	}
	fmt.Println("Error creating InfluxDB DATABASE: ", err.Error())
	return false, err
}

// Try to connect the influxDB client to the server and create the database.
// Retry this operation after a delay if it fails.
func (c *Client) connect(ctx context.Context) {
	var sleepDelay time.Duration = 10 * time.Second
	for ctx.Err() != nil {
		connectionSucced, _ := c.doConnect()
		if connectionSucced == true {
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

// Add metrics points to the the client attribute BleemeopendingPoints
func (c *Client) addPoints(points []types.MetricPoint) {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.bleemeoPendingPoints = append(c.bleemeoPendingPoints, points...)
}

// Convert the BleemeoPendingPoints in InfluxDBPendingPoints
func (c *Client) convertPendingPoints() {
}

// Run the influxDB service
func (c *Client) Run(ctx context.Context) error {

	// Connect the client to the server and create the database
	c.connect(ctx)

	// Suscribe to the Store to receive the metrics
	storeNotifieeID := c.option.Store.AddNotifiee(c.addPoints)

	// Convert the BleemeoPendingPoints in InfluxDBPendingPoints
	c.convertPendingPoints()
	return nil
}
