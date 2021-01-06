// nolint: dupl
package kubernetes

import (
	"context"
	"errors"
	"glouton/facts"
	"glouton/facts/container-runtime/docker"
	"io/ioutil"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/version"
	"sigs.k8s.io/yaml"
)

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
		return nil, errors.New("not implmented")
	}

	return &k.node.Items[0], nil
}

func (k *mockKubernetesClient) GetPODs(ctx context.Context, nodeName string) ([]corev1.Pod, error) {
	return k.pods.Items, nil
}

func (k *mockKubernetesClient) GetServerVersion(ctx context.Context) (*version.Info, error) {
	return k.versions.ServerVersion, nil
}

func TestKubernetes_Containers(t *testing.T) {
	tests := []struct {
		name                 string
		dir                  string
		createRuntime        func(dirname string) (RuntimeInterface, error)
		wantContainer        []facts.FakeContainer
		ignoredContainerName []string
		wantFacts            map[string]string
	}{
		{
			name: "with-docker",
			dir:  "testdata/with-docker-v1.20.0",
			createRuntime: func(dirname string) (RuntimeInterface, error) {
				dockerClient, err := docker.NewDockerMock(dirname)
				if err != nil {
					return nil, err
				}

				return docker.FakeDocker(dockerClient), nil
			},
			wantContainer: []facts.FakeContainer{
				{
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
				},
				{
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
				},
				{
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
				},
				{
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
				},
				{
					FakeContainerName:      "k8s_true_delete-me-once-584c74ccf5-hmb77_default_3db6f913-cc23-4e70-9c08-7bdcb73eb8c1_0",
					FakeStoppedAndReplaced: true,
					FakePodName:            "delete-me-once-584c74ccf5-hmb77",
					FakePodNamespace:       "default",
				},
				{
					FakeContainerName:      "k8s_true_delete-me-once-584c74ccf5-hmb77_default_3db6f913-cc23-4e70-9c08-7bdcb73eb8c1_1",
					FakeStoppedAndReplaced: false,
					FakePodName:            "delete-me-once-584c74ccf5-hmb77",
					FakePodNamespace:       "default",
				},
				{
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
				},
			},
			ignoredContainerName: []string{
				"k8s_POD_redis-memcached-56dfc4cbfc-2m2cq_default_c5bced17-e72c-4668-8329-76fa19cda44e_0",
			},
			wantFacts: map[string]string{
				"kubernetes_version": "v1.20.0",
				"kubelet_version":    "v1.20.0",
			},
		},
		{
			name: "with-docker-labels-stripped",
			dir:  "testdata/with-docker-v1.20.0",
			createRuntime: func(dirname string) (RuntimeInterface, error) {
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
				},
				{
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
				},
				{
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
				},
				{
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
				},
				{
					FakeContainerName:      "k8s_true_delete-me-once-584c74ccf5-hmb77_default_3db6f913-cc23-4e70-9c08-7bdcb73eb8c1_0",
					FakeStoppedAndReplaced: true,
					FakePodName:            "delete-me-once-584c74ccf5-hmb77",
					FakePodNamespace:       "default",
				},
				{
					FakeContainerName:      "k8s_true_delete-me-once-584c74ccf5-hmb77_default_3db6f913-cc23-4e70-9c08-7bdcb73eb8c1_1",
					FakeStoppedAndReplaced: false,
					FakePodName:            "delete-me-once-584c74ccf5-hmb77",
					FakePodNamespace:       "default",
				},
				{
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
			createRuntime: func(dirname string) (RuntimeInterface, error) {
				dockerClient, err := docker.NewDockerMock(dirname)
				if err != nil {
					return nil, err
				}

				return docker.FakeDocker(dockerClient), nil
			},
			wantContainer: []facts.FakeContainer{
				{
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
				},
				{
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
				},
				{
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
				},
				{
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
				},
				{
					FakeContainerName:      "k8s_true_delete-me-once-69c996b98d-rp9fl_default_f2d62dbb-708e-41b1-8ccc-7ed7ebed0326_0",
					FakeStoppedAndReplaced: true,
					FakePodName:            "delete-me-once-69c996b98d-rp9fl",
					FakePodNamespace:       "default",
				},
				{
					FakeContainerName:      "k8s_true_delete-me-once-69c996b98d-rp9fl_default_f2d62dbb-708e-41b1-8ccc-7ed7ebed0326_1",
					FakeStoppedAndReplaced: false,
					FakePodName:            "delete-me-once-69c996b98d-rp9fl",
					FakePodNamespace:       "default",
				},
				{
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
				},
			},
			ignoredContainerName: []string{
				"k8s_POD_redis-memcached-78f799c9c8-2gzks_default_f62b1b74-686e-43ae-9cf6-342b5bdbbda6_0",
			},
			wantFacts: map[string]string{
				"kubernetes_version": "v1.18.0",
				"kubelet_version":    "v1.18.0",
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

			gotMap, err := facts.ContainersToContainerNameMap(containers)
			if err != nil {
				t.Error(err)
			}

			for _, want := range tt.wantContainer {
				got := gotMap[want.ContainerName()]
				if got == nil {
					t.Errorf("Kubernetes.Containers() don't have container %v", want.ContainerName())

					continue
				}

				if diff := want.Diff(got); diff != "" {
					t.Errorf("Kubernetes.Containers()[%v]: %s", want.ContainerName(), diff)
				}

				got, ok := k.CachedContainer(got.ID())
				if !ok {
					t.Errorf("Kubernetes.CachedContainer() don't have container %v", want.ContainerName())
				} else if diff := want.Diff(got); diff != "" {
					t.Errorf("Kubernetes.CachedContainer(%s): %s", want.ContainerName(), diff)
				}
			}

			for _, name := range tt.ignoredContainerName {
				got := gotMap[name]

				if got == nil {
					t.Errorf("container %s not found", name)
				} else if !facts.ContainerIgnored(got) {
					t.Errorf("ContainerIgnored(%s) = false, want true", name)
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
