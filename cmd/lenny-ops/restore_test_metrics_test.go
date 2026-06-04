// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/lennylabs/lenny/pkg/ops/backup/restoretest"
)

// spec: §25.11 lines 4098, 4254-4256 — the restore-test-metrics sampler
// publishes the latest run's success flag, duration, and (when the
// sampled-HEAD check ran) artifact success rate, plus the cumulative
// artifact-missing counter. F-17.3.6.
func TestSampleRestoreTestMetrics_PublishesLatest_spec_25_11(t *testing.T) {
	if restoreTestSuccess == nil || restoreTestDuration == nil ||
		restoreTestArtifactRate == nil || restoreTestArtifactMissing == nil {
		t.Fatal("restore-test metrics failed to register")
	}
	ctx := context.Background()
	store := restoretest.NewMemory()
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	// An earlier failed run, then a later successful run with a checked
	// artifact sample; the sampler must report the later one.
	if err := store.Record(ctx, restoretest.Result{
		ID: "r1", CompletedAt: base, StartedAt: base.Add(-3 * time.Second),
		Success: false, ArtifactChecked: true, ArtifactMissing: 5, ArtifactSuccessRate: 0.5,
	}); err != nil {
		t.Fatalf("Record r1: %v", err)
	}
	if err := store.Record(ctx, restoretest.Result{
		ID: "r2", CompletedAt: base.Add(time.Hour), StartedAt: base.Add(time.Hour).Add(-4 * time.Second),
		Success: true, ArtifactChecked: true, ArtifactMissing: 0, ArtifactSuccessRate: 1.0,
	}); err != nil {
		t.Fatalf("Record r2: %v", err)
	}

	lastRestoreTestArtifactMissing = 0
	if err := sampleRestoreTestMetrics(ctx, store); err != nil {
		t.Fatalf("sampleRestoreTestMetrics: %v", err)
	}
	if got := testutil.ToFloat64(restoreTestSuccess.WithLabelValues()); got != 1 {
		t.Errorf("success gauge = %v, want 1 (latest run passed)", got)
	}
	if got := testutil.ToFloat64(restoreTestDuration.WithLabelValues()); got != 4 {
		t.Errorf("duration gauge = %v, want 4", got)
	}
	if got := testutil.ToFloat64(restoreTestArtifactRate.WithLabelValues()); got != 1.0 {
		t.Errorf("artifact rate gauge = %v, want 1.0", got)
	}
	// The cumulative artifact-missing counter sums both runs (5 + 0).
	if got := testutil.ToFloat64(restoreTestArtifactMissing.WithLabelValues()); got != 5 {
		t.Errorf("artifact missing counter = %v, want 5", got)
	}
}

// A nil store (Kubernetes-less local mode without Postgres) is a no-op.
func TestSampleRestoreTestMetrics_NilStore(t *testing.T) {
	if err := sampleRestoreTestMetrics(context.Background(), nil); err != nil {
		t.Fatalf("nil store must be a no-op, got %v", err)
	}
}

// An empty store leaves the success gauge unset rather than publishing a
// zero that reads as a failed restore before the first run.
func TestSampleRestoreTestMetrics_EmptyStore(t *testing.T) {
	before := testutil.ToFloat64(restoreTestSuccess.WithLabelValues())
	if err := sampleRestoreTestMetrics(context.Background(), restoretest.NewMemory()); err != nil {
		t.Fatalf("sampleRestoreTestMetrics: %v", err)
	}
	if after := testutil.ToFloat64(restoreTestSuccess.WithLabelValues()); after != before {
		t.Errorf("success gauge moved from %v to %v with no recorded run", before, after)
	}
}
