package kubernetes

import (
	"context"
	"glouton/types"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type metricsFunc func(kubeCache, time.Time) []types.MetricPoint

type kubeCache struct {
	pods                 []corev1.Pod
	replicasetOwnerByUID map[string]metav1.OwnerReference
	namespaces           []corev1.Namespace
	nodes                []corev1.Node
}

// getGlobalMetrics returns global cluster metrics.
func getGlobalMetrics(
	ctx context.Context,
	cl kubeClient,
	now time.Time,
	clusterName string,
) ([]types.MetricPoint, error) {
	var (
		err      error
		multiErr prometheus.MultiError
		cache    kubeCache
	)

	// Add resources to the cache.
	cache.pods, err = cl.GetPODs(ctx, "")
	multiErr.Append(err)

	cache.namespaces, err = cl.GetNamespaces(ctx)
	multiErr.Append(err)

	cache.nodes, err = cl.GetNodes(ctx)
	multiErr.Append(err)

	replicasets, err := cl.GetReplicasets(ctx)
	multiErr.Append(err)

	cache.replicasetOwnerByUID = make(map[string]metav1.OwnerReference, len(replicasets))

	for _, replicaset := range replicasets {
		if len(replicaset.OwnerReferences) > 0 {
			cache.replicasetOwnerByUID[string(replicaset.UID)] = replicaset.OwnerReferences[0]
		}
	}

	var points []types.MetricPoint

	metricFunctions := []metricsFunc{podsCount, requestsAndLimits, namespacesCount, nodesCount}

	for _, f := range metricFunctions {
		points = append(points, f(cache, now)...)
	}

	// Add the Kubernetes cluster meta label to global metrics, this is used to
	// replace the agent ID by the Kubernetes agent ID in the relabel hook.
	for _, point := range points {
		point.Labels[types.LabelMetaKubernetesCluster] = clusterName
	}

	return points, multiErr.MaybeUnwrap()
}

// namespacesCount returns the metric kubernetes_namespaces_count with the
// current state of the namespace in the labels (active or terminating).
func namespacesCount(cache kubeCache, now time.Time) []types.MetricPoint {
	nsCountByState := make(map[string]int)

	for _, namespace := range cache.namespaces {
		state := strings.ToLower(string(namespace.Status.Phase))
		nsCountByState[state]++
	}

	points := make([]types.MetricPoint, 0, len(nsCountByState))

	for state, count := range nsCountByState {
		points = append(points, types.MetricPoint{
			Point: types.Point{Time: now, Value: float64(count)},
			Labels: map[string]string{
				types.LabelName:  "kubernetes_namespaces_count",
				types.LabelState: state,
			},
		})
	}

	return points
}

// nodesCount returns the metric kubernetes_nodes_count.
func nodesCount(cache kubeCache, now time.Time) []types.MetricPoint {
	points := []types.MetricPoint{{
		Point: types.Point{Time: now, Value: float64(len(cache.nodes))},
		Labels: map[string]string{
			types.LabelName: "kubernetes_nodes_count",
		},
	}}

	return points
}

// podsCount returns the metric kubernetes_pods_count with the following labels:
// - owner_kind: the kind of the pod's owner, e.g. daemonset, deployment.
// - owner_name: the name of the pod's owner, e.g. glouton, kube-proxy.
// - state: the current state of the pod (pending, running, succeeded or failed).
// - namespace: the pod's namespace.
func podsCount(cache kubeCache, now time.Time) []types.MetricPoint {
	type podLabels struct {
		State     string
		Kind      string
		Name      string
		Namespace string
	}

	podsCountByLabels := make(map[podLabels]int, len(cache.pods))

	for _, pod := range cache.pods {
		kind, name := podOwner(pod, cache.replicasetOwnerByUID)

		labels := podLabels{
			State:     strings.ToLower(string(podPhase(pod))),
			Kind:      strings.ToLower(kind),
			Name:      strings.ToLower(name),
			Namespace: podNamespace(pod),
		}

		podsCountByLabels[labels]++
	}

	points := make([]types.MetricPoint, 0, len(podsCountByLabels))

	for podLabels, count := range podsCountByLabels {
		labels := map[string]string{
			types.LabelName:      "kubernetes_pods_count",
			types.LabelState:     podLabels.State,
			types.LabelNamespace: podLabels.Namespace,
		}

		if podLabels.Kind != "" {
			labels[types.LabelOwnerKind] = podLabels.Kind
		}

		if podLabels.Name != "" {
			labels[types.LabelOwnerName] = podLabels.Name
		}

		points = append(points, types.MetricPoint{
			Point:  types.Point{Time: now, Value: float64(count)},
			Labels: labels,
		})
	}

	return points
}

// podOwner return the kind and the name of the owner of a pod.
func podOwner(pod corev1.Pod, replicasetOwnerByUID map[string]metav1.OwnerReference) (kind string, name string) {
	if len(pod.OwnerReferences) == 0 {
		return "", ""
	}

	ownerRef := pod.OwnerReferences[0]
	kind, name = ownerRef.Kind, ownerRef.Name

	// For Kubernetes deployments with multiple replicas, a replicaset is created. This means the pod's
	// owner is the replicaset (which has a generated name, e.g. "coredns-565d847f94"). In this case we
	// prefer to associate this pod with the owner of the replicaset (e.g. the deployment "coredns").
	if kind == "ReplicaSet" {
		ownerRef := replicasetOwnerByUID[string(ownerRef.UID)]

		if ownerRef.Kind != "" {
			kind, name = ownerRef.Kind, ownerRef.Name
		}
	}

	return kind, name
}

// podNamespace returns the namespace of a pod.
func podNamespace(pod corev1.Pod) string {
	// An empty namespace is the default namespace.
	namespace := pod.Namespace
	if namespace == "" {
		namespace = "default"
	}

	return namespace
}

// podPhase returns the status of a pod.
func podPhase(pod corev1.Pod) corev1.PodPhase {
	if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
		return pod.Status.Phase
	}

	// When the phase is pending or running, we have to check the containers inside the pod to
	// return a relevant status, the pod may be in the running state while the container inside
	// is in a crash loop, or the pod may be pending if the init container failed.
	status := initContainerPhase(pod.Status)
	if status != corev1.PodRunning {
		return status
	}

	status = containerPhase(pod.Status)
	if status != corev1.PodRunning {
		return status
	}

	return pod.Status.Phase
}

// containerPhase returns the status of the containers.
func containerPhase(podStatus corev1.PodStatus) corev1.PodPhase {
	for _, status := range podStatus.ContainerStatuses {
		switch {
		case status.State.Terminated != nil:
			return corev1.PodFailed
		case status.State.Waiting != nil:
			if status.State.Waiting.Reason == "CrashLoopBackOff" {
				return corev1.PodFailed
			}

			return corev1.PodPending
		case !status.Ready:
			return corev1.PodPending
		}
	}

	return corev1.PodRunning
}

// initContainerPhase returns the status of the init containers.
func initContainerPhase(podStatus corev1.PodStatus) corev1.PodPhase {
	for _, status := range podStatus.InitContainerStatuses {
		switch {
		case status.State.Running != nil:
			continue
		case status.State.Terminated != nil:
			if status.State.Terminated.ExitCode == 0 {
				// An init container exited with code 0 means the container succeeded.
				continue
			}

			return corev1.PodFailed
		case status.State.Waiting != nil:
			return corev1.PodPending
		}
	}

	return corev1.PodRunning
}

// requestsAndLimits returns the metrics kubernetes_(cpu|memory)_(request|limit) with the following labels:
// - owner_kind: the kind of the pod's owner, e.g. daemonset, deployment.
// - owner_name: the name of the pod's owner, e.g. glouton, kube-proxy.
// - namespace: the pod's namespace.
func requestsAndLimits(cache kubeCache, now time.Time) []types.MetricPoint {
	// object represents a Kubernetes object.
	type object struct {
		Kind      string
		Name      string
		Namespace string
	}

	type resources struct {
		cpuRequests    float64
		cpuLimits      float64
		memoryRequests float64
		memoryLimits   float64
	}

	resourceMap := make(map[object]resources, len(cache.pods))

	for _, pod := range cache.pods {
		// Sum requests and limit over containers in the pod.
		cpuRequests, cpuLimits, memoryRequests, memoryLimits := 0., 0., 0., 0.

		for _, container := range pod.Spec.Containers {
			cpuRequests += container.Resources.Requests.Cpu().AsApproximateFloat64()
			cpuLimits += container.Resources.Limits.Cpu().AsApproximateFloat64()
			memoryRequests += container.Resources.Requests.Memory().AsApproximateFloat64()
			memoryLimits += container.Resources.Limits.Memory().AsApproximateFloat64()
		}

		kind, name := podOwner(pod, cache.replicasetOwnerByUID)

		obj := object{
			Kind:      kind,
			Name:      name,
			Namespace: podNamespace(pod),
		}

		prevResources := resourceMap[obj]

		resourceMap[obj] = resources{
			cpuRequests:    prevResources.cpuRequests + cpuRequests,
			cpuLimits:      prevResources.cpuLimits + cpuLimits,
			memoryRequests: prevResources.memoryRequests + memoryRequests,
			memoryLimits:   prevResources.memoryLimits + memoryLimits,
		}
	}

	// There are 4 points per Kubernetes object :
	// cpu requests, cpu limits, memory requests, memory limits.
	points := make([]types.MetricPoint, 0, len(resourceMap)*4)

	for obj, resource := range resourceMap {
		values := map[string]float64{
			"kubernetes_cpu_requests":    resource.cpuRequests,
			"kubernetes_cpu_limits":      resource.cpuLimits,
			"kubernetes_memory_requests": resource.memoryRequests,
			"kubernetes_memory_limits":   resource.memoryLimits,
		}

		for name, value := range values {
			labels := map[string]string{
				types.LabelName:      name,
				types.LabelNamespace: obj.Namespace,
			}

			if obj.Kind != "" {
				labels[types.LabelOwnerKind] = obj.Kind
			}

			if obj.Name != "" {
				labels[types.LabelOwnerName] = obj.Name
			}

			points = append(points, types.MetricPoint{
				Point:  types.Point{Time: now, Value: value},
				Labels: labels,
			})
		}
	}

	return points
}
