// Copyright 2015-2025 Bleemeo
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
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	metricfilter "github.com/bleemeo/glouton/agent/metric-filter"
	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/prometheus/exporter/blackbox"
	"github.com/bleemeo/glouton/types"
)

func main() {
	var (
		dnsURL            = flag.String("url", "dns://8.8.8.8/bleemeo.com", "The DNS URL to query")
		expectedContent   = flag.String("expected-content", "", "FailIfNoneMatchesRegexp")
		unexpectedContent = flag.String("unexpected-content", "", "FailIfMatchesRegexp")
		unfiltered        = flag.Bool("skip-filter", false, "Skip default Glouton metric filter")
	)

	flag.Parse()

	logger.SetLevel(3)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	points, err := blackbox.InternalRunProbe(
		ctx,
		types.Monitor{
			URL:              *dnsURL,
			ExpectedContent:  *expectedContent,
			ForbiddenContent: *unexpectedContent,
		},
		time.Time{},
		nil,
	)
	if err != nil {
		slog.Error("Gather failed", slog.String("err", err.Error()))

		return
	}

	if !*unfiltered {
		defaultCfg := config.DefaultConfig()
		filter, err := metricfilter.New(defaultCfg.Metric, false, false, true)
		if err != nil {
			slog.Error("Unable to create metric filter", slog.String("err", err.Error()))

			return
		}

		points = filter.FilterPoints(points, false)
	}

	fmt.Println("")

	pyIdent := "                    "
	for _, pts := range points {
		delete(pts.Labels, types.LabelInstance)
		fmt.Printf("%s%q: %v,\n", pyIdent, types.LabelsToTextNicer(pts.Labels), pts.Value)
		continue
		if pts.Labels["phase"] != "" {
			// python test only support connect phase
			if pts.Labels["phase"] == "connect" {
				fmt.Printf("%s'%s{phase=\"connect\"}': %v,\n", pyIdent, pts.Labels["__name__"], pts.Value)
			}
		} else {
			fmt.Printf("%s\"%s\": %v,\n", pyIdent, pts.Labels["__name__"], pts.Value)
		}
	}
}
