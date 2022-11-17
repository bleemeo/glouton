package kubernetes

import (
	"context"
	"glouton/types"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type metricsFunc func(context.Context, kubeClient, time.Time) ([]types.MetricPoint, error)

// getGlobalMetrics returns global cluster metrics.
func getGlobalMetrics(
	ctx context.Context,
	cl kubeClient,
	now time.Time,
	clusterName string,
) ([]types.MetricPoint, error) {
	var points []types.MetricPoint

	metricFunctions := []metricsFunc{podsCount, namespacesCount, nodesCount}

	for _, f := range metricFunctions {
		morePoints, err := f(ctx, cl, now)
		if err != nil {
			return points, err
		}

		points = append(points, morePoints...)
	}

	// Add the Kubernetes cluster meta label to global metrics, this is used to
	// replace the agent ID by the Kubernetes agent ID in the relabel hook.
	for _, point := range points {
		point.Labels[types.LabelMetaKubernetesCluster] = clusterName
	}

	return points, nil
}

// namespacesCount returns the metric kubernetes_namespaces_count with the
// current state of the namespace in the labels (active or terminating).
func namespacesCount(ctx context.Context, cl kubeClient, now time.Time) ([]types.MetricPoint, error) {
	ns, err := cl.GetNamespaces(ctx)
	if err != nil {
		return nil, err
	}

	nsCountByState := make(map[string]int)

	for _, namespace := range ns {
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

	return points, nil
}

// nodesCount returns the metric kubernetes_nodes_count.
func nodesCount(ctx context.Context, cl kubeClient, now time.Time) ([]types.MetricPoint, error) {
	nodes, err := cl.GetNodes(ctx)
	if err != nil {
		return nil, err
	}

	points := []types.MetricPoint{{
		Point: types.Point{Time: now, Value: float64(len(nodes))},
		Labels: map[string]string{
			types.LabelName: "kubernetes_nodes_count",
		},
	}}

	return points, nil
}

// podsCount returns the metric kubernetes_pods_count with three labels:
// - kind: the of the pod's owner, e.g. daemonset, deployment.
// - name: the name of the pod's owner, e.g. glouton, kube-proxy.
// - state: the current state of the pod (pending, running, succeeded or failed).
func podsCount(ctx context.Context, cl kubeClient, now time.Time) ([]types.MetricPoint, error) {
	// For Kubernetes deployments with multiple replicas, a replicaset is created. This means the pod's
	// owner is the replicaset (which has a generated name, e.g. "coredns-565d847f94"). In this case we
	// prefer to associate this pod with the owner of the replicaset (e.g. the deployment "coredns").
	replicasets, err := cl.GetReplicasets(ctx)
	if err != nil {
		return nil, err
	}

	replicasetOwnerByUUID := make(map[string]v1.OwnerReference)

	for _, rs := range replicasets {
		if len(rs.OwnerReferences) > 0 {
			replicasetOwnerByUUID[string(rs.UID)] = rs.OwnerReferences[0]
		}
	}

	pods, err := cl.GetPODs(ctx, "")
	if err != nil {
		return nil, err
	}

	type podLabels struct {
		State     string
		Kind      string
		Name      string
		Namespace string
	}

	podsCountByLabels := make(map[podLabels]int, len(pods))

	for _, pod := range pods {
		var kind, name string

		if len(pod.OwnerReferences) > 0 {
			ownerRef := pod.OwnerReferences[0]
			kind, name = ownerRef.Kind, ownerRef.Name

			// For replicasets, get the owner one level higher.
			if kind == "ReplicaSet" {
				ownerRef := replicasetOwnerByUUID[string(ownerRef.UID)]

				if ownerRef.Kind != "" {
					kind, name = ownerRef.Kind, ownerRef.Name
				}
			}
		}

		// An empty namespace is the default namespace.
		namespace := pod.Namespace
		if namespace == "" {
			namespace = "default"
		}

		labels := podLabels{
			State:     strings.ToLower(string(podPhase(pod))),
			Kind:      strings.ToLower(kind),
			Name:      strings.ToLower(name),
			Namespace: namespace,
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

	return points, nil
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
