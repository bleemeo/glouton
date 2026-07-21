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
	cron "github.com/robfig/cron/v3"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	defaultNamespace            = "default"
	metricNameReplicasDesired   = "kubernetes_replicas_desired"
	metricNameReplicasReady     = "kubernetes_replicas_ready"
	metricNameReplicasAvailable = "kubernetes_replicas_available"

	metricNameHPAMinReplicas    = "kubernetes_hpa_min_replicas"
	metricNameHPAMaxReplicas    = "kubernetes_hpa_max_replicas"
	metricNameHPAScalingLimited = "kubernetes_hpa_scaling_limited"
	metricNameHPAStatus         = "kubernetes_hpa_status"

	// hpaReasonScalingDisabled is the HPA controller's reason on the ScalingActive condition when
	// scaling is intentionally disabled (target scaled to 0 replicas). This is not a failure.
	hpaReasonScalingDisabled = "ScalingDisabled"

	// hpaDegradedGracePeriod is how long an HPA condition must stay in a failing state before
	// kubernetes_hpa_status escalates to Warning/Critical. A rollout briefly makes the HPA lose its
	// metrics (new pods not yet Ready / not yet scraped by metrics-server), which recovers on its
	// own; only a failure that outlasts this window is a real problem worth alerting on.
	hpaDegradedGracePeriod = 3 * time.Minute

	// maxMissedRunsCount caps the number of schedule ticks counted for
	// kubernetes_cronjob_missed_runs. A high-frequency CronJob that has been failing
	// for a long time would otherwise trigger a very long loop every cycle; the exact
	// value beyond the cap doesn't matter for alerting (thresholds are small).
	maxMissedRunsCount = 100

	// missedRunGracePeriod is how long a scheduled tick is given before it counts as missed. Without
	// it, the tick that just became due would be counted before the CronJob controller has created
	// its Job (which then shows in status.Active) or before the run completes, producing a transient
	// false missed run on every healthy execution. It must cover the scheduler + controller
	// reconcile latency (~10-20s); once a tick ages past it, either the run has succeeded (advancing
	// lastSuccessfulTime) or the Job is Active (and the active discount applies).
	missedRunGracePeriod = time.Minute
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
	hpas                 []autoscalingv2.HorizontalPodAutoscaler
	jobs                 []batchv1.Job
	cronJobs             []batchv1.CronJob
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

	cache.hpas, err = cl.GetHPAs(ctx)
	multiErr.Append(err)

	cache.jobs, err = cl.GetJobs(ctx)
	multiErr.Append(err)

	cache.cronJobs, err = cl.GetCronJobs(ctx)
	multiErr.Append(err)

	cache.replicasetOwnerByUID = buildReplicasetOwnerByUID(replicasets)

	// Compute cluster metrics.
	var points []types.MetricPoint //nolint:prealloc

	metricFunctions := []metricsFunc{podsCount, requestsAndLimits, namespacesCount, nodesCount, podsRestartCount, workloadReplicas, hpaMetrics, cronJobMetrics, jobMetrics}

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

// hpaMetrics returns metrics for each HorizontalPodAutoscaler in the cluster. To join naturally
// with the kubernetes_replicas_* metrics, HPA metrics are labelled by the HPA's scale target
// (owner_kind/owner_name) rather than by the HPA object itself:
//   - kubernetes_hpa_min_replicas / kubernetes_hpa_max_replicas: the configured bounds, which are
//     not derivable from the workload objects. Comparing kubernetes_replicas_desired to max lets
//     alerting detect an autoscaler pinned at its ceiling.
//   - kubernetes_hpa_scaling_limited: 1 when the HPA wants to scale beyond min/max but is clamped.
//   - kubernetes_hpa_status: a self-declared health status (bypasses user thresholds). Critical
//     when the HPA can't do its job (can't read metrics or can't act on the target), Warning when
//     scaling is intentionally disabled (target at 0 replicas), OK otherwise.
func hpaMetrics(cache kubeCache, now time.Time) []types.MetricPoint {
	// 4 points per HPA.
	points := make([]types.MetricPoint, 0, len(cache.hpas)*4)

	for _, hpa := range cache.hpas {
		// spec.minReplicas defaults to 1 when unset.
		minReplicas := float64(1)
		if hpa.Spec.MinReplicas != nil {
			minReplicas = float64(*hpa.Spec.MinReplicas)
		}

		labels := func(name string) map[string]string {
			return map[string]string{
				types.LabelName:      name,
				types.LabelOwnerKind: strings.ToLower(hpa.Spec.ScaleTargetRef.Kind),
				types.LabelOwnerName: strings.ToLower(hpa.Spec.ScaleTargetRef.Name),
				types.LabelNamespace: namespaceOrDefault(hpa.Namespace),
			}
		}

		points = append(
			points,
			types.MetricPoint{
				Point:  types.Point{Time: now, Value: minReplicas},
				Labels: labels(metricNameHPAMinReplicas),
			},
			types.MetricPoint{
				Point:  types.Point{Time: now, Value: float64(hpa.Spec.MaxReplicas)},
				Labels: labels(metricNameHPAMaxReplicas),
			},
			types.MetricPoint{
				Point:  types.Point{Time: now, Value: hpaScalingLimited(hpa)},
				Labels: labels(metricNameHPAScalingLimited),
			},
		)

		status := hpaHealth(hpa, now)
		points = append(points, types.MetricPoint{
			Point:       types.Point{Time: now, Value: float64(status.CurrentStatus.NagiosCode())},
			Labels:      labels(metricNameHPAStatus),
			Annotations: types.MetricAnnotations{Status: status},
		})
	}

	return points
}

// hpaScalingLimited returns 1 when the HPA's ScalingLimited condition is True (the desired replica
// count was clamped to min or max), 0 otherwise.
func hpaScalingLimited(hpa autoscalingv2.HorizontalPodAutoscaler) float64 {
	for _, cond := range hpa.Status.Conditions {
		if cond.Type == autoscalingv2.ScalingLimited && cond.Status == corev1.ConditionTrue {
			return 1
		}
	}

	return 0
}

// hpaHealth derives a health status from the HPA conditions. AbleToScale reports whether the
// controller can read and update the target's scale (plumbing); ScalingActive reports whether it
// can compute a desired replica count from its metrics. Either being false means the autoscaler
// isn't doing its job (Critical), except the benign ScalingDisabled case (Warning). The worst
// status across conditions wins.
//
// A condition that has only recently started failing is ignored (kept OK) until it has been failing
// for hpaDegradedGracePeriod, so a rollout's transient loss of metrics doesn't produce alert noise.
func hpaHealth(hpa autoscalingv2.HorizontalPodAutoscaler, now time.Time) types.StatusDescription {
	result := types.StatusDescription{
		CurrentStatus:     types.StatusOk,
		StatusDescription: "HPA is able to scale its target",
	}

	worsen := func(candidate types.StatusDescription) {
		if candidate.CurrentStatus > result.CurrentStatus {
			result = candidate
		}
	}

	for _, cond := range hpa.Status.Conditions {
		if cond.Status == corev1.ConditionTrue {
			continue
		}

		// Grace period: a condition that transitioned to its failing state less than
		// hpaDegradedGracePeriod ago is still considered healthy (LastTransitionTime only moves on a
		// real True<->False transition, so this measures how long it has been continuously failing).
		if now.Sub(cond.LastTransitionTime.Time) < hpaDegradedGracePeriod {
			continue
		}

		// The condition Reason (a short code) and Message (a human-readable explanation) are already
		// self-describing, so use them as-is rather than prefixing our own sentence.
		desc := cond.Reason
		if cond.Message != "" {
			desc += ": " + cond.Message
		}

		switch cond.Type { //nolint:exhaustive
		case autoscalingv2.AbleToScale:
			worsen(types.StatusDescription{CurrentStatus: types.StatusCritical, StatusDescription: desc})
		case autoscalingv2.ScalingActive:
			// ScalingDisabled (target scaled to 0) is intentional, not a failure.
			status := types.StatusCritical
			if cond.Reason == hpaReasonScalingDisabled {
				status = types.StatusWarning
			}

			worsen(types.StatusDescription{CurrentStatus: status, StatusDescription: desc})
		}
	}

	return result
}

// cronJobMetrics returns per-CronJob metrics:
//   - kubernetes_cronjob_missed_runs: number of scheduled runs that should have succeeded by now
//     but didn't, computed from spec.schedule. A currently-running job excuses at most one tick
//     (see missedRuns), so a healthy CronJob reports 0 while a stalled/failing one grows.
//   - kubernetes_cronjob_last_success_age_seconds: seconds since the last successful run, for
//     dashboards. Not emitted for a CronJob that never succeeded (missed_runs covers that case).
//
// Labels are stable across state changes (owner_kind=cronjob, owner_name, namespace) so the same
// series transitions cleanly instead of spawning a new series per state.
func cronJobMetrics(cache kubeCache, now time.Time) []types.MetricPoint {
	points := make([]types.MetricPoint, 0, len(cache.cronJobs)*2)

	for _, cronJob := range cache.cronJobs {
		namespace := namespaceOrDefault(cronJob.Namespace)
		labels := func(metricName string) map[string]string {
			return map[string]string{
				types.LabelName:      metricName,
				types.LabelOwnerKind: "cronjob",
				types.LabelOwnerName: strings.ToLower(cronJob.Name),
				types.LabelNamespace: namespace,
			}
		}

		// The last success (or creation, when it never succeeded) is the reference for both metrics.
		base := cronJob.CreationTimestamp.Time
		if cronJob.Status.LastSuccessfulTime != nil {
			base = cronJob.Status.LastSuccessfulTime.Time

			points = append(points, types.MetricPoint{
				Point:  types.Point{Time: now, Value: now.Sub(base).Seconds()},
				Labels: labels("kubernetes_cronjob_last_success_age_seconds"),
			})
		}

		missed, ok := missedRuns(cronJob, base, now)
		if !ok {
			// Unparsable schedule: skip missed_runs (age is still emitted above).
			continue
		}

		points = append(points, types.MetricPoint{
			Point:  types.Point{Time: now, Value: float64(missed)},
			Labels: labels("kubernetes_cronjob_missed_runs"),
		})
	}

	return points
}

// missedRuns returns the number of scheduled runs missed since base (the last success or creation),
// and whether the schedule could be parsed. A suspended CronJob always returns 0. Ticks are only
// counted up to now-missedRunGracePeriod (a just-due tick isn't a miss yet), and at most one active
// job is discounted, so a healthy run doesn't blip while an overlapping pile-up
// (concurrencyPolicy=Allow) still surfaces as missed.
func missedRuns(cronJob batchv1.CronJob, base, now time.Time) (int, bool) {
	if cronJob.Spec.Suspend != nil && *cronJob.Spec.Suspend {
		return 0, true
	}

	spec := cronJob.Spec.Schedule
	if cronJob.Spec.TimeZone != nil && *cronJob.Spec.TimeZone != "" {
		spec = "CRON_TZ=" + *cronJob.Spec.TimeZone + " " + spec
	}

	schedule, err := cron.ParseStandard(spec)
	if err != nil {
		logger.V(2).Printf("kubernetes: invalid schedule %q for cronjob %s/%s: %v", cronJob.Spec.Schedule, cronJob.Namespace, cronJob.Name, err)

		return 0, false
	}

	// Count the schedule ticks in (base, now - grace]. The grace period keeps the tick that just
	// became due from counting before its run had a chance to be scheduled and to complete.
	deadline := now.Add(-missedRunGracePeriod)

	ticks := 0

	for t := schedule.Next(base); !t.After(deadline); t = schedule.Next(t) {
		ticks++
		if ticks >= maxMissedRunsCount {
			break
		}
	}

	// A running job hasn't had its chance to succeed yet, but it only excuses its own (latest) tick,
	// not the whole backlog: cap the discount at 1.
	missed := max(ticks-min(len(cronJob.Status.Active), 1), 0)

	return missed, true
}

// ownerKey identifies the effective owner of a job (the CronJob for cronjob-owned jobs, the Job
// itself when standalone).
type ownerKey struct {
	kind      string
	name      string
	namespace string
}

// jobMetrics returns per-Job metrics, keyed by effective owner:
//   - kubernetes_job_failed_pods: number of failed pod attempts of the owner's latest job that has
//     produced a signal (finished, or already has a failed pod), forced to 0 once that job
//     succeeds. status.failed climbs from the very first failed attempt, so a job stuck retrying is
//     visible immediately with a "> 0" threshold, long before the Job's Failed condition (only set
//     once the backoff limit is exhausted, which can take many minutes). A freshly started run with
//     no failure yet is ignored so it doesn't reset a still-failing owner's signal to 0 (which would
//     flap ok/error on every CronJob run).
//   - kubernetes_last_job_duration_seconds: execution duration of the owner's most recent terminated
//     pod. Using the pod (not the Job's start-to-finish span) excludes retry backoff waits, and
//     using terminated pods only means an in-progress run doesn't reset the value to 0.
func jobMetrics(cache kubeCache, now time.Time) []types.MetricPoint {
	type failedCandidate struct {
		creation   time.Time
		failedPods float64
	}

	type durationCandidate struct {
		end      time.Time
		duration float64
	}

	failedByOwner := make(map[ownerKey]failedCandidate)
	durationByOwner := make(map[ownerKey]durationCandidate)
	// ownerByJobUID resolves a pod's owning Job (via OwnerReferences) to its effective owner.
	ownerByJobUID := make(map[string]ownerKey, len(cache.jobs))

	for _, job := range cache.jobs {
		kind, name := jobOwner(job)
		key := ownerKey{kind: kind, name: name, namespace: namespaceOrDefault(job.Namespace)}
		ownerByJobUID[string(job.UID)] = key

		// Only jobs that produced a signal count: a fresh run with no failure yet must not reset a
		// still-failing owner's failed_pods to 0.
		if !jobFinished(job) && job.Status.Failed == 0 {
			continue
		}

		candidate := failedCandidate{creation: job.CreationTimestamp.Time, failedPods: jobFailedPods(job)}
		if prev, exists := failedByOwner[key]; !exists || candidate.creation.After(prev.creation) {
			failedByOwner[key] = candidate
		}
	}

	for _, pod := range cache.pods {
		key, ok := jobPodOwner(pod, ownerByJobUID)
		if !ok {
			continue
		}

		end, duration, ok := podExecutionDuration(pod)
		if !ok {
			continue
		}

		if prev, exists := durationByOwner[key]; !exists || end.After(prev.end) {
			durationByOwner[key] = durationCandidate{end: end, duration: duration}
		}
	}

	points := make([]types.MetricPoint, 0, len(failedByOwner)+len(durationByOwner))

	emit := func(name string, key ownerKey, value float64) {
		points = append(points, types.MetricPoint{
			Point: types.Point{Time: now, Value: value},
			Labels: map[string]string{
				types.LabelName:      name,
				types.LabelOwnerKind: key.kind,
				types.LabelOwnerName: strings.ToLower(key.name),
				types.LabelNamespace: key.namespace,
			},
		})
	}

	for key, failed := range failedByOwner {
		emit("kubernetes_job_failed_pods", key, failed.failedPods)
	}

	for key, duration := range durationByOwner {
		emit("kubernetes_last_job_duration_seconds", key, duration.duration)
	}

	return points
}

// jobOwner returns the effective owner of a job: the CronJob that spawned it (kind "cronjob") when
// there is one, otherwise the job itself (kind "job", i.e. a standalone job).
func jobOwner(job batchv1.Job) (kind, name string) {
	for _, ref := range job.OwnerReferences {
		if ref.Kind == "CronJob" {
			return "cronjob", ref.Name
		}
	}

	return "job", job.Name
}

// jobPodOwner returns the effective owner of a pod created by a Job, resolved through the job UID
// map, and whether the pod is such a job pod.
func jobPodOwner(pod corev1.Pod, ownerByJobUID map[string]ownerKey) (ownerKey, bool) {
	for _, ref := range pod.OwnerReferences {
		if ref.Kind == "Job" {
			key, ok := ownerByJobUID[string(ref.UID)]

			return key, ok
		}
	}

	return ownerKey{}, false
}

// jobFailedPods returns the number of failed pod attempts of the job, or 0 once the job has
// succeeded (a job that succeeded after a few retries is healthy, not failing).
func jobFailedPods(job batchv1.Job) float64 {
	if jobSucceeded(job) {
		return 0
	}

	return float64(job.Status.Failed)
}

// jobSucceeded returns whether the job reached the Complete condition.
func jobSucceeded(job batchv1.Job) bool {
	for _, cond := range job.Status.Conditions {
		if cond.Type == batchv1.JobComplete && cond.Status == corev1.ConditionTrue {
			return true
		}
	}

	return false
}

// jobFinished returns whether the job reached a terminal state (Complete or Failed condition, or a
// completion time).
func jobFinished(job batchv1.Job) bool {
	if job.Status.CompletionTime != nil {
		return true
	}

	for _, cond := range job.Status.Conditions {
		if (cond.Type == batchv1.JobComplete || cond.Type == batchv1.JobFailed) && cond.Status == corev1.ConditionTrue {
			return true
		}
	}

	return false
}

// podExecutionDuration returns when the pod finished and how long it ran (max container FinishedAt
// minus min container StartedAt), and whether the pod has terminated. A pod with a container that
// hasn't terminated yet has no execution duration.
func podExecutionDuration(pod corev1.Pod) (end time.Time, duration float64, ok bool) {
	var start time.Time

	if len(pod.Status.ContainerStatuses) == 0 {
		return time.Time{}, 0, false
	}

	for _, status := range pod.Status.ContainerStatuses {
		term := status.State.Terminated
		if term == nil {
			// A container is still running (or waiting): the pod hasn't finished.
			return time.Time{}, 0, false
		}

		if start.IsZero() || term.StartedAt.Time.Before(start) {
			start = term.StartedAt.Time
		}

		if term.FinishedAt.Time.After(end) {
			end = term.FinishedAt.Time
		}
	}

	if start.IsZero() || end.Before(start) {
		return time.Time{}, 0, false
	}

	return end, end.Sub(start).Seconds(), true
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
