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

package synchronizer

import (
	"context"
	"testing"
	"time"

	"github.com/bleemeo/glouton/bleemeo/internal/synchronizer/types"

	"github.com/google/go-cmp/cmp"
)

//nolint:dupl
func TestExecution_LinkedSynchronization(t *testing.T) { //nolint:maintidx
	type link struct {
		target  types.EntityName
		trigger types.EntityName
	}

	type requestSync struct {
		name        types.EntityName
		requestFull bool
	}

	tests := []struct {
		name                                string
		linksCreated                        []link
		needSynchronization                 map[types.EntityName]bool
		callsToRequestSynchronizationBefore []requestSync
		callsToRequestSynchronizationAfter  []requestSync
		wantSync                            map[types.EntityName]types.SyncType
		wantPostSync                        map[types.EntityName]types.SyncType
	}{
		{
			name:         "no-link",
			linksCreated: []link{},
			needSynchronization: map[types.EntityName]bool{
				types.EntityMetric: true,
				types.EntityAgent:  true,
			},
			callsToRequestSynchronizationBefore: []requestSync{
				{name: types.EntityContainer, requestFull: false},
				{name: types.EntityVSphere, requestFull: true},
			},
			callsToRequestSynchronizationAfter: []requestSync{
				{name: types.EntityConfig, requestFull: false},
				{name: types.EntityDiagnostics, requestFull: true},
			},
			wantSync: map[types.EntityName]types.SyncType{
				types.EntityMetric:    types.SyncTypeNormal,
				types.EntityAgent:     types.SyncTypeNormal,
				types.EntityContainer: types.SyncTypeNormal,
				types.EntityVSphere:   types.SyncTypeForceCacheRefresh,
			},
			wantPostSync: map[types.EntityName]types.SyncType{
				types.EntityConfig:      types.SyncTypeNormal,
				types.EntityDiagnostics: types.SyncTypeForceCacheRefresh,
			},
		},
		{
			name: "link-one",
			linksCreated: []link{
				{
					trigger: types.EntityMetric,
					target:  types.EntityInfo,
				},
				{
					trigger: types.EntityContainer,
					target:  types.EntityAccountConfig,
				},
				{
					trigger: types.EntityDiagnostics,
					target:  types.EntitySNMP,
				},
			},
			needSynchronization: map[types.EntityName]bool{
				types.EntityMetric: true,
				types.EntityAgent:  true,
			},
			callsToRequestSynchronizationBefore: []requestSync{
				{name: types.EntityContainer, requestFull: false},
				{name: types.EntityVSphere, requestFull: true},
			},
			callsToRequestSynchronizationAfter: []requestSync{
				{name: types.EntityConfig, requestFull: false},
				{name: types.EntityDiagnostics, requestFull: true},
			},
			wantSync: map[types.EntityName]types.SyncType{
				types.EntityMetric:        types.SyncTypeNormal,
				types.EntityAgent:         types.SyncTypeNormal,
				types.EntityContainer:     types.SyncTypeNormal,
				types.EntityVSphere:       types.SyncTypeForceCacheRefresh,
				types.EntityInfo:          types.SyncTypeNormal, // metric -> info
				types.EntityAccountConfig: types.SyncTypeNormal, // container -> account info
			},
			wantPostSync: map[types.EntityName]types.SyncType{
				types.EntityConfig:      types.SyncTypeNormal,
				types.EntityDiagnostics: types.SyncTypeForceCacheRefresh,
				types.EntitySNMP:        types.SyncTypeForceCacheRefresh, // diagnostic -> snmp
			},
		},
		{
			name: "link-one-v2",
			linksCreated: []link{
				{
					trigger: types.EntityMetric,
					target:  types.EntityInfo,
				},
				{
					trigger: types.EntityMetric,
					target:  types.EntityAccountConfig,
				},
				{
					trigger: types.EntityMetric,
					target:  types.EntitySNMP,
				},
			},
			needSynchronization: map[types.EntityName]bool{
				types.EntityMetric: true,
			},
			callsToRequestSynchronizationBefore: []requestSync{
				{name: types.EntityMetric, requestFull: true},
			},
			callsToRequestSynchronizationAfter: []requestSync{
				{name: types.EntityMetric, requestFull: false},
			},
			wantSync: map[types.EntityName]types.SyncType{
				types.EntityMetric:        types.SyncTypeForceCacheRefresh,
				types.EntityInfo:          types.SyncTypeForceCacheRefresh, // metric -> info
				types.EntityAccountConfig: types.SyncTypeForceCacheRefresh, // metric -> account info
				types.EntitySNMP:          types.SyncTypeForceCacheRefresh, // metric -> snmp
			},
			wantPostSync: map[types.EntityName]types.SyncType{
				types.EntityMetric:        types.SyncTypeNormal,
				types.EntityInfo:          types.SyncTypeNormal, // metric -> info
				types.EntityAccountConfig: types.SyncTypeNormal, // metric -> account info
				types.EntitySNMP:          types.SyncTypeNormal, // metric -> snmp
			},
		},
		{
			name: "link-indirect-no-cycle",
			linksCreated: []link{
				{
					trigger: types.EntityMetric,
					target:  types.EntityInfo,
				},
				{
					trigger: types.EntityInfo,
					target:  types.EntityAccountConfig,
				},
				{
					trigger: types.EntityAccountConfig,
					target:  types.EntitySNMP,
				},
			},
			needSynchronization: map[types.EntityName]bool{
				types.EntityMetric: true,
			},
			callsToRequestSynchronizationBefore: []requestSync{
				{name: types.EntityMetric, requestFull: true},
			},
			callsToRequestSynchronizationAfter: []requestSync{
				{name: types.EntityMetric, requestFull: false},
			},
			wantSync: map[types.EntityName]types.SyncType{
				types.EntityMetric:        types.SyncTypeForceCacheRefresh,
				types.EntityInfo:          types.SyncTypeForceCacheRefresh, // metric -> info
				types.EntityAccountConfig: types.SyncTypeForceCacheRefresh, // metric -> info -> account info
				types.EntitySNMP:          types.SyncTypeForceCacheRefresh, // metric -> info -> account info -> snmp
			},
			wantPostSync: map[types.EntityName]types.SyncType{
				types.EntityMetric:        types.SyncTypeNormal,
				types.EntityInfo:          types.SyncTypeNormal,
				types.EntityAccountConfig: types.SyncTypeNormal,
				types.EntitySNMP:          types.SyncTypeNormal,
			},
		},
		{
			name: "link-indirect-cycle",
			linksCreated: []link{
				{
					trigger: types.EntityMetric,
					target:  types.EntityInfo,
				},
				{
					trigger: types.EntityInfo,
					target:  types.EntityAccountConfig,
				},
				{
					trigger: types.EntityAccountConfig,
					target:  types.EntitySNMP,
				},
				{
					trigger: types.EntityAccountConfig,
					target:  types.EntityInfo,
				},
			},
			needSynchronization: map[types.EntityName]bool{
				types.EntityMetric: true,
			},
			callsToRequestSynchronizationBefore: []requestSync{
				{name: types.EntityMetric, requestFull: true},
			},
			callsToRequestSynchronizationAfter: []requestSync{
				{name: types.EntityMetric, requestFull: false},
			},
			wantSync: map[types.EntityName]types.SyncType{
				types.EntityMetric:        types.SyncTypeForceCacheRefresh,
				types.EntityInfo:          types.SyncTypeForceCacheRefresh, // metric -> info
				types.EntityAccountConfig: types.SyncTypeForceCacheRefresh, // metric -> info -> account info
				types.EntitySNMP:          types.SyncTypeForceCacheRefresh, // metric -> info -> account info -> snmp
			},
			wantPostSync: map[types.EntityName]types.SyncType{
				types.EntityMetric:        types.SyncTypeNormal,
				types.EntityInfo:          types.SyncTypeNormal,
				types.EntityAccountConfig: types.SyncTypeNormal,
				types.EntitySNMP:          types.SyncTypeNormal,
			},
		},
		{
			name: "link-indirect-cycle-tree",
			linksCreated: []link{
				{
					trigger: types.EntityMetric,
					target:  types.EntityInfo,
				},
				{
					trigger: types.EntityInfo,
					target:  types.EntityAccountConfig,
				},
				{
					trigger: types.EntityInfo,
					target:  types.EntityAgent,
				},
				{
					trigger: types.EntityAccountConfig,
					target:  types.EntitySNMP,
				},
				{
					trigger: types.EntitySNMP,
					target:  types.EntityDiagnostics,
				},
				{
					trigger: types.EntityAgent,
					target:  types.EntityDiagnostics,
				},
				{
					trigger: types.EntityDiagnostics,
					target:  types.EntityVSphere,
				},
				{
					trigger: types.EntityVSphere,
					target:  types.EntityAccountConfig,
				},
				{
					trigger: types.EntityVSphere,
					target:  types.EntityMetric,
				},
			},
			needSynchronization: map[types.EntityName]bool{
				types.EntityMetric: true,
			},
			callsToRequestSynchronizationBefore: []requestSync{
				{name: types.EntityMetric, requestFull: true},
			},
			callsToRequestSynchronizationAfter: []requestSync{
				{name: types.EntityMetric, requestFull: false},
			},
			wantSync: map[types.EntityName]types.SyncType{
				types.EntityMetric:        types.SyncTypeForceCacheRefresh,
				types.EntityInfo:          types.SyncTypeForceCacheRefresh, // metric -> info
				types.EntityAccountConfig: types.SyncTypeForceCacheRefresh, // metric -> info -> account info
				types.EntityAgent:         types.SyncTypeForceCacheRefresh, // metric -> info -> agent
				types.EntitySNMP:          types.SyncTypeForceCacheRefresh, // metric -> info -> account info -> snmp
				// metric -> info -> account info -> snmp -> diagnostic
				// OR metric -> info -> agent -> diagnostic
				types.EntityDiagnostics: types.SyncTypeForceCacheRefresh,
				types.EntityVSphere:     types.SyncTypeForceCacheRefresh, // [...] -> diagnostic -> vpshere
				// cycle from vpshere -> metric and vpshere -> account info are broken
			},
			wantPostSync: map[types.EntityName]types.SyncType{
				types.EntityMetric:        types.SyncTypeNormal,
				types.EntityInfo:          types.SyncTypeNormal,
				types.EntityAccountConfig: types.SyncTypeNormal,
				types.EntityAgent:         types.SyncTypeNormal,
				types.EntitySNMP:          types.SyncTypeNormal,
				types.EntityDiagnostics:   types.SyncTypeNormal,
				types.EntityVSphere:       types.SyncTypeNormal,
			},
		},
		{
			name: "link-indirect-cycle-tree-v2",
			linksCreated: []link{
				{
					trigger: types.EntityMetric,
					target:  types.EntityInfo,
				},
				{
					trigger: types.EntityInfo,
					target:  types.EntityAccountConfig,
				},
				{
					trigger: types.EntityInfo,
					target:  types.EntityAgent,
				},
				{
					trigger: types.EntityAccountConfig,
					target:  types.EntitySNMP,
				},
				{
					trigger: types.EntitySNMP,
					target:  types.EntityDiagnostics,
				},
				{
					trigger: types.EntityAgent,
					target:  types.EntityDiagnostics,
				},
				{
					trigger: types.EntityDiagnostics,
					target:  types.EntityVSphere,
				},
				{
					trigger: types.EntityVSphere,
					target:  types.EntityAccountConfig,
				},
				{
					trigger: types.EntityVSphere,
					target:  types.EntityMetric,
				},
			},
			needSynchronization: map[types.EntityName]bool{
				types.EntityAgent: true,
			},
			callsToRequestSynchronizationBefore: []requestSync{
				{name: types.EntityMetric, requestFull: true},
			},
			callsToRequestSynchronizationAfter: []requestSync{
				{name: types.EntityVSphere, requestFull: false},
			},
			wantSync: map[types.EntityName]types.SyncType{
				types.EntityMetric:        types.SyncTypeForceCacheRefresh, // agent -> diagnostic -> vpshere -> metric
				types.EntityInfo:          types.SyncTypeForceCacheRefresh, // [...] -> metric -> info
				types.EntityAccountConfig: types.SyncTypeForceCacheRefresh, // [...] -> metric -> info -> account info
				types.EntityAgent:         types.SyncTypeForceCacheRefresh, // [...] -> metric -> info -> agent
				types.EntitySNMP:          types.SyncTypeForceCacheRefresh, // [...] -> metric -> info -> account info -> snmp
				// metric -> info -> account info -> snmp -> diagnostic
				// OR metric -> info -> agent -> diagnostic
				types.EntityDiagnostics: types.SyncTypeForceCacheRefresh,
				types.EntityVSphere:     types.SyncTypeForceCacheRefresh, // [...] -> diagnostic -> vpshere
				// cycle from vpshere -> metric and vpshere -> account info are broken
			},
			wantPostSync: map[types.EntityName]types.SyncType{
				types.EntityMetric:        types.SyncTypeNormal,
				types.EntityInfo:          types.SyncTypeNormal,
				types.EntityAccountConfig: types.SyncTypeNormal,
				types.EntityAgent:         types.SyncTypeNormal,
				types.EntitySNMP:          types.SyncTypeNormal,
				types.EntityDiagnostics:   types.SyncTypeNormal,
				types.EntityVSphere:       types.SyncTypeNormal,
			},
		},
	}
	for _, tt := range tests {
		for _, createLinkEndOfNeedSynchronization := range []bool{true, false} {
			name := tt.name

			if createLinkEndOfNeedSynchronization {
				name += "-create-link-after"
			}

			t.Run(name, func(t *testing.T) {
				t.Parallel()

				e := newTestExecution(tt.needSynchronization)

				if createLinkEndOfNeedSynchronization {
					for _, link := range tt.linksCreated {
						if err := e.RequestLinkedSynchronization(link.target, link.trigger); err != nil {
							t.Errorf("Create link %v failed: %v", link, err)
						}
					}
				}

				e.applyNeedSynchronization(t.Context())

				for _, request := range tt.callsToRequestSynchronizationBefore {
					e.RequestSynchronization(request.name, request.requestFull)
				}

				if !createLinkEndOfNeedSynchronization {
					for _, link := range tt.linksCreated {
						if err := e.RequestLinkedSynchronization(link.target, link.trigger); err != nil {
							t.Errorf("Create link %v failed: %v", link, err)
						}
					}
				}

				e.checkLinkedSynchronization()
				e.syncListStarted = true

				gotSync := e.syncRequested()
				if diff := cmp.Diff(tt.wantSync, gotSync); diff != "" {
					t.Errorf("syncRequested mismatch (-want +got)\n%s", diff)
				}

				for _, request := range tt.callsToRequestSynchronizationAfter {
					e.RequestSynchronization(request.name, request.requestFull)
				}

				gotSync = e.synchronizer.forceSync
				if diff := cmp.Diff(tt.wantPostSync, gotSync); diff != "" {
					t.Errorf("synchronizer.forceSync mismatch (-want +got)\n%s", diff)
				}
			})
		}
	}
}

type mockEntity struct {
	realEntity                 types.EntitySynchronizer
	replyToNeedSynchronization bool
}

func (e mockEntity) Name() types.EntityName {
	return e.realEntity.Name()
}

func (e mockEntity) EnabledInMaintenance() bool {
	return false
}

func (e mockEntity) EnabledInSuspendedMode() bool {
	return false
}

func (e mockEntity) PrepareExecution(ctx context.Context, execution types.SynchronizationExecution) (types.EntitySynchronizerExecution, error) {
	_ = ctx
	_ = execution

	return e, nil
}

func (e mockEntity) NeedSynchronization(ctx context.Context) (bool, error) {
	_ = ctx

	return e.replyToNeedSynchronization, nil
}

func (e mockEntity) RefreshCache(ctx context.Context, syncType types.SyncType) error {
	_ = ctx
	_ = syncType

	return nil
}

func (e mockEntity) SyncRemoteAndLocal(ctx context.Context, syncType types.SyncType) error {
	_ = ctx
	_ = syncType

	return nil
}

func (e mockEntity) FinishExecution(ctx context.Context) {
	_ = ctx
}

func getMockEntityExecution(s *Synchronizer, entityReplyToNeedSynchronization map[types.EntityName]bool) []EntityExecution {
	entities := make([]EntityExecution, 0, len(s.synchronizers))

	for _, entity := range s.synchronizers {
		row := EntityExecution{
			entity: mockEntity{
				realEntity:                 entity,
				replyToNeedSynchronization: entityReplyToNeedSynchronization[entity.Name()],
			},
			syncType: types.SyncTypeNone,
		}

		entities = append(entities, row)
	}

	return entities
}

func newTestExecution(entityReplyToNeedSynchronization map[types.EntityName]bool) *Execution {
	synchronizer := newForTest(types.Option{}, time.Now)

	execution := &Execution{
		synchronizer:        synchronizer,
		client:              nil,
		initialRequestCount: 0,
		startedAt:           synchronizer.now(),
		onlyEssential:       false,
		isNewAgent:          false,
		entities:            getMockEntityExecution(synchronizer, entityReplyToNeedSynchronization),
	}

	return execution
}

func (e *Execution) syncRequested() map[types.EntityName]types.SyncType {
	result := make(map[types.EntityName]types.SyncType, len(e.entities))

	for _, ee := range e.entities {
		if ee.syncType == types.SyncTypeNone {
			continue
		}

		result[ee.entity.Name()] = ee.syncType
	}

	return result
}
