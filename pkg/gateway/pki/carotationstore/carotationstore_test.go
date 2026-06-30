// SPDX-License-Identifier: MIT

package carotationstore

import (
	"context"
	"errors"
	"testing"
	"time"
)

// spec: §10.3 lines 344-350 — the Memory store round-trips the rotation
// record and enforces the optimistic version guard so concurrent stage
// transitions cannot clobber one another.
func TestMemory_initGetPutRoundTrip(t *testing.T) {
	ctx := context.Background()
	clk := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	m := NewMemory().WithClock(func() time.Time { return clk })

	if _, ok, err := m.Get(ctx); err != nil || ok {
		t.Fatalf("Get before init: ok=%v err=%v, want ok=false", ok, err)
	}

	// Initialization passes expectVersion 0 and bumps to version 1.
	stored, err := m.Put(ctx, Record{Stage: "idle", CurrentCAID: "ca-old", OverlapWindowSecs: 3600}, 0)
	if err != nil {
		t.Fatalf("init Put: %v", err)
	}
	if stored.Version != 1 {
		t.Fatalf("version after init = %d, want 1", stored.Version)
	}
	if !stored.UpdatedAt.Equal(clk) {
		t.Errorf("UpdatedAt = %v, want %v", stored.UpdatedAt, clk)
	}

	got, ok, err := m.Get(ctx)
	if err != nil || !ok {
		t.Fatalf("Get after init: ok=%v err=%v", ok, err)
	}
	if got.CurrentCAID != "ca-old" || got.Stage != "idle" {
		t.Errorf("round-trip mismatch: %+v", got)
	}

	// A second init (expectVersion 0) against an existing row conflicts.
	if _, err := m.Put(ctx, Record{Stage: "idle", CurrentCAID: "x"}, 0); !errors.Is(err, ErrConflict) {
		t.Fatalf("re-init err = %v, want ErrConflict", err)
	}

	// Stale-version update conflicts.
	if _, err := m.Put(ctx, Record{Stage: "new_ca_deployed", CurrentCAID: "ca-old", NewCAID: "ca-new"}, 0); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale update err = %v, want ErrConflict", err)
	}

	// Current-version update succeeds and bumps the version.
	next, err := m.Put(ctx, Record{
		Stage:            "new_ca_deployed",
		CurrentCAID:      "ca-old",
		NewCAID:          "ca-new",
		OverlapStartedAt: clk,
	}, 1)
	if err != nil {
		t.Fatalf("update Put: %v", err)
	}
	if next.Version != 2 {
		t.Errorf("version after update = %d, want 2", next.Version)
	}
}
