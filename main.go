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
	"os"
	"runtime"
	"strings"

	_ "net/http/pprof" //nolint: gosec
)

//nolint: gochecknoglobals
var (
	runAsRoot   = flag.Bool("yes-run-as-root", false, "Allows Glouton to run as root")
	configFiles = flag.String("config", "", "Configuration files/dirs to load.")
	showVersion = flag.Bool("version", false, "Show version and exit")
)

//nolint: gochecknoglobals
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

	var (
		postInstall       bool
		installConfigPath string
		account           string
		registration      string
	)

	if runtime.GOOS == "windows" {
		// flags related to the installation
		flag.BoolVar(&postInstall, "post-install", false, "Run the post-install step")
		flag.StringVar(&installConfigPath, "install-config-path", "", "Config file used to store the account id and registration key")
		flag.StringVar(&account, "account-id", "", "Account ID")
		flag.StringVar(&registration, "registration-key", "", "Registration key of your account")
	}

	flag.Parse()

	if *showVersion {
		fmt.Println(versionPkg.Version)
		return
	}

	if os.Getuid() == 0 && !*runAsRoot {
		fmt.Println("Error: trying to run Glouton as root without \"--yes-run-as-root\" option.")
		fmt.Println("If Glouton is installed using standard method, start it with:")
		fmt.Println("    service glouton start")
		fmt.Println("")
		os.Exit(1)
	}

	if postInstall {
		if installConfigPath == "" {
			fmt.Println("No config file specified, cannot install the agent configuration")
			return
		}

		_, err := os.Stat(installConfigPath)
		if err == nil {
			fmt.Println("The config file already exists, doing nothing")
			return
		}

		fd, err := os.Create(installConfigPath)
		if err != nil {
			fmt.Printf("Couldn't open the config file: %v.\n", err)
			os.Exit(1)
		}

		fmt.Fprintf(fd, "bleemeo:\n  account_id: %s\n  registration_key: %s", account, registration)

		fd.Close()

		return
	}

	agent.Run(strings.Split(*configFiles, ","))
}
