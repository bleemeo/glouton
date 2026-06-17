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

package kubernetes

import (
	"context"
	"strings"
	"time"

	"github.com/bleemeo/glouton/logger"
	"github.com/bleemeo/glouton/types"

	"github.com/prometheus/client_golang/prometheus"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	defaultNamespace            = "default"
	metricNameReplicasDesired   = "kubernetes_replicas_desired"
	metricNameReplicasReady     = "kubernetes_replicas_ready"
	metricNameReplicasAvailable = "kubernetes_replicas_available"
)

type metricsFunc func(kubeCache, time.Time) []types.MetricPoint

type kubeCache struct {
	pods                 []corev1.Pod
	replicasetOwnerByUID map[string]metav1.OwnerReference
	namespaces           []corev1.Namespace
	nodes                []corev1.Node
	deployments          []appsv1.Deployment
	statefulSets         []appsv1.StatefulSet
	daemonSets           []appsv1.DaemonSet
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

	cache.deployments, err = cl.GetDeployments(ctx)
	multiErr.Append(err)

	cache.statefulSets, err = cl.GetStatefulSets(ctx)
	multiErr.Append(err)

	cache.daemonSets, err = cl.GetDaemonSets(ctx)
	multiErr.Append(err)

	cache.replicasetOwnerByUID = make(map[string]metav1.OwnerReference, len(replicasets))

	for _, replicaset := range replicasets {
		if len(replicaset.OwnerReferences) > 0 {
			cache.replicasetOwnerByUID[string(replicaset.UID)] = replicaset.OwnerReferences[0]
		}
	}

	// Compute cluster metrics.
	var points []types.MetricPoint //nolint:prealloc

	metricFunctions := []metricsFunc{podsCount, requestsAndLimits, namespacesCount, nodesCount, podsRestartCount, workloadReplicas}

	for _, f := range metricFunctions {
		points = append(points, f(cache, now)...)
	}

	// Generic replicas metrics for owners not handled by workloadReplicas (operators/CRDs,
	// bare ReplicaSets). This needs the client (scale subresource) so it can't be a metricsFunc.
	points = append(points, genericReplicas(ctx, cl, cache, now)...)

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
			types.LabelOwnerKind: podLabels.Kind,
			types.LabelOwnerName: podLabels.Name,
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
	_, kind, name = podOwnerRef(pod, replicasetOwnerByUID)

	return kind, name
}

// podOwnerRef returns the apiVersion, kind and name of the owner of a pod.
func podOwnerRef(pod corev1.Pod, replicasetOwnerByUID map[string]metav1.OwnerReference) (apiVersion, kind, name string) {
	// If the pod has no owner, use the pod name.
	if len(pod.OwnerReferences) == 0 {
		return pod.APIVersion, pod.Kind, pod.Name
	}

	ownerRef := pod.OwnerReferences[0]
	apiVersion, kind, name = ownerRef.APIVersion, ownerRef.Kind, ownerRef.Name

	// For Kubernetes deployments with multiple replicas, a replicaset is created. This means the pod's
	// owner is the replicaset (which has a generated name, e.g. "coredns-565d847f94"). In this case we
	// prefer to associate this pod with the owner of the replicaset (e.g. the deployment "coredns").
	if kind == "ReplicaSet" {
		parentRef := replicasetOwnerByUID[string(ownerRef.UID)]

		if parentRef.Kind != "" {
			apiVersion, kind, name = parentRef.APIVersion, parentRef.Kind, parentRef.Name
		}
	}

	return apiVersion, kind, name
}

// podNamespace returns the namespace of a pod.
func podNamespace(pod corev1.Pod) string {
	return namespaceOrDefault(pod.Namespace)
}

// namespaceOrDefault returns the given namespace, or the default namespace if it is empty.
func namespaceOrDefault(namespace string) string {
	if namespace == "" {
		return defaultNamespace
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
			if status.State.Waiting.Reason == "CrashLoopBackOff" {
				return corev1.PodFailed
			}

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
		kind, name := podOwner(pod, cache.replicasetOwnerByUID)
		obj := object{
			Kind:      kind,
			Name:      name,
			Namespace: podNamespace(pod),
		}

		// Sum requests and limits over all containers in the pod.
		for _, container := range pod.Spec.Containers {
			prevResources := resourceMap[obj]

			resourceMap[obj] = resources{
				cpuRequests:    prevResources.cpuRequests + container.Resources.Requests.Cpu().AsApproximateFloat64(),
				cpuLimits:      prevResources.cpuLimits + container.Resources.Limits.Cpu().AsApproximateFloat64(),
				memoryRequests: prevResources.memoryRequests + container.Resources.Requests.Memory().AsApproximateFloat64(),
				memoryLimits:   prevResources.memoryLimits + container.Resources.Limits.Memory().AsApproximateFloat64(),
			}
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
				types.LabelOwnerKind: strings.ToLower(obj.Kind),
				types.LabelOwnerName: strings.ToLower(obj.Name),
				types.LabelNamespace: obj.Namespace,
			}

			points = append(points, types.MetricPoint{
				Point:  types.Point{Time: now, Value: value},
				Labels: labels,
			})
		}
	}

	return points
}

// podsRestartCount returns the metric kubernetes_pods_restart_count with the following labels:
// - pod_name: the pod's name.
// - owner_kind: the kind of the pod's owner, e.g. daemonset, deployment.
// - owner_name: the name of the pod's owner, e.g. glouton, kube-proxy.
// - namespace: the pod's namespace.
func podsRestartCount(cache kubeCache, now time.Time) []types.MetricPoint {
	points := make([]types.MetricPoint, 0, len(cache.pods))

	for _, pod := range cache.pods {
		kind, name := podOwner(pod, cache.replicasetOwnerByUID)

		labels := map[string]string{
			types.LabelName:      "kubernetes_pods_restart_count",
			types.LabelOwnerKind: strings.ToLower(kind),
			types.LabelOwnerName: strings.ToLower(name),
			types.LabelPodName:   pod.Name,
			types.LabelNamespace: pod.Namespace,
		}

		// The restart count of a pod is the sum of the restart counts of its containers.
		restartCount := int32(0)

		for _, container := range pod.Status.ContainerStatuses {
			restartCount += container.RestartCount
		}

		points = append(points, types.MetricPoint{
			Point:  types.Point{Time: now, Value: float64(restartCount)},
			Labels: labels,
		})
	}

	return points
}

// workloadReplicas returns the metrics kubernetes_replicas_(desired|ready|available) for each
// Deployment, StatefulSet and DaemonSet in the cluster, with the following labels:
// - owner_kind: the kind of the workload (deployment, statefulset or daemonset).
// - owner_name: the name of the workload.
// - namespace: the workload's namespace.
func workloadReplicas(cache kubeCache, now time.Time) []types.MetricPoint {
	type replicas struct {
		desired   float64
		ready     float64
		available float64
	}

	// 3 points (desired, ready, available) per workload.
	workloadCount := len(cache.deployments) + len(cache.statefulSets) + len(cache.daemonSets)
	points := make([]types.MetricPoint, 0, workloadCount*3)

	addWorkload := func(kind, name, namespace string, r replicas) {
		values := map[string]float64{
			metricNameReplicasDesired:   r.desired,
			metricNameReplicasReady:     r.ready,
			metricNameReplicasAvailable: r.available,
		}

		for metricName, value := range values {
			points = append(points, types.MetricPoint{
				Point: types.Point{Time: now, Value: value},
				Labels: map[string]string{
					types.LabelName:      metricName,
					types.LabelOwnerKind: kind,
					types.LabelOwnerName: strings.ToLower(name),
					types.LabelNamespace: namespace,
				},
			})
		}
	}

	for _, deployment := range cache.deployments {
		// spec.replicas defaults to 1 when unset.
		desired := float64(1)
		if deployment.Spec.Replicas != nil {
			desired = float64(*deployment.Spec.Replicas)
		}

		addWorkload("deployment", deployment.Name, namespaceOrDefault(deployment.Namespace), replicas{
			desired:   desired,
			ready:     float64(deployment.Status.ReadyReplicas),
			available: float64(deployment.Status.AvailableReplicas),
		})
	}

	for _, statefulSet := range cache.statefulSets {
		// spec.replicas defaults to 1 when unset.
		desired := float64(1)
		if statefulSet.Spec.Replicas != nil {
			desired = float64(*statefulSet.Spec.Replicas)
		}

		addWorkload("statefulset", statefulSet.Name, namespaceOrDefault(statefulSet.Namespace), replicas{
			desired:   desired,
			ready:     float64(statefulSet.Status.ReadyReplicas),
			available: float64(statefulSet.Status.AvailableReplicas),
		})
	}

	for _, daemonSet := range cache.daemonSets {
		// DaemonSets have no spec.replicas: the desired count is the number of nodes
		// that should run the daemon.
		addWorkload("daemonset", daemonSet.Name, namespaceOrDefault(daemonSet.Namespace), replicas{
			desired:   float64(daemonSet.Status.DesiredNumberScheduled),
			ready:     float64(daemonSet.Status.NumberReady),
			available: float64(daemonSet.Status.NumberAvailable),
		})
	}

	return points
}

// genericReplicasSkippedKinds are owner kinds handled elsewhere (workloadReplicas) or that have
// no controller / no meaningful desired count, so genericReplicas ignores them.
//
//nolint:gochecknoglobals
var genericReplicasSkippedKinds = map[string]bool{
	"Deployment":  true, // handled by workloadReplicas
	"StatefulSet": true, // handled by workloadReplicas
	"DaemonSet":   true, // handled by workloadReplicas
	"Node":        true, // static/mirror pods, no replicas concept
	"Pod":         true, // standalone pod
	"Job":         true, // completion semantics, not replicas (a finished Job has 0 ready pods)
	"CronJob":     true, // same as Job, and spawns a new Job name on every run
	"":            true, // unknown owner
}

// builtinScaleResources maps built-in "group/Kind" to their resource name (plural) for the scale
// subresource, avoiding a discovery round-trip. Only ReplicaSet is reachable here (the apps
// workloads are skipped above and DaemonSet has no scale), but the others are listed for clarity.
//
//nolint:gochecknoglobals
var builtinScaleResources = map[string]string{
	"apps/ReplicaSet":  "replicasets",
	"apps/Deployment":  "deployments",
	"apps/StatefulSet": "statefulsets",
}

// genericReplicas returns kubernetes_replicas_ready (and kubernetes_replicas_desired when a scale
// subresource is available) for pods whose owner is NOT handled by workloadReplicas. This covers
// operator-managed pods (owned by a CRD) and bare ReplicaSets.
//
//   - ready is computed by counting pods in the Ready condition, grouped by owner.
//   - desired comes from the owner's scale subresource (best-effort): owners without a scale
//     subresource, or for which the agent lacks permission, simply don't get a desired metric.
//     available isn't emitted here: it can't be derived generically (it depends on the controller's
//     minReadySeconds).
func genericReplicas(ctx context.Context, cl kubeClient, cache kubeCache, now time.Time) []types.MetricPoint {
	type owner struct {
		apiVersion string
		kind       string
		name       string
		namespace  string
	}

	readyByOwner := make(map[owner]int)

	for _, pod := range cache.pods {
		apiVersion, kind, name := podOwnerRef(pod, cache.replicasetOwnerByUID)

		if genericReplicasSkippedKinds[kind] {
			continue
		}

		key := owner{
			apiVersion: apiVersion,
			kind:       kind,
			name:       name,
			namespace:  namespaceOrDefault(pod.Namespace),
		}

		// "+= 0" still creates the map entry, so owners with no ready pod are reported with 0.
		readyByOwner[key] += boolToInt(isPodReady(pod))
	}

	points := make([]types.MetricPoint, 0, len(readyByOwner)*2)

	resolvePlural := newPluralResolver(ctx, cl)

	for owner, ready := range readyByOwner {
		labels := func(metricName string) map[string]string {
			return map[string]string{
				types.LabelName:      metricName,
				types.LabelOwnerKind: strings.ToLower(owner.kind),
				types.LabelOwnerName: strings.ToLower(owner.name),
				types.LabelNamespace: owner.namespace,
			}
		}

		points = append(points, types.MetricPoint{
			Point:  types.Point{Time: now, Value: float64(ready)},
			Labels: labels(metricNameReplicasReady),
		})

		gv, err := schema.ParseGroupVersion(owner.apiVersion)
		if err != nil {
			logger.V(2).Printf("kubernetes: invalid apiVersion %q for %s %s/%s: %v", owner.apiVersion, owner.kind, owner.namespace, owner.name, err)

			continue
		}

		plural, ok := resolvePlural(gv.Group, owner.kind)
		if !ok {
			// We can't map this owner to a resource (not a built-in we handle, and no matching CRD):
			// desired isn't available, only ready is emitted.
			logger.V(2).Printf("kubernetes: can't resolve resource for %s/%s, skipping desired", gv.Group, owner.kind)

			continue
		}

		gvr := schema.GroupVersionResource{Group: gv.Group, Version: gv.Version, Resource: plural}

		desired, err := cl.GetScale(ctx, gvr, owner.namespace, owner.name)
		if err != nil {
			// No scale subresource (or no permission): desired isn't available for this owner.
			logger.V(2).Printf("kubernetes: no scale for %s %s/%s: %v", owner.kind, owner.namespace, owner.name, err)

			continue
		}

		points = append(points, types.MetricPoint{
			Point:  types.Point{Time: now, Value: float64(desired)},
			Labels: labels(metricNameReplicasDesired),
		})
	}

	return points
}

// newPluralResolver returns a function mapping a (group, kind) to its resource name (plural),
// needed to build the scale subresource URL. It first checks the built-in table, then the cluster
// CRDs.
//
// The returned resolver is meant to be used for a single metrics cycle: the CRD list it fetches is
// memoized only for the lifetime of the resolver (at most one GetCRDs call, and none at all when
// every owner has a built-in group, e.g. bare ReplicaSets). A fresh resolver is created on each
// genericReplicas call, so the CRD list is naturally refreshed every cycle (no long-lived cache to
// invalidate).
func newPluralResolver(ctx context.Context, cl kubeClient) func(group, kind string) (string, bool) {
	var crdPlurals map[string]string // "group/Kind" -> plural, nil until the first CRD lookup.

	return func(group, kind string) (string, bool) {
		key := group + "/" + kind

		if plural, ok := builtinScaleResources[key]; ok {
			return plural, true
		}

		if crdPlurals == nil {
			crdPlurals = map[string]string{}

			crds, err := cl.GetCRDs(ctx)
			if err != nil {
				logger.V(2).Printf("kubernetes: can't list CRDs to resolve scale resources: %v", err)
			}

			for _, crd := range crds {
				crdPlurals[crd.Spec.Group+"/"+crd.Spec.Names.Kind] = crd.Spec.Names.Plural
			}
		}

		plural, ok := crdPlurals[key]

		return plural, ok
	}
}

// isPodReady returns whether the pod is in the Ready condition.
func isPodReady(pod corev1.Pod) bool {
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodReady {
			return cond.Status == corev1.ConditionTrue
		}
	}

	return false
}

func boolToInt(b bool) int {
	if b {
		return 1
	}

	return 0
}
