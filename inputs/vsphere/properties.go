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
	"slices"
	"sync"
	"time"

	"github.com/go-viper/mapstructure/v2"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
)

const (
	maxPropertiesBulkSize        = 100
	maxCachedPropertiesValidity  = 2 * time.Minute
	propertiesCachePurgeInterval = 10 * time.Minute
)

type refName struct {
	ref  types.ManagedObjectReference
	name string
}

func (r refName) Reference() types.ManagedObjectReference {
	return r.ref
}

func (r refName) Name() string {
	return r.name
}

// propsCaches holds the caches of object properties from different types.
type propsCaches struct {
	clusterCache   *propsCache[clusterLightProps]
	datastoreCache *propsCache[datastoreLightProps]
	hostCache      *propsCache[hostLightProps]
	vmCache        *propsCache[vmLightProps]

	lastPurge time.Time
}

func newPropsCaches() *propsCaches {
	return &propsCaches{
		clusterCache:   &propsCache[clusterLightProps]{m: make(map[string]cachedProp[clusterLightProps])},
		datastoreCache: &propsCache[datastoreLightProps]{m: make(map[string]cachedProp[datastoreLightProps])},
		hostCache:      &propsCache[hostLightProps]{m: make(map[string]cachedProp[hostLightProps])},
		vmCache:        &propsCache[vmLightProps]{m: make(map[string]cachedProp[vmLightProps])},
		lastPurge:      time.Now(),
	}
}

// purge removes all outdated entries in all its caches,
// while ensuring not purging too frequently.
func (propsCache *propsCaches) purge() {
	if time.Since(propsCache.lastPurge) < propertiesCachePurgeInterval {
		return
	}

	propsCache.clusterCache.purge()
	propsCache.datastoreCache.purge()
	propsCache.hostCache.purge()
	propsCache.vmCache.purge()

	propsCache.lastPurge = time.Now()
}

// propsCache represents a cache that contains the properties
// of objects of a given `propsType` type.
// It supports concurrent access.
type propsCache[propsType any] struct {
	l sync.Mutex

	m map[string]cachedProp[propsType]
}

// get returns the properties corresponding to the given moid,
// or `ok=false` if the properties weren't found or were outdated.
// If bypassStaleness is given as true, properties will be returned even outdated.
func (pc *propsCache[propsType]) get(moid string, bypassStaleness ...bool) (value propsType, ok bool) {
	pc.l.Lock()
	defer pc.l.Unlock()

	props, ok := pc.m[moid]
	if !ok {
		return value, false
	}

	if time.Since(props.lastUpdate) > maxCachedPropertiesValidity && !(len(bypassStaleness) == 1 && bypassStaleness[0]) {
		return value, false
	}

	return props.value, true
}

func (pc *propsCache[propsType]) set(moid string, value propsType) {
	pc.l.Lock()
	defer pc.l.Unlock()

	pc.m[moid] = cachedProp[propsType]{
		lastUpdate: time.Now(),
		value:      value,
	}
}

func (pc *propsCache[propsType]) purge() {
	pc.l.Lock()
	defer pc.l.Unlock()

	for moid, prop := range pc.m {
		if time.Since(prop.lastUpdate) > maxCachedPropertiesValidity {
			delete(pc.m, moid)
		}
	}
}

type cachedProp[propsType any] struct {
	lastUpdate time.Time
	value      propsType
}

// retrieveProps fetches the properties `ps` of the given objects and returns them.
// If an object already has its properties in the given cache, they won't be fetched this time.
// Otherwise, they will be fetched then stored in the cache for the next time.
func retrieveProps[ref commonObject, props any](ctx context.Context, client *vim25.Client, objects []ref, ps []string, cache *propsCache[props]) (map[refName]props, error) {
	if len(objects) == 0 {
		// Calling property.Collector.Retrieve() with an empty list would cause an error
		return map[refName]props{}, nil
	}

	refs := make([]types.ManagedObjectReference, 0, len(objects))
	cachedProps := make(map[types.ManagedObjectReference]props)

	for _, obj := range objects {
		ref := obj.Reference()
		if prop, ok := cache.get(ref.Value); ok {
			cachedProps[ref] = prop
		} else {
			refs = append(refs, ref)
		}
	}

	refs = slices.Clip(refs)
	dest := make([]mo.Reference, 0, len(refs))

	for i := 0; i < len(refs); i += maxPropertiesBulkSize {
		bulkSize := min(len(refs)-i, maxPropertiesBulkSize)

		err := func() error {
			retCtx, cancel := context.WithTimeout(ctx, commonTimeout)
			defer cancel()

			return property.DefaultCollector(client).Retrieve(retCtx, refs[i:i+bulkSize], ps, &dest)
		}()
		if err != nil {
			return nil, err
		}
	}

	destLookup := make(map[types.ManagedObjectReference]mo.Reference, len(dest))

	for _, dst := range dest {
		destLookup[dst.Reference()] = dst
	}

	m := make(map[refName]props, len(objects))

	for _, obj := range objects {
		var objProps props

		dst, found := destLookup[obj.Reference()]
		if found {
			err := mapstructure.Decode(dst, &objProps)
			if err != nil {
				return map[refName]props{}, err
			}

			cache.set(obj.Reference().Value, objProps)
		} else {
			objProps, found = cachedProps[obj.Reference()]
			if !found {
				continue
			}
		}

		rfName := refName{obj.Reference(), obj.Name()}
		m[rfName] = objProps
	}

	return m, nil
}

//nolint:gochecknoglobals
var (
	relevantClusterProperties = []string{
		"overallStatus",
		"datastore",
		"summary",
	}
	relevantDatastoreProperties = []string{
		"name",
		"info",
	}
	relevantHostProperties = []string{
		"name",
		"parent",
		"runtime.powerState",
		"summary.hardware.vendor",
		"summary.hardware.model",
		"summary.hardware.cpuModel",
		"summary.config.name",
		"summary.config.vmotionEnabled",
		"hardware.cpuInfo.numCpuCores",
		"hardware.memorySize",
		"config.product.version",
		"config.product.osType",
		"config.network.vnic",
		"config.network.dnsConfig.domainName",
		"config.network.ipV6Enabled",
		"config.dateTimeInfo.timeZone.name",
	}
	relevantVMProperties = []string{
		"config.name",
		"config.guestFullName",
		"config.version",
		"config.hardware.numCPU",
		"config.hardware.memoryMB",
		"config.hardware.device",
		"config.datastoreUrl",
		"resourcePool",
		"runtime.host",
		"runtime.powerState",
		"guest.guestFullName",
		"guest.hostName",
		"guest.ipAddress",
		"guest.disk",
		"summary.config.product.name",
		"summary.config.product.vendor",
	}
)

// The below types are reflecting some types defined in github.com/vmware/govmomi/vim25/mo,
// but only with the (above) properties we're interested in.
// This approach has two goals:
// - reduce the size of the request/response to the vSphere API
// - reduce the memory used by the cache to store the properties.
type (
	// Lightweight version of mo.ClusterComputeResource.
	clusterLightProps struct {
		ComputeResource clusterLightComputeResource
	}

	clusterLightComputeResource struct {
		ManagedEntity clusterLightComputeResourceManagedEntity
		Datastore     []types.ManagedObjectReference
		Summary       *clusterLightComputeResourceSummary
	}

	clusterLightComputeResourceManagedEntity struct {
		OverallStatus types.ManagedEntityStatus
	}

	clusterLightComputeResourceSummary struct {
		ComputeResourceSummary clusterLightComputeResourceSummaryComputeResourceSummary
	}

	clusterLightComputeResourceSummaryComputeResourceSummary struct {
		NumCpuCores int16 //nolint: revive,stylecheck
		TotalCpu    int32 //nolint: revive,stylecheck
		TotalMemory int64
	}

	// Lightweight version of mo.Datastore.
	datastoreLightProps struct {
		ManagedEntity datastoreLightManagedEntity
		Info          types.BaseDatastoreInfo
	}

	datastoreLightManagedEntity struct {
		Name string
	}

	// Lightweight version of mo.HostSystem.
	hostLightProps struct {
		ManagedEntity hostLightManagedEntity
		Runtime       hostLightRuntime
		Summary       hostLightSummary
		Hardware      *hostLightHardware
		Config        *hostLightConfig
	}

	hostLightManagedEntity struct {
		Parent *types.ManagedObjectReference
		Name   string
	}

	hostLightRuntime struct {
		PowerState types.HostSystemPowerState
	}

	hostLightSummary struct {
		Hardware *hostLightSummaryHardware
		Config   hostLightSummaryConfig
	}

	hostLightSummaryHardware struct {
		Vendor   string
		Model    string
		CpuModel string //nolint: revive,stylecheck
	}

	hostLightSummaryConfig struct {
		Name           string
		VmotionEnabled bool
	}

	hostLightHardware struct {
		CpuInfo    hostLightHardwareCpuInfo //nolint: revive,stylecheck
		MemorySize int64
	}

	hostLightHardwareCpuInfo struct { //nolint: revive,stylecheck
		NumCpuCores int16 //nolint: revive,stylecheck
	}

	hostLightConfig struct {
		Product      hostLightConfigProduct
		Network      *hostLightConfigNetwork
		DateTimeInfo *hostLightConfigDateTimeInfo
	}

	hostLightConfigProduct struct {
		Version string
		OsType  string
	}

	hostLightConfigNetwork struct {
		Vnic        []hostLightConfigNetworkVnic
		DnsConfig   types.BaseHostDnsConfig //nolint: revive,stylecheck
		IpV6Enabled *bool                   //nolint: revive,stylecheck
	}

	hostLightConfigNetworkVnic struct {
		Spec hostLightConfigNetworkVnicSpec
	}

	hostLightConfigNetworkVnicSpec struct {
		Ip *hostLightConfigNetworkVnicSpecIp //nolint: revive,stylecheck
	}

	hostLightConfigNetworkVnicSpecIp struct { //nolint: revive,stylecheck
		IpAddress string //nolint: revive,stylecheck
	}

	hostLightConfigDateTimeInfo struct {
		TimeZone hostLightConfigDateTimeInfoTimeZone
	}

	hostLightConfigDateTimeInfoTimeZone struct {
		Name string
	}

	// Lightweight version of mo.VirtualMachine.
	vmLightProps struct {
		Config       *vmLightConfig
		ResourcePool *types.ManagedObjectReference
		Runtime      vmLightRuntime
		Guest        *vmLightGuest
		Summary      vmLightSummary
	}

	vmLightConfig struct {
		Name          string
		GuestFullName string
		Version       string
		Hardware      vmLightConfigHardware
		DatastoreUrl  []vmLightConfigDatastoreUrl //nolint: revive,stylecheck
	}

	vmLightConfigHardware struct {
		NumCPU   int32
		MemoryMB int32
		Device   object.VirtualDeviceList
	}

	vmLightConfigDatastoreUrl struct { //nolint: revive,stylecheck
		Name string
		Url  string //nolint: revive,stylecheck
	}

	vmLightRuntime struct {
		Host       *types.ManagedObjectReference
		PowerState types.VirtualMachinePowerState
	}

	vmLightGuest struct {
		GuestFullName string
		HostName      string
		IpAddress     string //nolint: revive,stylecheck
		Disk          []vmLightGuestDisk
	}

	vmLightGuestDisk struct {
		DiskPath  string
		Capacity  int64
		FreeSpace int64
	}

	vmLightSummary struct {
		Vm     *types.ManagedObjectReference //nolint: revive,stylecheck
		Config vmLightSummaryConfig
	}

	vmLightSummaryConfig struct {
		Product *vmLightSummaryConfigProduct
	}

	vmLightSummaryConfigProduct struct {
		Name   string
		Vendor string
	}
)
