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

package main

import (
	"flag"
	"fmt"
	"glouton/agent"
	versionPkg "glouton/version"
	"log"
	"strings"

	_ "net/http/pprof" //nolint:gosec

	"github.com/getsentry/sentry-go"
)

//nolint:gochecknoglobals
var (
	configFiles = flag.String("config", "", "Configuration files/dirs to load.")
	showVersion = flag.Bool("version", false, "Show version and exit")
)

//nolint:gochecknoglobals
var (
	version string
	commit  string
)

func main() {
	if version != "" {
		versionPkg.Version = version
	}

	if commit != "" {
		versionPkg.BuildHash = commit
	}

	flag.Parse()

	if *showVersion {
		fmt.Println(versionPkg.Version) //nolint:forbidigo

		return
	}

	sentry.ConfigureScope(func(scope *sentry.Scope) {
		scope.SetContext("agent", map[string]interface{}{
			"glouton_version": versionPkg.Version,
		})
	})

	err := sentry.Init(sentry.ClientOptions{
		Dsn: "https://55b4938036a1488ca0362792a77ac3e2@errors.bleemeo.work/4",
	})
	if err != nil {
		log.Fatalf("sentry.Init: %s", err)
	}

	// run os-specific initialisation codd
	OSDependentMain()

	agent.Run(strings.Split(*configFiles, ","))
}
