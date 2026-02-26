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
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/inputs"
	gloutonDisk "github.com/bleemeo/glouton/inputs/disk"
	"github.com/bleemeo/glouton/inputs/internal"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/prometheus/registry"
	"github.com/bleemeo/glouton/types"
	"github.com/bleemeo/glouton/utils/envsetup"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/disk"
	psutilDisk "github.com/shirou/gopsutil/v4/disk"
)

func main() {
	action := flag.String("action", "telegraf", "Default actions to perform")

	slog.Info("Starting...")
	flag.Parse()

	logger.SetLevel(3)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Load configuration & setup hostRootPath in a similar way that agent.go does.
	cfg, _, warnings, err := config.Load(true, true)
	if err != nil {
		slog.Error("fail", slog.String("err", err.Error()))

		return
	}

	hostRootPath := "/"

	if cfg.Container.Type != "" {
		hostRootPath = cfg.DF.HostMountPoint
		// Removing potential trailing slash from hostroot path
		if len(hostRootPath) > len(string(os.PathSeparator)) {
			hostRootPath = strings.TrimSuffix(hostRootPath, string(os.PathSeparator))
		}

		envsetup.SetupContainer(hostRootPath)
	}

	for _, w := range warnings {
		slog.Warn("Config warning", slog.String("msg", w.Error()))
	}

	inputsConfig, err := buildCollectorsConfig(cfg, hostRootPath)
	if err != nil {
		slog.Error("fail", slog.String("err", err.Error()))

		return
	}

	switch *action {
	case "gopsutil":
		doPSUtils(ctx)
	case "telegraf":
		doTelegraf(ctx, inputsConfig)
	case "glouton":
		doGlouton(ctx, inputsConfig)
	default:
		fmt.Println("Valid actions are:")
		fmt.Println(" * gopsutil: list what PS utils view")
		fmt.Println(" * telegraf: list metrics sent by Telegraf, this is what Test_deduplicate use")
		fmt.Println(" * glouton: list metrics sent by Glouton")
	}

}

func doPSUtils(ctx context.Context) {
	pinfo, err := psutilDisk.PartitionsWithContext(ctx, true)
	if err != nil {
		slog.Error("fail", slog.String("err", err.Error()))

		return
	}

	for _, row := range pinfo {
		fmt.Printf("* mountpoint = %s\n", row.Mountpoint)
		fmt.Printf("  device = %s\n", row.Device)
		fmt.Printf("  fstype = %s\n", row.Fstype)
		fmt.Printf("  opts   = %s\n", strings.Join(row.Opts, ", "))
	}
}

func doTelegraf(ctx context.Context, inputsConfig inputs.CollectorConfig) {
	input, ok := telegraf_inputs.Inputs["disk"]
	if !ok {
		slog.Error("disk input disabled in Telegraf")

		return
	}

	diskInput, _ := input().(*disk.Disk)
	diskInput.IgnoreFS = inputsConfig.DFIgnoreFSTypes

	if err := diskInput.Init(); err != nil {
		slog.Error("disk input init failed", slog.String("err", err.Error()))

		return
	}

	tmp := &internal.StoreAccumulator{}
	err := diskInput.Gather(tmp)

	if err != nil {
		slog.Error("Gather() failed", slog.String("err", err.Error()))
	}

	fmt.Printf("\t\t{\n")
	fmt.Printf("\t\t\tname: \"TODO\",\n")
	fmt.Printf("\t\t\tinput: []internal.Measurement{\n")

	for _, row := range tmp.Measurement {
		indent := "\t\t\t\t"
		fmt.Printf("%s{\n", indent)
		fmt.Printf("%s\tName:   %q,\n", indent, row.Name)
		fmt.Printf("%s\tFields: nil,\n", indent) // metric values doesn't matter for this test
		fmt.Printf("%s\tTags: map[string]string{\n", indent)
		for k, v := range row.Tags {
			fmt.Printf("%s\t\t%q: %q,\n", indent, k, v)
		}
		fmt.Printf("%s\t},\n", indent)
		fmt.Printf("%s},\n", indent)
	}

	fmt.Printf("\t\t\t},\n")
	fmt.Printf("\t\t\twant: []internal.Measurement{},\n")
	fmt.Printf("\t\t},\n")
}

type pushFunction func(ctx context.Context, points []types.MetricPoint)

func (f pushFunction) PushPoints(ctx context.Context, points []types.MetricPoint) {
	f(ctx, points)
}

func doGlouton(ctx context.Context, inputsConfig inputs.CollectorConfig) {
	var (
		l         sync.Mutex
		resPoints []types.MetricPoint
	)

	reg, err := registry.New(registry.Option{
		FQDN:        "example.com",
		GloutonPort: "8015",
		PushPoint: pushFunction(func(_ context.Context, points []types.MetricPoint) {
			l.Lock()
			defer l.Unlock()

			resPoints = append(resPoints, points...)
		}),
	})
	if err != nil {
		slog.Error("registry.New failed", slog.String("err", err.Error()))

		return
	}

	input, err := gloutonDisk.New(inputsConfig.DFRootPath, inputsConfig.DFPathMatcher, inputsConfig.DFIgnoreFSTypes)
	if err != nil {
		slog.Error("gloutonDisk.New failed", slog.String("err", err.Error()))

		return
	}

	id, err := reg.RegisterInput(
		registry.RegistrationOption{
			Description: "disk input",
			IsEssential: true,
		},
		input,
	)
	if err != nil {
		slog.Error("RegisterInput failed", slog.String("err", err.Error()))

		return
	}

	reg.InternalRunScrape(ctx, ctx, time.Now(), id)

	indent := ""
	for _, pts := range resPoints {
		delete(pts.Labels, types.LabelInstance)
		fmt.Printf("%s%s: %v,\n", indent, types.LabelsToText(pts.Labels), pts.Value)
	}
}

func buildCollectorsConfig(cfg config.Config, hostRootPath string) (conf inputs.CollectorConfig, err error) {
	diskFilter, err := config.NewDiskIOMatcher(cfg)
	if err != nil {
		slog.Info("warning", slog.String("err", err.Error()))

		return
	}

	return inputs.CollectorConfig{
		DFRootPath:      hostRootPath,
		NetIfMatcher:    config.NewNetworkInterfaceMatcher(cfg),
		IODiskMatcher:   diskFilter,
		DFPathMatcher:   config.NewDFPathMatcher(cfg),
		DFIgnoreFSTypes: cfg.DF.IgnoreFSType,
	}, nil
}
