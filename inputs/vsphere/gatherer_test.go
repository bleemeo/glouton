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
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/bleemeo/glouton/config"
	"github.com/bleemeo/glouton/facts"
	"github.com/bleemeo/glouton/prometheus/registry"
	"github.com/bleemeo/glouton/types"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// Test label value constants for ESXI tests.
const (
	testMetaVSphereAddr = "127.0.0.1:xxxxx"
	testDCHaDatacenter  = "ha-datacenter"
	testHostESXI        = "esxi.test"
	testMOIDHaHost      = "ha-host"
	testMOIDVM10        = "10"
	testVMAlp1          = "alp1"
)

// Test label value constants for vcsim/vCenter tests.
const (
	testDCDC0         = "DC0"
	testClusterDC0C0  = "DC0_C0"
	testHostDC0C0H0   = "DC0_C0_H0"
	testVMDC0C0RP0VM0 = "DC0_C0_RP0_VM0"
	testMOIDDomainC16 = "domain-c16"
	testMOIDHost23    = "host-23"
	testMOIDVM28      = "vm-28"
)

// Test retained metric constants.
const (
	testFieldUsageMHzAvg = "usagemhz_average"
	testFieldUsageAvg    = "usage_average"
)

func setupGathering(t *testing.T, dirName string) (mfs []*dto.MetricFamily, deferFn func()) {
	t.Helper()

	vSphereCfg, vSphereDeferFn := setupVSphereAPITest(t, dirName)
	ctx, cancel := context.WithTimeout(t.Context(), commonTimeout)
	deferFn = func() { cancel(); vSphereDeferFn() }

	manager := new(Manager)
	manager.RegisterGatherers(
		ctx,
		[]config.VSphere{vSphereCfg},
		func(_ registry.RegistrationOption, _ prometheus.Gatherer) (types.Registration, error) {
			return nil, nil //nolint: nilnil
		},
		nil,
		facts.NewMockFacter(make(map[string]string)),
	)

	var (
		vSphere *vSphere
		ok      bool
	)

	u, _ := url.Parse(vSphereCfg.URL)
	if vSphere, ok = manager.vSpheres[u.Host]; !ok {
		deferFn()
		t.Fatalf("Expected manager to have a vSphere for the key %q.", u.Host)
	}

	manager.Devices(ctx, 0)

	t0 := time.Now().Round(time.Second)

	if willFail, offset := willGatheringTestFail(t0); willFail {
		t.Skipf("This test is likely to fail (offset=%d)", offset)

		return nil, nil
	}

	realtimeMfs, err := vSphere.realtimeGatherer.GatherWithState(ctx, registry.GatherState{T0: t0, FromScrapeLoop: true})
	if err != nil {
		deferFn()
		t.Fatalf("Got an error gathering (%s) vSphere: %v", gatherRT, err)
	}

	histo30minMfs, err := vSphere.historical30minGatherer.GatherWithState(ctx, registry.GatherState{T0: t0, FromScrapeLoop: true})
	if err != nil {
		deferFn()
		t.Fatalf("Got an error gathering (%s) vSphere: %v", gatherHist30m, err)
	}

	mfs = append(realtimeMfs, histo30minMfs...) //nolint: gocritic

	return mfs, deferFn
}

// willGatheringTestFail returns whether gathering tests are likely to fail at the given timestamp.
func willGatheringTestFail(t time.Time) (bool, int) {
	// fourZeroesOffsets contains the list of all the offset that will make
	// the simulator return 4 zeroes, which will lead to no series at all.
	// The third line contains offsets that shouldn't fail,
	// but sometime do due to a 20s jitter.
	fourZeroesOffsets := map[int]struct{}{
		0: {}, 1: {}, 2: {}, 3: {}, 4: {}, 20: {}, 27: {},
		28: {}, 29: {}, 50: {}, 51: {}, 65: {}, 66: {}, 99: {},
		5: {}, 21: {}, 30: {}, 52: {}, 67: {},
	}

	// The offset is evaluated like so:
	// (start_TS / interval) % simulator_points_count
	// For full details, see the code around
	// https://github.com/vmware/govmomi/blob/139b19e3641b61b0969f78c84266bfc3e12f109c/simulator/performance_manager.go#L225
	offset := int((t.Add(-1*time.Minute).Unix() / 20) % 100)
	_, found := fourZeroesOffsets[offset]

	// return found, offset
	// There is still too much test failure. Always skip vSphere test until a better solution is found
	_ = found

	return true, 0
}

//nolint:nolintlint,gofmt,dupl
func TestGatheringESXI(t *testing.T) { //nolint:maintidx
	mfs, deferFn := setupGathering(t, "esxi_1")
	defer deferFn()

	expectedMfs := []*dto.MetricFamily{
		{
			Name: new("cpu_used"),
			Help: new(""),
			Type: dto.MetricType_UNTYPED.Enum(),
			Metric: []*dto.Metric{
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDVM10)},
						{Name: new(tagDCName), Value: new(testDCHaDatacenter)},
						{Name: new(tagESXHostName), Value: new(testHostESXI)},
						{Name: new(tagVMName), Value: new(testVMAlp1)},
					},
					Untyped: &dto.Untyped{Value: new(1.)},
				},
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDVM10)},
						{Name: new(tagDCName), Value: new(testDCHaDatacenter)},
						{Name: new(tagESXHostName), Value: new(testHostESXI)},
						{Name: new(tagVMName), Value: new(testVMAlp1)},
					},
					Untyped: &dto.Untyped{Value: new(1.)},
				},
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDHaHost)},
						{Name: new(tagDCName), Value: new(testDCHaDatacenter)},
						{Name: new(tagESXHostName), Value: new(testHostESXI)},
					},
					Untyped: &dto.Untyped{Value: new(1.0)},
				},
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDHaHost)},
						{Name: new(tagDCName), Value: new(testDCHaDatacenter)},
						{Name: new(tagESXHostName), Value: new(testHostESXI)},
					},
					Untyped: &dto.Untyped{Value: new(1.)},
				},
			},
		},
		{
			Name: new("cpu_usedmhz"),
			Help: new(""),
			Type: dto.MetricType_UNTYPED.Enum(),
			Metric: []*dto.Metric{
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDHaHost)},
						{Name: new(tagDCName), Value: new(testDCHaDatacenter)},
						{Name: new(tagESXHostName), Value: new(testHostESXI)},
					},
					Untyped: &dto.Untyped{Value: new(1.0)},
				},
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDHaHost)},
						{Name: new(tagDCName), Value: new(testDCHaDatacenter)},
						{Name: new(tagESXHostName), Value: new(testHostESXI)},
					},
					Untyped: &dto.Untyped{Value: new(1.)},
				},
			},
		},
		{
			Name: new("disk_used_perc"),
			Help: new(""),
			Type: dto.MetricType_UNTYPED.Enum(),
			Metric: []*dto.Metric{
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDVM10)},
						{Name: new(tagClusterName), Value: new(testHostESXI)},
						{Name: new(tagDCName), Value: new(testDCHaDatacenter)},
						{Name: new(tagESXHostName), Value: new(testHostESXI)},
						{Name: new("item"), Value: new("/")},
						{Name: new(tagVMName), Value: new(testVMAlp1)},
					},
					Untyped: &dto.Untyped{Value: new(1.)},
				},
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDVM10)},
						{Name: new(tagClusterName), Value: new(testHostESXI)},
						{Name: new(tagDCName), Value: new(testDCHaDatacenter)},
						{Name: new(tagESXHostName), Value: new(testHostESXI)},
						{Name: new("item"), Value: new("/boot")},
						{Name: new(tagVMName), Value: new(testVMAlp1)},
					},
					Untyped: &dto.Untyped{Value: new(1.)},
				},
			},
		},
		{
			Name: new("io_read_bytes"),
			Help: new(""),
			Type: dto.MetricType_UNTYPED.Enum(),
			Metric: []*dto.Metric{
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDVM10)},
						{Name: new(tagDCName), Value: new(testDCHaDatacenter)},
						{Name: new("disk"), Value: new("*")},
						{Name: new(tagESXHostName), Value: new(testHostESXI)},
						{Name: new(tagVMName), Value: new(testVMAlp1)},
					},
					Untyped: &dto.Untyped{Value: new(1.)},
				},
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDVM10)},
						{Name: new(tagDCName), Value: new(testDCHaDatacenter)},
						{Name: new("disk"), Value: new("*")},
						{Name: new(tagESXHostName), Value: new(testHostESXI)},
						{Name: new(tagVMName), Value: new(testVMAlp1)},
					},
					Untyped: &dto.Untyped{Value: new(1.)},
				},
			},
		},
		{
			Name: new("io_write_bytes"),
			Help: new(""),
			Type: dto.MetricType_UNTYPED.Enum(),
			Metric: []*dto.Metric{
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDVM10)},
						{Name: new(tagDCName), Value: new(testDCHaDatacenter)},
						{Name: new("disk"), Value: new("*")},
						{Name: new(tagESXHostName), Value: new(testHostESXI)},
						{Name: new(tagVMName), Value: new(testVMAlp1)},
					},
					Untyped: &dto.Untyped{Value: new(1.)},
				},
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDVM10)},
						{Name: new(tagDCName), Value: new(testDCHaDatacenter)},
						{Name: new("disk"), Value: new("*")},
						{Name: new(tagESXHostName), Value: new(testHostESXI)},
						{Name: new(tagVMName), Value: new(testVMAlp1)},
					},
					Untyped: &dto.Untyped{Value: new(1.)},
				},
			},
		},
		{
			Name: new("mem_total"),
			Help: new(""),
			Type: dto.MetricType_UNTYPED.Enum(),
			Metric: []*dto.Metric{
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDHaHost)},
						{Name: new(tagDCName), Value: new(testDCHaDatacenter)},
						{Name: new(tagESXHostName), Value: new(testHostESXI)},
					},
					Untyped: &dto.Untyped{Value: new(1.)},
				},
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDHaHost)},
						{Name: new(tagDCName), Value: new(testDCHaDatacenter)},
						{Name: new(tagESXHostName), Value: new(testHostESXI)},
					},
					Untyped: &dto.Untyped{Value: new(1.)},
				},
			},
		},
		{
			Name: new("mem_used_perc"),
			Help: new(""),
			Type: dto.MetricType_UNTYPED.Enum(),
			Metric: []*dto.Metric{
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDVM10)},
						{Name: new(tagDCName), Value: new(testDCHaDatacenter)},
						{Name: new(tagESXHostName), Value: new(testHostESXI)},
						{Name: new(tagVMName), Value: new(testVMAlp1)},
					},
					Untyped: &dto.Untyped{Value: new(1.)},
				},
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDVM10)},
						{Name: new(tagDCName), Value: new(testDCHaDatacenter)},
						{Name: new(tagESXHostName), Value: new(testHostESXI)},
						{Name: new(tagVMName), Value: new(testVMAlp1)},
					},
					Untyped: &dto.Untyped{Value: new(1.)},
				},
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDHaHost)},
						{Name: new(tagDCName), Value: new(testDCHaDatacenter)},
						{Name: new(tagESXHostName), Value: new(testHostESXI)},
					},
					Untyped: &dto.Untyped{Value: new(1.)},
				},
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDHaHost)},
						{Name: new(tagDCName), Value: new(testDCHaDatacenter)},
						{Name: new(tagESXHostName), Value: new(testHostESXI)},
					},
					Untyped: &dto.Untyped{Value: new(1.)},
				},
			},
		},
		{
			Name: new("net_bits_recv"),
			Help: new(""),
			Type: dto.MetricType_UNTYPED.Enum(),
			Metric: []*dto.Metric{
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDVM10)},
						{Name: new(tagDCName), Value: new(testDCHaDatacenter)},
						{Name: new(tagESXHostName), Value: new(testHostESXI)},
						{Name: new("interface"), Value: new("*")},
						{Name: new(tagVMName), Value: new(testVMAlp1)},
					},
					Untyped: &dto.Untyped{Value: new(1.)},
				},
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDVM10)},
						{Name: new(tagDCName), Value: new(testDCHaDatacenter)},
						{Name: new(tagESXHostName), Value: new(testHostESXI)},
						{Name: new("interface"), Value: new("*")},
						{Name: new(tagVMName), Value: new(testVMAlp1)},
					},
					Untyped: &dto.Untyped{Value: new(1.)},
				},
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDHaHost)},
						{Name: new(tagDCName), Value: new(testDCHaDatacenter)},
						{Name: new(tagESXHostName), Value: new(testHostESXI)},
						{Name: new("interface"), Value: new("*")},
					},
					Untyped: &dto.Untyped{Value: new(1.)},
				},
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDHaHost)},
						{Name: new(tagDCName), Value: new(testDCHaDatacenter)},
						{Name: new(tagESXHostName), Value: new(testHostESXI)},
						{Name: new("interface"), Value: new("*")},
					},
					Untyped: &dto.Untyped{Value: new(1.)},
				},
			},
		},
		{
			Name: new("net_bits_sent"),
			Help: new(""),
			Type: dto.MetricType_UNTYPED.Enum(),
			Metric: []*dto.Metric{
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDVM10)},
						{Name: new(tagDCName), Value: new(testDCHaDatacenter)},
						{Name: new(tagESXHostName), Value: new(testHostESXI)},
						{Name: new("interface"), Value: new("*")},
						{Name: new(tagVMName), Value: new(testVMAlp1)},
					},
					Untyped: &dto.Untyped{Value: new(1.)},
				},
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDVM10)},
						{Name: new(tagDCName), Value: new(testDCHaDatacenter)},
						{Name: new(tagESXHostName), Value: new(testHostESXI)},
						{Name: new("interface"), Value: new("*")},
						{Name: new(tagVMName), Value: new(testVMAlp1)},
					},
					Untyped: &dto.Untyped{Value: new(1.)},
				},
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDHaHost)},
						{Name: new(tagDCName), Value: new(testDCHaDatacenter)},
						{Name: new(tagESXHostName), Value: new(testHostESXI)},
						{Name: new("interface"), Value: new("*")},
					},
					Untyped: &dto.Untyped{Value: new(1.)},
				},
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDHaHost)},
						{Name: new(tagDCName), Value: new(testDCHaDatacenter)},
						{Name: new(tagESXHostName), Value: new(testHostESXI)},
						{Name: new("interface"), Value: new("*")},
					},
					Untyped: &dto.Untyped{Value: new(1.)},
				},
			},
		},
		{
			Name: new("vms_running_count"),
			Help: new(""),
			Type: dto.MetricType_UNTYPED.Enum(),
			Metric: []*dto.Metric{
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDHaHost)},
						{Name: new(tagClusterName), Value: new(testHostESXI)},
						{Name: new(tagDCName), Value: new(testDCHaDatacenter)},
						{Name: new(tagESXHostName), Value: new(testHostESXI)},
					},
					Untyped: &dto.Untyped{Value: new(1.)},
				},
			},
		},
		{
			Name: new("vms_stopped_count"),
			Help: new(""),
			Type: dto.MetricType_UNTYPED.Enum(),
			Metric: []*dto.Metric{
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDHaHost)},
						{Name: new(tagClusterName), Value: new(testHostESXI)},
						{Name: new(tagDCName), Value: new(testDCHaDatacenter)},
						{Name: new(tagESXHostName), Value: new(testHostESXI)},
					},
					Untyped: &dto.Untyped{Value: new(1.)},
				},
			},
		},
		{
			Name: new("vsphere_vm_cpu_latency_perc"),
			Help: new(""),
			Type: dto.MetricType_UNTYPED.Enum(),
			Metric: []*dto.Metric{
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDVM10)},
						{Name: new(tagDCName), Value: new(testDCHaDatacenter)},
						{Name: new(tagESXHostName), Value: new(testHostESXI)},
						{Name: new(tagVMName), Value: new(testVMAlp1)},
					},
					Untyped: &dto.Untyped{Value: new(1.)},
				},
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDVM10)},
						{Name: new(tagDCName), Value: new(testDCHaDatacenter)},
						{Name: new(tagESXHostName), Value: new(testHostESXI)},
						{Name: new(tagVMName), Value: new(testVMAlp1)},
					},
					Untyped: &dto.Untyped{Value: new(1.)},
				},
			},
		},
	}

	ignoreUnexported := cmpopts.IgnoreUnexported([]any{dto.MetricFamily{}, dto.Metric{}, dto.LabelPair{}, dto.Untyped{}}...)
	ignoreUntypedValue := cmpopts.IgnoreFields(dto.Untyped{}, "Value")
	ignoreTimestamp := cmpopts.IgnoreFields(dto.Metric{}, "TimestampMs")
	opts := cmp.Options{ignoreUnexported, ignoreUntypedValue, ignoreTimestamp, cmp.Comparer(vSphereLabelComparer)}

	if diff := cmp.Diff(expectedMfs, mfs, opts, makeVirtualDiskMetricComparer(opts)); diff != "" {
		t.Errorf("Unexpected metric families (-want +got):\n%s", diff)
		// In case it still fails, we want to know for which offset it did so.
		ts := time.UnixMilli(mfs[0].GetMetric()[0].GetTimestampMs())
		t.Logf("Ts: %s / Offset: %d", ts, ((ts.Unix())/20)%100)
	}
}

//nolint:nolintlint,gofmt,dupl
func TestGatheringVcsim(t *testing.T) { //nolint:maintidx
	mfs, deferFn := setupGathering(t, "vcenter_1")
	defer deferFn()

	expectedMfs := []*dto.MetricFamily{
		{
			Name: new("cpu_used"),
			Help: new(""),
			Type: dto.MetricType_UNTYPED.Enum(),
			Metric: []*dto.Metric{
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDDomainC16)},
						{Name: new(tagClusterName), Value: new(testClusterDC0C0)},
						{Name: new(tagDCName), Value: new(testDCDC0)},
					},
					Untyped: &dto.Untyped{Value: new(1.)},
				},
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDDomainC16)},
						{Name: new(tagClusterName), Value: new(testClusterDC0C0)},
						{Name: new(tagDCName), Value: new(testDCDC0)},
					},
					Untyped: &dto.Untyped{Value: new(1.)},
				},
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDHost23)},
						{Name: new(tagClusterName), Value: new(testClusterDC0C0)},
						{Name: new(tagDCName), Value: new(testDCDC0)},
						{Name: new(tagESXHostName), Value: new(testHostDC0C0H0)},
					},
					Untyped: &dto.Untyped{Value: new(1.)},
				},
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDHost23)},
						{Name: new(tagClusterName), Value: new(testClusterDC0C0)},
						{Name: new(tagDCName), Value: new(testDCDC0)},
						{Name: new(tagESXHostName), Value: new(testHostDC0C0H0)},
					},
					Untyped: &dto.Untyped{Value: new(1.)},
				},
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDVM28)},
						{Name: new(tagClusterName), Value: new(testClusterDC0C0)},
						{Name: new(tagDCName), Value: new(testDCDC0)},
						{Name: new(tagESXHostName), Value: new(testHostDC0C0H0)},
						{Name: new(tagVMName), Value: new(testVMDC0C0RP0VM0)},
					},
					Untyped: &dto.Untyped{Value: new(1.0)},
				},
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDVM28)},
						{Name: new(tagClusterName), Value: new(testClusterDC0C0)},
						{Name: new(tagDCName), Value: new(testDCDC0)},
						{Name: new(tagESXHostName), Value: new(testHostDC0C0H0)},
						{Name: new(tagVMName), Value: new(testVMDC0C0RP0VM0)},
					},
					Untyped: &dto.Untyped{Value: new(1.)},
				},
			},
		},
		{
			Name: new("cpu_usedmhz"),
			Help: new(""),
			Type: dto.MetricType_UNTYPED.Enum(),
			Metric: []*dto.Metric{
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDHost23)},
						{Name: new(tagClusterName), Value: new(testClusterDC0C0)},
						{Name: new(tagDCName), Value: new(testDCDC0)},
						{Name: new(tagESXHostName), Value: new(testHostDC0C0H0)},
					},
					Untyped: &dto.Untyped{Value: new(1.)},
				},
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDHost23)},
						{Name: new(tagClusterName), Value: new(testClusterDC0C0)},
						{Name: new(tagDCName), Value: new(testDCDC0)},
						{Name: new(tagESXHostName), Value: new(testHostDC0C0H0)},
					},
					Untyped: &dto.Untyped{Value: new(1.)},
				},
			},
		},
		{
			Name: new("hosts_running_count"),
			Help: new(""),
			Type: dto.MetricType_UNTYPED.Enum(),
			Metric: []*dto.Metric{
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDDomainC16)},
						{Name: new(tagClusterName), Value: new(testClusterDC0C0)},
						{Name: new(tagDCName), Value: new(testDCDC0)},
					},
					Untyped: &dto.Untyped{Value: new(1.)},
				},
			},
		},
		{
			Name: new("hosts_stopped_count"),
			Help: new(""),
			Type: dto.MetricType_UNTYPED.Enum(),
			Metric: []*dto.Metric{
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDDomainC16)},
						{Name: new(tagClusterName), Value: new(testClusterDC0C0)},
						{Name: new(tagDCName), Value: new(testDCDC0)},
					},
					Untyped: &dto.Untyped{Value: new(1.)},
				},
			},
		},
		{
			Name: new("io_read_bytes"),
			Help: new(""),
			Type: dto.MetricType_UNTYPED.Enum(),
			Metric: []*dto.Metric{
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDVM28)},
						{Name: new(tagClusterName), Value: new(testClusterDC0C0)},
						{Name: new(tagDCName), Value: new(testDCDC0)},
						{Name: new("disk"), Value: new("*")},
						{Name: new(tagESXHostName), Value: new(testHostDC0C0H0)},
						{Name: new(tagVMName), Value: new(testVMDC0C0RP0VM0)},
					},
					Untyped: &dto.Untyped{Value: new(1.)},
				},
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDVM28)},
						{Name: new(tagClusterName), Value: new(testClusterDC0C0)},
						{Name: new(tagDCName), Value: new(testDCDC0)},
						{Name: new("disk"), Value: new("*")},
						{Name: new(tagESXHostName), Value: new(testHostDC0C0H0)},
						{Name: new(tagVMName), Value: new(testVMDC0C0RP0VM0)},
					},
					Untyped: &dto.Untyped{Value: new(1.)},
				},
			},
		},
		{
			Name: new("io_write_bytes"),
			Help: new(""),
			Type: dto.MetricType_UNTYPED.Enum(),
			Metric: []*dto.Metric{
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDVM28)},
						{Name: new(tagClusterName), Value: new(testClusterDC0C0)},
						{Name: new(tagDCName), Value: new(testDCDC0)},
						{Name: new("disk"), Value: new("*")},
						{Name: new(tagESXHostName), Value: new(testHostDC0C0H0)},
						{Name: new(tagVMName), Value: new(testVMDC0C0RP0VM0)},
					},
					Untyped: &dto.Untyped{Value: new(1.)},
				},
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDVM28)},
						{Name: new(tagClusterName), Value: new(testClusterDC0C0)},
						{Name: new(tagDCName), Value: new(testDCDC0)},
						{Name: new("disk"), Value: new("*")},
						{Name: new(tagESXHostName), Value: new(testHostDC0C0H0)},
						{Name: new(tagVMName), Value: new(testVMDC0C0RP0VM0)},
					},
					Untyped: &dto.Untyped{Value: new(1.)},
				},
			},
		},
		{
			Name: new("mem_total"),
			Help: new(""),
			Type: dto.MetricType_UNTYPED.Enum(),
			Metric: []*dto.Metric{
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDHost23)},
						{Name: new(tagClusterName), Value: new(testClusterDC0C0)},
						{Name: new(tagDCName), Value: new(testDCDC0)},
						{Name: new(tagESXHostName), Value: new(testHostDC0C0H0)},
					},
					Untyped: &dto.Untyped{Value: new(1.)},
				},
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDHost23)},
						{Name: new(tagClusterName), Value: new(testClusterDC0C0)},
						{Name: new(tagDCName), Value: new(testDCDC0)},
						{Name: new(tagESXHostName), Value: new(testHostDC0C0H0)},
					},
					Untyped: &dto.Untyped{Value: new(1.)},
				},
			},
		},
		{
			Name: new("mem_used_perc"),
			Help: new(""),
			Type: dto.MetricType_UNTYPED.Enum(),
			Metric: []*dto.Metric{
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDDomainC16)},
						{Name: new(tagClusterName), Value: new(testClusterDC0C0)},
						{Name: new(tagDCName), Value: new(testDCDC0)},
					},
					Untyped: &dto.Untyped{Value: new(1.)},
				},
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDDomainC16)},
						{Name: new(tagClusterName), Value: new(testClusterDC0C0)},
						{Name: new(tagDCName), Value: new(testDCDC0)},
					},
					Untyped: &dto.Untyped{Value: new(1.)},
				},
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDHost23)},
						{Name: new(tagClusterName), Value: new(testClusterDC0C0)},
						{Name: new(tagDCName), Value: new(testDCDC0)},
						{Name: new(tagESXHostName), Value: new(testHostDC0C0H0)},
					},
					Untyped: &dto.Untyped{Value: new(1.)},
				},
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDHost23)},
						{Name: new(tagClusterName), Value: new(testClusterDC0C0)},
						{Name: new(tagDCName), Value: new(testDCDC0)},
						{Name: new(tagESXHostName), Value: new(testHostDC0C0H0)},
					},
					Untyped: &dto.Untyped{Value: new(1.)},
				},
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDVM28)},
						{Name: new(tagClusterName), Value: new(testClusterDC0C0)},
						{Name: new(tagDCName), Value: new(testDCDC0)},
						{Name: new(tagESXHostName), Value: new(testHostDC0C0H0)},
						{Name: new(tagVMName), Value: new(testVMDC0C0RP0VM0)},
					},
					Untyped: &dto.Untyped{Value: new(1.)},
				},
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDVM28)},
						{Name: new(tagClusterName), Value: new(testClusterDC0C0)},
						{Name: new(tagDCName), Value: new(testDCDC0)},
						{Name: new(tagESXHostName), Value: new(testHostDC0C0H0)},
						{Name: new(tagVMName), Value: new(testVMDC0C0RP0VM0)},
					},
					Untyped: &dto.Untyped{Value: new(1.)},
				},
			},
		},
		{
			Name: new("net_bits_recv"),
			Help: new(""),
			Type: dto.MetricType_UNTYPED.Enum(),
			Metric: []*dto.Metric{
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDHost23)},
						{Name: new(tagClusterName), Value: new(testClusterDC0C0)},
						{Name: new(tagDCName), Value: new(testDCDC0)},
						{Name: new(tagESXHostName), Value: new(testHostDC0C0H0)},
						{Name: new("interface"), Value: new("*")},
					},
					Untyped: &dto.Untyped{Value: new(1.)},
				},
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDHost23)},
						{Name: new(tagClusterName), Value: new(testClusterDC0C0)},
						{Name: new(tagDCName), Value: new(testDCDC0)},
						{Name: new(tagESXHostName), Value: new(testHostDC0C0H0)},
						{Name: new("interface"), Value: new("*")},
					},
					Untyped: &dto.Untyped{Value: new(1.)},
				},
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDVM28)},
						{Name: new(tagClusterName), Value: new(testClusterDC0C0)},
						{Name: new(tagDCName), Value: new(testDCDC0)},
						{Name: new(tagESXHostName), Value: new(testHostDC0C0H0)},
						{Name: new("interface"), Value: new("*")},
						{Name: new(tagVMName), Value: new(testVMDC0C0RP0VM0)},
					},
					Untyped: &dto.Untyped{Value: new(1.)},
				},
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDVM28)},
						{Name: new(tagClusterName), Value: new(testClusterDC0C0)},
						{Name: new(tagDCName), Value: new(testDCDC0)},
						{Name: new(tagESXHostName), Value: new(testHostDC0C0H0)},
						{Name: new("interface"), Value: new("*")},
						{Name: new(tagVMName), Value: new(testVMDC0C0RP0VM0)},
					},
					Untyped: &dto.Untyped{Value: new(1.)},
				},
			},
		},
		{
			Name: new("net_bits_sent"),
			Help: new(""),
			Type: dto.MetricType_UNTYPED.Enum(),
			Metric: []*dto.Metric{
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDHost23)},
						{Name: new(tagClusterName), Value: new(testClusterDC0C0)},
						{Name: new(tagDCName), Value: new(testDCDC0)},
						{Name: new(tagESXHostName), Value: new(testHostDC0C0H0)},
						{Name: new("interface"), Value: new("*")},
					},
					Untyped: &dto.Untyped{Value: new(1.)},
				},
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDHost23)},
						{Name: new(tagClusterName), Value: new(testClusterDC0C0)},
						{Name: new(tagDCName), Value: new(testDCDC0)},
						{Name: new(tagESXHostName), Value: new(testHostDC0C0H0)},
						{Name: new("interface"), Value: new("*")},
					},
					Untyped: &dto.Untyped{Value: new(1.)},
				},
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDVM28)},
						{Name: new(tagClusterName), Value: new(testClusterDC0C0)},
						{Name: new(tagDCName), Value: new(testDCDC0)},
						{Name: new(tagESXHostName), Value: new(testHostDC0C0H0)},
						{Name: new("interface"), Value: new("*")},
						{Name: new(tagVMName), Value: new(testVMDC0C0RP0VM0)},
					},
					Untyped: &dto.Untyped{Value: new(1.)},
				},
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDVM28)},
						{Name: new(tagClusterName), Value: new(testClusterDC0C0)},
						{Name: new(tagDCName), Value: new(testDCDC0)},
						{Name: new(tagESXHostName), Value: new(testHostDC0C0H0)},
						{Name: new("interface"), Value: new("*")},
						{Name: new(tagVMName), Value: new(testVMDC0C0RP0VM0)},
					},
					Untyped: &dto.Untyped{Value: new(1.)},
				},
			},
		},
		{
			Name: new("swap_out"),
			Help: new(""),
			Type: dto.MetricType_UNTYPED.Enum(),
			Metric: []*dto.Metric{
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDHost23)},
						{Name: new(tagClusterName), Value: new(testClusterDC0C0)},
						{Name: new(tagDCName), Value: new(testDCDC0)},
						{Name: new(tagESXHostName), Value: new(testHostDC0C0H0)},
					},
					Untyped: &dto.Untyped{Value: new(1.)},
				},
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDHost23)},
						{Name: new(tagClusterName), Value: new(testClusterDC0C0)},
						{Name: new(tagDCName), Value: new(testDCDC0)},
						{Name: new(tagESXHostName), Value: new(testHostDC0C0H0)},
					},
					Untyped: &dto.Untyped{Value: new(1.)},
				},
			},
		},
		{
			Name: new("swap_used"),
			Help: new(""),
			Type: dto.MetricType_UNTYPED.Enum(),
			Metric: []*dto.Metric{
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDVM28)},
						{Name: new(tagClusterName), Value: new(testClusterDC0C0)},
						{Name: new(tagDCName), Value: new(testDCDC0)},
						{Name: new(tagESXHostName), Value: new(testHostDC0C0H0)},
						{Name: new(tagVMName), Value: new(testVMDC0C0RP0VM0)},
					},
					Untyped: &dto.Untyped{Value: new(1.)},
				},
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDVM28)},
						{Name: new(tagClusterName), Value: new(testClusterDC0C0)},
						{Name: new(tagDCName), Value: new(testDCDC0)},
						{Name: new(tagESXHostName), Value: new(testHostDC0C0H0)},
						{Name: new(tagVMName), Value: new(testVMDC0C0RP0VM0)},
					},
					Untyped: &dto.Untyped{Value: new(1.)},
				},
			},
		},
		{
			Name: new("vms_running_count"),
			Help: new(""),
			Type: dto.MetricType_UNTYPED.Enum(),
			Metric: []*dto.Metric{
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDHost23)},
						{Name: new(tagClusterName), Value: new(testClusterDC0C0)},
						{Name: new(tagDCName), Value: new(testDCDC0)},
						{Name: new(tagESXHostName), Value: new(testHostDC0C0H0)},
					},
					Untyped: &dto.Untyped{Value: new(1.)},
				},
			},
		},
		{
			Name: new("vms_stopped_count"),
			Help: new(""),
			Type: dto.MetricType_UNTYPED.Enum(),
			Metric: []*dto.Metric{
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDHost23)},
						{Name: new(tagClusterName), Value: new(testClusterDC0C0)},
						{Name: new(tagDCName), Value: new(testDCDC0)},
						{Name: new(tagESXHostName), Value: new(testHostDC0C0H0)},
					},
					Untyped: &dto.Untyped{Value: new(1.)},
				},
			},
		},
		{
			Name: new("vsphere_vm_cpu_latency_perc"),
			Help: new(""),
			Type: dto.MetricType_UNTYPED.Enum(),
			Metric: []*dto.Metric{
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDVM28)},
						{Name: new(tagClusterName), Value: new(testClusterDC0C0)},
						{Name: new(tagDCName), Value: new(testDCDC0)},
						{Name: new(tagESXHostName), Value: new(testHostDC0C0H0)},
						{Name: new(tagVMName), Value: new(testVMDC0C0RP0VM0)},
					},
					Untyped: &dto.Untyped{Value: new(1.)},
				},
				{
					Label: []*dto.LabelPair{
						{Name: new(types.LabelMetaVSphere), Value: new(testMetaVSphereAddr)},
						{Name: new(types.LabelMetaVSphereMOID), Value: new(testMOIDVM28)},
						{Name: new(tagClusterName), Value: new(testClusterDC0C0)},
						{Name: new(tagDCName), Value: new(testDCDC0)},
						{Name: new(tagESXHostName), Value: new(testHostDC0C0H0)},
						{Name: new(tagVMName), Value: new(testVMDC0C0RP0VM0)},
					},
					Untyped: &dto.Untyped{Value: new(1.)},
				},
			},
		},
	}

	ignoreUnexported := cmpopts.IgnoreUnexported([]any{dto.MetricFamily{}, dto.Metric{}, dto.LabelPair{}, dto.Untyped{}}...)
	ignoreUntypedValue := cmpopts.IgnoreFields(dto.Untyped{}, "Value")
	ignoreTimestamp := cmpopts.IgnoreFields(dto.Metric{}, "TimestampMs")
	opts := cmp.Options{ignoreUnexported, ignoreUntypedValue, ignoreTimestamp, cmp.Comparer(vSphereLabelComparer)}

	if diff := cmp.Diff(expectedMfs, mfs, opts, makeVirtualDiskMetricComparer(opts)); diff != "" {
		t.Errorf("Unexpected metric families (-want +got):\n%s", diff)
		// In case it still fails, we want to know for which offset it did so.
		ts := time.UnixMilli(mfs[0].GetMetric()[0].GetTimestampMs())
		t.Logf("Ts: %s / Offset: %d", ts, ((ts.Unix())/20)%100)
	}
}

// vSphereLabelComparer handles the comparison between two types.LabelMetaVSphere labels,
// which is unpredictable because the port used by the simulator is random.
func vSphereLabelComparer(x, y *dto.LabelPair) bool {
	if x.GetName() == types.LabelMetaVSphere && y.GetName() == types.LabelMetaVSphere {
		xParts, yParts := strings.Split(x.GetValue(), ":"), strings.Split(y.GetValue(), ":")
		if len(xParts) != 2 || len(yParts) != 2 {
			return false
		}

		return xParts[0] == yParts[0]
	}

	return cmp.Equal(x, y, cmpopts.IgnoreUnexported(dto.LabelPair{}))
}

// makeVirtualDiskMetricComparer returns a comparer that tolerates having
// non-matching metrics within the "io_read_bytes" family.
// This can happen because the simulator sometimes seems to return 1 extra point,
// a minute in the past, but only for this particular metric ...
func makeVirtualDiskMetricComparer(opts []cmp.Option) cmp.Option {
	return cmp.Comparer(func(x, y *dto.MetricFamily) bool {
		if x.GetName() == "io_read_bytes" && y.GetName() == "io_read_bytes" {
			return cmp.Equal(x, y, append(opts, cmpopts.IgnoreFields(dto.MetricFamily{}, "Metric"))...)
		}

		return cmp.Equal(x, y, opts...)
	})
}

func TestRetainedMetricsSort(t *testing.T) {
	t0 := time.Date(2024, time.January, 8, 14, 0, 0, 0, time.Local) //nolint: gosmopolitan
	retained := retainedMetrics{
		measurementVSphereHostCPU: []addedField{
			{
				field: testFieldUsageMHzAvg,
				value: 2,
				tags:  map[string]string{tagDCName: testDCHaDatacenter},
				t:     []time.Time{t0.Add(time.Minute)},
			},
			{
				field: testFieldUsageMHzAvg,
				value: 1,
				tags:  map[string]string{tagDCName: testDCHaDatacenter},
				t:     []time.Time{t0},
			},
		},
		measurementVSphereHostMem: []addedField{
			{
				field: testFieldUsageAvg,
				value: 1,
				tags:  map[string]string{tagDCName: testDCHaDatacenter},
				t:     []time.Time{},
			},
			{
				field: testFieldUsageAvg,
				value: 3,
				tags:  map[string]string{tagDCName: testDCHaDatacenter},
				t:     []time.Time{t0},
			},
			{
				field: testFieldUsageAvg,
				value: 2,
				tags:  map[string]string{tagDCName: testDCHaDatacenter},
				t:     []time.Time{t0.Add(-1 * time.Minute)},
			},
		},
	}
	expected := retainedMetrics{
		measurementVSphereHostCPU: []addedField{
			{
				field: testFieldUsageMHzAvg,
				value: 1,
				tags:  map[string]string{tagDCName: testDCHaDatacenter},
				t:     []time.Time{t0},
			},
			{
				field: testFieldUsageMHzAvg,
				value: 2,
				tags:  map[string]string{tagDCName: testDCHaDatacenter},
				t:     []time.Time{t0.Add(time.Minute)},
			},
		},
		measurementVSphereHostMem: []addedField{
			{
				field: testFieldUsageAvg,
				value: 1,
				tags:  map[string]string{tagDCName: testDCHaDatacenter},
				t:     []time.Time{},
			},
			{
				field: testFieldUsageAvg,
				value: 2,
				tags:  map[string]string{tagDCName: testDCHaDatacenter},
				t:     []time.Time{t0.Add(-1 * time.Minute)},
			},
			{
				field: testFieldUsageAvg,
				value: 3,
				tags:  map[string]string{tagDCName: testDCHaDatacenter},
				t:     []time.Time{t0},
			},
		},
	}

	retained.sort()

	if diff := cmp.Diff(expected, retained, cmp.AllowUnexported(addedField{})); diff != "" {
		t.Fatalf("Unexpected sort result (-want +got):\n%s", diff)
	}
}
