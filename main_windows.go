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
	"os"
	"path/filepath"
)

//nolint:gochecknoglobals
var (
	// flags related to the installation.
	postInstall       = flag.Bool("post-install", false, "Run the post-install step")
	basedir           = flag.String("basedir", "", "Base directory of configuration, state file and logs")
	configFileSubpath = flag.String("config-file-subpath", "", "Path of the config file used to store the account id and registration key, relative to basedir")
	account           = flag.String("account-id", "", "Account ID")
	registration      = flag.String("registration-key", "", "Registration key of your account")
)

//nolint:forbidigo
func OSDependentMain() {
	if !*postInstall {
		return
	}

	defer os.Exit(0)

	if *configFileSubpath == "" || *basedir == "" {
		fmt.Println("No config file specified, cannot install the agent configuration")

		return
	}

	configFilePath := filepath.Join(*basedir, *configFileSubpath)

	if _, err := os.Stat(configFilePath); err == nil {
		fmt.Println("The config file already exists, doing nothing")

		return
	}

	fd, err := os.Create(configFilePath)
	if err != nil {
		fmt.Printf("Couldn't open the config file: %v.\n", err)

		return
	}

	fmt.Fprintf(fd, "bleemeo:\n  account_id: %s\n  registration_key: %s", *account, *registration)

	fd.Close()
}
