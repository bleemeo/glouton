package kubernetes

import (
	"context"
	"errors"
	"glouton/facts"
	"glouton/facts/container-runtime/containerd"
	"glouton/facts/container-runtime/docker"
	"glouton/facts/container-runtime/internal/testutil"
	crTypes "glouton/facts/container-runtime/types"
	"io/ioutil"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/version"
	"sigs.k8s.io/yaml"
)

var errNotImplemented = errors.New("not implemented")

type mockKubernetesClient struct {
	node     corev1.NodeList
	pods     corev1.PodList
	versions struct {
		ClientVersion *version.Info `json:"clientVersion"`
		ServerVersion *version.Info `json:"serverVersion"`
	}
}

func newKubernetesMock(dirname string) (*mockKubernetesClient, error) {
	result := &mockKubernetesClient{}

	data, err := ioutil.ReadFile(filepath.Join(dirname, "node.yaml"))
	if err == nil {
		err = yaml.Unmarshal(data, &result.node)
		if err != nil {
			return nil, err
		}
	}

	data, err = ioutil.ReadFile(filepath.Join(dirname, "version.yaml"))
	if err == nil {
		err = yaml.Unmarshal(data, &result.versions)
		if err != nil {
			return nil, err
		}
	}

	data, err = ioutil.ReadFile(filepath.Join(dirname, "pods.yaml"))
	if err == nil {
		err = yaml.Unmarshal(data, &result.pods)
		if err != nil {
			return nil, err
		}
	}

	return result, err
}

func (k *mockKubernetesClient) GetNode(ctx context.Context, nodeName string) (*corev1.Node, error) {
	if len(k.node.Items) == 0 {
		return nil, errNotImplemented
	}

	return &k.node.Items[0], nil
}

func (k *mockKubernetesClient) GetPODs(ctx context.Context, nodeName string) ([]corev1.Pod, error) {
	return k.pods.Items, nil
}

func (k *mockKubernetesClient) GetServerVersion(ctx context.Context) (*version.Info, error) {
	return k.versions.ServerVersion, nil
}

func TestKubernetes_Containers(t *testing.T) { // nolint: gocyclo
	tests := []struct {
		name          string
		dir           string
		createRuntime func(dirname string) (crTypes.RuntimeInterface, error)
		wantContainer []facts.FakeContainer
		wantFacts     map[string]string
	}{
		{
			name: "with-docker",
			dir:  "testdata/with-docker-v1.20.0",
			createRuntime: func(dirname string) (crTypes.RuntimeInterface, error) {
				dockerClient, err := docker.NewDockerMock(dirname)
				if err != nil {
					return nil, err
				}

				return docker.FakeDocker(dockerClient), nil
			},
			wantContainer: []facts.FakeContainer{
				{
					FakeID:             "035094872e87d77cc4a1ed894248c8cd3283457d6885108324f95c74649062f6",
					FakeContainerName:  "k8s_rabbitmq_rabbitmq-labels-74cfb594d8-zfdmb_default_173e7224-1fef-485d-bb72-30d45e46a551_0",
					FakePrimaryAddress: "172.17.0.6",
					FakeListenAddresses: []facts.ListenAddress{
						{
							Address:       "172.17.0.6",
							NetworkFamily: "tcp",
							Port:          4369,
						},
						{
							Address:       "172.17.0.6",
							NetworkFamily: "tcp",
							Port:          5671,
						},
						{
							Address:       "172.17.0.6",
							NetworkFamily: "tcp",
							Port:          5672,
						},
						{
							Address:       "172.17.0.6",
							NetworkFamily: "tcp",
							Port:          15691,
						},
						{
							Address:       "172.17.0.6",
							NetworkFamily: "tcp",
							Port:          15692,
						},
						{
							Address:       "172.17.0.6",
							NetworkFamily: "tcp",
							Port:          25672,
						},
					},
					FakeListenAddressesExplicit: false,
					FakeAnnotations: map[string]string{
						"glouton.check.ignore.port.5671": "on",
						"glouton.check.ignore.port.4369": "TruE",
					},
					FakePodName:      "rabbitmq-labels-74cfb594d8-zfdmb",
					FakePodNamespace: "default",
					FakeHealth:       facts.ContainerNoHealthCheck,
					TestHasPod:       true,
				},
				{
					FakeID:             "35de2017cb16bfd1423d9b9f567f647f687a3fdc0f855fe2535ff81de7adf04f",
					FakeContainerName:  "k8s_rabbitmq_rabbitmq-container-port-66fdd44ccd-pk7rv_default_6d0e2a22-50ab-492f-a303-d477f3d8e3de_0",
					FakePrimaryAddress: "172.17.0.2",
					FakeListenAddresses: []facts.ListenAddress{
						{
							Address:       "172.17.0.2",
							NetworkFamily: "tcp",
							Port:          5672,
						},
					},
					FakeListenAddressesExplicit: true,
					FakePodName:                 "rabbitmq-container-port-66fdd44ccd-pk7rv",
					FakePodNamespace:            "default",
					FakeHealth:                  facts.ContainerNoHealthCheck,
					TestHasPod:                  true,
				},
				{
					FakeID:             "343a08aa54b463ed783a7b847902f70c0fca63f5d1f16f10cb4cee97904b4f84",
					FakeContainerName:  "k8s_a-memcached_redis-memcached-56dfc4cbfc-2m2cq_default_c5bced17-e72c-4668-8329-76fa19cda44e_0",
					FakePrimaryAddress: "172.17.0.5",
					FakeAnnotations: map[string]string{
						"glouton.enable": "off",
					},
					FakeListenAddresses: []facts.ListenAddress{
						{
							Address:       "172.17.0.5",
							Port:          11211,
							NetworkFamily: "tcp",
						},
					},
					FakeListenAddressesExplicit: false,
					FakePodName:                 "redis-memcached-56dfc4cbfc-2m2cq",
					FakePodNamespace:            "default",
					FakeHealth:                  facts.ContainerNoHealthCheck,
					TestIgnored:                 true,
					TestHasPod:                  true,
				},
				{
					FakeID:             "ffb768523fa85dd12cf0e35d11b764c5df747a243532ef29855137a52a849726",
					FakeContainerName:  "k8s_the-redis_redis-memcached-56dfc4cbfc-2m2cq_default_c5bced17-e72c-4668-8329-76fa19cda44e_0",
					FakePrimaryAddress: "172.17.0.5",
					FakeAnnotations: map[string]string{
						"glouton.enable": "off",
					},
					FakeListenAddresses: []facts.ListenAddress{
						{
							Address:       "172.17.0.5",
							Port:          6363,
							NetworkFamily: "tcp",
						},
					},
					FakeListenAddressesExplicit: true,
					FakePodName:                 "redis-memcached-56dfc4cbfc-2m2cq",
					FakePodNamespace:            "default",
					FakeHealth:                  facts.ContainerNoHealthCheck,
					TestIgnored:                 true,
					TestHasPod:                  true,
				},
				{
					FakeID:                 "0518c93817b136b9a06b0c65649cde4901dcb0efdd5fac603cdb8a543bd54d04",
					FakeContainerName:      "k8s_true_delete-me-once-584c74ccf5-hmb77_default_3db6f913-cc23-4e70-9c08-7bdcb73eb8c1_0",
					FakeStoppedAndReplaced: true,
					FakePodName:            "delete-me-once-584c74ccf5-hmb77",
					FakePodNamespace:       "default",
					FakeHealth:             facts.ContainerNoHealthCheck,
					TestHasPod:             true,
					TestIgnored:            true,
				},
				{
					FakeID:                 "967b51dffe07684eeaa6dd8c93a572eb1562e9ac5d0ea020498fd4a6df0e59e4",
					FakeContainerName:      "k8s_true_delete-me-once-584c74ccf5-hmb77_default_3db6f913-cc23-4e70-9c08-7bdcb73eb8c1_1",
					FakeStoppedAndReplaced: false,
					FakePodName:            "delete-me-once-584c74ccf5-hmb77",
					FakePodNamespace:       "default",
					FakeHealth:             facts.ContainerNoHealthCheck,
					TestHasPod:             true,
				},
				{
					FakeID:            "5caa874de6b37554139e2a05fced71488b823256e0691b968b69679115407cb3",
					FakeContainerName: "docker_default_without_k8s",
					FakeLabels: map[string]string{
						"test": "42",
					},
					FakePrimaryAddress: "172.17.0.8",
					FakeListenAddresses: []facts.ListenAddress{
						{
							Address:       "172.17.0.8",
							Port:          6379,
							NetworkFamily: "tcp",
						},
					},
					FakeListenAddressesExplicit: false,
					FakeHealth:                  facts.ContainerNoHealthCheck,
					TestHasPod:                  false,
				},
				{
					FakeID:            "403c412037a8ce9efb91f9ddcc91e522146df3b01aa42a6b928987ecc36e8cf0",
					FakeContainerName: "k8s_POD_rabbitmq-labels-74cfb594d8-zfdmb_default_173e7224-1fef-485d-bb72-30d45e46a551_0",
					FakeImageName:     "k8s.gcr.io/pause:3.2",
					FakeHealth:        facts.ContainerNoHealthCheck,
					TestIgnored:       true,
					TestHasPod:        true,
				},
				{
					FakeID:            "f7c72cd6533b6a873e8c0bec3c612216afb48777a6fe437421470b8a9aec6867",
					FakeContainerName: "k8s_POD_redis-memcached-56dfc4cbfc-2m2cq_default_c5bced17-e72c-4668-8329-76fa19cda44e_0",
					FakeHealth:        facts.ContainerNoHealthCheck,
					TestIgnored:       true,
					TestHasPod:        true,
				},
			},
			wantFacts: map[string]string{
				"kubernetes_version": "v1.20.0",
				"kubelet_version":    "v1.20.0",
			},
		},
		{
			name: "with-docker-labels-stripped",
			dir:  "testdata/with-docker-v1.20.0",
			createRuntime: func(dirname string) (crTypes.RuntimeInterface, error) {
				dockerClient, err := docker.NewDockerMock(dirname)
				if err != nil {
					return nil, err
				}

				for i := range dockerClient.Containers {
					dockerClient.Containers[i].Config.Labels = nil
				}

				return docker.FakeDocker(dockerClient), nil
			},
			wantContainer: []facts.FakeContainer{
				{
					FakeID:             "035094872e87d77cc4a1ed894248c8cd3283457d6885108324f95c74649062f6",
					FakeContainerName:  "k8s_rabbitmq_rabbitmq-labels-74cfb594d8-zfdmb_default_173e7224-1fef-485d-bb72-30d45e46a551_0",
					FakePrimaryAddress: "172.17.0.6",
					FakeListenAddresses: []facts.ListenAddress{
						{
							Address:       "172.17.0.6",
							NetworkFamily: "tcp",
							Port:          4369,
						},
						{
							Address:       "172.17.0.6",
							NetworkFamily: "tcp",
							Port:          5671,
						},
						{
							Address:       "172.17.0.6",
							NetworkFamily: "tcp",
							Port:          5672,
						},
						{
							Address:       "172.17.0.6",
							NetworkFamily: "tcp",
							Port:          15691,
						},
						{
							Address:       "172.17.0.6",
							NetworkFamily: "tcp",
							Port:          15692,
						},
						{
							Address:       "172.17.0.6",
							NetworkFamily: "tcp",
							Port:          25672,
						},
					},
					FakeListenAddressesExplicit: false,
					FakeAnnotations: map[string]string{
						"glouton.check.ignore.port.5671": "on",
						"glouton.check.ignore.port.4369": "TruE",
					},
					FakePodName:      "rabbitmq-labels-74cfb594d8-zfdmb",
					FakePodNamespace: "default",
					FakeHealth:       facts.ContainerNoHealthCheck,
					TestHasPod:       true,
				},
				{
					FakeID:             "35de2017cb16bfd1423d9b9f567f647f687a3fdc0f855fe2535ff81de7adf04f",
					FakeContainerName:  "k8s_rabbitmq_rabbitmq-container-port-66fdd44ccd-pk7rv_default_6d0e2a22-50ab-492f-a303-d477f3d8e3de_0",
					FakePrimaryAddress: "172.17.0.2",
					FakeListenAddresses: []facts.ListenAddress{
						{
							Address:       "172.17.0.2",
							NetworkFamily: "tcp",
							Port:          5672,
						},
					},
					FakeListenAddressesExplicit: true,
					FakePodName:                 "rabbitmq-container-port-66fdd44ccd-pk7rv",
					FakePodNamespace:            "default",
					FakeHealth:                  facts.ContainerNoHealthCheck,
					TestHasPod:                  true,
				},
				{
					FakeID:             "343a08aa54b463ed783a7b847902f70c0fca63f5d1f16f10cb4cee97904b4f84",
					FakeContainerName:  "k8s_a-memcached_redis-memcached-56dfc4cbfc-2m2cq_default_c5bced17-e72c-4668-8329-76fa19cda44e_0",
					FakePrimaryAddress: "172.17.0.5",
					FakeAnnotations: map[string]string{
						"glouton.enable": "off",
					},
					FakeListenAddresses: []facts.ListenAddress{
						{
							Address:       "172.17.0.5",
							Port:          11211,
							NetworkFamily: "tcp",
						},
					},
					FakeListenAddressesExplicit: false,
					FakePodName:                 "redis-memcached-56dfc4cbfc-2m2cq",
					FakePodNamespace:            "default",
					FakeHealth:                  facts.ContainerNoHealthCheck,
					TestIgnored:                 true,
					TestHasPod:                  true,
				},
				{
					FakeID:             "ffb768523fa85dd12cf0e35d11b764c5df747a243532ef29855137a52a849726",
					FakeContainerName:  "k8s_the-redis_redis-memcached-56dfc4cbfc-2m2cq_default_c5bced17-e72c-4668-8329-76fa19cda44e_0",
					FakePrimaryAddress: "172.17.0.5",
					FakeAnnotations: map[string]string{
						"glouton.enable": "off",
					},
					FakeListenAddresses: []facts.ListenAddress{
						{
							Address:       "172.17.0.5",
							Port:          6363,
							NetworkFamily: "tcp",
						},
					},
					FakeListenAddressesExplicit: true,
					FakePodName:                 "redis-memcached-56dfc4cbfc-2m2cq",
					FakePodNamespace:            "default",
					FakeHealth:                  facts.ContainerNoHealthCheck,
					TestIgnored:                 true,
					TestHasPod:                  true,
				},
				{
					FakeID:                 "0518c93817b136b9a06b0c65649cde4901dcb0efdd5fac603cdb8a543bd54d04",
					FakeContainerName:      "k8s_true_delete-me-once-584c74ccf5-hmb77_default_3db6f913-cc23-4e70-9c08-7bdcb73eb8c1_0",
					FakeStoppedAndReplaced: true,
					FakePodName:            "delete-me-once-584c74ccf5-hmb77",
					FakePodNamespace:       "default",
					FakeHealth:             facts.ContainerNoHealthCheck,
					TestHasPod:             true,
					TestIgnored:            true,
				},
				{
					FakeID:                 "967b51dffe07684eeaa6dd8c93a572eb1562e9ac5d0ea020498fd4a6df0e59e4",
					FakeContainerName:      "k8s_true_delete-me-once-584c74ccf5-hmb77_default_3db6f913-cc23-4e70-9c08-7bdcb73eb8c1_1",
					FakeStoppedAndReplaced: false,
					FakePodName:            "delete-me-once-584c74ccf5-hmb77",
					FakePodNamespace:       "default",
					FakeHealth:             facts.ContainerNoHealthCheck,
					TestHasPod:             true,
				},
				{
					FakeID:             "5caa874de6b37554139e2a05fced71488b823256e0691b968b69679115407cb3",
					FakeContainerName:  "docker_default_without_k8s",
					FakePrimaryAddress: "172.17.0.8",
					FakeListenAddresses: []facts.ListenAddress{
						{
							Address:       "172.17.0.8",
							Port:          6379,
							NetworkFamily: "tcp",
						},
					},
					FakeListenAddressesExplicit: false,
					FakeHealth:                  facts.ContainerNoHealthCheck,
					TestHasPod:                  false,
				},
				{
					FakeID:            "403c412037a8ce9efb91f9ddcc91e522146df3b01aa42a6b928987ecc36e8cf0",
					FakeContainerName: "k8s_POD_rabbitmq-labels-74cfb594d8-zfdmb_default_173e7224-1fef-485d-bb72-30d45e46a551_0",
					FakeImageName:     "k8s.gcr.io/pause:3.2",
					FakeHealth:        facts.ContainerNoHealthCheck,
					TestIgnored:       false, // Exclusion of POD container require labels on Docker
					TestHasPod:        false, // without labels, we don't known this a POD
				},
				{
					FakeID:            "f7c72cd6533b6a873e8c0bec3c612216afb48777a6fe437421470b8a9aec6867",
					FakeContainerName: "k8s_POD_redis-memcached-56dfc4cbfc-2m2cq_default_c5bced17-e72c-4668-8329-76fa19cda44e_0",
					FakeHealth:        facts.ContainerNoHealthCheck,
					TestIgnored:       false, // Exclusion of POD container require labels on Docker
					TestHasPod:        false, // without labels, we don't known this a POD
				},
			},
			wantFacts: map[string]string{
				"kubernetes_version": "v1.20.0",
				"kubelet_version":    "v1.20.0",
			},
		},
		{
			name: "In virtualbox",
			dir:  "testdata/with-docker-in-vbox-v1.18.0",
			createRuntime: func(dirname string) (crTypes.RuntimeInterface, error) {
				dockerClient, err := docker.NewDockerMock(dirname)
				if err != nil {
					return nil, err
				}

				return docker.FakeDocker(dockerClient), nil
			},
			wantContainer: []facts.FakeContainer{
				{
					FakeID:             "f51d48c545596c5e082f6a389b35a0118f4e8747082bdc8f6c8a59ec5b8aaeb7",
					FakeContainerName:  "k8s_rabbitmq_rabbitmq-labels-7fbb75dcd7-h6t28_default_f071e8b4-0b84-4d02-bdb7-60a817874385_0",
					FakePrimaryAddress: "10.88.0.6",
					FakeListenAddresses: []facts.ListenAddress{
						{
							Address:       "10.88.0.6",
							NetworkFamily: "tcp",
							Port:          4369,
						},
						{
							Address:       "10.88.0.6",
							NetworkFamily: "tcp",
							Port:          5671,
						},
						{
							Address:       "10.88.0.6",
							NetworkFamily: "tcp",
							Port:          5672,
						},
						{
							Address:       "10.88.0.6",
							NetworkFamily: "tcp",
							Port:          15691,
						},
						{
							Address:       "10.88.0.6",
							NetworkFamily: "tcp",
							Port:          15692,
						},
						{
							Address:       "10.88.0.6",
							NetworkFamily: "tcp",
							Port:          25672,
						},
					},
					FakeListenAddressesExplicit: false,
					FakeAnnotations: map[string]string{
						"glouton.check.ignore.port.5671": "on",
						"glouton.check.ignore.port.4369": "TruE",
					},
					FakePodName:      "rabbitmq-labels-7fbb75dcd7-h6t28",
					FakePodNamespace: "default",
					FakeHealth:       facts.ContainerNoHealthCheck,
					TestHasPod:       true,
				},
				{
					FakeID:             "8621fe83ccb9ad96da3b250d138c165d69ad3754053a61e99c980f4ce0dbc897",
					FakeContainerName:  "k8s_rabbitmq_rabbitmq-container-port-68c84fdd9-w5cdk_default_22b46f0b-ce48-4c0a-a70e-8b4596ef83fc_0",
					FakePrimaryAddress: "10.88.0.4",
					FakeListenAddresses: []facts.ListenAddress{
						{
							Address:       "10.88.0.4",
							NetworkFamily: "tcp",
							Port:          5672,
						},
					},
					FakeListenAddressesExplicit: true,
					FakePodName:                 "rabbitmq-container-port-68c84fdd9-w5cdk",
					FakePodNamespace:            "default",
					FakeHealth:                  facts.ContainerNoHealthCheck,
					TestHasPod:                  true,
				},
				{
					FakeID:             "d4a8b68f5f47a7388598e924981ac88d1489abbc8e4175bf4a5fd0f8ce02718a",
					FakeContainerName:  "k8s_a-memcached_redis-memcached-78f799c9c8-2gzks_default_f62b1b74-686e-43ae-9cf6-342b5bdbbda6_0",
					FakePrimaryAddress: "10.88.0.8",
					FakeAnnotations: map[string]string{
						"glouton.enable": "off",
					},
					FakeListenAddresses: []facts.ListenAddress{
						{
							Address:       "10.88.0.8",
							Port:          11211,
							NetworkFamily: "tcp",
						},
					},
					FakeListenAddressesExplicit: false,
					FakePodName:                 "redis-memcached-78f799c9c8-2gzks",
					FakePodNamespace:            "default",
					FakeHealth:                  facts.ContainerNoHealthCheck,
					TestIgnored:                 true,
					TestHasPod:                  true,
				},
				{
					FakeID:             "d04d3b4d7eec381acd22ec697a066b8be0525089e6ebdf2b15529aa1f796e910",
					FakeContainerName:  "k8s_the-redis_redis-memcached-78f799c9c8-2gzks_default_f62b1b74-686e-43ae-9cf6-342b5bdbbda6_0",
					FakePrimaryAddress: "10.88.0.8",
					FakeAnnotations: map[string]string{
						"glouton.enable": "off",
					},
					FakeListenAddresses: []facts.ListenAddress{
						{
							Address:       "10.88.0.8",
							Port:          6363,
							NetworkFamily: "tcp",
						},
					},
					FakeListenAddressesExplicit: true,
					FakePodName:                 "redis-memcached-78f799c9c8-2gzks",
					FakePodNamespace:            "default",
					FakeHealth:                  facts.ContainerNoHealthCheck,
					TestIgnored:                 true,
					TestHasPod:                  true,
				},
				{
					FakeID:                 "5dbe9891d1476b866897f90f3bf51f2e12f9484c431c3b8690936b0f7f0c7d97",
					FakeContainerName:      "k8s_true_delete-me-once-69c996b98d-rp9fl_default_f2d62dbb-708e-41b1-8ccc-7ed7ebed0326_0",
					FakeStoppedAndReplaced: true,
					FakePodName:            "delete-me-once-69c996b98d-rp9fl",
					FakePodNamespace:       "default",
					FakeHealth:             facts.ContainerNoHealthCheck,
					TestHasPod:             true,
					TestIgnored:            true,
				},
				{
					FakeID:                 "40a2d7f07a4213e67c9039c17d359014476a2020296ebc5e1b47c4d4b4224610",
					FakeContainerName:      "k8s_true_delete-me-once-69c996b98d-rp9fl_default_f2d62dbb-708e-41b1-8ccc-7ed7ebed0326_1",
					FakeStoppedAndReplaced: false,
					FakePodName:            "delete-me-once-69c996b98d-rp9fl",
					FakePodNamespace:       "default",
					FakeHealth:             facts.ContainerNoHealthCheck,
					TestHasPod:             true,
				},
				{
					FakeID:            "57383e9932591a13a201645eecd736e8308082eec945c8c774afc8b2b22872af",
					FakeContainerName: "docker_default_without_k8s",
					FakeLabels: map[string]string{
						"test": "42",
					},
					FakePrimaryAddress: "172.17.0.2",
					FakeListenAddresses: []facts.ListenAddress{
						{
							Address:       "172.17.0.2",
							Port:          6379,
							NetworkFamily: "tcp",
						},
					},
					FakeListenAddressesExplicit: false,
					FakeHealth:                  facts.ContainerNoHealthCheck,
					TestHasPod:                  false,
				},
				{
					FakeID:            "9df176973888d8344f4da54cb87058ee88d6048558f447b3dee05a735d367d9e",
					FakeContainerName: "k8s_POD_rabbitmq-labels-7fbb75dcd7-h6t28_default_f071e8b4-0b84-4d02-bdb7-60a817874385_0",
					FakeHealth:        facts.ContainerNoHealthCheck,
					TestIgnored:       true,
					TestHasPod:        true,
				},
				{
					FakeID:            "564a21c5ed276140188d8e0726ba2c02996229c234a0aaf25cb6ddae84776608",
					FakeContainerName: "k8s_POD_redis-memcached-78f799c9c8-2gzks_default_f62b1b74-686e-43ae-9cf6-342b5bdbbda6_0",
					FakeHealth:        facts.ContainerNoHealthCheck,
					TestIgnored:       true,
					TestHasPod:        true,
				},
			},
			wantFacts: map[string]string{
				"kubernetes_version": "v1.18.0",
				"kubelet_version":    "v1.18.0",
			},
		},
		{
			name: "with-containerd",
			dir:  "testdata/containerd-in-vbox-v1.19.0",
			createRuntime: func(dirname string) (crTypes.RuntimeInterface, error) {
				client, err := containerd.NewMockFromFile(filepath.Join(dirname, "containerd.json"))
				if err != nil {
					return nil, err
				}

				return containerd.FakeContainerd(client), nil
			},
			wantContainer: []facts.FakeContainer{
				{
					FakeID:             "k8s.io/e00f87ac94cffd0bf7e79c2605e97ed2df3ad3bc65b3abfafbc7df57a218f6d9",
					FakeContainerName:  "k8s_rabbitmq_rabbitmq-labels-74cfb594d8-cgbzn_default",
					FakePrimaryAddress: "10.244.120.67",
					FakeAnnotations: map[string]string{
						"cni.projectcalico.org/podIP":    "10.244.120.67/32",
						"cni.projectcalico.org/podIPs":   "10.244.120.67/32",
						"glouton.check.ignore.port.5671": "on",
						"glouton.check.ignore.port.4369": "TruE",
					},
					FakePodName:      "rabbitmq-labels-74cfb594d8-cgbzn",
					FakePodNamespace: "default",
					FakeHealth:       facts.ContainerNoHealthCheck,
					TestHasPod:       true,
				},
				{
					FakeID:             "k8s.io/fd5a2a65a67f64ff30969e09037b3918bc5bc3bdd82f56758ebc440d76849d22",
					FakeContainerName:  "k8s_rabbitmq_rabbitmq-container-port-66fdd44ccd-7hsqr_default",
					FakePrimaryAddress: "10.244.120.69",
					FakeListenAddresses: []facts.ListenAddress{
						{
							Address:       "10.244.120.69",
							NetworkFamily: "tcp",
							Port:          5672,
						},
					},
					FakeListenAddressesExplicit: true,
					FakePodName:                 "rabbitmq-container-port-66fdd44ccd-7hsqr",
					FakePodNamespace:            "default",
					FakeHealth:                  facts.ContainerNoHealthCheck,
					TestHasPod:                  true,
				},
				{
					FakeID:             "k8s.io/c901b6d5396a9a6eaf5699bfef5543f0279aa4e0e9548910013d9da6c04ec6b3",
					FakeContainerName:  "k8s_a-memcached_redis-memcached-56dfc4cbfc-5qnh2_default",
					FakePrimaryAddress: "10.244.120.66",
					FakeAnnotations: map[string]string{
						"cni.projectcalico.org/podIP":  "10.244.120.66/32",
						"cni.projectcalico.org/podIPs": "10.244.120.66/32",
						"glouton.enable":               "off",
					},
					FakePodName:      "redis-memcached-56dfc4cbfc-5qnh2",
					FakePodNamespace: "default",
					FakeHealth:       facts.ContainerNoHealthCheck,
					TestIgnored:      true,
					TestHasPod:       true,
				},
				{
					FakeID:             "k8s.io/d053d875c398a5c0bb1fe5bb7505c997c1b332c473f36096a2f4979a8a1dffc7",
					FakeContainerName:  "k8s_the-redis_redis-memcached-56dfc4cbfc-5qnh2_default",
					FakePrimaryAddress: "10.244.120.66",
					FakeAnnotations: map[string]string{
						"cni.projectcalico.org/podIP":  "10.244.120.66/32",
						"cni.projectcalico.org/podIPs": "10.244.120.66/32",
						"glouton.enable":               "off",
					},
					FakeListenAddresses: []facts.ListenAddress{
						{
							Address:       "10.244.120.66",
							Port:          6363,
							NetworkFamily: "tcp",
						},
					},
					FakeListenAddressesExplicit: true,
					FakePodName:                 "redis-memcached-56dfc4cbfc-5qnh2",
					FakePodNamespace:            "default",
					FakeHealth:                  facts.ContainerNoHealthCheck,
					TestIgnored:                 true,
					TestHasPod:                  true,
				},
				{
					FakeID:                 "k8s.io/12ff76c454b4b7d16c41c951c296df261d9d9f2c5f375908256e1fc2dbe195e9",
					FakeContainerName:      "k8s_true_delete-me-once-584c74ccf5-4278c_default",
					FakeStoppedAndReplaced: false,
					FakePodName:            "delete-me-once-584c74ccf5-4278c",
					FakePodNamespace:       "default",
					FakeHealth:             facts.ContainerNoHealthCheck,
					TestHasPod:             true,
				},
				{
					FakeID:            "default/docker_default_without_k8s",
					FakeContainerName: "docker_default_without_k8s",
					FakeLabels: map[string]string{
						"test":                                   "42",
						"io.containerd.image.config.stop-signal": "SIGTERM",
					},
					FakePrimaryAddress: "",
					FakeImageName:      "docker.io/library/redis:latest",
					FakeImageID:        "sha256:0f724af268d0d3f5fb1d6b33fc22127ba5cbca2d58523b286ed3122db0dc5381",
					FakeHealth:         facts.ContainerNoHealthCheck,
					TestHasPod:         false,
				},
				{
					FakeID:            "k8s.io/fae6537013bc47cace5de8883c72a57e4c0a02f33680e9ed0f714c62b8c27b31",
					FakeContainerName: "k8s_POD_rabbitmq-labels-74cfb594d8-cgbzn_default",
					FakeImageName:     "k8s.gcr.io/pause:3.2",
					FakeHealth:        facts.ContainerNoHealthCheck,
					TestIgnored:       true,
					TestHasPod:        true,
				},
				{
					FakeID:            "k8s.io/d3b4beb5ce3c53d08eacf7242f65311d5ac197040f5e75882982f5b6d12d34ba",
					FakeContainerName: "k8s_POD_redis-memcached-56dfc4cbfc-5qnh2_default",
					FakeHealth:        facts.ContainerNoHealthCheck,
					TestIgnored:       true,
					TestHasPod:        true,
				},
			},
			wantFacts: map[string]string{
				"kubernetes_version": "v1.19.0",
				"kubelet_version":    "v1.19.0",
			},
		},
	}
	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			mockClient, err := newKubernetesMock(tt.dir)
			if err != nil {
				t.Error(err)

				return
			}

			runtime, err := tt.createRuntime(tt.dir)
			if err != nil {
				t.Error(err)

				return
			}

			k := &Kubernetes{
				Runtime:  runtime,
				NodeName: "minikube",
				openConnection: func(_ context.Context, kubeConfig string) (kubeClient, error) {
					return mockClient, nil
				},
			}

			containers, err := k.Containers(context.Background(), 0, true)
			if err != nil {
				t.Error(err)
			}

			containersWithoutExclude, err := k.Containers(context.Background(), 0, false)
			if err != nil {
				t.Error(err)
			}

			gotMap, err := testutil.ContainersToMap(containers)
			if err != nil {
				t.Error(err)
			}

			gotWithoutExcludeMap, err := testutil.ContainersToMap(containersWithoutExclude)
			if err != nil {
				t.Error(err)
			}

			for _, want := range tt.wantContainer {
				got := gotMap[want.ID()]
				if got == nil {
					t.Errorf("Kubernetes.Containers() don't have container %v", want.ID())

					continue
				}

				if diff := want.Diff(got); diff != "" {
					t.Errorf("Kubernetes.Containers()[%v]: %s", want.ID(), diff)
				}

				got, ok := k.CachedContainer(want.ID())
				if !ok {
					t.Errorf("CachedContainer() don't have container %v", want.ID())
				} else if diff := want.Diff(got); diff != "" {
					t.Errorf("CachedContainer(%s): %s", want.ID(), diff)
				}

				if want.TestIgnored {
					_, ok := gotWithoutExcludeMap[want.FakeID]
					if ok {
						t.Errorf("container %s is listed by Containers()", want.FakeID)
					}

					if !facts.ContainerIgnored(got) {
						t.Errorf("ContainerIgnored(%s) = false, want true", want.FakeID)
					}
				} else {
					_, ok := gotWithoutExcludeMap[want.FakeID]
					if !ok {
						t.Errorf("container %s is not listed by Containers()", want.FakeID)
					}
				}

				gotWrapper, ok := got.(wrappedContainer)
				switch {
				case !ok && want.TestHasPod:
					t.Errorf("got isn't a wrappedContainer, but it must be assocated to a POD")
				case ok && want.TestHasPod && gotWrapper.pod.Name == "":
					t.Errorf("got is a wrappedContainer but POD is empty, want a POD")
				case ok && !want.TestHasPod && gotWrapper.pod.Name != "":
					t.Errorf("got is a wrappedContainer with a POD is empty, want no POD")
				}
			}

			facts := k.RuntimeFact(context.Background(), nil)

			// Add facts coming from container runtime. We only test that those facts
			// are passed as-is.
			want := k.Runtime.RuntimeFact(context.Background(), nil)
			for k, v := range tt.wantFacts {
				want[k] = v
			}

			if diff := cmp.Diff(want, facts); diff != "" {
				t.Errorf("facts:\n%s", diff)
			}
		})
	}
}
