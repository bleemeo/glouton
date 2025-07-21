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

//go:build linux

package process

import (
	"context"
	"sync"
	"time"

	"github.com/bleemeo/glouton/discovery"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/prometheus/registry"
	"github.com/bleemeo/glouton/types"

	common "github.com/ncabatoff/process-exporter"
	"github.com/ncabatoff/process-exporter/proc"
	"github.com/prometheus/client_golang/prometheus"
)

const defaultJitter = 0

// RegisterExporter will create a new prometheus exporter using the specified parameters and adds it to the registry.
func RegisterExporter(
	_ context.Context,
	reg *registry.Registry,
	psLister func() types.ProcIter,
	dynamicDiscovery *discovery.DynamicDiscovery,
	metricsIgnore, serviceIgnore discovery.IgnoredService,
) {
	processExporter := newExporter(psLister, dynamicDiscovery, metricsIgnore, serviceIgnore)
	if processExporter == nil {
		return
	}

	processGatherer := prometheus.NewRegistry()

	err := processGatherer.Register(processExporter)
	if err != nil {
		logger.Printf("Failed to register process-exporter: %v", err)
		logger.Printf("Processes metrics won't be available on /metrics endpoints")
	} else {
		_, err = reg.RegisterGatherer(
			registry.RegistrationOption{
				Description: "process-exporter metrics",
				JitterSeed:  defaultJitter,
			},
			processGatherer,
		)
		if err != nil {
			logger.Printf("Failed to register process-exporter: %v", err)
			logger.Printf("Processes metrics won't be available on /metrics endpoints")
		}
	}

	_, err = reg.RegisterAppenderCallback(
		registry.RegistrationOption{
			Description: "Bleemeo process-exporter metrics",
			JitterSeed:  defaultJitter,
		},
		&bleemeoExporter{exporter: processExporter},
	)
	if err != nil {
		logger.Printf("unable to add processes metrics: %v", err)
	}
}

// newExporter creates a new Prometheus exporter using the specified parameters.
func newExporter(psLister func() types.ProcIter, processQuerier *discovery.DynamicDiscovery, metricsIgnore, serviceIgnore discovery.IgnoredService) *Exporter {
	return &Exporter{
		Source:         psLister,
		ProcessQuerier: processQuerier,
		metricsIgnore:  metricsIgnore,
		serviceIgnore:  serviceIgnore,
	}
}

type processorQuerier interface {
	ProcessServiceInfo(cmdLine []string, pid int, createTime time.Time) (serviceName discovery.ServiceName, containerName string)
}

// Exporter is a Prometheus exporter to export processes metrics.
// It based on github.com/ncabatoff/process-exporter.
type Exporter struct {
	ProcessQuerier processorQuerier
	Source         func() types.ProcIter

	metricsIgnore, serviceIgnore discovery.IgnoredService

	l sync.Mutex

	grouper     *proc.Grouper
	groupActive map[string]bool

	scrapePartialErrors  int
	scrapeProcReadErrors int
	scrapeErrors         int

	cpuSecsDesc              *prometheus.Desc
	numprocsDesc             *prometheus.Desc
	readBytesDesc            *prometheus.Desc
	writeBytesDesc           *prometheus.Desc
	membytesDesc             *prometheus.Desc
	openFDsDesc              *prometheus.Desc
	worstFDRatioDesc         *prometheus.Desc
	startTimeDesc            *prometheus.Desc
	majorPageFaultsDesc      *prometheus.Desc
	minorPageFaultsDesc      *prometheus.Desc
	contextSwitchesDesc      *prometheus.Desc
	numThreadsDesc           *prometheus.Desc
	statesDesc               *prometheus.Desc
	scrapeErrorsDesc         *prometheus.Desc
	scrapeProcReadErrorsDesc *prometheus.Desc
	scrapePartialErrorsDesc  *prometheus.Desc
	threadWchanDesc          *prometheus.Desc
}

func (e *Exporter) init() {
	e.l.Lock()
	defer e.l.Unlock()

	var err error

	if e.grouper != nil {
		return
	}

	e.groupActive = make(map[string]bool)

	if e.numprocsDesc == nil {
		e.numprocsDesc = prometheus.NewDesc(
			"namedprocess_namegroup_num_procs",
			"number of processes in this group",
			[]string{"groupname"},
			nil,
		)
		e.cpuSecsDesc = prometheus.NewDesc(
			"namedprocess_namegroup_cpu_seconds_total",
			"Cpu user usage in seconds",
			[]string{"groupname", "mode"},
			nil,
		)
		e.readBytesDesc = prometheus.NewDesc(
			"namedprocess_namegroup_read_bytes_total",
			"number of bytes read by this group",
			[]string{"groupname"},
			nil,
		)
		e.writeBytesDesc = prometheus.NewDesc(
			"namedprocess_namegroup_write_bytes_total",
			"number of bytes written by this group",
			[]string{"groupname"},
			nil,
		)
		e.majorPageFaultsDesc = prometheus.NewDesc(
			"namedprocess_namegroup_major_page_faults_total",
			"Major page faults",
			[]string{"groupname"},
			nil,
		)
		e.minorPageFaultsDesc = prometheus.NewDesc(
			"namedprocess_namegroup_minor_page_faults_total",
			"Minor page faults",
			[]string{"groupname"},
			nil,
		)
		e.contextSwitchesDesc = prometheus.NewDesc(
			"namedprocess_namegroup_context_switches_total",
			"Context switches",
			[]string{"groupname", "ctxswitchtype"},
			nil,
		)
		e.membytesDesc = prometheus.NewDesc(
			"namedprocess_namegroup_memory_bytes",
			"number of bytes of memory in use",
			[]string{"groupname", "memtype"},
			nil,
		)
		e.openFDsDesc = prometheus.NewDesc(
			"namedprocess_namegroup_open_filedesc",
			"number of open file descriptors for this group",
			[]string{"groupname"},
			nil,
		)
		e.worstFDRatioDesc = prometheus.NewDesc(
			"namedprocess_namegroup_worst_fd_ratio",
			"the worst (closest to 1) ratio between open fds and max fds among all procs in this group",
			[]string{"groupname"},
			nil,
		)
		e.startTimeDesc = prometheus.NewDesc(
			"namedprocess_namegroup_oldest_start_time_seconds",
			"start time in seconds since 1970/01/01 of oldest process in group",
			[]string{"groupname"},
			nil,
		)
		e.numThreadsDesc = prometheus.NewDesc(
			"namedprocess_namegroup_num_threads",
			"Number of threads",
			[]string{"groupname"},
			nil,
		)
		e.statesDesc = prometheus.NewDesc(
			"namedprocess_namegroup_states",
			"Number of processes in states Running, Sleeping, Waiting, Zombie, or Other",
			[]string{"groupname", "state"},
			nil,
		)
		e.scrapeErrorsDesc = prometheus.NewDesc(
			"namedprocess_scrape_errors",
			"general scrape errors: no proc metrics collected during a cycle",
			nil,
			nil,
		)
		e.scrapeProcReadErrorsDesc = prometheus.NewDesc(
			"namedprocess_scrape_procread_errors",
			"incremented each time a proc's metrics collection fails",
			nil,
			nil,
		)
		e.scrapePartialErrorsDesc = prometheus.NewDesc(
			"namedprocess_scrape_partial_errors",
			"incremented each time a tracked proc's metrics collection fails partially, e.g. unreadable I/O stats",
			nil,
			nil,
		)
		e.threadWchanDesc = prometheus.NewDesc(
			"namedprocess_namegroup_threads_wchan",
			"Number of threads in this group waiting on each wchan",
			[]string{"groupname", "wchan"},
			nil,
		)
	}

	e.grouper = proc.NewGrouper(
		matchNamer{
			querier:       e.ProcessQuerier,
			metricsIgnore: e.metricsIgnore,
			serviceIgnore: e.serviceIgnore,
		},
		true,
		false,
		false,
		0,
		false,
		false,
	)

	iter, ok := e.Source().(proc.Iter)
	if !ok {
		logger.V(1).Printf("Unable to read processes information: iter isn't a proc.Iter")

		return
	}

	colErrs, _, err := e.grouper.Update(iter)
	if err != nil {
		logger.V(1).Printf("Unable to read processes information: %v", err)

		return
	}

	e.scrapePartialErrors += colErrs.Partial
	e.scrapeProcReadErrors += colErrs.Read
}

// Describe implement Describe of a Prometheus collector.
//nolint: wsl_v5,gofmt,gofumpt,goimports
func (e *Exporter) Describe(ch chan<- *prometheus.Desc) {
	e.init()

	ch <- e.cpuSecsDesc
	ch <- e.numprocsDesc
	ch <- e.readBytesDesc
	ch <- e.writeBytesDesc
	ch <- e.membytesDesc
	ch <- e.openFDsDesc
	ch <- e.worstFDRatioDesc
	ch <- e.startTimeDesc
	ch <- e.majorPageFaultsDesc
	ch <- e.minorPageFaultsDesc
	ch <- e.contextSwitchesDesc
	ch <- e.numThreadsDesc
	ch <- e.statesDesc
	ch <- e.scrapeErrorsDesc
	ch <- e.scrapeProcReadErrorsDesc
	ch <- e.scrapePartialErrorsDesc
}

// Collect implement Collect of a Prometheus collector.
func (e *Exporter) Collect(ch chan<- prometheus.Metric) {
	e.init()

	e.l.Lock()
	defer e.l.Unlock()

	iter, ok := e.Source().(proc.Iter)
	if !ok {
		e.scrapeErrors++

		return
	}

	permErrs, groups, err := e.grouper.Update(iter)

	e.scrapePartialErrors += permErrs.Partial

	if err != nil {
		e.scrapeErrors++
	} else {
		for gname, gcounts := range groups {
			if gcounts.Procs > 0 {
				e.groupActive[gname] = true
			}

			if !e.groupActive[gname] {
				continue
			}

			// we set the group inactive after testing for inactive group & skipping
			// to allow emitting metrics one last time
			if gcounts.Procs == 0 {
				e.groupActive[gname] = false
			}

			ch <- prometheus.MustNewConstMetric(e.numprocsDesc,
				prometheus.GaugeValue, float64(gcounts.Procs), gname)

			ch <- prometheus.MustNewConstMetric(e.membytesDesc,
				prometheus.GaugeValue, float64(gcounts.Memory.ResidentBytes), gname, "resident")

			ch <- prometheus.MustNewConstMetric(e.membytesDesc,
				prometheus.GaugeValue, float64(gcounts.Memory.VirtualBytes), gname, "virtual")

			ch <- prometheus.MustNewConstMetric(e.membytesDesc,
				prometheus.GaugeValue, float64(gcounts.Memory.VmSwapBytes), gname, "swapped")

			ch <- prometheus.MustNewConstMetric(e.startTimeDesc,
				prometheus.GaugeValue, float64(gcounts.OldestStartTime.Unix()), gname)

			ch <- prometheus.MustNewConstMetric(e.openFDsDesc,
				prometheus.GaugeValue, float64(gcounts.OpenFDs), gname)

			ch <- prometheus.MustNewConstMetric(e.worstFDRatioDesc,
				prometheus.GaugeValue, gcounts.WorstFDratio, gname)

			ch <- prometheus.MustNewConstMetric(e.cpuSecsDesc,
				prometheus.CounterValue, gcounts.CPUUserTime, gname, "user")

			ch <- prometheus.MustNewConstMetric(e.cpuSecsDesc,
				prometheus.CounterValue, gcounts.CPUSystemTime, gname, "system")

			ch <- prometheus.MustNewConstMetric(e.readBytesDesc,
				prometheus.CounterValue, float64(gcounts.ReadBytes), gname)

			ch <- prometheus.MustNewConstMetric(e.writeBytesDesc,
				prometheus.CounterValue, float64(gcounts.WriteBytes), gname)

			ch <- prometheus.MustNewConstMetric(e.majorPageFaultsDesc,
				prometheus.CounterValue, float64(gcounts.MajorPageFaults), gname)

			ch <- prometheus.MustNewConstMetric(e.minorPageFaultsDesc,
				prometheus.CounterValue, float64(gcounts.MinorPageFaults), gname)

			ch <- prometheus.MustNewConstMetric(e.contextSwitchesDesc,
				prometheus.CounterValue, float64(gcounts.CtxSwitchVoluntary), gname, "voluntary")

			ch <- prometheus.MustNewConstMetric(e.contextSwitchesDesc,
				prometheus.CounterValue, float64(gcounts.CtxSwitchNonvoluntary), gname, "nonvoluntary")

			ch <- prometheus.MustNewConstMetric(e.numThreadsDesc,
				prometheus.GaugeValue, float64(gcounts.NumThreads), gname)

			ch <- prometheus.MustNewConstMetric(e.statesDesc,
				prometheus.GaugeValue, float64(gcounts.States.Running), gname, "Running")

			ch <- prometheus.MustNewConstMetric(e.statesDesc,
				prometheus.GaugeValue, float64(gcounts.States.Sleeping), gname, "Sleeping")

			ch <- prometheus.MustNewConstMetric(e.statesDesc,
				prometheus.GaugeValue, float64(gcounts.States.Waiting), gname, "Waiting")

			ch <- prometheus.MustNewConstMetric(e.statesDesc,
				prometheus.GaugeValue, float64(gcounts.States.Zombie), gname, "Zombie")

			ch <- prometheus.MustNewConstMetric(e.statesDesc,
				prometheus.GaugeValue, float64(gcounts.States.Other), gname, "Other")

			for wchan, count := range gcounts.Wchans {
				ch <- prometheus.MustNewConstMetric(e.threadWchanDesc,
					prometheus.GaugeValue, float64(count), gname, wchan)
			}

			ch <- prometheus.MustNewConstMetric(e.membytesDesc,
				prometheus.GaugeValue, float64(gcounts.Memory.ProportionalBytes), gname, "proportionalResident")

			ch <- prometheus.MustNewConstMetric(e.membytesDesc,
				prometheus.GaugeValue, float64(gcounts.Memory.ProportionalSwapBytes), gname, "proportionalSwapped")
		}
	}

	ch <- prometheus.MustNewConstMetric(e.scrapeErrorsDesc,
		prometheus.CounterValue, float64(e.scrapeErrors))

	ch <- prometheus.MustNewConstMetric(e.scrapeProcReadErrorsDesc,
		prometheus.CounterValue, float64(e.scrapeProcReadErrors))

	ch <- prometheus.MustNewConstMetric(e.scrapePartialErrorsDesc,
		prometheus.CounterValue, float64(e.scrapePartialErrors))
}

type matchNamer struct {
	querier       processorQuerier
	metricsIgnore discovery.IgnoredService
	serviceIgnore discovery.IgnoredService
}

func (m matchNamer) MatchAndName(attr common.ProcAttributes) (bool, string) {
	if m.querier == nil {
		return false, ""
	}

	cmdLine := attr.Cmdline
	if len(cmdLine) == 0 {
		cmdLine = []string{attr.Name}
	}

	serviceName, containerName := m.querier.ProcessServiceInfo(cmdLine, attr.PID, attr.StartTime)
	if serviceName == "" {
		return false, ""
	}

	if m.metricsIgnore.IsServiceIgnoredNameAndContainer(string(serviceName), containerName) ||
		m.serviceIgnore.IsServiceIgnoredNameAndContainer(string(serviceName), containerName) {
		return false, ""
	}

	if containerName == "" {
		return true, string(serviceName)
	}

	return true, string(serviceName) + "-" + containerName
}

func (m matchNamer) String() string {
	return "this is required by process-exporter MatchNamer interface but unused"
}
