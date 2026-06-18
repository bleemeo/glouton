// Copyright 2015-2026 Bleemeo
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
	"maps"
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
	admv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const apiPathAPIs = "/apis"

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
	// replicasetOwnerByUID maps a ReplicaSet UID to its owner (usually a Deployment).
	// Used to resolve a pod's workload owner from its ReplicaSet. Best-effort: it stays
	// empty when listing ReplicaSets is not permitted, in which case the immediate owner
	// (the ReplicaSet itself) is reported.
	replicasetOwnerByUID map[string]metav1.OwnerReference
	// pvcByPVName maps a PersistentVolume name to the PersistentVolumeClaim bound to it.
	// Used to resolve the in-pod volume name of a CSI mount. Best-effort: it stays empty
	// when listing PersistentVolumeClaims is not permitted.
	pvcByPVName map[string]pvcReference
}

// pvcReference identifies a PersistentVolumeClaim.
type pvcReference struct {
	namespace string
	name      string
}

const (
	caExpLabelDay    = "kubernetes_ca_day_left"
	caExpLabelPerc   = "kubernetes_ca_left_perc"
	certExpLabelDay  = "kubernetes_certificate_day_left"
	certExpLabelPerc = "kubernetes_certificate_left_perc"
)

var (
	errNoCertFound         = errors.New("no certificate found")
	errNodeHasNoInternalIP = errors.New("node has no internal IP")
	errUnexpectedConnType  = errors.New("unexpected connection type")
	errNoDecodedData       = errors.New("no data decoded in raw certificate")
	errMissingConfig       = errors.New("missing configuration")
	errNoScaleReplicas     = errors.New("scale subresource has no spec.replicas")
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

// ContainerLastDelete return last time a containers was killed.
func (k *Kubernetes) ContainerLastDelete(containerID string) time.Time {
	return k.Runtime.ContainerLastDelete(containerID)
}

func (k *Kubernetes) ContainerByNameLastDelete(name string) time.Time {
	return k.Runtime.ContainerByNameLastDelete(name)
}

// ContainerTerminationGracePeriod return the duration between a kill and the forced stop. Use 0 if unknown.
func (k *Kubernetes) ContainerTerminationGracePeriod(containerID string) time.Duration {
	tmp, found := k.CachedContainer(containerID)
	if !found {
		return 0
	}

	container, ok := tmp.(wrappedContainer)
	if !ok {
		return 0
	}

	if container.pod.Spec.TerminationGracePeriodSeconds == nil {
		return 30 * time.Second
	}

	return time.Second * time.Duration(*container.pod.Spec.TerminationGracePeriodSeconds)
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
		morePoints, errors := k.getLocalAPIPoints(ctx, cl, now)

		points = append(points, morePoints...)
		multiErr = append(multiErr, errors...)
	}

	morePoints, errors := k.getKubeletPoints(ctx, cl, now)

	points = append(points, morePoints...)
	multiErr = append(multiErr, errors...)

	// Add global cluster metrics if this agent is the current kubernetes agent of the cluster.
	if k.ShouldGatherClusterMetrics() {
		morePoints, err = getGlobalMetrics(ctx, cl, now, k.ClusterName)
		if err != nil {
			multiErr = append(multiErr, err)
		}

		points = append(points, morePoints...)

		crdCertPoints, err := k.getCRDCertificateExpirations(ctx, now)
		if err != nil {
			multiErr = append(multiErr, err)
		}

		points = append(points, crdCertPoints...)

		webhookConfigCertPoints, err := k.getWebhookConfigCertificateExpirations(ctx, now)
		if err != nil {
			multiErr = append(multiErr, err)
		}

		points = append(points, webhookConfigCertPoints...)
	}

	return points, multiErr.MaybeUnwrap()
}

func (k *Kubernetes) getCertificateExpiration(ctx context.Context, config *rest.Config, now time.Time) ([]types.MetricPoint, error) {
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
		return nil, err
	}

	tlsConfig.ServerName = addr.Hostname()

	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr.Host)
	if err != nil {
		// Something went wrong with the connection, but it is not related to TLS
		return nil, err
	}

	defer conn.Close()

	tlsConn := tls.Client(conn, tlsConfig)

	err = tlsConn.HandshakeContext(ctx)
	if err != nil {
		// Something went wrong with the TLS handshake, we consider the certificate as expired
		logger.V(2).Println("An error occurred on TLS handshake:", err)

		return createPointsCertificateDaysAndPerc(time.Now(), time.Now(), certExpLabelDay, certExpLabelPerc, now, types.LabelItem, "api"), nil
	}

	if len(tlsConn.ConnectionState().PeerCertificates) == 0 {
		logger.V(2).Println("No peer certificate could be found for tls dial.")

		return nil, nil
	}

	expiry := tlsConn.ConnectionState().PeerCertificates[0]

	return createPointsCertificateDaysAndPerc(expiry.NotBefore, expiry.NotAfter, certExpLabelDay, certExpLabelPerc, now, types.LabelItem, "api"), nil
}

func (k *Kubernetes) getLocalAPIPoints(ctx context.Context, cl kubeClient, now time.Time) ([]types.MetricPoint, []error) {
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

	certificatePoints, errTmp := k.getCertificateExpiration(ctx, config, now)
	if errTmp != nil {
		err = append(err, errTmp)
	} else {
		points = append(points, certificatePoints...)
	}

	caCertificatePoints, errTmp := k.getCACertificateExpiration(config, now)
	if errTmp != nil {
		err = append(err, errTmp)
	} else {
		points = append(points, caCertificatePoints...)
	}

	return points, err
}

func (k *Kubernetes) getKubeletPoints(ctx context.Context, cl kubeClient, now time.Time) ([]types.MetricPoint, []error) {
	if k.NodeName == "" {
		return nil, []error{fmt.Errorf("%w: kubernetes.nodename is missing", errMissingConfig)}
	}

	node, err := cl.GetNode(ctx, k.NodeName)
	if err != nil {
		return nil, []error{err}
	}

	const kubeletConditionStatus = "kubernetes_kubelet_status"

	resultPoints := make([]types.MetricPoint, 0, 5) // expect 5 condition so 5 points

	for _, cond := range node.Status.Conditions {
		switch cond.Type {
		case corev1.NodeReady:
			status := types.StatusDescription{
				CurrentStatus:     types.StatusOk,
				StatusDescription: "node ready",
			}
			if cond.Status != corev1.ConditionTrue {
				status = types.StatusDescription{
					CurrentStatus:     types.StatusCritical,
					StatusDescription: "node is not ready: " + cond.Message,
				}
			}

			resultPoints = append(resultPoints, types.MetricPoint{
				Point: types.Point{Time: now, Value: float64(status.CurrentStatus.NagiosCode())},
				Labels: map[string]string{
					types.LabelName:      kubeletConditionStatus,
					types.LabelCondition: "ready",
				},
				Annotations: types.MetricAnnotations{
					Status: status,
				},
			})
		case corev1.NodeDiskPressure, corev1.NodeMemoryPressure, corev1.NodePIDPressure:
			typeText := map[corev1.NodeConditionType]string{
				corev1.NodeDiskPressure:   "disk pressure",
				corev1.NodeMemoryPressure: "memory pressure",
				corev1.NodePIDPressure:    "PID pressure",
			}[cond.Type]

			conditionLabel := map[corev1.NodeConditionType]string{
				corev1.NodeDiskPressure:   "disk_pressure",
				corev1.NodeMemoryPressure: "memory_pressure",
				corev1.NodePIDPressure:    "pid_pressure",
			}[cond.Type]

			status := types.StatusDescription{
				CurrentStatus:     types.StatusOk,
				StatusDescription: fmt.Sprintf("node %s cleared", typeText),
			}
			if cond.Status == corev1.ConditionTrue {
				status = types.StatusDescription{
					CurrentStatus:     types.StatusCritical,
					StatusDescription: fmt.Sprintf("node has %s: %s", typeText, cond.Message),
				}
			}

			resultPoints = append(resultPoints, types.MetricPoint{
				Point: types.Point{Time: now, Value: float64(status.CurrentStatus.NagiosCode())},
				Labels: map[string]string{
					types.LabelName:      kubeletConditionStatus,
					types.LabelCondition: conditionLabel,
				},
				Annotations: types.MetricAnnotations{
					Status: status,
				},
			})
		case corev1.NodeNetworkUnavailable:
			status := types.StatusDescription{
				CurrentStatus:     types.StatusOk,
				StatusDescription: "node network available",
			}
			if cond.Status == corev1.ConditionTrue {
				status = types.StatusDescription{
					CurrentStatus:     types.StatusCritical,
					StatusDescription: "node has networking issue: " + cond.Message,
				}
			}

			resultPoints = append(resultPoints, types.MetricPoint{
				Point: types.Point{Time: now, Value: float64(status.CurrentStatus.NagiosCode())},
				Labels: map[string]string{
					types.LabelName:      kubeletConditionStatus,
					types.LabelCondition: "network_unavailable",
				},
				Annotations: types.MetricAnnotations{
					Status: status,
				},
			})
		}
	}

	resultPoints = append(resultPoints, nodeAllocatablePoints(node, now)...)

	var ip string

	for _, a := range node.Status.Addresses {
		if a.Type == corev1.NodeInternalIP && a.Address != "" {
			ip = a.Address

			break
		}
	}

	if ip == "" {
		return resultPoints, []error{fmt.Errorf("can't retrieve kubelet cert: %w", errNodeHasNoInternalIP)}
	}

	addr := net.JoinHostPort(ip, "10250")

	cert, err := fetchServerCert(ctx, addr)
	if err != nil {
		return resultPoints, []error{fmt.Errorf("fetching kubelet cert: %w", err)}
	}

	resultPoints = append(resultPoints, createPointsCertificateDaysAndPerc(cert.NotBefore, cert.NotAfter, certExpLabelDay, certExpLabelPerc, now, types.LabelItem, "kubelet")...)

	return resultPoints, nil
}

// nodeAllocatablePoints returns the allocatable resources of the node as metrics:
// kubernetes_cpu_allocatable (cores), kubernetes_memory_allocatable (bytes),
// kubernetes_pods_allocatable (count) and kubernetes_ephemeral_storage_allocatable (bytes).
// We only expose allocatable (not capacity), as it reflects the resources usable by pods.
func nodeAllocatablePoints(node *corev1.Node, now time.Time) []types.MetricPoint {
	allocatable := node.Status.Allocatable

	values := map[string]float64{
		"kubernetes_cpu_allocatable":               allocatable.Cpu().AsApproximateFloat64(),
		"kubernetes_memory_allocatable":            allocatable.Memory().AsApproximateFloat64(),
		"kubernetes_pods_allocatable":              allocatable.Pods().AsApproximateFloat64(),
		"kubernetes_ephemeral_storage_allocatable": allocatable.StorageEphemeral().AsApproximateFloat64(),
	}

	points := make([]types.MetricPoint, 0, len(values))

	for name, value := range values {
		points = append(points, types.MetricPoint{
			Point: types.Point{Time: now, Value: value},
			Labels: map[string]string{
				types.LabelName: name,
			},
		})
	}

	return points
}

func (k *Kubernetes) getCACertificateExpiration(config *rest.Config, now time.Time) ([]types.MetricPoint, error) {
	caData := config.TLSClientConfig.CAData

	var err error

	if caData == nil && config.TLSClientConfig.CAFile != "" {
		// CAData takes precedence over CAFile, thus we only check CAFile if there is no CAData
		caData, err = os.ReadFile(config.TLSClientConfig.CAFile)
		if err != nil {
			return nil, err
		}
	} else if caData == nil {
		logger.V(2).Printf("No certificate data found for Kubernetes API")

		return nil, nil
	}

	caCert, err := decodeRawCert(caData)
	if err != nil {
		return nil, err
	}

	return createPointsCertificateDaysAndPerc(caCert.NotBefore, caCert.NotAfter, caExpLabelDay, caExpLabelPerc, now), nil
}

func (k *Kubernetes) getCRDCertificateExpirations(ctx context.Context, now time.Time) ([]types.MetricPoint, error) {
	crds, err := k.client.GetCRDs(ctx)
	if err != nil {
		return nil, err
	}

	certPoints := make([]types.MetricPoint, 0, len(crds))

	for _, crd := range crds {
		if crd.Spec.Conversion != nil &&
			crd.Spec.Conversion.Webhook != nil &&
			crd.Spec.Conversion.Webhook.ClientConfig != nil &&
			len(crd.Spec.Conversion.Webhook.ClientConfig.CABundle) > 0 {
			cert, err := decodeRawCert(crd.Spec.Conversion.Webhook.ClientConfig.CABundle)
			if err != nil {
				logger.V(2).Printf("Error decoding certificate from CRD %q: %v", crd.Name, err)

				continue
			}

			labels := []string{
				types.LabelItem, "crd-" + crd.Name,
				types.LabelMetaKubernetesCluster, k.ClusterName,
			}

			tmp := createPointsCertificateDaysAndPerc(cert.NotBefore, cert.NotAfter, certExpLabelDay, certExpLabelPerc, now, labels...)
			certPoints = append(certPoints, tmp...)
		}
	}

	return certPoints, nil
}

func (k *Kubernetes) getWebhookConfigCertificateExpirations(ctx context.Context, now time.Time) ([]types.MetricPoint, error) {
	mutList, valList, err := k.client.GetWebhookConfigurations(ctx)
	if err != nil {
		return nil, err
	}

	certPoints := make([]types.MetricPoint, 0, len(mutList)+len(valList))

	for _, mwc := range mutList {
		for _, webhook := range mwc.Webhooks {
			if len(webhook.ClientConfig.CABundle) > 0 {
				cert, err := decodeRawCert(webhook.ClientConfig.CABundle)
				if err != nil {
					logger.V(2).Printf("Error decoding certificate from MutatingWebhookConfiguration %q (WebHook %q): %v", mwc.Name, webhook.Name, err)

					continue
				}

				labels := []string{
					types.LabelItem, "mutatingwebhookconfig-" + mwc.Name + "-" + webhook.Name,
					types.LabelMetaKubernetesCluster, k.ClusterName,
				}

				tmp := createPointsCertificateDaysAndPerc(cert.NotBefore, cert.NotAfter, certExpLabelDay, certExpLabelPerc, now, labels...)
				certPoints = append(certPoints, tmp...)
			}
		}
	}

	for _, vwc := range valList {
		for _, webhook := range vwc.Webhooks {
			if len(webhook.ClientConfig.CABundle) > 0 {
				cert, err := decodeRawCert(webhook.ClientConfig.CABundle)
				if err != nil {
					logger.V(2).Printf("Error decoding certificate from ValidatingWebhookConfiguration %q (WebHook %q): %v", vwc.Name, webhook.Name, err)

					continue
				}

				labels := []string{
					types.LabelItem, "validatingwebhookconfig-" + vwc.Name + "-" + webhook.Name,
					types.LabelMetaKubernetesCluster, k.ClusterName,
				}

				tmp := createPointsCertificateDaysAndPerc(cert.NotBefore, cert.NotAfter, certExpLabelDay, certExpLabelPerc, now, labels...)
				certPoints = append(certPoints, tmp...)
			}
		}
	}

	return certPoints, nil
}

func fetchServerCert(ctx context.Context, addr string) (*x509.Certificate, error) {
	d := &tls.Dialer{
		NetDialer: &net.Dialer{Timeout: 5 * time.Second},
		Config: &tls.Config{
			InsecureSkipVerify: true, //nolint: gosec // We just want the cert, we don’t care about trust.
		},
	}

	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("dial error: %w", err)
	}

	defer conn.Close()

	tlsConn, ok := conn.(*tls.Conn)
	if !ok {
		return nil, fmt.Errorf("%w: %T", errUnexpectedConnType, conn)
	}

	state := tlsConn.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return nil, errNoCertFound
	}

	return state.PeerCertificates[0], nil
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

func createPointsCertificateDaysAndPerc(notBefore time.Time, notAfter time.Time, labelDays string, labelPerc string, now time.Time, extraLabelsKV ...string) []types.MetricPoint {
	labelsDays := map[string]string{types.LabelName: labelDays}
	labelsPerc := map[string]string{types.LabelName: labelPerc}

	if len(extraLabelsKV)%2 != 0 {
		panic(fmt.Sprintf("odd number of labels provided (got %q)", extraLabelsKV))
	}

	for i := 0; i < len(extraLabelsKV); i += 2 {
		labelsDays[extraLabelsKV[i]] = extraLabelsKV[i+1] //nolint:gosec // len is checked to be even above
		labelsPerc[extraLabelsKV[i]] = extraLabelsKV[i+1] //nolint:gosec // len is checked to be even above
	}

	remainingDays := notAfter.Sub(now).Hours() / 24

	if remainingDays < 0 {
		// we clamp remainingDays to 0 when the certificate already expired
		remainingDays = 0
	}

	lifeSpanDays := notAfter.Sub(notBefore).Hours() / 24

	var remainingPerc float64

	if lifeSpanDays > 365 {
		// Clamp to 1 year:
		//  * For other duration, when only ~8% of lifespan is remaining, it's fine to trigger
		//    a warning (30 days for 1 year lifespan, 7 days for 90 days lifespan).
		//    But for certificate valid for 10 year, it would result in warning at 300 days which is too soon.
		//  * Kubelet & Kubernetes API (at least with rancher) re-use the same NotBefore date, so even if the
		//    certificate is really valid only for 1 year, it's lifespan will increase every years.
		lifeSpanDays = 365
	}

	if lifeSpanDays <= 0 {
		// If lifeSpanHours is 0 (or negative, which shouldn't happen), use 0 as remaining perc to avoid division by 0
		remainingPerc = 0.0
	} else {
		remainingPerc = remainingDays / lifeSpanDays * 100
	}

	// Since we clamp the lifespan to max 1 year, make sure we don't have a remaining perc > 100
	if remainingPerc > 100 {
		remainingPerc = 100
	}

	pointDays := types.MetricPoint{
		Point: types.Point{
			Time:  now,
			Value: remainingDays,
		},
		Labels:      labelsDays,
		Annotations: types.MetricAnnotations{},
	}

	pointPerc := types.MetricPoint{
		Point: types.Point{
			Time:  now,
			Value: remainingPerc,
		},
		Labels:      labelsPerc,
		Annotations: types.MetricAnnotations{},
	}

	return []types.MetricPoint{pointDays, pointPerc}
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

	k.updateReplicasetOwners(ctx, cl)
	k.updatePVCs(ctx, cl)

	return nil
}

// updatePVCs refreshes the PersistentVolume -> PersistentVolumeClaim mapping used to
// resolve the in-pod volume name of a CSI mount. It is best-effort: on error (e.g.
// missing RBAC permission) the previous mapping is kept and the volume label is
// simply omitted on CSI volumes.
func (k *Kubernetes) updatePVCs(ctx context.Context, cl kubeClient) {
	pvcs, err := cl.GetPVCs(ctx)
	if err != nil {
		logger.V(2).Printf("kubernetes: unable to list PersistentVolumeClaims, the volume label will be missing on CSI volumes: %v", err)

		return
	}

	byPV := make(map[string]pvcReference, len(pvcs))

	for _, pvc := range pvcs {
		if pvc.Spec.VolumeName != "" {
			byPV[pvc.Spec.VolumeName] = pvcReference{namespace: pvc.Namespace, name: pvc.Name}
		}
	}

	k.pvcByPVName = byPV
}

// updateReplicasetOwners refreshes the ReplicaSet -> owner mapping used to resolve
// a pod's workload owner. It is best-effort: on error (e.g. missing RBAC permission)
// the previous mapping is kept and the immediate owner is reported instead.
func (k *Kubernetes) updateReplicasetOwners(ctx context.Context, cl kubeClient) {
	replicasets, err := cl.GetReplicasets(ctx)
	if err != nil {
		logger.V(2).Printf("kubernetes: unable to list ReplicaSets, owner will fall back to the immediate owner: %v", err)

		return
	}

	k.replicasetOwnerByUID = buildReplicasetOwnerByUID(replicasets)
}

// buildReplicasetOwnerByUID indexes ReplicaSets by their UID to their first owner
// (usually a Deployment), so a pod's workload owner can be resolved through its
// ReplicaSet.
func buildReplicasetOwnerByUID(replicasets []appsv1.ReplicaSet) map[string]metav1.OwnerReference {
	owners := make(map[string]metav1.OwnerReference, len(replicasets))

	for _, replicaset := range replicasets {
		if len(replicaset.OwnerReferences) > 0 {
			owners[string(replicaset.UID)] = replicaset.OwnerReferences[0]
		}
	}

	return owners
}

// PodVolumeLabels returns the Kubernetes labels (namespace, pod_name, owner_kind,
// owner_name) for the pod with the given UID. found is false when the pod is not
// (yet) known by the local cache.
func (k *Kubernetes) PodVolumeLabels(podUID string) (map[string]string, bool) {
	k.l.Lock()
	defer k.l.Unlock()

	pod, ok := k.podID2Pod[podUID]
	if !ok {
		return nil, false
	}

	_, ownerKind, ownerName := podOwnerRef(pod, k.replicasetOwnerByUID)

	return map[string]string{
		types.LabelNamespace: podNamespace(pod),
		types.LabelPodName:   pod.Name,
		types.LabelOwnerKind: strings.ToLower(ownerKind),
		types.LabelOwnerName: ownerName,
	}, true
}

// CSIVolumeLabels returns the labels identifying a CSI volume mount whose kubelet
// directory is dir. For a PVC-backed volume the directory is the PersistentVolume
// name, so it returns {pv: dir} plus {volume: <in-pod volume name>} when the PVC can
// be resolved (requires PVC list access). For an inline (ephemeral) CSI volume there
// is no PersistentVolume and the directory already is the in-pod volume name, so it
// returns {volume: dir}.
func (k *Kubernetes) CSIVolumeLabels(podUID, dir string) map[string]string {
	k.l.Lock()
	defer k.l.Unlock()

	pod, ok := k.podID2Pod[podUID]
	if !ok {
		return map[string]string{types.LabelPV: dir}
	}

	// Inline (ephemeral) CSI volume: the directory is the in-pod volume name, not a PV.
	for _, volume := range pod.Spec.Volumes {
		if volume.Name == dir && volume.CSI != nil {
			return map[string]string{types.LabelVolume: dir}
		}
	}

	// Otherwise the directory is the PersistentVolume name.
	labels := map[string]string{types.LabelPV: dir}

	if pvc, ok := k.pvcByPVName[dir]; ok && pvc.namespace == pod.Namespace {
		for _, volume := range pod.Spec.Volumes {
			if volumeClaimName(pod, volume) == pvc.name {
				labels[types.LabelVolume] = volume.Name

				break
			}
		}
	}

	return labels
}

// volumeClaimName returns the name of the PersistentVolumeClaim a pod volume is backed
// by, either referenced directly (persistentVolumeClaim) or auto-created for a generic
// ephemeral volume (the claim is named "<pod>-<volume>"). It returns "" for volumes
// that are not backed by a PVC.
func volumeClaimName(pod corev1.Pod, volume corev1.Volume) string {
	switch {
	case volume.PersistentVolumeClaim != nil:
		return volume.PersistentVolumeClaim.ClaimName
	case volume.Ephemeral != nil:
		return pod.Name + "-" + volume.Name
	default:
		return ""
	}
}

func kuberIDtoRuntimeID(containerID string) string {
	containerID = strings.TrimPrefix(containerID, "docker://")

	if after, ok := strings.CutPrefix(containerID, "containerd://"); ok {
		containerID = after
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
	// GetPVCs returns all PersistentVolumeClaims in the cluster.
	GetPVCs(ctx context.Context) ([]corev1.PersistentVolumeClaim, error)
	// GetReplicasets returns all replicasets in the cluster.
	GetReplicasets(ctx context.Context) ([]appsv1.ReplicaSet, error)
	// GetDeployments returns all deployments in the cluster.
	GetDeployments(ctx context.Context) ([]appsv1.Deployment, error)
	// GetStatefulSets returns all statefulsets in the cluster.
	GetStatefulSets(ctx context.Context) ([]appsv1.StatefulSet, error)
	// GetDaemonSets returns all daemonsets in the cluster.
	GetDaemonSets(ctx context.Context) ([]appsv1.DaemonSet, error)
	// GetScale returns the desired replicas (spec.replicas of the scale subresource) of the
	// object identified by gvr/namespace/name. It returns an error when the object's resource has
	// no scale subresource or when the agent lacks the permission to read it.
	GetScale(ctx context.Context, gvr schema.GroupVersionResource, namespace, name string) (int32, error)
	// GetCRDs returns all CRDs in the cluster.
	GetCRDs(ctx context.Context) ([]apiextv1.CustomResourceDefinition, error)
	// GetWebhookConfigurations returns all MutatingWebhookConfigurations and ValidatingWebhookConfigurations in the cluster.
	GetWebhookConfigurations(ctx context.Context) ([]admv1.MutatingWebhookConfiguration, []admv1.ValidatingWebhookConfiguration, error)
	GetServerVersion(ctx context.Context) (*version.Info, error)
	IsUsingLocalAPI() bool
	Config() *rest.Config
}

type realClient struct {
	// We need one client per API group (v1, discovery.k8s.io/v1, apps/v1, ...)
	coreClient  *rest.RESTClient
	discoClient *rest.RESTClient
	appsClient  *rest.RESTClient
	extClient   *rest.RESTClient
	admClient   *rest.RESTClient

	// The dynamic client is used to read the scale subresource of arbitrary resources (including
	// CRDs managed by operators), which the typed clients above cannot do.
	dynamicClient dynamic.Interface

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

func (cl *realClient) GetPVCs(ctx context.Context) ([]corev1.PersistentVolumeClaim, error) {
	var pvcs corev1.PersistentVolumeClaimList

	err := cl.coreClient.Get().Resource("persistentvolumeclaims").Do(ctx).Into(&pvcs)
	if err != nil {
		return nil, err
	}

	return pvcs.Items, nil
}

func (cl *realClient) GetReplicasets(ctx context.Context) ([]appsv1.ReplicaSet, error) {
	var replicasets appsv1.ReplicaSetList

	err := cl.appsClient.Get().Resource("replicasets").Do(ctx).Into(&replicasets)
	if err != nil {
		return nil, err
	}

	return replicasets.Items, nil
}

func (cl *realClient) GetDeployments(ctx context.Context) ([]appsv1.Deployment, error) {
	var deployments appsv1.DeploymentList

	err := cl.appsClient.Get().Resource("deployments").Do(ctx).Into(&deployments)
	if err != nil {
		return nil, err
	}

	return deployments.Items, nil
}

func (cl *realClient) GetStatefulSets(ctx context.Context) ([]appsv1.StatefulSet, error) {
	var statefulSets appsv1.StatefulSetList

	err := cl.appsClient.Get().Resource("statefulsets").Do(ctx).Into(&statefulSets)
	if err != nil {
		return nil, err
	}

	return statefulSets.Items, nil
}

func (cl *realClient) GetDaemonSets(ctx context.Context) ([]appsv1.DaemonSet, error) {
	var daemonSets appsv1.DaemonSetList

	err := cl.appsClient.Get().Resource("daemonsets").Do(ctx).Into(&daemonSets)
	if err != nil {
		return nil, err
	}

	return daemonSets.Items, nil
}

func (cl *realClient) GetScale(ctx context.Context, gvr schema.GroupVersionResource, namespace, name string) (int32, error) {
	// Pod owners are always namespaced (a pod can't be owned by a cluster-scoped object).
	scale, err := cl.dynamicClient.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{}, "scale")
	if err != nil {
		return 0, err
	}

	replicas, found, err := unstructured.NestedInt64(scale.Object, "spec", "replicas")
	if err != nil {
		return 0, err
	}

	if !found {
		return 0, errNoScaleReplicas
	}

	return int32(replicas), nil //nolint:gosec
}

func (cl *realClient) GetCRDs(ctx context.Context) ([]apiextv1.CustomResourceDefinition, error) {
	var crds apiextv1.CustomResourceDefinitionList

	err := cl.extClient.Get().Resource("customresourcedefinitions").Do(ctx).Into(&crds)
	if err != nil {
		return nil, err
	}

	return crds.Items, nil
}

func (cl *realClient) GetWebhookConfigurations(ctx context.Context) ([]admv1.MutatingWebhookConfiguration, []admv1.ValidatingWebhookConfiguration, error) {
	var (
		mutList admv1.MutatingWebhookConfigurationList
		valList admv1.ValidatingWebhookConfigurationList
	)

	err := cl.admClient.Get().
		AbsPath("/apis/admissionregistration.k8s.io/v1/mutatingwebhookconfigurations").
		Do(ctx).
		Into(&mutList)
	if err != nil {
		return nil, nil, fmt.Errorf("mutatingwebhookconfigurations: %w", err)
	}

	err = cl.admClient.Get().
		AbsPath("/apis/admissionregistration.k8s.io/v1/validatingwebhookconfigurations").
		Do(ctx).
		Into(&valList)
	if err != nil {
		return nil, nil, fmt.Errorf("validatingwebhookconfigurations: %w", err)
	}

	return mutList.Items, valList.Items, nil
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

func makeClients(config *rest.Config) (coreClient, discoClient, appsClient, extClient, admClient *rest.RESTClient, err error) {
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
			apiPath:       apiPathAPIs,
			result:        &discoClient,
		},
		{
			groupVersion:  &appsv1.SchemeGroupVersion,
			addToSchemeFn: appsv1.AddToScheme,
			apiPath:       apiPathAPIs,
			result:        &appsClient,
		},
		{
			groupVersion:  &apiextv1.SchemeGroupVersion,
			addToSchemeFn: apiextv1.AddToScheme,
			apiPath:       apiPathAPIs,
			result:        &extClient,
		},
		{
			groupVersion:  &admv1.SchemeGroupVersion,
			addToSchemeFn: admv1.AddToScheme,
			apiPath:       apiPathAPIs,
			result:        &admClient,
		},
	}

	for _, setup := range clientSetups {
		scheme := runtime.NewScheme()

		err = setup.addToSchemeFn(scheme)
		if err != nil {
			return nil, nil, nil, nil, nil, fmt.Errorf("failed to build scheme for %s: %w", setup.groupVersion, err)
		}

		cfgCopy := *config
		cfgCopy.GroupVersion = setup.groupVersion
		cfgCopy.APIPath = setup.apiPath
		cfgCopy.NegotiatedSerializer = serializer.NewCodecFactory(scheme).WithoutConversion()
		cfgCopy.UserAgent = rest.DefaultKubernetesUserAgent()

		*setup.result, err = rest.UnversionedRESTClientFor(&cfgCopy)
		if err != nil {
			return nil, nil, nil, nil, nil, fmt.Errorf("for %s: %w", setup.groupVersion, err)
		}
	}

	return coreClient, discoClient, appsClient, extClient, admClient, nil
}

func openConnection(ctx context.Context, kubeConfig string, localNode string) (kubeClient, error) {
	config, err := getRestConfig(kubeConfig)
	if err != nil {
		return nil, err
	}

	coreClient, discoClient, appsClient, extClient, admClient, err := makeClients(config)
	if err != nil {
		return nil, fmt.Errorf("failed to build rest clients: %w", err)
	}

	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to build dynamic client: %w", err)
	}

	client := realClient{
		coreClient:    coreClient,
		discoClient:   discoClient,
		appsClient:    appsClient,
		extClient:     extClient,
		admClient:     admClient,
		dynamicClient: dynamicClient,
		config:        config,
		useLocalAPI:   false,
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
		Namespace(defaultNamespace).
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

				coreClient, discoClient, appsClient, extClient, admClient, err := makeClients(&shallowCopy)
				if err != nil {
					return false, fmt.Errorf("failed to build rest clients: %w", err)
				}

				dynamicClient, err := dynamic.NewForConfig(&shallowCopy)
				if err != nil {
					return false, fmt.Errorf("failed to build dynamic client: %w", err)
				}

				err = discoClient.
					Get().
					Namespace(defaultNamespace).
					Resource("endpointslices").
					Do(ctx).
					Error()
				if err != nil {
					return false, fmt.Errorf("failed list endpointslices with new client: %w", err)
				}

				cl.coreClient = coreClient
				cl.discoClient = discoClient
				cl.appsClient = appsClient
				cl.extClient = extClient
				cl.admClient = admClient
				cl.dynamicClient = dynamicClient
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
	maps.Copy(labels, containerLabels)

	maps.Copy(labels, c.pod.Labels)

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
