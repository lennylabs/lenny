// SPDX-License-Identifier: MIT

package runtimeupgradestore_test

import (
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/runtimeupgradestore"
)

func fixedClock(t time.Time) func() time.Time { return func() time.Time { return t } }

// spec: §10.5 lines 466-540 — the first write registers the row at
// version 1; a re-read returns the stored phase and knobs verbatim.
func TestMemory_putGetRoundTrip_spec_10_5(t *testing.T) {
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	s := runtimeupgradestore.NewMemory().WithClock(fixedClock(now))
	rec := runtimeupgradestore.Record{
		Pool:                       "claude-worker",
		Phase:                      "pending",
		NewImage:                   "registry/img@sha256:abc",
		PreviousPoolSpec:           []byte(`{"minWarm":3}`),
		SchemaVersion:              "v2",
		DrainFirst:                 true,
		CanaryPercent:              10,
		StabilizationWindowSeconds: 120,
		AutoAdvance:                true,
		PhaseEnteredAt:             now,
	}
	stored, err := s.Put(t.Context(), rec, 0)
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if stored.Version != 1 {
		t.Fatalf("first version = %d, want 1", stored.Version)
	}
	if !stored.CreatedAt.Equal(now) || !stored.UpdatedAt.Equal(now) {
		t.Fatalf("timestamps not stamped: created=%v updated=%v", stored.CreatedAt, stored.UpdatedAt)
	}
	got, ok, err := s.Get(t.Context(), "claude-worker")
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if got.Phase != "pending" || got.NewImage != rec.NewImage || got.SchemaVersion != "v2" ||
		!got.DrainFirst || got.CanaryPercent != 10 || !got.AutoAdvance {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	if string(got.PreviousPoolSpec) != `{"minWarm":3}` {
		t.Fatalf("previous pool spec = %q", got.PreviousPoolSpec)
	}
}

// Get of an unregistered pool returns ok=false, not an error.
func TestMemory_getMissing(t *testing.T) {
	s := runtimeupgradestore.NewMemory()
	_, ok, err := s.Get(t.Context(), "nope")
	if err != nil || ok {
		t.Fatalf("missing get: ok=%v err=%v", ok, err)
	}
}

// spec: §10.5 line 468 — the version column serializes concurrent phase
// transitions; a stale expectVersion is rejected with ErrConflict.
func TestMemory_versionGuard_spec_10_5(t *testing.T) {
	s := runtimeupgradestore.NewMemory()
	rec := runtimeupgradestore.Record{Pool: "p", Phase: "pending", NewImage: "img"}
	v1, err := s.Put(t.Context(), rec, 0)
	if err != nil {
		t.Fatalf("put v1: %v", err)
	}
	// A second write echoing the stale version 0 conflicts.
	if _, err := s.Put(t.Context(), v1, 0); err != runtimeupgradestore.ErrConflict {
		t.Fatalf("stale write err = %v, want ErrConflict", err)
	}
	// Echoing the current version 1 succeeds and bumps to 2.
	v2, err := s.Put(t.Context(), v1, 1)
	if err != nil {
		t.Fatalf("put v2: %v", err)
	}
	if v2.Version != 2 {
		t.Fatalf("version after second write = %d, want 2", v2.Version)
	}
}

// A first write (no existing row) with a non-zero expectVersion conflicts:
// the caller is racing a registration that has not happened.
func TestMemory_firstWriteNonZeroVersionConflicts(t *testing.T) {
	s := runtimeupgradestore.NewMemory()
	rec := runtimeupgradestore.Record{Pool: "p", Phase: "pending", NewImage: "img"}
	if _, err := s.Put(t.Context(), rec, 5); err != runtimeupgradestore.ErrConflict {
		t.Fatalf("first write w/ version 5: %v, want ErrConflict", err)
	}
}

// List returns every recorded upgrade for the startup metric prime.
func TestMemory_list(t *testing.T) {
	s := runtimeupgradestore.NewMemory()
	for _, p := range []string{"a", "b"} {
		if _, err := s.Put(t.Context(), runtimeupgradestore.Record{Pool: p, Phase: "pending", NewImage: "img"}, 0); err != nil {
			t.Fatalf("put %s: %v", p, err)
		}
	}
	recs, err := s.List(t.Context())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("list len = %d, want 2", len(recs))
	}
}

// Stored records are cloned: mutating the caller's PreviousPoolSpec after
// Put does not corrupt the store.
func TestMemory_cloneIsolation(t *testing.T) {
	s := runtimeupgradestore.NewMemory()
	spec := []byte(`{"minWarm":1}`)
	rec := runtimeupgradestore.Record{Pool: "p", Phase: "pending", NewImage: "img", PreviousPoolSpec: spec}
	if _, err := s.Put(t.Context(), rec, 0); err != nil {
		t.Fatalf("put: %v", err)
	}
	spec[2] = 'X' // mutate the caller's slice
	got, _, _ := s.Get(t.Context(), "p")
	if string(got.PreviousPoolSpec) != `{"minWarm":1}` {
		t.Fatalf("store corrupted by caller mutation: %q", got.PreviousPoolSpec)
	}
}
