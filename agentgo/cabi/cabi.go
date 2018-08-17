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

// Exposes a C-ABI for agentgo

package main

import (
	"../system"
	"C"
)

type MetricPoint struct {
	// Value of the metric
	Value int
}

//export MetricPoint
type MetricPoint _Ctype_MetricPoint

//export FunctionTest
func FunctionTest() int64 { return 42 }

//export Test
func Test() MetricPoint { return MetricPoint{42} }

func main() {}
