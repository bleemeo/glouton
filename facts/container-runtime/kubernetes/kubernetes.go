// Package kubernetes isn't really a container runtime but wraps one to add information from PODs
package kubernetes

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"glouton/facts"
	crTypes "glouton/facts/container-runtime/types"
	"glouton/logger"
	"glouton/types"
	"io/ioutil"
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

// Kubernetes wraps a container runtime to add information from PODs.
// It will add annotation, IP detection, flag "StoppedAndRestarted".
type Kubernetes struct {
	Runtime crTypes.RuntimeInterface
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

// LastUpdate return the last time containers list was updated.
func (k *Kubernetes) LastUpdate() time.Time {
	t := k.Runtime.LastUpdate()

	k.l.Lock()
	defer k.l.Unlock()

	if t.Before(k.lastPodsUpdate) {
		return k.lastPodsUpdate
	}

	return t
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

		if (time.Since(k.lastPodsUpdate) > maxAge || (!ok && uid != "")) && !podsUpdated {
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

	if facts == nil {
		facts = make(map[string]string)
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

func (k *Kubernetes) Metrics(ctx context.Context) ([]types.MetricPoint, error) {
	points := make([]types.MetricPoint, 0)
	now := time.Now()

	certificatePoint, err := k.getCertificateExpiration(now)
	if err != nil {
		logger.V(2).Println("An error occurred while fetching Certificate expiration date: ", err)
		return nil, err
	}

	caCertificatePoint, err := k.getCACertificateExpiration(now)
	if err != nil {
		logger.V(2).Println("An error occurred while fetching CA Certificate expiration date: ", err)
		return nil, err
	}

	points = append(points, certificatePoint)
	points = append(points, caCertificatePoint)

	return points, nil
}

func (k *Kubernetes) getCertificateExpiration(now time.Time) (types.MetricPoint, error) {
	config, err := getRestConfig(k.KubeConfig)
	if err != nil {
		return types.MetricPoint{}, err
	}

	caPool := x509.NewCertPool()
	tlsConfig := &tls.Config{}

	if config.TLSClientConfig.CAFile == "" {
		// API did not provide the CA. We fall back to insecure
		tlsConfig.InsecureSkipVerify = true
	} else {
		caData, err := ioutil.ReadFile(config.TLSClientConfig.CAFile)
		if err != nil {
			// We set the ssl config as insecure and proceed if no CA was given
			tlsConfig.InsecureSkipVerify = true
		}
		ok := caPool.AppendCertsFromPEM(caData)
		if !ok {
			logger.V(2).Println("Could not add CA certificate to the CA Pool.")
			tlsConfig.InsecureSkipVerify = true
		}
		tlsConfig.RootCAs = caPool
	}

	conn, err := tls.Dial("tcp", strings.TrimPrefix(config.Host, "https://"), tlsConfig)
	if err != nil {
		return types.MetricPoint{}, err
	}

	expiry := conn.ConnectionState().PeerCertificates[0]

	return certificatePoint(expiry, "Kubernetes Certificate days left before expiration", now)
}

func (k *Kubernetes) getCACertificateExpiration(now time.Time) (types.MetricPoint, error) {
	config, err := getRestConfig(k.KubeConfig)

	if err != nil {
		return types.MetricPoint{}, err
	}

	if config.TLSClientConfig.CAData == nil {
		if config.TLSClientConfig.CAFile != "" {
			return decodeCertFile(config.TLSClientConfig.CAFile, "Kubernetes CA Certificate days left before expiration", now)
		}

		logger.V(2).Printf("No certificate data found for Kubernetes API")

		return types.MetricPoint{}, nil
	}

	return decodeRawCert(config.TLSClientConfig.CAData, "Kubernetes CA Certificate days left before expiration", now)
}

func decodeCertFile(file string, label string, now time.Time) (types.MetricPoint, error) {
	f, err := ioutil.ReadFile(file)
	if err != nil {
		return types.MetricPoint{}, err
	}

	return decodeRawCert(f, label, now)
}

func decodeRawCert(rawData []byte, label string, now time.Time) (types.MetricPoint, error) {
	certDataBlock, certLeft := pem.Decode(rawData)
	if certDataBlock == nil {
		return types.MetricPoint{}, errors.New("no data decoded in raw certificate")
	}

	certData, err := x509.ParseCertificate(certDataBlock.Bytes)
	if err != nil {
		return types.MetricPoint{}, err
	}

	if len(certLeft) != 0 {
		logger.V(2).Printf("Unexpected leftover blocks in kubernetes API Certificate")
	}

	return certificatePoint(certData, label, now)
}

func certificatePoint(cert *x509.Certificate, label string, now time.Time) (types.MetricPoint, error) {
	labels := make(map[string]string)

	labels[types.LabelName] = label

	remainingDays := cert.NotAfter.Sub(now).Hours() / 24

	return types.MetricPoint{
		Point: types.Point{
			Time:  now,
			Value: remainingDays,
		},
		Labels:      labels,
		Annotations: types.MetricAnnotations{},
	}, nil
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

	if strings.HasPrefix(containerID, "containerd://") {
		containerID = strings.TrimPrefix(containerID, "containerd://")
		// Glouton add the namespace in the container ID
		containerID = "k8s.io/" + containerID
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

func getRestConfig(kubeConfig string) (*rest.Config, error) {
	var (
		config *rest.Config
		err    error
	)

	if kubeConfig != "" {
		config, err = clientcmd.BuildConfigFromFlags("", kubeConfig)
	} else {
		config, err = rest.InClusterConfig()
	}

	return config, err
}

func openConnection(ctx context.Context, kubeConfig string) (kubeClient, error) {
	config, err := getRestConfig(kubeConfig)

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
