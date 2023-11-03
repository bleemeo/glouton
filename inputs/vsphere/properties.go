package vsphere

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/mitchellh/mapstructure"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
)

const maxPropertiesBulkSize = 100

const (
	maxCachedPropertiesValidity  = 2 * time.Minute
	propertiesCachePurgeInterval = 10 * time.Minute
)

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
		"guest.hostName",
		"guest.ipAddress",
		"guest.disk",
		"summary.config.product.name",
		"summary.config.product.vendor",
	}
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

type propsCache[propsType any] struct {
	l sync.Mutex

	m map[string]cachedProp[propsType]
}

// get returns the properties corresponding to the given moid,
// or `ok=false` if the properties weren't found or were outdated.
func (pc *propsCache[propsType]) get(moid string) (value propsType, ok bool) {
	pc.l.Lock()
	defer pc.l.Unlock()

	prop, ok := pc.m[moid]
	if !ok {
		return value, false
	}

	if time.Since(prop.lastUpdate) > maxCachedPropertiesValidity {
		delete(pc.m, moid) // This property won't be used anymore.

		return value, false
	}

	return prop.value, true
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
		retCtx, cancel := context.WithTimeout(ctx, commonTimeout)

		err := property.DefaultCollector(client).Retrieve(retCtx, refs[i:i+bulkSize], ps, &dest)
		cancel()
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

type (
	clusterLightProps struct {
		ComputeResource struct {
			ManagedEntity struct {
				OverallStatus types.ManagedEntityStatus
			}
			Datastore []types.ManagedObjectReference
			Summary   *struct {
				ComputeResourceSummary struct {
					NumCpuCores int16 //nolint: revive,stylecheck
				}
			}
		}
	}

	datastoreLightProps struct {
		ManagedEntity struct {
			Name string
		}
		Info types.BaseDatastoreInfo
	}

	hostLightProps struct {
		ManagedEntity struct {
			Parent *types.ManagedObjectReference
			Name   string
		}
		Runtime struct {
			PowerState types.HostSystemPowerState
		}
		Summary struct {
			Hardware *struct {
				Vendor   string
				Model    string
				CpuModel string //nolint: revive,stylecheck
			}
			Config struct {
				Name           string
				VmotionEnabled bool
			}
		}
		Hardware *struct {
			CpuInfo struct { //nolint: revive,stylecheck
				NumCpuCores int16 //nolint: revive,stylecheck
			}
			MemorySize int64
		}
		Config *struct {
			Product struct {
				Version string
				OsType  string
			}
			Network *struct {
				Vnic []struct {
					Spec struct {
						Ip *struct { //nolint: revive,stylecheck
							IpAddress string //nolint: revive,stylecheck
						}
					}
				}
				DnsConfig   types.BaseHostDnsConfig //nolint: revive,stylecheck
				IpV6Enabled *bool                   //nolint: revive,stylecheck
			}
			DateTimeInfo *struct {
				TimeZone struct {
					Name string
				}
			}
		}
	}

	vmLightProps struct {
		Config *struct {
			Name          string
			GuestFullName string
			Version       string
			Hardware      struct {
				NumCPU   int32
				MemoryMB int32
				Device   object.VirtualDeviceList
			}
			DatastoreUrl []struct{ Name, Url string } //nolint: revive,stylecheck
		}
		ResourcePool *types.ManagedObjectReference
		Runtime      struct {
			Host       *types.ManagedObjectReference
			PowerState types.VirtualMachinePowerState
		}
		Guest *struct {
			HostName  string
			IpAddress string //nolint: revive,stylecheck
			Disk      []struct {
				DiskPath  string
				Capacity  int64
				FreeSpace int64
			}
		}
		Summary struct {
			Vm     *types.ManagedObjectReference //nolint: revive,stylecheck
			Config struct {
				Product *struct {
					Name   string
					Vendor string
				}
			}
		}
	}
)
