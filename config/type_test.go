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

package config

import (
	"reflect"
	"testing"
)

func TestConvertMap(t *testing.T) {
	type Want struct {
		Exit map[string]string
		Err  error
	}

	cases := []struct {
		Entry string
		Want  Want
	}{
		{
			Entry: "hostname=bonjour,agent_uuid=1234",
			Want: Want{
				Exit: map[string]string{
					"hostname":   "bonjour",
					"agent_uuid": "1234",
				},
				Err: nil,
			},
		},
		{
			Entry: "value=1234.5,agent_uuid=1234",
			Want: Want{
				Exit: map[string]string{
					"value":      "1234.5",
					"agent_uuid": "1234",
				},
				Err: nil,
			},
		},
		{
			Entry: "value=1234,5,agent_uuid=1234",
			Want: Want{
				Exit: make(map[string]string),
				Err:  ErrWrongMapFormat,
			},
		},
		{
			Entry: "  value=1234 , agent_uuid=1234",
			Want: Want{
				Exit: map[string]string{
					"value":      "1234",
					"agent_uuid": "1234",
				},
				Err: nil,
			},
		},
		{
			Entry: "  hostname=bleemeo=glouton,agent_uuid=1234",
			Want: Want{
				Exit: map[string]string{
					"hostname":   "bleemeo=glouton",
					"agent_uuid": "1234",
				},
				Err: nil,
			},
		},
		{
			Entry: "  hostname=bleemeo,agent_uuid=1234,",
			Want: Want{
				Exit: map[string]string{
					"hostname":   "bleemeo",
					"agent_uuid": "1234",
				},
				Err: nil,
			},
		},
	}
	for _, c := range cases {
		mapResult, errResult := convertMap(c.Entry)
		if !reflect.DeepEqual(mapResult, c.Want.Exit) {
			t.Errorf("convertMap(%s) == %v, want %v", c.Entry, mapResult, c.Want.Exit)
		}

		if !reflect.DeepEqual(errResult, c.Want.Err) {
			t.Errorf("convertMap(%s) == '%v', want '%v'", c.Entry, errResult, c.Want.Err)
		}
	}
}
