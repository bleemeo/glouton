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
	"testing"
	"time"

	"github.com/bleemeo/glouton/types"

	"github.com/google/go-cmp/cmp"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
)

// pointKey identifies a metric point by its name and owner, ignoring the timestamp so tests can
// assert on the value alone.
type pointKey struct {
	name      string
	ownerKind string
	ownerName string
	namespace string
}

// pointsToMap indexes metric points by (name, owner_kind, owner_name, namespace) -> value. It fails
// the test if two points share the same key, which would mean we emitted a duplicate series.
func pointsToMap(t *testing.T, points []types.MetricPoint) map[pointKey]float64 {
	t.Helper()

	result := make(map[pointKey]float64, len(points))

	for _, point := range points {
		key := pointKey{
			name:      point.Labels[types.LabelName],
			ownerKind: point.Labels[types.LabelOwnerKind],
			ownerName: point.Labels[types.LabelOwnerName],
			namespace: point.Labels[types.LabelNamespace],
		}

		if _, ok := result[key]; ok {
			t.Fatalf("duplicate metric point for %+v", key)
		}

		result[key] = point.Point.Value
	}

	return result
}

func metaTime(t time.Time) *metav1.Time {
	mt := metav1.NewTime(t)

	return &mt
}

func TestCronJobMetrics(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	hourly := "0 * * * *"
	daily2AM := "0 2 * * *"

	cronJob := func(name, schedule string, mutate func(*batchv1.CronJob)) batchv1.CronJob {
		cj := batchv1.CronJob{
			ObjectMeta: metav1.ObjectMeta{
				Name:              name,
				Namespace:         "default",
				CreationTimestamp: metav1.NewTime(now.Add(-90 * time.Minute)),
			},
			Spec: batchv1.CronJobSpec{Schedule: schedule},
		}
		if mutate != nil {
			mutate(&cj)
		}

		return cj
	}

	cache := kubeCache{
		cronJobs: []batchv1.CronJob{
			// Just fired: the tick that just became due (12:00) must not count while its run hasn't
			// started (active=0) — this is the healthy-run blip the grace period fixes.
			cronJob("just-fired", hourly, func(cj *batchv1.CronJob) {
				cj.Status.LastSuccessfulTime = metaTime(now.Add(-time.Hour))
			}),
			// Long-running: the 11:00 tick is past the grace, but its job is still running, so the
			// active discount keeps it from counting as missed.
			cronJob("long-running", hourly, func(cj *batchv1.CronJob) {
				cj.Status.LastSuccessfulTime = metaTime(now.Add(-2 * time.Hour))
				cj.Status.Active = []corev1.ObjectReference{{Name: "long-run"}}
			}),
			// Late: last success 3 daily ticks ago, nothing running.
			cronJob("late", daily2AM, func(cj *batchv1.CronJob) {
				cj.Status.LastSuccessfulTime = metaTime(time.Date(2025, 12, 29, 2, 0, 0, 0, time.UTC))
			}),
			// Suspended: would be very late, but suspend forces missed_runs to 0.
			cronJob("suspended", daily2AM, func(cj *batchv1.CronJob) {
				suspend := true
				cj.Spec.Suspend = &suspend
				cj.Status.LastSuccessfulTime = metaTime(time.Date(2025, 12, 1, 2, 0, 0, 0, time.UTC))
			}),
			// Never succeeded: base is the creation time, hourly schedule, nothing running.
			cronJob("never-succeeded", hourly, func(cj *batchv1.CronJob) {
				cj.CreationTimestamp = metav1.NewTime(now.Add(-210 * time.Minute)) // 08:30
			}),
			// Stacking (concurrencyPolicy=Allow): several active jobs, but only one tick is excused.
			cronJob("stacking", hourly, func(cj *batchv1.CronJob) {
				cj.Status.LastSuccessfulTime = metaTime(now.Add(-5 * time.Hour)) // 07:00
				cj.Status.Active = []corev1.ObjectReference{{Name: "a"}, {Name: "b"}, {Name: "c"}}
			}),
			// Invalid schedule: only the age metric is emitted.
			cronJob("broken", "not a cron", func(cj *batchv1.CronJob) {
				cj.Status.LastSuccessfulTime = metaTime(now.Add(-time.Hour))
			}),
		},
	}

	got := pointsToMap(t, cronJobMetrics(cache, now))

	const (
		missedName = "kubernetes_cronjob_missed_runs"
		ageName    = "kubernetes_cronjob_last_success_age_seconds"
	)

	missed := func(name string) pointKey {
		return pointKey{name: missedName, ownerKind: "cronjob", ownerName: name, namespace: "default"}
	}
	age := func(name string) pointKey {
		return pointKey{name: ageName, ownerKind: "cronjob", ownerName: name, namespace: "default"}
	}

	want := map[pointKey]float64{
		missed("just-fired"):   0, // 12:00 tick within grace -> not counted
		age("just-fired"):      3600,
		missed("long-running"): 0, // 11:00 tick counted, but discounted by the active job
		age("long-running"):    7200,
		missed("late"):         3,
		age("late"):            (3*24 + 10) * 3600, // Dec 29 02:00 -> Jan 1 12:00
		missed("suspended"):    0,
		age("suspended"):       now.Sub(time.Date(2025, 12, 1, 2, 0, 0, 0, time.UTC)).Seconds(),
		// never-succeeded: missed_runs uses creation as base, but no last_success_age is emitted
		// (there was never a success to measure the age from).
		missed("never-succeeded"): 3, // creation 08:30 -> ticks 09:00, 10:00, 11:00 (12:00 within grace)
		missed("stacking"):        3, // ticks 08:00..11:00 = 4, minus min(3,1)
		age("stacking"):           5 * 3600,
		// "broken" emits age only (schedule unparsable).
		age("broken"): 3600,
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("cronJobMetrics mismatch (-want +got):\n%s", diff)
	}
}

func TestJobMetrics(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	job := func(name, uid, cronName string, created time.Time, mutate func(*batchv1.Job)) batchv1.Job {
		j := batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{
				Name:              name,
				Namespace:         "default",
				UID:               k8stypes.UID(uid),
				CreationTimestamp: metav1.NewTime(created),
			},
		}

		if cronName != "" {
			j.OwnerReferences = []metav1.OwnerReference{{Kind: "CronJob", Name: cronName}}
		}

		if mutate != nil {
			mutate(&j)
		}

		return j
	}

	condition := func(condType batchv1.JobConditionType) []batchv1.JobCondition {
		return []batchv1.JobCondition{{Type: condType, Status: corev1.ConditionTrue}}
	}

	// jobPod builds a pod owned by the given job UID, either terminated (with an execution window)
	// or still running.
	jobPod := func(jobUID string, startedAt, finishedAt time.Time, terminated bool) corev1.Pod {
		var state corev1.ContainerState
		if terminated {
			state.Terminated = &corev1.ContainerStateTerminated{
				StartedAt:  metav1.NewTime(startedAt),
				FinishedAt: metav1.NewTime(finishedAt),
			}
		} else {
			state.Running = &corev1.ContainerStateRunning{StartedAt: metav1.NewTime(startedAt)}
		}

		return corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:       "default",
				OwnerReferences: []metav1.OwnerReference{{Kind: "Job", UID: k8stypes.UID(jobUID)}},
			},
			Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{State: state}}},
		}
	}

	cache := kubeCache{
		jobs: []batchv1.Job{
			// Standalone job stuck retrying (busybox case): 4 failed attempts, no condition yet.
			job("job-fail", "u-jobfail", "", now.Add(-8*time.Minute), func(j *batchv1.Job) {
				j.Status.Active = 1
				j.Status.Failed = 4
			}),
			// Standalone job that succeeded after 2 retries: healthy despite status.Failed=2.
			job("backup-once", "u-backup", "", now.Add(-25*time.Minute), func(j *batchv1.Job) {
				j.Status.Failed = 2
				j.Status.CompletionTime = metaTime(now.Add(-10 * time.Minute))
				j.Status.Conditions = condition(batchv1.JobComplete)
			}),
			// Standalone job that definitively failed (backoff exhausted): 7 failed attempts.
			job("migrate", "u-migrate", "", now.Add(-30*time.Minute), func(j *batchv1.Job) {
				j.Status.Failed = 7
				j.Status.Conditions = condition(batchv1.JobFailed)
			}),
			// CronJob "report": previous run failed (5), latest run just started with no failure yet.
			// The fresh run must NOT reset failed_pods to 0 (no ok->error flap).
			job("report-1", "u-report1", "report", time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC), func(j *batchv1.Job) {
				j.Status.Failed = 5
				j.Status.Conditions = condition(batchv1.JobFailed)
			}),
			job("report-2", "u-report2", "report", time.Date(2026, 1, 1, 11, 0, 0, 0, time.UTC), func(j *batchv1.Job) {
				j.Status.Active = 1
			}),
			// Standalone job on its very first run, no failure yet: emits nothing.
			job("fresh", "u-fresh", "", now.Add(-2*time.Minute), func(j *batchv1.Job) {
				j.Status.Active = 1
			}),
		},
		pods: []corev1.Pod{
			// job-fail: two failed attempts; the most recent (60s) wins.
			jobPod("u-jobfail", now.Add(-12*time.Minute), now.Add(-11*time.Minute), true),
			jobPod("u-jobfail", now.Add(-9*time.Minute), now.Add(-8*time.Minute), true),
			// backup-once ran 10 minutes.
			jobPod("u-backup", now.Add(-20*time.Minute), now.Add(-10*time.Minute), true),
			// migrate's last attempt ran 2 minutes.
			jobPod("u-migrate", now.Add(-7*time.Minute), now.Add(-5*time.Minute), true),
			// report-1 ran 5 minutes; report-2 is still running -> duration stays report-1's.
			jobPod("u-report1", time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC), time.Date(2026, 1, 1, 10, 5, 0, 0, time.UTC), true),
			jobPod("u-report2", time.Date(2026, 1, 1, 11, 0, 0, 0, time.UTC), time.Time{}, false),
			// fresh's pod is still running -> no duration.
			jobPod("u-fresh", now.Add(-2*time.Minute), time.Time{}, false),
		},
	}

	got := pointsToMap(t, jobMetrics(cache, now))

	const (
		failedName   = "kubernetes_job_failed_pods"
		durationName = "kubernetes_last_job_duration_seconds"
	)

	failed := func(kind, name string) pointKey {
		return pointKey{name: failedName, ownerKind: kind, ownerName: name, namespace: "default"}
	}
	duration := func(kind, name string) pointKey {
		return pointKey{name: durationName, ownerKind: kind, ownerName: name, namespace: "default"}
	}

	want := map[pointKey]float64{
		failed("job", "job-fail"):    4, // retrying, surfaced immediately
		failed("job", "backup-once"): 0, // succeeded after retries -> healthy
		failed("job", "migrate"):     7, // definitively failed
		failed("cronjob", "report"):  5, // from the failed previous run, not reset by the fresh one
		// "fresh" produced no signal yet -> no failed_pods point.

		duration("job", "job-fail"):    60,  // last terminated pod, not the 8min job span
		duration("job", "backup-once"): 600, // 10 minutes
		duration("job", "migrate"):     120, // last attempt, 2 minutes
		duration("cronjob", "report"):  300, // report-1 (report-2 still running)
		// "fresh" has no terminated pod -> no duration.
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("jobMetrics mismatch (-want +got):\n%s", diff)
	}
}
