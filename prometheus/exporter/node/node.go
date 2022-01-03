package node

import (
	"fmt"
	"glouton/logger"
	"glouton/prometheus/exporter/buildinfo"
	"glouton/prometheus/exporter/common"
	"glouton/version"
	"strings"
	_ "unsafe" // using hack with go linkname to access private variable :)

	"github.com/go-kit/log"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/node_exporter/collector"
	"gopkg.in/alecthomas/kingpin.v2"
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

// NewCollector return a node_exporter.
func NewCollector(option Option) (prometheus.Collector, error) {
	var args []string

	if option.RootFS != "" {
		args = append(args, fmt.Sprintf("--path.rootfs=%s", option.RootFS))
	}

	if option.FilesystemIgnoredMountPoints != "" {
		args = append(args, fmt.Sprintf("--collector.filesystem.ignored-mount-points=%s", option.FilesystemIgnoredMountPoints))
	}

	if option.FilesystemIgnoredType != "" {
		args = append(args, fmt.Sprintf("--collector.filesystem.ignored-fs-types=%s", option.FilesystemIgnoredType))
	}

	if option.NetworkIgnoredDevices != "" {
		args = append(args, fmt.Sprintf("--collector.netclass.ignored-devices=%s", option.NetworkIgnoredDevices))
		args = append(args, fmt.Sprintf("--collector.netdev.device-blacklist=%s", option.NetworkIgnoredDevices))
	}

	if option.DiskStatsIgnoredDevices != "" {
		args = append(args, fmt.Sprintf("--collector.diskstats.ignored-devices=%s", option.DiskStatsIgnoredDevices))
	}

	logger.V(2).Printf("Starting node_exporter with %v as args", args)

	if _, err := kingpin.CommandLine.Parse(args); err != nil {
		return nil, fmt.Errorf("kingpin initialization: %w", err)
	}

	setCollector(option.EnabledCollectors)

	l := log.NewNopLogger()

	c, err := collector.NewNodeCollector(l)
	if err != nil {
		return nil, err
	}

	return buildinfo.AddBuildInfo(c, "node_exporter", "1.0.0-rc.0", version.BuildHash, "glouton"), nil
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
func (o *Option) WithPathIgnoreFSType(prefixes []string) *Option {
	var err error

	re, err := common.ReFromREs(prefixes)
	if err != nil {
		return o
	}

	o.FilesystemIgnoredType = re

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
