// Copyright 2015-2023 Bleemeo
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

package config

import (
	"fmt"
	"glouton/logger"
	"glouton/prometheus/exporter/common"
	"regexp"
	"strings"
)

type DiskIOMatcher struct {
	denylistRE  []*regexp.Regexp
	allowlistRE []*regexp.Regexp
}

type NetworkInterfaceMatcher struct {
	denylist []string
}

type DFPathMatcher struct {
	denylist []string
}

type DFFSTypeMatcher struct {
	denylistRE []*regexp.Regexp
}

func NewDiskIOMatcher(config Config) (DiskIOMatcher, error) {
	var (
		err    error
		result DiskIOMatcher
	)

	result.denylistRE, err = common.CompileREs(config.DiskIgnore)
	if err != nil {
		return result, fmt.Errorf("%w: failed to compile regexp in disk_ignore: %s", ErrInvalidValue, err)
	}

	result.allowlistRE, err = common.CompileREs(config.DiskMonitor)
	if err != nil {
		return result, fmt.Errorf("%w: failed to compile regexp in disk_monitor: %s", ErrInvalidValue, err)
	}

	return result, nil
}

func (f DiskIOMatcher) Match(disk string) bool {
	if len(f.denylistRE) > 0 {
		for _, r := range f.denylistRE {
			if r.MatchString(disk) {
				return false
			}
		}

		return true
	}

	for _, r := range f.allowlistRE {
		if r.MatchString(disk) {
			return true
		}
	}

	return false
}

// AsDenyRegexp return a regexp as string that should be used as denylist:
// any value that match this regexp should be ignored.
// If the denylist is empty, this regexp won't correspond to Match() method.
func (f DiskIOMatcher) AsDenyRegexp() string {
	if len(f.denylistRE) == 0 {
		return "^$" // match nothing.
	}

	// this will not fail, as we checked the validity of this regexp earlier
	denylist, _ := common.MergeREs(f.denylistRE)

	return denylist
}

func NewNetworkInterfaceMatcher(config Config) NetworkInterfaceMatcher {
	return NetworkInterfaceMatcher{denylist: config.NetworkInterfaceDenylist}
}

func (f NetworkInterfaceMatcher) Match(netif string) bool {
	for _, b := range f.denylist {
		if strings.HasPrefix(netif, b) {
			return false
		}
	}

	return true
}

func (f NetworkInterfaceMatcher) AsDenyRegexp() string {
	if len(f.denylist) == 0 {
		return "^$"
	}

	denylistREs := make([]string, 0, len(f.denylist))

	for _, inter := range f.denylist {
		denylistRE, err := common.ReFromPrefix(inter)
		if err != nil {
			logger.V(1).Printf("windows_exporter: failed to parse the network interface denylist: %v", err)
		} else {
			denylistREs = append(denylistREs, denylistRE)
		}
	}

	// this will not fail, as we checked the validity of every regexp earlier
	denylist, _ := common.ReFromREs(denylistREs)

	return denylist
}

func NewDFPathMatcher(config Config) DFPathMatcher {
	pathIgnoreTrimed := make([]string, len(config.DF.PathIgnore))

	for i, v := range config.DF.PathIgnore {
		pathIgnoreTrimed[i] = strings.TrimRight(v, "/")
	}

	return DFPathMatcher{denylist: pathIgnoreTrimed}
}

func (f DFPathMatcher) Match(path string) bool {
	for _, v := range f.denylist {
		if v == path || strings.HasPrefix(path, v+"/") {
			return false
		}
	}

	return true
}

func NewDFFSTypeMatcher(config Config) (DFFSTypeMatcher, error) {
	var (
		err    error
		result DFFSTypeMatcher
	)

	result.denylistRE, err = common.CompileREs(config.DF.IgnoreFSType)
	if err != nil {
		return result, fmt.Errorf("%w: failed to compile regexp in disk_ignore: %s", ErrInvalidValue, err)
	}

	return result, nil
}

func (f DFFSTypeMatcher) Match(fsType string) bool {
	for _, r := range f.denylistRE {
		if r.MatchString(fsType) {
			return false
		}
	}

	return true
}

func (f DFFSTypeMatcher) AsDenyRegexp() string {
	if len(f.denylistRE) == 0 {
		return "^$" // match nothing.
	}

	// this will not fail, as we checked the validity of this regexp earlier
	denylist, _ := common.MergeREs(f.denylistRE)

	return denylist
}
