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

package vsphere

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/bleemeo/bleemeo-go"
	bleemeoTypes "github.com/bleemeo/glouton/bleemeo/types"
	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/inputs"
	"github.com/bleemeo/glouton/inputs/internal"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/prometheus/registry"
	"github.com/bleemeo/glouton/types"

	telegraf_config "github.com/influxdata/telegraf/config"
	telegraf_inputs "github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/inputs/vsphere"
	dto "github.com/prometheus/client_model/go"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25"
	"google.golang.org/protobuf/proto"
)

const commonTimeout = 10 * time.Second

const (
	vCenterConsecutiveErrorsStatusThreshold = 2
	noMetricsStatusThreshold                = 5
)

// A common label value.
const instanceTotal = "instance-total"

type labelsMetadata struct {
	datastorePerLUN    map[string]string
	disksPerVM         map[string]map[string]string
	netInterfacesPerVM map[string]map[string]string
}

type vSphere struct {
	host string
	opts config.VSphere

	state        bleemeoTypes.State
	factProvider bleemeoTypes.FactProvider

	realtimeGatherer        *vSphereGatherer
	historical30minGatherer *vSphereGatherer

	hierarchy        *Hierarchy
	deviceCache      map[string]bleemeoTypes.VSphereDevice
	devicePropsCache *propsCaches
	labelsMetadata   labelsMetadata
	lastStatuses     map[string]types.Status
	lastErrorMessage string
	consecutiveErr   int

	l sync.Mutex
}

func newVSphere(host string, cfg config.VSphere, state bleemeoTypes.State, factProvider bleemeoTypes.FactProvider) *vSphere {
	return &vSphere{
		host:             host,
		opts:             cfg,
		state:            state,
		factProvider:     factProvider,
		hierarchy:        NewHierarchy(),
		deviceCache:      make(map[string]bleemeoTypes.VSphereDevice),
		devicePropsCache: newPropsCaches(),
		labelsMetadata: labelsMetadata{
			datastorePerLUN:    make(map[string]string),
			disksPerVM:         make(map[string]map[string]string),
			netInterfacesPerVM: make(map[string]map[string]string),
		},
		lastStatuses: make(map[string]types.Status),
	}
}

// Takes the lock.
func (vSphere *vSphere) setErr(err error) {
	vSphere.l.Lock()
	defer vSphere.l.Unlock()

	if err == nil {
		vSphere.lastErrorMessage = ""
		vSphere.consecutiveErr = 0
	} else {
		vSphere.lastErrorMessage = err.Error()
		vSphere.consecutiveErr++
	}
}

func (vSphere *vSphere) getStatus() (types.Status, string) {
	if vSphere.consecutiveErr >= vCenterConsecutiveErrorsStatusThreshold {
		return types.StatusCritical, vSphere.lastErrorMessage
	}

	switch {
	case vSphere.realtimeGatherer != nil && vSphere.realtimeGatherer.lastErr != nil:
		return types.StatusCritical, "realtime endpoint error: " + vSphere.realtimeGatherer.lastErr.Error()
	case vSphere.historical30minGatherer != nil && vSphere.historical30minGatherer.lastErr != nil:
		return types.StatusCritical, "historical 30min endpoint error: " + vSphere.historical30minGatherer.lastErr.Error()
	}

	return types.StatusOk, ""
}

func (vSphere *vSphere) String() string {
	return fmt.Sprintf("vSphere(%s)", vSphere.host)
}

func (vSphere *vSphere) devices(ctx context.Context, deviceChan chan<- bleemeoTypes.VSphereDevice) {
	findCtx, cancelFind := context.WithTimeout(ctx, commonTimeout)
	defer cancelFind()

	t0 := time.Now()

	finder, client, err := newDeviceFinder(findCtx, vSphere.opts)
	if err != nil {
		vSphere.setErr(err)
		logger.V(1).Printf("Can't create vSphere client for %q: %v", vSphere.host, err)

		return
	}

	clusters, datastores, resourcePools, hosts, vms, err := findDevices(findCtx, finder, true)
	if err != nil {
		vSphere.setErr(err)
		logger.V(1).Printf("Can't find devices on vSphere %q: %v", vSphere.host, err)

		return
	}

	logger.V(2).Printf("On vSphere %q, found %d clusters, %d hosts and %d vms in %v.", vSphere.host, len(clusters), len(hosts), len(vms), time.Since(t0))

	scraperFacts, err := vSphere.factProvider.Facts(ctx, time.Hour)
	if err != nil {
		vSphere.setErr(err)

		return
	}

	err = vSphere.hierarchy.Refresh(ctx, clusters, resourcePools, hosts, vms, vSphere.devicePropsCache.vmCache)
	if err != nil {
		vSphere.setErr(err)

		return
	}

	var (
		devs           []bleemeoTypes.VSphereDevice
		errs           []error
		labelsMetadata labelsMetadata
	)

	// A more precise context will be given by the function that retrieves the device properties.
	describedClusters, err := vSphere.describeClusters(ctx, client, clusters, scraperFacts)
	devs = append(devs, describedClusters...)
	errs = append(errs, err)

	describedHosts, err := vSphere.describeHosts(ctx, client, hosts, scraperFacts)
	devs = append(devs, describedHosts...)
	errs = append(errs, err)

	if !vSphere.opts.SkipMonitorVMs {
		var describedVMs []bleemeoTypes.VSphereDevice

		describedVMs, labelsMetadata, err = vSphere.describeVMs(ctx, client, vms, scraperFacts)
		devs = append(devs, describedVMs...)
		errs = append(errs, err)
	}

	dsPerLUN, err := getDatastorePerLUN(ctx, client, datastores, vSphere.devicePropsCache.datastoreCache)
	errs = append(errs, err)

	err = errors.Join(append(errs, ctx.Err())...)
	vSphere.setErr(err)

	if err != nil {
		// Don't save a potentially partial list of devices
		return
	}

	vSphere.l.Lock()
	defer vSphere.l.Unlock()

	labelsMetadata.datastorePerLUN = dsPerLUN
	vSphere.labelsMetadata = labelsMetadata
	vSphere.deviceCache = make(map[string]bleemeoTypes.VSphereDevice, len(devs))

	for _, dev := range devs {
		vSphere.deviceCache[dev.MOID()] = dev

		deviceChan <- dev
	}

	vSphere.devicePropsCache.purge()
}

func (vSphere *vSphere) describeClusters(ctx context.Context, client *vim25.Client, rawClusters []*object.ClusterComputeResource, scraperFacts map[string]string) ([]bleemeoTypes.VSphereDevice, error) {
	clusterProps, err := retrieveProps(ctx, client, rawClusters, relevantClusterProperties, vSphere.devicePropsCache.clusterCache)
	if err != nil {
		logger.V(1).Printf("Failed to retrieve cluster props of %s: %v", vSphere.host, err)

		return nil, err
	}

	clusters := make([]bleemeoTypes.VSphereDevice, 0, len(clusterProps))

	for cluster, props := range clusterProps {
		describedCluster := describeCluster(vSphere.host, cluster, props)
		describedCluster.facts["scraper_fqdn"] = scraperFacts["fqdn"]
		clusters = append(clusters, describedCluster)
	}

	return clusters, nil
}

func (vSphere *vSphere) describeHosts(ctx context.Context, client *vim25.Client, rawHosts []*object.HostSystem, scraperFacts map[string]string) ([]bleemeoTypes.VSphereDevice, error) {
	hostProps, err := retrieveProps(ctx, client, rawHosts, relevantHostProperties, vSphere.devicePropsCache.hostCache)
	if err != nil {
		logger.V(1).Printf("Failed to retrieve host props of %s: %v", vSphere.host, err)

		return nil, err
	}

	hosts := make([]bleemeoTypes.VSphereDevice, 0, len(hostProps))

	for host, props := range hostProps {
		describedHost := describeHost(vSphere.host, host, props)
		describedHost.facts["scraper_fqdn"] = scraperFacts["fqdn"]
		hosts = append(hosts, describedHost)
	}

	return hosts, nil
}

func (vSphere *vSphere) describeVMs(ctx context.Context, client *vim25.Client, rawVMs []*object.VirtualMachine, scraperFacts map[string]string) ([]bleemeoTypes.VSphereDevice, labelsMetadata, error) {
	vmProps, err := retrieveProps(ctx, client, rawVMs, relevantVMProperties, vSphere.devicePropsCache.vmCache)
	if err != nil {
		logger.V(1).Printf("Failed to retrieve VM props of %s: %v", vSphere.host, err)

		return nil, labelsMetadata{}, err
	}

	vms := make([]bleemeoTypes.VSphereDevice, 0, len(vmProps))
	labelsMetadata := labelsMetadata{
		disksPerVM:         make(map[string]map[string]string),
		netInterfacesPerVM: make(map[string]map[string]string),
	}

	for vm, props := range vmProps {
		describedVM, disks, netInterfaces := describeVM(vSphere.host, vm, props, vSphere.hierarchy)
		describedVM.facts["scraper_fqdn"] = scraperFacts["fqdn"]
		vms = append(vms, describedVM)
		labelsMetadata.disksPerVM[vm.Reference().Value] = disks
		labelsMetadata.netInterfacesPerVM[vm.Reference().Value] = netInterfaces
	}

	return vms, labelsMetadata, nil
}

func (vSphere *vSphere) makeRealtimeGatherer(ctx context.Context) (registry.GathererWithOrWithoutState, registry.RegistrationOption, error) {
	input, ok := telegraf_inputs.Inputs["vsphere"]
	if !ok {
		return nil, registry.RegistrationOption{}, inputs.ErrDisabledInput
	}

	vsphereInput, ok := input().(*vsphere.VSphere)
	if !ok {
		return nil, registry.RegistrationOption{}, inputs.ErrUnexpectedType
	}

	vsphereInput.Username = telegraf_config.NewSecret([]byte(vSphere.opts.Username))
	vsphereInput.Password = telegraf_config.NewSecret([]byte(vSphere.opts.Password))

	vsphereInput.VMInstances = !vSphere.opts.SkipMonitorVMs
	vsphereInput.HostInstances = true

	if vSphere.opts.SkipMonitorVMs {
		vsphereInput.VMMetricExclude = []string{"*"}
	} else {
		vsphereInput.VMMetricInclude = []string{
			"cpu.usage.average",
			"cpu.latency.average",
			"mem.active.average", // Will be converted to the percentage of used memory
			"mem.swapped.average",
			"net.transmitted.average",
			"net.received.average",
			"virtualDisk.read.average",
			"virtualDisk.write.average",
		}
	}

	vsphereInput.HostMetricInclude = []string{
		"cpu.usage.average",
		"cpu.usagemhz.average", // Will be converted to the percentage for Cluster CPU
		"mem.totalCapacity.average",
		"mem.usage.average",
		"mem.swapin.average",
		"mem.swapout.average",
		"datastore.read.average",
		"datastore.write.average",
		"net.transmitted.average",
		"net.received.average",
	}

	vsphereInput.DatacenterMetricExclude = []string{"*"}
	vsphereInput.ResourcePoolMetricExclude = []string{"*"}
	vsphereInput.ClusterMetricExclude = []string{"*"}
	vsphereInput.DatastoreMetricExclude = []string{"*"}
	vsphereInput.DatacenterInstances = false
	vsphereInput.ResourcePoolInstances = false
	vsphereInput.ClusterInstances = false
	vsphereInput.DatastoreInstances = false

	vsphereInput.InsecureSkipVerify = vSphere.opts.InsecureSkipVerify

	vsphereInput.ObjectDiscoveryInterval = telegraf_config.Duration(2 * time.Minute)

	vsphereInput.Log = logger.NewTelegrafLog(vSphere.String() + " realtime")

	acc := &internal.Accumulator{
		RenameMetrics:    renameMetrics,
		TransformMetrics: vSphere.transformMetrics,
		RenameGlobal:     vSphere.renameGlobal,
	}

	gatherer, err := newGatherer(ctx, gatherRT, &vSphere.opts, vsphereInput, acc, vSphere.hierarchy, vSphere.devicePropsCache)
	if err != nil {
		return nil, registry.RegistrationOption{}, err
	}

	vSphere.realtimeGatherer = gatherer

	noMetricsSinceIterations := 0
	noMetricsSince := make(map[string]int)
	opt := registry.RegistrationOption{
		Description: fmt.Sprint(vSphere, " ", gatherRT),
		// We use the VM agent type because it has the smallest resolution.
		MinInterval:         time.Minute,
		StopCallback:        gatherer.stop,
		ApplyDynamicRelabel: true,
		ExtraLabels: map[string]string{
			types.LabelMetaAgentTypes: fmt.Sprintf("%s,%s,%s", bleemeo.AgentType_vSphereVM, bleemeo.AgentType_vSphereHost, bleemeo.AgentType_vSphereCluster),
		},
		GatherModifier: func(mfs []*dto.MetricFamily, _ error) []*dto.MetricFamily {
			vSphere.purgeNoMetricsSinceMap(noMetricsSince, &noMetricsSinceIterations)

			return vSphere.gatherModifier(mfs, noMetricsSince, map[ResourceKind]bool{KindVM: true, KindHost: true, KindCluster: true})
		},
	}

	return gatherer, opt, nil
}

func (vSphere *vSphere) makeHistorical30minGatherer(ctx context.Context) (registry.GathererWithOrWithoutState, registry.RegistrationOption, error) {
	input, ok := telegraf_inputs.Inputs["vsphere"]
	if !ok {
		return nil, registry.RegistrationOption{}, inputs.ErrDisabledInput
	}

	vsphereInput, ok := input().(*vsphere.VSphere)
	if !ok {
		return nil, registry.RegistrationOption{}, inputs.ErrUnexpectedType
	}

	vsphereInput.Username = telegraf_config.NewSecret([]byte(vSphere.opts.Username))
	vsphereInput.Password = telegraf_config.NewSecret([]byte(vSphere.opts.Password))

	vsphereInput.DatastoreInstances = true

	vsphereInput.DatastoreMetricInclude = []string{
		"disk.used.latest",
		"disk.capacity.latest",
	}

	vsphereInput.VMMetricExclude = []string{"*"}
	vsphereInput.HostMetricExclude = []string{"*"}
	vsphereInput.ClusterMetricExclude = []string{"*"}
	vsphereInput.ResourcePoolMetricExclude = []string{"*"}
	vsphereInput.DatacenterMetricExclude = []string{"*"}
	vsphereInput.VMInstances = false
	vsphereInput.HostInstances = false
	vsphereInput.ClusterInstances = false
	vsphereInput.ResourcePoolInstances = false
	vsphereInput.DatacenterInstances = false

	vsphereInput.InsecureSkipVerify = vSphere.opts.InsecureSkipVerify
	vsphereInput.HistoricalInterval = telegraf_config.Duration(30 * time.Minute)
	vsphereInput.ObjectDiscoveryInterval = telegraf_config.Duration(5 * time.Minute)

	vsphereInput.Log = logger.NewTelegrafLog(vSphere.String() + " historical 30min")

	acc := &internal.Accumulator{
		RenameMetrics:    renameMetrics,
		TransformMetrics: vSphere.transformMetrics,
		RenameGlobal:     vSphere.renameGlobal,
	}

	gatherer, err := newGatherer(ctx, gatherHist30m, &vSphere.opts, vsphereInput, acc, vSphere.hierarchy, vSphere.devicePropsCache)
	if err != nil {
		return nil, registry.RegistrationOption{}, err
	}

	vSphere.historical30minGatherer = gatherer

	noMetricsSinceIterations := 0
	noMetricsSince := make(map[string]int)
	opt := registry.RegistrationOption{
		Description:         fmt.Sprint(vSphere, " ", gatherHist30m),
		MinInterval:         time.Minute, // 4 times out of 5, we will re-use the previous point
		StopCallback:        gatherer.stop,
		ApplyDynamicRelabel: true,
		ExtraLabels: map[string]string{
			types.LabelMetaAgentTypes: string(bleemeo.AgentType_vSphereCluster),
		},
		GatherModifier: func(mfs []*dto.MetricFamily, _ error) []*dto.MetricFamily {
			vSphere.purgeNoMetricsSinceMap(noMetricsSince, &noMetricsSinceIterations)

			return vSphere.gatherModifier(mfs, noMetricsSince, map[ResourceKind]bool{KindDatastore: true})
		},
	}

	return gatherer, opt, nil
}

func (vSphere *vSphere) purgeNoMetricsSinceMap(noMetricsSince map[string]int, iterations *int) {
	vSphere.l.Lock()
	defer vSphere.l.Unlock()

	*iterations++

	if *iterations < 3*noMetricsStatusThreshold {
		return
	}

	*iterations = 0

	for moid := range noMetricsSince {
		if _, exists := vSphere.deviceCache[moid]; !exists {
			delete(noMetricsSince, moid)
		}
	}
}

func (vSphere *vSphere) gatherModifier(mfs []*dto.MetricFamily, noMetricsSince map[string]int, devKinds map[ResourceKind]bool) []*dto.MetricFamily {
	vSphere.l.Lock()
	defer vSphere.l.Unlock()

	seenDevices := make(map[string]bool)

	for _, mf := range mfs {
		if mf == nil {
			continue
		}

		m := 0

		for i := range len(mf.Metric) { //nolint:protogetter
			metric := mf.Metric[i] //nolint:protogetter

			for _, label := range metric.GetLabel() {
				if label.GetName() == types.LabelMetaVSphereMOID {
					seenDevices[label.GetValue()] = true

					break
				}
			}

			if shouldBeKept, labels := vSphere.modifyLabels(metric.GetLabel()); shouldBeKept {
				metric.Label = labels
				mf.Metric[m] = metric
				m++
			}
		}

		mf.Metric = mf.Metric[:m] //nolint:protogetter
	}

	vSphereStatus, vSphereMsg := vSphere.getStatus()

	for _, dev := range vSphere.deviceCache {
		if !devKinds[dev.Kind()] {
			continue // we don't care about this kind of device in this gatherModifier
		}

		var (
			deviceStatus types.Status
			deviceMsg    string
		)

		moid := dev.MOID()
		metricSeen := seenDevices[moid]

		switch {
		case vSphereStatus == types.StatusCritical:
			deviceStatus = types.StatusCritical
			deviceMsg = "vSphere is unreachable"

			if vSphereMsg != "" {
				deviceMsg += ": " + vSphereMsg
			}
		case !dev.IsPoweredOn():
			deviceStatus = types.StatusCritical

			if err := dev.LatestError(); err != nil {
				deviceMsg = err.Error()
			} else {
				deviceMsg = dev.Kind() + " is stopped"
			}
		case metricSeen:
			deviceStatus = types.StatusOk
			noMetricsSince[moid] = 0
		case !metricSeen:
			noMetricsSince[moid]++

			if noMetricsSince[moid] >= noMetricsStatusThreshold {
				deviceStatus = types.StatusCritical
				deviceMsg = "No metrics seen since a long time"
			}
		}

		// We only want to publish a critical status when it is new, not to store points for all offline devices.
		if deviceStatus == types.StatusOk || deviceStatus == types.StatusCritical && vSphere.lastStatuses[moid] != types.StatusCritical {
			vSphereDeviceStatus := &dto.MetricFamily{
				Name: proto.String("agent_status"),
				Type: dto.MetricType_GAUGE.Enum(),
				Metric: []*dto.Metric{
					{
						Label: []*dto.LabelPair{
							{Name: proto.String(types.LabelMetaCurrentStatus), Value: proto.String(deviceStatus.String())},
							{Name: proto.String(types.LabelMetaCurrentDescription), Value: proto.String(deviceMsg)},
							{Name: proto.String(types.LabelMetaVSphere), Value: proto.String(vSphere.host)},
							{Name: proto.String(types.LabelMetaVSphereMOID), Value: proto.String(moid)},
						},
						Gauge: &dto.Gauge{
							Value: proto.Float64(float64(deviceStatus.NagiosCode())),
						},
					},
				},
			}

			mfs = append(mfs, vSphereDeviceStatus)
		}

		vSphere.lastStatuses[moid] = deviceStatus
	}

	return mfs
}

// modifyLabels applies some modifications to the given labels,
// and returns whether the related metric should be kept or not.
//
//nolint:nakedret
func (vSphere *vSphere) modifyLabels(labelPairs []*dto.LabelPair) (shouldBeKept bool, finalLabels []*dto.LabelPair) {
	labels := make(map[string]*dto.LabelPair, len(labelPairs))
	// Converting label pairs to a map, which is easier to edit
	for _, label := range labelPairs {
		if label != nil {
			labels[label.GetName()] = label
		}
	}

	defer func() {
		// Once we did everything we wanted with the labels, we rebuild the list
		if shouldBeKept {
			finalLabels = make([]*dto.LabelPair, 0, len(labels))

			for _, label := range labels {
				finalLabels = append(finalLabels, label)
			}
		}
	}()

	shouldBeKept = true // By default, we keep the metric

	moid := labels[types.LabelMetaVSphereMOID].GetValue()

	isVM := labels["vmname"].GetValue() != ""
	isHost := !isVM && labels["esxhostname"].GetValue() != ""
	isCluster := !isVM && !isHost && labels["dcname"].GetValue() != ""

	switch {
	case isVM:
		if vSphere.opts.SkipMonitorVMs {
			shouldBeKept = false

			break
		}

		if diskLabel, ok := labels["disk"]; ok {
			if diskLabel.GetValue() == instanceTotal {
				shouldBeKept = false

				break
			}

			if vmDisks, ok := vSphere.labelsMetadata.disksPerVM[moid]; ok {
				starLabelReplacer(diskLabel, vmDisks) // TODO: remove

				if diskName, ok := vmDisks[diskLabel.GetValue()]; ok {
					labels["item"] = &dto.LabelPair{Name: proto.String("item"), Value: &diskName}

					delete(labels, "disk")

					break
				}
			}

			shouldBeKept = false
		} else if interfaceLabel, ok := labels["interface"]; ok {
			if interfaceLabel.GetValue() == instanceTotal {
				shouldBeKept = false

				break
			}

			if vmInterfaces, ok := vSphere.labelsMetadata.netInterfacesPerVM[moid]; ok {
				starLabelReplacer(interfaceLabel, vmInterfaces) // TODO: remove

				if interfaceName, ok := vmInterfaces[interfaceLabel.GetValue()]; ok {
					labels["item"] = &dto.LabelPair{Name: proto.String("item"), Value: &interfaceName}
					delete(labels, "interface")

					break
				}
			}

			shouldBeKept = false
		}
	case isHost, isCluster:
		delete(labels, "disk")

		if interfaceLabel, ok := labels["interface"]; ok {
			if interfaceLabel.GetValue() == instanceTotal {
				shouldBeKept = false

				break
			}

			labels["item"] = &dto.LabelPair{Name: proto.String("item"), Value: interfaceLabel.Value}
			delete(labels, "interface")

			break
		}

		if lunLabel, ok := labels["lun"]; ok {
			starLabelReplacer(lunLabel, vSphere.labelsMetadata.datastorePerLUN) // TODO: remove

			if datastore, ok := vSphere.labelsMetadata.datastorePerLUN[lunLabel.GetValue()]; ok {
				labels["item"] = &dto.LabelPair{Name: proto.String("item"), Value: &datastore}
				delete(labels, "lun")

				break
			}

			shouldBeKept = false
		}
	}

	return
}

func ptr[T any](e T) *T { return &e }

// starLabelReplacer handles the special case where the label comes from a vcsim.
// It sets its value to the first key found in the given map,
// so its belonging metric is not ignored.
func starLabelReplacer(labelPair *dto.LabelPair, m map[string]string) {
	if labelPair.GetValue() != "*" {
		return
	}

	for k := range m {
		labelPair.Value = &k

		break
	}
}

func (vSphere *vSphere) renameGlobal(gatherContext internal.GatherContext) (result internal.GatherContext, drop bool) {
	tags := maps.Clone(gatherContext.Tags) // Prevents labels from being unexpectedly removed

	tags[types.LabelMetaVSphere] = vSphere.host
	tags[types.LabelMetaVSphereMOID] = tags["moid"]

	if tags["cpu"] == "*" { // Special case (vcsim)
		tags["cpu"] = instanceTotal
	}

	// Only keep the total of CPUs
	if value, ok := tags["cpu"]; ok && value != instanceTotal {
		return gatherContext, true
	}

	delete(tags, "cpu")
	delete(tags, "guest")
	delete(tags, "guesthostname")
	delete(tags, "instance")
	delete(tags, "moid")
	delete(tags, "rpname")
	delete(tags, "source")
	delete(tags, "uuid")
	delete(tags, "vcenter")

	if value, ok := tags["dsname"]; ok {
		delete(tags, "dsname")

		tags["item"] = value
	}

	gatherContext.Tags = tags

	return gatherContext, false
}

func (vSphere *vSphere) transformMetrics(currentContext internal.GatherContext, fields map[string]float64, _ map[string]any) map[string]float64 {
	// map is: measurement -> field -> factor
	factors := map[string]map[string]float64{
		// VM metrics
		"vsphere_vm_mem": {
			"active_average":  math.NaN(), // Special case
			"swapped_average": 1000,       // KB to B
		},
		"vsphere_vm_virtualDisk": {
			"read_average":  1000, // KB/s to B/s
			"write_average": 1000, // KB/s to B/s
		},
		"vsphere_vm_net": {
			"received_average":    8192, // KiB/s to b/s
			"transmitted_average": 8192, // KiB/s to b/s
		},
		// Host metrics
		"vsphere_host_mem": {
			"totalCapacity_average": 1000000, // MB to B
			"swapin_average":        1000,    // KB to B
			"swapout_average":       1000,    // KB to B
		},
		"vsphere_host_datastore": {
			"read_average":  1000, // KB/s to B/s
			"write_average": 1000, // KB/s to B/s
		},
		"vsphere_host_net": {
			"received_average":    8192, // KiB/s to b/s
			"transmitted_average": 8192, // KiB/s to b/s
		},
		// Datastore metrics
		"vsphere_datastore_datastore": {
			"write_average": 1000, // KB/s to B/s
			"read_average":  1000, // KB/s to B/s
		},
		"vsphere_datastore_disk": {
			"used_latest":     1000, // KB to B
			"capacity_latest": 1000, // KB to B
		},
	}

	for field, factor := range factors[currentContext.Measurement] {
		if value, ok := fields[field]; ok {
			if math.IsNaN(factor) {
				// NaN indicates that a special transformation must be applied.
				newValue, keep := vSphere.transformFieldValue(currentContext, field, value)
				if keep {
					fields[field] = newValue
				} else {
					delete(fields, field)
				}
			} else {
				fields[field] = value * factor
			}
		}
	}

	return fields
}

func (vSphere *vSphere) transformFieldValue(currentContext internal.GatherContext, field string, value float64) (float64, bool) {
	if currentContext.Measurement == "vsphere_vm_mem" && field == "active_average" {
		// The mem_used_perc value is currently in KB; convert it to a percentage.
		if moid, ok := currentContext.Tags[types.LabelMetaVSphereMOID]; ok {
			vmProps, ok := vSphere.devicePropsCache.vmCache.get(moid, true)
			if ok && vmProps.Config != nil {
				activeMemMB := value / 1000
				memUsedPerc := (activeMemMB * 100) / float64(vmProps.Config.Hardware.MemoryMB)

				return memUsedPerc, true
			}
		}
	}

	return 0, false
}

func renameMetrics(currentContext internal.GatherContext, metricName string) (newMeasurement string, newMetricName string) {
	newMeasurement = currentContext.Measurement
	newMetricName = strings.TrimSuffix(metricName, "_average")
	newMetricName = strings.TrimSuffix(newMetricName, "_latest")

	// We remove the prefix "vsphere_(vm|host|datastore|cluster)_", except for "vsphere_vm_cpu latency"
	if newMetricName != "latency" {
		newMeasurement = strings.TrimPrefix(newMeasurement, "vsphere_")
		newMeasurement = strings.TrimPrefix(newMeasurement, "vm_")
	}

	newMeasurement = strings.TrimPrefix(newMeasurement, "cluster_")
	newMeasurement = strings.TrimPrefix(newMeasurement, "datastore_")
	newMeasurement = strings.TrimPrefix(newMeasurement, "host_")

	switch newMeasurement {
	case "cpu", "vsphere_vm_cpu":
		newMetricName = strings.Replace(newMetricName, "usage", "used", 1)
		if newMetricName == "latency" {
			newMetricName = "latency_perc"
		}
	case "mem":
		switch newMetricName {
		case "swapped", "swapused":
			newMeasurement = "swap" //nolint:goconst
			newMetricName = "used"
		case "swapin":
			newMeasurement = "swap"
			newMetricName = "in"
		case "swapout":
			newMeasurement = "swap"
			newMetricName = "out"
		default:
			newMetricName = strings.Replace(newMetricName, "usage", "used_perc", 1)
			newMetricName = strings.Replace(newMetricName, "totalCapacity", "total", 1)
			// mem.active is not given as a percentage, but we will transform it later.
			newMetricName = strings.Replace(newMetricName, "active", "used_perc", 1)
		}
	case "disk", "virtualDisk", "datastore":
		switch newMetricName {
		case "read", "write":
			newMeasurement = "io"
			newMetricName = strings.Replace(newMetricName, "read", "read_bytes", 1)
			newMetricName = strings.Replace(newMetricName, "write", "write_bytes", 1)
		case "capacity":
			newMetricName = "total"
		}
	case "net":
		newMetricName = strings.Replace(newMetricName, "received", "bits_recv", 1)
		newMetricName = strings.Replace(newMetricName, "transmitted", "bits_sent", 1)
	}

	return newMeasurement, newMetricName
}
