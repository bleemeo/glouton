// Copyright 2015-2022 Bleemeo
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
	"os/user"
)

//nolint:gochecknoglobals
var runWithoutLocalService = flag.Bool("yes-run-without-local-service", false, "Allows Glouton to run as another user than 'NT AUTHORITY\\LocalService'")

// OSDependentMain is the main function used on Windows.
//
//nolint:forbidigo
func OSDependentMain() {
	// NT Authority (LocalService) Security Identifier.
	// https://docs.microsoft.com/en-us/windows-server/identity/ad-ds/manage/understand-security-identifiers
	const localServiceSID = "S-1-5-19"

	user, err := user.Current()
	if err != nil {
		fmt.Printf("Failed to get current user: %s, Glouton may be started with the wrong user.\n", err)

		return
	}

	if user.Uid != localServiceSID && !*runWithoutLocalService {
		fmt.Printf("Error: trying to run Glouton as %s without \"--yes-run-without-local-service\" option.\n", user.Username)
		fmt.Println("Running Glouton with another user is not supported if it was installed using the installer.")
		fmt.Println("You should be able to start the Glouton service with:")
		fmt.Println("    net start glouton")
		fmt.Println("")
		os.Exit(1)
	}
}
