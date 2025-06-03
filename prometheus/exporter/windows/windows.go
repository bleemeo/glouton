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

//go:build windows

package windows

import (
	"maps"
	"reflect"
	"regexp"
	"slices"
	"time"
	"unsafe"

	"github.com/bleemeo/glouton/inputs"
	"github.com/bleemeo/glouton/logger"

	"github.com/prometheus-community/windows_exporter/pkg/collector"
	"github.com/prometheus/client_golang/prometheus"
)

const maxScrapeDuration = 9500 * time.Millisecond

func makeColConfig(options inputs.CollectorConfig) collector.Config {
	var collConfig collector.Config

	if options.IODiskMatcher != nil {
		collConfig.LogicalDisk.VolumeExclude = regexp.MustCompile(options.IODiskMatcher.AsDenyRegexp())
	}

	if options.NetIfMatcher != nil {
		collConfig.Net.NicExclude = regexp.MustCompile(options.NetIfMatcher.AsDenyRegexp())
	}

	return collConfig
}

func NewCollector(enabledCollectors []string, options inputs.CollectorConfig) (prometheus.Collector, error) {
	return newCollector(enabledCollectors, options)
}

func newCollector(enabledCollectors []string, options inputs.CollectorConfig) (*collector.Handler, error) {
	collection := collector.NewWithConfig(makeColConfig(options))

	slogger := logger.NewSlog().With("collector", "windows_exporter")

	handler, err := collection.NewHandler(maxScrapeDuration, slogger, enabledCollectors)
	if err != nil {
		logger.V(0).Printf("windows_exporter: couldn't build the list of collectors: %s", err)

		return nil, err
	}

	rh := reflect.ValueOf(handler).Elem()
	rfn := rh.FieldByName("collection").Elem()
	rfn = reflect.NewAt(rfn.Type(), unsafe.Pointer(rfn.UnsafeAddr())).Elem()
	rfs := rfn.FieldByName("collectors")
	rfs = reflect.NewAt(rfs.Type(), unsafe.Pointer(rfs.UnsafeAddr())).Elem()

	if collectors, ok := rfs.Interface().(collector.Map); ok {
		logger.V(2).Printf("windows_exporter: the enabled collectors are %v", slices.Collect(maps.Keys(collectors)))
	} else {
		logger.V(0).Printf("Unexpected collectors type: %T", rfs.Interface())
	}

	return handler, nil
}
