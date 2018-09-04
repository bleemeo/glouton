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

// Package for mysql input

package mysql

/*
import (
	"errors"
	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/mysql"
)

// initMysqlInput initialize the mysql input
func initMysqlInput(server string) (telegraf.Input, error) {
	input := telegraf_inputs.Inputs["mysql"]()
	mysqlInput, ok := input.(*mysql.Mysql)
	if ok {
		slice := append(make([]string, 0), server)
		mysqlInput.Servers = slice
		return input, nil
	}
	return nil, errors.New("Failed to initialize mysql input")
}
*/
