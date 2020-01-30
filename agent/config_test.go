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

package agent

import (
	"reflect"
	"testing"
)

func Test_confFieldToSliceMap(t *testing.T) {
	var confInterface1 interface{} = []interface{}{
		map[interface{}]interface{}{
			"name": "mysql",
		},
		map[interface{}]interface{}{
			"name":     "postgres",
			"instance": "host:* container:*",
		},
		map[interface{}]interface{}{
			"id":            "myapplication",
			"port":          "8080",
			"check_type":    "nagios",
			"check_command": "command-to-run",
		},
		map[interface{}]interface{}{
			"id":         "custom_webserver",
			"port":       "8181",
			"check_type": "http",
		},
	}

	var confInterface2 interface{} = []interface{}{
		map[interface{}]interface{}{
			"name":     "postgres",
			"instance": "host:* container:*",
		},
		"wrong type",
		42,
	}

	type args struct {
		input    interface{}
		confType string
	}
	tests := []struct {
		name string
		args args
		want []map[string]string
	}{
		{
			name: "test1",
			args: args{
				input:    confInterface1,
				confType: "service_ignore_check",
			},
			want: []map[string]string{
				{
					"name": "mysql",
				},
				{
					"name":     "postgres",
					"instance": "host:* container:*",
				},
				{
					"id":            "myapplication",
					"port":          "8080",
					"check_type":    "nagios",
					"check_command": "command-to-run",
				},
				{
					"id":         "custom_webserver",
					"port":       "8181",
					"check_type": "http",
				},
			},
		},
		{
			name: "test2",
			args: args{
				input:    confInterface2,
				confType: "service_ignore_check",
			},
			want: []map[string]string{
				{
					"name":     "postgres",
					"instance": "host:* container:*",
				},
			},
		},
		{
			name: "test3",
			args: args{
				input:    "failed test",
				confType: "",
			},
			want: nil,
		},
		{
			name: "test4",
			args: args{
				input:    []interface{}{"first", "second"},
				confType: "",
			},
			want: []map[string]string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := confFieldToSliceMap(tt.args.input, tt.args.confType); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("confFieldToSliceMap() = %v, want %v", got, tt.want)
			}
		})
	}
}
