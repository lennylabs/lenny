// SPDX-License-Identifier: MIT

package ratewindow

import (
	"context"
	"testing"
	"time"
)

// TestCountsRollAtTheMinute checks that a key counts within its minute,
// that keys count apart, and that every count starts again when the minute
// advances.
// spec: §11.1 (Admission and Fairness)
func TestCountsRollAtTheMinute(t *testing.T) {
	m := New()
	ctx := context.Background()
	t0 := time.Unix(1_800_000_000, 0).Truncate(time.Minute)
	for i, want := range []int{1, 2, 3} {
		if got, err := m.Incr(ctx, "a", t0.Add(time.Duration(i)*time.Second)); err != nil || got != want {
			t.Fatalf("Incr a #%d = %d, %v; want %d", i, got, err, want)
		}
	}
	if got, _ := m.IncrementRate(ctx, "b", t0.Add(30*time.Second)); got != 1 {
		t.Errorf("IncrementRate b = %d, want 1 (keys count apart)", got)
	}
	if got, _ := m.Incr(ctx, "a", t0.Add(time.Minute)); got != 1 {
		t.Errorf("Incr a in the next minute = %d, want 1", got)
	}
	if got, _ := m.IncrementRate(ctx, "b", t0.Add(time.Minute+time.Second)); got != 1 {
		t.Errorf("IncrementRate b in the next minute = %d, want 1", got)
	}
}
