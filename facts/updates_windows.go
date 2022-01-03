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

package facts

import (
	"context"
	"glouton/logger"
	"io/ioutil"
	"os/user"
	"strconv"
	"strings"
	"time"

	"github.com/go-ole/go-ole"
)

func countUpdates(updateDispatcher *ole.IDispatch, query string) (res int, success bool) {
	searchResultPtr, err := updateDispatcher.CallMethod("Search", query)
	if err != nil {
		logger.V(2).Printf("Couldn't search the updates: %v", err)

		return
	}

	searchResult := searchResultPtr.ToIDispatch()
	if searchResult == nil {
		logger.V(2).Printf("Couldn't create an ISearchResult: %v", err)

		return
	}

	updatesCollectionPtr, err := searchResult.GetProperty("Updates")
	if err != nil {
		logger.V(2).Printf("Couldn't search the updates: %v", err)

		return
	}

	updatesCollection := updatesCollectionPtr.ToIDispatch()
	if updatesCollection == nil {
		logger.V(2).Printf("Couldn't create an IUpdateCollection: %v", err)

		return
	}

	countVariant, err := updatesCollection.GetProperty("Count")
	if err != nil {
		logger.V(2).Printf("Couldn't retrieve the collection count on the interface IUpdateCollection: %v", err)

		return
	}

	return int(countVariant.Val), true
}

func (uf updateFacter) freshness() time.Time {
	return time.Time{}
}

//nolint:nakedret
func (uf updateFacter) pendingUpdates(ctx context.Context) (pendingUpdates int, pendingSecurityUpdates int) {
	pendingUpdates = -1
	pendingSecurityUpdates = -1

	// when running under the LocalService account, we do not have enought permissions to call into the COM object,
	// so we try to read a file that a scheduled task is updating periodically
	user, err := user.Current()
	if err == nil && user.Username == `NT AUTHORITY\LOCAL SERVICE` {
		content, err := ioutil.ReadFile(`C:\ProgramData\glouton\windowsupdate.txt`)
		if err != nil {
			logger.V(2).Printf("Couldn't retrieve Windows Update informations: %v", err)
		}

		splits := strings.Split(string(content), "\r\n")

		// get rid of the trailing newline, if any
		if splits[len(splits)-1] == "" {
			splits = splits[:len(splits)-1]
		}

		if len(splits) != 2 {
			logger.V(2).Printf("Couldn't retrieve Windows Update informations: got %d values, expected 2 in %v", len(splits), splits)

			return
		}

		parsedPendingUpdates, err1 := strconv.Atoi(splits[0])
		parsedPendingSecurityUpdates, err2 := strconv.Atoi(splits[1])

		if err1 != nil || err2 != nil {
			logger.V(2).Printf("Couldn't retrieve Windows Update informations: cannot parse the file with content %v", splits)

			return
		}

		return parsedPendingUpdates, parsedPendingSecurityUpdates
	}

	connection := &ole.Connection{}

	err = connection.Initialize()
	if err != nil {
		logger.V(2).Printf("Couldn't instantiate an OLE connection: %v", err)

		return
	}

	defer connection.Uninitialize()

	errs := connection.Load("Microsoft.Update.Session")
	if errs != nil {
		logger.V(2).Printf("Couldn't load the 'Microsoft.Update.Session' COM object: %v", errs)

		return
	}

	dispatch, err := connection.Dispatch()
	if err != nil {
		logger.V(2).Printf("Couldn't create an OLE dispatcher: %v", err)

		return
	}

	updateSearcherPtr, err := dispatch.Call("CreateUpdateSearcher")
	if err != nil {
		logger.V(2).Printf("Couldn't create an IUpdateSearcher: %v", err)

		return
	}

	updateSearcher := updateSearcherPtr.ToIDispatch()
	if updateSearcher == nil {
		logger.V(2).Printf("Couldn't create an IUpdateSearcher: %v", err)

		return
	}

	var success bool

	pendingUpdates, success = countUpdates(updateSearcher, "IsInstalled = 0 and IsHidden = 0")
	if !success {
		return -1, -1
	}

	// see https://docs.microsoft.com/en-us/previous-versions/windows/desktop/ff357803(v=vs.85)
	// That's the only way I could think of to easily retrieve the number of pending security updates.
	// This should detect CriticalUpdates and SecurityUpdates.
	pendingSecurityUpdates, success = countUpdates(updateSearcher, "(IsInstalled = 0 and IsHidden = 0 and CategoryIDs contains 'E6CF1350-C01B-414D-A61F-263D14D133B4') or (IsInstalled = 0 and IsHidden = 0 and CategoryIDs contains '0FA1201D-4330-4FA8-8AE9-B877473B6441')")
	if !success {
		return -1, -1
	}

	logger.V(2).Println(pendingSecurityUpdates, pendingUpdates)

	return
}
