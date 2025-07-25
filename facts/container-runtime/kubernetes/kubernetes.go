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
	"net"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bleemeo/glouton/facts"
	crTypes "github.com/bleemeo/glouton/facts/container-runtime/types"
	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/types"

	"github.com/prometheus/client_golang/prometheus"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Kubernetes wraps a container runtime to add information from PODs.
// It will add annotation, IP detection, flag "StoppedAndRestarted".
type Kubernetes struct {
	Runtime crTypes.RuntimeInterface
	// NodeName is the node Glouton is running on. Allow to fetch only
	// relevant PODs (running on the same node) instead of all PODs.
	NodeName string
	// KubeConfig is a kubeconfig file to use for communication with
	// Kubernetes. If not provided, use in-cluster auto-configuration.
	KubeConfig         string
	IsContainerIgnored func(facts.Container) bool
	// ShouldGatherClusterMetrics returns whether this agent should gather global cluster metrics.
	ShouldGatherClusterMetrics func() bool
	// ClusterName is the name of the Kubernetes cluster.
	ClusterName string

	l              sync.Mutex
	openConnection func(ctx context.Context, kubeConfig string, localNode string) (kubeClient, error)
	client         kubeClient
	lastPodsUpdate time.Time
	pods           []corev1.Pod
	lastNodeUpdate time.Time
	node           *corev1.Node
	version        *version.Info
	id2Pod         map[string]corev1.Pod
	podID2Pod      map[string]corev1.Pod
}

const (
	caExpLabel   = "kubernetes_ca_day_left"
	certExpLabel = "kubernetes_certificate_day_left"
)

var (
	errNoDecodedData = errors.New("no data decoded in raw certificate")
	errMissingConfig = errors.New("missing configuration")
)

func (k *Kubernetes) ContainerExists(id string) bool {
	k.l.Lock()
	defer k.l.Unlock()

	_, found := k.id2Pod[id]

	return found
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

	k.l.Lock()
	pod, _ := k.getPod(c)
	k.l.Unlock()

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

		if !includeIgnored && k.IsContainerIgnored(c) {
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

func (k *Kubernetes) IsContainerNameRecentlyDeleted(name string) bool {
	return k.Runtime.IsContainerNameRecentlyDeleted(name)
}

func (k *Kubernetes) DiagnosticArchive(ctx context.Context, archive types.ArchiveWriter) error {
	if err := k.Runtime.DiagnosticArchive(ctx, archive); err != nil {
		return err
	}

	file, err := archive.Create("kubernetes.json")
	if err != nil {
		return err
	}

	k.l.Lock()
	defer k.l.Unlock()

	type podInfo struct {
		PodIP        string
		Name         string
		ContainerIDs []string
	}

	pods := make([]podInfo, 0, len(k.pods))
	podIDToIdx := make(map[string]int, len(k.pods))

	for _, pod := range k.pods {
		podIDToIdx[string(pod.UID)] = len(pods)

		pods = append(pods, podInfo{
			PodIP: string(pod.UID),
			Name:  pod.ObjectMeta.Name,
		})
	}

	for id, pod := range k.id2Pod {
		idx, ok := podIDToIdx[string(pod.UID)]
		if !ok {
			continue
		}

		pods[idx].ContainerIDs = append(pods[idx].ContainerIDs, id)
	}

	obj := struct {
		Pods           []podInfo
		LastUpdatePods time.Time
		LastUpdateNode time.Time
	}{
		Pods:           pods,
		LastUpdatePods: k.lastPodsUpdate,
		LastUpdateNode: k.lastNodeUpdate,
	}

	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")

	return enc.Encode(obj)
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

			return facts
		}

		k.node, err = cl.GetNode(ctx, k.NodeName)
		if err != nil {
			logger.V(2).Printf("Failed to get Kubernetes node %s: %v", k.NodeName, err)
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

func (k *Kubernetes) Metrics(ctx context.Context, now time.Time) ([]types.MetricPoint, error) {
	return k.Runtime.Metrics(ctx, now)
}

func (k *Kubernetes) MetricsMinute(ctx context.Context, now time.Time) ([]types.MetricPoint, error) {
	var multiErr prometheus.MultiError

	points, errMetrics := k.Runtime.MetricsMinute(ctx, now)
	if errMetrics != nil {
		multiErr = append(multiErr, errMetrics)
	}

	cl, err := k.getClient(ctx)
	if err != nil {
		multiErr = append(multiErr, err)

		return points, multiErr.MaybeUnwrap()
	}

	if cl.IsUsingLocalAPI() {
		morePoints, errors := k.getMasterPoints(ctx, cl, now)

		points = append(points, morePoints...)
		multiErr = append(multiErr, errors...)
	}

	morePoints, errors := k.getKubeletPoint(ctx, cl, now)

	points = append(points, morePoints...)
	multiErr = append(multiErr, errors...)

	// Add global cluster metrics if this agent is the current kubernetes agent of the cluster.
	if k.ShouldGatherClusterMetrics() {
		morePoints, err = getGlobalMetrics(ctx, cl, now, k.ClusterName)
		if err != nil {
			multiErr = append(multiErr, err)
		}

		points = append(points, morePoints...)
	}

	return points, multiErr.MaybeUnwrap()
}

func (k *Kubernetes) getCertificateExpiration(ctx context.Context, config *rest.Config, now time.Time) (types.MetricPoint, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	caPool := x509.NewCertPool()
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	caData := config.TLSClientConfig.CAData

	var err error

	if config.TLSClientConfig.Insecure {
		tlsConfig.InsecureSkipVerify = true
	}

	if caData == nil {
		caData, err = os.ReadFile(config.TLSClientConfig.CAFile)
		if err != nil {
			// We set the CertPool to nil in order to fallback to system CAs.
			caPool = nil
		}
	}

	if caPool != nil {
		ok := caPool.AppendCertsFromPEM(caData)
		if ok {
			tlsConfig.RootCAs = caPool
		} else {
			logger.V(2).Println("Could not add CA certificate to the CA Pool. Defaulting to system CAs")
		}
	}

	addr, err := url.Parse(config.Host)
	if err != nil {
		return types.MetricPoint{}, err
	}

	tlsConfig.ServerName = addr.Hostname()

	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr.Host)
	if err != nil {
		// Something went wrong with the connection, but it is not related to TLS
		return types.MetricPoint{}, err
	}

	defer conn.Close()

	tlsConn := tls.Client(conn, tlsConfig)

	err = tlsConn.HandshakeContext(ctx)
	if err != nil {
		// Something went wrong with the TLS handshake, we consider the certificate as expired
		logger.V(2).Println("An error occurred on TLS handshake:", err)

		return createPointFromCertTime(time.Now(), certExpLabel, now)
	}

	if len(tlsConn.ConnectionState().PeerCertificates) == 0 {
		logger.V(2).Println("No peer certificate could be found for tls dial.")

		return types.MetricPoint{}, nil
	}

	expiry := tlsConn.ConnectionState().PeerCertificates[0]

	return createPointFromCertTime(expiry.NotAfter, certExpLabel, now)
}

func (k *Kubernetes) getMasterPoints(ctx context.Context, cl kubeClient, now time.Time) ([]types.MetricPoint, []error) {
	var err types.MultiErrors

	points := make([]types.MetricPoint, 0, 3)

	apiStatus := types.MetricPoint{
		Point: types.Point{Time: now, Value: 0.0},
		Labels: map[string]string{
			types.LabelName: "kubernetes_api_status",
		},
	}

	_, errTmp := cl.GetServerVersion(ctx)
	if errTmp != nil {
		apiStatus.Annotations.Status = types.StatusDescription{
			CurrentStatus:     types.StatusCritical,
			StatusDescription: fmt.Sprintf("failed to contact Kubernetes API: %s", errTmp),
		}
	} else {
		apiStatus.Annotations.Status = types.StatusDescription{
			CurrentStatus:     types.StatusOk,
			StatusDescription: "Kubernetes API is alive",
		}
	}

	apiStatus.Point.Value = float64(apiStatus.Annotations.Status.CurrentStatus.NagiosCode())
	points = append(points, apiStatus)

	config := cl.Config()
	if config == nil {
		return nil, []error{fmt.Errorf("%w: Kubernetes client config is unset", errMissingConfig)}
	}

	certificatePoint, errTmp := k.getCertificateExpiration(ctx, config, now)
	if errTmp != nil {
		err = append(err, errTmp)
	} else {
		points = append(points, certificatePoint)
	}

	caCertificatePoint, errTmp := k.getCACertificateExpiration(config, now)
	if errTmp != nil {
		err = append(err, errTmp)
	} else {
		points = append(points, caCertificatePoint)
	}

	return points, err
}

func (k *Kubernetes) getKubeletPoint(ctx context.Context, cl kubeClient, now time.Time) ([]types.MetricPoint, []error) {
	if k.NodeName == "" {
		return nil, []error{fmt.Errorf("%w: kubernetes.nodename is missing", errMissingConfig)}
	}

	node, err := cl.GetNode(ctx, k.NodeName)
	if err != nil {
		return nil, []error{err}
	}

	point := types.MetricPoint{
		Point: types.Point{Time: now, Value: 3.0},
		Labels: map[string]string{
			types.LabelName: "kubernetes_kubelet_status",
		},
		Annotations: types.MetricAnnotations{
			Status: types.StatusDescription{
				CurrentStatus:     types.StatusOk,
				StatusDescription: "no known issue",
			},
		},
	}

	var resourceWarning []string

	for _, cond := range node.Status.Conditions {
		switch cond.Type {
		case corev1.NodeReady:
			if cond.Status != corev1.ConditionTrue {
				point.Annotations.Status = types.StatusDescription{
					CurrentStatus:     types.StatusCritical,
					StatusDescription: "node is not ready: ",
				}
			}
		case corev1.NodeDiskPressure, corev1.NodeMemoryPressure, corev1.NodePIDPressure:
			if cond.Status == corev1.ConditionTrue {
				typeText := map[corev1.NodeConditionType]string{
					corev1.NodeDiskPressure:   "disk",
					corev1.NodeMemoryPressure: "memory",
					corev1.NodePIDPressure:    "PID",
				}[cond.Type]
				resourceWarning = append(
					resourceWarning,
					fmt.Sprintf("node has %s pressure: %s", typeText, cond.Message),
				)
			}
		case corev1.NodeNetworkUnavailable:
			if cond.Status == corev1.ConditionTrue {
				resourceWarning = append(
					resourceWarning,
					"node has networking issue: "+cond.Message,
				)
			}
		}
	}

	if len(resourceWarning) > 0 && point.Annotations.Status.CurrentStatus == types.StatusOk {
		point.Annotations.Status = types.StatusDescription{
			CurrentStatus:     types.StatusWarning,
			StatusDescription: strings.Join(resourceWarning, ", "),
		}
	}

	point.Point.Value = float64(point.Annotations.Status.CurrentStatus.NagiosCode())

	return []types.MetricPoint{point}, nil
}

func (k *Kubernetes) getCACertificateExpiration(config *rest.Config, now time.Time) (types.MetricPoint, error) {
	caData := config.TLSClientConfig.CAData

	var err error

	if caData == nil && config.TLSClientConfig.CAFile != "" {
		// CAData takes precedence over CAFile, thus we only check CAFile if there is no CAData
		caData, err = os.ReadFile(config.TLSClientConfig.CAFile)
		if err != nil {
			return types.MetricPoint{}, err
		}
	} else if caData == nil {
		logger.V(2).Printf("No certificate data found for Kubernetes API")

		return types.MetricPoint{}, nil
	}

	caCert, err := decodeRawCert(caData)
	if err != nil {
		return types.MetricPoint{}, err
	}

	return createPointFromCertTime(caCert.NotAfter, caExpLabel, now)
}

func decodeRawCert(rawData []byte) (*x509.Certificate, error) {
	certDataBlock, certLeft := pem.Decode(rawData)
	if certDataBlock == nil {
		return nil, errNoDecodedData
	}

	certData, err := x509.ParseCertificate(certDataBlock.Bytes)
	if err != nil {
		return nil, err
	}

	if len(certLeft) != 0 {
		logger.V(2).Printf("Unexpected leftover blocks in kubernetes API Certificate")
	}

	return certData, nil
}

func createPointFromCertTime(certTime time.Time, label string, now time.Time) (types.MetricPoint, error) {
	labels := make(map[string]string)

	labels[types.LabelName] = label

	remainingDays := certTime.Sub(now).Hours() / 24

	if remainingDays < 0 {
		// we clamp remainingDays to 0 when the certificate already expired
		remainingDays = 0
	}

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
		cl, err := k.openConnection(ctx, k.KubeConfig, k.NodeName)
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
	containerID = strings.TrimPrefix(containerID, "docker://")

	if strings.HasPrefix(containerID, "containerd://") {
		containerID = strings.TrimPrefix(containerID, "containerd://")
		// Glouton add the namespace in the container ID
		containerID = "k8s.io/" + containerID
	}

	return containerID
}

type kubeClient interface {
	// GetNode returns a node by name.
	GetNode(ctx context.Context, nodeName string) (*corev1.Node, error)
	// GetNodes returns all nodes in the cluster.
	GetNodes(ctx context.Context) ([]corev1.Node, error)
	// GetPODs returns POD on given nodeName or all POD is nodeName is empty.
	GetPODs(ctx context.Context, nodeName string) ([]corev1.Pod, error)
	// GetNamespaces returns all namespaces in the cluster.
	GetNamespaces(ctx context.Context) ([]corev1.Namespace, error)
	// GetReplicasets return all replicasets in the cluster.
	GetReplicasets(ctx context.Context) ([]appsv1.ReplicaSet, error)
	GetServerVersion(ctx context.Context) (*version.Info, error)
	IsUsingLocalAPI() bool
	Config() *rest.Config
}

type realClient struct {
	// We need one client per "API version" (v1, discovery.k8s.io/v1, apps/v1)
	coreClient  *rest.RESTClient
	discoClient *rest.RESTClient
	appsClient  *rest.RESTClient

	config      *rest.Config
	useLocalAPI bool
}

func (cl *realClient) GetNode(ctx context.Context, nodeName string) (*corev1.Node, error) {
	var node corev1.Node

	err := cl.coreClient.
		Get().
		Resource("nodes").
		Name(nodeName).
		Do(ctx).
		Into(&node)

	return &node, err
}

func (cl *realClient) GetNodes(ctx context.Context) ([]corev1.Node, error) {
	var nodes corev1.NodeList

	err := cl.coreClient.
		Get().
		Resource("nodes").
		Do(ctx).
		Into(&nodes)
	if err != nil {
		return nil, err
	}

	return nodes.Items, nil
}

func (cl *realClient) GetPODs(ctx context.Context, nodeName string) ([]corev1.Pod, error) {
	var pods corev1.PodList

	req := cl.coreClient.Get().Resource("pods")

	if nodeName != "" {
		req.Param("fieldSelector", "spec.nodeName="+nodeName)
	}

	err := req.Do(ctx).Into(&pods)
	if err != nil {
		return nil, err
	}

	return pods.Items, nil
}

func (cl *realClient) GetNamespaces(ctx context.Context) ([]corev1.Namespace, error) {
	var namespaces corev1.NamespaceList

	err := cl.coreClient.
		Get().
		Resource("namespaces").
		Do(ctx).
		Into(&namespaces)
	if err != nil {
		return nil, err
	}

	return namespaces.Items, nil
}

func (cl *realClient) GetReplicasets(ctx context.Context) ([]appsv1.ReplicaSet, error) {
	var replicasets appsv1.ReplicaSetList

	err := cl.appsClient.Get().Resource("replicasets").Do(ctx).Into(&replicasets)
	if err != nil {
		return nil, err
	}

	return replicasets.Items, nil
}

func (cl *realClient) GetServerVersion(ctx context.Context) (*version.Info, error) {
	// This is cl.client.ServerVersion() but with a context.
	body, err := cl.coreClient.Get().AbsPath("/version").Do(ctx).Raw()
	if err != nil {
		return nil, err
	}

	var info version.Info

	err = json.Unmarshal(body, &info)
	if err != nil {
		return nil, fmt.Errorf("unable to parse the server version: %w", err)
	}

	return &info, nil
}

func (cl *realClient) IsUsingLocalAPI() bool {
	return cl.useLocalAPI
}

func (cl *realClient) Config() *rest.Config {
	return cl.config
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

func makeClients(config *rest.Config) (coreClient, discoClient, appsClient *rest.RESTClient, err error) {
	clientSetups := []struct {
		groupVersion  *schema.GroupVersion
		addToSchemeFn func(*runtime.Scheme) error
		apiPath       string
		result        **rest.RESTClient
	}{
		{
			groupVersion:  &corev1.SchemeGroupVersion,
			addToSchemeFn: corev1.AddToScheme,
			apiPath:       "/api",
			result:        &coreClient,
		},
		{
			groupVersion:  &discoveryv1.SchemeGroupVersion,
			addToSchemeFn: discoveryv1.AddToScheme,
			apiPath:       "/apis",
			result:        &discoClient,
		},
		{
			groupVersion:  &appsv1.SchemeGroupVersion,
			addToSchemeFn: appsv1.AddToScheme,
			apiPath:       "/apis",
			result:        &appsClient,
		},
	}

	for _, setup := range clientSetups {
		scheme := runtime.NewScheme()

		err = setup.addToSchemeFn(scheme)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to build scheme for %s: %w", setup.groupVersion, err)
		}

		cfgCopy := *config
		cfgCopy.ContentConfig.GroupVersion = setup.groupVersion
		cfgCopy.APIPath = setup.apiPath
		cfgCopy.NegotiatedSerializer = serializer.NewCodecFactory(scheme).WithoutConversion()
		cfgCopy.UserAgent = rest.DefaultKubernetesUserAgent()

		*setup.result, err = rest.UnversionedRESTClientFor(&cfgCopy)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("for %s: %w", setup.groupVersion, err)
		}
	}

	return coreClient, discoClient, appsClient, nil
}

func openConnection(ctx context.Context, kubeConfig string, localNode string) (kubeClient, error) {
	config, err := getRestConfig(kubeConfig)
	if err != nil {
		return nil, err
	}

	coreClient, discoClient, appsClient, err := makeClients(config)
	if err != nil {
		return nil, fmt.Errorf("failed to build rest clients: %w", err)
	}

	client := realClient{
		coreClient:  coreClient,
		discoClient: discoClient,
		appsClient:  appsClient,
		config:      config,
		useLocalAPI: false,
	}

	switched, err := client.switchToLocalAPI(ctx, localNode)

	switch {
	case err != nil:
		logger.V(1).Printf("Glouton is not able to communicate with local API. It will assume running on a worker node")
		logger.V(1).Printf("The error while trying to use local API: %v", err)
	case switched:
		logger.V(1).Printf("Glouton is running on a Kubernetes master, it will only use the local API server")
	default:
		logger.V(1).Printf("Glouton is running on a Kubernetes worker node")
	}

	return &client, nil
}

func (cl *realClient) switchToLocalAPI(ctx context.Context, localNode string) (bool, error) {
	if localNode == "" {
		return false, fmt.Errorf("%w: kubernetes.nodename is missing", errMissingConfig)
	}

	var (
		endpointSlice discoveryv1.EndpointSlice
		pods          corev1.PodList
	)

	err := cl.discoClient.
		Get().
		Namespace("default").
		Resource("endpointslices").
		Name("kubernetes").
		Do(ctx).
		Into(&endpointSlice)
	if err != nil {
		return false, fmt.Errorf("failed to get 'kubernetes' endpointslice: %w", err)
	}

	err = cl.coreClient.
		Get().
		Namespace("kube-system").
		Resource("pods").
		Param("fieldSelector", "spec.nodeName="+localNode).
		Do(ctx).
		Into(&pods)
	if err != nil {
		return false, fmt.Errorf("failed to list PODs: %w", err)
	}

	podsIP := make(map[string]corev1.Pod, len(pods.Items))
	for _, pod := range pods.Items {
		podsIP[pod.Status.PodIP] = pod
	}

	httpsPort := 8443

	for _, p := range endpointSlice.Ports {
		if p.Name != nil && *p.Name == "https" && p.Port != nil {
			httpsPort = int(*p.Port)
		}
	}

	for _, ep := range endpointSlice.Endpoints {
		for _, ip := range ep.Addresses {
			if pod, ok := podsIP[ip]; ok {
				logger.V(2).Printf("Found the POD running Kubernetes API on local node: %s", pod.Name)

				shallowCopy := *cl.config
				shallowCopy.Host = "https://" + net.JoinHostPort(ip, strconv.FormatInt(int64(httpsPort), 10))

				coreClient, discoClient, appsClient, err := makeClients(&shallowCopy)
				if err != nil {
					return false, fmt.Errorf("failed to build rest clients: %w", err)
				}

				err = discoClient.
					Get().
					Namespace("default").
					Resource("endpointslices").
					Do(ctx).
					Error()
				if err != nil {
					return false, fmt.Errorf("failed list endpointslices with new client: %w", err)
				}

				cl.coreClient = coreClient
				cl.discoClient = discoClient
				cl.appsClient = appsClient
				cl.config = &shallowCopy
				cl.useLocalAPI = true

				return true, nil
			}
		}
	}

	return false, nil
}

type wrappedContainer struct {
	facts.Container

	pod corev1.Pod
}

func (c wrappedContainer) Annotations() map[string]string {
	return c.pod.Annotations
}

// Labels returns the container labels and the pod labels merged.
func (c wrappedContainer) Labels() map[string]string {
	containerLabels := c.Container.Labels()

	// Return a copy of the labels, don't mutate the container labels.
	labels := make(map[string]string, len(containerLabels)+len(c.pod.Labels))
	for name, value := range containerLabels {
		labels[name] = value
	}

	for name, value := range c.pod.Labels {
		labels[name] = value
	}

	return labels
}

func (c wrappedContainer) LogPath() string {
	// The log path is present in the annotation "io.kubernetes.container.logpath" but
	// only with the Docker runtime, to also support ContainerD we use /var/log/containers.
	userContainerName := c.userContainerName()
	if userContainerName == "" {
		return ""
	}

	// Remove prefix used by ContainerD.
	containerID := strings.TrimPrefix(c.ID(), "k8s.io/")

	return fmt.Sprintf(
		"/var/log/containers/%s_%s_%s-%s.log",
		c.PodName(), c.PodNamespace(), userContainerName, containerID,
	)
}

// Return the container name assigned by the user, not the one created by Kubernetes.
// For instance a container may have the name
// "k8s_uwsgi_bleemeo-api-uwsgi-9b75c7f8c-grm4v_bleemeo-minikube_6646e89a-604f-491d-aa64-62a34db0e01b_3"
// but the user only assigned the name "uwsgi" to the container in the deployment.
func (c wrappedContainer) userContainerName() string {
	// The name we want is the one in c.pod.Spec.Containers[0].Name, but this doesn't work
	// if the pod has multiple containers, so we parse the container name instead.
	// The name has the format "k8s_<container_name>_[...]".
	splitName := strings.SplitN(c.ContainerName(), "_", 3)
	if len(splitName) < 3 {
		return ""
	}

	return splitName[1]
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

func (c wrappedContainer) ListenAddresses() []facts.ListenAddress {
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

		return exposedPorts
	}

	addresses := c.Container.ListenAddresses()

	if primaryAddress != "" {
		for i, addr := range addresses {
			if addr.Address != "" {
				continue
			}

			addresses[i].Address = primaryAddress
		}
	}

	return addresses
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

	w.k.l.Lock()
	pod, _ := w.k.getPod(c)
	w.k.l.Unlock()

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

	w.k.l.Lock()
	pod, _ := w.k.getPod(c)
	w.k.l.Unlock()

	return wrappedContainer{
		Container: c,
		pod:       pod,
	}, nil
}
