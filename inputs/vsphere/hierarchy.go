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

package vsphere

import (
	"context"
	"sync"
	"time"

	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
)

const hierarchyMinUpdateInterval = time.Minute

// Hierarchy represents the structure of a vSphere in a way that suits us.
// It drops the folder levels to get a hierarchy with a shape like:
// VM -> Host -> Cluster -> Datacenter
// It also indexes the device names of VMs, hosts, resource pools, clusters and datacenters.
type Hierarchy struct {
	deviceNamePerMOID  map[string]string
	parentPerChildMOID map[string]types.ManagedObjectReference

	lastUpdate time.Time
	l          sync.Mutex
}

func NewHierarchy() *Hierarchy {
	return &Hierarchy{
		deviceNamePerMOID:  make(map[string]string),
		parentPerChildMOID: make(map[string]types.ManagedObjectReference),
	}
}

func (h *Hierarchy) Refresh(ctx context.Context, clusters []*object.ClusterComputeResource, resourcePools []*object.ResourcePool, hosts []*object.HostSystem, vms []*object.VirtualMachine, vmPropsCache *propsCache[vmLightProps]) error {
	h.l.Lock()
	defer h.l.Unlock()

	if time.Since(h.lastUpdate) < hierarchyMinUpdateInterval {
		return nil
	}

	h.deviceNamePerMOID = make(map[string]string)
	h.parentPerChildMOID = make(map[string]types.ManagedObjectReference)
	h.lastUpdate = time.Now()

	var vmClient *vim25.Client

	for _, vm := range vms {
		err := h.recurseDescribe(ctx, vm.Client(), vm.Reference())
		if err != nil {
			return err
		}

		vmClient = vm.Client()
	}

	for _, host := range hosts {
		err := h.recurseDescribe(ctx, host.Client(), host.Reference())
		if err != nil {
			return err
		}
	}

	for _, cluster := range clusters {
		err := h.recurseDescribe(ctx, cluster.Client(), cluster.Reference())
		if err != nil {
			return err
		}
	}

	for _, resourcePool := range resourcePools {
		h.deviceNamePerMOID[resourcePool.Reference().Value] = resourcePool.Name()
	}

	h.filterParents()

	if vmClient != nil {
		vmProps, err := retrieveProps(ctx, vmClient, vms, relevantVMProperties, vmPropsCache)
		if err != nil {
			return err
		}

		h.fixVMParents(vmProps)
	}

	return nil
}

func (h *Hierarchy) fixVMParents(vmProps map[refName]vmLightProps) {
	for vmRef, props := range vmProps {
		if host := props.Runtime.Host; host != nil {
			h.parentPerChildMOID[vmRef.Reference().Value] = *host
		}
	}
}

func (h *Hierarchy) recurseDescribe(ctx context.Context, client *vim25.Client, objRef types.ManagedObjectReference) error {
	if _, alreadyExist := h.deviceNamePerMOID[objRef.Value]; alreadyExist {
		return nil
	}

	elements, err := mo.Ancestors(ctx, client, client.ServiceContent.PropertyCollector, objRef)
	if err != nil {
		return err
	}

	for _, e := range elements {
		h.deviceNamePerMOID[e.Reference().Value] = e.Name

		if e.Parent != nil {
			h.parentPerChildMOID[e.Reference().Value] = *e.Parent
		}

		err = h.recurseDescribe(ctx, client, e.Reference())
		if err != nil {
			return err
		}
	}

	return nil
}

// filterParents removes the folder elements in the hierarchy tree.
func (h *Hierarchy) filterParents() {
	for child, parent := range h.parentPerChildMOID {
		needsUpdate := false

		for parent.Type == "Folder" {
			delete(h.deviceNamePerMOID, parent.Value) // We will never care about the name of a folder.

			if grandParent, ok := h.parentPerChildMOID[parent.Value]; ok {
				parent = grandParent
				needsUpdate = true
			} else {
				break
			}
		}

		if needsUpdate {
			h.parentPerChildMOID[child] = parent
		}
	}
}

func (h *Hierarchy) findFirstParentOfType(child mo.Reference, typ string) string {
	if parent, ok := h.parentPerChildMOID[child.Reference().Value]; ok {
		if parent.Type == typ {
			return h.deviceNamePerMOID[parent.Value]
		}

		return h.findFirstParentOfType(parent, typ)
	}

	return ""
}

func (h *Hierarchy) ParentHostName(child mo.Reference) string {
	h.l.Lock()
	defer h.l.Unlock()

	return h.findFirstParentOfType(child, "HostSystem")
}

func (h *Hierarchy) ParentClusterName(child mo.Reference) string {
	h.l.Lock()
	defer h.l.Unlock()

	return h.findFirstParentOfType(child, "ComputeResource")
}

func (h *Hierarchy) ParentDCName(child mo.Reference) string {
	h.l.Lock()
	defer h.l.Unlock()

	return h.findFirstParentOfType(child, "Datacenter")
}

func (h *Hierarchy) DeviceName(moid string) (string, bool) {
	h.l.Lock()
	defer h.l.Unlock()

	name, found := h.deviceNamePerMOID[moid]

	return name, found
}
