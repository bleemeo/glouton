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

// Package for nginx input

package nginx

/*
import (
	"errors"
	"github.com/influxdata/telegraf"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/nginx"
)

// initNginxInput initialize the nginx input
func initNginxInput(url string) (telegraf.Input, error) {
	input := telegraf_inputs.Inputs["nginx"]()
	nginxInput, ok := input.(*nginx.Nginx)
	if ok {
		slice := append(make([]string, 0), url)
		nginxInput.Urls = slice
		nginxInput.InsecureSkipVerify = false
		return input, nil
	}
	return nil, errors.New("Failed to initialize nginx input")
}
*/
