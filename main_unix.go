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

// +build !windows

package main

import (
	"flag"
	"fmt"
	"os"
)

//nolint:gochecknoglobals
var (
	runAsRoot = flag.Bool("yes-run-as-root", false, "Allows Glouton to run as root")
)

// OSDependentMain is the function used as a main when glouton is used on an unix Os.
//nolint:forbidigo
func OSDependentMain() {
	if os.Getuid() == 0 && !*runAsRoot {
		fmt.Println("Error: trying to run Glouton as root without \"--yes-run-as-root\" option.")
		fmt.Println("If Glouton is installed using standard method, start it with:")
		fmt.Println("    service glouton start")
		fmt.Println("")
		os.Exit(1)
	}
}
