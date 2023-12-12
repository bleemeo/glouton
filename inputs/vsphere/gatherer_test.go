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
	"glouton/config"
	"glouton/facts"
	"glouton/prometheus/registry"
	"glouton/types"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/prometheus/client_golang/prometheus"
	io_prometheus_client "github.com/prometheus/client_model/go"
)

//nolint:dupl
func TestGatheringESXI(t *testing.T) { //nolint:maintidx
	vSphereCfg, deferFn := setupVSphereAPITest(t, "esxi_1")
	defer deferFn()

	ctx, cancel := context.WithTimeout(context.Background(), commonTimeout)
	defer cancel()

	manager := new(Manager)
	manager.RegisterGatherers(ctx, []config.VSphere{vSphereCfg}, func(opt registry.RegistrationOption, gatherer prometheus.Gatherer) (int, error) { return 0, nil }, nil, facts.NewMockFacter(make(map[string]string)))

	if len(manager.vSpheres) != 1 {
		t.Fatalf("Expected manager to have 1 vSphere, got %d.", len(manager.vSpheres))
	}

	manager.Devices(ctx, 0)

	var mfs []*io_prometheus_client.MetricFamily

	for host, vSphere := range manager.vSpheres {
		realtimeMfs, err := vSphere.realtimeGatherer.GatherWithState(ctx, registry.GatherState{T0: time.Now(), FromScrapeLoop: true})
		if err != nil {
			t.Fatalf("Got an error gathering (%s) vSphere %q: %v", gatherRT, host, err)
		}

		histo5minMfs, err := vSphere.historical5minGatherer.GatherWithState(ctx, registry.GatherState{T0: time.Now(), FromScrapeLoop: true})
		if err != nil {
			t.Fatalf("Got an error gathering (%s) vSphere %q: %v", gatherHist5m, host, err)
		}

		histo30minMfs, err := vSphere.historical30minGatherer.GatherWithState(ctx, registry.GatherState{T0: time.Now(), FromScrapeLoop: true})
		if err != nil {
			t.Fatalf("Got an error gathering (%s) vSphere %q: %v", gatherHist30m, host, err)
		}

		mfs = append(realtimeMfs, append(histo5minMfs, histo30minMfs...)...) //nolint: gocritic
	}

	expectedMfs := []*io_prometheus_client.MetricFamily{
		{
			Name: ptr("cpu_used"),
			Help: ptr(""),
			Type: ptr(io_prometheus_client.MetricType_UNTYPED),
			Metric: []*io_prometheus_client.Metric{
				{
					Label: []*io_prometheus_client.LabelPair{
						{Name: ptr("__meta_vsphere"), Value: ptr("127.0.0.1:xxxxx")},
						{Name: ptr("__meta_vsphere_moid"), Value: ptr("10")},
						{Name: ptr("dcname"), Value: ptr("ha-datacenter")},
						{Name: ptr("esxhostname"), Value: ptr("esxi.test")},
						{Name: ptr("vmname"), Value: ptr("alp1")},
					},
					Untyped: &io_prometheus_client.Untyped{Value: ptr(1.)},
				},
				{
					Label: []*io_prometheus_client.LabelPair{
						{Name: ptr("__meta_vsphere"), Value: ptr("127.0.0.1:xxxxx")},
						{Name: ptr("__meta_vsphere_moid"), Value: ptr("10")},
						{Name: ptr("dcname"), Value: ptr("ha-datacenter")},
						{Name: ptr("esxhostname"), Value: ptr("esxi.test")},
						{Name: ptr("vmname"), Value: ptr("alp1")},
					},
					Untyped: &io_prometheus_client.Untyped{Value: ptr(1.)},
				},
				{
					Label: []*io_prometheus_client.LabelPair{
						{Name: ptr("__meta_vsphere"), Value: ptr("127.0.0.1:xxxxx")},
						{Name: ptr("__meta_vsphere_moid"), Value: ptr("ha-host")},
						{Name: ptr("dcname"), Value: ptr("ha-datacenter")},
						{Name: ptr("esxhostname"), Value: ptr("esxi.test")},
					},
					Untyped: &io_prometheus_client.Untyped{Value: ptr(1.0)},
				},
				{
					Label: []*io_prometheus_client.LabelPair{
						{Name: ptr("__meta_vsphere"), Value: ptr("127.0.0.1:xxxxx")},
						{Name: ptr("__meta_vsphere_moid"), Value: ptr("ha-host")},
						{Name: ptr("dcname"), Value: ptr("ha-datacenter")},
						{Name: ptr("esxhostname"), Value: ptr("esxi.test")},
					},
					Untyped: &io_prometheus_client.Untyped{Value: ptr(1.)},
				},
			},
		},
		{
			Name: ptr("disk_used_perc"),
			Help: ptr(""),
			Type: ptr(io_prometheus_client.MetricType_UNTYPED),
			Metric: []*io_prometheus_client.Metric{
				{
					Label: []*io_prometheus_client.LabelPair{
						{Name: ptr("__meta_vsphere"), Value: ptr("127.0.0.1:xxxxx")},
						{Name: ptr("__meta_vsphere_moid"), Value: ptr("10")},
						{Name: ptr("clustername"), Value: ptr("esxi.test")},
						{Name: ptr("dcname"), Value: ptr("ha-datacenter")},
						{Name: ptr("esxhostname"), Value: ptr("esxi.test")},
						{Name: ptr("item"), Value: ptr("/")},
						{Name: ptr("vmname"), Value: ptr("alp1")},
					},
					Untyped: &io_prometheus_client.Untyped{Value: ptr(1.)},
				},
				{
					Label: []*io_prometheus_client.LabelPair{
						{Name: ptr("__meta_vsphere"), Value: ptr("127.0.0.1:xxxxx")},
						{Name: ptr("__meta_vsphere_moid"), Value: ptr("10")},
						{Name: ptr("clustername"), Value: ptr("esxi.test")},
						{Name: ptr("dcname"), Value: ptr("ha-datacenter")},
						{Name: ptr("esxhostname"), Value: ptr("esxi.test")},
						{Name: ptr("item"), Value: ptr("/boot")},
						{Name: ptr("vmname"), Value: ptr("alp1")},
					},
					Untyped: &io_prometheus_client.Untyped{Value: ptr(1.)},
				},
			},
		},
		{
			Name: ptr("io_read_bytes"),
			Help: ptr(""),
			Type: ptr(io_prometheus_client.MetricType_UNTYPED),
			Metric: []*io_prometheus_client.Metric{
				{
					Label: []*io_prometheus_client.LabelPair{
						{Name: ptr("__meta_vsphere"), Value: ptr("127.0.0.1:xxxxx")},
						{Name: ptr("__meta_vsphere_moid"), Value: ptr("10")},
						{Name: ptr("dcname"), Value: ptr("ha-datacenter")},
						{Name: ptr("disk"), Value: ptr("*")},
						{Name: ptr("esxhostname"), Value: ptr("esxi.test")},
						{Name: ptr("vmname"), Value: ptr("alp1")},
					},
					Untyped: &io_prometheus_client.Untyped{Value: ptr(1.)},
				},
			},
		},
		{
			Name: ptr("io_write_bytes"),
			Help: ptr(""),
			Type: ptr(io_prometheus_client.MetricType_UNTYPED),
			Metric: []*io_prometheus_client.Metric{
				{
					Label: []*io_prometheus_client.LabelPair{
						{Name: ptr("__meta_vsphere"), Value: ptr("127.0.0.1:xxxxx")},
						{Name: ptr("__meta_vsphere_moid"), Value: ptr("10")},
						{Name: ptr("dcname"), Value: ptr("ha-datacenter")},
						{Name: ptr("disk"), Value: ptr("*")},
						{Name: ptr("esxhostname"), Value: ptr("esxi.test")},
						{Name: ptr("vmname"), Value: ptr("alp1")},
					},
					Untyped: &io_prometheus_client.Untyped{Value: ptr(1.)},
				},
				{
					Label: []*io_prometheus_client.LabelPair{
						{Name: ptr("__meta_vsphere"), Value: ptr("127.0.0.1:xxxxx")},
						{Name: ptr("__meta_vsphere_moid"), Value: ptr("10")},
						{Name: ptr("dcname"), Value: ptr("ha-datacenter")},
						{Name: ptr("disk"), Value: ptr("*")},
						{Name: ptr("esxhostname"), Value: ptr("esxi.test")},
						{Name: ptr("vmname"), Value: ptr("alp1")},
					},
					Untyped: &io_prometheus_client.Untyped{Value: ptr(1.)},
				},
			},
		},
		{
			Name: ptr("mem_total"),
			Help: ptr(""),
			Type: ptr(io_prometheus_client.MetricType_UNTYPED),
			Metric: []*io_prometheus_client.Metric{
				{
					Label: []*io_prometheus_client.LabelPair{
						{Name: ptr("__meta_vsphere"), Value: ptr("127.0.0.1:xxxxx")},
						{Name: ptr("__meta_vsphere_moid"), Value: ptr("ha-host")},
						{Name: ptr("dcname"), Value: ptr("ha-datacenter")},
						{Name: ptr("esxhostname"), Value: ptr("esxi.test")},
					},
					Untyped: &io_prometheus_client.Untyped{Value: ptr(1.)},
				},
				{
					Label: []*io_prometheus_client.LabelPair{
						{Name: ptr("__meta_vsphere"), Value: ptr("127.0.0.1:xxxxx")},
						{Name: ptr("__meta_vsphere_moid"), Value: ptr("ha-host")},
						{Name: ptr("dcname"), Value: ptr("ha-datacenter")},
						{Name: ptr("esxhostname"), Value: ptr("esxi.test")},
					},
					Untyped: &io_prometheus_client.Untyped{Value: ptr(1.)},
				},
			},
		},
		{
			Name: ptr("mem_used_perc"),
			Help: ptr(""),
			Type: ptr(io_prometheus_client.MetricType_UNTYPED),
			Metric: []*io_prometheus_client.Metric{
				{
					Label: []*io_prometheus_client.LabelPair{
						{Name: ptr("__meta_vsphere"), Value: ptr("127.0.0.1:xxxxx")},
						{Name: ptr("__meta_vsphere_moid"), Value: ptr("10")},
						{Name: ptr("dcname"), Value: ptr("ha-datacenter")},
						{Name: ptr("esxhostname"), Value: ptr("esxi.test")},
						{Name: ptr("vmname"), Value: ptr("alp1")},
					},
					Untyped: &io_prometheus_client.Untyped{Value: ptr(1.)},
				},
				{
					Label: []*io_prometheus_client.LabelPair{
						{Name: ptr("__meta_vsphere"), Value: ptr("127.0.0.1:xxxxx")},
						{Name: ptr("__meta_vsphere_moid"), Value: ptr("10")},
						{Name: ptr("dcname"), Value: ptr("ha-datacenter")},
						{Name: ptr("esxhostname"), Value: ptr("esxi.test")},
						{Name: ptr("vmname"), Value: ptr("alp1")},
					},
					Untyped: &io_prometheus_client.Untyped{Value: ptr(1.)},
				},
				{
					Label: []*io_prometheus_client.LabelPair{
						{Name: ptr("__meta_vsphere"), Value: ptr("127.0.0.1:xxxxx")},
						{Name: ptr("__meta_vsphere_moid"), Value: ptr("ha-host")},
						{Name: ptr("dcname"), Value: ptr("ha-datacenter")},
						{Name: ptr("esxhostname"), Value: ptr("esxi.test")},
					},
					Untyped: &io_prometheus_client.Untyped{Value: ptr(1.)},
				},
				{
					Label: []*io_prometheus_client.LabelPair{
						{Name: ptr("__meta_vsphere"), Value: ptr("127.0.0.1:xxxxx")},
						{Name: ptr("__meta_vsphere_moid"), Value: ptr("ha-host")},
						{Name: ptr("dcname"), Value: ptr("ha-datacenter")},
						{Name: ptr("esxhostname"), Value: ptr("esxi.test")},
					},
					Untyped: &io_prometheus_client.Untyped{Value: ptr(1.)},
				},
			},
		},
		{
			Name: ptr("net_bits_recv"),
			Help: ptr(""),
			Type: ptr(io_prometheus_client.MetricType_UNTYPED),
			Metric: []*io_prometheus_client.Metric{
				{
					Label: []*io_prometheus_client.LabelPair{
						{Name: ptr("__meta_vsphere"), Value: ptr("127.0.0.1:xxxxx")},
						{Name: ptr("__meta_vsphere_moid"), Value: ptr("10")},
						{Name: ptr("dcname"), Value: ptr("ha-datacenter")},
						{Name: ptr("esxhostname"), Value: ptr("esxi.test")},
						{Name: ptr("interface"), Value: ptr("*")},
						{Name: ptr("vmname"), Value: ptr("alp1")},
					},
					Untyped: &io_prometheus_client.Untyped{Value: ptr(1.)},
				},
				{
					Label: []*io_prometheus_client.LabelPair{
						{Name: ptr("__meta_vsphere"), Value: ptr("127.0.0.1:xxxxx")},
						{Name: ptr("__meta_vsphere_moid"), Value: ptr("10")},
						{Name: ptr("dcname"), Value: ptr("ha-datacenter")},
						{Name: ptr("esxhostname"), Value: ptr("esxi.test")},
						{Name: ptr("interface"), Value: ptr("*")},
						{Name: ptr("vmname"), Value: ptr("alp1")},
					},
					Untyped: &io_prometheus_client.Untyped{Value: ptr(1.)},
				},
				{
					Label: []*io_prometheus_client.LabelPair{
						{Name: ptr("__meta_vsphere"), Value: ptr("127.0.0.1:xxxxx")},
						{Name: ptr("__meta_vsphere_moid"), Value: ptr("ha-host")},
						{Name: ptr("dcname"), Value: ptr("ha-datacenter")},
						{Name: ptr("esxhostname"), Value: ptr("esxi.test")},
						{Name: ptr("interface"), Value: ptr("*")},
					},
					Untyped: &io_prometheus_client.Untyped{Value: ptr(1.)},
				},
				{
					Label: []*io_prometheus_client.LabelPair{
						{Name: ptr("__meta_vsphere"), Value: ptr("127.0.0.1:xxxxx")},
						{Name: ptr("__meta_vsphere_moid"), Value: ptr("ha-host")},
						{Name: ptr("dcname"), Value: ptr("ha-datacenter")},
						{Name: ptr("esxhostname"), Value: ptr("esxi.test")},
						{Name: ptr("interface"), Value: ptr("*")},
					},
					Untyped: &io_prometheus_client.Untyped{Value: ptr(1.)},
				},
			},
		},
		{
			Name: ptr("net_bits_sent"),
			Help: ptr(""),
			Type: ptr(io_prometheus_client.MetricType_UNTYPED),
			Metric: []*io_prometheus_client.Metric{
				{
					Label: []*io_prometheus_client.LabelPair{
						{Name: ptr("__meta_vsphere"), Value: ptr("127.0.0.1:xxxxx")},
						{Name: ptr("__meta_vsphere_moid"), Value: ptr("10")},
						{Name: ptr("dcname"), Value: ptr("ha-datacenter")},
						{Name: ptr("esxhostname"), Value: ptr("esxi.test")},
						{Name: ptr("interface"), Value: ptr("*")},
						{Name: ptr("vmname"), Value: ptr("alp1")},
					},
					Untyped: &io_prometheus_client.Untyped{Value: ptr(1.)},
				},
				{
					Label: []*io_prometheus_client.LabelPair{
						{Name: ptr("__meta_vsphere"), Value: ptr("127.0.0.1:xxxxx")},
						{Name: ptr("__meta_vsphere_moid"), Value: ptr("10")},
						{Name: ptr("dcname"), Value: ptr("ha-datacenter")},
						{Name: ptr("esxhostname"), Value: ptr("esxi.test")},
						{Name: ptr("interface"), Value: ptr("*")},
						{Name: ptr("vmname"), Value: ptr("alp1")},
					},
					Untyped: &io_prometheus_client.Untyped{Value: ptr(1.)},
				},
				{
					Label: []*io_prometheus_client.LabelPair{
						{Name: ptr("__meta_vsphere"), Value: ptr("127.0.0.1:xxxxx")},
						{Name: ptr("__meta_vsphere_moid"), Value: ptr("ha-host")},
						{Name: ptr("dcname"), Value: ptr("ha-datacenter")},
						{Name: ptr("esxhostname"), Value: ptr("esxi.test")},
						{Name: ptr("interface"), Value: ptr("*")},
					},
					Untyped: &io_prometheus_client.Untyped{Value: ptr(1.)},
				},
				{
					Label: []*io_prometheus_client.LabelPair{
						{Name: ptr("__meta_vsphere"), Value: ptr("127.0.0.1:xxxxx")},
						{Name: ptr("__meta_vsphere_moid"), Value: ptr("ha-host")},
						{Name: ptr("dcname"), Value: ptr("ha-datacenter")},
						{Name: ptr("esxhostname"), Value: ptr("esxi.test")},
						{Name: ptr("interface"), Value: ptr("*")},
					},
					Untyped: &io_prometheus_client.Untyped{Value: ptr(1.)},
				},
			},
		},
		{
			Name: ptr("vms_running_count"),
			Help: ptr(""),
			Type: ptr(io_prometheus_client.MetricType_UNTYPED),
			Metric: []*io_prometheus_client.Metric{
				{
					Label: []*io_prometheus_client.LabelPair{
						{Name: ptr("__meta_vsphere"), Value: ptr("127.0.0.1:xxxxx")},
						{Name: ptr("__meta_vsphere_moid"), Value: ptr("ha-host")},
						{Name: ptr("clustername"), Value: ptr("esxi.test")},
						{Name: ptr("dcname"), Value: ptr("ha-datacenter")},
						{Name: ptr("esxhostname"), Value: ptr("esxi.test")},
					},
					Untyped: &io_prometheus_client.Untyped{Value: ptr(1.)},
				},
			},
		},
		{
			Name: ptr("vms_stopped_count"),
			Help: ptr(""),
			Type: ptr(io_prometheus_client.MetricType_UNTYPED),
			Metric: []*io_prometheus_client.Metric{
				{
					Label: []*io_prometheus_client.LabelPair{
						{Name: ptr("__meta_vsphere"), Value: ptr("127.0.0.1:xxxxx")},
						{Name: ptr("__meta_vsphere_moid"), Value: ptr("ha-host")},
						{Name: ptr("clustername"), Value: ptr("esxi.test")},
						{Name: ptr("dcname"), Value: ptr("ha-datacenter")},
						{Name: ptr("esxhostname"), Value: ptr("esxi.test")},
					},
					Untyped: &io_prometheus_client.Untyped{Value: ptr(1.)},
				},
			},
		},
		{
			Name: ptr("vsphere_vm_cpu_latency_perc"),
			Help: ptr(""),
			Type: ptr(io_prometheus_client.MetricType_UNTYPED),
			Metric: []*io_prometheus_client.Metric{
				{
					Label: []*io_prometheus_client.LabelPair{
						{Name: ptr("__meta_vsphere"), Value: ptr("127.0.0.1:xxxxx")},
						{Name: ptr("__meta_vsphere_moid"), Value: ptr("10")},
						{Name: ptr("dcname"), Value: ptr("ha-datacenter")},
						{Name: ptr("esxhostname"), Value: ptr("esxi.test")},
						{Name: ptr("vmname"), Value: ptr("alp1")},
					},
					Untyped: &io_prometheus_client.Untyped{Value: ptr(1.)},
				},
				{
					Label: []*io_prometheus_client.LabelPair{
						{Name: ptr("__meta_vsphere"), Value: ptr("127.0.0.1:xxxxx")},
						{Name: ptr("__meta_vsphere_moid"), Value: ptr("10")},
						{Name: ptr("dcname"), Value: ptr("ha-datacenter")},
						{Name: ptr("esxhostname"), Value: ptr("esxi.test")},
						{Name: ptr("vmname"), Value: ptr("alp1")},
					},
					Untyped: &io_prometheus_client.Untyped{Value: ptr(1.)},
				},
			},
		},
	}

	ignoreUnexported := cmpopts.IgnoreUnexported([]any{io_prometheus_client.MetricFamily{}, io_prometheus_client.Metric{}, io_prometheus_client.LabelPair{}, io_prometheus_client.Untyped{}}...)
	ignoreUntypedValue := cmpopts.IgnoreFields(io_prometheus_client.Untyped{}, "Value")
	ignoreTimestamp := cmpopts.IgnoreFields(io_prometheus_client.Metric{}, "TimestampMs")
	opts := cmp.Options{ignoreUnexported, ignoreUntypedValue, ignoreTimestamp, cmp.Comparer(vSphereLabelComparer)}

	if diff := cmp.Diff(expectedMfs, mfs, opts, makeVirtualDiskMetricComparer(opts)); diff != "" {
		t.Errorf("Unexpected metric families (-want +got):\n%s", diff)
	}
}

// vSphereLabelComparer handles the comparison between two "__meta_vsphere" labels,
// which is unpredictable because the port used by the simulator is random.
func vSphereLabelComparer(x, y *io_prometheus_client.LabelPair) bool {
	if x.GetName() == types.LabelMetaVSphere && y.GetName() == types.LabelMetaVSphere {
		xParts, yParts := strings.Split(x.GetValue(), ":"), strings.Split(y.GetValue(), ":")
		if len(xParts) != 2 || len(yParts) != 2 {
			return false
		}

		return xParts[0] == yParts[0]
	}

	return cmp.Equal(x, y, cmpopts.IgnoreUnexported(io_prometheus_client.LabelPair{}))
}

// makeVirtualDiskMetricComparer returns a comparer that tolerates having
// non-matching metrics within the "io_read_bytes" family.
// This can happen because the simulator sometimes seems to return 1 extra point,
// a minute in the past, but only for this particular metric ...
func makeVirtualDiskMetricComparer(opts []cmp.Option) cmp.Option {
	return cmp.Comparer(func(x, y *io_prometheus_client.MetricFamily) bool {
		if x.GetName() == "io_read_bytes" && y.GetName() == "io_read_bytes" {
			return cmp.Equal(x, y, append(opts, cmpopts.IgnoreFields(io_prometheus_client.MetricFamily{}, "Metric"))...)
		}

		return cmp.Equal(x, y, opts...)
	})
}
