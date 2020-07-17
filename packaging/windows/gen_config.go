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
	"fmt"
	"os"

	"gopkg.in/alecthomas/kingpin.v2"
)

const filePath string = `C:\ProgramData\glouton\glouton.conf.d\30-install.conf`

var (
	account      = kingpin.Flag("account", "Account ID.").Required().String()  //nolint
	registration = kingpin.Flag("key", "Registration key").Required().String() //nolint
)

func main() {
	kingpin.Parse()

	_, err := os.Stat(filePath)
	if err == nil {
		fmt.Println("The config file already exists, doing nothing")
		return
	}

	fd, err := os.Create(filePath)
	if err != nil {
		fmt.Printf("Could open the config file: %v.\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(fd,
		`bleemeo:
  account_id: %s
  registration_key: %s`,
		*account,
		*registration)

	fd.Close()
}
