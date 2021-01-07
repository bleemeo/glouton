// Package kubernetes isn't really a container runtime but wraps one to add information from PODs
package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"glouton/facts"
	"glouton/logger"
	"glouton/types"
	"sort"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// RuntimeInterface is the interface wrapped by Kubernetes.
type RuntimeInterface interface {
	CachedContainer(containerID string) (c facts.Container, found bool)
	ContainerLastKill(containerID string) time.Time
	Exec(ctx context.Context, containerID string, cmd []string) ([]byte, error)
	Containers(ctx context.Context, maxAge time.Duration, includeIgnored bool) (containers []facts.Container, err error)
	Events() <-chan facts.ContainerEvent
	GatherCallback(pusher types.PointPusher) func(time.Time)
	IsRuntimeRunning(ctx context.Context) bool
	ProcessWithCache() facts.ContainerRuntimeProcessQuerier
	Run(ctx context.Context) error
	RuntimeFact(ctx context.Context, currentFact map[string]string) map[string]string
}

// Kubernetes wraps a container runtime to add information from PODs.
// It will add annotation, IP detection, flag "StoppedAndRestarted".
type Kubernetes struct {
	Runtime RuntimeInterface
	// NodeName is the node Glouton is running on. Allow to fetch only relevant PODs (running on the same node) instead of all PODs.
	NodeName string
	// KubecConfig is a kubeconfig file to use for communication with Kubernetes. If not provided, use in-cluster auto-configuration.
	KubeConfig string

	l              sync.Mutex
	openConnection func(ctx context.Context, kubeConfig string) (kubeClient, error)
	client         kubeClient
	lastPodsUpdate time.Time
	pods           []corev1.Pod
	lastNodeUpdate time.Time
	node           *corev1.Node
	version        *version.Info
	id2Pod         map[string]corev1.Pod
	podID2Pod      map[string]corev1.Pod
}

// CachedContainer return the container for given ID.
func (k *Kubernetes) CachedContainer(containerID string) (c facts.Container, found bool) {
	c, found = k.Runtime.CachedContainer(containerID)
	if !found {
		return nil, found
	}

	pod, _ := k.getPod(c)

	return wrappedContainer{
		Container: c,
		pod:       pod,
	}, true
}

// ContainerLastKill return last time a containers was killed.
func (k *Kubernetes) ContainerLastKill(containerID string) time.Time {
	return k.Runtime.ContainerLastKill(containerID)
}

// Exec run command in the containers.
func (k *Kubernetes) Exec(ctx context.Context, containerID string, cmd []string) ([]byte, error) {
	return k.Runtime.Exec(ctx, containerID, cmd)
}

// Containers return all known container, with annotation added.
func (k *Kubernetes) Containers(ctx context.Context, maxAge time.Duration, includeIgnored bool) (containers []facts.Container, err error) {
	containers, err = k.Runtime.Containers(ctx, maxAge, includeIgnored)
	if err != nil {
		return nil, err
	}

	k.l.Lock()
	defer k.l.Unlock()

	podsUpdated := false

	response := make([]facts.Container, 0, len(containers))

	for _, c := range containers {
		pod, ok := k.getPod(c)
		uid := c.Labels()["io.kubernetes.pod.uid"]

		if !ok && (time.Since(k.lastPodsUpdate) > maxAge || uid != "" && !podsUpdated) {
			podsUpdated = true
			k.lastPodsUpdate = time.Now()

			err := k.updatePods(ctx)
			if err != nil {
				logger.V(2).Printf("Unable to list PODs: %v", err)
			}

			pod, _ = k.getPod(c)
		}

		c = wrappedContainer{
			Container: c,
			pod:       pod,
		}

		if !includeIgnored && facts.ContainerIgnored(c) {
			continue
		}

		response = append(response, c)
	}

	return response, nil
}

// Events return container events.
func (k *Kubernetes) Events() <-chan facts.ContainerEvent {
	return k.Runtime.Events()
}

// GatherCallback emit some metrics.
func (k *Kubernetes) GatherCallback(pusher types.PointPusher) func(time.Time) {
	return k.Runtime.GatherCallback(pusher)
}

// IsRuntimeRunning tells if Glouton is connected to the container runtime.
// Note: if Kubernetes isn't working but the underlying container runtime works, this method return true, but POD information will be missing.
func (k *Kubernetes) IsRuntimeRunning(ctx context.Context) bool {
	return k.Runtime.IsRuntimeRunning(ctx)
}

// ProcessWithCache implement ContainerRuntimeProcessQuerier.
func (k *Kubernetes) ProcessWithCache() facts.ContainerRuntimeProcessQuerier {
	return wrapProcessQuerier{
		ContainerRuntimeProcessQuerier: k.Runtime.ProcessWithCache(),
		k:                              k,
	}
}

// Run the connector.
func (k *Kubernetes) Run(ctx context.Context) error {
	return k.Runtime.Run(ctx)
}

// RuntimeFact return facts about the container runtime & Kubernetes.
func (k *Kubernetes) RuntimeFact(ctx context.Context, currentFact map[string]string) map[string]string {
	facts := k.Runtime.RuntimeFact(ctx, currentFact)

	if k.NodeName == "" {
		return facts
	}

	k.l.Lock()
	defer k.l.Unlock()

	if time.Since(k.lastNodeUpdate) > time.Hour {
		k.lastNodeUpdate = time.Now()
		k.node = nil
		k.version = nil

		cl, err := k.getClient(ctx)
		if err != nil {
			logger.V(2).Printf("Kubernetes client initialization fail: %v", err)
		} else {
			k.node, err = cl.GetNode(ctx, k.NodeName)
			if err != nil {
				logger.V(2).Printf("Failed to get Kubernetes node %s: %v", k.NodeName, err)
			}
		}

		k.version, err = cl.GetServerVersion(ctx)
		if err != nil {
			logger.V(2).Printf("Failed to get Kubernetes version: %v", err)
		}
	}

	if k.node != nil && k.node.Status.NodeInfo.KubeletVersion != "" {
		facts["kubelet_version"] = k.node.Status.NodeInfo.KubeletVersion
	}

	if k.version != nil && k.version.GitVersion != "" {
		facts["kubernetes_version"] = k.version.GitVersion
	}

	return facts
}

// Test check if connector is able to get PODs.
func (k *Kubernetes) Test(ctx context.Context) error {
	k.l.Lock()
	defer k.l.Unlock()

	return k.updatePods(ctx)
}

func (k *Kubernetes) getClient(ctx context.Context) (cl kubeClient, err error) {
	if k.openConnection == nil {
		k.openConnection = openConnection
	}

	if k.client == nil {
		cl, err := k.openConnection(ctx, k.KubeConfig)
		if err != nil {
			return nil, err
		}

		k.client = cl
	}

	return k.client, nil
}

func (k *Kubernetes) getPod(c facts.Container) (corev1.Pod, bool) {
	pod, ok := k.id2Pod[c.ID()]
	if !ok {
		uid := c.Labels()["io.kubernetes.pod.uid"]
		pod, ok = k.podID2Pod[uid]
	}

	return pod, ok
}

func (k *Kubernetes) updatePods(ctx context.Context) error {
	cl, err := k.getClient(ctx)
	if err != nil {
		return err
	}

	pods, err := cl.GetPODs(ctx, k.NodeName)
	if err != nil {
		return err
	}

	k.id2Pod = make(map[string]corev1.Pod, len(pods))
	k.podID2Pod = make(map[string]corev1.Pod, len(pods))
	k.pods = pods

	for _, pod := range pods {
		k.podID2Pod[string(pod.UID)] = pod

		for _, container := range pod.Status.ContainerStatuses {
			k.id2Pod[kuberIDtoRuntimeID(container.ContainerID)] = pod

			if container.LastTerminationState.Terminated != nil && container.LastTerminationState.Terminated.ContainerID != "" {
				k.id2Pod[kuberIDtoRuntimeID(container.LastTerminationState.Terminated.ContainerID)] = pod
			}
		}
	}

	return nil
}

func kuberIDtoRuntimeID(containerID string) string {
	if strings.HasPrefix(containerID, "docker://") {
		containerID = strings.TrimPrefix(containerID, "docker://")
	}

	return containerID
}

type kubeClient interface {
	// GetNode return the node by name.
	GetNode(ctx context.Context, nodeName string) (*corev1.Node, error)
	// GetPODs returns POD on given nodeName or all POD is nodeName is empty.
	GetPODs(ctx context.Context, nodeName string) ([]corev1.Pod, error)
	GetServerVersion(ctx context.Context) (*version.Info, error)
}

type realClient struct {
	client *kubernetes.Clientset
}

func (cl realClient) GetNode(ctx context.Context, nodeName string) (*corev1.Node, error) {
	opts := metav1.GetOptions{}
	node, err := cl.client.CoreV1().Nodes().Get(ctx, nodeName, opts)

	return node, err
}

func (cl realClient) GetPODs(ctx context.Context, nodeName string) ([]corev1.Pod, error) {
	opts := metav1.ListOptions{}

	if nodeName != "" {
		opts.FieldSelector = "spec.nodeName=" + nodeName
	}

	list, err := cl.client.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	return list.Items, nil
}

func (cl realClient) GetServerVersion(ctx context.Context) (*version.Info, error) {
	// This is cl.client.ServerVersion() but with a context.
	body, err := cl.client.RESTClient().Get().AbsPath("/version").Do(ctx).Raw()
	if err != nil {
		return nil, err
	}

	var info version.Info

	err = json.Unmarshal(body, &info)
	if err != nil {
		return nil, fmt.Errorf("unable to parse the server version: %v", err)
	}

	return &info, nil
}

func openConnection(ctx context.Context, kubeConfig string) (kubeClient, error) {
	var (
		config *rest.Config
		err    error
	)

	if kubeConfig != "" {
		config, err = clientcmd.BuildConfigFromFlags("", kubeConfig)
	} else {
		config, err = rest.InClusterConfig()
	}

	if err != nil {
		return nil, err
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	return realClient{client: clientset}, nil
}

type wrappedContainer struct {
	facts.Container
	pod corev1.Pod
}

func (c wrappedContainer) Annotations() map[string]string {
	return c.pod.Annotations
}

func (c wrappedContainer) PrimaryAddress() string {
	if c.pod.Status.PodIP != "" {
		return c.pod.Status.PodIP
	}

	return c.Container.PrimaryAddress()
}

func (c wrappedContainer) PodName() string {
	if c.pod.Name != "" {
		return c.pod.Name
	}

	return c.Container.PodName()
}

func (c wrappedContainer) PodNamespace() string {
	if c.pod.Namespace != "" {
		return c.pod.Namespace
	}

	return c.Container.PodNamespace()
}

func (c wrappedContainer) ListenAddresses() (addresses []facts.ListenAddress, explicit bool) {
	var kubeContainer corev1.Container

	name := c.Labels()["io.kubernetes.container.name"]
	if name == "" {
		for _, container := range c.pod.Status.ContainerStatuses {
			if c.ID() == kuberIDtoRuntimeID(container.ContainerID) {
				name = container.Name

				break
			}
		}
	}

	for _, kc := range c.pod.Spec.Containers {
		if kc.Name == name {
			kubeContainer = kc
			break
		}
	}

	primaryAddress := c.PrimaryAddress()

	if len(kubeContainer.Ports) > 0 {
		exposedPorts := make([]facts.ListenAddress, len(kubeContainer.Ports))

		for i, port := range kubeContainer.Ports {
			exposedPorts[i] = facts.ListenAddress{
				Address:       primaryAddress,
				Port:          int(port.ContainerPort),
				NetworkFamily: strings.ToLower(string(port.Protocol)),
			}
		}

		sort.Slice(exposedPorts, func(i, j int) bool {
			return exposedPorts[i].Port < exposedPorts[j].Port
		})

		return exposedPorts, true
	}

	addresses, explicit = c.Container.ListenAddresses()

	if primaryAddress != "" {
		for i, addr := range addresses {
			if addr.Address != "" {
				continue
			}

			addresses[i].Address = primaryAddress
		}
	}

	return addresses, explicit
}

func (c wrappedContainer) StoppedAndReplaced() bool {
	if c.State().IsRunning() {
		return false
	}

	if len(c.pod.Status.ContainerStatuses) != 0 {
		// If the container is not in current containerStatus, it's replaced
		for _, container := range c.pod.Status.ContainerStatuses {
			if c.ID() == kuberIDtoRuntimeID(container.ContainerID) {
				return false
			}
		}

		return true
	}

	return false
}

type wrapProcessQuerier struct {
	facts.ContainerRuntimeProcessQuerier
	k *Kubernetes
}

func (w wrapProcessQuerier) ContainerFromCGroup(ctx context.Context, cgroupData string) (facts.Container, error) {
	c, err := w.ContainerRuntimeProcessQuerier.ContainerFromCGroup(ctx, cgroupData)
	if c == nil || err != nil {
		return c, err
	}

	pod, _ := w.k.getPod(c)

	return wrappedContainer{
		Container: c,
		pod:       pod,
	}, nil
}

func (w wrapProcessQuerier) ContainerFromPID(ctx context.Context, parentContainerID string, pid int) (facts.Container, error) {
	c, err := w.ContainerRuntimeProcessQuerier.ContainerFromPID(ctx, parentContainerID, pid)
	if c == nil || err != nil {
		return c, err
	}

	pod, _ := w.k.getPod(c)

	return wrappedContainer{
		Container: c,
		pod:       pod,
	}, nil
}
