// Copyright 2015-2021 Bleemeo
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

package scrapper

import (
	"net/url"
	"testing"
)

func Test_Host_Port(t *testing.T) {
	url, _ := url.Parse("https://example.com:8080")
	want := "example.com:8080"

	got := HostPort(url)

	if got != want {
		t.Errorf("An error occurred: expected %s, got %s", want, got)
	}
}
