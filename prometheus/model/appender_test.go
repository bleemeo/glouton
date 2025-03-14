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

package model

import (
	"strconv"
	"testing"
	"time"

	"github.com/bleemeo/glouton/types"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/storage"
	"golang.org/x/sync/errgroup"
)

func TestBufferAppender(t *testing.T) { //nolint: maintidx
	// Each step will do theses actions. All do* are executed in the order
	// of the struct, if you want another other do two steps.
	// When wantPoints* are NIL (not empty, nil), we don't check result.
	// The appended copied TO is dropped between each actions and we Commit() always after copy.
	// The zero-value action means do nothing.
	type action struct {
		appendPoints     []types.MetricPoint
		doCommit         bool
		doRollback       bool
		doReset          bool
		doCopy           bool
		doCopyReset      bool
		doFixTime        bool
		fixTime          time.Time
		wantPoints       []types.MetricPoint
		wantPointsInCopy []types.MetricPoint
	}

	t0 := time.Date(2024, 3, 21, 9, 16, 42, 0, time.UTC)
	t1 := t0.Add(10 * time.Minute)
	t2 := t0.Add(15 * time.Minute)

	tests := []struct {
		name    string
		actions []action
	}{
		{
			name: "simple",
			actions: []action{
				{
					appendPoints: []types.MetricPoint{
						{
							Point: types.Point{Time: t0, Value: 1.2},
							Labels: map[string]string{
								types.LabelName: "metric_name",
								types.LabelItem: "item",
							},
						},
					},
					doCommit: true,
					wantPoints: []types.MetricPoint{
						{
							Point: types.Point{Time: t0, Value: 1.2},
							Labels: map[string]string{
								types.LabelName: "metric_name",
								types.LabelItem: "item",
							},
						},
					},
				},
			},
		},
		{
			name: "multiple-append",
			actions: []action{
				{
					appendPoints: []types.MetricPoint{
						{
							Point: types.Point{Time: t0, Value: 1.2},
							Labels: map[string]string{
								types.LabelName: "metric_name",
								"other_label":   "value1",
							},
						},
						{
							Point: types.Point{Time: t0, Value: 5},
							Labels: map[string]string{
								types.LabelName: "metric_name",
								"other_label":   "value2",
							},
						},
						{
							Point: types.Point{Time: t2, Value: 6},
							Labels: map[string]string{
								types.LabelName: "metric_name2",
							},
						},
					},
					doCommit: true,
					wantPoints: []types.MetricPoint{
						{
							Point: types.Point{Time: t0, Value: 1.2},
							Labels: map[string]string{
								types.LabelName: "metric_name",
								"other_label":   "value1",
							},
						},
						{
							Point: types.Point{Time: t0, Value: 5},
							Labels: map[string]string{
								types.LabelName: "metric_name",
								"other_label":   "value2",
							},
						},
						{
							Point: types.Point{Time: t2, Value: 6},
							Labels: map[string]string{
								types.LabelName: "metric_name2",
							},
						},
					},
				},
			},
		},
		{
			name: "append-append-rollback",
			actions: []action{
				{
					appendPoints: []types.MetricPoint{
						{
							Point: types.Point{Time: t0, Value: 1.2},
							Labels: map[string]string{
								types.LabelName: "metric_name",
								"other_label":   "value1",
							},
						},
					},
					doCommit: true,
					wantPoints: []types.MetricPoint{
						{
							Point: types.Point{Time: t0, Value: 1.2},
							Labels: map[string]string{
								types.LabelName: "metric_name",
								"other_label":   "value1",
							},
						},
					},
				},
				{
					appendPoints: []types.MetricPoint{
						{
							Point: types.Point{Time: t0, Value: 5},
							Labels: map[string]string{
								types.LabelName: "metric_name",
								"other_label":   "value2",
							},
						},
					},
					doCommit: true,
					wantPoints: []types.MetricPoint{
						{
							Point: types.Point{Time: t0, Value: 1.2},
							Labels: map[string]string{
								types.LabelName: "metric_name",
								"other_label":   "value1",
							},
						},
						{
							Point: types.Point{Time: t0, Value: 5},
							Labels: map[string]string{
								types.LabelName: "metric_name",
								"other_label":   "value2",
							},
						},
					},
				},
				{
					appendPoints: []types.MetricPoint{
						{
							Point: types.Point{Time: t2, Value: 6},
							Labels: map[string]string{
								types.LabelName: "metric_name2",
							},
						},
					},
					doRollback: true,
					wantPoints: []types.MetricPoint{
						{
							Point: types.Point{Time: t0, Value: 1.2},
							Labels: map[string]string{
								types.LabelName: "metric_name",
								"other_label":   "value1",
							},
						},
						{
							Point: types.Point{Time: t0, Value: 5},
							Labels: map[string]string{
								types.LabelName: "metric_name",
								"other_label":   "value2",
							},
						},
					},
				},
			},
		},
		{
			name: "append-read-commit",
			actions: []action{
				{
					appendPoints: []types.MetricPoint{
						{
							Point: types.Point{Time: t0, Value: 1.2},
							Labels: map[string]string{
								types.LabelName: "metric_name",
								"other_label":   "value1",
							},
						},
						{
							Point: types.Point{Time: t0, Value: 5},
							Labels: map[string]string{
								types.LabelName: "metric_name",
								"other_label":   "value2",
							},
						},
						{
							Point: types.Point{Time: t2, Value: 6},
							Labels: map[string]string{
								types.LabelName: "metric_name2",
							},
						},
					},
					wantPoints: []types.MetricPoint{},
				},
				{
					doCommit: true,
					wantPoints: []types.MetricPoint{
						{
							Point: types.Point{Time: t0, Value: 1.2},
							Labels: map[string]string{
								types.LabelName: "metric_name",
								"other_label":   "value1",
							},
						},
						{
							Point: types.Point{Time: t0, Value: 5},
							Labels: map[string]string{
								types.LabelName: "metric_name",
								"other_label":   "value2",
							},
						},
						{
							Point: types.Point{Time: t2, Value: 6},
							Labels: map[string]string{
								types.LabelName: "metric_name2",
							},
						},
					},
				},
			},
		},
		{
			name: "append-copy-commit-copy",
			actions: []action{
				{
					appendPoints: []types.MetricPoint{
						{
							Point: types.Point{Time: t0, Value: 1.2},
							Labels: map[string]string{
								types.LabelName: "metric_name",
								"other_label":   "value1",
							},
						},
						{
							Point: types.Point{Time: t0, Value: 5},
							Labels: map[string]string{
								types.LabelName: "metric_name",
								"other_label":   "value2",
							},
						},
						{
							Point: types.Point{Time: t2, Value: 6},
							Labels: map[string]string{
								types.LabelName: "metric_name2",
							},
						},
					},
					doCopy:           true,
					wantPoints:       []types.MetricPoint{},
					wantPointsInCopy: []types.MetricPoint{},
				},
				{
					doCommit: true,
					doCopy:   true,
					wantPoints: []types.MetricPoint{
						{
							Point: types.Point{Time: t0, Value: 1.2},
							Labels: map[string]string{
								types.LabelName: "metric_name",
								"other_label":   "value1",
							},
						},
						{
							Point: types.Point{Time: t0, Value: 5},
							Labels: map[string]string{
								types.LabelName: "metric_name",
								"other_label":   "value2",
							},
						},
						{
							Point: types.Point{Time: t2, Value: 6},
							Labels: map[string]string{
								types.LabelName: "metric_name2",
							},
						},
					},
					wantPointsInCopy: []types.MetricPoint{
						{
							Point: types.Point{Time: t0, Value: 1.2},
							Labels: map[string]string{
								types.LabelName: "metric_name",
								"other_label":   "value1",
							},
						},
						{
							Point: types.Point{Time: t0, Value: 5},
							Labels: map[string]string{
								types.LabelName: "metric_name",
								"other_label":   "value2",
							},
						},
						{
							Point: types.Point{Time: t2, Value: 6},
							Labels: map[string]string{
								types.LabelName: "metric_name2",
							},
						},
					},
				},
			},
		},
		{
			name: "append-copy-fixtime",
			actions: []action{
				{
					appendPoints: []types.MetricPoint{
						{
							Point: types.Point{Time: t0, Value: 1.2},
							Labels: map[string]string{
								types.LabelName: "metric_name",
								"other_label":   "value1",
							},
						},
						{
							Point: types.Point{Time: t0, Value: 5},
							Labels: map[string]string{
								types.LabelName: "metric_name",
								"other_label":   "value2",
							},
						},
						{
							Point: types.Point{Time: t2, Value: 6},
							Labels: map[string]string{
								types.LabelName: "metric_name2",
							},
						},
					},
					doCommit:  true,
					doCopy:    true,
					doFixTime: true,
					fixTime:   t1,
					wantPoints: []types.MetricPoint{
						{
							Point: types.Point{Time: t1, Value: 1.2},
							Labels: map[string]string{
								types.LabelName: "metric_name",
								"other_label":   "value1",
							},
						},
						{
							Point: types.Point{Time: t1, Value: 5},
							Labels: map[string]string{
								types.LabelName: "metric_name",
								"other_label":   "value2",
							},
						},
						{
							Point: types.Point{Time: t1, Value: 6},
							Labels: map[string]string{
								types.LabelName: "metric_name2",
							},
						},
					},
					wantPointsInCopy: []types.MetricPoint{
						{
							Point: types.Point{Time: t0, Value: 1.2},
							Labels: map[string]string{
								types.LabelName: "metric_name",
								"other_label":   "value1",
							},
						},
						{
							Point: types.Point{Time: t0, Value: 5},
							Labels: map[string]string{
								types.LabelName: "metric_name",
								"other_label":   "value2",
							},
						},
						{
							Point: types.Point{Time: t2, Value: 6},
							Labels: map[string]string{
								types.LabelName: "metric_name2",
							},
						},
					},
				},
			},
		},
		{
			name: "append-copyreset",
			actions: []action{
				{
					appendPoints: []types.MetricPoint{
						{
							Point: types.Point{Time: t0, Value: 1.2},
							Labels: map[string]string{
								types.LabelName: "metric_name",
								"other_label":   "value1",
							},
						},
						{
							Point: types.Point{Time: t0, Value: 5},
							Labels: map[string]string{
								types.LabelName: "metric_name",
								"other_label":   "value2",
							},
						},
						{
							Point: types.Point{Time: t2, Value: 6},
							Labels: map[string]string{
								types.LabelName: "metric_name2",
							},
						},
					},
					doCommit:    true,
					doCopyReset: true,
					wantPoints:  []types.MetricPoint{},
					wantPointsInCopy: []types.MetricPoint{
						{
							Point: types.Point{Time: t0, Value: 1.2},
							Labels: map[string]string{
								types.LabelName: "metric_name",
								"other_label":   "value1",
							},
						},
						{
							Point: types.Point{Time: t0, Value: 5},
							Labels: map[string]string{
								types.LabelName: "metric_name",
								"other_label":   "value2",
							},
						},
						{
							Point: types.Point{Time: t2, Value: 6},
							Labels: map[string]string{
								types.LabelName: "metric_name2",
							},
						},
					},
				},
			},
		},
		{
			name: "append-reset",
			actions: []action{
				{
					appendPoints: []types.MetricPoint{
						{
							Point: types.Point{Time: t0, Value: 1.2},
							Labels: map[string]string{
								types.LabelName: "metric_name",
								"other_label":   "value1",
							},
						},
						{
							Point: types.Point{Time: t0, Value: 5},
							Labels: map[string]string{
								types.LabelName: "metric_name",
								"other_label":   "value2",
							},
						},
						{
							Point: types.Point{Time: t2, Value: 6},
							Labels: map[string]string{
								types.LabelName: "metric_name2",
							},
						},
					},
					doCommit:   true,
					doReset:    true,
					wantPoints: []types.MetricPoint{},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			appender := NewBufferAppender()
			refByLabelsHash := make(map[uint64]storage.SeriesRef)

			for actIdx, act := range tt.actions {
				for _, pts := range act.appendPoints {
					lbls := labels.FromMap(pts.Labels)
					hash := lbls.Hash()
					ref := refByLabelsHash[hash]

					ref, err := appender.Append(ref, lbls, pts.Time.UnixMilli(), pts.Value)
					if err != nil {
						t.Fatalf("actIdx#%d: Append failed: %v", actIdx, err)
					}

					refByLabelsHash[hash] = ref
				}

				if act.doCommit {
					if err := appender.Commit(); err != nil {
						t.Fatalf("actIdx#%d: Commit failed: %v", actIdx, err)
					}
				}

				if act.doRollback {
					if err := appender.Rollback(); err != nil {
						t.Fatalf("actIdx#%d: Rollback failed: %v", actIdx, err)
					}
				}

				if act.doReset {
					appender.Reset()
				}

				if act.doCopy || act.doCopyReset {
					copyToAppender := NewBufferAppender()

					if act.doCopy {
						if err := appender.CopyTo(copyToAppender); err != nil {
							t.Fatalf("actIdx#%d: CopyTo failed: %v", actIdx, err)
						}

						if err := copyToAppender.Commit(); err != nil {
							t.Fatalf("actIdx#%d: copyToAppender.Commit failed: %v", actIdx, err)
						}
					}

					if act.doCopyReset {
						if err := appender.CommitCopyAndReset(copyToAppender); err != nil {
							t.Fatalf("CopyToAndReset failed: %v", err)
						}

						if err := copyToAppender.Commit(); err != nil {
							t.Fatalf("actIdx#%d: copyToAppender.Commit failed: %v", actIdx, err)
						}
					}

					if act.wantPointsInCopy != nil {
						mfs, err := copyToAppender.AsMF()
						if err != nil {
							t.Errorf("actIdx#%d: copyToAppender.AsMF() failed: %s", actIdx, err)
						}

						got := FamiliesToMetricPoints(time.Time{}, mfs, false)
						if diff := types.DiffMetricPoints(act.wantPointsInCopy, got, false); diff != "" {
							t.Errorf("actIdx#%d: copyToAppender.AsMF() mismatch (-want +got)\n%s", actIdx, diff)
						}
					}
				}

				if act.doFixTime {
					appender.FixSampleTimestamp(act.fixTime)
				}

				if act.wantPoints != nil {
					mfs, err := appender.AsMF()
					if err != nil {
						t.Errorf("actIdx#%d: appender.AsMF() failed: %s", actIdx, err)
					}

					got := FamiliesToMetricPoints(time.Time{}, mfs, false)
					if diff := types.DiffMetricPoints(act.wantPoints, got, false); diff != "" {
						t.Errorf("actIdx#%d: appender.AsMF() mismatch (-want +got)\n%s", actIdx, diff)
					}
				}
			}
		})
	}
}

func TestBufferAppender_Race(t *testing.T) {
	appender := NewBufferAppender()
	target := NewBufferAppender()

	ptsChannel := make(chan []types.MetricPoint)
	grp, ctx := errgroup.WithContext(t.Context())

	for range 5 {
		grp.Go(func() error {
			refByLabelsHash := make(map[uint64]storage.SeriesRef)

			for batch := range ptsChannel {
				for _, pts := range batch {
					lbls := labels.FromMap(pts.Labels)
					hash := lbls.Hash()
					ref := refByLabelsHash[hash]

					ref, err := appender.Append(ref, lbls, pts.Time.UnixMilli(), pts.Value)
					if err != nil {
						return err
					}

					refByLabelsHash[hash] = ref
				}

				if len(batch) == 0 {
					// batch of len 0 is a marker to trigger a CopyToAndReset
					if err := appender.CommitCopyAndReset(target); err != nil {
						return err
					}
				}

				if err := appender.Commit(); err != nil {
					return err
				}
			}

			return nil
		})
	}

	wantPoints := make([]types.MetricPoint, 0)
	t0 := time.Now().Truncate(time.Millisecond)

	for batchCount := range 100 {
		batchSize := batchCount % 4

		batch := make([]types.MetricPoint, batchSize)
		for idx := range batch {
			var (
				lbls labels.Labels
				pts  types.Point
			)

			if idx == 0 {
				// first metrics is always the same labels but will use
				// different timestamp
				lbls = labels.FromMap(map[string]string{
					types.LabelName: "same_metric",
					"timestamp":     "different",
				})
				pts = types.Point{
					Time:  t0.Add(time.Duration(batchCount) * time.Second),
					Value: float64(batchCount),
				}
			} else {
				lbls = labels.FromMap(map[string]string{
					types.LabelName: "unique_metric",
					"batchCount":    strconv.Itoa(batchCount),
					"idx":           strconv.Itoa(idx),
				})
				pts = types.Point{
					Time:  time.Now().Truncate(time.Millisecond),
					Value: float64(batchCount*1000 + idx),
				}
			}

			batch[idx] = types.MetricPoint{
				Labels: lbls.Map(),
				Point:  pts,
			}
		}

		wantPoints = append(wantPoints, batch...)

		select {
		case ptsChannel <- batch:
		case <-ctx.Done():
		}

		if ctx.Err() != nil {
			break
		}
	}

	close(ptsChannel)

	if err := grp.Wait(); err != nil {
		t.Errorf("grp.Wait: %v", err)
	}

	if err := appender.CommitCopyAndReset(target); err != nil {
		t.Errorf("CopyToAndReset: %v", err)
	}

	if err := target.Commit(); err != nil {
		t.Errorf("Commit: %v", err)
	}

	mfs, err := target.AsMF()
	if err != nil {
		t.Errorf("AsMF() failed: %s", err)
	}

	got := FamiliesToMetricPoints(time.Time{}, mfs, false)
	if diff := types.DiffMetricPoints(wantPoints, got, false); diff != "" {
		t.Errorf("AsMF() mismatch (-want +got)\n%s", diff)
	}
}
