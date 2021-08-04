// Copyright 2015-2019 Bleemeo
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

// +build linux

package process

import (
	"glouton/discovery"
	"glouton/logger"
	"glouton/prometheus/registry"
	"glouton/types"
	"sync"
	"time"

	common "github.com/ncabatoff/process-exporter"
	"github.com/ncabatoff/process-exporter/proc"
	"github.com/prometheus/client_golang/prometheus"
)

// RegisterExporter will create a new prometheus exporter using the specified parameters and adds it to the registry.
func RegisterExporter(reg *registry.Registry, psLister interface{}, dynamicDiscovery *discovery.DynamicDiscovery, bleemeoFormat bool) {
	if processExporter := NewExporter(psLister, dynamicDiscovery); processExporter != nil {
		processGatherer := prometheus.NewRegistry()

		err := processGatherer.Register(processExporter)
		if err != nil {
			logger.Printf("Failed to register process-exporter: %v", err)
			logger.Printf("Processes metrics won't be available on /metrics endpoints")
		} else {
			_, err = reg.RegisterGatherer(processGatherer, nil, nil, !bleemeoFormat)
			if err != nil {
				logger.Printf("Failed to register process-exporter: %v", err)
				logger.Printf("Processes metrics won't be available on /metrics endpoints")
			}
		}

		if bleemeoFormat {
			reg.AddPushPointsCallback(processExporter.PushTo(reg.WithTTL(5 * time.Minute)))
		}
	}
}

// NewExporter creates a new Prometheus exporter using the specified parameters.
func NewExporter(psLister interface{}, processQuerier *discovery.DynamicDiscovery) *Exporter {
	if source, ok := psLister.(*Processes); ok {
		return &Exporter{
			Source:         source,
			ProcessQuerier: processQuerier,
		}
	}

	return nil
}

type processerQuerier interface {
	ProcessServiceInfo(cmdLine []string, pid int, createTime time.Time) (serviceName discovery.ServiceName, containerName string)
}

// Exporter is a Prometheus exporter to export processes metrics.
// It based on github.com/ncabatoff/process-exporter.
type Exporter struct {
	ProcessQuerier processerQuerier
	Source         proc.Source

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

// PushTo return a callback function that will push points on each call.
func (e *Exporter) PushTo(p types.PointPusher) func(time.Time) {
	return (&pusher{
		exporter: e,
		pusher:   p,
	}).push
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
		matchNamer{querier: e.ProcessQuerier},
		true,
		false,
		false,
		false,
	)

	colErrs, _, err := e.grouper.Update(e.Source.AllProcs())
	if err != nil {
		logger.V(1).Printf("Unable to read processes information: %v", err)

		return
	}

	e.scrapePartialErrors += colErrs.Partial
	e.scrapeProcReadErrors += colErrs.Read
}

// Describe implment Describe of a Prometheus collector.
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

// Collect implment Collect of a Prometheus collector.
func (e *Exporter) Collect(ch chan<- prometheus.Metric) {
	e.init()

	e.l.Lock()
	defer e.l.Unlock()

	permErrs, groups, err := e.grouper.Update(e.Source.AllProcs())

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
	querier processerQuerier
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

	if containerName == "" {
		return true, string(serviceName)
	}

	return true, string(serviceName) + "-" + containerName
}

func (m matchNamer) String() string {
	return "this is required by process-exporter MatchNamer interface but unused"
}
