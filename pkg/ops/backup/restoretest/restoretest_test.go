// SPDX-License-Identifier: MIT

package restoretest

import (
	"context"
	"testing"
	"time"
)

// spec: §25.11 — the Memory store returns the most recently completed
// result and sums sampled-missing across runs for the counter source.
func TestMemoryLatestAndTotal_spec_25_11(t *testing.T) {
	ctx := context.Background()
	m := NewMemory()

	if _, ok, err := m.Latest(ctx); err != nil || ok {
		t.Fatalf("empty Latest: ok=%v err=%v, want false/nil", ok, err)
	}

	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	if err := m.Record(ctx, Result{ID: "a", CompletedAt: base, Success: false, ArtifactMissing: 3}); err != nil {
		t.Fatalf("Record a: %v", err)
	}
	if err := m.Record(ctx, Result{ID: "b", CompletedAt: base.Add(time.Hour), Success: true, ArtifactMissing: 1}); err != nil {
		t.Fatalf("Record b: %v", err)
	}

	latest, ok, err := m.Latest(ctx)
	if err != nil || !ok {
		t.Fatalf("Latest: ok=%v err=%v", ok, err)
	}
	if latest.ID != "b" || !latest.Success {
		t.Errorf("Latest = %+v, want the later success row b", latest)
	}

	total, err := m.TotalArtifactMissing(ctx)
	if err != nil {
		t.Fatalf("TotalArtifactMissing: %v", err)
	}
	if total != 4 {
		t.Errorf("TotalArtifactMissing = %d, want 4", total)
	}
}

// DurationSeconds clamps a negative span (clock skew) to zero.
func TestResultDurationSeconds(t *testing.T) {
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	r := Result{StartedAt: base, CompletedAt: base.Add(2500 * time.Millisecond)}
	if got := r.DurationSeconds(); got != 2.5 {
		t.Errorf("DurationSeconds = %v, want 2.5", got)
	}
	skew := Result{StartedAt: base, CompletedAt: base.Add(-time.Second)}
	if got := skew.DurationSeconds(); got != 0 {
		t.Errorf("skew DurationSeconds = %v, want 0", got)
	}
}
