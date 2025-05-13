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

package node

import (
	"fmt"
	"log/slog"
	"strings"
	_ "unsafe" // using hack with go linkname to access private variable :)

	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/prometheus/exporter/buildinfo"
	"github.com/bleemeo/glouton/prometheus/exporter/common"
	"github.com/bleemeo/glouton/types"
	"github.com/bleemeo/glouton/version"

	"github.com/alecthomas/kingpin/v2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/node_exporter/collector"
)

// Option are options for node_exporter. If absent, the default of node_exporter will be used.
// All Ingored string are a regular expression.
type Option struct {
	RootFS                       string
	FilesystemIgnoredMountPoints string
	FilesystemIgnoredType        string
	NetworkIgnoredDevices        string
	DiskStatsIgnoredDevices      string
	EnabledCollectors            []string
}

//go:linkname collectorState github.com/prometheus/node_exporter/collector.collectorState
var collectorState map[string]*bool //nolint:gochecknoglobals

func setCollector(collectorName []string) {
	var unknown []string

	logger.V(2).Printf("collectorState from node_exporter is %v", collectorState)

	if len(collectorName) == 0 {
		return
	}

	collector.DisableDefaultCollectors()

	for _, name := range collectorName {
		if collectorState[name] == nil {
			unknown = append(unknown, name)

			continue
		}

		*collectorState[name] = true
	}

	if len(unknown) > 0 {
		logger.Printf("Unknown node_exporter collector(s): %s", strings.Join(unknown, " "))
	}
}

func optionsToFlags(option Option) map[string]string {
	result := make(map[string]string)

	if option.RootFS != "" {
		result["path.rootfs"] = option.RootFS
	}

	if option.FilesystemIgnoredMountPoints != "" {
		result["collector.filesystem.ignored-mount-points"] = option.FilesystemIgnoredMountPoints
	}

	if option.FilesystemIgnoredType != "" {
		result["collector.filesystem.ignored-fs-types"] = option.FilesystemIgnoredType
	}

	if option.NetworkIgnoredDevices != "" {
		if version.IsLinux() {
			result["collector.netclass.ignored-devices"] = option.NetworkIgnoredDevices
		}

		result["collector.netdev.device-exclude"] = option.NetworkIgnoredDevices
	}

	if option.DiskStatsIgnoredDevices != "" {
		result["collector.diskstats.ignored-devices"] = option.DiskStatsIgnoredDevices
	}

	return result
}

func setKingpinOptions(option Option) error {
	optionMap := optionsToFlags(option)
	args := make([]string, 0, len(optionMap))

	for key, value := range optionMap {
		args = append(args, fmt.Sprintf("--%s=%s", key, value))
	}

	logger.V(2).Printf("Starting node_exporter with %v as args", args)

	if _, err := kingpin.CommandLine.Parse(args); err != nil {
		return fmt.Errorf("kingpin initialization: %w", err)
	}

	setCollector(option.EnabledCollectors)

	return nil
}

// NewCollector return a node_exporter.
func NewCollector(option Option) (prometheus.Collector, error) {
	c, err := newCollector(option)
	if err != nil {
		return nil, err
	}

	return buildinfo.AddBuildInfo(c, "node_exporter", "1.0.0-rc.0", version.BuildHash, "glouton"), nil
}

// newCollector return a node_exporter.
func newCollector(option Option) (*collector.NodeCollector, error) {
	if err := setKingpinOptions(option); err != nil {
		return nil, err
	}

	c, err := collector.NewNodeCollector(slog.New(slog.DiscardHandler))
	if err != nil {
		return nil, err
	}

	return c, nil
}

// WithPathIgnore set the of mount points to ignore.
// It use path-prefix, which means that if "/mnt" is to ignore, "/mnt" and "/mnt/disk" are ignored, but not "/mnt-disk"
// If any error occur, the ignored paths is not updated.
func (o *Option) WithPathIgnore(prefixes []string) *Option {
	var err error

	res := make([]string, len(prefixes))
	for i, p := range prefixes {
		res[i], err = common.ReFromPathPrefix(p)
		if err != nil {
			return o
		}
	}

	re, err := common.ReFromREs(res)
	if err != nil {
		return o
	}

	o.FilesystemIgnoredMountPoints = re

	return o
}

// WithDiskIgnore set the of disk device to ignore.
func (o *Option) WithDiskIgnore(prefixes []string) *Option {
	var err error

	re, err := common.ReFromREs(prefixes)
	if err != nil {
		return o
	}

	o.DiskStatsIgnoredDevices = re

	return o
}

// WithPathIgnoreFSType set the of filesystem type to ignore.
func (o *Option) WithPathIgnoreFSType(matcher types.MatcherRegexp) *Option {
	o.FilesystemIgnoredType = matcher.AsDenyRegexp()

	return o
}

// WithNetworkIgnore set the of device prefixes to ignore.
// If any error occur, the list is not updated.
func (o *Option) WithNetworkIgnore(prefixes []string) *Option {
	var err error

	res := make([]string, len(prefixes))
	for i, p := range prefixes {
		res[i], err = common.ReFromPrefix(p)
		if err != nil {
			return o
		}
	}

	re, err := common.ReFromREs(res)
	if err != nil {
		return o
	}

	o.NetworkIgnoredDevices = re

	return o
}
