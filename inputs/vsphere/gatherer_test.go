package vsphere

import (
	"context"
	"glouton/config"
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

func TestGatheringESXI(t *testing.T) {
	vSphereCfg, deferFn := setupVSphereAPITest(t, "esxi_1")
	defer deferFn()

	ctx, cancel := context.WithTimeout(context.Background(), commonTimeout)
	defer cancel()

	manager := new(Manager)
	manager.RegisterGatherers(ctx, []config.VSphere{vSphereCfg}, func(opt registry.RegistrationOption, gatherer prometheus.Gatherer) (int, error) { return 0, nil }, nil)

	manager.Devices(ctx, 0)

	mfsPerVSphere := make(map[string][]*io_prometheus_client.MetricFamily, len(manager.vSpheres))

	for host, vSphere := range manager.vSpheres {
		mfs, err := vSphere.gatherer.GatherWithState(ctx, registry.GatherState{T0: time.Now(), FromScrapeLoop: true})
		if err != nil {
			t.Fatalf("Got an error while gathering vSphere %q: %v", host, err)
		}

		mfsPerVSphere[strings.Split(host, ":")[0]] = mfs
	}

	expectedMfs := []*io_prometheus_client.MetricFamily{
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
					Untyped: &io_prometheus_client.Untyped{Value: ptr(6.161370103719307)},
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
					Untyped: &io_prometheus_client.Untyped{Value: ptr(27.40969523872782)},
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
					Untyped: &io_prometheus_client.Untyped{Value: ptr(2.)},
				},
			},
		},
	}

	ignoreUnexported := []any{io_prometheus_client.MetricFamily{}, io_prometheus_client.Metric{}, io_prometheus_client.LabelPair{}, io_prometheus_client.Untyped{}}

	expectedMfsPerVSphere := map[string][]*io_prometheus_client.MetricFamily{"127.0.0.1": expectedMfs}
	if diff := cmp.Diff(expectedMfsPerVSphere, mfsPerVSphere, cmpopts.IgnoreUnexported(ignoreUnexported...), cmp.Comparer(vSphereLabelComparer)); diff != "" {
		t.Errorf("Unexpected metric families (-want +got):\n%s", diff)
	}
}

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
