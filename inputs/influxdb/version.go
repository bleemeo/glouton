// Copyright 2015-2026 Bleemeo
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

package influxdb

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/bleemeo/glouton/version"
)

// line is which major line of InfluxDB a server belongs to. It decides where the metrics
// are read from, because the three lines share no endpoint:
//
//   - 1.x publishes InfluxDB-formatted JSON on /debug/vars, which is the only place its
//     own counters exist. Its /metrics holds the Go client library's default registry and
//     nothing about InfluxDB at all.
//   - 2.x has no /debug/vars (404) and publishes Prometheus text on /metrics.
//   - 3.x publishes Prometheus text on /metrics too, under entirely different names.
type line int

const (
	lineUnknown line = iota
	lineV1
	lineV2
	lineV3
)

func (l line) String() string {
	switch l {
	case lineV1:
		return "1.x"
	case lineV2:
		return "2.x"
	case lineV3:
		return "3.x"
	case lineUnknown:
		return "unknown"
	default:
		return "unknown"
	}
}

// versionHeader is the header every line sets on /ping. 1.x and 2.x answer that route
// unauthenticated even with authentication enabled, which is what makes them tellable
// apart at all: their command line is the same "influxd" and carries nothing to go on.
const versionHeader = "X-Influxdb-Version"

// detectLine asks the server which line it belongs to, and returns the version string it
// reported for the logs.
//
// The token is sent when there is one and ignored by 1.x and 2.x. A 401 is an answer
// rather than a failure: only 3.x authenticates /ping, so being refused there identifies
// it just as well as the header would.
func detectLine(ctx context.Context, pingURL string, token string) (line, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pingURL, nil)
	if err != nil {
		return lineUnknown, "", fmt.Errorf("prepare request to %s: %w", pingURL, err)
	}

	req.Header.Set("User-Agent", version.UserAgent())

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return lineUnknown, "", fmt.Errorf("read from %s: %w", pingURL, err)
	}

	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	reported := resp.Header.Get(versionHeader)

	if resp.StatusCode == http.StatusUnauthorized {
		// Nothing else refuses /ping. The version stays unknown until a token works, which
		// is fine: the line is what decides where to read from.
		return lineV3, reported, nil
	}

	if resp.StatusCode/100 != 2 {
		return lineUnknown, reported, fmt.Errorf("%w: %s returned status %d", errUnexpectedStatus, pingURL, resp.StatusCode)
	}

	return lineFromVersion(reported), reported, nil
}

// lineFromVersion reads the major out of the version string. 1.x reports it bare
// ("1.12.4") and 2.x with a leading v ("v2.9.1"), so the prefix is trimmed rather than
// matched on.
func lineFromVersion(reported string) line {
	major, _, _ := strings.Cut(strings.TrimPrefix(strings.TrimSpace(reported), "v"), ".")

	switch major {
	case "1":
		return lineV1
	case "2":
		return lineV2
	case "3":
		return lineV3
	default:
		return lineUnknown
	}
}
