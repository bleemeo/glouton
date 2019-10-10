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
	bleemeoTypes "glouton/bleemeo/types"
	"glouton/types"

	"sync"
)

// Option contians parameters for le influxDB client
type Option struct {
	bleemeoTypes.GlobalOption
}

// Metric point for influxDB
type InfluxDBPoint struct {
	// à vérifier si cette struct n'existe pas dans le clien influx db
}

// Client is an MQTT client for Bleemeo Cloud platform
type Client struct {
	option Option

	lock                  sync.Mutex
	bleemeoPendingPoints  []types.MetricPoint
	InfluxDBPendingPoints []InfluxDBPoint
}

// New create a new influxDB client
func New(option Option) *Client {
}

// Initialize the parameters to communicate with influxDB server
func (c *Client) setupInfluxDB() error {
}

// Connect influxDB client to the server and returns true if the connection is established
func (c *Client) connect() bool {
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
func (c *Client) run() error {
	// Initialize the server parameters
	if err := c.setupInfluxDB(); err != nil {
		return err
	}
	// Connect the client to the server
	c.connect()

	// Suscribe to the Store to receive the metrics
	storeNotifieeID := c.option.Store.AddNotifiee(c.addPoints)

	// Convert the BleemeoPendingPoints in InfluxDBPendingPoints
	c.convertPendingPoints()
}
