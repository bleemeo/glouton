package registry

import (
	"fmt"
	"glouton/logger"
	"regexp/syntax"
	"strings"
	_ "unsafe" // using hack with go linkname to access private variable :)

	"github.com/go-kit/kit/log"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/node_exporter/collector"
	"gopkg.in/alecthomas/kingpin.v2"
)

// NodeExporterOption are options for node_exporter. If absent, the default of node_exporter will be used.
// All Ingored string are a regular expression
type NodeExporterOption struct {
	RootFS                       string
	FilesystemIgnoredMountPoints string
	NetworkIgnoredDevices        string
	DiskStatsIgnoredDevices      string
	EnabledCollectors            []string
}

//go:linkname collectorState github.com/prometheus/node_exporter/collector.collectorState
var collectorState map[string]*bool // nolint: gochecknoglobals

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

// nodeExporterCollector return a node_exporter
func nodeExporterCollector(option NodeExporterOption) (prometheus.Collector, error) {
	var args []string

	if option.RootFS != "" {
		args = append(args, fmt.Sprintf("--path.rootfs=%s", option.RootFS))
	}
	if option.FilesystemIgnoredMountPoints != "" {
		args = append(args, fmt.Sprintf("--collector.filesystem.ignored-mount-points=%s", option.FilesystemIgnoredMountPoints))
	}
	if option.NetworkIgnoredDevices != "" {
		args = append(args, fmt.Sprintf("--collector.netclass.ignored-devices=%s", option.NetworkIgnoredDevices))
		args = append(args, fmt.Sprintf("--collector.netdev.device-blacklist=%s", option.NetworkIgnoredDevices))
	}
	if option.DiskStatsIgnoredDevices != "" {
		args = append(args, fmt.Sprintf("--collector.diskstats.ignored-devices=%s", option.DiskStatsIgnoredDevices))
	}

	if _, err := kingpin.CommandLine.Parse(args); err != nil {
		return nil, fmt.Errorf("kingpin initialization: %v", err)
	}

	setCollector(option.EnabledCollectors)

	l := log.NewNopLogger()
	return collector.NewNodeCollector(l)
}

// WithPathIgnore set the of mount points to ignore.
// It use path-prefix, which means that if "/mnt" is to ignore, "/mnt" and "/mnt/disk" are ignored, but not "/mnt-disk"
// If any error occur, the ignored paths is not updated
func (o *NodeExporterOption) WithPathIgnore(prefixes []string) *NodeExporterOption {
	var err error

	res := make([]string, len(prefixes))
	for i, p := range prefixes {
		res[i], err = reFromPathPrefix(p)
		if err != nil {
			return o
		}
	}

	re, err := reFromREs(res)
	if err != nil {
		return o
	}
	o.FilesystemIgnoredMountPoints = re
	return o
}

// WithNetworkIgnore set the of device prefixes to ignore.
// If any error occur, the list is not updated
func (o *NodeExporterOption) WithNetworkIgnore(prefixes []string) *NodeExporterOption {
	var err error

	res := make([]string, len(prefixes))
	for i, p := range prefixes {
		res[i], err = reFromPrefix(p)
		if err != nil {
			return o
		}
	}

	re, err := reFromREs(res)
	if err != nil {
		return o
	}
	o.NetworkIgnoredDevices = re
	return o
}

// reFromPrefix return a regular expression matching any prefixes in the input
// e.g. ReFromPrefixes("eth"}) will match eth, eth0, ...
func reFromPrefix(prefix string) (string, error) {
	re, err := syntax.Parse(prefix, syntax.Literal)
	return "^" + re.String(), err
}

// reFromPathPrefix return a regular expression matching any path prefix in the input
// By path-prefix, we means that the path-prefix "/mnt" will match "/mnt", "/mnt/disk" but not "/mnt-disk"
// Only "/" is supported (e.g. only unix)
func reFromPathPrefix(prefix string) (string, error) {
	re, err := syntax.Parse(prefix, syntax.Literal)
	return "^" + re.String() + "($|/)", err
}

// reFromREs return an RE matching any of the input REs
func reFromREs(input []string) (string, error) {
	var err error

	res := make([]*syntax.Regexp, len(input))

	for i, v := range input {
		res[i], err = syntax.Parse(v, syntax.Perl)
		if err != nil {
			return "", err
		}
	}

	re := syntax.Regexp{
		Op:    syntax.OpAlternate,
		Flags: syntax.Perl,
		Sub:   res,
	}
	return re.String(), nil
}
